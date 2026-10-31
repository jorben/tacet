"""Redaction styles (requirements FR-29): fixed textures tiled over the target regions.

Every texture is made here from constants alone, never from the page: the mosaic is a fixed four-tone
tile, not a downsampling of what it covers, so nothing of the original can be recovered from it. A
texture is anchored at the page's top-left pixel, so the pixels any region must hold are a function of
the style, the resolution and the region only; validation recomputes them with the same code and
compares (``expected``). A style changes only how the region looks, never which pixels are covered.
"""
from __future__ import annotations

from functools import lru_cache

from PIL import Image, ImageDraw

STYLES = ('black', 'gray', 'mosaic', 'stripe', 'heart', 'paw', 'snow', 'seal', 'star', 'dot', 'rainbow', 'plaid')
DEFAULT = 'black'


class StyleError(ValueError):
    pass


def _rgb(code: str) -> tuple[int, int, int]:
    return int(code[1:3], 16), int(code[3:5], 16), int(code[5:7], 16)


def _solid(colour):
    return Image.new('RGB', (8, 8), _rgb(colour))


def _mosaic():
    tile = Image.new('RGB', (8, 8))
    draw = ImageDraw.Draw(tile)
    for (x, y), colour in zip(((4, 0), (4, 4), (0, 4), (0, 0)), ('#2c3036', '#535961', '#3b4047', '#666c74')):
        draw.rectangle((x, y, x + 3, y + 3), fill=_rgb(colour))
    return tile


def _stripe():
    tile = Image.new('RGB', (8, 8))
    dark, light = _rgb('#16181b'), _rgb('#43484f')
    for y in range(8):
        for x in range(8):
            tile.putpixel((x, y), dark if (x + y) % 8 < 4 else light)
    return tile


def _heart(draw, x, y, s, colour):
    r = s / 4
    draw.ellipse((x, y, x + 2 * r, y + 2 * r), fill=colour)
    draw.ellipse((x + 2 * r, y, x + 4 * r, y + 2 * r), fill=colour)
    draw.polygon(((x, y + r * 1.1), (x + 4 * r, y + r * 1.1), (x + 2 * r, y + 4 * r)), fill=colour)


def _hearts():
    tile = Image.new('RGB', (14, 14), _rgb('#FFA8C5'))
    draw = ImageDraw.Draw(tile)
    _heart(draw, 1, 1, 6, _rgb('#FF4F8B'))
    _heart(draw, 8, 8, 6, (255, 255, 255))
    return tile


def _paws():
    tile = Image.new('RGB', (24, 24), _rgb('#F7D3A9'))
    draw = ImageDraw.Draw(tile)
    brown = _rgb('#94603F')
    for ox, oy in ((1, 1), (13, 12)):
        draw.ellipse((ox + 2, oy + 5, ox + 8, oy + 10), fill=brown)
        for tx, ty in ((0, 2), (2, 0), (5, 0), (7, 2)):
            draw.ellipse((ox + tx, oy + ty, ox + tx + 2, oy + ty + 2), fill=brown)
    return tile


def _snow():
    tile = Image.new('RGB', (17, 17), _rgb('#8BBDEE'))
    draw = ImageDraw.Draw(tile)
    white = (255, 255, 255)
    draw.line((9, 4, 9, 14), fill=white)
    draw.line((5, 6, 13, 12), fill=white)
    draw.line((5, 12, 13, 6), fill=white)
    draw.point((1, 1), fill=white)
    return tile


def _seal():
    # The screen shows the characters 回避; drawing text here would make the texture depend on the fonts
    # of the machine, so the output uses a fixed glyph-free emblem of the same colours.
    tile = Image.new('RGB', (34, 16), _rgb('#B3201C'))
    draw = ImageDraw.Draw(tile)
    gold, pale = _rgb('#F2C96B'), _rgb('#FFE7A3')
    draw.line((0, 1, 33, 1), fill=gold)
    draw.line((0, 14, 33, 14), fill=gold)
    draw.rectangle((9, 5, 14, 10), outline=pale)
    draw.rectangle((19, 5, 24, 10), outline=pale)
    return tile


def _stars():
    tile = Image.new('RGB', (16, 16), _rgb('#262B5E'))
    draw = ImageDraw.Draw(tile)
    draw.polygon(((5, 1), (6, 4), (9, 4), (7, 6), (8, 9), (5, 7), (2, 9), (3, 6), (1, 4), (4, 4)), fill=_rgb('#FFD86B'))
    draw.point((12, 11), fill=(255, 255, 255))
    draw.point((13, 4), fill=(255, 255, 255))
    draw.point((3, 13), fill=_rgb('#C9CCFF'))
    return tile


def _dots():
    tile = Image.new('RGB', (9, 9), _rgb('#FFD54A'))
    draw = ImageDraw.Draw(tile)
    draw.ellipse((0, 0, 2, 2), fill=(255, 255, 255))
    draw.ellipse((4, 4, 6, 6), fill=_rgb('#FF9F1C'))
    return tile


def _rainbow():
    tile = Image.new('RGB', (1, 12))
    for y, colour in enumerate(('#FF6B6B', '#FF6B6B', '#FFA94D', '#FFA94D', '#FFE066', '#FFE066',
                                '#69DB7C', '#69DB7C', '#4DABF7', '#4DABF7', '#B197FC', '#B197FC')):
        tile.putpixel((0, y), _rgb(colour))
    return tile


def _plaid():
    tile = Image.new('RGB', (8, 8), _rgb('#8FD3BC'))
    base, light = _rgb('#8FD3BC'), (255, 255, 255)
    one = tuple(round(b + (l - b) * 0.42) for b, l in zip(base, light))
    two = tuple(round(o + (l - o) * 0.42) for o, l in zip(one, light))
    for y in range(8):
        for x in range(8):
            n = (x < 4) + (y < 4)
            tile.putpixel((x, y), (base, one, two)[n])
    return tile


_MAKERS = {'black': lambda: _solid('#000000'), 'gray': lambda: _solid('#5c6169'), 'mosaic': _mosaic, 'stripe': _stripe,
           'heart': _hearts, 'paw': _paws, 'snow': _snow, 'seal': _seal, 'star': _stars, 'dot': _dots,
           'rainbow': _rainbow, 'plaid': _plaid}


@lru_cache(maxsize=64)
def tile(style: str, dpi: int) -> Image.Image:
    """The texture of ``style`` at ``dpi``; drawn at about 100 dpi and enlarged by whole pixels."""
    if style not in _MAKERS:
        raise StyleError(style)
    base = _MAKERS[style]()
    k = max(1, round(dpi / 100))
    return base if k == 1 else base.resize((base.width * k, base.height * k), Image.Resampling.NEAREST)


def expected(style: str, dpi: int, rect) -> Image.Image:
    """The pixels ``rect`` (left, top, right, bottom; right/bottom exclusive) holds when painted."""
    left, top, right, bottom = rect
    t = tile(style, dpi)
    out = Image.new('RGB', (right - left, bottom - top))
    x0, y0 = left - left % t.width, top - top % t.height
    for y in range(y0, bottom, t.height):
        for x in range(x0, right, t.width):
            out.paste(t, (x - left, y - top))
    return out


def paint(image: Image.Image, rect, style: str, dpi: int) -> None:
    left, top, right, bottom = rect
    patch = expected(style, dpi, rect)
    try:
        image.paste(patch, (left, top))
    finally:
        patch.close()
