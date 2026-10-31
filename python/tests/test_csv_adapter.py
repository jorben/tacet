import io
import unittest

from tacet_format_csv.adapter import CsvAdapter, detect_dialect, open_adapter
from tacet_format_csv.scanner import Dialect
from tacet_plugin_sdk import unitkit
from tacet_plugin_sdk.plan import Op
from tacet_plugin_sdk.schema import Schemas
from test_docx_extract import FakeTask

NUM = '13812345678'


def schemas():
    s = Schemas()
    for d in ('ExtractItem', 'WriteItem', 'ValidateItem'):
        s.compile('format.schema.json', d)
    return s


def units(raw):
    a = open_adapter(io.BytesIO(raw))
    return [(u.ordinal, u.text) for u in a.units(io.BytesIO(raw))]


def op(i, ordinal, start, end, repl='[号]'):
    return Op(f'op_{i}', 'text', 'content', ordinal, start, end, repl)


def write(raw, ops):
    a, sink, task = open_adapter(io.BytesIO(raw)), io.BytesIO(), FakeTask()
    unitkit.write(a, io.BytesIO(raw), ops, sink, task, schemas())
    return sink.getvalue(), task.items


def res(items):
    return {i['op']: (i['status'], i.get('reason')) for i in items if i['kind'] == 'op_result'}


def checks(raw, cand, ops):
    task = FakeTask()
    unitkit.validate(open_adapter(io.BytesIO(raw)), io.BytesIO(raw), open_adapter, io.BytesIO(cand), ops, task, schemas())
    return {i['op']: (i['executed'], i['residual']) for i in task.items if i['kind'] == 'op_check'}, task.items


class DialectTest(unittest.TestCase):
    def test_delimiter_and_encoding_detection(self):
        for name, raw, delim, enc in (('comma', b'a,b,c\n1,2,3\n', ',', 'utf-8'), ('semicolon', b'a;b;c\n1;2;3\n', ';', 'utf-8'), ('tab', b'a\tb\tc\n', '\t', 'utf-8'),
                                      ('pipe', b'a|b|c\n', '|', 'utf-8'), ('single column', b'a\nb\n', ',', 'utf-8'), ('commas only inside quotes', b'"a,b,c,d,e";x\n', ';', 'utf-8'), ('tie goes to the comma', b'a,b;c\n', ',', 'utf-8'), ('quoted delimiter', b'"a,b,c;d";x;y\n1;2;3\n', ';', 'utf-8'),
                                      ('gb18030', '姓名,电话\n张三丰,1\n'.encode('gb18030'), ',', 'gb18030'), ('bom', b'\xef\xbb\xbfa,b\n', ',', 'utf-8')):
            with self.subTest(name):
                d = detect_dialect(io.BytesIO(raw))
                self.assertEqual((d.delimiter, d.encoding), (delim, enc))

    def test_what_is_not_a_csv_is_not_claimed(self):
        for name, raw in (('nul', b'a,b\x00\n'), ('empty', b''), ('unterminated quote', b'a,"b\n1,2\n'), ('garbage', bytes(range(128, 256)) * 3)):
            with self.subTest(name):
                self.assertIsNone(open_adapter(io.BytesIO(raw)))


class UnitsTest(unittest.TestCase):
    def test_cells_are_numbered_by_the_running_count_with_empty_cells_and_quotes_decoded(self):
        raw = f'name,phone\r\n"Li, Wei",{NUM}\n,"say ""hi"""\n'.encode()
        self.assertEqual(units(raw), [(1, 'name'), (2, 'phone'), (3, 'Li, Wei'), (4, NUM), (5, ''), (6, 'say "hi"')])

    def test_a_multi_line_quoted_cell_is_one_unit(self):
        self.assertEqual(units(b'a,"line1\nline2"\nb,c\n')[1], (2, 'line1\nline2'))


class WriteTest(unittest.TestCase):
    RAW = f'name,phone,note\n张三,{NUM},"call {NUM} now"\nx,y,z\n'.encode()

    def test_only_the_edited_cells_change_and_their_quoting_is_kept(self):
        out, items = write(self.RAW, [op(1, 5, 0, 11), op(2, 6, 5, 16)])
        self.assertEqual(res(items), {'op_1': ('executed', None), 'op_2': ('executed', None)})
        self.assertEqual(out.decode(), 'name,phone,note\n张三,[号],"call [号] now"\nx,y,z\n')

    def test_a_replacement_that_needs_quotes_gets_them(self):
        out, _ = write(self.RAW, [op(1, 5, 0, 11, 'a,b')])
        self.assertEqual(out.decode().splitlines()[1], '张三,"a,b","call 13812345678 now"')

    def test_a_cell_with_several_operations_gets_them_all(self):
        raw = f'k,{NUM} and {NUM}\n'.encode()
        out, items = write(raw, [op(1, 2, 0, 11, '[A]'), op(2, 2, 16, 27, '[B]')])
        self.assertEqual(out.decode(), 'k,[A] and [B]\n')

    def test_crlf_and_the_encoding_survive(self):
        raw = f'姓名,电话\r\n张三丰,{NUM}\r\n'.encode('gb18030')
        out, _ = write(raw, [op(1, 4, 0, 11)])
        self.assertEqual(out, raw.replace(NUM.encode(), '[号]'.encode('gb18030')))

    def test_a_value_that_would_become_a_formula_is_protected_and_validation_notices(self):
        raw = b'a,x=1+2\n'
        out, items = write(raw, [op(1, 2, 0, 1, '')])  # deleting the x leaves "=1+2"
        self.assertEqual(out, b"a,'=1+2\n")
        got, _ = checks(raw, out, [op(1, 2, 0, 1, '')])
        self.assertEqual(got['op_1'], (False, False))  # not what the plan said: never reported as executed

    def test_operations_that_cannot_be_done_say_why(self):
        out, items = write(self.RAW, [op(1, 99, 0, 1), op(2, 1, 0, 99)])
        self.assertEqual(res(items), {'op_1': ('not_executed', 'UNIT_NOT_FOUND'), 'op_2': ('not_executed', 'RANGE_INVALID')})
        self.assertEqual(out, self.RAW)

    def test_a_replacement_the_encoding_cannot_hold_fails_that_operation(self):
        a = CsvAdapter(Dialect(',', 'cp1252'))
        raw = f'a,{NUM}\n'.encode()
        sink, task = io.BytesIO(), FakeTask()
        unitkit.write(a, io.BytesIO(raw), [op(1, 2, 0, 11, '号')], sink, task, schemas())
        self.assertEqual(res(task.items), {'op_1': ('not_executed', 'UNENCODABLE_TEXT')})
        self.assertEqual(sink.getvalue(), raw)


class ValidateTest(unittest.TestCase):
    RAW = WriteTest.RAW

    def test_a_correct_candidate_passes_and_a_missing_edit_is_a_residual(self):
        ops = [op(1, 5, 0, 11), op(2, 6, 5, 16)]
        cand, _ = write(self.RAW, ops)
        self.assertEqual(checks(self.RAW, cand, ops)[0], {'op_1': (True, False), 'op_2': (True, False)})
        half, _ = write(self.RAW, [ops[0]])
        self.assertEqual(checks(self.RAW, half, ops)[0], {'op_1': (True, False), 'op_2': (False, True)})

    def test_a_candidate_with_another_cell_structure_does_not_pass(self):
        ops = [op(1, 5, 0, 11)]
        broken = self.RAW.replace(b'x,y,z', b'x,y')  # a cell fewer: later ordinals no longer line up
        got, _ = checks(self.RAW, broken, ops)
        self.assertEqual(got['op_1'], (False, True))  # the number is still there
        gone = b'name,phone,note\nonly\n'
        self.assertEqual(checks(self.RAW, gone, ops)[0]['op_1'], (False, False))

    def test_a_candidate_that_will_not_parse_is_not_reopened(self):
        _, items = checks(self.RAW, b'a,"unterminated\n', [op(1, 5, 0, 11)])
        self.assertFalse(items[-1]['reopened'])


class ExtractTest(unittest.TestCase):
    def test_cells_become_units_and_empty_ones_are_skipped(self):
        raw = b'a,,c\n'
        task = FakeTask()
        unitkit.extract(open_adapter(io.BytesIO(raw)), io.BytesIO(raw), task, schemas())
        self.assertEqual([(u['ordinal'], u['text']) for u in task.items if u['kind'] == 'text_unit'], [(1, 'a'), (3, 'c')])
        self.assertEqual(task.items[-1], {'kind': 'extract_complete', 'complete': True, 'units': 2, 'parts': 1})


if __name__ == '__main__':
    unittest.main()
