import { UsageMeter } from "./UsageMeter";
import { useT } from "../i18n";
import { useQuotaDisplayMode } from "../quota-display";
import { usageBarPercent, usageWindowTone } from "../subscription-usage-model";

/** All subscription surfaces share the display mode; warnings still follow usage. */
export function SubscriptionQuotaMeter({
  label,
  caption,
  usedPercent,
  limitReached,
  compact = false,
}: {
  label: string;
  caption?: string | null;
  usedPercent: number;
  limitReached?: boolean;
  compact?: boolean;
}) {
  const t = useT();
  const mode = useQuotaDisplayMode();
  const used = usageBarPercent(usedPercent);
  const value = mode === "remaining" ? 100 - used : used;
  const tone = usageWindowTone(usedPercent, limitReached);
  return (
    <UsageMeter
      caption={caption}
      compact={compact}
      label={label}
      value={value}
      valueLabel={t(
        mode === "remaining" ? "usage.remainingPercent" : "usage.usedPercent",
        {
          percent: Math.round(value),
        },
      )}
      warning={
        limitReached || used >= 100 ? t("usage.limitReached") : undefined
      }
      tone={
        tone === "ok"
          ? "success"
          : tone === "critical"
            ? "destructive"
            : "warning"
      }
    />
  );
}
