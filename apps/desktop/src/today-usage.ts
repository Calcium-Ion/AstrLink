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

export interface TodayUsageGroup {
  id: string | null;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface TodayUsageSummary {
  requests: number;
  failed_requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  by_service: TodayUsageGroup[];
  by_model: TodayUsageGroup[];
  capped: boolean;
}

export type TodayUsageStatus = "blocked" | "loading" | "ready" | "error";

export type TodayUsageState = {
  status: TodayUsageStatus;
  summary: TodayUsageSummary | null;
  error: string | null;
};

export interface CatalogServiceRef {
  id: string;
  name: string;
  enabled: boolean;
}

export interface MergedServiceUsage extends TodayUsageGroup {
  name: string;
  enabled: boolean | null;
  in_catalog: boolean;
}

export function emptyTodayUsageSummary(capped = false): TodayUsageSummary {
  return {
    requests: 0,
    failed_requests: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    by_service: [],
    by_model: [],
    capped,
  };
}

export function aggregateTodayUsage(
  records: RequestRecord[],
  capped: boolean,
): TodayUsageSummary {
  let requests = 0;
  let failed_requests = 0;
  let input_tokens = 0;
  let output_tokens = 0;
  let total_tokens = 0;
  let cache_read_tokens = 0;
  let cache_write_tokens = 0;
  const serviceMap = new Map<string, TodayUsageGroup>();
  const modelMap = new Map<string, TodayUsageGroup>();

  for (const record of records) {
    // List endpoints return roots only; still exclude retry children so daily
    // totals never count failed attempts that later succeeded.
    if (record.parent_request_id !== null) continue;
    const status = displayRequestStatus(record.status, record.http_status);
    if (status === "failed") {
      failed_requests += 1;
      continue;
    }
    if (status !== "succeeded") continue;
    requests += 1;
    if (record.usage) {
      input_tokens += record.usage.input_tokens;
      output_tokens += record.usage.output_tokens;
      total_tokens += record.usage.total_tokens;
      cache_read_tokens += record.usage.cache_read_tokens ?? 0;
      cache_write_tokens += record.usage.cache_write_tokens ?? 0;
    }
    addUsageGroup(serviceMap, record.service_id, record.usage);
    addUsageGroup(modelMap, record.requested_model, record.usage);
  }

  return {
    requests,
    failed_requests,
    input_tokens,
    output_tokens,
    total_tokens,
    cache_read_tokens,
    cache_write_tokens,
    by_service: sortUsageGroups([...serviceMap.values()]),
    by_model: sortUsageGroups([...modelMap.values()]),
    capped,
  };
}

/** Cache hit rate as a 0–1 fraction; null when input is zero. */
export function cacheHitRate(summary: TodayUsageSummary): number | null {
  if (summary.input_tokens <= 0) return null;
  return summary.cache_read_tokens / summary.input_tokens;
}

export function formatCacheHitPercent(summary: TodayUsageSummary): string {
  const rate = cacheHitRate(summary);
  if (rate === null) return "—";
  return `${Math.round(rate * 100)}%`;
}

export function modelUsageLabel(id: string | null): string {
  const name = id?.trim() ?? "";
  return name || unknownModelLabel();
}

export function usageBarPercent(
  value: number,
  groups: Array<Pick<TodayUsageGroup, "total_tokens">>,
): number {
  const max = groups.reduce((highest, group) => Math.max(highest, group.total_tokens), 0);
  if (max <= 0 || value <= 0) return 0;
  return Math.round((value / max) * 100);
}

export function mergeCatalogServiceUsage(
  services: CatalogServiceRef[],
  byService: TodayUsageGroup[],
): MergedServiceUsage[] {
  const usageById = new Map<string, TodayUsageGroup>();
  const unattributed: TodayUsageGroup[] = [];
  for (const group of byService) {
    if (group.id === null) {
      unattributed.push(group);
      continue;
    }
    usageById.set(group.id, group);
  }

  const catalogIds = new Set(services.map((service) => service.id));
  const rows: MergedServiceUsage[] = services.map((service) => {
    const usage = usageById.get(service.id);
    return {
      id: service.id,
      name: service.name,
      enabled: service.enabled,
      in_catalog: true,
      requests: usage?.requests ?? 0,
      input_tokens: usage?.input_tokens ?? 0,
      output_tokens: usage?.output_tokens ?? 0,
      total_tokens: usage?.total_tokens ?? 0,
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

export function startOfTodayIso(now: Date): string {
  return new Date(now.getFullYear(), now.getMonth(), now.getDate()).toISOString();
}

function normalizeGroupId(id: string | null): string | null {
  const trimmed = id?.trim() ?? "";
  return trimmed || null;
}

function addUsageGroup(
  groups: Map<string, TodayUsageGroup>,
  id: string | null,
  usage: RequestRecord["usage"],
): void {
  const normalized = normalizeGroupId(id);
  const key = normalized ?? "";
  let group = groups.get(key);
  if (!group) {
    group = {
      id: normalized,
      requests: 0,
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
    };
    groups.set(key, group);
  }
  group.requests += 1;
  if (!usage) return;
  group.input_tokens += usage.input_tokens;
  group.output_tokens += usage.output_tokens;
  group.total_tokens += usage.total_tokens;
}

function sortUsageGroups(groups: TodayUsageGroup[]): TodayUsageGroup[] {
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
