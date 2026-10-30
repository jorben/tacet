"""Builds the app icon masters from the brand logo.

Run from the repository root when the brand logo changes:

    uv run --with pillow==12.1.1 python build/icons/make_masters.py

The brand original (docs/v1/assets/brand/tacet-seedling-logo.png) is an
opaque square. The prototype shows it with 22% rounded corners, and the
masters keep that shape:

- macos.png follows Apple's 1024 px icon grid: an 824 px rounded square
  centred with a 100 px transparent margin and a soft drop shadow, so the
  icon sits at the same visual size as other apps in the Dock.
- windows.png fills the canvas apart from a small margin and has no shadow,
  because the taskbar and Start menu show icons at 16 to 48 px.

The build turns these masters into darwin/icons.icns and windows/icon.ico
(build/Taskfile.yml, generate:icons); those outputs are not committed.
"""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "docs/v1/assets/brand/tacet-seedling-logo.png"
OUT = Path(__file__).resolve().parent

SIZE = 1024
RADIUS = 0.225  # Apple's corner radius on the 824 px grid (185.4 px); the prototype uses 22%
SUPERSAMPLE = 4  # draw corners large, then scale down for clean anti-aliasing


def rounded_mask(size: int) -> Image.Image:
    big = size * SUPERSAMPLE
    mask = Image.new("L", (big, big), 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, big - 1, big - 1), radius=round(big * RADIUS), fill=255)
    return mask.resize((size, size), Image.LANCZOS)


def master(source: Image.Image, plate: int, shadow: tuple[int, float, int] | None) -> Image.Image:
    art = source.resize((plate, plate), Image.LANCZOS).convert("RGBA")
    art.putalpha(rounded_mask(plate))
    canvas = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    offset = (SIZE - plate) // 2
    if shadow:
        dy, blur, alpha = shadow
        silhouette = Image.new("RGBA", (plate, plate), (0, 0, 0, 255))
        silhouette.putalpha(art.getchannel("A").point(lambda v: v * alpha // 255))
        layer = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
        layer.alpha_composite(silhouette, (offset, offset + dy))
        canvas = Image.alpha_composite(canvas, layer.filter(ImageFilter.GaussianBlur(blur)))
    canvas.alpha_composite(art, (offset, offset))
    return canvas


def main() -> None:
    source = Image.open(SOURCE)
    if source.size != (SIZE, SIZE):
        raise SystemExit(f"{SOURCE} must be {SIZE} x {SIZE}, got {source.size}")
    # Apple template shadow: 10 px down, about 10 px blur, black at 30%
    master(source, 824, (10, 5.0, 77)).save(OUT / "macos.png", optimize=True)
    master(source, 984, None).save(OUT / "windows.png", optimize=True)


if __name__ == "__main__":
    main()
