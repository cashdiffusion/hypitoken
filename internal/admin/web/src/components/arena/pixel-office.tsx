// PixelOffice — a 2D pixel-art office where each leaderboard player is a pixel
// coworker that WALKS the room (BFS pathfinding over a tile map), wanders, and
// returns to a desk to "type" when a billed request lands (pushed via the
// `pulse(actor)` ref from the page's SSE stream — fires for every token and
// both Claude + Codex).
//
// The scene is a furnished room: a desk floor that scales with headcount (up to
// 40 seats), a top wall with paintings / clock / whiteboard, a lounge with a
// sofa + coffee table + plants, bookshelves, and scattered greenery. Furniture
// and characters are drawn back-to-front by a painter's algorithm (baseline Y
// sort) so people tuck behind desks and pass in front of low props correctly.
//
// Rendering is crisp: drawn at an integer zoom into a DPR-sized backing store
// with image smoothing OFF. Name tags are DOM nodes layered over the canvas so
// text never blurs.
//
// Sprites: characters (MetroCity, CC0, recolored into 36 palette variants) are
// 112×96 = 7 frames × 3 rows of 16×32 (rows: down/up/right, left = mirror).
// Furniture is the MIT pixel-agents set. See public/arena/ASSET-CREDITS.md.
// Movement model adapted from pablodelucca/pixel-agents (MIT).

import { useReducedMotion } from "motion/react";
import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";

export interface OfficePlayer {
  actor: string;
  name: string;
  isYou: boolean;
}
export interface PixelOfficeHandle {
  pulse: (actor: string) => void;
}

// ---- tunables ---------------------------------------------------------------
const TILE = 16;
const CHAR_W = 16;
const CHAR_H = 32;
const CHAR_COUNT = 36;
const WALK_SPEED = 34;
const WALK_FRAME_SEC = 0.16;
const TYPE_FRAME_SEC = 0.28;
const WANDER_PAUSE = [1.4, 6.0];
const WANDER_MOVES = [2, 5];
const TYPE_AFTER_PULSE = 6.0;
const MAX_AGENTS = 40;

const UNITS_PER_ROW = 5; // desks per row
const UNIT_W = 3; // desk width in tiles
const GAP = 1; // gap between desk units
const MARGIN_X = 2;
const WALL_H = 3; // top wall rows
const LOUNGE_H = 5; // bottom lounge band rows

const A = "/arena";
const SRC = {
  floor: `${A}/floors/floor_2.png`,
  floorAlt: `${A}/floors/floor_3.png`,
  rug: `${A}/floors/floor_6.png`,
  wall: `${A}/walls/wall_0.png`,
  desk: `${A}/furniture/DESK/DESK_FRONT.png`,
  pc1: `${A}/furniture/PC/PC_FRONT_ON_1.png`,
  pc2: `${A}/furniture/PC/PC_FRONT_ON_2.png`,
  pc3: `${A}/furniture/PC/PC_FRONT_ON_3.png`,
  sofa: `${A}/furniture/SOFA/SOFA_FRONT.png`,
  coffeeTable: `${A}/furniture/COFFEE_TABLE/COFFEE_TABLE.png`,
  largePlant: `${A}/furniture/LARGE_PLANT/LARGE_PLANT.png`,
  plant: `${A}/furniture/PLANT/PLANT.png`,
  plant2: `${A}/furniture/PLANT_2/PLANT_2.png`,
  cactus: `${A}/furniture/CACTUS/CACTUS.png`,
  hanging: `${A}/furniture/HANGING_PLANT/HANGING_PLANT.png`,
  bookshelf: `${A}/furniture/BOOKSHELF/BOOKSHELF.png`,
  dblBookshelf: `${A}/furniture/DOUBLE_BOOKSHELF/DOUBLE_BOOKSHELF.png`,
  whiteboard: `${A}/furniture/WHITEBOARD/WHITEBOARD.png`,
  largePainting: `${A}/furniture/LARGE_PAINTING/LARGE_PAINTING.png`,
  smallPainting: `${A}/furniture/SMALL_PAINTING/SMALL_PAINTING.png`,
  clock: `${A}/furniture/CLOCK/CLOCK.png`,
  bin: `${A}/furniture/BIN/BIN.png`,
  coffee: `${A}/furniture/COFFEE/COFFEE.png`,
  bench: `${A}/furniture/WOODEN_BENCH/WOODEN_BENCH.png`,
  pot: `${A}/furniture/POT/POT.png`,
  smallTable: `${A}/furniture/SMALL_TABLE/SMALL_TABLE_FRONT.png`,
};
const PC_KEYS = ["pc1", "pc2", "pc3"] as const;
const CHAR_SRCS = Array.from({ length: CHAR_COUNT }, (_, i) => `${A}/characters/char_${i}.png`);

// ---- types ------------------------------------------------------------------
type Dir = 0 | 1 | 2 | 3;
type AState = "idle" | "walk" | "type";
interface Cell {
  col: number;
  row: number;
}
interface Seat {
  col: number;
  row: number;
}
// A drawable furniture sprite. `monitor` desks also paint an animated PC.
interface Prop {
  key: keyof typeof SRC;
  x: number;
  y: number;
  baseline: number;
  monitor?: boolean;
}
// Flat wall art drawn in the background (no depth sort, never blocks).
interface WallArt {
  key: keyof typeof SRC;
  x: number;
  y: number;
}
interface Agent {
  actor: string;
  name: string;
  isYou: boolean;
  sprite: number;
  seat: Seat | null;
  x: number;
  y: number;
  col: number;
  row: number;
  state: AState;
  dir: Dir;
  path: Cell[];
  prog: number;
  frame: number;
  frameT: number;
  wanderT: number;
  wanderN: number;
  wanderLimit: number;
}
interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}
interface Layout {
  cols: number;
  rows: number;
  blocked: boolean[][];
  seats: Seat[];
  props: Prop[];
  wallArt: WallArt[];
  rugs: Rect[];
}

// mulberry32 — tiny seeded PRNG so a given headcount always yields the same
// (organic-looking) layout instead of reshuffling on every re-render.
function mulberry32(seed: number) {
  let s = seed >>> 0;
  return () => {
    s |= 0;
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---- layout -----------------------------------------------------------------
function buildLayout(n: number): Layout {
  const deskCount = Math.min(Math.max(n, 1), MAX_AGENTS);
  const rng = mulberry32(Math.imul(deskCount + 1, 0x9e3779b1));
  const ri = (a: number, b: number) => a + Math.floor(rng() * (b - a + 1));

  // Distribute desks across rows of 4–5 so rows aren't all identical.
  const rowPlan: number[] = [];
  for (let rem = deskCount; rem > 0; ) {
    const take = Math.min(rem, ri(4, UNITS_PER_ROW));
    rowPlan.push(take);
    rem -= take;
  }
  const deskRows = rowPlan.length;
  // +2 slack columns give each row room to jitter left/right.
  const cols = MARGIN_X * 2 + UNITS_PER_ROW * UNIT_W + (UNITS_PER_ROW - 1) * GAP + 2;
  const rows = WALL_H + deskRows * 4 + LOUNGE_H + 1;
  const blocked: boolean[][] = Array.from({ length: rows }, () => Array(cols).fill(false));
  const block = (c: number, r: number) => {
    if (r >= 0 && r < rows && c >= 0 && c < cols) blocked[r][c] = true;
  };
  const usable = cols - 2; // interior cols 1..cols-2

  // border + top wall band
  for (let c = 0; c < cols; c++) for (let r = 0; r < WALL_H; r++) block(c, r);
  for (let c = 0; c < cols; c++) block(c, rows - 1);
  for (let r = 0; r < rows; r++) {
    block(0, r);
    block(cols - 1, r);
  }

  const seats: Seat[] = [];
  const props: Prop[] = [];
  const protectedEntry = new Set<number>(); // tiles a seat is entered from
  rowPlan.forEach((count, dr) => {
    const top = WALL_H + dr * 4; // [aisle, seat, desk, desk]
    const seatRow = top + 1;
    const deskRow = top + 2;
    // random gaps (1–2) between desks + a random left offset within the slack
    const gaps = Array.from({ length: count - 1 }, () => ri(GAP, GAP + 1));
    const rowW = count * UNIT_W + gaps.reduce((s, g) => s + g, 0);
    let c = 1 + ri(0, Math.max(0, usable - rowW));
    for (let u = 0; u < count; u++) {
      for (let cc = 0; cc < UNIT_W; cc++) {
        block(c + cc, deskRow);
        block(c + cc, deskRow + 1);
      }
      props.push({
        key: "desk",
        x: c * TILE,
        y: deskRow * TILE,
        baseline: deskRow * TILE + 32,
        monitor: true,
      });
      seats.push({ col: c + 1, row: seatRow });
      protectedEntry.add((seatRow - 1) * cols + (c + 1)); // aisle tile above seat
      c += UNIT_W + (gaps[u] ?? 0);
    }
  });

  // ---- wall art (randomised placement along the top wall) -------------------
  const wallArt: WallArt[] = [];
  const artRow = (WALL_H - 2) * TILE;
  wallArt.push({ key: "largePainting", x: ri(1, 3) * TILE, y: artRow });
  wallArt.push({
    key: "whiteboard",
    x: ri(Math.floor(cols * 0.35), Math.floor(cols * 0.5)) * TILE,
    y: artRow,
  });
  wallArt.push({ key: "clock", x: Math.floor(cols * 0.7) * TILE, y: (WALL_H - 3) * TILE });
  wallArt.push({ key: "smallPainting", x: (cols - ri(3, 5)) * TILE, y: (WALL_H - 2) * TILE - 8 });
  wallArt.push({ key: "hanging", x: Math.floor(cols * (0.55 + rng() * 0.1)) * TILE, y: 0 });

  // ---- lounge (one bottom corner, chosen at random) -------------------------
  const rugs: Rect[] = [];
  const loungeTop = rows - 1 - LOUNGE_H;
  const leftSide = rng() < 0.5;
  const lx = leftSide ? 2 : cols - 9;
  rugs.push({ x: lx * TILE, y: (loungeTop + 1) * TILE, w: 7 * TILE, h: (LOUNGE_H - 1) * TILE });
  const sofaY = loungeTop + 1;
  props.push({ key: "sofa", x: lx * TILE, y: sofaY * TILE, baseline: (sofaY + 1) * TILE });
  block(lx, sofaY);
  block(lx + 1, sofaY);
  props.push({
    key: "coffeeTable",
    x: (lx + 1) * TILE,
    y: (sofaY + 1) * TILE,
    baseline: (sofaY + 3) * TILE,
  });
  block(lx + 1, sofaY + 1);
  block(lx + 2, sofaY + 1);
  props.push({
    key: "largePlant",
    x: (lx + 4) * TILE,
    y: (loungeTop - 1) * TILE,
    baseline: (loungeTop + 2) * TILE,
  });
  block(lx + 4, loungeTop + 1);
  props.push({
    key: "bench",
    x: (lx + 5) * TILE,
    y: (sofaY + 1) * TILE,
    baseline: (sofaY + 2) * TILE,
  });
  block(lx + 5, sofaY + 1);
  // bookshelves against the bottom wall (opposite corner)
  const shelfX = leftSide ? cols - 3 : 1;
  props.push({
    key: "dblBookshelf",
    x: shelfX * TILE,
    y: (rows - 3) * TILE,
    baseline: (rows - 1) * TILE,
  });
  block(shelfX, rows - 2);
  block(shelfX + 1, rows - 2);

  // ---- scattered greenery / clutter with a connectivity guard ---------------
  // Reachability flood-fill: every seat must stay reachable after a placement,
  // so random clutter can never trap a coworker at a desk.
  const seatKeys = seats.map((s) => s.row * cols + s.col);
  const reachable = (): boolean => {
    let anchor = -1;
    for (let r = WALL_H; r < rows - 1 && anchor < 0; r++)
      for (let c2 = 1; c2 < cols - 1; c2++)
        if (!blocked[r][c2]) {
          anchor = r * cols + c2;
          break;
        }
    if (anchor < 0) return false;
    const seen = new Set<number>([anchor]);
    let q = [anchor];
    while (q.length) {
      const nq: number[] = [];
      for (const k of q) {
        const r = Math.floor(k / cols);
        const c2 = k % cols;
        for (const [dc, dr] of [
          [0, 1],
          [0, -1],
          [1, 0],
          [-1, 0],
        ]) {
          const nc = c2 + dc;
          const nr = r + dr;
          if (nc < 0 || nr < 0 || nc >= cols || nr >= rows || blocked[nr][nc]) continue;
          const nk = nr * cols + nc;
          if (seen.has(nk)) continue;
          seen.add(nk);
          nq.push(nk);
        }
      }
      q = nq;
    }
    return seatKeys.every((k) => seen.has(k));
  };

  const tall = new Set<keyof typeof SRC>(["plant", "plant2", "cactus"]);
  const decorPool: (keyof typeof SRC)[] = [
    "plant",
    "plant2",
    "cactus",
    "pot",
    "bin",
    "coffee",
    "smallTable",
  ];
  // candidate free body tiles (not seats, not seat entries)
  const free: number[] = [];
  for (let r = WALL_H; r < rows - 1; r++)
    for (let c2 = 1; c2 < cols - 1; c2++) {
      const k = r * cols + c2;
      if (!blocked[r][c2] && !seatKeys.includes(k) && !protectedEntry.has(k)) free.push(k);
    }
  // shuffle (Fisher–Yates with the seeded rng)
  for (let i = free.length - 1; i > 0; i--) {
    const j = Math.floor(rng() * (i + 1));
    [free[i], free[j]] = [free[j], free[i]];
  }
  const target = Math.min(free.length, Math.round(deskCount * 0.7) + 4);
  let added = 0;
  for (const k of free) {
    if (added >= target) break;
    const r = Math.floor(k / cols);
    const c2 = k % cols;
    const key = decorPool[ri(0, decorPool.length - 1)];
    const w = key === "smallTable" ? 2 : 1;
    if (c2 + w > cols - 1) continue;
    // tentatively block footprint
    const cells: [number, number][] = [];
    for (let cc = 0; cc < w; cc++) cells.push([c2 + cc, r]);
    if (cells.some(([cx, cy]) => blocked[cy][cx] || protectedEntry.has(cy * cols + cx))) continue;
    for (const [cx, cy] of cells) block(cx, cy);
    if (!reachable()) {
      cells.forEach(([cx, cy]) => {
        blocked[cy][cx] = false;
      });
      continue;
    }
    const th = tall.has(key);
    props.push({
      key,
      x: c2 * TILE,
      y: th ? r * TILE - 16 : r * TILE,
      baseline: (r + 1) * TILE,
    });
    if (rng() < 0.25) rugs.push({ x: c2 * TILE, y: r * TILE, w: TILE, h: TILE });
    added++;
  }

  return { cols, rows, blocked, seats, props, wallArt, rugs };
}

// BFS over 4-neighbours of walkable tiles; returns tiles AFTER `from`.
function bfsPath(layout: Layout, from: Cell, to: Cell): Cell[] {
  if (from.col === to.col && from.row === to.row) return [];
  const { cols, rows, blocked } = layout;
  const key = (c: number, r: number) => r * cols + c;
  const prev = new Map<number, number>();
  const seen = new Set<number>([key(from.col, from.row)]);
  let q: Cell[] = [from];
  const ok = (c: number, r: number) => c >= 0 && r >= 0 && c < cols && r < rows && !blocked[r][c];
  while (q.length) {
    const nextq: Cell[] = [];
    for (const cur of q) {
      for (const [dc, dr] of [
        [0, 1],
        [0, -1],
        [1, 0],
        [-1, 0],
      ]) {
        const nc = cur.col + dc;
        const nr = cur.row + dr;
        if (!ok(nc, nr) || seen.has(key(nc, nr))) continue;
        seen.add(key(nc, nr));
        prev.set(key(nc, nr), key(cur.col, cur.row));
        if (nc === to.col && nr === to.row) {
          const path: Cell[] = [];
          let k = key(nc, nr);
          const start = key(from.col, from.row);
          while (k !== start) {
            path.push({ col: k % cols, row: Math.floor(k / cols) });
            const p = prev.get(k);
            if (p == null) break;
            k = p;
          }
          return path.reverse();
        }
        nextq.push({ col: nc, row: nr });
      }
    }
    q = nextq;
  }
  return [];
}

function randRange([a, b]: number[]) {
  return a + Math.random() * (b - a);
}
function randInt([a, b]: number[]) {
  return Math.floor(a + Math.random() * (b - a + 1));
}
function spriteIndex(actor: string): number {
  let h = 2166136261;
  for (let i = 0; i < actor.length; i++) {
    h ^= actor.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return Math.abs(h) % CHAR_COUNT;
}
function loadImage(src: string): Promise<HTMLImageElement | null> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => resolve(null);
    img.src = src;
  });
}

// ---- component --------------------------------------------------------------
export const PixelOffice = forwardRef<PixelOfficeHandle, { players: OfficePlayer[] }>(
  function PixelOffice({ players }, ref) {
    const reduce = useReducedMotion();
    const wrapRef = useRef<HTMLDivElement | null>(null);
    const canvasRef = useRef<HTMLCanvasElement | null>(null);
    const labelLayerRef = useRef<HTMLDivElement | null>(null);
    const [zoom, setZoom] = useState(3);

    const list = useMemo(() => players.slice(0, MAX_AGENTS), [players]);
    const layout = useMemo(() => buildLayout(list.length || 1), [list.length]);
    const roomW = layout.cols * TILE;
    const roomH = layout.rows * TILE;

    const pulses = useRef<Map<string, number>>(new Map());
    useImperativeHandle(ref, () => ({
      pulse: (actor: string) => {
        pulses.current.set(actor, performance.now() + TYPE_AFTER_PULSE * 1000);
      },
    }));

    useEffect(() => {
      const el = wrapRef.current;
      if (!el) return;
      const ro = new ResizeObserver(() => {
        const w = el.clientWidth || roomW * 2;
        setZoom(Math.max(2, Math.min(4, Math.floor(w / roomW))));
      });
      ro.observe(el);
      return () => ro.disconnect();
    }, [roomW]);

    const agentsRef = useRef<Map<string, Agent>>(new Map());
    useEffect(() => {
      const next = new Map<string, Agent>();
      list.forEach((p, i) => {
        const seat = layout.seats[i] ?? null;
        const existing = agentsRef.current.get(p.actor);
        if (existing) {
          existing.name = p.name;
          existing.isYou = p.isYou;
          existing.seat = seat;
          next.set(p.actor, existing);
        } else {
          const col = seat?.col ?? 2;
          const row = seat?.row ?? WALL_H;
          next.set(p.actor, {
            actor: p.actor,
            name: p.name,
            isYou: p.isYou,
            sprite: spriteIndex(p.actor),
            seat,
            x: col * TILE + TILE / 2,
            y: row * TILE + TILE,
            col,
            row,
            state: "type",
            dir: 0,
            path: [],
            prog: 0,
            frame: 0,
            frameT: 0,
            wanderT: randRange(WANDER_PAUSE),
            wanderN: 0,
            wanderLimit: randInt(WANDER_MOVES),
          });
        }
      });
      agentsRef.current = next;
    }, [list, layout]);

    useEffect(() => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      const dpr = Math.min(2, window.devicePixelRatio || 1);
      canvas.width = Math.round(roomW * zoom * dpr);
      canvas.height = Math.round(roomH * zoom * dpr);
      canvas.style.width = `${roomW * zoom}px`;
      canvas.style.height = `${roomH * zoom}px`;

      const img: Partial<Record<keyof typeof SRC, HTMLImageElement | null>> = {};
      const chars: (HTMLImageElement | null)[] = [];
      let raf = 0;
      let last = performance.now();
      let cancelled = false;
      const center = (c: number, r: number) => ({ x: c * TILE + TILE / 2, y: r * TILE + TILE });

      const step = (a: Agent, dt: number, now: number) => {
        a.frameT += dt;
        const active = (pulses.current.get(a.actor) ?? 0) > now;
        if (a.state === "type") {
          if (a.frameT >= TYPE_FRAME_SEC) {
            a.frameT -= TYPE_FRAME_SEC;
            a.frame = (a.frame + 1) % 2;
          }
          if (!active) {
            a.state = "idle";
            a.frame = 0;
            a.wanderT = randRange(WANDER_PAUSE);
            a.wanderN = 0;
            a.wanderLimit = randInt(WANDER_MOVES);
          }
          return;
        }
        if (a.state === "idle") {
          a.frame = 0;
          if (active && a.seat) {
            a.path = bfsPath(layout, { col: a.col, row: a.row }, a.seat);
            a.state = a.path.length ? "walk" : "type";
            return;
          }
          a.wanderT -= dt;
          if (a.wanderT <= 0) {
            if (a.wanderN >= a.wanderLimit && a.seat) {
              a.path = bfsPath(layout, { col: a.col, row: a.row }, a.seat);
              if (a.path.length) {
                a.state = "walk";
                a.wanderN = 0;
                return;
              }
            }
            const t = randomFloor(layout);
            a.path = bfsPath(layout, { col: a.col, row: a.row }, t);
            if (a.path.length) {
              a.state = "walk";
              a.wanderN++;
            }
            a.wanderT = randRange(WANDER_PAUSE);
          }
          return;
        }
        // walk
        if (a.frameT >= WALK_FRAME_SEC) {
          a.frameT -= WALK_FRAME_SEC;
          a.frame = (a.frame + 1) % 4;
        }
        if (active && a.seat) {
          const lastTile = a.path[a.path.length - 1];
          if (!lastTile || lastTile.col !== a.seat.col || lastTile.row !== a.seat.row) {
            const np = bfsPath(layout, { col: a.col, row: a.row }, a.seat);
            if (np.length) {
              a.path = np;
              a.prog = 0;
            }
          }
        }
        if (a.path.length === 0) {
          const c = center(a.col, a.row);
          a.x = c.x;
          a.y = c.y;
          a.state = a.seat && a.col === a.seat.col && a.row === a.seat.row ? "type" : "idle";
          if (a.state === "type") a.dir = 0;
          else a.wanderT = randRange(WANDER_PAUSE);
          a.frame = 0;
          return;
        }
        const nx = a.path[0];
        a.dir = dirTo(a.col, a.row, nx.col, nx.row);
        a.prog += (WALK_SPEED / TILE) * dt;
        const f = center(a.col, a.row);
        const t = center(nx.col, nx.row);
        const k = Math.min(a.prog, 1);
        a.x = f.x + (t.x - f.x) * k;
        a.y = f.y + (t.y - f.y) * k;
        if (a.prog >= 1) {
          a.col = nx.col;
          a.row = nx.row;
          a.prog = 0;
          a.path.shift();
        }
      };

      const draw = (now: number) => {
        const dt = reduce ? 0 : Math.min(0.05, (now - last) / 1000);
        last = now;
        ctx.setTransform(zoom * dpr, 0, 0, zoom * dpr, 0, 0);
        ctx.imageSmoothingEnabled = false;

        // floor
        const floor = img.floor;
        const floorAlt = img.floorAlt ?? floor;
        for (let r = 0; r < layout.rows; r++) {
          for (let c = 0; c < layout.cols; c++) {
            const f = (r + c) % 7 === 0 ? floorAlt : floor;
            if (f) ctx.drawImage(f, c * TILE, r * TILE);
            else {
              ctx.fillStyle = (r + c) % 2 ? "#16241c" : "#18271e";
              ctx.fillRect(c * TILE, r * TILE, TILE, TILE);
            }
          }
        }
        // rugs (lounge + scattered accents)
        if (img.rug) {
          for (const rg of layout.rugs)
            for (let y = 0; y < rg.h; y += TILE)
              for (let x = 0; x < rg.w; x += TILE) ctx.drawImage(img.rug, rg.x + x, rg.y + y);
        }
        // top wall band + flat wall art
        const wall = img.wall;
        for (let c = 0; c < layout.cols; c++) {
          if (wall)
            ctx.drawImage(wall, 0, 0, TILE, WALL_H * TILE, c * TILE, 0, TILE, WALL_H * TILE);
          else {
            ctx.fillStyle = "#0d1813";
            ctx.fillRect(c * TILE, 0, TILE, WALL_H * TILE);
          }
        }
        for (const w of layout.wallArt) {
          const im = img[w.key];
          if (im) ctx.drawImage(im, w.x, w.y);
        }

        // step everyone
        const agents = [...agentsRef.current.values()];
        for (const a of agents) step(a, dt, now);

        // depth-sorted draw of props + characters
        const pcKey = PC_KEYS[Math.floor(now / 260) % PC_KEYS.length];
        type Item =
          | { kind: "prop"; baseline: number; p: Prop }
          | { kind: "char"; baseline: number; a: Agent };
        const items: Item[] = [];
        for (const p of layout.props) items.push({ kind: "prop", baseline: p.baseline, p });
        for (const a of agents) items.push({ kind: "char", baseline: a.y, a });
        items.sort((x, y) => x.baseline - y.baseline);
        for (const it of items) {
          if (it.kind === "prop") {
            const im = img[it.p.key];
            if (im) ctx.drawImage(im, it.p.x, it.p.y);
            else {
              ctx.fillStyle = "#5a4632";
              ctx.fillRect(it.p.x, it.p.y, TILE, TILE);
            }
            if (it.p.monitor) {
              const pc = img[pcKey] ?? img.pc1;
              if (pc) ctx.drawImage(pc, it.p.x + TILE, it.p.y - 18);
            }
          } else {
            drawChar(ctx, chars[it.a.sprite], it.a, reduce);
          }
        }
        // working bubbles above active agents
        for (const a of agents) {
          if ((pulses.current.get(a.actor) ?? 0) > now) drawBubble(ctx, a, now, reduce);
        }
        positionLabels(labelLayerRef.current, agents, zoom);
        if (!cancelled) raf = requestAnimationFrame(draw);
      };

      const furnitureKeys = Object.keys(SRC) as (keyof typeof SRC)[];
      Promise.all([
        ...furnitureKeys.map((k) => loadImage(SRC[k]).then((i) => (img[k] = i))),
        ...CHAR_SRCS.map((s, i) => loadImage(s).then((im) => (chars[i] = im))),
      ]).then(() => {
        if (cancelled) return;
        last = performance.now();
        raf = requestAnimationFrame(draw);
      });

      return () => {
        cancelled = true;
        cancelAnimationFrame(raf);
      };
    }, [zoom, layout, roomW, roomH, reduce]);

    return (
      <div ref={wrapRef} className="flex justify-center overflow-x-auto">
        <div className="relative" style={{ width: roomW * zoom, height: roomH * zoom }}>
          <canvas ref={canvasRef} className="block" style={{ imageRendering: "pixelated" }} />
          <div ref={labelLayerRef} className="pointer-events-none absolute inset-0 overflow-hidden">
            {list.map((p) => (
              <span
                key={p.actor}
                data-actor={p.actor}
                className={`absolute -translate-x-1/2 whitespace-nowrap rounded px-1 py-px text-[10px] font-medium leading-tight ${
                  p.isYou ? "bg-primary/90 text-primary-foreground" : "bg-black/55 text-white"
                }`}
                style={{ left: 0, top: 0, willChange: "transform" }}
              >
                {p.name}
              </span>
            ))}
          </div>
        </div>
      </div>
    );
  },
);

// ---- draw helpers -----------------------------------------------------------
function drawChar(
  ctx: CanvasRenderingContext2D,
  sheet: HTMLImageElement | null | undefined,
  a: Agent,
  reduce: boolean | null,
) {
  const drawX = Math.round(a.x - CHAR_W / 2);
  const drawY = Math.round(a.y - CHAR_H);
  const rowIdx = a.dir === 1 ? 1 : a.dir === 2 || a.dir === 3 ? 2 : 0;
  let frameCol = 0;
  if (a.state === "walk" && !reduce) frameCol = a.frame % 7;
  else if (a.state === "type" && !reduce) frameCol = 1 + (a.frame % 2);
  if (!sheet) {
    ctx.fillStyle = a.isYou ? "#34d399" : "#94a3b8";
    ctx.fillRect(drawX, drawY, CHAR_W, CHAR_H);
    return;
  }
  const sx = frameCol * CHAR_W;
  const sy = rowIdx * CHAR_H;
  if (a.dir === 3) {
    ctx.save();
    ctx.translate(drawX + CHAR_W, drawY);
    ctx.scale(-1, 1);
    ctx.drawImage(sheet, sx, sy, CHAR_W, CHAR_H, 0, 0, CHAR_W, CHAR_H);
    ctx.restore();
  } else {
    ctx.drawImage(sheet, sx, sy, CHAR_W, CHAR_H, drawX, drawY, CHAR_W, CHAR_H);
  }
}

function drawBubble(ctx: CanvasRenderingContext2D, a: Agent, now: number, reduce: boolean | null) {
  const bx = Math.round(a.x) + 5;
  const by = Math.round(a.y - CHAR_H - 9);
  const w = 14;
  const h = 9;
  ctx.fillStyle = "rgba(16,185,129,0.95)";
  roundRect(ctx, bx, by, w, h, 3);
  ctx.fill();
  ctx.beginPath();
  ctx.moveTo(bx + 3, by + h);
  ctx.lineTo(bx + 6, by + h);
  ctx.lineTo(bx + 3, by + h + 3);
  ctx.closePath();
  ctx.fill();
  const phase = reduce ? 0 : Math.floor(now / 220) % 3;
  for (let i = 0; i < 3; i++) {
    ctx.fillStyle = i === phase ? "#ffffff" : "rgba(255,255,255,0.5)";
    ctx.fillRect(bx + 3 + i * 4, by + 4, 2, 2);
  }
}

function roundRect(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

function dirTo(fc: number, fr: number, tc: number, tr: number): Dir {
  if (tc > fc) return 2;
  if (tc < fc) return 3;
  if (tr > fr) return 0;
  return 1;
}

function randomFloor(layout: Layout): Cell {
  for (let i = 0; i < 50; i++) {
    const c = 1 + Math.floor(Math.random() * (layout.cols - 2));
    const r = WALL_H + Math.floor(Math.random() * (layout.rows - WALL_H - 1));
    if (!layout.blocked[r][c]) return { col: c, row: r };
  }
  return { col: 1, row: WALL_H };
}

function positionLabels(layer: HTMLDivElement | null, agents: Agent[], zoom: number) {
  if (!layer) return;
  const byActor = new Map(agents.map((a) => [a.actor, a]));
  for (const node of Array.from(layer.children) as HTMLElement[]) {
    const a = byActor.get(node.dataset.actor || "");
    if (!a) {
      node.style.opacity = "0";
      continue;
    }
    node.style.opacity = "1";
    node.style.transform = `translate(${a.x * zoom}px, ${(a.y - CHAR_H - 3) * zoom}px)`;
  }
}
