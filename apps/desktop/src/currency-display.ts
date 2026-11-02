import { useSyncExternalStore } from "react";

/** Mirrors `CURRENCIES` in `src-tauri/src/currency.rs`. */
export const DISPLAY_CURRENCIES = [
  "USD",
  "CNY",
  "HKD",
  "TWD",
  "JPY",
  "KRW",
  "SGD",
  "EUR",
  "GBP",
  "CHF",
  "CAD",
  "AUD",
  "INR",
  "RUB",
  "BRL",
] as const;
export type DisplayCurrencyCode = (typeof DISPLAY_CURRENCIES)[number];
/** The setting: `auto` follows the system region. */
export type DisplayCurrencyChoice = "auto" | DisplayCurrencyCode;
export const DISPLAY_CURRENCY_EVENT = "display-currency-changed";

export function isDisplayCurrencyCode(
  value: unknown,
): value is DisplayCurrencyCode {
  return (DISPLAY_CURRENCIES as readonly unknown[]).includes(value);
}

export function isDisplayCurrencyChoice(
  value: unknown,
): value is DisplayCurrencyChoice {
  return value === "auto" || isDisplayCurrencyCode(value);
}

/**
 * What amounts estimated at official API prices are shown in. Core and the
 * bridge keep US dollars; only the text the operator reads converts.
 */
export interface DisplayCurrency {
  /** USD until a rate for `wanted` is known. */
  code: DisplayCurrencyCode;
  symbol: string;
  digits: number;
  /** Units of `code` per US dollar. */
  per_usd: number;
  /** The day the rate was published; null for USD. */
  rate_date: string | null;
  /** The currency the setting resolves to. */
  wanted: DisplayCurrencyCode;
  /** What `auto` resolves to on this system. */
  system: DisplayCurrencyCode;
}

export const USD_DISPLAY: DisplayCurrency = {
  code: "USD",
  symbol: "$",
  digits: 2,
  per_usd: 1,
  rate_date: null,
  wanted: "USD",
  system: "USD",
};

export function parseDisplayCurrency(value: unknown): DisplayCurrency {
  const v = value as Partial<Record<keyof DisplayCurrency, unknown>> | null;
  if (
    !v ||
    typeof v !== "object" ||
    !isDisplayCurrencyCode(v.code) ||
    typeof v.symbol !== "string" ||
    typeof v.digits !== "number" ||
    !Number.isInteger(v.digits) ||
    v.digits < 0 ||
    v.digits > 4 ||
    typeof v.per_usd !== "number" ||
    !Number.isFinite(v.per_usd) ||
    v.per_usd <= 0 ||
    (v.rate_date !== null && typeof v.rate_date !== "string") ||
    !isDisplayCurrencyCode(v.wanted) ||
    !isDisplayCurrencyCode(v.system)
  )
    throw new Error("Invalid display currency");
  return {
    code: v.code,
    symbol: v.symbol,
    digits: v.digits,
    per_usd: v.per_usd,
    rate_date: v.rate_date,
    wanted: v.wanted,
    system: v.system,
  };
}

let current = USD_DISPLAY;
const listeners = new Set<() => void>();

export function applyDisplayCurrency(next: DisplayCurrency): void {
  if (
    current.code === next.code &&
    current.per_usd === next.per_usd &&
    current.rate_date === next.rate_date &&
    current.wanted === next.wanted &&
    current.system === next.system
  )
    return;
  current = next;
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/**
 * Re-renders the caller when the currency or its rate changes. Components
 * that print amounts call it even when the formatters read the value.
 */
export function useDisplayCurrency(): DisplayCurrency {
  return useSyncExternalStore(
    subscribe,
    () => current,
    () => USD_DISPLAY,
  );
}

export function displayCurrency(): DisplayCurrency {
  return current;
}

/** A USD amount in the display currency, before rounding. */
export function convertUSD(amountUSD: number, currency = current): number {
  return amountUSD * currency.per_usd;
}

/** A converted amount with its symbol, grouped like the rest of the app. */
export function formatCurrency(
  amount: number,
  digits: number,
  currency = current,
): string {
  const number = new Intl.NumberFormat("en-US", {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(Math.abs(amount));
  return `${amount < 0 ? "-" : ""}${currency.symbol}${number}`;
}

/** The smallest amount `currency` shows: 0.01, or 1 for yen and won. */
export function smallestUnit(currency = current): number {
  return 10 ** -currency.digits;
}

/** The currency's name in `locale`, such as 人民币 or Japanese Yen. */
export function currencyName(
  code: DisplayCurrencyCode,
  locale: string,
): string {
  try {
    return new Intl.DisplayNames(locale, { type: "currency" }).of(code) ?? code;
  } catch {
    return code;
  }
}

/** The day a rate was published (`YYYY-MM-DD`), as `11月1日` or `Nov 1`. */
export function formatRateDate(date: string, locale: string): string {
  const [year, month, day] = date.split("-").map(Number);
  if (!year || !month || !day) return date;
  return new Intl.DateTimeFormat(locale, {
    month: "short",
    day: "numeric",
  }).format(new Date(year, month - 1, day));
}
