import { afterEach, describe, expect, it } from "vitest";

import {
  formatCompactNumber,
  formatExactNumber,
} from "./format-compact-number";
import { applyLocale } from "./i18n";

afterEach(async () => {
  await applyLocale("zh-CN");
});

describe("formatCompactNumber in English", () => {
  it("abbreviates thousands, millions and billions", async () => {
    await applyLocale("en");

    expect(formatCompactNumber(0)).toBe("0");
    expect(formatCompactNumber(362)).toBe("362");
    expect(formatCompactNumber(999)).toBe("999");
    expect(formatCompactNumber(1_000)).toBe("1K");
    expect(formatCompactNumber(1_234)).toBe("1.23K");
    expect(formatCompactNumber(999_999)).toBe("1000K");
    expect(formatCompactNumber(1_000_000)).toBe("1M");
    expect(formatCompactNumber(46_646_695)).toBe("46.65M");
    expect(formatCompactNumber(1_000_000_000)).toBe("1B");
    expect(formatCompactNumber(2_500_000_000)).toBe("2.5B");
  });

  it("keeps grouped digits below the first unit", async () => {
    await applyLocale("en");

    expect(formatCompactNumber(-999)).toBe("-999");
    expect(formatExactNumber(46_646_695)).toBe("46,646,695");
  });

  it("abbreviates negative magnitudes too", async () => {
    await applyLocale("en");

    expect(formatCompactNumber(-46_646_695)).toBe("-46.65M");
  });
});

describe("formatCompactNumber in Chinese", () => {
  it("abbreviates myriads and hundred-millions", () => {
    expect(formatCompactNumber(0)).toBe("0");
    expect(formatCompactNumber(362)).toBe("362");
    expect(formatCompactNumber(1_234)).toBe("1,234");
    expect(formatCompactNumber(9_999)).toBe("9,999");
    expect(formatCompactNumber(10_000)).toBe("1万");
    expect(formatCompactNumber(12_345)).toBe("1.23万");
    expect(formatCompactNumber(46_646_695)).toBe("4664.67万");
    expect(formatCompactNumber(99_999_999)).toBe("10000万");
    expect(formatCompactNumber(100_000_000)).toBe("1亿");
    expect(formatCompactNumber(150_000_000)).toBe("1.5亿");
  });

  it("keeps exact rendering grouped", () => {
    expect(formatExactNumber(46_646_695)).toBe("46,646,695");
  });
});

describe("formatCompactNumber guards", () => {
  it("renders an em dash for non-finite input", () => {
    expect(formatCompactNumber(Number.NaN)).toBe("—");
    expect(formatCompactNumber(Number.POSITIVE_INFINITY)).toBe("—");
    expect(formatExactNumber(Number.NaN)).toBe("—");
  });
});
