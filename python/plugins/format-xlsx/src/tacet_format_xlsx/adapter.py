"""XLSX (value subset) as the unit-based protocol sees them.

A unit is one cell holding text, addressed ``(sheet part, cell ordinal)`` where the ordinal counts every
cell of that sheet in file order (so an edit never moves another cell's ordinal). A shared-string cell
shows the text of its shared string; editing it turns that cell into an inline string, and shared
strings nobody refers to any more are emptied, so no original value survives in an orphaned entry.
Number cells of seven digits or more are reported as read-only units (``<sheet>#numbers``): a phone or
identity number stored as a number is found, but turning it into text changes the cell's type, which
needs the plan's explicit authorization, so such operations are refused with a reason code.

Anything the value subset cannot carry (formulas, comments, drawings, tables, defined names, external
links, uninspected parts other than document properties) makes the file not admitted: the adapter does
not open it. Document properties (author, company, custom fields, thumbnail) are reported as parts that
were not inspected and may be removed by a ``part`` operation, all together or not at all.
"""
from __future__ import annotations

from tacet_plugin_sdk.byteedit import FormatError
from tacet_plugin_sdk.opc import PackageError
from tacet_plugin_sdk.safezip import Archive, ArchiveError
from tacet_plugin_sdk.unitkit import Adapter, Unit, apply_ops

from .cells import SHEET_OUTER, STRINGS_OUTER, CellChange, describe_cell, describe_string
from .workbook import _gate, _property_parts, inventory, rewrite_value_subset
from .xmlunits import M, XMLUnitError, scan_units

MIN_NUMBER_DIGITS = 7
MAX_STRING_CHARS = 64 << 20
SHEET_TYPE = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'


def _fail(error) -> FormatError:
    code = str(error).split(':')[0].strip().lower().replace(' ', '_') or 'xlsx_invalid'
    return FormatError(code, 0)


class XlsxAdapter(Adapter):
    format = 'xlsx'
    content_type = SHEET_TYPE

    def __init__(self, sheets, strings_part, properties):
        self._sheets, self._strings_part, self._properties = sheets, strings_part, frozenset(properties)
        self.editable = frozenset(s.part for s in sheets)
        self.removable = self._properties
        self.virtual = frozenset(f'{s.part}#numbers' for s in sheets)

    def _archive(self, source):
        try:
            source.seek(0)
            return Archive(source)
        except ArchiveError as error:
            raise _fail(error) from None

    def enumerate(self, source):
        archive = self._archive(source)
        try:
            out = []
            for p in inventory(archive).parts:
                cls, state = ('text', 'inspected') if p.role in ('sheet', 'strings') else ('other', 'inspected')
                if p.name in self._properties or (p.role == 'uninspected'):
                    cls, state = 'unknown', 'not_inspected'
                out.append((p.name, p.content_type or 'application/octet-stream', cls, state))
            return out
        except PackageError as error:
            raise _fail(error) from None
        finally:
            archive.close()

    def also_rewritten(self, touched):
        return frozenset({self._strings_part}) if self._strings_part else frozenset()

    def _shared(self, archive):
        if not self._strings_part:
            return []
        texts, total = [], 0

        def take(element, index):
            nonlocal total
            text = describe_string(element, index).text
            total += len(text)
            if total > MAX_STRING_CHARS:
                raise XMLUnitError('XLSX_STRINGS_LIMIT')
            texts.append(text)

        with archive.open(self._strings_part) as stream:
            scan_units(stream, M + 'si', STRINGS_OUTER, take)
        return texts

    def units(self, source):
        archive = self._archive(source)
        try:
            texts = self._shared(archive)
            for sheet in self._sheets:
                found: list[Unit] = []
                n = 0

                def visit(element, _i, sheet=sheet):
                    nonlocal n
                    n += 1
                    cell = describe_cell(element)
                    if cell.formula:
                        raise XMLUnitError('XLSX_FORMULA_POLICY_REQUIRED')
                    if cell.kind == 'shared':
                        if cell.shared_index >= len(texts):
                            raise XMLUnitError('XLSX_SHARED_INDEX_INVALID')
                        found.append(Unit(sheet.part, n, texts[cell.shared_index], None, cell))
                    elif cell.kind == 'inline':
                        found.append(Unit(sheet.part, n, cell.text or '', None, cell))
                    elif cell.kind == 'number' and cell.text and sum(c.isdigit() for c in cell.text) >= MIN_NUMBER_DIGITS:
                        found.append(Unit(f'{sheet.part}#numbers', n, cell.text, None, cell))

                with archive.open(sheet.part) as stream:
                    scan_units(stream, M + 'c', SHEET_OUTER, visit)
                yield from found  # one sheet's cells are held at a time
        except (XMLUnitError, PackageError, ArchiveError) as error:
            raise _fail(error) from None
        finally:
            archive.close()

    def edits(self, unit, ops):
        return [(unit.part, unit.ordinal, apply_ops(unit.text, ops))]

    def apply(self, source, sink, edits, removals):
        plan = {(part, n): text for part, n, text in edits}
        counters: dict[str, int] = {}

        def policy(sheet, _cell):
            n = counters[sheet.part] = counters.get(sheet.part, 0) + 1
            text = plan.get((sheet.part, n))
            return None if text is None else CellChange(text)

        if removals and set(removals) != self._properties:
            raise FormatError('property_parts_all_or_none', 0)
        try:
            source.seek(0)
            rewrite_value_subset(source, sink, policy, strip_properties=bool(removals))
        except (PackageError, ArchiveError, XMLUnitError) as error:
            raise _fail(error) from None


def open_adapter(source):
    try:
        source.seek(0)
        archive = Archive(source)
    except ArchiveError:
        return None
    try:
        inv = inventory(archive)
        props = _property_parts(inv)
        sheets, strings = _gate(archive, inv, props)
    except (PackageError, ArchiveError, XMLUnitError):
        return None
    finally:
        archive.close()
        source.seek(0)
    adapter = XlsxAdapter(sheets, strings, props)
    try:
        for _ in adapter.units(source):  # the whole workbook must read cleanly before it is claimed
            pass
    except FormatError:
        return None
    finally:
        source.seek(0)
    return adapter
