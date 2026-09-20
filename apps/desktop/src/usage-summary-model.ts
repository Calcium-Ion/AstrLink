import {
  emptyUsageSummary,
  emptyUsageTotals,
  type UsageGroup,
  type UsageSummary,
  type UsageTotals,
  type UsageWindow,
} from "./usage-range";

const totalKeys = Object.keys(emptyUsageTotals());

function objectAt(value: unknown, keys: string[]): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid usage summary object");
  }
  const object = value as Record<string, unknown>;
  if (Object.keys(object).length !== keys.length || keys.some((key) => !Object.hasOwn(object, key))) {
    throw new Error("Invalid usage summary fields");
  }
  return object;
}

function count(value: unknown): number {
  if (!Number.isSafeInteger(value) || (value as number) < 0) {
    throw new Error("Invalid usage summary count");
  }
  return value as number;
}

function totalsAt(object: Record<string, unknown>): UsageTotals {
  return Object.fromEntries(totalKeys.map((key) => [key, count(object[key])])) as unknown as UsageTotals;
}

function arrayAt(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new Error("Invalid usage summary array");
  return value;
}

function groupsAt(value: unknown): UsageGroup[] {
  const seen = new Set<string | null>();
  return arrayAt(value).map((value) => {
    const object = objectAt(value, ["id", ...totalKeys]);
    const id = object.id;
    if (id !== null && (typeof id !== "string" || !id.trim() || id.length > 1024)) {
      throw new Error("Invalid usage summary group");
    }
    if (seen.has(id as string | null)) throw new Error("Duplicate usage summary group");
    seen.add(id as string | null);
    return { id: id as string | null, ...totalsAt(object) };
  });
}

export function parseUsageSummary(value: unknown, window: UsageWindow): UsageSummary {
  const object = objectAt(value, ["totals", "by_day", "by_hour", "by_service", "by_model", "scanned_records"]);
  const summary = emptyUsageSummary(window);
  summary.totals = totalsAt(objectAt(object.totals, totalKeys));
  summary.scanned_records = count(object.scanned_records);
  if (summary.scanned_records !== summary.totals.requests + summary.totals.failed_requests) {
    throw new Error("Inconsistent usage summary counts");
  }
  summary.by_service = groupsAt(object.by_service);
  summary.by_model = groupsAt(object.by_model);
  const fill = (raw: unknown, hourly: boolean) => {
    const buckets = hourly ? summary.by_hour : summary.by_day;
    const keyOf = (date: unknown, hour: unknown) => hourly ? `${date}T${hour}` : String(date);
    const byKey = new Map(buckets.map((bucket) => [
      keyOf(bucket.date, "hour" in bucket ? bucket.hour : undefined), bucket,
    ]));
    const seen = new Set<string>();
    for (const value of arrayAt(raw)) {
      const bucket = objectAt(value, ["date", ...(hourly ? ["hour"] : []), ...totalKeys]);
      if (typeof bucket.date !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(bucket.date) ||
        (hourly && (!Number.isInteger(bucket.hour) || (bucket.hour as number) < 0 || (bucket.hour as number) > 23))) {
        throw new Error("Invalid usage summary bucket");
      }
      const key = keyOf(bucket.date, bucket.hour);
      const target = byKey.get(key);
      if (!target || seen.has(key)) throw new Error("Unexpected usage summary bucket");
      seen.add(key);
      Object.assign(target, totalsAt(bucket));
    }
  };
  fill(object.by_day, false);
  fill(object.by_hour, true);
  return summary;
}
