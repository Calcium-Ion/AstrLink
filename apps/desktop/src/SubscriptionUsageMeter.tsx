import { RotateCcw } from "@/components/icons";
import { useT } from "./i18n";

import { Button } from "@/components/ui/button";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { UsageMeter } from "@/components/UsageMeter";

import { formatUSD } from "./pricing-model";
import {
  formatQuotaExpiry,
  formatResetCountdown,
  quotaUsedPercent,
  usageWindowTone,
  usageBarPercent,
  windowLabel,
  type AdditionalRateLimit,
  type RateLimitWindow,
  type SubscriptionUsage,
  type UsageQuota,
  type UsageWindowTone,
} from "./subscription-usage-model";

export type SubscriptionUsageStatus = "loading" | "ready" | "error";

export function SubscriptionUsageMeter({
  error,
  now,
  status,
  usage,
}: {
  error?: string;
  now: Date;
  status: SubscriptionUsageStatus;
  usage?: SubscriptionUsage;
}) {
  const t = useT();
  if (status === "loading" && !usage) {
    return (
      <div
        aria-busy="true"
        className="grid gap-2"
        data-testid="subscription-usage"
      >
        {[0, 1].map((index) => (
          <div aria-hidden="true" className="grid gap-1.5" key={index}>
            <div className="flex items-center justify-between">
              <span className="h-3 w-12 animate-pulse rounded-sm bg-muted motion-reduce:animate-none" />
              <span className="h-3 w-8 animate-pulse rounded-sm bg-muted motion-reduce:animate-none" />
            </div>
            <span className="h-1 animate-pulse rounded-full bg-muted motion-reduce:animate-none" />
            <span className="h-2.5 w-20 animate-pulse rounded-sm bg-muted motion-reduce:animate-none" />
          </div>
        ))}
      </div>
    );
  }
  if (status === "error" && !usage) {
    return (
      <div data-testid="subscription-usage">
        {error ? (
          <HelpDisclosure title={t("usage.readFailed")} tone="warning">
            <p className="text-micro break-all">{error}</p>
          </HelpDisclosure>
        ) : (
          <p className="text-xs text-warning-foreground">
            {t("usage.readFailed")}
          </p>
        )}
      </div>
    );
  }
  if (!usage) return null;

  const extras = usage.additional_rate_limits ?? [];
  return (
    <div className="grid min-w-0 gap-2" data-testid="subscription-usage">
      {usage.primary || usage.secondary ? (
        <div className="grid gap-2.5">
          <UsageWindowRow
            limitReached={usage.limit_reached}
            now={now}
            window={usage.primary}
            isSecondary={false}
          />
          <UsageWindowRow
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
      <UsageWindowRow now={now} window={extra.primary} isSecondary={false} />
      <UsageWindowRow now={now} window={extra.secondary} isSecondary />
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
    // An unlimited key has nothing to fill a bar against; show its spend only.
    return (
      <div
        className="grid min-w-0 gap-1.5"
        data-testid="subscription-usage-quota"
      >
        <div className="flex min-w-0 items-center justify-between gap-2 text-xs">
          <span className="min-w-0 truncate" title={label}>
            {label}
          </span>
          <span className="shrink-0 font-medium">
            {t("usage.quotaUnlimited")}
          </span>
        </div>
        <p className="text-micro text-muted-foreground">
          {[t("usage.quotaUsed", { amount: formatUSD(quota.used_usd) }), expiry]
            .filter(Boolean)
            .join(" · ")}
        </p>
      </div>
    );
  }
  const usedPercent = usageBarPercent(quotaUsedPercent(quota));
  const tone = usageWindowTone(usedPercent, limitReached);
  return (
    <div data-testid="subscription-usage-quota" data-tone={tone}>
      <UsageMeter
        caption={[
          t("usage.quotaRemaining", {
            remaining: formatUSD(quota.remaining_usd),
            total: formatUSD(quota.total_usd),
          }),
          expiry,
        ]
          .filter(Boolean)
          .join(" · ")}
        label={label}
        valueLabel={t("usage.usedPercent", {
          percent: Math.round(usedPercent),
        })}
        warning={
          limitReached || usedPercent >= 100
            ? t("usage.limitReached")
            : undefined
        }
        tone={meterTone(tone)}
        value={usedPercent}
      />
    </div>
  );
}

function meterTone(tone: UsageWindowTone) {
  if (tone === "ok") return "success";
  if (tone === "critical") return "destructive";
  return "warning";
}

function UsageWindowRow({
  isSecondary,
  limitReached,
  now,
  window,
}: {
  isSecondary: boolean;
  limitReached?: boolean;
  now: Date;
  window?: RateLimitWindow;
}) {
  const t = useT();
  if (!window) return null;
  const label = windowLabel(window.limit_window_seconds, isSecondary);
  const reset = formatResetCountdown(window, now);
  const tone = usageWindowTone(window.used_percent, limitReached);
  const usedPercent = usageBarPercent(window.used_percent);
  return (
    <div data-tone={tone}>
      <UsageMeter
        caption={reset}
        label={label}
        valueLabel={t("usage.usedPercent", {
          percent: Math.round(usedPercent),
        })}
        warning={
          limitReached || usedPercent >= 100
            ? t("usage.limitReached")
            : undefined
        }
        tone={meterTone(tone)}
        value={usedPercent}
      />
    </div>
  );
}
