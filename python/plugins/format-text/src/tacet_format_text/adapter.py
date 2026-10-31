"""TXT / Markdown / log files as the unit-based protocol sees them.

A unit is one line (terminator excluded), numbered by line: ``ordinal = line * 64 + piece``. A line longer
than ``PIECE`` code points is cut into pieces of that size, at most 64 of them; a longer line fails the
file instead of being truncated. Numbering by line keeps every other unit's ordinal where it was when a
replacement changes a line's length, which is what lets validation find the same unit in the candidate.
"""
from __future__ import annotations

import re

from tacet_plugin_sdk.byteedit import Edit
from tacet_plugin_sdk.unitkit import Adapter, Unit

from .scanner import ENCODINGS, TextError, scan

PIECE = 16384
PIECES = 64
_TERMINATOR = re.compile(r'\r\n|\r|\n')
_CANDIDATES = ('utf-8', 'gb18030')


def detect_encoding(source):
    """The encoding the file is read and written in, or None if it is not text this plugin will claim."""
    source.seek(0)
    head = source.read(4)
    source.seek(0)
    if head[:4] == b'\xff\xfe\x00\x00' or head[:4] == b'\x00\x00\xfe\xff':
        return None  # UTF-32
    if head[:2] == b'\xff\xfe':
        order = ['utf-16-le']
    elif head[:2] == b'\xfe\xff':
        order = ['utf-16-be']
    else:
        order = list(_CANDIDATES)
    for encoding in order:
        source.seek(0)
        try:
            for chunk in scan(source, encoding):
                if '\x00' in chunk.text and not encoding.startswith('utf-16'):
                    raise TextError('binary', chunk.start)
        except TextError:
            continue
        source.seek(0)
        return encoding
    source.seek(0)
    return None


def _lines(chunks):
    """(line number from 1, text, byte offsets of every code point plus the end) in file order."""
    text_parts: list[str] = []
    offs: list[int] = []
    number, pending_lf = 0, False
    for chunk in chunks:
        text, chunk_offs = chunk.text, chunk.offsets
        pos = 0
        if pending_lf and text.startswith('\n'):
            pos = 1
        pending_lf = False
        for m in _TERMINATOR.finditer(text, pos):
            number += 1
            text_parts.append(text[pos:m.start()])
            offs.extend(chunk_offs[pos:m.start()])
            offs.append(chunk_offs[m.start()])
            yield number, ''.join(text_parts), offs
            text_parts, offs = [], []
            pos = m.end()
            if m.group() == '\r' and m.end() == len(text):
                pending_lf = True
        text_parts.append(text[pos:])
        offs.extend(chunk_offs[pos:len(text)])
    if text_parts and any(text_parts):
        number += 1
        offs.append(chunk.offsets[-1])  # the end of the file
        yield number, ''.join(text_parts), offs


class TextAdapter(Adapter):
    format = 'txt'
    content_type = 'text/plain'

    def __init__(self, encoding):
        self.encoding = encoding
        self.codec = ENCODINGS[encoding][0]

    def units(self, source):
        source.seek(0)
        for number, text, offs in _lines(scan(source, self.encoding)):
            if len(text) <= PIECE:
                yield Unit('content', number * PIECES, text, offs)
                continue
            if len(text) > PIECE * PIECES:
                raise TextError('line_too_long', offs[0])
            for k in range(0, (len(text) + PIECE - 1) // PIECE):
                yield Unit('content', number * PIECES + k, text[k * PIECE:(k + 1) * PIECE], offs[k * PIECE:min((k + 1) * PIECE, len(text)) + 1])

    def edits(self, unit, ops):
        out = []
        for op in ops:
            try:
                data = op.replacement.encode(self.codec)
            except UnicodeEncodeError:
                raise TextError('unencodable_text', unit.offs[op.start]) from None
            out.append(Edit(unit.offs[op.start], unit.offs[op.end], data))
        return out


def open_adapter(source):
    encoding = detect_encoding(source)
    return TextAdapter(encoding) if encoding else None
