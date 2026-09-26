import { useEffect, useState } from "react";
import { getUsageSummary } from "./bridge";
import {
  resolveUsageWindow,
  type ServicePerformance,
  type UsageRangePreset,
  type UsageStatus,
} from "./usage-range";

export function useServicePerformance(
  ready: boolean,
  preset: UsageRangePreset,
  epoch: number,
) {
  const [state, setState] = useState<{
    status: UsageStatus;
    byService: Record<string, ServicePerformance | undefined>;
  }>({ status: "blocked", byService: {} });

  useEffect(() => {
    let cancelled = false;
    let running = false;
    setState({ status: ready ? "loading" : "blocked", byService: {} });
    if (!ready) return;
    const refresh = async () => {
      if (cancelled || running) return;
      running = true;
      try {
        const summary = await getUsageSummary(
          resolveUsageWindow(preset, new Date()),
        );
        if (!cancelled)
          setState({
            status: "ready",
            byService: Object.fromEntries(
              summary.by_service
                .filter((group) => group.id !== null)
                .map((group) => [group.id, group.performance]),
            ),
          });
      } catch {
        if (!cancelled) setState({ status: "error", byService: {} });
      } finally {
        running = false;
      }
    };
    void refresh();
    const timer = window.setInterval(() => {
      if (!document.hidden) void refresh();
    }, 60_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [ready, preset, epoch]);
  return state;
}
