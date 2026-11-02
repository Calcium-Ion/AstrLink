import { currentLocale, i18n } from "./i18n";
import {
  displayRequestStatus,
  isModelDiscoveryProtocol,
  type ClientType,
  type GatewayRewrite,
  type RequestRecord,
  type RequestStatus,
} from "./request-record-model";

export function unattributedServiceLabel(): string {
  return i18n.t("today.unattributed");
}
export function unknownServiceLabel(): string {
  return i18n.t("today.unknownService");
}
export function unknownModelLabel(): string {
  return i18n.t("today.unknownModel");
}
export const UNATTRIBUTED_SERVICE_LABEL = unattributedServiceLabel;
export const UNKNOWN_SERVICE_LABEL = unknownServiceLabel;
export const UNKNOWN_MODEL_LABEL = unknownModelLabel;

export const USAGE_RANGE_PRESETS = ["1d", "7d", "30d", "90d", "1y"] as const;

export type UsageRangePreset = (typeof USAGE_RANGE_PRESETS)[number];

export const DEFAULT_USAGE_RANGE_PRESET: UsageRangePreset = "1d";

export function isUsageRangePreset(value: unknown): value is UsageRangePreset {
  return USAGE_RANGE_PRESETS.includes(value as UsageRangePreset);
}

/**
 * Whole local days from `from` through `to`, both inclusive, as `YYYY-MM-DD`.
 * Unlike a preset it stays put as time passes.
 */
export interface CustomUsageRange {
  from: string;
  to: string;
}

export type UsageRange = UsageRangePreset | CustomUsageRange;

/** Core sums at most 366 days; a year of local days stays under it across DST. */
export const MAX_CUSTOM_RANGE_DAYS = 365;

/** Core's limit for hour-by-model buckets: 32 days, room for a DST shift. */
export const HOUR_MODEL_MAX_SPAN_MS = 32 * 24 * 3_600_000;

export function isCustomUsageRange(
  range: UsageRange,
): range is CustomUsageRange {
  return typeof range === "object";
}

/** Equal ranges share a key, so a list or a cache can be kept per range. */
export function usageRangeKey(range: UsageRange): string {
  return isCustomUsageRange(range) ? `${range.from}~${range.to}` : range;
}

const DAY_KEY_PATTERN = /^(\d{4})-(\d{2})-(\d{2})$/;

/** Local midnight of a `YYYY-MM-DD` day; null unless it names a real day. */
export function parseDayKey(day: string): Date | null {
  const match = DAY_KEY_PATTERN.exec(day);
  if (!match) return null;
  const [year, month, date] = match.slice(1).map(Number);
  const parsed = new Date(year, month - 1, date);
  return parsed.getFullYear() === year &&
    parsed.getMonth() === month - 1 &&
    parsed.getDate() === date
    ? parsed
    : null;
}

/** Local days a custom range spans; 0 when it does not name a valid span. */
export function customRangeDays(range: CustomUsageRange): number {
  const from = parseDayKey(range.from);
  const to = parseDayKey(range.to);
  if (!from || !to || to < from) return 0;
  // Calendar arithmetic, so a DST day still counts once.
  return (
    Math.round(
      (Date.UTC(to.getFullYear(), to.getMonth(), to.getDate()) -
        Date.UTC(from.getFullYear(), from.getMonth(), from.getDate())) /
        86_400_000,
    ) + 1
  );
}

export type CustomRangeProblem = "invalid" | "order" | "future" | "too_long";

/** Why a custom range cannot be read, or null when it can. */
export function customRangeProblem(
  range: CustomUsageRange,
  now: Date,
): CustomRangeProblem | null {
  const from = parseDayKey(range.from);
  const to = parseDayKey(range.to);
  if (!from || !to) return "invalid";
  if (to < from) return "order";
  if (from > startOfLocalDay(now)) return "future";
  if (customRangeDays(range) > MAX_CUSTOM_RANGE_DAYS) return "too_long";
  return null;
}

export function usageRangeDays(preset: UsageRangePreset): number {
  switch (preset) {
    case "1d":
      return 1;
    case "7d":
      return 7;
    case "30d":
      return 30;
    case "90d":
      return 90;
    case "1y":
      return 365;
  }
}

/**
 * The calendar span a summary covers. `from` is the inclusive local midnight
 * that opens the window and `to` is the exclusive local midnight after today,
 * so the pair can be handed to the control API verbatim.
 */
export interface UsageWindow {
  preset: UsageRangePreset | "custom";
  from: string;
  to: string;
  time_zone: string;
}

export interface UsageTotals {
  requests: number;
  failed_requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

/**
 * Root model calls in the window by outcome. `succeeded` and `failed` equal
 * the totals' `requests` and `failed_requests`; the rest never count as
 * requests.
 */
export type UsageStatusCounts = Record<RequestStatus, number>;

/** The order outcomes are charted and listed in. */
export const USAGE_STATUSES = [
  "succeeded",
  "failed",
  "blocked",
  "cancelled",
  "pending",
] as const satisfies readonly RequestStatus[];

export function emptyUsageStatusCounts(): UsageStatusCounts {
  return { succeeded: 0, failed: 0, blocked: 0, cancelled: 0, pending: 0 };
}

export interface UsageGroup extends UsageTotals {
  id: string | null;
  performance?: ServicePerformance;
}

export interface ServicePerformance {
  cache_hit_rate: number | null;
  output_tokens_per_second: number | null;
  cache_samples: number;
  speed_samples: number;
}

export interface UsageDayBucket extends UsageTotals {
  /** Local calendar day as `YYYY-MM-DD`. */
  date: string;
}

export interface UsageHourBucket extends UsageTotals {
  /** Local calendar day as `YYYY-MM-DD`. */
  date: string;
  /** Local hour of day, 0–23. */
  hour: number;
}

/** One model's successful calls in one local hour. */
export interface UsageHourModelBucket {
  /** Local calendar day as `YYYY-MM-DD`. */
  date: string;
  /** Local hour of day, 0–23. */
  hour: number;
  /** Null when no model was recorded. */
  model: string | null;
  requests: number;
  total_tokens: number;
}

/** Calls that took one way through the gateway, failed ones included. */
export interface UsageRoute {
  /** Null for unknown and historical clients. */
  client_type: ClientType | null;
  /** The provider of the last attempt; null when none was reached. */
  service_id: string | null;
  /** The provider the call failed over from. */
  failed_service_id: string | null;
  failed: boolean;
  /** In the order the gateway applies them. */
  rewrites: GatewayRewrite[];
  requests: number;
}

export interface UsageSummary {
  window: UsageWindow;
  totals: UsageTotals;
  status_counts: UsageStatusCounts;
  /** One bucket per local day in the window, empty days included. */
  by_day: UsageDayBucket[];
  /**
   * Local-hour buckets for an hourly window, empty hours included.
   * Multi-day ranges leave this empty so the chart stays on `by_day`.
   */
  by_hour: UsageHourBucket[];
  /**
   * Sparse local hours with successful calls, split by model. Core fills it
   * for windows of about a month or less and leaves longer ones empty.
   */
  by_hour_model: UsageHourModelBucket[];
  by_service: UsageGroup[];
  by_model: UsageGroup[];
  by_token: UsageGroup[];
  /** By detected client; unknown and historical calls share the null id. */
  by_client: UsageGroup[];
  /**
   * Routes with their rewrites and failovers, busiest first. Core fills it
   * for windows of about a month or less and leaves longer ones empty.
   */
  by_route: UsageRoute[];
  scanned_records: number;
  capped: boolean;
}

export type UsageStatus = "blocked" | "loading" | "ready" | "error";

export type UsageState = {
  status: UsageStatus;
  summary: UsageSummary | null;
  error: string | null;
};

export interface CatalogServiceRef {
  id: string;
  name: string;
  enabled: boolean;
}

export interface MergedServiceUsage extends UsageGroup {
  name: string;
  enabled: boolean | null;
  in_catalog: boolean;
}

export function emptyUsageTotals(): UsageTotals {
  return {
    requests: 0,
    failed_requests: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
  };
}

export function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
}

export interface ZonedDateTime {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
}

/** Wall-clock parts of an instant in an IANA zone (system zone when omitted). */
export function zonedDateTime(date: Date, timeZone?: string): ZonedDateTime {
  const options: Intl.DateTimeFormatOptions = {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  };
  if (timeZone) options.timeZone = timeZone;
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat("en-US", options)
      .formatToParts(date)
      .filter((part) => part.type !== "literal")
      .map((part) => [part.type, part.value]),
  );
  return {
    year: Number(parts.year),
    month: Number(parts.month),
    day: Number(parts.day),
    hour: Number(parts.hour),
    minute: Number(parts.minute),
  };
}

export function zonedDayKey(date: Date, timeZone?: string): string {
  const zoned = zonedDateTime(date, timeZone);
  const month = `${zoned.month}`.padStart(2, "0");
  const day = `${zoned.day}`.padStart(2, "0");
  return `${zoned.year}-${month}-${day}`;
}

function zoneOffsetMs(date: Date, timeZone?: string): number {
  if (!timeZone) return -date.getTimezoneOffset() * 60_000;
  const zoned = zonedDateTime(date, timeZone);
  return (
    Date.UTC(zoned.year, zoned.month - 1, zoned.day, zoned.hour, zoned.minute) -
    date.getTime()
  );
}

/** Interpret a wall-clock time in `timeZone` as a UTC instant. */
export function zonedWallToUtc(
  parts: Omit<ZonedDateTime, "minute"> & { minute?: number },
  timeZone?: string,
): Date {
  const minute = parts.minute ?? 0;
  if (!timeZone) {
    return new Date(parts.year, parts.month - 1, parts.day, parts.hour, minute);
  }
  const asUtc = Date.UTC(
    parts.year,
    parts.month - 1,
    parts.day,
    parts.hour,
    minute,
  );
  const first = new Date(asUtc - zoneOffsetMs(new Date(asUtc), timeZone));
  return new Date(asUtc - zoneOffsetMs(first, timeZone));
}

export function startOfZonedHour(date: Date, timeZone?: string): Date {
  const zoned = zonedDateTime(date, timeZone);
  return zonedWallToUtc({ ...zoned, minute: 0 }, timeZone);
}

export function addZonedHours(
  date: Date,
  hours: number,
  timeZone?: string,
): Date {
  const zoned = zonedDateTime(date, timeZone);
  const wall = new Date(
    Date.UTC(zoned.year, zoned.month - 1, zoned.day, zoned.hour + hours),
  );
  return zonedWallToUtc(
    {
      year: wall.getUTCFullYear(),
      month: wall.getUTCMonth() + 1,
      day: wall.getUTCDate(),
      hour: wall.getUTCHours(),
      minute: 0,
    },
    timeZone,
  );
}

function hourBucketKey(date: Date, timeZone?: string): string {
  const zoned = zonedDateTime(date, timeZone);
  return `${zonedDayKey(date, timeZone)}T${zoned.hour}`;
}

/** Local calendar day of a date as `YYYY-MM-DD`. */
export function localDayKey(date: Date): string {
  return zonedDayKey(date);
}

export function startOfLocalDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate());
}

export function startOfLocalHour(date: Date): Date {
  return startOfZonedHour(date);
}

export function startOfTodayIso(now: Date): string {
  return startOfLocalDay(now).toISOString();
}

/** Today from local midnight to the next, the day the tray's figures cover. */
export function localDayWindow(now: Date): Pick<UsageWindow, "from" | "to"> {
  const from = startOfLocalDay(now);
  const to = new Date(from);
  to.setDate(to.getDate() + 1);
  return { from: from.toISOString(), to: to.toISOString() };
}

/**
 * The window for a range. `1d` is the rolling last 24 local hours (current
 * hour included). The other presets are calendar days ending tomorrow's
 * midnight; a custom range runs from its first local midnight to the one
 * after its last day.
 */
export function resolveUsageWindow(
  range: UsageRange,
  now: Date,
  timeZone: string = localTimeZone(),
): UsageWindow {
  if (isCustomUsageRange(range)) {
    const from = parseDayKey(range.from) ?? startOfLocalDay(now);
    const last = parseDayKey(range.to) ?? from;
    const to = new Date(last);
    to.setDate(to.getDate() + 1);
    return {
      preset: "custom",
      from: from.toISOString(),
      to: to.toISOString(),
      time_zone: timeZone,
    };
  }
  const preset = range;
  if (preset === "1d") {
    const zone = timeZone || undefined;
    const currentHour = startOfZonedHour(now, zone);
    return {
      preset,
      from: addZonedHours(currentHour, -23, zone).toISOString(),
      to: addZonedHours(currentHour, 1, zone).toISOString(),
      time_zone: timeZone,
    };
  }
  const days = usageRangeDays(preset);
  const today = startOfLocalDay(now);
  const from = new Date(today);
  from.setDate(from.getDate() - (days - 1));
  const to = new Date(today);
  to.setDate(to.getDate() + 1);
  return {
    preset,
    from: from.toISOString(),
    to: to.toISOString(),
    time_zone: timeZone,
  };
}

/** Every local day key overlapping the window, oldest first. */
export function usageWindowDayKeys(
  window: Pick<UsageWindow, "from" | "to">,
): string[] {
  const start = startOfLocalDay(new Date(window.from));
  const to = new Date(window.to);
  let end = startOfLocalDay(to);
  if (to.getTime() > end.getTime()) {
    end = new Date(end);
    end.setDate(end.getDate() + 1);
  }
  const keys: string[] = [];
  for (let day = new Date(start); day < end; day.setDate(day.getDate() + 1)) {
    keys.push(localDayKey(day));
  }
  return keys;
}

/**
 * Whether the window is read in local hours: the rolling day, or a custom
 * range of one day, which a single daily bar would say little about.
 */
export function hourlyUsageWindow(
  window: Pick<UsageWindow, "preset" | "from" | "to">,
): boolean {
  if (window.preset === "1d") return true;
  return window.preset === "custom" && usageWindowDayKeys(window).length === 1;
}

/** Local-hour slots for an hourly window, oldest first. */
export function usageWindowHourSlots(
  window: UsageWindow,
): Array<{ date: string; hour: number }> {
  if (!hourlyUsageWindow(window)) return [];
  const zone = window.time_zone || undefined;
  const end = new Date(window.to);
  const slots: Array<{ date: string; hour: number }> = [];
  const seen = new Set<string>();
  let at = startOfZonedHour(new Date(window.from), zone);
  // A fall-back day has 25 elapsed hours. Both occurrences of a repeated
  // local hour share one chart bucket, but the final hour must still appear.
  for (let step = 0; step < 26 && at < end; step += 1) {
    const zoned = zonedDateTime(at, zone);
    const date = zonedDayKey(at, zone);
    const key = `${date}T${zoned.hour}`;
    if (!seen.has(key)) {
      slots.push({ date, hour: zoned.hour });
      seen.add(key);
    }
    at = new Date(at.getTime() + 3_600_000);
  }
  return slots;
}

export function emptyUsageSummary(
  window: UsageWindow,
  capped = false,
): UsageSummary {
  const days = usageWindowDayKeys(window);
  return {
    window,
    totals: emptyUsageTotals(),
    status_counts: emptyUsageStatusCounts(),
    by_day: days.map((date) => ({
      date,
      ...emptyUsageTotals(),
    })),
    by_hour: emptyHourSeries(window),
    by_hour_model: [],
    by_service: [],
    by_model: [],
    by_token: [],
    by_client: [],
    by_route: [],
    scanned_records: 0,
    capped,
  };
}

function emptyHourSeries(window: UsageWindow): UsageHourBucket[] {
  return usageWindowHourSlots(window).map((slot) => ({
    ...slot,
    ...emptyUsageTotals(),
  }));
}

export interface UsageAggregate {
  totals: UsageTotals;
  status_counts: UsageStatusCounts;
  by_service: UsageGroup[];
  by_model: UsageGroup[];
  by_token: UsageGroup[];
  by_day: UsageDayBucket[];
  scanned_records: number;
}

/**
 * Fold inference root records into totals and breakdowns. Discovery and retry
 * children are skipped so a failed attempt that later succeeded is never
 * counted twice, and only successful roots contribute tokens.
 */
export function aggregateUsageRecords(
  records: RequestRecord[],
): UsageAggregate {
  const totals = emptyUsageTotals();
  const status_counts = emptyUsageStatusCounts();
  const serviceMap = new Map<string, UsageGroup>();
  const modelMap = new Map<string, UsageGroup>();
  // The client only sees records, so it cannot apply Core's current-token
  // whitelist. The display layer must intersect this list with the catalog.
  const tokenMap = new Map<string, UsageGroup>();
  const dayMap = new Map<string, UsageDayBucket>();
  let scanned_records = 0;

  for (const record of records) {
    const status = rootCallStatus(record);
    if (status == null) continue;
    status_counts[status] += 1;
    if (status !== "failed" && status !== "succeeded") continue;
    scanned_records += 1;
    const day = dayBucket(dayMap, record.started_at);

    addUsageGroup(
      serviceMap,
      record.service_id,
      record.usage,
      status === "failed",
    );
    // Match Core and billing: group by the model sent upstream.
    addUsageGroup(
      modelMap,
      record.recovery?.upstream_model ||
        record.model_redirect?.to ||
        record.requested_model,
      record.usage,
      status === "failed",
    );
    addUsageGroup(
      tokenMap,
      record.local_access_token_id,
      record.usage,
      status === "failed",
      false,
    );

    if (status === "failed") {
      totals.failed_requests += 1;
      if (day) day.failed_requests += 1;
      continue;
    }

    totals.requests += 1;
    if (day) day.requests += 1;
    if (record.usage) {
      addUsage(totals, record.usage);
      if (day) addUsage(day, record.usage);
    }
  }

  return {
    totals,
    status_counts,
    by_service: sortUsageGroups([...serviceMap.values()]),
    by_model: sortUsageGroups([...modelMap.values()]),
    by_token: sortUsageGroups([...tokenMap.values()]),
    by_day: [...dayMap.values()].sort((left, right) =>
      left.date.localeCompare(right.date),
    ),
    scanned_records,
  };
}

/** Aggregate records into a window-scoped summary with a padded day series. */
export function aggregateUsage(
  records: RequestRecord[],
  window: UsageWindow,
  capped: boolean,
): UsageSummary {
  const aggregate = aggregateUsageRecords(records);
  const observed = new Map(
    aggregate.by_day.map((bucket) => [bucket.date, bucket]),
  );
  const days = usageWindowDayKeys(window);
  return {
    window,
    totals: aggregate.totals,
    status_counts: aggregate.status_counts,
    by_day: days.map(
      (date) => observed.get(date) ?? { date, ...emptyUsageTotals() },
    ),
    by_hour: padHourBuckets(records, window),
    by_hour_model: hourModelBuckets(records, window),
    by_service: aggregate.by_service,
    by_model: aggregate.by_model,
    by_token: aggregate.by_token,
    by_client: clientGroups(records),
    // Only Core tells which rewrites a call got.
    by_route: [],
    scanned_records: aggregate.scanned_records,
    capped,
  };
}

/** The outcome of a model call; discovery and retry attempts are not calls. */
function rootCallStatus(record: RequestRecord): RequestStatus | null {
  if (record.parent_request_id !== null) return null;
  if (isModelDiscoveryProtocol(record.input_protocol)) return null;
  return displayRequestStatus(record.status, record.http_status);
}

/** Failed and succeeded inference roots only; discovery and retries never count. */
function usageRecordStatus(
  record: RequestRecord,
): "failed" | "succeeded" | null {
  const status = rootCallStatus(record);
  if (status !== "failed" && status !== "succeeded") return null;
  return status;
}

/** Core's hour-by-model buckets, from records, for windows Core fills them. */
function hourModelBuckets(
  records: RequestRecord[],
  window: UsageWindow,
): UsageHourModelBucket[] {
  const from = new Date(window.from).getTime();
  const to = new Date(window.to).getTime();
  if (to - from > HOUR_MODEL_MAX_SPAN_MS) return [];
  const zone = window.time_zone || undefined;
  const buckets = new Map<string, UsageHourModelBucket>();
  for (const record of records) {
    if (usageRecordStatus(record) !== "succeeded") continue;
    const started = new Date(record.started_at);
    const at = started.getTime();
    if (Number.isNaN(at) || at < from || at >= to) continue;
    const model = normalizeGroupId(
      record.recovery?.upstream_model ||
        record.model_redirect?.to ||
        record.requested_model,
    );
    const date = zonedDayKey(started, zone);
    const hour = zonedDateTime(started, zone).hour;
    const key = `${date}T${hour}\u0000${model ?? ""}`;
    let bucket = buckets.get(key);
    if (!bucket) {
      bucket = { date, hour, model, requests: 0, total_tokens: 0 };
      buckets.set(key, bucket);
    }
    bucket.requests += 1;
    bucket.total_tokens += record.usage?.total_tokens ?? 0;
  }
  return [...buckets.values()].sort(
    (left, right) =>
      left.date.localeCompare(right.date) ||
      left.hour - right.hour ||
      right.total_tokens - left.total_tokens ||
      right.requests - left.requests ||
      (left.model ?? "").localeCompare(right.model ?? ""),
  );
}

function clientGroups(records: RequestRecord[]): UsageGroup[] {
  const groups = new Map<string, UsageGroup>();
  for (const record of records) {
    const status = usageRecordStatus(record);
    if (status == null) continue;
    const client =
      record.client_type && record.client_type !== "unknown"
        ? record.client_type
        : null;
    addUsageGroup(groups, client, record.usage, status === "failed");
  }
  return sortUsageGroups([...groups.values()]);
}

function padHourBuckets(
  records: RequestRecord[],
  window: UsageWindow,
): UsageHourBucket[] {
  const hours = emptyHourSeries(window);
  if (hours.length === 0) return hours;
  const from = new Date(window.from).getTime();
  const to = new Date(window.to).getTime();
  const index = new Map(
    hours.map((bucket, at) => [`${bucket.date}T${bucket.hour}`, at]),
  );
  for (const record of records) {
    const status = usageRecordStatus(record);
    if (status == null) continue;
    const started = new Date(record.started_at);
    if (Number.isNaN(started.getTime())) continue;
    const at = started.getTime();
    if (at < from || at >= to) continue;
    const zone = window.time_zone || undefined;
    const key = hourBucketKey(started, zone);
    const bucket = hours[index.get(key) ?? -1];
    if (!bucket) continue;
    if (status === "failed") {
      bucket.failed_requests += 1;
      continue;
    }
    bucket.requests += 1;
    if (record.usage) addUsage(bucket, record.usage);
  }
  return hours;
}

/** Cache hit rate as a 0–1 fraction; null when input is zero. */
export function cacheHitRate(totals: UsageTotals): number | null {
  if (totals.input_tokens <= 0) return null;
  return totals.cache_read_tokens / totals.input_tokens;
}

export function formatCacheHitPercent(totals: UsageTotals): string {
  const rate = cacheHitRate(totals);
  if (rate === null) return "—";
  return `${Math.round(rate * 100)}%`;
}

export type DayTokenStackSeries =
  | "input"
  | "output"
  | "cache_write"
  | "cache_read";

export interface DayTokenStack {
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
}

/**
 * Split one day's total into stacked segments. Cache read/write are subsets
 * of input, so they come out of `total_tokens` first; the leftover is then
 * split into output and uncached input. Dirty totals allocate read, then
 * write, then output.
 */
export function dayTokenStack(
  totals: Pick<
    UsageTotals,
    | "total_tokens"
    | "output_tokens"
    | "cache_read_tokens"
    | "cache_write_tokens"
  >,
): DayTokenStack {
  const total = Math.max(0, totals.total_tokens);
  const read = Math.min(Math.max(0, totals.cache_read_tokens), total);
  const write = Math.min(
    Math.max(0, totals.cache_write_tokens),
    Math.max(0, total - read),
  );
  const leftover = Math.max(0, total - read - write);
  const output = Math.min(Math.max(0, totals.output_tokens), leftover);
  return {
    input: leftover - output,
    output,
    cache_read: read,
    cache_write: write,
  };
}

export function modelUsageLabel(id: string | null): string {
  const name = id?.trim() ?? "";
  return name || unknownModelLabel();
}

export function mergeCatalogServiceUsage(
  services: CatalogServiceRef[],
  byService: UsageGroup[],
): MergedServiceUsage[] {
  const usageById = new Map<string, UsageGroup>();
  const unattributed: UsageGroup[] = [];
  for (const group of byService) {
    if (group.id === null) {
      unattributed.push(group);
      continue;
    }
    usageById.set(group.id, group);
  }

  const catalogIds = new Set(services.map((service) => service.id));
  const rows: MergedServiceUsage[] = services.map((service) => {
    const usage = usageById.get(service.id) ?? {
      id: service.id,
      ...emptyUsageTotals(),
    };
    return {
      ...usage,
      id: service.id,
      name: service.name,
      enabled: service.enabled,
      in_catalog: true,
    };
  });

  for (const [id, usage] of usageById) {
    if (catalogIds.has(id)) continue;
    rows.push({
      ...usage,
      name: unknownServiceLabel(),
      enabled: null,
      in_catalog: false,
    });
  }

  for (const usage of unattributed) {
    rows.push({
      ...usage,
      name: unattributedServiceLabel(),
      enabled: null,
      in_catalog: false,
    });
  }

  return rows.sort(compareUsageThenName);
}

function normalizeGroupId(id: string | null): string | null {
  const trimmed = id?.trim() ?? "";
  return trimmed || null;
}

function addUsage(
  totals: UsageTotals,
  usage: NonNullable<RequestRecord["usage"]>,
): void {
  totals.input_tokens += usage.input_tokens;
  totals.output_tokens += usage.output_tokens;
  totals.total_tokens += usage.total_tokens;
  totals.cache_read_tokens += usage.cache_read_tokens ?? 0;
  totals.cache_write_tokens += usage.cache_write_tokens ?? 0;
}

/** The day bucket a record belongs to; null when `started_at` is unusable. */
function dayBucket(
  buckets: Map<string, UsageDayBucket>,
  startedAt: string,
): UsageDayBucket | null {
  const started = new Date(startedAt);
  if (Number.isNaN(started.getTime())) return null;
  const date = localDayKey(started);
  let bucket = buckets.get(date);
  if (!bucket) {
    bucket = { date, ...emptyUsageTotals() };
    buckets.set(date, bucket);
  }
  return bucket;
}

function addUsageGroup(
  groups: Map<string, UsageGroup>,
  id: string | null,
  usage: RequestRecord["usage"],
  failed: boolean,
  allowNull = true,
): void {
  const normalized = normalizeGroupId(id);
  if (!allowNull && normalized === null) return;
  const key = normalized ?? "";
  let group = groups.get(key);
  if (!group) {
    group = { id: normalized, ...emptyUsageTotals() };
    groups.set(key, group);
  }
  if (failed) {
    group.failed_requests += 1;
    return;
  }
  group.requests += 1;
  if (!usage) return;
  addUsage(group, usage);
}

function sortUsageGroups(groups: UsageGroup[]): UsageGroup[] {
  return groups.sort((left, right) => {
    if (right.total_tokens !== left.total_tokens) {
      return right.total_tokens - left.total_tokens;
    }
    if (right.requests !== left.requests) return right.requests - left.requests;
    return (left.id ?? "").localeCompare(right.id ?? "");
  });
}

function compareUsageThenName(
  left: Pick<MergedServiceUsage, "name" | "requests" | "total_tokens">,
  right: Pick<MergedServiceUsage, "name" | "requests" | "total_tokens">,
): number {
  if (right.total_tokens !== left.total_tokens) {
    return right.total_tokens - left.total_tokens;
  }
  if (right.requests !== left.requests) return right.requests - left.requests;
  return left.name.localeCompare(right.name, currentLocale());
}
