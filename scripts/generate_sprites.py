#!/usr/bin/env python3
"""Generate pixel character spritesheets by palette-swapping a template.

Reads character definitions from a JSON file and recolors the pixel-agents
char_0.png template for each character using HSL-based palette swapping.

Usage:
    python3 scripts/generate_sprites.py characters.json
    python3 scripts/generate_sprites.py characters.json -o output_dir
    python3 scripts/generate_sprites.py characters.json -t custom_template.png

JSON format:
    {
      "my_char": {
        "name": "Display Name",
        "hair": "#FF0000",
        "eyes": "#00FF00",
        "skin": "#FFCCAA",
        "shirt": "#0000FF",
        "pants": "#333333",
        "shoes": "#111111"
      }
    }
"""

import argparse
import colorsys
import json
import os
import sys
from PIL import Image

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
DEFAULT_TEMPLATE = os.path.join(SCRIPT_DIR, "char_0.png")
DEFAULT_OUTPUT = os.path.join(os.path.expanduser("~"), ".tetora", "media", "sprites")

TEMPLATE_URL = "https://raw.githubusercontent.com/pablodelucca/pixel-agents/main/webview-ui/public/assets/characters/char_0.png"

# ---------------------------------------------------------------------------
# Source color groups (from pixel-agents char_0.png analysis)
# Each group: list of (R, G, B) tuples
# ---------------------------------------------------------------------------

SRC_GROUPS = {
    'hair': [
        (0x39, 0x16, 0x24),
        (0x32, 0x19, 0x1D),
        (0x34, 0x1F, 0x20),
    ],
    'shirt': [
        (0x07, 0x1C, 0x2E),
        (0x0F, 0x40, 0x6A),
        (0x11, 0x49, 0x78),
        (0x11, 0x64, 0xA9),
    ],
    'pants': [
        (0x6D, 0x47, 0x26),
        (0x6F, 0x4A, 0x2A),
        (0x8F, 0x64, 0x39),
        (0xB1, 0x86, 0x49),
    ],
    'skin': [
        (0x84, 0x52, 0x3A),
        (0xC5, 0x89, 0x6E),
        (0xE2, 0x98, 0x78),
        (0xE9, 0xA3, 0x84),
        (0xFB, 0xBF, 0x97),
        (0xFF, 0xD8, 0xB2),
    ],
    'shoes': [
        (0x35, 0x2C, 0x27),
        (0x35, 0x35, 0x35),
        (0x40, 0x36, 0x32),
        (0x49, 0x3E, 0x38),
        (0x4F, 0x4F, 0x4F),
        (0x59, 0x59, 0x59),
        (0x9F, 0x9F, 0x9F),
    ],
    'eyes': [
        (0xFF, 0xFF, 0xFF),
    ],
    'outline': [
        (0x00, 0x00, 0x00),
        (0x04, 0x06, 0x05),
        (0x19, 0x19, 0x19),
        (0x1A, 0x1A, 0x1A),
    ],
    'accessory': [
        (0xA2, 0xAA, 0xAF),
        (0xE1, 0xE3, 0xE9),
    ],
}

# ---------------------------------------------------------------------------
# Color math helpers
# ---------------------------------------------------------------------------

def hex_to_rgb(h):
    """Parse '#RRGGBB' to (R, G, B) tuple."""
    h = h.lstrip('#')
    return (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16))


def rgb_to_hls(r, g, b):
    return colorsys.rgb_to_hls(r / 255.0, g / 255.0, b / 255.0)


def hls_to_rgb(h, l, s):
    r, g, b = colorsys.hls_to_rgb(h, l, s)
    return (
        max(0, min(255, int(round(r * 255)))),
        max(0, min(255, int(round(g * 255)))),
        max(0, min(255, int(round(b * 255)))),
    )


def clamp01(v):
    return max(0.0, min(1.0, v))


def group_base_hls(colors):
    """Return the HLS of the median-luminance color in a group."""
    hls_list = [rgb_to_hls(*c) for c in colors]
    hls_list.sort(key=lambda x: x[1])
    return hls_list[len(hls_list) // 2]


def recolor(src_rgb, src_base_hls, tgt_base_hls):
    """Recolor a pixel using proportional HSL scaling."""
    sh, sl, ss = rgb_to_hls(*src_rgb)
    _, sbl, sbs = src_base_hls
    th, tl, ts = tgt_base_hls

    if sbl > 0.01:
        new_l = clamp01(tl * (sl / sbl))
    else:
        new_l = tl

    if sbs > 0.05:
        new_s = clamp01(ts * (ss / sbs))
    else:
        new_s = ts

    return hls_to_rgb(th, new_l, new_s)


# ---------------------------------------------------------------------------
# Build color map & generate sprites
# ---------------------------------------------------------------------------

def load_characters(json_path):
    """Load character definitions from JSON, converting hex colors to RGB tuples."""
    with open(json_path) as f:
        raw = json.load(f)

    characters = {}
    color_keys = {'hair', 'eyes', 'skin', 'shirt', 'pants', 'shoes'}
    for char_id, data in raw.items():
        palette = {}
        for k, v in data.items():
            if k in color_keys and isinstance(v, str):
                palette[k] = hex_to_rgb(v)
            else:
                palette[k] = v
        characters[char_id] = palette

    return characters


def build_color_map(palette):
    """Build source->target RGB mapping for all classified colors."""
    cmap = {}

    for group, src_colors in SRC_GROUPS.items():
        if group in ('outline', 'accessory'):
            for c in src_colors:
                cmap[c] = c
            continue

        if group == 'eyes':
            tgt = palette.get('eyes', (0xFF, 0xFF, 0xFF))
            for c in src_colors:
                cmap[c] = tgt
            continue

        if group not in palette:
            for c in src_colors:
                cmap[c] = c
            continue

        src_base = group_base_hls(src_colors)
        tgt_base = rgb_to_hls(*palette[group])

        for c in src_colors:
            cmap[c] = recolor(c, src_base, tgt_base)

    return cmap


def generate_sprite(template_path, cmap, output_path):
    """Apply color map to template and save."""
    img = Image.open(template_path).convert('RGBA')
    px = img.load()
    w, h = img.size

    unmapped = set()
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a == 0:
                continue
            key = (r, g, b)
            if key in cmap:
                nr, ng, nb = cmap[key]
                px[x, y] = (nr, ng, nb, a)
            else:
                unmapped.add(key)

    if unmapped:
        hexes = sorted(f'#{r:02X}{g:02X}{b:02X}' for r, g, b in unmapped)
        print(f"  Warning: {len(unmapped)} unmapped colors: {hexes}")

    img.save(output_path)
    return img.size


def verify_output(output_path, cmap):
    """Verify no source palette colors remain in the output."""
    img = Image.open(output_path).convert('RGBA')
    px = img.load()
    w, h = img.size

    src_colors = set()
    target_colors = set()
    for group in SRC_GROUPS:
        if group in ('outline', 'accessory'):
            continue
        for c in SRC_GROUPS[group]:
            mapped = cmap.get(c)
            if mapped and mapped != c:
                src_colors.add(c)
                target_colors.add(mapped)

    src_colors -= target_colors

    residual = {}
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a == 0:
                continue
            key = (r, g, b)
            if key in src_colors:
                residual[key] = residual.get(key, 0) + 1

    if residual:
        print(f"  FAIL: {len(residual)} source colors still present!")
        for c, n in residual.items():
            print(f"    #{c[0]:02X}{c[1]:02X}{c[2]:02X} -- {n} pixels")
        return False
    return True


def main():
    parser = argparse.ArgumentParser(
        description="Generate pixel character spritesheets by palette-swapping a template."
    )
    parser.add_argument("characters", help="JSON file with character palette definitions")
    parser.add_argument("-t", "--template", default=DEFAULT_TEMPLATE,
                        help=f"Template spritesheet PNG (default: {DEFAULT_TEMPLATE})")
    parser.add_argument("-o", "--output", default=DEFAULT_OUTPUT,
                        help=f"Output directory (default: {DEFAULT_OUTPUT})")
    args = parser.parse_args()

    if not os.path.exists(args.template):
        print(f"ERROR: Template not found at {args.template}")
        print("Download it first:")
        print(f"  curl -o {args.template} {TEMPLATE_URL}")
        sys.exit(1)

    if not os.path.exists(args.characters):
        print(f"ERROR: Character file not found: {args.characters}")
        sys.exit(1)

    characters = load_characters(args.characters)
    os.makedirs(args.output, exist_ok=True)

    print("=== Pixel Sprite Generator ===")
    print(f"Template:   {args.template}")
    print(f"Characters: {args.characters} ({len(characters)} defined)")
    print(f"Output:     {args.output}/\n")

    all_ok = True
    for char_id, palette in characters.items():
        name = palette.get('name', char_id)
        print(f"[{name}] Generating {char_id}.png ...")
        cmap = build_color_map(palette)
        out = os.path.join(args.output, f"{char_id}.png")
        size = generate_sprite(args.template, cmap, out)
        print(f"  Size: {size[0]}x{size[1]}")

        ok = verify_output(out, cmap)
        if ok:
            print(f"  Verify: OK")
        all_ok = all_ok and ok
        print()

    if all_ok:
        print("All sprites generated and verified successfully!")
    else:
        print("Some sprites have issues -- check warnings above.")
        sys.exit(1)


if __name__ == '__main__':
    main()
