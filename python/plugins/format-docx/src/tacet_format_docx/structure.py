"""Inspection of the structure parts every Word document has: styles, settings, font table, web settings,
theme, list numbering (requirements 4.3: every part is registered and has a declared policy).

They hold formatting, not document text, but some of their elements and attributes can hold any string:
document variables, mail-merge data, an attached template, the names of styles the author made, or any
attribute value. A part passes only when it holds no character data, none of those elements, and every
attribute value is a plain token, except the few attributes that name a font or a theme. Anything else
is reported with its reason and stays a coverage gap, handled by the policy like any other.
"""
from __future__ import annotations

import re

from lxml import etree

W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
A = "{http://schemas.openxmlformats.org/drawingml/2006/main}"
_CT = "application/vnd.openxmlformats-officedocument."
STRUCTURE_TYPES = {
    _CT + "wordprocessingml.styles+xml",
    _CT + "wordprocessingml.settings+xml",
    _CT + "wordprocessingml.fontTable+xml",
    _CT + "wordprocessingml.webSettings+xml",
    _CT + "theme+xml",
    _CT + "wordprocessingml.numbering+xml",
}
# elements whose content or attributes are strings of the author's
_CONTENT = {W + "docVars", W + "docVar", W + "mailMerge", W + "attachedTemplate", W + "attachedSchema",
            W + "aliases", W + "captions", W + "autoCaptions", W + "writeProtection"}
# attributes that may hold free strings: names of fonts and of the theme and its schemes
_NAMES = {(W + "font", W + "name"), (W + "altName", W + "val"), (W + "rFonts", W + "ascii"), (W + "rFonts", W + "hAnsi"),
          (W + "rFonts", W + "eastAsia"), (W + "rFonts", W + "cs"), (A + "latin", "typeface"), (A + "ea", "typeface"),
          (A + "cs", "typeface"), (A + "font", "typeface"), (A + "font", "script"), (A + "theme", "name"),
          (A + "clrScheme", "name"), (A + "fontScheme", "name"), (A + "fmtScheme", "name")}
_TOKEN = re.compile(r"^[A-Za-z0-9 _.:/#+,=()-]{0,128}$")
# identifiers Word writes in braces (w15:docId, a theme's family id, an extension's uri): hex digits only
_GUID = re.compile(r"^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$")
# A list level's number text (w:lvlText): placeholders %1-%9 with the punctuation and the few words of
# Chinese numbering (第…章), or a bullet (symbol blocks, private-use glyphs of symbol fonts). Nothing else:
# a word in it would be the author's text.
_LEVEL_TEXT = re.compile(r"^(?:%[1-9]|[ .,:;)(\]\[-]|[、．。，：；（）【】〔〕《》〈〉「」第章节条款项部篇级]"
                         r"|[\u2000-\u2BFF\u25A0-\u25FF\uE000-\uF8FF\uFF0D])*$")
MAX_BYTES = 16 << 20


def inspect(stream) -> str | None:
    """None when the part holds only structure; otherwise the closed reason it does not pass."""
    chunks, total = [], 0
    while block := stream.read(64 * 1024):
        total += len(block)
        if total > MAX_BYTES:
            return "DOCX_STRUCTURE_PART_LIMIT"
        chunks.append(block)
    data = b"".join(chunks)
    parser = etree.XMLParser(resolve_entities=False, load_dtd=False, no_network=True, huge_tree=False, recover=False)
    try:
        root = etree.fromstring(data, parser)
    except etree.XMLSyntaxError:
        return "XML_INVALID"
    finally:
        data = b""
    if root.getroottree().docinfo.doctype:
        return "XML_DTD_FORBIDDEN"
    custom = set()
    for node in root.iter():
        if not isinstance(node.tag, str):  # a comment or processing instruction may carry anything
            return "DOCX_STRUCTURE_PART_STRING"
        if (node.text and node.text.strip()) or (node.tail and node.tail.strip()):
            return "DOCX_STRUCTURE_PART_TEXT"
        if node.tag in _CONTENT:
            return "DOCX_STRUCTURE_PART_CONTENT"
        if node.tag == W + "style" and node.get(W + "customStyle") in {"1", "true", "on"}:
            custom.add(node)
        if node.tag == W + "name" and (node.getparent() in custom or node.getparent().tag == W + "abstractNum"):
            return "DOCX_CUSTOM_STYLE_NAME"  # a style or a list the author named
        for key, value in node.attrib.items():
            if node.tag == W + "lvlText" and key == W + "val":
                if len(value) > 32 or not _LEVEL_TEXT.match(value):
                    return "DOCX_STRUCTURE_PART_STRING"
                continue
            if (node.tag, key) not in _NAMES and not _TOKEN.match(value) and not _GUID.match(value):
                return "DOCX_STRUCTURE_PART_STRING"
    return None
