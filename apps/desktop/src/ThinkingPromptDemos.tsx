import { Fragment } from "react";
import { motion, useReducedMotion } from "motion/react";

import {
  Hourglass,
  LockKeyhole,
  MessageCircle,
  Terminal,
} from "./components/icons";
import { sceneFrameClass } from "./components/SettingSceneBody";
import {
  adaptiveRange,
  reasoningLevels,
  type LevelRange,
  type ReasoningLevel,
  type RoutingSettings,
} from "./failure-policy-model";
import { useT } from "./i18n";
import { cn } from "./lib/utils";

const rank = (level: ReasoningLevel) => reasoningLevels.indexOf(level);
const topRank = reasoningLevels.length - 1;

/**
 * Where a request with hits signals lands inside range, as the gateway picks
 * it: the lower end without signals, a level higher for each, and the upper
 * end for all three.
 */
function rangeLevel(range: LevelRange, hits: number) {
  const top = rank(range[1]);
  return reasoningLevels[
    hits >= 3 ? top : Math.min(rank(range[0]) + hits, top)
  ];
}

/** Three example requests: the signals each shows and the level its client picks. */
const requestCards = [
  { id: "quick", hits: 0, pick: "low", Icon: MessageCircle },
  { id: "bug", hits: 1, pick: "high", Icon: Terminal },
  { id: "afternoon", hits: 3, pick: "medium", Icon: Hourglass },
] as const;

/**
 * The level each example request is sent with, and the levels the setting
 * allows: none when following the client, one when fixed, the range when
 * adaptive.
 */
export function thinkingSceneLevels(settings: RoutingSettings) {
  const mode = settings.reasoning_mode ?? "passthrough";
  if (mode === "passthrough")
    return {
      allowed: null,
      cards: requestCards.map((card) => ({
        ...card,
        level: card.pick as ReasoningLevel,
      })),
    };
  if (mode === "fixed") {
    const level = settings.reasoning_level ?? "medium";
    return {
      allowed: [level, level] as LevelRange,
      cards: requestCards.map((card) => ({ ...card, level })),
    };
  }
  const range = adaptiveRange(settings);
  return {
    allowed: range,
    cards: requestCards.map((card) => ({
      ...card,
      level: rangeLevel(range, card.hits),
    })),
  };
}

/** Two cards share a row when they cannot touch; one at an end of the scale reaches further in. */
function clearOfEachOther(a: number, b: number) {
  const atEnd = [a, b].some((at) => at === 0 || at === topRank);
  return Math.abs(a - b) >= (atEnd ? 3 : 2);
}

/** The row of each level's card, the first that has room for it. */
export function thinkingCardRows(levels: readonly ReasoningLevel[]) {
  const rows: number[][] = [];
  return levels.map((level) => {
    const at = rank(level);
    let row = rows.findIndex((taken) =>
      taken.every((other) => clearOfEachOther(at, other)),
    );
    if (row < 0) row = rows.push([]) - 1;
    rows[row].push(at);
    return row;
  });
}

const cardHeight = 24;
const rowPitch = cardHeight + 2;
const axisBottom = 26;
const rowTop = (row: number) => 5 + row * rowPitch;
// A card hangs centred from its level, or flush with the frame's side at an
// end of the scale, so the ends stay inside the frame.
const cardShift = (level: ReasoningLevel) =>
  rank(level) === 0 ? "-14%" : rank(level) === topRank ? "-86%" : "-50%";
const levelLeft = (level: ReasoningLevel) =>
  `${(rank(level) / topRank) * 100}%`;

/**
 * The level scale with the example requests hanging over the level each is
 * sent with. The range the setting allows is marked on the scale, and the
 * levels in use stand out. A change slides the cards to their new levels.
 */
export function ThinkingScene({ settings }: { settings: RoutingSettings }) {
  const t = useT();
  const reducedMotion = useReducedMotion();
  const { allowed, cards } = thinkingSceneLevels(settings);
  const rows = thinkingCardRows(cards.map((card) => card.level));
  const rowCount = Math.max(...rows) + 1;
  const move = {
    duration: reducedMotion ? 0 : 0.35,
    ease: "easeInOut",
  } as const;
  const inUse = (level: ReasoningLevel) =>
    allowed === null
      ? cards.some((card) => card.level === level)
      : rank(level) >= rank(allowed[0]) && rank(level) <= rank(allowed[1]);
  return (
    <div
      aria-hidden="true"
      className={cn(
        sceneFrameClass,
        "relative overflow-hidden transition-[min-height] duration-300 motion-reduce:transition-none",
      )}
      data-allowed={allowed ? `${allowed[0]}..${allowed[1]}` : undefined}
      data-visual="levels"
      style={{ minHeight: rowCount * rowPitch + 46 }}
    >
      <div className="absolute inset-y-0 right-10 left-10">
        <span
          className="absolute inset-x-0 h-1 rounded-full bg-border"
          style={{ bottom: axisBottom }}
        />
        {allowed ? (
          <motion.span
            animate={{
              left: levelLeft(allowed[0]),
              width: `${((rank(allowed[1]) - rank(allowed[0])) / topRank) * 100}%`,
            }}
            className="absolute h-1 rounded-full bg-warning"
            initial={false}
            style={{ bottom: axisBottom }}
            transition={move}
          />
        ) : null}
        {reasoningLevels.map((level) => (
          <Fragment key={level}>
            <span
              className={cn(
                "absolute size-2.5 -translate-x-1/2 rounded-full bg-card ring-2 transition-shadow",
                inUse(level) ? "ring-warning" : "ring-muted-foreground/25",
              )}
              style={{ bottom: axisBottom - 3, left: levelLeft(level) }}
            />
            <span
              className={cn(
                "absolute -translate-x-1/2 text-micro whitespace-nowrap",
                inUse(level)
                  ? "font-semibold text-warning-foreground"
                  : "text-muted-foreground",
              )}
              style={{ bottom: 4, left: levelLeft(level) }}
            >
              {t(`routing.thinkingLevels.${level}`)}
            </span>
          </Fragment>
        ))}
        {cards.map((card, index) => {
          const top = rowTop(rows[index]);
          return (
            <Fragment key={card.id}>
              <motion.span
                animate={{ left: levelLeft(card.level), top: top + cardHeight }}
                className="absolute w-px bg-muted-foreground/35"
                initial={false}
                style={{ bottom: axisBottom + 7 }}
                transition={move}
              />
              <motion.span
                animate={{
                  left: levelLeft(card.level),
                  top,
                  x: cardShift(card.level),
                }}
                className="absolute flex h-6 items-center gap-1.5 rounded-md border bg-card px-2 text-micro whitespace-nowrap text-foreground shadow-xs"
                data-level={card.level}
                data-request={card.id}
                initial={false}
                transition={move}
              >
                <card.Icon
                  animateOnHover={false}
                  aria-hidden="true"
                  className="shrink-0 text-muted-foreground"
                  size={12}
                />
                {t(`routing.thinkingDemo.requests.${card.id}`)}
              </motion.span>
            </Fragment>
          );
        })}
      </div>
    </div>
  );
}

/** The thinking setting in a sentence or two, for the saved mode and range. */
export function thinkingSceneText(
  settings: RoutingSettings,
  t: ReturnType<typeof useT>,
) {
  const mode = settings.reasoning_mode ?? "passthrough";
  const level = settings.reasoning_level ?? "medium";
  if (mode === "fixed")
    return level === "off"
      ? t("routing.thinkingDemo.text.fixedOff")
      : t("routing.thinkingDemo.text.fixed", {
          level: t(`routing.thinkingLevels.${level}`),
        });
  if (mode === "adaptive")
    return t(
      `routing.thinkingDemo.text.${settings.adaptive_preset ?? "everyday"}`,
    );
  return t("routing.thinkingDemo.text.passthrough");
}

/** The first line of a saved prompt text. */
function excerpt(text: string) {
  return text.trim().split("\n")[0].trim();
}

/**
 * A place for one of the user's texts: filled with its first line when the
 * prompt is on and the text is set, a dashed outline around an example
 * otherwise.
 */
function PromptSlot({
  block,
  example,
  label,
  text,
}: {
  block: "prefix" | "suffix";
  example: string;
  label: string;
  text: string;
}) {
  return (
    <li
      className={cn(
        "flex min-w-0 items-baseline gap-1.5 rounded-sm border px-2 py-1 text-micro transition-colors",
        text
          ? "border-transparent bg-tide-wash text-accent-foreground shadow-[inset_2px_0_0_var(--tide)]"
          : "border-dashed text-muted-foreground",
      )}
      data-filled={text ? true : undefined}
      data-prompt-block={block}
    >
      <span className="shrink-0 font-semibold">{label}</span>
      <span className="truncate">{text || example}</span>
    </li>
  );
}

/**
 * The system prompt a model receives, block by block, with the user's two
 * texts in their places around the client's own prompt.
 */
export function PromptScene({
  enabled,
  prefix,
  suffix,
}: {
  enabled: boolean;
  prefix: string;
  suffix: string;
}) {
  const t = useT();
  return (
    <section
      aria-hidden="true"
      className={cn(sceneFrameClass, "grid content-start gap-1.5 px-3 py-2.5")}
      data-prompt={enabled ? "on" : "off"}
      data-visual="prompt"
    >
      <h3 className="m-0 text-micro font-normal text-muted-foreground">
        {t("routing.promptDemo.title")}
      </h3>
      <ol className="m-0 grid list-none gap-1.5 p-0">
        <PromptSlot
          block="prefix"
          example={t("routing.promptPrefixPlaceholder")}
          label={t("routing.promptDemo.prefix")}
          text={enabled ? excerpt(prefix) : ""}
        />
        <li
          className="grid min-w-0 gap-1.5 rounded-sm border border-dashed px-2 py-1.5 text-micro text-muted-foreground"
          data-prompt-block="client"
        >
          <span className="flex min-w-0 items-center gap-1">
            <LockKeyhole
              animateOnHover={false}
              aria-hidden="true"
              className="shrink-0"
              size={11}
            />
            <span className="truncate">{t("routing.promptDemo.client")}</span>
          </span>
          <span className="h-1.5 w-[92%] rounded-full bg-border" />
          <span className="h-1.5 w-[64%] rounded-full bg-border" />
        </li>
        <PromptSlot
          block="suffix"
          example={t("routing.promptSuffixPlaceholder")}
          label={t("routing.promptDemo.suffix")}
          text={enabled ? excerpt(suffix) : ""}
        />
      </ol>
    </section>
  );
}
