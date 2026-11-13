import { useEffect, useState } from "react";

import astrlinkLogo from "@/assets/astrlink-logo.svg";
import { HelpPopover } from "@/components/HelpPopover";
import { Plus } from "@/components/icons";
import {
  StarBody,
  StarMark,
  StarSphere,
  StarStage,
  bodySize,
  type StageMark,
  type StagePoint,
  type StageRay,
  type StageScene,
} from "@/components/StarStage";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { getUsageSummary } from "./bridge";
import { formatExactNumber } from "./format-compact-number";
import { useT } from "./i18n";
import {
  pluginWork,
  starPlugins,
  type StarPlugin,
} from "./overview-star-model";
import type { PluginSummary } from "./plugin-model";
import {
  PluginStarDetail,
  pluginStarColor,
  pluginStarFact,
} from "./PluginStarParts";
import type { Service } from "./service-model";
import { resolveUsageWindow, type UsageSummary } from "./usage-range";

type Side = "left" | "right";

// The system, in world units: AstrLink in the middle, the plugins that
// enhance its requests on the left half of their orbit, those that connect it
// to upstream providers on the right half.
const WORLD = { width: 1400, height: 900 };
const CENTER: StagePoint = { x: 700, y: 450 };
const ORBIT = { rx: 560, ry: 400 };
/** The arc each side's plugins spread over, in degrees, 90 nearest the viewer. */
const ARCS: Record<Side, [number, number]> = {
  left: [100, 260],
  right: [-80, 80],
};
/** How far the plane leans back: less than the overview's, for the room a list of planets needs. */
const TILT = 54;
/** A plugin's planet on screen: the least, and the most for the busiest. */
const PLANET = { least: 14, most: 32 };
/** The planet with its name beside it, or over or under it at the back and the front. */
const BODY = { width: 200, height: 38 };
const STACKED = { width: 150, caption: 32 };
const PILL = { width: 176, height: 28 };
const STAR = { width: 72, height: 72 };
/** The most moons a planet shows, and their sizes. */
const MOONS = { most: 4, least: 4, largest: 7 };
/** The headings over the map and the line under it. */
const INSET = { top: 40, right: 12, bottom: 36, left: 12 };

/**
 * Places along a side's arc, top to bottom on screen, each with where its
 * name goes: away from the star, over a planet at the back, under one at
 * the front, beside one at the side.
 */
function sidePoints(
  count: number,
  side: Side,
): Array<StagePoint & { caption: "above" | "below" | "before" | "after" }> {
  const [from, to] = ARCS[side];
  return (
    Array.from({ length: count }, (_, index) => {
      const degrees =
        count === 1
          ? (from + to) / 2
          : from + ((to - from) * index) / (count - 1);
      const angle = (degrees * Math.PI) / 180;
      const toward = Math.sin(angle);
      return {
        x: CENTER.x + ORBIT.rx * Math.cos(angle),
        y: CENTER.y + ORBIT.ry * toward,
        caption:
          toward < -0.6
            ? ("above" as const)
            : toward > 0.6
              ? ("below" as const)
              : side === "left"
                ? ("before" as const)
                : ("after" as const),
      };
    })
      // The left arc runs from the front; both list from the back.
      .sort((a, b) => a.y - b.y)
  );
}

/** A planet's box and the point of it on its place, by where its name goes. */
function bodyBox(caption: "above" | "below" | "before" | "after") {
  if (caption === "above" || caption === "below") {
    const height = PLANET.most + 4 + STACKED.caption;
    return {
      box: { width: STACKED.width, height },
      origin: {
        x: STACKED.width / 2,
        y: caption === "above" ? height - PLANET.most / 2 : PLANET.most / 2,
      },
    };
  }
  return {
    box: BODY,
    origin: {
      x: caption === "before" ? BODY.width - PLANET.most / 2 : PLANET.most / 2,
      y: BODY.height / 2,
    },
  };
}

/**
 * The plugins' last 30 days, read again whenever what they report changes:
 * their state, or their counts since the gateway started. Undefined until
 * the first read ends, null if it failed; a new read keeps the last.
 */
export function useRecentPluginUsage(
  isReady: boolean,
  plugins: readonly PluginSummary[],
): UsageSummary | null | undefined {
  const [usage, setUsage] = useState<UsageSummary | null>();
  const revision = plugins
    .map(
      (plugin) =>
        `${plugin.key}:${plugin.state}:${plugin.enabled}:${plugin.calls ?? 0}:${plugin.tasks ?? 0}`,
    )
    .join();
  useEffect(() => {
    if (!isReady) return;
    let live = true;
    getUsageSummary(resolveUsageWindow("30d", new Date()))
      .then((summary) => {
        if (live) setUsage(summary);
      })
      .catch(() => {
        // The map shows the plugins without what it could not read.
        if (live) setUsage(null);
      });
    return () => {
      live = false;
    };
  }, [isReady, revision]);
  return usage;
}

/**
 * The installed plugins as a star map: AstrLink as the star in the middle,
 * the plugins as its planets, those that enhance its requests on the left,
 * where requests come in, and those that connect it to upstream providers on
 * the right, with the providers that run one as its moons. A planet is the
 * larger the more it did over the last 30 days; sparks run to a plugin at
 * work, amber marks one that failed over those days, grey one that is off.
 * A plugin opens from its planet; the star opens the settings of every plugin.
 */
export function PluginStarMap({
  plugins,
  services,
  matches,
  paused,
  className,
  onOpen,
  onHub,
  onAddEnhancer,
  onAddProvider,
  onShowList,
  usage,
}: {
  plugins: PluginSummary[];
  /** The providers, for the moons of each provider plugin. */
  services: Service[];
  /** Whether a plugin matches the search; the others fade. */
  matches?: (plugin: PluginSummary) => boolean;
  /** Every plugin is paused: requests pass none of them. */
  paused: boolean;
  className?: string;
  onOpen: (key: string) => void;
  onHub: () => void;
  onAddEnhancer?: () => void;
  onAddProvider?: () => void;
  /** The list of every plugin, for a side with more than the map shows. */
  onShowList: () => void;
  /** The last 30 days, once read. */
  usage?: UsageSummary | null;
}) {
  const t = useT();
  const [focus, setFocus] = useState<string | null>(null);
  const stars = starPlugins(
    plugins,
    usage?.by_plugin ?? [],
    services,
    usage?.by_service ?? [],
  );
  // The plugins keep the order they run in.
  const sides: Record<Side, StarPlugin[]> = {
    left: stars.filter((plugin) => plugin.side === "enhancer"),
    right: stars.filter((plugin) => plugin.side === "provider"),
  };
  const busiest = Math.max(0, ...stars.map(pluginWork));
  const served = new Map(
    (usage?.by_service ?? []).map((group) => [
      group.id,
      group.requests + group.failed_requests,
    ]),
  );
  const moonsOf = (plugin: StarPlugin, color: string) => {
    const bound = services
      .filter(
        (service) =>
          service.kind === "plugin" &&
          service.plugin?.key === plugin.plugin.key,
      )
      .slice(0, MOONS.most);
    const most = Math.max(
      0,
      ...bound.map((service) => served.get(service.id) ?? 0),
    );
    return bound.map((service) => ({
      size: bodySize(served.get(service.id) ?? 0, most, {
        least: MOONS.least,
        most: MOONS.largest,
      }),
      color: `color-mix(in srgb, #fff 35%, ${color})`,
    }));
  };
  const faded = (plugin: PluginSummary) =>
    matches !== undefined && !matches(plugin);
  const focusOf = (id: string, fade = false) => ({
    focus:
      focus === null
        ? fade
          ? ("dim" as const)
          : undefined
        : focus === id
          ? ("lit" as const)
          : ("dim" as const),
    onBlur: () => setFocus(null),
    onFocus: () => setFocus(id),
    onMouseEnter: () => setFocus(id),
    onMouseLeave: () => setFocus(null),
  });

  // The plugins with at most `room` on a side, as a scene.
  const sceneWith = (room: number): StageScene => {
    const rays: StageRay[] = [];
    const marks: StageMark[] = [];
    const dimmed = (id: string) => focus !== null && focus !== id;
    for (const side of ["left", "right"] as const) {
      const listed = sides[side];
      const shown =
        listed.length > room ? listed.slice(0, Math.max(1, room - 1)) : listed;
      const more = listed.length - shown.length;
      const points = sidePoints(
        Math.max(1, shown.length + (more > 0 ? 1 : 0)),
        side,
      );
      // A pill at the planets' end of the box, toward the star.
      const pillOrigin = {
        x: side === "left" ? PILL.width - 8 : 8,
        y: PILL.height / 2,
      };
      if (listed.length === 0) {
        marks.push({
          id: `add:${side}`,
          at: points[0],
          box: PILL,
          origin: pillOrigin,
          children: (
            <span
              className={cn(
                "flex h-full items-center",
                side === "left" ? "justify-end" : "justify-start",
              )}
            >
              <AddMark
                label={
                  side === "left"
                    ? t("plugins.map.addEnhancer")
                    : t("plugins.map.addProvider")
                }
                onAdd={side === "left" ? onAddEnhancer : onAddProvider}
                side={side}
              />
            </span>
          ),
        });
        continue;
      }
      shown.forEach((plugin, index) => {
        const at = points[index];
        const id = `plugin:${plugin.plugin.key}`;
        const color = pluginStarColor(plugin);
        const live =
          !paused && (plugin.state === "running" || plugin.state === "failing");
        const fade = faded(plugin.plugin);
        rays.push({
          id,
          to: at,
          color,
          sparks: live ? "out" : undefined,
          dimmed: dimmed(id) || (focus === null && fade),
        });
        const size = bodySize(pluginWork(plugin), busiest, PLANET);
        marks.push({
          id,
          at,
          // The planet sits on its orbit; its name runs outward.
          ...bodyBox(at.caption),
          children: ({ toStar }) => (
            <StarBody
              aria-label={t("plugins.star.open", {
                name: plugin.plugin.name,
                fact: pluginStarFact(plugin, t),
              })}
              className="h-full"
              data-faded={fade || undefined}
              data-plugin-key={plugin.plugin.key}
              data-side={side}
              data-testid="plugin-star"
              fact={
                <span
                  className={
                    plugin.state === "failed" || plugin.state === "failing"
                      ? "text-star-warn"
                      : undefined
                  }
                >
                  {pluginStarFact(plugin, t)}
                </span>
              }
              captionAt={at.caption}
              caption={plugin.plugin.name}
              onClick={() => onOpen(plugin.plugin.key)}
              sphere={
                <span
                  className="grid place-items-center"
                  style={{ width: PLANET.most, height: PLANET.most }}
                >
                  <StarSphere
                    color={color}
                    moons={moonsOf(plugin, color)}
                    quiet={plugin.state === "off"}
                    size={size}
                    toStar={toStar}
                  />
                </span>
              }
              {...focusOf(id, fade)}
            />
          ),
        });
      });
      if (more > 0) {
        const id = `more:${side}`;
        marks.push({
          id,
          at: points[points.length - 1],
          box: PILL,
          origin: pillOrigin,
          children: (
            <span
              className={cn(
                "flex h-full items-center",
                side === "left" ? "justify-end" : "justify-start",
              )}
            >
              <StarMark
                aria-label={t("plugins.star.showList")}
                className="px-2.5 py-1 text-xs text-star-dim"
                onClick={onShowList}
                {...focusOf(id)}
              >
                {t("plugins.star.more", { count: more })}
              </StarMark>
            </span>
          ),
        });
      }
    }
    const shownPlugin = stars.find(
      (plugin) => `plugin:${plugin.plugin.key}` === focus,
    );
    return {
      center: CENTER,
      orbits: [ORBIT],
      rays,
      marks,
      star: {
        box: STAR,
        tone: paused ? "var(--star-warn)" : undefined,
        children: <Hub onOpen={onHub} paused={paused} />,
      },
      callout: shownPlugin
        ? {
            mark: `plugin:${shownPlugin.plugin.key}`,
            children: <PluginStarDetail plugin={shownPlugin} />,
          }
        : undefined,
      inset: INSET,
    };
  };

  // Every plugin on a side, then one fewer, down to two and the rest.
  function* scenes(): Generator<StageScene> {
    const most = Math.max(sides.left.length, sides.right.length, 2);
    for (let room = most; room >= 2; room -= 1) yield sceneWith(room);
  }

  const calls = stars.reduce((sum, plugin) => sum + plugin.calls, 0);
  const tasks = stars.reduce((sum, plugin) => sum + plugin.tasks, 0);

  return (
    <StarStage
      aria-label={t("plugins.map.label")}
      className={cn("min-h-0 flex-1 rounded-md", className)}
      data-testid="plugin-star-map"
      role="group"
      scene={scenes}
      tilt={TILT}
      world={WORLD}
    >
      <SideHeading
        align="left"
        count={sides.left.length}
        help={t("plugins.map.enhancersHelp")}
        title={t("plugins.groups.extensions")}
      />
      <SideHeading
        align="right"
        count={sides.right.length}
        help={t("plugins.map.providersHelp")}
        title={t("plugins.groups.providers")}
      />
      {paused ? (
        <Badge
          className="absolute top-3 left-1/2 -translate-x-1/2 bg-warning-wash text-warning-foreground"
          variant="secondary"
        >
          {t("plugins.host.pausedShort")}
        </Badge>
      ) : null}
      {usage ? (
        <p className="absolute bottom-3 left-4 text-xs text-star-dim tabular-nums">
          {t("plugins.star.window", {
            requests: formatExactNumber(calls),
            tasks: formatExactNumber(tasks),
          })}
        </p>
      ) : null}
    </StarStage>
  );
}

/** A side's title and count, its kind of plugin explained behind ?. */
function SideHeading({
  align,
  title,
  count,
  help,
}: {
  align: Side;
  title: string;
  count: number;
  help: string;
}) {
  const t = useT();
  return (
    <div
      className={cn(
        "absolute top-3 flex items-center gap-1.5 text-xs font-medium text-star-dim",
        align === "left" ? "left-4" : "right-4 flex-row-reverse",
      )}
    >
      <span className="flex items-center gap-1.5">
        <span
          aria-hidden="true"
          className={cn(
            "size-2 rounded-full",
            align === "left" ? "bg-star-enhancer" : "bg-star-provider",
          )}
        />
        {title}
        <span className="text-star-ink tabular-nums">{count}</span>
      </span>
      <HelpPopover
        label={t("plugins.map.helpLabel", { kind: title })}
        triggerClassName="text-star-dim hover:bg-star-surface hover:text-star-ink [&_svg]:text-current"
      >
        <p>{help}</p>
      </HelpPopover>
    </div>
  );
}

/** The star: AstrLink's logo in its light, opening the plugins' settings. */
function Hub({ paused, onOpen }: { paused: boolean; onOpen: () => void }) {
  const t = useT();
  return (
    <Button
      aria-label={t("plugins.host.open")}
      className="size-full rounded-full p-0 hover:bg-transparent"
      data-testid="plugin-star-hub"
      onClick={onOpen}
      title={paused ? t("plugins.host.pausedShort") : t("plugins.host.open")}
      type="button"
      variant="ghost"
    >
      <img alt="" className="size-10" draggable={false} src={astrlinkLogo} />
    </Button>
  );
}

/** The place of an empty side, which leads to where its plugins come from. */
function AddMark({
  side,
  label,
  onAdd,
}: {
  side: Side;
  label: string;
  onAdd?: () => void;
}) {
  return (
    <StarMark
      className="gap-1.5 border-dashed px-2.5 py-1 text-xs text-star-dim"
      data-side={side}
      data-testid="plugin-star-add"
      disabled={!onAdd}
      onClick={onAdd}
    >
      <Plus aria-hidden="true" className="size-3.5" />
      {label}
    </StarMark>
  );
}
