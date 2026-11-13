import { afterEach, describe, expect, it } from "vitest";

import {
  applyDisplayCurrency,
  currencyName,
  formatCurrency,
  formatRateDate,
  parseDisplayCurrency,
  USD_DISPLAY,
  type DisplayCurrency,
} from "./currency-display";
import { billingAmount, formatUSD } from "./pricing-model";
import { formatExtraUSD } from "./request-context-model";
import { formatEstimateUSD } from "./service-package-model";
import { formatUsd } from "./tray-model";

const cny: DisplayCurrency = {
  code: "CNY",
  symbol: "¥",
  digits: 2,
  per_usd: 6.65,
  rate_date: "2026-11-01",
  wanted: "CNY",
  system: "CNY",
};
const jpy: DisplayCurrency = {
  ...cny,
  code: "JPY",
  digits: 0,
  per_usd: 157,
  wanted: "JPY",
};

afterEach(() => applyDisplayCurrency(USD_DISPLAY));

describe("display currency", () => {
  it("reads the host payload and rejects anything else", () => {
    expect(parseDisplayCurrency({ ...cny })).toEqual(cny);
    for (const bad of [
      null,
      { ...cny, code: "THB" },
      { ...cny, per_usd: 0 },
      { ...cny, per_usd: Number.NaN },
      { ...cny, digits: 1.5 },
      { ...cny, rate_date: 20261101 },
      { ...cny, wanted: undefined },
    ])
      expect(() => parseDisplayCurrency(bad)).toThrow(
        "Invalid display currency",
      );
  });

  it("keeps dollars exactly as before by default", () => {
    expect(formatUSD("18.420000000")).toBe("$18.42");
    expect(formatUSD("12345.67", { short: true })).toBe("$12,346");
    expect(formatExtraUSD("0.004")).toBe("< $0.01");
    expect(formatEstimateUSD(4.5)).toBe("$4.50");
    expect(formatEstimateUSD(120.4)).toBe("$120");
    expect(formatUsd(0.834)).toBe("$0.83");
  });

  it("converts every estimate formatter to the chosen currency", () => {
    applyDisplayCurrency(cny);
    expect(formatUSD("1")).toBe("¥6.65");
    expect(formatUSD("200", { short: true })).toBe("¥1,330");
    expect(formatUSD(undefined)).toBe("—");
    expect(
      billingAmount({ amount_usd: "2", priced: 1, unpriced: 0, pending: 0 }),
    ).toBe("¥13.30");
    expect(formatExtraUSD("0.0005")).toBe("< ¥0.01");
    expect(formatExtraUSD("0.01")).toBe("¥0.07");
    // Whole yuan from ¥10 up, as dollars were from $10.
    expect(formatEstimateUSD(1)).toBe("¥6.65");
    expect(formatEstimateUSD(100)).toBe("¥665");
    expect(formatUsd(0)).toBe("¥0.00");
    expect(formatUsd(0.001)).toBe("<¥0.01");
    expect(formatUsd(0.834)).toBe("¥5.55");
  });

  it("drops decimals for currencies without minor units", () => {
    applyDisplayCurrency(jpy);
    expect(formatUSD("0.834")).toBe("¥131");
    expect(formatExtraUSD("0.002")).toBe("< ¥1");
    expect(formatUsd(0.004)).toBe("<¥1");
    expect(formatCurrency(-3, 2, cny)).toBe("-¥3.00");
  });

  it("names currencies and rate days in the interface language", () => {
    expect(currencyName("CNY", "zh-CN")).toBe("人民币");
    expect(currencyName("USD", "en")).toBe("US Dollar");
    expect(formatRateDate("2026-11-01", "zh-CN")).toBe("11月1日");
    expect(formatRateDate("2026-11-01", "en")).toBe("Nov 1");
    expect(formatRateDate("soon", "en")).toBe("soon");
  });
});
