import { currentLocale, type Locale } from "./i18n";

interface CompactUnit {
  threshold: number;
  divisor: number;
  suffix: string;
}

/**
 * Compact number rendering, written by hand rather than through
 * `Intl.NumberFormat`'s `notation: "compact"`, because the locales need
 * different grouping (thousands versus myriads) and the engine's rounding of
 * `4664.6695万` is not stable across runtimes.
 */
const UNITS: Readonly<Record<Locale, readonly CompactUnit[]>> = {
  en: [
    { threshold: 1_000_000_000, divisor: 1_000_000_000, suffix: "B" },
    { threshold: 1_000_000, divisor: 1_000_000, suffix: "M" },
    { threshold: 1_000, divisor: 1_000, suffix: "K" },
  ],
  ja: [
    { threshold: 100_000_000, divisor: 100_000_000, suffix: "億" },
    { threshold: 10_000, divisor: 10_000, suffix: "万" },
  ],
  "zh-CN": [
    { threshold: 100_000_000, divisor: 100_000_000, suffix: "亿" },
    { threshold: 10_000, divisor: 10_000, suffix: "万" },
  ],
};

const MAX_FRACTION_DIGITS = 2;

/**
 * Rounds to at most two fraction digits and drops trailing zeros, so `1.00万`
 * renders as `1万`. The mantissa stays ungrouped: `4664.67万`, not `4,664.67万`.
 */
function trimFraction(value: number, digits = MAX_FRACTION_DIGITS): number {
  return Number(value.toFixed(digits));
}

/** About three significant digits: two fraction digits below 10, one below 100. */
function shortFraction(mantissa: number): number {
  const magnitude = Math.abs(mantissa);
  return magnitude < 10 ? 2 : magnitude < 100 ? 1 : 0;
}

/** Full grouped digits, used for exact tooltips beside a compact value. */
export function formatExactNumber(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return value.toLocaleString(currentLocale());
}

/**
 * Abbreviate large counts for the active language: `K`/`M`/`B` in English and
 * `万`/`亿` or `万`/`億` in Chinese and Japanese. Values below the first unit
 * keep their grouped digits.
 *
 * `short` is for tight spots: fewer fraction digits as the number grows, so
 * `1022.72万` reads `1023万` and `9.8765亿` reads `9.88亿`. A value that rounds
 * up to the next unit takes it: `1亿`, not `10000万`.
 */
export function formatCompactNumber(
  value: number,
  { short = false }: { short?: boolean } = {},
): string {
  if (!Number.isFinite(value)) return "—";
  const magnitude = Math.abs(value);
  const units = UNITS[currentLocale()];
  for (const [at, unit] of units.entries()) {
    if (magnitude < unit.threshold) continue;
    const mantissa = value / unit.divisor;
    if (!short) return `${trimFraction(mantissa)}${unit.suffix}`;
    const rounded = trimFraction(mantissa, shortFraction(mantissa));
    const larger = units[at - 1];
    if (larger && Math.abs(rounded) * unit.divisor >= larger.threshold) {
      const next = (rounded * unit.divisor) / larger.divisor;
      return `${trimFraction(next, shortFraction(next))}${larger.suffix}`;
    }
    return `${rounded}${unit.suffix}`;
  }
  return formatExactNumber(value);
}

/** A share too small to round up still reads as present. */
export function formatShare(percent: number): string {
  if (percent > 0 && percent < 1) return "<1%";
  return `${Math.round(percent)}%`;
}
