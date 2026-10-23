import type { TrajectoryRow } from "./request-trajectory-model";

export interface TrajectoryCardRow {
  edge: "first" | "middle" | "last" | "only";
  callNumber: number;
  timing?: {
    /** Fractions of this request segment's known time window. */
    offset: number;
    duration: number;
    durationMs: number;
  };
  /** The shared request window; open windows may extend with a live clock. */
  timingWindow?: { startMs: number; endMs: number; open: boolean };
}

function phaseInterval(row: TrajectoryRow, nowMs: number | undefined) {
  const start = Date.parse(row.startedAt);
  const end =
    row.endedAt !== null
      ? Date.parse(row.endedAt)
      : row.tone === "pending"
        ? nowMs
        : undefined;
  return Number.isFinite(start) &&
    end !== undefined &&
    Number.isFinite(end) &&
    end >= start
    ? { start, end }
    : null;
}

/**
 * Card edges without wrapping or replacing the virtualized phase rows.
 * Pass the complete trajectoryRows output before folding: its root phases
 * precede their retry children, so retries keep their root's call number.
 * A standalone child with no preceding root receives its own number.
 * Valid intervals in each request segment define its independent time window.
 * An unfinished pending phase is timed only when the caller supplies nowMs.
 */
export function trajectoryCardLayout(
  rows: readonly TrajectoryRow[],
  nowMs?: number,
): Map<string, TrajectoryCardRow> {
  const layout = new Map<string, TrajectoryCardRow>();
  const callNumbers = new Map<string, number>();
  let parentRequestId: string | null = null;

  for (let start = 0; start < rows.length; ) {
    const first = rows[start]!;
    if (first.chip === "TURN") {
      parentRequestId = null;
      start += 1;
      continue;
    }

    let end = start + 1;
    while (
      end < rows.length &&
      rows[end]!.chip !== "TURN" &&
      rows[end]!.requestId === first.requestId
    ) {
      end += 1;
    }

    if (!first.child) parentRequestId = first.requestId;
    const callId = first.child
      ? (parentRequestId ?? first.requestId)
      : first.requestId;
    let callNumber = callNumbers.get(callId);
    if (callNumber === undefined) {
      callNumber = callNumbers.size + 1;
      callNumbers.set(callId, callNumber);
    }

    const intervals = rows
      .slice(start, end)
      .map((row) => phaseInterval(row, nowMs));
    let windowStart = Infinity;
    let windowEnd = -Infinity;
    for (const interval of intervals) {
      if (!interval) continue;
      windowStart = Math.min(windowStart, interval.start);
      windowEnd = Math.max(windowEnd, interval.end);
    }
    const windowMs = windowEnd - windowStart;
    const timingWindow =
      Number.isFinite(windowStart) && Number.isFinite(windowEnd)
        ? {
            startMs: windowStart,
            endMs: windowEnd,
            open: intervals.some(
              (interval, index) =>
                interval !== null &&
                rows[start + index]!.endedAt === null &&
                rows[start + index]!.tone === "pending",
            ),
          }
        : undefined;

    for (let index = start; index < end; index += 1) {
      const interval = intervals[index - start];
      const offset =
        interval && windowMs > 0
          ? Math.min(1, Math.max(0, (interval.start - windowStart) / windowMs))
          : 0;
      layout.set(rows[index]!.id, {
        edge:
          end - start === 1
            ? "only"
            : index === start
              ? "first"
              : index === end - 1
                ? "last"
                : "middle",
        callNumber,
        ...(timingWindow ? { timingWindow } : {}),
        ...(interval
          ? {
              timing: {
                offset,
                duration:
                  windowMs > 0
                    ? Math.min(
                        1 - offset,
                        Math.max(0, (interval.end - interval.start) / windowMs),
                      )
                    : 0,
                durationMs: interval.end - interval.start,
              },
            }
          : {}),
      });
    }
    start = end;
  }

  return layout;
}
