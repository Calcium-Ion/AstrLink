import { parseUpdatePreferences, type UpdatePreferences } from "./update-model";
import { isQuotaDisplayMode, type QuotaDisplayMode } from "./quota-display";
import { isLocale, type Locale } from "./i18n/locale";
import { isThemePreference, type ThemePreference } from "./theme-model";

export type CloseBehavior = "hide_to_tray" | "quit";

/** Which interfaces the inference port answers; mirrors `InferenceListen` in `preferences.rs`. */
export const INFERENCE_LISTENS = ["loopback", "all_interfaces"] as const;
export type InferenceListen = (typeof INFERENCE_LISTENS)[number];

export function isInferenceListen(value: unknown): value is InferenceListen {
  return (
    typeof value === "string" &&
    (INFERENCE_LISTENS as readonly string[]).includes(value)
  );
}

export const DEFAULT_MAX_CONCURRENT_INSPECTIONS = 16;
export const MIN_MAX_CONCURRENT_INSPECTIONS = 4;
export const MAX_MAX_CONCURRENT_INSPECTIONS = 128;
export const MAX_REQUEST_BODY_MIB = 0xffffffff;
export const DEFAULT_RESPONSE_START_TIMEOUT_SECONDS = 0;
export const MAX_RESPONSE_START_TIMEOUT_SECONDS = 86400;

/** Text shown next to the macOS status-item icon; tooltip elsewhere. */
export const TRAY_MENUBAR_TEXTS = [
  "none",
  "requests",
  "tokens",
  "cost",
  "subscription",
  "alert_only",
] as const;
export type TrayMenubarText = (typeof TRAY_MENUBAR_TEXTS)[number];

/** Quick-jump pages in navigation order. Overview and settings are fixed. */
export const TRAY_PAGES = [
  "records",
  "services",
  "tokens",
  "safety",
  "routing",
  "agent_tools",
] as const;
export type TrayPage = (typeof TRAY_PAGES)[number];

export const TRAY_USAGE_KEYS = [
  "today",
  "cost",
  "subscription_windows",
  "top_model",
  "compare_yesterday",
  "cache_hit",
  "top_client",
  "last_request",
  "month_total",
] as const;
export type TrayUsageKey = (typeof TRAY_USAGE_KEYS)[number];
export type TrayUsagePreferences = Record<TrayUsageKey, boolean>;

export interface TrayPreferences {
  menubar_text: TrayMenubarText;
  copy_address: boolean;
  gateway_controls: boolean;
  usage: TrayUsagePreferences;
  pages: TrayPage[];
}

/** Mirrors `TrayPreferences::default()` in `src-tauri/src/preferences.rs`. */
export function defaultTrayPreferences(): TrayPreferences {
  return {
    menubar_text: "none",
    copy_address: true,
    gateway_controls: true,
    usage: {
      today: true,
      cost: true,
      subscription_windows: true,
      top_model: true,
      compare_yesterday: false,
      cache_hit: false,
      top_client: false,
      last_request: false,
      month_total: false,
    },
    pages: ["records", "services", "tokens"],
  };
}

export interface Preferences {
  close_behavior: CloseBehavior;
  autostart: boolean;
  core_auto_start: boolean;
  core_auto_recover: boolean;
  use_system_proxy: boolean;
  inference_port: number;
  inference_listen: InferenceListen;
  max_concurrent_inspections: number;
  response_start_timeout_seconds: number;
  max_request_body_mib: number;
  locale: Locale;
  theme: ThemePreference;
  quota_display_mode: QuotaDisplayMode;
  tray: TrayPreferences;
  updates: UpdatePreferences;
}

/** A `*.bak*` file in the data directory; possibly a plain-text copy. */
export interface DataBackupFile {
  name: string;
  size_bytes: number;
  modified_unix: number | null;
}

/**
 * Where Core's local key lives: the macOS keychain on signed builds, a key file
 * elsewhere, or a key file because a signed build could not use the keychain.
 */
export type LocalKeyStorage = "keychain" | "file" | "keychain_unavailable";

const LOCAL_KEY_STORAGE: readonly LocalKeyStorage[] = [
  "keychain",
  "file",
  "keychain_unavailable",
];

export interface SettingsSnapshot {
  values: Preferences;
  load_warning: string | null;
  autostart_actual: boolean | null;
  autostart_error: string | null;
  data_backups: DataBackupFile[];
  /** `null` until Core has started once. */
  local_key_storage: LocalKeyStorage | null;
}

const MAX_DATA_BACKUPS = 100;

function invalid(path: string, detail: string): never {
  throw new Error(`Invalid AstrLink preferences IPC at ${path}: ${detail}`);
}

function objectAt(value: unknown, path: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as Record<string, unknown>;
}

function exactKeys(
  value: Record<string, unknown>,
  expected: readonly string[],
  path: string,
): void {
  const keys = new Set(expected);
  for (const key of Object.keys(value)) {
    if (!keys.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of expected) {
    if (!Object.hasOwn(value, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function nullableString(value: unknown, path: string): string | null {
  if (value === null) return null;
  if (typeof value !== "string" || value.length === 0 || value.length > 2048) {
    return invalid(path, "expected null or a bounded non-empty string");
  }
  return value;
}

export function isTrayPage(value: unknown): value is TrayPage {
  return (
    typeof value === "string" &&
    (TRAY_PAGES as readonly string[]).includes(value)
  );
}

export function parseTrayPreferences(
  value: unknown,
  path: string,
): TrayPreferences {
  const tray = objectAt(value, path);
  exactKeys(
    tray,
    ["menubar_text", "copy_address", "gateway_controls", "usage", "pages"],
    path,
  );
  if (
    typeof tray.menubar_text !== "string" ||
    !(TRAY_MENUBAR_TEXTS as readonly string[]).includes(tray.menubar_text)
  ) {
    invalid(`${path}.menubar_text`, "unknown menubar text");
  }
  for (const field of ["copy_address", "gateway_controls"] as const) {
    if (typeof tray[field] !== "boolean")
      invalid(`${path}.${field}`, "expected boolean");
  }
  const usage = objectAt(tray.usage, `${path}.usage`);
  exactKeys(usage, TRAY_USAGE_KEYS, `${path}.usage`);
  for (const key of TRAY_USAGE_KEYS) {
    if (typeof usage[key] !== "boolean")
      invalid(`${path}.usage.${key}`, "expected boolean");
  }
  if (!Array.isArray(tray.pages) || tray.pages.length > TRAY_PAGES.length) {
    invalid(`${path}.pages`, "expected a bounded array");
  }
  const seen = new Set<string>();
  for (const [index, page] of (tray.pages as unknown[]).entries()) {
    if (!isTrayPage(page))
      invalid(`${path}.pages[${index}]`, "unknown tray page");
    if (seen.has(page))
      invalid(`${path}.pages[${index}]`, "duplicate tray page");
    seen.add(page);
  }
  return tray as unknown as TrayPreferences;
}

export function parseSettingsSnapshot(value: unknown): SettingsSnapshot {
  const root = objectAt(value, "$");
  exactKeys(
    root,
    [
      "values",
      "load_warning",
      "autostart_actual",
      "autostart_error",
      "data_backups",
      "local_key_storage",
    ],
    "$",
  );
  const values = objectAt(root.values, "$.values");
  exactKeys(
    values,
    [
      "close_behavior",
      "autostart",
      "core_auto_start",
      "core_auto_recover",
      "use_system_proxy",
      "inference_port",
      "inference_listen",
      "max_concurrent_inspections",
      "response_start_timeout_seconds",
      "max_request_body_mib",
      "locale",
      "theme",
      "quota_display_mode",
      "tray",
      "updates",
    ],
    "$.values",
  );
  if (!isQuotaDisplayMode(values.quota_display_mode)) {
    invalid("$.values.quota_display_mode", "unknown quota display mode");
  }
  parseTrayPreferences(values.tray, "$.values.tray");
  parseUpdatePreferences(values.updates);
  if (
    values.close_behavior !== "hide_to_tray" &&
    values.close_behavior !== "quit"
  ) {
    invalid("$.values.close_behavior", "unknown close behavior");
  }
  if (!isLocale(values.locale)) {
    invalid("$.values.locale", "unknown locale");
  }
  if (!isThemePreference(values.theme)) {
    invalid("$.values.theme", "unknown theme preference");
  }
  for (const field of [
    "autostart",
    "core_auto_start",
    "core_auto_recover",
    "use_system_proxy",
  ] as const) {
    if (typeof values[field] !== "boolean")
      invalid(`$.values.${field}`, "expected boolean");
  }
  if (
    typeof values.inference_port !== "number" ||
    !Number.isInteger(values.inference_port) ||
    values.inference_port < 1024 ||
    values.inference_port > 65535
  ) {
    invalid(
      "$.values.inference_port",
      "expected an integer from 1024 through 65535",
    );
  }
  if (!isInferenceListen(values.inference_listen)) {
    invalid("$.values.inference_listen", "unknown inference listen scope");
  }
  if (
    typeof values.max_concurrent_inspections !== "number" ||
    !Number.isInteger(values.max_concurrent_inspections) ||
    values.max_concurrent_inspections < MIN_MAX_CONCURRENT_INSPECTIONS ||
    values.max_concurrent_inspections > MAX_MAX_CONCURRENT_INSPECTIONS
  ) {
    invalid(
      "$.values.max_concurrent_inspections",
      `expected an integer from ${MIN_MAX_CONCURRENT_INSPECTIONS} through ${MAX_MAX_CONCURRENT_INSPECTIONS}`,
    );
  }
  if (
    typeof values.response_start_timeout_seconds !== "number" ||
    !Number.isInteger(values.response_start_timeout_seconds) ||
    values.response_start_timeout_seconds <
      DEFAULT_RESPONSE_START_TIMEOUT_SECONDS ||
    values.response_start_timeout_seconds > MAX_RESPONSE_START_TIMEOUT_SECONDS
  ) {
    invalid(
      "$.values.response_start_timeout_seconds",
      `expected an integer from ${DEFAULT_RESPONSE_START_TIMEOUT_SECONDS} through ${MAX_RESPONSE_START_TIMEOUT_SECONDS}`,
    );
  }
  if (
    typeof values.max_request_body_mib !== "number" ||
    !Number.isInteger(values.max_request_body_mib) ||
    values.max_request_body_mib < 0 ||
    values.max_request_body_mib > MAX_REQUEST_BODY_MIB
  ) {
    invalid(
      "$.values.max_request_body_mib",
      `expected an integer from 0 through ${MAX_REQUEST_BODY_MIB}`,
    );
  }
  if (
    root.autostart_actual !== null &&
    typeof root.autostart_actual !== "boolean"
  ) {
    invalid("$.autostart_actual", "expected null or boolean");
  }
  return {
    values: values as unknown as Preferences,
    load_warning: nullableString(root.load_warning, "$.load_warning"),
    autostart_actual: root.autostart_actual as boolean | null,
    autostart_error: nullableString(root.autostart_error, "$.autostart_error"),
    data_backups: parseDataBackups(root.data_backups),
    local_key_storage: parseLocalKeyStorage(root.local_key_storage),
  };
}

function parseLocalKeyStorage(value: unknown): LocalKeyStorage | null {
  if (value === null) return null;
  if (
    typeof value !== "string" ||
    !LOCAL_KEY_STORAGE.includes(value as LocalKeyStorage)
  ) {
    return invalid(
      "$.local_key_storage",
      `expected null or one of ${LOCAL_KEY_STORAGE.join(", ")}`,
    );
  }
  return value as LocalKeyStorage;
}

function nonNegativeInteger(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    return invalid(path, "expected a non-negative integer");
  }
  return value;
}

function parseDataBackups(value: unknown): DataBackupFile[] {
  if (!Array.isArray(value) || value.length > MAX_DATA_BACKUPS) {
    return invalid(
      "$.data_backups",
      `expected an array of at most ${MAX_DATA_BACKUPS} files`,
    );
  }
  return value.map((item, index) => {
    const path = `$.data_backups[${index}]`;
    const file = objectAt(item, path);
    exactKeys(file, ["name", "size_bytes", "modified_unix"], path);
    const name = nullableString(file.name, `${path}.name`);
    if (name === null) invalid(`${path}.name`, "expected a file name");
    return {
      name,
      size_bytes: nonNegativeInteger(file.size_bytes, `${path}.size_bytes`),
      modified_unix:
        file.modified_unix === null
          ? null
          : nonNegativeInteger(file.modified_unix, `${path}.modified_unix`),
    };
  });
}
