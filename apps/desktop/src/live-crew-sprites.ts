import type { ClientType } from "./request-record-model";

/**
 * The crew's sprites: a small critter per client, and for the clients with a
 * well-known fan "musume" design an original pixel chibi drawn from her
 * traits. Pure data and the geometry that follows from it.
 */

/** Art pixel size of a critter in logical px. */
export const CRITTER_U = 4;
/** The hand pixel at the end of a musume's arm. */
export const SKIN = "#fde3cf";

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

interface Musume {
  rows: readonly string[];
  pal: Readonly<Record<string, string>>;
  /** The sleeve, drawn out to the hand. */
  arm: string;
  /** The hard hat's top, in art pixels above the first row's top + 2. */
  hatTop: number;
}

/**
 * Each girl is a 34-row chibi from a shape-based generator: ellipses and
 * curves, 3×3 supersampled, rim-shaded hair, a dark outline and hand-placed
 * eyes. Letters name colors in her own palette.
 */
const MUSUME: Partial<Record<ClientType, Musume>> = {
  // Claude娘: orange wavy hair, a cream spark clip, a cream dress with a black corset and an orange tie.
  claude_code: {
    rows: [
      "..........KKKKKKBK.........",
      "........KKhhhhhBhKK.K......",
      "......KKhhHHHHBHHhhKWK.....",
      ".....KhhHHHHHHHHHHhwWwK....",
      "....KhHHHLLLHHHLLhWWOWWK...",
      "...KhHHHLLHHHHHHHLhwWwhK...",
      "...KhHHLLHHHHHHHHHLhWhhK...",
      "..KhHHLLHHHHHHHHHHHLhHHhK..",
      "..KhHHHHHHHHHHHHHHHHHHHhK..",
      "..KhhHHHHHHHHhHHHHHHHHhhK..",
      "..KhHHHHhhHHHSHHHhhHHHHhK..",
      ".KhHHHHHSSHHHSHHHSSHHHHHhK.",
      "..KhHHHSSSSSSSSSSSSSHHHhK..",
      "..KhHHHEEEESSSSSEEEEHHHhK..",
      ".KhHHHHSWEESSSSSEEWSHHHHhK.",
      "KhHLHHHSeeeSSSSSeeeSHHHLHhK",
      "KhHLHHHSeleSSSSSeleSHHHLHhK",
      ".KhLhHHHsSSSSMSSSSsHHHhLhK.",
      "..KhhhHWWSSSSSSSSSWWHhhhK..",
      "..KhhHWWWWWSSSSSWWWWWHhhK..",
      ".KKhhHWWWWWWWWWWWWWWWHhhKK.",
      "KLLLhiiSWWBBBBBBBWWSiihLLLK",
      "KLiLhiSSWWBBBBBBBWWSSihLiLK",
      "KLiLhiLWWWWWBOBWWWWWLLhLiLK",
      ".KLLhhKWWWWBOOOBWWWWKKhLLK.",
      "..KhhKWWWWWBOOOBWWWWWKLhK..",
      "...KKKWWWWWBOOOBWWWWWKKhK..",
      "....KWWWWWBOOOOOBWWWWWKK...",
      "....KWWWWWBOOOOOBWWWWWK....",
      "...KwwwwwwwwwwwwwwwwwwwK...",
      "....KKKKKKbbbKbbbKKKKKK....",
      ".........KbbbKbbbK.........",
      ".........KbbbKbbbK.........",
      ".........KbbbKbbbK.........",
    ],
    pal: {
      K: "#4e2414",
      h: "#b5461c",
      H: "#e9743a",
      L: "#f6a067",
      i: "#ffc27a",
      S: "#fde5d2",
      s: "#f6ad94",
      E: "#7a3410",
      e: "#e2802a",
      l: "#ffc370",
      W: "#fbf4e8",
      w: "#e4d3bc",
      M: "#c8564a",
      B: "#2b2530",
      G: "#ebb84e",
      O: "#f08a30",
      b: "#efe6d8",
    },
    arm: "#f3eadc",
    hatTop: 1,
  },
  // Codex娘: lavender hair, pointed purple horns, a big white hoodie with `>_`, slippers.
  codex: {
    rows: [
      "....KrK.....KKKKKKhK....KrK...",
      "....KRRKKKKKhhhhhhhKKKKKRRK...",
      "....KrRRRRRhHHHHhHHhRRRRRrK...",
      ".....KrRRRRRHHHHHHHRRRRRrK....",
      "......KrrrrrLLHHHLLrrrrrK.....",
      ".....KhHrrrrHHHHHHHrrrrHhK....",
      ".....KhHHLLHHHHHHHHHLLHHhK....",
      "....KhHHLLHHHHHHHHHHHLLHHhK...",
      "....KhHHHHHHHHHHHHHHHHHHHhK...",
      "....KhhHHHHHHHHhHHHHHHHHhhK...",
      "....KhHHHHhhHHHSHHHhhHHHHhK...",
      "...KhHHHHHSSHHHSHHHSSHHHHHhK..",
      "....KhHHHSSSSSSSSSSSSSHHHhK...",
      "....KhHHHEEEESSSSSEEEEHHHhK...",
      "...KhHHHHSWEESSSSSEEWSHHHHhK..",
      "..KhHLHHHSeeeSSSSSeeeSHHHLHhK.",
      "..KhHLHHHSeleSSSSSeleSHHHLHhK.",
      "...KhLhHHWsSSSSMSSSSsWHHhLhK..",
      "....KhhhHWWSSSSSSSSSWWHhhhK...",
      "....KhhWWWWWWSSSSSWWWWWWhhK...",
      "...KhLhWWWWWWWWWWWWWWWWWhLhK..",
      "..KLiLhWWWWWWPWWWWWWWWWWhLiLK.",
      ".KLiiLhWWWWWWWPWWWWWWWWWhLiiLK",
      ".KLLiLhiWWWWWPWPPPWWWWWhhLiLLK",
      "KTKKLLhwwwwWWWWWWWWWwwwwhLLKK.",
      "TTK.KhhKWWWWwwwwwwwWWWWKLhK...",
      "KTTK.KKKWWWWWWWWWWWWWWWKKhK...",
      ".KTTK.KWWWWWWWWWWWWWWWWWKK....",
      "..KTTKKwwwwwwwwwwwwwwwwwK.....",
      "...KK..KKKKKSSKKKSSKKKKK......",
      "...........KSSK.KSSK..........",
      "...........KSSSKWSSK..........",
      "..........KWWpWKWpWWK.........",
      "..........KWWWWKWWWWK.........",
    ],
    pal: {
      K: "#4a4080",
      h: "#a397dc",
      H: "#cbc3f2",
      L: "#efecfd",
      i: "#f6f4ff",
      R: "#8f74e6",
      r: "#5b43b4",
      S: "#fde8dc",
      s: "#f5b2ac",
      E: "#45349c",
      e: "#8b78ea",
      l: "#c6b8ff",
      W: "#fbfaff",
      w: "#d9d4f0",
      M: "#c8564a",
      P: "#6a55d6",
      p: "#f4a9c0",
      T: "#ad9fea",
    },
    arm: "#f1f0fa",
    hatTop: 1,
  },
  // The blue whale maid (鲸鱼娘): maid headband, fin ears, a navy maid dress, an apron with a whale emblem, a tail.
  deepseek_harness: {
    rows: [
      "............KKKKKKhK...........",
      "..........KKhhWwhhhKK..........",
      "........KKhwWWWWhWWwhKK........",
      ".......KhwWWWWHHHWWWWwhK.......",
      "......KhWWWLLLHHHLLLWWWhK......",
      ".....KhwWWLLHHHHHHHLLWWwhK.....",
      ".....KhHHLLHHHHHHHHHLLHbbK.....",
      "....KhHHLLHHHHHHHHHHHLLbbbK....",
      "....KhHHHHHHHHHHHHHHHHHHHhK....",
      "....KhhHHHHHHHHhHHHHHHHHhhK....",
      "....KhHHHHhhHHHSHHHhhHHHHhK....",
      "...KFHHHHHSSHHHSHHHSSHHHHHFK...",
      "..KFfHHHHSSSSSSSSSSSSSHHHHfFK..",
      ".KfffHHHHEEEESSSSSEEEEHHHHfffK.",
      "KFfFFHHHHSWEESSSSSEEWSHHHHFFfFK",
      ".KKhHLHHHSeeeSSSSSeeeSHHHLHhKK.",
      "..KhHLHHHSeleSSSSSeleSHHHLHhK..",
      "...KhLhHHHsSSSSMSSSSshHHhLhK...",
      "....KhhhHnnSSSSSSSSSnnHhhhK....",
      "....KhhHnnnNNSSSSSNNnnnHhhK....",
      "...KLLhinnnNNWWWWWNNnnnihLLK...",
      "..KLiLhiiSnNWWWWWWWNNSiihLiLK..",
      ".KLLiLhiSSNNWWbbbbWNNSSLhLiLLK.",
      ".KLKLLhhKSNNWWbbbWWNNhKKhLLKK..",
      "K.K.KhhKKNNWWWWWWWWWNNKKLhK....",
      "TKTK.KK.KNNWWWWWWWWWNNK.KhK....",
      "TTTK...KNNNWWWWWWWWWNNNK.K.....",
      "KTK....KNNNNNNNNNNNNNNNK.......",
      "KTTKK.KWwWwWwWwWwWwWwWwWK......",
      ".KTTTK.KKKKKWWwKwWWKKKKK.......",
      "..KKK......KWWK.KWWK...........",
      "...........KWWK.KWWK...........",
      "..........KNNNNKNNNK...........",
      "..........KNNNNKNNNK...........",
    ],
    pal: {
      K: "#172045",
      h: "#22408c",
      H: "#3463c4",
      L: "#5f97e6",
      i: "#8cc8f6",
      S: "#fde8dc",
      s: "#f6b2aa",
      E: "#1d3a8f",
      e: "#3f7fe0",
      l: "#8fd4ff",
      W: "#ffffff",
      w: "#d2dbeb",
      M: "#c8564a",
      F: "#2a4690",
      f: "#dce9f8",
      N: "#232f5e",
      n: "#34488c",
      b: "#6fb0f0",
      T: "#2c4b9a",
    },
    arm: "#2c3a6e",
    hatTop: 2,
  },
};

export interface Sprite {
  rows: readonly string[];
  /** Art pixel size in logical px. */
  u: number;
  /** Draws its own feet: no legs, sitting hides the bottom `crop` rows. */
  feet: boolean;
  crop: number;
  /** Poses are written for an 11-row critter; taller sprites reach higher. */
  reach: number;
  /** Art pixels above the feet where the arm leaves the body. */
  shoulder: number;
  /** Where the site's hard hat sits, in art pixels down from the image top. */
  hatTop: number;
  /** Colors by letter; a critter takes its client's tint instead. */
  pal?: Readonly<Record<string, string>>;
  arm?: string;
}

const sprites = new Map<ClientType, Sprite>();

export function spriteOf(client: ClientType): Sprite {
  let sprite = sprites.get(client);
  if (!sprite) {
    const girl = MUSUME[client];
    sprite = girl
      ? {
          rows: girl.rows,
          u: 1.5,
          feet: true,
          crop: 5,
          reach: girl.rows.length / 11,
          shoulder: 15,
          hatTop: girl.hatTop,
          pal: girl.pal,
          arm: girl.arm,
        }
      : {
          rows: CRITTERS[client] ?? GENERIC,
          u: CRITTER_U,
          feet: false,
          crop: 0,
          reach: 1,
          shoulder: 5,
          hatTop: 0,
        };
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
  const sprite = spriteOf(client);
  return figure.fy - (sprite.rows.length + (sprite.feet ? 0 : 2)) * figure.u;
}
