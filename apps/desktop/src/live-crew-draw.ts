import { modelVendor } from "@/components/ModelBrandIcon";
import type { Vendor } from "@/components/vendor-marks";

import type {
  Crew,
  CrewClone,
  CrewMember,
  CrewWalker,
} from "./live-crew-model";
import { crownOf, SKIN, spriteOf } from "./live-crew-sprites";
import type { ClientType } from "./request-record-model";
import { clientSeriesSlot } from "./series-colors";

/**
 * Drawing for the live crew: a pen that lands every pixel on device pixels,
 * the colors from the `--crew-*` tokens, the characters' sprites and what
 * they do at a station. Scenes build on these; nothing here keeps state
 * beyond the particles a scene hands in.
 */

/** The width the scenes were drawn for; wider or narrower ones spread out. */
export const DEMO_WIDTH = 1006;
/** Art pixel size in logical px. */
export const U = 4;

/** Every color the scenes use, by the token that holds it. */
const TOKENS = {
  bg: "--crew-bg",
  ink: "--crew-ink",
  eye: "--crew-eye",
  muted: "--crew-muted",
  navy: "--crew-navy",
  chip: "--crew-chip",
  cloud: "--crew-cloud",
  line: "--crew-line",
  paper: "--crew-paper",
  shade: "--crew-shade",
  wood: "--crew-wood",
  woodTop: "--crew-wood-top",
  crate: "--crew-crate",
  box: "--crew-box",
  plank: "--crew-plank",
  plankEdge: "--crew-plank-edge",
  steel: "--crew-steel",
  console: "--crew-console",
  screen: "--crew-screen",
  screenOff: "--crew-screen-off",
  screenOn: "--crew-screen-on",
  slate: "--crew-slate",
  slateLight: "--crew-slate-light",
  steam: "--crew-steam",
  lampOff: "--crew-lamp-off",
  ground: "--crew-ground",
  hills: "--crew-hills",
  moon: "--crew-moon",
  bookLine: "--crew-book-line",
  pageFlip: "--crew-page-flip",
  keys: "--crew-keys",
  pale: "--crew-pale",
  paleLight: "--crew-pale-light",
  panel: "--crew-panel",
  wall: "--crew-wall",
  floor: "--crew-floor",
  floorSeam: "--crew-floor-seam",
  skirting: "--crew-skirting",
  shelfBack: "--crew-shelf-back",
  sheet: "--crew-sheet",
  monitor: "--crew-monitor",
  printer: "--crew-printer",
  plant: "--crew-plant",
  plantLight: "--crew-plant-light",
  hull: "--crew-hull",
  deck: "--crew-deck",
  deckTop: "--crew-deck-top",
  glass: "--crew-glass",
  siteLine: "--crew-site-line",
  siteGround: "--crew-site-ground",
  speck: "--crew-speck",
  windows: "--crew-windows",
  craneDark: "--crew-crane-dark",
  dirt: "--crew-dirt",
  dirtDark: "--crew-dirt-dark",
  blueprint: "--crew-blueprint",
  blueprintInk: "--crew-blueprint-ink",
  brick: "--crew-brick",
  brickDark: "--crew-brick-dark",
  brickEdge: "--crew-brick-edge",
  yellow: "--crew-yellow",
  yellowLight: "--crew-yellow-light",
  flame: "--crew-flame",
  orange: "--crew-orange",
  red: "--crew-red",
  blue: "--crew-blue",
  green: "--crew-green",
  pink: "--crew-pink",
  violet: "--crew-violet",
  gold: "--crew-gold",
  brass: "--crew-brass",
  redDark: "--crew-red-dark",
  pine: "--crew-pine",
  pineLight: "--crew-pine-light",
  trunk: "--crew-trunk",
  tent: "--crew-tent",
  tentLight: "--crew-tent-light",
  cabin: "--crew-cabin",
  cabinLine: "--crew-cabin-line",
  timber: "--crew-timber",
  timberLight: "--crew-timber-light",
  timberDark: "--crew-timber-dark",
  skyline: "--crew-skyline",
  skylineLight: "--crew-skyline-light",
  sky: "--crew-sky",
  parchment: "--crew-parchment",
  rug: "--crew-rug",
  rugEdge: "--crew-rug-edge",
  footing: "--crew-footing",
  tower: "--crew-tower",
  towerLight: "--crew-tower-light",
  towerRaw: "--crew-tower-raw",
  towerRawWindow: "--crew-tower-raw-window",
  shine: "--crew-shine",
  failover: "--crew-failover",
  unknown: "--crew-unknown",
  series1: "--crew-series-1",
  series2: "--crew-series-2",
  series3: "--crew-series-3",
  series4: "--crew-series-4",
  series5: "--crew-series-5",
  series6: "--crew-series-6",
  series7: "--crew-series-7",
  series8: "--crew-series-8",
} as const;

export type CrewColor = keyof typeof TOKENS;
export type CrewPalette = Record<CrewColor, string>;

const SERIES: readonly CrewColor[] = [
  "series1",
  "series2",
  "series3",
  "series4",
  "series5",
  "series6",
  "series7",
  "series8",
];

let probe: CanvasRenderingContext2D | null | undefined;

/** Any CSS color as `#rrggbb` (or `rgba(…)`), so it can be mixed. */
function normalize(color: string): string {
  probe ??= document.createElement("canvas").getContext("2d");
  if (!probe) return color;
  probe.fillStyle = "#000000";
  probe.fillStyle = color;
  return String(probe.fillStyle);
}

/** Reads the crew's colors from the tokens in effect on `element`. */
export function readCrewPalette(element: Element): CrewPalette {
  const style = getComputedStyle(element);
  const palette = {} as CrewPalette;
  for (const [key, token] of Object.entries(TOKENS) as Array<
    [CrewColor, string]
  >) {
    palette[key] = normalize(style.getPropertyValue(token).trim() || "#888");
  }
  return palette;
}

function rgb(color: string): [number, number, number] {
  if (color.startsWith("#") && color.length === 7) {
    return [1, 3, 5].map((at) => parseInt(color.slice(at, at + 2), 16)) as [
      number,
      number,
      number,
    ];
  }
  const parts = color.match(/[\d.]+/g)?.map(Number) ?? [0, 0, 0];
  return [parts[0] ?? 0, parts[1] ?? 0, parts[2] ?? 0];
}

const rgbaCache = new Map<string, string>();
export function rgba(color: string, alpha: number): string {
  const key = `${color}|${alpha.toFixed(3)}`;
  let value = rgbaCache.get(key);
  if (!value) {
    value = `rgba(${rgb(color).join(",")},${alpha.toFixed(3)})`;
    if (rgbaCache.size > 4096) rgbaCache.clear();
    rgbaCache.set(key, value);
  }
  return value;
}

const mixCache = new Map<string, string>();
export function mix(color: string, to: string, k: number): string {
  const key = `${color}|${to}|${k}`;
  let value = mixCache.get(key);
  if (!value) {
    const a = rgb(color);
    const b = rgb(to);
    value = `rgb(${a.map((part, index) => Math.round(part + (b[index] - part) * k)).join(",")})`;
    if (mixCache.size > 1024) mixCache.clear();
    mixCache.set(key, value);
  }
  return value;
}

export const clamp = (value: number, low = 0, high = 1) =>
  Math.min(high, Math.max(low, value));

/** Logical bounds of a stamp around its origin: left, top, right, bottom. */
export type Box = readonly [number, number, number, number];

interface Stamp {
  canvas: HTMLCanvasElement | null;
  /** Device px from the bitmap's corner to the drawing origin. */
  x: number;
  y: number;
}

/** Bitmaps a pen has drawn once and blits since, least recently used first. */
class Stamps {
  private readonly map = new Map<string, Stamp>();

  get(key: string): Stamp | undefined {
    const stamp = this.map.get(key);
    if (stamp) {
      this.map.delete(key);
      this.map.set(key, stamp);
    }
    return stamp;
  }

  set(key: string, stamp: Stamp) {
    this.map.set(key, stamp);
    if (this.map.size <= STAMP_LIMIT) return;
    for (const old of this.map.keys()) {
      this.map.delete(old);
      if (this.map.size <= STAMP_LIMIT * 0.75) break;
    }
  }
}

const STAMP_LIMIT = 480;

function bitmap(width: number, height: number) {
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, width);
  canvas.height = Math.max(1, height);
  const g = canvas.getContext("2d");
  return g ? { canvas, g } : null;
}

/**
 * A pen in the scene's logical px. `m` device px make one logical px; every
 * rect is rounded to device pixels, so art stays sharp at any width. Text
 * keeps its CSS size whatever the scale.
 *
 * Anything drawn the same way more than once goes through `stamp`: drawn once
 * into a bitmap, then blitted, landing on exactly the device pixels drawing
 * it in place would have.
 */
export class Pen {
  readonly g: CanvasRenderingContext2D;
  readonly m: number;
  readonly dpr: number;
  readonly W: number;
  readonly H: number;
  readonly font: string;
  readonly palette: CrewPalette;
  /** Device px added to every position, for drawing into a stamp. */
  readonly ox: number;
  readonly oy: number;
  /** Where in the page text is drawn, so it looks as it would on screen. */
  readonly host: Element | null;
  /** Seconds on the crew's clock; animations read it. */
  T = 0;
  /** Seconds since the last frame; 0 for a still picture, which spawns nothing. */
  dt = 0;
  private fill = "";
  private readonly stamps: Stamps;
  private readonly widths: Map<string, number>;

  constructor(
    g: CanvasRenderingContext2D,
    options: {
      m: number;
      dpr: number;
      W: number;
      H: number;
      font: string;
      palette: CrewPalette;
      ox?: number;
      oy?: number;
      host?: Element | null;
    },
  ) {
    this.g = g;
    this.m = options.m;
    this.dpr = options.dpr;
    this.W = options.W;
    this.H = options.H;
    this.font = options.font;
    this.palette = options.palette;
    this.ox = options.ox ?? 0;
    this.oy = options.oy ?? 0;
    this.host = options.host ?? null;
    this.stamps = new Stamps();
    this.widths = new Map();
  }

  color(color: string) {
    if (color !== this.fill) {
      this.g.fillStyle = color;
      this.fill = color;
    }
  }

  private dx(x: number) {
    return Math.round(x * this.m + this.ox);
  }

  private dy(y: number) {
    return Math.round(y * this.m + this.oy);
  }

  /** A rect in logical px, snapped to device pixels. */
  rect(x: number, y: number, w: number, h: number, color: string) {
    if (!(w > 0 && h > 0)) return;
    const x0 = this.dx(x);
    const y0 = this.dy(y);
    this.color(color);
    this.g.fillRect(
      x0,
      y0,
      Math.max(1, this.dx(x + w) - x0),
      Math.max(1, this.dy(y + h) - y0),
    );
  }

  /** Many rects of one color as a single fill: `[x, y, w, h]` in logical px. */
  rectsOf(color: string, list: ReadonlyArray<readonly number[]>) {
    if (list.length < 2) {
      for (const [x, y, w, h] of list) this.rect(x, y, w, h, color);
      return;
    }
    const g = this.g;
    g.beginPath();
    for (const [x, y, w, h] of list) {
      if (!(w > 0 && h > 0)) continue;
      const x0 = this.dx(x);
      const y0 = this.dy(y);
      g.rect(
        x0,
        y0,
        Math.max(1, this.dx(x + w) - x0),
        Math.max(1, this.dy(y + h) - y0),
      );
    }
    this.color(color);
    g.fill();
  }

  /** A rect `w` × `h` art pixels at logical (x, y). */
  P(x: number, y: number, w: number, h: number, color: string) {
    this.rect(Math.round(x), Math.round(y), w * U, h * U, color);
  }

  /** `[x, y, w, h, color]` in art pixels from (ox, floor), y up the floor. */
  rects(
    ox: number,
    floor: number,
    list: ReadonlyArray<readonly [number, number, number, number, string]>,
  ) {
    for (const [x, y, w, h, color] of list) {
      this.P(ox + x * U, floor - (y + h) * U, w, h, color);
    }
  }

  alpha(value: number) {
    this.g.globalAlpha = value;
  }

  /** True `chance` times per second of frames, the demo's rate at 60 fps. */
  chance(rand: () => number, perFrame: number): boolean {
    return this.dt > 0 && rand() < perFrame * this.dt * 60;
  }

  /**
   * Draws what `paint` draws around the origin, within `box`, with the
   * origin at logical (x, y) rounded. `key` names the drawing; the first
   * call draws it into a bitmap that later calls blit.
   */
  stamp(
    key: string,
    x: number,
    y: number,
    box: Box,
    paint: (pen: Pen) => void,
  ) {
    const dx = Math.round(x) * this.m + this.ox;
    const dy = Math.round(y) * this.m + this.oy;
    const left = Math.floor(dx);
    const top = Math.floor(dy);
    // Where the origin falls inside a device pixel changes the rounding.
    const fx = dx - left;
    const fy = dy - top;
    const id = `${key}|${fx}|${fy}`;
    let stamp = this.stamps.get(id);
    if (!stamp) {
      stamp = this.draw(box, fx, fy, paint);
      this.stamps.set(id, stamp);
    }
    if (stamp.canvas) {
      this.g.drawImage(stamp.canvas, left - stamp.x, top - stamp.y);
    }
  }

  private draw(box: Box, fx: number, fy: number, paint: (pen: Pen) => void) {
    const m = this.m;
    const x = Math.ceil(-box[0] * m) + 1;
    const y = Math.ceil(-box[1] * m) + 1;
    const made = bitmap(
      x + Math.ceil(box[2] * m) + 2,
      y + Math.ceil(box[3] * m) + 2,
    );
    if (made) {
      paint(
        new Pen(made.g, {
          m,
          dpr: this.dpr,
          W: this.W,
          H: this.H,
          font: this.font,
          palette: this.palette,
          ox: x + fx,
          oy: y + fy,
          host: this.host,
        }),
      );
    }
    return { canvas: made?.canvas ?? null, x, y };
  }

  /** A vertical fade of `color` from alpha `top` down to `bottom`. */
  wash(
    x: number,
    y: number,
    w: number,
    h: number,
    color: string,
    top: number,
    bottom: number,
  ) {
    const x0 = this.dx(x);
    const y0 = this.dy(y);
    const y1 = this.dy(y + h);
    const gradient = this.g.createLinearGradient(0, y0, 0, y1);
    gradient.addColorStop(0, rgba(color, top));
    gradient.addColorStop(1, rgba(color, bottom));
    this.g.fillStyle = gradient;
    this.fill = "";
    this.g.fillRect(x0, y0, this.dx(x + w) - x0, y1 - y0);
  }

  /** A filled circle around a logical point. */
  disc(x: number, y: number, r: number, color: string) {
    const m = this.m;
    this.g.beginPath();
    this.g.arc(x * m + this.ox, y * m + this.oy, r * m, 0, Math.PI * 2);
    this.color(color);
    this.g.fill();
  }

  /** An image scaled smoothly into a logical rect. */
  image(source: CanvasImageSource, x: number, y: number, w: number, h: number) {
    const x0 = this.dx(x);
    const y0 = this.dy(y);
    const smooth = this.g.imageSmoothingEnabled;
    this.g.imageSmoothingEnabled = true;
    this.g.drawImage(source, x0, y0, this.dx(x + w) - x0, this.dy(y + h) - y0);
    this.g.imageSmoothingEnabled = smooth;
  }

  /** Draws `paint` clipped to a logical rect. */
  clip(x: number, y: number, w: number, h: number, paint: () => void) {
    const x0 = this.dx(x);
    const y0 = this.dy(y);
    this.g.save();
    this.g.beginPath();
    this.g.rect(x0, y0, this.dx(x + w) - x0, this.dy(y + h) - y0);
    this.g.clip();
    paint();
    this.g.restore();
    this.fill = "";
  }

  /** A soft round glow of `color`, fading out at radius `r`. */
  glow(
    x: number,
    y: number,
    r0: number,
    r: number,
    color: string,
    alpha: number,
  ) {
    const m = this.m;
    const cx = x * m + this.ox;
    const cy = y * m + this.oy;
    const gradient = this.g.createRadialGradient(cx, cy, r0 * m, cx, cy, r * m);
    gradient.addColorStop(0, rgba(color, alpha));
    gradient.addColorStop(1, rgba(color, 0));
    this.g.fillStyle = gradient;
    this.fill = "";
    this.g.fillRect(
      this.dx(x - r),
      this.dy(y - r),
      Math.round(2 * r * m),
      Math.round(2 * r * m),
    );
  }

  /** A thick line through logical points. */
  stroke(
    points: ReadonlyArray<readonly [number, number]>,
    width: number,
    color: string,
  ) {
    const m = this.m;
    const g = this.g;
    g.strokeStyle = color;
    g.lineWidth = width * m;
    g.beginPath();
    points.forEach(([x, y], n) => {
      const px = x * m + this.ox;
      const py = y * m + this.oy;
      if (n) g.lineTo(px, py);
      else g.moveTo(px, py);
    });
    g.stroke();
  }

  private setFont(size: number, weight: number) {
    this.g.font = `${weight} ${size * this.dpr}px ${this.font}`;
  }

  /** Text width in CSS px. */
  measure(value: string, size: number, weight = 600): number {
    const key = `${weight}|${size}|${value}`;
    let width = this.widths.get(key);
    if (width === undefined) {
      this.setFont(size, weight);
      width = this.g.measureText(value).width / this.dpr;
      if (this.widths.size > STAMP_LIMIT) this.widths.clear();
      this.widths.set(key, width);
    }
    return width;
  }

  /**
   * CSS-px text at a logical point, from a bitmap of it. The glyphs land as
   * `fillText` at the rounded point would put them.
   */
  text(
    value: string,
    x: number,
    y: number,
    size: number,
    color: string,
    weight = 400,
    align: CanvasTextAlign = "left",
  ) {
    const id = `text|${weight}|${size}|${color}|${align}|${value}`;
    let stamp = this.stamps.get(id);
    if (!stamp) {
      stamp = this.glyphs(value, size, weight, align, color);
      this.stamps.set(id, stamp);
    }
    if (stamp.canvas) {
      this.g.drawImage(
        stamp.canvas,
        this.dx(x) - stamp.x,
        this.dy(y) - stamp.y,
      );
    }
  }

  /**
   * Text on a clear bitmap, with its origin at the baseline where `align`
   * puts it.
   */
  private glyphs(
    value: string,
    size: number,
    weight: number,
    align: CanvasTextAlign,
    color: string,
  ): Stamp {
    const d = this.dpr;
    const width = Math.ceil(this.measure(value, size, weight) * d);
    const pad = Math.ceil(2 * d);
    const x =
      pad +
      (align === "center"
        ? Math.ceil(width / 2)
        : align === "right" || align === "end"
          ? width
          : 0);
    const y = Math.ceil(size * d * 1.1) + pad;
    const made = bitmap(width + 2 * pad, y + Math.ceil(size * d * 0.4));
    if (!made) return { canvas: null, x, y };
    // Canvas text takes its font smoothing from where the canvas sits in the
    // page, and a detached one draws heavier glyphs: draw it in the host.
    made.canvas.hidden = true;
    this.host?.append(made.canvas);
    const g = made.g;
    g.font = `${weight} ${size * d}px ${this.font}`;
    g.textAlign = align;
    g.fillStyle = color;
    g.fillText(value, x, y);
    made.canvas.remove();
    return { canvas: made.canvas, x, y };
  }

  /**
   * A pixel-framed label around a logical point, sized in CSS px and kept
   * inside the canvas.
   */
  chip(value: string, x: number, y: number, color: string, alpha = 1) {
    const d = this.dpr;
    const w = Math.round((this.measure(value, 11) + 14) * d);
    const h = 18 * d;
    const edge = 2 * d;
    const id = `chip|${color}|${value}`;
    let stamp = this.stamps.get(id);
    if (!stamp) {
      const made = bitmap(w + 2 * edge, h + 2 * edge);
      if (made) {
        const g = made.g;
        g.fillStyle = this.palette.chip;
        g.fillRect(edge, edge, w, h);
        g.fillStyle = rgba(color, 0.6);
        g.fillRect(edge, 0, w, edge);
        g.fillRect(edge, edge + h, w, edge);
        g.fillRect(0, edge, edge, h);
        g.fillRect(edge + w, edge, edge, h);
        const text = this.glyphs(value, 11, 600, "center", color);
        if (text.canvas) {
          // Where the rounded frame leaves the center of the text.
          g.drawImage(
            text.canvas,
            edge + Math.floor(w / 2) - text.x,
            Math.round(edge + Math.floor(h / 2) + 4 * d) - text.y,
          );
        }
      }
      stamp = { canvas: made?.canvas ?? null, x: edge, y: edge };
      this.stamps.set(id, stamp);
    }
    if (!stamp.canvas) return;
    const cx = clamp(
      this.dx(x),
      w / 2 + 3 * d,
      this.g.canvas.width - w / 2 - 3 * d,
    );
    const left = Math.round(cx - w / 2);
    const top = Math.round(this.dy(y) - h / 2);
    this.g.globalAlpha = alpha;
    this.g.drawImage(stamp.canvas, left - stamp.x, top - stamp.y);
    this.g.globalAlpha = 1;
  }
}

/** A four-point pixel star. */
export function pixStar(
  pen: Pen,
  x: number,
  y: number,
  s: number,
  color: string,
) {
  pen.rect(x - s / 2, y - s * 1.5, s, s * 3, color);
  pen.rect(x - s * 1.5, y - s / 2, s * 3, s, color);
}

/** Scattered dots on the sky, the same every time for a seed. */
export function sparkle(
  pen: Pen,
  rand: () => number,
  count: number,
  maxY: number,
) {
  const cells = Math.round((252 * pen.W) / DEMO_WIDTH);
  for (let n = 0; n < count; n += 1) {
    const size = rand() < 0.2 ? 4 : 2;
    const alpha = 0.1 + rand() * 0.28;
    const x = Math.floor(rand() * cells) * 4;
    const y = Math.floor((rand() * maxY) / 4) * 4;
    pen.rect(x, y, size, size, rgba(pen.palette.navy, alpha));
  }
}

export function seeded(seed: number): () => number {
  let a = seed | 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------- particles ----------

/** A palette color, a client's body color, or a fixed brand color. */
export type Ink = CrewColor | { client: ClientType } | { color: string };

export interface Pixel {
  x: number;
  y: number;
  vx: number;
  vy: number;
  t: number;
  life: number;
  ink: Ink;
  s: number;
}

export function inkColor(palette: CrewPalette, ink: Ink): string {
  if (typeof ink === "string") return palette[ink];
  return "color" in ink ? ink.color : tintOf(palette, ink.client).o;
}

/** A short-lived spray of pixels. */
export function spawn(
  list: Pixel[],
  rand: () => number,
  x: number,
  y: number,
  n: number,
  ink: Ink,
  spread = 60,
  up = 40,
  life = 0.6,
  s = 4,
) {
  for (let k = 0; k < n; k += 1) {
    list.push({
      x,
      y,
      vx: (rand() - 0.5) * spread,
      vy: -rand() * up - 8,
      t: 0,
      life: life * (0.6 + rand() * 0.6),
      ink,
      s,
    });
  }
}

export function stepPixels(list: Pixel[], dt: number, gravity = 60) {
  for (let k = list.length - 1; k >= 0; k -= 1) {
    const p = list[k];
    p.t += dt;
    p.x += p.vx * dt;
    p.y += p.vy * dt;
    p.vy += gravity * dt;
    if (p.t >= p.life) list.splice(k, 1);
  }
}

export function drawPixels(pen: Pen, list: readonly Pixel[]) {
  const byColor = new Map<string, number[][]>();
  for (const p of list) {
    // Fading in sixteenths draws pixels of a color in one fill.
    const alpha = Math.round(clamp(1 - p.t / p.life + 0.2) * 16) / 16;
    const color = rgba(inkColor(pen.palette, p.ink), alpha);
    let rects = byColor.get(color);
    if (!rects) byColor.set(color, (rects = []));
    rects.push([Math.round(p.x / 2) * 2, Math.round(p.y / 2) * 2, p.s, p.s]);
  }
  for (const [color, rects] of byColor) pen.rectsOf(color, rects);
}

// ---------- characters ----------

/** A client's body color and the shades its sprite and limbs use. */
export interface Tint {
  o: string;
  l: string;
  d: string;
  w: string;
  K: string;
  legs: string;
  arm: string;
}

export function clientColor(palette: CrewPalette, client: ClientType): string {
  const slot = clientSeriesSlot(client);
  return slot === null ? palette.unknown : palette[SERIES[slot]];
}

const tints = new WeakMap<CrewPalette, Map<ClientType, Tint>>();
export function tintOf(palette: CrewPalette, client: ClientType): Tint {
  let byClient = tints.get(palette);
  if (!byClient) {
    byClient = new Map();
    tints.set(palette, byClient);
  }
  let tint = byClient.get(client);
  if (!tint) {
    const o = clientColor(palette, client);
    tint = {
      o,
      l: mix(o, "#ffffff", 0.45),
      d: mix(o, palette.shade, 0.25),
      w: "#ffffff",
      K: palette.eye,
      legs: mix(o, palette.shade, 0.35),
      arm: mix(o, palette.shade, 0.3),
    };
    byClient.set(client, tint);
  }
  return tint;
}

/** What drawing needs of a character or a clone. */
export type Figure = CrewWalker & {
  client: ClientType;
  hat?: boolean;
  dx?: number;
  jump?: number;
  beat?: number;
  hit?: number;
};

export function figureOf(
  owner: CrewMember,
  clone?: CrewClone,
  hat = false,
): Figure {
  const figure = (clone ?? owner) as Figure;
  figure.client = owner.client;
  figure.hat = hat;
  return figure;
}

const widthOf = (figure: Figure) => spriteOf(figure.client).rows[0].length;

/** The sleeve color: a girl's own, a critter's from its tint. */
function armColor(palette: CrewPalette, client: ClientType): string {
  return spriteOf(client).arm ?? tintOf(palette, client).arm;
}

type Pose = "stand" | "sit" | "lie";

/** The body, on its feet (legs: -1 still, 0/1 walking), sitting, or lying. */
export function body(
  pen: Pen,
  ch: Figure,
  opt: { lift?: number; legs?: number; sit?: boolean; lie?: boolean } = {},
) {
  const pose: Pose = opt.lie ? "lie" : opt.sit ? "sit" : "stand";
  const legs =
    pose === "stand" && (opt.legs === 0 || opt.legs === 1) ? opt.legs : -1;
  const hat = pose !== "lie" && Boolean(ch.hat);
  const face = ch.face > 0 ? 1 : -1;
  const rows = spriteOf(ch.client).rows;
  const u = ch.u;
  const reach = (Math.max(rows[0].length, rows.length) / 2 + 5) * u;
  pen.stamp(
    `body|${ch.client}|${u}|${face}|${hat}|${pose}|${legs}`,
    ch.dx ?? ch.x,
    ch.fy - (opt.lift ?? 0),
    [-reach, -(rows.length + 5) * u, reach, u],
    (at) => paintBody(at, ch.client, u, face, hat, pose, legs),
  );
}

/** Draws a body with its feet at the origin, one rect per art pixel. */
function paintBody(
  pen: Pen,
  client: ClientType,
  u: number,
  face: number,
  hat: boolean,
  pose: Pose,
  legs: number,
) {
  const sprite = spriteOf(client);
  const tint = tintOf(pen.palette, client);
  const colors = sprite.pal ?? (tint as unknown as Record<string, string>);
  const w = sprite.rows[0].length;
  const h = sprite.rows.length;
  if (pose === "lie") {
    const ox = -(h * u) / 2;
    const oy = -w * u * 0.62;
    sprite.rows.forEach((row, r) => {
      for (let q = 0; q < row.length; q += 1) {
        const value = row[q];
        if (value === ".") continue;
        const yy = face > 0 ? q : w - 1 - q;
        pen.rect(
          ox + (h - 1 - r) * u,
          oy + yy * u * 0.62,
          u,
          Math.ceil(u * 0.62),
          colors[value],
        );
      }
    });
    return;
  }
  const sit = pose === "sit";
  if (sit) {
    const seat = pen.palette.wood;
    pen.rect(-3 * u, -u, 6 * u, u / 2 + 1, seat);
    pen.rect(-2.5 * u, -u, u / 2 + 1, u, seat);
    pen.rect(2 * u, -u, u / 2 + 1, u, seat);
  }
  // A girl stands on her own feet: sitting hides them, walking bobs.
  const crop = sit ? sprite.crop : 0;
  const rows = crop ? sprite.rows.slice(0, h - crop) : sprite.rows;
  const legRow = sit || sprite.feet ? 0 : 2;
  const bob = sprite.feet && legs === 1 ? u : 0;
  const ox = -(w * u) / 2;
  const oy = -(rows.length + legRow) * u - (sit ? u : 0) - bob;
  // Cells at 1.5 px land on whole device pixels only unrounded: the pen
  // snaps each edge, so neighbours meet without a seam.
  rows.forEach((row, r) => {
    for (let q = 0; q < row.length; q += 1) {
      const value = row[q];
      if (value === ".") continue;
      pen.rect(
        ox + (face > 0 ? q : w - 1 - q) * u,
        oy + r * u,
        u,
        u,
        colors[value],
      );
    }
  });
  if (hat) {
    const half = sprite.feet ? Math.round(w * 0.32) : 4;
    const top = Math.round(oy + (sprite.hatTop - 2) * u);
    pen.rect(-half * u, top, half * 2 * u, 2 * u, pen.palette.yellow);
    pen.rect(
      -(half + 1) * u,
      top + 2 * u,
      (half + 1) * 2 * u,
      u,
      pen.palette.yellow,
    );
  }
  if (!sit && !sprite.feet) {
    [-3 * u, 2 * u].forEach((leg, n) =>
      pen.rect(leg, -2 * u, u, legs === n ? u : 2 * u, tint.legs),
    );
  }
}

/**
 * A hand at (dx past the body edge, dy up from the feet). Poses are written
 * for an 11-row critter; taller sprites reach proportionally higher.
 */
export function handAt(ch: Figure, dx: number, dy: number): [number, number] {
  return [
    (ch.dx ?? ch.x) + ch.face * ((widthOf(ch) / 2) * ch.u + dx * ch.u),
    ch.fy - dy * spriteOf(ch.client).reach * ch.u,
  ];
}

/** Draws the arm out to a hand and returns where the hand is. */
export function arm(
  pen: Pen,
  ch: Figure,
  dx: number,
  dy: number,
): [number, number] {
  const sprite = spriteOf(ch.client);
  const [x, y] = handAt(ch, dx, dy);
  const u = ch.u;
  const root = (ch.dx ?? ch.x) + ch.face * ((widthOf(ch) / 2 - 1) * u);
  const color = armColor(pen.palette, ch.client);
  const lo = Math.min(root, x);
  const hi = Math.max(root, x);
  pen.rect(Math.round(lo), Math.round(y), Math.round(hi - lo + u), u, color);
  const ry = ch.fy - sprite.shoulder * u;
  pen.rect(
    Math.round(root),
    Math.round(Math.min(y, ry)),
    u,
    Math.round(Math.abs(y - ry) + u),
    color,
  );
  if (sprite.feet) pen.rect(Math.round(x), Math.round(y), u, u, SKIN);
  return [x, y];
}

/** What a character does at a station. */
export const Act = {
  type(pen: Pen, ch: Figure) {
    body(pen, ch, { sit: true });
    const k = Math.floor(ch.t * 5) % 2;
    arm(pen, ch, 2 + k, 3 + k * 0.5);
    arm(pen, ch, 4 - k, 3 + (1 - k) * 0.5);
  },
  hammer(
    pen: Pen,
    ch: Figure,
    fx: Pixel[],
    rand: () => number,
    onHit?: () => void,
  ) {
    body(pen, ch, { legs: -1 });
    const f = Math.floor(ch.t * 4) % 4;
    const pose = (
      [
        [1, 9],
        [2, 7.5],
        [3, 4.5],
        [2, 7],
      ] as const
    )[f];
    const [x, y] = arm(pen, ch, pose[0], pose[1]);
    const u = ch.u;
    const down = f === 2 ? 3 * u : 0;
    pen.rect(
      Math.round(x),
      Math.round(y - 3 * u + down),
      u,
      3 * u,
      pen.palette.wood,
    );
    pen.rect(
      Math.round(x - u),
      Math.round(y - 4 * u + down),
      3 * u,
      2 * u,
      pen.palette.steel,
    );
    if (f === 2 && ch.hit !== Math.floor(ch.t) && pen.dt > 0) {
      ch.hit = Math.floor(ch.t);
      spawn(fx, rand, x + ch.face * 2 * u, y + u, 7, "yellow", 90, 60, 0.45, 3);
      onHit?.();
    }
  },
  wrench(pen: Pen, ch: Figure, fx: Pixel[], rand: () => number) {
    body(pen, ch, { legs: -1 });
    const a = ch.t * 2.6;
    const [x, y] = arm(pen, ch, 2 + Math.cos(a) * 1.5, 6 + Math.sin(a) * 1.5);
    const u = ch.u;
    pen.rect(Math.round(x), Math.round(y - u), 3 * u, u, pen.palette.slate);
    pen.rect(
      Math.round(x + 2 * u),
      Math.round(y - 2 * u),
      u,
      3 * u,
      pen.palette.slate,
    );
    if (pen.chance(rand, 0.08))
      spawn(fx, rand, x + 3 * u, y, 3, "yellow", 80, 40, 0.4, 2);
  },
  read(pen: Pen, ch: Figure) {
    const nod = Math.floor(ch.t * 1.2) % 2;
    body(pen, ch, { sit: true, lift: nod });
    const [x, y] = arm(pen, ch, 2, 5);
    const u = ch.u;
    const flip = (ch.t * 0.7) % 1;
    const bx = Math.round(x + (ch.face > 0 ? 0 : -6 * u));
    const p = pen.palette;
    pen.stamp(`book|${u}`, bx, y, [0, -4 * u, 7 * u, 2 * u], (at) => {
      at.rect(0, -3 * u, 7 * u, 5 * u, p.blue);
      at.rect(u, -3 * u, 2.5 * u, 4 * u, p.paper);
      at.rect(3.5 * u, -3 * u, 2.5 * u, 4 * u, p.paper);
      for (let n = 0; n < 3; n += 1) {
        at.rect(1.5 * u, -2.5 * u + n * u, 1.5 * u, u / 2, p.bookLine);
        at.rect(4 * u, -2.5 * u + n * u, 1.5 * u, u / 2, p.bookLine);
      }
    });
    if (flip < 0.18) {
      const w = (flip < 0.09 ? flip / 0.09 : (0.18 - flip) / 0.09) * 2.5 * u;
      pen.rect(
        Math.round(bx + 3.5 * u),
        Math.round(y - 4 * u),
        w,
        5 * u,
        p.pageFlip,
      );
    }
  },
  blueprint(pen: Pen, ch: Figure) {
    body(pen, ch, { legs: -1 });
    const [x, y] = arm(pen, ch, 1, 6);
    const u = ch.u;
    const bx = x + (ch.face > 0 ? 0 : -11 * u);
    const wob = Math.floor(ch.t * 3) % 2;
    const dot = Math.floor(ch.t * 2) % 4;
    const p = pen.palette;
    pen.stamp(
      `blueprint|${u}|${wob}|${dot}`,
      bx,
      y,
      [0, -7 * u, 12 * u, 2 * u],
      (at) => {
        const ink = p.blueprintInk;
        at.rect(0, -6 * u - wob, 12 * u, 8 * u, p.blueprint);
        at.rect(2 * u, -4 * u - wob, 4 * u, u / 2, ink);
        at.rect(2 * u, -4 * u - wob, u / 2, 4 * u, ink);
        at.rect(2 * u, -wob, 8 * u, u / 2, ink);
        at.rect(9.5 * u, -3 * u - wob, u / 2, 3 * u, ink);
        at.rect(6 * u, -3 * u - wob, 4 * u, u / 2, ink);
        at.rect((3 + dot * 2) * u, -2 * u - wob, u, u, p.orange);
      },
    );
  },
  scope(pen: Pen, ch: Figure) {
    const tip = Math.floor(ch.t * 1.5) % 2;
    body(pen, ch, { legs: -1, lift: tip * 2 });
    arm(pen, ch, 0.5, 8 + tip * 0.5);
  },
  think(pen: Pen, ch: Figure, sit = true) {
    body(pen, ch, { sit, legs: -1 });
    arm(pen, ch, -0.5, 5 + (Math.floor(ch.t * 1.2) % 2) * 0.5);
  },
  scribble(pen: Pen, ch: Figure) {
    body(pen, ch, { legs: -1 });
    const sweep = Math.sin(ch.t * 2.4) * 2.5 + 4;
    const [x, y] = arm(pen, ch, sweep, 9 + Math.sin(ch.t * 6) * 1.2);
    pen.rect(
      Math.round(x),
      Math.round(y - ch.u),
      ch.u,
      2 * ch.u,
      pen.palette.red,
    );
  },
  penmark(pen: Pen, ch: Figure) {
    body(pen, ch, { sit: true });
    const sweep = Math.sin(ch.t * 2.2) * 2 + 3;
    const [x, y] = arm(pen, ch, sweep, 4 + Math.sin(ch.t * 7) * 0.5);
    pen.rect(
      Math.round(x),
      Math.round(y - 2 * ch.u),
      ch.u,
      3 * ch.u,
      pen.palette.red,
    );
  },
  coffee(pen: Pen, ch: Figure, fx: Pixel[], rand: () => number) {
    body(pen, ch, { sit: true });
    const ph = (ch.t % 3.2) / 3.2;
    const up = ph > 0.65;
    const [x, y] = arm(pen, ch, up ? -1 : 1.5, up ? 7 : 4);
    const u = ch.u;
    pen.rect(
      Math.round(x),
      Math.round(y - 2 * u),
      3 * u,
      3 * u,
      pen.palette.paper,
    );
    pen.rect(
      Math.round(x + u / 2),
      Math.round(y - 2 * u),
      2 * u,
      u,
      pen.palette.wood,
    );
    if (!up && pen.chance(rand, 0.05)) {
      fx.push({
        x: x + u,
        y: y - 3 * u,
        vx: 0,
        vy: -16,
        t: 0,
        life: 1,
        ink: "steam",
        s: 3,
      });
    }
  },
  sleep(pen: Pen, ch: Figure) {
    body(pen, ch, { lie: true, lift: Math.floor(ch.t * 1.1) % 2 });
  },
  celebrate(pen: Pen, ch: Figure) {
    const lift = Math.sin((ch.jump ?? 0) * Math.PI) * 24;
    body(pen, ch, { lift, legs: -1 });
    const [ax, ay] = handAt(ch, 0, 9 + lift / ch.u);
    const bx = (ch.dx ?? ch.x) - ch.face * (widthOf(ch) / 2) * ch.u;
    const color = armColor(pen.palette, ch.client);
    pen.rect(Math.round(ax), Math.round(ay), ch.u, 3 * ch.u, color);
    pen.rect(Math.round(bx), Math.round(ay), ch.u, 3 * ch.u, color);
  },
  slip(pen: Pen, ch: Figure) {
    body(pen, ch, { lie: true });
    const a = pen.T * 7;
    const cy = ch.fy - 36;
    for (let n = 0; n < 3; n += 1) {
      pixStar(
        pen,
        (ch.dx ?? ch.x) + Math.cos(a + n * 2.1) * 16,
        cy + Math.sin(a + n * 2.1) * 5,
        2,
        pen.palette.yellow,
      );
    }
  },
};

/** The cloud over a thinking head: a turning gear, a lit bulb when close. */
function thought(pen: Pen, ch: CrewMember) {
  const u = 3;
  const face = ch.face > 0 ? 1 : -1;
  const close = ch.queue.length > 0;
  const beat = close ? Math.floor(ch.t * 8) % 2 : Math.floor(ch.t * 4) % 2;
  pen.stamp(
    `thought|${ch.client}|${face}|${close}|${beat}`,
    ch.x + ch.face * 18,
    crownOf(ch, ch.client) - 32,
    [-9 * u, -6 * u, 9 * u, 12 * u],
    (at) => {
      const p = at.palette;
      at.rect(-5 * u, -4 * u, 10 * u, 8 * u, p.cloud);
      at.rect(-6 * u, -3 * u, 12 * u, 6 * u, p.cloud);
      at.rect(-5 * u, -5 * u, 10 * u, u, p.line);
      at.rect(-5 * u, 4 * u, 10 * u, u, p.line);
      at.rect(-7 * u, -3 * u, u, 6 * u, p.line);
      at.rect(6 * u, -3 * u, u, 6 * u, p.line);
      at.rect(-face * 6 * u, 7 * u, 2 * u, 2 * u, p.cloud);
      at.rect(-face * 8 * u, 10 * u, u, u, p.line);
      const color = tintOf(p, ch.client).o;
      if (!close) {
        at.rect(-2 * u, -2 * u, 4 * u, 4 * u, color);
        at.rect(-u, -u, 2 * u, 2 * u, p.cloud);
        const teeth = beat
          ? [
              [-1, -3],
              [-1, 2],
              [-3, -1],
              [2, -1],
            ]
          : [
              [-3, -3],
              [2, -3],
              [-3, 2],
              [2, 2],
            ];
        for (const [a, b] of teeth) at.rect(a * u, b * u, 2 * u, 2 * u, color);
      } else {
        // Something else is queued: the thought is nearly done.
        const lit = beat ? p.yellow : p.yellowLight;
        at.rect(-2 * u, -3 * u, 4 * u, 4 * u, lit);
        at.rect(-u, u, 2 * u, u, lit);
        at.rect(-u, 2 * u, 2 * u, u, p.muted);
      }
    },
  );
}

function snore(pen: Pen, ch: CrewMember) {
  for (let n = 0; n < 3; n += 1) {
    const ph = (pen.T * 0.45 + n / 3) % 1;
    pen.alpha(1 - ph);
    pen.text(
      "Z",
      ch.x + 18 + ph * 16,
      ch.fy - 34 - ph * 26,
      Math.round(10 + ph * 6),
      pen.palette.muted,
      800,
    );
  }
  pen.alpha(1);
}

// ---------- model orbs ----------

/**
 * The model rides along as a little winged orb wearing its vendor's mark: the
 * client is the body, the model the spirit. Vendors without a color here
 * glow in their character's color.
 */
const MARK_COLOR: Partial<Record<Vendor, string>> = {
  claude: "#d97757",
  openai: "#10a37f",
  kimi: "#1783ff",
  deepseek: "#4d6bfe",
  zhipu: "#3b6cf6",
  gemini: "#8e75ff",
};
/** Models without a known vendor wear the Brain mark, as elsewhere. */
export const BRAIN_MARK = "brain";
const ORB = [
  "..KKKKK..",
  ".KWWWWWK.",
  "KWhWWWWWK",
  "KWWWWWWWK",
  "KWWWWWWWK",
  "KWWWWWWWK",
  "KWWWWWWWK",
  ".KWWWWWK.",
  "..KKKKK..",
] as const;
/** Wings up and down; the right one mirrors the left. */
const WING = [
  ["..KK.", ".KWWK", "KWWK.", ".KK.."],
  [".KK..", "KWWK.", ".KWWK", "..KK."],
] as const;
const orbInk = (p: CrewPalette, cell: string) =>
  cell === "K" ? p.slate : cell === "h" ? p.shine : p.cloud;

const vendors = new Map<string, string>();
/** The mark a model wears: its vendor's, or the Brain's. */
export function markOf(model: string): string {
  let mark = vendors.get(model);
  if (mark === undefined) {
    mark = modelVendor(model) ?? BRAIN_MARK;
    if (vendors.size > 256) vendors.clear();
    vendors.set(model, mark);
  }
  return mark;
}

const orbInks = new Map<string, Ink>();
/** The glow and sparks of an orb, made once per color. */
function orbInkOf(mark: string, client: ClientType): Ink {
  const color = MARK_COLOR[mark as Vendor];
  const key = color ?? client;
  let ink = orbInks.get(key);
  if (!ink) {
    ink = color ? { color } : { client };
    orbInks.set(key, ink);
  }
  return ink;
}

/**
 * Moves a character's orb toward where its act wants it: following while it
 * moves, circling while it thinks, beside the face while it writes, resting
 * while tools run, dozing while it waits. A new model bursts the old orb and
 * flies the new one in from above. `snap` puts it there at once.
 */
export function stepOrb(
  member: CrewMember,
  T: number,
  dt: number,
  fx: Pixel[],
  rand: () => number,
  snap = false,
) {
  if (!member.model) {
    member.orb = null;
    return;
  }
  const top = crownOf(member, member.client);
  const side = (widthOf(member as Figure) * member.u) / 2 + 8;
  let o = member.orb;
  if (!o) {
    o = member.orb = {
      x: member.x - member.face * side,
      y: top + 6,
      in: 1,
      spark: 0,
      model: member.model,
      flap: true,
      glow: false,
      dim: false,
    };
  }
  if (o.model !== member.model) {
    if (!snap) {
      spawn(
        fx,
        rand,
        o.x,
        o.y,
        14,
        orbInkOf(markOf(o.model), member.client),
        90,
        50,
        0.8,
        2,
      );
      o.y -= 50;
      o.in = 0;
    }
    o.model = member.model;
  }
  o.in = snap ? 1 : Math.min(1, o.in + dt * 1.5);
  const act =
    !member.arrived || member.climb
      ? "move"
      : member.slip > 0
        ? "slip"
        : member.jump > 0
          ? "jump"
          : member.act;
  let tx = member.x - member.face * side;
  let ty = top + 8;
  let ease = 5;
  o.flap = true;
  o.glow = false;
  o.dim = false;
  if (act === "move") ty = top + 4 + Math.sin(T * 5) * 2;
  else if (act === "think") {
    const a = T * 2.6 + member.order;
    tx = member.x + Math.cos(a) * (side + 2);
    ty = top + 4 + Math.sin(a) * 6;
    ease = 14;
  } else if (act === "write") {
    tx = member.x + member.face * side;
    ty = top + 2 + Math.sin(T * 6) * 2;
    o.glow = true;
    o.spark += (dt * (member.rate || 40)) / 22;
    const ink = orbInkOf(markOf(o.model), member.client);
    while (o.spark >= 1) {
      o.spark -= 1;
      spawn(fx, rand, o.x + (rand() - 0.5) * 10, o.y, 1, ink, 50, 30, 0.7, 2);
    }
  } else if (act === "slip") {
    const a = T * 10;
    tx = member.x + Math.cos(a) * 16;
    ty = top + Math.sin(a) * 8;
    ease = 18;
  } else if (act === "jump") ty = top - 10 - Math.abs(Math.sin(T * 9)) * 8;
  else if (act === "wait") {
    ty = top + 16;
    o.flap = false;
    o.dim = true;
  } else {
    ty = top + 10;
    o.flap = false;
  }
  if (snap) {
    o.x = tx;
    o.y = ty;
    return;
  }
  o.x += (tx - o.x) * clamp(dt * ease);
  o.y += (ty - o.y) * clamp(dt * ease);
  // Long walks peak at several hundred px/s; the orb never trails far behind.
  const lag = act === "move" ? 30 : 80;
  o.x = clamp(o.x, tx - lag, tx + lag);
  o.y = clamp(o.y, ty - lag, ty + lag);
}

/** Vendor marks rasterised once, by mark; a missing one draws no orb yet. */
export type CrewMarks = ReadonlyMap<string, CanvasImageSource>;

function drawOrb(pen: Pen, member: CrewMember, marks: CrewMarks) {
  const o = member.orb;
  if (!o || member.alpha < 0.05) return;
  const markName = markOf(o.model);
  const mark = marks.get(markName);
  if (!mark) return;
  const p = pen.palette;
  const x = Math.round(o.x);
  const y = Math.round(o.y);
  pen.alpha(member.alpha * o.in * (o.dim ? 0.55 : 1));
  if (o.glow) {
    const color = inkColor(p, orbInkOf(markName, member.client));
    pen.disc(x, y, 14, rgba(color, 0.16 + 0.08 * Math.sin(pen.T * 7)));
  }
  const f = o.flap ? Math.floor(pen.T * 9) % 2 : 1;
  pen.stamp(`wing|${f}`, x - 19, y - (f ? 4 : 8), [0, 0, 38, 8], (at) =>
    WING[f].forEach((row, wy) => {
      for (let wx = 0; wx < row.length; wx += 1) {
        if (row[wx] === ".") continue;
        const color = orbInk(at.palette, row[wx]);
        at.rect(wx * 2, wy * 2, 2, 2, color);
        at.rect(36 - wx * 2, wy * 2, 2, 2, color);
      }
    }),
  );
  pen.stamp(`orb|${markName}`, x - 9, y - 9, [0, 0, 18, 18], (at) => {
    ORB.forEach((row, oy) => {
      for (let ox = 0; ox < row.length; ox += 1) {
        if (row[ox] !== ".")
          at.rect(ox * 2, oy * 2, 2, 2, orbInk(at.palette, row[ox]));
      }
    });
    at.image(mark, 3, 3, 12, 12);
  });
  pen.alpha(1);
}

/** Words the crew shows; the shell passes them in the user's language. */
export interface CrewWords {
  failover: string;
  name(client: ClientType): string;
}

/** How a scene draws a character at its station, and its hats. */
export interface Stage {
  hat: boolean;
  perform(pen: Pen, member: CrewMember): void;
  fx: Pixel[];
}

const CONFETTI: readonly CrewColor[] = [
  "blue",
  "orange",
  "green",
  "yellow",
  "pink",
  "violet",
];

/** Draws the crew over a scene: characters, clones, bubbles and names. */
export function drawCrew(
  pen: Pen,
  crew: Crew,
  stage: Stage,
  words: CrewWords,
  marks: CrewMarks,
) {
  const p = pen.palette;
  const sorted = [...crew.members].sort((a, b) => a.fy - b.fy || a.x - b.x);
  const tags: Array<{ s: string; x: number; y: number; a: number }> = [];
  const bubbles: Array<{ s: string; x: number; y: number; color: string }> = [];
  for (const member of sorted) {
    if (member.x < -60 || member.x > pen.W + 60) continue;
    const ch = figureOf(member, undefined, stage.hat);
    const tint = tintOf(p, member.client);
    const above = crownOf(member, member.client) - 16;
    for (const clone of member.clones) {
      const small = figureOf(member, clone, stage.hat);
      if (clone.phase === "work" && clone.act === "bash") Act.type(pen, small);
      else if (clone.phase === "work" && clone.act === "read")
        Act.read(pen, small);
      else if (clone.phase === "work") Act.scope(pen, small);
      else {
        body(pen, small, {
          legs:
            clone.act === "wait" && clone.phase === "go"
              ? -1
              : Math.floor(clone.walk) % 2,
        });
        if (clone.phase === "back") {
          pen.rect(
            Math.round(clone.x - 5),
            Math.round(clone.fy - 34),
            10,
            8,
            p.box,
          );
          pen.rect(
            Math.round(clone.x - 5),
            Math.round(clone.fy - 31),
            10,
            2,
            p.wood,
          );
        }
      }
    }
    if (member.slip > 0) Act.slip(pen, ch);
    else if (member.jump > 0) Act.celebrate(pen, ch);
    else if (member.climb) {
      body(pen, ch, { legs: Math.floor(member.walk) % 2 });
      const rung = Math.floor(member.walk) % 2 ? -1 : 1;
      const sleeve = armColor(p, member.client);
      pen.rect(
        Math.round(member.x - 28),
        Math.round(member.fy - 30 + rung * 3),
        8,
        4,
        sleeve,
      );
      pen.rect(
        Math.round(member.x + 20),
        Math.round(member.fy - 30 - rung * 3),
        8,
        4,
        sleeve,
      );
    } else if (!member.arrived)
      body(pen, ch, { legs: Math.floor(member.walk) % 2 });
    else stage.perform(pen, member);
    drawOrb(pen, member, marks);
    const working =
      !member.leaving && member.arrived && member.slip <= 0 && member.jump <= 0;
    if (working && member.act === "think") thought(pen, member);
    if (working && member.act === "wait") snore(pen, member);
    if (working && member.act === "write" && Math.round(member.rate) > 0) {
      bubbles.push({
        s: `${Math.round(member.rate)} tok/s`,
        x: member.x,
        y: above,
        color: tint.o,
      });
    } else if (working && member.tool) {
      bubbles.push({
        s: member.count > 1 ? `${member.tool} ×${member.count}` : member.tool,
        x: member.x,
        y: above,
        color: tint.o,
      });
    }
    if (member.slip > 0) {
      bubbles.push({
        s: words.failover,
        x: member.x,
        y: above,
        color: p.failover,
      });
    }
    if (!member.climb) {
      tags.push({
        s: words.name(member.client),
        x: clamp(member.x, 44, pen.W - 54),
        y: member.fy + 15,
        a: member.alpha,
      });
    }
  }
  // Bubbles and names that would touch step up or down a row.
  const stack = (
    list: Array<{ s: string; x: number; y: number }>,
    size: number,
    dy: number,
  ) => {
    const placed: Array<{ x: number; y: number; w: number }> = [];
    list
      .sort((a, b) => a.x - b.x)
      .forEach((item) => {
        const w = (pen.measure(item.s, size) + 16) / (pen.m / pen.dpr);
        let y = item.y;
        while (
          placed.some(
            (other) =>
              other.y === y && Math.abs(other.x - item.x) < (other.w + w) / 2,
          )
        ) {
          y += dy;
        }
        placed.push({ x: item.x, y, w });
        item.y = y;
      });
  };
  const k = pen.m / pen.dpr;
  stack(bubbles, 11, -24 / k);
  stack(tags, 11, 14 / k);
  for (const bubble of bubbles)
    pen.chip(bubble.s, bubble.x, bubble.y, bubble.color);
  for (const tag of tags) {
    pen.alpha(tag.a);
    pen.text(tag.s, tag.x, tag.y, 11, p.ink, 600, "center");
    pen.alpha(1);
  }
  drawPixels(pen, stage.fx);
  for (const item of crew.pops) {
    pixStar(
      pen,
      item.x,
      item.y - (1 - (1 - item.t / 1.4) ** 3) * 30,
      2 + (Math.floor(item.t * 4) % 2),
      tintOf(p, item.client).o,
    );
  }
  CONFETTI.forEach((color, n) =>
    pen.rectsOf(
      p[color],
      crew.confetti
        .filter((piece) => piece.color % CONFETTI.length === n)
        .map((piece) => [
          Math.round(piece.x / 4) * 4,
          Math.round(piece.y / 4) * 4,
          4,
          4,
        ]),
    ),
  );
  if (crew.banner > 0 && crew.bannerText) {
    pen.chip(
      crew.bannerText,
      pen.W / 2,
      22,
      p.navy,
      Math.min(1, crew.banner * 3),
    );
  }
  if (crew.overflow > 0) {
    pen.chip(`+${crew.overflow}`, 24, pen.H - 24, p.muted);
  }
}
