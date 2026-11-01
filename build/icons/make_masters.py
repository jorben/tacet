"""Builds the app icon masters from the brand logo.

The build runs this automatically through common:generate:icon-masters.
To regenerate manually from the repository root:

    uv run --no-project --python 3.13 --with pillow==12.1.1 python build/icons/make_masters.py

The brand original (docs/v1/assets/brand/tacet-seedling-logo.png) is an
opaque square. The prototype shows it with 22% rounded corners, and the
masters keep that shape:

- macos.png follows Apple's 1024 px icon grid: an 824 px rounded square
  centred with a 100 px transparent margin and a soft drop shadow, so the
  icon sits at the same visual size as other apps in the Dock.
- windows.png fills the canvas apart from a small margin and has no shadow,
  because the taskbar and Start menu show icons at 16 to 48 px.

Development variants (macos-dev.png and windows-dev.png) add a yellow DEV
badge inset within the top-right corner, preserving the release icon's alpha
channel and outer silhouette. The embedded Pillow font keeps generation independent
of fonts installed on the build machine.

The build turns these masters into darwin/icons[.dev].icns and
windows/icon[.dev].ico (build/Taskfile.yml, generate:icons). Both the masters
and final icons are ignored; only the brand original and this script are committed.
"""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

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


def development_badge(icon: Image.Image, plate: int) -> Image.Image:
    # Draw at 4x so the rounded tag and text stay crisp at small icon sizes.
    layer = Image.new("RGBA", (SIZE * SUPERSAMPLE, SIZE * SUPERSAMPLE))
    draw = ImageDraw.Draw(layer)
    offset = (SIZE - plate) // 2
    # Keep the entire tag inside the plate's curved corner. Protruding into
    # transparent pixels changes the silhouette used by macOS icon adaptation.
    left = round((offset + plate * 0.55) * SUPERSAMPLE)
    top = round((offset + plate * 0.075) * SUPERSAMPLE)
    width = round(plate * 0.36 * SUPERSAMPLE)
    height = round(plate * 0.185 * SUPERSAMPLE)
    draw.rounded_rectangle(
        (left, top, left + width, top + height),
        radius=round(height * 0.22),
        fill="#FFD43B",
    )
    font = ImageFont.load_default(size=round(height * 0.65))
    stroke = round(height * 0.012)
    x0, y0, x1, y1 = draw.textbbox((0, 0), "DEV", font=font, stroke_width=stroke)
    draw.text(
        (left + (width - (x1 - x0)) / 2 - x0, top + (height - (y1 - y0)) / 2 - y0),
        "DEV",
        font=font,
        fill="#20251C",
        stroke_width=stroke,
        stroke_fill="#20251C",
    )
    result = Image.alpha_composite(icon, layer.resize(icon.size, Image.LANCZOS))
    # The badge changes artwork only; the outer mask and shadow stay identical.
    result.putalpha(icon.getchannel("A"))
    return result


def main() -> None:
    source = Image.open(SOURCE)
    if source.size != (SIZE, SIZE):
        raise SystemExit(f"{SOURCE} must be {SIZE} x {SIZE}, got {source.size}")
    # Apple template shadow: 10 px down, about 10 px blur, black at 30%
    macos = master(source, 824, (10, 5.0, 77))
    windows = master(source, 984, None)
    macos.save(OUT / "macos.png", optimize=True)
    windows.save(OUT / "windows.png", optimize=True)
    development_badge(macos, 824).save(OUT / "macos-dev.png", optimize=True)
    development_badge(windows, 984).save(OUT / "windows-dev.png", optimize=True)


if __name__ == "__main__":
    main()
