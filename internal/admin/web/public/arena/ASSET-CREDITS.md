# Arena pixel-art assets — credits & licenses

These sprites power the `/app/leaderboard` "Agent office" visualization. All
assets are bundled locally (no CDN, no runtime fetch) and are free for
commercial use.

## Office furniture, floors, walls
- `furniture/`, `floors/`, `walls/`
- Source: [`pablodelucca/pixel-agents`](https://github.com/pablodelucca/pixel-agents)
  (the project ships these as fully open-source, included in-repo).
- License: **MIT**.

## Characters
- `characters/char_*.png`
- Source: **MetroCity – Free Top-Down Character Pack** by **JIK-A-4**
  (<https://jik-a-4.itch.io/metrocity-free-topdown-character-pack>).
- License: **CC0 1.0 Universal** (public domain dedication — commercial use,
  modification, and redistribution all permitted; credit appreciated, not
  required).
- **Modification:** recolored toward hypitoken's emerald theme (cosmetic hue
  shift on clothing only) via `design/arena/recolor_characters.py`. Pristine
  CC0 originals are kept in `design/arena/_original/`; re-running the script
  always reads from there so the shift never compounds.

## Notes
- The recolor source (`_original/` + the Python script) lives under
  `design/arena/` at the repo root so it is **not** bundled into the SPA /
  the Go binary — only the final PNGs in this directory are served.
