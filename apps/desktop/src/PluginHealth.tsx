import { useEffect, useState } from "react";

import { HelpPopover } from "@/components/HelpPopover";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel, PanelHeader } from "@/components/Panel";
import { Sparkline } from "@/components/Sparkline";

import { getUsageSummary } from "./bridge";
import { formatExactNumber } from "./format-compact-number";
import { useT } from "./i18n";
import type { PluginDetail } from "./plugin-model";
import { dayPoints, pluginDays } from "./plugin-usage";
import { resolveUsageWindow, type UsagePluginDay } from "./usage-range";

/** One plugin's last 7 days; null until read or if they could not be. */
function usePluginDays(key: string, revision: number) {
  const [days, setDays] = useState<UsagePluginDay[] | null>(null);
  useEffect(() => {
    let live = true;
    getUsageSummary(resolveUsageWindow("7d", new Date()))
      .then((summary) => {
        if (live) setDays(pluginDays(summary, key));
      })
      .catch(() => {
        if (live) setDays(null);
      });
    return () => {
      live = false;
    };
  }, [key, revision]);
  return days;
}

/**
 * How a plugin that serves providers or checks requests ran over the last 7
 * days, from the request records: what it worked on and what failed, with
 * its tasks for a task plugin, each with its days as small columns.
 */
export function PluginHealth({
  detail,
  revision = 0,
}: {
  detail: PluginDetail;
  /** A new value reads the days again. */
  revision?: number;
}) {
  const t = useT();
  const days = usePluginDays(detail.key, revision);
  if (!days) return null;
  const sum = (value: (day: UsagePluginDay) => number) =>
    days.reduce((total, day) => total + value(day), 0);
  const requests = sum((day) => day.requests + day.failed_requests);
  const failedRequests = sum((day) => day.failed_requests);
  const tasks = sum((day) => day.tasks);
  const failedTasks = sum((day) => day.failed_tasks);
  const times = (value: number) => t("plugins.row.times", { count: value });
  const tiles = [
    ...(detail.task || tasks > 0
      ? [
          {
            key: "tasks",
            label: t("plugins.health.tasks"),
            value: tasks,
            detail: detail.running_tasks
              ? t("plugins.task.runningCount", {
                  count: detail.running_tasks,
                })
              : undefined,
            points: dayPoints(days, (day) => day.tasks),
            warn: false,
          },
          {
            key: "failedTasks",
            label: t("plugins.health.failedTasks"),
            value: failedTasks,
            detail: undefined,
            points: dayPoints(days, (day) => day.failed_tasks),
            warn: true,
          },
        ]
      : []),
    ...(!detail.task || requests > 0
      ? [
          {
            key: "requests",
            label:
              detail.kind === "request"
                ? t("plugins.health.checked")
                : t("plugins.health.requests"),
            value: requests,
            detail: undefined,
            points: dayPoints(
              days,
              (day) => day.requests + day.failed_requests,
            ),
            warn: false,
          },
          {
            key: "failedRequests",
            label: t("plugins.health.failedRequests"),
            value: failedRequests,
            detail: undefined,
            points: dayPoints(days, (day) => day.failed_requests),
            warn: true,
          },
        ]
      : []),
  ];
  return (
    <Panel data-testid="plugin-health">
      <PanelHeader
        actions={
          <HelpPopover label={t("plugins.health.helpLabel")}>
            {t("plugins.health.help")}
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
      <MetricGroup
        className={
          tiles.length > 2
            ? undefined
            : "@min-[640px]/workspace-surface:grid-cols-2"
        }
      >
        {tiles.map((tile) => (
          <Metric
            detail={tile.detail}
            key={tile.key}
            label={tile.label}
            size="sm"
            trend={
              <Sparkline
                format={times}
                label={tile.label}
                points={tile.points}
                tone={tile.warn ? "warning" : "primary"}
              />
            }
            value={
              <span
                className={
                  tile.warn && tile.value > 0
                    ? "text-warning-foreground"
                    : undefined
                }
              >
                {formatExactNumber(tile.value)}
              </span>
            }
          />
        ))}
      </MetricGroup>
    </Panel>
  );
}
