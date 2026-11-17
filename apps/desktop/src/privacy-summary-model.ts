import { privacyKindLabel } from "./privacy-kind";
import {
  hourlyUsageWindow,
  usageWindowDayKeys,
  usageWindowHourSlots,
  type UsageWindow,
} from "./usage-range";

/**
 * What the privacy policy did over a window. Mirrors `PrivacyTotals` in
 * `core/internal/storage/privacy_summary.go`: every finished root call is in
 * `requests`, and at most one outcome count holds it.
 */
export interface PrivacyTotals {
  requests: number;
  /** Calls with nothing to protect, matches of a disabled kind included. */
  allowed: number;
  /** Calls allowed only because every match was allowlisted or too unsure. */
  ignored: number;
  warned: number;
  redacted: number;
  blocked: number;
  /** Calls that went to the model unchecked because the check failed. */
  fail_open: number;
  /** Calls that went uninspected to a provider the user trusts. */
  trusted: number;
  /** Distinct values each call replaced, summed over calls. */
  replaced: number;
  restored: number;
  restored_visible: number;
  restored_tool_arguments: number;
  /** Answer parts left with stand-in values in them. */
  unrestored: number;
  /** Matches left in requests by the allowlist, over every outcome. */
  ignored_allowlisted: number;
  /** Matches left in requests below the confidence threshold. */
  ignored_low_confidence: number;
}

export interface PrivacyDayBucket extends PrivacyTotals {
  /** Local calendar day as `YYYY-MM-DD`. */
  date: string;
}

export interface PrivacyHourBucket extends PrivacyTotals {
  date: string;
  /** Local hour of day, 0–23. */
  hour: number;
}

export interface PrivacyKindCount {
  kind: string;
  count: number;
}

export interface PrivacySummary {
  window: UsageWindow;
  totals: PrivacyTotals;
  /** One bucket per local day in the window, empty days included. */
  by_day: PrivacyDayBucket[];
  /** 24 local-hour buckets for a `1d` window; empty for longer ranges. */
  by_hour: PrivacyHourBucket[];
  /** The most frequent kind first. */
  by_kind: PrivacyKindCount[];
}

const totalKeys = [
  "requests",
  "allowed",
  "ignored",
  "warned",
  "redacted",
  "blocked",
  "fail_open",
  "trusted",
  "replaced",
  "restored",
  "restored_visible",
  "restored_tool_arguments",
  "unrestored",
  "ignored_allowlisted",
  "ignored_low_confidence",
] as const satisfies readonly (keyof PrivacyTotals)[];

/** Core names ten kinds; the bound leaves room for newer ones. */
const MAX_KINDS = 64;

/**
 * A kind's name; a custom kind reads as its display name, and a kind newer
 * than this build reads as itself.
 */
const kindLabel = privacyKindLabel;

export function emptyPrivacyTotals(): PrivacyTotals {
  return Object.fromEntries(
    totalKeys.map((key) => [key, 0]),
  ) as unknown as PrivacyTotals;
}

export function emptyPrivacySummary(window: UsageWindow): PrivacySummary {
  return {
    window,
    totals: emptyPrivacyTotals(),
    by_day: usageWindowDayKeys(window).map((date) => ({
      date,
      ...emptyPrivacyTotals(),
    })),
    by_hour: usageWindowHourSlots(window).map((slot) => ({
      ...slot,
      ...emptyPrivacyTotals(),
    })),
    by_kind: [],
  };
}

function objectAt(
  value: unknown,
  keys: readonly string[],
): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid privacy summary object");
  }
  const object = value as Record<string, unknown>;
  if (
    Object.keys(object).length !== keys.length ||
    keys.some((key) => !Object.hasOwn(object, key))
  ) {
    throw new Error("Invalid privacy summary fields");
  }
  return object;
}

function count(value: unknown): number {
  if (!Number.isSafeInteger(value) || (value as number) < 0) {
    throw new Error("Invalid privacy summary count");
  }
  return value as number;
}

function arrayAt(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new Error("Invalid privacy summary array");
  return value;
}

function totalsAt(object: Record<string, unknown>): PrivacyTotals {
  const totals = Object.fromEntries(
    totalKeys.map((key) => [key, count(object[key])]),
  ) as unknown as PrivacyTotals;
  if (
    totals.allowed +
      totals.ignored +
      totals.warned +
      totals.redacted +
      totals.blocked +
      totals.fail_open +
      totals.trusted >
      totals.requests ||
    totals.restored_visible + totals.restored_tool_arguments > totals.restored
  ) {
    throw new Error("Inconsistent privacy summary counts");
  }
  return totals;
}

/** Validates Core's sparse summary and pads it to every slot of `window`. */
export function parsePrivacySummary(
  value: unknown,
  window: UsageWindow,
): PrivacySummary {
  const object = objectAt(value, ["totals", "by_day", "by_hour", "by_kind"]);
  const summary = emptyPrivacySummary(window);
  summary.totals = totalsAt(objectAt(object.totals, totalKeys));
  const fill = (raw: unknown, hourly: boolean) => {
    const buckets: Array<PrivacyDayBucket | PrivacyHourBucket> = hourly
      ? summary.by_hour
      : summary.by_day;
    const keyOf = (date: unknown, hour: unknown) =>
      hourly ? `${date}T${hour}` : String(date);
    const byKey = new Map(
      buckets.map((bucket) => [
        keyOf(bucket.date, "hour" in bucket ? bucket.hour : undefined),
        bucket,
      ]),
    );
    const seen = new Set<string>();
    for (const value of arrayAt(raw)) {
      const bucket = objectAt(value, [
        "date",
        ...(hourly ? ["hour"] : []),
        ...totalKeys,
      ]);
      if (
        typeof bucket.date !== "string" ||
        !/^\d{4}-\d{2}-\d{2}$/.test(bucket.date) ||
        (hourly &&
          (!Number.isInteger(bucket.hour) ||
            (bucket.hour as number) < 0 ||
            (bucket.hour as number) > 23))
      ) {
        throw new Error("Invalid privacy summary bucket");
      }
      const key = keyOf(bucket.date, bucket.hour);
      const target = byKey.get(key);
      if (!target || seen.has(key))
        throw new Error("Unexpected privacy summary bucket");
      seen.add(key);
      Object.assign(target, totalsAt(bucket));
    }
  };
  fill(object.by_day, false);
  fill(object.by_hour, true);
  const kinds = arrayAt(object.by_kind);
  if (kinds.length > MAX_KINDS) throw new Error("Too many privacy kinds");
  const seenKinds = new Set<string>();
  summary.by_kind = kinds.map((value) => {
    const entry = objectAt(value, ["kind", "count"]);
    if (
      typeof entry.kind !== "string" ||
      !/^[a-z][a-z0-9_]{0,63}$/.test(entry.kind) ||
      seenKinds.has(entry.kind)
    ) {
      throw new Error("Invalid privacy summary kind");
    }
    seenKinds.add(entry.kind);
    const kindCount = count(entry.count);
    if (kindCount === 0) throw new Error("Invalid privacy summary kind count");
    return { kind: entry.kind, count: kindCount };
  });
  return summary;
}

/** Calls the policy finished checking. */
export function checkedCalls(totals: PrivacyTotals): number {
  return (
    totals.allowed +
    totals.ignored +
    totals.warned +
    totals.redacted +
    totals.blocked
  );
}

/** Checked calls in which the policy found private data. */
export function flaggedCalls(totals: PrivacyTotals): number {
  return totals.warned + totals.redacted + totals.blocked;
}

/**
 * The policy reached at least one call, whether or not its check finished.
 * Without that there is nothing for the overview to show.
 */
export function hasPrivacyActivity(totals: PrivacyTotals): boolean {
  return checkedCalls(totals) + totals.fail_open > 0;
}

/**
 * Calls that reached the model without any check because no policy applied:
 * protection was off, or the record predates stored decisions. Calls to a
 * trusted provider are counted apart, in `trusted`.
 */
export function unprotectedCalls(totals: PrivacyTotals): number {
  return Math.max(
    0,
    totals.requests - checkedCalls(totals) - totals.fail_open - totals.trusted,
  );
}

/** Whole percent of `part` in `whole`; 0 without a whole. */
export function sharePercent(part: number, whole: number): number {
  if (whole <= 0) return 0;
  return Math.round((part * 100) / whole);
}

export interface PrivacyKindShare {
  kind: string;
  label: string;
  count: number;
  /** Share of every kind found, 0–100. */
  percent: number;
}

/**
 * Kinds with their share of everything found, most frequent first. `limit`
 * keeps the head and reports how many kinds it left out.
 */
export function privacyKindShares(
  kinds: readonly PrivacyKindCount[],
  limit = Infinity,
): { rows: PrivacyKindShare[]; hiddenKinds: number } {
  const total = kinds.reduce((sum, kind) => sum + kind.count, 0);
  const sorted = [...kinds].sort((left, right) => right.count - left.count);
  const rows = sorted.slice(0, limit).map((kind) => ({
    kind: kind.kind,
    label: kindLabel(kind.kind),
    count: kind.count,
    percent: total > 0 ? (kind.count * 100) / total : 0,
  }));
  return { rows, hiddenKinds: sorted.length - rows.length };
}

/** How a checked call ended, in the order the outcome bar stacks them. */
export const PRIVACY_OUTCOME_SEGMENTS = [
  "redacted",
  "blocked",
  "warned",
  "fail_open",
  "ignored",
  "allowed",
] as const;
export type PrivacyOutcomeSegment = (typeof PRIVACY_OUTCOME_SEGMENTS)[number];

export interface PrivacyTrendPoint {
  /** Stable chart key: the day, or the day and hour. */
  slot: string;
  date: string;
  hour?: number;
  replaced: number;
  restored: number;
  flagged: number;
  blocked: number;
}

/** Hourly points for an hourly window, daily points otherwise. */
export function privacyTrendPoints(
  summary: PrivacySummary,
): PrivacyTrendPoint[] {
  const buckets: Array<PrivacyDayBucket & { hour?: number }> =
    hourlyUsageWindow(summary.window) ? summary.by_hour : summary.by_day;
  return buckets.map((bucket) => ({
    slot:
      bucket.hour == null
        ? bucket.date
        : `${bucket.date}T${String(bucket.hour).padStart(2, "0")}`,
    date: bucket.date,
    ...(bucket.hour == null ? {} : { hour: bucket.hour }),
    replaced: bucket.replaced,
    restored: bucket.restored,
    flagged: flaggedCalls(bucket),
    blocked: bucket.blocked,
  }));
}
