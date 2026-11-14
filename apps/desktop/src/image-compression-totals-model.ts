/**
 * What request image compression has done since counting began. Mirrors
 * `ImageCompressionTotals` in `core/internal/storage/image_compression_totals.go`:
 * a request counts once however many attempts it took, sizes are decoded
 * image bytes, and an image sent as it was adds the same size to both sums.
 */
export interface ImageCompressionTotals {
  /** When counting began, as an RFC 3339 timestamp. */
  since: string;
  /** Requests that went upstream with at least one inline image. */
  requests: number;
  images: number;
  /** Images sent as a smaller WebP. */
  compressed: number;
  original_bytes: number;
  sent_bytes: number;
}

const countKeys = [
  "requests",
  "images",
  "compressed",
  "original_bytes",
  "sent_bytes",
] as const;

function count(value: unknown): number {
  if (!Number.isSafeInteger(value) || (value as number) < 0) {
    throw new Error("Invalid image compression totals count");
  }
  return value as number;
}

/** Validates Core's totals; throws on a shape or count Core never sends. */
export function parseImageCompressionTotals(
  value: unknown,
): ImageCompressionTotals {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Invalid image compression totals");
  }
  const object = value as Record<string, unknown>;
  const keys = ["since", ...countKeys];
  if (
    Object.keys(object).length !== keys.length ||
    keys.some((key) => !Object.hasOwn(object, key))
  ) {
    throw new Error("Invalid image compression totals fields");
  }
  if (
    typeof object.since !== "string" ||
    Number.isNaN(Date.parse(object.since))
  ) {
    throw new Error("Invalid image compression totals start");
  }
  const totals = {
    since: object.since,
    ...Object.fromEntries(countKeys.map((key) => [key, count(object[key])])),
  } as ImageCompressionTotals;
  if (
    totals.compressed > totals.images ||
    totals.sent_bytes > totals.original_bytes
  ) {
    throw new Error("Inconsistent image compression totals");
  }
  return totals;
}

/** Bytes compression kept from going upstream. */
export function imageCompressionSavedBytes(
  totals: ImageCompressionTotals,
): number {
  return totals.original_bytes - totals.sent_bytes;
}

/**
 * The saved share of the original bytes in whole percent, "<1" for a saving
 * that rounds to zero; null when nothing was saved.
 */
export function imageCompressionSavedShare(
  totals: ImageCompressionTotals,
): string | null {
  const saved = imageCompressionSavedBytes(totals);
  if (saved <= 0 || totals.original_bytes <= 0) return null;
  const percent = Math.round((saved / totals.original_bytes) * 100);
  return percent < 1 ? "<1" : String(percent);
}
