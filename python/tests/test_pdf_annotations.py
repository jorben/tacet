"""Annotation / form coverage of the PDFium raster chain (M0 evidence, not admission)."""
import io
import math
import unittest

import pypdfium2 as pdfium
from tacet_format_pdf.raster import PDFError, rasterize_text_subset

SECRET = 'ANNOT_SECRET_VALUE'
FONT = '/Resources << /Font << /F1 3 0 R >> >>'


def build(annots='', acro='', objs=None):
    stream = b'BT /F1 18 Tf 20 80 Td (Public body) Tj ET'
    table = {1: f'<< /Type /Catalog /Pages 2 0 R {acro} >>'.encode(),
             2: b'<< /Type /Pages /Count 1 /Kids [4 0 R] >>',
             3: b'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
             4: f'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 240 120] {FONT} /Contents 5 0 R {annots} >>'.encode(),
             5: f'<< /Length {len(stream)} >>\nstream\n'.encode() + stream + b'\nendstream'}
    table.update(objs or {})
    out, offsets = bytearray(b'%PDF-1.4\n'), [0]
    for i in range(1, max(table) + 1):
        offsets.append(len(out))
        out += f'{i} 0 obj\n'.encode() + table[i] + b'\nendobj\n'
    xref = len(out)
    out += f'xref\n0 {len(offsets)}\n0000000000 65535 f \n'.encode()
    for offset in offsets[1:]:
        out += f'{offset:010d} 00000 n \n'.encode()
    out += f'trailer\n<< /Size {len(offsets)} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n'.encode()
    return bytes(out)


def form(body, resources=''):
    data = body.encode()
    return (f'<< /Type /XObject /Subtype /Form /BBox [0 0 180 30] /Resources << /Font << /F1 3 0 R >> {resources} >> '
            f'/Length {len(data)} >>\nstream\n').encode() + data + b'\nendstream'


def freetext(content=SECRET, body=None):
    body = body or f'BT /F1 12 Tf 2 10 Td ({content}) Tj ET'
    return {6: f'<< /Type /Annot /Subtype /FreeText /Rect [20 20 200 50] /Contents ({content}) /DA (/F1 12 Tf 0 g) '
               f'/AP << /N 7 0 R >> >>'.encode(), 7: form(body)}


NOTE = {6: f'<< /Type /Annot /Subtype /Text /Rect [20 20 40 40] /Contents ({SECRET}) /Open false >>'.encode()}
LINK = {6: f'<< /Type /Annot /Subtype /Link /Rect [20 20 100 40] /Border [0 0 0] /A << /S /URI /URI ({SECRET}) >> >>'.encode()}
WIDGET = {6: f'<< /Type /Annot /Subtype /Widget /FT /Tx /T (name) /V ({SECRET}) /Rect [20 20 200 50] /F 4 '
             f'/DA (/F1 12 Tf 0 g) >>'.encode()}


def run(data, select=lambda page, px: []):
    out = io.BytesIO()
    rasterize_text_subset(io.BytesIO(data), out, select, dpi=150)
    return out.getvalue()


def dark_pixels(candidate, top=70, bottom=120):
    with pdfium.PdfDocument(candidate) as document:
        page = document[0]
        try:
            image = page.render(scale=1).to_pil().convert('L')
        finally:
            page.close()
    return sum(1 for value in image.crop((0, top, 240, bottom)).tobytes() if value < 128)


def covering(needle):
    def select(page, pixels):
        start = page.text.index(needle)
        boxes = [c.box for c in page.characters[start:start + len(needle)]]
        scale = pixels[0] / page.width
        return [(math.floor(min(b[0] for b in boxes) * scale) - 2,
                 math.floor((page.height - max(b[3] for b in boxes)) * scale) - 2,
                 math.ceil(max(b[2] for b in boxes) * scale) + 2,
                 math.ceil((page.height - min(b[1] for b in boxes)) * scale) + 2)]
    return select


class InvisibleAnnotationTests(unittest.TestCase):
    def test_annotations_without_appearance_are_admitted_and_leave_nothing_behind(self):
        plain = run(build())
        for name, objs, acro in (('note', NOTE, ''), ('link', LINK, ''),
                                 ('widget', WIDGET, '/AcroForm << /Fields [6 0 R] /NeedAppearances true >>')):
            with self.subTest(name=name):
                candidate = run(build('/Annots [6 0 R]', acro, objs))
                self.assertNotIn(SECRET.encode(), candidate)       # Contents, URI and field value never reach the output
                for marker in (b'/Annots', b'/URI', b'/AcroForm', b'/Widget'):
                    self.assertNotIn(marker, candidate)
                self.assertEqual(dark_pixels(candidate, 0, 120), dark_pixels(plain, 0, 120))  # nothing drawn

    def test_metadata_in_the_source_does_not_reach_the_output(self):
        data = build().replace(b'trailer\n<<', f'trailer\n<< /Info << /Author ({SECRET}) >>'.encode(), 1)
        self.assertNotIn(SECRET.encode(), run(data))


def dark_box(candidate, dpi=150, top_pt=70):
    """Bounding box (pixels at ``dpi``) of dark pixels in the lower area, where the annotation sits."""
    with pdfium.PdfDocument(candidate) as document:
        page = document[0]
        try:
            image = page.render(scale=dpi / 72).to_pil().convert('L')
        finally:
            page.close()
    offset = math.floor(top_pt * dpi / 72)
    box = image.crop((0, offset, image.width, image.height)).point(lambda v: 255 if v < 128 else 0).getbbox()
    return None if box is None else (box[0], box[1] + offset, box[2], box[3] + offset)


class VisibleAnnotationTests(unittest.TestCase):
    def test_visible_annotation_text_is_extracted_and_can_be_redacted(self):
        data = build('/Annots [6 0 R]', objs=freetext())
        seen = {}

        def probe(page, pixels):
            seen['text'] = page.text
            seen['rect'] = covering(SECRET)(page, pixels)[0]
            return [seen['rect']]

        text_box = dark_box(run(data))
        self.assertIsNotNone(text_box)                               # the annotation is really drawn
        candidate = run(data, probe)
        self.assertIn(SECRET, seen['text'])                          # detection can see it, with a character box
        left, top, right, bottom = seen['rect']
        self.assertTrue(left <= text_box[0] and top <= text_box[1] and text_box[2] <= right and text_box[3] <= bottom,
                        'the annotation text is not inside the character-box region')
        remaining = dark_box(candidate)                              # re-rendering resamples the edge by up to a pixel
        self.assertTrue(all(abs(got - want) <= 1 for got, want in zip(remaining, (left, top, right, bottom))),
                        (remaining, seen['rect']))                   # only the solid region remains
        self.assertNotIn(SECRET.encode(), candidate)

    def test_annotation_with_vector_content_is_still_a_coverage_gap(self):
        body = f'0 0 180 30 re f BT /F1 12 Tf 2 10 Td ({SECRET}) Tj ET'
        with self.assertRaisesRegex(PDFError, 'PDF_NON_TEXT_COVERAGE_POLICY_REQUIRED'):
            run(build('/Annots [6 0 R]', objs=freetext(body=body)))

    def test_form_nesting_below_the_traversal_depth_is_a_coverage_gap(self):
        def chain(depth, leaf):
            objs = freetext()
            objs[7] = form('/X Do', '/XObject << /X 8 0 R >>')
            for level in range(depth):
                last = level == depth - 1
                objs[8 + level] = form(leaf if last else '/X Do', '' if last else f'/XObject << /X {9 + level} 0 R >>')
            return objs
        shallow = build('/Annots [6 0 R]', objs=chain(3, f'BT /F1 12 Tf 2 10 Td ({SECRET}) Tj ET'))
        self.assertIn(b'/XObject', shallow)
        run(shallow)                                               # three levels of text-only forms are traversed
        deep = build('/Annots [6 0 R]', objs=chain(20, '0 0 180 30 re f'))
        with self.assertRaises(PDFError) as ctx:                    # a path hidden below the cutoff must not pass
            run(deep)
        self.assertIn(ctx.exception.args[0], {'PDF_FORM_DEPTH_COVERAGE_GAP', 'PDF_NON_TEXT_COVERAGE_POLICY_REQUIRED'})


if __name__ == '__main__':
    unittest.main()
