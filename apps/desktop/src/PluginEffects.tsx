import type { CSSProperties, ReactNode } from "react";

import { BarList, type BarListItem } from "@/components/BarList";
import { HelpPopover } from "@/components/HelpPopover";
import { Metric } from "@/components/Metric";
import { Panel, PanelHeader } from "@/components/Panel";
import { Sparkline } from "@/components/Sparkline";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { dayLabel } from "./chart-axis";
import { formatCompactNumber } from "./format-compact-number";
import { useT } from "./i18n";
import type {
  PluginCacheShare,
  PluginDayStats,
  PluginDetail,
  PluginStats,
} from "./plugin-model";

type Translate = ReturnType<typeof useT>;

/** The reasons a block is computed again in a session that has one. */
const REFRESH_TRIGGERS = ["compact", "manual", "auto"];

/** About 3 bytes of injected text make a token, Chinese or English. */
const BYTES_PER_TOKEN = 3;

/** The ways a session got its value, in the order the card lists them. */
const BINDING_WAYS = [
  "workspace",
  "picked",
  "created",
  "declined",
  "unanswered",
] as const;

/** The kinds of trouble, in the order the card lists them. */
const PROBLEM_KINDS = ["failed", "timeouts", "privacy", "over_budget"] as const;

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function tokensOf(bytes: number): number {
  return Math.round(bytes / BYTES_PER_TOKEN);
}

function percent(part: number, whole: number): string | null {
  return whole > 0 ? `${Math.round((100 * part) / whole)}%` : null;
}

function cacheRate(share: PluginCacheShare): string | null {
  return percent(share.cache_read_tokens, share.input_tokens);
}

function problemCount(day: PluginDayStats): number {
  return PROBLEM_KINDS.reduce((sum, kind) => sum + day.problems[kind], 0);
}

/** Which parts of the account apply to the plugin. */
function partsOf(detail: PluginDetail) {
  const injects = ["prepareSession", "prepareTurn"].some((hook) =>
    detail.hooks.includes(hook),
  );
  return {
    load: injects,
    lookup: detail.tools.some((tool) => !tool.writes),
    binding: detail.metadata.length > 0,
    write:
      detail.hooks.includes("observeTurn") ||
      detail.tools.some((tool) => tool.writes),
    cache: injects,
  };
}

/** Whether a day or the whole has anything to show. */
function active(day: PluginDayStats): boolean {
  const binding = Object.values(day.binding).some((count) => count > 0);
  return (
    day.load.requests > 0 ||
    day.load.count > 0 ||
    day.lookup.ran + day.lookup.refused > 0 ||
    binding ||
    day.write.ok + day.write.failed > 0 ||
    problemCount(day) > 0
  );
}

/** The parts of a line that have something to say, joined. */
function joined(parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" · ");
}

interface Tile {
  key: string;
  label: string;
  value: string;
  detail: ReactNode;
  trend: (day: PluginDayStats) => number;
  format: (value: number) => string;
  tone?: "warning";
}

function tilesOf(
  total: PluginDayStats,
  parts: ReturnType<typeof partsOf>,
  t: Translate,
): Tile[] {
  const tiles: Tile[] = [];
  if (parts.load) {
    const refreshes = REFRESH_TRIGGERS.reduce(
      (sum, trigger) => sum + (total.load.by_trigger[trigger] ?? 0),
      0,
    );
    tiles.push({
      key: "load",
      label: t("plugins.effects.load"),
      value: t("plugins.effects.tokens", {
        tokens: formatCompactNumber(tokensOf(total.load.injected_bytes)),
      }),
      detail: joined([
        t("plugins.effects.loads", { count: total.load.count }),
        total.load.wait_ms &&
          t("plugins.effects.wait", { p50: total.load.wait_ms.p50 }),
        refreshes > 0 && t("plugins.effects.refreshes", { count: refreshes }),
      ]),
      trend: (day) => tokensOf(day.load.injected_bytes),
      format: (value) =>
        t("plugins.effects.tokens", { tokens: formatCompactNumber(value) }),
    });
  }
  if (parts.lookup) {
    const lookup = total.lookup;
    tiles.push({
      key: "lookup",
      label: t("plugins.effects.lookup"),
      value: t("plugins.effects.times", { count: lookup.ran }),
      detail: joined([
        percent(lookup.with_hits, lookup.counted) &&
          t("plugins.effects.hitRate", {
            rate: percent(lookup.with_hits, lookup.counted),
          }),
        lookup.looking_up > 0 &&
          t("plugins.effects.lookupSessions", { count: lookup.looking_up }),
        lookup.refused > 0 &&
          t("plugins.effects.refused", { count: lookup.refused }),
      ]),
      trend: (day) => day.lookup.ran,
      format: (value) => t("plugins.effects.times", { count: value }),
    });
  }
  if (parts.write) {
    const write = total.write;
    tiles.push({
      key: "write",
      label: t("plugins.effects.write"),
      value: t("plugins.effects.entries", { count: write.ok }),
      detail: (
        <>
          {write.reported > 0
            ? t("plugins.effects.reported", { count: write.reported })
            : null}
          {write.failed > 0 ? (
            <span className="text-warning-foreground">
              {write.reported > 0 ? " · " : ""}
              {t("plugins.effects.writeFailed", { count: write.failed })}
            </span>
          ) : null}
        </>
      ),
      trend: (day) => day.write.ok,
      format: (value) => t("plugins.effects.entries", { count: value }),
    });
  }
  const problems = problemCount(total);
  tiles.push({
    key: "problems",
    label: t("plugins.effects.problems"),
    value: String(problems),
    detail:
      problems === 0
        ? t("plugins.effects.noProblems")
        : joined(
            PROBLEM_KINDS.map(
              (kind) =>
                total.problems[kind] > 0 &&
                t(`plugins.effects.problemKinds.${kind}`, {
                  count: total.problems[kind],
                }),
            ),
          ),
    trend: problemCount,
    format: (value) => String(value),
    tone: problems > 0 ? "warning" : undefined,
  });
  return tiles;
}

/**
 * How the plugin ran over the last 7 days on the overview: a tile for each of
 * what its blocks cost, how often the model looked things up, what it wrote
 * and what went wrong, each with its days as small columns; then how sessions
 * got their values and how the prompt cache fared, as bars.
 */
export function EffectCard({
  detail,
  stats,
}: {
  detail: PluginDetail;
  stats: PluginStats;
}) {
  const t = useT();
  const total = stats.total;
  const parts = partsOf(detail);
  const tiles = tilesOf(total, parts, t);
  const ways: BarListItem[] = BINDING_WAYS.filter(
    (way) => total.binding[way] > 0,
  ).map((way) => ({
    key: way,
    label: t(`plugins.effects.ways.${way}`),
    value: total.binding[way],
  }));
  const cacheKnown =
    parts.cache &&
    (cacheRate(total.cache.with) !== null ||
      cacheRate(total.cache.without) !== null);
  const cache: BarListItem[] = (["with", "without"] as const).map((side) => {
    const rate = cacheRate(total.cache[side]);
    return {
      key: side,
      label: t(`plugins.effects.cacheSides.${side}`),
      value: rate === null ? 0 : Number.parseInt(rate, 10),
      display: rate ?? "—",
    };
  });
  return (
    <Panel data-testid="plugin-effects">
      <PanelHeader
        actions={
          <HelpPopover label={t("plugins.effects.helpLabel")}>
            <p>{t("plugins.effects.help")}</p>
          </HelpPopover>
        }
        size="sm"
      >
        <div className="flex items-baseline gap-2">
          <h3 className="text-xs font-semibold">
            {t("plugins.effects.title")}
          </h3>
          <span className="text-micro text-muted-foreground">
            {t("plugins.effects.period")}
          </span>
        </div>
      </PanelHeader>
      {active(total) ? (
        <>
          <dl
            className="grid grid-cols-2 gap-px bg-border @[560px]/overview:[grid-template-columns:repeat(var(--tiles),minmax(0,1fr))]"
            style={{ "--tiles": tiles.length } as CSSProperties}
          >
            {tiles.map((tile, index) => (
              <div
                className={cn(
                  "grid",
                  index === tiles.length - 1 &&
                    tiles.length % 2 === 1 &&
                    "col-span-2 @[560px]/overview:col-span-1",
                )}
                data-testid="plugin-effect-tile"
                key={tile.key}
              >
                <Metric
                  detail={tile.detail || undefined}
                  label={tile.label}
                  size="sm"
                  trend={
                    <Sparkline
                      format={tile.format}
                      label={tile.label}
                      points={stats.days.map((day) => ({
                        label: day.date ? dayLabel(day.date, "short") : "",
                        value: tile.trend(day),
                      }))}
                      tone={tile.tone}
                    />
                  }
                  value={tile.value}
                />
              </div>
            ))}
          </dl>
          {(parts.binding && ways.length > 0) || cacheKnown ? (
            <div className="grid gap-x-6 gap-y-4 border-t p-3 @[560px]/overview:grid-cols-2">
              {parts.binding && ways.length > 0 ? (
                <section data-testid="plugin-effect-bindings">
                  <h4 className="mb-2 text-micro font-medium text-muted-foreground">
                    {t("plugins.effects.binding")}
                  </h4>
                  <BarList items={ways} label={t("plugins.effects.binding")} />
                  {total.binding.changed > 0 ? (
                    <p className="mt-1.5 text-micro text-muted-foreground">
                      {t("plugins.effects.changed", {
                        count: total.binding.changed,
                      })}
                    </p>
                  ) : null}
                </section>
              ) : null}
              {cacheKnown ? (
                <section data-testid="plugin-effect-cache">
                  <h4 className="mb-2 text-micro font-medium text-muted-foreground">
                    {t("plugins.effects.cache")}
                  </h4>
                  <BarList
                    items={cache}
                    label={t("plugins.effects.cache")}
                    max={100}
                  />
                </section>
              ) : null}
            </div>
          ) : null}
        </>
      ) : (
        <p className="p-3 text-xs text-muted-foreground">
          {t("plugins.effects.empty")}
        </p>
      )}
    </Panel>
  );
}

/** The same account day by day on the status page, today first. */
export function DailyEffects({
  detail,
  stats,
}: {
  detail: PluginDetail;
  stats: PluginStats;
}) {
  const t = useT();
  const parts = partsOf(detail);
  const days = [...stats.days].reverse();
  return (
    <section className="grid gap-1.5" data-testid="plugin-daily-effects">
      <div className="flex items-center gap-1">
        <h3 className="text-xs font-medium text-muted-foreground">
          {t("plugins.effects.daily")}
        </h3>
        <HelpPopover label={t("plugins.effects.dailyHelpLabel")}>
          <p>{t("plugins.effects.dailyHelp")}</p>
        </HelpPopover>
      </div>
      <Table className="text-xs">
        <TableHeader>
          <TableRow>
            <TableHead>{t("plugins.effects.columns.date")}</TableHead>
            {parts.load ? (
              <>
                <TableHead>{t("plugins.effects.columns.loads")}</TableHead>
                <TableHead>{t("plugins.effects.columns.injected")}</TableHead>
              </>
            ) : null}
            {parts.lookup ? (
              <>
                <TableHead>{t("plugins.effects.columns.lookups")}</TableHead>
                <TableHead>{t("plugins.effects.columns.hits")}</TableHead>
              </>
            ) : null}
            {parts.binding ? (
              <TableHead>{t("plugins.effects.columns.bindings")}</TableHead>
            ) : null}
            {parts.write ? (
              <TableHead>{t("plugins.effects.columns.writes")}</TableHead>
            ) : null}
            <TableHead>{t("plugins.effects.columns.problems")}</TableHead>
            {parts.cache ? (
              <TableHead>{t("plugins.effects.columns.cache")}</TableHead>
            ) : null}
          </TableRow>
        </TableHeader>
        <TableBody>
          {days.map((day) => {
            const bindings = BINDING_WAYS.filter(
              (way) => way !== "unanswered",
            ).reduce((sum, way) => sum + day.binding[way], 0);
            return (
              // A day without activity stays in the column of days, quietly.
              <TableRow
                className={cn(!active(day) && "text-muted-foreground")}
                data-active={active(day) ? "true" : "false"}
                key={day.date}
              >
                <TableCell className="tabular-nums">{day.date}</TableCell>
                {parts.load ? (
                  <>
                    <TableCell className="tabular-nums">
                      {day.load.count}
                    </TableCell>
                    <TableCell className="tabular-nums">
                      {formatSize(day.load.injected_bytes)}
                    </TableCell>
                  </>
                ) : null}
                {parts.lookup ? (
                  <>
                    <TableCell className="tabular-nums">
                      {day.lookup.ran}
                    </TableCell>
                    <TableCell className="tabular-nums">
                      {percent(day.lookup.with_hits, day.lookup.counted) ?? "—"}
                    </TableCell>
                  </>
                ) : null}
                {parts.binding ? (
                  <TableCell className="tabular-nums">{bindings}</TableCell>
                ) : null}
                {parts.write ? (
                  <TableCell className="tabular-nums">
                    {day.write.failed > 0
                      ? t("plugins.effects.writeCell", day.write)
                      : day.write.ok}
                  </TableCell>
                ) : null}
                <TableCell className="tabular-nums">
                  {problemCount(day)}
                </TableCell>
                {parts.cache ? (
                  <TableCell className="tabular-nums">
                    {t("plugins.effects.cacheCell", {
                      with: cacheRate(day.cache.with) ?? "—",
                      without: cacheRate(day.cache.without) ?? "—",
                    })}
                  </TableCell>
                ) : null}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </section>
  );
}
