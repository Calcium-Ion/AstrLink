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
  const [loaded, setLoaded] = useState(false);
  const [settings, setSettings] = useState<RoutingSettings>(() => ({
    default_failure_policy: defaultFailurePolicy(),
    allow_unmatched_failover: false,
    strategy: "failover_only",
    max_attempts: 6,
  }));
  useEffect(() => {
    if (!ready) {
      setLoaded(false);
      return;
    }
    let active = true;
    setLoaded(false);
    void (async () => {
      try {
        const loaded = await getRoutingSettings();
        if (active && loaded) {
          setSettings(loaded);
          setLoaded(true);
        }
      } catch {
        /* Editing explicit overrides remains possible while Core reconnects. */
      }
    })();
    return () => {
      active = false;
    };
  }, [ready, revision]);
  return { ...settings, loaded, reload };
}
