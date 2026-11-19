import { isGirl } from "./live-crew-rig";
import type { ClientType } from "./request-record-model";

/**
 * The crew's sprites: a small critter per client, and the size of the
 * clients with a well-known fan "musume" design, whom the rig in
 * `live-crew-rig` draws. Pure data and the geometry that follows from it.
 */

/** Art pixel size of a critter in logical px. */
export const CRITTER_U = 4;

/**
 * Critters by client, in rows: `o` body, `l` light, `d` dark, `w` white, `K`
 * eyes. Pi's and Cursor's are the demo's; the rest follow its style.
 */
const CRITTERS: Partial<Record<ClientType, readonly string[]>> = {
  pi: [
    "o..........o",
    "oo........oo",
    "oooooooooooo",
    "oooooooooooo",
    "ooKooooooKoo",
    "oooooKKooooo",
    "oooooooooooo",
    ".oooooooooo.",
  ],
  cursor: [
    "...llllll...",
    ".llllllllll.",
    ".oooooooooo.",
    ".oKKooooKKo.",
    ".oooooooooo.",
    ".oooKKKKooo.",
    ".oooooooooo.",
    ".dddddddddd.",
  ],
  kimi_code: [
    "..........ll",
    "..........ll",
    "..oooooooo..",
    ".oooooooooo.",
    ".oKKooooKKo.",
    ".oooooooooo.",
    ".oooodddooo.",
    "..oooooooo..",
  ],
  opencode: [
    "oooooooooooo",
    "owwwwwwwwwwo",
    "owKKwwwwKKwo",
    "owwwwwwwwwwo",
    "owwwwKKKKwwo",
    "oooooooooooo",
    ".dddddddddd.",
  ],
  gemini_cli: [
    ".....oo.....",
    "....oooo....",
    "..oooooooo..",
    "oooooooooooo",
    "ooKKooooKKoo",
    "oooooooooooo",
    "..oooooooo..",
    "...oooooo...",
  ],
  qwen_code: [
    "...oooooo...",
    "..oooooooo..",
    ".oooooooooo.",
    ".oKKooooKKo.",
    ".oooooooooo.",
    ".llllllllll.",
    "..oooooooo..",
    "...oooooo...",
  ],
  grok_cli: [
    "....oooo....",
    "..oooooooo..",
    ".oooooooooo.",
    ".oKKKKKKKKo.",
    ".oKwKKKKwKo.",
    ".oooooooooo.",
    "..oooooooo..",
  ],
};

/** Any other client, recognised or not. */
const GENERIC = [
  "...oooooo...",
  "..oooooooo..",
  ".oooooooooo.",
  ".ooKKooKKoo.",
  ".oooooooooo.",
  ".oooooooooo.",
  ".o.oo..oo.o.",
] as const;

export interface Sprite {
  /** A critter's rows; empty for a musume, whom the rig draws. */
  rows: readonly string[];
  /** Width and standing height in art pixels, a critter's legs included. */
  w: number;
  h: number;
  /** Art pixel size in logical px. */
  u: number;
  girl: boolean;
}

const sprites = new Map<ClientType, Sprite>();

export function spriteOf(client: ClientType): Sprite {
  let sprite = sprites.get(client);
  if (!sprite) {
    if (isGirl(client)) sprite = { rows: [], w: 28, h: 34, u: 1.5, girl: true };
    else {
      const rows = CRITTERS[client] ?? GENERIC;
      sprite = {
        rows,
        w: rows[0].length,
        h: rows.length + 2,
        u: CRITTER_U,
        girl: false,
      };
    }
    sprites.set(client, sprite);
  }
  return sprite;
}

/** A clone or the excavator's driver: the same sprite at half size. */
export const smallU = (u: number) => Math.max(1, u / 2);

/** The top of a standing character's head, in logical px. */
export function crownOf(
  figure: { fy: number; u: number },
  client: ClientType,
): number {
  return figure.fy - spriteOf(client).h * figure.u;
}
