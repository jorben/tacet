"""``format.write`` for text PDF: a rasterised output.

Every page of the output is an image of the input page with the planned character ranges painted
with the planned redaction style; the output has no text layer, no metadata, no outline and no embedded files, so those parts
are reported as removed and every page as converted. A page this writer cannot render faithfully
(rotation, content other than text) makes the operations not executed with the rasteriser's reason code, never a
silently different page. A text operation names ``page/N``, the unit's chunk ordinal and a code point
range inside that chunk, the same coordinates ``format.extract`` reported.
"""
from __future__ import annotations

import re
from collections import defaultdict

import pypdfium2 as pdfium

from tacet_plugin_sdk.runtime import TaskError

from .extract import chunks
from .geometry import GeometryError, regions
from .parts import DROPPED, OUTPUT_PAGE_TYPE, input_parts, page_index
from .raster import PDFError, rasterize_text_subset
from .styles import DEFAULT as DEFAULT_STYLE

_CODE = re.compile(r'^[A-Z][A-Z0-9_]*$')
DEFAULT_DPI = 200


class _Counting:
    def __init__(self, sink):
        self._sink = sink
        self.bytes = 0

    def write(self, data):
        self.bytes += len(data)
        return self._sink.write(data)


def op_boxes(text, characters, ordinal, start, end):
    """Character boxes of a range inside one chunk of a page's text, or a reason code."""
    pieces = list(chunks(text))
    if not 1 <= ordinal <= len(pieces):
        return None, 'UNIT_NOT_FOUND'
    if end > len(pieces[ordinal - 1]):
        return None, 'RANGE_INVALID'
    offset = sum(len(p) for p in pieces[:ordinal - 1])
    boxes = [c.box for c in characters[offset + start:offset + end] if c.box[2] > c.box[0] and c.box[3] > c.box[1]]
    return (boxes, None) if boxes else (None, 'UNRELIABLE_LOCATION')


def write(source, ops, sink, task, schemas, dpi=DEFAULT_DPI, style=DEFAULT_STYLE):
    def emit(item):
        schemas.validate('format.schema.json', 'WriteItem', item)
        task.emit(item)

    try:
        document = pdfium.PdfDocument(source)
    except pdfium.PdfiumError:
        raise TaskError('INPUT_CORRUPT') from None
    try:
        parts = input_parts(document)
        pages = len(document)
    finally:
        document.close()
    names = {name for name, _ in parts}
    results: dict[str, tuple[str, str | None]] = {}
    text_ops: dict[int, dict[int, list]] = defaultdict(lambda: defaultdict(list))
    for op in ops:
        if op.kind == 'text':
            index = page_index(op.part)
            if index is None or not 0 <= index < pages:
                results[op.id] = ('not_executed', 'PART_NOT_TEXT')
            else:
                text_ops[index][op.ordinal].append(op)
        elif op.part in DROPPED and op.part in names:
            results[op.id] = ('executed', None)  # rasterising leaves them out
        elif op.part in names or op.part.startswith('page/'):
            results[op.id] = ('not_executed', 'PART_OPERATION_UNSUPPORTED')
        else:
            results[op.id] = ('not_executed', 'PART_UNKNOWN')

    out = _Counting(sink)
    failure = None

    def pick(facts, pixels):
        task.check()
        rects = []
        for ordinal, group in text_ops.get(facts.page - 1, {}).items():
            for op in group:
                boxes, reason = op_boxes(facts.text, facts.characters, ordinal, op.start, op.end)
                if reason:
                    results[op.id] = ('not_executed', reason)
                    continue
                try:
                    found = regions(boxes, facts.width, facts.height, dpi)
                except GeometryError:
                    results[op.id] = ('not_executed', 'UNRELIABLE_LOCATION')
                    continue
                rects.extend(found)
                results[op.id] = ('executed', None)
        return rects

    source.seek(0)
    try:
        rasterize_text_subset(source, out, pick, dpi=dpi, style=style)
    except PDFError as error:
        failure = str(error) if _CODE.match(str(error)) else 'PDF_PROCESSING_FAILED'
    if failure:
        # nothing usable was produced: every text operation says why, and the host fails the export
        for op in ops:
            if results.get(op.id, ('', ''))[0] != 'not_executed':
                results[op.id] = ('not_executed', failure[:64])
    for op in ops:
        status, reason = results.get(op.id, ('not_executed', 'UNIT_NOT_FOUND'))
        item = {'kind': 'op_result', 'op': op.id, 'status': status}
        if reason:
            item['reason'] = reason
        emit(item)
    fates = []
    for name, _ in parts:
        fate = 'removed' if name in DROPPED else ('kept' if failure else 'converted')
        item = {'kind': 'part_fate', 'part': name, 'fate': fate}
        if fate == 'converted':
            item['expected_type'] = OUTPUT_PAGE_TYPE
        fates.append(item)
        emit(item)
    emit({'kind': 'write_complete', 'complete': True, 'operations': len(ops), 'parts': len(fates), 'bytes': max(out.bytes, 1)})
