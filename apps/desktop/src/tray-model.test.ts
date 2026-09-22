import { describe, expect, it } from "vitest";

import { defaultTrayPreferences } from "./preferences-model";
import {
  cacheHitPercent,
  formatCompactTokens,
  formatUsd,
  parseTrayState,
  percentChange,
} from "./tray-model";

export const readyTrayState = {
  app_version: "0.1.0",
  platform: "macos",
  view: {
    phase: "ready",
    inference_url: "http://127.0.0.1:8317",
    core_version: "0.1.0",
    inference_port_fallback: null,
    last_error: null,
    recovery_attempt: 0,
    recovery_scheduled: false,
    observer_active: false,
  },
  digest: {
    today: { requests: 128, failed: 3, total_tokens: 1_230_000, input_tokens: 1_000_000, cache_read_tokens: 410_000 },
    hourly_tokens: Array.from({ length: 24 }, (_, hour) => (hour === 14 ? 400_000 : hour < 14 ? 50_000 : 0)),
    yesterday_tokens: 1_000_000,
    top_model: { name: "claude-sonnet-4", percent: 62 },
    cost_today: { amount_usd: 0.834, unpriced: 0 },
    top_client: { name: "Cursor", percent: 71 },
    last_request: { started_at: "2026-09-22T10:00:00Z", model: "gpt-5", latency_ms: 2100, failed: false },
    month_tokens: 48_000_000,
    subscriptions: [
      {
        name: "Codex",
        windows: [
          { label: null, limit_window_seconds: 18_000, secondary: false, used_percent: 62, reset_at: "2026-09-22T12:13:00Z" },
          { label: null, limit_window_seconds: 604_800, secondary: true, used_percent: 18, reset_at: null },
        ],
      },
    ],
  },
  digest_age_ms: 1200,
  tray: defaultTrayPreferences(),
  popover_below: true,
};

describe("tray state IPC contract", () => {
  it("parses the host snapshot", () => {
    const parsed = parseTrayState(readyTrayState);
    expect(parsed.view.phase).toBe("ready");
    expect(parsed.digest?.today?.requests).toBe(128);
    expect(parsed.digest?.hourly_tokens).toHaveLength(24);
    expect(parsed.digest?.subscriptions[0].windows[1].secondary).toBe(true);
    expect(parsed.tray.pages).toEqual(["records", "services", "tokens"]);
    expect(parsed.view.observer_active).toBe(false);
    expect(parsed.popover_below).toBe(true);
  });

  it("accepts a stopped gateway without a digest", () => {
    const parsed = parseTrayState({
      ...readyTrayState,
      view: { ...readyTrayState.view, phase: "stopped", inference_url: null, core_version: null },
      digest: null,
      digest_age_ms: null,
    });
    expect(parsed.digest).toBeNull();
    expect(parsed.view.inference_url).toBeNull();
  });

  it("rejects malformed payloads at the offending path", () => {
    const withDigest = (patch: Record<string, unknown>) =>
      parseTrayState({ ...readyTrayState, digest: { ...readyTrayState.digest, ...patch } });
    expect(() => parseTrayState({ ...readyTrayState, surprise: 1 })).toThrow("$.surprise");
    expect(() => parseTrayState({ ...readyTrayState, view: { ...readyTrayState.view, phase: "unavailable" } })).toThrow(
      "$.view.phase",
    );
    expect(() => withDigest({ hourly_tokens: [1, 2, 3] })).toThrow("$.digest.hourly_tokens");
    expect(() => withDigest({ top_model: { name: "x", percent: 101 } })).toThrow("$.digest.top_model.percent");
    expect(() => withDigest({ cost_today: { amount_usd: "0.83", unpriced: 0 } })).toThrow(
      "$.digest.cost_today.amount_usd",
    );
    expect(() => withDigest({ subscriptions: [{ name: "Codex", windows: [] }] })).toThrow(
      "$.digest.subscriptions[0].windows",
    );
    expect(() =>
      withDigest({ last_request: { started_at: "yesterday", model: null, latency_ms: null, failed: false } }),
    ).toThrow("$.digest.last_request.started_at");
    expect(() => parseTrayState({ ...readyTrayState, tray: { ...readyTrayState.tray, pages: ["overview"] } })).toThrow(
      "$.tray.pages[0]",
    );
  });

  it("formats numbers the way the tiles show them", () => {
    expect(formatCompactTokens(912)).toBe("912");
    expect(formatCompactTokens(1_230)).toBe("1.2K");
    expect(formatCompactTokens(348_000)).toBe("348K");
    expect(formatCompactTokens(1_230_000)).toBe("1.2M");
    expect(formatCompactTokens(48_000_000)).toBe("48M");
    expect(formatCompactTokens(2_000_000_000)).toBe("2B");
    expect(formatUsd(0)).toBe("0.00");
    expect(formatUsd(0.004)).toBe("<0.01");
    expect(formatUsd(0.834)).toBe("0.83");
    expect(percentChange(1_230_000, 1_000_000)).toBe(23);
    expect(percentChange(800_000, 1_000_000)).toBe(-20);
    expect(percentChange(5, 0)).toBeNull();
    expect(cacheHitPercent({ requests: 1, failed: 0, total_tokens: 1, input_tokens: 1_000_000, cache_read_tokens: 410_000 })).toBe(41);
    expect(cacheHitPercent({ requests: 1, failed: 0, total_tokens: 1, input_tokens: 0, cache_read_tokens: 0 })).toBeNull();
  });
});
