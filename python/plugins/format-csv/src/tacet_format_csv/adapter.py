"""CSV / TSV files as the unit-based protocol sees them.

A unit is one cell, numbered by the running cell count of the file (empty cells and blank lines'
no-cells included in the count of cells that exist). A replacement never changes how many cells there are,
so every ordinal stays where it was, which is what lets validation find the same cell in the candidate.
A cell is replaced as a whole: the new value is built from the cell's decoded text with the operations
applied and re-quoted the way the original was.
"""
from __future__ import annotations

from tacet_plugin_sdk.unitkit import Adapter, Unit, apply_ops

from .scanner import Cell, CsvError, Dialect, cell_edit, scan

_ENCODINGS = ('utf-8', 'gb18030')
_DELIMITERS = (',', ';', '\t', '|')
_HEAD = 1 << 16


def _first_record_counts(source):
    head = source.read(_HEAD)
    source.seek(0)
    line = head.split(b'\n', 1)[0]
    counts = {}
    inside = False
    for d in _DELIMITERS:
        n, inside = 0, False
        for byte in line:
            if byte == 0x22:
                inside = not inside
            elif not inside and byte == ord(d):
                n += 1
        counts[d] = n
    return counts


def _complete(source, dialect) -> bool:
    source.seek(0)
    try:
        cells = 0
        for event in scan(source, dialect):
            if isinstance(event, Cell):
                cells += 1
                if '\x00' in event.text:
                    return False
        return cells > 0
    except CsvError:
        return False
    finally:
        source.seek(0)


def detect_dialect(source):
    counts = _first_record_counts(source)
    best = max(_DELIMITERS, key=lambda d: counts[d])  # on a tie the first of _DELIMITERS wins: the comma
    for encoding in _ENCODINGS:
        dialect = Dialect(delimiter=best, encoding=encoding)
        if _complete(source, dialect):
            return dialect
    return None


class CsvAdapter(Adapter):
    format = 'csv'
    content_type = 'text/csv'

    def __init__(self, dialect):
        self.dialect = dialect

    def units(self, source):
        source.seek(0)
        n = 0
        for event in scan(source, self.dialect):
            if isinstance(event, Cell):
                n += 1
                yield Unit('content', n, event.text, None, event)

    def edits(self, unit, ops):
        cell = unit.ref
        return [cell_edit(cell, apply_ops(cell.text, ops), self.dialect)]


def open_adapter(source):
    dialect = detect_dialect(source)
    return CsvAdapter(dialect) if dialect else None
