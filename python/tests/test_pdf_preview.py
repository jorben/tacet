import io
import unittest

import pypdfium2 as pdfium
from PIL import Image

from pdf_fixtures import build
from test_pdf_probe_unlock import image_pdf
from tacet_format_pdf.preview import preview
from tacet_format_pdf.write import op_boxes
from tacet_format_pdf.raster import Character
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.schema import Schemas

TEXT = 'Call 13812345678 today'   # the number is characters 5..16
DOC = build([TEXT, 'second page'])


def run(raw, page=1, dpi=100, marks=()):
    sink = io.BytesIO()
    item = preview(io.BytesIO(raw), sink, page, dpi, list(marks))
    s = Schemas()
    s.compile('format.schema.json', 'PreviewItem')
    s.validate('format.schema.json', 'PreviewItem', item)
    return item, sink.getvalue()


def mark(i='f1', ordinal=1, start=5, end=16):
    return {'id': i, 'ordinal': ordinal, 'start': start, 'end': end}


def dark(image, rect):
    l, t, r, b = rect
    return sum(image.crop((l, t, r, b)).convert('L').histogram()[:128])


class PreviewTest(unittest.TestCase):
    def test_png_matches_the_declared_size_and_page_count(self):
        item, data = run(DOC, page=2)
        self.assertEqual((item['page'], item['pages'], item['bytes']), (2, 2, len(data)))
        image = Image.open(io.BytesIO(data))
        self.assertEqual(image.format, 'PNG')
        self.assertEqual(image.size, (item['width_px'], item['height_px']))
        self.assertGreater(dark(image, (0, 0) + image.size), 20)   # something is drawn
        self.assertEqual((item['rects'], item['unmapped']), ([], []))

    def test_a_mark_is_outlined_where_its_text_is_and_nowhere_else(self):
        item, data = run(DOC, marks=[mark()])
        image = Image.open(io.BytesIO(data))
        self.assertTrue(item['rects'] and {r['id'] for r in item['rects']} == {'f1'})
        rects = [r['rect'] for r in item['rects']]
        self.assertGreater(sum(dark(image, r) for r in rects), 30)
        # the same rectangle on the page without that text is empty paper
        other, other_data = run(build(['', 'x']), marks=[])
        empty = Image.open(io.BytesIO(other_data))
        self.assertEqual(sum(dark(empty, r) for r in rects), 0)

    def test_rectangles_equal_what_the_writer_would_cover(self):
        from tacet_format_pdf.geometry import regions
        doc = pdfium.PdfDocument(DOC)
        try:
            page = doc[0]
            width, height = page.get_size()
            tp = page.get_textpage()
            chars = [Character(tp.get_text_range(i, 1), tp.get_charbox(i)) for i in range(tp.count_chars())]
            text = ''.join(c.value for c in chars)
            tp.close()
            page.close()
        finally:
            doc.close()
        boxes, reason = op_boxes(text, chars, 1, 5, 16)
        self.assertIsNone(reason)
        want = [list(r) for r in regions(boxes, width, height, 100)]
        item, _ = run(DOC, marks=[mark()])
        self.assertEqual([r['rect'] for r in item['rects']], want)

    def test_unlocatable_marks_are_reported_not_guessed(self):
        item, _ = run(DOC, marks=[mark('a', ordinal=9), mark('b', start=5, end=500), mark('c')])
        self.assertEqual(item['unmapped'], ['a', 'b'])
        self.assertEqual({r['id'] for r in item['rects']}, {'c'})

    def test_a_page_beyond_the_document_and_garbage_are_refused(self):
        with self.assertRaises(TaskError) as ctx:
            run(DOC, page=3)
        self.assertEqual(ctx.exception.code, 'PROTOCOL_INVALID')
        with self.assertRaises(TaskError) as ctx:
            run(b'not a pdf')
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')

    def test_a_page_with_an_image_is_still_drawn(self):
        item, data = run(image_pdf())
        self.assertEqual(Image.open(io.BytesIO(data)).size, (item['width_px'], item['height_px']))

    def test_dpi_scales_the_page(self):
        low, _ = run(DOC, dpi=72)
        high, _ = run(DOC, dpi=150)
        self.assertEqual(high['width_px'], -(-low['width_px'] * 150 // 72))


if __name__ == '__main__':
    unittest.main()
