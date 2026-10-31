import io
import unittest

import pypdfium2 as pdfium

from pdf_fixtures import build
from test_docx_extract import FakeTask
from test_pdf_write import DOC, checks, schemas, text_op
from tacet_format_pdf import styles
from tacet_format_pdf.raster import PDFError, rasterize_text_subset
from tacet_format_pdf.validate import validate
from tacet_format_pdf.write import write


def run(style, dpi=200, ops=None):
    ops = ops or [text_op(1)]
    task, sink = FakeTask(), io.BytesIO()
    write(io.BytesIO(DOC), ops, sink, task, schemas(), dpi, style)
    vtask = FakeTask()
    validate(io.BytesIO(DOC), ops, io.BytesIO(sink.getvalue()), vtask, schemas(), dpi, style)
    return sink.getvalue(), checks(vtask.items)


class StylesTest(unittest.TestCase):
    def test_every_style_is_a_fixed_texture_that_does_not_depend_on_the_page(self):
        for style in styles.STYLES:
            for dpi in (150, 200, 300):
                a = styles.expected(style, dpi, (10, 20, 70, 44))
                b = styles.expected(style, dpi, (10, 20, 70, 44))
                self.assertEqual(a.tobytes(), b.tobytes(), style)
                # anchored at the page origin: a region's pixels are the same cut of one page-wide texture
                big = styles.expected(style, dpi, (0, 0, 80, 50))
                self.assertEqual(big.crop((10, 20, 70, 44)).tobytes(), a.tobytes(), style)

    def test_the_mosaic_does_not_come_from_the_pixels_it_covers(self):
        # two different pages, the same region: the painted pixels are identical
        other = build(['Call 19999999999 today', 'second page'])
        a = styles.expected('mosaic', 200, (100, 40, 260, 80))
        page_a, page_b = io.BytesIO(), io.BytesIO()
        rect = (100, 40, 260, 80)
        rasterize_text_subset(io.BytesIO(DOC), page_a, lambda p, size: [rect] if p.page == 1 else [], dpi=200, style='mosaic')
        rasterize_text_subset(io.BytesIO(other), page_b, lambda p, size: [rect] if p.page == 1 else [], dpi=200, style='mosaic')
        crops = []
        for raw in (page_a.getvalue(), page_b.getvalue()):
            d = pdfium.PdfDocument(io.BytesIO(raw))
            crops.append(d[0].render(scale=200 / 72).to_pil().convert('RGB').crop(rect).tobytes())
            d.close()
        self.assertEqual(crops[0], crops[1])
        self.assertEqual(crops[0], a.tobytes())

    def test_each_style_is_written_and_validated_and_looks_different(self):
        seen = set()
        for style in styles.STYLES:
            out, result = run(style)
            self.assertEqual(result, {'op_1': (True, False)}, style)
            seen.add(out)
        self.assertEqual(len(seen), len(styles.STYLES))

    def test_a_region_painted_in_another_style_is_not_counted_as_executed(self):
        task, sink = FakeTask(), io.BytesIO()
        write(io.BytesIO(DOC), [text_op(1)], sink, task, schemas(), 200, 'black')
        for planned in ('stripe', 'rainbow', 'gray'):
            vtask = FakeTask()
            validate(io.BytesIO(DOC), [text_op(1)], io.BytesIO(sink.getvalue()), vtask, schemas(), 200, planned)
            self.assertEqual(checks(vtask.items), {'op_1': (False, True)}, planned)

    def test_an_unpainted_region_is_a_residual_for_a_light_style_too(self):
        task, sink = FakeTask(), io.BytesIO()
        write(io.BytesIO(DOC), [], sink, task, schemas(), 200, 'dot')
        vtask = FakeTask()
        validate(io.BytesIO(DOC), [text_op(1)], io.BytesIO(sink.getvalue()), vtask, schemas(), 200, 'dot')
        self.assertEqual(checks(vtask.items), {'op_1': (False, True)})

    def test_an_unknown_style_is_refused(self):
        with self.assertRaises(PDFError):
            rasterize_text_subset(io.BytesIO(DOC), io.BytesIO(), lambda p, size: [], dpi=200, style='glitter')
        with self.assertRaises(styles.StyleError):
            styles.expected('glitter', 200, (0, 0, 4, 4))


if __name__ == '__main__':
    unittest.main()
