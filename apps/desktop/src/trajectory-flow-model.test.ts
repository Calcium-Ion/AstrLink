import { describe, expect, it } from "vitest";

import {
  trajectoryListLayout,
  type TrajectoryChip,
  type TrajectoryRow,
} from "./request-trajectory-model";
import { trajectoryCardLayout } from "./trajectory-card-model";
import {
  flowCallProgressAtOffset,
  flowCallSpanMs,
  flowDurationScale,
  flowDurationShare,
  flowFocusRow,
  flowHoldScrollDelta,
  flowListScrollForTimeline,
  flowScrollAnchorAtOffset,
  flowScrollOffsetForAnchor,
  flowScrollOffsetForCall,
  flowTimelineScrollForList,
  phaseDurationMs,
  trajectoryFlowEntries,
  trajectoryFlowRowIndices,
  trajectoryFlowSteps,
  trajectoryTurnDigests,
  type TrajectoryFlowCall,
  type TrajectoryFlowMeasurement,
} from "./trajectory-flow-model";

function row(
  requestId: string,
  chip: TrajectoryChip,
  overrides: Partial<TrajectoryRow> = {},
): TrajectoryRow {
  return {
    id: `${requestId}:${chip}`,
    requestId,
    chip,
    summary: "Recorded phase detail",
    result: "HTTP 200",
    status: "succeeded",
    tone: "ok",
    startedAt: "2026-10-22T10:00:00Z",
    endedAt: "2026-10-22T10:00:01Z",
    lane: "client",
    child: false,
    turnIndex: 1,
    ...overrides,
  };
}

function call(rows: TrajectoryRow[]): TrajectoryFlowCall {
  const entry = trajectoryFlowEntries(rows)[0];
  if (entry?.kind !== "call") throw new Error("Expected a call entry");
  return entry;
}

describe("trajectoryFlowEntries", () => {
  it("keeps turn headers and adjacent calls in their recorded order", () => {
    const rows = Object.freeze(
      [
        row("a", "TURN"),
        row("a", "CLIENT"),
        row("a", "RESULT"),
        row("b", "CLIENT"),
        row("b", "RESULT"),
        row("c", "TURN"),
      ].map((phase) => Object.freeze(phase)),
    );
    const before = structuredClone(rows);
    const entries = trajectoryFlowEntries(rows);

    expect(
      entries.map((entry) => [entry.id, entry.kind, entry.turnId]),
    ).toEqual([
      ["a:TURN", "turn", "a:TURN"],
      ["a:CLIENT", "call", "a:TURN"],
      ["b:CLIENT", "call", "a:TURN"],
      ["c:TURN", "turn", "c:TURN"],
    ]);
    expect(entries.flatMap((entry) => entry.rows)).toEqual(rows);
    expect(entries[1]!.rows[0]).toBe(rows[1]);
    expect(rows).toEqual(before);
    expect(trajectoryFlowEntries([])).toEqual([]);
  });

  it("folds a retry into the call it retried and keeps the root's outcome", () => {
    const at = (second: number) => `2026-10-22T10:00:0${second}Z`;
    const rows = [
      row("a", "CLIENT", { startedAt: at(0), endedAt: at(0) }),
      row("a", "UPSTREAM", {
        lane: "upstream",
        startedAt: at(4),
        endedAt: at(6),
      }),
      row("a", "RESULT", { startedAt: at(6), endedAt: at(6) }),
      // A retry child is an earlier attempt that failed; it is listed after
      // its root but happened before the root's final attempt.
      row("retry-a", "RETRY", {
        child: true,
        lane: "upstream",
        tone: "failed",
        startedAt: at(1),
        endedAt: at(3),
      }),
      row("b", "CLIENT", { startedAt: at(8), endedAt: at(8) }),
    ];
    const entries = trajectoryFlowEntries(rows).filter(
      (entry) => entry.kind === "call",
    );

    expect(
      entries.map((entry) => [
        entry.requestId,
        entry.callNumber,
        entry.attempts.map((attempt) => attempt.requestId),
      ]),
    ).toEqual([
      ["a", 1, ["a", "retry-a"]],
      ["b", 2, ["b"]],
    ]);
    const [call] = entries;
    expect(call!.rows.map((phase) => phase.id)).toEqual([
      "a:CLIENT",
      "retry-a:RETRY",
      "a:UPSTREAM",
      "a:RESULT",
    ]);
    expect(call!.resultRow).toBe(rows[2]);
    expect(call!.tone).toBe("ok");
    expect(call!.timingWindow).toEqual({
      startMs: Date.parse(at(0)),
      endMs: Date.parse(at(6)),
      open: false,
    });
    expect([...trajectoryFlowRowIndices(entries)].slice(0, 4)).toEqual([
      ["a:CLIENT", 0],
      ["retry-a:RETRY", 0],
      ["a:UPSTREAM", 0],
      ["a:RESULT", 0],
    ]);
  });

  it("keeps a retry listed without its root as a call of its own", () => {
    const [entry] = trajectoryFlowEntries([
      row("retry-a", "RETRY", { child: true, tone: "failed" }),
    ]);
    expect(entry).toMatchObject({
      kind: "call",
      child: true,
      tone: "failed",
      callNumber: 1,
    });
  });

  it("preserves full-session numbering and row lookup after earlier turns fold", () => {
    const rows = [
      row("a", "TURN"),
      row("a", "CLIENT"),
      row("a", "RESULT"),
      row("b", "TURN"),
      row("b", "CLIENT"),
      row("b", "RESULT"),
      row("c", "CLIENT"),
    ];
    const folded = trajectoryListLayout(rows, new Map(), null);
    const entries = trajectoryFlowEntries(
      folded.rows,
      trajectoryCardLayout(rows),
    );
    expect(
      entries
        .filter((entry) => entry.kind === "call")
        .map((entry) => entry.callNumber),
    ).toEqual([2, 3]);
    expect([...trajectoryFlowRowIndices(entries)]).toEqual([
      ["a:TURN", 0],
      ["b:TURN", 1],
      ["b:CLIENT", 2],
      ["b:RESULT", 2],
      ["c:CLIENT", 3],
    ]);
  });

  it("retains actual client and final result rows without guessing their text", () => {
    const phases = [
      row("a", "POLICY"),
      row("a", "CLIENT", { summary: "original model · protocol" }),
      row("a", "RESULT", { id: "earlier-result", tone: "ok" }),
      row("a", "RESULT", { tone: "blocked", result: "Policy blocked" }),
      row("a", "RESTORE"),
    ];
    const entry = call(phases);
    expect(entry.firstRow).toBe(phases[0]);
    expect(entry.lastRow).toBe(phases[4]);
    expect(entry.primaryRow).toBe(phases[1]);
    expect(entry.resultRow).toBe(phases[3]);
    expect(entry.tone).toBe("blocked");
    expect(entry.activeRow).toBeNull();
  });

  it("uses the unfinished pending phase, not an already ended pending phase", () => {
    const phases = [
      row("a", "CLIENT", { tone: "pending" }),
      row("a", "POLICY"),
      row("a", "UPSTREAM", {
        tone: "pending",
        status: "pending",
        endedAt: null,
      }),
    ];
    const entry = call(phases);
    expect(entry.activeRow).toBe(phases[2]);
    expect(entry.running).toBe(true);
    expect(entry.tone).toBe("pending");
    expect(call([phases[0]!])).toMatchObject({
      running: false,
      activeRow: null,
    });
  });

  it("lets terminal results settle stale pending stages and preserves failures", () => {
    for (const tone of ["failed", "blocked", "cancelled", "ok"] as const) {
      const phases = [
        row("a", "CLIENT", { tone: "pending", endedAt: null }),
        row("a", "UPSTREAM", { tone: "pending", endedAt: null }),
        row("a", "RESULT", { tone, endedAt: null }),
      ];
      expect(call(phases)).toMatchObject({
        tone,
        running: false,
        activeRow: null,
      });
    }
    expect(
      call([row("a", "CLIENT"), row("a", "UPSTREAM", { tone: "failed" })]).tone,
    ).toBe("failed");
  });

  it("reuses the per-request timing window including an explicitly supplied live clock", () => {
    const rows = [
      row("a", "CLIENT"),
      row("a", "UPSTREAM", { tone: "pending", endedAt: null }),
    ];
    const layout = trajectoryCardLayout(
      rows,
      Date.parse("2026-10-22T10:00:05Z"),
    );
    const entry = trajectoryFlowEntries(rows, layout)[0];
    expect(entry?.kind).toBe("call");
    if (entry?.kind !== "call") return;
    expect(entry.timingWindow).toBe(layout.get(rows[0]!.id)?.timingWindow);
    expect(entry.timingWindow?.open).toBe(true);
  });

  it("excludes stale unfinished phases from a terminal call's measured duration", () => {
    const rows = [
      row("a", "CLIENT"),
      row("a", "UPSTREAM", { tone: "pending", endedAt: null }),
      row("a", "RESULT", { tone: "failed" }),
    ];
    const layout = trajectoryCardLayout(
      rows,
      Date.parse("2026-10-22T10:01:00Z"),
    );
    const entry = trajectoryFlowEntries(rows, layout)[0];
    expect(entry?.kind).toBe("call");
    if (entry?.kind !== "call") return;
    expect(entry.running).toBe(false);
    expect(entry.timingWindow).toEqual({
      startMs: Date.parse("2026-10-22T10:00:00Z"),
      endMs: Date.parse("2026-10-22T10:00:01Z"),
      open: false,
    });
    expect(layout.get(rows[0]!.id)?.timingWindow?.endMs).toBe(
      Date.parse("2026-10-22T10:01:00Z"),
    );
  });
});

const measurements: TrajectoryFlowMeasurement[] = [
  { id: "a:TURN", requestId: "a", start: 0, size: 32 },
  { id: "a:CLIENT", requestId: "a", start: 32, size: 168 },
  { id: "b:CLIENT", requestId: "b", start: 212, size: 288 },
  { id: "c:CLIENT", requestId: "c", start: 512, size: 88 },
];

describe("measured flow scroll anchors", () => {
  it("uses measured card heights and gives boundary gaps to the next entry", () => {
    expect(flowScrollAnchorAtOffset(measurements, 116)).toEqual({
      id: "a:CLIENT",
      requestId: "a",
      fraction: 0.5,
    });
    expect(flowScrollAnchorAtOffset(measurements, 32)).toEqual({
      id: "a:CLIENT",
      requestId: "a",
      fraction: 0,
    });
    expect(flowScrollAnchorAtOffset(measurements, 205)).toEqual({
      id: "b:CLIENT",
      requestId: "b",
      fraction: 0,
    });
    expect(flowScrollAnchorAtOffset(measurements, -20)).toEqual({
      id: "a:TURN",
      requestId: "a",
      fraction: 0,
    });
    expect(flowScrollAnchorAtOffset(measurements, 1000)).toEqual({
      id: "c:CLIENT",
      requestId: "c",
      fraction: 1,
    });
  });

  it("restores the same position inside a card after previous cards change height", () => {
    const anchor = flowScrollAnchorAtOffset(measurements, 356)!;
    const expanded = measurements.map((entry) =>
      entry.requestId === "a"
        ? { ...entry, size: entry.size + (entry.id === "a:CLIENT" ? 120 : 0) }
        : { ...entry, start: entry.start + 120 },
    );
    expect(flowScrollOffsetForAnchor(expanded, anchor, 1000)).toBe(476);
    expect(flowScrollOffsetForAnchor(expanded, anchor, 400)).toBe(400);
  });

  it("locates a timeline call across both its header and variable-height card", () => {
    expect(flowScrollOffsetForCall(measurements, "a", 0.5, 600)).toBe(100);
    expect(flowScrollOffsetForCall(measurements, "b", 0.5, 600)).toBe(356);
    expect(flowScrollOffsetForCall(measurements, "b", -1, 600)).toBe(212);
    expect(flowScrollOffsetForCall(measurements, "c", 2, 550)).toBe(550);
    expect(flowScrollOffsetForCall(measurements, "missing", 0.5, 600)).toBe(0);
  });

  it("falls back to a request anchor when folding removes an entry", () => {
    const anchor = flowScrollAnchorAtOffset(measurements, 116)!;
    expect(flowScrollOffsetForAnchor([measurements[0]!], anchor, 600)).toBe(16);
    expect(flowScrollOffsetForAnchor([], anchor, 600)).toBe(0);
  });

  it("reports continuous request progress through shared headers and retry cards", () => {
    expect(flowCallProgressAtOffset(measurements, 16)).toEqual({
      requestId: "a",
      fraction: 0.08,
    });
    expect(flowCallProgressAtOffset(measurements, 32)).toEqual({
      requestId: "a",
      fraction: 0.16,
    });
    expect(flowCallProgressAtOffset(measurements, 100)).toEqual({
      requestId: "a",
      fraction: 0.5,
    });
    const withRetry = measurements.map((entry) => ({
      ...entry,
      requestId: entry.requestId === "b" ? "a" : entry.requestId,
    }));
    expect(flowCallProgressAtOffset(withRetry, 356)).toEqual({
      requestId: "a",
      fraction: 356 / 500,
    });
    expect(flowScrollOffsetForCall(withRetry, "a", 356 / 500, 600)).toBe(356);
  });

  it("keeps empty, zero-height and invalid geometry finite", () => {
    expect(flowScrollAnchorAtOffset([], 1)).toBeNull();
    expect(
      flowScrollAnchorAtOffset(
        [{ id: "x", requestId: "x", start: 0, size: 0 }],
        10,
      ),
    ).toEqual({ id: "x", requestId: "x", fraction: 0 });
    expect(
      flowScrollAnchorAtOffset(
        [{ id: "bad", requestId: "bad", start: NaN, size: 50 }],
        10,
      ),
    ).toBeNull();
    expect(flowScrollOffsetForCall(measurements, "b", NaN, 600)).toBe(212);
    expect(flowScrollOffsetForCall(measurements, "b", 1, -10)).toBe(0);
    expect(flowScrollOffsetForCall(measurements, "b", 1, Infinity)).toBe(0);
  });
});

describe("measured flow and timeline scroll synchronization", () => {
  const columns = [
    { requestId: "a", offset: 0, width: 100 },
    { requestId: "b", offset: 100, width: 200 },
    { requestId: "c", offset: 300, width: 300 },
  ];

  it("maps measured card progress rather than assuming equal phase heights", () => {
    expect(
      flowTimelineScrollForList(measurements, columns, 100, 400, 300),
    ).toBe(37.5);
    expect(
      flowListScrollForTimeline(measurements, columns, 37.5, 300, 400),
    ).toBe(100);
    for (const offset of [0, 50, 100, 200, 250, 350, 400]) {
      const timelineOffset = flowTimelineScrollForList(
        measurements,
        columns,
        offset,
        400,
        300,
      );
      expect(
        flowListScrollForTimeline(
          measurements,
          columns,
          timelineOffset,
          300,
          400,
        ),
      ).toBeCloseTo(offset);
    }
  });

  it("keeps both scroll ends reachable when the two viewports have different extents", () => {
    expect(flowTimelineScrollForList(measurements, columns, 400, 400, 90)).toBe(
      90,
    );
    expect(flowListScrollForTimeline(measurements, columns, 90, 90, 400)).toBe(
      400,
    );
    expect(flowTimelineScrollForList(measurements, columns, 800, 400, 90)).toBe(
      90,
    );
    expect(flowListScrollForTimeline(measurements, columns, -20, 90, 400)).toBe(
      0,
    );
  });

  it("supports a folded turn represented by a single measured header", () => {
    const folded = [
      { id: "a:TURN", requestId: "a", start: 0, size: 32 },
      { id: "c:CLIENT", requestId: "c", start: 32, size: 268 },
    ];
    const foldedColumns = [
      { requestId: "a", offset: 0, width: 300 },
      { requestId: "c", offset: 300, width: 300 },
    ];
    const offset = flowTimelineScrollForList(
      folded,
      foldedColumns,
      8,
      150,
      300,
    );
    expect(offset).toBe(75);
    expect(
      flowListScrollForTimeline(folded, foldedColumns, offset, 300, 150),
    ).toBe(8);
  });

  it("does not scroll when either pane has no scroll range or content", () => {
    expect(flowTimelineScrollForList(measurements, columns, 100, 0, 300)).toBe(
      0,
    );
    expect(flowListScrollForTimeline(measurements, columns, 100, 0, 300)).toBe(
      0,
    );
    expect(flowTimelineScrollForList([], columns, 100, 400, 300)).toBe(0);
    expect(flowListScrollForTimeline(measurements, [], 100, 300, 400)).toBe(0);
  });
});

describe("trajectoryFlowSteps", () => {
  const at = (ms: number) =>
    new Date(Date.parse("2026-10-22T10:00:00Z") + ms).toISOString();

  it("walks the lanes a call crossed, with each phase's own duration", () => {
    const entry = call([
      row("a", "CLIENT", { startedAt: at(0), endedAt: at(16) }),
      row("a", "POLICY", {
        lane: "gateway",
        startedAt: at(16),
        endedAt: at(320),
      }),
      row("a", "UPSTREAM", {
        lane: "upstream",
        startedAt: at(320),
        endedAt: at(5320),
      }),
      row("a", "RESTORE", {
        lane: "gateway",
        startedAt: at(5320),
        endedAt: at(5400),
      }),
      row("a", "RESULT", { startedAt: at(5400), endedAt: at(5400) }),
    ]);
    expect(
      trajectoryFlowSteps(entry, Date.parse(at(9000))).map((step) => [
        step.row.chip,
        step.fromLane,
        step.lane,
        step.toLane,
        step.durationMs,
        step.pending,
      ]),
    ).toEqual([
      ["CLIENT", null, 0, 1, 16, false],
      ["POLICY", 0, 1, 2, 304, false],
      ["UPSTREAM", 1, 2, 1, 5000, false],
      ["RESTORE", 2, 1, 0, 80, false],
      ["RESULT", 1, 0, null, 0, false],
    ]);
  });

  it("times only the unfinished phase of a running attempt against the clock", () => {
    const running = call([
      row("a", "CLIENT", { startedAt: at(0), endedAt: at(100) }),
      row("a", "UPSTREAM", {
        lane: "upstream",
        tone: "pending",
        status: "pending",
        startedAt: at(500),
        endedAt: null,
      }),
    ]);
    const steps = trajectoryFlowSteps(running, Date.parse(at(2000)));
    expect(steps.map((step) => [step.durationMs, step.pending])).toEqual([
      [100, false],
      [1500, true],
    ]);
    expect(flowCallSpanMs(running, Date.parse(at(2000)))).toBe(2000);

    // The same phase without an end, once a result settled the call, is
    // unknown rather than running.
    const settled = call([
      ...running.rows,
      row("a", "RESULT", { startedAt: at(900), endedAt: at(900) }),
    ]);
    const after = trajectoryFlowSteps(settled, Date.parse(at(60_000)));
    expect(after.map((step) => [step.durationMs, step.pending])).toEqual([
      [100, false],
      [null, false],
      [0, false],
    ]);
    expect(flowCallSpanMs(settled, Date.parse(at(60_000)))).toBe(900);
  });

  it("marks where each retry begins", () => {
    const [entry] = trajectoryFlowEntries([
      row("a", "CLIENT", { startedAt: at(0) }),
      row("a", "UPSTREAM", { lane: "upstream", startedAt: at(3000) }),
      row("retry-a", "ROUTE", {
        child: true,
        lane: "gateway",
        startedAt: at(100),
      }),
      row("retry-a", "RETRY", {
        child: true,
        lane: "upstream",
        startedAt: at(200),
      }),
    ]);
    if (entry?.kind !== "call") throw new Error("Expected a call entry");
    expect(
      trajectoryFlowSteps(entry, 0).map((step) => [
        step.row.id,
        step.retryStart,
      ]),
    ).toEqual([
      ["a:CLIENT", false],
      ["retry-a:ROUTE", true],
      ["retry-a:RETRY", false],
      ["a:UPSTREAM", false],
    ]);
  });

  it("reads an invalid or backwards interval as unknown", () => {
    expect(
      phaseDurationMs(row("a", "CLIENT", { startedAt: "soon" })),
    ).toBeNull();
    expect(
      phaseDurationMs(
        row("a", "CLIENT", { startedAt: at(500), endedAt: at(100) }),
      ),
    ).toBeNull();
    expect(phaseDurationMs(row("a", "CLIENT", { endedAt: null }))).toBeNull();
    expect(
      phaseDurationMs(
        row("a", "CLIENT", { startedAt: at(0), endedAt: null }),
        Date.parse(at(250)),
      ),
    ).toBe(250);
  });
});

describe("flowFocusRow", () => {
  const entry = call([
    row("a", "CLIENT"),
    row("a", "UPSTREAM", { lane: "upstream" }),
    row("a", "RESULT"),
  ]);

  it("opens a call on the kind of phase being read, then its result", () => {
    expect(flowFocusRow(entry, "UPSTREAM")).toBe(entry.rows[1]);
    expect(flowFocusRow(entry, "POLICY")).toBe(entry.rows[2]);
    expect(flowFocusRow(entry, "TURN")).toBe(entry.rows[2]);
    expect(flowFocusRow(entry)).toBe(entry.rows[2]);
  });

  it("opens a retried call on the root's phase rather than a lost attempt's", () => {
    const at = (second: number) => `2026-10-22T10:00:0${second}Z`;
    const [retried] = trajectoryFlowEntries([
      row("a", "CLIENT", { startedAt: at(0) }),
      row("a", "RESULT", { startedAt: at(6) }),
      row("retry-a", "RESULT", {
        child: true,
        tone: "failed",
        startedAt: at(2),
      }),
    ]);
    if (retried?.kind !== "call") throw new Error("Expected a call entry");
    expect(retried.rows.map((phase) => phase.requestId)).toEqual([
      "a",
      "retry-a",
      "a",
    ]);
    expect(flowFocusRow(retried, "RESULT")).toBe(retried.resultRow);
    expect(flowFocusRow(retried, "RESULT").requestId).toBe("a");
  });

  it("prefers the phase still running over the result", () => {
    const running = call([
      row("a", "CLIENT"),
      row("a", "UPSTREAM", { tone: "pending", endedAt: null }),
    ]);
    expect(flowFocusRow(running, "POLICY")).toBe(running.rows[1]);
  });
});

describe("call duration scale", () => {
  const at = (ms: number) =>
    new Date(Date.parse("2026-10-22T10:00:00Z") + ms).toISOString();
  const timed = (id: string, ms: number) =>
    row(id, "CLIENT", { startedAt: at(0), endedAt: at(ms) });

  it("measures each call against the longest call in the session", () => {
    const entries = trajectoryFlowEntries([timed("a", 1000), timed("b", 4000)]);
    const scale = flowDurationScale(entries, 5000, 0);
    expect(scale).toEqual({ kneeMs: 5000, maxWeight: 4000 });
    expect(flowDurationShare(1000, scale)).toBe(0.25);
    expect(flowDurationShare(8000, scale)).toBe(1);
    expect(flowDurationShare(null, scale)).toBe(0);
    expect(flowDurationShare(1000, { kneeMs: 5000, maxWeight: 0 })).toBe(0);
  });

  it("compresses a call past the knee as the timeline strip does", () => {
    const entries = trajectoryFlowEntries([
      timed("a", 5000),
      timed("b", 50_000),
    ]);
    const scale = flowDurationScale(entries, 5000, 0);
    expect(scale.maxWeight).toBeCloseTo(5000 * (1 + Math.log(10)));
    expect(flowDurationShare(5000, scale)).toBeCloseTo(1 / (1 + Math.log(10)));
  });
});

describe("trajectoryTurnDigests", () => {
  it("keeps one outcome per call under each turn, retries inside their call", () => {
    const entries = trajectoryFlowEntries([
      row("a", "TURN"),
      row("a", "CLIENT"),
      row("retry-a", "RETRY", { child: true, tone: "failed" }),
      row("b", "CLIENT", { tone: "failed" }),
      row("c", "TURN"),
      row("c", "UPSTREAM", { tone: "pending", endedAt: null }),
    ]);
    expect(
      Object.fromEntries(
        [...trajectoryTurnDigests(entries)].map(([turn, digest]) => [
          turn,
          digest.tones,
        ]),
      ),
    ).toEqual({
      "a:TURN": [
        { tone: "ok", running: false },
        { tone: "failed", running: false },
      ],
      "c:TURN": [{ tone: "pending", running: true }],
    });
  });
});

describe("flowHoldScrollDelta", () => {
  const hold = (
    pickTop: number,
    heldTop: number | null,
    regionBottom: number,
  ) =>
    flowHoldScrollDelta({
      pickTop,
      heldTop,
      regionBottom,
      listHeight: 400,
      topGap: 8,
      bottomGap: 12,
    });

  it("keeps a pick in place while what grew below it still fits", () => {
    expect(hold(220, 220, 380)).toBe(0);
    // Calls above it closed, so it moved up; scroll it back under the cursor.
    expect(hold(170, 220, 330)).toBe(-50);
  });

  it("scrolls what grew below a pick at the bottom back into the list", () => {
    // The open call ends 30 px under the edge: show it, with the gap below.
    expect(hold(220, 220, 430)).toBe(42);
  });

  it("never pushes the pick above the top edge to show a taller region", () => {
    expect(hold(220, 220, 900)).toBe(212);
    expect(hold(5, 5, 900)).toBe(-3);
  });

  it("brings a reveal to the top edge whatever lies below it", () => {
    expect(hold(300, null, 1200)).toBe(292);
    expect(hold(8, null, 30)).toBe(0);
  });
});
