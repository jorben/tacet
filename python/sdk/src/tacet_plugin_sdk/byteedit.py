"""Byte-span edits shared by the streaming format plugins.

A format plugin reports byte spans for the values it found; the caller supplies
sorted, non-overlapping edits and ``rewrite`` copies every other byte unchanged.
Errors carry a stable code and a byte offset, never content.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import BinaryIO, Iterable


class FormatError(ValueError):
    """Stable ``code`` plus the absolute byte ``offset``; never contains content."""

    def __init__(self, code: str, offset: int):
        super().__init__(f'{code} at byte {offset}')
        self.code = code
        self.offset = offset


@dataclass(frozen=True)
class Edit:
    start: int
    end: int
    replacement: bytes


def rewrite(source: BinaryIO, sink: BinaryIO, edits: Iterable[Edit], block: int = 1 << 20) -> int:
    """Copy source to sink replacing exactly the edited byte spans. Returns bytes read."""
    position = 0
    for edit in edits:
        if edit.start < position or edit.end < edit.start:
            raise FormatError('overlapping_edits', edit.start)
        position = _advance(source, sink, position, edit.start, block)
        position = _advance(source, None, position, edit.end, block)
        sink.write(edit.replacement)
    while True:
        data = source.read(block)
        if not data:
            return position
        sink.write(data)
        position += len(data)


def _advance(source, sink, position, target, block):
    while position < target:
        data = source.read(min(block, target - position))
        if not data:
            raise FormatError('edit_beyond_source', target)
        if sink is not None:
            sink.write(data)
        position += len(data)
    return position
