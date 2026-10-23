import {
  callProgressAtScrollLeft,
  scrollLeftForCall,
  timelineWeight,
  type TrajectoryCallColumn,
  type TrajectoryCallProgress,
  type TrajectoryChip,
  type TrajectoryLane,
  type TrajectoryRow,
  type TrajectoryTone,
} from "./request-trajectory-model";
import {
  trajectoryCardLayout,
  type TrajectoryCardRow,
} from "./trajectory-card-model";

type TimingWindow = NonNullable<TrajectoryCardRow["timingWindow"]>;

export interface TrajectoryFlowTurn {
  id: string;
  kind: "turn";
  requestId: string;
  turnId: string;
  row: TrajectoryRow;
  rows: [TrajectoryRow];
}

/** One request of a call: the root, or a retry the gateway sent for it. */
export interface TrajectoryFlowAttempt {
  requestId: string;
  child: boolean;
  rows: TrajectoryRow[];
  primaryRow: TrajectoryRow;
  resultRow: TrajectoryRow;
  activeRow: TrajectoryRow | null;
  /** An ended phase that still carries pending status must not animate. */
  running: boolean;
  timingWindow?: TimingWindow;
}

export interface TrajectoryFlowCall {
  id: string;
  kind: "call";
  /** The root request; retries keep it, as the timeline strip does. */
  requestId: string;
  turnId: string | null;
  /** Every phase of every attempt, in recorded order. */
  rows: TrajectoryRow[];
  attempts: TrajectoryFlowAttempt[];
  firstRow: TrajectoryRow;
  lastRow: TrajectoryRow;
  primaryRow: TrajectoryRow;
  /** The final attempt's result: what the client was left with. */
  resultRow: TrajectoryRow;
  activeRow: TrajectoryRow | null;
  tone: TrajectoryTone;
  running: boolean;
  /** A retry listed without its root. */
  child: boolean;
  callNumber: number;
  /** All attempts together, from the first start to the last end. */
  timingWindow?: TimingWindow;
}

export type TrajectoryFlowEntry = TrajectoryFlowTurn | TrajectoryFlowCall;

/**
 * Group adjacent phases into calls, retaining the original row objects for
 * selection, find and inspector actions. A retry joins the call it retried,
 * since it shares that call's number. With folded rows, pass the layout
 * computed from the full trajectory so call numbering and retry parents remain
 * stable. Turn headers remain independently addressable virtual entries.
 */
export function trajectoryFlowEntries(
  rows: readonly TrajectoryRow[],
  cardLayout: ReadonlyMap<string, TrajectoryCardRow> = trajectoryCardLayout(
    rows,
  ),
): TrajectoryFlowEntry[] {
  const entries: TrajectoryFlowEntry[] = [];
  let turnId: string | null = null;

  for (let start = 0; start < rows.length; ) {
    const firstRow = rows[start]!;
    if (firstRow.chip === "TURN") {
      turnId = firstRow.id;
      entries.push({
        id: firstRow.id,
        kind: "turn",
        requestId: firstRow.requestId,
        turnId,
        row: firstRow,
        rows: [firstRow],
      });
      start += 1;
      continue;
    }

    let end = start + 1;
    while (
      end < rows.length &&
      rows[end]!.chip !== "TURN" &&
      rows[end]!.requestId === firstRow.requestId
    ) {
      end += 1;
    }

    const attempt = flowAttempt(rows.slice(start, end), cardLayout);
    const callNumber = cardLayout.get(firstRow.id)?.callNumber ?? 0;
    const previous = entries[entries.length - 1];
    if (
      attempt.child &&
      previous?.kind === "call" &&
      previous.callNumber === callNumber
    ) {
      entries[entries.length - 1] = flowCall(
        [...previous.attempts, attempt],
        previous.turnId,
        callNumber,
      );
    } else {
      entries.push(flowCall([attempt], turnId, callNumber));
    }
    start = end;
  }

  return entries;
}

function flowAttempt(
  phases: TrajectoryRow[],
  cardLayout: ReadonlyMap<string, TrajectoryCardRow>,
): TrajectoryFlowAttempt {
  const firstRow = phases[0]!;
  let resultRow = phases[phases.length - 1]!;
  let activeRow: TrajectoryRow | null = null;
  for (const row of phases) {
    if (row.chip === "RESULT") resultRow = row;
    if (row.tone === "pending" && row.endedAt === null) activeRow = row;
  }
  const settledResult =
    resultRow.chip === "RESULT" && resultRow.tone !== "pending";
  // A completed result is authoritative even when an older recorded phase
  // never received its matching end event.
  if (settledResult) activeRow = null;
  const card = cardLayout.get(firstRow.id);
  // Older phases can retain a pending status after the request completed.
  // Do not let their live-clock intervals inflate a terminal call's duration.
  const timingWindow =
    settledResult && card?.timingWindow?.open
      ? trajectoryCardLayout(phases).get(firstRow.id)?.timingWindow
      : card?.timingWindow;
  return {
    requestId: firstRow.requestId,
    child: firstRow.child,
    rows: phases,
    primaryRow: phases.find((row) => row.chip === "CLIENT") ?? firstRow,
    resultRow,
    activeRow,
    running: activeRow !== null,
    ...(timingWindow ? { timingWindow } : {}),
  };
}

function flowCall(
  attempts: TrajectoryFlowAttempt[],
  turnId: string | null,
  callNumber: number,
): TrajectoryFlowCall {
  const first = attempts[0]!;
  // A retry child is an attempt that failed before the root's final one; the
  // root keeps what the client was answered.
  const root = attempts.find((attempt) => !attempt.child) ?? first;
  const rows = chronologicalRows(attempts);
  const activeRow =
    [...attempts].reverse().find((attempt) => attempt.running)?.activeRow ??
    null;
  const timingWindow = unionWindow(attempts);
  return {
    id: first.rows[0]!.id,
    kind: "call",
    requestId: first.requestId,
    turnId,
    rows,
    attempts,
    firstRow: rows[0]!,
    lastRow: rows[rows.length - 1]!,
    primaryRow: root.primaryRow,
    resultRow: root.resultRow,
    activeRow,
    tone: (activeRow ?? root.resultRow).tone,
    running: activeRow !== null,
    child: first.child,
    callNumber,
    ...(timingWindow ? { timingWindow } : {}),
  };
}

/**
 * Retries are listed after their root but happened before its final
 * attempt, so the route follows the clock. A phase without a valid start
 * stays behind the phase listed before it.
 */
function chronologicalRows(
  attempts: readonly TrajectoryFlowAttempt[],
): TrajectoryRow[] {
  if (attempts.length === 1) return attempts[0]!.rows;
  let carried = -Infinity;
  return attempts
    .flatMap((attempt) => attempt.rows)
    .map((row, index) => {
      const start = Date.parse(row.startedAt);
      if (Number.isFinite(start)) carried = start;
      return { row, index, start: carried };
    })
    .sort((a, b) => a.start - b.start || a.index - b.index)
    .map(({ row }) => row);
}

function unionWindow(
  attempts: readonly TrajectoryFlowAttempt[],
): TimingWindow | undefined {
  const windows = attempts.flatMap((attempt) =>
    attempt.timingWindow ? [attempt.timingWindow] : [],
  );
  if (windows.length <= 1) return windows[0];
  return {
    startMs: Math.min(...windows.map((window) => window.startMs)),
    endMs: Math.max(...windows.map((window) => window.endMs)),
    open: windows.some((window) => window.open),
  };
}

export type TrajectoryFlowLane = 0 | 1 | 2;

const laneOrder: Record<TrajectoryLane, TrajectoryFlowLane> = {
  client: 0,
  gateway: 1,
  upstream: 2,
};

/** One phase as the route draws it, between the lanes it came from and goes to. */
export interface TrajectoryFlowStep {
  row: TrajectoryRow;
  lane: TrajectoryFlowLane;
  fromLane: TrajectoryFlowLane | null;
  toLane: TrajectoryFlowLane | null;
  /** Null where the record leaves the phase's end unknown. */
  durationMs: number | null;
  /** The phase is still running, so its duration follows the clock. */
  pending: boolean;
  /** The first phase of a retry. */
  retryStart: boolean;
}

/**
 * The route a call took through client, gateway and upstream. Only an
 * unfinished phase of a running attempt is timed against `nowMs`; a settled
 * phase without an end is unknown, never in flight.
 */
export function trajectoryFlowSteps(
  call: TrajectoryFlowCall,
  nowMs: number,
): TrajectoryFlowStep[] {
  const attemptOf = new Map<TrajectoryRow, TrajectoryFlowAttempt>();
  for (const attempt of call.attempts) {
    for (const row of attempt.rows) attemptOf.set(row, attempt);
  }
  const started = new Set<TrajectoryFlowAttempt>();
  const steps = call.rows.map((row) => {
    const attempt = attemptOf.get(row);
    const pending = Boolean(
      attempt?.running && row.tone === "pending" && row.endedAt === null,
    );
    const retryStart = Boolean(attempt?.child) && !started.has(attempt!);
    if (attempt) started.add(attempt);
    return {
      row,
      lane: laneOrder[row.lane],
      durationMs: phaseDurationMs(row, pending ? nowMs : undefined),
      pending,
      retryStart: retryStart && call.attempts.length > 1,
    };
  });
  return steps.map((step, index) => ({
    ...step,
    fromLane: steps[index - 1]?.lane ?? null,
    toLane: steps[index + 1]?.lane ?? null,
  }));
}

/** A phase's recorded duration, or its running time when `nowMs` is given. */
export function phaseDurationMs(
  row: TrajectoryRow,
  nowMs?: number,
): number | null {
  const start = Date.parse(row.startedAt);
  const end = row.endedAt === null ? (nowMs ?? NaN) : Date.parse(row.endedAt);
  return Number.isFinite(start) && Number.isFinite(end) && end >= start
    ? end - start
    : null;
}

/** The call's span, extended to `nowMs` only while an attempt is running. */
export function flowCallSpanMs(
  call: TrajectoryFlowCall,
  nowMs: number,
): number | null {
  const window = call.timingWindow;
  if (!window) return null;
  const end = call.running ? Math.max(window.endMs, nowMs) : window.endMs;
  return Math.max(0, end - window.startMs);
}

/**
 * The phase a call opens on: the same kind of phase the reader was looking
 * at, so stepping between calls compares like with like, then the phase
 * still running, then the final result. The root's own phase wins over a
 * failed attempt's, since the root holds what the client was answered.
 */
export function flowFocusRow(
  call: TrajectoryFlowCall,
  preferredChip?: TrajectoryChip | null,
): TrajectoryRow {
  const kind =
    preferredChip && preferredChip !== "TURN"
      ? call.rows.filter((row) => row.chip === preferredChip)
      : [];
  const same = kind.find((row) => row.requestId === call.requestId) ?? kind[0];
  return same ?? call.activeRow ?? call.resultRow;
}

/**
 * How far to scroll a list holding a pick in place while what is around it
 * opens and closes. Offsets are measured from the list's top edge. The pick
 * stays where it was clicked unless the region below it, which it expects
 * to see, would then end past the bottom edge; it never goes above the top
 * edge for that. Without a held offset the pick goes to the top.
 */
export function flowHoldScrollDelta({
  pickTop,
  heldTop,
  regionBottom,
  listHeight,
  topGap,
  bottomGap,
}: {
  pickTop: number;
  heldTop: number | null;
  regionBottom: number;
  listHeight: number;
  topGap: number;
  bottomGap: number;
}): number {
  if (heldTop === null) return pickTop - topGap;
  const below = Math.max(0, regionBottom - pickTop);
  return (
    pickTop -
    Math.max(topGap, Math.min(heldTop, listHeight - below - bottomGap))
  );
}

/** Duration on the same compressed scale the timeline strip draws. */
export interface FlowDurationScale {
  kneeMs: number;
  maxWeight: number;
}

export function flowDurationScale(
  entries: readonly TrajectoryFlowEntry[],
  kneeMs: number,
  nowMs: number,
): FlowDurationScale {
  let maxWeight = 0;
  for (const entry of entries) {
    if (entry.kind !== "call") continue;
    const span = flowCallSpanMs(entry, nowMs);
    if (span !== null) {
      maxWeight = Math.max(maxWeight, timelineWeight(span, kneeMs));
    }
  }
  return { kneeMs, maxWeight };
}

/** A duration's share of the session's longest call, from 0 to 1. */
export function flowDurationShare(
  durationMs: number | null,
  scale: FlowDurationScale,
): number {
  if (durationMs === null || scale.maxWeight <= 0) return 0;
  return Math.min(
    1,
    timelineWeight(durationMs, scale.kneeMs) / scale.maxWeight,
  );
}

/** What a folded turn shows of its calls: one outcome per call. */
export interface FlowTurnDigest {
  tones: { tone: TrajectoryTone; running: boolean }[];
}

export function trajectoryTurnDigests(
  entries: readonly TrajectoryFlowEntry[],
): Map<string, FlowTurnDigest> {
  const digests = new Map<string, FlowTurnDigest>();
  for (const entry of entries) {
    if (entry.kind !== "call" || entry.turnId === null) continue;
    const digest = digests.get(entry.turnId) ?? { tones: [] };
    digest.tones.push({ tone: entry.tone, running: entry.running });
    digests.set(entry.turnId, digest);
  }
  return digests;
}

export function trajectoryFlowRowIndices(
  entries: readonly TrajectoryFlowEntry[],
): Map<string, number> {
  const indices = new Map<string, number>();
  entries.forEach((entry, index) => {
    for (const row of entry.rows) indices.set(row.id, index);
  });
  return indices;
}

export interface TrajectoryFlowMeasurement {
  id: string;
  requestId: string;
  start: number;
  size: number;
}

export interface TrajectoryFlowScrollAnchor {
  id: string;
  requestId: string;
  fraction: number;
}

function clampFraction(fraction: number): number {
  return Number.isNaN(fraction) ? 0 : Math.min(1, Math.max(0, fraction));
}

function clampScroll(offset: number, maxScroll: number): number {
  if (!Number.isFinite(maxScroll) || maxScroll <= 0) return 0;
  return Math.min(maxScroll, Math.max(0, offset));
}

function validMeasurements(entries: readonly TrajectoryFlowMeasurement[]) {
  return entries.filter(
    (entry) =>
      Number.isFinite(entry.start) &&
      Number.isFinite(entry.size) &&
      entry.size >= 0,
  );
}

/**
 * Anchor within a measured entry rather than a fixed-height phase row. Gaps
 * attach to the following entry at its start, and overscroll clamps at either
 * end. Keep the id when restoring after a card expands or contracts.
 */
export function flowScrollAnchorAtOffset(
  entries: readonly TrajectoryFlowMeasurement[],
  scrollTop: number,
): TrajectoryFlowScrollAnchor | null {
  const measured = validMeasurements(entries);
  if (measured.length === 0) return null;
  const offset = Number.isNaN(scrollTop) ? 0 : Math.max(0, scrollTop);
  const entry =
    measured.find((item) => offset < item.start + item.size) ??
    measured[measured.length - 1]!;
  return {
    id: entry.id,
    requestId: entry.requestId,
    fraction:
      entry.size > 0 ? clampFraction((offset - entry.start) / entry.size) : 0,
  };
}

export function flowScrollOffsetForAnchor(
  entries: readonly TrajectoryFlowMeasurement[],
  anchor: TrajectoryFlowScrollAnchor,
  maxScroll: number,
): number {
  const entry = validMeasurements(entries).find(
    (item) => item.id === anchor.id,
  );
  if (!entry) {
    return flowScrollOffsetForCall(
      entries,
      anchor.requestId,
      anchor.fraction,
      maxScroll,
    );
  }
  return clampScroll(
    entry.start + clampFraction(anchor.fraction) * entry.size,
    maxScroll,
  );
}

/**
 * Match the timeline's request position against measured vertical geometry.
 * A TURN header and its first call can share a request id; their whole span is
 * used so a folded turn and an expanded first call both stay reachable.
 */
export function flowScrollOffsetForCall(
  entries: readonly TrajectoryFlowMeasurement[],
  requestId: string,
  fraction: number,
  maxScroll: number,
): number {
  const matching = validMeasurements(entries).filter(
    (entry) => entry.requestId === requestId,
  );
  const first = matching[0];
  const last = matching[matching.length - 1];
  if (!first || !last) return 0;
  const size = Math.max(0, last.start + last.size - first.start);
  return clampScroll(first.start + clampFraction(fraction) * size, maxScroll);
}

/** The timeline sees the whole request span, including its TURN header. */
export function flowCallProgressAtOffset(
  entries: readonly TrajectoryFlowMeasurement[],
  scrollTop: number,
): TrajectoryCallProgress | null {
  const anchor = flowScrollAnchorAtOffset(entries, scrollTop);
  if (!anchor) return null;
  const matching = validMeasurements(entries).filter(
    (entry) => entry.requestId === anchor.requestId,
  );
  const first = matching[0]!;
  const last = matching[matching.length - 1]!;
  const size = Math.max(0, last.start + last.size - first.start);
  return {
    requestId: anchor.requestId,
    fraction: size > 0 ? clampFraction((scrollTop - first.start) / size) : 0,
  };
}

/**
 * Project through both complete scroll ranges, using measured card geometry.
 * Mapping only leading edges would clamp the shorter viewport's scroll before
 * the other pane reaches its final call.
 */
export function flowTimelineScrollForList(
  entries: readonly TrajectoryFlowMeasurement[],
  columns: readonly TrajectoryCallColumn[],
  listOffset: number,
  listMaxScroll: number,
  timelineMaxScroll: number,
): number {
  const measured = validMeasurements(entries);
  const lastEntry = measured[measured.length - 1];
  const listExtent = lastEntry ? lastEntry.start + lastEntry.size : 0;
  const lastColumn = columns[columns.length - 1];
  const timelineExtent = lastColumn ? lastColumn.offset + lastColumn.width : 0;
  if (
    listMaxScroll <= 0 ||
    timelineMaxScroll <= 0 ||
    listExtent <= 0 ||
    timelineExtent <= 0
  ) {
    return 0;
  }
  const ratio = clampFraction(listOffset / listMaxScroll);
  const progress = flowCallProgressAtOffset(measured, ratio * listExtent);
  if (!progress) return 0;
  const position = scrollLeftForCall(
    columns,
    progress.requestId,
    progress.fraction,
    timelineExtent,
  );
  return clampScroll(
    (position / timelineExtent) * timelineMaxScroll,
    timelineMaxScroll,
  );
}

export function flowListScrollForTimeline(
  entries: readonly TrajectoryFlowMeasurement[],
  columns: readonly TrajectoryCallColumn[],
  timelineOffset: number,
  timelineMaxScroll: number,
  listMaxScroll: number,
): number {
  const measured = validMeasurements(entries);
  const lastEntry = measured[measured.length - 1];
  const listExtent = lastEntry ? lastEntry.start + lastEntry.size : 0;
  const lastColumn = columns[columns.length - 1];
  const timelineExtent = lastColumn ? lastColumn.offset + lastColumn.width : 0;
  if (
    listMaxScroll <= 0 ||
    timelineMaxScroll <= 0 ||
    listExtent <= 0 ||
    timelineExtent <= 0
  ) {
    return 0;
  }
  const ratio = clampFraction(timelineOffset / timelineMaxScroll);
  const progress = callProgressAtScrollLeft(columns, ratio * timelineExtent);
  if (!progress) return 0;
  const position = flowScrollOffsetForCall(
    measured,
    progress.requestId,
    progress.fraction,
    listExtent,
  );
  return clampScroll((position / listExtent) * listMaxScroll, listMaxScroll);
}
