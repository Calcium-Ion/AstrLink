export type CloseBehavior = "hide_to_tray" | "quit";

export interface Preferences {
  close_behavior: CloseBehavior;
  autostart: boolean;
  core_auto_start: boolean;
  core_auto_recover: boolean;
  inference_port: number;
}

export interface SettingsSnapshot {
  values: Preferences;
  load_warning: string | null;
  autostart_actual: boolean | null;
  autostart_error: string | null;
}

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

export function parseSettingsSnapshot(value: unknown): SettingsSnapshot {
  const root = objectAt(value, "$");
  exactKeys(root, ["values", "load_warning", "autostart_actual", "autostart_error"], "$");
  const values = objectAt(root.values, "$.values");
  exactKeys(
    values,
    [
      "close_behavior",
      "autostart",
      "core_auto_start",
      "core_auto_recover",
      "inference_port",
    ],
    "$.values",
  );
  if (values.close_behavior !== "hide_to_tray" && values.close_behavior !== "quit") {
    invalid("$.values.close_behavior", "unknown close behavior");
  }
  for (const field of ["autostart", "core_auto_start", "core_auto_recover"] as const) {
    if (typeof values[field] !== "boolean") invalid(`$.values.${field}`, "expected boolean");
  }
  if (
    typeof values.inference_port !== "number" ||
    !Number.isInteger(values.inference_port) ||
    values.inference_port < 1024 ||
    values.inference_port > 65535
  ) {
    invalid("$.values.inference_port", "expected an integer from 1024 through 65535");
  }
  if (root.autostart_actual !== null && typeof root.autostart_actual !== "boolean") {
    invalid("$.autostart_actual", "expected null or boolean");
  }
  return {
    values: values as unknown as Preferences,
    load_warning: nullableString(root.load_warning, "$.load_warning"),
    autostart_actual: root.autostart_actual as boolean | null,
    autostart_error: nullableString(root.autostart_error, "$.autostart_error"),
  };
}
