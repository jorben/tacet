"""``format.extract`` for text PDF.

One unit per page chunk, in PDFium's text order; a unit's text is the page's
characters verbatim (including PDFium's ``\\r\\n`` line breaks), so a code-point
position in the unit is the character index on the page, which is what the
character boxes of the writer are addressed by. Source map: part ``page/N``,
ordinal = chunk number (a page longer than ``CHUNK`` code points is split after
the last line break before the limit, deterministically, so a re-scan finds the
same chunks).

Reported parts: every page (text), and, only where they exist, ``page/N/images``,
``page/N/annotations``, ``document/metadata``, ``document/outline`` and
``document/attachments``, each as ``not_inspected`` with class ``unknown`` (never ``other``: that class means "no
text" and the host treats it as out of scope, but an image, a title or a bookmark can
hold sensitive text). They are coverage gaps: this
plugin does not read image content, annotation text, metadata, bookmarks or
attachments, and says so instead of leaving them out. A page whose text cannot be
read reliably is ``failed`` and contributes no unit.

Not covered (recorded, not hidden): invisible text (render mode 3) is not told
apart from visible text; vector outlines are not examined; JavaScript and XFA
are not read.
"""
from __future__ import annotations

import math

import pypdfium2 as pdfium
import pypdfium2.raw as raw

from .raster import Limits, OBJECT_DEPTH

CHUNK = 262_144


def chunks(text: str):
    """Deterministic split of one page's text."""
    start = 0
    while start < len(text):
        end = min(len(text), start + CHUNK)
        if end < len(text):
            cut = text.rfind('\n', start, end)
            if cut > start:
                end = cut + 1
        yield text[start:end]
        start = end


def _characters(page, limits: Limits) -> str:
    textpage = page.get_textpage()
    try:
        count = textpage.count_chars()
        if count > limits.chars:
            raise ValueError('PDF_TEXT_LIMIT')
        out = []
        for index in range(count):
            value = raw.FPDFText_GetUnicode(textpage, index)
            if (raw.FPDFText_HasUnicodeMapError(textpage, index) != 0 or value == 0
                    or value > 0x10FFFF or 0xD800 <= value <= 0xDFFF):
                raise ValueError('PDF_UNICODE_COVERAGE_GAP')
            box = textpage.get_charbox(index)
            if not all(math.isfinite(number) for number in box):
                raise ValueError('PDF_UNRELIABLE_LOCATION')
            out.append(chr(value))
        return ''.join(out)
    finally:
        textpage.close()


def _has_images(page) -> bool:
    return any(True for _ in page.get_objects(filter=[raw.FPDF_PAGEOBJ_IMAGE], max_depth=OBJECT_DEPTH))


def _has_annotations(page) -> bool:
    """Any annotation other than a link: notes, free text and form widgets carry text."""
    for index in range(raw.FPDFPage_GetAnnotCount(page)):
        annotation = raw.FPDFPage_GetAnnot(page, index)
        if not annotation:
            return True  # cannot be read, so cannot be called absent
        try:
            if raw.FPDFAnnot_GetSubtype(annotation) != raw.FPDF_ANNOT_LINK:
                return True
        finally:
            raw.FPDFPage_CloseAnnot(annotation)
    return False


def extract(document, task, schemas, limits: Limits = Limits()) -> None:
    units = parts = 0

    def emit(item):
        schemas.validate('format.schema.json', 'ExtractItem', item)
        task.emit(item)

    def state(part, cls, status, reason=None):
        nonlocal parts
        parts += 1
        item = {'kind': 'part_state', 'part': part, 'class': cls, 'state': status}
        if reason:
            item['reason'] = reason
        emit(item)

    for index in range(len(document)):
        task.check()
        name = f'page/{index + 1}'
        page = document[index]
        try:
            try:
                text = _characters(page, limits)
                images, annotations = _has_images(page), _has_annotations(page)
            except ValueError as error:
                state(name, 'text', 'failed', str(error))
                continue
            except pdfium.PdfiumError:
                state(name, 'text', 'failed', 'PAGE_UNREADABLE')
                continue
            for ordinal, chunk in enumerate(chunks(text), start=1):
                units += 1
                emit({'kind': 'text_unit', 'unit_id': f'u{units}', 'part': name, 'ordinal': ordinal, 'text': chunk,
                      'visibility': 'visible', 'source_map': {'kind': 'paragraph_ordinal', 'part': name, 'ordinal': ordinal}})
            state(name, 'text', 'inspected')
            if images:
                state(f'{name}/images', 'unknown', 'not_inspected', 'IMAGE_CONTENT_NOT_INSPECTED')
            if annotations:
                state(f'{name}/annotations', 'unknown', 'not_inspected', 'ANNOTATION_NOT_INSPECTED')
        finally:
            page.close()
    task.check()
    if document.get_metadata_dict(skip_empty=True):
        state('document/metadata', 'unknown', 'not_inspected', 'METADATA_NOT_INSPECTED')
    if any(True for _ in document.get_toc()):
        state('document/outline', 'unknown', 'not_inspected', 'OUTLINE_NOT_INSPECTED')
    if document.count_attachments():
        state('document/attachments', 'unknown', 'not_inspected', 'EMBEDDED_FILE_NOT_INSPECTED')
    emit({'kind': 'extract_complete', 'complete': True, 'units': units, 'parts': parts})
