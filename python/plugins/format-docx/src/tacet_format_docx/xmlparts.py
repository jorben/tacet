"""M0 WordprocessingML streaming prototype: explicit code-point locations.

This module handles bounded paragraph text, not DOCX package admission. Headers,
relationships, media, revision policy and input/output reconciliation belong to
separate package operations; they must be implemented before production use.
"""
from __future__ import annotations

import re
from dataclasses import dataclass
from typing import BinaryIO, Callable

from lxml import etree

W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
XML_SPACE = "{http://www.w3.org/XML/1998/namespace}space"
# Only this tiny vocabulary is allowed through the M0 writer. Everything else
# needs package policy, source mapping or coverage handling before it is copied.
_SUBSET_TAGS = {W + name for name in ("document", "body", "p", "r", "rPr", "b", "i", "t")}
# Layout vocabulary: tables, paragraph alignment and section geometry carry no
# text, so they are copied unchanged, but only with attributes of a known shape.
_LAYOUT = {
    "tbl": {}, "tr": {}, "tc": {}, "tblPr": {}, "tblGrid": {}, "tcPr": {}, "pPr": {}, "sectPr": {},
    "tblW": {"w": "digits", "type": {"dxa", "auto", "pct", "nil"}},
    "tcW": {"w": "digits", "type": {"dxa", "auto", "pct", "nil"}},
    "gridCol": {"w": "digits"},
    "jc": {"val": {"left", "right", "center", "both", "start", "end"}},
    "pgSz": {"w": "digits", "h": "digits", "orient": {"portrait", "landscape"}},
    "pgMar": {n: "digits" for n in ("top", "right", "bottom", "left", "header", "footer", "gutter")},
    # Page markers and paragraph styles carry no text either: they are what review locations are read from
    # (locate.py), so a document saved by Word keeps them. A style is named by its id only.
    "pStyle": {"val": "style"},
    "outlineLvl": {"val": {str(n) for n in range(10)}},
    "pageBreakBefore": {"val": {"0", "1", "true", "false", "on", "off"}},
    "lastRenderedPageBreak": {},
    # a list item names its list and level; the number text lives in numbering.xml (structure.py)
    "numPr": {}, "ilvl": {"val": "digits"}, "numId": {"val": "digits"},
    "br": {"type": {"page"}},  # and only that: a line break inside text is a boundary the mapping lacks
    "type": {"val": {"nextPage", "continuous", "evenPage", "oddPage", "nextColumn"}},
    # Formatting Word writes into every document (requirements 4.3: structure kept, formatting not broken): sizes,
    # colours, shading, cell geometry. Each value has a closed shape; none is a free string.
    "color": {"val": "colour"},
    "sz": {"val": "digits"}, "szCs": {"val": "digits"},
    "rFonts": {"hint": {"default", "eastAsia", "cs"}},
    "shd": {"val": {"clear", "solid", "nil", "pct10", "pct20", "pct25", "pct50"}, "color": "colour", "fill": "colour"},
    "vAlign": {"val": {"top", "center", "bottom", "both"}},
    "tblCellMar": {}, "tcMar": {}, "tblPrEx": {}, "trPr": {},
    "top": {"w": "digits", "type": {"dxa", "nil", "auto", "pct"}},
    "bottom": {"w": "digits", "type": {"dxa", "nil", "auto", "pct"}},
    "left": {"w": "digits", "type": {"dxa", "nil", "auto", "pct"}},
    "right": {"w": "digits", "type": {"dxa", "nil", "auto", "pct"}},
    "start": {"w": "digits", "type": {"dxa", "nil", "auto", "pct"}},
    "end": {"w": "digits", "type": {"dxa", "nil", "auto", "pct"}},
    "tblInd": {"w": "int", "type": {"dxa", "nil", "auto", "pct"}},
    "tblLayout": {"type": {"fixed", "autofit"}},
    "tblLook": {"val": "hex4", **{n: {"0", "1"} for n in ("firstRow", "lastRow", "firstColumn", "lastColumn", "noHBand", "noVBand")}},
    "trHeight": {"val": "digits", "hRule": {"auto", "exact", "atLeast"}},
    "cols": {"space": "digits", "num": "digits"},
}
_COLOUR = re.compile(r"^(?:[0-9A-Fa-f]{6}|auto)$")
_HEX4 = re.compile(r"^[0-9A-Fa-f]{4}$")
_HEX8 = re.compile(r"^[0-9A-Fa-f]{8}$")
W14 = "{http://schemas.microsoft.com/office/word/2010/wordml}"
MC = "{http://schemas.openxmlformats.org/markup-compatibility/2006}"
# revision-session ids and paragraph ids Word puts on paragraphs, runs, rows and sections: hex ids only
_IDS = {W + n for n in ("rsidR", "rsidRDefault", "rsidRPr", "rsidP", "rsidTr", "rsidDel", "rsidSect")} | {W14 + "paraId", W14 + "textId"}
_PREFIX = re.compile(r"^[A-Za-z][A-Za-z0-9]{0,15}$")


def _value_ok(rule, value):
    if rule == "digits":
        return value.isascii() and value.isdigit() and len(value) <= 9
    if rule == "int":
        v = value[1:] if value.startswith("-") else value
        return v.isascii() and v.isdigit() and len(v) <= 9
    if rule == "style":
        return bool(_STYLE_ID.match(value))
    if rule == "colour":
        return bool(_COLOUR.match(value))
    if rule == "hex4":
        return bool(_HEX4.match(value))
    return value in rule


def _strip_ids(tag, attributes):
    """The attributes left once the id attributes (checked here) are set aside."""
    rest = {}
    for key, value in attributes.items():
        if key in _IDS:
            if not _HEX8.match(value):
                raise XMLPartError("DOCX_TEXT_SUBSET_UNSUPPORTED")
        elif tag == W + "document" and key == MC + "Ignorable":
            if not all(_PREFIX.match(p) for p in value.split()) or len(value) > 256:
                raise XMLPartError("DOCX_TEXT_SUBSET_UNSUPPORTED")
        else:
            rest[key] = value
    return rest
_STYLE_ID = re.compile(r"^[A-Za-z0-9_-]{1,64}$")
_SUBSET_TAGS |= {W + name for name in _LAYOUT}


def _subset_element(tag, attributes):
    if tag not in _SUBSET_TAGS:
        raise XMLPartError("DOCX_TEXT_SUBSET_UNSUPPORTED")
    attributes = _strip_ids(tag, attributes)
    name = tag[len(W):]
    if name in _LAYOUT:
        rules = _LAYOUT[name]
        for key, value in attributes.items():
            rule = rules.get(key[len(W):]) if key.startswith(W) else None
            if rule is None or not _value_ok(rule, value):
                raise XMLPartError("DOCX_TEXT_SUBSET_UNSUPPORTED")
        if name == "br" and attributes.get(W + "type") != "page":
            raise XMLPartError("DOCX_TEXT_SUBSET_UNSUPPORTED")
        return
    allowed = {XML_SPACE} if tag == W + "t" else ({W + "val"} if tag in {W + "b", W + "i"} else set())
    if set(attributes) - allowed:
        raise XMLPartError("DOCX_TEXT_SUBSET_UNSUPPORTED")
    if XML_SPACE in attributes and attributes[XML_SPACE] not in {"default", "preserve"}:
        raise XMLPartError("DOCX_TEXT_SUBSET_UNSUPPORTED")
    if W + "val" in attributes and attributes[W + "val"] not in {"0", "1", "true", "false", "on", "off"}:
        raise XMLPartError("DOCX_TEXT_SUBSET_UNSUPPORTED")


class XMLPartError(ValueError):
    """Stable, non-sensitive error code."""


@dataclass(frozen=True)
class XMLLimits:
    depth: int = 128
    nodes: int = 2_000_000
    paragraph_nodes: int = 50_000
    paragraph_chars: int = 1024 * 1024
    total_bytes: int = 256 * 1024 * 1024


@dataclass(frozen=True)
class TextSegment:
    node: int
    start: int
    end: int


@dataclass(frozen=True)
class Paragraph:
    ordinal: int
    text: str
    segments: tuple[TextSegment, ...]
    hidden: bool
    revision: bool


@dataclass(frozen=True)
class Edit:
    start: int
    end: int
    replacement: str


def describe(element: etree._Element, ordinal: int) -> Paragraph:
    """Each w:t is a source segment; Python offsets are Unicode code points."""
    text = []
    segments = []
    position = 0
    for index, node in enumerate(element.iter(W + "t")):
        value = node.text or ""
        segments.append(TextSegment(index, position, position + len(value)))
        text.append(value)
        position += len(value)
    # These are separate inspection classes. The caller cannot treat them as
    # visible text without explicit policy; the M0 writer refuses them.
    hidden = any(node.tag == W + "vanish" for node in element.iter())
    revision = any(node.tag in {W + "ins", W + "del", W + "delText", W + "moveFrom", W + "moveTo"}
                   for node in element.iter())
    return Paragraph(ordinal, "".join(text), tuple(segments), hidden, revision)


def apply_edits(element: etree._Element, edits: list[Edit]) -> None:
    """Change w:t values only, retaining run nodes, attributes and run styles.

    Changes refer to the immutable original paragraph. Apply from right to left
    so a length-changing replacement does not move an earlier source location.
    Tabs, fields and breaks are not represented by the w:t mapping: crossing any
    such semantic boundary is refused by the caller's supported-subset check.
    """
    original = describe(element, 0)
    if original.hidden or original.revision:
        raise XMLPartError("DOCX_NONVISIBLE_POLICY_REQUIRED")
    if any(n.tag in {W + "tab", W + "instrText", W + "fldChar", W + "drawing", W + "object"}
           or (n.tag == W + "br" and n.get(W + "type") != "page") for n in element.iter()):
        raise XMLPartError("DOCX_COMPLEX_PARAGRAPH_UNSUPPORTED")
    # where the paragraph's text is broken across pages; an edit may not span such a place
    breaks, offset = [], 0
    for n in element.iter():
        if n.tag == W + "t":
            offset += len(n.text or "")
        elif n.tag == W + "lastRenderedPageBreak" or n.tag == W + "br":
            breaks.append(offset)
    ordered = sorted(edits, key=lambda e: (e.start, e.end))
    previous = -1
    for edit in ordered:
        if edit.start < 0 or edit.start >= edit.end or edit.end > len(original.text):
            raise XMLPartError("DOCX_INVALID_SPAN")
        if edit.start < previous:
            raise XMLPartError("DOCX_OVERLAPPING_EDITS")
        if any(edit.start < b < edit.end for b in breaks):
            raise XMLPartError("DOCX_EDIT_ACROSS_PAGE_BREAK")
        previous = edit.end
        if any((ord(char) < 32 and char not in "\t\n\r") or 0xD800 <= ord(char) <= 0xDFFF
               or ord(char) in {0xFFFE, 0xFFFF} for char in edit.replacement):
            raise XMLPartError("DOCX_INVALID_REPLACEMENT")
    nodes = list(element.iter(W + "t"))
    for edit in reversed(ordered):
        selected = [seg for seg in original.segments if seg.end > edit.start and seg.start < edit.end]
        if not selected:
            raise XMLPartError("DOCX_UNRELIABLE_LOCATION")
        for i, segment in enumerate(selected):
            node = nodes[segment.node]
            value = node.text or ""
            left = max(0, edit.start - segment.start)
            right = min(segment.end, edit.end) - segment.start
            replacement = edit.replacement if i == 0 else ""
            node.text = value[:left] + replacement + value[right:]
            if node.text and (node.text[0].isspace() or node.text[-1].isspace()):
                node.set(XML_SPACE, "preserve")


class _Target:
    """SAX-style lxml target: buffer only one bounded paragraph at a time."""

    def __init__(self, output, on_paragraph, limits):
        self.output = output
        self.on_paragraph = on_paragraph
        self.limits = limits
        self.depth = 0
        self.nodes = 0
        self.paragraph_nodes = 0
        self.paragraph_chars = 0
        self.ordinal = 0
        self.builder = None
        self.paragraph_depth = 0
        self.contexts = []
        self.namespaces = {}
        self.pending_ns = {}
        self.ns_stacks = {}
        self.tags = []
        # (table, row, column) of the innermost table cell outside paragraphs, 1-based; tables are numbered
        # in document order. ``cell`` is the position of the paragraph being read.
        self.tables = 0
        self.cells = []
        self.cell = None

    def start_ns(self, prefix, uri):
        prefix = prefix or None
        self.ns_stacks.setdefault(prefix, []).append(self.namespaces.get(prefix))
        self.namespaces[prefix] = uri
        self.pending_ns[prefix] = uri

    def end_ns(self, prefix):
        prefix = prefix or None
        old = self.ns_stacks[prefix].pop()
        if old is None:
            self.namespaces.pop(prefix, None)
        else:
            self.namespaces[prefix] = old

    def doctype(self, name, public_id, system_id):
        raise XMLPartError("XML_DTD_FORBIDDEN")

    def start(self, tag, attributes):
        if self.output is not None and self.builder is None and tag != W + "p":
            _subset_element(tag, attributes)
        self.tags.append(tag)
        self.depth += 1
        self.nodes += 1
        if self.depth > self.limits.depth or self.nodes > self.limits.nodes:
            raise XMLPartError("XML_STRUCTURE_LIMIT")
        if self.builder is None:
            if tag == W + "tbl":
                self.tables += 1
                self.cells.append([self.tables, 0, 0])
            elif tag == W + "tr" and self.cells:
                self.cells[-1][1] += 1
                self.cells[-1][2] = 0
            elif tag == W + "tc" and self.cells:
                self.cells[-1][2] += 1
        if tag == W + "p":
            if self.builder is not None:
                raise XMLPartError("DOCX_NESTED_PARAGRAPH_UNSUPPORTED")
            self.cell = tuple(self.cells[-1]) if self.cells else None
            self.builder = etree.TreeBuilder()
            self.paragraph_depth = self.depth
            self.paragraph_nodes = 0
            self.paragraph_chars = 0
        if self.builder is not None:
            self.paragraph_nodes += 1
            if self.paragraph_nodes > self.limits.paragraph_nodes:
                raise XMLPartError("DOCX_PARAGRAPH_LIMIT")
            # Use the in-scope namespace map because this detached subtree will
            # be written independently of its streamed ancestors.
            self.builder.start(tag, attributes, nsmap=dict(self.namespaces))
        elif self.output is not None:
            context = self.output.element(tag, attributes, nsmap=self.pending_ns or None)
            context.__enter__()
            self.contexts.append(context)
        self.pending_ns = {}

    def data(self, data):
        if self.output is not None and data.strip() and (not self.tags or self.tags[-1] != W + "t"):
            raise XMLPartError("DOCX_UNMAPPED_TEXT")
        if self.builder is not None:
            self.paragraph_chars += len(data)
            if self.paragraph_chars > self.limits.paragraph_chars:
                raise XMLPartError("DOCX_PARAGRAPH_LIMIT")
            self.builder.data(data)
        elif self.output is not None:
            self.output.write(data)

    def end(self, tag):
        if self.builder is not None:
            self.builder.end(tag)
            if self.depth == self.paragraph_depth:
                element = self.builder.close()
                self.builder = None
                self.ordinal += 1
                self.on_paragraph(element, self.ordinal)
                if self.output is not None:
                    self.output.write(element)
        elif self.output is not None:
            self.contexts.pop().__exit__(None, None, None)
        if self.builder is None and tag == W + "tbl" and self.cells:
            self.cells.pop()
        self.depth -= 1
        self.tags.pop()

    def comment(self, text):
        # XML comments may contain sensitive data; the M0 text-only subset must
        # not silently copy content outside the w:t source map.
        raise XMLPartError("DOCX_XML_COMMENT_POLICY_REQUIRED")

    def pi(self, target, data):
        raise XMLPartError("XML_PROCESSING_INSTRUCTION_FORBIDDEN")

    def close(self):
        # lxml calls close even while propagating a target callback failure.
        # Well-formedness is checked by XMLParser itself; raising here would
        # mask the original budget, location or policy rejection.
        return self.ordinal


def _parse(source: BinaryIO, target: _Target, limits: XMLLimits) -> int:
    parser = etree.XMLParser(target=target, resolve_entities=False, load_dtd=False,
                             no_network=True, huge_tree=False, recover=False)
    count = 0
    try:
        while chunk := source.read(64 * 1024):
            count += len(chunk)
            if count > limits.total_bytes:
                raise XMLPartError("XML_SIZE_LIMIT")
            parser.feed(chunk)
        return parser.close()
    except etree.XMLSyntaxError:
        raise XMLPartError("XML_INVALID") from None


def scan(source: BinaryIO, consume: Callable[[Paragraph], None], limits: XMLLimits = XMLLimits()) -> int:
    """Visit bounded paragraph descriptors. The consumer owns backpressure."""
    return _parse(source, _Target(None, lambda e, n: consume(describe(e, n)), limits), limits)


def scan_located(source: BinaryIO, consume: Callable[[Paragraph, object, tuple | None], None],
                 limits: XMLLimits = XMLLimits()) -> int:
    """scan, also giving each paragraph's element (for its page markers and style) and table cell."""
    target = _Target(None, None, limits)
    target.on_paragraph = lambda e, n: consume(describe(e, n), e, target.cell)
    return _parse(source, target, limits)


def rewrite(source: BinaryIO, destination: BinaryIO,
            decide: Callable[[Paragraph], list[Edit]], limits: XMLLimits = XMLLimits()) -> int:
    """Stream XML to an append-only candidate; failure means discard candidate.

    The caller must keep destination private and encrypted and reconcile the
    package before publishing. This function alone cannot authorize delivery.
    """
    def transform(element, ordinal):
        paragraph = describe(element, ordinal)
        # Untouched hidden/revised content still needs an explicit policy.
        # Refuse it even when detection returned no edits for this paragraph.
        if paragraph.hidden or paragraph.revision:
            raise XMLPartError("DOCX_NONVISIBLE_POLICY_REQUIRED")
        edits = decide(paragraph)
        if edits:
            apply_edits(element, edits)
        for node in element.iter():
            _subset_element(node.tag, node.attrib)

    with etree.xmlfile(destination, encoding="utf-8", buffered=False) as output:
        output.write_declaration()
        return _parse(source, _Target(output, transform, limits), limits)
