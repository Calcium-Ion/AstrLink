import { describe, expect, it } from "vitest";
import {
  parseBillingSummary,
  parseServiceBilling,
  parsePricingConfig,
  billingAmount,
  currentBillingPeriod,
  formatUSD,
  resolveCatalogPrice,
  catalogRateRows,
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
it("shows the same unique catalog binding used for billing and preserves tier prices", () => {
  const price = {
    provider: "openai",
    model: "gpt-6-astra",
    name: "GPT-6 Astra",
    expression:
      'len <= 272000 ? tier("0_272k", p * 10 + cr * 1 + cc * 12.5 + c * 50) : tier("272k_plus", p * 20 + cr * 2 + cc * 25 + c * 75)',
  };
  expect(
    resolveCatalogPrice(
      { ...config, provider: "", bindings: {} },
      price.model,
      [price],
    ),
  ).toBe(price);
  expect(
    resolveCatalogPrice(
      { ...config, provider: "", bindings: {} },
      "deepseek-v4.1-flash",
      [price],
    ),
  ).toBeUndefined();
  expect(
    resolveCatalogPrice(
      { ...config, provider: "", bindings: {} },
      price.model,
      [price, { ...price, provider: "other" }],
    ),
  ).toBeUndefined();
  expect(catalogRateRows(price.expression).map((row) => row.rates)).toEqual([
    { input: "10", output: "50", cache_read: "1", cache_write: "12.5" },
    { input: "20", output: "75", cache_read: "2", cache_write: "25" },
  ]);
  expect(catalogRateRows('hour("UTC") < 8 ? p * 1 : p * 2')).toEqual([]);
  expect(catalogRateRows("p * 2 + c * 8")[0].rates).toEqual({
    input: "2",
    output: "8",
    cache_read: "2",
    cache_write: "2",
  });
  expect(
    catalogRateRows(
      'len <= 100 ? tier("low", p * 2 + c * 8) : tier("high", p * 4 + cr * 1 + c * 16)',
    )[0].rates.cache_read,
  ).toBe("0");
});
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
      by_token: [],
    };
    expect(parseBillingSummary(value).amount_usd).toBe("0.000000001");
    expect(parseBillingSummary(value).by_token).toEqual([]);
    expect(() =>
      parseBillingSummary({ ...value, amount_usd: "NaN" }),
    ).toThrow();
    expect(() => parseBillingSummary({ ...value, unpriced: -1 })).toThrow();
  });

  it("requires the token billing breakdown field", () => {
    expect(() =>
      parseBillingSummary({
        amount_usd: "0",
        priced: 0,
        unpriced: 0,
        pending: 0,
        revalued: 0,
        requests: 0,
        from: "2026-09-19T00:00:00Z",
        to: "2026-09-20T00:00:00Z",
        by_model: [],
      }),
    ).toThrow("Invalid pricing list");
  });
  it("requires an array for every service-period token breakdown", () => {
    const summary = {
      amount_usd: "0",
      priced: 0,
      unpriced: 0,
      pending: 0,
      revalued: 0,
      requests: 0,
      from: "2026-09-19T00:00:00Z",
      to: "2026-09-20T00:00:00Z",
      by_model: [],
      by_token: [],
    };
    const period = {
      id: "month",
      kind: "month",
      start: summary.from,
      end: summary.to,
      observed_at: null,
      used_percent: null,
      budget_usd: "",
      remaining_usd: "",
      coverage: "complete",
      summary,
    };
    expect(
      parseServiceBilling({ config, periods: [period] }).periods[0].summary
        .by_token,
    ).toEqual([]);
    for (const by_token of [undefined, null, {}]) {
      const invalid = { ...summary, by_token };
      expect(() => parseBillingSummary(invalid)).toThrow(
        "Invalid pricing list",
      );
      expect(() =>
        parseServiceBilling({
          config,
          periods: [{ ...period, summary: invalid }],
        }),
      ).toThrow("Invalid pricing list");
    }
  });
  it("parses token billing groups with the stable token_id field", () => {
    const value = {
      amount_usd: "1.25",
      priced: 1,
      unpriced: 0,
      pending: 0,
      revalued: 0,
      requests: 1,
      from: "2026-09-19T00:00:00Z",
      to: "2026-09-20T00:00:00Z",
      by_model: [],
      by_token: [
        {
          token_id: "token_a",
          amount_usd: "1.25",
          priced: 1,
          unpriced: 0,
          pending: 0,
          revalued: 0,
          requests: 1,
        },
      ],
    };
    expect(parseBillingSummary(value).by_token).toEqual(value.by_token);
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
