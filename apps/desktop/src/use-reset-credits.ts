import { useEffect, useRef, useState } from "react";
import { getServiceResetCredits } from "./bridge";
import type { ResetCreditsDetails } from "./subscription-usage-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

const refreshInterval = 3 * 60 * 60_000;
type ResetCreditsState = (
  | { status: "loading" }
  | { status: "error" }
  | { status: "ready"; details: ResetCreditsDetails }
) & { version: number; checkedAt: number };

// Lists reuse the three-hour snapshot; opening a dialog always requests fresh details.
export function useResetCredits(
  serviceId: string | undefined,
  expectedCount?: number,
) {
  const [state, setState] = useWorkspaceSnapshot<ResetCreditsState>(
    `reset-credits:${serviceId ?? "none"}`,
    { status: "loading", version: 0, checkedAt: 0 },
  );
  const [attempt, setAttempt] = useState(0);
  const [now, setNow] = useState(Date.now);
  const previousCount = useRef(expectedCount);
  useEffect(() => {
    let active = true;
    let running = false;
    if (!serviceId) return;
    const refresh = async (force = false) => {
      if (!active || running) return;
      let version = 0;
      setState((current) => {
        if (
          !force &&
          current.version > 0 &&
          Date.now() - current.checkedAt < refreshInterval
        )
          return current;
        version = current.version + 1;
        return { status: "loading", version, checkedAt: Date.now() };
      });
      if (!version) return;
      running = true;
      try {
        const details = await getServiceResetCredits(serviceId);
        // A newer dialog refresh wins over a slow background response.
        setState((current) =>
          current.version === version
            ? { status: "ready", details, version, checkedAt: Date.now() }
            : current,
        );
      } catch {
        setState((current) =>
          current.version === version
            ? { status: "error", version, checkedAt: Date.now() }
            : current,
        );
      } finally {
        running = false;
        if (active) setNow(Date.now());
      }
    };
    const refreshVisible = () => {
      if (!document.hidden) void refresh();
    };
    const countChanged = previousCount.current !== expectedCount;
    previousCount.current = expectedCount;
    void refresh(expectedCount === undefined || attempt > 0 || countChanged);
    const clock = window.setInterval(() => {
      setNow(Date.now());
      refreshVisible();
    }, 30_000);
    document.addEventListener("visibilitychange", refreshVisible);
    return () => {
      active = false;
      window.clearInterval(clock);
      document.removeEventListener("visibilitychange", refreshVisible);
    };
  }, [serviceId, expectedCount, attempt, setState]);
  return { state, now, retry: () => setAttempt((value) => value + 1) };
}
