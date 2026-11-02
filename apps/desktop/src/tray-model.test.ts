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
    observer_read_level: null,
    pending_raw_access: 0,
    active_raw_grants: 0,
    raw_password_required: false,
    raw_key_event: null,
    raw_key_replaced: false,
  },
  digest: {
    today: {
      requests: 128,
      failed: 3,
      total_tokens: 1_230_000,
      input_tokens: 1_000_000,
      cache_read_tokens: 410_000,
    },
    hourly_tokens: Array.from({ length: 24 }, (_, hour) =>
      hour === 14 ? 400_000 : hour < 14 ? 50_000 : 0,
    ),
    yesterday_tokens: 1_000_000,
    top_model: { name: "claude-sonnet-4", percent: 62 },
    cost_today: { amount_usd: 0.834, unpriced: 0 },
    privacy: {
      enabled: true,
      checked: 120,
      redacted: 9,
      warned: 0,
      blocked: 1,
      unchecked: 0,
      replaced: 23,
      restored: 17,
      unrestored: 0,
      kinds: [
        { kind: "email", count: 14 },
        { kind: "common_secret", count: 6 },
        { kind: "phone", count: 3 },
        { kind: "url", count: 1 },
      ],
    },
    top_client: { name: "Cursor", percent: 71 },
    last_request: {
      started_at: "2026-09-22T10:00:00Z",
      model: "gpt-5",
      latency_ms: 2100,
      failed: false,
    },
    month_tokens: 48_000_000,
    subscriptions: [
      {
        name: "Codex",
        kind: "codex_subscription",
        windows: [
          {
            label: null,
            limit_window_seconds: 18_000,
            secondary: false,
            used_percent: 62,
            reset_at: "2026-09-22T12:13:00Z",
          },
          {
            label: null,
            limit_window_seconds: 604_800,
            secondary: true,
            used_percent: 18,
            reset_at: null,
          },
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
    expect(parsed.view.observer_read_level).toBeNull();
    expect(parsed.view.pending_raw_access).toBe(0);
    expect(parsed.view.raw_password_required).toBe(false);
    expect(parsed.view.raw_key_event).toBeNull();
    expect(parsed.view.raw_key_replaced).toBe(false);
    expect(parsed.popover_below).toBe(true);
  });

  it("reads the raw password gate and the latest raw key change", () => {
    const parsed = parseTrayState({
      ...readyTrayState,
      view: {
        ...readyTrayState.view,
        raw_password_required: true,
        raw_key_event: { kind: "raw_key_reset", at: "2026-09-28T09:30:00Z" },
        raw_key_replaced: true,
      },
    });
    expect(parsed.view.raw_password_required).toBe(true);
    expect(parsed.view.raw_key_replaced).toBe(true);
    expect(parsed.view.raw_key_event).toEqual({
      kind: "raw_key_reset",
      at: "2026-09-28T09:30:00Z",
    });
  });

  it("reads today's privacy counts and rejects ones that cannot add up", () => {
    const parsed = parseTrayState(readyTrayState);
    expect(parsed.digest?.privacy?.replaced).toBe(23);
    expect(parsed.digest?.privacy?.kinds[0]).toEqual({
      kind: "email",
      count: 14,
    });
    const privacy = readyTrayState.digest.privacy;
    const withPrivacy = (value: unknown) => ({
      ...readyTrayState,
      digest: { ...readyTrayState.digest, privacy: value },
    });
    expect(parseTrayState(withPrivacy(null)).digest?.privacy).toBeNull();
    expect(
      parseTrayState(withPrivacy({ ...privacy, enabled: null })).digest?.privacy
        ?.enabled,
    ).toBeNull();
    expect(() =>
      parseTrayState(withPrivacy({ ...privacy, checked: 5 })),
    ).toThrow("$.digest.privacy.checked");
    expect(() =>
      parseTrayState(
        withPrivacy({
          ...privacy,
          kinds: Array.from({ length: 17 }, () => privacy.kinds[0]),
        }),
      ),
    ).toThrow("$.digest.privacy.kinds");
    const { unrestored: _unrestored, ...missing } = privacy;
    expect(() => parseTrayState(withPrivacy(missing))).toThrow(
      "$.digest.privacy.unrestored",
    );
  });

  it("accepts a stopped gateway without a digest", () => {
    const parsed = parseTrayState({
      ...readyTrayState,
      view: {
        ...readyTrayState.view,
        phase: "stopped",
        inference_url: null,
        core_version: null,
      },
      digest: null,
      digest_age_ms: null,
    });
    expect(parsed.digest).toBeNull();
    expect(parsed.view.inference_url).toBeNull();
  });

  it("rejects malformed payloads at the offending path", () => {
    const withDigest = (patch: Record<string, unknown>) =>
      parseTrayState({
        ...readyTrayState,
        digest: { ...readyTrayState.digest, ...patch },
      });
    expect(() => parseTrayState({ ...readyTrayState, surprise: 1 })).toThrow(
      "$.surprise",
    );
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        view: { ...readyTrayState.view, phase: "unavailable" },
      }),
    ).toThrow("$.view.phase");
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        view: { ...readyTrayState.view, observer_read_level: "full" },
      }),
    ).toThrow("$.view.observer_read_level");
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        view: { ...readyTrayState.view, pending_raw_access: -1 },
      }),
    ).toThrow("$.view.pending_raw_access");
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        view: { ...readyTrayState.view, active_raw_grants: 1.5 },
      }),
    ).toThrow("$.view.active_raw_grants");
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        view: { ...readyTrayState.view, raw_password_required: "yes" },
      }),
    ).toThrow("$.view.raw_password_required");
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        view: { ...readyTrayState.view, raw_key_replaced: null },
      }),
    ).toThrow("$.view.raw_key_replaced");
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        view: {
          ...readyTrayState.view,
          raw_key_event: { kind: "raw_read", at: "2026-09-28T09:30:00Z" },
        },
      }),
    ).toThrow("$.view.raw_key_event.kind");
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        view: {
          ...readyTrayState.view,
          raw_key_event: { kind: "raw_key_reset", at: "yesterday" },
        },
      }),
    ).toThrow("$.view.raw_key_event.at");
    expect(() => withDigest({ hourly_tokens: [1, 2, 3] })).toThrow(
      "$.digest.hourly_tokens",
    );
    expect(() =>
      withDigest({ top_model: { name: "x", percent: 101 } }),
    ).toThrow("$.digest.top_model.percent");
    expect(() =>
      withDigest({ cost_today: { amount_usd: "0.83", unpriced: 0 } }),
    ).toThrow("$.digest.cost_today.amount_usd");
    expect(() =>
      withDigest({
        subscriptions: [
          { name: "Codex", kind: "codex_subscription", windows: [] },
        ],
      }),
    ).toThrow("$.digest.subscriptions[0].windows");
    expect(() =>
      withDigest({
        subscriptions: [
          {
            ...readyTrayState.digest.subscriptions[0],
            kind: "openai",
          },
        ],
      }),
    ).toThrow("$.digest.subscriptions[0].kind");
    expect(() =>
      withDigest({
        last_request: {
          started_at: "yesterday",
          model: null,
          latency_ms: null,
          failed: false,
        },
      }),
    ).toThrow("$.digest.last_request.started_at");
    expect(() =>
      parseTrayState({
        ...readyTrayState,
        tray: { ...readyTrayState.tray, pages: ["overview"] },
      }),
    ).toThrow("$.tray.pages[0]");
  });

  it("formats numbers the way the tiles show them", () => {
    expect(formatCompactTokens(912)).toBe("912");
    expect(formatCompactTokens(1_230)).toBe("1.2K");
    expect(formatCompactTokens(348_000)).toBe("348K");
    expect(formatCompactTokens(1_230_000)).toBe("1.2M");
    expect(formatCompactTokens(48_000_000)).toBe("48M");
    expect(formatCompactTokens(2_000_000_000)).toBe("2B");
    expect(formatUsd(0)).toBe("$0.00");
    expect(formatUsd(0.004)).toBe("<$0.01");
    expect(formatUsd(0.834)).toBe("$0.83");
    expect(percentChange(1_230_000, 1_000_000)).toBe(23);
    expect(percentChange(800_000, 1_000_000)).toBe(-20);
    expect(percentChange(5, 0)).toBeNull();
    expect(
      cacheHitPercent({
        requests: 1,
        failed: 0,
        total_tokens: 1,
        input_tokens: 1_000_000,
        cache_read_tokens: 410_000,
      }),
    ).toBe(41);
    expect(
      cacheHitPercent({
        requests: 1,
        failed: 0,
        total_tokens: 1,
        input_tokens: 0,
        cache_read_tokens: 0,
      }),
    ).toBeNull();
  });
});
