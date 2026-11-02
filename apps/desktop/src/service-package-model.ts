import type { ServicePackage } from "./bridge";
import {
  convertUSD,
  displayCurrency,
  formatCurrency,
} from "./currency-display";
import type { ServiceQuotaEstimate } from "./pricing-model";
import { codingPlanPresetIDs } from "./service-presets";
import {
  hasPlanUsage,
  isSubscriptionKind,
  type Service,
} from "./service-model";
import type {
  RateLimitWindow,
  SubscriptionUsage,
} from "./subscription-usage-model";

/** What the list knows about one service's quota read. */
export interface UsageEntry {
  status: "loading" | "ready" | "error";
  usage?: SubscriptionUsage;
  error?: string;
  /** Why the last read failed, when Core names it (`key_rejected`, …). */
  reason?: string;
}

/**
 * A card on the provider page: a backend package, or a lone service the
 * package list does not cover yet (not loaded, or the read failed).
 */
export interface PackageCard {
  id: string;
  /** The backend package; absent for a fallback single card. */
  pkg?: ServicePackage;
  /** Members in routing order. */
  services: Service[];
  section: "subscription" | "relay";
}

/** A window's reset time, or null when the provider reported neither field. */
export function windowResetTime(
  window: RateLimitWindow,
  fetchedAt: string | undefined,
): number | null {
  if (window.reset_at) {
    const at = Date.parse(window.reset_at);
    return Number.isFinite(at) ? at : null;
  }
  if (window.reset_after_seconds != null && fetchedAt) {
    const from = Date.parse(fetchedAt);
    if (Number.isFinite(from)) return from + window.reset_after_seconds * 1000;
  }
  return null;
}

/**
 * The window an account runs out of first: the 5-hour or weekly window with
 * less left; on a tie the later reset, since quota returns only once both
 * reset. A window whose reset has passed counts as unused, as in routing.
 */
export interface BindingQuota {
  /** Percent left, 0–100. */
  remaining: number;
  /** Epoch ms of the binding window's reset, or null when unknown. */
  resetAt: number | null;
  /** Which window binds; `secondary` is the longer one. */
  window: "primary" | "secondary";
  exhausted: boolean;
}

export function bindingQuota(
  usage: SubscriptionUsage | undefined,
  now: number,
): BindingQuota | null {
  if (!usage) return null;
  let quota: BindingQuota | null = null;
  let rolled = false;
  for (const name of ["primary", "secondary"] as const) {
    const window = usage[name];
    if (!window) continue;
    let used = window.used_percent;
    let resetAt = windowResetTime(window, usage.fetched_at);
    if (resetAt !== null && resetAt <= now) {
      used = 0;
      resetAt = null;
      rolled = true;
    }
    const remaining = Math.min(Math.max(100 - used, 0), 100);
    if (
      !quota ||
      remaining < quota.remaining ||
      (remaining === quota.remaining &&
        resetAt !== null &&
        (quota.resetAt === null || resetAt > quota.resetAt))
    )
      quota = { remaining, resetAt, window: name, exhausted: false };
  }
  if (!quota) return null;
  const flagged = usage.limit_reached === true || usage.allowed === false;
  quota.exhausted = (flagged && !rolled) || quota.remaining <= 0;
  if (quota.exhausted) quota.remaining = 0;
  return quota;
}

/** Subscriptions and coding plans share the first section. */
export function cardSection(service: Service): PackageCard["section"] {
  return isSubscriptionKind(service.kind) ||
    (codingPlanPresetIDs as readonly string[]).includes(service.kind)
    ? "subscription"
    : "relay";
}

/**
 * Cards in routing order. A package sits where its earliest member is in
 * `ordered`, so a saved reorder shows before packages reload. Services the
 * package list does not name become single cards.
 */
export function packageCards(
  ordered: Service[],
  packages: ServicePackage[] | null,
): PackageCard[] {
  const position = new Map(
    ordered.map((service, index) => [service.id, index]),
  );
  const byID = new Map(ordered.map((service) => [service.id, service]));
  const covered = new Set<string>();
  const cards: { card: PackageCard; at: number }[] = [];
  for (const pkg of packages ?? []) {
    const services = pkg.service_ids
      .map((id) => byID.get(id))
      .filter((service): service is Service => !!service)
      .sort((a, b) => position.get(a.id)! - position.get(b.id)!);
    if (services.length === 0) continue;
    services.forEach((service) => covered.add(service.id));
    cards.push({
      card: {
        id: pkg.id,
        pkg,
        services,
        section:
          pkg.kind === "subscription"
            ? "subscription"
            : cardSection(services[0]),
      },
      at: position.get(services[0].id)!,
    });
  }
  for (const service of ordered) {
    if (covered.has(service.id)) continue;
    cards.push({
      card: {
        id: `service:${service.id}`,
        services: [service],
        section: cardSection(service),
      },
      at: position.get(service.id)!,
    });
  }
  return cards.sort((a, b) => a.at - b.at).map(({ card }) => card);
}

/**
 * The service order after moving section cards: `next` is the section's
 * visible cards in their new order. Their slots in the full card list take
 * them in that order; every card keeps its members together and in order.
 */
export function reorderedServices(
  cards: PackageCard[],
  next: PackageCard[],
): Service[] | null {
  // Packages can be re-derived between a drag's start and its drop, for
  // example when the first usage read after a restart names the plans. A drop
  // that names cards which no longer exist is dropped rather than guessed at.
  const current = new Map(cards.map((card) => [card.id, card]));
  const moving = next.map((card) => current.get(card.id));
  if (
    moving.some((card) => !card) ||
    new Set(next.map((c) => c.id)).size !== next.length
  )
    return null;
  const moved = new Set(next.map((card) => card.id));
  let index = 0;
  return cards
    .map((card) => (moved.has(card.id) ? moving[index++]! : card))
    .flatMap((card) => card.services);
}

/** Colors of the quota area: green, amber at 20% or less, red when used up. */
export type QuotaTone = "positive" | "warning" | "critical" | "neutral";

export function remainingTone(remaining: number, exhausted = false): QuotaTone {
  if (exhausted || remaining <= 0) return "critical";
  return remaining <= 20 ? "warning" : "positive";
}

/**
 * The quota area of one card, before wording: a status sentence, one bar and
 * a two-part caption. Each kind maps to one bar shape.
 */
export type CardQuota =
  | { kind: "loading" }
  /** The last read failed; `message` and `reason` say why, when known. */
  | { kind: "error"; message?: string; reason?: string }
  | { kind: "none" }
  | {
      kind: "windows";
      /** The 5-hour window, drawn inside the weekly one. */
      primary: QuotaWindow | null;
      secondary: QuotaWindow | null;
      binding: BindingQuota;
    }
  | {
      kind: "accounts";
      /** Enabled members with a known quota. */
      live: number;
      known: number;
      average: number;
      exhausted: { name: string; resetAt: number | null }[];
      /** The lowest member at 20% or less, when none is used up. */
      low: { name: string; remaining: number; resetAt: number | null } | null;
      disabled: number;
      pending: number;
      failed: number;
      /** Enabled members that must sign in before they report a quota. */
      signedOut: number;
    }
  /** Every member of a package is turned off. */
  | { kind: "disabled"; count: number }
  /** No enabled member is signed in, so none can report a quota. */
  | { kind: "signedOut"; count: number; disabled: number }
  | { kind: "key"; remainingUSD: string; totalUSD: string; remaining: number }
  | { kind: "unlimited"; usedUSD?: string }
  | { kind: "balance"; balance: string };

/** One rate-limit window of a card; `seconds` is its length when reported. */
export interface QuotaWindow {
  remaining: number;
  resetAt: number | null;
  seconds?: number;
}

function windowRemaining(
  window: RateLimitWindow | undefined,
  fetchedAt: string | undefined,
  now: number,
): QuotaWindow | null {
  if (!window) return null;
  const seconds = window.limit_window_seconds;
  const resetAt = windowResetTime(window, fetchedAt);
  if (resetAt !== null && resetAt <= now)
    return { remaining: 100, resetAt: null, seconds };
  return {
    remaining: Math.min(Math.max(100 - window.used_percent, 0), 100),
    resetAt,
    seconds,
  };
}

/** Core's reasons for a failed quota read, as `packages.readFailure` keys. */
const failureKeys: Record<string, string> = {
  key_rejected: "keyRejected",
  no_plan: "noPlan",
  wrong_address: "wrongAddress",
  unreachable: "unreachable",
  timeout: "timeout",
  provider_down: "providerDown",
  unexpected_reply: "unexpectedReply",
  no_quota: "noQuota",
  provider_error: "providerSaid",
};

/**
 * Why a quota read failed, as one short caption: the i18n key under
 * `packages.readFailure`, worded with the provider's name. Core's `reason`
 * decides when it names one; otherwise the message is read for the same
 * causes. `detail` is the provider's own words for an error only it can
 * explain. The full message stays available as the caption's tooltip.
 */
export function readFailureReason(
  message?: string,
  reason?: string,
): { key: string; detail?: string } {
  const raw = (message ?? "").trim();
  // The provider's words after Core's "<label> usage unavailable: " prefix.
  const detail = raw.replace(/^.*\busage unavailable: /i, "").trim();
  const known = reason ? failureKeys[reason] : undefined;
  if (known)
    return known === "providerSaid" && detail
      ? { key: known, detail }
      : { key: known === "providerSaid" ? "other" : known };
  const text = raw.toLowerCase();
  if (text.includes("service_credential_unavailable")) return { key: "noKey" };
  if (text.includes("subscription_usage_timeout") || text.includes("timeout"))
    return { key: "timeout" };
  const status = /status (\d{3})/.exec(text)?.[1];
  if (status === "401" || status === "403") return { key: "keyRejected" };
  if (status === "404") return { key: "wrongAddress" };
  if (status?.startsWith("5")) return { key: "providerDown" };
  if (
    text.includes("connection refused") ||
    text.includes("no such host") ||
    text.includes("dial tcp")
  )
    return { key: "unreachable" };
  if (text.includes("invalid payload")) return { key: "unexpectedReply" };
  if (text.includes("no usage windows")) return { key: "noQuota" };
  // Anything else is the provider's own error, or a message the app already
  // worded, such as a rate limit.
  return detail ? { key: "providerSaid", detail } : { key: "other" };
}

/** The quota area of a single service from its last read. */
export function serviceQuota(
  entry: UsageEntry | undefined,
  now: number,
): CardQuota {
  if (!entry || entry.status === "loading") return { kind: "loading" };
  if (entry.status === "error" || !entry.usage)
    return { kind: "error", message: entry.error, reason: entry.reason };
  const usage = entry.usage;
  const binding = bindingQuota(usage, now);
  if (binding)
    return {
      kind: "windows",
      primary: windowRemaining(usage.primary, usage.fetched_at, now),
      secondary: windowRemaining(usage.secondary, usage.fetched_at, now),
      binding,
    };
  const quota = usage.quota;
  if (quota?.unlimited) return { kind: "unlimited", usedUSD: quota.used_usd };
  if (quota?.remaining_usd != null && quota.total_usd != null) {
    const total = Number(quota.total_usd);
    const left = Number(quota.remaining_usd);
    const remaining =
      total > 0 && Number.isFinite(left)
        ? Math.min(Math.max((left / total) * 100, 0), 100)
        : 0;
    return {
      kind: "key",
      remainingUSD: quota.remaining_usd,
      totalUSD: quota.total_usd,
      remaining,
    };
  }
  const credits = usage.credits;
  if (credits?.unlimited)
    return { kind: "unlimited", usedUSD: quota?.used_usd };
  if (credits?.balance) return { kind: "balance", balance: credits.balance };
  return { kind: "none" };
}

/**
 * The quota area of a multi-account package. Disabled accounts are counted
 * but left out of the average, since routing skips them; so are accounts
 * whose last read failed and accounts that are not signed in.
 */
export function packageQuota(
  services: Service[],
  usage: Record<string, UsageEntry | undefined>,
  now: number,
  name: (service: Service) => string,
): CardQuota {
  let disabled = 0;
  let pending = 0;
  let failed = 0;
  let signedOut = 0;
  let total = 0;
  let message: string | undefined;
  let reason: string | undefined;
  const known: { service: Service; quota: BindingQuota }[] = [];
  for (const service of services) {
    if (!service.enabled) {
      disabled++;
      continue;
    }
    if (!hasPlanUsage(service)) {
      signedOut++;
      continue;
    }
    const entry = usage[service.id];
    if (!entry || entry.status === "loading") {
      pending++;
      continue;
    }
    const quota =
      entry.status === "error" ? null : bindingQuota(entry.usage, now);
    if (!quota) {
      failed++;
      if (message === undefined) {
        message = entry.error;
        reason = entry.reason;
      }
      continue;
    }
    known.push({ service, quota });
    total += quota.remaining;
  }
  if (known.length === 0) {
    if (disabled === services.length)
      return { kind: "disabled", count: disabled };
    if (failed > 0) return { kind: "error", message, reason };
    if (pending > 0) return { kind: "loading" };
    return { kind: "signedOut", count: signedOut, disabled };
  }
  const exhausted = known
    .filter(({ quota }) => quota.exhausted)
    .map(({ service, quota }) => ({
      name: name(service),
      resetAt: quota.resetAt,
    }))
    .sort(
      (a, b) =>
        (a.resetAt ?? Number.POSITIVE_INFINITY) -
        (b.resetAt ?? Number.POSITIVE_INFINITY),
    );
  const lowest = known
    .filter(({ quota }) => !quota.exhausted && quota.remaining <= 20)
    .sort((a, b) => a.quota.remaining - b.quota.remaining)[0];
  return {
    kind: "accounts",
    live: known.length - exhausted.length,
    known: known.length,
    average: Math.round(total / known.length),
    exhausted,
    low: lowest
      ? {
          name: name(lowest.service),
          remaining: lowest.quota.remaining,
          resetAt: lowest.quota.resetAt,
        }
      : null,
    disabled,
    pending,
    failed,
    signedOut,
  };
}

/** Estimated whole quota windows of one account in USD, by usage window. */
export type WindowTotals = Partial<Record<"primary" | "secondary", number>>;

const windowKinds = ["primary", "secondary"] as const;

/**
 * Every member's estimated windows. Accounts of one plan share its limits, so
 * a member without an estimate of its own takes the mean of the members that
 * have one; a package whose plan is unknown may mix plans and lends nothing.
 */
export function memberWindowTotals(
  cards: PackageCard[],
  estimates: ServiceQuotaEstimate[],
): Record<string, WindowTotals> {
  const own: Record<string, WindowTotals> = {};
  for (const estimate of estimates) {
    const totals: WindowTotals = {};
    for (const window of estimate.windows) {
      const total = Number(window.total_usd);
      if (Number.isFinite(total) && total > 0) totals[window.kind] = total;
    }
    own[estimate.service_id] = totals;
  }
  const result: Record<string, WindowTotals> = {};
  for (const card of cards) {
    const shared: WindowTotals = {};
    if (card.pkg?.kind === "subscription" && card.pkg.plan_type)
      for (const kind of windowKinds) {
        const known = card.services
          .map((service) => own[service.id]?.[kind])
          .filter((total): total is number => total !== undefined);
        if (known.length)
          shared[kind] =
            known.reduce((sum, total) => sum + total, 0) / known.length;
      }
    for (const service of card.services)
      result[service.id] = { ...shared, ...own[service.id] };
  }
  return result;
}

/**
 * What an account can still spend, valued: the reported window with the
 * least left in USD, since it runs out first; a used-up account has nothing
 * left of its binding window. Null unless every reported window has an
 * estimate.
 */
export interface QuotaMoney {
  window: "primary" | "secondary";
  left: number;
  total: number;
}

export function windowMoney(
  quota: Extract<CardQuota, { kind: "windows" }>,
  totals: WindowTotals | undefined,
): QuotaMoney | null {
  let money: QuotaMoney | null = null;
  for (const kind of windowKinds) {
    const window = quota[kind];
    if (!window) continue;
    const total = totals?.[kind];
    if (total === undefined) return null;
    const left = (total * window.remaining) / 100;
    if (!money || left < money.left) money = { window: kind, left, total };
  }
  if (!money || !quota.binding.exhausted) return money;
  const total = totals?.[quota.binding.window];
  return total === undefined
    ? null
    : { window: quota.binding.window, left: 0, total };
}

/**
 * A package's money over the accounts whose quota is known, the ones
 * packageQuota averages: what they have left and what their windows hold.
 * Null unless each of them has an estimate.
 */
export function packageMoney(
  services: Service[],
  usage: Record<string, UsageEntry | undefined>,
  totals: Record<string, WindowTotals>,
  now: number,
): { left: number; total: number; accounts: number } | null {
  let left = 0;
  let total = 0;
  let accounts = 0;
  for (const service of services) {
    if (!service.enabled || !hasPlanUsage(service)) continue;
    const quota = serviceQuota(usage[service.id], now);
    if (quota.kind !== "windows") continue;
    const money = windowMoney(quota, totals[service.id]);
    if (!money) return null;
    left += money.left;
    total += money.total;
    accounts++;
  }
  return accounts ? { left, total, accounts } : null;
}

/**
 * A dollar estimate in the display currency, in whole units from 10 up since
 * it is not exact.
 */
export function formatEstimateUSD(value: number): string {
  const currency = displayCurrency();
  const amount = convertUSD(value, currency);
  return formatCurrency(amount, amount < 10 ? currency.digits : 0, currency);
}

export type ServiceFilter = "all" | "enabled" | "disabled";

/**
 * A card counts as on while any member is on, since routing can still use it,
 * and as off once every member is off; so each card has one status and the
 * filter counts add up.
 */
export function cardEnabled(card: PackageCard): boolean {
  return card.services.some((service) => service.enabled);
}

export function cardMatchesFilter(
  card: PackageCard,
  filter: ServiceFilter,
): boolean {
  return filter === "all" || cardEnabled(card) === (filter === "enabled");
}

/** True when any member's name, kind, address, account or model matches. */
export function cardMatches(
  card: PackageCard,
  query: string,
  fields: (service: Service) => (string | null | undefined)[],
): boolean {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return true;
  return card.services.some((service) =>
    fields(service).some((value) =>
      value?.toLocaleLowerCase().includes(needle),
    ),
  );
}
