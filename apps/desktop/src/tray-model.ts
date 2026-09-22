import type { CorePhase, InferencePortFallback } from "./core-model";
import { parseTrayPreferences, type TrayPreferences } from "./preferences-model";

/**
 * What the tray popover renders. Mirrors `TrayStateSnapshot` in
 * `src-tauri/src/tray.rs`; the host is the single source of truth for both
 * the popover and the settings preview.
 */
export interface TrayCoreView {
  phase: CorePhase;
  inference_url: string | null;
  core_version: string | null;
  inference_port_fallback: InferencePortFallback | null;
  last_error: string | null;
  recovery_attempt: number;
  recovery_scheduled: boolean;
  /** An agent is reading records through the MCP bridge right now. */
  observer_active: boolean;
}

export interface TrayUsageTotals {
  requests: number;
  failed: number;
  total_tokens: number;
  input_tokens: number;
  cache_read_tokens: number;
}

export interface TrayShare {
  name: string;
  percent: number;
}

export interface TrayCost {
  amount_usd: number;
  unpriced: number;
}

export interface TrayLastRequest {
  started_at: string;
  model: string | null;
  latency_ms: number | null;
  failed: boolean;
}

export interface TrayWindow {
  /** Provider-named limit (Kimi "Monthly", Claude "Opus"); null for the primary pair. */
  label: string | null;
  limit_window_seconds: number | null;
  secondary: boolean;
  used_percent: number;
  reset_at: string | null;
}

export interface TraySubscription {
  name: string;
  windows: TrayWindow[];
}

export interface TrayUsageDigest {
  today: TrayUsageTotals | null;
  /** Tokens per local hour of today; 24 slots or empty when unknown. */
  hourly_tokens: number[];
  yesterday_tokens: number | null;
  top_model: TrayShare | null;
  cost_today: TrayCost | null;
  top_client: TrayShare | null;
  last_request: TrayLastRequest | null;
  month_tokens: number | null;
  subscriptions: TraySubscription[];
}

export interface TrayState {
  app_version: string;
  platform: string;
  view: TrayCoreView;
  digest: TrayUsageDigest | null;
  digest_age_ms: number | null;
  tray: TrayPreferences;
  /** The popover hangs below its anchor (menu bar) or rises above it (taskbar). */
  popover_below: boolean;
}

export type TrayAction =
  | { kind: "open" }
  | { kind: "navigate"; page: string }
  | { kind: "copy_address" }
  | { kind: "core"; op: "start" | "stop" | "restart" }
  | { kind: "refresh" }
  | { kind: "quit" };

const phases = new Set<CorePhase>([
  "stopped",
  "spawning",
  "waiting_for_ready",
  "handshaking",
  "ready",
  "stopping",
  "exited",
  "error",
]);

function invalid(path: string, detail: string): never {
  throw new Error(`Invalid AstrLink tray state at ${path}: ${detail}`);
}

function objectAt(value: unknown, path: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, expected: readonly string[], path: string): void {
  const keys = new Set(expected);
  for (const key of Object.keys(value)) {
    if (!keys.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of expected) {
    if (!Object.hasOwn(value, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function stringAt(value: unknown, path: string, max = 1024): string {
  if (typeof value !== "string" || value.length > max) {
    return invalid(path, `expected a string of at most ${max} characters`);
  }
  return value;
}

function nullableStringAt(value: unknown, path: string, max = 1024): string | null {
  return value === null ? null : stringAt(value, path, max);
}

function countAt(value: unknown, path: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < 0) {
    return invalid(path, "expected a non-negative integer");
  }
  return value as number;
}

function nullableCountAt(value: unknown, path: string): number | null {
  return value === null ? null : countAt(value, path);
}

function numberAt(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return invalid(path, "expected a finite number");
  }
  return value;
}

function booleanAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") return invalid(path, "expected a boolean");
  return value;
}

function nullable<T>(
  value: unknown,
  path: string,
  parse: (value: unknown, path: string) => T,
): T | null {
  return value === null ? null : parse(value, path);
}

function parseView(value: unknown, path: string): TrayCoreView {
  const view = objectAt(value, path);
  exactKeys(
    view,
    [
      "phase",
      "inference_url",
      "core_version",
      "inference_port_fallback",
      "last_error",
      "recovery_attempt",
      "recovery_scheduled",
      "observer_active",
    ],
    path,
  );
  if (typeof view.phase !== "string" || !phases.has(view.phase as CorePhase)) {
    invalid(`${path}.phase`, "unknown Core phase");
  }
  const fallback = nullable(view.inference_port_fallback, `${path}.inference_port_fallback`, (raw, rawPath) => {
    const object = objectAt(raw, rawPath);
    exactKeys(object, ["requested_port", "active_port"], rawPath);
    return {
      requested_port: countAt(object.requested_port, `${rawPath}.requested_port`),
      active_port: countAt(object.active_port, `${rawPath}.active_port`),
    };
  });
  return {
    phase: view.phase as CorePhase,
    inference_url: nullableStringAt(view.inference_url, `${path}.inference_url`, 256),
    core_version: nullableStringAt(view.core_version, `${path}.core_version`, 64),
    inference_port_fallback: fallback,
    last_error: nullableStringAt(view.last_error, `${path}.last_error`, 4096),
    recovery_attempt: countAt(view.recovery_attempt, `${path}.recovery_attempt`),
    recovery_scheduled: booleanAt(view.recovery_scheduled, `${path}.recovery_scheduled`),
    observer_active: booleanAt(view.observer_active, `${path}.observer_active`),
  };
}

function parseShare(value: unknown, path: string): TrayShare {
  const share = objectAt(value, path);
  exactKeys(share, ["name", "percent"], path);
  const percent = countAt(share.percent, `${path}.percent`);
  if (percent > 100) invalid(`${path}.percent`, "expected 0 through 100");
  return { name: stringAt(share.name, `${path}.name`, 256), percent };
}

function parseDigest(value: unknown, path: string): TrayUsageDigest {
  const digest = objectAt(value, path);
  exactKeys(
    digest,
    [
      "today",
      "hourly_tokens",
      "yesterday_tokens",
      "top_model",
      "cost_today",
      "top_client",
      "last_request",
      "month_tokens",
      "subscriptions",
    ],
    path,
  );
  const today = nullable(digest.today, `${path}.today`, (raw, rawPath) => {
    const totals = objectAt(raw, rawPath);
    exactKeys(totals, ["requests", "failed", "total_tokens", "input_tokens", "cache_read_tokens"], rawPath);
    return {
      requests: countAt(totals.requests, `${rawPath}.requests`),
      failed: countAt(totals.failed, `${rawPath}.failed`),
      total_tokens: countAt(totals.total_tokens, `${rawPath}.total_tokens`),
      input_tokens: countAt(totals.input_tokens, `${rawPath}.input_tokens`),
      cache_read_tokens: countAt(totals.cache_read_tokens, `${rawPath}.cache_read_tokens`),
    };
  });
  if (!Array.isArray(digest.hourly_tokens) || (digest.hourly_tokens.length !== 0 && digest.hourly_tokens.length !== 24)) {
    invalid(`${path}.hourly_tokens`, "expected 0 or 24 entries");
  }
  const hourly = (digest.hourly_tokens as unknown[]).map((tokens, index) =>
    countAt(tokens, `${path}.hourly_tokens[${index}]`),
  );
  const cost = nullable(digest.cost_today, `${path}.cost_today`, (raw, rawPath) => {
    const object = objectAt(raw, rawPath);
    exactKeys(object, ["amount_usd", "unpriced"], rawPath);
    const amount = numberAt(object.amount_usd, `${rawPath}.amount_usd`);
    if (amount < 0) invalid(`${rawPath}.amount_usd`, "expected a non-negative amount");
    return { amount_usd: amount, unpriced: countAt(object.unpriced, `${rawPath}.unpriced`) };
  });
  const last = nullable(digest.last_request, `${path}.last_request`, (raw, rawPath) => {
    const object = objectAt(raw, rawPath);
    exactKeys(object, ["started_at", "model", "latency_ms", "failed"], rawPath);
    const startedAt = stringAt(object.started_at, `${rawPath}.started_at`, 64);
    if (Number.isNaN(Date.parse(startedAt))) invalid(`${rawPath}.started_at`, "expected a timestamp");
    return {
      started_at: startedAt,
      model: nullableStringAt(object.model, `${rawPath}.model`, 256),
      latency_ms: nullableCountAt(object.latency_ms, `${rawPath}.latency_ms`),
      failed: booleanAt(object.failed, `${rawPath}.failed`),
    };
  });
  if (!Array.isArray(digest.subscriptions) || digest.subscriptions.length > 16) {
    invalid(`${path}.subscriptions`, "expected a bounded array");
  }
  const subscriptions = (digest.subscriptions as unknown[]).map((raw, index) => {
    const subscriptionPath = `${path}.subscriptions[${index}]`;
    const object = objectAt(raw, subscriptionPath);
    exactKeys(object, ["name", "windows"], subscriptionPath);
    if (!Array.isArray(object.windows) || object.windows.length === 0 || object.windows.length > 8) {
      invalid(`${subscriptionPath}.windows`, "expected 1 through 8 windows");
    }
    return {
      name: stringAt(object.name, `${subscriptionPath}.name`, 256),
      windows: (object.windows as unknown[]).map((rawWindow, windowIndex) => {
        const windowPath = `${subscriptionPath}.windows[${windowIndex}]`;
        const window = objectAt(rawWindow, windowPath);
        exactKeys(window, ["label", "limit_window_seconds", "secondary", "used_percent", "reset_at"], windowPath);
        const used = numberAt(window.used_percent, `${windowPath}.used_percent`);
        if (used < 0) invalid(`${windowPath}.used_percent`, "expected a non-negative percent");
        const resetAt = nullableStringAt(window.reset_at, `${windowPath}.reset_at`, 64);
        if (resetAt !== null && Number.isNaN(Date.parse(resetAt))) {
          invalid(`${windowPath}.reset_at`, "expected a timestamp");
        }
        return {
          label: nullableStringAt(window.label, `${windowPath}.label`, 64),
          limit_window_seconds: nullableCountAt(window.limit_window_seconds, `${windowPath}.limit_window_seconds`),
          secondary: booleanAt(window.secondary, `${windowPath}.secondary`),
          used_percent: used,
          reset_at: resetAt,
        };
      }),
    };
  });
  return {
    today,
    hourly_tokens: hourly,
    yesterday_tokens: nullableCountAt(digest.yesterday_tokens, `${path}.yesterday_tokens`),
    top_model: nullable(digest.top_model, `${path}.top_model`, parseShare),
    cost_today: cost,
    top_client: nullable(digest.top_client, `${path}.top_client`, parseShare),
    last_request: last,
    month_tokens: nullableCountAt(digest.month_tokens, `${path}.month_tokens`),
    subscriptions,
  };
}

export function parseTrayState(value: unknown): TrayState {
  const root = objectAt(value, "$");
  exactKeys(root, ["app_version", "platform", "view", "digest", "digest_age_ms", "tray", "popover_below"], "$");
  return {
    app_version: stringAt(root.app_version, "$.app_version", 64),
    platform: stringAt(root.platform, "$.platform", 32),
    view: parseView(root.view, "$.view"),
    digest: nullable(root.digest, "$.digest", parseDigest),
    digest_age_ms: nullableCountAt(root.digest_age_ms, "$.digest_age_ms"),
    tray: parseTrayPreferences(root.tray, "$.tray"),
    popover_below: booleanAt(root.popover_below, "$.popover_below"),
  };
}

/** Compact token counts for tiles: 912, 1.2K, 348K, 1.2M, 48M, 2B. */
export function formatCompactTokens(tokens: number): string {
  const value = Math.max(0, tokens);
  const scale = (divisor: number, unit: string) => {
    const scaled = value / divisor;
    if (scaled >= 100) return `${Math.round(scaled)}${unit}`;
    const text = scaled.toFixed(1).replace(/\.0$/, "");
    return `${text}${unit}`;
  };
  if (value >= 1_000_000_000) return scale(1_000_000_000, "B");
  if (value >= 1_000_000) return scale(1_000_000, "M");
  if (value >= 1_000) return scale(1_000, "K");
  return `${Math.round(value)}`;
}

export function formatUsd(amount: number): string {
  if (!Number.isFinite(amount) || amount <= 0) return "0.00";
  if (amount < 0.01) return "<0.01";
  return amount.toFixed(2);
}

/** Signed percent change of today against yesterday; null without a baseline. */
export function percentChange(today: number, yesterday: number): number | null {
  if (yesterday <= 0) return null;
  return Math.round(((today - yesterday) * 100) / yesterday);
}

export function cacheHitPercent(totals: TrayUsageTotals): number | null {
  if (totals.input_tokens <= 0) return null;
  return Math.round((totals.cache_read_tokens * 100) / totals.input_tokens);
}
