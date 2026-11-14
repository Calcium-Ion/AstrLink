import { i18n } from "@/i18n";

/** How long ago `from` was, at `now`: just now, minutes, hours or days. */
export function formatAgo(from: Date, now: Date): string {
  const seconds = Math.max(
    0,
    Math.floor((now.getTime() - from.getTime()) / 1000),
  );
  if (seconds < 60) return i18n.t("common.justNow");
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return i18n.t("common.minutesAgo", { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return i18n.t("common.hoursAgo", { count: hours });
  return i18n.t("common.daysAgo", { count: Math.floor(hours / 24) });
}
