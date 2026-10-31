import io
import unittest

from tacet_plugin_sdk import unitkit
from tacet_plugin_sdk.byteedit import Edit, FormatError
from tacet_plugin_sdk.plan import Op
from tacet_plugin_sdk.schema import Schemas
from tacet_plugin_sdk.unitkit import Adapter, Unit
from test_docx_extract import FakeTask


class Fake(Adapter):
    """Two parts: ``body`` (editable) and ``meta`` (read-only). Units are the lines of the file, 4 bytes wide."""
    format = 'fake'
    content_type = 'x/fake'
    parts = (('body', 'text'), ('meta', 'text'))
    editable = frozenset({'body'})

    def __init__(self, bad_edit=False, overlap=False):
        self.bad_edit, self.overlap = bad_edit, overlap

    def units(self, source):
        data = source.read().decode()
        if data.startswith('!'):
            raise FormatError('bad_input', 0)
        for i, line in enumerate(data.split('|')):
            part = 'body' if i % 2 == 0 else 'meta'
            yield Unit(part, i // 2 + 1, line, [i * 6 + k for k in range(len(line) + 1)])

    def edits(self, unit, ops):
        if self.bad_edit:
            raise FormatError('cannot_edit', 0)
        out = [Edit(unit.offs[o.start], unit.offs[o.end], o.replacement.encode()) for o in ops]
        return out + out if self.overlap else out


def opener(source):
    head = source.read(1)
    source.seek(0)
    return Fake() if head != b'?' else None


def schemas():
    s = Schemas()
    for d in ('WriteItem', 'ValidateItem', 'ExtractItem'):
        s.compile('format.schema.json', d)
    return s


def write(a, raw, ops):
    sink, task = io.BytesIO(), FakeTask()
    unitkit.write(a, io.BytesIO(raw), ops, sink, task, schemas())
    return sink.getvalue(), task.items


def res(items):
    return {i['op']: (i['status'], i.get('reason')) for i in items if i['kind'] == 'op_result'}


RAW = b'abcdef|meta__|ghijkl'


class KitTest(unittest.TestCase):
    def test_a_read_only_part_is_never_edited_and_says_so(self):
        out, items = write(Fake(), RAW, [Op('o1', 'text', 'meta', 1, 0, 2, 'X'), Op('o2', 'text', 'body', 1, 0, 2, 'X')])
        self.assertEqual(res(items), {'o1': ('not_executed', 'PART_OPERATION_UNSUPPORTED'), 'o2': ('executed', None)})
        self.assertEqual(out, b'Xcdef|meta__|ghijkl')

    def test_a_failing_edit_fails_its_operations_with_the_code_and_the_rest_still_runs(self):
        out, items = write(Fake(bad_edit=True), RAW, [Op('o1', 'text', 'body', 1, 0, 2, 'X')])
        self.assertEqual(res(items), {'o1': ('not_executed', 'CANNOT_EDIT')})
        self.assertEqual(out, RAW)

    def test_a_rewrite_that_fails_marks_every_operation_not_executed_and_every_part_kept(self):
        out, items = write(Fake(overlap=True), RAW, [Op('o1', 'text', 'body', 1, 0, 2, 'X'), Op('o2', 'text', 'body', 2, 0, 2, 'Y')])
        self.assertEqual(res(items), {'o1': ('not_executed', 'OVERLAPPING_EDITS'), 'o2': ('not_executed', 'OVERLAPPING_EDITS')})
        self.assertEqual({i['fate'] for i in items if i['kind'] == 'part_fate'}, {'kept'})
        self.assertEqual(items[-1]['kind'], 'write_complete')

    def test_every_part_of_the_adapter_is_reported_in_each_stage(self):
        out, items = write(Fake(), RAW, [])
        self.assertEqual([i['part'] for i in items if i['kind'] == 'part_fate'], ['body', 'meta'])
        self.assertEqual(items[-1]['parts'], 2)
        task = FakeTask()
        unitkit.extract(Fake(), io.BytesIO(RAW), task, schemas())
        self.assertEqual(task.items[-1], {'kind': 'extract_complete', 'complete': True, 'units': 3, 'parts': 2})
        self.assertEqual([i['part'] for i in task.items if i['kind'] == 'part_state'], ['body', 'meta'])

    def test_a_candidate_the_opener_reads_as_another_format_is_not_reopened(self):
        class Other(Fake):
            format = 'other'
        task = FakeTask()
        unitkit.validate(Fake(), io.BytesIO(RAW), lambda s: Other(), io.BytesIO(RAW), [Op('o1', 'text', 'body', 1, 0, 2, 'X')], task, schemas())
        self.assertFalse(task.items[-1]['reopened'])
        self.assertEqual(task.items[-1]['output_parts'], 0)

    def test_a_part_only_the_candidate_has_is_listed_without_a_source(self):
        class Grown(Fake):
            def enumerate(self, source):
                return super().enumerate(source) + [('extra', 'x/fake', 'other', 'inspected')]
        task = FakeTask()
        unitkit.validate(Fake(), io.BytesIO(RAW), lambda s: Grown(), io.BytesIO(RAW), [], task, schemas())
        outs = {i['part']: i.get('source') for i in task.items if i['kind'] == 'output_part'}
        self.assertEqual(outs, {'body': 'body', 'meta': 'meta', 'extra': None})
        self.assertEqual(task.items[-1]['output_parts'], 3)

    def test_a_removable_part_that_is_gone_is_executed_and_one_that_is_present_is_a_residual(self):
        class Rm(Fake):
            removable = frozenset({'meta'})
            def __init__(self, drop=False):
                super().__init__()
                self.drop = drop
            def enumerate(self, source):
                parts = super().enumerate(source)
                return [p for p in parts if not (self.drop and p[0] == 'meta')]
        op = Op('o1', 'part', 'meta')
        for drop, want in ((True, (True, False)), (False, (False, True))):
            task = FakeTask()
            unitkit.validate(Rm(), io.BytesIO(RAW), lambda s, d=drop: Rm(d), io.BytesIO(RAW), [op], task, schemas())
            self.assertEqual([(i['executed'], i['residual']) for i in task.items if i['kind'] == 'op_check'], [want])

    def test_a_candidate_that_will_not_parse_is_not_reopened(self):
        task = FakeTask()
        unitkit.validate(Fake(), io.BytesIO(RAW), opener, io.BytesIO(b'!broken'), [Op('o1', 'text', 'body', 1, 0, 2, 'X')], task, schemas())
        self.assertFalse(task.items[-1]['reopened'])

    def test_part_operations_are_checked_as_unaccounted(self):
        task = FakeTask()
        unitkit.validate(Fake(), io.BytesIO(RAW), opener, io.BytesIO(RAW), [Op('o1', 'part', 'meta')], task, schemas())
        self.assertEqual([(i['executed'], i['residual'], i['unit']) for i in task.items if i['kind'] == 'op_check'], [(False, False, 'meta')])

    def test_probe_reports_the_adapters_format_or_unknown_and_the_size(self):
        self.assertEqual(unitkit.probe(io.BytesIO(RAW), opener)['format'], 'fake')
        r = unitkit.probe(io.BytesIO(b'?xx'), opener)
        self.assertEqual((r['format'], r['bytes']), ('unknown', 3))

    def test_apply_ops_replaces_right_to_left(self):
        ops = [Op('a', 'text', 'p', 1, 0, 2, '[1]'), Op('b', 'text', 'p', 1, 4, 6, '')]
        self.assertEqual(unitkit.apply_ops('abcdef', ops), '[1]cd')


if __name__ == '__main__':
    unittest.main()
