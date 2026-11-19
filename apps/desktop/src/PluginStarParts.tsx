import { StarDetail } from "@/components/StarStage";

import {
  formatCompactNumber,
  formatExactNumber,
} from "./format-compact-number";
import { useT } from "./i18n";
import type { StarPlugin } from "./overview-star-model";

type Translate = ReturnType<typeof useT>;

/** A plugin's color on a star map: its kind's, or what is wrong with it. */
export function pluginStarColor(plugin: StarPlugin): string {
  switch (plugin.state) {
    case "failed":
      return "var(--destructive)";
    case "failing":
      return "var(--star-warn)";
    case "off":
      return "var(--star-off)";
    default:
      return plugin.side === "provider"
        ? "var(--star-provider)"
        : "var(--star-enhancer)";
  }
}

/**
 * The line under a plugin's name: its tasks under way, else what it did over
 * the range, else for a provider plugin how many providers run it, or the
 * state of one that does not run.
 */
export function pluginStarFact(plugin: StarPlugin, t: Translate): string {
  if (plugin.state === "failed") return t("plugins.state.failed");
  if (plugin.state === "off") return t("plugins.state.disabled");
  if (plugin.plugin.running_tasks)
    return t("plugins.task.runningCount", {
      count: plugin.plugin.running_tasks,
    });
  const calls = plugin.calls + plugin.served;
  const count = (value: number) => ({
    count: value,
    value: formatCompactNumber(value),
  });
  if (plugin.side === "enhancer")
    return t("plugins.star.callCount", count(calls));
  if (plugin.tasks > 0) return t("plugins.star.taskCount", count(plugin.tasks));
  if (calls > 0) return t("plugins.star.callCount", count(calls));
  return plugin.providers > 0
    ? t("plugins.map.providers", { count: plugin.providers })
    : t("plugins.map.noProviders");
}

/** What a plugin did over the range, beside it on a star map. */
export function PluginStarDetail({ plugin }: { plugin: StarPlugin }) {
  const t = useT();
  const rows: Array<{ label: string; value: string }> = [];
  const times = (value: number) =>
    t("plugins.star.times", { value: formatExactNumber(value) });
  if (plugin.side === "enhancer" || plugin.calls > 0)
    rows.push({ label: t("plugins.star.calls"), value: times(plugin.calls) });
  if (plugin.failedCalls > 0)
    rows.push({
      label: t("plugins.star.failed"),
      value: times(plugin.failedCalls),
    });
  if (plugin.side === "provider") {
    rows.push({
      label: t("plugins.star.providers"),
      value: formatExactNumber(plugin.providers),
    });
    if (plugin.served > 0)
      rows.push({
        label: t("plugins.star.served"),
        value: times(plugin.served),
      });
    if (plugin.tasks > 0 || plugin.plugin.kind === "task")
      rows.push({
        label: t("plugins.star.tasks"),
        value: formatExactNumber(plugin.tasks),
      });
    if (plugin.failedTasks > 0)
      rows.push({
        label: t("plugins.star.failedTasks"),
        value: formatExactNumber(plugin.failedTasks),
      });
  }
  return (
    <StarDetail
      className="w-60"
      kind={`${t(`plugins.star.kind.${plugin.side}`)} · ${t(
        `plugins.star.state.${plugin.state}`,
      )}`}
      rows={rows}
      title={plugin.plugin.name}
    />
  );
}
