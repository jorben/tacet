"""``format.preview`` for text PDF: one page as a PNG, plus the pixel rectangles of the marked ranges.

The page is drawn as it is (forms are not run, scripts and XFA are never touched). The rectangles
come from the same character boxes and the same geometry as the rasterising writer, so what the
person sees outlined is what an approved operation would cover. A mark that cannot be located
reliably is reported as unmapped instead of being guessed. Pages with images, annotations or
rotation are still drawn: this is a view, not a judgement of coverage (extract reports the gaps).
"""
from __future__ import annotations

import io
import math

import pypdfium2 as pdfium
import pypdfium2.raw as raw

from tacet_plugin_sdk.runtime import TaskError

from .geometry import GeometryError, regions
from .raster import Character, Limits
from .write import op_boxes


def _characters(page, limit):
    textpage = page.get_textpage()
    try:
        count = textpage.count_chars()
        if count > limit:
            return None
        out, text = [], []
        for index in range(count):
            value = raw.FPDFText_GetUnicode(textpage, index)
            if (raw.FPDFText_HasUnicodeMapError(textpage, index) != 0 or value == 0
                    or value > 0x10FFFF or 0xD800 <= value <= 0xDFFF):
                return None
            box = textpage.get_charbox(index)
            if not all(math.isfinite(number) for number in box):
                return None
            out.append(Character(chr(value), box))
            text.append(chr(value))
        return ''.join(text), out
    finally:
        textpage.close()


def preview(source, sink, page_number, dpi, marks, limits: Limits = Limits()):
    """Writes the PNG to ``sink``; returns the ``page_preview`` item."""
    try:
        document = pdfium.PdfDocument(source)
    except pdfium.PdfiumError:
        raise TaskError('INPUT_CORRUPT') from None
    try:
        pages = len(document)
        if not 0 < pages <= limits.pages:
            raise TaskError('RESOURCE_LIMIT')
        if page_number > pages:
            raise TaskError('PROTOCOL_INVALID')
        page = document[page_number - 1]
        try:
            width, height = page.get_size()
            pixels = (math.ceil(width * dpi / 72), math.ceil(height * dpi / 72))
            if pixels[0] <= 0 or pixels[1] <= 0 or pixels[0] > 20000 or pixels[1] > 20000 or pixels[0] * pixels[1] > limits.pixels:
                raise TaskError('RESOURCE_LIMIT')
            facts = _characters(page, limits.chars) if marks else None
            rects, unmapped = [], []
            for mark in marks:
                located = None
                if facts is not None:
                    boxes, reason = op_boxes(facts[0], facts[1], mark['ordinal'], mark['start'], mark['end'])
                    if not reason:
                        try:
                            located = regions(boxes, width, height, dpi)
                        except GeometryError:
                            located = None
                if not located:
                    unmapped.append(mark['id'])
                    continue
                rects.extend({'id': mark['id'], 'rect': list(r)} for r in located)
                if len(rects) > 20000:
                    raise TaskError('RESOURCE_LIMIT')
            bitmap = page.render(scale=dpi / 72, rev_byteorder=True, may_draw_forms=False)
            try:
                image = bitmap.to_pil().convert('RGB')
                try:
                    buffer = io.BytesIO()
                    image.save(buffer, format='PNG', optimize=False)
                finally:
                    image.close()
            finally:
                bitmap.close()
        finally:
            page.close()
    except pdfium.PdfiumError:
        raise TaskError('INPUT_CORRUPT') from None
    finally:
        document.close()
    data = buffer.getvalue()
    if len(data) > 64 << 20:
        raise TaskError('OUTPUT_LIMIT')
    sink.write(data)
    return {'kind': 'page_preview', 'page': page_number, 'pages': pages, 'width_px': pixels[0], 'height_px': pixels[1],
            'image': 'png', 'bytes': len(data), 'rects': rects, 'unmapped': unmapped}
