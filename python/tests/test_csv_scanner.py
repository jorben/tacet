import csv
import io
import random
import unittest

from tacet_format_csv import scanner as sc
from tacet_format_csv.scanner import Cell, CsvError, Dialect, Limits, RecordEnd


def run(data, dialect=Dialect(), **limits):
    if isinstance(data, str):
        data = data.encode('utf-8')
    return list(sc.scan(io.BytesIO(data), dialect, Limits(**limits)))


def rows(events):
    out, current = [], []
    for e in events:
        if isinstance(e, Cell):
            current.append(e.text)
        else:
            out.append(current)
            current = []
    assert not current
    return out


def stdlib_rows(data, delimiter=','):
    reader = csv.reader(io.StringIO(data.decode('utf-8'), newline=''), delimiter=delimiter, strict=True)
    return [r for r in reader]


class GrammarTests(unittest.TestCase):
    CASES = [
        'a,b,c\n1,2,3\n', 'a,b\r\n1,2\r\n', 'a,b\r1,2\r', 'a,b\n1,2', '"a,b",c\n', '"a""b",c\n',
        '"multi\nline",x\n', '"multi\r\nline",x\r\n', ',,\n', 'a,\n', ',a\n', '""\n', '"",""\n',
        'a\n\nb\n', '\n\n', '', '\n', 'x', '"x"', 'a,b\n1\n1,2,3\n', '中文,é\n"联系, 人",😀\n',
        'a, b\n', '"a"\n', '"a",\n', '"""",x\n', '"a\r"\n',
    ]

    def test_agrees_with_stdlib_on_valid_input(self):
        for text in self.CASES:
            with self.subTest(text=text):
                data = text.encode('utf-8')
                self.assertEqual(rows(run(data)), stdlib_rows(data))

    def test_random_tables_agree_with_stdlib_at_every_read_size(self):
        rng = random.Random(5)
        alphabet = ['a', 'b', ',', '"', '\n', '\r', ' ', '中', '😀', ';', '1']
        for _ in range(300):
            table = [[''.join(rng.choice(alphabet) for _ in range(rng.randrange(0, 6)))
                      for _ in range(rng.randrange(1, 5))] for _ in range(rng.randrange(0, 6))]
            out = io.StringIO(newline='')
            csv.writer(out, quoting=rng.choice([csv.QUOTE_MINIMAL, csv.QUOTE_ALL]),
                       lineterminator=rng.choice(['\n', '\r\n', '\r'])).writerows(table)
            data = out.getvalue().encode('utf-8')
            expected = stdlib_rows(data)
            for size in (1, 2, 3, 7, 1 << 16):
                self.assertEqual(rows(run(data, read_size=size)), expected, (data, size))

    def test_semicolon_and_tab_delimiters(self):
        for delimiter in (';', '\t', '|'):
            data = f'a{delimiter}"b{delimiter}c"{delimiter}d\n'.encode()
            self.assertEqual(rows(run(data, Dialect(delimiter))), stdlib_rows(data, delimiter))

    def test_invalid_input_is_rejected(self):
        for text, code in [('a"b,c\n', 'bare_quote'), ('"a"b,c\n', 'text_after_quote'), ('"abc', 'unterminated_quote'),
                           ('"abc""', 'unterminated_quote'), ('a,"b\nc,d\n', 'unterminated_quote'),
                           ('"a" ,b\n', 'text_after_quote'), ('"a"\x00,b\n', 'text_after_quote')]:
            for size in (1, 3, 1 << 16):
                with self.subTest(text=text, size=size):
                    with self.assertRaises(CsvError) as ctx:
                        run(text, read_size=size)
                    self.assertEqual(ctx.exception.code, code)

    def test_blank_lines_are_records_without_cells(self):
        ends = [e for e in run('a\n\n\nb\n') if isinstance(e, RecordEnd)]
        self.assertEqual([(e.record, e.cells) for e in ends], [(0, 1), (1, 0), (2, 0), (3, 1)])

    def test_ragged_rows_are_reported_not_rejected(self):
        ends = [e.cells for e in run('a,b\n1\n1,2,3\n') if isinstance(e, RecordEnd)]
        self.assertEqual(ends, [2, 1, 3])

    def test_bad_dialects_are_refused(self):
        for kwargs in ({'delimiter': '"'}, {'delimiter': ',,'}, {'delimiter': '\n'}, {'encoding': 'utf-16-le'},
                       {'delimiter': '中'}):
            with self.assertRaises(CsvError):
                Dialect(**kwargs)


class SpanTests(unittest.TestCase):
    def test_spans_point_at_the_cells_even_across_buffer_boundaries(self):
        data = '名,"a ""q"" ,b",x\r\n,"换\n行",\n中文,t'.encode()
        for size in (1, 2, 3, 5, 1 << 16):
            for e in run(data, read_size=size):
                if isinstance(e, Cell):
                    raw = data[e.start:e.end]
                    if e.quoted:
                        self.assertEqual(raw[:1] + raw[-1:], b'""')
                        self.assertEqual(raw[1:-1].decode().replace('""', '"'), e.text)
                    else:
                        self.assertEqual(raw.decode(), e.text)

    def test_record_spans_cover_the_file_exactly(self):
        data = 'a,b\r\n\n"x\ny",z\nq'.encode()
        ends = [e for e in run(data) if isinstance(e, RecordEnd)]
        self.assertEqual(ends[0].start, 0)
        for earlier, later in zip(ends, ends[1:]):
            self.assertEqual(earlier.end, later.start)
        self.assertEqual(ends[-1].end, len(data))

    def test_bom_is_skipped_and_offsets_stay_absolute(self):
        events = run(b'\xef\xbb\xbfa,b\n')
        self.assertEqual([(e.start, e.end) for e in events if isinstance(e, Cell)], [(3, 4), (5, 6)])

    def test_legacy_encodings(self):
        for name in ('gb18030', 'big5', 'cp1252'):
            text = {'gb18030': '姓名,"电话, 号"\n张三,138\n', 'big5': '姓名,"電話, 號"\n張三,138\n',
                    'cp1252': 'name,"caf\u00e9, \u20ac"\nx,1\n'}[name]
            data = text.encode(sc.ENCODINGS[name])
            for size in (1, 2, 1 << 16):
                with self.subTest(name=name, size=size):
                    self.assertEqual(rows(run(data, Dialect(encoding=name), read_size=size)),
                                     stdlib_rows(text.encode('utf-8')))

    def test_invalid_encoding_offset_is_absolute(self):
        with self.assertRaises(CsvError) as ctx:
            run(b'a,b\nc,\xff\n')
        self.assertEqual((ctx.exception.code, ctx.exception.offset), ('invalid_encoding', 6))

    def test_events_do_not_expose_content(self):
        [cell, _end] = run('secret-value\n')
        self.assertNotIn('secret', repr(cell))
        self.assertNotIn('secret', str(CsvError('bare_quote', 3)))


class RewriteTests(unittest.TestCase):
    def rewrite(self, data, edits, block=1 << 20):
        out = io.BytesIO()
        sc.rewrite(io.BytesIO(data), out, edits, block)
        return out.getvalue()

    def edit_where(self, data, predicate, text, dialect=Dialect(), **limits):
        return [sc.cell_edit(e, text, dialect) for e in run(data, dialect, **limits)
                if isinstance(e, Cell) and predicate(e)]

    def test_untouched_cells_quotes_and_line_endings_survive(self):
        data = 'id,name,note\r\n001,"Zhang, San",x\n"002",Li,"q""uote"\r\n,,\n'.encode()
        out = self.rewrite(data, self.edit_where(data, lambda c: c.text == 'Zhang, San', '[NAME]'), block=4)
        self.assertEqual(out, 'id,name,note\r\n001,"[NAME]",x\n"002",Li,"q""uote"\r\n,,\n'.encode())
        self.assertEqual(rows(run(out))[1], ['001', '[NAME]', 'x'])

    def test_replacement_is_quoted_when_required(self):
        data = b'a,b\n1,2\n'
        out = self.rewrite(data, self.edit_where(data, lambda c: c.text == '1', 'x,"y"\nz'))
        self.assertEqual(rows(run(out)), [['a', 'b'], ['x,"y"\nz', '2']])

    def test_quoted_cell_stays_quoted_and_empty_values_work(self):
        data = b'"a",b\n'
        out = self.rewrite(data, self.edit_where(data, lambda c: True, ''))
        self.assertEqual(out, b'"",\n')
        self.assertEqual(rows(run(out)), [['', '']])

    def test_formula_injection_guard(self):
        data = b'a,b,c\n1,2,3\n'
        for trigger in ('=1+1', '+1', '-1', '@x', '\tx', '\rx'):
            with self.subTest(trigger=repr(trigger)):
                out = self.rewrite(data, self.edit_where(data, lambda c: c.text == '1', trigger))
                self.assertEqual(rows(run(out))[1][0], "'" + trigger)
        out = self.rewrite(data, self.edit_where(data, lambda c: c.text == '1', '[X]'))
        self.assertEqual(rows(run(out))[1][0], '[X]')

    def test_existing_formula_like_cells_are_not_altered_by_the_guard(self):
        data = b'=SUM(A1),x\n'
        cell = run(data)[0]
        edit = sc.cell_edit(cell, '=SUM(A1)', Dialect())  # same leading character as the original
        self.assertEqual(self.rewrite(data, [edit]), data)

    def test_guard_can_be_disabled_explicitly(self):
        [cell, *_] = run(b'a\n')
        self.assertEqual(sc.cell_edit(cell, '=x', Dialect(), formula_guard=False).replacement, b'=x')

    def test_unencodable_replacement_is_rejected(self):
        data = 'a\n'.encode('cp1252')
        [cell, *_] = run(data, Dialect(encoding='cp1252'))
        with self.assertRaises(CsvError) as ctx:
            sc.cell_edit(cell, '中', Dialect(encoding='cp1252'))
        self.assertEqual(ctx.exception.code, 'unencodable_text')

    def test_legacy_encoding_rewrite(self):
        d = Dialect(encoding='gb18030')
        data = '姓名,电话\n张三,13812345678\n'.encode('gb18030')
        out = self.rewrite(data, self.edit_where(data, lambda c: c.text == '张三', '[姓名]', d), block=3)
        self.assertEqual(out, '姓名,电话\n[姓名],13812345678\n'.encode('gb18030'))

    def test_overlapping_edits_fail(self):
        with self.assertRaises(CsvError):
            self.rewrite(b'a,b\n', [sc.Edit(0, 2, b''), sc.Edit(1, 3, b'')])

    def test_random_tables_round_trip_with_no_edits_and_with_one(self):
        rng = random.Random(9)
        for _ in range(100):
            table = [[''.join(rng.choice('ab,"\n中 ') for _ in range(rng.randrange(0, 5)))
                      for _ in range(rng.randrange(1, 4))] for _ in range(rng.randrange(1, 5))]
            out = io.StringIO(newline='')
            csv.writer(out, lineterminator=rng.choice(['\n', '\r\n'])).writerows(table)
            data = out.getvalue().encode()
            self.assertEqual(self.rewrite(data, []), data)
            cells = [e for e in run(data) if isinstance(e, Cell)]
            if not cells:
                continue
            target = rng.choice(cells)
            result = self.rewrite(data, [sc.cell_edit(target, 'Z,"z"', Dialect())], block=rng.randrange(1, 9))
            expected = stdlib_rows(data)
            expected[target.record][target.column] = 'Z,"z"'
            self.assertEqual(stdlib_rows(result), expected)


class LimitTests(unittest.TestCase):
    def test_limits_fail_closed(self):
        with self.assertRaises(CsvError) as ctx:
            run('a,' + 'x' * 5000 + '\n', max_cell_bytes=1000)
        self.assertEqual(ctx.exception.code, 'cell_too_large')
        with self.assertRaises(CsvError) as ctx:
            run('"' + 'x' * 5000 + '"\n', max_cell_bytes=1000, read_size=64)
        self.assertEqual(ctx.exception.code, 'cell_too_large')
        with self.assertRaises(CsvError) as ctx:
            run(','.join('x' * 20) + '\n', max_cells_per_record=10)
        self.assertEqual(ctx.exception.code, 'too_many_cells')
        with self.assertRaises(CsvError) as ctx:
            run('a,b\n' * 1 + 'x,' * 3000 + '\n', max_record_bytes=1000)
        self.assertEqual(ctx.exception.code, 'record_too_large')

    def test_unterminated_quoted_cell_stops_reading_at_the_limit(self):
        class Endless(io.RawIOBase):
            def __init__(self):
                self.served = 0
                self.opened = False

            def readable(self):
                return True

            def read(self, n=-1):
                if not self.opened:
                    self.opened = True
                    self.served += 1
                    return b'"'
                self.served += n
                if self.served > 1 << 30:
                    raise AssertionError('scanner kept reading an unterminated cell')
                return b'x' * n

        source = Endless()
        with self.assertRaises(CsvError) as ctx:
            list(sc.scan(source, Dialect(), Limits(max_cell_bytes=100_000, read_size=4096)))
        self.assertEqual(ctx.exception.code, 'cell_too_large')
        self.assertLess(source.served, 1 << 20)  # bounded by roughly twice the limit

    def test_unquoted_endless_cell_stops_reading_at_the_limit(self):
        class Endless(io.RawIOBase):
            served = 0

            def readable(self):
                return True

            def read(self, n=-1):
                self.served += n
                if self.served > 1 << 30:
                    raise AssertionError('scanner kept reading an endless cell')
                return b'x' * n

        source = Endless()
        with self.assertRaises(CsvError) as ctx:
            list(sc.scan(source, Dialect(), Limits(max_cell_bytes=100_000, read_size=4096)))
        self.assertEqual(ctx.exception.code, 'cell_too_large')

    def test_limit_applies_per_cell_and_per_record_not_per_file(self):
        data = ('abc,def\n' * 5000).encode()
        self.assertEqual(len(rows(run(data, max_cell_bytes=10, max_record_bytes=20))), 5000)

    def test_large_quoted_cell_with_small_reads_is_accepted_within_limit(self):
        body = ('x' * 100 + '""') * 300
        [cell, _end] = run('"' + body + '"\n', read_size=16)
        self.assertEqual(len(cell.text), 300 * 101)


if __name__ == '__main__':
    unittest.main()
