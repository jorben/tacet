import io
import unittest

from tacet_format_text import adapter as module
from tacet_format_text.adapter import PIECE, PIECES, detect_encoding, open_adapter
from tacet_format_text.scanner import Limits
from tacet_plugin_sdk import unitkit
from tacet_plugin_sdk.plan import Op
from tacet_plugin_sdk.runtime import TaskError
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


def check(raw, cand, ops):
    task = FakeTask()
    unitkit.validate(open_adapter(io.BytesIO(raw)), io.BytesIO(raw), open_adapter, io.BytesIO(cand), ops, task, schemas())
    return task.items


def checks(items):
    return {i['op']: (i['executed'], i['residual']) for i in items if i['kind'] == 'op_check'}


class UnitsTest(unittest.TestCase):
    def test_lines_are_numbered_by_line_with_every_terminator_kind_and_empty_lines_counting(self):
        raw = f'a {NUM}\r\n\nb\rc\nlast'.encode()
        self.assertEqual(units(raw), [(64, f'a {NUM}'), (128, ''), (192, 'b'), (256, 'c'), (320, 'last')])

    def test_a_trailing_terminator_adds_no_line_and_an_empty_file_has_none(self):
        self.assertEqual(units(b'a\n'), [(64, 'a')])
        self.assertEqual(units(b'a\r\n'), [(64, 'a')])
        self.assertEqual(units(b'\n'), [(64, '')])
        self.assertEqual(units(b''), [])

    def test_a_crlf_split_across_two_chunks_is_one_terminator(self):
        text = 'x' * 20 + '\r\n' + 'y'
        old = module.scan
        module.scan = lambda s, e: old(s, e, Limits(chunk_chars=21))  # the chunk ends between \r and \n
        try:
            self.assertEqual(units(text.encode()), [(64, 'x' * 20), (128, 'y')])
        finally:
            module.scan = old

    def test_lines_spanning_many_chunks_keep_exact_offsets(self):
        raw = ('é' * 30000 + NUM + '\n' + 'tail').encode()
        old = module.scan
        module.scan = lambda s, e: old(s, e, Limits(chunk_chars=1000))
        try:
            a = open_adapter(io.BytesIO(raw))
            us = list(a.units(io.BytesIO(raw)))
        finally:
            module.scan = old
        self.assertEqual(us[-1].text, 'tail')
        self.assertTrue(all(len(u.offs) == len(u.text) + 1 for u in us))
        u = us[1]  # second piece of the long line
        self.assertEqual(raw[u.offs[0]:u.offs[-1]].decode(), u.text)

    def test_a_long_line_is_cut_in_pieces_that_keep_their_own_ordinals_and_too_long_a_line_fails(self):
        raw = ('a' * (PIECE + 5) + '\nb').encode()
        self.assertEqual([(o, len(t)) for o, t in units(raw)], [(64, PIECE), (65, 5), (128, 1)])
        with self.assertRaises(TaskError) as ctx:
            list(unitkit._guard(open_adapter(io.BytesIO(b'x')).units(io.BytesIO(('a' * (PIECE * PIECES + 1)).encode()))))
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')


class EncodingTest(unittest.TestCase):
    def test_detection(self):
        for name, raw, want in (('ascii', b'hello', 'utf-8'), ('utf-8', '你好'.encode(), 'utf-8'), ('bom', b'\xef\xbb\xbf' + '你好'.encode(), 'utf-8'),
                                ('gb18030', '你好，世界'.encode('gb18030'), 'gb18030'), ('utf-16-le', b'\xff\xfe' + '你好'.encode('utf-16-le'), 'utf-16-le'),
                                ('utf-16-be', b'\xfe\xff' + '你好'.encode('utf-16-be'), 'utf-16-be')):
            with self.subTest(name):
                self.assertEqual(detect_encoding(io.BytesIO(raw)), want)

    def test_what_is_not_text_is_not_claimed(self):
        for name, raw in (('nul bytes', b'ab\x00cd'), ('utf-32', b'\xff\xfe\x00\x00a\x00\x00\x00'), ('binary', bytes(range(256)) * 4), ('invalid both', b'\xff\xff\xff\xfe\x80\x81\x82')):
            with self.subTest(name):
                self.assertIsNone(open_adapter(io.BytesIO(raw)))

    def test_probe_names_the_format_or_says_unknown(self):
        self.assertEqual(unitkit.probe(io.BytesIO(b'hello'), open_adapter)['format'], 'txt')
        r = unitkit.probe(io.BytesIO(b'a\x00b'), open_adapter)
        self.assertEqual((r['format'], r['encryption'], r['bytes']), ('unknown', 'none', 3))


class WriteTest(unittest.TestCase):
    def test_bytes_outside_the_ranges_are_untouched_whatever_the_terminators_and_encoding(self):
        for name, raw, enc in (('utf-8 crlf', f'一 {NUM} 二\r\n三\n四\r五'.encode(), 'utf-8'), ('bom', b'\xef\xbb\xbf' + f'一 {NUM} 二\r\n三'.encode(), 'utf-8'),
                               ('gb18030', f'一 {NUM} 二\r\n三'.encode('gb18030'), 'gb18030'), ('utf-16', b'\xff\xfe' + f'一 {NUM} 二\r\n三'.encode('utf-16-le'), 'utf-16-le')):
            with self.subTest(name):
                codec = module.ENCODINGS[enc][0]
                out, items = write(raw, [op(1, 64, 2, 13)])
                self.assertEqual(res(items), {'op_1': ('executed', None)})
                want = raw.replace(NUM.encode(codec), '[号]'.encode(codec), 1)
                self.assertEqual(out, want)

    def test_several_operations_in_one_line_and_in_different_lines(self):
        raw = f'{NUM} 和 {NUM}\nzz {NUM}'.encode()
        out, items = write(raw, [op(1, 64, 0, 11, '[A]'), op(2, 64, 14, 25, '[B]'), op(3, 128, 3, 14, '')])
        self.assertEqual({k: v[0] for k, v in res(items).items()}, {'op_1': 'executed', 'op_2': 'executed', 'op_3': 'executed'})
        self.assertEqual(out.decode(), '[A] 和 [B]\nzz ')

    def test_ordinals_of_other_lines_do_not_move_when_a_line_changes_length(self):
        raw = f'{NUM}\nsecond {NUM}'.encode()
        out, _ = write(raw, [op(1, 64, 0, 11, '十一个字符的替换文本')])
        self.assertEqual([o for o, _ in units(out)], [64, 128])

    def test_operations_that_cannot_be_done_say_why(self):
        raw = f'abc {NUM}'.encode()
        ops = [op(1, 999, 0, 1), op(2, 64, 0, 99), op(3, 64, 5, 5), Op('op_4', 'text', 'nope', 64, 0, 1, 'x'), Op('op_5', 'part', 'content'), Op('op_6', 'part', 'ghost'),
               op(7, 64, 0, 3), op(8, 64, 2, 5)]
        out, items = write(raw, ops)
        self.assertEqual(res(items), {'op_1': ('not_executed', 'UNIT_NOT_FOUND'), 'op_2': ('not_executed', 'RANGE_INVALID'), 'op_3': ('not_executed', 'RANGE_INVALID'),
                                      'op_4': ('not_executed', 'PART_NOT_TEXT'), 'op_5': ('not_executed', 'PART_OPERATION_UNSUPPORTED'), 'op_6': ('not_executed', 'PART_UNKNOWN'),
                                      'op_7': ('executed', None), 'op_8': ('not_executed', 'RANGE_INVALID')})  # op_8 overlaps op_7

    def test_a_replacement_the_encoding_cannot_hold_fails_that_operation_not_the_file(self):
        raw = f'caf\xe9 {NUM}'.encode('cp1252')
        a, sink, task = module.TextAdapter('cp1252'), io.BytesIO(), FakeTask()
        unitkit.write(a, io.BytesIO(raw), [op(1, 64, 5, 16, '号')], sink, task, schemas())
        self.assertEqual(res(task.items), {'op_1': ('not_executed', 'UNENCODABLE_TEXT')})
        self.assertEqual(sink.getvalue(), raw)
        # gb18030 holds any code point
        gb = f'联系人张三的电话是 {NUM}'.encode('gb18030')
        out, items = write(gb, [op(1, 64, 10, 21, '😀')])
        self.assertEqual(res(items), {'op_1': ('executed', None)})
        self.assertEqual(out.decode('gb18030'), '联系人张三的电话是 😀')

    def test_a_very_short_gb18030_text_that_is_also_valid_utf8_is_read_as_utf8(self):
        # inherent ambiguity of automatic detection (documented limitation): the bytes d2 bb are both 一 and U+04BB
        self.assertEqual(detect_encoding(io.BytesIO('一'.encode('gb18030'))), 'utf-8')

    def test_fates_and_the_completion_marker_count_what_was_done(self):
        raw = f'x {NUM}'.encode()
        out, items = write(raw, [op(1, 64, 2, 13)])
        self.assertEqual([i for i in items if i['kind'] == 'part_fate'], [{'kind': 'part_fate', 'part': 'content', 'fate': 'redacted'}])
        self.assertEqual(items[-1], {'kind': 'write_complete', 'complete': True, 'operations': 1, 'parts': 1, 'bytes': len(out)})
        out, items = write(raw, [])
        self.assertEqual((out, [i['fate'] for i in items if i['kind'] == 'part_fate']), (raw, ['kept']))
        out, items = write(raw, [op(1, 999, 0, 1)])
        self.assertEqual([i['fate'] for i in items if i['kind'] == 'part_fate'], ['kept'])
        self.assertEqual(items[-1]['bytes'], len(out))

    def test_an_empty_candidate_still_reports_a_byte(self):
        _, items = write(b'', [])
        self.assertEqual(items[-1]['bytes'], 1)


class ValidateTest(unittest.TestCase):
    RAW = f'一 {NUM} 二\nzz {NUM}'.encode()

    def test_a_correct_candidate_passes_and_both_part_lists_are_given(self):
        ops = [op(1, 64, 2, 13), op(2, 128, 3, 14)]
        cand, _ = write(self.RAW, ops)
        items = check(self.RAW, cand, ops)
        self.assertEqual(checks(items), {'op_1': (True, False), 'op_2': (True, False)})
        self.assertEqual([(i['part'], i['source']) for i in items if i['kind'] == 'output_part'], [('content', 'content')])
        self.assertEqual(items[-1], {'kind': 'validate_complete', 'complete': True, 'input_parts': 1, 'output_parts': 1, 'operations': 2, 'reopened': True})

    def test_what_was_not_done_is_a_residual_and_what_was_done_still_counts(self):
        ops = [op(1, 64, 2, 13), op(2, 128, 3, 14)]
        cand, _ = write(self.RAW, [ops[0]])
        self.assertEqual(checks(check(self.RAW, cand, ops)), {'op_1': (True, False), 'op_2': (False, True)})

    def test_a_removed_line_is_unaccounted_not_executed(self):
        self.assertEqual(checks(check(self.RAW, '一 x'.encode(), [op(1, 128, 3, 14)])), {'op_1': (False, False)})

    def test_a_candidate_that_will_not_read_as_the_same_kind_of_file_says_so(self):
        for junk in (b'a\x00b', b'\xff\xfe\xfd\xfc\xfb', bytes(range(256))):
            items = check(self.RAW, junk, [op(1, 64, 2, 13)])
            self.assertEqual(items[-1], {'kind': 'validate_complete', 'complete': True, 'input_parts': 1, 'output_parts': 0, 'operations': 0, 'reopened': False})

    def test_a_damaged_input_is_a_failed_validation(self):
        class Fake(module.TextAdapter):
            pass
        bad = io.BytesIO(b'\xff\xff\xff')
        with self.assertRaises(TaskError) as ctx:
            unitkit.validate(module.TextAdapter('utf-8'), bad, open_adapter, io.BytesIO(b'x'), [op(1, 64, 0, 1)], FakeTask(), schemas())
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')


class ExtractTest(unittest.TestCase):
    def test_units_parts_and_the_completion_marker(self):
        raw = f'a {NUM}\n\nb'.encode()
        task = FakeTask()
        unitkit.extract(open_adapter(io.BytesIO(raw)), io.BytesIO(raw), task, schemas())
        us = [i for i in task.items if i['kind'] == 'text_unit']
        self.assertEqual([(u['part'], u['ordinal'], u['text']) for u in us], [('content', 64, f'a {NUM}'), ('content', 192, 'b')])
        self.assertEqual(len({u['unit_id'] for u in us}), 2)
        self.assertEqual(us[0]['source_map'], {'kind': 'paragraph_ordinal', 'part': 'content', 'ordinal': 64})
        self.assertEqual(task.items[-2:], [{'kind': 'part_state', 'part': 'content', 'class': 'text', 'state': 'inspected'},
                                          {'kind': 'extract_complete', 'complete': True, 'units': 2, 'parts': 1}])

    def test_a_unit_above_the_protocol_limit_fails_the_extraction(self):
        old = unitkit.MAX_UNIT_CHARS
        unitkit.MAX_UNIT_CHARS = 3
        try:
            with self.assertRaises(TaskError) as ctx:
                unitkit.extract(open_adapter(io.BytesIO(b'abcd')), io.BytesIO(b'abcd'), FakeTask(), schemas())
            self.assertEqual(ctx.exception.code, 'RESOURCE_LIMIT')
        finally:
            unitkit.MAX_UNIT_CHARS = old

    def test_cancellation_leaves_no_completion_marker(self):
        from tacet_plugin_sdk.runtime import Cancelled
        task = FakeTask(cancel_after=1)
        with self.assertRaises(Cancelled):
            unitkit.extract(open_adapter(io.BytesIO(b'a\nb\nc')), io.BytesIO(b'a\nb\nc'), task, schemas())
        self.assertFalse([i for i in task.items if i['kind'] == 'extract_complete'])


if __name__ == '__main__':
    unittest.main()
