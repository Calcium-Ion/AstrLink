import {
  CLIENT_TYPES,
  GATEWAY_REWRITES,
  type ClientType,
} from "./request-record-model";
import {
  emptyUsageStatusCounts,
  emptyUsageSummary,
  emptyUsageTotals,
  type ServicePerformance,
  type UsageGroup,
  type UsageHourModelBucket,
  type UsageRoute,
  type UsageSummary,
  type UsageTotals,
  type UsageWindow,
} from "./usage-range";

const totalKeys = Object.keys(emptyUsageTotals());
const statusKeys = Object.keys(emptyUsageStatusCounts());

function objectAt(value: unknown, keys: string[]): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid usage summary object");
  }
  const object = value as Record<string, unknown>;
  if (
    Object.keys(object).length !== keys.length ||
    keys.some((key) => !Object.hasOwn(object, key))
  ) {
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
  return Object.fromEntries(
    totalKeys.map((key) => [key, count(object[key])]),
  ) as unknown as UsageTotals;
}

function arrayAt(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new Error("Invalid usage summary array");
  return value;
}

function groupsAt(value: unknown): UsageGroup[] {
  const seen = new Set<string | null>();
  return arrayAt(value).map((value) => {
    const hasPerformance =
      value !== null &&
      typeof value === "object" &&
      Object.hasOwn(value, "performance");
    const object = objectAt(value, [
      "id",
      ...totalKeys,
      ...(hasPerformance ? ["performance"] : []),
    ]);
    const id = object.id;
    if (
      id !== null &&
      (typeof id !== "string" || !id.trim() || id.length > 1024)
    ) {
      throw new Error("Invalid usage summary group");
    }
    if (seen.has(id as string | null))
      throw new Error("Duplicate usage summary group");
    seen.add(id as string | null);
    return {
      id: id as string | null,
      ...totalsAt(object),
      ...(hasPerformance
        ? { performance: parseUsagePerformance(object.performance) }
        : {}),
    };
  });
}

/** Sparse hour-by-model buckets, each on a day of the window. */
function hourModelsAt(
  value: unknown,
  days: ReadonlySet<string>,
): UsageHourModelBucket[] {
  const seen = new Set<string>();
  return arrayAt(value).map((value) => {
    const object = objectAt(value, [
      "date",
      "hour",
      "model",
      "requests",
      "total_tokens",
    ]);
    const { date, hour, model } = object;
    if (
      typeof date !== "string" ||
      !days.has(date) ||
      !Number.isInteger(hour) ||
      (hour as number) < 0 ||
      (hour as number) > 23 ||
      (model !== null &&
        (typeof model !== "string" || !model.trim() || model.length > 1024))
    ) {
      throw new Error("Invalid usage summary hour model");
    }
    const key = `${date}T${hour}\u0000${model ?? ""}`;
    if (seen.has(key)) throw new Error("Duplicate usage summary hour model");
    seen.add(key);
    return {
      date,
      hour: hour as number,
      model: model as string | null,
      requests: count(object.requests),
      total_tokens: count(object.total_tokens),
    };
  });
}

function idAt(value: unknown): string | null {
  if (
    value !== null &&
    (typeof value !== "string" || !value.trim() || value.length > 1024)
  ) {
    throw new Error("Invalid usage summary route");
  }
  return value as string | null;
}

/**
 * Routes, each once. A client or rewrite this build does not know reads as
 * an unknown client or is left out, so a newer Core still draws.
 */
function routesAt(value: unknown): UsageRoute[] {
  const seen = new Set<string>();
  return arrayAt(value).map((value) => {
    const object = objectAt(value, [
      "client_type",
      "service_id",
      "failed_service_id",
      "failed",
      "rewrites",
      "requests",
    ]);
    const client = idAt(object.client_type);
    if (typeof object.failed !== "boolean")
      throw new Error("Invalid usage summary route");
    const named = arrayAt(object.rewrites).map((rewrite) => {
      if (typeof rewrite !== "string")
        throw new Error("Invalid usage summary route");
      return rewrite;
    });
    if (new Set(named).size !== named.length)
      throw new Error("Invalid usage summary route");
    const route: UsageRoute = {
      client_type:
        client !== null && CLIENT_TYPES.includes(client as ClientType)
          ? (client as ClientType)
          : null,
      service_id: idAt(object.service_id),
      failed_service_id: idAt(object.failed_service_id),
      failed: object.failed,
      rewrites: GATEWAY_REWRITES.filter((rewrite) => named.includes(rewrite)),
      requests: count(object.requests),
    };
    if (route.requests === 0) throw new Error("Invalid usage summary route");
    const key = JSON.stringify([
      client,
      route.service_id,
      route.failed_service_id,
      route.failed,
      named,
    ]);
    if (seen.has(key)) throw new Error("Duplicate usage summary route");
    seen.add(key);
    return route;
  });
}

export function parseUsagePerformance(value: unknown): ServicePerformance {
  const object = objectAt(value, [
    "cache_hit_rate",
    "output_tokens_per_second",
    "cache_samples",
    "speed_samples",
  ]);
  const rate = (
    value: unknown,
    samples: number,
    maximum = Infinity,
  ): number | null => {
    if (value === null && samples === 0) return null;
    if (
      typeof value !== "number" ||
      !Number.isFinite(value) ||
      value < 0 ||
      value > maximum ||
      samples === 0
    ) {
      throw new Error("Invalid service performance rate");
    }
    return value;
  };
  const cache_samples = count(object.cache_samples);
  const speed_samples = count(object.speed_samples);
  return {
    cache_samples,
    speed_samples,
    cache_hit_rate: rate(object.cache_hit_rate, cache_samples, 1),
    output_tokens_per_second: rate(
      object.output_tokens_per_second,
      speed_samples,
    ),
  };
}

export function parseUsageSummary(
  value: unknown,
  window: UsageWindow,
): UsageSummary {
  const object = objectAt(value, [
    "totals",
    "status_counts",
    "by_day",
    "by_hour",
    "by_hour_model",
    "by_service",
    "by_model",
    "by_token",
    "by_client",
    "by_route",
    "scanned_records",
  ]);
  const summary = emptyUsageSummary(window);
  summary.totals = totalsAt(objectAt(object.totals, totalKeys));
  summary.scanned_records = count(object.scanned_records);
  const statuses = objectAt(object.status_counts, statusKeys);
  for (const key of statusKeys) {
    summary.status_counts[key as keyof typeof summary.status_counts] = count(
      statuses[key],
    );
  }
  if (
    summary.scanned_records !==
      summary.totals.requests + summary.totals.failed_requests ||
    summary.status_counts.succeeded !== summary.totals.requests ||
    summary.status_counts.failed !== summary.totals.failed_requests
  ) {
    throw new Error("Inconsistent usage summary counts");
  }
  summary.by_service = groupsAt(object.by_service);
  summary.by_model = groupsAt(object.by_model);
  summary.by_token = groupsAt(object.by_token);
  summary.by_client = groupsAt(object.by_client);
  summary.by_route = routesAt(object.by_route);
  summary.by_hour_model = hourModelsAt(
    object.by_hour_model,
    new Set(summary.by_day.map((bucket) => bucket.date)),
  );
  const fill = (raw: unknown, hourly: boolean) => {
    const buckets = hourly ? summary.by_hour : summary.by_day;
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
        throw new Error("Invalid usage summary bucket");
      }
      const key = keyOf(bucket.date, bucket.hour);
      const target = byKey.get(key);
      if (!target || seen.has(key))
        throw new Error("Unexpected usage summary bucket");
      seen.add(key);
      Object.assign(target, totalsAt(bucket));
    }
  };
  fill(object.by_day, false);
  fill(object.by_hour, true);
  return summary;
}
