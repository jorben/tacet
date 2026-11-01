import io
import unittest

import pypdfium2 as pdfium

from pdf_fixtures import build
from test_pdf_write import checks, results, text_op, run_write
from tacet_format_pdf.raster import Limits, PDFError, _page_text

TEXT = 'Call 13812345678 today'


def page(drawing, text=TEXT):
    return build([text], extra={11: ('<< ', f'BT /F1 12 Tf 20 100 Td ({text}) Tj ET {drawing}'.encode())})


def facts(raw_pdf):
    d = pdfium.PdfDocument(io.BytesIO(raw_pdf))
    try:
        return _page_text(d[0], 1, Limits())
    finally:
        d.close()


# Table rules, cell borders and shading are admitted; shapes that could be outlined text are a coverage gap.
STRUCTURE = {
    'rules': '0.5 w 10 90 m 290 90 l 10 120 m 290 120 l S',
    'column rule': '0.5 w 150 80 m 150 130 l S',
    'thin filled border': '10 89 280 1 re f',
    'cell shading': '0.9 g 10 95 120 20 re f',
    'table frame': '1 w 10 80 280 50 re S',
    'scaled frame': 'q 2 0 0 2 0 0 cm 5 40 140 25 re S Q',
    'small box scaled up to a cell': 'q 5 0 0 5 0 0 cm 4 2 9 9 re f Q',  # judged where it is drawn
}
GAPS = {
    'curve': '20 150 m 30 170 40 150 50 160 c f',
    'diagonal stroke': '1 w 20 150 m 30 170 l S',
    'glyph-sized box': '20 150 9 9 re f',
    'rotated rule': 'q 0.7071 0.7071 -0.7071 0.7071 100 100 cm 0 0 m 80 0 l S Q',
    'many segments': '1 w 20 150 m 20 160 l 21 160 l 21 150 l 22 150 l 22 160 l 23 160 l S',
    # each of the following is refused by one rule alone
    'large curve on an axis-aligned frame': '1 w 20 100 m 20 160 80 160 80 100 c S',
    'large diagonal': '1 w 20 100 m 120 180 l S',
    'triangle closed by an implied diagonal': '20 120 m 120 120 l 120 190 l h f',
    'cell scaled down to a glyph': 'q 0.2 0 0 0.2 0 0 cm 100 750 45 45 re f Q',
}


class VectorTest(unittest.TestCase):
    def test_structure_is_admitted_and_the_text_on_the_page_is_redacted(self):
        for name, drawing in STRUCTURE.items():
            with self.subTest(name):
                self.assertEqual(facts(page(drawing)).text.strip()[:4], 'Call')
                _, items = run_write(page(drawing), [text_op(1)])
                self.assertEqual(results(items), {'op_1': ('executed', None)})

    def test_shapes_that_could_be_outlined_text_are_a_coverage_gap(self):
        for name, drawing in GAPS.items():
            with self.subTest(name):
                with self.assertRaisesRegex(PDFError, 'PDF_VECTOR_COVERAGE_GAP'):
                    facts(page(drawing))

    def test_a_structural_page_validates_and_its_rules_stay(self):
        from test_docx_extract import FakeTask
        from test_pdf_write import schemas
        from tacet_format_pdf.validate import validate
        raw_pdf = page(STRUCTURE['rules'] + ' ' + STRUCTURE['cell shading'])
        out, items = run_write(raw_pdf, [text_op(1)])
        task = FakeTask()
        validate(io.BytesIO(raw_pdf), [text_op(1)], io.BytesIO(out), task, schemas())
        self.assertEqual(checks(task.items), {'op_1': (True, False)})
        d = pdfium.PdfDocument(io.BytesIO(out))
        img = d[0].render(scale=200 / 72).to_pil().convert('L')
        d.close()
        y = round((200 - 120) * 200 / 72)  # the upper rule
        self.assertTrue(any(img.getpixel((x, yy)) < 128 for x in range(700, 760) for yy in (y - 1, y, y + 1)))


if __name__ == '__main__':
    unittest.main()
