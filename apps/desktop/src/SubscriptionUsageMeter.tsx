import { RotateCcw } from "@/components/icons";
import { useT } from "./i18n";

import { Button } from "@/components/ui/button";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { UsageMeter } from "@/components/UsageMeter";

import {
  formatResetCountdown,
  usageWindowTone,
  usageBarPercent,
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
  const resetCount = usage.rate_limit_reset_credits?.available_count ?? 0;
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
      ) : usage.limit_reached ? (
        <p className="text-micro text-destructive">
          {t("usage.limitReached")}
        </p>
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
            <RotateCcw aria-hidden="true" />
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
  const remainingPercent = 100 - usageBarPercent(window.used_percent);
  return (
    <div data-tone={tone}>
      <UsageMeter
        caption={reset}
        label={label}
        valueLabel={t("usage.remainingPercent", {
          percent: Math.round(remainingPercent),
        })}
        warning={
          limitReached || usageBarPercent(window.used_percent) >= 100
            ? t("usage.limitReached")
            : undefined
        }
        tone={
          tone === "ok"
            ? "success"
            : tone === "critical"
              ? "destructive"
              : "warning"
        }
        value={remainingPercent}
      />
    </div>
  );
}
