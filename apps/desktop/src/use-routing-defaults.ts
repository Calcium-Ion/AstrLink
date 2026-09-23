import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { useCallback, useEffect, useState } from "react";
import { getRoutingSettings } from "./bridge";
import {
  defaultFailurePolicy,
  type RoutingSettings,
} from "./failure-policy-model";

export function useRoutingDefaults(
  ready: boolean,
): RoutingSettings & { loaded: boolean; reload: () => void } {
  const [revision, setRevision] = useState(0);
  const reload = useCallback(() => setRevision((value) => value + 1), []);
  const [settings, setSettings] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  const fallback: RoutingSettings = {
    default_failure_policy: defaultFailurePolicy(),
    allow_unmatched_failover: false,
    strategy: "failover_only",
    max_attempts: 6,
  };
  useEffect(() => {
    if (!ready) return;
    let active = true;
    void (async () => {
      try {
        const loaded = await getRoutingSettings();
        if (active && loaded) {
          setSettings(loaded);
        }
      } catch {
        /* Editing explicit overrides remains possible while Core reconnects. */
      }
    })();
    return () => {
      active = false;
    };
  }, [ready, revision, setSettings]);
  return {
    ...(settings ?? fallback),
    loaded: ready && settings !== null,
    reload,
  };
}
