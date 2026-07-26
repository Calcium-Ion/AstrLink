import { describe, expect, it } from "vitest";

import { RequestGate } from "./request-gate";

describe("RequestGate", () => {
  it("keeps only the newest refresh response current", () => {
    const gate = new RequestGate();
    const slow = gate.begin();
    const fast = gate.begin();

    expect(slow).not.toBeNull();
    expect(fast).not.toBeNull();
    expect(gate.isCurrent(slow!)).toBe(false);
    expect(gate.isCurrent(fast!)).toBe(true);
  });

  it("makes restart exclusive and invalidates an older poll", () => {
    const gate = new RequestGate();
    const poll = gate.begin();
    const restart = gate.beginExclusive();

    expect(gate.isCurrent(poll!)).toBe(false);
    expect(restart).not.toBeNull();
    expect(gate.isCurrent(restart!)).toBe(true);
    expect(gate.begin()).toBeNull();

    expect(gate.endExclusive(restart!)).toBe(true);
    expect(gate.begin()).not.toBeNull();
  });

  it("rejects an exclusive reentry without invalidating its owner", () => {
    const gate = new RequestGate();
    const restart = gate.beginExclusive();

    expect(restart).not.toBeNull();
    expect(gate.beginExclusive()).toBeNull();
    expect(gate.isCurrent(restart!)).toBe(true);
    expect(gate.begin()).toBeNull();

    expect(gate.endExclusive(restart!)).toBe(true);
    expect(gate.begin()).not.toBeNull();
  });

  it("does not let a stale exclusive token release the current owner", () => {
    const gate = new RequestGate();
    const first = gate.beginExclusive();

    expect(first).not.toBeNull();
    expect(gate.endExclusive(first!)).toBe(true);

    const second = gate.beginExclusive();
    expect(second).not.toBeNull();
    expect(gate.endExclusive(first!)).toBe(false);
    expect(gate.begin()).toBeNull();
    expect(gate.beginExclusive()).toBeNull();

    expect(gate.endExclusive(second!)).toBe(true);
    expect(gate.begin()).not.toBeNull();
  });

  it("invalidates pending work when the component unmounts", () => {
    const gate = new RequestGate();
    const pending = gate.begin();
    gate.invalidate();

    expect(gate.isCurrent(pending!)).toBe(false);
  });
});
