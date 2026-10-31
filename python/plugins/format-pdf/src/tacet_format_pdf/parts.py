"""The parts of a text PDF as write and validate see them.

Pages are the parts a rasterised output carries over (each becomes a raster page); the document-level
parts (metadata, outline, embedded files) exist only when the input has them and are dropped by
construction, since the output is built from page images only. Sub-page gap parts reported by
``format.extract`` (images, annotations) are not output parts: they live inside their page.
"""
from __future__ import annotations

INPUT_PAGE_TYPE = 'application/x-tacet-pdf-page'
INPUT_DOCUMENT_TYPE = 'application/x-tacet-pdf-document-part'
OUTPUT_PAGE_TYPE = 'image/x-tacet-raster-page'
DROPPED = ('document/metadata', 'document/outline', 'document/attachments')


def input_parts(document) -> list[tuple[str, str]]:
    """(part id, content type) for every part of the input, in a stable order."""
    parts = [(f'page/{i + 1}', INPUT_PAGE_TYPE) for i in range(len(document))]
    if document.get_metadata_dict(skip_empty=True):
        parts.append(('document/metadata', INPUT_DOCUMENT_TYPE))
    if any(True for _ in document.get_toc()):
        parts.append(('document/outline', INPUT_DOCUMENT_TYPE))
    if document.count_attachments():
        parts.append(('document/attachments', INPUT_DOCUMENT_TYPE))
    return parts


def page_index(part: str):
    """0-based page index of ``page/N``, or None."""
    head, _, tail = part.partition('/')
    if head != 'page' or not tail.isdigit() or tail.startswith('0') or len(tail) > 7:
        return None
    return int(tail) - 1
