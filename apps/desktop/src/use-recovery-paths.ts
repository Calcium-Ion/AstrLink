import { useCallback, useEffect, useState } from "react";
import { listRecoveryPaths } from "./bridge";
import type { RecoveryPathRecord } from "./recovery-path-model";
export function useRecoveryPaths(ready: boolean) {
  const [records, setRecords] = useState<RecoveryPathRecord[]>([]),
    [error, setError] = useState(""),
    [revision, setRevision] = useState(0),
    [loaded, setLoaded] = useState(false);
  const reload = useCallback(() => setRevision((value) => value + 1), []);
  useEffect(() => {
    if (!ready) {
      setLoaded(false);
      return;
    }
    let active = true;
    void (async () => {
      try {
        const next = await listRecoveryPaths();
        if (active) {
          setRecords(next);
          setLoaded(true);
          setError("");
        }
      } catch (error) {
        if (active) {
          setLoaded(false);
          setError(String(error));
        }
      }
    })();
    return () => {
      active = false;
    };
  }, [ready, revision]);
  return { records, error, loaded, reload };
}
