import { useId } from "react";
import { RefreshCw, RotateCcw } from "@/components/icons";
import { useT } from "./i18n";
import { useResetCredits } from "./use-reset-credits";
import { StatusBadge } from "@/components/StatusBadge";

import { IconButton } from "@/components/IconButton";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { SubscriptionQuotaMeter } from "@/components/SubscriptionQuotaMeter";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { UsageMeterPlaceholder } from "@/components/UsageMeter";

import {
  formatQuotaExpiry,
  formatQuotaUSD,
  formatResetCountdown,
  formatResetCreditExpiry,
  resetCreditExpiryWarning,
  quotaUsedPercent,
  usageWindowTone,
  usageBarPercent,
  windowLabel,
  type RateLimitWindow,
  type SubscriptionUsage,
  type UsageQuota,
} from "./subscription-usage-model";

export type SubscriptionUsageStatus = "loading" | "ready" | "error";

export function SubscriptionUsageMeter({
  error,
  now,
  onRefresh,
  refreshing = false,
  status,
  usage,
}: {
  error?: string;
  now: Date;
  onRefresh?: () => void;
  refreshing?: boolean;
  status: SubscriptionUsageStatus;
  usage?: SubscriptionUsage;
}) {
  const t = useT();
  const titleId = useId();
  const summaryId = `${titleId}-summary`;
  if (status === "loading" && !usage) {
    return (
      <div
        aria-busy="true"
        className="grid min-w-0 gap-2"
        data-testid="subscription-usage"
      >
        <span className="sr-only">{t("common.loading")}</span>
        <UsageMeterPlaceholder />
      </div>
    );
  }
  if (status === "error" && !usage) {
    return (
      <div
        className="flex min-w-0 items-start gap-1 [&_summary]:min-h-5.5"
        data-testid="subscription-usage"
      >
        {error ? (
          <HelpDisclosure title={t("usage.readFailed")} tone="warning">
            <p className="text-micro break-all">{error}</p>
          </HelpDisclosure>
        ) : (
          <p className="flex min-h-5.5 items-center text-xs text-warning-foreground">
            {t("usage.readFailed")}
          </p>
        )}
        {onRefresh ? (
          <IconButton
            aria-busy={refreshing || undefined}
            className="text-muted-foreground"
            disabled={refreshing}
            label={refreshing ? t("common.refreshing") : t("common.refresh")}
            onClick={onRefresh}
            size="icon-xs"
            type="button"
          >
            <RefreshCw
              aria-hidden="true"
              className={
                refreshing
                  ? "animate-spin motion-reduce:animate-none"
                  : undefined
              }
            />
          </IconButton>
        ) : null}
      </div>
    );
  }
  if (!usage) return null;

  const windows = [
    { limit_name: "", primary: usage.primary, secondary: usage.secondary },
    ...(usage.additional_rate_limits ?? []),
  ]
    .flatMap((limit, index) =>
      (["primary", "secondary"] as const).flatMap((kind) => {
        const window = limit[kind];
        return window
          ? [
              {
                key: `${index}-${kind}`,
                name: limit.limit_name,
                window,
                isSecondary: kind === "secondary",
                limitReached: index === 0 ? usage.limit_reached : undefined,
              },
            ]
          : [];
      }),
    )
    .sort(
      (a, b) =>
        Number(b.window.used_percent > 0) - Number(a.window.used_percent > 0),
    );
  const summary = windows
    .slice(0, 2)
    .map(({ key, ...row }) => (
      <UsageWindowRow
        key={key}
        {...row}
        compact={windows.length > 1}
        now={now}
      />
    ));
  return (
    <div className="grid min-w-0 gap-2" data-testid="subscription-usage">
      {windows.length > 2 ? (
        <Popover>
          <div className="relative min-w-0">
            <div id={summaryId} className="grid gap-2">
              {summary}
            </div>
            <PopoverTrigger asChild>
              <Button
                aria-label={t("usage.viewAllLimits", { count: windows.length })}
                aria-describedby={summaryId}
                title={t("usage.viewAllLimits", { count: windows.length })}
                className="absolute inset-0 h-full w-full cursor-pointer rounded-sm p-0 hover:bg-transparent hover:ring-1 hover:ring-border"
                variant="ghost"
                type="button"
              />
            </PopoverTrigger>
          </div>
          <PopoverContent
            align="start"
            className="w-80 max-h-[min(24rem,var(--radix-popover-content-available-height))]"
            aria-labelledby={titleId}
          >
            <h3 id={titleId} className="mb-3 text-sm font-semibold">
              {t("usage.allLimits")}
            </h3>
            <div className="grid gap-3">
              {windows.map(({ key, ...row }) => (
                <UsageWindowRow key={key} {...row} now={now} />
              ))}
            </div>
          </PopoverContent>
        </Popover>
      ) : windows.length > 0 ? (
        summary
      ) : usage.quota ? (
        <QuotaRow
          limitReached={usage.limit_reached}
          now={now}
          quota={usage.quota}
        />
      ) : usage.limit_reached ? (
        <p className="text-micro text-destructive">{t("usage.limitReached")}</p>
      ) : null}
    </div>
  );
}

export function SubscriptionResetButton({
  onReset,
  resetting = false,
  usage,
}: {
  onReset?: () => void;
  resetting?: boolean;
  usage?: SubscriptionUsage;
}) {
  const t = useT();
  const resetCount = usage?.rate_limit_reset_credits?.available_count ?? 0;
  const { state, now } = useResetCredits(
    onReset && resetCount > 0 ? usage?.service_id : undefined,
    resetCount,
  );
  const warning =
    state.status === "ready"
      ? resetCreditExpiryWarning(state.details, now)
      : null;
  const displayCount =
    state.status === "ready" ? state.details.available_count : resetCount;
  if (displayCount <= 0) return null;
  if (!onReset) {
    return (
      <p className="text-xs text-muted-foreground">
        {t("usage.resetAvailable", { count: displayCount })}
      </p>
    );
  }
  return (
    <div className="flex max-w-full flex-wrap items-center gap-1.5">
      <Button
        data-testid="subscription-usage-reset"
        disabled={resetting}
        onClick={onReset}
        size="xs"
        type="button"
        variant="outline"
      >
        <RotateCcw aria-hidden="true" />
        {resetting
          ? t("usage.resetting")
          : t("usage.resetCount", { count: displayCount })}
      </Button>
      {warning ? (
        <Button
          size="xs"
          variant="ghost"
          className="h-auto min-w-0 max-w-full p-0"
          disabled={resetting}
          onClick={onReset}
          title={t("usage.resetExpiryOpen")}
        >
          <StatusBadge
            tone={warning.urgent ? "negative" : "pending"}
            className="whitespace-normal text-left"
          >
            {t("usage.resetExpiryWarning", {
              count: warning.count,
              time: formatResetCreditExpiry(warning.expiresAt, now),
            })}
          </StatusBadge>
        </Button>
      ) : state.status === "error" ? (
        <span className="text-micro text-muted-foreground">
          {t("usage.resetExpiryUnavailable")}
        </span>
      ) : null}
    </div>
  );
}

function QuotaRow({
  limitReached,
  now,
  quota,
}: {
  limitReached?: boolean;
  now: Date;
  quota: UsageQuota;
}) {
  const t = useT();
  const label = t("usage.keyQuota");
  const expiry = formatQuotaExpiry(quota, now);
  if (quota.unlimited) {
    // An unlimited key has no bar to span the width, so its spend follows the
    // label instead of being pushed to the far edge.
    return (
      <div
        className="grid min-w-0 gap-1.5"
        data-testid="subscription-usage-quota"
      >
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs">
          <span className="min-w-0 truncate" title={label}>
            {label}
          </span>
          <span className="shrink-0 font-medium tabular-nums">
            {t("usage.quotaUsed", { amount: formatQuotaUSD(quota.used_usd) })}
          </span>
        </div>
        <p className="text-micro text-muted-foreground">
          {[t("usage.quotaUnlimited"), expiry].filter(Boolean).join(" · ")}
        </p>
      </div>
    );
  }
  const usedPercent = usageBarPercent(quotaUsedPercent(quota));
  const tone = usageWindowTone(usedPercent, limitReached);
  return (
    <div data-testid="subscription-usage-quota" data-tone={tone}>
      <SubscriptionQuotaMeter
        caption={[
          t("usage.quotaRemaining", {
            remaining: formatQuotaUSD(quota.remaining_usd),
            total: formatQuotaUSD(quota.total_usd),
          }),
          expiry,
        ]
          .filter(Boolean)
          .join(" · ")}
        label={label}
        limitReached={limitReached}
        usedPercent={usedPercent}
      />
    </div>
  );
}

function UsageWindowRow({
  compact = false,
  isSecondary,
  limitReached,
  name,
  now,
  window,
}: {
  compact?: boolean;
  isSecondary: boolean;
  limitReached?: boolean;
  name?: string;
  now: Date;
  window?: RateLimitWindow;
}) {
  if (!window) return null;
  const period = windowLabel(window.limit_window_seconds, isSecondary);
  const label = name
    ? window.limit_window_seconds || isSecondary
      ? `${name} · ${period}`
      : name
    : period;
  const reset = formatResetCountdown(window, now);
  const tone = usageWindowTone(window.used_percent, limitReached);
  const usedPercent = usageBarPercent(window.used_percent);
  return (
    <div data-tone={tone}>
      <SubscriptionQuotaMeter
        caption={reset}
        compact={compact}
        label={label}
        limitReached={limitReached}
        usedPercent={usedPercent}
      />
    </div>
  );
}
