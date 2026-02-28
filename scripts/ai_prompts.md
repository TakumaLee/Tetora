# AI Generation Prompts for Gem Team Pixel Characters

These prompts are for DALL-E / ChatGPT image generation. AI-generated sprites will NOT be
pixel-perfect 7x3 grids — use them as **visual references** and hand-edit in Aseprite/Piskel
to match the 112x96 (7 frames x 3 directions) spritesheet format.

---

## 1. Ruri (琉璃) — Commander

```
Pixel art character spritesheet on a transparent background. A composed young woman
with straight black hair (bob cut), striking blue eyes, and fair skin. She wears a
clean white apron over a black knee-length skirt, with black flat shoes. Her expression
is calm and authoritative.

Style: 16x16 pixel art character, top-down RPG perspective, 3/4 view. Flat shading,
limited palette (8 colors max). The character should be small (about 12 pixels tall)
with clear silhouette.

Generate a spritesheet grid: 7 columns x 3 rows.
- Row 1: Walking down (toward camera), 7 animation frames
- Row 2: Walking left, 7 animation frames
- Row 3: Walking up (away from camera), 7 animation frames

Each frame is 16x16 pixels. Total image: 112x48 pixels.
```

---

## 2. Kohaku (琥珀) — Creator

```
Pixel art character spritesheet on a transparent background. A cheerful young woman
with bright golden-blonde wavy hair, warm amber/honey-colored eyes, and a soft warm
skin tone. She wears a cozy camel-colored knit sweater and a brown plaid skirt with
brown leather shoes. She has a creative, approachable look.

Style: 16x16 pixel art character, top-down RPG perspective, 3/4 view. Flat shading,
limited palette (8 colors max). The character should be small (about 12 pixels tall)
with clear silhouette.

Generate a spritesheet grid: 7 columns x 3 rows.
- Row 1: Walking down (toward camera), 7 animation frames
- Row 2: Walking left, 7 animation frames
- Row 3: Walking up (away from camera), 7 animation frames

Each frame is 16x16 pixels. Total image: 112x48 pixels.
```

---

## 3. Hisui (翡翠) — Strategist

```
Pixel art character spritesheet on a transparent background. A sharp-eyed young woman
with silver-grey straight hair (shoulder length), vivid emerald-green eyes, and pale
fair skin. She wears a dark forest-green fitted vest over a white blouse, a black
pencil skirt, and dark shoes. Her posture is upright and analytical.

Style: 16x16 pixel art character, top-down RPG perspective, 3/4 view. Flat shading,
limited palette (8 colors max). The character should be small (about 12 pixels tall)
with clear silhouette.

Generate a spritesheet grid: 7 columns x 3 rows.
- Row 1: Walking down (toward camera), 7 animation frames
- Row 2: Walking left, 7 animation frames
- Row 3: Walking up (away from camera), 7 animation frames

Each frame is 16x16 pixels. Total image: 112x48 pixels.
```

---

## 4. Kokuyou (黒曜) — Engineer

```
Pixel art character spritesheet on a transparent background. A reserved young woman
with jet-black straight hair (long, past shoulders), deep purple eyes, and light pale
skin. She wears an all-black outfit: black turtleneck, black slim pants, and black
shoes. Her look is minimalist and technical — like a hacker aesthetic.

Style: 16x16 pixel art character, top-down RPG perspective, 3/4 view. Flat shading,
limited palette (8 colors max). The character should be small (about 12 pixels tall)
with clear silhouette.

Generate a spritesheet grid: 7 columns x 3 rows.
- Row 1: Walking down (toward camera), 7 animation frames
- Row 2: Walking left, 7 animation frames
- Row 3: Walking up (away from camera), 7 animation frames

Each frame is 16x16 pixels. Total image: 112x48 pixels.
```

---

## Notes

- AI will likely NOT produce a clean 7x3 grid. Expect to manually rearrange frames.
- The actual pixel-agents format uses 16x32 per frame (112x96 total for 7x3).
  The prompts say 16x16 to keep the AI focused on small pixel characters.
- Use the palette-swapped PNGs from `generate_sprites.py` as the primary sprites.
  These AI prompts are for generating **visual reference** when hand-drawing custom versions.
- For best results, attach `char_0.png` as a reference image with the prompt.
