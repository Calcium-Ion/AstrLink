import { useEffect, useState } from "react";

import { getUsageSummary } from "./bridge";
import { dayLabel } from "./chart-axis";
import type { PluginSummary } from "./plugin-model";
import {
  resolveUsageWindow,
  usageWindowDayKeys,
  type UsagePluginDay,
  type UsageSummary,
} from "./usage-range";

/**
 * The plugins' last 7 days, read again whenever what they report changes:
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
    getUsageSummary(resolveUsageWindow("7d", new Date()))
      .then((summary) => {
        if (live) setUsage(summary);
      })
      .catch(() => {
        // The page shows the plugins without what it could not read.
        if (live) setUsage(null);
      });
    return () => {
      live = false;
    };
  }, [isReady, revision]);
  return usage;
}

/** Every day of the window, oldest first, with what one plugin, or all, did. */
export function pluginDays(
  usage: UsageSummary,
  key?: string,
): UsagePluginDay[] {
  const days = new Map<string, UsagePluginDay>();
  for (const group of usage.by_plugin) {
    if (key !== undefined && group.plugin !== key) continue;
    for (const day of group.days ?? []) {
      const total = days.get(day.date) ?? {
        date: day.date,
        requests: 0,
        failed_requests: 0,
        tasks: 0,
        failed_tasks: 0,
      };
      total.requests += day.requests;
      total.failed_requests += day.failed_requests;
      total.tasks += day.tasks;
      total.failed_tasks += day.failed_tasks;
      days.set(day.date, total);
    }
  }
  return usageWindowDayKeys(usage.window).map(
    (date) =>
      days.get(date) ?? {
        date,
        requests: 0,
        failed_requests: 0,
        tasks: 0,
        failed_tasks: 0,
      },
  );
}

/** A day series as sparkline points. */
export function dayPoints(
  days: readonly UsagePluginDay[],
  value: (day: UsagePluginDay) => number,
) {
  return days.map((day) => ({
    label: dayLabel(day.date, "short"),
    value: value(day),
  }));
}

/** What a plugin worked on in a day: requests it acted on and tasks it took. */
export const dayWork = (day: UsagePluginDay) =>
  day.requests + day.failed_requests + day.tasks;

/** What went wrong in a day: failed requests and failed tasks. */
export const dayTrouble = (day: UsagePluginDay) =>
  day.failed_requests + day.failed_tasks;
