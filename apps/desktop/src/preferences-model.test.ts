import { describe, expect, it } from "vitest";

import { defaultTrayPreferences, parseSettingsSnapshot } from "./preferences-model";

const valid = {
  values: {
    close_behavior: "hide_to_tray",
    autostart: false,
    core_auto_start: true,
    core_auto_recover: true,
    use_system_proxy: true,
    inference_port: 8317,
    max_concurrent_inspections: 16,
    response_start_timeout_seconds: 0,
    max_request_body_mib: 0,
    theme: "system" as const,
    locale: "zh-CN",
    tray: defaultTrayPreferences(),
  },
  load_warning: null,
  autostart_actual: false,
  autostart_error: null,
};

describe("preferences IPC contract", () => {
  it("strictly parses the complete settings snapshot", () => {
    expect(parseSettingsSnapshot(valid)).toEqual(valid);
  });

  it("validates the tray section", () => {
    const tray = defaultTrayPreferences();
    const withTray = (patch: Record<string, unknown>) =>
      parseSettingsSnapshot({ ...valid, values: { ...valid.values, tray: { ...tray, ...patch } } });
    expect(withTray({ menubar_text: "cost" }).values.tray.menubar_text).toBe("cost");
    expect(withTray({ pages: [] }).values.tray.pages).toEqual([]);
    expect(withTray({ pages: ["agent_tools", "records"] }).values.tray.pages).toEqual(["agent_tools", "records"]);
    expect(() => withTray({ menubar_text: "weather" })).toThrow("$.values.tray.menubar_text");
    expect(() => withTray({ pages: ["records", "records"] })).toThrow("$.values.tray.pages[1]");
    expect(() => withTray({ pages: ["overview"] })).toThrow("$.values.tray.pages[0]");
    expect(() => withTray({ usage: { ...tray.usage, cost: "yes" } })).toThrow("$.values.tray.usage.cost");
    expect(() => withTray({ usage: { ...tray.usage, streak: true } })).toThrow("$.values.tray.usage.streak");
    expect(() => withTray({ extra: 1 })).toThrow("$.values.tray.extra");
    expect(() =>
      parseSettingsSnapshot({ ...valid, values: { ...valid.values, tray: undefined } }),
    ).toThrow("$.values.tray");
  });

  it("accepts supported themes and rejects missing or invalid preferences", () => {
    for (const theme of ["system", "light", "dark"]) {
      expect(parseSettingsSnapshot({ ...valid, values: { ...valid.values, theme } }).values.theme).toBe(theme);
    }
    for (const theme of [undefined, null, "auto", true]) {
      expect(() => parseSettingsSnapshot({ ...valid, values: { ...valid.values, theme } })).toThrow("$.values.theme");
    }
  });

  it("rejects unknown fields and unsafe ports", () => {
    expect(() => parseSettingsSnapshot({
      ...valid,
      values: { ...valid.values, use_system_proxy: "true" },
    })).toThrow("$.values.use_system_proxy");
    expect(() => parseSettingsSnapshot({ ...valid, surprise: true })).toThrow(
      "$.surprise",
    );
    expect(() =>
      parseSettingsSnapshot({
        ...valid,
        values: { ...valid.values, inference_port: 80 },
      }),
    ).toThrow("$.values.inference_port");
    expect(() =>
      parseSettingsSnapshot({
        ...valid,
        values: { ...valid.values, max_concurrent_inspections: 3 },
      }),
    ).toThrow("$.values.max_concurrent_inspections");
    expect(() =>
      parseSettingsSnapshot({
        ...valid,
        values: { ...valid.values, response_start_timeout_seconds: 86401 },
      }),
    ).toThrow("$.values.response_start_timeout_seconds");
  });

  it("accepts unlimited and explicit request body limits and rejects invalid values", () => {
    for (const max_request_body_mib of [0, 1, 64, 0xffffffff]) {
      expect(parseSettingsSnapshot({ ...valid, values: { ...valid.values, max_request_body_mib } }).values.max_request_body_mib).toBe(max_request_body_mib);
    }
    for (const max_request_body_mib of [-1, 1.5, 0x100000000, NaN, Infinity, "8", null, undefined]) {
      expect(() => parseSettingsSnapshot({ ...valid, values: { ...valid.values, max_request_body_mib } })).toThrow("$.values.max_request_body_mib");
    }
  });

  it("does not invent an OS state when reconciliation failed", () => {
    const parsed = parseSettingsSnapshot({
      ...valid,
      autostart_actual: null,
      autostart_error: "系统 API 不可用",
    });
    expect(parsed.autostart_actual).toBeNull();
    expect(parsed.autostart_error).toBe("系统 API 不可用");
  });
});
