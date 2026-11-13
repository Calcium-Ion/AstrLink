import { useEffect } from "react";

import { getQuotaEstimates } from "./pricing-bridge";
import type { ServiceQuotaEstimate } from "./pricing-model";
import type { UsageEntry } from "./service-package-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

/**
 * Core's estimated whole quota windows. Core records a window with every
 * usage read, so a newer read asks again. A failed read keeps the last
 * estimates.
 */
export function useQuotaEstimates(
  ready: boolean,
  usage: Record<string, UsageEntry | undefined>,
): ServiceQuotaEstimate[] {
  const [estimates, setEstimates] = useWorkspaceSnapshot<
    ServiceQuotaEstimate[]
  >("quota-estimates", []);
  const reads = Object.entries(usage)
    .map(([id, entry]) => `${id}:${entry?.usage?.fetched_at ?? ""}`)
    .sort()
    .join("\0");
  useEffect(() => {
    if (!ready) return;
    let cancelled = false;
    getQuotaEstimates().then(
      (value) => {
        if (!cancelled) setEstimates(value);
      },
      () => undefined,
    );
    return () => {
      cancelled = true;
    };
  }, [ready, reads, setEstimates]);
  return estimates;
}
