"""Where a paragraph is, for review, reports and manifests (requirements 4.3, architecture 4.5).

A location is numbers only, never text: the approximate page, the paragraph ordinal of the nearest heading
(the section) and, inside a table, its position. The page comes from the page markers the document was
saved with (``w:lastRenderedPageBreak``, ``w:br w:type="page"``, ``w:pageBreakBefore`` and section breaks):
it is where Word or WPS broke the pages when the file was last saved, so it is approximate, and a document
saved without markers (often a generated one) has no page at all. A run of markers with no text between
them is one page break, so an explicit break and the rendered marker Word writes after it count once.
"""
from __future__ import annotations

import re
from dataclasses import dataclass

W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
_HEADING_STYLE = re.compile(r"^(?:[Hh]eading ?[1-9]|Title|[1-9])$")
_OFF = {"0", "false", "off"}


@dataclass(frozen=True)
class Layout:
    """What one paragraph says about pages and headings, read from its own subtree."""
    lead: bool         # a page starts at this paragraph (a break before any of its text)
    inner: int         # page breaks between pieces of its text
    trail: bool        # a page starts after it (a break after all of its text, or a section break)
    heading: bool
    markers: bool      # it carries any page marker at all


def _on(node) -> bool:
    return node.get(W + "val") not in _OFF


def layout(element, has_text: bool) -> Layout:
    seen_text, pending, lead, inner, markers = False, False, False, 0, False
    heading = section_ends = False
    for node in element.iter():
        tag = node.tag
        if tag == W + "pageBreakBefore" and _on(node):
            lead, markers = True, True
        elif tag == W + "lastRenderedPageBreak" or (tag == W + "br" and node.get(W + "type") == "page"):
            pending, markers = True, True
        elif tag == W + "t" and node.text:
            if pending:
                if seen_text:
                    inner += 1
                else:
                    lead = True
                pending = False
            seen_text = True
        elif tag == W + "outlineLvl":
            heading = heading or (node.get(W + "val") or "").isdigit() and int(node.get(W + "val")) <= 8
        elif tag == W + "pStyle":
            heading = heading or bool(_HEADING_STYLE.match(node.get(W + "val") or ""))
        elif tag == W + "sectPr":
            # the paragraph ends its section, though the mark sits in its properties, before its text
            kind = node.find(W + "type")
            markers = True
            if kind is None or kind.get(W + "val") != "continuous":
                section_ends = True
    return Layout(lead, inner, pending or section_ends, heading and has_text, markers)


class Locator:
    """Follows the paragraphs of one part in order and gives each its location."""

    def __init__(self, paged: bool):
        self.paged = paged       # the part was saved with page markers
        self.page = 1
        self.carry = False       # the previous paragraph ended with a break
        self.section = 0

    def place(self, ordinal: int, lay: Layout, cell) -> dict:
        if lay.lead or self.carry:
            self.page += 1 if ordinal > 1 else 0
        start = self.page
        self.page += lay.inner
        self.carry = lay.trail
        if lay.heading:
            self.section = ordinal
        out = {}
        if self.paged:
            out["page_hint"] = start
        if self.section:
            out["section"] = self.section
        if cell is not None:
            out["table"] = {"index": cell[0], "row": cell[1], "col": cell[2]}
        return out
