"""M0 PDFium raster chain and minimal append-only PDF construction.

The writer creates a new document from RGB pixels and copies no input objects.
The prototype deliberately supports only unrotated, text-only pages. It is not
format admission: object inventory, arbitrary transforms, images/outlined-text
policy, font fallback and annotation coverage still require dedicated evidence.
"""
from __future__ import annotations

from .styles import DEFAULT as DEFAULT_STYLE, STYLES, paint

import ctypes
import math
import zlib
from dataclasses import dataclass
from typing import BinaryIO, Callable

import pypdfium2 as pdfium
import pypdfium2.raw as raw
from PIL import Image


OBJECT_DEPTH = 16  # deepest Form nesting the traversal descends into
SCAN_SHARE = 0.5   # images covering this share of a page without text make it a scanned page
# A vector path drawn by the page is admitted only when it is plainly structure (table rules, cell borders,
# shading): straight horizontal and vertical segments, few per subpath, each subpath either thin like a
# rule or larger than any glyph. Anything else may be outlined text, which cannot be read (a coverage gap,
# requirements 6.3), so the page is refused.
THIN_POINTS = 3.0
LARGE_POINTS = 36.0
MAX_SUBPATH_SEGMENTS = 5
MAX_PATH_SEGMENTS = 4096
AXIS_EPSILON = 0.01


def _structural(obj) -> bool:
    m = raw.FS_MATRIX()
    if not raw.FPDFPageObj_GetMatrix(obj.raw, m) or abs(m.b) > 1e-6 or abs(m.c) > 1e-6 or m.a == 0 or m.d == 0:
        return False  # rotated or sheared: not a plain rule
    n = raw.FPDFPath_CountSegments(obj.raw)
    if not 0 < n <= MAX_PATH_SEGMENTS:
        return False
    x, y = ctypes.c_float(), ctypes.c_float()
    subpaths, current = [], None
    for i in range(n):
        seg = raw.FPDFPath_GetPathSegment(obj.raw, i)
        kind = raw.FPDFPathSegment_GetType(seg)
        if not seg or not raw.FPDFPathSegment_GetPoint(seg, x, y) or kind == raw.FPDF_SEGMENT_BEZIERTO:
            return False
        point = (m.a * x.value + m.e, m.d * y.value + m.f)
        if not all(math.isfinite(v) for v in point):
            return False
        if kind == raw.FPDF_SEGMENT_MOVETO or current is None:
            current = [point]
            subpaths.append(current)
        else:
            # PDFium reports the edge a close (h) implies as a segment of its own, flagged closing, so it is
            # checked like any other edge
            current.append(point)
    for points in subpaths:
        if len(points) - 1 > MAX_SUBPATH_SEGMENTS:
            return False
        for (x0, y0), (x1, y1) in zip(points, points[1:]):
            if abs(x1 - x0) > AXIS_EPSILON and abs(y1 - y0) > AXIS_EPSILON:
                return False  # diagonal
        xs, ys = [p[0] for p in points], [p[1] for p in points]
        w, h = max(xs) - min(xs), max(ys) - min(ys)
        if not (min(w, h) <= THIN_POINTS or max(w, h) >= LARGE_POINTS):
            return False  # glyph-sized
    return True


def _covered_share(boxes, width, height):
    """Upper bound of the page share the boxes cover (their clipped areas summed)."""
    total = 0.0
    for left, bottom, right, top in boxes:
        w = min(right, width) - max(left, 0)
        h = min(top, height) - max(bottom, 0)
        if w > 0 and h > 0:
            total += w * h
    return total / (width * height)


class PDFError(ValueError):
    pass


@dataclass(frozen=True)
class Limits:
    pages: int = 2000
    pixels: int = 32_000_000
    chars: int = 1024 * 1024
    objects: int = 100_000
    output_bytes: int = 4 * 1024**3


@dataclass(frozen=True)
class Character:
    value: str
    box: tuple[float, float, float, float]


@dataclass(frozen=True)
class PageText:
    page: int
    width: float
    height: float
    characters: tuple[Character, ...]
    # visible bounds (left, bottom, right, top, in points) of the page's images, after their transform
    images: tuple[tuple[float, float, float, float], ...] = ()

    @property
    def text(self):
        return ''.join(char.value for char in self.characters)


class RasterWriter:
    """Write pages immediately; retain only a bounded xref table and page IDs."""

    def __init__(self, destination: BinaryIO, limits: Limits = Limits()):
        self.destination = destination
        self.limits = limits
        self.position = 0
        self.offsets = [0, 0, 0]
        self.pages = []
        self.closed = False
        self._write(b'%PDF-1.4\n%\xe2\xe3\xcf\xd3\n')
        self._object(1, b'<< /Type /Catalog /Pages 2 0 R >>')

    def _write(self, data):
        if self.position + len(data) > self.limits.output_bytes:
            raise PDFError('PDF_OUTPUT_LIMIT')
        count = self.destination.write(data)
        if count is not None and count != len(data):
            raise PDFError('PDF_OUTPUT_WRITE_FAILED')
        self.position += len(data)

    def _start(self, identifier):
        if identifier >= len(self.offsets):
            self.offsets.extend([0] * (identifier + 1 - len(self.offsets)))
        self.offsets[identifier] = self.position
        self._write(f'{identifier} 0 obj\n'.encode())

    def _object(self, identifier, body):
        self._start(identifier)
        self._write(body)
        self._write(b'\nendobj\n')

    def page(self, image: Image.Image, width: float, height: float):
        if self.closed:
            raise PDFError('PDF_WRITER_CLOSED')
        if len(self.pages) >= self.limits.pages:
            raise PDFError('PDF_PAGE_LIMIT')
        if image.mode != 'RGB' or image.width * image.height > self.limits.pixels:
            raise PDFError('PDF_PIXEL_LIMIT')
        if not all(math.isfinite(n) and 0 < n <= 14400 for n in (width, height)):
            raise PDFError('PDF_PAGE_SIZE_UNSUPPORTED')
        page_id = 3 + 4 * len(self.pages)
        image_id, content_id, length_id = page_id + 1, page_id + 2, page_id + 3
        self._object(page_id, (f'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 {width:.6f} {height:.6f}] '
                              f'/Resources << /XObject << /Im0 {image_id} 0 R >> >> '
                              f'/Contents {content_id} 0 R >>').encode())
        self._start(image_id)
        self._write((f'<< /Type /XObject /Subtype /Image /Width {image.width} /Height {image.height} '
                     f'/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode '
                     f'/Length {length_id} 0 R >>\nstream\n').encode())
        start = self.position
        compressor = zlib.compressobj(level=6)
        # One bounded strip, rather than image.tobytes() for the full page.
        for row in range(0, image.height, 16):
            strip = image.crop((0, row, image.width, min(image.height, row + 16)))
            try:
                self._write(compressor.compress(strip.tobytes()))
            finally:
                strip.close()
        self._write(compressor.flush())
        length = self.position - start
        self._write(b'\nendstream\nendobj\n')
        self._object(length_id, str(length).encode())
        operators = f'q {width:.6f} 0 0 {height:.6f} 0 0 cm /Im0 Do Q\n'.encode()
        self._object(content_id, f'<< /Length {len(operators)} >>\nstream\n'.encode() + operators + b'endstream')
        self.pages.append(page_id)

    def finish(self):
        if self.closed or not self.pages:
            raise PDFError('PDF_WRITER_INVALID_STATE')
        children = ' '.join(f'{identifier} 0 R' for identifier in self.pages)
        self._object(2, f'<< /Type /Pages /Count {len(self.pages)} /Kids [{children}] >>'.encode())
        start = self.position
        self._write(f'xref\n0 {len(self.offsets)}\n0000000000 65535 f \n'.encode())
        for offset in self.offsets[1:]:
            if offset >= 10**10:
                raise PDFError('PDF_OUTPUT_LIMIT')
            self._write(f'{offset:010d} 00000 n \n'.encode())
        self._write((f'trailer\n<< /Size {len(self.offsets)} /Root 1 0 R >>\n'
                     f'startxref\n{start}\n%%EOF\n').encode())
        self.closed = True


def _page_text(page, ordinal, limits):
    if page.get_rotation() != 0:
        raise PDFError('PDF_ROTATION_REQUIRES_HOST_TRANSFORM')
    width, height = page.get_size()
    if page.get_bbox() != (0, 0, width, height):
        raise PDFError('PDF_PAGE_ORIGIN_UNSUPPORTED')
    count = 0
    images = []
    for obj in page.get_objects(max_depth=OBJECT_DEPTH):
        count += 1
        if count > limits.objects:
            raise PDFError('PDF_OBJECT_LIMIT')
        if obj.type == raw.FPDF_PAGEOBJ_FORM:
            # Annotation flattening wraps every annotation, including invisible
            # ones (links, notes), in a Form. A Form is admitted only if the
            # traversal actually descended into it; the helper stops descending
            # at depth OBJECT_DEPTH - 1, and what lies below is unvisited. Its
            # children are then judged by the same rule, so a Form holding a
            # path or an image is still refused.
            if obj.level >= OBJECT_DEPTH - 1:
                raise PDFError('PDF_FORM_DEPTH_COVERAGE_GAP')
            continue
        if obj.type == raw.FPDF_PAGEOBJ_IMAGE:
            # An image drawn by the page itself is located by its transformed bounding box, which the
            # plan may paint over (requirements 4.2). One inside a Form is in the Form's coordinates and
            # under its clip; it is not located here.
            if obj.level != 0:
                raise PDFError('PDF_NESTED_IMAGE_COVERAGE_GAP')
            bounds = tuple(float(v) for v in obj.get_bounds())
            if not all(math.isfinite(v) for v in bounds) or bounds[2] < bounds[0] or bounds[3] < bounds[1]:
                raise PDFError('PDF_UNRELIABLE_LOCATION')
            images.append(bounds)
            continue
        if obj.type == raw.FPDF_PAGEOBJ_PATH and obj.level == 0:
            if not _structural(obj):
                raise PDFError('PDF_VECTOR_COVERAGE_GAP')
            continue
        if obj.type != raw.FPDF_PAGEOBJ_TEXT:
            raise PDFError('PDF_NON_TEXT_COVERAGE_POLICY_REQUIRED')
    textpage = page.get_textpage()
    try:
        count = textpage.count_chars()
        if count > limits.chars:
            raise PDFError('PDF_TEXT_LIMIT')
        characters = []
        for index in range(count):
            value = raw.FPDFText_GetUnicode(textpage, index)
            if (raw.FPDFText_HasUnicodeMapError(textpage, index) != 0 or value == 0
                    or value > 0x10FFFF or 0xD800 <= value <= 0xDFFF):
                raise PDFError('PDF_UNICODE_COVERAGE_GAP')
            box = textpage.get_charbox(index)
            if not all(math.isfinite(number) for number in box):
                raise PDFError('PDF_UNRELIABLE_LOCATION')
            characters.append(Character(chr(value), box))
        if images and not characters and _covered_share(images, width, height) >= SCAN_SHARE:
            # a scanned page: removing its image removes the page; that needs OCR (P1) or the person's
            # explicit choice, never a default (requirements 4.2)
            raise PDFError('PDF_SCAN_PAGE_REQUIRES_OCR')
        return PageText(ordinal, width, height, tuple(characters), tuple(images))
    finally:
        textpage.close()


def rasterize_text_subset(source: BinaryIO, destination: BinaryIO,
                          regions: Callable[[PageText, tuple[int, int]], list[tuple[int, int, int, int]]],
                          dpi: int = 200, limits: Limits = Limits(), style: str = DEFAULT_STYLE):
    """Apply host-supplied *pixel* rectangles, filled with the texture of ``style``, page by page.

    A fresh candidate must be discarded on any exception. No password is
    accepted in parameters here; encrypted input requires the unlock stage.
    The caller must independently validate the candidate and authorize publish.
    """
    if dpi not in {150, 200, 300}:
        raise PDFError('PDF_DPI_UNSUPPORTED')
    if style not in STYLES:
        raise PDFError('PDF_STYLE_UNSUPPORTED')
    try:
        document = pdfium.PdfDocument(source)
    except pdfium.PdfiumError:
        raise PDFError('PDF_OPEN_FAILED') from None
    try:
        if not 0 < len(document) <= limits.pages:
            raise PDFError('PDF_PAGE_LIMIT')
        writer = RasterWriter(destination, limits)
        for index in range(len(document)):
            page = document[index]
            try:
                # Use the raw function because the helper requires a form env
                # even for documents without AcroForm. Never initialize JS/XFA.
                status = raw.FPDFPage_Flatten(page, raw.FLAT_NORMALDISPLAY)
                if status not in {raw.FLATTEN_SUCCESS, raw.FLATTEN_NOTHINGTODO}:
                    raise PDFError('PDF_FLATTEN_COVERAGE_GAP')
            finally:
                page.close()
            page = document[index]  # flatten invalidates page-related handles
            try:
                facts = _page_text(page, index + 1, limits)
                pixels = (math.ceil(facts.width * dpi / 72), math.ceil(facts.height * dpi / 72))
                if pixels[0] <= 0 or pixels[1] <= 0 or pixels[0] * pixels[1] > limits.pixels:
                    raise PDFError('PDF_PIXEL_LIMIT')
                rectangles = regions(facts, pixels)
                if len(rectangles) > limits.chars:
                    raise PDFError('PDF_REGION_LIMIT')
                for left, top, right, bottom in rectangles:
                    if not (all(type(v) is int for v in (left, top, right, bottom)) and
                            0 <= left < right <= pixels[0] and 0 <= top < bottom <= pixels[1]):
                        raise PDFError('PDF_REGION_INVALID')
                bitmap = page.render(scale=dpi / 72, rev_byteorder=True, may_draw_forms=False)
                try:
                    image = bitmap.to_pil().convert('RGB')
                    try:
                        for rectangle in rectangles:
                            paint(image, rectangle, style, dpi)
                        writer.page(image, facts.width, facts.height)
                    finally:
                        image.close()
                finally:
                    bitmap.close()
            finally:
                page.close()
        writer.finish()
    except pdfium.PdfiumError:
        raise PDFError('PDF_PROCESSING_FAILED') from None
    finally:
        document.close()
