// Shared by the desktop pet's window and its settings picture: one paced
// crew both read, and a canvas that draws the orbs with the overview's crew
// renderer.
import {
  drawCrew,
  Pen,
  readCrewPalette,
  spawn,
  stepPixels,
  type CrewPalette,
  type CrewWords,
  type Pixel,
  type Stage,
} from "../live-crew-draw";
import {
  createCrew,
  stepCrew,
  syncCrew,
  type Crew,
  type CrewLayout,
  type CrewMember,
  type CrewReading,
} from "../live-crew-model";
import { motion } from "./toy-config";

export const DPR = () => window.devicePixelRatio || 1;
export const FONT = '"IBM Plex Sans Variable", "PingFang SC", sans-serif';
export const clamp = (v: number, lo = 0, hi = 1) =>
  Math.min(hi, Math.max(lo, v));

/** Waving after its reply, for as long as the level says, before it dozes off. */
export const waving = (m: CrewMember) =>
  m.act === "wait" && m.arrived && !m.leaving && m.t < motion().wave;

/**
 * The engine's pacing (minimum holds, ×N merges, backlog drops) without its
 * walking: every station is the same spot, so arrival is instant and the pet
 * places the orbs herself.
 */
export const pace = createCrew();
pace.width = 1e6;
const HERE: CrewLayout = {
  floors: [0],
  door: 100,
  exit: 100,
  spot: () => ({ x: 100, level: 0 }),
};

export function syncPace(reading: CrewReading) {
  syncCrew(pace, reading, HERE);
}

export function stepPace(dt: number) {
  for (let left = dt; left > 1e-6; left -= 1 / 30)
    stepCrew(pace, HERE, Math.min(1 / 30, left));
}

/** Members still in their conversation, oldest first. */
export const live = () =>
  pace.members.filter((m) => !m.leaving).sort((a, b) => a.order - b.order);

/** Pixels added since `from`, thinned out or doubled up to the level's share. */
export function scaleFx(fx: Pixel[], from: number, rand: () => number) {
  const k = motion().fx;
  if (k === 1 || fx.length <= from) return;
  const added = fx.splice(from);
  for (const p of added) {
    const n = Math.floor(k) + (rand() < k % 1 ? 1 : 0);
    for (let i = 0; i < n; i += 1)
      fx.push(
        i
          ? {
              ...p,
              vx: p.vx * (0.6 + rand() * 0.8) + (rand() - 0.5) * 40,
              vy: p.vy * (0.6 + rand() * 0.8),
            }
          : p,
      );
  }
}

/** The engine's spray, as many as the level allows. */
export function puff(...args: Parameters<typeof spawn>) {
  const [fx, rand] = args;
  const from = fx.length;
  spawn(...args);
  scaleFx(fx, from, rand);
}

/** Vendor marks as SVG text, by mark; each board rasterises them in its ink. */
export type MarkSvgs = ReadonlyMap<string, string>;

async function loadMarks(svgs: MarkSvgs, ink: string) {
  const map = new Map<string, HTMLImageElement>();
  await Promise.all(
    [...svgs].map(async ([key, svg]) => {
      const img = new Image();
      img.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(
        svg.replaceAll("currentColor", ink),
      )}`;
      await img.decode().catch(() => undefined);
      map.set(key, img);
    }),
  );
  return map;
}

/** A canvas with a pen in its own px (`m` device px each). */
export class Board {
  g: CanvasRenderingContext2D;
  pen!: Pen;
  palette!: CrewPalette;
  marks = new Map<string, HTMLImageElement>();
  fx: Pixel[] = [];

  constructor(
    public canvas: HTMLCanvasElement,
    public W: number,
    public H: number,
    public m = DPR(),
  ) {
    this.g = canvas.getContext("2d")!;
    this.size();
  }

  private size() {
    const { canvas, W, H, m } = this;
    canvas.width = Math.round(W * m);
    canvas.height = Math.round(H * m);
    canvas.style.width = `${(W * m) / DPR()}px`;
    canvas.style.height = `${(H * m) / DPR()}px`;
  }

  private makePen() {
    this.pen = new Pen(this.g, {
      m: this.m,
      dpr: Math.min(DPR(), this.m),
      W: this.W,
      H: this.H,
      font: FONT,
      palette: this.palette,
      host: this.canvas.parentElement,
    });
  }

  async init(svgs: MarkSvgs) {
    this.palette = readCrewPalette(this.canvas);
    this.marks = await loadMarks(svgs, this.palette.ink);
    this.makePen();
  }

  /**
   * The window moved to a screen of another size or scale: the canvas and
   * the pen follow; the marks and the sparks stay.
   */
  resize(W: number, H: number, m = DPR()) {
    if (W === this.W && H === this.H && m === this.m) return;
    Object.assign(this, { W, H, m });
    this.size();
    if (this.palette) this.makePen();
  }

  clear(T: number, dt: number) {
    this.g.clearRect(0, 0, this.canvas.width, this.canvas.height);
    this.pen.T = T;
    this.pen.dt = dt;
    stepPixels(this.fx, dt);
  }

  /** Draws views with the app's crew renderer: orbs, bubbles, sparks. */
  draw(
    views: CrewMember[],
    perform: Stage["perform"],
    words: CrewWords,
    fx = this.fx,
  ) {
    const crew = {
      ...createCrew(),
      members: views,
      time: pace.time,
      width: this.W,
      height: this.H,
    } as Crew;
    drawCrew(this.pen, crew, { hat: false, fx, perform }, words, this.marks);
  }
}

/** Runs `frame` at most `fps` times a second until the returned stop is called. */
export function runLoop(frame: (dt: number) => void, fps = 30): () => void {
  let last = performance.now();
  let id = 0;
  let stopped = false;
  const tick = (now: number) => {
    if (stopped) return;
    id = requestAnimationFrame(tick);
    const elapsed = now - last;
    if (elapsed < 1000 / fps - 2) return;
    last = now;
    frame(Math.min(0.05, elapsed / 1000));
  };
  id = requestAnimationFrame(tick);
  return () => {
    stopped = true;
    cancelAnimationFrame(id);
  };
}
