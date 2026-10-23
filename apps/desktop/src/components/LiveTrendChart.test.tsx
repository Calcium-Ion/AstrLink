import { describe, expect, it } from "vitest";

import { trendPointAt, trendSegments } from "./LiveTrendChart";

const NOW = 1_000_000;
const window = { gapMs: 5_000, nowMs: NOW, windowMs: 60_000 };

describe("trendSegments", () => {
  it("breaks the line where nothing was sampled and reaches now while fresh", () => {
    const segments = trendSegments(
      [
        { at: NOW - 50_000, value: 1 },
        { at: NOW - 49_000, value: 2 },
        { at: NOW - 30_000, value: 3 },
        { at: NOW - 1_000, value: 4 },
      ],
      window,
    );
    expect(segments).toEqual([
      {
        points: [
          { at: NOW - 50_000, value: 1 },
          { at: NOW - 49_000, value: 2 },
        ],
        end: NOW - 49_000,
      },
      { points: [{ at: NOW - 30_000, value: 3 }], end: NOW - 30_000 },
      { points: [{ at: NOW - 1_000, value: 4 }], end: NOW },
    ]);
  });

  it("starts a run that began before the window at the window's edge", () => {
    const segments = trendSegments(
      [
        { at: NOW - 64_000, value: 5 },
        { at: NOW - 61_000, value: 2 },
        { at: NOW - 58_000, value: 3 },
      ],
      window,
    );
    expect(segments).toEqual([
      {
        points: [
          { at: NOW - 60_000, value: 2 },
          { at: NOW - 58_000, value: 3 },
        ],
        end: NOW - 58_000,
      },
    ]);
  });

  it("stops a stale run at its last sample and drops runs before the window", () => {
    expect(
      trendSegments(
        [
          { at: NOW - 90_000, value: 7 },
          { at: NOW - 20_000, value: 1 },
        ],
        window,
      ),
    ).toEqual([
      { points: [{ at: NOW - 20_000, value: 1 }], end: NOW - 20_000 },
    ]);
  });
});

describe("trendPointAt", () => {
  it("reads the step in force and nothing inside a gap", () => {
    const segments = trendSegments(
      [
        { at: NOW - 40_000, value: 1 },
        { at: NOW - 38_000, value: 2 },
        { at: NOW - 2_000, value: 3 },
      ],
      window,
    );
    expect(trendPointAt(segments, NOW - 39_000)?.value).toBe(1);
    expect(trendPointAt(segments, NOW - 38_000)?.value).toBe(2);
    expect(trendPointAt(segments, NOW - 20_000)).toBeNull();
    expect(trendPointAt(segments, NOW)?.value).toBe(3);
  });

  it("keeps a sample's own fields at the window's edge", () => {
    const segments = trendSegments(
      [
        { at: NOW - 62_000, value: 2, who: "early" },
        { at: NOW - 59_000, value: 1, who: "late" },
      ],
      window,
    );
    expect(trendPointAt(segments, NOW - 60_000)).toEqual({
      at: NOW - 60_000,
      value: 2,
      who: "early",
    });
  });
});
