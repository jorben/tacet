import io
import json
import random
import unittest

from tacet_format_json import scanner as sc
from tacet_format_json.scanner import Edit, JsonError, Limits


def run(data, *, jsonl=False, **limits):
    if isinstance(data, str):
        data = data.encode('utf-8')
    return list(sc.scan(io.BytesIO(data), Limits(**limits), jsonl=jsonl))


def signature(events):
    """Everything an event reports, except object identity."""
    return [(e.kind, e.start, e.end, e.path, e.text, None if e.offsets is None else list(e.offsets),
             e.occurrence, e.record) for e in events]


def merged(events):
    """Chunk boundaries depend on read size; compare merged string content."""
    out, text, offs = [], None, None
    for e in events:
        if e.kind == sc.STRING_CHUNK:
            text = (text or '') + e.text
            offs = (offs or []) + list(e.offsets[:-1])
            last = e.offsets[-1]
        elif e.kind == sc.STRING_END:
            out.append(('string', e.start, e.end, e.path, text or '', (offs or []) + [e.end - 1]))
            text = offs = None
        elif e.kind != sc.STRING_BEGIN:
            out.append((e.kind, e.start, e.end, e.path, e.text, e.occurrence, e.record))
    return out


def rebuild(events):
    """Rebuild the Python value from events (numbers via their lexeme)."""
    stack, key, text, root = [], None, None, []

    def put(value):
        nonlocal key
        if not stack:
            root.append(value)
        elif isinstance(stack[-1], list):
            stack[-1].append(value)
        else:
            stack[-1][key] = value

    for e in events:
        if e.kind in (sc.START_OBJECT, sc.START_ARRAY):
            container = {} if e.kind == sc.START_OBJECT else []
            put(container)
            stack.append(container)
        elif e.kind in (sc.END_OBJECT, sc.END_ARRAY):
            stack.pop()
        elif e.kind == sc.KEY:
            key = e.text
        elif e.kind == sc.STRING_BEGIN:
            text = ''
        elif e.kind == sc.STRING_CHUNK:
            text += e.text
        elif e.kind == sc.STRING_END:
            put(text)
        elif e.kind == sc.NUMBER:
            put(json.loads(e.text))
        elif e.kind == sc.LITERAL:
            put(json.loads(e.text))
    return root[0]


VALID = [
    '{}', '[]', '0', '-0', '1.5e+10', 'true', 'false', 'null', '""', '"a"',
    '[1,2,3]', '{"a":1,"b":[true,null,{"c":"d"}]}', ' \n\t[ 1 , 2 ]\r\n',
    '"\\u00e9\\ud83d\\ude00\\n\\/"', '[[[[[[]]]]]]', '{"":0}', '123456789012345678901234567890',
    '1E400', '-1.0e-0', '0.0', '[0.10,1.00,1e2]', '"\u4e2d\u6587 emoji \U0001F600"',
]

INVALID = [
    '', ' ', '{', '[', '}', ']', '[1,]', '{"a":1,}', '{"a"}', '{"a":}', '{a:1}', "{'a':1}",
    '[1 2]', '01', '-', '1.', '.5', '+1', '1e', '1e+', '0x10', 'NaN', 'Infinity', '-Infinity',
    'tru', 'nul', 'truee', '"abc', '"a\nb"', '"\\x"', '"\\u12"', '"\\uZZZZ"', '[1]]', '{} {}',
    '[1]x', '"\\ud800"', '"\\udc00"', '"\\ud800\\u0041"', '[,1]', '[1,,2]', '{"a":1 "b":2}',
    '\x00', '[\x00]', '{"a":1}}', '["a"\t"b"]', '[1;2]',
]


class GrammarTests(unittest.TestCase):
    def test_valid_documents_agree_with_stdlib(self):
        for text in VALID:
            with self.subTest(text=text):
                run(text)
                json.loads(text)

    def test_invalid_documents_are_rejected(self):
        for text in INVALID:
            with self.subTest(text=text):
                with self.assertRaises(JsonError):
                    run(text)

    def test_random_documents_round_trip_structure(self):
        rng = random.Random(7)

        def value(depth):
            kind = rng.randrange(7 if depth < 4 else 5)
            if kind == 0:
                return rng.randrange(-10**6, 10**6)
            if kind == 1:
                return rng.random() * 10 ** rng.randrange(-5, 25)
            if kind == 2:
                return ''.join(rng.choice('ab\\"\n\u4e2d\U0001F600 \u00e9\x01') for _ in range(rng.randrange(8)))
            if kind == 3:
                return rng.choice([True, False, None])
            if kind == 4:
                return rng.choice(['x', 1, None])
            if kind == 5:
                return [value(depth + 1) for _ in range(rng.randrange(4))]
            return {str(i) + rng.choice('ab'): value(depth + 1) for i in range(rng.randrange(4))}

        for _ in range(300):
            doc = value(0)
            for ascii_only in (True, False):
                text = json.dumps(doc, ensure_ascii=ascii_only, indent=rng.choice([None, 1]))
                for size in (1, 5, 1 << 18):
                    self.assertEqual(rebuild(run(text, read_size=size)), json.loads(text))

    def test_depth_limit(self):
        run('[' * 10 + ']' * 10, max_depth=10)
        with self.assertRaises(JsonError) as ctx:
            run('[' * 11 + ']' * 11, max_depth=10)
        self.assertEqual(ctx.exception.code, 'too_deep')

    def test_bom_is_skipped_and_offsets_stay_absolute(self):
        events = run(b'\xef\xbb\xbf[1]')
        self.assertEqual([(e.kind, e.start) for e in events],
                         [(sc.START_ARRAY, 3), (sc.NUMBER, 4), (sc.END_ARRAY, 5)])


class NumberTests(unittest.TestCase):
    def test_lexemes_are_reported_verbatim(self):
        text = '[0.10, 1.00, 1E400, -0, 12345678901234567890123, 1e2, 0.1e-0]'
        numbers = [e.text for e in run(text) if e.kind == sc.NUMBER]
        self.assertEqual(numbers, ['0.10', '1.00', '1E400', '-0', '12345678901234567890123', '1e2', '0.1e-0'])

    def test_number_length_limit(self):
        with self.assertRaises(JsonError) as ctx:
            run('1' * 2000)
        self.assertEqual(ctx.exception.code, 'number_too_long')
        run('1' * 1024)

    def test_number_spans_buffer_boundary(self):
        for size in (1, 2, 3, 5):
            numbers = [e.text for e in run('[123456.789e+12,-0.5]', read_size=size) if e.kind == sc.NUMBER]
            self.assertEqual(numbers, ['123456.789e+12', '-0.5'])


class KeyTests(unittest.TestCase):
    def test_duplicate_keys_are_reported_not_merged(self):
        keys = [(e.text, e.occurrence) for e in run('{"a":1,"b":2,"a":3,"a":4}') if e.kind == sc.KEY]
        self.assertEqual(keys, [('a', 0), ('b', 0), ('a', 1), ('a', 2)])

    def test_same_key_in_sibling_objects_is_not_duplicate(self):
        keys = [e.occurrence for e in run('[{"a":1},{"a":2}]') if e.kind == sc.KEY]
        self.assertEqual(keys, [0, 0])

    def test_escaped_key_equals_literal_key(self):
        keys = [(e.text, e.occurrence) for e in run('{"a":1,"\\u0061":2}') if e.kind == sc.KEY]
        self.assertEqual(keys, [('a', 0), ('a', 1)])

    def test_paths(self):
        paths = [(e.kind, e.path) for e in run('{"a":[10,{"b":"x"}],"c":null}')
                 if e.kind in (sc.NUMBER, sc.STRING_BEGIN, sc.LITERAL)]
        self.assertEqual(paths, [(sc.NUMBER, ('a', 0)), (sc.STRING_BEGIN, ('a', 1, 'b')),
                                 (sc.LITERAL, ('c',))])

    def test_key_limits(self):
        with self.assertRaises(JsonError) as ctx:
            run('{"' + 'k' * 50 + '":1}', max_key_chars=10)
        self.assertEqual(ctx.exception.code, 'key_too_long')
        with self.assertRaises(JsonError) as ctx:
            run('{' + ','.join(f'"k{i}":1' for i in range(20)) + '}', max_keys_per_object=10)
        self.assertEqual(ctx.exception.code, 'too_many_keys')


class StringTests(unittest.TestCase):
    def chunks(self, text, **limits):
        return [e for e in run(text, **limits) if e.kind == sc.STRING_CHUNK]

    def test_offsets_map_code_points_to_raw_bytes_with_escapes(self):
        raw = '"a\\u00e9\u4e2d\\n\U0001F600\\ud83d\\ude00z"'
        data = raw.encode('utf-8')
        [chunk] = self.chunks(raw)
        self.assertEqual(chunk.text, 'a\u00e9\u4e2d\n\U0001F600\U0001F600z')
        spans = [data[chunk.offsets[i]:chunk.offsets[i + 1]] for i in range(len(chunk.text))]
        self.assertEqual(spans, [b'a', b'\\u00e9', '\u4e2d'.encode(), b'\\n', '\U0001F600'.encode(),
                                 b'\\ud83d\\ude00', b'z'])

    def test_empty_string(self):
        events = run('""')
        self.assertEqual([e.kind for e in events], [sc.STRING_BEGIN, sc.STRING_CHUNK, sc.STRING_END])
        self.assertEqual(events[1].text, '')
        self.assertEqual((events[0].start, events[2].end), (0, 2))

    def test_long_string_is_chunked_with_bounded_pieces(self):
        body = 'x' * 10_000 + '\u4e2d' * 5_000
        chunks = self.chunks(json.dumps(body, ensure_ascii=False), chunk_chars=1000, read_size=700)
        self.assertGreater(len(chunks), 10)
        self.assertEqual(''.join(c.text for c in chunks), body)
        self.assertLess(max(len(c.text) for c in chunks), 1000 + 700)

    def test_string_limit(self):
        with self.assertRaises(JsonError) as ctx:
            self.chunks('"' + 'a' * 5000 + '"', max_string_chars=1000)
        self.assertEqual(ctx.exception.code, 'string_too_long')

    def test_invalid_utf8_is_rejected_everywhere(self):
        for bad in (b'\xff', b'\xc0\xaf', b'\xed\xa0\x80', b'\xf4\x90\x80\x80', b'\xe4\xb8', b'\x80'):
            for size in (1, 2, 1 << 18):
                with self.subTest(bad=bad, size=size):
                    with self.assertRaises(JsonError):
                        run(b'"a' + bad + b'b"', read_size=size)
        with self.assertRaises(JsonError):
            run(b'["\xe4\xb8')  # truncated multibyte at EOF

    def test_events_do_not_expose_content_in_repr(self):
        [chunk] = self.chunks('"secret-value"')
        self.assertNotIn('secret', repr(chunk))
        self.assertNotIn('secret', str(JsonError('bad_escape', 3)))


class BoundaryTests(unittest.TestCase):
    DOCS = [
        '{"a":[1,2.50,-0,"\u4e2d\u6587\\u00e9\\ud83d\\ude00",true,null],"a":{"b":"\U0001F600x"}}',
        '  [ "x" , {"k" : 12345678901234567890} ,\n false ]  ',
        '"' + 'ab\u4e2d\U0001F600\\n' * 50 + '"',
    ]

    def test_events_are_independent_of_read_size(self):
        for doc in self.DOCS:
            reference = merged(run(doc))
            for size in (1, 2, 3, 4, 5, 7, 11, 64):
                with self.subTest(size=size, doc=doc[:20]):
                    self.assertEqual(merged(run(doc, read_size=size)), reference)

    def test_offsets_point_at_the_same_bytes(self):
        doc = self.DOCS[0].encode()
        for size in (1, 3, 1 << 18):
            for e in run(doc, read_size=size):
                if e.kind == sc.NUMBER:
                    self.assertEqual(doc[e.start:e.end].decode(), e.text)
                if e.kind == sc.KEY:
                    self.assertEqual(json.loads(doc[e.start:e.end]), e.text)


class JsonlTests(unittest.TestCase):
    def test_records_and_blank_lines(self):
        events = run('{"a":1}\n\n  {"a":2}\r\n[3]', jsonl=True)
        records = sorted({e.record for e in events})
        self.assertEqual(records, [0, 1, 2])
        self.assertEqual([e.text for e in events if e.kind == sc.NUMBER], ['1', '2', '3'])

    def test_record_may_not_span_lines(self):
        with self.assertRaises(JsonError) as ctx:
            run('{"a":\n1}', jsonl=True)
        self.assertEqual(ctx.exception.code, 'record_incomplete')

    def test_two_values_on_one_line_are_rejected(self):
        with self.assertRaises(JsonError) as ctx:
            run('1 2\n', jsonl=True)
        self.assertEqual(ctx.exception.code, 'trailing_data')

    def test_scalar_records_and_empty_input(self):
        self.assertEqual([e.text for e in run('1\n"x"\ntrue\n', jsonl=True) if e.text is not None and e.kind != sc.STRING_CHUNK],
                         ['1', 'true'])
        self.assertEqual(run('', jsonl=True), [])
        self.assertEqual(run('\n\n', jsonl=True), [])

    def test_record_size_limit(self):
        with self.assertRaises(JsonError) as ctx:
            run('{"a":"' + 'x' * 5000 + '"}\n', jsonl=True, max_record_bytes=1000)
        self.assertEqual(ctx.exception.code, 'record_too_large')
        run('{"a":"xx"}\n' * 500, jsonl=True, max_record_bytes=100)  # limit is per record

    def test_bad_record_does_not_hide_earlier_ones(self):
        seen = []
        with self.assertRaises(JsonError):
            for event in sc.scan(io.BytesIO(b'{"a":1}\n{bad}\n'), jsonl=True):
                seen.append(event.kind)
        self.assertIn(sc.END_OBJECT, seen)  # caller sees record 0, then the error


class RewriteTests(unittest.TestCase):
    def rewrite(self, data, edits, block=1 << 20):
        out = io.BytesIO()
        sc.rewrite(io.BytesIO(data), out, edits, block)
        return out.getvalue()

    def test_untouched_bytes_and_lexemes_are_preserved(self):
        doc = '{ "id" : 12345678901234567890123 ,"v":1.50e+2,\n"name":"Zhang \u4e2d\\u00e9 San","dup":1,"dup":2 }'.encode()
        edits = []
        for e in run(doc):
            if e.kind == sc.STRING_CHUNK:
                i = e.text.index('Zhang')
                edits.append(sc.range_edit(e, i, i + 5, '[NAME]'))
        out = self.rewrite(doc, edits, block=7)
        self.assertEqual(out, doc.replace(b'Zhang', b'[NAME]'))
        self.assertEqual([e.text for e in run(out) if e.kind == sc.NUMBER],
                         ['12345678901234567890123', '1.50e+2', '1', '2'])

    def test_edit_inside_escape_sequence_region(self):
        doc = '"x\\u00e9\\ud83d\\ude00y"'.encode()
        [chunk] = [e for e in run(doc) if e.kind == sc.STRING_CHUNK]
        out = self.rewrite(doc, [sc.range_edit(chunk, 1, 3, '[X]')])
        self.assertEqual(out, b'"x[X]y"')
        self.assertEqual(json.loads(out), 'x[X]y')

    def test_replacement_is_escaped(self):
        doc = b'"abc"'
        [chunk] = [e for e in run(doc) if e.kind == sc.STRING_CHUNK]
        out = self.rewrite(doc, [sc.range_edit(chunk, 1, 2, 'q"\\\n\x01')])
        self.assertEqual(json.loads(out), 'aq"\\\n\x01c')

    def test_replace_whole_number_with_null_or_string(self):
        doc = b'{"n":1.0e5,"m":2}'
        numbers = [e for e in run(doc) if e.kind == sc.NUMBER]
        out = self.rewrite(doc, [Edit(numbers[0].start, numbers[0].end, b'null'),
                                 Edit(numbers[1].start, numbers[1].end, sc.string_value('[NUM]'))])
        self.assertEqual(json.loads(out), {'n': None, 'm': '[NUM]'})

    def test_edit_spanning_chunk_boundary_via_two_edits(self):
        body = 'ab' + '\u4e2d' * 10 + 'SECRET' + '\u4e2d' * 10
        doc = json.dumps(body, ensure_ascii=False).encode()
        for chunk_chars in (3, 7, 12, 13, 14):
            events = [e for e in run(doc, chunk_chars=chunk_chars, read_size=5) if e.kind == sc.STRING_CHUNK]
            self.assertGreater(len(events), 2)
            edits, position = [], 0  # position of each chunk's first code point in the whole string
            for e in events:
                lo, hi = max(position, 12), min(position + len(e.text), 18)
                if lo < hi:
                    edits.append(sc.range_edit(e, lo - position, hi - position, '[X]' if lo == 12 else ''))
                position += len(e.text)
            out = self.rewrite(doc, edits, block=4)
            self.assertEqual(json.loads(out), body.replace('SECRET', '[X]'), chunk_chars)

    def test_overlapping_and_out_of_range_edits_fail(self):
        with self.assertRaises(JsonError):
            self.rewrite(b'[1,2]', [Edit(1, 3, b''), Edit(2, 4, b'')])
        with self.assertRaises(JsonError):
            self.rewrite(b'[1]', [Edit(10, 12, b'')])

    def test_unencodable_replacement_is_rejected(self):
        with self.assertRaises(JsonError):
            sc.string_content('\ud800')


class ResourceTests(unittest.TestCase):
    def test_large_array_has_bounded_memory(self):
        import tracemalloc

        class Source(io.RawIOBase):
            """~20 MiB of numbers produced on demand; never held in memory."""
            def __init__(self):
                self.remaining = 20 << 20
                self.stage = 0

            def readable(self):
                return True

            def read(self, n=-1):
                if self.stage == 0:
                    self.stage = 1
                    return b'['
                if self.remaining > 0:
                    block = b'1234567890.125e3,' * (n // 17)
                    self.remaining -= len(block)
                    return block
                if self.stage == 1:
                    self.stage = 2
                    return b'0]'
                return b''

        tracemalloc.start()
        count = 0
        for event in sc.scan(Source(), Limits(read_size=1 << 16)):
            count += event.kind == sc.NUMBER
        _current, peak = tracemalloc.get_traced_memory()
        tracemalloc.stop()
        self.assertGreater(count, 1_000_000)
        self.assertLess(peak, 8 << 20)


if __name__ == '__main__':
    unittest.main()
