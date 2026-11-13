// The big pet drawn from traced pixel art: one floating picture of her and,
// for each action, only the cells that action changes. Her faces, a slow bob
// and swaying feet are laid over it in code by whole pixels; nothing is
// rotated or moved by part of a pixel.
import type { Pen } from "../live-crew-draw";
import type { ClientType } from "../request-record-model";
import claude from "./pet-art/claude.json";
import codex from "./pet-art/codex.json";
import deepseek from "./pet-art/deepseek.json";
import { cfg } from "./toy-config";

export interface Point {
  x: number;
  y: number;
}

interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Rows of palette letters at (x, y): a space keeps what is under it, `.` clears it. */
export interface Patch {
  x: number;
  y: number;
  rows: string[];
}

export interface Kit {
  w: number;
  h: number;
  palette: Record<string, string>;
  /** Her floating picture, `.` for clear. */
  base: string[];
  /** What each action changes on the base. */
  frames: Record<string, Patch>;
  /** Frames in turn, by clip; an empty frame name is the base. */
  clips: Record<string, Array<{ frame: string; ms: number }>>;
  /** Whole faces laid over the base's: a blink, a laugh, a pout. */
  faces: Partial<Record<Face, Patch>>;
  /** Frames with a face of their own, which no face is laid over. */
  ownFace: string[];
  /** The point on her foot line: between her soles. */
  anchor: Point;
  /** The top row of her, curl and all. */
  crown: number;
  /** The top row of her head under the curl; badges sit from here. */
  head?: number;
  /** The curl on top of her head, which sways a column either way. */
  curl?: Rect | null;
  /** Her near and far leg, lifted a pixel in turn. */
  legs: Rect[];
  /** Her waving hand in the greet frame. */
  hand: Point | null;
}

/** Her faces, beyond the one drawn on her picture. */
export type Face =
  | "blink"
  | "happy"
  | "shy"
  | "sparkle"
  | "pout"
  | "wow"
  | "wince";

const KITS: Partial<Record<ClientType, Kit>> = {
  claude_code: claude as Kit,
  codex: codex as Kit,
  deepseek_harness: deepseek as Kit,
};

/** Whether the client has a drawn girl of its own. */
export const hasKit = (client: string): client is ClientType =>
  Object.hasOwn(KITS, client);

export const kitOf = (client: ClientType): Kit | null =>
  hasKit(client) ? KITS[client]! : null;

/** Points a grid pixel at each size: 2, 3 and 4 device pixels on Retina. */
const PX = { small: 1, normal: 1.5, large: 2 } as const;

/**
 * Points a grid pixel at the chosen size, on whole device pixels; between
 * two, the smaller, so she is never bigger than chosen.
 */
export function kitPx(): number {
  const dpr = typeof window === "undefined" ? 1 : window.devicePixelRatio || 1;
  return Math.max(1, Math.round(PX[cfg.prefs.pet.size] * dpr - 0.01)) / dpr;
}

const spans = new WeakMap<Kit, { lo: number; hi: number }>();

/** Her height from crown to soles and half her width, in points. */
export function kitSize(kit: Kit) {
  let s = spans.get(kit);
  if (!s) {
    s = { lo: kit.w, hi: 0 };
    for (const row of kit.base)
      for (let x = 0; x < row.length; x += 1)
        if (row[x] !== ".") {
          s.lo = Math.min(s.lo, x);
          s.hi = Math.max(s.hi, x + 1);
        }
    spans.set(kit, s);
  }
  const px = kitPx();
  return {
    tall: (kit.anchor.y - kit.crown) * px,
    half: Math.max(kit.anchor.x - s.lo, s.hi - kit.anchor.x) * px,
  };
}

/** Grid rows she floats up by: a slow rise and fall, two rows at most. */
export const bobOf = (t: number) => [0, 1, 2, 1][Math.floor(t / 0.6) % 4];

export interface KitPose {
  clip: string;
  /** The face laid over her picture, if it has one; null keeps it. */
  face: Face | null;
  /** Feet swaying: 0 holds them still, 1 is the drawn pace. */
  pace: number;
  /** Seconds on her clock. */
  t: number;
  /** Small pictures worn on her head. */
  worn?: readonly Badge[];
}

/** A little pixel picture worn on her head, one grid pixel a pixel. */
export interface Badge {
  rows: readonly string[];
  ink: Readonly<Record<string, string>>;
  /** Its centre, from the top of her head. */
  x: number;
  y: number;
}

/** One frame laid out: palette letters or colors, `.` for clear. */
export interface Built {
  cells: readonly string[];
  /** Names everything that went into the frame. */
  key: string;
}

function frameAt(kit: Kit, clip: string, ms: number): string {
  const frames = kit.clips[clip] ?? kit.clips.idle;
  const total = frames.reduce((s, f) => s + f.ms, 0);
  let left = ms % total;
  for (const f of frames) {
    if (left < f.ms) return f.frame;
    left -= f.ms;
  }
  return frames[0].frame;
}

function overlay(kit: Kit, grid: string[], p: Patch) {
  p.rows.forEach((row, r) => {
    const y = p.y + r;
    if (y < 0 || y >= kit.h) return;
    for (let c = 0; c < row.length; c += 1) {
      const x = p.x + c;
      if (row[c] !== " " && x >= 0 && x < kit.w) grid[y * kit.w + x] = row[c];
    }
  });
}

/** Moves the upper part of `r` a column sideways; its root stays put. */
function sway(kit: Kit, grid: string[], r: Rect, dx: number) {
  const rows = Math.ceil(r.h * 0.6);
  for (let y = r.y; y < r.y + rows; y += 1) {
    const row = grid.slice(y * kit.w + r.x, y * kit.w + r.x + r.w);
    for (let x = 0; x < r.w; x += 1) grid[y * kit.w + r.x + x] = ".";
    row.forEach((c, x) => {
      const to = r.x + x + dx;
      if (c !== "." && to >= 0 && to < kit.w) grid[y * kit.w + to] = c;
    });
  }
}

/** Lifts what is inside `r` one row, leaving clear under it. */
function lift(kit: Kit, grid: string[], r: Rect) {
  for (let y = r.y; y < r.y + r.h; y += 1)
    for (let x = r.x; x < r.x + r.w; x += 1) {
      const at = y * kit.w + x;
      if (y > 0 && grid[at] !== ".") grid[at - kit.w] = grid[at];
      grid[at] = ".";
    }
}

/** Frames already laid out, by key: she shows only a handful of them. */
const laid = new WeakMap<Kit, Map<string, Built>>();

/** Lays her picture out for a pose. */
export function build(kit: Kit, pose: KitPose): Built {
  const ms = pose.t * 1000;
  const name = frameAt(kit, pose.clip, ms);
  const face =
    pose.face && !kit.ownFace.includes(name) ? kit.faces[pose.face] : undefined;
  // Her feet take turns: near up, both down, far up, both down.
  const step = pose.pace > 0 ? Math.floor((ms * pose.pace) / 1000) % 4 : 1;
  const leg = step === 0 ? 0 : step === 2 ? 1 : -1;
  const worn = pose.worn ?? [];
  // Her curl sways while she floats, a beat behind her.
  const curl =
    kit.curl && pose.pace > 0
      ? [0, 1, 0, -1][Math.floor(pose.t / 0.45) % 4]
      : 0;
  const key = [name, face ? pose.face : "", leg, curl, worn.length].join();
  let cache = laid.get(kit);
  if (!cache) laid.set(kit, (cache = new Map()));
  const hit = cache.get(key);
  if (hit) return hit;

  const grid = kit.base.join("").split("");
  const frame = kit.frames[name];
  if (frame) overlay(kit, grid, frame);
  if (face) overlay(kit, grid, face);
  if (leg >= 0 && kit.legs[leg]) lift(kit, grid, kit.legs[leg]);
  if (curl && kit.curl) sway(kit, grid, kit.curl, curl);
  for (const b of worn) {
    const x0 = Math.round(kit.anchor.x + b.x - b.rows[0].length / 2);
    const y0 = Math.round((kit.head ?? kit.crown) + b.y - b.rows.length / 2);
    b.rows.forEach((row, r) => {
      for (let q = 0; q < row.length; q += 1) {
        const y = y0 + r;
        const x = x0 + q;
        const ink = b.ink[row[q]];
        if (ink && y >= 0 && y < kit.h && x >= 0 && x < kit.w)
          grid[y * kit.w + x] = ink;
      }
    });
  }
  const built: Built = { cells: grid, key };
  if (cache.size > 160) cache.clear();
  cache.set(key, built);
  return built;
}

/** Where a grid point lands on screen with her anchor at (x, foot). */
export function onScreen(
  kit: Kit,
  p: Point,
  x: number,
  foot: number,
  px = kitPx(),
): Point {
  return {
    x: x + (p.x - kit.anchor.x) * px,
    y: foot + (p.y - kit.anchor.y) * px,
  };
}

/**
 * Paints a built frame with her anchor at (x, foot). Runs of one color in a
 * row go down as one rect, on whole device pixels so every grid pixel keeps
 * the same size.
 */
export function paintKit(
  pen: Pen,
  kit: Kit,
  built: Built,
  x: number,
  foot: number,
  px = kitPx(),
) {
  const k = pen.dpr || 1;
  const ox = Math.round(x * k) / k;
  const oy = Math.round(foot * k) / k;
  const { cells } = built;
  const { anchor } = kit;
  for (let y = 0; y < kit.h; y += 1) {
    const sy = oy + (y - anchor.y) * px;
    let x0 = 0;
    while (x0 < kit.w) {
      const c = cells[y * kit.w + x0];
      let x1 = x0 + 1;
      while (x1 < kit.w && cells[y * kit.w + x1] === c) x1 += 1;
      if (c !== ".") {
        // Worn badges are laid out as colors, her picture as palette letters.
        const color = c.startsWith("#") ? c : kit.palette[c];
        if (color)
          pen.rect(ox + (x0 - anchor.x) * px, sy, (x1 - x0) * px, px, color);
      }
      x0 = x1;
    }
  }
}
