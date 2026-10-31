"""Cell and shared-string model for the XLSX value subset (M0 prototype).

Text locations are Unicode code points of the concatenated ``t`` runs of one
string. Everything this subset cannot represent exactly is refused instead of
being copied unseen: formulas, phonetic runs, Excel ``_xHHHH_`` escapes,
unknown cell attributes and children. This is not a format admission decision.
"""
from __future__ import annotations

import re
from dataclasses import dataclass

from lxml import etree

from .xmlunits import M, XML_SPACE, XMLUnitError

SHEET_OUTER = {M + name for name in (
    "worksheet", "sheetPr", "tabColor", "pageSetUpPr", "outlinePr", "dimension", "sheetViews", "sheetView",
    "selection", "pane", "sheetFormatPr", "cols", "col", "sheetData", "row", "mergeCells", "mergeCell",
    "pageMargins", "printOptions")}
STRINGS_OUTER = {M + "sst"}

_ESCAPE = re.compile(r"_x[0-9A-Fa-f]{4}_")
_CELL_ATTRIBUTES = {"r", "s", "t"}
_CELL_TYPES = {None: "number", "n": "number", "s": "shared", "inlineStr": "inline", "b": "bool",
               "e": "error", "d": "date", "str": "formula"}


@dataclass(frozen=True)
class Cell:
    ref: str
    kind: str            # number | shared | inline | bool | error | date | formula
    text: str | None     # number/date: the raw value text; inline: concatenated runs; shared: None
    shared_index: int | None
    formula: bool


@dataclass(frozen=True)
class CellChange:
    """Replacement of a whole cell value. ``text=None`` clears the cell.

    A numeric or date cell becomes a string (or empty), so the caller must say
    so with ``allow_type_change``; it is the caller's explicit authorization.
    """
    text: str | None
    allow_type_change: bool = False


@dataclass(frozen=True)
class TextSegment:
    node: int
    start: int
    end: int


@dataclass(frozen=True)
class SharedString:
    index: int
    text: str
    segments: tuple[TextSegment, ...]


@dataclass(frozen=True)
class Edit:
    start: int
    end: int
    replacement: str


def _clean(value: str, code: str) -> str:
    if _ESCAPE.search(value):
        raise XMLUnitError(code)
    return value


def _runs(element, root_tag, code):
    """Yield the ``t`` nodes of an ``is`` / ``si`` element, refusing anything unmodelled."""
    nodes = []
    for child in element:
        if child.tag == M + "t":
            if len(child):
                raise XMLUnitError(code)
            nodes.append(child)
        elif child.tag == M + "r":
            for part in child:
                if part.tag == M + "t" and not len(part):
                    nodes.append(part)
                elif part.tag != M + "rPr":  # formatting only; its subtree carries no text
                    raise XMLUnitError(code)
        else:  # rPh, phoneticPr and anything else
            raise XMLUnitError(code)
    return nodes


def describe_cell(element: etree._Element) -> Cell:
    if element.tag != M + "c" or set(element.attrib) - _CELL_ATTRIBUTES:
        raise XMLUnitError("XLSX_CELL_UNSUPPORTED")
    ref = element.get("r", "")
    if not ref:
        raise XMLUnitError("XLSX_CELL_UNSUPPORTED")
    declared = element.get("t")
    if declared not in _CELL_TYPES:
        raise XMLUnitError("XLSX_CELL_UNSUPPORTED")
    formula = False
    value = None
    inline = None
    for child in element:
        if child.tag == M + "f":
            formula = True
        elif child.tag == M + "v" and not len(child) and value is None:
            value = child.text or ""
        elif child.tag == M + "is" and inline is None:
            inline = child
        else:
            raise XMLUnitError("XLSX_CELL_UNSUPPORTED")
    if formula or declared == "str":
        return Cell(ref, "formula", None, None, True)
    kind = _CELL_TYPES[declared]
    if kind == "shared":
        if inline is not None or value is None or not value.isascii() or not value.isdigit():
            raise XMLUnitError("XLSX_CELL_UNSUPPORTED")
        return Cell(ref, kind, None, int(value), False)
    if kind == "inline":
        if value is not None or inline is None:
            raise XMLUnitError("XLSX_CELL_UNSUPPORTED")
        text = "".join(node.text or "" for node in _runs(inline, M + "is", "XLSX_INLINE_UNSUPPORTED"))
        return Cell(ref, kind, _clean(text, "XLSX_ESCAPE_POLICY_REQUIRED"), None, False)
    if inline is not None:
        raise XMLUnitError("XLSX_CELL_UNSUPPORTED")
    if kind in ("number", "date"):
        return Cell(ref, kind, value, None, False)  # an absent value is an empty styled cell
    return Cell(ref, kind, value, None, False)


def _check_text(text: str) -> None:
    if any((ord(c) < 32 and c not in "\t\n\r") or 0xD800 <= ord(c) <= 0xDFFF or ord(c) in (0xFFFE, 0xFFFF)
           for c in text) or _ESCAPE.search(text):
        raise XMLUnitError("XLSX_INVALID_REPLACEMENT")


def apply_cell_change(element: etree._Element, cell: Cell, change: CellChange) -> None:
    """Rewrite the cell as an inline string (or empty), keeping reference and style."""
    if cell.kind in ("formula", "bool", "error"):
        raise XMLUnitError("XLSX_CELL_KIND_POLICY_REQUIRED")
    if cell.kind in ("number", "date") and not change.allow_type_change:
        raise XMLUnitError("XLSX_TYPE_CHANGE_POLICY_REQUIRED")
    if change.text is not None:
        _check_text(change.text)
    for child in list(element):
        element.remove(child)
    if "t" in element.attrib:
        del element.attrib["t"]
    if change.text is None:
        return
    element.set("t", "inlineStr")
    holder = etree.SubElement(element, M + "is")
    run = etree.SubElement(holder, M + "t")
    run.text = change.text
    if change.text and (change.text[0].isspace() or change.text[-1].isspace()):
        run.set(XML_SPACE, "preserve")


def describe_string(element: etree._Element, index: int) -> SharedString:
    if element.tag != M + "si" or element.attrib:
        raise XMLUnitError("XLSX_STRING_UNSUPPORTED")
    segments, text, position = [], [], 0
    for node_index, node in enumerate(_runs(element, M + "si", "XLSX_STRING_UNSUPPORTED")):
        value = node.text or ""
        segments.append(TextSegment(node_index, position, position + len(value)))
        text.append(value)
        position += len(value)
    return SharedString(index, _clean("".join(text), "XLSX_ESCAPE_POLICY_REQUIRED"), tuple(segments))


def apply_string_edits(element: etree._Element, entry: SharedString, edits: list[Edit]) -> None:
    """Change ``t`` text only, keeping run nodes and formatting; right to left."""
    ordered = sorted(edits, key=lambda e: (e.start, e.end))
    previous = -1
    for edit in ordered:
        if edit.start < 0 or edit.start >= edit.end or edit.end > len(entry.text):
            raise XMLUnitError("XLSX_INVALID_SPAN")
        if edit.start < previous:
            raise XMLUnitError("XLSX_OVERLAPPING_EDITS")
        previous = edit.end
        _check_text(edit.replacement)
    nodes = _runs(element, M + "si", "XLSX_STRING_UNSUPPORTED")
    for edit in reversed(ordered):
        selected = [s for s in entry.segments if s.end > edit.start and s.start < edit.end]
        if not selected:
            raise XMLUnitError("XLSX_UNRELIABLE_LOCATION")
        for i, segment in enumerate(selected):
            node = nodes[segment.node]
            value = node.text or ""
            left = max(0, edit.start - segment.start)
            right = min(segment.end, edit.end) - segment.start
            node.text = value[:left] + (edit.replacement if i == 0 else "") + value[right:]
            if node.text and (node.text[0].isspace() or node.text[-1].isspace()):
                node.set(XML_SPACE, "preserve")


def blank_string(element: etree._Element) -> None:
    """Empty an unreferenced entry, keeping its position so every index stays valid."""
    for child in list(element):
        element.remove(child)
    etree.SubElement(element, M + "t")
