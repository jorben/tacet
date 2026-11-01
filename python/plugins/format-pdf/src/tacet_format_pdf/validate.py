"""``format.validate`` for text PDF (rasterised output).

Independent of the writer's report: the candidate is opened again, its pages are enumerated, and each planned
text operation is checked on the pixels of the candidate page itself. The region of an operation is
computed from the *input's* character boxes with the shared geometry, so the check does not trust
anything the writer said about where it painted. An operation is executed when its region holds the
texture of the planned style in the candidate (``styles.expected``, which depends on the style, the
resolution and the region only, never on the page); if it does not, the approved content is still visible there, which is a residual. What is
not (and cannot be) verified here is stated: the output pages are images, so the candidate has no text
layer to compare and the host's re-detection finds nothing by construction.
"""
from __future__ import annotations

import pypdfium2 as pdfium
from PIL import ImageChops

from tacet_plugin_sdk.runtime import TaskError

from .geometry import GeometryError, regions
from .parts import DROPPED, INPUT_PAGE_TYPE, OUTPUT_PAGE_TYPE, input_parts, page_index
from .raster import Limits, PDFError, _page_text
from .styles import DEFAULT as DEFAULT_STYLE, STYLES, expected
from .write import DEFAULT_DPI, image_page, image_regions, op_boxes

TOLERANCE = 8      # a channel this close to the texture counts as painted
COVERED = 0.995    # share of an operation's pixels that must be painted


def _covered(image, rect, style, dpi) -> bool:
    left, top, right, bottom = rect
    if right > image.width or bottom > image.height:
        return False
    crop, want = image.crop(rect), expected(style, dpi, rect)
    try:
        diff = ImageChops.difference(crop, want)
        try:
            r, g, b = diff.split()
            worst = ImageChops.lighter(ImageChops.lighter(r, g), b)  # largest channel difference per pixel
            close = worst.histogram()[:TOLERANCE + 1]
            return sum(close) >= COVERED * (right - left) * (bottom - top)
        finally:
            diff.close()
    finally:
        crop.close()
        want.close()


def validate(inp, ops, cand, task, schemas, dpi=DEFAULT_DPI, style=DEFAULT_STYLE) -> None:
    if style not in STYLES:
        raise TaskError('PROTOCOL_INVALID')
    def emit(item):
        schemas.validate('format.schema.json', 'ValidateItem', item)
        task.emit(item)

    try:
        source = pdfium.PdfDocument(inp)
    except pdfium.PdfiumError:
        raise TaskError('INPUT_CORRUPT') from None
    try:
        parts = input_parts(source)
        for name, content_type in parts:
            emit({'kind': 'input_part', 'part': name, 'content_type': content_type})
        try:
            out = pdfium.PdfDocument(cand)
        except pdfium.PdfiumError:
            emit({'kind': 'validate_complete', 'complete': True, 'input_parts': len(parts), 'output_parts': 0, 'operations': 0, 'reopened': False})
            return
        try:
            candidate_pages = len(out)
            for i in range(candidate_pages):
                task.check()
                emit({'kind': 'output_part', 'part': f'page/{i + 1}', 'content_type': OUTPUT_PAGE_TYPE, 'source': f'page/{i + 1}', 'references': []})
            checks = _check_operations(source, out, ops, dpi, task, style)
            for check in checks:
                emit(check)
            emit({'kind': 'validate_complete', 'complete': True, 'input_parts': len(parts), 'output_parts': candidate_pages,
                  'operations': len(checks), 'reopened': True})
        finally:
            out.close()
    finally:
        source.close()


def _check_operations(source, out, ops, dpi, task, style):
    names = {name for name, _ in input_parts(source)}
    checks = {}
    wanted = {}
    for op in ops:
        if op.kind == 'text':
            index = page_index(op.part)
            if index is None or not 0 <= index < len(source):
                checks[op.id] = (False, False, op.part)
            else:
                wanted.setdefault(index, []).append(op)
        elif image_page(op.part) is not None:
            index = image_page(op.part)
            if 0 <= index < len(source):
                wanted.setdefault(index, []).append(op)
            else:
                checks[op.id] = (False, False, op.part)
        elif op.part in DROPPED and op.part in names:
            gone = not _has_part(out, op.part)
            checks[op.id] = (gone, not gone, op.part)
        else:
            checks[op.id] = (False, False, op.part)
    limits = Limits()
    for index, group in wanted.items():
        task.check()
        unit = lambda op: f'{op.part}#{op.ordinal}'  # noqa: E731
        if index >= len(out):
            for op in group:
                checks[op.id] = (False, False, unit(op))
            continue
        page = source[index]
        try:
            facts = _page_text(page, index + 1, limits)
        except (PDFError, pdfium.PdfiumError):
            for op in group:
                checks[op.id] = (False, False, unit(op))
            page.close()
            continue
        page.close()
        rendered = out[index]
        bitmap = rendered.render(scale=dpi / 72, rev_byteorder=True, may_draw_forms=False)
        image = bitmap.to_pil().convert('RGB')
        try:
            for op in group:
                if op.kind == 'part':  # the images of the page: painted where they were visible
                    try:
                        rects = image_regions(facts, dpi)
                    except GeometryError:
                        rects = []
                    done = bool(rects) and all(_covered(image, r, style, dpi) for r in rects)
                    checks[op.id] = (done, bool(rects) and not done, op.part)
                    continue
                boxes, reason = op_boxes(facts.text, facts.characters, op.ordinal, op.start, op.end)
                try:
                    rects = regions(boxes, facts.width, facts.height, dpi) if boxes else []
                except GeometryError:
                    rects = []
                if reason or not rects:
                    checks[op.id] = (False, False, unit(op))
                    continue
                done = all(_covered(image, r, style, dpi) for r in rects)
                checks[op.id] = (done, not done, unit(op))
        finally:
            image.close()
            bitmap.close()
            rendered.close()
    return [{'kind': 'op_check', 'op': op.id, 'executed': checks[op.id][0], 'residual': checks[op.id][1], 'unit': checks[op.id][2]} for op in ops]


def _has_part(document, name) -> bool:
    if name == 'document/metadata':
        return bool(document.get_metadata_dict(skip_empty=True))
    if name == 'document/outline':
        return any(True for _ in document.get_toc())
    return bool(document.count_attachments())
