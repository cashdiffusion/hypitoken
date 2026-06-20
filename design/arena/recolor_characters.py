#!/usr/bin/env python3
"""Generate hypitoken's office character pool from the (CC0) MetroCity sprites.

Source art is license-clean (CC0); this is a cosmetic pass that rotates the hue
of *saturated* pixels (clothing/hair) toward a set of palette hues, leaving skin
tones and neutral greys/whites alone. We emit one recolored sheet per
(hue × base sprite) so the office shows a varied team instead of a one-colour
crowd. Emerald (the brand hue) is emitted first so low indices stay on-brand.

Output: public/arena/characters/char_0.png .. char_<N-1>.png, where
N = len(HUES) × (#base sprites). The office assigns one per user by a stable
hash, so a given account always gets the same coworker.

Idempotent: always reads the pristine originals in ./_original.

Run:  python3 recolor_characters.py
"""
import colorsys
import os

from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(HERE, "_original")
DST = os.path.abspath(
    os.path.join(HERE, "..", "..", "internal", "admin", "web", "public", "arena", "characters")
)

# Palette hues (degrees). Emerald first = brand default for low hash indices.
HUES = [158, 202, 248, 286, 36, 342]  # emerald, sky, indigo, violet, amber, rose

SAT_FLOOR = 0.28  # below this, treat as skin/neutral and leave alone
HUE_BLEND = 0.55  # how far clothing hue is pulled toward the target
SAT_LIFT = 1.08  # gentle saturation lift so the colour reads as deliberate


def shift_pixel(r, g, b, target_hue):
    h, s, v = colorsys.rgb_to_hsv(r / 255.0, g / 255.0, b / 255.0)
    if s < SAT_FLOOR:
        return r, g, b
    dh = target_hue - h
    if dh > 0.5:
        dh -= 1.0
    elif dh < -0.5:
        dh += 1.0
    h = (h + dh * HUE_BLEND) % 1.0
    s = min(1.0, s * SAT_LIFT)
    nr, ng, nb = colorsys.hsv_to_rgb(h, s, v)
    return int(nr * 255), int(ng * 255), int(nb * 255)


def recolor(im, target_hue):
    out = im.copy()
    px = out.load()
    w, h = out.size
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a == 0:
                continue
            nr, ng, nb = shift_pixel(r, g, b, target_hue)
            px[x, y] = (nr, ng, nb, a)
    return out


def main():
    bases = [
        Image.open(os.path.join(SRC, f)).convert("RGBA")
        for f in sorted(f for f in os.listdir(SRC) if f.endswith(".png"))
    ]
    idx = 0
    for hue in HUES:
        th = hue / 360.0
        for base in bases:
            recolor(base, th).save(os.path.join(DST, f"char_{idx}.png"))
            idx += 1
    print(f"wrote {idx} character sheets ({len(HUES)} hues × {len(bases)} bases) to {DST}")


if __name__ == "__main__":
    main()
