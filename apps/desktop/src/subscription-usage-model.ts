import { i18n } from "./i18n";
import type { SubscriptionProvider } from "./subscription-model";

const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const rfc3339Pattern =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;
const usdAmountPattern = /^[0-9]{1,15}(?:\.[0-9]{1,6})?$/;
const credentialLeakPattern =
  /(?:Bearer\s+[A-Za-z0-9._~+/=-]{12,}|(?:access_token|refresh_token|id_token|device_auth_id|code_verifier|authorization_code)["']?\s*[:=]\s*["']?[A-Za-z0-9._~+/=-]{8,}|eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,})/i;

type JsonObject = Record<string, unknown>;

export interface RateLimitWindow {
  used_percent: number;
  limit_window_seconds?: number;
  reset_at?: string;
  reset_after_seconds?: number;
}

export interface AdditionalRateLimit {
  limit_name: string;
  metered_feature?: string;
  primary?: RateLimitWindow;
  secondary?: RateLimitWindow;
}

export interface UsageCredits {
  has_credits: boolean;
  unlimited: boolean;
  balance?: string;
}

/** Prepaid USD allowance of an API key (New API); unlimited keys only report spend. */
export interface UsageQuota {
  unlimited: boolean;
  used_usd: string;
  remaining_usd?: string;
  total_usd?: string;
  expires_at?: string;
}

export interface RateLimitResetCredits {
  available_count: number;
}

export interface SubscriptionUsage {
  service_id: string;
  fetched_at: string;
  plan_type?: string;
  allowed?: boolean;
  limit_reached?: boolean;
  primary?: RateLimitWindow;
  secondary?: RateLimitWindow;
  additional_rate_limits?: AdditionalRateLimit[];
  credits?: UsageCredits;
  quota?: UsageQuota;
  rate_limit_reset_credits?: RateLimitResetCredits;
}

export type UsageResetOutcome =
  "reset" | "nothing_to_reset" | "no_credit" | "already_redeemed";

export interface SubscriptionUsageReset {
  service_id: string;
  outcome: UsageResetOutcome;
  windows_reset?: number;
}

function invalid(path: string, message: string): never {
  throw new Error(`Invalid subscription usage at ${path}: ${message}`);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as JsonObject;
}

function keysAt(
  object: JsonObject,
  required: readonly string[],
  optional: readonly string[],
  path: string,
): void {
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(object)) {
    if (!allowed.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of required) {
    if (!Object.hasOwn(object, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function stringAt(
  value: unknown,
  path: string,
  min: number,
  max: number,
): string {
  if (typeof value !== "string") {
    return invalid(path, `expected ${min} to ${max} characters`);
  }
  const length = [...value].length;
  if (length < min || length > max) {
    return invalid(path, `expected ${min} to ${max} characters`);
  }
  if (credentialLeakPattern.test(value) || value.includes("@")) {
    invalid(path, "must not contain credential material");
  }
  return value;
}

function timestampAt(value: unknown, path: string): string {
  const timestamp = stringAt(value, path, 20, 64);
  if (!rfc3339Pattern.test(timestamp) || Number.isNaN(Date.parse(timestamp))) {
    invalid(path, "expected an RFC 3339 timestamp");
  }
  return timestamp;
}

function numberAt(
  value: unknown,
  path: string,
  min: number,
  max: number,
): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return invalid(path, `expected a number from ${min} to ${max}`);
  }
  if (value < min || value > max) {
    return invalid(path, `expected a number from ${min} to ${max}`);
  }
  return value;
}

function intAt(value: unknown, path: string, min: number, max: number): number {
  const number = numberAt(value, path, min, max);
  if (!Number.isInteger(number)) {
    return invalid(path, `expected an integer from ${min} to ${max}`);
  }
  return number;
}

function usdAt(value: unknown, path: string): string {
  if (typeof value !== "string" || !usdAmountPattern.test(value)) {
    return invalid(path, "expected a USD decimal");
  }
  return value;
}

function parseQuota(value: unknown, path: string): UsageQuota {
  const quota = objectAt(value, path);
  keysAt(
    quota,
    ["unlimited", "used_usd"],
    ["remaining_usd", "total_usd", "expires_at"],
    path,
  );
  if (typeof quota.unlimited !== "boolean") {
    invalid(`${path}.unlimited`, "expected a boolean");
  }
  const parsed: UsageQuota = {
    unlimited: quota.unlimited,
    used_usd: usdAt(quota.used_usd, `${path}.used_usd`),
  };
  if (Object.hasOwn(quota, "remaining_usd")) {
    parsed.remaining_usd = usdAt(quota.remaining_usd, `${path}.remaining_usd`);
  }
  if (Object.hasOwn(quota, "total_usd")) {
    parsed.total_usd = usdAt(quota.total_usd, `${path}.total_usd`);
  }
  if (
    !parsed.unlimited &&
    (parsed.remaining_usd == null || parsed.total_usd == null)
  ) {
    invalid(path, "a limited quota needs remaining_usd and total_usd");
  }
  if (Object.hasOwn(quota, "expires_at")) {
    parsed.expires_at = timestampAt(quota.expires_at, `${path}.expires_at`);
  }
  return parsed;
}

function parseWindow(value: unknown, path: string): RateLimitWindow {
  const window = objectAt(value, path);
  keysAt(
    window,
    ["used_percent"],
    ["limit_window_seconds", "reset_at", "reset_after_seconds"],
    path,
  );
  const parsed: RateLimitWindow = {
    used_percent: numberAt(
      window.used_percent,
      `${path}.used_percent`,
      0,
      1000,
    ),
  };
  if (Object.hasOwn(window, "limit_window_seconds")) {
    parsed.limit_window_seconds = intAt(
      window.limit_window_seconds,
      `${path}.limit_window_seconds`,
      1,
      31_622_400,
    );
  }
  if (Object.hasOwn(window, "reset_at")) {
    parsed.reset_at = timestampAt(window.reset_at, `${path}.reset_at`);
  }
  if (Object.hasOwn(window, "reset_after_seconds")) {
    parsed.reset_after_seconds = intAt(
      window.reset_after_seconds,
      `${path}.reset_after_seconds`,
      0,
      31_622_400,
    );
  }
  return parsed;
}

function parseAdditional(value: unknown, path: string): AdditionalRateLimit {
  const extra = objectAt(value, path);
  keysAt(
    extra,
    ["limit_name"],
    ["metered_feature", "primary", "secondary"],
    path,
  );
  const parsed: AdditionalRateLimit = {
    limit_name: stringAt(extra.limit_name, `${path}.limit_name`, 1, 128),
  };
  if (Object.hasOwn(extra, "metered_feature")) {
    parsed.metered_feature = stringAt(
      extra.metered_feature,
      `${path}.metered_feature`,
      1,
      128,
    );
  }
  if (Object.hasOwn(extra, "primary")) {
    parsed.primary = parseWindow(extra.primary, `${path}.primary`);
  }
  if (Object.hasOwn(extra, "secondary")) {
    parsed.secondary = parseWindow(extra.secondary, `${path}.secondary`);
  }
  return parsed;
}

export function parseSubscriptionUsage(value: unknown): SubscriptionUsage {
  const usage = objectAt(value, "$");
  keysAt(
    usage,
    ["service_id", "fetched_at"],
    [
      "plan_type",
      "allowed",
      "limit_reached",
      "primary",
      "secondary",
      "additional_rate_limits",
      "credits",
      "quota",
      "rate_limit_reset_credits",
    ],
    "$",
  );
  const serviceID = stringAt(usage.service_id, "$.service_id", 3, 96);
  if (!resourceIDPattern.test(serviceID)) {
    invalid("$.service_id", "invalid service ID");
  }
  const parsed: SubscriptionUsage = {
    service_id: serviceID,
    fetched_at: timestampAt(usage.fetched_at, "$.fetched_at"),
  };
  if (Object.hasOwn(usage, "plan_type")) {
    parsed.plan_type = stringAt(usage.plan_type, "$.plan_type", 1, 64);
  }
  if (Object.hasOwn(usage, "allowed")) {
    if (typeof usage.allowed !== "boolean")
      invalid("$.allowed", "expected a boolean");
    parsed.allowed = usage.allowed;
  }
  if (Object.hasOwn(usage, "limit_reached")) {
    if (typeof usage.limit_reached !== "boolean") {
      invalid("$.limit_reached", "expected a boolean");
    }
    parsed.limit_reached = usage.limit_reached;
  }
  if (Object.hasOwn(usage, "primary")) {
    parsed.primary = parseWindow(usage.primary, "$.primary");
  }
  if (Object.hasOwn(usage, "secondary")) {
    parsed.secondary = parseWindow(usage.secondary, "$.secondary");
  }
  if (Object.hasOwn(usage, "additional_rate_limits")) {
    if (
      !Array.isArray(usage.additional_rate_limits) ||
      usage.additional_rate_limits.length > 16
    ) {
      invalid(
        "$.additional_rate_limits",
        "expected an array with at most 16 items",
      );
    }
    parsed.additional_rate_limits = usage.additional_rate_limits.map(
      (item, index) =>
        parseAdditional(item, `$.additional_rate_limits[${index}]`),
    );
  }
  if (Object.hasOwn(usage, "credits")) {
    const credits = objectAt(usage.credits, "$.credits");
    keysAt(credits, ["has_credits", "unlimited"], ["balance"], "$.credits");
    if (typeof credits.has_credits !== "boolean") {
      invalid("$.credits.has_credits", "expected a boolean");
    }
    if (typeof credits.unlimited !== "boolean") {
      invalid("$.credits.unlimited", "expected a boolean");
    }
    parsed.credits = {
      has_credits: credits.has_credits,
      unlimited: credits.unlimited,
    };
    if (Object.hasOwn(credits, "balance")) {
      parsed.credits.balance = stringAt(
        credits.balance,
        "$.credits.balance",
        1,
        32,
      );
    }
  }
  if (Object.hasOwn(usage, "quota")) {
    parsed.quota = parseQuota(usage.quota, "$.quota");
  }
  if (Object.hasOwn(usage, "rate_limit_reset_credits")) {
    const resets = objectAt(
      usage.rate_limit_reset_credits,
      "$.rate_limit_reset_credits",
    );
    keysAt(resets, ["available_count"], [], "$.rate_limit_reset_credits");
    parsed.rate_limit_reset_credits = {
      available_count: intAt(
        resets.available_count,
        "$.rate_limit_reset_credits.available_count",
        0,
        1000,
      ),
    };
  }
  return parsed;
}

const resetOutcomes = new Set<UsageResetOutcome>([
  "reset",
  "nothing_to_reset",
  "no_credit",
  "already_redeemed",
]);

export function parseSubscriptionUsageReset(
  value: unknown,
): SubscriptionUsageReset {
  const result = objectAt(value, "$");
  keysAt(result, ["service_id", "outcome"], ["windows_reset"], "$");
  const serviceID = stringAt(result.service_id, "$.service_id", 3, 96);
  if (!resourceIDPattern.test(serviceID)) {
    invalid("$.service_id", "invalid service ID");
  }
  if (
    typeof result.outcome !== "string" ||
    !resetOutcomes.has(result.outcome as UsageResetOutcome)
  ) {
    invalid("$.outcome", "expected an official consume outcome");
  }
  const parsed: SubscriptionUsageReset = {
    service_id: serviceID,
    outcome: result.outcome as UsageResetOutcome,
  };
  if (Object.hasOwn(result, "windows_reset")) {
    parsed.windows_reset = intAt(
      result.windows_reset,
      "$.windows_reset",
      0,
      1000,
    );
  }
  return parsed;
}

const planTypeLabels: Record<SubscriptionProvider, Record<string, string>> = {
  openai_codex: {
    plus: "Plus",
    pro: "Pro 20x",
    prolite: "Pro 5x",
    go: "Go",
    free: "Free",
    team: "Team",
    business: "Business",
    enterprise: "Enterprise",
    edu: "Edu",
    education: "Edu",
  },
  claude_code: {
    pro: "Pro",
    max: "Max",
    max_5x: "Max 5×",
    max_20x: "Max 20×",
    free: "Free",
    team: "Team",
    enterprise: "Enterprise",
  },
  xai_grok: {
    free: "Free",
    supergrok: "SuperGrok",
    supergrok_pro: "SuperGrok Pro",
    supergrok_heavy: "SuperGrok Heavy",
  },
};

export function planTypeLabel(
  planType: string | undefined,
  provider?: SubscriptionProvider,
): string | null {
  if (!planType) return null;
  const labels = provider && planTypeLabels[provider];
  const key = planType.toLowerCase();
  return labels && Object.hasOwn(labels, key) ? labels[key] : planType;
}

export function windowLabel(
  seconds: number | undefined,
  isSecondary: boolean,
): string {
  if (seconds === 18_000) return i18n.t("usage.hours5");
  if (seconds === 604_800) return i18n.t("usage.days7");
  if (seconds === 86_400) return i18n.t("usage.daily");
  if (seconds === 3_600) return i18n.t("usage.hourly");
  if (seconds && seconds > 0) {
    if (seconds % 86_400 === 0)
      return i18n.t("usage.days", { count: seconds / 86_400 });
    if (seconds % 3_600 === 0)
      return i18n.t("usage.hours", { count: seconds / 3_600 });
    if (seconds % 60 === 0)
      return i18n.t("usage.minutes", { count: seconds / 60 });
  }
  return isSecondary
    ? i18n.t("usage.periodLimit")
    : i18n.t("usage.rollingLimit");
}

export function formatResetCountdown(
  window: RateLimitWindow,
  now: Date,
): string | null {
  let target: Date | null = null;
  if (window.reset_at) {
    const parsed = new Date(window.reset_at);
    if (!Number.isNaN(parsed.getTime())) target = parsed;
  } else if (window.reset_after_seconds != null) {
    target = new Date(now.getTime() + window.reset_after_seconds * 1000);
  }
  if (!target) return null;
  const deltaMs = Math.max(0, target.getTime() - now.getTime());
  const minutes = Math.floor(deltaMs / 60_000);
  if (minutes < 1) return i18n.t("usage.resetSoon");
  if (minutes < 60) return i18n.t("usage.resetInMinutes", { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return i18n.t("usage.resetInHours", { count: hours });
  const days = Math.round(hours / 24);
  return i18n.t("usage.resetInDays", { count: days });
}

export function formatQuotaExpiry(quota: UsageQuota, now: Date): string | null {
  if (!quota.expires_at) return null;
  const deltaMs = Date.parse(quota.expires_at) - now.getTime();
  if (Number.isNaN(deltaMs)) return null;
  if (deltaMs <= 0) return i18n.t("usage.quotaExpired");
  const hours = Math.floor(deltaMs / 3_600_000);
  if (hours < 1) return i18n.t("usage.expiresSoon");
  if (hours < 24) return i18n.t("usage.expiresInHours", { count: hours });
  return i18n.t("usage.expiresInDays", { count: Math.floor(hours / 24) });
}

/** Share of a limited key quota already spent; an empty grant reads as used up. */
export function quotaUsedPercent(quota: UsageQuota): number {
  const total = Number(quota.total_usd);
  if (!(total > 0)) return 100;
  return (Number(quota.used_usd) / total) * 100;
}

export function usageBarPercent(usedPercent: number): number {
  if (!Number.isFinite(usedPercent) || usedPercent <= 0) return 0;
  return Math.min(100, usedPercent);
}

export type UsageWindowTone = "ok" | "warning" | "critical";

export function usageWindowTone(
  usedPercent: number,
  limitReached?: boolean,
): UsageWindowTone {
  if (limitReached || usedPercent >= 100) return "critical";
  if (usedPercent >= 80) return "warning";
  return "ok";
}

export function usageBarFillClass(tone: UsageWindowTone): string {
  if (tone === "critical") return "bg-destructive";
  if (tone === "warning") return "bg-warning";
  return "bg-success";
}

export function usageBarTrackClass(tone: UsageWindowTone): string {
  if (tone === "critical") return "bg-danger-wash";
  if (tone === "warning") return "bg-warning-wash";
  return "bg-success-wash";
}

export function usagePercentClass(tone: UsageWindowTone): string {
  if (tone === "critical") return "text-destructive";
  if (tone === "warning") return "text-warning-foreground";
  return "text-success-foreground";
}

function rawErrorText(error: unknown): string {
  if (error instanceof Error && error.message.trim())
    return error.message.trim();
  if (typeof error === "string" && error.trim()) return error.trim();
  if (typeof error === "object" && error !== null && "message" in error) {
    const message = (error as { message: unknown }).message;
    if (typeof message === "string" && message.trim()) return message.trim();
  }
  return "";
}

export function resetOutcomeMessage(outcome: UsageResetOutcome): string {
  switch (outcome) {
    case "reset":
    case "already_redeemed":
      return i18n.t("usage.resetOk");
    case "nothing_to_reset":
      return i18n.t("usage.resetNone");
    case "no_credit":
      return i18n.t("usage.resetNoAttempts");
  }
}

export function formatSubscriptionUsageError(error: unknown): string {
  const raw = rawErrorText(error);
  if (!raw) return i18n.t("usage.readFailed");
  const jsonStart = raw.indexOf("{");
  if (jsonStart >= 0) {
    try {
      const parsed = JSON.parse(raw.slice(jsonStart)) as {
        error?: { code?: unknown; message?: unknown };
      };
      const message =
        typeof parsed.error?.message === "string"
          ? parsed.error.message.trim()
          : "";
      const code =
        typeof parsed.error?.code === "string" ? parsed.error.code.trim() : "";
      if (message && code) return `${code}: ${message}`;
      if (message) return message;
    } catch {
      // Keep the raw sidecar text when the control envelope is not JSON.
    }
  }
  return raw;
}
