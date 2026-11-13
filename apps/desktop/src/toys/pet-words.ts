// What the big pet's bubbles say, in the user's language.
import { i18n } from "../i18n";
import type { Say } from "./pet-brain";
import { duration, fmt, nameOf } from "./toy-words";

export interface Words {
  title: string;
  text: string;
  /** Opens the conversations in AstrLink. */
  action?: string;
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
      return {
        title: t("desktopToys.pet.today", { count: say.replies }),
        text: line([
          tokens(say.tokens),
          t("desktopToys.pet.pats", { count: say.pats }),
        ]),
      };
    case "poked":
      return { title: t("desktopToys.pet.poked"), text: "" };
  }
}
