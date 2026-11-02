import {
  useEffect,
  useId,
  useLayoutEffect,
  useState,
  type ReactNode,
  type RefObject,
} from "react";

import {
  ChevronDown,
  ChevronUp,
  Ellipsis,
  Flask,
  ShieldCheck,
} from "@/components/icons";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { IconButton } from "@/components/IconButton";
import {
  PackageAddSlot,
  PackageCardShell,
  PackageLogo,
  QuotaBar,
  QuotaSummary,
  type QuotaBarTone,
} from "@/components/PackageCard";
import { SegmentedControl } from "@/components/SegmentedControl";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { ServicePerformanceMeter } from "@/components/ServicePerformanceMeter";
import {
  SortableGrid,
  type SortableGridHandle,
} from "@/components/SortableGrid";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

import { useDisplayCurrency } from "./currency-display";
import {
  PACKAGE_STRATEGIES,
  type PackageStrategy,
  type ServicePackage,
} from "./bridge";
import { useT } from "./i18n";
import {
  billingAmount,
  type BillingAmounts,
  type ServiceQuotaEstimate,
} from "./pricing-model";
import { useQuotaDisplayMode, type QuotaDisplayMode } from "./quota-display";
import {
  cardMatches,
  cardMatchesFilter,
  formatEstimateUSD,
  memberWindowTotals,
  packageCards,
  packageMoney,
  packageQuota,
  readFailureReason,
  remainingTone,
  reorderedServices,
  serviceQuota,
  type CardQuota,
  type PackageCard,
  type QuotaWindow,
  type ServiceFilter,
  type UsageEntry,
  type WindowTotals,
  windowMoney,
} from "./service-package-model";
import {
  activeServiceRisk,
  hasPlanUsage,
  serviceKindLabel,
  serviceStatusLabel,
  subscriptionRiskLabel,
  type Service,
} from "./service-model";
import type { SubscriptionProvider } from "./subscription-model";
import {
  formatQuotaUSD,
  formatResetCountdown,
  planTypeLabel,
  windowLabel,
} from "./subscription-usage-model";
import type {
  ServicePerformance,
  UsageRangePreset,
  UsageStatus,
} from "./usage-range";

type Translate = ReturnType<typeof useT>;

/** What the page does for a card; the grid only lays the cards out. */
export interface ServiceCardActions {
  /** Items of a service's ⋯ menu. */
  menu: (service: Service) => ReactNode;
  /** A badge for the service's active risk, or null. */
  risk: (service: Service) => ReactNode;
  /** The line under a single card's name: its address or account. */
  detail: (service: Service) => string;
  edit: (service: Service, tab?: "models") => void;
  test: (service: Service) => void;
  toggle: (service: Service) => void;
  login: (service: Service) => void;
  billing: (service: Service) => void;
  retryUsage: (services: Service[]) => void;
  /** Starts signing in another account of the service's kind. */
  addAccount: (service: Service) => void;
  addSubscription: () => void;
  addRelay: () => void;
  clearFilters: () => void;
}

export interface CardBilling {
  status: "idle" | "loading" | "ready" | "error";
  byService: Map<string, BillingAmounts>;
}

export interface StrategyControl {
  savingID: string | null;
  error: { id: string; message: string } | null;
  onChange: (id: string, strategy: PackageStrategy) => void;
}

interface CardEnv {
  t: Translate;
  mode: QuotaDisplayMode;
  now: Date;
  usage: Record<string, UsageEntry | undefined>;
  performance: {
    status: UsageStatus;
    byService: Record<string, ServicePerformance | undefined>;
  };
  preset: UsageRangePreset;
  billing: CardBilling;
  /** Estimated whole quota windows by service, borrowed within a plan. */
  totals: Record<string, WindowTotals>;
  ready: boolean;
  actingID: string | null;
  /** Services whose quota is being read again. */
  refreshing: ReadonlySet<string>;
  actions: ServiceCardActions;
}

type ServiceDot = "positive" | "pending" | "negative" | "neutral";

function serviceDot(service: Service, now: Date): ServiceDot {
  if (!service.enabled) return "neutral";
  const risk = activeServiceRisk(service, now);
  if (risk?.state === "suspended") return "negative";
  const status = service.subscription?.status;
  if (status === "needs_reauth" || status === "error") return "negative";
  if (risk) return "pending";
  if (!status || status === "connected") return "positive";
  if (status === "authorizing" || status === "disconnected") return "pending";
  return "negative";
}

const dotTones: Record<ServiceDot, QuotaBarTone> = {
  positive: "positive",
  pending: "warning",
  negative: "critical",
  neutral: "neutral",
};

/** Short product names for package titles such as "Claude Max 20×". */
const providerBrands: Record<SubscriptionProvider, string> = {
  openai_codex: "Codex",
  claude_code: "Claude",
  xai_grok: "Grok",
  antigravity: "Antigravity",
  github_copilot: "Copilot",
  factory_droid: "Droid",
};

/** Cards stay at least this wide; the grid drops a column below it. */
const minCardWidth = 236;
const gridGap = 12;

function columnsFor(width: number) {
  return Math.min(
    4,
    Math.max(1, Math.floor((width + gridGap) / (minCardWidth + gridGap))),
  );
}

function joined(...parts: (string | null | undefined | false)[]) {
  return parts.filter(Boolean).join(" · ");
}

/** "2 小时后" for a reset time, or null when it is unknown. */
function resetIn(at: number | null, now: Date) {
  if (at === null) return null;
  return formatResetCountdown(
    { used_percent: 0, reset_at: new Date(at).toISOString() },
    now,
    { short: true },
  );
}

function packageBrand(card: PackageCard) {
  const provider = card.services[0]?.subscription?.provider;
  return provider
    ? providerBrands[provider]
    : serviceKindLabel(card.services[0].kind);
}

function packageTitle(card: PackageCard, t: Translate) {
  const brand = packageBrand(card);
  const plan = planTypeLabel(
    card.pkg?.plan_type,
    card.services[0]?.subscription?.provider,
  );
  return plan ? `${brand} ${plan}` : t("packages.unknownPlan", { brand });
}

/**
 * Why a single service cannot take requests: a sign-in state or an upstream
 * pause. The dot and the switch already show an available or switched-off
 * service, so those get no words.
 */
function blocker(services: Service[], now: Date): string | null {
  if (services.length !== 1) return null;
  // Judged as if switched on; the switch shows that part.
  const service = { ...services[0], enabled: true };
  if (serviceDot(service, now) === "positive") return null;
  const risk = activeServiceRisk(service, now);
  return risk && service.subscription?.status === "connected"
    ? subscriptionRiskLabel(risk.code, risk.state)
    : serviceStatusLabel(service, now);
}

interface QuotaView {
  tone: QuotaBarTone;
  status: string;
  statusEnd?: ReactNode;
  bar: ReactNode;
  captionStart?: ReactNode;
  captionEnd?: ReactNode;
  busy?: boolean;
}

function windowText(
  window: QuotaWindow,
  secondary: boolean,
  env: CardEnv,
  withReset: boolean,
) {
  const { t, mode, now } = env;
  const name = windowLabel(window.seconds, secondary);
  const percent = Math.round(
    mode === "remaining" ? window.remaining : 100 - window.remaining,
  );
  return joined(
    t(mode === "remaining" ? "packages.windowLeft" : "packages.windowUsed", {
      window: name,
      percent,
    }),
    withReset && resetIn(window.resetAt, now),
  );
}

/**
 * What each estimated window of an account holds, with what is left or used
 * of it in the display mode, and how the estimate is made.
 */
function windowEstimateHint(
  totals: WindowTotals,
  windows: Record<"primary" | "secondary", QuotaWindow | null>,
  env: CardEnv,
) {
  const { t, mode } = env;
  const lines = (["primary", "secondary"] as const).flatMap((kind) => {
    const window = windows[kind];
    const total = totals[kind];
    if (!window || total === undefined) return [];
    const share =
      mode === "remaining" ? window.remaining : 100 - window.remaining;
    return [
      t(
        mode === "remaining"
          ? "packages.estimateLeft"
          : "packages.estimateUsed",
        {
          window: windowLabel(window.seconds, kind === "secondary"),
          total: formatEstimateUSD(total),
          amount: formatEstimateUSD((total * share) / 100),
        },
      ),
    ];
  });
  return [...lines, t("packages.estimateBasis")].join("\n");
}

/** "还剩约 $29", or what was used in the used display mode. */
function moneyText(left: number, total: number, env: CardEnv) {
  const { t, mode } = env;
  return mode === "remaining"
    ? t("packages.moneyLeft", { amount: formatEstimateUSD(left) })
    : t("packages.moneyUsed", { amount: formatEstimateUSD(total - left) });
}

/** The status line's "共约 $41", explained on hover. */
function estimateTotal(total: number, hint: string, env: CardEnv) {
  return (
    <span data-testid="quota-estimate" title={hint}>
      {env.t("packages.estimateTotal", { amount: formatEstimateUSD(total) })}
    </span>
  );
}

function quotaView(
  quota: CardQuota,
  services: Service[],
  env: CardEnv,
  hasUsage: boolean,
): QuotaView {
  const { t, mode, now, actions } = env;
  const empty = <QuotaBar kind="empty" />;
  const blocked = blocker(services, now);
  const shown = (remaining: number) =>
    Math.round(mode === "remaining" ? remaining : 100 - remaining);
  const percentLabel = (remaining: number) =>
    t(mode === "remaining" ? "usage.remainingPercent" : "usage.usedPercent", {
      percent: shown(remaining),
    });
  const flat = (remaining: number, tone: QuotaBarTone) => (
    <QuotaBar
      kind="flat"
      label={percentLabel(remaining)}
      percent={mode === "remaining" ? remaining : 100 - remaining}
      tone={tone}
    />
  );
  const dot = services.every((service) => !service.enabled)
    ? "neutral"
    : services.length === 1
      ? dotTones[serviceDot(services[0], now)]
      : "positive";
  if (!hasUsage) {
    const subscription = services[0]?.subscription;
    return {
      tone: dot,
      status: blocked ?? t("packages.noQuotaData"),
      bar: empty,
      captionStart: subscription ? t("packages.signInForQuota") : undefined,
      captionEnd:
        subscription && services[0].enabled ? (
          <Button
            className="h-auto p-0 text-micro"
            disabled={!env.ready || env.actingID === services[0].id}
            onClick={() => actions.login(services[0])}
            type="button"
            variant="link"
          >
            {t("services.signIn")}
          </Button>
        ) : undefined,
    };
  }
  switch (quota.kind) {
    case "loading":
      return {
        tone: "neutral",
        status: t("packages.reading"),
        bar: empty,
        busy: true,
      };
    case "error": {
      const retrying = services.some((service) =>
        env.refreshing.has(service.id),
      );
      const reason = readFailureReason(quota.message, quota.reason);
      return {
        tone: "neutral",
        status: t("packages.unreadable"),
        // No placeholder bar: the reason takes its room.
        bar: null,
        captionStart: (
          <span title={quota.message}>
            {t(`packages.readFailure.${reason.key}`, {
              provider: serviceKindLabel(services[0]!.kind),
              detail: reason.detail,
            })}
            {quota.message && !reason.detail ? (
              <span className="sr-only">{quota.message}</span>
            ) : null}
          </span>
        ),
        captionEnd: (
          <Button
            className="h-auto p-0 text-micro"
            disabled={!env.ready || retrying}
            onClick={() => actions.retryUsage(services)}
            type="button"
            variant="link"
          >
            {t(retrying ? "common.refreshing" : "common.retry")}
          </Button>
        ),
      };
    }
    case "none":
      return {
        tone: dot,
        status: blocked ?? t("packages.noQuotaData"),
        bar: empty,
        captionStart: blocked ? t("packages.noQuotaData") : undefined,
      };
    case "disabled":
      return {
        tone: "neutral",
        status: t("common.disabled"),
        bar: empty,
        captionStart: t("packages.allDisabled", { count: quota.count }),
      };
    case "signedOut":
      return {
        tone: "warning",
        status: t("subscription.disconnected"),
        bar: empty,
        captionStart: t("packages.signInForQuota"),
        captionEnd: quota.disabled
          ? t("packages.disabledCount", { count: quota.disabled })
          : undefined,
      };
    case "windows": {
      const { binding, primary, secondary } = quota;
      const tone = remainingTone(binding.remaining, binding.exhausted);
      const totals = env.totals[services[0]!.id] ?? {};
      // A used-up or paused account needs the status for when and why.
      const money =
        binding.exhausted || blocked ? null : windowMoney(quota, totals);
      const status = binding.exhausted
        ? joined(
            t("packages.usedUp"),
            resetIn(binding.resetAt, now) &&
              t("packages.backAt", { time: resetIn(binding.resetAt, now) }),
          )
        : joined(
            blocked,
            money
              ? moneyText(money.left, money.total, env)
              : percentLabel(binding.remaining),
          );
      const part = (window: QuotaWindow | null) =>
        window
          ? {
              percent:
                mode === "remaining"
                  ? window.remaining
                  : 100 - window.remaining,
              tone: remainingTone(window.remaining),
            }
          : null;
      const label = joined(
        primary && windowText(primary, false, env, false),
        secondary && windowText(secondary, true, env, false),
      );
      // A reset time shows only on a window running low; the used-up
      // window's return is in the status. Hovering shows every reset.
      const caption = (window: QuotaWindow | null, secondary: boolean) => {
        if (!window) return undefined;
        const kind = secondary ? "secondary" : "primary";
        const reset =
          window.remaining <= 20 &&
          !(binding.exhausted && binding.window === kind);
        return (
          <span title={windowText(window, secondary, env, true)}>
            {windowText(window, secondary, env, reset)}
          </span>
        );
      };
      return {
        tone: services[0]?.enabled === false ? "neutral" : tone,
        status,
        statusEnd: money
          ? estimateTotal(
              money.total,
              windowEstimateHint(totals, { primary, secondary }, env),
              env,
            )
          : undefined,
        bar:
          primary && secondary ? (
            <QuotaBar
              inner={part(primary)}
              kind="nested"
              label={label}
              outer={part(secondary)}
            />
          ) : (
            <QuotaBar
              kind="flat"
              label={label}
              percent={part(primary ?? secondary)!.percent}
              tone={tone}
            />
          ),
        captionStart: primary
          ? caption(primary, false)
          : caption(secondary, true),
        captionEnd: primary && secondary ? caption(secondary, true) : undefined,
      };
    }
    case "accounts": {
      const tone = remainingTone(quota.average, quota.live === 0);
      const first = quota.exhausted[0];
      const money =
        quota.live === 0
          ? null
          : packageMoney(services, env.usage, env.totals, now.getTime());
      const status =
        quota.live === 0
          ? joined(
              t("packages.allUsedUp", { count: quota.known }),
              first &&
                resetIn(first.resetAt, now) &&
                t("packages.backAt", { time: resetIn(first.resetAt, now) }),
            )
          : money
            ? moneyText(money.left, money.total, env)
            : t(
                mode === "remaining"
                  ? "packages.liveAverageLeft"
                  : "packages.liveAverageUsed",
                { count: quota.live, percent: shown(quota.average) },
              );
      const lowReset = quota.low && resetIn(quota.low.resetAt, now);
      const firstReset = first && resetIn(first.resetAt, now);
      const note = quota.exhausted.length
        ? quota.live === 0
          ? null
          : firstReset
            ? t("packages.someUsedUpBack", {
                count: quota.exhausted.length,
                time: firstReset,
              })
            : t("packages.someUsedUp", { count: quota.exhausted.length })
        : quota.low
          ? lowReset
            ? t("packages.lowBack", { name: quota.low.name, time: lowReset })
            : t("packages.low", { name: quota.low.name })
          : quota.failed
            ? t("packages.someUnreadable", { count: quota.failed })
            : quota.signedOut
              ? t("packages.someSignedOut", { count: quota.signedOut })
              : quota.pending
                ? t("packages.someReading", { count: quota.pending })
                : t("packages.allPlenty");
      return {
        tone,
        status,
        statusEnd: money
          ? estimateTotal(
              money.total,
              [
                t("packages.estimateAccounts", { count: money.accounts }),
                t("packages.estimateBasis"),
              ].join("\n"),
              env,
            )
          : undefined,
        bar: flat(quota.average, tone),
        captionStart: note,
        captionEnd: quota.disabled
          ? t("packages.disabledCount", { count: quota.disabled })
          : undefined,
      };
    }
    case "key": {
      const tone = remainingTone(quota.remaining);
      return {
        tone: dot === "positive" ? tone : dot,
        status: joined(
          blocked,
          t("packages.keyLeft", { amount: formatQuotaUSD(quota.remainingUSD) }),
        ),
        bar: flat(quota.remaining, tone),
        captionStart: t("packages.keyTotal", {
          amount: formatQuotaUSD(quota.totalUSD),
        }),
        captionEnd: `${shown(quota.remaining)}%`,
      };
    }
    case "unlimited":
      return {
        tone: dot,
        status: joined(blocked, t("usage.quotaUnlimited")),
        bar: empty,
        captionStart: quota.usedUSD
          ? t("usage.quotaUsed", { amount: formatQuotaUSD(quota.usedUSD) })
          : undefined,
        captionEnd: t("packages.noTotal"),
      };
    case "balance":
      return {
        tone: dot,
        status: joined(
          blocked,
          t("packages.balance", { amount: quota.balance }),
        ),
        bar: empty,
        captionStart: t("packages.balanceOnly"),
        captionEnd: t("packages.noTotal"),
      };
  }
}

function QuotaArea({
  quota,
  services,
  env,
  hasUsage,
}: {
  quota: CardQuota;
  services: Service[];
  env: CardEnv;
  hasUsage: boolean;
}) {
  const view = quotaView(quota, services, env, hasUsage);
  return (
    <QuotaSummary
      bar={view.bar}
      busy={view.busy}
      captionEnd={view.captionEnd}
      captionStart={view.captionStart}
      status={view.status}
      statusEnd={view.statusEnd}
      tone={view.tone}
    />
  );
}

function sumBilling(services: Service[], billing: CardBilling) {
  let amount = 0;
  const total: BillingAmounts = {
    amount_usd: "0",
    priced: 0,
    unpriced: 0,
    pending: 0,
    revalued: 0,
    requests: 0,
  };
  let found = false;
  for (const service of services) {
    const item = billing.byService.get(service.id);
    if (!item) continue;
    found = true;
    amount += Number(item.amount_usd) || 0;
    total.priced += item.priced;
    total.unpriced += item.unpriced;
    total.pending += item.pending;
    total.revalued += item.revalued;
    total.requests += item.requests;
  }
  total.amount_usd = amount.toFixed(6);
  return found ? total : null;
}

/** The period's cost of some services, as a button that opens the details. */
function CardCost({
  services,
  name = services[0].name,
  env,
  periodLabel,
}: {
  services: Service[];
  /** The card's name; the details open on the first service. */
  name?: string;
  env: CardEnv;
  periodLabel: string;
}) {
  const { t, billing, actions } = env;
  const total = sumBilling(services, billing);
  const amount =
    billing.status === "loading" && !total
      ? "…"
      : billing.status === "error" && !total
        ? "—"
        : // A service with no billed request in the period cost nothing.
          billingAmount(
            total ?? {
              amount_usd: "0",
              priced: 1,
              unpriced: 0,
              pending: 0,
              revalued: 0,
              requests: 0,
            },
          );
  const incomplete = !!total && total.unpriced > 0;
  return (
    <Button
      aria-label={t("packages.costFor", {
        amount,
        period: periodLabel,
        name,
      })}
      className="h-auto shrink-0 p-0 text-xs font-semibold text-foreground tabular-nums"
      data-testid="package-cost"
      onClick={() => actions.billing(services[0])}
      title={incomplete ? t("pricing.incomplete") : undefined}
      type="button"
      variant="link"
    >
      {amount}
      {incomplete ? <span aria-hidden="true">*</span> : null}
    </Button>
  );
}

/** Test, cache and speed details, and the ⋯ menu of one service. */
function ServiceActions({
  service,
  env,
  grip,
}: {
  service: Service;
  env: CardEnv;
  grip?: ReactNode;
}) {
  const { t, actions, ready, actingID, performance, preset } = env;
  const acting = actingID === service.id;
  return (
    <>
      {grip}
      <ServicePerformanceMeter
        performance={performance.byService[service.id]}
        preset={preset}
        ready={ready}
        serviceId={service.id}
        serviceName={service.name}
        status={performance.status}
      />
      <IconButton
        disabled={!ready || acting}
        label={t("serviceTest.testNamed", { name: service.name })}
        onClick={() => actions.test(service)}
        size="icon-xs"
        type="button"
      >
        <Flask aria-hidden="true" />
      </IconButton>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <IconButton
            disabled={acting}
            label={t("services.moreNamed", { name: service.name })}
            size="icon-xs"
            type="button"
          >
            <Ellipsis aria-hidden="true" />
          </IconButton>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {actions.menu(service)}
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  );
}

/** A service's name, which opens its editor, with its plan and trust marks. */
function ServiceName({ service, env }: { service: Service; env: CardEnv }) {
  const { t, actions, actingID, usage } = env;
  const plan = service.subscription
    ? planTypeLabel(
        usage[service.id]?.usage?.plan_type,
        service.subscription.provider,
      )
    : null;
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <Button
        className="block h-auto min-w-0 truncate rounded-sm p-0 text-left text-sm font-semibold text-foreground"
        disabled={actingID === service.id}
        onClick={() => actions.edit(service)}
        title={service.name}
        type="button"
        variant="link"
      >
        {service.name}
      </Button>
      {plan ? (
        <Badge
          className="shrink-0 px-1.5 py-0 text-micro font-semibold tabular-nums"
          data-testid="subscription-plan"
          title={t("subscription.plan", { plan })}
          variant="accent"
        >
          {plan}
        </Badge>
      ) : null}
      {service.privacy_trusted ? (
        <span
          className="inline-flex shrink-0 text-muted-foreground"
          data-testid="privacy-trusted-badge"
          title={t("services.privacyTrustedBadgeHint")}
        >
          <ShieldCheck aria-hidden="true" className="size-3.5" />
          <span className="sr-only">
            {t("services.privacyTrustedBadgeHint")}
          </span>
        </span>
      ) : null}
    </div>
  );
}

/** The line under a service's name: an active risk, else its address. */
function ServiceSubline({ service, env }: { service: Service; env: CardEnv }) {
  const risk = env.actions.risk(service);
  if (risk) return <div className="flex min-w-0">{risk}</div>;
  const detail = env.actions.detail(service);
  return (
    <div
      className="min-w-0 truncate text-xs text-muted-foreground"
      title={service.http?.base_url ?? detail}
    >
      {detail}
    </div>
  );
}

function ServiceFooter({
  service,
  env,
  periodLabel,
}: {
  service: Service;
  env: CardEnv;
  periodLabel: string;
}) {
  const { t, actions, ready, actingID } = env;
  return (
    <>
      <CardCost env={env} periodLabel={periodLabel} services={[service]} />
      <span className="flex min-w-0 items-center gap-1 text-muted-foreground">
        <span className="truncate">{periodLabel}</span>
        <span aria-hidden="true">·</span>
        <Button
          aria-label={t("services.openModels", { name: service.name })}
          className="h-auto shrink-0 p-0 text-xs font-normal text-muted-foreground tabular-nums"
          disabled={actingID === service.id}
          onClick={() => actions.edit(service, "models")}
          type="button"
          variant="link"
        >
          {t("services.modelCount", { count: service.models.length })}
        </Button>
      </span>
      <Switch
        aria-label={t("services.enableNamed", { name: service.name })}
        checked={service.enabled}
        className="ml-auto"
        disabled={!ready || actingID === service.id}
        onCheckedChange={() => actions.toggle(service)}
        size="sm"
      />
    </>
  );
}

/** A relay, key or single account: its own quota, toggle and actions. */
function ServiceCard({
  service,
  env,
  handle,
  periodLabel,
  compact = false,
  packageID,
}: {
  service: Service;
  env: CardEnv;
  handle?: SortableGridHandle;
  periodLabel: string;
  compact?: boolean;
  packageID?: string;
}) {
  return (
    <PackageCardShell
      actions={
        <ServiceActions env={env} grip={handle?.grip} service={service} />
      }
      aria-label={service.name}
      compact={compact}
      data-package-id={packageID}
      data-service-id={service.id}
      data-testid={compact ? "package-account" : "package-card"}
      disabled={!service.enabled}
      dragging={handle?.dragging}
      footer={
        <ServiceFooter env={env} periodLabel={periodLabel} service={service} />
      }
      header={
        <>
          <PackageLogo>
            <ServiceKindIcon kind={service.kind} size={compact ? 18 : 22} />
          </PackageLogo>
          <div className="grid min-w-0 flex-1 gap-0.5">
            <ServiceName env={env} service={service} />
            <ServiceSubline env={env} service={service} />
          </div>
        </>
      }
    >
      <QuotaArea
        env={env}
        hasUsage={hasPlanUsage(service)}
        quota={serviceQuota(env.usage[service.id], env.now.getTime())}
        services={[service]}
      />
    </PackageCardShell>
  );
}

/** Account names for package captions; hints tell apart repeated names. */
function memberName(services: Service[]) {
  const names = services.map((service) => service.name);
  const unique = new Set(names).size === names.length;
  return (service: Service) =>
    unique
      ? service.name
      : (service.subscription?.account_hint ?? service.name);
}

function modelCount(services: Service[]) {
  return new Set(services.flatMap((service) => service.models)).size;
}

/** Several accounts of one plan, stacked; the tray below lists them. */
function PackageStackCard({
  card,
  env,
  handle,
  periodLabel,
  expanded,
  trayID,
  onToggle,
}: {
  card: PackageCard;
  env: CardEnv;
  handle: SortableGridHandle;
  periodLabel: string;
  expanded: boolean;
  trayID: string;
  onToggle: () => void;
}) {
  const { t, usage, now } = env;
  const { services } = card;
  const title = packageTitle(card, t);
  const enabled = services.some((service) => service.enabled);
  return (
    <PackageCardShell
      actions={handle.grip}
      aria-label={title}
      data-package-id={card.id}
      data-testid="package-card"
      disabled={!enabled}
      dragging={handle.dragging}
      footer={
        <>
          <CardCost
            env={env}
            name={title}
            periodLabel={periodLabel}
            services={services}
          />
          <span className="flex min-w-0 items-center gap-1 text-muted-foreground">
            <span className="truncate">{periodLabel}</span>
            <span aria-hidden="true">·</span>
            <span className="shrink-0 tabular-nums">
              {t("services.modelCount", { count: modelCount(services) })}
            </span>
          </span>
          <Button
            aria-controls={expanded ? trayID : undefined}
            aria-expanded={expanded}
            aria-label={t(
              expanded ? "packages.collapseNamed" : "packages.expandNamed",
              {
                name: title,
              },
            )}
            className="ml-auto h-auto shrink-0 gap-0.5 p-0 text-xs font-medium"
            data-testid="package-expand"
            onClick={onToggle}
            type="button"
            variant="link"
          >
            {t(expanded ? "packages.collapse" : "packages.expand")}
            {expanded ? (
              <ChevronUp aria-hidden="true" className="size-3" />
            ) : (
              <ChevronDown aria-hidden="true" className="size-3" />
            )}
          </Button>
        </>
      }
      header={
        <>
          <PackageLogo
            count={services.length}
            countLabel={t("packages.accountCount", { count: services.length })}
          >
            <ServiceKindIcon kind={services[0].kind} size={22} />
          </PackageLogo>
          <div className="grid min-w-0 flex-1 gap-0.5">
            <div className="truncate text-sm font-semibold" title={title}>
              {title}
            </div>
            <div className="truncate text-xs text-muted-foreground">
              {t("packages.accountCount", { count: services.length })}
            </div>
          </div>
        </>
      }
      stacked
    >
      <QuotaArea
        env={env}
        hasUsage
        quota={packageQuota(
          services,
          usage,
          now.getTime(),
          memberName(services),
        )}
        services={services}
      />
    </PackageCardShell>
  );
}

/** The expanded package: its accounts, one more sign-in, and the strategy. */
function PackageTray({
  card,
  env,
  columns,
  periodLabel,
  strategy,
  trayID,
}: {
  card: PackageCard;
  env: CardEnv;
  columns: number;
  periodLabel: string;
  strategy: StrategyControl;
  trayID: string;
}) {
  const { t, ready, actions } = env;
  const title = packageTitle(card, t);
  const current = card.pkg?.strategy ?? "auto";
  const error = strategy.error?.id === card.id ? strategy.error.message : null;
  return (
    <section
      aria-label={t("packages.trayLabel", { name: title })}
      className="grid min-w-0 gap-3 rounded-xl border bg-muted/40 p-3 motion-safe:animate-[panel-reveal_220ms_cubic-bezier(0.22,1,0.36,1)]"
      data-testid="package-tray"
      id={trayID}
    >
      <div
        className="grid gap-3"
        style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
      >
        {card.services.map((service) => (
          <ServiceCard
            compact
            env={env}
            key={service.id}
            packageID={card.id}
            periodLabel={periodLabel}
            service={service}
          />
        ))}
        <PackageAddSlot
          className="min-h-24"
          disabled={!ready}
          onClick={() => actions.addAccount(card.services[0])}
          title={t("packages.addAccount", { brand: packageBrand(card) })}
        />
      </div>
      {card.pkg ? (
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
          <span className="text-xs font-medium">{t("packages.strategy")}</span>
          <SegmentedControl<PackageStrategy>
            disabled={!ready || strategy.savingID !== null}
            label={t("packages.strategyNamed", { name: title })}
            onValueChange={(value) => strategy.onChange(card.id, value)}
            options={PACKAGE_STRATEGIES.map((value) => ({
              value,
              label: t(`packages.strategies.${value}`),
            }))}
            value={current}
          />
          <p
            className="min-w-0 text-xs text-muted-foreground"
            data-testid="package-strategy-hint"
          >
            {t(`packages.strategyHints.${current}`)}
          </p>
        </div>
      ) : null}
      {error ? (
        <FormMessage tone="error">
          {t("packages.strategyFailed", { message: error })}
        </FormMessage>
      ) : null}
    </section>
  );
}

function cardName(card: PackageCard, t: Translate) {
  return card.services.length > 1
    ? packageTitle(card, t)
    : card.services[0].name;
}

/** One section of cards under a heading, reordered within itself. */
function PackageSection({
  title,
  summary,
  cards,
  columns,
  env,
  periodLabel,
  reorderDisabled,
  onReorder,
  expandedID,
  onToggleExpanded,
  strategy,
  trayID,
  more,
  trailing,
  testID,
}: {
  title: string;
  summary: string;
  cards: PackageCard[];
  columns: number;
  env: CardEnv;
  periodLabel: string;
  reorderDisabled: boolean;
  onReorder: (next: PackageCard[]) => void;
  expandedID: string | null;
  onToggleExpanded: (id: string) => void;
  strategy: StrategyControl;
  trayID: string;
  more?: ReactNode;
  trailing?: ReactNode;
  testID: string;
}) {
  const { t } = env;
  const headingID = useId();
  const expanded = cards.find(
    (card) => card.id === expandedID && card.services.length > 1,
  );
  return (
    <section
      aria-labelledby={headingID}
      className="grid gap-2"
      data-testid={testID}
    >
      <div className="flex min-w-0 items-baseline gap-2">
        <h2
          className="shrink-0 text-xs font-semibold text-text-secondary"
          id={headingID}
        >
          {title}
        </h2>
        <span className="min-w-0 truncate text-xs text-muted-foreground tabular-nums">
          {summary}
        </span>
        {more}
      </div>
      <SortableGrid
        after={
          expanded
            ? {
                id: expanded.id,
                content: (
                  <PackageTray
                    card={expanded}
                    columns={columns}
                    env={env}
                    periodLabel={periodLabel}
                    strategy={strategy}
                    trayID={trayID}
                  />
                ),
              }
            : null
        }
        columns={columns}
        disabled={reorderDisabled}
        itemLabel={(card, position) =>
          t("packages.moveNamed", { name: cardName(card, t), position })
        }
        items={cards}
        label={t("packages.orderNamed", { section: title })}
        onChange={onReorder}
        renderItem={(card, handle) =>
          card.services.length > 1 ? (
            <PackageStackCard
              card={card}
              env={env}
              expanded={card.id === expanded?.id}
              handle={handle}
              onToggle={() => onToggleExpanded(card.id)}
              periodLabel={periodLabel}
              trayID={trayID}
            />
          ) : (
            <ServiceCard
              env={env}
              handle={handle}
              packageID={card.id}
              periodLabel={periodLabel}
              service={card.services[0]}
            />
          )
        }
        trailing={trailing}
      />
    </section>
  );
}

/**
 * The provider page body: subscription packages, then relays and keys, each
 * as a grid of equal cards in routing order. Dragging a card moves all of a
 * package's accounts together; `onReorder` receives the full service order.
 */
export function ServicePackageGrid({
  services,
  packages,
  query,
  filter,
  usage,
  performance,
  preset,
  billing,
  estimates,
  ready,
  reorderDisabled,
  actingID,
  refreshing,
  now,
  strategy,
  actions,
  onReorder,
  scrollerRef,
}: {
  /** All services in routing order. */
  services: Service[];
  packages: ServicePackage[] | null;
  query: string;
  filter: ServiceFilter;
  usage: Record<string, UsageEntry | undefined>;
  performance: CardEnv["performance"];
  preset: UsageRangePreset;
  billing: CardBilling;
  /** Estimated whole quota windows, from Core. */
  estimates: ServiceQuotaEstimate[];
  ready: boolean;
  reorderDisabled: boolean;
  actingID: string | null;
  refreshing: ReadonlySet<string>;
  now: Date;
  strategy: StrategyControl;
  actions: ServiceCardActions;
  onReorder: (services: Service[]) => void;
  scrollerRef: RefObject<HTMLDivElement | null>;
}) {
  const t = useT();
  const mode = useQuotaDisplayMode();
  useDisplayCurrency();
  const trayID = useId();
  const [columns, setColumns] = useState(4);
  const [expandedID, setExpandedID] = useState<string | null>(null);
  const [showAllRelays, setShowAllRelays] = useState(false);

  useLayoutEffect(() => {
    const node = scrollerRef.current;
    if (!node) return;
    const measure = () => {
      if (node.clientWidth > 0) setColumns(columnsFor(node.clientWidth));
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [scrollerRef]);

  const allCards = packageCards(services, packages);
  // Packages are re-derived under new IDs, for example once a plan is read;
  // an expanded tray belongs to one package and closes when it goes away.
  const cardIDs = allCards.map((card) => card.id).join("\0");
  useEffect(() => {
    if (expandedID && !cardIDs.split("\0").includes(expandedID))
      setExpandedID(null);
  }, [cardIDs, expandedID]);

  const fields = (service: Service) => [
    service.name,
    serviceKindLabel(service.kind),
    service.http?.base_url,
    service.subscription?.account_hint,
    service.subscription?.provider_account_id,
    planTypeLabel(
      usage[service.id]?.usage?.plan_type,
      service.subscription?.provider,
    ),
    ...service.models,
  ];
  const filtered = !!query.trim() || filter !== "all";
  const visible = allCards.filter(
    (card) =>
      cardMatchesFilter(card, filter) && cardMatches(card, query, fields),
  );
  const subscriptions = visible.filter(
    (card) => card.section === "subscription",
  );
  const relays = visible.filter((card) => card.section === "relay");
  const relayLimit = columns * 2;
  const relaysCollapsed =
    !filtered && !showAllRelays && relays.length > relayLimit;
  const shownRelays = relaysCollapsed ? relays.slice(0, relayLimit) : relays;

  const env: CardEnv = {
    t,
    mode,
    now,
    usage,
    performance,
    preset,
    billing,
    totals: memberWindowTotals(allCards, estimates),
    ready,
    actingID,
    refreshing,
    actions,
  };
  const periodLabel = t(`overview.range.${preset}`);
  const reorder = (next: PackageCard[]) => {
    const result = reorderedServices(allCards, next);
    if (result) onReorder(result);
  };
  const section = {
    columns,
    env,
    periodLabel,
    reorderDisabled,
    onReorder: reorder,
    expandedID,
    onToggleExpanded: (id: string) =>
      setExpandedID((current) => (current === id ? null : id)),
    strategy,
    trayID,
  };
  const relayServices = relays.flatMap((card) => card.services);

  return (
    <div
      className="gutter-scroller min-h-0 min-w-0 flex-1 overflow-y-auto"
      data-testid="service-list-scroller"
      ref={scrollerRef}
    >
      {visible.length === 0 ? (
        <EmptyState
          action={
            <Button
              onClick={actions.clearFilters}
              size="sm"
              type="button"
              variant="outline"
            >
              {t("services.clearFilters")}
            </Button>
          }
          description={t("packages.noSearchResults")}
          title={t("common.noSearchResults")}
        />
      ) : (
        <div className="grid gap-4 px-0.5 pb-3">
          {subscriptions.length > 0 ? (
            <PackageSection
              {...section}
              cards={subscriptions}
              summary={t("packages.subscriptionSummary", {
                cards: subscriptions.length,
                accounts: subscriptions.reduce(
                  (total, card) => total + card.services.length,
                  0,
                ),
              })}
              testID="package-section-subscription"
              title={t("packages.subscriptions")}
              trailing={
                <PackageAddSlot
                  disabled={!ready}
                  hint={t("packages.addSubscriptionHint")}
                  onClick={actions.addSubscription}
                  title={t("packages.addSubscription")}
                />
              }
            />
          ) : null}
          {relays.length > 0 ? (
            <PackageSection
              {...section}
              cards={shownRelays}
              more={
                !filtered && relays.length > relayLimit ? (
                  <Button
                    aria-expanded={!relaysCollapsed}
                    className="ml-auto h-auto shrink-0 gap-0.5 p-0 text-xs font-medium"
                    onClick={() => setShowAllRelays((value) => !value)}
                    type="button"
                    variant="link"
                  >
                    {relaysCollapsed
                      ? t("packages.showAll", { count: relays.length })
                      : t("packages.showLess")}
                    {relaysCollapsed ? (
                      <ChevronDown aria-hidden="true" className="size-3" />
                    ) : (
                      <ChevronUp aria-hidden="true" className="size-3" />
                    )}
                  </Button>
                ) : null
              }
              summary={t("packages.relaySummary", {
                count: relayServices.length,
                enabled: relayServices.filter((service) => service.enabled)
                  .length,
              })}
              testID="package-section-relay"
              title={t("packages.relays")}
              trailing={
                relaysCollapsed ? undefined : (
                  <PackageAddSlot
                    className={cn(columns === 1 && "min-h-24")}
                    disabled={!ready}
                    hint={t("packages.addRelayHint")}
                    onClick={actions.addRelay}
                    title={t("packages.addRelay")}
                  />
                )
              }
            />
          ) : null}
        </div>
      )}
    </div>
  );
}
