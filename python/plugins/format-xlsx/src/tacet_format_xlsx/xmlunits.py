"""Bounded streaming of SpreadsheetML parts, one buffered unit at a time.

A part is parsed with a SAX-style target. Elements outside the unit tag are
validated against a small allow-list and streamed straight to the output; each
unit (``c`` in a worksheet, ``si`` in shared strings) is buffered as a detached
subtree so the caller can describe and change it. Everything unknown fails
closed: comments, processing instructions, DTDs and text outside units are
rejected because they would otherwise be copied without being inspected.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import BinaryIO, Callable

from lxml import etree

M = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"
XML_SPACE = "{http://www.w3.org/XML/1998/namespace}space"


class XMLUnitError(ValueError):
    """Stable, non-sensitive error code."""


@dataclass(frozen=True)
class XMLLimits:
    depth: int = 64
    nodes: int = 50_000_000
    unit_nodes: int = 50_000
    unit_chars: int = 1024 * 1024
    total_bytes: int = 4 * 1024 * 1024 * 1024


def _emit(output, element):
    """Write a buffered unit through the writer's own context so it inherits the
    enclosing namespace declarations instead of repeating them on every unit."""
    with output.element(element.tag, dict(element.attrib)):
        if element.text:
            output.write(element.text)
        for child in element:
            _emit(output, child)
            if child.tail:
                output.write(child.tail)


XML_NAMESPACE = "{http://www.w3.org/XML/1998/namespace}"


def _write_unit(output, element):
    # The incremental writer would bind an arbitrary prefix (ns0) to the xml
    # namespace for xml:space, which Namespaces in XML forbids. Such units, rare
    # and small, are written whole so the attribute keeps its reserved prefix.
    if any(key.startswith(XML_NAMESPACE) for node in element.iter() for key in node.attrib):
        output.write(element)
    else:
        _emit(output, element)


class _Target:
    def __init__(self, output, unit_tag, outer_tags, on_unit, limits, root_hook=None):
        self.output = output
        self.unit_tag = unit_tag
        self.outer_tags = outer_tags
        self.on_unit = on_unit
        self.limits = limits
        self.root_hook = root_hook
        self.depth = 0
        self.nodes = 0
        self.unit_nodes = 0
        self.unit_chars = 0
        self.ordinal = 0
        self.builder = None
        self.unit_depth = 0
        self.contexts = []
        self.namespaces = {}
        self.pending_ns = {}
        self.ns_stacks = {}
        self.tags = []

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
        raise XMLUnitError("XML_DTD_FORBIDDEN")

    def start(self, tag, attributes):
        if self.builder is None and tag != self.unit_tag and tag not in self.outer_tags:
            raise XMLUnitError("XLSX_PART_SUBSET_UNSUPPORTED")
        if not self.tags and self.root_hook is not None:
            attributes = self.root_hook(dict(attributes))
        self.tags.append(tag)
        self.depth += 1
        self.nodes += 1
        if self.depth > self.limits.depth or self.nodes > self.limits.nodes:
            raise XMLUnitError("XML_STRUCTURE_LIMIT")
        if tag == self.unit_tag and self.builder is None:
            self.builder = etree.TreeBuilder()
            self.unit_depth = self.depth
            self.unit_nodes = 0
            self.unit_chars = 0
        if self.builder is not None:
            self.unit_nodes += 1
            if self.unit_nodes > self.limits.unit_nodes:
                raise XMLUnitError("XLSX_UNIT_LIMIT")
            self.builder.start(tag, attributes, nsmap=dict(self.namespaces))
        elif self.output is not None:
            context = self.output.element(tag, attributes, nsmap=self.pending_ns or None)
            context.__enter__()
            self.contexts.append(context)
        self.pending_ns = {}

    def data(self, data):
        if self.builder is not None:
            self.unit_chars += len(data)
            if self.unit_chars > self.limits.unit_chars:
                raise XMLUnitError("XLSX_UNIT_LIMIT")
            self.builder.data(data)
        elif data.strip():
            raise XMLUnitError("XLSX_UNMAPPED_TEXT")
        elif self.output is not None:
            self.output.write(data)

    def end(self, tag):
        if self.builder is not None:
            self.builder.end(tag)
            if self.depth == self.unit_depth:
                element = self.builder.close()
                self.builder = None
                self.ordinal += 1
                self.on_unit(element, self.ordinal - 1)
                if self.output is not None:
                    _write_unit(self.output, element)
        elif self.output is not None:
            self.contexts.pop().__exit__(None, None, None)
        self.depth -= 1
        self.tags.pop()

    def comment(self, text):
        raise XMLUnitError("XLSX_XML_COMMENT_POLICY_REQUIRED")

    def pi(self, target, data):
        raise XMLUnitError("XML_PROCESSING_INSTRUCTION_FORBIDDEN")

    def close(self):
        return self.ordinal


def _parse(source: BinaryIO, target: _Target, limits: XMLLimits) -> int:
    parser = etree.XMLParser(target=target, resolve_entities=False, load_dtd=False,
                             no_network=True, huge_tree=False, recover=False)
    count = 0
    try:
        while chunk := source.read(64 * 1024):
            count += len(chunk)
            if count > limits.total_bytes:
                raise XMLUnitError("XML_SIZE_LIMIT")
            parser.feed(chunk)
        return parser.close()
    except etree.XMLSyntaxError:
        raise XMLUnitError("XML_INVALID") from None


def scan_units(source: BinaryIO, unit_tag: str, outer_tags: set[str],
               consume: Callable[[etree._Element, int], None], limits: XMLLimits = XMLLimits()) -> int:
    """Visit every unit read-only. Returns the number of units."""
    return _parse(source, _Target(None, unit_tag, outer_tags, consume, limits), limits)


def rewrite_units(source: BinaryIO, destination: BinaryIO, unit_tag: str, outer_tags: set[str],
                  transform: Callable[[etree._Element, int], None], limits: XMLLimits = XMLLimits(),
                  root_hook: Callable[[dict], dict] | None = None) -> int:
    """Stream to an append-only candidate; a failure means the candidate is discarded.

    ``transform`` may mutate the unit in place. ``root_hook`` receives the root
    element's attributes and returns the attributes to write.
    """
    with etree.xmlfile(destination, encoding="utf-8", buffered=False) as output:
        output.write_declaration()
        return _parse(source, _Target(output, unit_tag, outer_tags, transform, limits, root_hook), limits)
