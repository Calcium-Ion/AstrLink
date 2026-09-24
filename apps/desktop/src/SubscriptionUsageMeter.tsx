import { RefreshCw, RotateCcw } from "@/components/icons";
import { useT } from "./i18n";

import { IconButton } from "@/components/IconButton";
import { Button } from "@/components/ui/button";
import { SubscriptionQuotaMeter } from "@/components/SubscriptionQuotaMeter";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { UsageMeterPlaceholder } from "@/components/UsageMeter";

import {
  formatQuotaExpiry,
  formatQuotaUSD,
  formatResetCountdown,
  quotaUsedPercent,
  usageWindowTone,
  usageBarPercent,
  windowLabel,
  type AdditionalRateLimit,
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

  const extras = usage.additional_rate_limits ?? [];
  return (
    <div className="grid min-w-0 gap-2" data-testid="subscription-usage">
      {usage.primary || usage.secondary ? (
        <div className="grid gap-2">
          <UsageWindowRow
            compact={Boolean(usage.primary && usage.secondary)}
            limitReached={usage.limit_reached}
            now={now}
            window={usage.primary}
            isSecondary={false}
          />
          <UsageWindowRow
            compact={Boolean(usage.primary && usage.secondary)}
            limitReached={usage.limit_reached}
            now={now}
            window={usage.secondary}
            isSecondary
          />
        </div>
      ) : usage.quota ? (
        <QuotaRow
          limitReached={usage.limit_reached}
          now={now}
          quota={usage.quota}
        />
      ) : usage.limit_reached ? (
        <p className="text-micro text-destructive">{t("usage.limitReached")}</p>
      ) : null}
      {extras.length > 0 ? (
        <div className="grid gap-2.5" data-testid="subscription-usage-extras">
          {extras.map((extra) => (
            <div
              className="grid min-w-0 gap-2 border-t pt-2"
              key={extra.limit_name}
            >
              <AdditionalLimitRows extra={extra} now={now} />
            </div>
          ))}
        </div>
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
  if (resetCount <= 0) return null;
  if (!onReset) {
    return (
      <p className="text-xs text-muted-foreground">
        {t("usage.resetAvailable", { count: resetCount })}
      </p>
    );
  }
  return (
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
        : t("usage.resetCount", { count: resetCount })}
    </Button>
  );
}

function AdditionalLimitRows({
  extra,
  now,
}: {
  extra: AdditionalRateLimit;
  now: Date;
}) {
  return (
    <>
      <p className="text-micro font-medium text-muted-foreground break-words [overflow-wrap:anywhere]">
        {extra.limit_name}
      </p>
      <UsageWindowRow
        compact={Boolean(extra.primary && extra.secondary)}
        now={now}
        window={extra.primary}
        isSecondary={false}
      />
      <UsageWindowRow
        compact={Boolean(extra.primary && extra.secondary)}
        now={now}
        window={extra.secondary}
        isSecondary
      />
    </>
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
    // An unlimited key has nothing to fill a bar against; its spend takes the
    // value slot a metered row gives its percentage.
    return (
      <div
        className="grid min-w-0 gap-1.5"
        data-testid="subscription-usage-quota"
      >
        <div className="flex min-w-0 items-center justify-between gap-2 text-xs">
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
  now,
  window,
}: {
  compact?: boolean;
  isSecondary: boolean;
  limitReached?: boolean;
  now: Date;
  window?: RateLimitWindow;
}) {
  if (!window) return null;
  const label = windowLabel(window.limit_window_seconds, isSecondary);
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
