export interface PriceBinding {
  provider: string;
  model: string;
}
export interface PricingConfig {
  overrides?: Record<string, ModelRates>;
  provider: string;
  bindings: Record<string, PriceBinding>;
  monthly_budget_usd: string;
  billing_day: number;
  time_zone: string;
}
export interface ModelRates {
  input: string;
  output: string;
  cache_read: string;
  cache_write: string;
}
export interface CatalogPrice {
  provider: string;
  model: string;
  name: string;
  expression: string;
}
export function resolveCatalogPrice(
  config: PricingConfig,
  model: string,
  prices: CatalogPrice[],
) {
  const binding = config.bindings[model] ?? {
    provider: config.provider,
    model,
  };
  const matches = prices.filter(
    (price) =>
      price.model === binding.model &&
      (!binding.provider || price.provider === binding.provider),
  );
  return matches.length === 1 ? matches[0] : undefined;
}
// Display only recognized token-rate formulas; other catalog rules remain
// visible as their exact expression, never guessed as a flat token price.
export function catalogRateRows(
  expression: string,
): { label: string; rates: ModelRates }[] {
  const linear = (source: string): ModelRates | undefined => {
    const rates: ModelRates = {
      input: "0",
      output: "0",
      cache_read: "0",
      cache_write: "0",
    };
    const keys = {
      p: "input",
      c: "output",
      cr: "cache_read",
      cc: "cache_write",
    } as const;
    const seen = new Set<string>();
    for (const term of source.split("+")) {
      const match = term.trim().match(/^(p|c|cr|cc)\s*\*\s*(\d+(?:\.\d+)?)$/);
      if (!match || seen.has(match[1])) return undefined;
      seen.add(match[1]);
      rates[keys[match[1] as keyof typeof keys]] = match[2];
    }
    // Core keeps cache tokens in p unless the formula prices them separately.
    if (!/\bcr\b/.test(expression)) rates.cache_read = rates.input;
    if (!/\bcc\b/.test(expression)) rates.cache_write = rates.input;
    return rates;
  };
  const tiered = expression.match(
    /^len\s*<=\s*(\d+)\s*\?\s*tier\("[^"]+",\s*([^()]+)\)\s*:\s*tier\("[^"]+",\s*([^()]+)\)$/,
  );
  if (tiered) {
    const low = linear(tiered[2]),
      high = linear(tiered[3]);
    if (low && high)
      return [
        {
          label: `输入 ≤ ${Number(tiered[1]).toLocaleString()} Token`,
          rates: low,
        },
        {
          label: `输入 > ${Number(tiered[1]).toLocaleString()} Token`,
          rates: high,
        },
      ];
    return [];
  }
  const simple = expression.match(/^tier\("[^"]+",\s*([^()]+)\)$/);
  const rates = linear(simple?.[1] ?? expression);
  return rates ? [{ label: "标准价格", rates }] : [];
}
export interface BillingAmounts {
  amount_usd: string;
  priced: number;
  unpriced: number;
  pending: number;
  revalued: number;
  requests: number;
}
export interface BillingGroup extends BillingAmounts {
  model: string;
  provider: string;
}
export interface TokenGroup extends BillingAmounts {
  token_id: string;
}
export interface BillingSummary extends BillingAmounts {
  from: string;
  to: string;
  by_model: BillingGroup[];
  by_token: TokenGroup[];
}
export interface BillingPeriod {
  id: string;
  kind: string;
  start: string;
  end: string;
  observed_at: string | null;
  used_percent: number | null;
  budget_usd: string;
  remaining_usd: string;
  coverage: string;
  summary: BillingSummary;
}
export interface ServiceBilling {
  config: PricingConfig;
  periods: BillingPeriod[];
}

export const OFFICIAL_PROVIDERS: Record<string, string> = {
  openai: "OpenAI",
  anthropic: "Anthropic",
  moonshotai: "Moonshot AI",
  zai: "Z.AI",
  minimax: "MiniMax",
  google: "Google",
  deepseek: "DeepSeek",
  alibaba: "Alibaba",
  xai: "xAI",
  mistral: "Mistral",
  cohere: "Cohere",
  meta: "Meta",
  perplexity: "Perplexity",
  stepfun: "StepFun",
  inception: "Inception",
  longcat: "LongCat",
  bailing: "Bailing",
  morph: "Morph",
  nvidia: "NVIDIA",
};
// Monetary fields stay decimal strings at the bridge boundary. Number is only
// used for display and progress bars; Core owns all accounting arithmetic.
export function formatUSD(value: string | undefined): string {
  if (value == null) return "—";
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  }).format(Number(value));
}
export function billingAmount(amounts: BillingAmounts | undefined): string {
  if (
    !amounts ||
    (amounts.priced === 0 && amounts.unpriced + amounts.pending > 0)
  )
    return "—";
  return formatUSD(amounts.amount_usd);
}
export function currentBillingPeriod(
  periods: BillingPeriod[],
  now = Date.now(),
): BillingPeriod | undefined {
  const current = periods.filter(
    (p) => Date.parse(p.start) <= now && now < Date.parse(p.end),
  );
  return (
    current.find((p) => p.kind === "primary") ??
    current.find((p) => p.kind === "month")
  );
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid pricing response");
  return value as Record<string, unknown>;
}
function str(value: unknown): string {
  if (typeof value !== "string") throw new Error("Invalid pricing text");
  return value;
}
function timestamp(value: unknown): string {
  const s = str(value);
  if (!Number.isFinite(Date.parse(s)))
    throw new Error("Invalid pricing timestamp");
  return s;
}
function count(value: unknown): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    throw new Error("Invalid billing count");
  return value;
}
function decimal(value: unknown, empty = false): string {
  const s = str(value);
  if (!(empty && s === "") && !/^-?\d+(\.\d{1,9})?$/.test(s))
    throw new Error("Invalid billing amount");
  return s;
}
function array<T>(value: unknown, read: (v: unknown) => T): T[] {
  if (!Array.isArray(value)) throw new Error("Invalid pricing list");
  return value.map(read);
}
function amounts(value: unknown): BillingAmounts {
  const o = object(value);
  return {
    amount_usd: decimal(o.amount_usd),
    priced: count(o.priced),
    unpriced: count(o.unpriced),
    pending: count(o.pending),
    revalued: count(o.revalued),
    requests: count(o.requests),
  };
}
export function parseBillingSummary(value: unknown): BillingSummary {
  const o = object(value);
  return {
    ...amounts(o),
    from: timestamp(o.from),
    to: timestamp(o.to),
    by_model: array(o.by_model, (v) => {
      const row = object(v);
      return {
        ...amounts(row),
        provider: str(row.provider),
        model: str(row.model),
      };
    }),
    by_token: array(o.by_token, (v) => {
      const row = object(v);
      return {
        ...amounts(row),
        token_id: str(row.token_id),
      };
    }),
  };
}
export function parsePricingConfig(value: unknown): PricingConfig {
  const o = object(value),
    bindings: Record<string, PriceBinding> = {};
  for (const [model, value] of Object.entries(object(o.bindings))) {
    const b = object(value);
    const provider = str(b.provider);
    if (!Object.hasOwn(OFFICIAL_PROVIDERS, provider))
      throw new Error("Unsupported official provider");
    Object.defineProperty(bindings, model, {
      value: { provider, model: str(b.model) },
      enumerable: true,
      configurable: true,
      writable: true,
    });
  }
  const provider = str(o.provider);
  if (provider && !Object.hasOwn(OFFICIAL_PROVIDERS, provider))
    throw new Error("Unsupported official provider");
  const day = count(o.billing_day);
  if (day < 1 || day > 31) throw new Error("Invalid billing day");
  const overrides = Object.fromEntries(
    Object.entries(o.overrides == null ? {} : object(o.overrides)).map(
      ([model, value]) => {
        const rate = object(value);
        return [
          model,
          {
            input: decimal(rate.input),
            output: decimal(rate.output),
            cache_read: decimal(rate.cache_read),
            cache_write: decimal(rate.cache_write),
          },
        ];
      },
    ),
  );
  return {
    ...(o.overrides == null ? {} : { overrides }),
    provider,
    bindings,
    monthly_budget_usd: decimal(o.monthly_budget_usd, true),
    billing_day: day,
    time_zone: str(o.time_zone),
  };
}
export function parseServiceBilling(value: unknown): ServiceBilling {
  const o = object(value);
  return {
    config: parsePricingConfig(o.config),
    periods: array(o.periods, (value) => {
      const p = object(value);
      if (
        p.used_percent !== null &&
        (typeof p.used_percent !== "number" ||
          !Number.isFinite(p.used_percent) ||
          p.used_percent < 0)
      )
        throw new Error("Invalid official quota");
      return {
        id: str(p.id),
        kind: str(p.kind),
        start: timestamp(p.start),
        end: timestamp(p.end),
        observed_at: p.observed_at === null ? null : timestamp(p.observed_at),
        used_percent: p.used_percent as number | null,
        budget_usd: decimal(p.budget_usd, true),
        remaining_usd: decimal(p.remaining_usd, true),
        coverage: str(p.coverage),
        summary: parseBillingSummary(p.summary),
      };
    }),
  };
}
