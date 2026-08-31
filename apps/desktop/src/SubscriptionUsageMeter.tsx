import { useState } from "react";
import { ChevronDown } from "lucide-react";
import { useT } from "./i18n";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import {
  extraLimitsSummary,
  formatResetCountdown,
  usageBarFillClass,
  usageBarPercent,
  usageBarTrackClass,
  usagePercentClass,
  usageWindowTone,
  windowLabel,
  type AdditionalRateLimit,
  type RateLimitWindow,
  type SubscriptionUsage,
} from "./subscription-usage-model";

export type SubscriptionUsageStatus = "loading" | "ready" | "error";

export function SubscriptionUsageMeter({
  error,
  now,
  onReset,
  resetting = false,
  status,
  usage,
}: {
  error?: string;
  now: Date;
  onReset?: () => void;
  resetting?: boolean;
  status: SubscriptionUsageStatus;
  usage?: SubscriptionUsage;
}) {
  const t = useT();
  const [extrasOpen, setExtrasOpen] = useState(false);
  if (status === "loading" && !usage) {
    return (
      <div
        aria-busy="true"
        className="mt-2 flex flex-col gap-1.5"
        data-testid="subscription-usage"
      >
        <span className="h-1 animate-pulse rounded-full bg-muted" />
        <span className="h-1 animate-pulse rounded-full bg-muted" />
      </div>
    );
  }
  if (status === "error" && !usage) {
    return (
      <p className="mt-2 text-xs text-destructive" data-testid="subscription-usage">
        {t("usage.readFailed")}
        {error ? (
          <span className="mt-0.5 block text-micro break-all text-muted-foreground">
            {error}
          </span>
        ) : null}
      </p>
    );
  }
  if (!usage) return null;

  const extras = usage.additional_rate_limits ?? [];
  const resetCount = usage.rate_limit_reset_credits?.available_count ?? 0;
  return (
    <div className="mt-2 flex flex-col gap-1.5" data-testid="subscription-usage">
      {usage.limit_reached ? (
        <p className="text-xs text-destructive">{t("usage.limitReached")}</p>
      ) : null}
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
      {extras.length > 0 ? (
        <div>
          <Button
            aria-expanded={extrasOpen}
            className="h-auto w-full justify-between gap-2 px-0 py-0 text-left text-xs font-normal text-muted-foreground hover:bg-transparent"
            data-testid="subscription-usage-extras"
            onClick={() => setExtrasOpen((open) => !open)}
            type="button"
            variant="ghost"
          >
            <span className="min-w-0 truncate">{extraLimitsSummary(extras)}</span>
            <ChevronDown
              aria-hidden="true"
              className={cn("size-3 shrink-0 transition-transform", extrasOpen && "rotate-180")}
            />
          </Button>
          {extrasOpen
            ? extras.map((extra) => (
                <div className="mt-1.5" key={extra.limit_name}>
                  <AdditionalLimitRows extra={extra} now={now} />
                </div>
              ))
            : null}
        </div>
      ) : null}
      {resetCount > 0 && onReset ? (
        <div className="pt-0.5">
          <Button
            data-testid="subscription-usage-reset"
            disabled={resetting}
            onClick={onReset}
            size="xs"
            type="button"
            variant="outline"
          >
            {resetting
              ? t("usage.resetting")
              : t("usage.resetCount", { count: resetCount })}
          </Button>
        </div>
      ) : resetCount > 0 ? (
        <p className="text-xs text-muted-foreground">
          {t("usage.resetAvailable", { count: resetCount })}
        </p>
      ) : null}
    </div>
  );
}

function AdditionalLimitRows({
  extra,
  now,
}: {
  extra: AdditionalRateLimit;
  now: Date;
}) {
  const t = useT();
  return (
    <>
      <UsageWindowRow
        heading={extra.limit_name}
        now={now}
        window={extra.primary}
        isSecondary={false}
      />
      <UsageWindowRow
        heading={t("usage.extraPeriod", { name: extra.limit_name })}
        now={now}
        window={extra.secondary}
        isSecondary
      />
    </>
  );
}

function UsageWindowRow({
  heading,
  isSecondary,
  limitReached,
  now,
  window,
}: {
  heading?: string;
  isSecondary: boolean;
  limitReached?: boolean;
  now: Date;
  window?: RateLimitWindow;
}) {
  const t = useT();
  if (!window) return null;
  const label = heading ?? windowLabel(window.limit_window_seconds, isSecondary);
  const reset = formatResetCountdown(window, now);
  const tone = usageWindowTone(window.used_percent, limitReached);
  const percent = Math.round(window.used_percent);
  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-2 text-xs">
        <span className="min-w-0 truncate text-muted-foreground">{label}</span>
        <span className={cn("shrink-0 tabular-nums", usagePercentClass(tone))}>
          {t("usage.usedPercent", { percent })}
        </span>
      </div>
      <span
        aria-hidden="true"
        className={cn(
          "mt-1 block h-1.5 overflow-hidden rounded-full",
          usageBarTrackClass(tone),
        )}
      >
        <span
          className={cn("block h-full rounded-full", usageBarFillClass(tone))}
          data-tone={tone}
          style={{ width: `${usageBarPercent(window.used_percent)}%` }}
        />
      </span>
      {reset ? (
        <p className="mt-0.5 text-micro text-muted-foreground">{reset}</p>
      ) : null}
    </div>
  );
}
