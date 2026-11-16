import { afterEach, describe, expect, it, vi } from "vitest";

import type { Pen } from "../live-crew-draw";
import { seededRandom, type CrewMember } from "../live-crew-model";
import { PetBrain } from "./pet-brain";
import { defaultDesktopToys } from "../preferences-model";
import { boxAt, paint, statusOf, type PaintCache } from "./pet-draw";
import { cfg } from "./toy-config";
import { throughput, type Board } from "./toy-kit";

/** A board that records what reaches it instead of drawing. */
function fakeBoard() {
  const pen = new Proxy(
    { palette: new Proxy({}, { get: () => "#000" }), T: 0, dt: 0 },
    {
      get: (target, key) =>
        key in target ? target[key as keyof typeof target] : () => undefined,
    },
  ) as unknown as Pen;
  const board = {
    g: { setTransform: vi.fn(), getTransform: () => ({}) },
    pen,
    fx: [],
    rand: () => 0.5,
    clear: vi.fn(),
    draw: vi.fn(),
  };
  return board as typeof board & Board;
}

function member(over: Partial<CrewMember> = {}): CrewMember {
  return {
    id: "a",
    client: "codex",
    model: "gpt-5.5",
    orb: null,
    order: 0,
    act: "wait",
    tool: "",
    count: 1,
    rate: 0,
    shown: 10,
    waiting: false,
    queue: [],
    done: false,
    failed: false,
    jump: 0,
    slip: 0,
    spot: null,
    arrived: true,
    lastKey: "",
    reading: "wait",
    clones: [],
    leaving: false,
    alpha: 1,
    x: 0,
    level: 0,
    fy: 0,
    face: 1,
    walk: 0,
    t: 60,
    move: null,
    climb: false,
    u: 1.5,
    ...over,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  throughput.rate = 0;
  cfg.prefs = defaultDesktopToys();
});

describe("drawing the big pet", () => {
  it("skips a frame that would look the same and draws once she changes", () => {
    vi.stubGlobal("window", { devicePixelRatio: 2 });
    const list = { now: [member()] };
    const brain = new PetBrain({
      W: 1440,
      H: 900,
      members: () => list.now,
      rand: seededRandom(1),
    });
    brain.setFrames({
      wins: [{ id: 1, kind: "code", x: 300, y: 400, w: 700, h: 400 }],
      dock: null,
      floor: 898,
      top: 24,
      full: false,
    });
    // Settled: the orb has flown in.
    for (let i = 0; i < 120; i += 1) brain.step(1 / 30);
    const pet = fakeBoard();
    const orbs = fakeBoard();
    const cache: PaintCache = { sig: "", painted: null };
    const first = paint(brain, pet, orbs, 0.1, cache);
    expect(first.box).toEqual(boxAt(brain.x, brain.feet()));
    expect(pet.clear).toHaveBeenCalledTimes(1);
    expect(orbs.draw).toHaveBeenCalledTimes(1);
    expect(paint(brain, pet, orbs, 0.1, cache)).toBe(first);
    expect(pet.clear).toHaveBeenCalledTimes(1);
    // Work starts: a new pose and a chip.
    list.now = [member({ act: "read", tool: "Read", reading: "read" })];
    brain.step(0.1);
    paint(brain, pet, orbs, 0.1, cache);
    expect(pet.clear).toHaveBeenCalledTimes(2);
    // Hearts float every frame.
    brain.patHead();
    brain.step(0.4);
    paint(brain, pet, orbs, 0.1, cache);
    paint(brain, pet, orbs, 0.1, cache);
    expect(pet.clear).toHaveBeenCalledTimes(4);
  });
});

describe("the chips over her head", () => {
  it("stacks the tool over the tokens per second", () => {
    throughput.rate = 41.6;
    const lead = member({ act: "read", tool: "Read", count: 2 });
    expect(statusOf("read", lead, true)).toEqual(["Read ×2", "42 tok/s"]);
  });

  it("keeps the tokens per second up whatever she is doing", () => {
    throughput.rate = 42;
    cfg.prefs = { ...defaultDesktopToys(), status: false };
    const lead = member({ act: "read", tool: "Read" });
    expect(statusOf("read", lead, true)).toEqual(["42 tok/s"]);
    expect(statusOf("think", lead, true)).toEqual(["42 tok/s"]);
    expect(statusOf("wait", member(), false)).toEqual(["42 tok/s"]);
    expect(statusOf(undefined, null, false)).toEqual(["42 tok/s"]);
  });

  it("shows 0 tok/s while a conversation works", () => {
    const lead = member({ act: "write" });
    expect(statusOf("write", lead, true)).toEqual(["0 tok/s"]);
    expect(statusOf("wait", member(), false)).toEqual([]);
  });

  it("hides the tokens per second when its switch is off", () => {
    throughput.rate = 42;
    const toys = defaultDesktopToys();
    cfg.prefs = { ...toys, pet: { ...toys.pet, rate: false } };
    const lead = member({ act: "read", tool: "Read" });
    expect(statusOf("read", lead, true)).toEqual(["Read"]);
    expect(statusOf("write", lead, true)).toEqual([]);
  });
});
