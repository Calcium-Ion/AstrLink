import { describe, expect, it } from "vitest";
import {
  emptyUsageTotals,
  resolveUsageWindow,
  usageWindowHourSlots,
} from "./usage-range";
import { parseUsageSummary } from "./usage-summary-model";

const window = resolveUsageWindow("1d", new Date(2026, 8, 19, 23));
const date = "2026-09-19";
const totals = { ...emptyUsageTotals(), requests: 5001, total_tokens: 10002 };
const statusCounts = {
  succeeded: 5001,
  failed: 0,
  blocked: 2,
  cancelled: 1,
  pending: 0,
};
const response = {
  totals,
  status_counts: statusCounts,
  by_day: [{ date, ...totals }],
  by_hour: [{ date, hour: 9, ...totals }],
  by_hour_model: [
    { date, hour: 9, model: "model_a", requests: 5000, total_tokens: 10000 },
    { date, hour: 9, model: null, requests: 1, total_tokens: 2 },
  ],
  by_service: [{ id: null, ...totals }],
  by_model: [{ id: "model_a", ...totals }],
  by_token: [{ id: "token_a", ...totals }],
  by_client: [{ id: null, ...totals }],
  by_route: [
    {
      client_type: "codex",
      service_id: "service_b",
      failed_service_id: "service_a",
      failed: false,
      rewrites: ["model_redirect", "client_identity"],
      requests: 5000,
    },
    {
      client_type: null,
      service_id: null,
      failed_service_id: null,
      failed: true,
      rewrites: [],
      requests: 1,
    },
  ],
  scanned_records: 5001,
};

describe("usage summary contract", () => {
  it("accepts nullable service performance and validates sample coverage", () => {
    const performance = {
      cache_hit_rate: 0.26,
      output_tokens_per_second: 49,
      cache_samples: 2,
      speed_samples: 3,
    };
    const withPerformance = (value: unknown) => ({
      ...response,
      by_service: [{ ...response.by_service[0], performance: value }],
    });
    expect(
      parseUsageSummary(withPerformance(performance), window).by_service[0]
        .performance,
    ).toEqual(performance);
    expect(
      parseUsageSummary(
        withPerformance({
          cache_hit_rate: null,
          output_tokens_per_second: null,
          cache_samples: 0,
          speed_samples: 0,
        }),
        window,
      ).by_service[0].performance?.cache_hit_rate,
    ).toBeNull();
    for (const invalid of [
      { ...performance, cache_hit_rate: 1.1 },
      { ...performance, output_tokens_per_second: Infinity },
      { ...performance, cache_samples: 0 },
      { ...performance, cache_hit_rate: null },
      { ...performance, speed_samples: -1 },
    ])
      expect(() =>
        parseUsageSummary(withPerformance(invalid), window),
      ).toThrow();
  });
  it("keeps the final hour on a fall-back day without duplicate buckets", () => {
    const slots = usageWindowHourSlots({
      preset: "1d",
      from: "2026-11-01T04:00:00Z",
      to: "2026-11-02T05:00:00Z",
      time_zone: "America/New_York",
    });
    expect(slots).toHaveLength(24);
    expect(slots.filter((slot) => slot.hour === 1)).toHaveLength(1);
    expect(slots.at(-1)).toEqual({ date: "2026-11-01", hour: 23 });
  });
  it("pads sparse buckets without truncating complete totals", () => {
    const summary = parseUsageSummary(response, window);
    expect(summary.totals.total_tokens).toBe(10002);
    expect(summary.status_counts).toEqual(statusCounts);
    expect(summary.scanned_records).toBe(5001);
    expect(summary.by_token).toEqual(response.by_token);
    expect(summary.capped).toBe(false);
    expect(summary.by_hour).toHaveLength(24);
    expect(summary.by_hour[9].total_tokens).toBe(10002);
    expect(summary.by_hour[8].total_tokens).toBe(0);
    expect(summary.by_day).toEqual(response.by_day);
  });

  it("keeps hour-by-model buckets and client groups", () => {
    const summary = parseUsageSummary(response, window);
    expect(summary.by_hour_model).toEqual(response.by_hour_model);
    expect(summary.by_client).toEqual(response.by_client);
  });

  it("keeps routes and drops kinds it does not know", () => {
    expect(parseUsageSummary(response, window).by_route).toEqual(
      response.by_route,
    );
    const newer = parseUsageSummary(
      {
        ...response,
        by_route: [
          {
            ...response.by_route[0],
            client_type: "future_client",
            rewrites: ["future_rewrite", "client_identity", "privacy"],
          },
        ],
      },
      window,
    ).by_route[0];
    expect(newer.client_type).toBeNull();
    expect(newer.rewrites).toEqual(["privacy", "client_identity"]);
  });

  it.each([
    { ...response, totals: { ...totals, total_tokens: -1 } },
    {
      ...response,
      totals: { ...totals, total_tokens: Number.MAX_SAFE_INTEGER + 1 },
    },
    { ...response, scanned_records: 10 },
    { ...response, status_counts: { ...statusCounts, succeeded: 5000 } },
    { ...response, status_counts: { ...statusCounts, failed: 1 } },
    { ...response, status_counts: { ...statusCounts, blocked: -1 } },
    { ...response, status_counts: { ...statusCounts, interrupted: 0 } },
    { ...response, status_counts: undefined },
    { ...response, by_day: [{ date: "2026-09-18", ...totals }] },
    { ...response, by_hour: [{ date, hour: 24, ...totals }] },
    { ...response, by_model: [...response.by_model, ...response.by_model] },
    { ...response, by_client: [...response.by_client, ...response.by_client] },
    { ...response, by_client: undefined },
    { ...response, by_hour_model: undefined },
    { ...response, by_route: undefined },
    { ...response, by_route: [response.by_route[0], response.by_route[0]] },
    { ...response, by_route: [{ ...response.by_route[0], requests: 0 }] },
    { ...response, by_route: [{ ...response.by_route[0], failed: 1 }] },
    {
      ...response,
      by_route: [{ ...response.by_route[0], rewrites: ["privacy", "privacy"] }],
    },
    {
      ...response,
      by_hour_model: [{ ...response.by_hour_model[0], date: "2026-09-17" }],
    },
    {
      ...response,
      by_hour_model: [{ ...response.by_hour_model[0], hour: 24 }],
    },
    {
      ...response,
      by_hour_model: [{ ...response.by_hour_model[0], model: "" }],
    },
    {
      ...response,
      by_hour_model: [{ ...response.by_hour_model[0], requests: -1 }],
    },
    {
      ...response,
      by_hour_model: [response.by_hour_model[0], response.by_hour_model[0]],
    },
    { ...response, extra: true },
  ])("rejects malformed or inconsistent statistics", (value) => {
    expect(() => parseUsageSummary(value, window)).toThrow();
  });
});
