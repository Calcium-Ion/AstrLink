// Draws the big pet: her pose for what she is doing, floating with a slow
// bob, what she has earned to wear, and the orbs beside her. The canvases
// cover only a box around her and follow her over the screen, so a quiet pet
// costs little to draw.
import { drawPixels, pixStar, tintOf, type Pen } from "../live-crew-draw";
import type { CrewMember } from "../live-crew-model";
import { ORB_SCALE, type PetBrain } from "./pet-brain";
import { badgesOf, gearRise } from "./pet-gear";
import {
  bobOf,
  build,
  kitOf,
  kitPx,
  onScreen,
  paintKit,
  type Face,
  type Kit,
} from "./pet-kit";
import { cfg, motion } from "./toy-config";
import { DPR, pace, waving, type Board } from "./toy-kit";
import { HIDDEN } from "./toy-words";

/** How much of the screen the canvases cover around her, in points. */
export const BOX = { w: 640, h: 600 } as const;
/** Room in the box for her from her soles up, at the largest size. */
const TALL = 178;
/** Room over her head in the box: hearts, confetti, a snore. */
const HEADROOM = 300;

const HEART = [
  ".XX.XX.",
  "XXXXXXX",
  "XXXXXXX",
  ".XXXXX.",
  "..XXX..",
  "...X...",
];
const DROP = ["..B..", ".BBB.", "BBWBB", "BBBBB", ".BBB."];
const VEIN = [".R.R.", "RR.RR", ".....", "RR.RR", ".R.R."];

/** The box's top-left in screen points for her feet at (x, foot). */
export function boxAt(x: number, foot: number) {
  return {
    x: Math.round(x - BOX.w / 2),
    y: Math.round(foot - TALL - HEADROOM),
  };
}

/** Shrinks a board's canvas to the box; call before `init`, which makes its pen. */
export function fitBox(b: Board) {
  b.canvas.width = Math.round(BOX.w * DPR());
  b.canvas.height = Math.round(BOX.h * DPR());
  b.canvas.style.width = `${BOX.w}px`;
  b.canvas.style.height = `${BOX.h}px`;
}

/** Clears a board and points its pen at the box at (x, y), in screen points. */
function begin(b: Board, box: { x: number; y: number }, dt: number) {
  b.g.setTransform(1, 0, 0, 1, 0, 0);
  b.clear(pace.time, dt);
  const k = DPR();
  b.g.setTransform(1, 0, 0, 1, -Math.round(box.x * k), -Math.round(box.y * k));
}

function pixels(
  pen: Pen,
  rows: string[],
  x: number,
  y: number,
  u: number,
  colors: Record<string, string>,
) {
  rows.forEach((row, r) => {
    for (let q = 0; q < row.length; q += 1)
      if (row[q] !== ".") pen.rect(x + q * u, y + r * u, u, u, colors[row[q]]);
  });
}

/** A thought cloud with dots lighting up in turn. */
function cloud(pen: Pen, x: number, y: number, t: number, face: number) {
  const u = 4;
  const p = pen.palette;
  pen.rect(x - 6 * u, y - 4 * u, 12 * u, 8 * u, p.cloud);
  pen.rect(x - 7 * u, y - 3 * u, 14 * u, 6 * u, p.cloud);
  pen.rect(x - 6 * u, y - 5 * u, 12 * u, u, p.line);
  pen.rect(x - 6 * u, y + 4 * u, 12 * u, u, p.line);
  pen.rect(x - 8 * u, y - 3 * u, u, 6 * u, p.line);
  pen.rect(x + 7 * u, y - 3 * u, u, 6 * u, p.line);
  for (const [cx, cy] of [
    [-7, -4],
    [6, -4],
    [-7, 3],
    [6, 3],
  ])
    pen.rect(x + cx * u, y + cy * u, u, u, p.line);
  const lit = Math.floor(t * 3) % 4;
  for (let n = 0; n < 3; n += 1)
    pen.rect(
      x + (n - 1) * 3 * u - u,
      y - u,
      2 * u,
      2 * u,
      n < lit ? p.ink : p.line,
    );
  pen.rect(x - face * 7 * u, y + 6 * u, 2 * u, 2 * u, p.cloud);
  pen.rect(x - face * 9 * u, y + 9 * u, u, u, p.line);
}

/** A cup now and then while she waits. */
const coffee = (t: number) => Math.floor(t / 9) % 3 === 1;

/** What she is doing, as the clip of her drawn picture to play. */
export function actOf(brain: PetBrain, act: string | undefined): string {
  if (brain.mode === "held" || brain.mode === "fly") return "held";
  if (brain.stretch > 0) return "stretch";
  if (brain.hop > 0 && brain.hopBig) return "glad";
  // Patted, she blushes where she is if she has the face for it.
  if (brain.pat > 0 && !kitOf(brain.client)?.faces.shy) return "glad";
  if (brain.wave > 0) return "greet";
  if (brain.sleeping) return "doze";
  if (act === "write" || act === "bash") return "type";
  if (act === "read") return "read";
  if (act === "edit") return "sketch";
  if (act === "search") return "lens";
  if (act === "think") return "think";
  if (act === "wait" && !brain.busyFace() && coffee(brain.t)) return "coffee";
  return "idle";
}

/** Her face on her drawn picture this frame; null keeps the one drawn. */
export function moodOf(brain: PetBrain): Face | null {
  if (brain.dizzy > 0 || brain.mode === "held" || brain.mode === "fly")
    return "wince";
  if (brain.pout > 0) return "pout";
  if (brain.bang > 0 || brain.sweat > 0) return "wow";
  if (brain.sleeping) return "blink";
  if (brain.pat > 0) return "shy";
  if (brain.wave > 0) return "happy";
  if (brain.whim) return brain.whim.face;
  if (brain.blink > 0) return "blink";
  // The mouse over her, she smiles.
  const p = brain.pointer;
  if (p && brain.floating() && brain.hitBody(p.x, p.y)) return "happy";
  return null;
}

/**
 * Which way she faces this frame. Her drawn picture faces you and never
 * turns; this is the side her hand, a thought or a sulk goes to.
 */
export function faceOf(brain: PetBrain, act: string | undefined): 1 | -1 {
  const kit = kitOf(brain.client);
  // Greeting, the orb goes up beside the hand she waves.
  if (brain.wave > 0 && kit?.hand) return kit.hand.x < kit.anchor.x ? -1 : 1;
  let face = brain.face;
  const p = brain.pointer;
  // Pouting, she turns away from the mouse; idle, toward it.
  if (brain.pout > 0 && p) face = p.x > brain.x ? -1 : 1;
  else if (p && !act && brain.floating()) face = p.x > brain.x ? 1 : -1;
  // Greeting, she turns to the orb she holds up, so it never crosses her face.
  if (brain.wave > 0 && cfg.prefs.orb)
    for (const o of brain.orbs.values())
      if (o.hold > 0) {
        face = o.x > brain.x ? 1 : -1;
        break;
      }
  return face;
}

/** The last frame drawn, so a frame that would look the same is skipped. */
export interface PaintCache {
  sig: string;
  painted: Painted | null;
}

/** What the chip over her head says for the busiest conversation. */
function statusOf(
  brain: PetBrain,
  act: string | undefined,
  lead: CrewMember | null,
): string {
  if (!cfg.prefs.status || !act || act === "wait" || act === "think" || !lead)
    return "";
  if (act === "write") {
    const rate = Math.round(
      brain
        .members()
        .filter((m) => m.act === "write" && !waving(m))
        .reduce((s, m) => s + m.rate, 0),
    );
    return rate > 0 ? `${rate} tok/s` : "";
  }
  return lead.count > 1 ? `${lead.tool} ×${lead.count}` : lead.tool;
}

/** The orbs as the crew renderer draws them: working ones glow, waiting ones dim. */
function orbViews(brain: PetBrain): CrewMember[] {
  const views: CrewMember[] = [];
  if (!cfg.prefs.orb) return views;
  for (const m of brain.members()) {
    const o = brain.orbs.get(m.id);
    if (!o) continue;
    const working = m.act !== "wait" && !waving(m);
    views.push({
      ...m,
      x: o.x / ORB_SCALE,
      fy: o.y / ORB_SCALE + 30,
      u: 1,
      clones: [],
      leaving: true,
      arrived: true,
      slip: 0,
      jump: 0,
      climb: false,
      // She has no door to queue at: every orb shows at once.
      waiting: false,
      alpha: 1,
      orb: {
        x: o.x / ORB_SCALE,
        y: o.y / ORB_SCALE,
        in: o.in,
        model: o.model,
        glow: working,
        dim: !working && o.hold <= 0,
      },
    } as CrewMember);
  }
  return views;
}

/** Her body for this frame: how to draw it and what it looks like. */
interface Figure {
  /** Px her feet are lifted off her foot line. */
  lift: number;
  /** Everything that changes how she looks, for skipping repeated frames. */
  sig: unknown;
  /** Draws her with her feet at (x, foot) and says where her near hand is. */
  draw: (pen: Pen, x: number, foot: number) => readonly [number, number];
}

/** Her drawn picture, laid out for what she is doing. */
function drawnFigure(
  brain: PetBrain,
  kit: Kit,
  act: string | undefined,
  swing: number,
  lift: number,
): Figure {
  const clip = actOf(brain, act);
  const built = build(kit, {
    clip,
    face: moodOf(brain),
    pace: clip === "idle" ? swing : 0,
    t: brain.t,
    worn: badgesOf(brain.stage()),
  });
  return {
    lift,
    sig: built.key,
    draw: (pen, x, foot) => {
      paintKit(pen, kit, built, x, foot);
      const hand = kit.hand ?? { x: kit.anchor.x, y: kit.crown + 40 };
      const p = onScreen(kit, hand, x, foot);
      return [p.x, p.y];
    },
  };
}

export interface Painted {
  /** The box the canvases cover, in screen points. */
  box: { x: number; y: number };
  /** The top of her head. */
  top: number;
  /** Her soles, as drawn this frame. */
  bottom: number;
  /** Where the status chip over her head sits. */
  chipY: number;
}

/** Draws her and the orbs for this frame. */
export function paint(
  brain: PetBrain,
  b: Board,
  ob: Board,
  dt: number,
  cache: PaintCache = { sig: "", painted: null },
): Painted {
  const lead = brain.lead();
  // Held up or flying, or busy with you, her work waits.
  const act =
    brain.busyFace() || brain.mode === "held" || brain.mode === "fly"
      ? undefined
      : lead?.act;
  const foot = brain.feet();
  const box = boxAt(brain.x, foot);
  const pen = b.pen;
  const p = pen.palette;
  // Floating, she rises and falls a pixel or two; a hop goes on top of it.
  const bob = brain.floating() ? bobOf(brain.t) * kitPx() : 0;
  const hopLift =
    bob +
    (brain.hop > 0
      ? Math.sin(brain.hop * Math.PI) *
        (brain.hopBig ? 10 : 6) *
        (motion().hop || 1)
      : 0);
  const level = cfg.prefs.level;
  const kick = level === "calm" ? 0 : level === "lively" ? 1.5 : 1;
  const face = faceOf(brain, act);
  brain.shown = face;
  const stage = brain.stage();
  // Floating idle, her feet swing slowly.
  const swing =
    brain.floating() &&
    kick > 0 &&
    (!act || act === "wait" || act === "think") &&
    !brain.sleeping &&
    brain.dizzy <= 0;
  const figure = drawnFigure(
    brain,
    brain.kit(),
    act,
    swing ? kick : 0,
    hopLift,
  );
  const top = brain.top(foot) - figure.lift;
  const half = brain.half();
  const chipY = top - 16 - gearRise(stage);
  const label = statusOf(brain, act, lead);
  const orbs = orbViews(brain);
  // Most of the time she changes a few times a second: draw only then.
  // Sparks, hearts, a snore and the like move every frame.
  const moving =
    b.fx.length > 0 ||
    ob.fx.length > 0 ||
    brain.hearts.length > 0 ||
    brain.dizzy > 0 ||
    brain.sweat > 0 ||
    brain.bang > 0 ||
    (brain.sleeping && cfg.prefs.status);
  const sig = JSON.stringify([
    brain.client,
    Math.round(brain.x),
    Math.round(foot - figure.lift),
    face,
    figure.sig,
    label,
    act === "think" && cfg.prefs.status ? Math.floor(brain.t * 3) % 4 : -1,
    brain.pout > 0,
    orbs.map((v) => [
      v.id,
      Math.round(v.orb!.x * 2),
      Math.round(v.orb!.y * 2),
      Math.round(v.orb!.in * 8),
      v.orb!.model,
      v.orb!.glow,
      v.orb!.dim,
    ]),
  ]);
  if (!moving && cache.painted && sig === cache.sig) return cache.painted;
  cache.sig = sig;
  begin(b, box, dt);
  const near = figure.draw(pen, brain.x, foot - figure.lift);
  brain.nearHand = { x: near[0], y: near[1] };
  // What she has on her mind, over her head.
  if (cfg.prefs.status) {
    if (act === "think")
      cloud(pen, brain.x + face * (half + 14), top + 4, brain.t, face);
    else if (lead) {
      // The chip keeps itself inside the canvas by its own measure, so it
      // is placed in the box's own px.
      if (label) {
        const g = b.g.getTransform();
        b.g.setTransform(1, 0, 0, 1, 0, 0);
        pen.chip(
          label,
          brain.x - box.x,
          chipY - box.y,
          tintOf(p, lead.client).o,
        );
        b.g.setTransform(g);
      }
    }
    if (brain.sleeping)
      for (let n = 0; n < 3; n += 1) {
        const ph = (pen.T * 0.45 + n / 3) % 1;
        pen.alpha(1 - ph);
        pen.text(
          "Z",
          brain.x - face * (half + 8) - ph * 22,
          top + 60 - ph * 40,
          Math.round(14 + ph * 10),
          p.muted,
          800,
        );
        pen.alpha(1);
      }
  }
  if (brain.dizzy > 0) {
    const a = pen.T * 6;
    for (let n = 0; n < 3; n += 1)
      pixStar(
        pen,
        brain.x + Math.cos(a + n * 2.1) * 44,
        top + 6 + Math.sin(a + n * 2.1) * 9,
        3,
        p.yellow,
      );
  }
  if (brain.pout > 0)
    pixels(pen, VEIN, brain.x - face * half, top + 14, 3, { R: "#e5484d" });
  if (brain.sweat > 0)
    pixels(
      pen,
      DROP,
      brain.x + face * (half - 4),
      top + 34 + (brain.sweat % 1) * 8,
      3,
      {
        B: "#5aa9ff",
        W: "#e8f4ff",
      },
    );
  if (brain.bang > 0)
    pen.text(
      "!",
      brain.x + face * (half - 6),
      top + 18 - (0.7 - brain.bang) * 24,
      30,
      p.orange,
      800,
    );
  for (const h of brain.hearts) {
    pen.alpha(1 - h.t / 1.4);
    pixels(pen, HEART, h.x - 10, h.y, 3, { X: "#ff6f91" });
    pen.alpha(1);
  }
  drawPixels(pen, b.fx);
  begin(ob, box, dt);
  ob.draw(orbs, () => undefined, HIDDEN);
  cache.painted = { box, top, bottom: foot - figure.lift, chipY };
  return cache.painted;
}
