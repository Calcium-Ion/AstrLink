import { afterEach, describe, expect, it } from "vitest";

import { formatClockTime, formatDateTime, formatMoment } from "./format-time";
import { applyLocale } from "./i18n";

afterEach(async () => {
  await applyLocale("zh-CN");
});

describe("formatClockTime", () => {
  it("reads the 24-hour clock in every language", async () => {
    const afternoon = new Date(2026, 9, 24, 16, 4, 38);
    for (const locale of ["zh-CN", "en", "ja"] as const) {
      await applyLocale(locale);
      expect(formatClockTime(afternoon)).toBe("16:04:38");
      expect(formatClockTime(afternoon.toISOString())).toBe("16:04:38");
      expect(formatDateTime(afternoon)).toContain("16:04:38");
    }
  });

  it("gives a moment of another day its day", async () => {
    await applyLocale("en");
    const now = new Date(2026, 9, 25, 9, 0, 0);
    expect(formatMoment(new Date(2026, 9, 25, 8, 30, 5), now)).toBe("08:30:05");
    expect(formatMoment(new Date(2026, 9, 24, 16, 4, 38), now)).toBe(
      "10/24, 16:04:38",
    );
  });

  it("keeps what is not a time as it is", () => {
    expect(formatClockTime("soon")).toBe("soon");
  });
});
