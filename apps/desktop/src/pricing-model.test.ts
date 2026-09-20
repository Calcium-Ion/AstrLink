import { describe, expect, it } from "vitest";
import {
  parseBillingSummary,
  parsePricingConfig,
  billingAmount,
  currentBillingPeriod,
  formatUSD,
  type BillingPeriod,
} from "./pricing-model";
const config = {
  provider: "moonshotai",
  bindings: {
    "kimi-for-coding": { provider: "moonshotai", model: "kimi-k2.7-code" },
  },
  monthly_budget_usd: "50",
  billing_day: 31,
  time_zone: "Asia/Shanghai",
};
describe("official pricing boundary", () => {
  it("preserves money as decimal strings and accepts only canonical suppliers", () => {
    expect(parsePricingConfig(config)).toEqual(config);
    for (const provider of [
      "zai-coding-plan",
      "minimax-coding-plan",
      "moonshotai-token-plan",
      "__proto__",
    ]) {
      expect(() => parsePricingConfig({ ...config, provider })).toThrow();
      expect(() =>
        parsePricingConfig({
          ...config,
          bindings: { model: { provider, model: "glm-5.3" } },
        }),
      ).toThrow();
    }
    expect(formatUSD("18.420000000")).toBe("$18.42");
  });
  it("does not accept non-finite money, counts, or fabricated plan prices", () => {
    const row = {
      amount_usd: "0.000000001",
      priced: 1,
      unpriced: 2,
      pending: 0,
      revalued: 0,
      requests: 3,
    };
    const value = {
      ...row,
      from: "2026-09-19T00:00:00Z",
      to: "2026-09-20T00:00:00Z",
      by_model: [],
    };
    expect(parseBillingSummary(value).amount_usd).toBe("0.000000001");
    expect(() =>
      parseBillingSummary({ ...value, amount_usd: "NaN" }),
    ).toThrow();
    expect(() => parseBillingSummary({ ...value, unpriced: -1 })).toThrow();
  });
  it("keeps missing prices distinct from free calls and prefers the official cycle", () => {
    const amounts = {
      amount_usd: "0",
      priced: 0,
      unpriced: 1,
      pending: 0,
      revalued: 0,
      requests: 1,
    };
    expect(billingAmount(amounts)).toBe("—");
    expect(billingAmount({ ...amounts, unpriced: 0 })).toBe("$0.00");
    const month = {
      id: "month",
      kind: "month",
      start: "2026-09-01T00:00:00Z",
      end: "2026-10-01T00:00:00Z",
    } as BillingPeriod;
    const primary = {
      ...month,
      id: "quota",
      kind: "primary",
      start: "2026-09-19T00:00:00Z",
      end: "2026-09-19T05:00:00Z",
    };
    const periods = [month, primary];
    expect(
      currentBillingPeriod(periods, Date.parse("2026-09-19T04:00:00Z")),
    ).toBe(primary);
    expect(currentBillingPeriod(periods, Date.parse(primary.end))).toBe(month);
  });
});
