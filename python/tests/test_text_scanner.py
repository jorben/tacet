import io
import random
import unittest

from tacet_format_text import scanner as sc
from tacet_format_text.scanner import Limits, TextError


def run(data, encoding='utf-8', **limits):
    if isinstance(data, str):
        data = data.encode('utf-8')
    return list(sc.scan(io.BytesIO(data), encoding, Limits(**limits)))


def joined(chunks):
    return ''.join(c.text for c in chunks)


def spans(chunks, data):
    """Raw bytes of every code point, taken through the offset map."""
    out = []
    for c in chunks:
        out.extend(data[c.offsets[i]:c.offsets[i + 1]] for i in range(len(c.text)))
    return out


SAMPLES = {
    'utf-8': 'ab中文😀 e\u0301\r\n行二\nx\r末尾\u00e9',
    'utf-16-le': 'ab中文😀 e\u0301\r\n行二\nx\r末尾\u00e9',
    'utf-16-be': 'ab中文😀 e\u0301\r\n行二\nx\r末尾\u00e9',
    'gb18030': 'ab中文 e\r\n行二\nx\r末尾 \U0001F600',
    'big5': 'ab中文 e\r\n行二\nx\r末尾',
    'cp1252': 'abc \u00e9\u20ac\r\nline2\n',
}


class DecodeTests(unittest.TestCase):
    def test_every_encoding_maps_each_code_point_to_its_bytes(self):
        for name, text in SAMPLES.items():
            codec = sc.ENCODINGS[name][0]
            data = text.encode(codec)
            for size in (1, 2, 3, 5, 64, 1 << 16):
                for chunk_chars in (1, 4, 1 << 14):
                    with self.subTest(encoding=name, size=size, chunk=chunk_chars):
                        chunks = run(data, name, read_size=size, chunk_chars=chunk_chars)
                        self.assertEqual(joined(chunks), text)
                        self.assertEqual(spans(chunks, data), [ch.encode(codec) for ch in text])
                        for c in chunks:
                            self.assertEqual(c.end, c.offsets[len(c.text)])

    def test_chunks_are_contiguous_and_cover_the_file(self):
        data = ('ab中文😀\r\n' * 200).encode()
        chunks = run(data, chunk_chars=37, read_size=11)
        self.assertEqual(chunks[0].start, 0)
        for earlier, later in zip(chunks, chunks[1:]):
            self.assertEqual(earlier.end, later.start)
        self.assertEqual(chunks[-1].end, len(data))
        self.assertLessEqual(max(len(c.text) for c in chunks), 37)

    def test_newlines_are_never_translated(self):
        data = b'a\r\nb\rc\nd\r\n'
        self.assertEqual(joined(run(data)).encode(), data)

    def test_line_numbers_across_chunks_and_split_crlf(self):
        data = b'a\r\nb\r\nc\rd\ne\n\nf'
        text = data.decode()
        for chunk_chars in (1, 2, 3, 5, 100):
            consumed = 0
            for c in run(data, chunk_chars=chunk_chars):
                before = text[:consumed]  # line breaks completed before this chunk
                breaks = before.count('\n') + before.count('\r') - before.count('\r\n')
                self.assertEqual(c.first_line, breaks, (chunk_chars, consumed))
                consumed += len(c.text)

    def test_bom_is_skipped_and_offsets_stay_absolute(self):
        data = b'\xef\xbb\xbfab'
        [chunk] = run(data)
        self.assertEqual((chunk.text, list(chunk.offsets)), ('ab', [3, 4, 5]))
        [chunk] = run(b'\xff\xfea\x00', 'utf-16-le')
        self.assertEqual((chunk.text, list(chunk.offsets)), ('a', [2, 4]))

    def test_utf16_with_the_other_byte_order_mark_is_rejected(self):
        with self.assertRaises(TextError) as ctx:
            run(b'\xfe\xff\x00a', 'utf-16-le')
        self.assertEqual(ctx.exception.code, 'bom_mismatch')

    def test_short_and_empty_inputs(self):
        self.assertEqual(run(b''), [])
        self.assertEqual(joined(run(b'a')), 'a')
        self.assertEqual(joined(run(b'\xef\xbb\xbf')), '')


class RejectionTests(unittest.TestCase):
    def test_invalid_bytes_are_rejected_at_any_read_size(self):
        cases = [('utf-8', b'a\xffb'), ('utf-8', b'a\xc0\xafb'), ('utf-8', b'\xed\xa0\x80'),
                 ('utf-8', b'ab\xe4\xb8'), ('utf-16-le', b'a\x00b'), ('utf-16-le', b'\x00\xd8a\x00'),
                 ('gb18030', b'a\x81'), ('cp1252', b'a\x81b')]
        for name, data in cases:
            for size in (1, 2, 1 << 16):
                with self.subTest(name=name, data=data, size=size):
                    with self.assertRaises(TextError):
                        run(data, name, read_size=size)

    def test_error_offsets_are_absolute(self):
        with self.assertRaises(TextError) as ctx:
            run(b'a' * 100 + b'\xff', read_size=7)
        self.assertEqual(ctx.exception.offset, 100)

    def test_unsupported_encoding_is_refused(self):
        for name in ('iso-2022-jp', 'utf-7', 'shift_jis', 'latin-1', 'nope'):
            with self.assertRaises(TextError) as ctx:
                run(b'abc', name)
            self.assertEqual(ctx.exception.code, 'unsupported_encoding')

    def test_non_canonical_mappings_are_refused(self):
        # these big5 byte pairs decode, but the character re-encodes to different bytes
        for data in (b'\xa2\xcc', b'\xa2\xce', b'\xa1\xfe', b'\xa2\x40'):
            for size in (1, 64):
                with self.subTest(data=data, size=size):
                    with self.assertRaises(TextError) as ctx:
                        run(b'ab' + data + b'cd', 'big5', read_size=size)
                    self.assertEqual(ctx.exception.code, 'non_canonical_encoding')

    def test_errors_and_reprs_do_not_expose_content(self):
        [chunk] = run('secret-value')
        self.assertNotIn('secret', repr(chunk))
        self.assertNotIn('secret', str(TextError('bad_range', 1)))


class RewriteTests(unittest.TestCase):
    def rewrite(self, data, edits, block=1 << 20):
        out = io.BytesIO()
        sc.rewrite(io.BytesIO(data), out, edits, block)
        return out.getvalue()

    def edits_for(self, data, encoding, word, replacement, **limits):
        text_so_far, edits = '', []
        chunks = run(data, encoding, **limits)
        # a word may straddle chunks; locate in the whole text and edit per chunk
        whole = joined(chunks)
        start = whole.index(word)
        position = 0
        for c in chunks:
            lo, hi = max(start, position), min(start + len(word), position + len(c.text))
            if lo < hi:
                edits.append(sc.range_edit(c, lo - position, hi - position,
                                           replacement if lo == start else '', encoding))
            position += len(c.text)
        return edits

    def test_untouched_bytes_including_line_endings_are_preserved(self):
        for name in ('utf-8', 'utf-16-le', 'utf-16-be', 'gb18030', 'big5'):
            codec = sc.ENCODINGS[name][0]
            text = 'x\r\n聯繫 張三 電話\ny\r末'
            data = text.encode(codec)
            for chunk_chars in (2, 3, 100):
                with self.subTest(name=name, chunk=chunk_chars):
                    edits = self.edits_for(data, name, '張三', '[姓名]', chunk_chars=chunk_chars, read_size=5)
                    out = self.rewrite(data, edits, block=3)
                    self.assertEqual(out, text.replace('張三', '[姓名]').encode(codec))

    def test_edit_that_straddles_a_chunk_boundary(self):
        data = ('abcdef' + 'SECRET' + 'ghijkl').encode()
        for chunk_chars in range(1, 20):
            edits = self.edits_for(data, 'utf-8', 'SECRET', '[X]', chunk_chars=chunk_chars, read_size=4)
            self.assertEqual(self.rewrite(data, edits), b'abcdef[X]ghijkl', chunk_chars)

    def test_bom_is_kept(self):
        data = b'\xef\xbb\xbfhello SECRET'
        edits = self.edits_for(data, 'utf-8', 'SECRET', '[X]')
        self.assertEqual(self.rewrite(data, edits), b'\xef\xbb\xbfhello [X]')

    def test_replacement_must_be_encodable(self):
        [chunk] = run('abc'.encode('cp1252'), 'cp1252')
        with self.assertRaises(TextError) as ctx:
            sc.range_edit(chunk, 0, 1, '中', 'cp1252')
        self.assertEqual(ctx.exception.code, 'unencodable_text')

    def test_bad_ranges_and_overlaps_fail(self):
        [chunk] = run('abc')
        with self.assertRaises(TextError):
            sc.range_edit(chunk, 2, 9, 'x')
        with self.assertRaises(TextError):
            self.rewrite(b'abc', [sc.Edit(0, 2, b''), sc.Edit(1, 3, b'')])

    def test_random_files_round_trip_and_map_exactly(self):
        rng = random.Random(11)
        alphabet = 'ab 1\r\n\t中文é😀\u0301'
        for _ in range(100):
            text = ''.join(rng.choice(alphabet) for _ in range(rng.randrange(0, 80)))
            for name in ('utf-8', 'utf-16-le', 'utf-16-be'):
                codec = sc.ENCODINGS[name][0]
                data = text.encode(codec)
                chunks = run(data, name, read_size=rng.randrange(1, 9), chunk_chars=rng.randrange(1, 9))
                self.assertEqual(joined(chunks), text)
                self.assertEqual(self.rewrite(data, []), data)
                self.assertEqual(spans(chunks, data), [ch.encode(codec) for ch in text])


class ResourceTests(unittest.TestCase):
    def test_long_line_without_newline_stays_chunked(self):
        data = ('x' * 3_000_000 + '中').encode()
        chunks = run(data)
        self.assertGreater(len(chunks), 100)
        self.assertLessEqual(max(len(c.text) for c in chunks), 16384)
        self.assertEqual(chunks[-1].end, len(data))


if __name__ == '__main__':
    unittest.main()
