"""Incremental decoding of TXT / Markdown / log files with a byte map.

The file is read as bytes and decoded in bounded chunks with the declared
encoding. No newline translation happens, so line endings survive a rewrite.
Each chunk carries ``offsets``: the absolute byte offset of every code point
plus one trailing entry, so code points [i, j) map to raw bytes
[offsets[i], offsets[j]). Anything that does not decode strictly, or does not
re-encode to exactly the bytes it came from, raises ``TextError``: a lossy or
ambiguous mapping would make a redaction land on the wrong bytes.
"""
from __future__ import annotations

import codecs
from array import array
from dataclasses import dataclass
from typing import BinaryIO, Iterator

from tacet_plugin_sdk import byteedit
from tacet_plugin_sdk.byteedit import Edit, FormatError

# declared name -> (python codec used for decoding, BOM bytes accepted/skipped at start)
ENCODINGS = {
    'utf-8': ('utf-8', (b'\xef\xbb\xbf',)),
    'utf-8-sig': ('utf-8', (b'\xef\xbb\xbf',)),
    'utf-16-le': ('utf-16-le', (b'\xff\xfe',)),
    'utf-16-be': ('utf-16-be', (b'\xfe\xff',)),
    'gb18030': ('gb18030', ()),
    'big5': ('big5', ()),
    'cp1252': ('cp1252', ()),
}
# Only stateless, byte-order-free-of-escape encodings are admitted. ISO-2022 style
# shift encodings and encodings with ambiguous canonical forms are refused.


class TextError(FormatError):
    """Stable ``code`` plus the absolute byte ``offset``; never contains content."""


@dataclass(frozen=True)
class Limits:
    chunk_chars: int = 16384
    read_size: int = 1 << 16


class Chunk:
    __slots__ = ('text', 'offsets', 'start', 'end', 'first_line')

    def __init__(self, text, offsets, first_line):
        self.text = text
        self.offsets = offsets
        self.start = offsets[0]
        self.end = offsets[-1]
        self.first_line = first_line  # 0-based line number of the first code point

    def __repr__(self):  # no text: chunks may hold sensitive values
        return f'Chunk({self.start}-{self.end}, chars={len(self.text)})'


_OPPOSITE_BOM = {'utf-16-le': b'\xfe\xff', 'utf-16-be': b'\xff\xfe'}


def _offsets(codec, text, consumed, origin):
    """Absolute byte offset of every code point of ``text`` decoded from ``consumed``."""
    if codec == 'utf-8':  # a code point starts at every byte that is not a continuation
        return [origin + i for i, byte in enumerate(consumed) if byte & 0xC0 != 0x80]
    if text.isascii() and codec not in ('utf-16-le', 'utf-16-be'):
        return list(range(origin, origin + len(text)))
    offsets, at = [], origin
    for ch in text:
        offsets.append(at)
        at += len(ch.encode(codec))
    return offsets


def scan(source: BinaryIO, encoding: str = 'utf-8', limits: Limits = Limits()) -> Iterator[Chunk]:
    if encoding not in ENCODINGS:
        raise TextError('unsupported_encoding', 0)
    codec, boms = ENCODINGS[encoding]
    decoder = codecs.getincrementaldecoder(codec)('strict')
    base = 0            # absolute offset of the first byte not yet turned into code points
    held = b''          # bytes the decoder is holding for an incomplete sequence
    text_buf, offsets = '', array('Q')
    line, after_cr, first_line = 0, False, 0
    head = b''          # leading bytes collected for BOM detection
    started = False
    while True:
        block = source.read(limits.read_size)
        eof = not block
        if not started:
            head += block
            if len(head) < 3 and not eof:
                continue
            started = True
            if codec in _OPPOSITE_BOM and head.startswith(_OPPOSITE_BOM[codec]):
                raise TextError('bom_mismatch', 0)
            for bom in boms:
                if head.startswith(bom):
                    head, base = head[len(bom):], len(bom)
                    break
            block = head
        try:
            text = decoder.decode(block, final=eof)
        except UnicodeDecodeError as exc:
            raise TextError('invalid_encoding', base + exc.start) from None
        data = held + block
        held = b'' if eof else decoder.getstate()[0]
        consumed = data[:len(data) - len(held)]
        if text:
            if text.encode(codec) != consumed:
                raise TextError('non_canonical_encoding', base)
            offs = _offsets(codec, text, consumed, base)
            if len(offs) != len(text):
                raise TextError('invalid_encoding', base)
            base += len(consumed)
            offsets.extend(offs)
            text_buf += text
            while len(text_buf) >= limits.chunk_chars:
                cut = limits.chunk_chars
                piece = array('Q', offsets[:cut])
                piece.append(offsets[cut] if len(offsets) > cut else base)
                yield Chunk(text_buf[:cut], piece, first_line)
                line, after_cr = _count_lines(text_buf[:cut], line, after_cr)
                first_line = line
                text_buf, offsets = text_buf[cut:], offsets[cut:]
        if eof:
            break
    if text_buf:
        offsets.append(base)
        yield Chunk(text_buf, offsets, first_line)


def _count_lines(text, line, after_cr):
    """Advance a 0-based line counter over text; \\r\\n, \\n and \\r each end one line."""
    n = text.count('\n') + text.count('\r') - text.count('\r\n')
    if after_cr and text.startswith('\n'):
        n -= 1
    return line + n, text.endswith('\r')


def range_edit(chunk: Chunk, first: int, last: int, replacement: str, encoding: str = 'utf-8') -> Edit:
    """Edit replacing code points [first, last) of a chunk with ``replacement``."""
    if not 0 <= first <= last < len(chunk.offsets):
        raise TextError('bad_range', 0)
    codec = ENCODINGS[encoding][0]
    try:
        data = replacement.encode(codec)
    except UnicodeEncodeError:
        raise TextError('unencodable_text', chunk.offsets[first]) from None
    return Edit(chunk.offsets[first], chunk.offsets[last], data)


def rewrite(source: BinaryIO, sink: BinaryIO, edits, block: int = 1 << 20) -> int:
    try:
        return byteedit.rewrite(source, sink, edits, block)
    except FormatError as exc:
        raise TextError(exc.code, exc.offset) from None
