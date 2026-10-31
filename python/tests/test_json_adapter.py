import io
import json
import unittest

from tacet_format_json import adapter as module
from tacet_format_json.adapter import PIECE, PIECES, open_adapter
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
    return [(u.part, u.ordinal, u.text) for u in a.units(io.BytesIO(raw))]


def op(i, ordinal, start, end, repl='[号]', part='values'):
    return Op(f'op_{i}', 'text', part, ordinal, start, end, repl)


def write(raw, ops):
    a, sink, task = open_adapter(io.BytesIO(raw)), io.BytesIO(), FakeTask()
    unitkit.write(a, io.BytesIO(raw), ops, sink, task, schemas())
    return sink.getvalue(), task.items


def res(items):
    return {i['op']: (i['status'], i.get('reason')) for i in items if i['kind'] == 'op_result'}


def check(raw, cand, ops):
    task = FakeTask()
    unitkit.validate(open_adapter(io.BytesIO(raw)), io.BytesIO(raw), open_adapter, io.BytesIO(cand), ops, task, schemas())
    return {i['op']: (i['executed'], i['residual']) for i in task.items if i['kind'] == 'op_check'}, task.items


DOC = json.dumps({'name': '张三', 'phone': f'call {NUM} now', 'id': 110101199003071234, 'n': 42, 'list': [f'{NUM}', 'x']}, ensure_ascii=False, separators=(',', ':')).encode()


class UnitsTest(unittest.TestCase):
    def test_strings_keys_and_long_numbers_are_separate_parts_numbered_by_structure(self):
        us = units(DOC)
        self.assertEqual([u for u in us if u[0] == 'values'], [('values', 1 * PIECES, '张三'), ('values', 2 * PIECES, f'call {NUM} now'), ('values', 3 * PIECES, NUM), ('values', 4 * PIECES, 'x')])
        self.assertEqual([u[2] for u in us if u[0] == 'keys'], ['name', 'phone', 'id', 'n', 'list'])
        self.assertEqual([u for u in us if u[0] == 'numbers'], [('numbers', 1, '110101199003071234')])  # 42 is too short to be an identifier

    def test_json_lines_is_recognised_and_json_wins_when_both_parse(self):
        self.assertEqual(open_adapter(io.BytesIO(b'{"a":"x"}\n{"a":"y"}\n')).format, 'jsonl')
        self.assertEqual(open_adapter(io.BytesIO(b'{"a":"x"}')).format, 'json')
        self.assertEqual(open_adapter(io.BytesIO(b'["x"]\n')).format, 'json')
        self.assertEqual(open_adapter(io.BytesIO(b'{"a":"x"}\n{"a":"y"}\n')).content_type, 'application/x-ndjson')

    def test_what_is_not_json_is_not_claimed(self):
        for raw in (b'', b'{"a":', b'plain text', b'{"a":1,}', b'\xff\xfe', b'{"a":"x"} trailing'):
            with self.subTest(raw):
                self.assertIsNone(open_adapter(io.BytesIO(raw)))

    def test_piece_boundaries_do_not_depend_on_how_the_scanner_happened_to_buffer(self):
        text = '数据' * 20000
        sizes = []
        for pad in (0, 7, 40000):
            raw = json.dumps({'pad': 'p' * pad, 'k': text}, ensure_ascii=False).encode()
            sizes.append([len(u[2]) for u in units(raw) if u[0] == 'values' and u[1] // PIECES == 2])
        self.assertEqual(sizes[0], [PIECE, PIECE, 40000 - 2 * PIECE])
        self.assertEqual(sizes[0], sizes[1])
        self.assertEqual(sizes[0], sizes[2])

    def test_pieces_of_a_long_string_edit_at_the_right_bytes(self):
        text = '数' * 20000 + NUM
        raw = json.dumps({'k': text}, ensure_ascii=False).encode()
        out, items = write(raw, [op(1, PIECES + 1, 20000 - PIECE, 20000 - PIECE + 11)])
        self.assertEqual(res(items), {'op_1': ('executed', None)})
        self.assertEqual(json.loads(out)['k'], '数' * 20000 + '[号]')

    def test_a_string_larger_than_the_scanners_read_buffer_edits_at_the_right_bytes(self):
        text = 'é' * 100000 + NUM
        raw = json.dumps({'k': text}, ensure_ascii=False).encode()
        pieces = [u for u in units(raw) if u[0] == 'values']
        self.assertGreater(len(pieces), 6)
        last = pieces[-1]
        start = last[2].index(NUM)
        out, items = write(raw, [op(1, last[1], start, start + 11)])
        self.assertEqual(res(items), {'op_1': ('executed', None)})
        self.assertEqual(json.loads(out)['k'], 'é' * 100000 + '[号]')

    def test_a_string_the_scanner_delivers_in_many_chunks_is_joined_before_it_is_cut(self):
        from tacet_format_json.scanner import Event, STRING_CHUNK
        real = module.scan

        def chopped(source, limits, jsonl=False):
            for e in real(source, limits, jsonl=jsonl):
                if e.kind != STRING_CHUNK or len(e.text) < 2000:
                    yield e
                    continue
                for i in range(0, len(e.text), 1000):
                    yield Event(STRING_CHUNK, e.offsets[i], e.offsets[min(i + 1000, len(e.text))], e.path, text=e.text[i:i + 1000],
                                offsets=e.offsets[i:min(i + 1000, len(e.text)) + 1], record=e.record)
        text = 'é' * 40000 + NUM
        raw = json.dumps({'k': text}, ensure_ascii=False).encode()
        module.scan = chopped
        try:
            pieces = [u for u in units(raw) if u[0] == 'values']
            last = pieces[-1]
            start = last[2].index(NUM)
            out, items = write(raw, [op(1, last[1], start, start + 11)])
        finally:
            module.scan = real
        self.assertEqual([len(p[2]) for p in pieces], [PIECE, PIECE, 40011 - 2 * PIECE])
        self.assertEqual(res(items), {'op_1': ('executed', None)})
        self.assertEqual(json.loads(out)['k'], 'é' * 40000 + '[号]')

    def test_numbers_are_extracted_from_seven_digits_up(self):
        got = [u[2] for u in units(b'{"a":123456,"b":1234567,"c":-12345678.5,"d":1e10}') if u[0] == 'numbers']
        self.assertEqual(got, ['1234567', '-12345678.5'])

    def test_an_empty_string_is_a_unit_so_numbering_holds(self):
        self.assertEqual([u for u in units(b'["","a"]') if u[0] == 'values'], [('values', 64, ''), ('values', 128, 'a')])

    def test_a_long_string_is_cut_in_pieces_with_their_own_ordinals_and_too_long_fails(self):
        raw = json.dumps({'k': 'a' * 20000, 'z': 'b'}).encode()
        us = [u for u in units(raw) if u[0] == 'values']
        self.assertEqual([(u[1], len(u[2])) for u in us], [(64, 16384), (65, 3616), (128, 1)])
        big = json.dumps({'k': 'a' * (PIECE * PIECES + 1)}).encode()
        with self.assertRaises(TaskError) as ctx:
            list(unitkit._guard(open_adapter(io.BytesIO(big)).units(io.BytesIO(big))))
        self.assertEqual(ctx.exception.code, 'INPUT_CORRUPT')


class WriteTest(unittest.TestCase):
    def test_only_the_planned_range_of_a_string_changes_and_everything_else_is_byte_identical(self):
        out, items = write(DOC, [op(1, 2 * PIECES, 5, 16), op(2, 3 * PIECES, 0, 11, '')])
        self.assertEqual(res(items), {'op_1': ('executed', None), 'op_2': ('executed', None)})
        self.assertEqual(out, DOC.replace(f'call {NUM} now'.encode(), 'call [号] now'.encode()).replace(f'"{NUM}"'.encode(), b'""'))
        json.loads(out)

    def test_replacements_are_escaped_as_json(self):
        out, _ = write(DOC, [op(1, 3 * PIECES, 0, 11, '"\\\n\u2028')])
        self.assertEqual(json.loads(out)['list'][0], '"\\\n\u2028')

    def test_keys_and_numbers_cannot_be_edited_and_say_so(self):
        out, items = write(DOC, [op(1, 2, 0, 5, 'X', part='keys'), op(2, 1, 0, 18, '0', part='numbers')])
        self.assertEqual(res(items), {'op_1': ('not_executed', 'PART_OPERATION_UNSUPPORTED'), 'op_2': ('not_executed', 'PART_OPERATION_UNSUPPORTED')})
        self.assertEqual(out, DOC)

    def test_json_lines_edits_keep_the_line_structure(self):
        raw = f'{{"a":"{NUM}"}}\n\n{{"a":"y {NUM}"}}\n'.encode()
        a = open_adapter(io.BytesIO(raw))
        self.assertEqual([u.ordinal for u in a.units(io.BytesIO(raw)) if u.part == 'values'], [64, 128])
        out, items = write(raw, [op(1, 64, 0, 11), op(2, 128, 2, 13)])
        self.assertEqual(out.decode(), '{"a":"[号]"}\n\n{"a":"y [号]"}\n')

    def test_unicode_escapes_in_the_source_are_replaced_by_their_decoded_range(self):
        raw = ('{"a":"\\u5f20\\u4e09 ' + NUM + '"}').encode()
        out, items = write(raw, [op(1, 64, 3, 14)])
        self.assertEqual(json.loads(out)['a'], '张三 [号]')
        self.assertTrue(out.startswith(b'{"a":"\\u5f20\\u4e09 '))  # the untouched escapes stay as written

    def test_bad_operations_say_why(self):
        out, items = write(DOC, [op(1, 999, 0, 1), op(2, 3 * PIECES, 0, 99)])
        self.assertEqual(res(items), {'op_1': ('not_executed', 'UNIT_NOT_FOUND'), 'op_2': ('not_executed', 'RANGE_INVALID')})


class ValidateTest(unittest.TestCase):
    def test_correct_candidate_and_missing_edit(self):
        ops = [op(1, 2 * PIECES, 5, 16), op(2, 3 * PIECES, 0, 11)]
        cand, _ = write(DOC, ops)
        self.assertEqual(check(DOC, cand, ops)[0], {'op_1': (True, False), 'op_2': (True, False)})
        half, _ = write(DOC, ops[:1])
        self.assertEqual(check(DOC, half, ops)[0], {'op_1': (True, False), 'op_2': (False, True)})

    def test_a_candidate_that_is_no_longer_json_is_not_reopened(self):
        _, items = check(DOC, b'{"a":', [op(1, 2 * PIECES, 5, 16)])
        self.assertFalse(items[-1]['reopened'])
        _, items = check(DOC, b'{"a":"x"}\n{"b":"y"}\n', [op(1, 2 * PIECES, 5, 16)])  # json lines is another format than json
        self.assertFalse(items[-1]['reopened'])

    def test_the_three_parts_are_listed_for_input_and_output(self):
        ops = [op(1, 2 * PIECES, 5, 16)]
        cand, _ = write(DOC, ops)
        _, items = check(DOC, cand, ops)
        self.assertEqual([i['part'] for i in items if i['kind'] == 'input_part'], ['values', 'keys', 'numbers'])
        self.assertEqual([i['part'] for i in items if i['kind'] == 'output_part'], ['values', 'keys', 'numbers'])


class ExtractTest(unittest.TestCase):
    def test_extraction_reports_all_three_parts_inspected(self):
        task = FakeTask()
        unitkit.extract(open_adapter(io.BytesIO(DOC)), io.BytesIO(DOC), task, schemas())
        self.assertEqual({i['part']: i['state'] for i in task.items if i['kind'] == 'part_state'}, {'values': 'inspected', 'keys': 'inspected', 'numbers': 'inspected'})
        self.assertEqual(task.items[-1], {'kind': 'extract_complete', 'complete': True, 'units': 10, 'parts': 3})


if __name__ == '__main__':
    unittest.main()
