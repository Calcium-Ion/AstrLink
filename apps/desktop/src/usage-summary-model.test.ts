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
const response = {
  totals,
  by_day: [{ date, ...totals }],
  by_hour: [{ date, hour: 9, ...totals }],
  by_service: [{ id: null, ...totals }],
  by_model: [{ id: "model_a", ...totals }],
  by_token: [{ id: "token_a", ...totals }],
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
    expect(summary.scanned_records).toBe(5001);
    expect(summary.by_token).toEqual(response.by_token);
    expect(summary.capped).toBe(false);
    expect(summary.by_hour).toHaveLength(24);
    expect(summary.by_hour[9].total_tokens).toBe(10002);
    expect(summary.by_hour[8].total_tokens).toBe(0);
    expect(summary.by_day).toEqual(response.by_day);
  });

  it.each([
    { ...response, totals: { ...totals, total_tokens: -1 } },
    {
      ...response,
      totals: { ...totals, total_tokens: Number.MAX_SAFE_INTEGER + 1 },
    },
    { ...response, scanned_records: 10 },
    { ...response, by_day: [{ date: "2026-09-18", ...totals }] },
    { ...response, by_hour: [{ date, hour: 24, ...totals }] },
    { ...response, by_model: [...response.by_model, ...response.by_model] },
    { ...response, extra: true },
  ])("rejects malformed or inconsistent statistics", (value) => {
    expect(() => parseUsageSummary(value, window)).toThrow();
  });
});
