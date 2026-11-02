import { CLIENT_TYPES, type ClientType } from "./request-record-model";
import {
  clientSeriesColor,
  modelSeriesColor,
  MUTED_SERIES_COLOR,
} from "./series-colors";
import type { UsageGroup, UsageSummary } from "./usage-range";

/** Models the star trail names; the rest share one muted row and color. */
export const STAR_TRAIL_MODEL_LIMIT = 5;

/** The ranges the star trail reads: a ring per model for a day, else per day. */
export const STAR_TRAIL_RANGES = ["1d", "7d", "30d"] as const;

export interface StarTrailModelRow {
  /** Null on the row that folds the rest. */
  id: string | null;
  total_tokens: number;
  requests: number;
  /** Whole percent of all tokens; the rows and the rest add up to 100. */
  percent: number;
  /** Bar length as a percentage of the largest row. */
  bar: number;
  color: string;
  /** Models folded into the rest row, the unknown model included. */
  folded: number;
}

export interface StarTrailLegend {
  rows: StarTrailModelRow[];
  rest: StarTrailModelRow | null;
}

/**
 * The top models by tokens, each in the color its arcs use, and one muted
 * row for every other model. Calls without a model always fold into the rest.
 */
export function starTrailLegend(
  byModel: readonly UsageGroup[],
  limit = STAR_TRAIL_MODEL_LIMIT,
): StarTrailLegend {
  const used = byModel
    .filter((group) => group.total_tokens > 0 || group.requests > 0)
    .sort(compareUsage);
  const named = used.filter((group) => group.id !== null).slice(0, limit);
  const shown = new Set(named);
  const folded = used.filter((group) => !shown.has(group));
  const rest =
    folded.length > 0
      ? {
          id: null,
          total_tokens: sum(folded, (group) => group.total_tokens),
          requests: sum(folded, (group) => group.requests),
        }
      : null;
  const percents = wholePercents([
    ...named.map((group) => group.total_tokens),
    rest?.total_tokens ?? 0,
  ]);
  const top = Math.max(0, ...named.map((group) => group.total_tokens));
  const row = (
    group: { id: string | null; total_tokens: number; requests: number },
    index: number,
    color: string,
    count: number,
  ): StarTrailModelRow => ({
    id: group.id,
    total_tokens: group.total_tokens,
    requests: group.requests,
    percent: percents[index],
    bar: top > 0 ? Math.round((group.total_tokens / top) * 100) : 0,
    color,
    folded: count,
  });
  return {
    rows: named.map((group, index) =>
      row(group, index, modelSeriesColor(group.id), 0),
    ),
    rest: rest
      ? row(rest, named.length, MUTED_SERIES_COLOR, folded.length)
      : null,
  };
}

export interface StarTrailArc {
  /** Unique in the trail: `date` and `hour`, and the model ring's row. */
  key: string;
  date: string;
  hour: number;
  /**
   * 0 is the innermost ring. By day the last ring is today; by model it is
   * the busiest model, and the muted rest, if any, is ring 0.
   */
  ring: number;
  requests: number;
  total_tokens: number;
  /**
   * By day, the hour's top model by tokens; by model, the ring's. Null when
   * none was recorded, and on the rest ring.
   */
  model: string | null;
  color: string;
  /**
   * By day, requests against the window's busiest hour; by model, tokens
   * against its busiest arc. 0–1.
   */
  brightness: number;
}

export interface StarTrail {
  /** What a ring stands for. */
  by: "day" | "model";
  /** Local days the window spans, oldest first. */
  days: string[];
  rings: number;
  /** Lit hours, ring by ring from the innermost, in time order. */
  arcs: StarTrailArc[];
}

/**
 * One arc per local hour with successful calls. The arc takes the color of
 * the hour's top model by tokens when the legend names it, and the muted rest
 * color otherwise, so every color on the disc appears in the legend.
 */
export function starTrail(
  summary: Pick<UsageSummary, "by_day" | "by_hour_model">,
  legend: StarTrailLegend,
): StarTrail {
  const days = summary.by_day.map((bucket) => bucket.date);
  const rings = new Map(days.map((date, index) => [date, index]));
  const named = new Map(legend.rows.map((row) => [row.id, row.color]));
  const hours = new Map<
    string,
    {
      date: string;
      hour: number;
      requests: number;
      total_tokens: number;
      top: { model: string | null; requests: number; total_tokens: number };
    }
  >();
  for (const bucket of summary.by_hour_model) {
    if (!rings.has(bucket.date) || bucket.requests <= 0) continue;
    const key = `${bucket.date}T${bucket.hour}`;
    const hour = hours.get(key);
    if (!hour) {
      hours.set(key, {
        date: bucket.date,
        hour: bucket.hour,
        requests: bucket.requests,
        total_tokens: bucket.total_tokens,
        top: bucket,
      });
      continue;
    }
    hour.requests += bucket.requests;
    hour.total_tokens += bucket.total_tokens;
    if (compareHourModel(bucket, hour.top) < 0) hour.top = bucket;
  }
  const busiest = Math.max(0, ...[...hours.values()].map((h) => h.requests));
  const arcs = [...hours.entries()]
    .map(([key, hour]) => ({
      key,
      date: hour.date,
      hour: hour.hour,
      ring: rings.get(hour.date)!,
      requests: hour.requests,
      total_tokens: hour.total_tokens,
      model: hour.top.model,
      color:
        (hour.top.model !== null && named.get(hour.top.model)) ||
        MUTED_SERIES_COLOR,
      brightness: busiest > 0 ? hour.requests / busiest : 0,
    }))
    .sort((left, right) => left.ring - right.ring || left.hour - right.hour);
  return { by: "day", days, rings: days.length, arcs };
}

/**
 * A day as a ring per model: the legend's models with the busiest outermost,
 * and inside them one muted ring for every other model. Each hour a model was
 * used is an arc in its color, as bright as its tokens against the busiest
 * arc, so the model list stays the legend.
 */
export function starTrailByModel(
  summary: Pick<UsageSummary, "by_day" | "by_hour_model">,
  legend: StarTrailLegend,
): StarTrail {
  const rows = new Map(legend.rows.map((row, index) => [row.id, index]));
  const hours = new Map<
    string,
    {
      row: number | null;
      date: string;
      hour: number;
      requests: number;
      total_tokens: number;
    }
  >();
  for (const bucket of summary.by_hour_model) {
    if (bucket.requests <= 0) continue;
    const row = bucket.model === null ? null : (rows.get(bucket.model) ?? null);
    const key = `${bucket.date}T${bucket.hour}/${row ?? "rest"}`;
    const hour = hours.get(key);
    if (hour) {
      hour.requests += bucket.requests;
      hour.total_tokens += bucket.total_tokens;
    } else {
      hours.set(key, {
        row,
        date: bucket.date,
        hour: bucket.hour,
        requests: bucket.requests,
        total_tokens: bucket.total_tokens,
      });
    }
  }
  const lit = [...hours.values()];
  const rest = legend.rest !== null || lit.some((hour) => hour.row === null);
  const rings = legend.rows.length + (rest ? 1 : 0);
  const busiest = Math.max(0, ...lit.map((hour) => hour.total_tokens));
  const arcs = [...hours.entries()]
    .map(([key, hour]) => ({
      key,
      date: hour.date,
      hour: hour.hour,
      ring: hour.row === null ? 0 : rings - 1 - hour.row,
      requests: hour.requests,
      total_tokens: hour.total_tokens,
      model: hour.row === null ? null : legend.rows[hour.row].id,
      color:
        hour.row === null ? MUTED_SERIES_COLOR : legend.rows[hour.row].color,
      brightness: busiest > 0 ? hour.total_tokens / busiest : 0,
    }))
    .sort(
      (left, right) =>
        left.ring - right.ring ||
        left.date.localeCompare(right.date) ||
        left.hour - right.hour,
    );
  return {
    by: "model",
    days: summary.by_day.map((bucket) => bucket.date),
    rings,
    arcs,
  };
}

export interface ClientShare {
  /** Null for unknown clients and calls recorded before clients were. */
  id: ClientType | null;
  total_tokens: number;
  /** Whole percent of all tokens; the segments add up to 100. */
  percent: number;
  color: string;
}

/**
 * Clients by tokens as one bar that adds up to 100%. Unknown clients,
 * historical calls and labels this build does not know form the last, muted
 * segment.
 */
export function clientShares(byClient: readonly UsageGroup[]): ClientShare[] {
  const known = new Map<ClientType, number>();
  let unknown = 0;
  for (const group of byClient) {
    const id = group.id as ClientType | null;
    if (id && id !== "unknown" && CLIENT_TYPES.includes(id)) {
      known.set(id, (known.get(id) ?? 0) + group.total_tokens);
    } else {
      unknown += group.total_tokens;
    }
  }
  const segments: Array<{ id: ClientType | null; total_tokens: number }> = [
    ...known,
  ]
    .map(([id, total_tokens]) => ({ id, total_tokens }))
    .filter((segment) => segment.total_tokens > 0)
    .sort(
      (left, right) =>
        right.total_tokens - left.total_tokens ||
        (left.id ?? "").localeCompare(right.id ?? ""),
    );
  if (unknown > 0) segments.push({ id: null, total_tokens: unknown });
  const percents = wholePercents(segments.map((s) => s.total_tokens));
  return segments.map((segment, index) => ({
    ...segment,
    percent: percents[index],
    color: clientSeriesColor(segment.id),
  }));
}

/**
 * Whole percentages of `values` that add up to exactly 100 when any value is
 * positive: each takes its floor, and the largest remainders get the rest.
 */
export function wholePercents(values: readonly number[]): number[] {
  const total = sum(values, (value) => Math.max(0, value));
  if (total <= 0) return values.map(() => 0);
  const exact = values.map((value) => (Math.max(0, value) / total) * 100);
  const result = exact.map(Math.floor);
  let left = 100 - sum(result, (value) => value);
  const order = exact
    .map((value, index) => ({ index, remainder: value - Math.floor(value) }))
    .sort((a, b) => b.remainder - a.remainder || a.index - b.index);
  for (const { index } of order) {
    if (left <= 0) break;
    result[index] += 1;
    left -= 1;
  }
  return result;
}

function compareUsage(left: UsageGroup, right: UsageGroup): number {
  return (
    right.total_tokens - left.total_tokens ||
    right.requests - left.requests ||
    (left.id ?? "").localeCompare(right.id ?? "")
  );
}

/** Most tokens first, then most calls; a recorded model wins a tie. */
function compareHourModel(
  left: { model: string | null; requests: number; total_tokens: number },
  right: { model: string | null; requests: number; total_tokens: number },
): number {
  if (left.total_tokens !== right.total_tokens)
    return right.total_tokens - left.total_tokens;
  if (left.requests !== right.requests) return right.requests - left.requests;
  if (left.model === null || right.model === null)
    return left.model === null ? (right.model === null ? 0 : 1) : -1;
  return left.model.localeCompare(right.model);
}

function sum<T>(items: readonly T[], value: (item: T) => number): number {
  return items.reduce((total, item) => total + value(item), 0);
}
