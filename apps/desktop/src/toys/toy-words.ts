// What the pet says about a member, today's tokens and time away, in the
// user's language.
import { clientTypeName } from "@/components/ClientTypeIcon";

import { formatCompactNumber } from "../format-compact-number";
import { i18n } from "../i18n";
import type { CrewWords } from "../live-crew-draw";
import type { CrewMember } from "../live-crew-model";
import { waving } from "./toy-kit";

export const nameOf = clientTypeName;

export const words = (): CrewWords => ({
  failover: i18n.t("overview.liveCrew.failover"),
  name: clientTypeName,
});
export const HIDDEN: CrewWords = { failover: "", name: () => "" };

export function fmt(value: number): string {
  return formatCompactNumber(Math.round(value), { short: true });
}

function times(m: CrewMember) {
  return m.count > 1
    ? i18n.t("desktopToys.acts.times", { count: m.count })
    : "";
}

/** What a member of the paced crew is doing, in words. */
export function labelOf(m: CrewMember): string {
  if (waving(m)) return i18n.t("desktopToys.waved");
  switch (m.act) {
    case "bash":
      return i18n.t("desktopToys.acts.bash", { tool: m.tool }) + times(m);
    case "read":
    case "edit":
      return i18n.t(`desktopToys.acts.${m.act}`) + times(m);
    case "write":
      return i18n.t("desktopToys.acts.write", { rate: Math.round(m.rate) });
    case "search":
    case "think":
    case "wait":
    case "leave":
      return i18n.t(`desktopToys.acts.${m.act}`);
    default:
      return "";
  }
}

export function duration(seconds: number): string {
  const m = Math.max(1, Math.round(seconds / 60));
  if (m < 60) return i18n.t("desktopToys.duration.minutes", { count: m });
  const h = Math.floor(m / 60);
  return m % 60
    ? i18n.t("desktopToys.duration.hoursMinutes", { hours: h, minutes: m % 60 })
    : i18n.t("desktopToys.duration.hours", { count: h });
}
