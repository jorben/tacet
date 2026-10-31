import io
import unittest

import pypdfium2 as pdfium

from pdf_fixtures import build
from test_docx_extract import FakeTask
from test_pdf_probe_unlock import image_pdf
from tacet_format_pdf import extract as extract_module
from tacet_format_pdf.extract import extract
from tacet_format_pdf.parts import OUTPUT_PAGE_TYPE
from tacet_format_pdf.validate import validate
from tacet_format_pdf.write import op_boxes, write
from tacet_plugin_sdk.plan import Op
from tacet_plugin_sdk.runtime import TaskError
from tacet_plugin_sdk.schema import Schemas

TEXT = 'Call 13812345678 today'   # the number is characters 5..16
DOC = build([TEXT, 'second page'], info={'Title': 'T'})


def schemas():
    s = Schemas()
    for d in ('WriteItem', 'ValidateItem', 'ExtractItem'):
        s.compile('format.schema.json', d)
    return s


def text_op(i, part='page/1', ordinal=1, start=5, end=16, repl='[x]'):
    return Op(f'op_{i}', 'text', part, ordinal, start, end, repl)


def run_write(raw, ops, task=None):
    task, sink = task or FakeTask(), io.BytesIO()
    write(io.BytesIO(raw), ops, sink, task, schemas())
    return sink.getvalue(), task.items


def results(items):
    return {i['op']: (i['status'], i.get('reason')) for i in items if i['kind'] == 'op_result'}


def fates(items):
    return {i['part']: (i['fate'], i.get('expected_type')) for i in items if i['kind'] == 'part_fate'}


def run_validate(raw, cand, ops):
    task = FakeTask()
    validate(io.BytesIO(raw), ops, io.BytesIO(cand), task, schemas())
    return task.items


def checks(items):
    return {i['op']: (i['executed'], i['residual']) for i in items if i['kind'] == 'op_check'}


def render(raw, page=0, scale=200 / 72):
    d = pdfium.PdfDocument(io.BytesIO(raw))
    image = d[page].render(scale=scale).to_pil().convert('L')
    d.close()
    return image


class WriteTest(unittest.TestCase):
    def test_the_planned_range_is_painted_and_the_rest_of_the_page_is_not(self):
        out, items = run_write(DOC, [text_op(1)])
        self.assertEqual(results(items), {'op_1': ('executed', None)})
        before, after = render(DOC), render(out)
        self.assertEqual(before.size, after.size)
        changed = [(x, y) for y in range(0, before.height, 2) for x in range(0, before.width, 2) if before.getpixel((x, y)) != after.getpixel((x, y))]
        self.assertTrue(changed)
        xs = [x for x, _ in changed]
        # only the part of the line that holds the number differs: the word "Call" at the left is untouched
        self.assertGreater(min(xs), 60)
        self.assertEqual(render(DOC, 1).tobytes(), render(out, 1).tobytes())  # the second page is unchanged

    def test_the_output_has_no_text_layer_metadata_or_outline_and_every_page_is_converted(self):
        out, items = run_write(DOC, [text_op(1), Op('op_m', 'part', 'document/metadata')])
        self.assertEqual(fates(items), {'page/1': ('converted', OUTPUT_PAGE_TYPE), 'page/2': ('converted', OUTPUT_PAGE_TYPE), 'document/metadata': ('removed', None)})
        d = pdfium.PdfDocument(io.BytesIO(out))
        self.assertEqual(len(d), 2)
        self.assertEqual(d[0].get_textpage().count_chars(), 0)
        self.assertEqual(d.get_metadata_dict(skip_empty=True), {})
        d.close()
        done = items[-1]
        self.assertEqual((done['kind'], done['operations'], done['parts'], done['bytes']), ('write_complete', 2, 3, len(out)))
        task = FakeTask()
        extract(pdfium.PdfDocument(io.BytesIO(out)), task, schemas())
        self.assertEqual([i for i in task.items if i['kind'] == 'text_unit'], [])  # nothing to find by construction

    def test_a_text_free_plan_still_rasterises_every_page(self):
        out, items = run_write(DOC, [])
        self.assertEqual({v[0] for v in fates(items).values()}, {'converted', 'removed'})
        self.assertEqual(len(pdfium.PdfDocument(io.BytesIO(out))), 2)

    def test_operations_that_cannot_be_done_say_why(self):
        ops = [text_op(1, ordinal=9), text_op(2, end=99), text_op(3, part='page/7'), text_op(4, part='nonsense'), text_op(5, start=0, end=1),
               Op('op_6', 'part', 'page/1/images'), Op('op_7', 'part', 'page/2'), Op('op_8', 'part', 'ghost'), Op('op_9', 'part', 'document/outline')]
        _, items = run_write(DOC, ops)
        self.assertEqual(results(items), {
            'op_1': ('not_executed', 'UNIT_NOT_FOUND'), 'op_2': ('not_executed', 'RANGE_INVALID'), 'op_3': ('not_executed', 'PART_NOT_TEXT'),
            'op_4': ('not_executed', 'PART_NOT_TEXT'), 'op_5': ('executed', None), 'op_6': ('not_executed', 'PART_OPERATION_UNSUPPORTED'),
            'op_7': ('not_executed', 'PART_OPERATION_UNSUPPORTED'), 'op_8': ('not_executed', 'PART_UNKNOWN'), 'op_9': ('not_executed', 'PART_UNKNOWN')})

    def test_a_range_made_only_of_generated_characters_cannot_be_located(self):
        from types import SimpleNamespace as C
        text = 'ab\r\ncd'
        # PDFium reports the generated line break with an empty box
        chars = [C(box=(0, 0, 1, 5)), C(box=(1, 0, 2, 5)), C(box=(2, 0, 2, 0)), C(box=(2, 0, 2, 0)), C(box=(3, 0, 4, 5)), C(box=(4, 0, 5, 5))]
        self.assertEqual(op_boxes(text, chars, 1, 2, 4), (None, 'UNRELIABLE_LOCATION'))
        boxes, reason = op_boxes(text, chars, 1, 1, 5)
        self.assertEqual((reason, len(boxes)), (None, 2))  # b and c: the empty boxes of the line break are dropped

    def test_a_page_the_rasteriser_refuses_fails_every_operation_with_its_reason(self):
        out, items = run_write(image_pdf('Call 13812345678 today'), [text_op(1)])
        self.assertEqual(results(items), {'op_1': ('not_executed', 'PDF_NON_TEXT_COVERAGE_POLICY_REQUIRED')})
        self.assertEqual({v[0] for v in fates(items).values()}, {'kept'})
        self.assertEqual(items[-1]['kind'], 'write_complete')

    def test_a_damaged_input_is_a_failed_write_and_cancellation_leaves_no_completion(self):
        with self.assertRaises(TaskError) as ctx:
            run_write(b'%PDF-1.4 nonsense', [])
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')
        from tacet_plugin_sdk.runtime import Cancelled
        task = FakeTask(cancel_after=0)
        with self.assertRaises(Cancelled):
            run_write(DOC, [text_op(1)], task)
        self.assertFalse([i for i in task.items if i['kind'] == 'write_complete'])

    def test_chunk_ordinals_are_offsets_inside_the_pages_text(self):
        from types import SimpleNamespace as C
        text = 'aaaa\nbbbb\ncccc\n'
        chars = [C(box=(i, 0, i + 1, 5)) for i in range(len(text))]
        old = extract_module.CHUNK
        extract_module.CHUNK = 10
        try:
            self.assertEqual(op_boxes(text, chars, 1, 0, 2)[0][0][0], 0)
            boxes, reason = op_boxes(text, chars, 2, 0, 2)  # second chunk starts at character 10
            self.assertEqual((reason, boxes[0][0], boxes[1][0]), (None, 10, 11))
            self.assertEqual(op_boxes(text, chars, 2, 0, 6)[1], 'RANGE_INVALID')
            self.assertEqual(op_boxes(text, chars, 3, 0, 1)[1], 'UNIT_NOT_FOUND')
            self.assertEqual(op_boxes(text, chars, 0, 0, 1)[1], 'UNIT_NOT_FOUND')
        finally:
            extract_module.CHUNK = old


class ValidateTest(unittest.TestCase):
    def setUp(self):
        self.ops = [text_op(1), Op('op_m', 'part', 'document/metadata')]
        self.out, _ = run_write(DOC, self.ops)

    def test_a_correct_candidate_passes_every_check(self):
        items = run_validate(DOC, self.out, self.ops)
        self.assertEqual(checks(items), {'op_1': (True, False), 'op_m': (True, False)})
        self.assertEqual([i['part'] for i in items if i['kind'] == 'input_part'], ['page/1', 'page/2', 'document/metadata'])
        outs = [i for i in items if i['kind'] == 'output_part']
        self.assertEqual([(o['part'], o['source'], o['content_type']) for o in outs], [('page/1', 'page/1', OUTPUT_PAGE_TYPE), ('page/2', 'page/2', OUTPUT_PAGE_TYPE)])
        done = items[-1]
        self.assertEqual((done['reopened'], done['input_parts'], done['output_parts'], done['operations']), (True, 3, 2, 2))
        self.assertEqual([i['unit'] for i in items if i['kind'] == 'op_check'], ['page/1#1', 'document/metadata'])

    def test_content_that_is_still_visible_is_a_residual(self):
        items = run_validate(DOC, DOC, self.ops)  # the untouched input as the candidate
        self.assertEqual(checks(items), {'op_1': (False, True), 'op_m': (False, True)})

    def test_painting_only_part_of_the_range_is_not_enough(self):
        partial, _ = run_write(DOC, [text_op(1, start=5, end=9)])
        self.assertEqual(checks(run_validate(DOC, partial, [text_op(1)]))['op_1'], (False, True))

    def test_painting_somewhere_else_does_not_count(self):
        elsewhere, _ = run_write(DOC, [text_op(1, start=0, end=4)])
        self.assertEqual(checks(run_validate(DOC, elsewhere, [text_op(1)]))['op_1'], (False, True))

    def test_operations_on_another_page_are_checked_on_that_page(self):
        second = [text_op(1, part='page/2', start=0, end=6)]
        cand, _ = run_write(DOC, second)
        self.assertEqual(checks(run_validate(DOC, cand, second)), {'op_1': (True, False)})
        self.assertEqual(checks(run_validate(DOC, self.out, second)), {'op_1': (False, True)})

    def test_what_cannot_be_checked_is_unaccounted_not_a_pass(self):
        ops = [text_op(1, part='page/9'), text_op(2, ordinal=4), Op('op_3', 'part', 'page/1/images'), text_op(4, end=99)]
        self.assertEqual(checks(run_validate(DOC, self.out, ops)), {f'op_{i}': (False, False) for i in (1, 2, 3, 4)})

    def test_a_candidate_with_a_smaller_page_cannot_pass_by_having_its_missing_pixels_read_as_black(self):
        small = DOC.replace(b'/MediaBox [0 0 300 200]', b'/MediaBox [0 0 100 60 ]')
        self.assertNotEqual(small, DOC)
        self.assertEqual(checks(run_validate(DOC, small, [text_op(1)]))['op_1'], (False, True))

    def test_an_operation_on_a_page_the_candidate_does_not_have_is_unaccounted(self):
        one_page, _ = run_write(build([TEXT]), [])
        self.assertEqual(checks(run_validate(DOC, one_page, [text_op(1, part='page/2', start=0, end=6)])), {'op_1': (False, False)})

    def test_a_candidate_with_more_pages_has_an_output_part_without_an_input_part(self):
        longer, _ = run_write(build([TEXT, 'second page', 'third']), [])
        items = run_validate(DOC, longer, [])
        self.assertEqual([i['part'] for i in items if i['kind'] == 'output_part'], ['page/1', 'page/2', 'page/3'])
        self.assertEqual(items[-1]['input_parts'], 3)

    def test_a_candidate_that_will_not_open_says_so(self):
        for junk in (b'', b'%PDF-1.4 nonsense'):
            items = run_validate(DOC, junk, self.ops)
            self.assertEqual(items[-1], {'kind': 'validate_complete', 'complete': True, 'input_parts': 3, 'output_parts': 0, 'operations': 0, 'reopened': False})

    def test_a_damaged_input_is_a_failed_validation(self):
        with self.assertRaises(TaskError) as ctx:
            run_validate(b'%PDF nonsense', self.out, [])
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')


class PartsTest(unittest.TestCase):
    def test_the_document_level_parts_appear_only_when_the_input_has_them(self):
        from tacet_format_pdf.parts import input_parts, page_index
        outline = build(['x'], catalog_extra='/Outlines 70 0 R', extra={70: b'<< /Type /Outlines /First 71 0 R /Last 71 0 R /Count 1 >>',
                                                                        71: b'<< /Title (Chapter) /Parent 70 0 R /Dest [10 0 R /Fit] >>'})
        for raw, want in ((build(['x']), ['page/1']), (outline, ['page/1', 'document/outline']),
                          (build(['x', 'y'], info={'Title': 'T'}), ['page/1', 'page/2', 'document/metadata'])):
            d = pdfium.PdfDocument(io.BytesIO(raw))
            self.assertEqual([n for n, _ in input_parts(d)], want)
            d.close()
        self.assertEqual([page_index(p) for p in ('page/1', 'page/12', 'page/0', 'page/01', 'page/', 'page/x', 'page/12345678', 'pages/1', 'page/1/images', 'document/metadata')],
                         [0, 11, None, None, None, None, None, None, None, None])


if __name__ == '__main__':
    unittest.main()
