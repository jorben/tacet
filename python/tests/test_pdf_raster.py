import io
import math
import unittest

import pypdfium2 as pdfium
import pypdfium2.raw as raw
from PIL import Image
from tacet_format_pdf.raster import Limits, PDFError, RasterWriter, rasterize_text_subset


def text_pdf(text='Public SECRET End', pages=1):
    # Minimal synthetic source, independent of the raster writer under test.
    objects = {1: b'<< /Type /Catalog /Pages 2 0 R >>',
               3: b'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'}
    children = []
    for index in range(pages):
        page_id = 4 + 2 * index
        content_id = page_id + 1
        children.append(page_id)
        stream = f'BT /F1 18 Tf 20 80 Td ({text}) Tj ET'.encode()
        objects[page_id] = (f'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 240 120] '
                            f'/Resources << /Font << /F1 3 0 R >> >> /Contents {content_id} 0 R >>').encode()
        objects[content_id] = f'<< /Length {len(stream)} >>\nstream\n'.encode() + stream + b'\nendstream'
    objects[2] = f'<< /Type /Pages /Count {pages} /Kids [{" ".join(str(p)+" 0 R" for p in children)}] >>'.encode()
    output = bytearray(b'%PDF-1.4\n')
    offsets = [0]
    for identifier in range(1, max(objects) + 1):
        offsets.append(len(output))
        output += f'{identifier} 0 obj\n'.encode() + objects[identifier] + b'\nendobj\n'
    xref = len(output)
    output += f'xref\n0 {len(offsets)}\n0000000000 65535 f \n'.encode()
    for offset in offsets[1:]:
        output += f'{offset:010d} 00000 n \n'.encode()
    output += f'trailer\n<< /Size {len(offsets)} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n'.encode()
    return bytes(output)


class PDFRasterTest(unittest.TestCase):
    def test_real_pdfium_text_mapping_to_redacted_pixels_and_new_pdf(self):
        rectangles = []
        def select(page, pixels):
            start = page.text.index('SECRET')
            boxes = [char.box for char in page.characters[start:start+6]]
            scale = pixels[0] / page.width
            # Test harness represents the host's geometry; production geometry
            # remains owned by Go plan/preview, not this format plugin.
            rect = (math.floor(min(b[0] for b in boxes) * scale) - 2,
                    math.floor((page.height - max(b[3] for b in boxes)) * scale) - 2,
                    math.ceil(max(b[2] for b in boxes) * scale) + 2,
                    math.ceil((page.height - min(b[1] for b in boxes)) * scale) + 2)
            rectangles.append(rect)
            return [rect]
        output = io.BytesIO()
        rasterize_text_subset(io.BytesIO(text_pdf()), output, select, dpi=150)
        candidate = output.getvalue()
        self.assertNotIn(b'SECRET', candidate)
        self.assertNotIn(b'/Font', candidate)
        self.assertEqual(candidate.count(b'%%EOF'), 1)
        with pdfium.PdfDocument(candidate) as document:
            self.assertEqual(len(document), 1)
            self.assertEqual(document.count_attachments(), 0)
            page = document[0]
            try:
                textpage = page.get_textpage()
                try:
                    self.assertEqual(textpage.count_chars(), 0)
                finally:
                    textpage.close()
                self.assertEqual([o.type for o in page.get_objects()], [raw.FPDF_PAGEOBJ_IMAGE])
                bitmap = page.render(scale=150/72, rev_byteorder=True)
                try:
                    image = bitmap.to_pil().convert('RGB')
                    try:
                        left, top, right, bottom = rectangles[0]
                        for x, y in ((left+2, top+2), ((left+right)//2, (top+bottom)//2), (right-3, bottom-3)):
                            self.assertEqual(image.getpixel((x, y)), (0, 0, 0))
                    finally:
                        image.close()
                finally:
                    bitmap.close()
            finally:
                page.close()

    def test_page_and_pixel_limits_apply_before_render(self):
        for limits, code in ((Limits(pages=1), 'PDF_PAGE_LIMIT'),
                             (Limits(pixels=20), 'PDF_PIXEL_LIMIT')):
            with self.subTest(code=code), self.assertRaisesRegex(PDFError, code):
                rasterize_text_subset(io.BytesIO(text_pdf(pages=2)), io.BytesIO(), lambda p, size: [], limits=limits)

    def test_image_source_is_a_coverage_gap_not_zero_text_success(self):
        output = io.BytesIO()
        writer = RasterWriter(output)
        image = Image.new('RGB', (20, 20), (255, 255, 255))
        try:
            writer.page(image, 20, 20)
            writer.finish()
        finally:
            image.close()
        # a page that is one image and no text is a scanned page: never painted over by default
        with self.assertRaisesRegex(PDFError, 'PDF_SCAN_PAGE_REQUIRES_OCR'):
            rasterize_text_subset(io.BytesIO(output.getvalue()), io.BytesIO(), lambda p, size: [])

    def test_append_only_writer_and_output_budget(self):
        class AppendOnly:
            def __init__(self):
                self.parts = []
            def write(self, value):
                self.parts.append(value)
                return len(value)
        destination = AppendOnly()
        writer = RasterWriter(destination)
        image = Image.new('RGB', (10, 20), (15, 20, 25))
        try:
            writer.page(image, 100, 200)
            writer.finish()
        finally:
            image.close()
        with pdfium.PdfDocument(b''.join(destination.parts)) as document:
            self.assertEqual(len(document), 1)
        with self.assertRaisesRegex(PDFError, 'PDF_OUTPUT_LIMIT'):
            RasterWriter(io.BytesIO(), Limits(output_bytes=1))

    def test_invalid_host_rectangles_are_rejected(self):
        with self.assertRaisesRegex(PDFError, 'PDF_REGION_INVALID'):
            rasterize_text_subset(io.BytesIO(text_pdf()), io.BytesIO(), lambda p, size: [(-1, 0, 10, 10)])


if __name__ == '__main__':
    unittest.main()
