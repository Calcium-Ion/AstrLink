import { describe, expect, it } from "vitest";

import {
  imageCompressionSavedBytes,
  imageCompressionSavedShare,
  parseImageCompressionTotals,
} from "./image-compression-totals-model";
import { formatImageBytes } from "./request-trajectory-model";

const totals = {
  since: "2026-11-12T08:00:00Z",
  requests: 2,
  images: 3,
  compressed: 1,
  original_bytes: 10_000,
  sent_bytes: 4_000,
};

describe("image compression totals", () => {
  it("parses Core's totals and works out the saving", () => {
    const parsed = parseImageCompressionTotals(totals);
    expect(parsed).toEqual(totals);
    expect(imageCompressionSavedBytes(parsed)).toBe(6_000);
    expect(imageCompressionSavedShare(parsed)).toBe("60");
    expect(imageCompressionSavedShare({ ...parsed, sent_bytes: 9_990 })).toBe(
      "<1",
    );
    // Images sent as they were save nothing.
    expect(
      imageCompressionSavedShare({ ...parsed, sent_bytes: 10_000 }),
    ).toBeNull();
    expect(
      imageCompressionSavedShare({
        ...parsed,
        original_bytes: 0,
        sent_bytes: 0,
      }),
    ).toBeNull();
  });

  it("rejects totals Core never sends", () => {
    for (const value of [
      null,
      [],
      { ...totals, extra: 1 },
      { ...totals, since: "yesterday" },
      { ...totals, images: -1 },
      { ...totals, requests: 1.5 },
      { ...totals, compressed: 4 },
      { ...totals, sent_bytes: 10_001 },
    ])
      expect(() => parseImageCompressionTotals(value)).toThrow();
    const { since: _since, ...missing } = totals;
    expect(() => parseImageCompressionTotals(missing)).toThrow();
  });

  it("formats large totals in GB", () => {
    expect(formatImageBytes(900)).toBe("900 B");
    expect(formatImageBytes(1536)).toBe("1.5 KB");
    expect(formatImageBytes(5 * 1024 * 1024)).toBe("5.0 MB");
    expect(formatImageBytes(3 * 1024 ** 3)).toBe("3.00 GB");
  });
});
