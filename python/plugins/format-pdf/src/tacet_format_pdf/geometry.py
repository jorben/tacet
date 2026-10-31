"""Target regions for PDF write-back: the Python side of ``plan.Regions`` (MD-15).

Preview overlays (Go) and write-back (this plugin) must cover exactly the same pixels, so this is a
direct port of internal/plan/geometry.go for unrotated pages (the rasteriser refuses rotated ones)
and is held to the shared vectors in protocol/testdata/regions-v1.json.
"""
from __future__ import annotations

import math

MARGIN_POINTS = 1.0   # outward expansion this plugin declares
ANTIALIAS_PIXELS = 1  # covers the soft edge of rendered glyphs


class GeometryError(ValueError):
    pass


def merge_lines(boxes):
    """Join boxes that sit on one text line (same rule as the Go implementation)."""
    ordered = sorted(boxes, key=lambda b: (-b[1], b[0]))
    lines = []
    for b in ordered:
        for i, line in enumerate(lines):
            overlap = min(line[3], b[3]) - max(line[1], b[1])
            shorter = min(line[3] - line[1], b[3] - b[1])
            tall = max(line[3] - line[1], b[3] - b[1])
            gap = max(line[0], b[0]) - min(line[2], b[2])
            if overlap > shorter / 2 and gap <= tall * 1.5:
                lines[i] = (min(line[0], b[0]), min(line[1], b[1]), max(line[2], b[2]), max(line[3], b[3]))
                break
        else:
            lines.append(tuple(b))
    return lines


def regions(boxes, page_w, page_h, dpi, margin=MARGIN_POINTS, antialias=ANTIALIAS_PIXELS):
    """Boxes are (left, bottom, right, top) in points; returns (left, top, right, bottom) pixel rectangles."""
    values = [page_w, page_h, dpi, margin]
    for b in boxes:
        values.extend(b)
    if not all(math.isfinite(v) for v in values) or page_w <= 0 or page_h <= 0 or dpi <= 0 or margin < 0 or not 0 <= antialias <= 16:
        raise GeometryError('PLAN_GEOMETRY_INVALID')
    for left, bottom, right, top in boxes:
        if right < left or top <= bottom:
            raise GeometryError('PLAN_GEOMETRY_INVALID')
    scale = dpi / 72
    width, height = math.ceil(page_w * scale), math.ceil(page_h * scale)
    out = []
    for left, bottom, right, top in merge_lines(boxes):
        left, bottom, right, top = left - margin, bottom - margin, right + margin, top + margin
        x0, x1 = left * scale, right * scale
        y0, y1 = (page_h - top) * scale, (page_h - bottom) * scale
        rect = [max(math.floor(x0) - antialias, 0), max(math.floor(y0) - antialias, 0),
                min(math.ceil(x1) + antialias, width), min(math.ceil(y1) + antialias, height)]
        if rect[2] > rect[0] and rect[3] > rect[1]:
            out.append(tuple(rect))
    out.sort(key=lambda r: (r[1], r[0]))
    return out
