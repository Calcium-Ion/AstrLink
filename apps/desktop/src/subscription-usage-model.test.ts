import { describe, expect, it } from "vitest";

import {
  formatQuotaExpiry,
  formatQuotaUSD,
  formatResetCountdown,
  formatSubscriptionUsageError,
  parseSubscriptionUsage,
  parseSubscriptionUsageReset,
  parseResetCreditsDetails,
  resetCreditExpiryWarning,
  planTypeLabel,
  quotaUsedPercent,
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
  it("warns within three days and escalates within 24 hours, excluding expired credits", () => {
    const now = Date.parse("2026-09-30T12:00:00Z");
    const details = {
      available_count: 5,
      credits: [
        { expires_at: "2026-10-04T12:00:00Z" },
        { expires_at: "2026-10-03T12:00:00Z" },
        { expires_at: "2026-10-01T12:00:00Z" },
        { expires_at: "2026-09-30T12:00:00Z" },
        {},
      ],
    };
    expect(resetCreditExpiryWarning(details, now)).toEqual({
      count: 2,
      expiresAt: Date.parse("2026-10-01T12:00:00Z"),
      urgent: true,
    });
    expect(
      resetCreditExpiryWarning(
        { available_count: 1, credits: [details.credits[1]] },
        now,
      )?.urgent,
    ).toBe(false);
    expect(
      resetCreditExpiryWarning({ ...details, available_count: 0 }, now),
    ).toBeNull();
    expect(
      resetCreditExpiryWarning(
        { available_count: 1, credits: [details.credits[0], {}] },
        now,
      ),
    ).toBeNull();
  });
  it("validates reset expiry details and rejects unexpected upstream fields", () => {
    const details = {
      available_count: 2,
      credits: [{ expires_at: "2026-10-15T12:00:00Z" }, {}],
    };
    expect(parseResetCreditsDetails(details)).toEqual(details);
    for (const invalid of [
      null,
      { ...details, available_count: -1 },
      { ...details, credits: [{ expires_at: "invalid" }] },
      { ...details, credits: [{ id: "private-id" }] },
    ]) {
      expect(() => parseResetCreditsDetails(invalid)).toThrow();
    }
  });
  it.each([17, 33, 256])("keeps all %i per-model quota windows", (count) => {
    const usage = {
      service_id: "service_antigravity",
      fetched_at: snapshot.fetched_at,
      additional_rate_limits: Array.from({ length: count }, (_, index) => ({
        limit_name: `gemini-test-${index}`,
        primary: { used_percent: 25 },
      })),
    };
    expect(parseSubscriptionUsage(usage)).toEqual(usage);
  });

  it("rejects more than 256 quota windows", () => {
    expect(() =>
      parseSubscriptionUsage({
        ...snapshot,
        additional_rate_limits: Array.from(
          { length: 257 },
          () => snapshot.additional_rate_limits[0],
        ),
      }),
    ).toThrow(/at most 256/);
  });

  it("parses a sanitized official snapshot", () => {
    expect(parseSubscriptionUsage(snapshot)).toEqual(snapshot);
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

  it.each([
    ["openai_codex", "plus", "Plus"],
    ["openai_codex", "prolite", "Pro 5×"],
    ["openai_codex", "PRO", "Pro 20×"],
    ["openai_codex", "team", "Team"],
    ["claude_code", "pro", "Pro"],
    ["claude_code", "max", "Max"],
    ["claude_code", "max_5x", "Max 5×"],
    ["claude_code", "max_20x", "Max 20×"],
    ["xai_grok", "supergrok", "SuperGrok"],
    ["xai_grok", "supergrok_heavy", "SuperGrok Heavy"],
    ["xai_grok", "SuperGrok Heavy", "SuperGrok Heavy"],
    ["claude_code", "prolite", "prolite"],
    ["xai_grok", "pro", "pro"],
    ["openai_codex", "future_plan", "future_plan"],
    ["openai_codex", "constructor", "constructor"],
    [undefined, "pro", "pro"],
    ["openai_codex", undefined, null],
  ] as const)(
    "formats %s / %s within its provider",
    (provider, planType, label) => {
      expect(planTypeLabel(planType, provider)).toBe(label);
    },
  );

  it("rejects PII and unexpected fields", () => {
    expect(() =>
      parseSubscriptionUsage({ ...snapshot, email: "owner@example.com" }),
    ).toThrow(/unexpected field/);
    expect(() =>
      parseSubscriptionUsage({ ...snapshot, plan_type: "user@example.com" }),
    ).toThrow(/credential/);
  });

  it("parses a New API key quota and rejects a limited one without totals", () => {
    const keyQuota = {
      service_id: "service_newapi",
      fetched_at: "2026-09-22T11:00:00Z",
      limit_reached: false,
      quota: {
        unlimited: false,
        used_usd: "2.5",
        remaining_usd: "7.5",
        total_usd: "10",
        expires_at: "2026-09-25T11:00:00Z",
      },
    };
    const parsed = parseSubscriptionUsage(keyQuota);
    expect(parsed).toEqual(keyQuota);
    expect(quotaUsedPercent(parsed.quota!)).toBe(25);
    expect(
      quotaUsedPercent({ unlimited: false, used_usd: "0", total_usd: "0" }),
    ).toBe(100);

    const now = new Date("2026-09-22T11:00:00Z");
    expect(formatQuotaExpiry(parsed.quota!, now)).toBe("3 天后到期");
    expect(
      formatQuotaExpiry(
        { ...parsed.quota!, expires_at: "2026-09-22T16:30:00Z" },
        now,
      ),
    ).toBe("5 小时后到期");
    expect(
      formatQuotaExpiry(
        { ...parsed.quota!, expires_at: "2026-09-22T10:00:00Z" },
        now,
      ),
    ).toBe("已过期");

    expect(formatQuotaUSD("4490.884098")).toBe("$4,490.88");
    expect(formatQuotaUSD("0.697178")).toBe("$0.70");
    expect(formatQuotaUSD("0.004")).toBe("<$0.01");
    expect(formatQuotaUSD("0")).toBe("$0.00");
    expect(formatQuotaUSD(undefined)).toBe("—");

    expect(
      parseSubscriptionUsage({
        ...keyQuota,
        quota: { unlimited: true, used_usd: "12.345678" },
      }).quota,
    ).toEqual({ unlimited: true, used_usd: "12.345678" });
    expect(() =>
      parseSubscriptionUsage({
        ...keyQuota,
        quota: { unlimited: false, used_usd: "1" },
      }),
    ).toThrow(/limited quota/);
    expect(() =>
      parseSubscriptionUsage({
        ...keyQuota,
        quota: { ...keyQuota.quota, used_usd: "-1" },
      }),
    ).toThrow(/USD decimal/);
    expect(() =>
      parseSubscriptionUsage({
        ...keyQuota,
        quota: { ...keyQuota.quota, name: "desk" },
      }),
    ).toThrow(/unexpected field/);
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
    expect(
      formatResetCountdown(
        { used_percent: 34, reset_at: "2026-09-03T11:00:00Z" },
        now,
        { short: true },
      ),
    ).toBe("4 天后");
  });

  it("extracts the control error from a sidecar failure", () => {
    expect(
      formatSubscriptionUsageError(
        new Error(
          `GET /control/v1/services/service_codex_01/usage returned 502 Bad Gateway: {"error":{"code":"subscription_usage_failed","message":"codex usage unavailable: status 403"},"request_id":"req_1"}`,
        ),
      ),
    ).toBe("subscription_usage_failed: codex usage unavailable: status 403");
    expect(formatSubscriptionUsageError("网关尚未就绪。")).toBe(
      "网关尚未就绪。",
    );
    expect(formatSubscriptionUsageError({})).toBe("无法读取额度");
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
