import { describe, expect, it } from "vitest";
import {
  parseBillingSummary,
  parseQuotaEstimates,
  parseServiceBilling,
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
    // Short amounts drop the cents from $1,000 up.
    expect(formatUSD("999.99", { short: true })).toBe("$999.99");
    expect(formatUSD("1000", { short: true })).toBe("$1,000");
    expect(formatUSD("12345.67", { short: true })).toBe("$12,346");
    expect(formatUSD("12345.67")).toBe("$12,345.67");
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
      by_service: [],
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
        by_service: [],
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
      by_service: [],
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
    for (const by_service of [undefined, null, {}]) {
      expect(() => parseBillingSummary({ ...summary, by_service })).toThrow(
        "Invalid pricing list",
      );
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
      by_service: [
        {
          service_id: "service_a",
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
    expect(parseBillingSummary(value).by_service).toEqual(value.by_service);
    expect(() =>
      parseBillingSummary({
        ...value,
        by_service: [{ ...value.by_service[0], service_id: 1 }],
      }),
    ).toThrow();
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
describe("quota estimates", () => {
  it("reads each service's estimated windows and rejects unknown ones", () => {
    const estimates = {
      services: [
        {
          service_id: "service_codex",
          windows: [{ kind: "primary", total_usd: "40.00" }],
        },
      ],
    };
    expect(parseQuotaEstimates(estimates)).toEqual(estimates.services);
    expect(() =>
      parseQuotaEstimates({
        services: [
          {
            service_id: "service_codex",
            windows: [{ kind: "spark/primary", total_usd: "40.00" }],
          },
        ],
      }),
    ).toThrow("Invalid quota window");
    expect(() =>
      parseQuotaEstimates({
        services: [
          {
            service_id: "service_codex",
            windows: [{ kind: "primary", total_usd: "about 40" }],
          },
        ],
      }),
    ).toThrow("Invalid billing amount");
  });
});
