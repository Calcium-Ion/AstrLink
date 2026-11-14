import { afterEach, describe, expect, it, vi } from "vitest";

import { seededRandom, type CrewMember } from "../live-crew-model";
import {
  DOZE_AFTER,
  PetBrain,
  dayOf,
  parseMemo,
  type PetMemo,
  type Say,
} from "./pet-brain";
import { actOf, faceOf, moodOf } from "./pet-draw";
import { badgesOf, gearAt } from "./pet-gear";
import { build, kitOf, kitPx, onScreen } from "./pet-kit";
import { petWords } from "./pet-words";
import { cfg } from "./toy-config";
import { readIdle, readScreenMove } from "./toy-feed";
import type { Frames } from "./toy-frames";
import { defaultDesktopToys } from "../preferences-model";

const W = 1440;
const H = 900;
const DAY = new Date(2026, 10, 8, 10, 0, 0).getTime();

function member(over: Partial<CrewMember> = {}): CrewMember {
  return {
    id: "a",
    client: "claude_code",
    model: "claude-opus-5-5",
    orb: null,
    order: 0,
    act: "write",
    tool: "",
    count: 1,
    rate: 80,
    shown: 10,
    waiting: false,
    queue: [],
    done: false,
    jump: 0,
    slip: 0,
    spot: null,
    arrived: true,
    lastKey: "",
    reading: "write",
    clones: [],
    leaving: false,
    alpha: 1,
    x: 100,
    level: 0,
    fy: 0,
    face: 1,
    walk: 0,
    t: 10,
    move: null,
    climb: false,
    u: 1.5,
    ...over,
  };
}

/** An editor in front with room for her, a terminal behind, the Dock. */
function screen(over: Partial<Frames> = {}): Frames {
  return {
    wins: [
      { id: 1, kind: "code", x: 300, y: 400, w: 700, h: 400 },
      { id: 2, kind: "term", x: 100, y: 300, w: 500, h: 400 },
    ],
    dock: { x0: 400, x1: 1040, y: 830 },
    floor: H - 2,
    top: 24,
    full: false,
    ...over,
  };
}

function setup(
  members: CrewMember[] = [],
  o: {
    tokens?: number | null;
    clock?: number;
    frames?: Frames;
    memo?: PetMemo;
  } = {},
) {
  const list = { now: members };
  const clock = { now: o.clock ?? DAY };
  let tokens = o.tokens ?? null;
  const brain = new PetBrain({
    W,
    H,
    members: () => list.now,
    tokens: () => tokens,
    clock: () => clock.now,
    rand: seededRandom(3),
    memo: o.memo,
  });
  brain.setFrames(o.frames ?? screen());
  return {
    brain,
    list,
    clock,
    setTokens: (next: number | null) => {
      tokens = next;
    },
    run: (seconds: number, dt = 1 / 30) => {
      for (let t = 0; t < seconds - 1e-9; t += dt) {
        clock.now += dt * 1000;
        brain.step(dt);
      }
    },
  };
}

const kinds = (brain: PetBrain) => brain.bubbles.map((b) => b.say.kind);

afterEach(() => {
  cfg.prefs = defaultDesktopToys();
  vi.unstubAllGlobals();
});

describe("where she floats", () => {
  /** Picks her up by the body and lets go at (x, y), moving `steps` times on the way. */
  function carry(
    brain: PetBrain,
    run: (s: number) => void,
    to: { x: number; y: number },
    steps = 20,
  ) {
    const from = { x: brain.x, y: brain.feet() - 40 };
    expect(brain.pressAt(from.x, from.y)).toBe(true);
    // A small pull up picks her up; from then on she keeps to the mouse.
    brain.dragTo(from.x, from.y - 6);
    for (let i = 1; i <= steps; i += 1) {
      run(0.016);
      brain.dragTo(
        from.x + ((to.x - from.x) * i) / steps,
        from.y + ((to.y - from.y) * i) / steps,
      );
    }
    // Held still a moment before letting go: no throw.
    run(0.2);
    brain.dragTo(to.x, to.y);
    brain.releaseAt(to.x, to.y);
  }

  it("floats at the right above the Dock until you move her", () => {
    const { brain, run } = setup();
    expect(brain.mode).toBe("float");
    expect(brain.x).toBe(W - 200);
    expect(brain.feet()).toBe(830 - 40);
    run(5);
    expect(brain.x).toBe(W - 200);
    expect(brain.feet()).toBe(830 - 40);
  });

  it("stays wherever she is dropped, windows or not", () => {
    const { brain, run } = setup();
    carry(brain, run, { x: 400, y: 300 });
    expect(brain.mode).toBe("float");
    const at = { x: brain.x, foot: brain.feet() };
    expect(at.x).toBeCloseTo(400, 0);
    // Windows move, close and go full screen: she stays put.
    brain.setFrames(screen({ wins: [] }));
    run(3);
    brain.setFrames(screen({ full: true }));
    run(3);
    expect(brain.x).toBe(at.x);
    expect(brain.feet()).toBe(at.foot);
  });

  it("comes back to her spot after a restart", () => {
    const { brain, run } = setup();
    carry(brain, run, { x: 300, y: 500 });
    const spot = brain.memo.spot!;
    expect(spot.x).toBeCloseTo(brain.x / W, 5);
    const memo = parseMemo(JSON.stringify(brain.memo))!;
    expect(memo.spot).toEqual(spot);
    const again = setup([], { memo }).brain;
    expect(again.x).toBeCloseTo(brain.x, 0);
    expect(again.feet()).toBe(brain.feet());
  });

  it("glides to a stop after a throw instead of falling", () => {
    const { brain, run } = setup();
    const x = brain.x;
    const y = brain.feet() - 40;
    brain.pressAt(x, y);
    for (let i = 1; i <= 4; i += 1) {
      run(0.016);
      brain.dragTo(x - i * 14, y - i * 6);
    }
    brain.releaseAt(x - 56, y - 24);
    expect(brain.mode).toBe("fly");
    expect(brain.vx).toBeLessThan(0);
    run(3);
    expect(brain.mode).toBe("float");
    expect(brain.x).toBeLessThan(x - 100);
    // Thrown up and to the left, she ends up higher, not on the floor.
    expect(brain.feet()).toBeLessThan(y + 40);
  });

  it("bounces off the screen's edge and gets dizzy after a hard throw", () => {
    const { brain, run } = setup();
    brain.mode = "fly";
    brain.vx = 1500;
    run(1);
    const r = brain.room();
    expect(brain.x).toBeLessThanOrEqual(r.x1);
    expect(brain.dizzy).toBeGreaterThan(0);
    run(3);
    expect(brain.mode).toBe("float");
  });

  it("keeps her whole body on the screen and her head under the menu bar", () => {
    const { brain, run } = setup();
    carry(brain, run, { x: -500, y: -500 });
    const [body] = brain.hits();
    expect(body.x).toBeGreaterThanOrEqual(0);
    expect(body.y).toBeGreaterThanOrEqual(24);
    carry(brain, run, { x: W + 500, y: H + 500 });
    const [low] = brain.hits();
    expect(low.x + low.w).toBeLessThanOrEqual(W);
    expect(low.y + low.h).toBeLessThanOrEqual(H);
  });

  it("keeps her spot as shares when the screen's size changes", () => {
    const { brain, run } = setup();
    carry(brain, run, { x: 360, y: 450 });
    const share = { x: brain.x / W, foot: brain.foot / H };
    brain.resize(W * 2, H * 2);
    expect(brain.x / brain.W).toBeCloseTo(share.x, 5);
    expect(brain.foot / brain.H).toBeCloseTo(share.foot, 5);
    expect(brain.mode).toBe("float");
  });

  it("goes to another screen in your hand and stays there", () => {
    const { brain, run } = setup();
    const x = brain.x;
    const y = brain.feet() - 40;
    brain.pressAt(x, y);
    brain.dragTo(x + 10, y);
    run(0.016);
    brain.dragTo(x + 30, y);
    const held = { x: brain.x, foot: brain.foot };
    // The mouse crosses onto a bigger screen to the right and lower down;
    // her window follows: 1440 points left, 200 points down, in its points.
    const move = { dx: -W, dy: 200, w: 3008, h: 1692 };
    brain.carried(move.dx, move.dy);
    brain.resize(move.w, move.h);
    expect(brain.mode).toBe("held");
    expect(brain.x).toBe(held.x - W);
    expect(brain.foot).toBe(held.foot + 200);
    // On the new screen she follows the mouse past the old one's size.
    run(0.016);
    brain.dragTo(1900, 1300);
    run(0.3);
    brain.dragTo(1900, 1300);
    brain.releaseAt(1900, 1300);
    expect(brain.mode).toBe("float");
    // Picked up 10 points left of the mouse, she stays 10 points left of it.
    expect(brain.x).toBeCloseTo(1900 - 10, 0);
    expect(brain.foot).toBeGreaterThan(H);
    // Her spot is kept as shares of the new screen.
    expect(brain.memo.spot!.x).toBeCloseTo(brain.x / 3008, 5);
  });

  it("brings her orbs along to another screen", () => {
    const { brain, run } = setup([member()]);
    run(2);
    const orb = brain.orbs.get("a")!;
    const at = { x: orb.x, y: orb.y };
    brain.pressAt(brain.x, brain.feet() - 40);
    brain.dragTo(brain.x, brain.feet() - 50);
    brain.carried(-W, 200);
    expect(orb.x).toBe(at.x - W);
    expect(orb.y).toBe(at.y + 200);
    // A drag with a fresh velocity after the move: no wild throw from the jump.
    brain.resize(3008, 1692);
    run(0.3);
    brain.dragTo(brain.x, brain.feet() - 40);
    brain.releaseAt(brain.x, brain.feet() - 40);
    expect(brain.mode).toBe("float");
  });

  it("stays and takes the mouse over a full-screen app", () => {
    const { brain, run } = setup();
    brain.setFrames(screen({ full: true }));
    run(1);
    const [body] = brain.hits();
    expect(brain.x).toBeGreaterThan(body.x);
    expect(brain.x).toBeLessThan(body.x + body.w);
    expect(body.click).toBe(true);
    expect(brain.pressAt(brain.x, brain.feet() - 40)).toBe(true);
  });
});

describe("what happens in the conversations", () => {
  it("waves at a finished reply, holds its orb up and says so", () => {
    const m = member();
    const { brain, list, run } = setup([m]);
    run(0.5);
    list.now = [member({ act: "wait", t: 0 })];
    run(0.1);
    expect(brain.wave).toBeGreaterThan(0);
    expect(brain.orbs.get("a")?.hold).toBeGreaterThan(0);
    expect(kinds(brain)).toEqual(["reply"]);
    expect(brain.memo.replies).toBe(1);
    expect(actOf(brain, undefined)).toBe("greet");
  });

  it("counts a reply without calling when her reply bubble is off", () => {
    const toys = defaultDesktopToys();
    cfg.prefs = { ...toys, pet: { ...toys.pet, reply: false } };
    const { brain, list, run } = setup([member()]);
    run(0.5);
    list.now = [member({ act: "wait", t: 0 })];
    run(0.1);
    expect(brain.memo.replies).toBe(1);
    expect(brain.wave).toBe(0);
    expect(brain.bubbles).toEqual([]);
  });

  it("holds the orb up beside the hand she waves, clear of her hair", () => {
    const { brain, list, run } = setup([member()]);
    run(1);
    list.now = [member({ act: "wait", t: 0 })];
    run(0.1);
    const orb = brain.orbs.get("a")!;
    brain.shown = faceOf(brain, undefined);
    run(1.5);
    expect(Math.sign(orb.x - brain.x)).toBe(brain.shown);
    expect(Math.abs(orb.x - brain.x)).toBeGreaterThan(brain.half());
  });

  it("sweats and says so when a conversation switches routes", () => {
    const { brain, list, run } = setup([member()]);
    run(0.5);
    list.now = [member({ slip: 2, model: "gpt-5.5" })];
    run(0.1);
    expect(brain.sweat).toBeGreaterThan(0);
    const [b] = brain.bubbles;
    expect(b.say).toEqual({
      kind: "failover",
      client: "claude_code",
      model: "gpt-5.5",
    });
  });

  it("clasps her hands for a milestone and names what she now wears", () => {
    const { brain } = setup();
    brain.milestone(3);
    expect(brain.hopBig).toBe(true);
    expect(actOf(brain, undefined)).toBe("glad");
    expect(brain.bubbles[0].say).toEqual({
      kind: "milestone",
      tokens: 2_000_000,
      gear: "flower",
    });
    brain.milestone(2);
    expect(brain.bubbles.at(-1)?.say).toMatchObject({ gear: null });
  });

  it("wears what today's tokens have earned", () => {
    expect(badgesOf(0)).toHaveLength(0);
    expect(badgesOf(3)).toHaveLength(2);
    expect(badgesOf(5)).toHaveLength(3);
    // The crown takes the bow's place.
    expect(badgesOf(7)).toHaveLength(3);
    expect(gearAt(7)).toBe("crown");
    const { brain, setTokens } = setup();
    expect(brain.stage()).toBe(0);
    setTokens(3_000_000);
    expect(brain.stage()).toBe(3);
  });

  it("does what the busiest conversation does", () => {
    const { brain } = setup([
      member({ id: "a", act: "think" }),
      member({ id: "b", act: "read", order: 1 }),
    ]);
    expect(brain.lead()?.id).toBe("b");
    expect(actOf(brain, "read")).toBe("read");
    // Editing or searching, she stays floating where she is.
    for (const [act, clip] of [
      ["edit", "sketch"],
      ["search", "lens"],
      ["write", "type"],
    ] as const) {
      const busy = setup([member({ act })]).brain;
      const x = busy.x;
      busy.step(0.5);
      expect(busy.mode).toBe("float");
      expect(busy.x).toBe(x);
      expect(actOf(busy, act)).toBe(clip);
    }
  });

  it("keeps the orbs beside her head, clear of her face", () => {
    const { brain, run } = setup([
      member({ id: "a" }),
      member({ id: "b", order: 1 }),
      member({ id: "c", order: 2 }),
    ]);
    run(3);
    for (const o of brain.orbs.values())
      expect(Math.abs(o.x - brain.x)).toBeGreaterThanOrEqual(brain.half() + 40);
  });

  it("dresses as the client with the most conversations going", () => {
    const { brain, run } = setup([
      member({ id: "a", client: "codex" }),
      member({ id: "b", client: "deepseek_harness", order: 1 }),
      member({ id: "c", client: "deepseek_harness", order: 2 }),
      member({ id: "d", client: "pi", order: 3 }),
      member({ id: "e", client: "pi", order: 4 }),
      member({ id: "f", client: "pi", order: 5 }),
    ]);
    run(0.1);
    expect(brain.client).toBe("deepseek_harness");
    expect(brain.memo.client).toBe("deepseek_harness");
  });

  it("is DeepSeek until one of the three clients has worked", () => {
    const { brain, run } = setup([member({ id: "a", client: "pi" })]);
    run(0.1);
    expect(brain.client).toBe("deepseek_harness");
    expect(brain.memo.client).toBe("deepseek_harness");
  });

  it("stays as she is on a tie or for clients without a girl", () => {
    const { brain, list, run } = setup([member({ id: "a", client: "codex" })]);
    run(0.1);
    expect(brain.client).toBe("codex");
    // Claude joins with as many conversations: no switching back and forth.
    list.now = [
      member({ id: "b", client: "claude_code" }),
      member({ id: "a", client: "codex", order: 1 }),
    ];
    run(0.1);
    expect(brain.client).toBe("codex");
    // Only Pi at work: she keeps her look, works along and calls its reply.
    list.now = [member({ id: "c", client: "pi", act: "read" })];
    run(0.1);
    expect(brain.client).toBe("codex");
    expect(actOf(brain, brain.lead()?.act)).toBe("read");
    list.now = [member({ id: "c", client: "pi", act: "wait", t: 0 })];
    run(0.1);
    expect(brain.client).toBe("codex");
    expect(brain.bubbles.map((b) => b.say)).toContainEqual({
      kind: "reply",
      client: "pi",
    });
  });

  it("wears the girl chosen in settings, whoever works", () => {
    const toys = defaultDesktopToys();
    cfg.prefs = {
      ...toys,
      pet: { ...toys.pet, character: "deepseek_harness" },
    };
    const { brain, run } = setup([
      member({ id: "a", client: "claude_code" }),
      member({ id: "b", client: "claude_code", order: 1 }),
    ]);
    run(0.1);
    expect(brain.client).toBe("deepseek_harness");
    cfg.prefs = toys;
    run(0.1);
    expect(brain.client).toBe("claude_code");
  });
});

describe("naps and time away", () => {
  it("dozes when nothing happens and wakes when the mouse moves", () => {
    const { brain, run } = setup([member({ act: "wait", t: 60 })]);
    run(DOZE_AFTER + 1, 0.25);
    expect(brain.sleeping).toBe(true);
    expect(brain.dozing).toBe(true);
    brain.point({ x: 10, y: 10 });
    expect(brain.sleeping).toBe(false);
    expect(brain.stretch).toBe(0);
  });

  it("stays awake while you type, by the host's idle time", () => {
    const { brain, run } = setup([]);
    for (let i = 0; i < DOZE_AFTER + 5; i += 1) {
      brain.idle({ idle: 0.5, away: 180 });
      run(1, 0.25);
    }
    expect(brain.sleeping).toBe(false);
  });

  it("stretches awake when work starts during a doze", () => {
    const { brain, list, run } = setup([]);
    run(DOZE_AFTER + 1, 0.25);
    expect(brain.dozing).toBe(true);
    list.now = [member({ act: "read" })];
    run(0.1);
    expect(brain.sleeping).toBe(false);
    expect(brain.stretch).toBeGreaterThan(0);
    expect(actOf(brain, undefined)).toBe("stretch");
  });

  it("sleeps while you are away and tells you what happened when you are back", () => {
    const { brain, list, run, clock, setTokens } = setup([member()], {
      tokens: 1_000_000,
    });
    run(0.5);
    brain.idle({ idle: 200, away: 180 });
    expect(brain.away()).toBe(true);
    expect(brain.sleeping).toBe(true);
    list.now = [member({ act: "wait", t: 0 })];
    run(0.1);
    expect(brain.bubbles).toEqual([]);
    clock.now += 10 * 60_000;
    setTokens(1_400_000);
    brain.idle({ idle: 0.4, away: 180 });
    expect(brain.away()).toBe(false);
    expect(brain.stretch).toBeGreaterThan(0);
    const say = brain.bubbles[0].say as Extract<Say, { kind: "back" }>;
    expect(say.kind).toBe("back");
    expect(say.replies).toBe(1);
    expect(say.tokens).toBe(400_000);
    expect(say.waiting).toBe(1);
    expect(Math.round(say.seconds / 60)).toBe(13);
  });

  it("leaves out the tokens when the day turned over while you were away", () => {
    const { brain, setTokens } = setup([], { tokens: 9_000_000 });
    brain.idle({ idle: 200, away: 180 });
    setTokens(20_000);
    brain.idle({ idle: 0.2, away: 180 });
    expect(brain.bubbles[0].say).toMatchObject({ kind: "back", tokens: null });
  });
});

describe("play", () => {
  /** Strokes across her head, turning back and forth. */
  function stroke(brain: PetBrain, run: (s: number) => void, turns: number) {
    const y = brain.top(brain.feet()) + 20;
    const dx = brain.half() / 2;
    for (let i = 0; i <= turns; i += 1) {
      brain.point({ x: brain.x - dx, y });
      run(0.05);
      brain.point({ x: brain.x + dx, y });
      run(0.05);
    }
  }

  it("is patted by stroking her head back and forth", () => {
    const { brain, run } = setup();
    stroke(brain, run, 3);
    expect(brain.pat).toBeGreaterThan(0);
    expect(brain.memo.pats).toBe(1);
    run(2);
    expect(brain.hearts.length).toBeGreaterThan(0);
  });

  it("jumps a little when poked and pouts after five quick pokes", () => {
    const { brain, run } = setup();
    const at = { x: brain.x, y: brain.feet() - 40 };
    expect(brain.pressAt(at.x, at.y)).toBe(true);
    brain.releaseAt(at.x, at.y);
    expect(brain.hop).toBe(1);
    expect(brain.hopBig).toBe(false);
    for (let i = 0; i < 4; i += 1) {
      run(0.2);
      brain.pressAt(at.x, at.y);
      brain.releaseAt(at.x, at.y);
    }
    expect(brain.pout).toBeGreaterThan(0);
    expect(kinds(brain)).toEqual(["poked"]);
  });

  it("says she has had enough at once, ahead of older bubbles", () => {
    const { brain, run } = setup();
    brain.greet();
    const at = { x: brain.x, y: brain.feet() - 40 };
    for (let i = 0; i < 5; i += 1) {
      run(0.2);
      brain.pressAt(at.x, at.y);
      brain.releaseAt(at.x, at.y);
    }
    expect(kinds(brain)).toEqual(["poked", "greet"]);
    expect(brain.bubbleT).toBeLessThan(0.1);
  });

  it("is picked up, dangles, and floats where she is let go", () => {
    const { brain, run } = setup();
    const x = brain.x;
    const y = brain.feet() - 40;
    expect(brain.pressAt(x, y)).toBe(true);
    brain.dragTo(x - 10, y - 10);
    expect(brain.mode).toBe("held");
    expect(actOf(brain, undefined)).toBe("held");
    brain.dragTo(x - 40, y - 10);
    expect(brain.x).toBe(x - 30);
    run(0.3);
    brain.releaseAt(x - 40, y - 10);
    expect(brain.mode).toBe("float");
    expect(brain.x).toBe(x - 30);
  });

  it("only gets poked when picking up is off", () => {
    const toys = defaultDesktopToys();
    cfg.prefs = { ...toys, pet: { ...toys.pet, grab: false } };
    const { brain } = setup();
    const x = brain.x;
    const y = brain.feet() - 40;
    brain.pressAt(x, y);
    brain.dragTo(x + 40, y - 40);
    expect(brain.mode).toBe("float");
    expect(brain.x).toBe(x);
    brain.releaseAt(x + 2, y);
    expect(brain.bang).toBeGreaterThan(0);
  });

  it("waves and sums up the day on a double click", () => {
    const { brain } = setup([], { tokens: 3_200_000 });
    brain.patHead();
    brain.greet();
    expect(brain.wave).toBeGreaterThan(0);
    expect(brain.bubbles.at(-1)?.say).toEqual({
      kind: "greet",
      replies: 0,
      tokens: 3_200_000,
      pats: 1,
    });
    expect(petWords(brain.bubbles.at(-1)!.say).title).toBe("今天写完 0 次回复");
  });

  it("says nothing while quiet", () => {
    const { brain } = setup();
    brain.toggleQuiet();
    brain.greet();
    expect(brain.bubbles).toEqual([]);
  });

  it("starts today's counts over on a new day", () => {
    const { brain, clock } = setup();
    brain.patHead();
    expect(brain.memo.pats).toBe(1);
    clock.now += 24 * 3600_000;
    brain.patHead();
    expect(brain.memo.pats).toBe(1);
    expect(brain.memo.day).toBe(dayOf(clock.now));
  });
});

describe("her own poses", () => {
  it("dangles while held and naps with her eyes shut", () => {
    const { brain } = setup();
    brain.mode = "held";
    expect(actOf(brain, undefined)).toBe("held");
    brain.mode = "float";
    brain.sleeping = true;
    expect(actOf(brain, "wait")).toBe("doze");
    expect(moodOf(brain)).toBe("blink");
  });

  it("makes a face for what happens to her", () => {
    const { brain } = setup();
    expect(moodOf(brain)).toBeNull();
    brain.blink = 0.1;
    expect(moodOf(brain)).toBe("blink");
    brain.patHead();
    expect(moodOf(brain)).toBe("shy");
    brain.pat = 0;
    brain.poke();
    expect(moodOf(brain)).toBe("wow");
    brain.bang = 0;
    brain.pout = 2;
    expect(moodOf(brain)).toBe("pout");
    brain.pout = 0;
    brain.mode = "held";
    expect(moodOf(brain)).toBe("wince");
    brain.mode = "float";
    brain.blink = 0;
    brain.hop = 0;
    // The mouse over her, she smiles.
    brain.point({ x: brain.x, y: brain.feet() - 60 });
    expect(moodOf(brain)).toBe("happy");
  });

  it("hums or lights up now and then while she floats with nothing to do", () => {
    const { brain } = setup();
    let starts = 0;
    let was = null as typeof brain.whim;
    // Before she would doze off.
    for (let n = 0; n < 28 * 20; n += 1) {
      brain.step(1 / 20);
      if (brain.whim && !was) {
        starts += 1;
        expect(["happy", "sparkle"]).toContain(brain.whim.face);
        // Humming, she gives a little hop.
        if (brain.whim.face === "happy") expect(brain.hop).toBeGreaterThan(0);
        expect(moodOf(brain)).toBe(brain.whim.face);
      }
      was = brain.whim;
    }
    expect(starts).toBeGreaterThanOrEqual(2);
    // Busy, she keeps to her work.
    const busy = setup([member()]).brain;
    for (let n = 0; n < 60 * 20; n += 1) {
      busy.step(1 / 20);
      expect(busy.whim).toBeNull();
    }
  });

  it("is as tall as her drawn picture, crown to soles", () => {
    const { brain } = setup();
    const kit = kitOf(brain.client)!;
    const foot = brain.feet();
    const crown = onScreen(
      kit,
      { x: kit.anchor.x, y: kit.crown },
      brain.x,
      foot,
    );
    expect(crown.y).toBeCloseTo(brain.top(foot), 5);
    const built = build(kit, { clip: "idle", face: null, pace: 0, t: 0 });
    const top = built.cells.findIndex((c) => c !== ".");
    expect(Math.floor(top / kit.w)).toBe(kit.crown);
  });

  it("draws a grid pixel on whole device pixels, never bigger than chosen", () => {
    vi.stubGlobal("window", { devicePixelRatio: 2 });
    expect(kitPx()).toBe(1.5);
    const toys = defaultDesktopToys();
    cfg.prefs = { ...toys, pet: { ...toys.pet, size: "small" } };
    expect(kitPx()).toBe(1);
    cfg.prefs = { ...toys, pet: { ...toys.pet, size: "large" } };
    expect(kitPx()).toBe(2);
    vi.stubGlobal("window", { devicePixelRatio: 1 });
    cfg.prefs = defaultDesktopToys();
    expect(kitPx()).toBe(1);
  });

  it("lays a face over her picture, and swings one foot at a time", () => {
    const { brain } = setup();
    const kit = kitOf(brain.client)!;
    const blink = kit.faces.blink!;
    const open = build(kit, { clip: "idle", face: null, pace: 0, t: 0 });
    const shut = build(kit, { clip: "idle", face: "blink", pace: 0, t: 0 });
    const eyes = (b: typeof open) =>
      b.cells
        .slice(blink.y * kit.w, (blink.y + blink.rows.length) * kit.w)
        .join("");
    expect(eyes(shut)).not.toBe(eyes(open));
    // A frame with a face of its own keeps it.
    const own = kit.ownFace[0];
    const clip = Object.keys(kit.clips).find(
      (c) => kit.clips[c][0].frame === own,
    )!;
    expect(build(kit, { clip, face: "blink", pace: 0, t: 0 }).key).toBe(
      build(kit, { clip, face: null, pace: 0, t: 0 }).key,
    );
    // A face she was not drawn with leaves hers as it is.
    const missing = (
      ["happy", "shy", "sparkle", "pout", "wow", "wince"] as const
    ).find((f) => !kit.faces[f]);
    if (missing)
      expect(
        build(kit, { clip: "idle", face: missing, pace: 0, t: 0 }).cells,
      ).toEqual(open.cells);
    const near = build(kit, { clip: "idle", face: null, pace: 1, t: 0 });
    const far = build(kit, { clip: "idle", face: null, pace: 1, t: 2 });
    // Each foot lifts in its own turn: only that leg's cells change.
    const leg = (b: typeof open, i: number) => {
      const r = kit.legs[i];
      const out: string[] = [];
      for (let y = r.y - 1; y < r.y + r.h && y < kit.h; y += 1)
        out.push(
          b.cells.slice(y * kit.w + r.x, y * kit.w + r.x + r.w).join(""),
        );
      return out.join("|");
    };
    expect(leg(near, 0)).not.toBe(leg(open, 0));
    expect(leg(near, 1)).toBe(leg(open, 1));
    expect(leg(far, 1)).not.toBe(leg(open, 1));
    expect(leg(far, 0)).toBe(leg(open, 0));
  });

  it("draws Claude, Codex and DeepSeek from their own pictures, the same size", () => {
    const { brain } = setup();
    const sizes = (["claude_code", "codex", "deepseek_harness"] as const).map(
      (client) => {
        brain.client = client;
        const kit = kitOf(client)!;
        expect(Object.keys(kit.faces).sort()).toEqual([
          "blink",
          "happy",
          "pout",
          "shy",
          "sparkle",
          "wince",
          "wow",
        ]);
        expect(Object.keys(kit.clips)).toContain("held");
        return brain.feet() - brain.top(brain.feet());
      },
    );
    // Within two grid rows of each other.
    expect(Math.max(...sizes) - Math.min(...sizes)).toBeLessThanOrEqual(
      2 * kitPx(),
    );
  });
});

describe("what the window keeps and hears", () => {
  it("reads today's counts back and refuses anything else", () => {
    const memo = {
      day: "2026-11-8",
      replies: 4,
      pats: 2,
      quiet: true,
      client: "codex" as const,
    };
    expect(parseMemo(JSON.stringify({ ...memo, extra: 1 }))).toEqual(memo);
    expect(parseMemo(null)).toBeNull();
    expect(parseMemo("{")).toBeNull();
    expect(parseMemo("null")).toBeNull();
    expect(parseMemo(JSON.stringify({ ...memo, client: "pi" }))).toBeNull();
    expect(parseMemo(JSON.stringify({ ...memo, pats: "2" }))).toBeNull();
    const { brain } = setup();
    const kept = new PetBrain({ W, H, memo: { ...memo, day: brain.memo.day } });
    expect(kept.client).toBe("codex");
    expect(kept.memo.quiet).toBe(true);
  });

  it("reads the host's idle time", () => {
    expect(readIdle({ idle: 3.5, away: 180 })).toEqual({
      idle: 3.5,
      away: 180,
    });
    expect(readIdle({ idle: "3", away: 180 })).toBeNull();
    expect(readIdle({ idle: Number.NaN, away: 180 })).toBeNull();
    expect(readIdle(null)).toBeNull();
  });

  it("reads the host's screen move", () => {
    expect(readScreenMove({ dx: -1440, dy: 200, w: 3008, h: 1692 })).toEqual({
      dx: -1440,
      dy: 200,
      w: 3008,
      h: 1692,
    });
    expect(readScreenMove({ dx: 0, dy: 0, w: 0, h: 900 })).toBeNull();
    expect(readScreenMove({ dx: "1", dy: 0, w: 10, h: 10 })).toBeNull();
    expect(readScreenMove({ dx: 1, dy: 0, w: 10 })).toBeNull();
    expect(readScreenMove(null)).toBeNull();
  });
});
