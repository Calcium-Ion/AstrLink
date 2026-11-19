import type { CrewPalette } from "./live-crew-draw";
import {
  CX,
  hammer,
  plainLook,
  prop,
  SIT,
  stick,
  type Look,
  type Pose,
} from "./live-crew-rig";

/**
 * What the musume do, as poses: both arms placed together with what they
 * hold, the head and eyes going along, and small idle motion (breathing,
 * blinking, glancing) so nobody stands frozen. Positions are in the rig's
 * units; `t` is seconds into the act, `seed` keeps each girl on her own beat.
 * A `lift` raises the whole girl, in fine cells (2/3 of a unit).
 */

const step = (t: number, fps: number) => Math.floor(t * fps);

/** Shut for a moment every few seconds. */
const blinking = (t: number, seed: number) =>
  (t + seed * 1.37 + 1) % (3.4 + (seed % 3) * 0.7) < 0.14;

/** The head sinks a row on each breath out. */
const breath = (t: number, seed: number) =>
  (t + seed * 0.5) % 2.6 > 1.5 ? 1 : 0;

function look(t: number, seed: number, base: Partial<Look> = {}): Look {
  const l = { ...plainLook(), ...base };
  if (l.eyes === "open" && blinking(t, seed)) l.eyes = "closed";
  return l;
}

/** A stable small number for a name, to put each girl on her own beat. */
export function seedOf(name: string): number {
  let h = 0;
  for (let i = 0; i < name.length; i += 1)
    h = (h * 31 + name.charCodeAt(i)) | 0;
  return Math.abs(h) % 97;
}

const HIP: Pick<Pose, "far"> = {
  far: { x: CX - 4.4, y: 24.8, elbow: [CX - 8.2, 22.6] },
};

export function standPose(t: number, seed: number, sit = false): Pose {
  const g = step(t + seed, 0.4) % 5;
  return {
    sit,
    nod: breath(t, seed),
    look: look(t, seed, { lx: g === 1 ? 1 : g === 3 ? -1 : 0 }),
    tail: step(t, 1.2) % 2,
  };
}

/** A bouncy little step: up on one foot, down, up on the other; `walk` counts steps. */
export function walkPose(
  walk: number,
  t: number,
  seed: number,
): { pose: Pose; lift: number } {
  const f = Math.floor(walk) % 4;
  return {
    pose: {
      legs: [f === 1 ? 1 : 0, f === 3 ? 1 : 0],
      nod: f % 2 ? 0 : 1,
      look: look(t, seed),
      tail: f < 2 ? 1 : 0,
    },
    lift: f % 2 ? 2 : 0,
  };
}

/**
 * Typing: at a keyboard at her near side, or with a laptop on her lap, its
 * lid to you, her forearms going behind it.
 */
export function typePose(
  t: number,
  seed: number,
  p: CrewPalette,
  lap: boolean,
): Pose {
  const k = step(t, 6) % 2;
  const peek = step(t + seed, 0.35) % 4 === 0;
  const eyes = look(t, seed, {
    ly: peek ? 0 : 1,
    lx: peek || !lap ? 1 : 0,
    mouth: peek ? "dot" : "flat",
  });
  if (!lap)
    return {
      sit: true,
      nod: peek ? 0 : 1,
      lean: 1,
      near: { x: CX + 8.8, y: 21.6 + SIT - k * 0.7 },
      far: { x: CX + 6.6, y: 22.2 + SIT - (1 - k) * 0.7 },
      look: eyes,
    };
  const top = 26 + SIT;
  return {
    sit: true,
    nod: peek ? 0 : 1,
    over: [
      prop(
        (x, y) => {
          if (Math.abs(x - CX) > 3.7 || y < top || y > top + 3.6) return null;
          if (Math.hypot(x - CX, y - (top + 1.7)) < 0.55)
            return k ? p.screenOn : p.paleLight;
          if (y < top + 0.55) return p.paleLight;
          if (y > top + 3.0) return p.steel;
          return p.pale;
        },
        [CX - 4, top - 1, CX + 4, top + 4],
      ),
    ],
    key: `lap${k}`,
    near: { x: CX + 2.4, y: top + 1.2 - k * 0.9 },
    far: { x: CX - 2.4, y: top + 1.2 - (1 - k) * 0.9 },
    look: eyes,
  };
}

/** A book held up at her chest, its cover to you; now and then a page turns. */
export function readPose(t: number, seed: number, p: CrewPalette): Pose {
  const turn = (t + seed) % 4.5 < 0.5;
  const top = 22.6 + SIT;
  return {
    sit: true,
    nod: 1,
    props: [
      prop(
        (x, y) => {
          const d = x - CX;
          if (Math.abs(d) > 3.9 || y < top || y > top + 3.8) return null;
          if (y < top + 0.6) return Math.abs(d) < 0.3 ? p.navy : p.paper;
          if (Math.abs(d) < 0.35) return p.navy;
          if (d > 1.2 && d < 2.8 && y > top + 1.4 && y < top + 2.2)
            return p.yellow;
          return p.blue;
        },
        [CX - 4.2, top - 1, CX + 4.2, top + 4.2],
      ),
    ],
    key: "book",
    near: turn ? { x: CX + 1.6, y: top + 0.2 } : { x: CX + 4.0, y: top + 2.2 },
    far: { x: CX - 4.0, y: top + 2.2 },
    look: look(t, seed, { ly: 1, lx: step(t, 0.8) % 2 ? 1 : -1 }),
  };
}

/**
 * Hammering: up over her head and down at her near side, at a bench's
 * height, or `low` on a crate at her feet. `strike` is the frame it lands;
 * `head` is where the hammer head lands, in units.
 */
export function hammerPose(
  t: number,
  seed: number,
  p: CrewPalette,
  low: boolean,
): { pose: Pose; strike: boolean; head: { x: number; y: number } } {
  const f = step(t, 4) % 4;
  const swing: ReadonlyArray<readonly [number, number, number, number]> = low
    ? [
        [CX + 7.2, 14.4, -0.35, -0.94],
        [CX + 8.8, 15.8, 0.55, -0.84],
        [CX + 9.4, 24.2, 0.3, 0.95],
        [CX + 9.6, 19.6, 0.92, -0.38],
      ]
    : [
        [CX + 7.2, 14.4, -0.35, -0.94],
        [CX + 8.8, 15.4, 0.55, -0.84],
        [CX + 9.6, 18.4, 0.85, 0.5],
        [CX + 9.4, 17.0, 0.92, -0.38],
      ];
  const [hx, hy, dx, dy] = swing[f];
  const props = [];
  if (low)
    props.push(
      prop(
        (x, y) => {
          if (x < CX + 8.2 || x > CX + 13 || y < 30.2 || y > 34) return null;
          if (y < 30.8) return p.woodTop;
          if (Math.abs(y - 32.1) < 0.3 || x < CX + 8.8 || x > CX + 12.4)
            return p.box;
          return p.crate;
        },
        [CX + 8, 30, CX + 13.2, 34],
      ),
    );
  return {
    pose: {
      props,
      over: hammer({ x: hx, y: hy }, dx, dy, {
        wood: p.wood,
        steel: p.paleLight,
        shade: p.steel,
      }),
      key: `hammer${f}${low ? "l" : ""}`,
      lean: f === 2 ? 1 : 0,
      nod: f === 2 ? 1 : 0,
      sway: f === 2 ? 1 : f === 0 ? -1 : 0,
      near: { x: hx, y: hy },
      ...HIP,
      look: look(t, seed, {
        lx: 1,
        ly: f === 2 ? 1 : 0,
        mouth: f === 2 ? "flat" : "dot",
      }),
    },
    strike: f === 2,
    head: { x: hx + dx * 5.4, y: hy + dy * 5.4 },
  };
}

/** A wrench turned round in front of her near side; `tip` is its jaw, in units. */
export function wrenchPose(
  t: number,
  seed: number,
  p: CrewPalette,
): { pose: Pose; tip: { x: number; y: number } } {
  const f = step(t, 6) % 6;
  const a = (f / 6) * Math.PI * 2;
  const hand = { x: CX + 8 + Math.cos(a) * 1.2, y: 21.4 + Math.sin(a) * 1.2 };
  const tip = { x: hand.x + 3.2, y: hand.y - 1.4 };
  const jaw = prop(
    (x, y) => (Math.hypot(x - tip.x, y - tip.y) < 1.1 ? p.slate : null),
    [tip.x - 2, tip.y - 2, tip.x + 2, tip.y + 2],
  );
  return {
    pose: {
      over: [stick(hand, tip, 0.4, p.slate), jaw],
      key: `wrench${f}`,
      near: hand,
      ...HIP,
      lean: 1,
      look: look(t, seed, { lx: 1, mouth: "flat" }),
    },
    tip,
  };
}

/** A big blueprint held open in both hands, eyes going over it. */
export function blueprintPose(t: number, seed: number, p: CrewPalette): Pose {
  const wob = step(t, 3) % 2;
  const dot = step(t, 2) % 4;
  const top = 21.6 + wob * 0.4;
  return {
    props: [
      prop(
        (x, y) => {
          const d = x - CX;
          if (Math.abs(d) > 5.2 || y < top || y > top + 5) return null;
          if (Math.abs(y - (top + 1.4)) < 0.25 && d > -3.8 && d < 2)
            return p.blueprintInk;
          if (Math.abs(d + 3.8) < 0.25 && y > top + 1.2 && y < top + 4)
            return p.blueprintInk;
          if (Math.abs(y - (top + 3.8)) < 0.25 && d > -3.8 && d < 3.8)
            return p.blueprintInk;
          if (Math.hypot(d - (-2.4 + dot * 1.6), y - (top + 2.6)) < 0.5)
            return p.orange;
          return p.blueprint;
        },
        [CX - 5.6, top - 1, CX + 5.6, top + 5.4],
      ),
    ],
    key: `print${wob}${dot}`,
    near: { x: CX + 5.4, y: top + 1.2 },
    far: { x: CX - 5.4, y: top + 1.2 },
    look: look(t, seed, { ly: 1, lx: dot < 2 ? -1 : 1 }),
  };
}

/** A spyglass at her near eye, the other shut; up on her toes. */
export function scopePose(
  t: number,
  p: CrewPalette,
): { pose: Pose; lift: number } {
  const f = step(t, 1.4) % 2;
  const eye = { x: CX + 3.2, y: 15.4 };
  const end = { x: CX + 11.5, y: 13.8 + f * 1.2 };
  return {
    pose: {
      over: [
        prop(
          (x, y) => {
            const vx = end.x - eye.x;
            const vy = end.y - eye.y;
            const len = Math.hypot(vx, vy);
            const t0 = ((x - eye.x) * vx + (y - eye.y) * vy) / (len * len);
            if (t0 < 0 || t0 > 1) return null;
            const qx = eye.x + vx * t0;
            const qy = eye.y + vy * t0;
            if (Math.hypot(x - qx, y - qy) > 0.75 + t0 * 0.55) return null;
            if (t0 > 0.93) return p.screenOn;
            if (Math.abs(t0 - 0.45) < 0.05 || Math.abs(t0 - 0.8) < 0.04)
              return p.wood;
            return y - qy > 0.15 ? p.wood : p.brass;
          },
          [
            eye.x - 2,
            Math.min(eye.y, end.y) - 3,
            end.x + 2,
            Math.max(eye.y, end.y) + 3,
          ],
        ),
      ],
      key: `scope${f}`,
      lean: 1,
      legs: f ? [1, 1] : [0, 0],
      near: { x: CX + 7.0, y: 16.0 + f * 0.5, front: true },
      ...HIP,
      look: { ...plainLook(), lx: 1, wink: true, mouth: "flat" },
    },
    lift: f ? 2 : 0,
  };
}

/**
 * Drawing in a sketchbook propped on her knees, its cover to you and the
 * pencil going along its top edge; now and then she lifts it and looks up.
 * The big pet's quiet edit.
 */
export function notePose(t: number, seed: number, p: CrewPalette): Pose {
  const f = step(t, 3) % 4;
  const peek = (t + seed) % 6 > 5;
  const top = 22.6 + SIT;
  const hand = peek
    ? { x: CX + 3.0, y: top - 1.4 }
    : { x: CX + 0.6 + f * 0.8, y: top - 0.5 - (f % 2) * 0.3 };
  const tip = peek
    ? { x: hand.x - 0.4, y: hand.y + 1.0 }
    : { x: hand.x - 0.4, y: top + 0.1 };
  const end = { x: hand.x + 0.6, y: hand.y - 2.4 };
  return {
    sit: true,
    nod: peek ? 0 : 1,
    props: [
      prop(
        (x, y) => {
          const d = x - CX;
          if (Math.abs(d) > 4.4 || y < top || y > top + 5) return null;
          if (y < top + 0.6)
            return Math.round((d + 4.4) / 0.9) % 2 ? p.paleLight : p.steel;
          if (Math.abs(d) < 2.4 && y > top + 1.8 && y < top + 3.2)
            return p.paper;
          return p.pink;
        },
        [CX - 4.6, top - 0.5, CX + 4.6, top + 5.3],
      ),
    ],
    over: [
      stick(tip, end, 0.45, p.yellow),
      stick(tip, { x: tip.x + 0.25, y: tip.y - 0.45 }, 0.3, p.steel),
    ],
    key: `note${peek ? "p" : f}`,
    near: hand,
    far: { x: CX - 4.0, y: top + 1.8 },
    look: look(t, seed, {
      ly: peek ? 0 : 1,
      lx: peek ? 1 : 0,
      mouth: peek ? "smile" : "dot",
    }),
  };
}

/**
 * A magnifying glass held up beside her face, moved slowly from spot to
 * spot, her eyes going with it. The big pet's quiet search.
 */
export function lensPose(t: number, seed: number, p: CrewPalette): Pose {
  const f = step(t + seed, 0.7) % 3;
  const spots: ReadonlyArray<readonly [number, number, number]> = [
    [CX + 6.6, 18.2 + SIT, -1],
    [CX + 7.2, 20.4 + SIT, 0],
    [CX + 6.0, 22.0 + SIT, 1],
  ];
  const [hx, hy, ly] = spots[f];
  const lens = { x: hx + 1.3, y: hy - 2.4 };
  return {
    sit: true,
    over: [
      stick({ x: hx - 0.3, y: hy + 0.5 }, lens, 0.35, p.wood),
      prop(
        (x, y) => {
          const d = Math.hypot(x - lens.x, y - lens.y);
          if (d > 1.9) return null;
          if (d > 1.35) return p.brass;
          return Math.hypot(x - (lens.x - 0.5), y - (lens.y - 0.5)) < 0.45
            ? p.paleLight
            : p.glass;
        },
        [lens.x - 2.2, lens.y - 2.2, lens.x + 2.2, lens.y + 2.2],
      ),
    ],
    key: `lens${f}`,
    lean: 1,
    near: { x: hx, y: hy, front: true },
    look: look(t, seed, { lx: 1, ly, mouth: "dot" }),
  };
}

/** A finger at her chin, eyes up and away. */
export function thinkPose(t: number, seed: number, sit: boolean): Pose {
  const f = step(t + seed, 0.6) % 2;
  const drop = sit ? SIT : 0;
  return {
    sit,
    lean: 1,
    near: {
      x: CX + 2.6,
      y: 20.0 + drop + f * 0.3,
      front: true,
      elbow: [CX + 6.8, 25 + drop],
    },
    look: look(t, seed, { lx: -1, ly: -1, mouth: f ? "dot" : "flat" }),
  };
}

/** Writing on a board at her near side, a marker sweeping high; `tip` in units. */
export function scribblePose(
  t: number,
  seed: number,
  p: CrewPalette,
): { pose: Pose; tip: { x: number; y: number } } {
  const hand = {
    x: CX + 8.6 + Math.round(Math.sin(t * 2.4) * 2) * 0.5,
    y: 15.8 + Math.round(Math.sin(t * 6) * 2) * 0.4,
  };
  const tip = { x: hand.x + 1.6, y: hand.y - 1.2 };
  return {
    pose: {
      over: [stick(hand, tip, 0.45, p.red)],
      key: `marker${hand.x}${hand.y}`,
      near: { ...hand, front: true },
      ...HIP,
      lean: 1,
      look: look(t, seed, { lx: 1, ly: -1, mouth: "flat" }),
    },
    tip,
  };
}

/** Marking a sheet at her near side, a pen going back and forth. */
export function penmarkPose(t: number, seed: number, p: CrewPalette): Pose {
  const hand = {
    x: CX + 7.8 + Math.round(Math.sin(t * 2.2) * 2) * 0.5,
    y: 23.6 + SIT + Math.round(Math.sin(t * 7)) * 0.3,
  };
  return {
    sit: true,
    nod: 1,
    lean: 1,
    over: [stick(hand, { x: hand.x + 0.8, y: hand.y - 2 }, 0.4, p.red)],
    key: `pen${hand.x}${hand.y}`,
    near: hand,
    look: look(t, seed, { lx: 1, ly: 1, mouth: "flat" }),
  };
}

/** A mug by its handle; every few seconds a sip. `steam` rises from it, in units. */
export function coffeePose(
  t: number,
  seed: number,
  p: CrewPalette,
): { pose: Pose; steam: { x: number; y: number } | null } {
  const sip = (t + seed) % 4 > 2.9;
  const mx = sip ? CX + 0.4 : CX + 2.2;
  const my = sip ? 17.4 + SIT : 23.0 + SIT;
  return {
    pose: {
      sit: true,
      held: [
        prop(
          (x, y) => {
            const d = Math.hypot(x - (mx + 1.9), y - (my + 1.5));
            if (d < 1.05 && d > 0.45 && x > mx + 1.2) return p.pale;
            if (Math.abs(x - mx) > 1.4 || y < my || y > my + 3) return null;
            if (y < my + 0.5) return p.wood;
            return x > mx + 0.7 ? p.pale : p.paper;
          },
          [mx - 2, my - 1, mx + 3.4, my + 3.4],
        ),
      ],
      key: sip ? "sip" : "mug",
      nod: sip ? 0 : breath(t, seed),
      near: { x: mx + 2.6, y: my + 1.8, front: true },
      look: sip
        ? { ...plainLook(), eyes: "closed", blush: 1 }
        : look(t, seed, { mouth: "smile", ly: 1 }),
    },
    steam: sip ? null : { x: mx, y: my - 0.6 },
  };
}

/** Asleep on her back, or dizzy after a slip. */
export function lyingPose(t: number, dizzy: boolean): Pose {
  return {
    look: {
      ...plainLook(),
      eyes: dizzy ? "dizzy" : "closed",
      mouth: dizzy ? "o" : step(t, 0.7) % 2 ? "o" : "dot",
      blush: dizzy ? 0 : 1,
    },
    near: { x: CX + 6.4, y: 26.4 },
    far: { x: CX - 6.4, y: 26.4 },
  };
}

/** A celebration hop: hands up while she is in the air. */
export function cheerPose(up: boolean): Pose {
  return {
    near: up
      ? { x: CX + 8.4, y: 14.6, open: true }
      : { x: CX + 7.0, y: 22.4, open: true },
    far: up
      ? { x: CX - 8.4, y: 14.6, open: true }
      : { x: CX - 7.0, y: 22.4, open: true },
    legs: up ? [1, 1] : [0, 0],
    sway: up ? -1 : 1,
    nod: up ? 0 : 1,
    look: { ...plainLook(), eyes: "happy", mouth: "wide", blush: 1 },
    tail: up ? 1 : 0,
  };
}

/** Climbing a ladder: hands on the rungs, one up, one down. */
export function climbPose(walk: number): Pose {
  const k = Math.floor(walk) % 2 ? 1 : -1;
  return {
    near: { x: CX + 5.4, y: 15.6 + k * 1.2 },
    far: { x: CX - 5.4, y: 15.6 - k * 1.2 },
    legs: k > 0 ? [1, 0] : [0, 1],
    look: plainLook(),
  };
}

/** Carrying something in front of her in both hands; the near hand is where it goes. */
export function carryPose(
  walk: number,
  walking: boolean,
): { pose: Pose; lift: number } {
  const w = walking ? walkPose(walk, 0, 0) : { pose: {}, lift: 0 };
  return {
    pose: {
      ...w.pose,
      near: { x: CX + 7.6, y: 23.4 },
      far: { x: CX + 4.6, y: 24.2 },
      look: { ...plainLook(), lx: 1 },
    },
    lift: w.lift,
  };
}
