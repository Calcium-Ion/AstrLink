// What the big pet wears on her head as today's tokens grow: a star clip, a
// flower, a bow, then a crown. They are props in the rig's units, worn on
// her head so they nod and lean with it.
import { CX, prop, type Prop } from "../live-crew-rig";
import type { Badge } from "./pet-kit";

const INK: Record<string, string> = {
  Y: "#f4c95d",
  y: "#c99a2e",
  w: "#fffaf0",
  P: "#ff8fa3",
  p: "#d9607a",
  R: "#e5484d",
  r: "#a3262b",
  B: "#5aa9ff",
};

const STAR = ["..Y..", ".YYY.", "YYwYY", ".YyY.", ".y.y."];
const FLOWER = [".P.P.", "PPPPP", ".PYP.", "PPPPP", ".p.p."];
const BOW = ["RR.....RR", "RRR...RRR", "RRRRrRRRR", "RRr...rRR", "Rr.....rR"];
const CROWN = [
  "Y.....Y.....Y",
  "YY...YYY...YY",
  "YYY.YYYYY.YYY",
  "YYYYYYYYYYYYY",
  "YYYYwYYYwYYYY",
  "yyRyyyByyyRyy",
];

/** The stages that bring something new to wear, and what it is. */
export const GEAR = { 1: "star", 3: "flower", 5: "bow", 7: "crown" } as const;
export type Gear = (typeof GEAR)[keyof typeof GEAR];

/** What reaching `stage` puts on her, if anything. */
export const gearAt = (stage: number): Gear | null =>
  GEAR[stage as keyof typeof GEAR] ?? null;

/** A little pixel picture centred at (x, y), one fine cell (2/3 unit) a pixel. */
function badge(rows: string[], x: number, y: number): Prop {
  const c = 2 / 3;
  const w = rows[0].length * c;
  const h = rows.length * c;
  const x0 = x - w / 2;
  const y0 = y - h / 2;
  return prop(
    (px, py) =>
      INK[rows[Math.floor((py - y0) / c)]?.[Math.floor((px - x0) / c)] ?? ""] ??
      null,
    [x0, y0, x0 + w, y0 + h],
  );
}

const WORN = {
  star: badge(STAR, CX - 7.4, 6.4),
  flower: badge(FLOWER, CX + 8.6, 8.6),
  bow: badge(BOW, CX, 1.4),
  crown: badge(CROWN, CX, 0.4),
};

/** What she has on by today's stage; the crown replaces the bow. */
export function gearOf(stage: number): Prop[] {
  const out: Prop[] = [];
  if (stage >= 1) out.push(WORN.star);
  if (stage >= 3) out.push(WORN.flower);
  if (stage >= 7) out.push(WORN.crown);
  else if (stage >= 5) out.push(WORN.bow);
  return out;
}

/**
 * The same, on her drawn picture: one grid pixel a pixel, from the top of her
 * head. Both clips go on her left, clear of the daisy in her hair.
 */
const PINNED = {
  star: { rows: STAR, ink: INK, x: -8, y: 7 },
  flower: { rows: FLOWER, ink: INK, x: -15, y: 16 },
  bow: { rows: BOW, ink: INK, x: 2, y: -1 },
  crown: { rows: CROWN, ink: INK, x: 2, y: -2 },
} satisfies Record<Gear, Badge>;

export function badgesOf(stage: number): Badge[] {
  const out: Badge[] = [];
  if (stage >= 1) out.push(PINNED.star);
  if (stage >= 3) out.push(PINNED.flower);
  if (stage >= 7) out.push(PINNED.crown);
  else if (stage >= 5) out.push(PINNED.bow);
  return out;
}

/** How far the gear reaches above her head, in px at `u` px a unit. */
export const gearRise = (stage: number) =>
  stage >= 7 ? 10 : stage >= 5 ? 2 : 0;
