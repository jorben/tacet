"""M0 OPC package inventory and conservative text-only rewrite prototype.

All entries are registered, including orphans. Unsupported parts are explicit
coverage gaps; the prototype refuses to export them. This is intentionally not
a production format admission decision and does not claim Office compatibility.
It accepts file-like resources only and never publishes to a user directory.
"""
from __future__ import annotations

import zipfile
from dataclasses import dataclass
from typing import BinaryIO, Callable

from tacet_plugin_sdk.safezip import Archive, Limits

from .xmlparts import Edit, Paragraph, XMLPartError, rewrite

from tacet_plugin_sdk import opc
from tacet_plugin_sdk.opc import Inventory, Part, PackageError, Relationship  # noqa: F401

WORD_TYPE = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
TEXT_TYPES = {
    WORD_TYPE,
    "application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml",
    "application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml",
    "application/vnd.openxmlformats-officedocument.wordprocessingml.footnotes+xml",
    "application/vnd.openxmlformats-officedocument.wordprocessingml.endnotes+xml",
}
# These parts can carry strings, URLs, hidden text or cached content. The M0
# prototype does not label any uninspected XML as harmless structural content.


def inventory(archive: Archive) -> Inventory:
    return opc.inventory(archive, prefix="DOCX", main_type=WORD_TYPE,
                         classify=lambda name, content_type: "text" if content_type in TEXT_TYPES else "uninspected")


def rewrite_text_subset(source: BinaryIO, candidate: BinaryIO,
                        decide: Callable[[str, Paragraph], list[Edit]],
                        limits: Limits = Limits()) -> Inventory:
    """M0 limited writer, refusing gaps instead of silently copying originals.

    The subset consists only of w:t text parts and OPC metadata. Unknown/media/
    styles/properties and external relationships require later explicit policy.
    Candidate is private and is invalid until the host independently validates it.
    """
    archive = Archive(source, limits)
    try:
        before = inventory(archive)
        if any(p.role == "uninspected" for p in before.parts):
            raise PackageError("DOCX_UNINSPECTED_PART_POLICY_REQUIRED")
        if any(r.external for r in before.relationships):
            raise PackageError("DOCX_EXTERNAL_RELATIONSHIP_POLICY_REQUIRED")
        with zipfile.ZipFile(candidate, "w", compression=zipfile.ZIP_DEFLATED, allowZip64=False) as output:
            for part in before.parts:
                with archive.open(part.name) as input_part, output.open(part.name, "w") as output_part:
                    if part.role == "text":
                        rewrite(input_part, output_part, lambda p: decide(part.name, p))
                    else:
                        while chunk := input_part.read(64 * 1024):
                            output_part.write(chunk)
        return before
    finally:
        archive.close()
