"""Strict, bounded, streaming CSV scanner with byte spans per cell.

Cells are reported with the absolute byte span of the cell in the source, so a
caller replaces one cell and every other byte (quoting, line endings, ragged
rows) is copied unchanged. The scanner works on bytes and only decodes a single
cell at a time, so the encoding must keep the delimiter, quote, CR and LF bytes
distinct from any lead/trail byte (UTF-8, GB18030, Big5, cp1252). UTF-16 is
refused until the host transcodes.

Grammar: RFC 4180 with a single-byte delimiter. A bare quote inside an unquoted
cell, text after a closing quote, and an unterminated quoted cell are errors
(stricter than the standard library's default dialect). A blank line is a
record without cells, like ``csv.reader``. Rows may have different cell counts:
reporting that is the plugin's job, not a parse error.
"""
from __future__ import annotations

import re
from dataclasses import dataclass
from typing import BinaryIO, Iterable, Iterator

from tacet_plugin_sdk import byteedit
from tacet_plugin_sdk.byteedit import Edit, FormatError

ENCODINGS = {'utf-8': 'utf-8', 'utf-8-sig': 'utf-8', 'gb18030': 'gb18030', 'big5': 'big5', 'cp1252': 'cp1252'}
_BOM = b'\xef\xbb\xbf'
FORMULA_TRIGGERS = ('=', '+', '-', '@', '\t', '\r')


class CsvError(FormatError):
    """Stable ``code`` plus the absolute byte ``offset``; never contains content."""


@dataclass(frozen=True)
class Dialect:
    delimiter: str = ','
    encoding: str = 'utf-8'

    def __post_init__(self):
        if self.encoding not in ENCODINGS:
            raise CsvError('unsupported_encoding', 0)
        if len(self.delimiter) != 1 or ord(self.delimiter) > 0x7E or self.delimiter in '"\r\n' or ord(self.delimiter) < 0x09:
            raise CsvError('bad_delimiter', 0)


@dataclass(frozen=True)
class Limits:
    max_cell_bytes: int = 8 << 20
    max_cells_per_record: int = 65536
    max_record_bytes: int = 64 << 20
    read_size: int = 1 << 16


class Cell:
    __slots__ = ('record', 'column', 'start', 'end', 'text', 'quoted')

    def __init__(self, record, column, start, end, text, quoted):
        self.record = record    # 0-based record ordinal, blank lines count
        self.column = column    # 0-based
        self.start = start      # absolute byte span of the cell, quotes included
        self.end = end
        self.text = text        # decoded value ("" unescaped, quotes removed)
        self.quoted = quoted

    def __repr__(self):  # no text: cells may hold sensitive values
        return f'Cell(r{self.record}c{self.column}, {self.start}-{self.end})'


class RecordEnd:
    __slots__ = ('record', 'cells', 'start', 'end')

    def __init__(self, record, cells, start, end):
        self.record = record
        self.cells = cells      # number of cells in the record (0 for a blank line)
        self.start = start      # first byte of the record
        self.end = end          # first byte after the record terminator

    def __repr__(self):
        return f'RecordEnd(r{self.record}, cells={self.cells})'


def scan(source: BinaryIO, dialect: Dialect = Dialect(), limits: Limits = Limits()) -> Iterator:
    """Yield Cell and RecordEnd events in file order."""
    delimiter = dialect.delimiter.encode()
    codec = ENCODINGS[dialect.encoding]
    unquoted = re.compile(b'[^"\\r\\n' + re.escape(delimiter) + b']*')
    quoted = re.compile(rb'"(?:[^"]++|"")*+"')
    buf, pos, base, eof = b'', 0, 0, False

    def fill(minimum=0):
        nonlocal buf, pos, base, eof
        if eof:
            return False
        data = source.read(max(limits.read_size, minimum))
        if not data:
            eof = True
            return False
        buf = buf[pos:] + data
        base += pos
        pos = 0
        return True

    def need(n):
        while len(buf) - pos < n:
            if not fill():
                return False
        return True

    def decode(raw, offset):
        try:
            return raw.decode(codec)
        except UnicodeDecodeError as exc:
            raise CsvError('invalid_encoding', offset + exc.start) from None

    if need(3) and buf[:3] == _BOM and codec == 'utf-8':
        pos = 3
    record, column, record_start, at_record_start = 0, 0, base + pos, True
    while True:
        if at_record_start:
            if pos >= len(buf) and not fill():
                return
            record_start, column = base + pos, 0
            at_record_start = False
        if base + pos - record_start > limits.max_record_bytes:
            raise CsvError('record_too_large', base + pos)
        if pos >= len(buf):
            fill()
        # ---- one cell
        start = base + pos
        if pos < len(buf) and buf[pos] == 0x22:
            while True:
                match = quoted.match(buf, pos)
                if match is not None:
                    # the closing quote may really start an escaped quote not yet read
                    if match.end() == len(buf) and fill():
                        continue
                    break
                if len(buf) - pos > limits.max_cell_bytes + 2:
                    raise CsvError('cell_too_large', start)
                if not fill(len(buf) - pos):  # grow geometrically: rescans stay bounded
                    raise CsvError('unterminated_quote', start)
            raw = match.group()
            if len(raw) > limits.max_cell_bytes + 2:
                raise CsvError('cell_too_large', start)
            pos = match.end()
            text = decode(raw[1:-1], start + 1).replace('""', '"')
            is_quoted = True
            if not need(1):
                nxt = -1
            else:
                nxt = buf[pos]
            if nxt not in (-1, 0x0A, 0x0D, delimiter[0]):
                raise CsvError('text_after_quote', base + pos)
        else:
            while True:
                match = unquoted.match(buf, pos)
                if match.end() - pos > limits.max_cell_bytes:  # stop before reading more
                    raise CsvError('cell_too_large', start)
                if match.end() == len(buf) and fill():
                    continue
                break
            raw = match.group()
            if len(raw) > limits.max_cell_bytes:
                raise CsvError('cell_too_large', start)
            pos = match.end()
            if pos < len(buf) and buf[pos] == 0x22:
                raise CsvError('bare_quote', base + pos)
            text = decode(raw, start)
            is_quoted = False
            nxt = buf[pos] if pos < len(buf) else -1
        blank_line = (not is_quoted and not raw and column == 0 and nxt in (-1, 0x0A, 0x0D))
        if not blank_line:
            yield Cell(record, column, start, base + pos, text, is_quoted)
            column += 1
            if column > limits.max_cells_per_record:
                raise CsvError('too_many_cells', base + pos)
        # ---- separator or terminator
        if nxt == delimiter[0]:
            pos += 1
            continue
        if nxt == 0x0D:
            need(2)
            pos += 2 if pos + 1 < len(buf) and buf[pos + 1] == 0x0A else 1
        elif nxt == 0x0A:
            pos += 1
        yield RecordEnd(record, 0 if blank_line else column, record_start, base + pos)
        record += 1
        at_record_start = True


def encode_cell(text: str, dialect: Dialect, *, force_quotes: bool = False) -> bytes:
    """Cell bytes for ``text``: quoted only when required (or forced)."""
    needs = force_quotes or any(c in text for c in (dialect.delimiter, '"', '\r', '\n'))
    body = text.replace('"', '""') if needs else text
    try:
        data = body.encode(ENCODINGS[dialect.encoding])
    except UnicodeEncodeError:
        raise CsvError('unencodable_text', 0) from None
    return b'"' + data + b'"' if needs else data


def cell_edit(cell: Cell, new_text: str, dialect: Dialect, *, formula_guard: bool = True) -> Edit:
    """Edit replacing the whole cell value.

    A cell that was quoted stays quoted. If the new value starts with a
    spreadsheet formula trigger that the original did not start with, a single
    quote is prefixed so the value is not interpreted as a formula. An
    untouched cell is never passed through here, so existing content is kept.
    """
    if formula_guard and new_text[:1] in FORMULA_TRIGGERS and new_text[:1] != cell.text[:1]:
        new_text = "'" + new_text
    return Edit(cell.start, cell.end, encode_cell(new_text, dialect, force_quotes=cell.quoted))


def rewrite(source: BinaryIO, sink: BinaryIO, edits: Iterable[Edit], block: int = 1 << 20) -> int:
    try:
        return byteedit.rewrite(source, sink, edits, block)
    except FormatError as exc:
        raise CsvError(exc.code, exc.offset) from None
