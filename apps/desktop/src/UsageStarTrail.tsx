import { memo, useId, useMemo, type ReactNode } from "react";

import { ClientTypeIcon, clientTypeName } from "@/components/ClientTypeIcon";
import { EmptyState } from "@/components/EmptyState";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import {
  StarTrailChart,
  type StarTrailChartArc,
} from "@/components/StarTrailChart";
import { cn } from "@/lib/utils";

import {
  formatCompactNumber,
  formatExactNumber,
} from "./format-compact-number";
import { currentLocale, useT } from "./i18n";
import {
  clientShares,
  starTrail,
  starTrailByModel,
  starTrailLegend,
  type StarTrail,
  type StarTrailArc,
  type StarTrailLegend,
  type StarTrailModelRow,
} from "./star-trail-model";
import {
  hourlyUsageWindow,
  localDayKey,
  modelUsageLabel,
  type UsageStatus,
  type UsageSummary,
} from "./usage-range";

const HOUR_LABELS = ["0", "6", "12", "18"] as const;

const dayFormats = new Map<string, Intl.DateTimeFormat>();

/** `YYYY-MM-DD` as a short local date with its weekday. */
function trailDayLabel(date: string, locale: string = currentLocale()): string {
  const [year, month, day] = date.split("-").map(Number);
  if (!year || !month || !day) return date;
  let format = dayFormats.get(locale);
  if (!format) {
    format = new Intl.DateTimeFormat(locale, {
      month: "short",
      day: "numeric",
      weekday: "short",
    });
    dayFormats.set(locale, format);
  }
  return format.format(new Date(year, month - 1, day));
}

function hourRange(hour: number): string {
  const pad = (value: number) => `${String(value).padStart(2, "0")}:00`;
  return `${pad(hour)}–${pad(hour + 1)}`;
}

/** The fraction of today that has passed, if the window's last day is today. */
function todayProgress(lastDay: string | undefined) {
  return (now: Date): number | null =>
    lastDay !== undefined && localDayKey(now) === lastDay
      ? (now.getHours() * 60 + now.getMinutes()) / 1440
      : null;
}

/**
 * One panel: the disc on the left, about 300px with its hour labels, and the
 * range's numbers, models and clients on the right. Narrow workspaces stack
 * them.
 */
const PANEL =
  "grid min-w-0 grid-cols-1 items-center justify-items-center gap-x-7 gap-y-4 py-4 pr-6.5 pl-5.5 [background:radial-gradient(circle_at_10.75rem_50%,var(--muted),transparent_22rem)] @min-[720px]/workspace-surface:grid-cols-[18.75rem_minmax(0,1fr)] @min-[720px]/workspace-surface:justify-items-stretch";
// A container, so the numbers fold to two by two when the column is narrow.
const COLUMN = "@container grid w-full min-w-0 content-center gap-3";
const pulse = "animate-pulse rounded bg-muted motion-reduce:animate-none";

/** The panel's shape while the first read is on its way. */
export function UsageStarTrailSkeleton() {
  return (
    <div className={PANEL}>
      <span
        className={cn(
          "block aspect-square w-full max-w-75 rounded-full",
          pulse,
        )}
      />
      <div className={COLUMN}>
        <div className="grid grid-cols-4 gap-4">
          {Array.from({ length: 4 }, (_, index) => (
            <span className="grid gap-1.5" key={index}>
              <span className={cn("block h-4 w-12", pulse)} />
              <span className={cn("block h-7 w-20", pulse)} />
            </span>
          ))}
        </div>
        {Array.from({ length: 5 }, (_, index) => (
          <span className={cn("block h-4", pulse)} key={index} />
        ))}
      </div>
    </div>
  );
}

/**
 * Usage as a star trail. Over a day it draws a ring per model, the busiest
 * outermost, with an arc in its color for each hour it was used. Over longer
 * ranges it draws a ring per day, today outermost, with each lit hour colored
 * by its top model. Beside it sit the range's numbers, the model list that is
 * the color legend, and the client bar that splits the same tokens.
 */
export const UsageStarTrail = memo(function UsageStarTrail({
  metrics,
  now,
  status,
  summary,
}: {
  /** The range's numbers above the lists; keep it the same between reads. */
  metrics?: ReactNode;
  /** Fixed clock for tests. */
  now?: Date;
  status: UsageStatus;
  summary: UsageSummary | null;
}) {
  const t = useT();
  const modelsId = useId();
  const clientsId = useId();
  const unavailable = status === "blocked" || status === "error";
  const view = useMemo(() => {
    const legend = starTrailLegend(summary?.by_model ?? []);
    const trail =
      summary && hourlyUsageWindow(summary.window)
        ? starTrailByModel(summary, legend)
        : starTrail(summary ?? { by_day: [], by_hour_model: [] }, legend);
    return {
      legend,
      trail,
      clients: clientShares(summary?.by_client ?? []),
    };
  }, [summary]);
  const { legend, trail, clients } = view;
  // Labels are built once per summary and language, not on every render.
  const language = currentLocale();
  const chartArcs = useMemo(
    () => trail.arcs.map((arc) => chartArc(arc, trail, legend, t, language)),
    [trail, legend, t, language],
  );
  // The same function while the days stay, so the disc keeps its renders.
  const lastDay = trail.days.at(-1);
  const today = useMemo(() => todayProgress(lastDay), [lastDay]);

  if (unavailable) {
    return (
      <div className={PANEL} data-slot="usage-star-trail">
        <EmptyState
          className="min-h-40 w-full max-w-75 border-0 py-4"
          title={t(
            status === "blocked"
              ? "overview.chartBlocked"
              : "overview.chartFailed",
          )}
        />
        {metrics ? <div className={COLUMN}>{metrics}</div> : null}
      </div>
    );
  }

  const byModel = trail.by === "model";
  const busiest = trail.arcs.reduce<StarTrailArc | null>(
    (top, arc) =>
      top === null ||
      (byModel
        ? arc.total_tokens > top.total_tokens
        : arc.requests > top.requests)
        ? arc
        : top,
    null,
  );
  const summaryText = !busiest
    ? t("overview.noUsage")
    : byModel
      ? t("overview.starTrailModelSummary", {
          models: trail.rings,
          hours: trail.arcs.length,
          busiest: `${arcModelName(busiest, legend, t)} ${hourRange(busiest.hour)}`,
          count: formatExactNumber(busiest.total_tokens),
        })
      : t("overview.starTrailSummary", {
          days: trail.days.length,
          hours: trail.arcs.length,
          busiest: `${trailDayLabel(busiest.date)} ${hourRange(busiest.hour)}`,
          count: formatExactNumber(busiest.requests),
        });
  const empty = legend.rows.length === 0 && legend.rest === null;

  return (
    <div className={PANEL} data-slot="usage-star-trail">
      <div className="relative w-full max-w-75">
        <StarTrailChart
          arcs={chartArcs}
          className="max-w-75"
          hourLabels={HOUR_LABELS}
          label={t(
            byModel
              ? "overview.starTrailModelLabel"
              : "overview.starTrailLabel",
          )}
          now={now}
          rings={trail.rings}
          summary={summaryText}
          today={today}
        />
        {status === "ready" && trail.arcs.length === 0 ? (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
            <span className="rounded-md bg-card px-3 py-2 text-xs text-muted-foreground">
              {t("overview.noUsage")}
            </span>
          </div>
        ) : null}
      </div>
      {empty && !metrics ? null : (
        <div className={COLUMN}>
          {metrics}
          {empty ? null : (
            <section aria-labelledby={modelsId}>
              <h4
                className="mb-1 text-micro font-medium text-muted-foreground"
                id={modelsId}
              >
                {t("overview.starTrailModels")}
              </h4>
              <ul className="grid" data-slot="star-trail-models">
                {legend.rows.map((row) => (
                  <ModelLine key={row.id} row={row} />
                ))}
                {legend.rest ? <ModelLine row={legend.rest} /> : null}
              </ul>
            </section>
          )}
          {!empty && clients.length > 0 ? (
            <section aria-labelledby={clientsId}>
              <h4
                className="mb-1.5 text-micro font-medium text-muted-foreground"
                id={clientsId}
              >
                {t("overview.starTrailClients")}
              </h4>
              <div
                aria-hidden="true"
                className="flex h-2.5 gap-0.5 overflow-hidden rounded-full"
                data-slot="star-trail-client-bar"
              >
                {clients.map((client) => (
                  <span
                    className="min-w-0.75"
                    data-client={client.id ?? "unknown"}
                    key={client.id ?? "unknown"}
                    style={{
                      background: client.color,
                      flexGrow: client.total_tokens,
                      opacity: client.id === null ? 0.45 : 1,
                    }}
                  />
                ))}
              </div>
              <ul
                className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-text-secondary"
                data-slot="star-trail-client-chips"
              >
                {clients.map((client) => (
                  <li
                    className="inline-flex min-w-0 items-center gap-1.5"
                    key={client.id ?? "unknown"}
                    title={`${formatExactNumber(client.total_tokens)} Token`}
                  >
                    <span
                      aria-hidden="true"
                      className="size-1.75 shrink-0 rounded-full"
                      style={{
                        background: client.color,
                        opacity: client.id === null ? 0.45 : 1,
                      }}
                    />
                    {client.id ? (
                      <ClientTypeIcon
                        clientType={client.id}
                        decorative
                        size={14}
                      />
                    ) : null}
                    <span className="truncate">
                      {clientTypeName(client.id)}
                    </span>
                    <strong className="font-semibold text-foreground tabular-nums">
                      {client.percent}%
                    </strong>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
        </div>
      )}
    </div>
  );
});

function ModelLine({ row }: { row: StarTrailModelRow }) {
  const t = useT();
  const rest = row.id === null;
  const name = rest
    ? t("overview.starTrailOtherModels", { count: row.folded })
    : modelUsageLabel(row.id);
  return (
    <li
      className="grid h-6 min-w-0 grid-cols-[1rem_minmax(0,9rem)_minmax(2rem,1fr)_4rem_2.25rem] items-center gap-2 text-xs"
      data-model={row.id ?? undefined}
      data-slot="star-trail-model"
    >
      <span className="flex items-center">
        {rest ? null : <ModelBrandIcon model={row.id} />}
      </span>
      <span
        className={cn("truncate", rest && "text-muted-foreground")}
        title={name}
      >
        {name}
      </span>
      <span
        aria-hidden="true"
        className="block h-1.25 overflow-hidden rounded-full bg-muted"
      >
        <span
          className="block h-full rounded-full"
          data-slot="star-trail-model-bar"
          style={{
            background: row.color,
            opacity: rest ? 0.45 : 1,
            width: `${row.bar}%`,
          }}
        />
      </span>
      <span
        className="text-right font-semibold tabular-nums"
        title={`${formatExactNumber(row.total_tokens)} Token`}
      >
        {formatCompactNumber(row.total_tokens, { short: true })}
      </span>
      <span className="text-right text-micro text-muted-foreground tabular-nums">
        {row.percent}%
      </span>
    </li>
  );
}

/** A model ring's name: the model, or the rest of them. */
function arcModelName(
  arc: StarTrailArc,
  legend: StarTrailLegend,
  t: ReturnType<typeof useT>,
): string {
  return arc.model === null
    ? t("overview.starTrailOtherModels", { count: legend.rest?.folded ?? 0 })
    : modelUsageLabel(arc.model);
}

function chartArc(
  arc: StarTrailArc,
  trail: StarTrail,
  legend: StarTrailLegend,
  t: ReturnType<typeof useT>,
  language: string,
): StarTrailChartArc {
  const day = trailDayLabel(arc.date, language);
  const hours = hourRange(arc.hour);
  const requests = t("overview.dayRequests", {
    count: formatExactNumber(arc.requests),
  });
  // A model ring names its own model; a day ring the hour's top one.
  const model =
    trail.by === "model"
      ? arcModelName(arc, legend, t)
      : t("overview.starTrailTopModel", { model: modelUsageLabel(arc.model) });
  return {
    key: arc.key,
    ring: arc.ring,
    hour: arc.hour,
    color: arc.color,
    brightness: arc.brightness,
    label: `${day} ${hours}, ${requests}, ${formatExactNumber(arc.total_tokens)} Token, ${model}`,
    detail: () => (
      <span className="grid gap-0.5 tabular-nums">
        <strong className="font-medium">
          {day} · {hours}
        </strong>
        <span>{requests}</span>
        <span>
          {t("overview.dayTokens", {
            compact: formatCompactNumber(arc.total_tokens),
            exact: formatExactNumber(arc.total_tokens),
          })}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span
            aria-hidden="true"
            className="size-1.75 rounded-full"
            style={{ background: arc.color }}
          />
          {model}
        </span>
      </span>
    ),
  };
}
