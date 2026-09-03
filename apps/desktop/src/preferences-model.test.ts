import { describe, expect, it } from "vitest";

import { parseSettingsSnapshot } from "./preferences-model";

const valid = {
  values: {
    close_behavior: "hide_to_tray",
    autostart: false,
    core_auto_start: true,
    core_auto_recover: true,
    inference_port: 8317,
    max_concurrent_inspections: 16,
    response_start_timeout_seconds: 0,
    locale: "zh-CN",
  },
  load_warning: null,
  autostart_actual: false,
  autostart_error: null,
};

describe("preferences IPC contract", () => {
  it("strictly parses the complete settings snapshot", () => {
    expect(parseSettingsSnapshot(valid)).toEqual(valid);
  });

  it("rejects unknown fields and unsafe ports", () => {
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
