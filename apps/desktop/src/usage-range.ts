import { i18n } from "./i18n";
import {
  displayRequestStatus,
  type RequestRecord,
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

export const USAGE_RANGE_PRESETS = ["1d", "7d", "30d"] as const;

export type UsageRangePreset = (typeof USAGE_RANGE_PRESETS)[number];

export const DEFAULT_USAGE_RANGE_PRESET: UsageRangePreset = "7d";

export function isUsageRangePreset(value: unknown): value is UsageRangePreset {
  return USAGE_RANGE_PRESETS.includes(value as UsageRangePreset);
}

export function usageRangeDays(preset: UsageRangePreset): number {
  switch (preset) {
    case "1d":
      return 1;
    case "7d":
      return 7;
    case "30d":
      return 30;
  }
}

/**
 * The calendar span a summary covers. `from` is the inclusive local midnight
 * that opens the window and `to` is the exclusive local midnight after today,
 * so the pair can be handed to the control API verbatim.
 */
export interface UsageWindow {
  preset: UsageRangePreset;
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

export interface UsageGroup extends UsageTotals {
  id: string | null;
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

export interface UsageSummary {
  window: UsageWindow;
  totals: UsageTotals;
  /** One bucket per local day in the window, empty days included. */
  by_day: UsageDayBucket[];
  /**
   * 24 local-hour buckets for a `1d` window, empty hours included.
   * Multi-day ranges leave this empty so the chart stays on `by_day`.
   */
  by_hour: UsageHourBucket[];
  by_service: UsageGroup[];
  by_model: UsageGroup[];
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

/**
 * The window for a preset ending now. `1d` is the rolling last 24 local
 * hours (current hour included). `7d` / `30d` are calendar days ending
 * tomorrow's midnight.
 */
export function resolveUsageWindow(
  preset: UsageRangePreset,
  now: Date,
  timeZone: string = localTimeZone(),
): UsageWindow {
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
export function usageWindowDayKeys(window: UsageWindow): string[] {
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

/** 24 local-hour slots for a `1d` window, oldest first. */
export function usageWindowHourSlots(
  window: UsageWindow,
): Array<{ date: string; hour: number }> {
  if (window.preset !== "1d") return [];
  const zone = window.time_zone || undefined;
  const end = new Date(window.to);
  const slots: Array<{ date: string; hour: number }> = [];
  let at = startOfZonedHour(new Date(window.from), zone);
  for (let step = 0; step < 24 && at < end; step += 1) {
    const zoned = zonedDateTime(at, zone);
    slots.push({ date: zonedDayKey(at, zone), hour: zoned.hour });
    at = addZonedHours(at, 1, zone);
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
    by_day: days.map((date) => ({
      date,
      ...emptyUsageTotals(),
    })),
    by_hour: emptyHourSeries(window),
    by_service: [],
    by_model: [],
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
  by_service: UsageGroup[];
  by_model: UsageGroup[];
  by_day: UsageDayBucket[];
  scanned_records: number;
}

/**
 * Fold root records into totals and breakdowns. Retry children are skipped so a
 * failed attempt that later succeeded is never counted twice, and only
 * successful roots contribute tokens.
 */
export function aggregateUsageRecords(records: RequestRecord[]): UsageAggregate {
  const totals = emptyUsageTotals();
  const serviceMap = new Map<string, UsageGroup>();
  const modelMap = new Map<string, UsageGroup>();
  const dayMap = new Map<string, UsageDayBucket>();
  let scanned_records = 0;

  for (const record of records) {
    const status = usageRecordStatus(record);
    if (status == null) continue;
    scanned_records += 1;
    const day = dayBucket(dayMap, record.started_at);

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
    addUsageGroup(serviceMap, record.service_id, record.usage);
    addUsageGroup(modelMap, record.requested_model, record.usage);
  }

  return {
    totals,
    by_service: sortUsageGroups([...serviceMap.values()]),
    by_model: sortUsageGroups([...modelMap.values()]),
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
    by_day: days.map(
      (date) => observed.get(date) ?? { date, ...emptyUsageTotals() },
    ),
    by_hour: padHourBuckets(records, window),
    by_service: aggregate.by_service,
    by_model: aggregate.by_model,
    scanned_records: aggregate.scanned_records,
    capped,
  };
}

/** Failed and succeeded roots only; retry children never count. */
function usageRecordStatus(
  record: RequestRecord,
): "failed" | "succeeded" | null {
  if (record.parent_request_id !== null) return null;
  const status = displayRequestStatus(record.status, record.http_status);
  if (status !== "failed" && status !== "succeeded") return null;
  return status;
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

export function usageBarPercent(
  value: number,
  groups: Array<Pick<UsageGroup, "total_tokens">>,
): number {
  const max = groups.reduce((highest, group) => Math.max(highest, group.total_tokens), 0);
  if (max <= 0 || value <= 0) return 0;
  return Math.round((value / max) * 100);
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
): void {
  const normalized = normalizeGroupId(id);
  const key = normalized ?? "";
  let group = groups.get(key);
  if (!group) {
    group = { id: normalized, ...emptyUsageTotals() };
    groups.set(key, group);
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
  return left.name.localeCompare(right.name, i18n.language === "zh-CN" ? "zh" : "en");
}
