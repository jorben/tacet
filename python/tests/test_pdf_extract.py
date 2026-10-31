import io
import unittest

import pypdfium2 as pdfium

from pdf_fixtures import build
from test_docx_extract import FakeTask
from test_pdf_probe_unlock import attachment_pdf, image_pdf
from tacet_format_pdf import extract as module
from tacet_format_pdf.extract import chunks, extract
from tacet_plugin_sdk.schema import Schemas


def run(raw):
    schemas = Schemas()
    schemas.compile('format.schema.json', 'ExtractItem')
    task = FakeTask()
    document = pdfium.PdfDocument(io.BytesIO(raw))
    try:
        extract(document, task, schemas)
    finally:
        document.close()
    return task.items


def states(items):
    return {i['part']: (i['class'], i['state'], i.get('reason')) for i in items if i['kind'] == 'part_state'}


class ExtractTest(unittest.TestCase):
    def test_one_unit_per_page_with_its_text_and_a_complete_marker(self):
        items = run(build(['phone 13812345678', 'second page']))
        units = [i for i in items if i['kind'] == 'text_unit']
        self.assertEqual([u['part'] for u in units], ['page/1', 'page/2'])
        self.assertIn('13812345678', units[0]['text'])
        self.assertEqual([u['visibility'] for u in units], ['visible', 'visible'])
        self.assertEqual(units[0]['source_map'], {'kind': 'paragraph_ordinal', 'part': 'page/1', 'ordinal': 1})
        self.assertEqual(len({u['unit_id'] for u in units}), 2)
        self.assertEqual(items[-1], {'kind': 'extract_complete', 'complete': True, 'units': 2, 'parts': 2})
        self.assertEqual(states(items), {'page/1': ('text', 'inspected', None), 'page/2': ('text', 'inspected', None)})

    def test_text_position_is_the_character_index_on_the_page(self):
        raw = build(['abc 13812345678'])
        unit = [i for i in run(raw) if i['kind'] == 'text_unit'][0]
        document = pdfium.PdfDocument(io.BytesIO(raw))
        page_text = document[0].get_textpage().get_text_range()
        self.assertEqual(unit['text'], page_text)
        start = unit['text'].index('13812345678')
        textpage = document[0].get_textpage()
        self.assertEqual(textpage.get_text_range(start, 11), '13812345678')
        document.close()

    def test_images_annotations_metadata_and_attachments_are_reported_as_gaps(self):
        s = states(run(image_pdf()))
        self.assertEqual(s['page/1/images'], ('unknown', 'not_inspected', 'IMAGE_CONTENT_NOT_INSPECTED'))
        s = states(run(attachment_pdf()))
        self.assertEqual(s['document/attachments'][1:], ('not_inspected', 'EMBEDDED_FILE_NOT_INSPECTED'))
        note = '<< /Type /Annot /Subtype /Text /Rect [20 20 40 40] /Contents (secret note) >>'
        s = states(run(build(['x'], extra={60: note.encode()}, page_extra='/Annots [60 0 R]')))
        self.assertEqual(s['page/1/annotations'][1:], ('not_inspected', 'ANNOTATION_NOT_INSPECTED'))
        # every part that was not read is class unknown: 'other' would make the host treat it as out of scope
        for items in (run(image_pdf()), run(attachment_pdf()), run(build(['x'], info={'Title': 'Quarterly'}))):
            for part, (cls, state, _) in states(items).items():
                if state == 'not_inspected':
                    self.assertEqual(cls, 'unknown', part)
        s = states(run(build(['x'], info={'Title': 'Quarterly'})))
        self.assertEqual(s['document/metadata'], ('unknown', 'not_inspected', 'METADATA_NOT_INSPECTED'))

    def test_link_annotations_and_empty_metadata_are_not_gaps(self):
        link = '<< /Type /Annot /Subtype /Link /Rect [20 20 100 40] /Border [0 0 0] >>'
        s = states(run(build(['x'], extra={60: link.encode()}, page_extra='/Annots [60 0 R]')))
        self.assertEqual(set(s), {'page/1'})

    def test_a_page_with_no_text_is_inspected_and_has_no_unit(self):
        items = run(build(['']))
        self.assertEqual([i for i in items if i['kind'] == 'text_unit'], [])
        self.assertEqual(items[-1]['units'], 0)

    def test_long_page_text_is_split_deterministically_at_line_breaks(self):
        old = module.CHUNK
        module.CHUNK = 10
        try:
            # lengths chosen so that a hard cut at 10 would land mid-line
            self.assertEqual(list(chunks('aaa\nbbbbb\ncc\ndddddd')), ['aaa\nbbbbb\n', 'cc\ndddddd'])
            self.assertEqual(list(chunks('aaaaaaa\nbbbbbbbbb')), ['aaaaaaa\n', 'bbbbbbbbb'])
            self.assertEqual(list(chunks('x' * 25)), ['x' * 10, 'x' * 10, 'x' * 5])
            self.assertEqual(list(chunks('')), [])
            self.assertEqual(''.join(chunks('a\n' * 17)), 'a\n' * 17)
        finally:
            module.CHUNK = old

    def test_a_page_that_cannot_be_read_reliably_is_a_failed_part_not_a_failed_file(self):
        original = module._characters
        calls = []

        def flaky(page, limits):
            calls.append(1)
            if len(calls) == 1:
                raise ValueError('PDF_UNICODE_COVERAGE_GAP')
            return original(page, limits)
        module._characters = flaky
        try:
            items = run(build(['one', 'two']))
        finally:
            module._characters = original
        s = states(items)
        self.assertEqual(s['page/1'], ('text', 'failed', 'PDF_UNICODE_COVERAGE_GAP'))
        self.assertEqual(s['page/2'][1], 'inspected')
        self.assertEqual([u['part'] for u in items if u['kind'] == 'text_unit'], ['page/2'])
        self.assertEqual(items[-1]['parts'], 2)

    def test_cancellation_stops_without_a_completion_marker(self):
        from tacet_plugin_sdk.runtime import Cancelled
        schemas = Schemas()
        schemas.compile('format.schema.json', 'ExtractItem')
        task = FakeTask()
        task.check = lambda: (_ for _ in ()).throw(Cancelled())
        document = pdfium.PdfDocument(io.BytesIO(build(['a', 'b'])))
        with self.assertRaises(Cancelled):
            extract(document, task, schemas)
        document.close()
        self.assertFalse([i for i in task.items if i['kind'] == 'extract_complete'])


if __name__ == '__main__':
    unittest.main()
