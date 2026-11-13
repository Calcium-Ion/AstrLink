import {
  Act,
  arm,
  body,
  clamp,
  type CrewColor,
  DEMO_WIDTH,
  drawPixels,
  figureOf,
  type Figure,
  Pen,
  type Pixel,
  pixStar,
  rgba,
  seeded,
  sparkle,
  spawn,
  type Stage,
  stepPixels,
  tintOf,
  U,
} from "./live-crew-draw";
import {
  CREW_TOP,
  type Crew,
  type CrewAct,
  type CrewGrowth,
  type CrewLayout,
  type CrewMember,
  type CrewSpot,
} from "./live-crew-model";
import { smallU } from "./live-crew-sprites";
import type { ClientType } from "./request-record-model";

/**
 * The four places the crew works in. Each is the demo's scene: its props,
 * stations and timing at the demo's 1006 px, spread to the canvas width.
 * Props keep their size; only where they stand moves. Today's tokens grow
 * each one, a stage at a time.
 */
export const CREW_SCENES = ["camp", "office", "ship", "site"] as const;
export type CrewSceneId = (typeof CREW_SCENES)[number];
export const DEFAULT_CREW_SCENE: CrewSceneId = "camp";
/** Every scene's logical height, so switching scenes keeps the module still. */
export const CREW_HEIGHT = 280;
/** The floor line of the one-floor scenes; name tags go in the band below. */
const GROUND = CREW_HEIGHT - 54;

export interface CrewScene {
  readonly id: CrewSceneId;
  /** Logical height. */
  readonly H: number;
  readonly layout: CrewLayout;
  readonly stage: Stage;
  /** Where the last bake added something new, for a burst of sparkles. */
  readonly fresh: ReadonlyArray<readonly [number, number]>;
  /**
   * Takes how far today's tokens have come; returns what the cached layers
   * show, so they bake again only when it changes.
   */
  grow(growth: CrewGrowth): string;
  /** Drawn once per size, palette and growth: the opaque background. */
  back(pen: Pen): void;
  /** Drawn every frame between the two cached layers. */
  movers?(pen: Pen): void;
  /** Drawn once per size, palette and growth, over the movers. */
  front?(pen: Pen): void;
  /** Props that move or light up. */
  props(pen: Pen, crew: Crew): void;
  step(crew: Crew, dt: number): void;
  /** Nothing of the scene's own is still moving. */
  quiet(): boolean;
}

type Rect = readonly [number, number, number, number, string];
type Places = Record<CrewAct, number>;
type Fresh = Array<readonly [number, number]>;

/** Places beside a station, nearest first: the demo's four, then further out. */
const OFFSETS = [-50, 50, -98, 98, -146, 146, -194, 194, -242, 242];
/** How close to the canvas edge a character may stand. */
const EDGE = 28;

/** The demo's station x positions, spread across `W`. */
function spread(places: Places, W: number): Places {
  const k = W / DEMO_WIDTH;
  return Object.fromEntries(
    Object.entries(places).map(([act, x]) => [act, Math.round(x * k)]),
  ) as Places;
}

/**
 * The `slot`th place beside station `x`, facing it. Places past `from` or
 * `to` are skipped, so a station near an edge seats people on its other side.
 */
function beside(
  x: number,
  slot: number,
  [from, to]: readonly [number, number],
  offsets = OFFSETS,
  level = 0,
): CrewSpot {
  const inside = offsets.filter((off) => x + off >= from && x + off <= to);
  const off = inside[slot % inside.length] ?? 0;
  return { x: x + off, face: off < 0 ? 1 : -1, level };
}

/** Someone at `act` who has arrived. */
function busy(crew: Crew, act: CrewAct): CrewMember | undefined {
  return crew.members.find(
    (member) => !member.leaving && member.act === act && member.arrived,
  );
}

function table(
  pen: Pen,
  x: number,
  w: number,
  h: number,
  floor: number,
  top = pen.palette.woodTop,
  leg = pen.palette.wood,
) {
  pen.rects(x, floor, [
    [-w / 2, h, w, 1, top],
    [-w / 2 + 1, 0, 1, h, leg],
    [w / 2 - 2, 0, 1, h, leg],
  ]);
}

/** Lines of text on a screen, scrolling while someone works at it. */
function screenRows(
  pen: Pen,
  x: number,
  y: number,
  on: boolean,
  n = 4,
  width = 9,
) {
  const p = pen.palette;
  for (let k = 0; k < n; k += 1) {
    const length = on
      ? 2 + ((Math.floor(pen.T * 6 + k * 3) * 7 + k * 5) % width)
      : [6, 4, 8, 3][k % 4];
    pen.P(
      x,
      y + k * U,
      Math.min(length, width),
      0.5,
      on ? p.screenOn : p.screenOff,
    );
  }
}

/**
 * Stars as `[x, y, size, alpha]`, one fill per shade: alphas in steps of
 * 1/32 look the same.
 */
function starsOf(pen: Pen, list: ReadonlyArray<readonly number[]>) {
  const byAlpha = new Map<number, number[][]>();
  for (const [x, y, size, alpha] of list) {
    const step = Math.round(alpha * 32);
    let rects = byAlpha.get(step);
    if (!rects) byAlpha.set(step, (rects = []));
    rects.push([x, y, size, size]);
  }
  for (const [step, rects] of byAlpha)
    pen.rectsOf(rgba(pen.palette.navy, step / 32), rects);
}

/** A pine `h` art pixels tall, standing on `floor`. */
function pine(pen: Pen, x: number, floor: number, h: number, color: string) {
  for (let n = 0; n < h; n += 1) {
    const half = 1 + Math.floor(n * 0.55) - (n && n % 3 === 0 ? 1 : 0);
    pen.P(x - half * U, floor - (h + 2 - n) * U, half * 2, 1, color);
  }
  pen.P(x - 2, floor - 2 * U, 1, 2, pen.palette.trunk);
}

const FLAGS: readonly CrewColor[] = [
  "red",
  "yellow",
  "green",
  "blue",
  "pink",
  "violet",
];

/** A string of flags from `a` to `b`, sagging `sag` px below `y0`. */
function bunting(
  pen: Pen,
  a: number,
  b: number,
  y0: number,
  sag: number,
  seed: number,
) {
  const p = pen.palette;
  for (let x = a; x <= b; x += 4) {
    const k = (x - a) / (b - a);
    const y = Math.round((y0 + Math.sin(k * Math.PI) * sag) / 2) * 2;
    pen.rect(x, y, 4, 2, p.steam);
    if ((x - a) % 32 === 16) {
      const color = p[FLAGS[Math.floor(((x - a) / 32 + seed) % 6)]];
      pen.rect(x - 4, y + 2, 12, 6, color);
      pen.rect(x, y + 8, 4, 4, color);
    }
  }
}

// ---------- camp ----------

const FIREWORKS: readonly CrewColor[] = ["yellow", "pink", "blue", "green"];

/** Pines as `[x, stage, height]`: the woods fill in as the day goes on. */
const PINES = [
  [30, 1, 11],
  [300, 1, 9],
  [560, 2, 12],
  [690, 2, 9],
  [160, 3, 10],
  [990, 3, 9],
  [120, 4, 8],
  [590, 4, 11],
  [650, 5, 9],
  [850, 5, 12],
  [200, 6, 8],
  [330, 6, 11],
  [520, 7, 9],
  [740, 7, 12],
] as const;

function camp(W: number): CrewScene {
  const FLOOR = GROUND;
  const H = CREW_HEIGHT;
  const k = W / DEMO_WIDTH;
  const at = (x: number) => Math.round(x * k);
  const X = spread(
    {
      bash: 78,
      read: 210,
      edit: 345,
      search: 485,
      write: 625,
      think: 770,
      wait: 915,
    },
    W,
  );
  const rand = seeded(11);
  const fx: Pixel[] = [];
  const fresh: Fresh = [];
  let st = 0;
  let dents = 0;
  let roll = 0;
  let boom = 0;
  const note = (x: number, y: number, min: number) => {
    if (min === st) fresh.push([x, y]);
  };
  return {
    id: "camp",
    H,
    fresh,
    layout: {
      floors: [FLOOR],
      door: -40,
      exit: W + 60,
      spot: (act, slot) => beside(X[act], slot, [EDGE, W - EDGE]),
      onDone(act) {
        if (act === "edit") dents = 0;
      },
    },
    stage: {
      hat: false,
      fx,
      perform(pen, member) {
        const ch = figureOf(member);
        const act = member.act;
        if (act === "bash" || act === "write") Act.type(pen, ch);
        else if (act === "read") Act.read(pen, ch);
        else if (act === "edit")
          Act.hammer(pen, ch, fx, rand, () => (dents = Math.min(6, dents + 1)));
        else if (act === "search") Act.scope(pen, ch);
        else if (act === "think") Act.think(pen, ch);
        else Act.sleep(pen, ch);
        if (act === "write" && pen.chance(rand, member.rate * 0.006)) {
          fx.push({
            x: X.write + (rand() - 0.5) * 18,
            y: FLOOR - 64,
            vx: (rand() - 0.5) * 30,
            vy: -40 - rand() * 40,
            t: 0,
            life: 1.3,
            ink: { client: member.client },
            s: 4,
          });
        }
      },
    },
    grow(growth) {
      st = growth.stage;
      return String(st);
    },
    back(pen) {
      const p = pen.palette;
      fresh.length = 0;
      pen.rect(0, 0, W, H, p.bg);
      sparkle(pen, seeded(21), Math.round((30 + st * 14) * k), FLOOR - 60);
      // Far hills.
      for (let x = 0; x < W; x += 16) {
        const h =
          16 +
          Math.floor(Math.sin(x * 0.012) * 8 + Math.sin(x * 0.031) * 5) * 2;
        pen.rect(x, FLOOR - h, 16, h, p.hills);
      }
      // The moon fills out over the day.
      const moon = W - 66;
      for (let y = 0; y < 12; y += 1) {
        for (let x = 0; x < 12; x += 1) {
          const lit = (x - 5.5) ** 2 + (y - 5.5) ** 2 <= 34;
          const shade =
            st < CREW_TOP &&
            (x - 5.5 - 2 - st * 1.4) ** 2 + (y - 5.5) ** 2 <= 34;
          if (lit && !shade) {
            pen.rect(
              moon + x * 4,
              10 + y * 4,
              4,
              4,
              st === CREW_TOP ? p.gold : p.moon,
            );
          }
        }
      }
      // The camp grows: woods, more tents, bunting, a cabin, a watchtower.
      PINES.forEach(([x, min, h], n) => {
        if (st < min) return;
        pine(pen, at(x), FLOOR, h, n % 2 ? p.pine : p.pineLight);
        note(at(x), FLOOR - h * U, min);
      });
      for (const [x, min] of [
        [255, 2],
        [655, 5],
      ] as const) {
        if (st < min) continue;
        for (let n = 0; n < 8; n += 1) {
          pen.rects(at(x), FLOOR, [
            [-(n + 1), 8 - n, (n + 1) * 2, 1, n % 2 ? p.tent : p.tentLight],
          ]);
        }
        pen.rects(at(x), FLOOR, [[-1, 0, 2, 4, p.paleLight]]);
        note(at(x), FLOOR - 32, min);
      }
      if (st >= 4) {
        pen.rects(at(425), FLOOR, [
          [-10, 0, 20, 12, p.cabin],
          [-10, 4, 20, 1, p.cabinLine],
          [-10, 8, 20, 1, p.cabinLine],
          [-2, 0, 4, 7, p.timberDark],
          [-8, 5, 4, 4, p.gold],
          [4, 5, 4, 4, p.gold],
        ]);
        for (let n = 0; n < 6; n += 1) {
          pen.rects(at(425), FLOOR, [
            [-12 + n * 2, 12 + n, 24 - n * 4, 1, p.timber],
          ]);
        }
        note(at(425), FLOOR - 64, 4);
      }
      if (st >= 6) {
        pen.rects(at(712), FLOOR, [
          [-8, 0, 2, 22, p.timber],
          [6, 0, 2, 22, p.timber],
          [-6, 8, 12, 1, p.timberLight],
          [-6, 15, 12, 1, p.timberLight],
          [-10, 22, 20, 2, p.timber],
          [-10, 24, 1, 4, p.timber],
          [9, 24, 1, 4, p.timber],
          [-11, 28, 22, 2, p.timberDark],
          [-7, 30, 14, 1, p.timberDark],
          [0, 31, 1, 6, p.steam],
          [1, 34, 5, 3, p.red],
        ]);
        note(at(712), FLOOR - 130, 6);
      }
      if (st >= 3) {
        bunting(pen, at(40), at(450), 50, 16, 0);
        bunting(pen, at(450), at(860), 50, 16, 3);
        note(at(245), 64, 3);
        note(at(655), 64, 3);
      }
      if (st >= 5) {
        bunting(pen, at(120), at(520), 28, 12, 1);
        bunting(pen, at(520), at(900), 28, 12, 4);
        note(at(320), 40, 5);
        note(at(710), 40, 5);
      }
      pen.rect(0, FLOOR, W, 4, p.line);
      pen.rect(0, FLOOR + 4, W, H - FLOOR - 4, p.ground);
      // Laptop on a crate.
      pen.rects(X.bash, FLOOR, [
        [-8, 0, 16, 6, p.crate],
        [-8, 2.5, 16, 1, p.wood],
        [-7, 6, 14, 1, p.steel],
        [-6, 7, 12, 8, p.console],
        [-5, 8, 10, 6, p.screen],
      ]);
      // A log to sit on while reading, and a crate of books.
      pen.rects(X.read, FLOOR, [
        [-14, 0, 8, 3, p.wood],
        [6, 0, 8, 3, p.wood],
        [-13, 3, 6, 1, p.woodTop],
        [7, 3, 6, 1, p.woodTop],
      ]);
      pen.rects(X.read, FLOOR, [
        [-5, 0, 10, 8, p.box],
        [-5, 8, 10, 1, p.wood],
        [-4, 9, 3, 4, p.red],
        [-1, 9, 3, 3, p.blue],
        [2, 9, 3, 5, p.green],
      ]);
      // Workbench.
      table(pen, X.edit, 22, 7, FLOOR);
      pen.rects(X.edit, FLOOR, [[-8, 8, 16, 2, p.plank]]);
      // Telescope.
      pen.rects(X.search, FLOOR, [
        [-5, 0, 1, 3, p.steel],
        [3, 0, 1, 3, p.steel],
        [-4, 3, 2, 3, p.steel],
        [2, 3, 2, 3, p.steel],
        [-1, 5, 2, 4, p.steel],
      ]);
      for (let n = 0; n < 7; n += 1) {
        pen.rects(X.search, FLOOR, [
          [-6 + n * 2, 9 + n * 1.5, 3, 3, n < 2 ? p.console : p.slate],
        ]);
      }
      // Typewriter on a crate.
      pen.rects(X.write, FLOOR, [
        [-9, 0, 18, 6, p.crate],
        [-9, 2.5, 18, 1, p.wood],
        [-6, 6, 12, 4, p.steel],
        [-8, 10, 16, 2, p.console],
      ]);
      // Logs, and the tent nobody has to walk to.
      pen.rects(X.think, FLOOR, [
        [-6, 0, 12, 2, p.wood],
        [-8, 0, 3, 1, p.steam],
        [5, 0, 3, 1, p.steam],
      ]);
      for (let n = 0; n < 11; n += 1) {
        pen.rects(X.wait, FLOOR, [
          [-(n + 1), 11 - n, (n + 1) * 2, 1, n % 2 ? p.pale : p.paleLight],
        ]);
      }
      for (let n = 0; n < 6; n += 1) {
        pen.rects(X.wait, FLOOR, [
          [-(n * 0.7 + 0.5), 6 - n, n * 1.4 + 1, 1, p.steel],
        ]);
      }
    },
    props(pen, crew) {
      const p = pen.palette;
      const T = pen.T;
      if (st === CREW_TOP) {
        // A balloon drifts over, and fireworks go up now and then.
        pen.stamp(
          "balloon",
          ((T * 14) % (W + 160)) - 80,
          30 + Math.sin(T * 0.7) * 6 + 52,
          [-24, -52, 24, 4],
          (at) =>
            at.rects(0, 0, [
              [-4, 10, 8, 2, p.red],
              [-5, 6, 10, 4, p.red],
              [-5, 6, 2, 4, p.yellow],
              [3, 6, 2, 4, p.yellow],
              [-4, 4, 8, 2, p.red],
              [-3, 3, 6, 1, p.redDark],
              [-2, 0, 4, 2, p.woodTop],
              [-2, 2, 1, 1, p.steam],
              [1, 2, 1, 1, p.steam],
            ]),
        );
        // Only for company, so an empty camp still goes quiet.
        if (pen.dt > 0 && crew.members.length > 0 && T - boom > 1.6) {
          boom = T;
          const x = at(120 + ((T * 977) % 760));
          const y = 30 + ((T * 331) % 50);
          for (const color of FIREWORKS)
            spawn(fx, rand, x, y, 6, color, 150, 80, 1, 4);
        }
      }
      const lit: number[][] = [];
      for (let n = 0; n < 12; n += 1) {
        if (Math.sin(T * 0.9 + n * 2.2) > 0.3) {
          lit.push([
            at((n * 83 + 40) % DEMO_WIDTH),
            14 + ((n * 37) % 90),
            4,
            4,
          ]);
        }
      }
      pen.rectsOf(rgba(p.navy, 0.28), lit);
      screenRows(
        pen,
        X.bash - 4 * U,
        FLOOR - 13.5 * U,
        Boolean(busy(crew, "bash")),
        4,
        8,
      );
      // The plank shows nail heads as the hammer lands.
      pen.stamp(`dents|${dents}`, X.edit, FLOOR, [-28, -44, 28, 0], (at) => {
        for (let n = 0; n < dents; n += 1)
          at.P((-6 + n * 2) * U, -10.5 * U, 1, 0.5, p.steel);
      });
      if (busy(crew, "search")) {
        pixStar(
          pen,
          X.search + 46 + Math.sin(T * 2) * 8,
          FLOOR - 104 + Math.cos(T * 2) * 8,
          2 + (Math.floor(T * 5) % 2) * 2,
          p.yellow,
        );
      }
      // Paper rolls out of the typewriter while writing.
      pen.rects(X.write, FLOOR, [[-3, 12, 6, 3 + roll * 5, p.paper]]);
      const tapped = roll > 0.5 ? Math.floor(T * 8) % 5 : -1;
      pen.stamp(`keys|${tapped}`, X.write, FLOOR, [-24, -40, 24, 0], (at) => {
        for (let n = 0; n < 5; n += 1) {
          const tap = tapped >= 0 && (tapped + n) % 5 === 0 ? -0.5 : 0;
          at.rects(0, 0, [[-5 + n * 2, 7.5 + tap, 1, 1, p.keys]]);
        }
      });
      // Fire, bigger as the day goes on.
      const f = Math.floor(T * 7) % 3;
      const fireOn = Boolean(busy(crew, "think"));
      const s = (fireOn ? 1 : 0.55) * (0.8 + st * 0.08);
      pen.stamp(
        `fire|${f}|${fireOn}|${st}`,
        X.think,
        FLOOR,
        [-80, -80, 80, 48],
        (at) => {
          if (fireOn) at.glow(0, -16, 2, 62, p.orange, 0.26);
          at.rects(0, 0, [
            [-3, 2, 6, (5 + Number(f === 0)) * s, p.orange],
            [-2 + Number(f === 2), 2, 4, (7 + Number(f === 1)) * s, p.yellow],
            [-1 - Number(f === 1), 2, 2, (4 + Number(f === 2)) * s, p.flame],
          ]);
        },
      );
    },
    step(crew, dt) {
      roll += (Number(Boolean(busy(crew, "write"))) - roll) * clamp(dt);
      stepPixels(fx, dt);
    },
    quiet: () => fx.length === 0,
  };
}

// ---------- office ----------

function office(W: number): CrewScene {
  const FLOOR = GROUND;
  const H = CREW_HEIGHT;
  const X = spread(
    {
      bash: 118,
      read: 245,
      edit: 375,
      search: 510,
      write: 645,
      think: 775,
      wait: 898,
    },
    W,
  );
  const k = W / DEMO_WIDTH;
  const at = (x: number) => Math.round(x * k);
  const rand = seeded(12);
  const fx: Pixel[] = [];
  const fresh: Fresh = [];
  let st = 0;
  let sheets = 0;
  let strokes = 0;
  let marks = 0;
  let globe = 0;
  const clock = at(330);
  const note = (x: number, y: number, min: number) => {
    if (min === st) fresh.push([x, y]);
  };
  const books = (pen: Pen) => {
    const p = pen.palette;
    return [p.pink, p.blue, p.green, p.yellow, p.violet, p.red];
  };
  return {
    id: "office",
    H,
    fresh,
    layout: {
      floors: [FLOOR],
      door: 18,
      exit: 18,
      spot: (act, slot) => beside(X[act], slot, [EDGE, W - EDGE]),
      onDone(act) {
        if (act === "write") sheets = Math.min(10, sheets + 2);
        if (act === "edit") marks = 0;
      },
    },
    stage: {
      hat: false,
      fx,
      perform(pen, member) {
        const ch = figureOf(member);
        const act = member.act;
        if (act === "bash" || act === "write" || act === "search")
          Act.type(pen, ch);
        else if (act === "read") Act.read(pen, ch);
        else if (act === "edit") Act.penmark(pen, ch);
        else if (act === "think") Act.scribble(pen, ch);
        else Act.coffee(pen, ch, fx, rand);
      },
    },
    grow(growth) {
      st = growth.stage;
      return String(st);
    },
    back(pen) {
      const p = pen.palette;
      fresh.length = 0;
      pen.rect(0, 0, W, FLOOR, p.wall);
      if (st === CREW_TOP) pen.wash(0, 0, W, FLOOR, p.gold, 0.3, 0.04);
      // The company grows: more windows, a taller skyline outside, and
      // things on the walls.
      const r = seeded(4);
      for (const [x0, min] of [
        [440, 0],
        [140, 2],
        [710, 4],
      ] as const) {
        if (st < min) continue;
        const x = at(x0);
        const w = 104;
        pen.rect(x - 2, 14, w + 4, 56, p.line);
        pen.rect(x, 16, w, 52, p.bg);
        for (let n = 0; n < 8; n += 1) {
          const color = rgba(p.navy, 0.15 + r() * 0.35);
          pen.rect(
            x + Math.floor((r() * w) / 4) * 4,
            18 + Math.floor((r() * 20) / 4) * 4,
            2,
            2,
            color,
          );
        }
        for (let q = 0; q < 6; q += 1) {
          const h = Math.min(44, 6 + st * (3 + ((q * 7 + x0) % 5)));
          const bx = x + 2 + q * 17;
          pen.rect(bx, 68 - h, 15, h, q % 2 ? p.skyline : p.skylineLight);
          if (st < 3) continue;
          for (let y = 68 - h + 4; y < 64; y += 7) {
            if ((y + q) % 3) pen.rect(bx + 3, y, 3, 3, p.gold);
            if ((y + q * 2) % 4) pen.rect(bx + 9, y, 3, 3, p.gold);
          }
        }
        note(x + 52, 42, min);
      }
      if (st >= 1) {
        pen.rects(at(72), 104, [
          [-6, 0, 12, 10, p.paper],
          [-6, 0, 12, 0.5, p.woodTop],
          [-6, 9.5, 12, 0.5, p.woodTop],
          [-6, 0, 0.5, 10, p.woodTop],
          [5.5, 0, 0.5, 10, p.woodTop],
          [-4, 2, 8, 4, p.sky],
          [-3, 2, 3, 3, p.plantLight],
          [1, 2, 3, 2, p.plant],
          [2, 6, 2, 2, p.yellow],
        ]);
        note(at(72), 84, 1);
      }
      if (st >= 2) {
        pen.rects(at(627), 74, [
          [-7, 0, 14, 12, p.blue],
          [-6, 1, 12, 10, p.monitor],
          [-4, 3, 2, 3, p.pink],
          [-1, 3, 2, 5, p.orange],
          [2, 3, 2, 7, p.green],
        ]);
        note(at(627), 50, 2);
      }
      if (st >= 3) {
        pen.rects(at(390), 80, [
          [-6, 0, 12, 9, p.woodTop],
          [-5, 1, 10, 7, p.parchment],
          [-3, 5, 6, 1, p.steam],
          [-3, 3, 4, 1, p.printer],
          [1, 1, 2, 2, p.red],
        ]);
        note(at(390), 62, 3);
      }
      if (st >= 4) {
        pen.rects(at(880), 106, [
          [-14, 0, 28, 1, p.woodTop],
          [-13, -1, 1, 1, p.wood],
          [12, -1, 1, 1, p.wood],
          [-10, 1, 6, 1, p.printer],
          [-9, 2, 4, 3, p.printer],
          [-10, 3, 1, 2, p.printer],
          [-5, 3, 1, 2, p.printer],
          [-8, 5, 2, 2, p.printer],
          [-10, 7, 6, 1, p.printer],
        ]);
        note(at(872), 90, 4);
      }
      if (st >= 5) {
        for (const x of [250, 588, 868].map(at)) {
          pen.rect(x - 1, 0, 2, 16, p.steam);
          pen.rect(x - 10, 16, 20, 6, p.steel);
          pen.rect(x - 6, 22, 12, 2, p.gold);
        }
        pen.rects(at(577), FLOOR, [
          [-5, 0, 10, 22, p.console],
          [-4, 2, 8, 0.5, p.steel],
          [-4, 6, 8, 0.5, p.steel],
          [-4, 10, 8, 0.5, p.steel],
          [-4, 14, 8, 0.5, p.steel],
          [-4, 18, 8, 0.5, p.steel],
        ]);
        note(at(577), FLOOR - 90, 5);
        note(at(250), 22, 5);
        note(at(868), 22, 5);
      }
      if (st >= 6) {
        pen.rects(at(880), 106, [
          [2, 1, 6, 1, p.craneDark],
          [3, 2, 4, 1, p.yellow],
          [4, 3, 2, 2, p.yellow],
          [2, 5, 6, 3, p.yellow],
          [1, 6, 1, 2, p.yellow],
          [8, 6, 1, 2, p.yellow],
          [3, 6, 1, 1, p.flame],
        ]);
        pen.rects(at(300), FLOOR, [
          [-3, 0, 6, 5, p.crate],
          [-1, 5, 2, 5, p.plant],
          [-5, 8, 4, 3, p.plantLight],
          [1, 9, 4, 3, p.plantLight],
          [-3, 11, 6, 3, p.plant],
        ]);
        note(at(890), 80, 6);
        note(at(300), FLOOR - 50, 6);
      }
      if (st === CREW_TOP) {
        bunting(pen, at(10), at(500), 2, 10, 0);
        bunting(pen, at(500), at(1000), 2, 10, 2);
        note(at(250), 12, CREW_TOP);
        note(at(750), 12, CREW_TOP);
      }
      pen.rect(0, FLOOR - 6, W, 2, p.line);
      pen.rect(0, FLOOR, W, H - FLOOR, p.floor);
      for (let x = 0; x < W; x += 48)
        pen.rect(x, FLOOR, 2, H - FLOOR, p.floorSeam);
      if (st >= 3) {
        const rug = at(540);
        pen.rect(at(300), FLOOR + 8, rug, 22, p.rug);
        pen.rect(at(300), FLOOR + 8, rug, 2, p.rugEdge);
        pen.rect(at(300), FLOOR + 28, rug, 2, p.rugEdge);
        note(at(570), FLOOR + 10, 3);
      }
      pen.rect(0, FLOOR, W, 3, p.skirting);
      // Door and clock.
      pen.rects(18, FLOOR, [
        [-5, 0, 10, 17, p.woodTop],
        [2.5, 7, 1, 1, p.yellow],
      ]);
      const hy = 34;
      pen.rect(clock - 10, hy - 10, 20, 20, p.paper);
      pen.rect(clock - 12, hy - 8, 2, 16, p.console);
      pen.rect(clock + 10, hy - 8, 2, 16, p.console);
      pen.rect(clock - 8, hy - 12, 16, 2, p.console);
      pen.rect(clock - 8, hy + 10, 16, 2, p.console);
      pen.rect(clock - 1, hy - 7, 2, 8, p.console);
      // Computer desk.
      table(pen, X.bash, 18, 6, FLOOR);
      pen.rects(X.bash, FLOOR, [
        [-6, 7, 12, 8, p.console],
        [-5, 8, 10, 6, p.screen],
        [-1, 6.5, 2, 1, p.console],
        [-5, 7, 10, 0.5, p.steel],
      ]);
      // Bookshelf, minus the row whose books slide out.
      pen.rects(X.read, FLOOR, [
        [-8, 0, 16, 26, p.wood],
        [-7, 1, 14, 24, p.shelfBack],
      ]);
      [5, 12, 19].forEach((y, row) => {
        pen.rects(X.read, FLOOR, [[-7, y, 14, 1, p.wood]]);
        if (row === 1) return;
        books(pen).forEach((color, n) =>
          pen.rects(X.read, FLOOR, [
            [-6 + n * 2, y + 1, 2, 5 - (n % 3 === 1 ? 1 : 0), color],
          ]),
        );
      });
      // Review desk.
      table(pen, X.edit, 20, 6, FLOOR);
      pen.rects(X.edit, FLOOR, [
        [-5, 7, 8, 1, p.paper],
        [-4.5, 8, 8, 1, p.sheet],
        [4, 7, 1, 7, p.steel],
      ]);
      // A monitor with a turning globe.
      table(pen, X.search, 16, 6, FLOOR);
      pen.rects(X.search, FLOOR, [
        [-8, 8, 16, 11, p.console],
        [-7, 9, 14, 9, p.monitor],
        [-1, 7, 2, 1, p.console],
      ]);
      // Printer.
      table(pen, X.write, 16, 6, FLOOR);
      pen.rects(X.write, FLOOR, [
        [-5, 7, 10, 4, p.printer],
        [-6, 11, 12, 3, p.steam],
        [-3, 14, 6, 1, p.paper],
        [-4, 7, 8, 1, p.steel],
      ]);
      // Whiteboard.
      pen.rects(X.think, FLOOR, [
        [-10, 0, 1, 7, p.slateLight],
        [9, 0, 1, 7, p.slateLight],
        [-12, 7, 24, 16, p.steam],
        [-11, 8, 22, 14, p.paper],
      ]);
      // Sofa, and a plant once the day has begun.
      pen.rects(X.wait, FLOOR, [
        [-14, 0, 28, 2, p.slate],
        [-14, 2, 28, 4, p.panel],
        [-14, 6, 28, 6, p.pale],
        [-16, 2, 3, 8, p.panel],
        [13, 2, 3, 8, p.panel],
      ]);
      if (st >= 1) {
        pen.rects(W - 21, FLOOR, [
          [-3, 0, 6, 4, p.crate],
          [-1, 4, 2, 4, p.plant],
          [-4, 7, 3, 2, p.plantLight],
          [1, 8, 4, 2, p.plantLight],
          [-2, 10, 3, 2, p.plant],
        ]);
        note(W - 21, FLOOR - 40, 1);
      }
    },
    props(pen, crew) {
      const p = pen.palette;
      const T = pen.T;
      if (st >= 5) {
        // The server rack blinks.
        for (let n = 0; n < 5; n += 1) {
          pen.rect(
            at(577) - 12 + ((n * 5) % 3) * 8,
            FLOOR - 82 + n * 16,
            4,
            2,
            Math.floor(T * 3 + n * 1.7) % 3 ? p.screenOn : p.yellow,
          );
        }
      }
      pen.rect(clock - 1, 33, Math.floor(T) % 2 ? 7 : 5, 2, p.console);
      screenRows(
        pen,
        X.bash - 4 * U,
        FLOOR - 13.5 * U,
        Boolean(busy(crew, "bash")),
        4,
        8,
      );
      // A book slides out when someone reads.
      const shelfOn = Boolean(busy(crew, "read"));
      const out = shelfOn ? Math.floor(T * 1.3) % 6 : -1;
      pen.stamp(`books|${out}`, X.read, FLOOR, [-28, -80, 28, 0], (at) =>
        books(at).forEach((color, n) => {
          at.rects(0, 0, [
            [
              -6 + n * 2,
              13 + (n === out ? 1.5 : 0),
              2,
              5 - (n % 3 === 1 ? 1 : 0),
              color,
            ],
          ]);
        }),
      );
      // The page collects red marks under the lamp.
      pen.rects(X.edit, FLOOR, [
        [2, 14, 5, 2, busy(crew, "edit") ? p.yellow : p.slateLight],
      ]);
      const marked = Math.floor(marks);
      pen.stamp(`marks|${marked}`, X.edit, FLOOR, [-20, -40, 20, 0], (at) => {
        for (let n = 0; n < marked; n += 1)
          at.rects(0, 0, [[-4 + n * 1.5, 8.5, 1, 0.5, p.red]]);
      });
      const cx = X.search;
      const cy = FLOOR - 13.5 * U;
      pen.rect(cx - 12, cy - 12, 24, 24, p.blue);
      for (const [a, b, w, h] of [
        [-8, -8, 8, 6],
        [2, -2, 10, 8],
        [-10, 4, 6, 6],
      ]) {
        const nx = Math.max(-12, ((a + globe * 6 + 12) % 24) - 12);
        pen.rect(cx + nx, cy + b, Math.min(w, 12 - nx), h, p.green);
      }
      if (busy(crew, "search")) {
        pen.rect(cx + 10, cy + 8, 8, 2, p.yellow);
        pen.rect(cx + 16, cy + 8, 2, 8, p.yellow);
      }
      // Sheets stack up while replies come out.
      if (busy(crew, "write")) {
        const ph = (T * 1.6) % 1;
        pen.rects(X.write, FLOOR, [[-3, 7.5 - ph * 2 + 2, 6, 3, p.paper]]);
      }
      pen.stamp(`sheets|${sheets}`, X.write, FLOOR, [0, -48, 68, 0], (at) => {
        at.rects(0, 0, [[9, 0, 7, Math.max(1, sheets), p.paper]]);
        for (let n = 1; n < sheets; n += 1)
          at.rects(0, 0, [[9, n, 7, 0.25, p.line]]);
      });
      // A diagram gets drawn on the whiteboard while thinking.
      const segments: Rect[] = [
        [-9, 18, 5, 2, p.blue],
        [-4, 18.75, 3, 0.5, p.blue],
        [-1, 18, 5, 2, p.orange],
        [4, 18.75, 3, 0.5, p.orange],
        [7, 18, 3, 2, p.green],
        [-9, 12, 8, 1, p.steel],
        [-9, 10, 12, 1, p.steel],
        [4, 11, 6, 2, p.pink],
      ];
      const drawn = Math.ceil(strokes * segments.length);
      pen.stamp(`strokes|${drawn}`, X.think, FLOOR, [-40, -84, 44, 0], (at) =>
        at.rects(0, 0, segments.slice(0, drawn)),
      );
    },
    step(crew, dt) {
      if (busy(crew, "think")) strokes = Math.min(1, strokes + dt / 4);
      else strokes = Math.max(0, strokes - dt / 3);
      if (busy(crew, "edit")) marks = Math.min(5, marks + dt * 1.1);
      if (busy(crew, "write") && rand() < dt * 0.9)
        sheets = Math.min(10, sheets + 1);
      if (rand() < dt * 0.06) sheets = Math.max(0, sheets - 1);
      globe += dt * (busy(crew, "search") ? 2.4 : 0.2);
      stepPixels(fx, dt);
    },
    quiet: () => fx.length === 0 && (strokes === 0 || strokes === 1),
  };
}

// ---------- starship ----------

interface Star {
  x: number;
  y: number;
  v: number;
  s: number;
  o: number;
}

interface Beam {
  x: number;
  y: number;
  t: number;
  client: ClientType;
}

/**
 * What the observation window passes as the voyage goes on, by stage: a
 * planet's two colors and radius in 2 px cells, or a spiral without one.
 */
const PLANET_LOOK: ReadonlyArray<readonly [string, string, number]> = [
  ["#2a78d6", "#1baf7a", 7],
  ["#d5dbe6", "#aab4c4", 5],
  ["#e0704a", "#b85c3c", 6],
  ["#d9a66a", "#b8805a", 10],
  ["#eac38a", "#c9a26a", 8],
  ["#4d7fe0", "#3a66c4", 8],
  ["#e87ba4", "#6250d6", 0],
  ["#eda100", "#e87ba4", 0],
];

function paintPlanet(pen: Pen, stage: number) {
  const [a, c, r] = PLANET_LOOK[stage];
  if (!r) {
    for (let n = 0; n < 90; n += 1) {
      const t = (n / 90) * 12.6;
      const rr = 1 + n * 0.17;
      pen.rect(
        Math.round(32 + Math.cos(t) * rr * 2.4),
        Math.round(24 + Math.sin(t) * rr * 1.1),
        2,
        2,
        n % 3 ? a : c,
      );
    }
    pen.rect(29, 21, 6, 6, "#fff2b8");
    return;
  }
  for (let y = -r; y <= r; y += 1) {
    for (let x = -r; x <= r; x += 1) {
      if (x * x + y * y > r * r + r * 0.6) continue;
      const band =
        stage === 0
          ? ((x * 3 + y * 5) & 7) < 3
          : stage === 3 || stage === 4
            ? (y + 30) % 3 === 0
            : stage === 1 || stage === 2
              ? (x * 7 + y * 3) % 9 === 0
              : x + y > r * 0.6;
      pen.rect(32 + x * 2, 24 + y * 2, 2, 2, band ? c : a);
    }
  }
  if (stage === 4) {
    // Saturn's ring.
    for (let x = -15; x <= 15; x += 1) {
      if (Math.abs(x) > r - 2)
        pen.rect(32 + x * 2, 24 + Math.round(x * 0.25) * 2, 2, 2, "#b8956a");
    }
  }
}

function ship(W: number): CrewScene {
  const H = CREW_HEIGHT;
  const UP = 126;
  const LO = H - 34;
  const k = W / DEMO_WIDTH;
  const rooms: Record<CrewAct, readonly [number, number]> = {
    bash: [165, 0],
    search: [335, 0],
    write: [720, 0],
    read: [165, 1],
    edit: [345, 1],
    think: [690, 1],
    wait: [860, 1],
  };
  const R = Object.fromEntries(
    Object.entries(rooms).map(([act, [x, level]]) => [
      act,
      [Math.round(x * k), level] as const,
    ]),
  ) as Record<CrewAct, readonly [number, number]>;
  const ladder = Math.round(520 * k);
  const rand = seeded(13);
  const fx: Pixel[] = [];
  const steam: Pixel[] = [];
  const fresh: Fresh = [];
  let st = 0;
  let beams: Beam[] = [];
  const stars: Star[] = Array.from({ length: 60 }, () => ({
    x: rand() * W,
    y: rand() * H,
    v: 12 + rand() * 42,
    s: rand() < 0.25 ? 4 : 2,
    o: 0.14 + rand() * 0.32,
  }));
  const xl = 30;
  const xr = W - 56;
  const top = 12;
  const bottom = H - 16;
  // The observation window's glass, on the upper deck's wall.
  const pane = UP - 80;
  // The hold sits at the stern end of the nose.
  const hold = xr - 144;
  const NOSE = [
    [0, 36],
    [16, 62],
    [32, 84],
  ] as const;
  const note = (x: number, y: number, min: number) => {
    if (min === st) fresh.push([x, y]);
  };
  return {
    id: "ship",
    H,
    fresh,
    layout: {
      floors: [UP, LO],
      ladder,
      // The airlock is on the lower deck.
      door: 90,
      exit: 90,
      doorLevel: 1,
      spot: (act, slot) =>
        beside(
          R[act][0],
          slot,
          [xl + EDGE, xr - EDGE],
          [-52, 52, -98, 98, -144, 144, -190, 190, -236, 236],
          R[act][1],
        ),
    },
    stage: {
      hat: false,
      fx,
      perform(pen, member) {
        const ch = figureOf(member);
        const act = member.act;
        if (act === "bash" || act === "write") Act.type(pen, ch);
        else if (act === "read") Act.read(pen, ch);
        else if (act === "edit") Act.wrench(pen, ch, fx, rand);
        else if (act === "search") Act.scope(pen, ch);
        else if (act === "think") Act.think(pen, ch, false);
        else Act.sleep(pen, ch);
      },
    },
    grow(growth) {
      st = growth.stage;
      return String(st);
    },
    back(pen) {
      fresh.length = 0;
      pen.rect(0, 0, W, H, pen.palette.bg);
    },
    movers(pen) {
      const p = pen.palette;
      starsOf(
        pen,
        stars.map((star) => [
          Math.round(star.x / 2) * 2,
          star.y,
          star.s,
          star.o,
        ]),
      );
      // More engines and longer flames the further the voyage.
      const flick = Math.floor(pen.T * 12) % 3;
      const length = 10 + st * 2 + flick * 3;
      const engines =
        st >= 5 ? [-82, -38, 6, 50] : st >= 2 ? [-64, -14, 36] : [-48, 32];
      const mid = (top + bottom) / 2;
      for (const y of engines) {
        pen.stamp(`exhaust|${length}`, 0, mid + y, [-4, 0, 32, 16], (at) => {
          at.rect(28 - length, 0, length, 16, p.yellow);
          at.rect(28 - length * 0.7, 4, length * 0.7, 8, p.orange);
          at.rect(20, 4, 8, 8, p.flame);
        });
      }
    },
    front(pen) {
      const p = pen.palette;
      // Hull in stepped pixels, nose to the right; gilded at the end.
      const edge = st === CREW_TOP ? p.brass : p.pale;
      pen.rect(xl - 4, top - 4, xr - xl + 8, bottom - top + 8, edge);
      for (const [inset, ht] of NOSE) {
        pen.rect(xr + inset, top + ht - 4, 20, bottom - top - ht * 2 + 8, edge);
      }
      pen.rect(xl, top, xr - xl, bottom - top, p.hull);
      for (const [inset, ht] of NOSE) {
        pen.rect(xr + inset, top + ht, 16, bottom - top - ht * 2, p.hull);
      }
      // Decks and ladder.
      pen.rect(xl, UP, xr - xl, 16, p.deck);
      pen.rect(xl, UP + 4, xr - xl, 12, p.deckTop);
      pen.rect(xl, LO, xr - xl, 18, p.deck);
      pen.rect(xl, LO + 4, xr - xl, 14, p.deckTop);
      pen.rect(ladder - 26, UP - 44, 4, LO - UP + 44, p.slate);
      pen.rect(ladder + 22, UP - 44, 4, LO - UP + 44, p.slate);
      for (let y = UP - 40; y < LO; y += 14)
        pen.rect(ladder - 26, y, 52, 3, p.slate);
      pen.rect(ladder - 30, UP, 60, 16, p.hull);
      for (let y = UP; y < UP + 16; y += 14)
        pen.rect(ladder - 26, y, 52, 3, p.slate);
      pen.rect(ladder - 26, UP, 4, 16, p.slate);
      pen.rect(ladder + 22, UP, 4, 16, p.slate);
      // Airlock.
      pen.rects(90, LO, [
        [-6, 0, 12, 16, p.slateLight],
        [-5, 1, 10, 14, p.pale],
        [-0.5, 1, 1, 14, p.slate],
      ]);
      // Console.
      pen.rects(R.bash[0], UP, [
        [-9, 0, 18, 7, p.slate],
        [-10, 7, 20, 2, p.steel],
        [-7, 9, 14, 7, p.console],
        [-6, 10, 12, 5, p.screen],
      ]);
      // Window frame.
      const wx = R.search[0];
      pen.rect(wx - 26, pane - 4, 132, 60, p.slate);
      // Stripes go on the hull and cargo piles up in the hold as the day
      // goes on.
      if (st >= 3) {
        pen.rect(xl, top + 2, xr - xl, 4, st === CREW_TOP ? p.yellow : p.blue);
        note(Math.round(470 * k), top + 4, 3);
      }
      if (st >= 5) {
        pen.rect(xl, top + 8, xr - xl, 2, p.red);
        note(Math.round(620 * k), top + 8, 5);
      }
      for (let n = 0; n < st * 3; n += 1) {
        const cx = hold + (n % 7) * 20;
        const cy = LO - 16 * (1 + Math.floor(n / 7));
        pen.rect(cx, cy, 18, 15, n % 2 ? p.box : p.crate);
        pen.rect(cx, cy + 6, 18, 2, p.wood);
        if (n >= (st - 1) * 3) note(cx + 9, cy, st);
      }
      for (let x = hold - 6; x < xr - 4; x += 16) {
        pen.rect(x, LO - 2, 8, 2, p.yellow);
        pen.rect(x + 8, LO - 2, 8, 2, p.console);
      }
      // Telescope stand.
      pen.rects(wx, UP, [
        [-4, 0, 1, 3, p.steel],
        [3, 0, 1, 3, p.steel],
        [-3, 3, 7, 1, p.steel],
        [0, 4, 1, 4, p.steel],
      ]);
      // Transmitter.
      const tx = R.write[0];
      pen.rects(tx, UP, [
        [-9, 0, 18, 6, p.slate],
        [-6, 6, 12, 4, p.steel],
        [-7, 10, 14, 2, p.console],
        // The mast reaches the antenna on the roof.
        [-1, 12, 2, (UP - top - 14) / U - 12, p.slateLight],
      ]);
      pen.rect(tx - 4, 14, 8, 8, p.slateLight);
      // Archive cabinet, engine pipes, star map table.
      pen.rects(R.read[0], LO, [[-9, 0, 18, 16, p.panel]]);
      pen.rects(R.edit[0], LO, [
        [-12, 3, 24, 3, p.slateLight],
        [-12, 11, 24, 3, p.slateLight],
        [-3, 6, 6, 5, p.slate],
        [8, 0, 3, 14, p.steel],
      ]);
      pen.rects(R.think[0], LO, [
        [-7, 0, 14, 5, p.slate],
        [-9, 5, 18, 1, p.steel],
      ]);
    },
    props(pen, crew) {
      const p = pen.palette;
      const T = pen.T;
      const bashOn = Boolean(busy(crew, "bash"));
      screenRows(pen, R.bash[0] - 5 * U, UP - 14.5 * U, bashOn, 3, 10);
      const lights = [p.red, p.yellow, p.green, p.blue];
      const lit = lights.map((_, n) =>
        bashOn ? Math.floor(T * 4 + n) % 2 : n % 2,
      );
      pen.stamp(
        `lights|${lit.join("")}`,
        R.bash[0],
        UP,
        [-28, -20, 28, 0],
        (at) =>
          lights.forEach((color, n) =>
            at.rects(0, 0, [[-7 + n * 4, 3, 2, 2, lit[n] ? color : p.lampOff]]),
          ),
      );
      // The observation window shows how far the ship has got; the
      // telescope points out at the stars passing.
      const scopeOn = Boolean(busy(crew, "search"));
      const wx = R.search[0];
      pen.rect(wx - 22, pane, 124, 52, p.bg);
      pen.clip(wx - 22, pane, 124, 52, () => {
        starsOf(
          pen,
          stars
            .filter((_, n) => n % 3 === 0)
            .map((star) => [
              wx - 22 + (star.x % 124),
              pane + (star.y % 52),
              star.s,
              star.o + 0.2,
            ]),
        );
        pen.stamp(
          `planet|${st}`,
          wx + 40 - 32 + Math.round(Math.sin(T * 0.2) * 6),
          pane + 2,
          [0, 0, 64, 48],
          (at) => paintPlanet(at, st),
        );
        if (scopeOn)
          pixStar(pen, wx + 40 + Math.sin(T) * 34, pane + 14, 3, p.yellow);
      });
      pen.stamp("telescope", wx, UP, [-20, -64, 32, 0], (at) => {
        for (let n = 0; n < 5; n += 1) {
          at.rects(0, 0, [
            [-3 + n * 2, 7 + n, 3, 3, n < 2 ? p.console : p.slate],
          ]);
        }
      });
      // Replies leave through the roof as a beam.
      const writer = busy(crew, "write");
      const tx = R.write[0];
      if (writer) {
        pen.rect(
          tx - 7,
          0,
          14,
          UP - 12 * U,
          rgba(tintOf(p, writer.client).o, 0.22 + 0.1 * Math.sin(T * 8)),
        );
      }
      for (const beam of beams) {
        pen.rect(
          Math.round(beam.x),
          Math.round(beam.y),
          4,
          4,
          tintOf(p, beam.client).o,
        );
      }
      // Drawers open in the archive.
      const readOn = Boolean(busy(crew, "read"));
      const opened = readOn ? Math.floor(T * 1.4) % 9 : -1;
      pen.stamp(`drawers|${opened}`, R.read[0], LO, [-36, -68, 44, 0], (at) => {
        for (let r = 0; r < 3; r += 1) {
          for (let n = 0; n < 3; n += 1) {
            const open = opened === r * 3 + n;
            at.rects(0, 0, [
              [
                -8 + n * 6,
                1 + r * 5,
                4 + (open ? 2 : 0),
                3,
                open ? p.yellow : p.deckTop,
              ],
            ]);
          }
        }
      });
      // A leaking joint gets fixed with a wrench.
      const editOn = Boolean(busy(crew, "edit"));
      pen.rects(R.edit[0], LO, [
        [-2, 7, 4, 3, editOn && Math.floor(T * 5) % 2 ? p.yellow : p.deckTop],
      ]);
      if (editOn && pen.chance(rand, 5 / 60)) {
        steam.push({
          x: R.edit[0] + 28 + rand() * 10,
          y: LO - 62,
          vx: (rand() - 0.5) * 14,
          vy: -22,
          t: 0,
          life: 1,
          ink: "steam",
          s: 4,
        });
      }
      drawPixels(pen, steam);
      // A hologram turns over the star map.
      const mapOn = Boolean(busy(crew, "think"));
      const mx = R.think[0];
      const glow = mapOn ? 0.55 : 0.2;
      for (let n = 0; n < 8; n += 1) {
        const a = T * (mapOn ? 1.8 : 0.4) + n * 0.785;
        pen.rect(
          Math.round(mx + Math.cos(a) * 26),
          Math.round(LO - 58 + Math.sin(a) * 10 + ((n % 3) - 1) * 8),
          4,
          4,
          rgba(p.blue, glow * (0.5 + 0.5 * Math.sin(a))),
        );
      }
      pen.rect(mx - 28, LO - 72, 56, 48, rgba(p.blue, glow * 0.25));
    },
    step(crew, dt) {
      // Writing fast speeds the ship up.
      const fast = crew.members.some((member) => member.rate > 10);
      for (const star of stars) {
        star.x -= star.v * dt * (fast ? 1.8 : 1);
        if (star.x < 0) star.x += W;
      }
      for (const member of crew.members) {
        if (member.leaving || member.act !== "write" || !member.arrived)
          continue;
        if (rand() < dt * member.rate * 0.1) {
          beams.push({
            x: R.write[0] + (rand() - 0.5) * 8,
            y: 14,
            t: 0,
            client: member.client,
          });
        }
      }
      for (const beam of beams) {
        beam.y -= 90 * dt;
        beam.t += dt;
      }
      beams = beams.filter((beam) => beam.y > -8);
      stepPixels(steam, dt, -6);
      stepPixels(fx, dt);
    },
    quiet: () => fx.length === 0 && steam.length === 0 && beams.length === 0,
  };
}

// ---------- construction site ----------

/** Floors of the tower next to the crane when today's tokens top out. */
const MAX_FLOORS = 12;

function site(W: number): CrewScene {
  const FLOOR = GROUND;
  const H = CREW_HEIGHT;
  const X = spread(
    {
      bash: 95,
      read: 262,
      edit: 375,
      write: 545,
      search: 730,
      think: 835,
      wait: 935,
    },
    W,
  );
  const k = W / DEMO_WIDTH;
  const at = (x: number) => Math.round(x * k);
  const rand = seeded(14);
  const fx: Pixel[] = [];
  const fresh: Fresh = [];
  let st = 0;
  let floors = 0;
  let courses = 3;
  let bricks = 0;
  let dirt = 4;
  let nails = 0;
  let crane = 0;
  let cargo = 0;
  const cx = at(478);
  const jib = 44;
  const BX = at(622);
  const BW = 64;
  const driver = (crew: Crew) =>
    crew.members.find(
      (member) =>
        !member.leaving &&
        member.act === "bash" &&
        member.arrived &&
        member.spot?.drive,
    );
  return {
    id: "site",
    H,
    fresh,
    layout: {
      floors: [FLOOR],
      door: -40,
      exit: W + 60,
      spot(act, slot) {
        // The first one at Bash drives the excavator, the next stands by.
        if (act === "bash" && slot < 2) {
          const x = slot ? Math.max(EDGE, X.bash - 60) : X.bash;
          return { x, face: 1, level: 0, drive: !slot };
        }
        return beside(X[act], slot, [EDGE, W - EDGE]);
      },
      onDone() {
        cargo = 1;
      },
    },
    stage: {
      hat: true,
      fx,
      perform(pen, member) {
        const ch = figureOf(member, undefined, true);
        const act = member.act;
        if (act === "bash" && member.spot?.drive) return;
        if (act === "bash") Act.think(pen, ch, false);
        else if (act === "read") Act.blueprint(pen, ch);
        else if (act === "edit")
          Act.hammer(pen, ch, fx, rand, () => (nails = Math.min(8, nails + 1)));
        else if (act === "search") Act.scope(pen, ch);
        else if (act === "think") Act.think(pen, ch);
        else if (act === "wait") Act.coffee(pen, ch, fx, rand);
        else if (act === "write") carryBrick(pen, ch, member);
      },
    },
    grow(growth) {
      st = growth.stage;
      // Today's tokens put the tower up a floor at a time.
      floors = Math.round(
        (Math.min(growth.level, CREW_TOP) / CREW_TOP) * MAX_FLOORS,
      );
      return `${floors}/${st}`;
    },
    back(pen) {
      const p = pen.palette;
      fresh.length = 0;
      pen.rect(0, 0, W, H, p.bg);
      sparkle(pen, seeded(57), Math.round(44 * k), FLOOR - 70);
      // The neighbourhood fills in behind the site.
      [20, 880, 150, 760, 290, 960, 420].slice(0, 1 + st).forEach((x0, n) => {
        const x = at(x0);
        const h = 28 + ((n * 37) % 42);
        pen.rect(x, FLOOR - h, 60, h, p.hills);
        for (let y = FLOOR - h + 6; y < FLOOR - 6; y += 12) {
          for (let q = x + 6; q < x + 54; q += 14)
            pen.rect(q, y, 6, 6, p.windows);
        }
        if (n === st) note(x + 30, FLOOR - h, st);
      });
      // The tower: finished floors light up, the top two are still in
      // scaffolding.
      if (!floors) {
        pen.rect(BX - 8, FLOOR - 6, BW + 16, 6, p.footing);
        for (let x = BX - 8; x <= BX + BW + 4; x += 18)
          pen.rect(x, FLOOR - 16, 4, 16, p.plankEdge);
        pen.rect(BX - 8, FLOOR - 14, BW + 16, 2, p.plank);
      }
      for (let n = 0; n < floors; n += 1) {
        const y = FLOOR - (n + 1) * 11;
        const raw = n >= floors - 2 && floors < MAX_FLOORS;
        pen.rect(
          BX,
          y,
          BW,
          10,
          raw ? p.towerRaw : n % 2 ? p.tower : p.towerLight,
        );
        pen.rect(BX, y + 10, BW, 1, p.panel);
        for (let q = 0; q < 4; q += 1) {
          const window = raw
            ? p.towerRawWindow
            : (n * 3 + q * 5) % 7
              ? p.gold
              : p.slateLight;
          pen.rect(BX + 5 + q * 15, y + 3, 9, 5, window);
        }
        if (raw) {
          pen.rect(BX - 4, y, 2, 11, p.craneDark);
          pen.rect(BX + BW + 2, y, 2, 11, p.craneDark);
          pen.rect(BX - 4, y + 5, BW + 8, 1, p.craneDark);
        }
      }
      if (floors) note(BX + BW / 2, FLOOR - floors * 11, st);
      if (floors >= MAX_FLOORS) {
        // Topped out: a roof, a flag and bunting.
        const y = FLOOR - floors * 11;
        pen.rect(BX - 2, y - 4, BW + 4, 4, p.panel);
        pen.rect(BX + BW - 6, y - 24, 2, 20, p.steel);
        pen.rect(BX + BW - 4, y - 24, 14, 9, p.red);
        bunting(pen, BX - 30, BX + BW / 2, y + 2, 8, 0);
      }
      pen.rect(0, FLOOR, W, 4, p.siteLine);
      pen.rect(0, FLOOR + 4, W, H - FLOOR - 4, p.siteGround);
      const r = seeded(9);
      const cells = Math.round((252 * W) / DEMO_WIDTH);
      for (let n = 0; n < Math.round(90 * k); n += 1) {
        const color = rgba(p.speck, 0.1 + r() * 0.16);
        pen.rect(
          Math.floor(r() * cells) * 4,
          FLOOR + 8 + Math.floor((r() * 36) / 4) * 4,
          4,
          4,
          color,
        );
      }
      // Tower crane.
      pen.rect(cx - 4, jib, 8, FLOOR - jib, p.yellow);
      for (let y = jib; y < FLOOR; y += 12)
        pen.rect(cx - 4, y, 8, 3, p.craneDark);
      pen.rect(cx - 84, jib - 6, 280, 6, p.yellow);
      pen.rect(cx - 12, jib - 18, 24, 18, p.steel);
      pen.rect(cx - 8, jib - 14, 16, 8, p.glass);
      pen.rect(cx - 82, jib, 16, 12, p.slate);
      // Excavator.
      pen.rects(X.bash, FLOOR, [
        [-14, 0, 22, 3, p.steel],
        [-12, 3, 18, 3, p.yellow],
        [-11, 6, 9, 8, p.yellow],
        [-10, 7, 7, 5, p.glass],
      ]);
      // Blueprint table.
      table(pen, X.read, 22, 6, FLOOR);
      pen.rects(X.read, FLOOR, [
        [-8, 7, 16, 1, p.blueprint],
        [-6, 8, 2, 1, p.steel],
        [4, 8, 2, 1, p.steel],
      ]);
      // Trestles and a beam.
      pen.rects(X.edit, FLOOR, [
        [-12, 0, 2, 7, p.wood],
        [10, 0, 2, 7, p.wood],
        [-14, 7, 28, 3, p.plank],
        [-14, 7, 28, 1, p.plankEdge],
      ]);
      // The brick pile.
      pen.rects(X.write - 92, FLOOR, [
        [-6, 0, 12, 1, p.brick],
        [-5, 1, 10, 1, p.brickDark],
        [-4, 2, 8, 1, p.brick],
      ]);
      // Survey tripod.
      pen.rects(X.search, FLOOR, [
        [-5, 0, 1, 3, p.steel],
        [3, 0, 1, 3, p.steel],
        [-4, 3, 2, 4, p.steel],
        [2, 3, 2, 4, p.steel],
        [-1, 6, 2, 5, p.steel],
        [-3, 11, 7, 3, p.yellow],
        [-5, 12, 2, 1, p.console],
      ]);
      // Lunch crate.
      pen.rects(X.think, FLOOR, [
        [-5, 0, 10, 5, p.crate],
        [-5, 2, 10, 1, p.wood],
        [-2, 5, 5, 2, p.pink],
      ]);
      // Stacked girders.
      pen.rects(X.wait, FLOOR, [
        [-14, 0, 28, 1, p.steel],
        [-12, 1, 24, 2, p.slate],
        [-14, 3, 28, 1, p.steel],
        [-14, 4, 28, 1, p.slateLight],
      ]);
      // Barrier.
      for (let n = 0; n < 6; n += 1) {
        pen.rect(
          W - 36 + n * 6,
          FLOOR - 16,
          6,
          6,
          n % 2 ? p.yellow : p.console,
        );
      }
      pen.rects(W - 21, FLOOR, [[-1, 0, 1, 4, p.steel]]);
    },
    props(pen, crew) {
      const p = pen.palette;
      const T = pen.T;
      // The jib swings a beam over whenever a call finishes.
      const hook = cx + 40 + (Math.sin(crane) + 1) * 66;
      const rope = cargo > 0 ? 30 + (1 - cargo) * 60 : 26;
      pen.rect(hook - 1, jib, 2, rope, p.steel);
      pen.rect(hook - 3, jib + rope, 6, 4, p.steel);
      if (cargo > 0) pen.rect(hook - 18, jib + rope + 4, 36, 6, p.slate);
      // The driver works the arm and the dirt pile grows.
      const dig = driver(crew);
      const X0 = X.bash;
      const swing = dig ? Math.sin(((T * 0.9) % 1) * 6.28) : 0;
      if (dig) {
        const seated: Figure = {
          ...figureOf(dig, undefined, true),
          x: X0 - 26,
          fy: FLOOR - 19,
          u: smallU(dig.u),
          face: 1,
          dx: undefined,
        };
        body(pen, seated, { sit: true });
      } else {
        pen.rects(X0, FLOOR, [[-8, 7, 3, 2, p.pale]]);
      }
      const elbowX = X0 + 32;
      const elbowY = FLOOR - 62 + swing * 10;
      const tipX = elbowX + 22 + swing * 6;
      const tipY = elbowY + 34 - swing * 4;
      pen.stroke(
        [
          [X0 + 4, FLOOR - 26],
          [elbowX, elbowY],
          [tipX, tipY],
        ],
        8,
        p.craneDark,
      );
      pen.rect(Math.round(tipX - 8), Math.round(tipY), 18, 10, p.steel);
      const heap = Math.floor(dirt);
      pen.stamp(`dirt|${heap}`, X0 + 62, FLOOR, [-52, -40, 52, 0], (at) => {
        for (let n = 0; n < heap; n += 1) {
          const half = (heap - n) / 1.2;
          at.rects(0, 0, [
            [-half, n * 0.6, half * 2, 0.6, n % 2 ? p.dirt : p.dirtDark],
          ]);
        }
      });
      pen.stamp(`nails|${nails}`, X.edit, FLOOR, [-48, -48, 48, 0], (at) => {
        for (let n = 0; n < nails; n += 1)
          at.rects(0, 0, [[-11 + n * 3, 10, 1, 1, p.steel]]);
      });
      // The wall: one course a batch, plus the course in progress.
      const wx = X.write;
      pen.stamp(
        `wall|${courses}`,
        wx,
        FLOOR,
        [-48, -courses * 8 - 1, 56, 0],
        (at) => {
          for (let row = 0; row < courses; row += 1) {
            for (let n = 0; n < 8; n += 1) {
              at.rect(
                -48 + n * 12 + (row % 2 ? 6 : 0),
                -(row + 1) * 8,
                11,
                7,
                (n + row) % 3 ? p.brick : p.brickDark,
              );
            }
          }
        },
      );
      const odd = courses % 2 ? 6 : 0;
      pen.stamp(
        `course|${odd}|${bricks}`,
        wx,
        FLOOR - (courses + 1) * 8,
        [-48, 0, 56, 8],
        (at) => {
          for (let n = 0; n < bricks; n += 1)
            at.rect(-48 + n * 12 + odd, 0, 11, 7, p.brick);
        },
      );
      if (busy(crew, "search")) {
        pen.rect(X.search + 14, FLOOR - 13 * U, 80, 2, rgba(p.red, 0.45));
        pen.rect(X.search + 92, FLOOR - 13 * U - 2, 6, 6, p.red);
      }
    },
    step(crew, dt) {
      dirt = driver(crew)
        ? Math.min(14, dirt + dt * 0.8)
        : Math.max(4, dirt - dt * 0.05);
      crane += dt * 0.25;
      cargo = Math.max(0, cargo - dt / 4);
      stepPixels(fx, dt);
    },
    quiet: () => fx.length === 0 && cargo <= 0,
  };

  /** Fetches a brick from the pile, carries it over and sets it on the wall. */
  function carryBrick(pen: Pen, ch: Figure, member: CrewMember) {
    const pace = 0.28 + member.rate / 520;
    const ph = (member.t * pace) % 1;
    const near = member.x;
    const pile = member.x - 44 * (member.face > 0 ? 1 : -1);
    const walking = ph < 0.42 || (ph > 0.5 && ph < 0.92);
    const out =
      ph < 0.42
        ? ph / 0.42
        : ph < 0.5
          ? 1
          : ph < 0.92
            ? 1 - (ph - 0.5) / 0.42
            : 0;
    ch.dx = near + (pile - near) * out;
    const carrying = ph >= 0.46 && ph < 0.96;
    const face = ch.face;
    if (ph < 0.42) ch.face = face > 0 ? -1 : 1;
    body(pen, ch, { legs: walking ? Math.floor(member.t * 6) % 2 : -1 });
    ch.face = face;
    if (carrying) {
      const [hx, hy] = arm(pen, ch, 1, 5);
      pen.rect(Math.round(hx), Math.round(hy - 8), 16, 8, pen.palette.brick);
      pen.rect(
        Math.round(hx),
        Math.round(hy - 4),
        16,
        2,
        pen.palette.brickEdge,
      );
    }
    ch.dx = undefined;
    const beat = Math.floor(member.t * pace);
    if (ph > 0.94 && member.beat !== beat && pen.dt > 0) {
      member.beat = beat;
      bricks += 1;
      if (bricks >= 8) {
        bricks = 0;
        courses = courses >= 15 ? 3 : courses + 1;
      }
      spawn(
        fx,
        rand,
        X.write + bricks * 12 - 44,
        FLOOR - courses * 12 - 6,
        4,
        "brick",
        40,
        20,
        0.35,
        3,
      );
    }
  }

  function note(x: number, y: number, min: number) {
    if (min === st) fresh.push([x, y]);
  }
}

const BUILDERS: Record<CrewSceneId, (W: number) => CrewScene> = {
  camp,
  office,
  ship,
  site,
};

/** A scene laid out for a logical width `W`. */
export function createScene(id: CrewSceneId, W: number): CrewScene {
  return BUILDERS[id](W);
}
