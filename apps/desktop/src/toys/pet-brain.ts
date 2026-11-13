// The big desktop pet's mind: one girl who floats wherever you leave her,
// over every window and full-screen app. Every conversation in progress is an
// orb beside her; she does what the busiest one does, says what happened in a
// bubble, dozes off when nothing happens, and can be patted, poked, picked up
// and thrown.
// Everything here is plain state stepped by the window's loop on its own
// clock, so tests drive it the same way.
import type { Pixel } from "../live-crew-draw";
import { CREW_STAGES, crewGrowth, type CrewMember } from "../live-crew-model";
import type { ClientType } from "../request-record-model";
import { gearAt, type Gear } from "./pet-gear";
import { hasKit, kitOf, kitSize, type Face, type Kit } from "./pet-kit";
import { cfg, motion } from "./toy-config";
import type { IdleReading } from "./toy-feed";
import type { Frames } from "./toy-frames";
import { clamp, live, pace, puff, waving } from "./toy-kit";

/** The orbs are drawn half again as big as on the small pets. */
export const ORB_SCALE = 1.5;
/** How fast a throw slows down in the air, per second. */
const DRAG = 3.2;
/** Below this speed a throw is over and she floats where she is. */
const REST = 40;
/** Points kept between her and the screen's edges. */
const MARGIN = 6;
/** Seconds without input, with nothing to do, before she dozes off. */
export const DOZE_AFTER = 30;
/** The order she picks what to do in when several conversations work. */
const RANK: Record<string, number> = {
  write: 0,
  bash: 1,
  edit: 2,
  read: 3,
  search: 4,
  think: 5,
  wait: 6,
  leave: 7,
};

/** What a bubble says; the window puts it in words. */
export type Say =
  | { kind: "reply"; client: ClientType }
  | { kind: "failover"; client: ClientType; model: string }
  | { kind: "milestone"; tokens: number; gear: Gear | null }
  | {
      kind: "back";
      seconds: number;
      replies: number;
      tokens: number | null;
      waiting: number;
    }
  | { kind: "greet"; replies: number; tokens: number | null; pats: number }
  | { kind: "poked" };

export interface Bubble {
  say: Say;
  /** Seconds it stays. */
  life: number;
}

export interface Orb {
  id: string;
  x: number;
  y: number;
  model: string;
  /** Fades in after a model change, 0 → 1. */
  in: number;
  /** Seconds left in her hands after its reply. */
  hold: number;
}

export interface HitRect {
  x: number;
  y: number;
  w: number;
  h: number;
  click?: boolean;
}

/** Today's counts and where she floats, kept across restarts of the window. */
/** Who she is before any client with a girl of its own has worked. */
export const FIRST_GIRL: ClientType = "deepseek_harness";

export interface PetMemo {
  day: string;
  replies: number;
  pats: number;
  quiet: boolean;
  client: ClientType;
  /** Her spot as shares of the screen's width and height, once you move her. */
  spot?: { x: number; y: number };
}

/** Floating where you left her, in your hand, or still gliding from a throw. */
type Mode = "float" | "held" | "fly";

export interface PetOptions {
  /** The window's size in points. */
  W: number;
  H: number;
  /** Conversations in progress, oldest first. */
  members?: () => CrewMember[];
  /** Today's tokens, or null before the first read. */
  tokens?: () => number | null;
  /** Wall clock in ms, for the day and time away. */
  clock?: () => number;
  rand?: () => number;
  /** Where dust and sparks around her and around the orbs go. */
  fx?: { pet: Pixel[]; orbs: Pixel[] };
  memo?: PetMemo | null;
}

const isShare = (v: unknown): v is number =>
  typeof v === "number" && Number.isFinite(v) && v >= 0 && v <= 1;

/** Today's counts as stored, or null if they are not. */
export function parseMemo(raw: string | null): PetMemo | null {
  try {
    const v = raw ? (JSON.parse(raw) as Partial<PetMemo> | null) : null;
    if (
      !v ||
      typeof v.day !== "string" ||
      typeof v.replies !== "number" ||
      typeof v.pats !== "number" ||
      typeof v.quiet !== "boolean" ||
      typeof v.client !== "string" ||
      !hasKit(v.client)
    )
      return null;
    const memo: PetMemo = {
      day: v.day,
      replies: v.replies,
      pats: v.pats,
      quiet: v.quiet,
      client: v.client,
    };
    const spot = v.spot as Partial<PetMemo["spot"]> | undefined;
    if (spot && isShare(spot.x) && isShare(spot.y))
      memo.spot = { x: spot.x, y: spot.y };
    return memo;
  } catch {
    return null;
  }
}

/** The local day of `ms`, for today's counts. */
export function dayOf(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
}

export class PetBrain {
  W: number;
  H: number;
  members: () => CrewMember[];
  tokens: () => number | null;
  clock: () => number;
  rand: () => number;
  fx: { pet: Pixel[]; orbs: Pixel[] };
  memo: PetMemo;

  frames: Frames;
  /** Real frames have come in and she has found her spot. */
  placed = false;
  client: ClientType = FIRST_GIRL;
  x = 0;
  /** Her foot line: where her soles are. */
  foot = 0;
  mode: Mode = "float";
  face: 1 | -1 = -1;
  vx = 0;
  vy = 0;
  /** Where her near hand was last drawn. */
  nearHand: { x: number; y: number } | null = null;
  /** Which way she faced last frame. */
  shown: 1 | -1 = -1;
  /** Seconds on her own clock. */
  now = 0;
  t = 0;
  blinkAt = 2.5;
  blink = 0;
  /** A little flourish now and then while she floats with nothing to do. */
  whim: { face: Face; t: number } | null = null;
  whimAt = 9;
  pointer: { x: number; y: number } | null = null;
  movedAt = 0;
  /** The host's last idle reading and when it came. */
  idleFor: number | null = null;
  idleAt = 0;
  turns: number[] = [];
  dir = 0;
  pat = 0;
  patCounted = false;
  heartAt = 0;
  hearts: Array<{ x: number; y: number; t: number }> = [];
  blush = 0;
  hop = 0;
  hopBig = false;
  bang = 0;
  pokes: number[] = [];
  pout = 0;
  dizzy = 0;
  wave = 0;
  stretch = 0;
  sweat = 0;
  sleeping = false;
  /** Asleep because nothing happened for a while, not because you left. */
  dozing = false;
  /** Wall-clock ms you left at; 0 while you are here. */
  awaySince = 0;
  awayReplies = 0;
  awayTokens: number | null = null;
  orbs = new Map<string, Orb>();
  seen = new Map<string, { wave: boolean; slip: boolean }>();
  bubbles: Bubble[] = [];
  bubbleT = 0;
  /** The mouse rests on the bubble: it stays. */
  bubbleHeld = false;
  press: { x: number; y: number } | null = null;
  grab: {
    dx: number;
    dy: number;
    samples: Array<{ x: number; y: number; t: number }>;
  } | null = null;

  constructor(o: PetOptions) {
    this.W = o.W;
    this.H = o.H;
    this.members = o.members ?? live;
    this.tokens = o.tokens ?? (() => null);
    this.clock = o.clock ?? Date.now;
    this.rand = o.rand ?? pace.rand;
    this.fx = o.fx ?? { pet: [], orbs: [] };
    const day = dayOf(this.clock());
    this.memo =
      o.memo && hasKit(o.memo.client)
        ? { ...o.memo }
        : { day, replies: 0, pats: 0, quiet: false, client: FIRST_GIRL };
    this.client = this.memo.client;
    this.frames = {
      wins: [],
      dock: null,
      floor: o.H - 2,
      top: 24,
      full: false,
    };
    this.place();
  }

  // ---------- where she is ----------

  setFrames(next: Frames) {
    this.frames = next;
    if (this.placed) {
      this.keepOn();
      return;
    }
    // The first look at the screen: back to her spot, or above the Dock.
    this.placed = true;
    this.place();
  }

  /** Back where you left her, or at the right above the Dock. */
  place() {
    const spot = this.memo.spot;
    if (spot) {
      this.x = spot.x * this.W;
      this.foot = spot.y * this.H;
    } else {
      const f = this.frames;
      this.x = this.W - 200;
      this.foot = (f.dock ? f.dock.y : f.floor) - 40;
    }
    this.mode = "float";
    this.vx = 0;
    this.vy = 0;
    this.keepOn();
  }

  /** The box her foot line stays in: her whole body on the screen, under the menu bar. */
  room() {
    const { tall, half } = this.shape();
    return {
      x0: half + MARGIN,
      x1: this.W - half - MARGIN,
      y0: this.frames.top + tall + MARGIN,
      y1: this.H - MARGIN,
    };
  }

  keepOn() {
    const r = this.room();
    this.x = clamp(this.x, r.x0, r.x1);
    this.foot = clamp(this.foot, r.y0, r.y1);
  }

  /**
   * The window took a new size. Out of your hand she keeps her spot as
   * shares of it, as after a restart; in your hand the mouse places her.
   */
  resize(W: number, H: number) {
    if (W === this.W && H === this.H) return;
    if (!this.grab) {
      this.x = (this.x / this.W) * W;
      this.foot = (this.foot / this.H) * H;
    }
    this.W = W;
    this.H = H;
    if (!this.grab) this.keepOn();
  }

  /**
   * Her window followed her onto another screen, `dx`, `dy` points from
   * where it was: she, her orbs and her hearts stay where they were on the
   * glass; the sparks go.
   */
  carried(dx: number, dy: number) {
    const move = (p: { x: number; y: number } | null) => {
      if (!p) return;
      p.x += dx;
      p.y += dy;
    };
    this.x += dx;
    this.foot += dy;
    move(this.press);
    move(this.pointer);
    this.grab?.samples.forEach(move);
    this.orbs.forEach(move);
    this.hearts.forEach(move);
    this.nearHand = null;
    this.fx.pet.length = 0;
    this.fx.orbs.length = 0;
  }

  /** She floats where she is now, and keeps the spot across restarts. */
  settle() {
    this.mode = "float";
    this.vx = 0;
    this.vy = 0;
    this.keepOn();
    this.memo.spot = {
      x: clamp(this.x / this.W),
      y: clamp(this.foot / this.H),
    };
  }

  /** Her foot line, on whole points. */
  feet() {
    return Math.round(this.foot);
  }

  top(foot: number) {
    return foot - this.shape().tall;
  }

  /** Her drawn picture. */
  kit(): Kit {
    return kitOf(this.client) ?? kitOf(FIRST_GIRL)!;
  }

  /** Her height in px from the top of her head to her soles, and half her width. */
  shape() {
    return kitSize(this.kit());
  }

  half() {
    return this.shape().half;
  }

  /** The busiest conversation: she does what it does. */
  lead(): CrewMember | null {
    let best: CrewMember | null = null;
    for (const m of this.members())
      if (!best || (RANK[m.act] ?? 9) < (RANK[best.act] ?? 9)) best = m;
    return best;
  }

  /** Floating where you left her: not in your hand, not flying from a throw. */
  floating() {
    return this.mode === "float";
  }

  /** Something other than work has her attention. */
  busyFace() {
    return (
      this.sleeping ||
      this.dizzy > 0 ||
      this.pout > 0 ||
      this.pat > 0 ||
      this.wave > 0 ||
      this.stretch > 0
    );
  }

  hitBody(x: number, y: number) {
    const foot = this.feet();
    return (
      Math.abs(x - this.x) < this.half() - 6 &&
      y > this.top(foot) - 6 &&
      y < foot + 4
    );
  }

  hitHead(x: number, y: number) {
    const top = this.top(this.feet());
    return this.hitBody(x, y) && y < top + 0.42 * this.shape().tall;
  }

  /** The parts of the window that take the mouse: her body. */
  hits(): HitRect[] {
    const foot = this.feet();
    const top = this.top(foot) - 6;
    const half = this.half() - 6;
    return [
      {
        x: Math.round(this.x - half),
        y: Math.round(top),
        w: Math.round(2 * half),
        h: Math.round(foot + 4 - top),
        click: true,
      },
    ];
  }

  /** Today's tokens' stage: what she wears. */
  stage() {
    return crewGrowth(this.tokens()).stage;
  }

  // ---------- events ----------

  /** Queues a bubble; `now` puts it up at once, for her own reactions. */
  say(say: Say, life: number, now = false) {
    if (this.memo.quiet || this.away()) return;
    const key = (s: Say) => `${s.kind}|${"client" in s ? s.client : ""}`;
    if (now || (this.bubbles[0] && key(this.bubbles[0].say) === key(say)))
      this.bubbleT = 0;
    this.bubbles = this.bubbles.filter((b) => key(b.say) !== key(say));
    if (now) this.bubbles.unshift({ say, life });
    else this.bubbles.push({ say, life });
    if (this.bubbles.length > 3) this.bubbles.splice(now ? 3 : 1, 1);
  }

  /** Starts a new day's counts once the date changes. */
  roll() {
    const day = dayOf(this.clock());
    if (day !== this.memo.day)
      this.memo = { ...this.memo, day, replies: 0, pats: 0 };
  }

  countPat() {
    this.roll();
    this.memo.pats += 1;
  }

  reply(m: CrewMember) {
    this.roll();
    this.memo.replies += 1;
    if (this.away()) {
      this.awayReplies += 1;
      return;
    }
    if (!cfg.prefs.pet.reply) return;
    const o = this.orbs.get(m.id);
    if (o) o.hold = 2.6;
    this.wave = Math.min(4, motion().wave);
    this.say({ kind: "reply", client: m.client }, 5);
  }

  failover(m: CrewMember) {
    if (!cfg.prefs.pet.failover || this.away()) return;
    this.sweat = 3;
    const o = this.orbs.get(m.id);
    if (o) {
      puff(
        this.fx.orbs,
        this.rand,
        o.x / ORB_SCALE,
        o.y / ORB_SCALE,
        14,
        { client: m.client },
        90,
        50,
        0.6,
        2,
      );
      o.in = 0;
      o.model = m.model;
    }
    this.say({ kind: "failover", client: m.client, model: m.model }, 5);
  }

  /** Today's tokens passed `stage`: a little hop, hands clasped. */
  milestone(stage: number) {
    if (this.away()) return;
    this.hop = 1;
    this.hopBig = true;
    const top = this.top(this.feet());
    for (const ink of [
      "blue",
      "orange",
      "green",
      "yellow",
      "pink",
      "violet",
    ] as const)
      puff(this.fx.pet, this.rand, this.x, top + 10, 8, ink, 300, 260, 1.6, 4);
    this.say(
      {
        kind: "milestone",
        tokens: CREW_STAGES[stage] ?? 0,
        gear: gearAt(stage),
      },
      5,
    );
  }

  away() {
    return this.awaySince > 0;
  }

  /** You stepped away: she falls asleep and keeps count. */
  leave(idleSeconds: number) {
    this.awaySince = this.clock() - idleSeconds * 1000;
    this.awayReplies = 0;
    this.awayTokens = this.tokens();
    this.sleeping = true;
    this.dozing = false;
    this.bubbles = [];
  }

  /** You are back: a stretch and what happened meanwhile. */
  back() {
    if (!this.away()) return;
    const seconds = (this.clock() - this.awaySince) / 1000;
    this.awaySince = 0;
    this.sleeping = false;
    this.dozing = false;
    this.stretch = 1.2;
    const now = this.tokens();
    const was = this.awayTokens;
    this.say(
      {
        kind: "back",
        seconds,
        replies: this.awayReplies,
        // A new day starts the count over: nothing to compare with.
        tokens: now !== null && was !== null && now >= was ? now - was : null,
        waiting: this.members().filter((m) => m.act === "wait").length,
      },
      7,
    );
  }

  /**
   * The host says how long the keyboard and mouse have been still, and how
   * long makes you away: longer while the AI works and you may be watching.
   */
  idle(reading: IdleReading) {
    this.idleFor = reading.idle;
    this.idleAt = this.now;
    if (this.away()) {
      if (reading.idle < 2) this.back();
      return;
    }
    if (reading.idle < 1) this.wake();
    if (reading.idle >= reading.away && this.floating())
      this.leave(reading.idle);
  }

  /** Seconds since the last key or mouse input she knows of. */
  stillFor() {
    const moved = this.now - this.movedAt;
    if (this.idleFor === null) return moved;
    return Math.min(moved, this.idleFor + (this.now - this.idleAt));
  }

  /** Woken from a doze by you, not by work: no stretch. */
  wake() {
    if (!this.dozing) return;
    this.dozing = false;
    this.sleeping = false;
  }

  greet() {
    this.roll();
    this.hop = 0;
    this.bang = 0;
    this.pokes = [];
    this.wave = 2.4;
    this.say(
      {
        kind: "greet",
        replies: this.memo.replies,
        tokens: this.tokens(),
        pats: this.memo.pats,
      },
      6,
    );
  }

  poke() {
    if (this.pout > 0) return;
    this.pokes = this.pokes.filter((t) => this.now - t < 3).concat(this.now);
    if (this.pokes.length >= 5) {
      this.pokes = [];
      this.pout = 3.2;
      this.say({ kind: "poked" }, 2.4, true);
      return;
    }
    this.hop = 1;
    this.hopBig = false;
    this.bang = 0.7;
  }

  /** Patted from the menu. */
  patHead() {
    this.pat = 1.8;
    this.patCounted = true;
    this.countPat();
  }

  toggleQuiet() {
    this.memo.quiet = !this.memo.quiet;
    if (this.memo.quiet) this.bubbles = [];
  }

  setClient(next: ClientType) {
    if (next === this.client) return;
    const top = this.top(this.feet());
    puff(
      this.fx.pet,
      this.rand,
      this.x,
      top + 80,
      30,
      { client: next },
      200,
      120,
      0.8,
      4,
    );
    this.client = next;
    this.memo.client = next;
  }

  /**
   * She is the girl chosen in settings, or else dresses as the client with
   * the most conversations going. On a tie, or while only clients without a
   * girl of their own work, she stays as she is.
   */
  chooseClient() {
    const chosen = cfg.prefs.pet.character;
    let best: ClientType | null = chosen === "auto" ? null : chosen;
    if (!best) {
      const counts = new Map<ClientType, number>();
      for (const m of this.members())
        if (hasKit(m.client))
          counts.set(m.client, (counts.get(m.client) ?? 0) + 1);
      let most = 0;
      for (const [client, n] of counts)
        if (n > most || (n === most && client === this.client)) {
          best = client;
          most = n;
        }
    }
    if (best && this.mode !== "held" && this.mode !== "fly")
      this.setClient(best);
  }

  // ---------- input ----------

  /** Where the mouse is, from the host; null once it leaves the screen. */
  point(p: { x: number; y: number } | null) {
    const was = this.pointer;
    this.pointer = p;
    if (!p) return;
    if (!was || was.x !== p.x || was.y !== p.y) {
      this.movedAt = this.now;
      if (this.away()) this.back();
      else this.wake();
    }
    if (this.grab || !was || this.mode === "fly" || this.mode === "held")
      return;
    // Stroking her head back and forth is a pat.
    if (!this.hitHead(p.x, p.y)) return;
    const d = Math.sign(p.x - was.x);
    if (Math.abs(p.x - was.x) >= 2 && d !== this.dir) {
      if (this.dir !== 0) this.turns.push(this.now);
      this.dir = d;
    }
    this.turns = this.turns.filter((t) => this.now - t < 1.2);
    if (this.turns.length >= 3 && this.pout <= 0 && !this.sleeping) {
      this.pat = 1.4;
      if (!this.patCounted) {
        this.countPat();
        this.patCounted = true;
      }
    }
  }

  /** A button went down at (x, y); true if it landed on her. */
  pressAt(x: number, y: number) {
    if (!this.hitBody(x, y)) return false;
    this.press = { x, y };
    return true;
  }

  dragTo(x: number, y: number) {
    this.pointer = { x, y };
    if (this.press && !this.grab && cfg.prefs.pet.grab) {
      if (Math.hypot(x - this.press.x, y - this.press.y) > 5) {
        const foot = this.feet();
        this.grab = { dx: this.x - x, dy: foot - y, samples: [] };
        this.mode = "held";
        this.foot = foot;
        this.sleeping = false;
        this.dozing = false;
      }
    }
    if (!this.grab) return;
    this.grab.samples.push({ x, y, t: this.now });
    this.grab.samples = this.grab.samples.filter((s) => this.now - s.t < 0.08);
    const r = this.room();
    this.x = clamp(x + this.grab.dx, r.x0, r.x1);
    this.foot = clamp(y + this.grab.dy, r.y0, r.y1);
  }

  /** The button came up: let go where she is, a throw, or a poke if it never moved. */
  releaseAt(x: number, y: number) {
    if (this.grab) {
      // Only the last moment counts: held still before letting go, no throw.
      const s = this.grab.samples.filter((o) => this.now - o.t < 0.08);
      const first = s[0];
      const last = s.at(-1);
      const dt = first && last ? Math.max(0.016, last.t - first.t) : 1;
      this.vx = first && last ? clamp((last.x - first.x) / dt, -1600, 1600) : 0;
      this.vy = first && last ? clamp((last.y - first.y) / dt, -1600, 1600) : 0;
      this.grab = null;
      if (Math.hypot(this.vx, this.vy) < 2 * REST) this.settle();
      else this.mode = "fly";
    } else if (this.press && this.hitBody(x, y)) {
      if (this.sleeping) {
        this.sleeping = false;
        this.dozing = false;
        if (this.away()) this.back();
        else this.stretch = 1.2;
      } else this.poke();
    }
    this.press = null;
  }

  /** The window lost the mouse mid-drag: she stays where she is. */
  cancel() {
    if (this.grab) {
      this.grab = null;
      this.settle();
    }
    this.press = null;
  }

  /** Floating with nothing to do, she hums with a little hop or lights up. */
  private stepWhim(dt: number) {
    if (this.whim) {
      this.whim.t -= dt;
      if (this.whim.t <= 0) this.whim = null;
      return;
    }
    const calm =
      this.floating() &&
      !this.busyFace() &&
      this.hop <= 0 &&
      (this.lead()?.act ?? "wait") === "wait";
    if (!calm) return;
    this.whimAt -= dt;
    if (this.whimAt > 0) return;
    this.whimAt = 8 + this.rand() * 8;
    if (this.rand() < 0.5) {
      this.whim = { face: "happy", t: 1.4 };
      this.hop = 1;
      this.hopBig = false;
    } else this.whim = { face: "sparkle", t: 1.6 };
  }

  // ---------- step ----------

  watch() {
    const ms = this.members();
    for (const m of ms) {
      const was = this.seen.get(m.id);
      const next = { wave: waving(m), slip: m.slip > 0 };
      if (was && next.wave && !was.wave) this.reply(m);
      if (was && next.slip && !was.slip) this.failover(m);
      this.seen.set(m.id, next);
    }
    for (const id of this.seen.keys())
      if (!ms.some((m) => m.id === id)) this.seen.delete(id);
  }

  step(dt: number) {
    this.now += dt;
    this.t += dt;
    this.watch();
    this.chooseClient();
    for (const k of [
      "pat",
      "bang",
      "pout",
      "dizzy",
      "wave",
      "stretch",
      "sweat",
    ] as const)
      this[k] = Math.max(0, this[k] - dt);
    if (this.pat <= 0) this.patCounted = false;
    this.hop = Math.max(0, this.hop - dt * (this.hopBig ? 1.1 : 2.2));
    this.blinkAt -= dt;
    if (this.blinkAt <= 0) {
      this.blink = 0.13;
      this.blinkAt = 2.5 + this.rand() * 3.5;
    }
    this.blink = Math.max(0, this.blink - dt);
    this.stepWhim(dt);
    this.blush +=
      ((this.pat > 0 ? 1 : this.mode === "held" ? 0.45 : 0) - this.blush) *
      clamp(dt * 5);
    // Hearts float up while she is patted.
    this.heartAt -= dt;
    if (this.pat > 0 && this.heartAt <= 0) {
      this.heartAt = 0.32;
      const top = this.top(this.feet());
      this.hearts.push({
        x: this.x + (this.rand() - 0.5) * 80,
        y: top + 10,
        t: 0,
      });
    }
    for (const h of this.hearts) {
      h.t += dt;
      h.y -= 34 * dt;
    }
    this.hearts = this.hearts.filter((h) => h.t < 1.4);

    // Nothing to do and nobody around: she dozes off.
    const idle = (this.lead()?.act ?? "wait") === "wait";
    if (
      idle &&
      !this.sleeping &&
      this.floating() &&
      !this.busyFace() &&
      this.stillFor() > DOZE_AFTER
    ) {
      this.sleeping = true;
      this.dozing = true;
    }
    // Work starts again: she wakes and stretches.
    if (this.dozing && !idle) {
      this.dozing = false;
      this.sleeping = false;
      this.stretch = 1.2;
    }
    if (this.mode === "fly") this.fly(dt);
    this.stepOrbs(dt);
    if (this.bubbles[0]) this.bubbleT += dt;
    if (
      this.bubbles[0] &&
      this.bubbleT > this.bubbles[0].life &&
      !this.bubbleHeld
    ) {
      this.bubbles.shift();
      this.bubbleT = 0;
    }
  }

  /** Gliding from a throw: she slows down, bounces off the screen's edges and stops. */
  fly(dt: number) {
    const k = Math.exp(-DRAG * dt);
    this.vx *= k;
    this.vy *= k;
    this.x += this.vx * dt;
    this.foot += this.vy * dt;
    const r = this.room();
    let hit = 0;
    if (this.x < r.x0 || this.x > r.x1) {
      hit = Math.max(hit, Math.abs(this.vx));
      this.x = clamp(this.x, r.x0, r.x1);
      this.vx *= -0.45;
    }
    if (this.foot < r.y0 || this.foot > r.y1) {
      hit = Math.max(hit, Math.abs(this.vy));
      this.foot = clamp(this.foot, r.y0, r.y1);
      this.vy *= -0.45;
    }
    if (hit > 0) {
      const hard = hit > 700;
      puff(
        this.fx.pet,
        this.rand,
        this.x,
        this.foot - this.shape().tall / 2,
        hard ? 16 : 8,
        "muted",
        160,
        50,
        0.5,
        4,
      );
      if (hard) this.dizzy = 2.6;
    }
    if (Math.hypot(this.vx, this.vy) < REST) this.settle();
  }

  /**
   * An orb's spot beside her head: left and right, stacked upward. Near a
   * screen edge it goes to her other side, a row lower, so it stays on screen.
   */
  slot(i: number, top: number) {
    let side = i % 2 ? 1 : -1;
    const row = Math.floor(i / 2);
    const reach = this.half() + 58 + row * 14;
    let y = top + 52 - row * 50;
    const x = this.x + side * reach;
    if (x < 24 || x > this.W - 24) {
      side = -side;
      y += 50;
    }
    return {
      x: this.x + side * reach,
      y: Math.max(this.frames.top + 24, y),
    };
  }

  /** Where an orb she holds up floats: past her waving hand, clear of her hair. */
  hand(top: number) {
    const y = this.nearHand?.y ?? top + 88;
    return { x: this.x + this.shown * (this.half() + 22), y: y - 8 };
  }

  stepOrbs(dt: number) {
    const ms = this.members();
    const top = this.top(this.feet());
    ms.forEach((m, i) => {
      let o = this.orbs.get(m.id);
      const at = this.slot(i, top);
      if (!o) {
        o = {
          id: m.id,
          x: at.x,
          y: at.y - 120,
          model: m.model,
          in: 0,
          hold: 0,
        };
        this.orbs.set(m.id, o);
      }
      if (o.model !== m.model && m.slip <= 0) {
        o.model = m.model;
        o.in = 0;
      }
      o.in = Math.min(1, o.in + dt * 1.4);
      o.hold = Math.max(0, o.hold - dt);
      const to = o.hold > 0 ? this.hand(top) : at;
      const k = clamp(dt * (o.hold > 0 ? 7 : 5));
      o.x += (to.x - o.x) * k;
      o.y += (to.y - o.y) * k;
    });
    for (const [id, o] of this.orbs)
      if (!ms.some((m) => m.id === id)) {
        puff(
          this.fx.orbs,
          this.rand,
          o.x / ORB_SCALE,
          o.y / ORB_SCALE,
          10,
          "muted",
          60,
          30,
          0.5,
          2,
        );
        this.orbs.delete(id);
      }
  }

  /** Something moves fast enough to need every frame. */
  busy() {
    return (
      this.whim !== null ||
      this.mode !== "float" ||
      this.grab !== null ||
      this.hop > 0 ||
      this.bang > 0 ||
      this.pat > 0 ||
      this.wave > 0 ||
      this.stretch > 0 ||
      this.dizzy > 0 ||
      this.hearts.length > 0 ||
      this.fx.pet.length > 0 ||
      this.fx.orbs.length > 0 ||
      [...this.orbs.values()].some((o) => o.in < 1 || o.hold > 0)
    );
  }
}
