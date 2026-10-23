import { describe, expect, it } from "vitest";

import {
  trajectoryListLayout,
  type TrajectoryChip,
  type TrajectoryRow,
} from "./request-trajectory-model";
import { trajectoryCardLayout } from "./trajectory-card-model";

function row(
  requestId: string,
  chip: TrajectoryChip,
  child = false,
): TrajectoryRow {
  return {
    id: `${requestId}:${chip}`,
    requestId,
    chip,
    summary: "Same model and provider",
    result: "HTTP 200",
    status: "succeeded",
    tone: "ok",
    startedAt: "2026-10-22T10:00:00Z",
    endedAt: "2026-10-22T10:00:01Z",
    lane: "client",
    child,
    turnIndex: 1,
  };
}

function edges(rows: readonly TrajectoryRow[]) {
  return [...trajectoryCardLayout(rows)].map(([id, { edge, callNumber }]) => [
    id,
    { edge, callNumber },
  ]);
}

const origin = Date.parse("2026-10-22T10:00:00Z");
const at = (offsetMs: number) => new Date(origin + offsetMs).toISOString();
function timedRow(
  requestId: string,
  chip: TrajectoryChip,
  start: number,
  end: number | null,
): TrajectoryRow {
  return {
    ...row(requestId, chip),
    startedAt: at(start),
    endedAt: end === null ? null : at(end),
  };
}

describe("trajectoryCardLayout", () => {
  it("has no cards for an empty trajectory or turn headers alone", () => {
    expect(trajectoryCardLayout([]).size).toBe(0);
    expect(trajectoryCardLayout([row("a", "TURN")]).size).toBe(0);
  });

  it("marks card edges while preserving every phase and its identity", () => {
    const rows = Object.freeze(
      [row("a", "CLIENT"), row("a", "UPSTREAM"), row("a", "RESULT")].map(
        (phase) => Object.freeze(phase),
      ),
    );
    const before = structuredClone(rows);

    expect(edges(rows)).toEqual([
      ["a:CLIENT", { edge: "first", callNumber: 1 }],
      ["a:UPSTREAM", { edge: "middle", callNumber: 1 }],
      ["a:RESULT", { edge: "last", callNumber: 1 }],
    ]);
    expect(rows).toEqual(before);
  });

  it("keeps calls in the same turn separate even with identical descriptions", () => {
    const rows = [
      row("a", "TURN"),
      row("a", "CLIENT"),
      row("a", "RESULT"),
      row("b", "CLIENT"),
      row("b", "RESULT"),
      row("c", "RESULT"),
    ];

    expect(edges(rows)).toEqual([
      ["a:CLIENT", { edge: "first", callNumber: 1 }],
      ["a:RESULT", { edge: "last", callNumber: 1 }],
      ["b:CLIENT", { edge: "first", callNumber: 2 }],
      ["b:RESULT", { edge: "last", callNumber: 2 }],
      ["c:RESULT", { edge: "only", callNumber: 3 }],
    ]);
  });

  it("does not join a turn header to the first call sharing its request id", () => {
    const rows = [
      row("a", "TURN"),
      row("a", "RESULT"),
      row("b", "TURN"),
      row("b", "CLIENT"),
      row("b", "RESULT"),
    ];

    expect(edges(rows)).toEqual([
      ["a:RESULT", { edge: "only", callNumber: 1 }],
      ["b:CLIENT", { edge: "first", callNumber: 2 }],
      ["b:RESULT", { edge: "last", callNumber: 2 }],
    ]);
  });

  it("gives retry attempts their own edges and their parent call number", () => {
    const rows = [
      row("a", "CLIENT"),
      row("a", "RESULT"),
      row("retry-a-1", "CLIENT", true),
      row("retry-a-1", "RETRY", true),
      row("retry-a-1", "RESULT", true),
      row("retry-a-2", "RETRY", true),
      row("b", "CLIENT"),
      row("b", "RESULT"),
      row("retry-b", "RETRY", true),
    ];

    expect(edges(rows)).toEqual([
      ["a:CLIENT", { edge: "first", callNumber: 1 }],
      ["a:RESULT", { edge: "last", callNumber: 1 }],
      ["retry-a-1:CLIENT", { edge: "first", callNumber: 1 }],
      ["retry-a-1:RETRY", { edge: "middle", callNumber: 1 }],
      ["retry-a-1:RESULT", { edge: "last", callNumber: 1 }],
      ["retry-a-2:RETRY", { edge: "only", callNumber: 1 }],
      ["b:CLIENT", { edge: "first", callNumber: 2 }],
      ["b:RESULT", { edge: "last", callNumber: 2 }],
      ["retry-b:RETRY", { edge: "only", callNumber: 2 }],
    ]);
  });

  it("keeps full-session numbering when earlier turns are folded", () => {
    const rows = [
      row("a", "TURN"),
      row("a", "CLIENT"),
      row("a", "RESULT"),
      row("b", "TURN"),
      row("b", "CLIENT"),
      row("b", "RESULT"),
      row("c", "CLIENT"),
      row("c", "RESULT"),
    ];
    const cards = trajectoryCardLayout(rows);
    const folded = trajectoryListLayout(rows, new Map(), null);

    expect(
      folded.rows
        .filter((phase) => phase.chip !== "TURN")
        .map((phase) => cards.get(phase.id)?.callNumber),
    ).toEqual([2, 2, 3, 3]);
    expect(cards.get("a:CLIENT")).toMatchObject({
      edge: "first",
      callNumber: 1,
    });
  });

  it("positions phases by their actual times, including gaps and overlapping work", () => {
    const cards = trajectoryCardLayout([
      timedRow("a", "CLIENT", 0, 100),
      timedRow("a", "POLICY", 300, 500),
      timedRow("a", "UPSTREAM", 400, 1000),
      timedRow("a", "RESULT", 1000, 1000),
    ]);

    expect(cards.get("a:CLIENT")?.timing).toEqual({
      offset: 0,
      duration: 0.1,
      durationMs: 100,
    });
    expect(cards.get("a:POLICY")?.timing).toEqual({
      offset: 0.3,
      duration: 0.2,
      durationMs: 200,
    });
    expect(cards.get("a:UPSTREAM")?.timing).toEqual({
      offset: 0.4,
      duration: 0.6,
      durationMs: 600,
    });
    expect(cards.get("a:RESULT")?.timing).toEqual({
      offset: 1,
      duration: 0,
      durationMs: 0,
    });
    for (const card of cards.values()) {
      expect(card.timingWindow).toEqual({
        startMs: origin,
        endMs: origin + 1000,
        open: false,
      });
    }
  });

  it("keeps zero-duration requests as finite ticks", () => {
    const cards = trajectoryCardLayout([
      timedRow("a", "CLIENT", 0, 0),
      timedRow("a", "RESULT", 0, 0),
    ]);
    for (const card of cards.values()) {
      expect(card.timing).toEqual({ offset: 0, duration: 0, durationMs: 0 });
    }
  });

  it("times unfinished pending phases only against an explicit valid clock", () => {
    const pending = Object.freeze({
      ...timedRow("a", "UPSTREAM", 200, null),
      tone: "pending" as const,
      status: "pending" as const,
    });
    const rows = Object.freeze([
      Object.freeze(timedRow("a", "CLIENT", 0, 0)),
      pending,
      Object.freeze(timedRow("a", "RESULT", 100, null)),
    ]);
    const before = structuredClone(rows);
    expect(trajectoryCardLayout(rows).get(pending.id)?.timing).toBeUndefined();
    const cards = trajectoryCardLayout(rows, origin + 1000);
    expect(cards.get(pending.id)?.timing).toEqual({
      offset: 0.2,
      duration: 0.8,
      durationMs: 800,
    });
    for (const card of cards.values()) {
      expect(card.timingWindow).toEqual({
        startMs: origin,
        endMs: origin + 1000,
        open: true,
      });
    }
    expect(cards.get("a:RESULT")?.timing).toBeUndefined();
    for (const nowMs of [NaN, Infinity, origin + 199]) {
      const invalidClock = trajectoryCardLayout(rows, nowMs).get(pending.id);
      expect(invalidClock?.timing).toBeUndefined();
      expect(invalidClock?.timingWindow).toEqual({
        startMs: origin,
        endMs: origin,
        open: false,
      });
    }
    expect(rows).toEqual(before);
  });

  it("ignores invalid and backwards intervals without contaminating valid phases", () => {
    const cards = trajectoryCardLayout(
      [
        { ...row("a", "CLIENT"), startedAt: "invalid" },
        { ...row("a", "POLICY"), endedAt: "invalid" },
        timedRow("a", "ROUTE", -200, -300),
        timedRow("a", "UPSTREAM", 0, 1000),
        {
          ...row("a", "RESULT"),
          tone: "pending",
          endedAt: "invalid",
        },
      ],
      origin + 10000,
    );
    expect(cards.get("a:UPSTREAM")?.timing).toEqual({
      offset: 0,
      duration: 1,
      durationMs: 1000,
    });
    expect(cards.get("a:UPSTREAM")?.timingWindow?.open).toBe(false);
    for (const chip of ["CLIENT", "POLICY", "ROUTE", "RESULT"]) {
      expect(cards.get(`a:${chip}`)?.timing).toBeUndefined();
    }
    expect(
      trajectoryCardLayout([
        { ...row("invalid", "CLIENT"), startedAt: "invalid", endedAt: null },
      ]).get("invalid:CLIENT"),
    ).toEqual({ edge: "only", callNumber: 1 });
  });

  it("isolates retry and root time windows from each other and turn headers", () => {
    const cards = trajectoryCardLayout([
      timedRow("a", "TURN", -10000, 10000),
      timedRow("a", "CLIENT", 0, 100),
      timedRow("a", "RESULT", 100, 100),
      { ...timedRow("retry-a", "RETRY", 1000, 4000), child: true },
      timedRow("b", "CLIENT", 5000, 5001),
    ]);
    expect(cards.has("a:TURN")).toBe(false);
    expect(cards.get("a:CLIENT")?.timing).toEqual({
      offset: 0,
      duration: 1,
      durationMs: 100,
    });
    expect(cards.get("retry-a:RETRY")).toEqual({
      edge: "only",
      callNumber: 1,
      timing: { offset: 0, duration: 1, durationMs: 3000 },
      timingWindow: {
        startMs: origin + 1000,
        endMs: origin + 4000,
        open: false,
      },
    });
    expect(cards.get("b:CLIENT")?.timing).toEqual({
      offset: 0,
      duration: 1,
      durationMs: 1,
    });
  });

  it("keeps pending windows within their own request and closes them on completion", () => {
    const pending: TrajectoryRow = {
      ...timedRow("a", "UPSTREAM", 200, null),
      tone: "pending",
      status: "pending",
    };
    const closed = timedRow("b", "UPSTREAM", 100, 300);
    const active = trajectoryCardLayout([pending, closed], origin + 1000);
    expect(active.get(pending.id)?.timingWindow).toEqual({
      startMs: origin + 200,
      endMs: origin + 1000,
      open: true,
    });
    expect(active.get(closed.id)?.timingWindow).toEqual({
      startMs: origin + 100,
      endMs: origin + 300,
      open: false,
    });
    const completed = trajectoryCardLayout(
      [timedRow("a", "UPSTREAM", 200, 1200), closed],
      origin + 10000,
    );
    expect(completed.get(pending.id)?.timingWindow).toEqual({
      startMs: origin + 200,
      endMs: origin + 1200,
      open: false,
    });
  });

  it("keeps fractional positions within the request window", () => {
    const cards = trajectoryCardLayout([
      timedRow("a", "CLIENT", 0, 0),
      timedRow("a", "UPSTREAM", 2, 3),
      timedRow("a", "RESULT", 3, 3),
    ]);
    for (const { timing } of cards.values()) {
      expect(Number.isFinite(timing!.offset)).toBe(true);
      expect(Number.isFinite(timing!.duration)).toBe(true);
      expect(timing!.offset).toBeGreaterThanOrEqual(0);
      expect(timing!.duration).toBeGreaterThanOrEqual(0);
      expect(timing!.offset + timing!.duration).toBeLessThanOrEqual(1);
    }
  });
});
