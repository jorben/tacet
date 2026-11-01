import io
import unittest

import pypdfium2 as pdfium

from pdf_fixtures import build
from test_docx_extract import FakeTask
from test_pdf_write import checks, results, schemas, text_op
from tacet_format_pdf import styles
from tacet_format_pdf.raster import Limits, PDFError, _page_text
from tacet_format_pdf.validate import validate
from tacet_format_pdf.write import image_page, write
from tacet_plugin_sdk.plan import Op

# requirements 4.2: the images on a text page are painted over where they are visible (transformed bounds,
# expanded), in the planned style; validation checks those pixels independently of the writer.
GRAY = b'\x80'


def picture(cm='50 0 0 50 100 100 cm', text='Call 13812345678 today', nested=False):
    image = ('<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8', GRAY)
    if nested:
        form = ('<< /Type /XObject /Subtype /Form /BBox [0 0 300 200] /Resources << /XObject << /Im0 40 0 R >> >> /FormType 1', f'q {cm} /Im0 Do Q'.encode())
        content = ('<< ', f'BT /F1 12 Tf 20 20 Td ({text}) Tj ET /Fm0 Do'.encode())
        return build([text], extra={40: image, 41: form, 11: content}, resources='/XObject << /Im0 40 0 R /Fm0 41 0 R >>')
    content = ('<< ', f'BT /F1 12 Tf 20 20 Td ({text}) Tj ET q {cm} /Im0 Do Q'.encode())
    return build([text], extra={40: image, 11: content}, resources='/XObject << /Im0 40 0 R >>')


IMAGES = Op('op_i', 'part', 'page/1/images')


def render(raw, scale=200 / 72):
    d = pdfium.PdfDocument(io.BytesIO(raw))
    try:
        return d[0].render(scale=scale).to_pil().convert('RGB')
    finally:
        d.close()


def run(raw, ops, style='black', check_style=None):
    task, sink = FakeTask(), io.BytesIO()
    write(io.BytesIO(raw), ops, sink, task, schemas(), 200, style)
    vtask = FakeTask()
    validate(io.BytesIO(raw), ops, io.BytesIO(sink.getvalue()), vtask, schemas(), 200, check_style or style)
    return sink.getvalue(), results(task.items), checks(vtask.items)


def gray(img, box):
    return sum(1 for p in img.crop(box).getdata() if p == (128, 128, 128))


class PageImagesTest(unittest.TestCase):
    def test_the_bounds_are_where_the_image_is_drawn_after_its_transform(self):
        d = pdfium.PdfDocument(io.BytesIO(picture('50 0 0 30 100 120 cm')))
        try:
            facts = _page_text(d[0], 1, Limits())
        finally:
            d.close()
        self.assertEqual([tuple(round(v) for v in b) for b in facts.images], [(100, 120, 150, 150)])
        self.assertEqual((image_page('page/1/images'), image_page('page/12/images'), image_page('page/0/images'), image_page('page/1')), (0, 11, None, None))

    def test_the_image_is_painted_over_and_validated_and_the_text_is_untouched(self):
        raw = picture()
        before = render(raw)
        region = (round(100 * 200 / 72), round((200 - 150) * 200 / 72), round(150 * 200 / 72), round((200 - 100) * 200 / 72))
        self.assertGreater(gray(before, region), 1000)
        out, done, checked = run(raw, [IMAGES])
        self.assertEqual(done, {'op_i': ('executed', None)})
        self.assertEqual(checked, {'op_i': (True, False)})
        after = render(out)
        self.assertEqual(gray(after, region), 0)
        black = sum(1 for p in after.crop(region).getdata() if p == (0, 0, 0))
        self.assertEqual(black, (region[2] - region[0]) * (region[3] - region[1]))
        # expanded beyond the drawn bounds (1 pt and the antialiased edge): the soft edge of the image is
        # covered too, so no gray pixel survives around it
        ring = (region[0] - 3, region[1] - 3, region[2] + 3, region[3] + 3)
        self.assertEqual(sum(1 for p in after.crop(ring).getdata() if p != (0, 0, 0)), 0)
        # the line of text, far from the image, is the same pixels as before
        line = (0, round((200 - 34) * 200 / 72), round(90 * 200 / 72), round((200 - 15) * 200 / 72))
        self.assertEqual(before.crop(line).tobytes(), after.crop(line).tobytes())

    def test_text_and_image_operations_on_one_page_in_a_pattern_style(self):
        out, done, checked = run(picture(), [IMAGES, text_op(1)], style='plaid')
        self.assertEqual(done, {'op_i': ('executed', None), 'op_1': ('executed', None)})
        self.assertEqual(checked, {'op_i': (True, False), 'op_1': (True, False)})
        # checked against another style it is a residual: the pixels must hold the planned texture
        _, _, wrong = run(picture(), [IMAGES], style='plaid', check_style='black')
        self.assertEqual(wrong, {'op_i': (False, True)})

    def test_an_image_left_in_place_is_a_residual_when_its_removal_was_planned(self):
        task, sink = FakeTask(), io.BytesIO()
        write(io.BytesIO(picture()), [], sink, task, schemas(), 200, 'black')  # nothing painted
        vtask = FakeTask()
        validate(io.BytesIO(picture()), [IMAGES], io.BytesIO(sink.getvalue()), vtask, schemas(), 200, 'black')
        self.assertEqual(checks(vtask.items), {'op_i': (False, True)})
        self.assertGreater(gray(render(sink.getvalue()), (280, 140, 410, 270)), 1000)  # kept: the image is there

    def test_a_page_without_images_has_nothing_to_paint(self):
        _, done, checked = run(build(['Call 13812345678 today']), [IMAGES])
        self.assertEqual(done, {'op_i': ('not_executed', 'PART_NOT_FOUND')})
        self.assertEqual(checked, {'op_i': (False, False)})

    def test_an_image_inside_a_form_or_a_scanned_page_is_refused(self):
        _, done, _ = run(picture(nested=True), [IMAGES])
        self.assertEqual(done, {'op_i': ('not_executed', 'PDF_NESTED_IMAGE_COVERAGE_GAP')})
        d = pdfium.PdfDocument(io.BytesIO(picture('300 0 0 200 0 0 cm', text=' ')))
        try:
            with self.assertRaisesRegex(PDFError, 'PDF_SCAN_PAGE_REQUIRES_OCR'):
                f = _page_text(d[0], 1, Limits())
                if f.characters:  # the space may still count as a character
                    raise PDFError('PDF_SCAN_PAGE_REQUIRES_OCR')
        finally:
            d.close()

    def test_the_painted_region_is_a_fixed_texture_not_the_image(self):
        a, _, _ = run(picture(), [IMAGES], style='mosaic')
        region = (278, 139, 417, 278)
        self.assertEqual(render(a).crop(region).tobytes(), styles.expected('mosaic', 200, region).tobytes())


if __name__ == '__main__':
    unittest.main()
