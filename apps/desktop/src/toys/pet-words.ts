// What the big pet's bubbles say, in the user's language.
import { i18n } from "../i18n";
import { CREW_STAGES } from "../live-crew-model";
import type { DaySum, Say } from "./pet-brain";
import { duration, fmt, nameOf } from "./toy-words";

export interface Words {
  title: string;
  text: string;
  /** Opens the conversations in AstrLink. */
  action?: string;
  /** The day's summary: more lines under the text, and its hours. */
  day?: DayWords;
}

export interface DayWords {
  lines: string[];
  /** Tokens of each local hour, 0 to 23, and the hour now. */
  hours: number[];
  hour: number;
}

const clock = (minutes: number) =>
  `${Math.floor(minutes / 60)}:${String(minutes % 60).padStart(2, "0")}`;

/** The day's summary, as lines she says. */
export function dayWords(sum: DaySum): Words {
  const t = i18n.t.bind(i18n);
  const lines: string[] = [];
  const last = sum.marks.at(-1);
  if (last)
    lines.push(
      t("desktopToys.pet.day.passed", {
        time: clock(last.at),
        amount: fmt(last.tokens),
      }),
    );
  const next = CREW_STAGES.find((stage) => stage > (sum.tokens ?? 0));
  if (sum.tokens !== null && next)
    lines.push(
      t("desktopToys.pet.day.next", {
        target: fmt(next),
        amount: fmt(next - sum.tokens),
      }),
    );
  const peak = Math.max(0, ...sum.hours);
  if (peak > 0) {
    const hour = sum.hours.indexOf(peak);
    lines.push(
      t("desktopToys.pet.day.peak", {
        from: hour,
        to: hour + 1,
        amount: fmt(peak),
      }),
    );
  }
  if (sum.client && sum.model)
    lines.push(
      t("desktopToys.pet.day.busiest", {
        name: nameOf(sum.client),
        model: sum.model,
      }),
    );
  else if (sum.model)
    lines.push(t("desktopToys.pet.day.model", { model: sum.model }));
  const words = [
    sum.tokens === null
      ? null
      : t("desktopToys.away.tokens", { amount: fmt(sum.tokens) }),
    t("desktopToys.pet.pats", { count: sum.pats }),
  ].filter((part): part is string => Boolean(part));
  return {
    title: t(
      sum.replies > 0 ? "desktopToys.pet.today" : "desktopToys.pet.day.none",
      { count: sum.replies },
    ),
    text: words.join(" · "),
    day: {
      lines,
      hours: peak > 0 && sum.hours.length === 24 ? sum.hours : [],
      hour: sum.hour,
    },
  };
}

export function petWords(say: Say): Words {
  const t = i18n.t.bind(i18n);
  const look = t("desktopToys.pet.look");
  const tokens = (amount: number | null) =>
    amount === null
      ? null
      : t("desktopToys.away.tokens", { amount: fmt(amount) });
  const line = (parts: Array<string | null>) =>
    parts.filter((part): part is string => Boolean(part)).join(" · ");
  switch (say.kind) {
    case "reply":
      return {
        title: t("desktopToys.pet.replied", { name: nameOf(say.client) }),
        text: t("desktopToys.acts.wait"),
        action: look,
      };
    case "failover":
      return {
        title: t("desktopToys.pet.failover", { name: nameOf(say.client) }),
        text: t("desktopToys.pet.failoverModel", { model: say.model }),
        action: look,
      };
    case "milestone":
      return {
        title: t("desktopToys.milestone", { amount: fmt(say.tokens) }),
        text: say.gear ? t(`desktopToys.pet.gear.${say.gear}`) : "",
      };
    case "back":
      return {
        title: t("desktopToys.away.title", {
          duration: duration(say.seconds),
        }),
        text: line([
          t("desktopToys.away.replies", { count: say.replies }),
          tokens(say.tokens),
          say.waiting > 0
            ? t("desktopToys.away.waiting", { count: say.waiting })
            : null,
        ]),
        action: say.waiting > 0 ? look : undefined,
      };
    case "greet":
      return dayWords(say);
    case "poked":
      return { title: t("desktopToys.pet.poked"), text: "" };
  }
}
