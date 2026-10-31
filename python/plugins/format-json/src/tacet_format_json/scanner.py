"""Strict, bounded, streaming JSON / JSONL scanner.

The scanner reports tokens with absolute byte spans instead of building a
value tree, so a caller can replace exactly one value or one code-point range
and copy every other byte unchanged. Numbers are never converted: the lexeme
is reported as written. Duplicate keys are reported (never merged) with an
occurrence index. Every limit failure raises JsonError; a partial result is
never returned as success.

String values are delivered in chunks. Each chunk carries the decoded text and
``offsets``: the absolute byte offset of every code point plus one trailing
entry for the end of the chunk, so code points [i, j) map to the raw byte range
[offsets[i], offsets[j]) even when the source uses escapes.
"""
from __future__ import annotations

import json
import re
from array import array
from dataclasses import dataclass
from typing import BinaryIO, Iterable, Iterator

from tacet_plugin_sdk import byteedit
from tacet_plugin_sdk.byteedit import Edit, FormatError

START_OBJECT = 'start_object'
END_OBJECT = 'end_object'
START_ARRAY = 'start_array'
END_ARRAY = 'end_array'
KEY = 'key'
STRING_BEGIN = 'string_begin'
STRING_CHUNK = 'string_chunk'
STRING_END = 'string_end'
NUMBER = 'number'
LITERAL = 'literal'

_WS_ALL = re.compile(rb'[ \t\n\r]*')
_WS_LINE = re.compile(rb'[ \t\r]*')
_NUMBER = re.compile(rb'-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?')
_STRING_RUN = re.compile(rb'[^"\\\x00-\x1f]*')
_HEX4 = re.compile(rb'[0-9a-fA-F]{4}')
_SIMPLE_ESCAPES = {
    ord('"'): '"', ord('\\'): '\\', ord('/'): '/', ord('b'): '\b',
    ord('f'): '\f', ord('n'): '\n', ord('r'): '\r', ord('t'): '\t',
}
_LITERALS = {ord('t'): b'true', ord('f'): b'false', ord('n'): b'null'}
_BOM = b'\xef\xbb\xbf'


class JsonError(FormatError):
    """Stable ``code`` plus the absolute byte ``offset``; never contains content."""


@dataclass(frozen=True)
class Limits:
    max_depth: int = 128
    max_key_chars: int = 4096
    max_keys_per_object: int = 1_000_000
    max_key_bytes_per_object: int = 64 << 20
    max_number_bytes: int = 1024
    max_string_chars: int = 256 << 20
    chunk_chars: int = 16384
    read_size: int = 1 << 16
    max_record_bytes: int = 64 << 20  # JSONL: one line, including whitespace


class Event:
    __slots__ = ('kind', 'start', 'end', 'path', 'text', 'offsets', 'occurrence', 'record')

    def __init__(self, kind, start, end, path, text=None, offsets=None, occurrence=0, record=0):
        self.kind = kind
        self.start = start
        self.end = end
        self.path = path
        self.text = text
        self.offsets = offsets
        self.occurrence = occurrence  # KEY only: 0 for the first use of a key in its object
        self.record = record          # JSONL line ordinal (0-based, blank lines excluded)

    def __repr__(self):  # no text: events may hold sensitive values
        return f'Event({self.kind}, {self.start}-{self.end}, path_len={len(self.path)})'


class _Frame:
    __slots__ = ('is_object', 'keys', 'key_bytes')

    def __init__(self, is_object):
        self.is_object = is_object
        self.keys = {} if is_object else None
        self.key_bytes = 0


# parser states
_VALUE, _ARRAY_FIRST, _OBJECT_FIRST, _OBJECT_KEY, _COLON, _AFTER, _TOP_END = range(7)


class Scanner:
    def __init__(self, source: BinaryIO, limits: Limits = Limits(), *, jsonl: bool = False):
        self._src = source
        self._limits = limits
        self._jsonl = jsonl
        self._buf = b''
        self._pos = 0
        self._base = 0  # absolute offset of _buf[0]
        self._eof = False
        self._record = -1
        self._record_start = 0
        self._escape_start = 0

    # ---- low-level buffer -------------------------------------------------
    def _abs(self):
        return self._base + self._pos

    def _fill(self):
        """Read more; keeps unread bytes. Returns False at EOF."""
        if self._eof:
            return False
        data = self._src.read(self._limits.read_size)
        if not data:
            self._eof = True
            return False
        self._buf = self._buf[self._pos:] + data
        self._base += self._pos
        self._pos = 0
        return True

    def _ensure(self, n):
        while len(self._buf) - self._pos < n:
            if not self._fill():
                return False
        return True

    def _skip_ws(self, regex):
        while True:
            self._pos = regex.match(self._buf, self._pos).end()
            if self._pos < len(self._buf):
                return self._buf[self._pos]
            if not self._fill():
                return -1

    def _check_record_size(self):
        if self._jsonl and self._abs() - self._record_start > self._limits.max_record_bytes:
            raise JsonError('record_too_large', self._abs())

    # ---- strings ----------------------------------------------------------
    @staticmethod
    def _incomplete_tail(run):
        for k in range(1, 4):
            if k > len(run):
                return 0
            b = run[-k]
            if b < 0x80:
                return 0
            if b >= 0xC0:
                need = 2 if b < 0xE0 else 3 if b < 0xF0 else 4
                return k if need > k else 0
        return 0

    def _scan_string(self, chunk_chars):
        """Generator over (text, offsets, done). Opening quote already consumed."""
        limits = self._limits
        parts, offsets, count, total = [], array('Q'), 0, 0
        while True:
            if self._pos >= len(self._buf) and not self._fill():
                raise JsonError('unterminated_string', self._abs())
            end = _STRING_RUN.match(self._buf, self._pos).end()
            if end > self._pos:
                run = self._buf[self._pos:end]
                tail = self._incomplete_tail(run) if end == len(self._buf) and not self._eof else 0
                if tail:
                    run = run[:-tail]
                if run:
                    try:
                        text = run.decode('utf-8')
                    except UnicodeDecodeError as exc:
                        raise JsonError('invalid_utf8', self._abs() + exc.start) from None
                    origin = self._abs()
                    if text.isascii():
                        offsets.extend(range(origin, origin + len(text)))
                    else:
                        # a code point starts at every byte that is not a continuation byte
                        offsets.extend(origin + i for i, byte in enumerate(run)
                                       if byte & 0xC0 != 0x80)
                    parts.append(text)
                    count += len(text)
                    total += len(text)
                    self._pos += len(run)
                if tail:
                    if not self._fill():
                        raise JsonError('invalid_utf8', self._abs())
                    continue
            elif self._buf[self._pos] == 0x22:  # closing quote
                self._pos += 1
                offsets.append(self._abs() - 1)
                yield ''.join(parts), offsets, True
                return
            elif self._buf[self._pos] == 0x5C:  # backslash
                text = self._escape()
                offsets.append(self._escape_start)
                parts.append(text)
                count += 1
                total += 1
            else:
                raise JsonError('control_character_in_string', self._abs())
            if total > limits.max_string_chars:
                raise JsonError('string_too_long', self._abs())
            self._check_record_size()
            if count >= chunk_chars:
                offsets.append(self._abs())
                yield ''.join(parts), offsets, False
                parts, offsets, count = [], array('Q'), 0

    def _escape(self):
        """Consume one escape at the backslash; one code point comes out.

        The caller records the code point's offset from _escape_start."""
        start = self._abs()
        if not self._ensure(2):
            raise JsonError('unterminated_string', start)
        selector = self._buf[self._pos + 1]
        if selector in _SIMPLE_ESCAPES:
            self._pos += 2
            text = _SIMPLE_ESCAPES[selector]
        elif selector == 0x75:  # u
            if not self._ensure(6) or not _HEX4.fullmatch(self._buf, self._pos + 2, self._pos + 6):
                raise JsonError('bad_unicode_escape', start)
            unit = int(self._buf[self._pos + 2:self._pos + 6], 16)
            if 0xD800 <= unit <= 0xDBFF:
                if (not self._ensure(12) or self._buf[self._pos + 6:self._pos + 8] != b'\\u'
                        or not _HEX4.fullmatch(self._buf, self._pos + 8, self._pos + 12)):
                    raise JsonError('lone_surrogate', start)
                low = int(self._buf[self._pos + 8:self._pos + 12], 16)
                if not 0xDC00 <= low <= 0xDFFF:
                    raise JsonError('lone_surrogate', start)
                text = chr(0x10000 + ((unit - 0xD800) << 10) + (low - 0xDC00))
                self._pos += 12
            elif 0xDC00 <= unit <= 0xDFFF:
                raise JsonError('lone_surrogate', start)
            else:
                text = chr(unit)
                self._pos += 6
        else:
            raise JsonError('bad_escape', start)
        self._escape_start = start
        return text

    # ---- tokens -----------------------------------------------------------
    def _read_key(self):
        start = self._abs()
        self._pos += 1  # opening quote
        text = ''
        for piece, _offsets, done in self._scan_string(self._limits.max_key_chars + 1):
            text += piece
            if len(text) > self._limits.max_key_chars or not done:
                raise JsonError('key_too_long', start)
        return text, start, self._abs()

    def _read_number(self):
        start = self._abs()
        while True:
            match = _NUMBER.match(self._buf, self._pos)
            if match is None:
                if len(self._buf) - self._pos < 2 and self._fill():
                    continue  # a lone '-' may be followed by digits not yet read
                raise JsonError('bad_number', start)
            # a prefix may be complete only because the rest has not arrived:
            # '.d' or 'e+d' needs up to three more bytes of lookahead
            if len(self._buf) - match.end() < 3 and self._fill():
                continue
            break
        lexeme = match.group()
        if len(lexeme) > self._limits.max_number_bytes:
            raise JsonError('number_too_long', start)
        self._pos = match.end()
        return lexeme.decode('ascii'), start, self._abs()

    def _read_literal(self, first):
        word = _LITERALS[first]
        start = self._abs()
        if not self._ensure(len(word)) or self._buf[self._pos:self._pos + len(word)] != word:
            raise JsonError('bad_literal', start)
        self._pos += len(word)
        return word.decode('ascii'), start, self._abs()

    # ---- driver -----------------------------------------------------------
    def __iter__(self) -> Iterator[Event]:
        limits = self._limits
        if self._ensure(3) and self._buf[:3] == _BOM:
            self._pos = 3
        stack: list[_Frame] = []
        path: list = []
        state = _VALUE
        top_level_done = False

        def finish_value():
            nonlocal state, top_level_done
            if stack:
                if stack[-1].is_object:
                    path.pop()
                state = _AFTER
            else:
                state = _TOP_END
                top_level_done = True

        while True:
            if self._jsonl and state == _VALUE and not stack:
                c = self._skip_ws(_WS_ALL)  # blank lines between records
                if c == -1:
                    return
                self._record += 1
                self._record_start = self._abs()
            elif self._jsonl and state == _TOP_END:
                c = self._skip_ws(_WS_LINE)
                if c == -1:
                    return
                if c != 0x0A:
                    raise JsonError('trailing_data', self._abs())
                self._pos += 1
                state = _VALUE
                continue
            else:
                c = self._skip_ws(_WS_LINE if self._jsonl else _WS_ALL)
                if self._jsonl and c == 0x0A:
                    raise JsonError('record_incomplete', self._abs())
            self._check_record_size()
            if c == -1:
                if state == _TOP_END:
                    return
                raise JsonError('unexpected_end', self._abs())
            if state == _TOP_END:
                raise JsonError('trailing_data', self._abs())

            record = max(self._record, 0)
            if state == _COLON:
                if c != 0x3A:
                    raise JsonError('expected_colon', self._abs())
                self._pos += 1
                state = _VALUE
                continue

            if state == _AFTER:
                frame = stack[-1]
                if c == 0x2C:
                    self._pos += 1
                    if frame.is_object:
                        state = _OBJECT_KEY
                    else:
                        path[-1] += 1
                        state = _VALUE
                    continue
                closer = 0x7D if frame.is_object else 0x5D
                if c != closer:
                    raise JsonError('expected_comma_or_close', self._abs())
                state = _AFTER  # closing handled below
            elif state in (_OBJECT_FIRST, _OBJECT_KEY):
                if c == 0x7D and state == _OBJECT_FIRST:
                    pass  # close below
                elif c == 0x22:
                    text, start, end = self._read_key()
                    frame = stack[-1]
                    seen = frame.keys.get(text, 0)
                    frame.keys[text] = seen + 1
                    frame.key_bytes += end - start
                    if (len(frame.keys) > limits.max_keys_per_object
                            or frame.key_bytes > limits.max_key_bytes_per_object):
                        raise JsonError('too_many_keys', start)
                    path.append(text)
                    yield Event(KEY, start, end, tuple(path), text=text,
                                occurrence=seen, record=record)
                    state = _COLON
                    continue
                else:
                    raise JsonError('expected_key', self._abs())

            if c in (0x7D, 0x5D) and stack:  # closing bracket reached legitimately
                frame = stack[-1]
                if (c == 0x7D) != frame.is_object or state not in (_AFTER, _OBJECT_FIRST, _ARRAY_FIRST):
                    raise JsonError('mismatched_close', self._abs())
                start = self._abs()
                self._pos += 1
                if not frame.is_object:
                    path.pop()
                stack.pop()
                yield Event(END_OBJECT if frame.is_object else END_ARRAY,
                            start, self._abs(), tuple(path), record=record)
                finish_value()
                continue

            # a value is required here (_VALUE or _ARRAY_FIRST)
            if state not in (_VALUE, _ARRAY_FIRST):
                raise JsonError('unexpected_byte', self._abs())
            if c in (0x7B, 0x5B):
                if len(stack) >= limits.max_depth:
                    raise JsonError('too_deep', self._abs())
                start = self._abs()
                self._pos += 1
                is_object = c == 0x7B
                yield Event(START_OBJECT if is_object else START_ARRAY,
                            start, start + 1, tuple(path), record=record)
                stack.append(_Frame(is_object))
                if is_object:
                    state = _OBJECT_FIRST
                else:
                    path.append(0)
                    state = _ARRAY_FIRST
            elif c == 0x22:
                start = self._abs()
                self._pos += 1
                where = tuple(path)
                yield Event(STRING_BEGIN, start, start + 1, where, record=record)
                for text, offsets, done in self._scan_string(limits.chunk_chars):
                    yield Event(STRING_CHUNK, offsets[0], offsets[-1], where, text=text, offsets=offsets, record=record)
                yield Event(STRING_END, start, self._abs(), where, record=record)
                finish_value()
            elif c == 0x2D or 0x30 <= c <= 0x39:
                lexeme, start, end = self._read_number()
                yield Event(NUMBER, start, end, tuple(path), text=lexeme, record=record)
                finish_value()
            elif c in _LITERALS:
                word, start, end = self._read_literal(c)
                yield Event(LITERAL, start, end, tuple(path), text=word, record=record)
                finish_value()
            else:
                raise JsonError('unexpected_byte', self._abs())


def scan(source: BinaryIO, limits: Limits = Limits(), *, jsonl: bool = False) -> Iterator[Event]:
    return iter(Scanner(source, limits, jsonl=jsonl))


# ---- rewrite ---------------------------------------------------------------
def string_content(text: str) -> bytes:
    """Escaped body of a JSON string (no quotes) for a replacement."""
    try:
        return json.dumps(text, ensure_ascii=False)[1:-1].encode('utf-8')
    except UnicodeEncodeError:
        raise JsonError('unencodable_text', 0) from None


def string_value(text: str) -> bytes:
    return b'"' + string_content(text) + b'"'


def range_edit(chunk: Event, first: int, last: int, replacement: str) -> Edit:
    """Edit replacing code points [first, last) of a STRING_CHUNK."""
    if chunk.kind != STRING_CHUNK or not 0 <= first <= last < len(chunk.offsets):
        raise JsonError('bad_range', 0)
    return Edit(chunk.offsets[first], chunk.offsets[last], string_content(replacement))


def rewrite(source: BinaryIO, sink: BinaryIO, edits: Iterable[Edit], block: int = 1 << 20) -> int:
    """Copy source to sink replacing exactly the edited byte spans. Returns bytes read."""
    try:
        return byteedit.rewrite(source, sink, edits, block)
    except FormatError as exc:
        raise JsonError(exc.code, exc.offset) from None
