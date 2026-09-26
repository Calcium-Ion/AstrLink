import { useEffect, useState } from "react";
import { getUsageSummary } from "./bridge";
import {
  resolveUsageWindow,
  startOfLocalDay,
  type UsageGroup,
  type UsageWindow,
} from "./usage-range";

export interface PerformanceTarget {
  kind: "service" | "token";
  id: string;
  name: string;
}

export type PerformancePeriod = "today" | "7d" | "30d";
export interface PerformanceDetailRow {
  period: PerformancePeriod;
  status: "loading" | "ready" | "error";
  usage?: UsageGroup;
}

export function performanceDetailWindows(
  now: Date,
): Array<{ period: PerformancePeriod; window: UsageWindow }> {
  const week = resolveUsageWindow("7d", now);
  return [
    {
      period: "today",
      window: {
        ...week,
        preset: "1d",
        from: startOfLocalDay(now).toISOString(),
      },
    },
    { period: "7d", window: week },
    { period: "30d", window: resolveUsageWindow("30d", now) },
  ];
}

export function usePerformanceDetails(
  target: PerformanceTarget,
  open: boolean,
) {
  const [revision, setRevision] = useState(0);
  const [rows, setRows] = useState<PerformanceDetailRow[]>([]);
  const { kind, id } = target;
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    let running = false;
    const refresh = async () => {
      if (cancelled || running) return;
      running = true;
      const windows = performanceDetailWindows(new Date());
      setRows(windows.map(({ period }) => ({ period, status: "loading" })));
      await Promise.all(
        windows.map(async ({ period, window }) => {
          let row: PerformanceDetailRow;
          try {
            const summary = await getUsageSummary(window);
            const usage = (
              kind === "service" ? summary.by_service : summary.by_token
            ).find((group) => group.id === id);
            row = { period, status: "ready", usage };
          } catch {
            row = { period, status: "error" };
          }
          if (!cancelled)
            setRows((current) =>
              current.map((entry) => (entry.period === period ? row : entry)),
            );
        }),
      );
      running = false;
    };
    void refresh();
    const timer = window.setInterval(() => {
      if (!document.hidden) void refresh();
    }, 60_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [kind, id, open, revision]);
  return {
    rows,
    refresh: () => setRevision((current) => current + 1),
    loading: rows.some((row) => row.status === "loading"),
  };
}
