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

describe("formatCompactNumber in Japanese", () => {
  it("abbreviates myriads and hundred-millions", async () => {
    await applyLocale("ja");

    expect(formatCompactNumber(1_234)).toBe("1,234");
    expect(formatCompactNumber(10_000)).toBe("1万");
    expect(formatCompactNumber(12_345)).toBe("1.23万");
    expect(formatCompactNumber(100_000_000)).toBe("1億");
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

describe("formatCompactNumber, short", () => {
  const short = (value: number) => formatCompactNumber(value, { short: true });

  it("drops a decimal for each digit the mantissa grows", async () => {
    await applyLocale("zh-CN");

    expect(short(9_999)).toBe("9,999");
    expect(short(12_345)).toBe("1.23万");
    expect(short(10_000)).toBe("1万");
    expect(short(248_000)).toBe("24.8万");
    expect(short(250_000)).toBe("25万");
    expect(short(9_980_000)).toBe("998万");
    expect(short(10_227_200)).toBe("1023万");
    expect(short(1_234_567)).toBe("123万");
    expect(short(102_272_000)).toBe("1.02亿");
    expect(short(987_000_000)).toBe("9.87亿");
    expect(short(1_230_000_000)).toBe("12.3亿");
    expect(short(123_400_000_000)).toBe("1234亿");
    expect(short(-10_227_200)).toBe("-1023万");
    // The default keeps two decimals for pages that have the room.
    expect(formatCompactNumber(10_227_200)).toBe("1022.72万");
  });

  it("takes the next unit when rounding reaches it", async () => {
    await applyLocale("zh-CN");
    expect(short(99_995_000)).toBe("1亿");

    await applyLocale("en");
    expect(short(999_950)).toBe("1M");
    expect(short(999_999_999)).toBe("1B");
  });

  it("uses thousands, millions and billions in English", async () => {
    await applyLocale("en");

    expect(short(1_234)).toBe("1.23K");
    expect(short(24_800)).toBe("24.8K");
    expect(short(998_000)).toBe("998K");
    expect(short(1_020_000)).toBe("1.02M");
    expect(short(1_234_567)).toBe("1.23M");
    expect(short(1_230_000_000)).toBe("1.23B");
    expect(short(98_700_000_000)).toBe("98.7B");
  });

  it("uses 億 in Japanese", async () => {
    await applyLocale("ja");

    expect(short(987_000_000)).toBe("9.87億");
    expect(short(10_227_200)).toBe("1023万");
  });
});
