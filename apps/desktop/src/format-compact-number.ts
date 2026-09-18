import { i18n } from "./i18n";

/**
 * Compact number rendering, written by hand rather than through
 * `Intl.NumberFormat`'s `notation: "compact"`, because the two locales need
 * different grouping (thousands versus myriads) and the engine's rounding of
 * `4664.6695万` is not stable across runtimes.
 */
const EN_UNITS = [
  { threshold: 1_000_000_000, divisor: 1_000_000_000, suffix: "B" },
  { threshold: 1_000_000, divisor: 1_000_000, suffix: "M" },
  { threshold: 1_000, divisor: 1_000, suffix: "K" },
] as const;

const ZH_UNITS = [
  { threshold: 100_000_000, divisor: 100_000_000, suffix: "亿" },
  { threshold: 10_000, divisor: 10_000, suffix: "万" },
] as const;

const MAX_FRACTION_DIGITS = 2;

function numberLocale(): string {
  return i18n.language === "zh-CN" ? "zh-CN" : "en";
}

function isMyriadLocale(): boolean {
  return i18n.language === "zh-CN";
}

/**
 * Rounds to at most two fraction digits and drops trailing zeros, so `1.00万`
 * renders as `1万`. The mantissa stays ungrouped: `4664.67万`, not `4,664.67万`.
 */
function trimFraction(value: number): string {
  return `${Number(value.toFixed(MAX_FRACTION_DIGITS))}`;
}

/** Full grouped digits, used for exact tooltips beside a compact value. */
export function formatExactNumber(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return value.toLocaleString(numberLocale());
}

/**
 * Abbreviate large counts for the active language: `K`/`M`/`B` in English and
 * `万`/`亿` in Chinese. Values below the first unit keep their grouped digits.
 */
export function formatCompactNumber(value: number): string {
  if (!Number.isFinite(value)) return "—";
  const magnitude = Math.abs(value);
  const units = isMyriadLocale() ? ZH_UNITS : EN_UNITS;
  for (const unit of units) {
    if (magnitude < unit.threshold) continue;
    return `${trimFraction(value / unit.divisor)}${unit.suffix}`;
  }
  return formatExactNumber(value);
}
