"""XLSX value-subset package operations (M0 prototype, not a format admission).

The subset: workbook, worksheets, one shared-strings part, and styles/theme
copied unchanged. Every sheet is processed whatever its visibility state. Any
other part (properties, comments, drawings, tables, pivots, connections,
custom XML...), any external relationship, defined names, formulas and phonetic
text stop the export: a gap is never reported as harmless. Edited shared-string
cells become inline strings; entries left without references are emptied (their
index stays valid) so no original value survives in an orphaned entry.
"""
from __future__ import annotations

import posixpath
import zipfile
from array import array
from dataclasses import dataclass
from typing import BinaryIO, Callable

from lxml import etree
from tacet_plugin_sdk import opc
from tacet_plugin_sdk.opc import CT, REL, Inventory, PackageError
from tacet_plugin_sdk.safezip import Archive, Limits

from .cells import (SHEET_OUTER, STRINGS_OUTER, Cell, CellChange, Edit, SharedString, apply_cell_change,
                    apply_string_edits, blank_string, describe_cell, describe_string)
from .xmlunits import M, XMLLimits, XMLUnitError, rewrite_units, scan_units

WORKBOOK_TYPE = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"
SHEET_TYPE = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"
STRINGS_TYPE = "application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"
STRUCTURE_TYPES = {"application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml",
                   "application/vnd.openxmlformats-officedocument.theme+xml"}
R = "{http://schemas.openxmlformats.org/officeDocument/2006/relationships}"
_ROLES = {WORKBOOK_TYPE: "workbook", SHEET_TYPE: "sheet", STRINGS_TYPE: "strings"}
_WORKBOOK_TAGS = {M + n for n in ("workbook", "fileVersion", "workbookPr", "bookViews", "workbookView",
                                  "sheets", "sheet", "calcPr")}
MAX_SHEETS = 1000
MAX_STRINGS = 20_000_000


@dataclass(frozen=True)
class SheetInfo:
    name: str
    sheet_id: str
    state: str   # visible | hidden | veryHidden
    part: str


@dataclass(frozen=True)
class Report:
    sheets: tuple[SheetInfo, ...]
    cells: int
    cells_changed: int
    strings: int
    strings_edited: int
    strings_blanked: int
    parts_removed: tuple[str, ...] = ()


def _classify(name: str, content_type: str) -> str:
    if content_type in STRUCTURE_TYPES:
        return "structure"
    return _ROLES.get(content_type, "uninspected")


def inventory(archive: Archive) -> Inventory:
    return opc.inventory(archive, prefix="XLSX", main_type=WORKBOOK_TYPE, classify=_classify)


def sheets(archive: Archive, inv: Inventory) -> tuple[SheetInfo, ...]:
    """Sheets of the workbook; any workbook construct this subset cannot carry is an error."""
    main = next(p for p in inv.parts if p.role == "workbook")
    if sum(p.role == "workbook" for p in inv.parts) != 1:
        raise PackageError("XLSX_MAIN_PART_INVALID")
    chunks, size = [], 0
    with archive.open(main.name) as stream:
        while chunk := stream.read(64 * 1024):
            size += len(chunk)
            if size > 2 * 1024 * 1024:
                raise PackageError("XLSX_WORKBOOK_UNSUPPORTED")
            chunks.append(chunk)
    data = b"".join(chunks)
    if b"<!DOCTYPE" in data[:4096] + data[-4096:] or b"<!ENTITY" in data:
        raise PackageError("XLSX_WORKBOOK_UNSUPPORTED")
    try:
        root = etree.fromstring(data, etree.XMLParser(resolve_entities=False, load_dtd=False, no_network=True,
                                                      huge_tree=False, remove_comments=False, remove_pis=False))
    except etree.XMLSyntaxError:
        raise PackageError("XLSX_WORKBOOK_UNSUPPORTED") from None
    if any(not isinstance(node.tag, str) or node.tag not in _WORKBOOK_TAGS for node in root.iter()):
        raise PackageError("XLSX_WORKBOOK_POLICY_REQUIRED")  # defined names, external refs, pivots, comments...
    targets = {r.identifier: r.target for r in inv.relationships
               if r.source == main.name and r.kind.endswith("/worksheet") and not r.external}
    found, names, used = [], set(), set()
    for node in root.iter(M + "sheet"):
        name, sheet_id, rid = node.get("name", ""), node.get("sheetId", ""), node.get(R + "id", "")
        state = node.get("state", "visible")
        part = targets.get(rid)
        if (not name or not sheet_id or part is None or state not in ("visible", "hidden", "veryHidden")
                or name in names or part in used or len(found) >= MAX_SHEETS):
            raise PackageError("XLSX_SHEET_INVALID")
        names.add(name)
        used.add(part)
        found.append(SheetInfo(name, sheet_id, state, part))
    if not found:
        raise PackageError("XLSX_SHEET_INVALID")
    return tuple(found)


PROPERTY_SUFFIXES = ("/core-properties", "/extended-properties", "/custom-properties", "/thumbnail")


def _property_parts(inv: Inventory) -> frozenset[str]:
    """Metadata parts (author, company, custom fields, thumbnail) reached only from the package root."""
    candidates = {r.target for r in inv.relationships
                  if not r.source and not r.external and r.kind.endswith(PROPERTY_SUFFIXES)}
    if any(r.target in candidates and r.source for r in inv.relationships):
        raise PackageError("XLSX_PROPERTY_PART_SHARED")  # also referenced from elsewhere: not removable here
    roles = {p.name: p.role for p in inv.parts}
    if any(roles[name] != "uninspected" for name in candidates):
        raise PackageError("XLSX_PROPERTY_PART_SHARED")
    return frozenset(candidates)


def _gate(archive: Archive, inv: Inventory, strip: frozenset[str]) -> tuple[tuple[SheetInfo, ...], str | None]:
    if any(p.role == "uninspected" and p.name not in strip for p in inv.parts):
        raise PackageError("XLSX_UNINSPECTED_PART_POLICY_REQUIRED")
    if any(r.external for r in inv.relationships):
        raise PackageError("XLSX_EXTERNAL_RELATIONSHIP_POLICY_REQUIRED")
    listed = sheets(archive, inv)
    if {p.name for p in inv.parts if p.role == "sheet"} != {s.part for s in listed}:
        raise PackageError("XLSX_ORPHAN_SHEET")  # a worksheet part no workbook entry reaches
    strings = [p.name for p in inv.parts if p.role == "strings"]
    if len(strings) > 1:
        raise PackageError("XLSX_STRINGS_AMBIGUOUS")
    return listed, (strings[0] if strings else None)


def _read_small(stream, limit: int = 2 * 1024 * 1024) -> bytes:
    chunks, size = [], 0
    while chunk := stream.read(64 * 1024):
        size += len(chunk)
        if size > limit:
            raise PackageError("XLSX_METADATA_LIMIT")
        chunks.append(chunk)
    return b"".join(chunks)


def _without(data: bytes, tag: str, attribute: str, drop: set[str], root_dir: str = "") -> bytes:
    """Remove the children whose ``attribute`` names a dropped part; everything else is kept."""
    root = etree.fromstring(data, etree.XMLParser(resolve_entities=False, load_dtd=False, no_network=True,
                                                  huge_tree=False))
    for node in list(root):
        value = node.get(attribute, "")
        name = value[1:] if value.startswith("/") else posixpath.normpath(posixpath.join(root_dir, value))
        if node.tag == tag and name in drop:
            root.remove(node)
    return etree.tostring(root, xml_declaration=True, encoding="UTF-8", standalone=True)


def rewrite_value_subset(source: BinaryIO, candidate: BinaryIO,
                         cell_policy: Callable[[SheetInfo, Cell], CellChange | None],
                         string_policy: Callable[[SharedString], list[Edit] | None] | None = None,
                         limits: Limits = Limits(), xml_limits: XMLLimits = XMLLimits(),
                         strip_properties: bool = False) -> Report:
    """Write a private candidate; any exception means the candidate must be discarded.

    ``cell_policy`` sees every cell (shared-string cells carry only the index);
    ``string_policy`` sees every unique shared string once, so a value used by a
    million cells is detected once. ``strip_properties`` is the caller's explicit
    authorization to drop document properties (author, company, custom fields,
    thumbnail) with their content-type and relationship entries; without it such
    parts stop the export. The candidate is not authorized for delivery
    until the host has validated it independently.
    """
    archive = Archive(source, limits)
    try:
        inv = inventory(archive)
        strip = _property_parts(inv) if strip_properties else frozenset()
        listed, strings_part = _gate(archive, inv, strip)
        by_part = {s.part: s for s in listed}
        total = 0
        if strings_part:
            with archive.open(strings_part) as stream:
                total = scan_units(stream, M + "si", STRINGS_OUTER,
                                   lambda element, i: describe_string(element, i), xml_limits)
            if total > MAX_STRINGS:
                raise PackageError("XLSX_STRINGS_LIMIT")
        refs = array("I", bytes(4 * total))
        cells = 0

        def count(element, _ordinal):
            nonlocal cells
            cell = describe_cell(element)
            cells += 1
            if cell.formula:
                raise XMLUnitError("XLSX_FORMULA_POLICY_REQUIRED")
            if cell.kind == "shared":
                if cell.shared_index >= total:
                    raise XMLUnitError("XLSX_SHARED_INDEX_INVALID")
                refs[cell.shared_index] += 1

        for sheet in listed:
            with archive.open(sheet.part) as stream:
                scan_units(stream, M + "c", SHEET_OUTER, count, xml_limits)

        changed = edited = blanked = 0
        with zipfile.ZipFile(candidate, "w", compression=zipfile.ZIP_DEFLATED, allowZip64=False) as output:
            ordered = [p for p in inv.parts if p.name != strings_part and p.name not in strip]
            ordered += [p for p in inv.parts if p.name == strings_part]  # needs final reference counts
            for part in ordered:
                with archive.open(part.name) as input_part, output.open(part.name, "w", force_zip64=False) as out:
                    if part.role == "content_types" and strip:
                        out.write(_without(_read_small(input_part), CT + "Override", "PartName", set(strip)))
                    elif part.name == "_rels/.rels" and strip:
                        out.write(_without(_read_small(input_part), REL + "Relationship", "Target", set(strip)))
                    elif part.role == "sheet":
                        sheet = by_part[part.name]

                        def transform(element, _ordinal, sheet=sheet):
                            nonlocal changed
                            cell = describe_cell(element)
                            change = cell_policy(sheet, cell)
                            if change is None:
                                return
                            apply_cell_change(element, cell, change)
                            changed += 1
                            if cell.kind == "shared":
                                refs[cell.shared_index] -= 1

                        rewrite_units(input_part, out, M + "c", SHEET_OUTER, transform, xml_limits)
                    elif part.role == "strings":
                        def transform_string(element, ordinal):
                            nonlocal edited, blanked
                            entry = describe_string(element, ordinal)
                            edits = string_policy(entry) if string_policy else None
                            if edits:
                                apply_string_edits(element, entry, list(edits))
                                edited += 1
                            if refs[ordinal] == 0 and (entry.text or edits):
                                blank_string(element)
                                blanked += 1

                        remaining = sum(refs)

                        def root_hook(attributes, remaining=remaining):
                            if "count" in attributes:
                                attributes["count"] = str(remaining)
                            return attributes

                        rewrite_units(input_part, out, M + "si", STRINGS_OUTER, transform_string,
                                      xml_limits, root_hook)
                    else:
                        while chunk := input_part.read(64 * 1024):
                            out.write(chunk)
        return Report(listed, cells, changed, total, edited, blanked, tuple(sorted(strip)))
    except XMLUnitError as exc:
        raise PackageError(str(exc)) from None
    finally:
        archive.close()
