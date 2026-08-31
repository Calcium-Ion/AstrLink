import { describe, expect, it } from "vitest";

import {
  extraLimitsSummary,
  formatResetCountdown,
  formatSubscriptionUsageError,
  parseSubscriptionUsage,
  parseSubscriptionUsageReset,
  planTypeLabel,
  resetOutcomeMessage,
  usageBarFillClass,
  usageBarPercent,
  usageBarTrackClass,
  usageWindowTone,
  windowLabel,
} from "./subscription-usage-model";

const snapshot = {
  service_id: "service_codex_01",
  fetched_at: "2026-08-30T11:00:00Z",
  plan_type: "plus",
  allowed: true,
  limit_reached: false,
  primary: {
    used_percent: 34,
    limit_window_seconds: 18_000,
    reset_after_seconds: 7_200,
    reset_at: "2026-08-30T13:00:00Z",
  },
  secondary: {
    used_percent: 12,
    limit_window_seconds: 604_800,
    reset_at: "2026-09-05T12:00:00Z",
  },
  additional_rate_limits: [
    {
      limit_name: "GPT-5.3-Codex-Spark",
      metered_feature: "codex_bengalfox",
      primary: { used_percent: 0, limit_window_seconds: 18_000 },
    },
  ],
  credits: { has_credits: false, unlimited: false, balance: "0" },
  rate_limit_reset_credits: { available_count: 2 },
};

describe("subscription usage contract", () => {
  it("parses a sanitized official snapshot", () => {
    expect(parseSubscriptionUsage(snapshot)).toEqual(snapshot);
    expect(planTypeLabel("plus")).toBe("Plus");
    expect(windowLabel(18_000, false)).toBe("5 小时");
    expect(windowLabel(604_800, true)).toBe("7 天");
    expect(usageBarPercent(134)).toBe(100);
    expect(usageWindowTone(80)).toBe("warning");
    expect(usageWindowTone(12, true)).toBe("critical");
    expect(usageBarFillClass("ok")).toBe("bg-success");
    expect(usageBarFillClass("warning")).toBe("bg-warning");
    expect(usageBarFillClass("critical")).toBe("bg-destructive");
    expect(usageBarTrackClass("ok")).toBe("bg-success-wash");
  });

  it("rejects PII and unexpected fields", () => {
    expect(() =>
      parseSubscriptionUsage({ ...snapshot, email: "owner@example.com" }),
    ).toThrow(/unexpected field/);
    expect(() =>
      parseSubscriptionUsage({ ...snapshot, plan_type: "user@example.com" }),
    ).toThrow(/credential/);
  });

  it("formats reset countdown from reset_at", () => {
    const now = new Date("2026-08-30T11:00:00Z");
    expect(
      formatResetCountdown(
        { used_percent: 34, reset_at: "2026-08-30T13:00:00Z" },
        now,
      ),
    ).toBe("2 小时后重置");
    expect(
      formatResetCountdown({ used_percent: 34, reset_after_seconds: 45 }, now),
    ).toBe("即将重置");
  });

  it("extracts the control error from a sidecar failure", () => {
    expect(
      formatSubscriptionUsageError(
        new Error(
          `GET /control/v1/services/service_codex_01/usage returned 502 Bad Gateway: {"error":{"code":"subscription_usage_failed","message":"codex usage unavailable: status 403"},"request_id":"req_1"}`,
        ),
      ),
    ).toBe("subscription_usage_failed: codex usage unavailable: status 403");
    expect(formatSubscriptionUsageError("网关尚未就绪。")).toBe("网关尚未就绪。");
    expect(formatSubscriptionUsageError({})).toBe("无法读取额度");
  });

  it("collapses extra limits to a label without a window duration", () => {
    expect(extraLimitsSummary(snapshot.additional_rate_limits)).toBe("附加额度");
    expect(extraLimitsSummary([])).toBe("");
  });

  it("parses an official consume outcome", () => {
    expect(
      parseSubscriptionUsageReset({
        service_id: "service_codex_01",
        outcome: "reset",
        windows_reset: 2,
      }),
    ).toEqual({
      service_id: "service_codex_01",
      outcome: "reset",
      windows_reset: 2,
    });
    expect(resetOutcomeMessage("reset")).toBe("额度已重置。");
    expect(() =>
      parseSubscriptionUsageReset({
        service_id: "service_codex_01",
        outcome: "full_reset",
      }),
    ).toThrow(/outcome/);
  });
});
