import type { Pen } from "./live-crew-draw";
import type { ClientType } from "./request-record-model";

/**
 * The posable musume. Each girl is drawn from shapes in a 32 × 34 unit space
 * (hair, head, dress, tail) and sampled onto a cell grid, 1.5 cells a unit
 * where the cells land on whole device pixels and 1 cell a unit where they
 * would not. A pose solves each arm from shoulder through elbow to hand, lays
 * every part and held prop on the grid and outlines them there, so a raised
 * arm or a held book keeps the art's dark edge. Faces are placed cell by cell.
 *
 * A girl's size is given as `scale`: logical px a unit, the critters' art
 * pixel size; she stands 34 units tall at any resolution.
 */

export type Shape = (x: number, y: number) => string | null | undefined;
/** A held thing: a shape and the box it fits in, in units. */
export interface Prop {
  s: Shape;
  box: Box;
}
type Box = readonly [number, number, number, number];
type P = { x: number; y: number };

/** The body's middle and the line under the shoes, in units. */
export const CX = 15.5;
export const FOOT = 34;
/** How far a sitting girl drops, in units: whole cells at either resolution. */
export const SIT = 4;
const SH_X = 3.9;
const SH_Y = 20.3;
const UPPER = 4.0;
const FORE = 3.8;

const ell = (
  x: number,
  y: number,
  cx: number,
  cy: number,
  rx: number,
  ry: number,
) => ((x - cx) / rx) ** 2 + ((y - cy) / ry) ** 2 <= 1;
const clamp = (v: number, lo: number, hi: number) =>
  Math.max(lo, Math.min(hi, v));

/** Painter's order: the last shape that covers a point wins. */
const stack =
  (...fns: Shape[]): Shape =>
  (x, y) => {
    let c: string | null | undefined = null;
    for (const f of fns) {
      const v = f(x, y);
      if (v) c = v;
    }
    return c;
  };

type Layer = Map<number, string>;

/** Cells of one resolution: columns from the middle, rows up from the feet. */
class Grid {
  readonly c0: number;
  readonly c1: number;
  readonly r0: number;
  readonly r1: number;
  readonly w: number;
  readonly h: number;

  constructor(readonly k: number) {
    this.c0 = this.col(-10);
    this.c1 = this.col(41) + 1;
    this.r0 = this.row(-8);
    this.r1 = this.row(41) + 1;
    this.w = this.c1 - this.c0;
    this.h = this.r1 - this.r0;
  }

  col(x: number) {
    return Math.floor((x - CX) * this.k + 0.5);
  }

  row(y: number) {
    return Math.floor((y - FOOT) * this.k);
  }

  idx(c: number, r: number) {
    return (r - this.r0) * this.w + (c - this.c0);
  }

  has(c: number, r: number) {
    return c >= this.c0 && c < this.c1 && r >= this.r0 && r < this.r1;
  }

  cOf(i: number) {
    return (i % this.w) + this.c0;
  }

  rOf(i: number) {
    return Math.floor(i / this.w) + this.r0;
  }

  /** Cells at least 4/9 covered take their most common color. */
  sample(shape: Shape, box?: Box): Layer {
    const out: Layer = new Map();
    const n = new Map<string, number>();
    const c0 = box ? Math.max(this.c0, this.col(box[0]) - 1) : this.c0;
    const c1 = box ? Math.min(this.c1, this.col(box[2]) + 2) : this.c1;
    const r0 = box ? Math.max(this.r0, this.row(box[1]) - 1) : this.r0;
    const r1 = box ? Math.min(this.r1, this.row(box[3]) + 2) : this.r1;
    for (let r = r0; r < r1; r += 1)
      for (let c = c0; c < c1; c += 1) {
        n.clear();
        let covered = 0;
        for (const fy of [0.2, 0.5, 0.8])
          for (const fx of [0.2, 0.5, 0.8]) {
            const v = shape(
              CX + (c - 0.5 + fx) / this.k,
              FOOT + (r + fy) / this.k,
            );
            if (!v) continue;
            covered += 1;
            n.set(v, (n.get(v) ?? 0) + 1);
          }
        if (covered < 4) continue;
        let best = "";
        let most = 0;
        for (const [v, count] of n)
          if (count > most) {
            best = v;
            most = count;
          }
        out.set(this.idx(c, r), best);
      }
    return out;
  }
}

const FINE = new Grid(1.5);
const COARSE = new Grid(1);
/** The desktop pet's own, finer still, for her hair and face up close. */
const PET = new Grid(2.1);

/** From this size up a girl is the desktop pet and gets the big face. */
const BIG = 4;

const halves = (cell: number) =>
  Math.abs(cell * 2 - Math.round(cell * 2)) < 1e-6 && cell >= 0.5;

/** Fine cells where they land on half pixels, else one cell a unit. */
function gridFor(scale: number): Grid {
  if (scale >= BIG && halves(scale / PET.k)) return PET;
  return halves(scale / FINE.k) ? FINE : COARSE;
}

/** Distance to the bone a→b, how far along it, and which side (+ away from the light). */
function segDist(x: number, y: number, a: P, b: P): [number, number, number] {
  const vx = b.x - a.x;
  const vy = b.y - a.y;
  const len2 = vx * vx + vy * vy || 1e-6;
  const t = clamp(((x - a.x) * vx + (y - a.y) * vy) / len2, 0, 1);
  const px = a.x + vx * t;
  const py = a.y + vy * t;
  const len = Math.sqrt(len2);
  let nx = -vy / len;
  let ny = vx / len;
  if (nx * 0.5 + ny < 0) {
    nx = -nx;
    ny = -ny;
  }
  return [Math.hypot(x - px, y - py), t, (x - px) * nx + (y - py) * ny];
}

/** Distance to the curve a→b→c and how far along it. */
function curveDist(x: number, y: number, a: P, b: P, c: P): [number, number] {
  let best = Infinity;
  let at = 0;
  for (let i = 0; i <= 16; i += 1) {
    const t = i / 16;
    const px = (1 - t) ** 2 * a.x + 2 * t * (1 - t) * b.x + t * t * c.x;
    const py = (1 - t) ** 2 * a.y + 2 * t * (1 - t) * b.y + t * t * c.y;
    const d = Math.hypot(x - px, y - py);
    if (d < best) {
      best = d;
      at = t;
    }
  }
  return [best, at];
}

// ---------- the girls ----------

interface ArmStyle {
  sleeve: string;
  shade: string;
  cuff: string;
  /** A highlight on the puff, if any. */
  shine?: string;
  puff: number;
  upper: number;
  fore: number;
  hand: number;
  /** How much of the forearm the cuff covers, 0 → 1. */
  cuffFrom: number;
}

interface LegStyle {
  stocking: string;
  shoe: string;
  /** A strap across the shoe. */
  trim?: string;
}

export interface Hand {
  /** Where the hand is, in units (standing frame unless the pose sits). */
  x: number;
  y: number;
  /** In front of the face (a hand at the chin, a visor); else behind it. */
  front?: boolean;
  /** Behind the body, for hands clasped at the back. */
  back?: boolean;
  /** An open palm, a little bigger. */
  open?: boolean;
  /** Roughly where the elbow should go, when out-and-down is wrong. */
  elbow?: readonly [number, number];
}

interface Girl {
  pal: Readonly<Record<string, string>>;
  back: (sway: number) => Shape;
  head: (sway: number) => Shape;
  torso: Shape;
  tail?: (wag: number) => Shape;
  arm: ArmStyle;
  /** How she stands with nothing to do: her own habit. */
  idle: { near: Hand; far: Hand };
  legs: LegStyle;
  rim: Readonly<Record<string, string>>;
  /** Darker shades, for the shadow under the chin. */
  shade: Readonly<Record<string, string>>;
}

/** Alternates cell by cell, for frills and ribs; two cells on the pet's grid. */
const odd = (x: number, grid = FINE) =>
  ((grid === PET ? grid.col(x) >> 1 : grid.col(x)) & 1) === 1;

/** The pet's scalloped lace hem. */
const lace = (x: number) => 29.4 - 0.45 * Math.abs(Math.sin((x - CX) * 1.9));

/**
 * Back hair, and the head in front: face, fringe, side locks, a shine ring.
 * Long hair is the desktop pet's: down past her hips in waves.
 */
function common(o: { hairTo: number; spread: number; long: boolean }) {
  const hy = 11.5;
  const hem = o.hairTo;
  const mass = (x: number, y: number) => {
    if (ell(x, y, CX, hy, 11, 10.5))
      return y > hy + 1 && Math.abs(x - CX) < 8.5 ? "h" : "H";
    if (y >= hy && y <= hem + 1.6 * Math.sin(x * 1.3)) {
      const half = 10.6 + 1.3 * Math.sin(y * 0.9) + (y - hy) * o.spread;
      if (Math.abs(x - CX) <= half) return Math.abs(x - CX) < 7.5 ? "h" : "H";
    }
    return null;
  };
  // Long hair hangs in locks, front ones first: each waves, fans out a
  // little, is shaded where it lies over the next, catches the light in a
  // band, and curls out at its end.
  const lock = (
    d: number,
    y: number,
    l: readonly [number, number, number, number, number],
  ) => {
    const [at, w0, tip, phase, light] = l;
    if (y > tip) return null;
    const u = y - hy;
    const end = clamp(1 - (tip - y) / 2.6, 0, 1);
    const c =
      at * (0.9 + u * 0.009) +
      (0.3 + u * 0.055) * Math.sin(u * 0.6 + phase) +
      end * end * 0.9;
    const t = (d - c) / (w0 * (1 - end * 0.7));
    if (Math.abs(t) >= 1) return null;
    if (t < -0.5) return "h";
    if (t > 0.1 && t < 0.65 && y > light && y < light + 2.4)
      return y < light + 0.7 && t < 0.4 ? "i" : "L";
    return "H";
  };
  const locks = [
    [10.6, 1.25, 29.6, 0, 16.5],
    [9.0, 1.35, 31.0, 1.3, 19.4],
    [7.4, 1.3, 30.4, 2.4, 23.5],
    [5.8, 1.3, 29.0, 3.3, 25.2],
    [4.4, 1.4, 27.5, 0.7, 99],
  ] as const;
  const sideLock = [7.6, 1.1, 24.2, 0.5, 99] as const;
  // A tufted crown rather than a smooth dome.
  const crown = (x: number, y: number) => {
    const dx = (x - CX) / 11;
    const dy = (y - hy) / 10.5;
    const bump = y < hy ? Math.abs(Math.sin(Math.atan2(dx, -dy) * 4.5)) : 0;
    return Math.hypot(dx, dy) <= 1 + 0.04 * bump;
  };
  const flow = (x: number, y: number) => {
    if (crown(x, y)) return y > hy + 1 && Math.abs(x - CX) < 8.5 ? "h" : "H";
    if (y < hy) return null;
    const d = Math.abs(x - CX);
    for (const l of locks) {
      const v = lock(d, y, l);
      // Behind her face, in its shadow.
      if (v) return y < 21.5 && d < 8.4 ? "h" : v;
    }
    return null;
  };
  const back =
    (sway: number): Shape =>
    (x0, y) => {
      // The lower the hair, the further a sway carries it.
      if (o.long) return flow(x0 - sway * clamp((y - 12) / 16, 0, 1) * 1.8, y);
      const x = x0 - sway * clamp((y - 12) / 12, 0, 1) * 1.4;
      const v = mass(x, y);
      if (!v) return null;
      const k = Math.abs(x - CX);
      if (y > hy + 2 && k > 6.5) {
        if (Math.abs(k - 8.6) < 0.35) return "h";
        if (Math.abs(k - 10.0) < 0.3 && y > 15) return "L";
        if (y > hem - 4) return "i";
      }
      return v;
    };
  const face: Shape = (x, y) =>
    ell(x, y, CX, hy + 2.2, 7.2, 6.2) ? "S" : null;
  // The pet's fringe falls in pointed strands, the longest between her eyes:
  // where each is from the middle, how far below `hy` it reaches, how wide.
  const strands = [
    [-6.4, 0.5, 1.9],
    [-3.6, 0.9, 2.0],
    [-0.6, 2.2, 1.6],
    [1.8, 1.0, 1.7],
    [4.4, 0.8, 2.0],
    [6.8, 0.4, 1.8],
  ] as const;
  const fringe = (x: number) => {
    let tip = hy - 2;
    for (const [dx, dy, hw] of strands) {
      const a = 1 - Math.abs(x - CX - dx) / hw;
      if (a > 0) tip = Math.max(tip, hy - 2 + (dy + 2) * a ** 0.7);
    }
    return tip;
  };
  const bangs =
    (sway: number): Shape =>
    (x0, y) => {
      if (ell(x0, y, CX, hy, 10.6, 10.2)) {
        if (o.long) {
          const tip = fringe(x0);
          if (y < tip) return y > tip - 0.7 && tip > hy - 1 ? "h" : "H";
        } else {
          const w = Math.abs(Math.sin((x0 - CX) * 0.7));
          const tip = hy - 1.6 + 2.4 * w ** 0.8;
          if (y < tip) return y > tip - 0.9 && w < 0.45 ? "h" : "H";
        }
      } else if (!(y > hy && Math.abs(x0 - CX) < 11.5)) return null;
      // Side locks in front of the face edges; their ends swing.
      if (o.long) {
        // Two locks a side down over her shoulders, framing her face.
        const x = x0 - sway * clamp((y - 14) / 10, 0, 1) * 1.3;
        const d = Math.abs(x - CX);
        return lock(d, y, [8.9, 1.3, 26.8, 1.8, 14.2]) ?? lock(d, y, sideLock);
      }
      const x = x0 - sway * clamp((y - 14) / 7, 0, 1) * 0.9;
      const d = Math.abs(x - CX);
      if (d > 6.2 && d < 10.4 && y < hy + 9.5 + 0.8 * Math.sin(x)) {
        if (y > hy + 2 && Math.abs(d - 8.0) < 0.3) return "h";
        return "H";
      }
      return null;
    };
  const ring: Shape = (x, y) => {
    const d = Math.hypot(x - CX, (y - hy) / 0.95);
    return d > 7.1 && d < 8.2 && y < hy - 3.5 && Math.abs(x - CX) > 1.5
      ? "L"
      : null;
  };
  return { back, head: (sway: number) => stack(face, bangs(sway), ring) };
}

const ahoge =
  (c: string): Shape =>
  (x, y) =>
    y >= 0 && y <= 2.5 && Math.abs(x - (CX + 1 + (2.5 - y) * 0.9)) < 0.55
      ? c
      : null;

// Claude娘: orange wavy hair, a cream spark clip, a cream dress with a black
// corset and an orange panel; hands behind her back.
function claude(big: boolean): Girl {
  const { back, head } = common({ hairTo: 25, spread: 0.08, long: big });
  const hem = (x: number) => 29.4 + 0.35 * Math.sin(x * 1.9);
  // The pet's skirt falls straighter, so her hair frames it.
  const flare = big ? 0.45 : 0.82;
  const corset: Shape = (x, y) => {
    if (y >= 21.8 && y <= 24.6 && Math.abs(x - CX) <= 3.7 - (y - 21.8) * 0.15)
      return Math.abs(x - CX) < 0.4 && Math.floor(y * 1.5) % 2 ? "G" : "B";
    return null;
  };
  const torso: Shape = stack(
    (x, y) => {
      if (ell(x, y, CX, 21.4, 4.4, 4.2)) return "W";
      if (
        y >= 22.5 &&
        y <= hem(x) &&
        Math.abs(x - CX) <= 3.6 + (y - 22.5) * flare
      )
        return "W";
      return null;
    },
    // Folds and the shaded side.
    (x, y) => {
      if (y < 24.2 || y > hem(x)) return null;
      const half = 3.6 + (y - 22.5) * flare;
      const d = x - CX;
      if (Math.abs(d) > half) return null;
      if (d > half - 1.2) return "w";
      if (Math.abs(Math.abs(d) - half * 0.45) < 0.25 && y > 25) return "w";
      return null;
    },
    (x, y) =>
      y >= 28.8 && y <= hem(x) && Math.abs(x - CX) <= (big ? 6.9 : 9.2)
        ? "w"
        : null,
    // Corset with gold lacing.
    corset,
    // The orange front panel.
    (x, y) => {
      if (y < 24.6 || y > 29.2) return null;
      const d = Math.abs(x - CX);
      if (d <= 0.6 + (y - 24.6) * 0.3) return "O";
      if (d <= 1.4 + (y - 24.6) * 0.3) return "B";
      return null;
    },
    // A black bow under the chin with an orange knot.
    (x, y) => {
      if (ell(x, y, CX, 20.6, 0.55, 0.55)) return "O";
      if (
        ell(x, y, CX - 1.4, 20.5, 1.2, 0.8) ||
        ell(x, y, CX + 1.4, 20.5, 1.2, 0.8)
      )
        return "B";
      return null;
    },
  );
  // The pet's dress: a cream blouse, the corset, a chocolate skirt with a
  // frilled white apron and petticoat, so her cream sleeves stand out on it.
  const skirt = (y: number) => 3.6 + (y - 22.5) * 0.5;
  const petTorso: Shape = stack(
    (x, y) => {
      if (ell(x, y, CX, 21.4, 4.4, 4.2)) return "W";
      const half = skirt(y);
      if (y < 22.5 || y > hem(x) || Math.abs(x - CX) > half) return null;
      if (x - CX > half - 1.1) return "m";
      // Pleats.
      const d = Math.abs(x - CX);
      return y > 26 && Math.abs(d - half * 0.62) < 0.25 ? "m" : "n";
    },
    (x, y) =>
      y > hem(x) && y <= lace(x) + 0.6 && Math.abs(x - CX) <= skirt(29.4) + 0.2
        ? "W"
        : null,
    (x, y) => {
      if (y < 24.2 || y > hem(x) - 0.3) return null;
      const half = 1.8 + (y - 24.2) * 0.42;
      if (Math.abs(x - CX) > half) return null;
      if (y > hem(x) - 1.1 && y > lace(x) - 1.1) return "w";
      return x - CX > half - 0.6 ? "w" : "W";
    },
    corset,
    // A black bow under the chin with an orange knot.
    (x, y) => {
      if (ell(x, y, CX, 20.6, 0.55, 0.55)) return "O";
      if (
        ell(x, y, CX - 1.4, 20.5, 1.2, 0.8) ||
        ell(x, y, CX + 1.4, 20.5, 1.2, 0.8)
      )
        return "B";
      return null;
    },
  );
  // Claude's spark as a hair clip: cream rays round an orange core, edged in dark hair.
  const spark: Shape = (x, y) => {
    const dx = Math.abs(x - 22.4);
    const dy = Math.abs(y - 4.4);
    const s = dx ** 0.62 + dy ** 0.62;
    if (Math.hypot(dx, dy) < 0.55) return "O";
    if (s <= 2.5 ** 0.62)
      return dx + dy > 1.4 && x > 22.4 && y > 4.4 ? "w" : "W";
    if (s <= 3.0 ** 0.62) return "h";
    return null;
  };
  return {
    back,
    head: (sway) => stack(head(sway), spark, ahoge("h")),
    torso: big ? petTorso : torso,
    arm: {
      sleeve: "W",
      shade: "w",
      cuff: "O",
      puff: big ? 1.1 : 1.3,
      upper: big ? 0.85 : 1.0,
      fore: big ? 0.8 : 0.95,
      hand: big ? 0.9 : 0.95,
      cuffFrom: 0.74,
    },
    // The pet folds her hands in front, where they show.
    idle: big
      ? { near: { x: CX + 1.0, y: 26.0 }, far: { x: CX - 1.0, y: 26.2 } }
      : {
          near: { x: CX + 2.0, y: 26.4, back: true },
          far: { x: CX - 2.0, y: 26.4, back: true },
        },
    legs: { stocking: "b", shoe: "B", trim: "G" },
    rim: { H: "h", L: "h", i: "L" },
    shade: { W: "w", O: "o", n: "m" },
    pal: {
      K: "#4e2414",
      k: "#a0532a",
      h: "#b5461c",
      H: "#e9743a",
      L: "#f6a067",
      i: "#ffc27a",
      S: "#fde5d2",
      s: "#f7a993",
      Z: "#f4cdb6",
      E: "#4a1d08",
      d: "#8f3e12",
      e: "#d9772a",
      l: "#ffbe6a",
      W: "#fbf4e8",
      Y: "#ffffff",
      w: "#e4d3bc",
      M: "#b9483e",
      Q: "#e8737a",
      B: "#2b2530",
      G: "#ebb84e",
      O: "#f08a30",
      o: "#c86a1e",
      b: "#efe6d8",
      // The pet's hair is a softer apricot, her skirt chocolate.
      ...(big
        ? {
            h: "#d2703a",
            H: "#f2964f",
            L: "#f9b77a",
            i: "#ffd6a2",
            n: "#7a4630",
            m: "#5c3222",
          }
        : {}),
    },
  };
}

// Codex娘: lavender hair, curled purple horns, a big white hoodie worn as a
// dress with `>_` on it, sleeves longer than her hands, a thin tail.
function codex(big: boolean): Girl {
  const { back, head } = common({ hairTo: 25, spread: 0.14, long: big });
  const horns: Shape = (x, y) => {
    for (const s of [-1, 1])
      for (let i = 0; i <= 20; i += 1) {
        const t = i / 20;
        const px =
          (1 - t) ** 2 * (CX + s * 5.2) +
          2 * t * (1 - t) * (CX + s * 9.4) +
          t * t * (CX + s * 10.2);
        const py = (1 - t) ** 2 * 4.2 + 2 * t * (1 - t) * 3.6 + t * t * 0.1;
        const r = 1.55 * (1 - t) + 0.4;
        if (Math.hypot(x - px, y - py) <= r)
          return y - py < -0.2 * r && t < 0.75 ? "R" : "r";
      }
    return null;
  };
  // The pet's hoodie hangs straighter, so her hair frames it.
  const flare = big ? 0.42 : 0.6;
  const stroke = (a: P, b: P) => (x: number, y: number) =>
    segDist(x, y, a, b)[0] <= 0.4;
  const prompt = [
    stroke({ x: 13.4, y: 21.7 }, { x: 14.7, y: 22.7 }),
    stroke({ x: 14.7, y: 22.7 }, { x: 13.4, y: 23.7 }),
    stroke({ x: 15.6, y: 23.8 }, { x: 17.8, y: 23.8 }),
  ];
  const torso: Shape = stack(
    // The hood bunched behind the neck.
    (x, y) =>
      y >= 16.6 && y <= 19.6 && Math.abs(x - CX) <= 6.0 - (y - 16.6) * 0.5
        ? "w"
        : null,
    (x, y) => {
      if (ell(x, y, CX, 21.6, 4.8, 4.3)) return "W";
      if (y >= 22 && y <= 28.6 && Math.abs(x - CX) <= 4.4 + (y - 22) * flare)
        return "W";
      return null;
    },
    (x, y) => {
      if (y < 23.5 || y > 28.6) return null;
      const half = 4.4 + (y - 22) * flare;
      return x - CX > half - 1.1 && x - CX <= half ? "w" : null;
    },
    // Ribbed hem, drawstrings, a pocket seam, `>_` on the chest.
    (x, y) =>
      y >= 27.5 && y <= 28.6 && Math.abs(x - CX) <= (big ? 7.2 : 8.4)
        ? odd(x, big ? PET : FINE)
          ? "w"
          : "W"
        : null,
    (x, y) =>
      y >= 20.2 && y <= 22.6 && Math.abs(Math.abs(x - CX) - 1.3) < 0.35
        ? "P"
        : null,
    (x, y) => (y >= 25.4 && y <= 25.9 && Math.abs(x - CX) <= 3.2 ? "w" : null),
    (x, y) => (prompt.some((f) => f(x, y)) ? "P" : null),
  );
  const tail =
    (wag: number): Shape =>
    (x, y) => {
      const tip = { x: 1.2 - wag * 0.5, y: 23.6 + wag * 0.6 };
      if (ell(x, y, tip.x, tip.y - 0.3, 0.9, 0.9)) return "T";
      const [d] = curveDist(
        x,
        y,
        { x: 6.2, y: 28.4 },
        { x: 1.4, y: 28.8 },
        tip,
      );
      return d <= 0.5 ? "T" : null;
    };
  return {
    back,
    head: (sway) => stack(head(sway), horns, ahoge("h")),
    torso,
    tail,
    arm: {
      sleeve: "W",
      shade: "w",
      cuff: "w",
      puff: big ? 1.15 : 1.3,
      upper: big ? 1.0 : 1.15,
      fore: big ? 1.05 : 1.2,
      hand: big ? 0.7 : 0.75,
      cuffFrom: 0.62,
    },
    idle: { near: { x: CX + 5.4, y: 27.6 }, far: { x: CX - 5.4, y: 27.6 } },
    legs: { stocking: "S", shoe: "W", trim: "p" },
    rim: { H: "h", L: "h", i: "L" },
    shade: { W: "w" },
    pal: {
      K: "#4a4080",
      k: "#8f82cf",
      h: "#a397dc",
      H: "#cbc3f2",
      L: "#efecfd",
      i: "#f6f4ff",
      R: "#8f74e6",
      r: "#5b43b4",
      S: "#fde8dc",
      s: "#f5aaa6",
      Z: "#f3d0c4",
      E: "#2a1f6e",
      d: "#4936a8",
      e: "#7f6ae6",
      l: "#b3a3ff",
      W: "#fbfaff",
      Y: "#ffffff",
      w: "#d9d4f0",
      M: "#b9483e",
      Q: "#e8737a",
      P: "#6a55d6",
      p: "#f4a9c0",
      T: "#ad9fea",
    },
  };
}

// The blue whale maid (鲸鱼娘): a maid headband, fin ears, a navy dress with
// a white apron and a whale on it, a whale's tail; hands folded on the apron.
function whale(big: boolean): Girl {
  const { back, head } = common({ hairTo: 24, spread: 0.16, long: big });
  const fins: Shape = (x, y) => {
    for (const s of [-1, 1]) {
      const t = (x - (CX + s * 9.5)) * s;
      const c = 12.6 + t * 0.35;
      if (t >= 0 && t <= 5.5 && Math.abs(y - c) <= 2.4 - t * 0.42)
        return Math.abs(y - c) < 0.9 - t * 0.12 && t < 4 ? "f" : "F";
    }
    return null;
  };
  const band: Shape = (x, y) => {
    const d = Math.hypot(x - CX, (y - 11.5) * 1.05);
    return d > 8.7 && d < 10.5 && y < 6.4
      ? d > 10.0 && odd(x, big ? PET : FINE)
        ? "w"
        : "W"
      : null;
  };
  // The pet's dress falls straighter, so her hair frames it.
  const flare = big ? 0.62 : 0.85;
  const bow: Shape = (x, y) =>
    ell(x, y, CX + 8.6, 7.4, 1.5, 1.0)
      ? x > CX + 8.6 && y > 7.4
        ? "c"
        : "b"
      : null;
  const torso: Shape = stack(
    (x, y) => {
      if (ell(x, y, CX, 21.4, 4.4, 4.1)) return "N";
      if (
        y >= 22.5 &&
        y <= 28.4 &&
        Math.abs(x - CX) <= 3.6 + (y - 22.5) * flare
      )
        return "N";
      return null;
    },
    (x, y) => {
      if (y < 24 || y > 28.4) return null;
      const half = 3.6 + (y - 22.5) * flare;
      return x - CX > half - 1.1 && x - CX <= half ? "m" : null;
    },
    // The apron: a bib and a skirt.
    (x, y) => {
      if (y >= 20.6 && y <= 23.2 && Math.abs(x - CX) <= 2.3) return "W";
      if (y > 23.2 && y <= 27.8 && Math.abs(x - CX) <= 2.3 + (y - 23.2) * 0.6)
        return "W";
      return null;
    },
    (x, y) =>
      y > 24 &&
      y <= 27.8 &&
      x - CX > 1.5 + (y - 23.2) * 0.6 &&
      x - CX <= 2.3 + (y - 23.2) * 0.6
        ? "w"
        : null,
    // A little whale on the apron.
    (x, y) =>
      ell(x, y, CX - 0.2, 25.0, 1.6, 0.85) ||
      ell(x, y, CX + 1.7, 24.3, 0.55, 0.55)
        ? "b"
        : null,
    (x, y) => {
      if (y < 27.8 || Math.abs(x - CX) > (big ? 7.4 : 8.8)) return null;
      if (!big) return y <= 29.3 ? (odd(x) ? "W" : "w") : null;
      return y <= lace(x) ? (y < 28.4 ? "w" : "W") : null;
    },
    // A white collar and a blue bow.
    (x, y) => {
      if (ell(x, y, CX, 20.4, 0.5, 0.5)) return "c";
      if (
        ell(x, y, CX - 1.4, 20.3, 1.2, 0.75) ||
        ell(x, y, CX + 1.4, 20.3, 1.2, 0.75)
      )
        return "b";
      if (y >= 19.4 && y <= 21 && Math.abs(x - CX) <= 3.0) return "W";
      return null;
    },
  );
  const tail =
    (wag: number): Shape =>
    (x, y) => {
      const tip = { x: 2.0 - wag * 0.4, y: 25.6 + wag * 0.5 };
      for (const s of [-1, 1])
        if (
          ell(
            x,
            y,
            tip.x + s * 1.0,
            tip.y - 0.7 - Math.abs(x - tip.x) * 0.2,
            1.0,
            0.6,
          )
        )
          return "T";
      const [d, t] = curveDist(
        x,
        y,
        { x: 6.0, y: 29.6 },
        { x: 1.6, y: 29.8 },
        tip,
      );
      return d <= 0.75 - t * 0.25 ? "T" : null;
    };
  return {
    back,
    head: (sway) => stack(head(sway), fins, band, bow, ahoge("h")),
    torso,
    tail,
    arm: {
      sleeve: "n",
      shade: "N",
      cuff: "W",
      shine: "v",
      puff: big ? 1.2 : 1.4,
      upper: big ? 0.85 : 1.0,
      fore: big ? 0.8 : 0.95,
      hand: big ? 0.9 : 0.95,
      cuffFrom: 0.72,
    },
    idle: { near: { x: CX + 0.9, y: 26.2 }, far: { x: CX - 0.9, y: 26.4 } },
    legs: { stocking: "W", shoe: "N" },
    rim: { H: "h", L: "h", i: "L" },
    shade: { W: "w", N: "m", b: "c" },
    pal: {
      K: "#172045",
      k: "#141c3c",
      h: "#22408c",
      H: "#3463c4",
      L: "#5f97e6",
      i: "#8cc8f6",
      S: "#fde8dc",
      s: "#f6aaa4",
      Z: "#f2cfc4",
      E: "#0f1f58",
      d: "#1f3f92",
      e: "#3a78d8",
      l: "#5aa2ee",
      W: "#ffffff",
      Y: "#ffffff",
      w: "#d2dbeb",
      M: "#b9483e",
      Q: "#e8737a",
      F: "#2a4690",
      f: "#dce9f8",
      N: "#232f5e",
      n: "#4862b4",
      v: "#6a86d8",
      m: "#1a2348",
      b: "#6fb0f0",
      c: "#3f86d6",
      T: "#2c4b9a",
      // The pet's hair is a brighter sea blue.
      ...(big
        ? { h: "#2c58b4", H: "#3f7ad8", L: "#6fa6ec", i: "#a8d2fa" }
        : {}),
    },
  };
}

const GIRLS: Partial<Record<ClientType, (big: boolean) => Girl>> = {
  claude_code: claude,
  codex,
  deepseek_harness: whale,
};

/** Clients drawn as a musume rather than a critter. */
export const isGirl = (client: ClientType) => client in GIRLS;

interface Built {
  girl: Girl;
  grid: Grid;
  back: Map<number, Layer>;
  head: Map<number, Layer>;
  tail: Map<number, Layer>;
  legs: Map<string, Layer>;
  torso: Layer;
}

const built = new Map<string, Built>();
const girls = new Map<string, Girl>();

function build(client: ClientType, grid: Grid, big: boolean): Built {
  const key = `${client}|${grid.k}|${big ? 1 : 0}`;
  let b = built.get(key);
  if (!b) {
    let girl = girls.get(`${client}|${big ? 1 : 0}`);
    if (!girl)
      girls.set(`${client}|${big ? 1 : 0}`, (girl = GIRLS[client]!(big)));
    b = {
      girl,
      grid,
      back: new Map(),
      head: new Map(),
      tail: new Map(),
      legs: new Map(),
      torso: grid.sample(girl.torso),
    };
    built.set(key, b);
  }
  return b;
}

function memo<K>(m: Map<K, Layer>, k: K, make: () => Layer): Layer {
  let v = m.get(k);
  if (!v) m.set(k, (v = make()));
  return v;
}

const backOf = (b: Built, sway: number) =>
  memo(b.back, sway, () => b.grid.sample(b.girl.back(sway)));

const tailOf = (b: Built, wag: number) =>
  memo(b.tail, wag, () =>
    b.girl.tail ? b.grid.sample(b.girl.tail(wag), [-2, 20, 8, 31]) : new Map(),
  );

const headOf = (b: Built, sway: number) =>
  memo(b.head, sway, () => {
    const m = b.grid.sample(b.girl.head(sway));
    // A soft shadow under the fringe.
    for (const [i, v] of m) {
      if (v !== "S") continue;
      const up = m.get(i - b.grid.w);
      if (up && "HhL".includes(up)) m.set(i, "Z");
    }
    return m;
  });

/**
 * Legs under the skirt; sitting, shoes poking out in front of it, or, perched
 * on an edge, legs hanging below the seat, a lifted one swung up.
 */
function legsOf(
  b: Built,
  lift: readonly [number, number],
  sit: boolean,
  perch: boolean,
) {
  return memo(
    b.legs,
    `${lift.join(",")}|${sit ? 1 : 0}${perch ? 1 : 0}`,
    () => {
      const st = b.girl.legs;
      return b.grid.sample(
        (x, y) => {
          for (const n of [0, 1]) {
            const s = n ? 1 : -1;
            if (sit && perch) {
              const sy = 36.2 - lift[n] * 1.8;
              if (ell(x, y, CX + s * 1.9, sy, 1.6, 1.1) && y > sy - 0.8)
                return st.trim &&
                  y < sy - 0.2 &&
                  Math.abs(x - (CX + s * 1.9)) < 1.2
                  ? st.trim
                  : st.shoe;
              if (Math.abs(x - (CX + s * 1.8)) <= 1.2 && y >= 31 && y <= sy)
                return st.stocking;
              continue;
            }
            if (sit) {
              if (ell(x, y, CX + s * 2.5, 33.1, 1.7, 0.95))
                return st.trim && Math.abs(x - (CX + s * 2.3)) < 0.35
                  ? st.trim
                  : st.shoe;
              continue;
            }
            const sy = 33.0 - lift[n];
            if (ell(x, y, CX + s * 2.4, sy, 1.6, 1.0) && y > sy - 0.8)
              return st.trim &&
                y < sy - 0.2 &&
                Math.abs(x - (CX + s * 2.4)) < 1.2
                ? st.trim
                : st.shoe;
            if (Math.abs(x - (CX + s * 2.2)) <= 1.0 && y >= 28.6 && y <= sy)
              return st.stocking;
          }
          return null;
        },
        [10, 27, 21, 40],
      );
    },
  );
}

// ---------- faces ----------

export type Eyes = "open" | "closed" | "happy" | "dizzy" | "sleepy";
export type Mouth = "dot" | "smile" | "o" | "wide" | "cat" | "flat";
export interface Look {
  eyes: Eyes;
  /** Pupils: -1 toward the far side, 1 toward the near side. */
  lx: number;
  /** -1 up, 1 down. */
  ly: number;
  mouth: Mouth;
  blush: number;
  /** The far eye shut, looking through something with the near one. */
  wink?: boolean;
}

export const plainLook = (): Look => ({
  eyes: "open",
  lx: 0,
  ly: 0,
  mouth: "dot",
  blush: 0,
});

type Dot = readonly [number, number, string];

/** A face for one resolution. Eye rows are written for the far eye, its outer side first. */
interface FaceArt {
  /** The column of the far eye's lash flick; the eye follows to the right. */
  eyeL: number;
  eyes: Record<string, readonly string[]>;
  /** The near eye's patterns where it is no mirror image of the far eye's,
   * written left to right as it shows: its catchlight stays on the same side. */
  near?: Record<string, readonly string[]>;
  /** Per open eye: the catchlight's row and the pupil's row. Without one
   * the eye is drawn as written and its pupil keeps still. */
  shine: Record<string, readonly [number, number]>;
  /** The catchlight's cells from the eye's left column: [column, row offset]. */
  light: ReadonlyArray<readonly [number, number]>;
  /** A small low catchlight, [column, row], for open and lowered eyes. */
  glint?: readonly [number, number];
  /** The pupil's columns from the eye's left column, and its height in rows. */
  pupil: readonly number[];
  pupilH?: number;
  /** Cheek and blush columns, on the mouth's row and `cheekRows` above it. */
  cheeks: readonly number[];
  blush: readonly number[];
  cheekRows?: readonly number[];
  mouths: Record<Mouth, readonly Dot[]>;
  /** Where the eyes' top row and the mouth are, in units. */
  eyeY?: number;
  mouthY?: number;
}

const FACES: Record<number, FaceArt> = {
  [FINE.k]: {
    eyeL: -10,
    eyes: {
      open: ["...EEEE", "..EYYdd", "EEYYYdd", ".EYYddd", "..YYede", "..YYele"],
      up: ["...EEEE", "..EYYdd", "EEYYYdd", ".EYYede", "..YYele", "..YYYY."],
      down: [".......", "...EEEE", "EEEYYdd", ".EYYddd", "..YYede", "..YYele"],
      sleepy: [
        ".......",
        ".......",
        "...EEEE",
        "EEEYddd",
        ".EYYede",
        "..YYele",
      ],
      closed: [
        ".......",
        ".......",
        ".......",
        ".EE...E",
        "...EEE.",
        ".......",
      ],
      happy: [".......", ".......", "....E..", "...E.E.", "..E...E", "......."],
      dizzy: [".......", "..E...E", "...E.E.", "....E..", "...E.E.", "..E...E"],
    },
    near: {
      open: ["EEEE...", "YddYE..", "YddYYEE", "dddYYE.", "edeYY..", "eleYY.."],
      up: ["EEEE...", "YddYE..", "YddYYEE", "edeYYE.", "eleYY..", ".YYYY.."],
      down: [".......", "EEEE...", "YddYEEE", "dddYYE.", "edeYY..", "eleYY.."],
      sleepy: [
        ".......",
        ".......",
        "EEEE...",
        "dddYEEE",
        "edeYYE.",
        "eleYY..",
      ],
    },
    // Traced from astra's painting: a dark lash arching over the eye and
    // flicking out at the corner, white on the outer side, the iris inside
    // going light at the bottom, and a catchlight at the iris's top left on
    // both eyes. The pupils stay put on a glance.
    shine: {},
    light: [],
    pupil: [],
    cheeks: [7, 8, 9],
    blush: [6, 7, 8],
    mouths: {
      dot: [[0, 0, "M"]],
      flat: [
        [-1, 0, "M"],
        [0, 0, "M"],
        [1, 0, "M"],
      ],
      smile: [
        [-1, 0, "M"],
        [0, 1, "M"],
        [1, 0, "M"],
      ],
      cat: [
        [-2, 0, "M"],
        [-1, 1, "M"],
        [0, 0, "M"],
        [1, 1, "M"],
        [2, 0, "M"],
      ],
      o: [
        [0, 0, "M"],
        [-1, 1, "M"],
        [0, 1, "Q"],
        [1, 1, "M"],
        [0, 2, "M"],
      ],
      wide: [
        [-2, 0, "M"],
        [-1, 0, "M"],
        [0, 0, "M"],
        [1, 0, "M"],
        [2, 0, "M"],
        [-2, 1, "M"],
        [-1, 1, "Q"],
        [0, 1, "Q"],
        [1, 1, "Q"],
        [2, 1, "M"],
        [-1, 2, "M"],
        [0, 2, "M"],
        [1, 2, "M"],
      ],
    },
  },
  [COARSE.k]: {
    eyeL: -6,
    eyes: {
      open: ["EEEEE", ".dddd", ".dEEd", ".elle", "..ee."],
      up: ["EEEEE", ".dEEd", ".dddd", ".eeee", "..ee."],
      down: [".....", "EEEEE", ".dEEd", ".elle", "..ee."],
      sleepy: [".....", ".....", "EEEEE", ".dEEd", "..ee."],
      closed: [".....", ".....", "EE..E", "..EE.", "....."],
      happy: [".....", "..EE.", ".E..E", ".....", "....."],
      dizzy: [".....", ".E..E", "..EE.", ".E..E", "....."],
    },
    shine: { open: [1, 2], up: [1, 1], down: [2, 2], sleepy: [3, 3] },
    light: [[0, 0]],
    pupil: [1, 2],
    cheeks: [4],
    blush: [3, 5],
    mouths: {
      dot: [[0, 0, "M"]],
      flat: [[0, 0, "M"]],
      smile: [
        [-1, 0, "M"],
        [0, 1, "M"],
        [1, 0, "M"],
      ],
      cat: [
        [-1, 0, "M"],
        [0, 1, "M"],
        [1, 0, "M"],
      ],
      o: [
        [0, 0, "M"],
        [0, 1, "M"],
      ],
      wide: [
        [-1, 0, "M"],
        [0, 0, "M"],
        [1, 0, "M"],
        [-1, 1, "M"],
        [0, 1, "Q"],
        [1, 1, "M"],
      ],
    },
  },
};

/**
 * The face for a big girl (the desktop pet), on the fine grid. Up close the
 * small face's square eyes and lowered lids read as a cold stare, so these
 * are taller and rounder with a big catchlight, look down by moving the
 * pupil instead of the lid, and sit over soft cheeks and a small mouth.
 */
const BIG_FACE: FaceArt = {
  eyeL: -9,
  eyeY: 12.6,
  mouthY: 18.6,
  eyes: {
    open: [
      "..EEEE.",
      "EEEEEEE",
      ".EddddE",
      ".dddddd",
      ".ddEEdd",
      ".edEEde",
      ".eellee",
      "..eeee.",
    ],
    up: [
      "..EEEE.",
      "EEEEEEE",
      ".EdEEdE",
      ".ddEEdd",
      ".dddddd",
      ".edddde",
      ".eellee",
      "..eeee.",
    ],
    down: [
      "..EEEE.",
      "EEEEEEE",
      ".EddddE",
      ".dddddd",
      ".dddddd",
      ".edEEde",
      ".eeEEee",
      "..eeee.",
    ],
    sleepy: [
      ".......",
      ".......",
      "..EEEE.",
      "EEEEEEE",
      ".EdEEdE",
      ".eellee",
      "..eeee.",
      ".......",
    ],
    closed: [
      ".......",
      ".......",
      ".......",
      ".......",
      "EE....E",
      ".EEEEE.",
      ".......",
      ".......",
    ],
    happy: [
      ".......",
      ".......",
      "...EE..",
      "..E..E.",
      ".E....E",
      ".......",
      ".......",
      ".......",
    ],
    dizzy: [
      ".......",
      ".E....E",
      "..E..E.",
      "...EE..",
      "..E..E.",
      ".E....E",
      ".......",
      ".......",
    ],
  },
  shine: { open: [2, 4], up: [2, 2], down: [3, 5], sleepy: [4, 4] },
  light: [
    [1, 0],
    [2, 0],
    [1, 1],
    [2, 1],
  ],
  glint: [4, 6],
  pupil: [2, 3],
  pupilH: 2,
  cheeks: [6, 7, 8],
  blush: [5, 9],
  cheekRows: [0, -1],
  mouths: {
    dot: [[0, 0, "M"]],
    flat: [[0, 0, "M"]],
    smile: [
      [-1, 0, "M"],
      [0, 1, "M"],
      [1, 0, "M"],
    ],
    cat: [
      [-1, 0, "M"],
      [0, 1, "M"],
      [1, 0, "M"],
    ],
    o: [
      [0, 0, "M"],
      [0, 1, "Q"],
    ],
    wide: [
      [-1, 0, "M"],
      [0, 0, "M"],
      [1, 0, "M"],
      [-1, 1, "Q"],
      [0, 1, "Q"],
      [1, 1, "Q"],
      [0, 2, "M"],
    ],
  },
};

/**
 * The desktop pet's face on her own finer grid: eyes with a lash that flicks
 * out at the corner, an iris going light toward the bottom, two catchlights.
 */
const PET_FACE: FaceArt = {
  eyeL: -12,
  eyeY: 12.0,
  mouthY: 18.9,
  eyes: {
    open: [
      "..EEEEEE.",
      ".EEEEEEEE",
      "EEEddddEE",
      ".dddddddd",
      ".dddddddd",
      ".dddEEddd",
      ".ddeEEedd",
      ".deeeeeed",
      ".eeellee.",
      "..eellee.",
      "...eeee..",
    ],
    up: [
      "..EEEEEE.",
      ".EEEEEEEE",
      "EEEddddEE",
      ".dddEEddd",
      ".dddEEddd",
      ".dddddddd",
      ".ddeeeedd",
      ".deeeeeed",
      ".eeellee.",
      "..eellee.",
      "...eeee..",
    ],
    down: [
      "..EEEEEE.",
      ".EEEEEEEE",
      "EEEddddEE",
      ".dddddddd",
      ".dddddddd",
      ".dddddddd",
      ".dddEEddd",
      ".deeEEeed",
      ".eeellee.",
      "..eellee.",
      "...eeee..",
    ],
    sleepy: [
      ".........",
      ".........",
      ".........",
      ".........",
      "..EEEEEE.",
      "EEEEEEEEE",
      ".dddEEddd",
      ".deeeeeed",
      "..eellee.",
      "...eeee..",
      ".........",
    ],
    closed: [
      ".........",
      ".........",
      ".........",
      ".........",
      ".........",
      ".........",
      "EE......E",
      ".EE....EE",
      "..EEEEEE.",
      ".........",
      ".........",
    ],
    happy: [
      ".........",
      ".........",
      ".........",
      ".........",
      "...EEEE..",
      "..EE..EE.",
      ".EE....EE",
      ".E......E",
      ".........",
      ".........",
      ".........",
    ],
    dizzy: [
      ".........",
      ".........",
      ".........",
      "..E....E.",
      "...E..E..",
      "....EE...",
      "....EE...",
      "...E..E..",
      "..E....E.",
      ".........",
      ".........",
    ],
  },
  shine: { open: [3, 5], up: [5, 3], down: [3, 6], sleepy: [6, 6] },
  light: [
    [1, 0],
    [2, 0],
    [1, 1],
    [2, 1],
  ],
  glint: [6, 7],
  pupil: [3, 4],
  pupilH: 2,
  cheeks: [9, 10, 11],
  blush: [8, 12],
  cheekRows: [-4, -3],
  mouths: {
    dot: [[0, 0, "M"]],
    flat: [
      [-1, 0, "M"],
      [0, 0, "M"],
      [1, 0, "M"],
    ],
    smile: [
      [-2, 0, "M"],
      [-1, 1, "M"],
      [0, 1, "M"],
      [1, 1, "M"],
      [2, 0, "M"],
    ],
    cat: [
      [-1, 0, "M"],
      [0, 1, "M"],
      [1, 0, "M"],
    ],
    o: [
      [0, 0, "M"],
      [-1, 1, "M"],
      [0, 1, "Q"],
      [1, 1, "M"],
      [0, 2, "M"],
    ],
    wide: [
      [-2, 0, "M"],
      [-1, 0, "M"],
      [0, 0, "M"],
      [1, 0, "M"],
      [2, 0, "M"],
      [-1, 1, "Q"],
      [0, 1, "Q"],
      [1, 1, "Q"],
      [-1, 2, "M"],
      [0, 2, "M"],
      [1, 2, "M"],
    ],
  },
};

function paintFace(m: Layer, grid: Grid, look: Look, art: FaceArt) {
  const put = (c: number, r: number, v: string) => m.set(grid.idx(c, r), v);
  const top = grid.row(art.eyeY ?? 13);
  const mouth = grid.row(art.mouthY ?? 18);
  const tall = art.pupilH ?? 1;
  const width = art.eyes.open[0].length;
  const g = Math.round(clamp(look.lx, -1, 1));
  for (const side of [-1, 1]) {
    const kind =
      side < 0 && look.wink
        ? "closed"
        : look.eyes === "open"
          ? look.ly < 0
            ? "up"
            : look.ly > 0
              ? "down"
              : "open"
          : look.eyes;
    const own = side > 0 ? art.near?.[kind] : undefined;
    const rows = own ?? art.eyes[kind];
    // The column of pattern index k on this side; the near eye mirrors,
    // unless it has its own pattern, read left to right.
    const col = (k: number) => (side < 0 ? art.eyeL + k : -art.eyeL - k);
    const at = own ? (k: number) => col(width - 1 - k) : col;
    for (let r = 0; r < rows.length; r += 1)
      for (let k = 1; k < width; k += 1) put(col(k), top + r, "S");
    rows.forEach((row, r) => {
      for (let k = 0; k < width; k += 1)
        if (row[k] !== ".") put(at(k), top + r, row[k]);
    });
    const shine = art.shine[kind];
    if (!shine) continue;
    // The pupil slides with a glance; the catchlights stay up and to the
    // left on both eyes, as the light does.
    const left = side < 0 ? art.eyeL + 1 : -art.eyeL - (width - 1);
    const [hr, pr] = shine;
    if (g)
      for (let r = pr; r < pr + tall; r += 1) {
        for (const k of art.pupil) put(left + k, top + r, "d");
        for (const k of art.pupil) put(left + k + g, top + r, "E");
      }
    for (const [k, r] of art.light)
      if (kind !== "sleepy" || r === 0) put(left + k + g, top + hr + r, "W");
    if (art.glint && (kind === "open" || kind === "down"))
      put(left + art.glint[0] + g, top + art.glint[1], "W");
  }
  for (const side of [-1, 1])
    for (const dr of art.cheekRows ?? [0]) {
      for (const k of art.cheeks) put(side * k, mouth + dr, "s");
      if (look.blush > 0.5)
        for (const k of art.blush) put(side * k, mouth + dr, "s");
    }
  for (const [c, r, v] of art.mouths[look.mouth]) put(c, mouth + r, v);
}

// ---------- arms ----------

function solve(s: P, h: P, side: number, hint?: readonly [number, number]) {
  let dx = h.x - s.x;
  let dy = h.y - s.y;
  let d = Math.hypot(dx, dy) || 0.001;
  const max = UPPER + FORE - 0.05;
  const min = Math.abs(UPPER - FORE) + 0.6;
  if (d > max || d < min) {
    const k = clamp(d, min, max) / d;
    dx *= k;
    dy *= k;
    d = Math.hypot(dx, dy);
  }
  const hand = { x: s.x + dx, y: s.y + dy };
  const a = (UPPER ** 2 - FORE ** 2 + d * d) / (2 * d);
  const hh = Math.sqrt(Math.max(0, UPPER ** 2 - a * a));
  const ux = dx / d;
  const uy = dy / d;
  const bx = s.x + ux * a;
  const by = s.y + uy * a;
  const c1 = { x: bx - uy * hh, y: by + ux * hh };
  const c2 = { x: bx + uy * hh, y: by - ux * hh };
  // Elbows bend out from the body, and down, unless told otherwise.
  const score = hint
    ? (c: P) => -Math.hypot(c.x - hint[0], c.y - hint[1])
    : (c: P) => (c.x - s.x) * side + (c.y - s.y) * 0.7;
  return { e: score(c1) >= score(c2) ? c1 : c2, h: hand };
}

function armLayers(grid: Grid, st: ArmStyle, s: P, e: P, h: P, open: boolean) {
  const fx = h.x - e.x;
  const fy = h.y - e.y;
  const fl = Math.hypot(fx, fy) || 1;
  const wrist = { x: h.x - (fx / fl) * 0.75, y: h.y - (fy / fl) * 0.75 };
  const hr = st.hand * (open ? 1.2 : 1);
  const sleeve: Shape = (x, y) => {
    const [df, tf, sf] = segDist(x, y, e, wrist);
    if (df <= st.fore) {
      if (tf > st.cuffFrom) return st.cuff;
      return sf > st.fore * 0.3 ? st.shade : st.sleeve;
    }
    const [du, tu, su] = segDist(x, y, s, e);
    const r = st.puff + (st.upper - st.puff) * clamp(tu * 1.4, 0, 1);
    if (du <= r) {
      if (st.shine && tu < 0.4 && su < -r * 0.3) return st.shine;
      return su > r * 0.3 ? st.shade : st.sleeve;
    }
    return null;
  };
  const hand: Shape = (x, y) =>
    ell(x, y, h.x, h.y, hr, hr * 0.95)
      ? x - h.x + (y - h.y) > hr * 0.9
        ? "Z"
        : "S"
      : null;
  const box: Box = [
    Math.min(s.x, e.x, h.x) - 2.5,
    Math.min(s.y, e.y, h.y) - 2.5,
    Math.max(s.x, e.x, h.x) + 2.5,
    Math.max(s.y, e.y, h.y) + 2.5,
  ];
  return { sleeve: grid.sample(sleeve, box), palm: grid.sample(hand, box) };
}

// ---------- poses ----------

export interface Pose {
  sit?: boolean;
  /** Rows the head sinks: breathing, nodding off. */
  nod?: number;
  /** Columns the head leans toward the near side. */
  lean?: number;
  /** Hair swinging: -1 back, 0, 1 forward. */
  sway?: number;
  /** Units each leg is lifted: [far, near]. */
  legs?: readonly [number, number];
  tail?: number;
  look?: Look;
  near?: Hand;
  far?: Hand;
  /** Held things behind the hair and the hands: a book. */
  props?: readonly Prop[];
  /** Held things over the face, under the hands: a cup at the lips. */
  held?: readonly Prop[];
  /** Things in front of everything: a hammer, a lens, a laptop lid. */
  over?: readonly Prop[];
  /** Names the props, for the cache. */
  key?: string;
  /** Things worn on her head (a hard hat, a hair clip), moving with it. */
  onHead?: readonly Prop[];
  /** Sitting on an edge, her legs hanging over it below her foot line. */
  perch?: boolean;
}

/** A hard hat: a dome over the crown and a brim at the forehead. */
export function hardHat(c: {
  color: string;
  light: string;
  shade: string;
}): Prop {
  return prop(
    (x, y) => {
      const d = x - CX;
      if (y >= 5.0 && y <= 6.4 && Math.abs(d) <= 10.2)
        return y > 5.8 ? c.shade : c.color;
      if (y < 5.0 && ell(x, y, CX, 5.2, 8.2, 6.0)) {
        if (Math.abs(d + 2.6) < 0.6 && y < 3.6) return c.light;
        return d > 5 ? c.shade : c.color;
      }
      return null;
    },
    [CX - 11, -2, CX + 11, 7],
  );
}

interface Composed {
  cells: Array<readonly [number, number, string]>;
  hands: { near: P; far: P };
  /** How far her cells reach from the middle, and her top and bottom rows. */
  reach: number;
  top: number;
  bottom: number;
}

// What lies over what; a line goes on the lower of two touching parts when
// either is an arm or a held thing and their colors would run together.
const Z = {
  back: 1,
  armBack: 2,
  legs: 3,
  torso: 4,
  legsFront: 5,
  prop: 6,
  hair: 7,
  arm: 8,
  face: 9,
  held: 10,
  armFront: 11,
  over: 12,
};
const LIMB = new Set([Z.armBack, Z.prop, Z.arm, Z.held, Z.armFront, Z.over]);

/** Channels of `#rrggbb` or `rgb(…)`, the forms the scene's palette comes in. */
function rgbOf(color: string): [number, number, number] {
  if (color[0] === "#") {
    const n = Number.parseInt(color.slice(1, 7), 16);
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  }
  const [r = 0, g = 0, b = 0] = (color.match(/[\d.]+/g) ?? []).map(Number);
  return [r, g, b];
}

function compose(
  client: ClientType,
  grid: Grid,
  pose: Pose,
  big: boolean,
): Composed {
  const b = build(client, grid, big);
  const g = b.girl;
  const size = grid.w * grid.h;
  const v: Array<string | null> = Array.from({ length: size }, () => null);
  const z = new Float32Array(size);
  const put = (
    c: number,
    r: number,
    color: string,
    depth: number,
    keep?: (d: number) => boolean,
  ) => {
    if (!grid.has(c, r)) return;
    const i = grid.idx(c, r);
    if (keep && v[i] && keep(z[i])) return;
    v[i] = color;
    z[i] = depth;
  };
  const blit = (
    m: Layer,
    dc: number,
    dr: number,
    depth: number | ((i: number) => number),
    keep?: (d: number) => boolean,
  ) => {
    for (const [i, c] of m)
      put(
        grid.cOf(i) + dc,
        grid.rOf(i) + dr,
        c,
        typeof depth === "number" ? depth : depth(i),
        keep,
      );
  };
  const props = (list: readonly Prop[] | undefined, depth: number) => {
    for (const p of list ?? []) blit(grid.sample(p.s, p.box), 0, 0, depth);
  };
  const sit = Boolean(pose.sit);
  const drop = sit ? Math.round(SIT * grid.k) : 0;
  const nod = pose.nod ?? 0;
  const lean = pose.lean ?? 0;
  const sway = clamp(Math.round(pose.sway ?? 0), -1, 1);

  const perch = sit && Boolean(pose.perch);
  // Perched on a window, her hands rest on its edge beside her; sitting on
  // the ground, in her lap.
  const rest = (side: number): Hand =>
    perch
      ? { x: CX + side * 6.4, y: 27.4 + SIT }
      : sit
        ? { x: CX + side * 1.6, y: 27.2 + SIT, front: true }
        : side > 0
          ? g.idle.near
          : g.idle.far;
  const arms = (
    [
      [pose.far ?? rest(-1), -1],
      [pose.near ?? rest(1), 1],
    ] as const
  ).map(([hand, side]) => {
    // The shoulders go where the dropped torso puts them.
    const s = { x: CX + side * SH_X, y: SH_Y + drop / grid.k };
    const { e, h } = solve(s, hand, side, hand.elbow);
    return {
      hand,
      h,
      // The near arm lies a hair over the far one.
      over: side > 0 ? 0.5 : 0,
      ...armLayers(grid, g.arm, s, e, h, Boolean(hand.open)),
    };
  });

  // Sitting on the floor, long hair lies on it.
  const floor = sit && !perch ? grid.row(FOOT) : Infinity;
  for (const [i, c] of backOf(b, sway))
    if (grid.rOf(i) + drop + nod < floor)
      put(grid.cOf(i) + lean, grid.rOf(i) + drop + nod, c, Z.back);
  blit(tailOf(b, pose.tail ?? 0), 0, drop, Z.back);
  for (const a of arms)
    if (a.hand.back) {
      blit(a.sleeve, 0, 0, Z.armBack + a.over);
      blit(a.palm, 0, 0, Z.armBack + a.over);
    }
  const legs = legsOf(b, pose.legs ?? [0, 0], sit, perch);
  if (!sit || perch) blit(legs, 0, 0, Z.legs);
  blit(b.torso, 0, drop, Z.torso);
  if (sit && !perch) blit(legs, 0, 0, Z.legsFront);
  props(pose.props, Z.prop);
  // The head: face cells are face, the rest is hair.
  const raw = headOf(b, sway);
  const head = new Map(raw);
  paintFace(
    head,
    grid,
    pose.look ?? plainLook(),
    grid === PET ? PET_FACE : big && grid === FINE ? BIG_FACE : FACES[grid.k],
  );
  blit(head, lean, drop + nod, (i) => {
    const r = raw.get(i);
    return !r || r === "S" || r === "Z" ? Z.face : Z.hair;
  });
  // What she wears lies over the hair as a held thing does, so a line parts them.
  for (const p of pose.onHead ?? [])
    blit(grid.sample(p.s, p.box), lean, drop + nod, Z.held);
  // A shadow on the dress under the chin.
  for (let i = grid.w; i < size; i += 1) {
    const c = v[i];
    if (c && z[i] === Z.torso && z[i - grid.w] === Z.face)
      v[i] = g.shade[c] ?? c;
  }
  props(pose.held, Z.held);
  for (const a of arms) {
    if (a.hand.back) continue;
    const depth = (a.hand.front ? Z.armFront : Z.arm) + a.over;
    const keep = a.hand.front ? undefined : (d: number) => d >= Z.face;
    blit(a.sleeve, 0, 0, depth, keep);
    blit(a.palm, 0, 0, depth, keep);
  }
  props(pose.over, Z.over);

  // One letter names her own color; anything longer is a prop's CSS color.
  const hex = (c: string) => (c.length > 1 ? c : (g.pal[c] ?? "#000000"));
  const alike = (a: string, c: string) => {
    const [r1, g1, b1] = rgbOf(hex(a));
    const [r2, g2, b2] = rgbOf(hex(c));
    return Math.hypot(r1 - r2, g1 - g2, b1 - b2) < 110;
  };
  const line = new Set<number>();
  for (let i = 0; i < size; i += 1) {
    const a = v[i];
    if (!a) continue;
    const c = grid.cOf(i);
    const r = grid.rOf(i);
    for (const [oc, or] of [
      [1, 0],
      [0, 1],
    ]) {
      if (!grid.has(c + oc, r + or)) continue;
      const j = grid.idx(c + oc, r + or);
      const d = v[j];
      if (!d || z[i] === z[j]) continue;
      if (!LIMB.has(Math.floor(z[i])) && !LIMB.has(Math.floor(z[j]))) continue;
      if (!alike(a, d)) continue;
      line.add(z[i] < z[j] ? i : j);
    }
  }
  // Up close a line takes the darkened color of what it edges: soft, not ink.
  const soft = (c: string) => {
    const [r1, g1, b1] = rgbOf(hex(c));
    const [r2, g2, b2] = rgbOf(hex("K"));
    const m = (a: number, b: number) =>
      Math.round(a * 0.32 + b * 0.68)
        .toString(16)
        .padStart(2, "0");
    return `#${m(r1, r2)}${m(g1, g2)}${m(b1, b2)}`;
  };
  for (const i of line) v[i] = big ? soft(v[i]!) : "k";
  // Hair touching the outside gets its dark shade, then an outline goes round.
  const solid = (c: number, r: number) =>
    grid.has(c, r) && v[grid.idx(c, r)] !== null;
  const open = (i: number) => {
    const c = grid.cOf(i);
    const r = grid.rOf(i);
    return (
      !solid(c + 1, r) ||
      !solid(c - 1, r) ||
      !solid(c, r + 1) ||
      !solid(c, r - 1)
    );
  };
  const rim: Array<[number, string]> = [];
  for (let i = 0; i < size; i += 1) {
    const c = v[i];
    if (!c || (z[i] !== Z.hair && z[i] !== Z.back)) continue;
    const to = g.rim[c];
    if (to && open(i)) rim.push([i, to]);
  }
  for (const [i, to] of rim) v[i] = to;
  const edge: number[] = [];
  for (let i = 0; i < size; i += 1) {
    if (v[i]) continue;
    const c = grid.cOf(i);
    const r = grid.rOf(i);
    if (
      solid(c + 1, r) ||
      solid(c - 1, r) ||
      solid(c, r + 1) ||
      solid(c, r - 1)
    )
      edge.push(i);
  }
  const ink = edge.map((i) => {
    if (!big) return "K";
    // The darkest thing it edges.
    let best = "K";
    let lum = Infinity;
    const c = grid.cOf(i);
    const r = grid.rOf(i);
    for (const [oc, or] of [
      [0, 1],
      [0, -1],
      [1, 0],
      [-1, 0],
    ]) {
      const n = solid(c + oc, r + or) ? v[grid.idx(c + oc, r + or)]! : null;
      if (!n) continue;
      const [cr, cg, cb] = rgbOf(hex(n));
      const l = cr * 0.3 + cg * 0.59 + cb * 0.11;
      if (l < lum) {
        lum = l;
        best = n;
      }
    }
    return soft(best);
  });
  edge.forEach((i, n) => (v[i] = ink[n]));
  const cells: Array<readonly [number, number, string]> = [];
  let reach = 0;
  let top = 0;
  let bottom = grid.r0;
  for (let i = 0; i < size; i += 1) {
    const c = v[i];
    if (!c) continue;
    const col = grid.cOf(i);
    const row = grid.rOf(i);
    cells.push([col, row, hex(c)]);
    reach = Math.max(reach, Math.abs(col));
    top = Math.min(top, row);
    bottom = Math.max(bottom, row);
  }
  return {
    cells,
    hands: { far: arms[0].h, near: arms[1].h },
    reach,
    top,
    bottom,
  };
}

// Props take the scene's colors, so each palette keeps its own poses.
const caches = new WeakMap<object, Map<string, Composed>>();

function keyOf(pose: Pose): string {
  const l = pose.look ?? plainLook();
  const h = (x?: Hand) =>
    x
      ? `${x.x.toFixed(2)},${x.y.toFixed(2)}${x.front ? "f" : ""}${x.back ? "b" : ""}${x.open ? "o" : ""}${x.elbow ?? ""}`
      : "-";
  return [
    pose.sit ? 1 : 0,
    pose.nod ?? 0,
    pose.lean ?? 0,
    pose.sway ?? 0,
    (pose.legs ?? [0, 0]).join(","),
    pose.tail ?? 0,
    l.eyes,
    Math.round(l.lx),
    Math.round(l.ly),
    l.mouth,
    l.blush > 0.5 ? 1 : 0,
    l.wink ? 1 : 0,
    h(pose.near),
    h(pose.far),
    pose.key ?? "",
    pose.perch ? 1 : 0,
  ].join("|");
}

function composed(
  palette: object,
  client: ClientType,
  grid: Grid,
  pose: Pose,
  big: boolean,
): Composed {
  let cache = caches.get(palette);
  if (!cache) caches.set(palette, (cache = new Map()));
  const key = `${client}|${grid.k}|${big ? "big|" : ""}${keyOf(pose)}`;
  let c = cache.get(key);
  if (!c) {
    c = compose(client, grid, pose, big);
    cache.set(key, c);
    if (cache.size > 900)
      for (const k of cache.keys()) {
        cache.delete(k);
        if (cache.size < 700) break;
      }
  }
  return c;
}

/** Logical px of unit point (x, y) on a girl standing at (cx, fy). */
export function unitPx(
  cx: number,
  fy: number,
  scale: number,
  face: number,
  x: number,
  y: number,
): [number, number] {
  return [cx + (face > 0 ? 1 : -1) * (x - CX) * scale, fy + (y - FOOT) * scale];
}

/**
 * Draws a girl, feet at (cx, fy), `scale` px a unit, facing `face`; lying,
 * she is on her back along the floor with her head toward the side she
 * faces. Returns where her hands are, in logical px.
 */
export function drawGirl(
  pen: Pen,
  client: ClientType,
  cx: number,
  fy: number,
  scale: number,
  face: number,
  pose: Pose,
  lie = false,
): { near: [number, number]; far: [number, number] } {
  const grid = gridFor(scale);
  const c = composed(pen.palette, client, grid, pose, scale >= BIG);
  const f = face > 0 ? 1 : -1;
  const u = scale / grid.k;
  const key = `girl|${client}|${scale}|${f}|${lie ? 1 : 0}|${keyOf(pose)}`;
  // Lying, rows run along the floor and columns stack up, squashed to half
  // pixels.
  const flat = Math.max(0.5, Math.round(u * 1.24) / 2);
  const mid = Math.round(grid.k * 17);
  const along = Math.max(Math.abs(c.top + mid), Math.abs(c.bottom + mid)) + 2;
  const box = lie
    ? ([-along * u, (-2 * c.reach - 3) * flat, along * u, flat] as const)
    : ([
        -(c.reach + 1) * u,
        (c.top - 1) * u,
        (c.reach + 1) * u,
        (c.bottom + 2) * u,
      ] as const);
  pen.stamp(key, cx, fy, box, (p) => {
    const by = new Map<string, number[][]>();
    for (const [col, row, color] of c.cells) {
      let list = by.get(color);
      if (!list) by.set(color, (list = []));
      if (lie)
        list.push([
          -f * (row + mid + 1) * u,
          (f * col - c.reach - 1) * flat,
          u,
          flat,
        ]);
      else list.push([(f * col - 0.5) * u, row * u, u, u]);
    }
    for (const [color, list] of by) p.rectsOf(color, list);
  });
  return {
    near: unitPx(cx, fy, scale, f, c.hands.near.x, c.hands.near.y),
    far: unitPx(cx, fy, scale, f, c.hands.far.x, c.hands.far.y),
  };
}

// ---------- held things ----------

export const prop = (s: Shape, box: Box): Prop => ({ s, box });

/** A straight stick from a to b, `r` units thick. */
export function stick(a: P, b: P, r: number, color: string): Prop {
  return prop(
    (x, y) => (segDist(x, y, a, b)[0] <= r ? color : null),
    [
      Math.min(a.x, b.x) - r - 1,
      Math.min(a.y, b.y) - r - 1,
      Math.max(a.x, b.x) + r + 1,
      Math.max(a.y, b.y) + r + 1,
    ],
  );
}

/** A hammer gripped at `hand`, its head across the end of a handle pointing (dx, dy). */
export function hammer(
  hand: P,
  dx: number,
  dy: number,
  colors: { wood: string; steel: string; shade: string },
): Prop[] {
  const end = { x: hand.x + dx * 4.6, y: hand.y + dy * 4.6 };
  return [
    stick(
      { x: hand.x - dx * 0.8, y: hand.y - dy * 0.8 },
      end,
      0.4,
      colors.wood,
    ),
    prop(
      (x, y) => {
        const ax = x - end.x;
        const ay = y - end.y;
        const along = ax * -dy + ay * dx;
        const across = ax * dx + ay * dy;
        if (Math.abs(along) <= 2.0 && across >= -1.1 && across <= 0.9)
          return across > 0.1 ? colors.shade : colors.steel;
        return null;
      },
      [end.x - 3, end.y - 3, end.x + 3, end.y + 3],
    ),
  ];
}
