"""OPC package inventory shared by the Office-format plugins (DOCX, XLSX).

All entries are registered, including orphans. Parts whose role the format
plugin does not know stay ``uninspected`` and are coverage gaps: nothing is
labelled harmless because it was not looked at. Relationship targets are never
followed outside the package. This is not a format admission decision.
"""
from __future__ import annotations

import posixpath
import re
from dataclasses import dataclass
from urllib.parse import unquote, urlsplit

from lxml import etree

from .safezip import Archive

CT = "{http://schemas.openxmlformats.org/package/2006/content-types}"
REL = "{http://schemas.openxmlformats.org/package/2006/relationships}"


class PackageError(ValueError):
    pass


@dataclass(frozen=True)
class Relationship:
    source: str
    identifier: str
    kind: str
    target: str
    external: bool


@dataclass(frozen=True)
class Part:
    name: str
    content_type: str
    size: int
    role: str  # content_types | relationships | caller-defined roles | uninspected
    referenced: bool


@dataclass(frozen=True)
class Inventory:
    parts: tuple[Part, ...]
    relationships: tuple[Relationship, ...]
    complete: bool


class _Metadata:
    """Bound metadata parse with DTD, external entities and PI forbidden."""

    def __init__(self, accept):
        self.accept = accept
        self.depth = 0
        self.nodes = 0

    def start(self, tag, attrs):
        self.depth += 1
        self.nodes += 1
        if self.depth > 8 or self.nodes > 16_384:
            raise PackageError("OPC_METADATA_LIMIT")
        self.accept(tag, attrs, self.depth)

    def end(self, tag):
        self.depth -= 1

    def data(self, value):
        if value.strip():
            raise PackageError("OPC_METADATA_INVALID")

    def doctype(self, *args):
        raise PackageError("XML_DTD_FORBIDDEN")

    def pi(self, *args):
        raise PackageError("XML_PROCESSING_INSTRUCTION_FORBIDDEN")

    def comment(self, *args):
        raise PackageError("OPC_METADATA_UNINSPECTED_COMMENT")

    def close(self):
        return None


def _metadata(stream, accept):
    parser = etree.XMLParser(target=_Metadata(accept), resolve_entities=False,
                             load_dtd=False, no_network=True, recover=False)
    size = 0
    try:
        while chunk := stream.read(64 * 1024):
            size += len(chunk)
            if size > 2 * 1024 * 1024:
                raise PackageError("OPC_METADATA_LIMIT")
            parser.feed(chunk)
        parser.close()
    except etree.XMLSyntaxError:
        raise PackageError("OPC_METADATA_INVALID") from None


def _part_name(uri: str) -> str:
    # OPC names are URI paths. Reject encodings that create ambiguous segment
    # or host interpretations; relationships are never followed over a network.
    if re.search(r"%(?:2f|5c|00)", uri, re.IGNORECASE):
        raise PackageError("OPC_TARGET_INVALID")
    parsed = urlsplit(uri)
    if parsed.scheme or parsed.netloc or parsed.query or parsed.fragment:
        raise PackageError("OPC_TARGET_INVALID")
    try:
        name = unquote(parsed.path, errors="strict")
    except UnicodeDecodeError:
        raise PackageError("OPC_TARGET_INVALID") from None
    if "\\" in name or "\0" in name or ":" in name:
        raise PackageError("OPC_TARGET_INVALID")
    return name


def _source(name):
    if name == "_rels/.rels":
        return ""
    parent, leaf = posixpath.split(name)
    if posixpath.basename(parent) != "_rels" or not leaf.endswith(".rels"):
        raise PackageError("OPC_RELATIONSHIP_LOCATION_INVALID")
    return posixpath.join(posixpath.dirname(parent), leaf[:-5])


def inventory(archive: Archive, *, prefix: str, main_type: str, classify) -> Inventory:
    """Register every entry and relationship. ``classify(name, content_type)`` returns the role
    of a part that is neither content types nor relationships."""
    entries = {entry.name: entry for entry in archive.entries}
    if "[Content_Types].xml" not in entries or "_rels/.rels" not in entries:
        raise PackageError(f"{prefix}_REQUIRED_PART_MISSING")
    defaults, overrides = {}, {}

    def content_types(tag, attrs, depth):
        if depth == 1 and tag == CT + "Types":
            if attrs:
                raise PackageError("OPC_CONTENT_TYPES_INVALID")
            return
        if depth != 2 or tag not in {CT + "Default", CT + "Override"}:
            raise PackageError("OPC_CONTENT_TYPES_INVALID")
        key = attrs.get("Extension" if tag == CT + "Default" else "PartName", "")
        value = attrs.get("ContentType", "")
        if not key or not value or len(attrs) != 2:
            raise PackageError("OPC_CONTENT_TYPES_INVALID")
        table = defaults if tag == CT + "Default" else overrides
        if table is overrides:
            if not key.startswith("/"):
                raise PackageError("OPC_CONTENT_TYPES_INVALID")
            key = _part_name(key)[1:]
        else:
            key = key.lower()
        if key in table:
            raise PackageError("OPC_CONTENT_TYPES_DUPLICATE")
        table[key] = value

    with archive.open("[Content_Types].xml") as source:
        _metadata(source, content_types)
    if any(name not in entries for name in overrides):
        raise PackageError("OPC_CONTENT_TYPE_TARGET_MISSING")
    relationships = []
    for name in entries:
        if not name.endswith(".rels"):
            continue
        owner = _source(name)
        if owner and owner not in entries:
            raise PackageError("OPC_RELATIONSHIP_SOURCE_MISSING")
        seen = set()

        def relation(tag, attrs, depth):
            if depth == 1 and tag == REL + "Relationships":
                if attrs:
                    raise PackageError("OPC_RELATIONSHIPS_INVALID")
                return
            if depth != 2 or tag != REL + "Relationship":
                raise PackageError("OPC_RELATIONSHIPS_INVALID")
            identifier, kind, target = (attrs.get(k, "") for k in ("Id", "Type", "Target"))
            mode = attrs.get("TargetMode", "Internal")
            if not identifier or not kind or not target or mode not in {"Internal", "External"}:
                raise PackageError("OPC_RELATIONSHIPS_INVALID")
            if set(attrs) - {"Id", "Type", "Target", "TargetMode"}:
                raise PackageError("OPC_RELATIONSHIPS_INVALID")
            if identifier in seen:
                raise PackageError("OPC_RELATIONSHIP_DUPLICATE")
            seen.add(identifier)
            external = mode == "External"
            if not external:
                path = _part_name(target)
                target = posixpath.normpath(path[1:] if path.startswith("/") else
                                           posixpath.join(posixpath.dirname(owner), path))
                if target.startswith("../") or target == ".." or target not in entries:
                    raise PackageError("OPC_RELATIONSHIP_TARGET_MISSING")
            if len(relationships) >= 16_384:
                raise PackageError("OPC_METADATA_LIMIT")
            relationships.append(Relationship(owner, identifier, kind, target, external))

        with archive.open(name) as source:
            _metadata(source, relation)
    main = [r.target for r in relationships if not r.source and
            r.kind.endswith("/officeDocument") and not r.external]
    if len(main) != 1:
        raise PackageError(f"{prefix}_MAIN_PART_INVALID")
    targets = {r.target for r in relationships if not r.external}
    parts = []
    for entry in archive.entries:
        name = entry.name
        content_type = overrides.get(name, defaults.get(name.rsplit(".", 1)[-1].lower(), ""))
        role = ("content_types" if name == "[Content_Types].xml" else
                "relationships" if name.endswith(".rels") else
                classify(name, content_type))
        parts.append(Part(name, content_type, entry.size, role, name in targets))
    if not any(p.name == main[0] and p.content_type == main_type for p in parts):
        raise PackageError(f"{prefix}_MAIN_PART_INVALID")
    return Inventory(tuple(parts), tuple(relationships), True)
