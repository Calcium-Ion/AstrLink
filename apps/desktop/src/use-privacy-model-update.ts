import { isDesktopHost, invoke } from "./native-host/core";
import { listen } from "./native-host/event";
import { useEffect, useState } from "react";

/**
 * A newer release of the privacy model the policy runs. `available` still
 * needs a download; `ready` is installed and waits for the policy to switch.
 */
export interface PrivacyModelUpdate {
  catalog_id: string;
  name: string;
  version: string;
  phase: "available" | "ready";
}

export function parsePrivacyModelUpdate(
  value: unknown,
): PrivacyModelUpdate | null {
  if (value === null) return null;
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid privacy model update");
  const v = value as Record<string, unknown>;
  if (
    Object.keys(v).sort().join() !== "catalog_id,name,phase,version" ||
    typeof v.catalog_id !== "string" ||
    typeof v.name !== "string" ||
    typeof v.version !== "string" ||
    (v.phase !== "available" && v.phase !== "ready")
  )
    throw new Error("Invalid privacy model update");
  return {
    catalog_id: v.catalog_id,
    name: v.name,
    version: v.version,
    phase: v.phase,
  };
}

/**
 * Mounted at the shell. The host checks in the background, so the reminder
 * reaches operators who never open the privacy page.
 */
export function usePrivacyModelUpdate(): PrivacyModelUpdate | null {
  const [update, setUpdate] = useState<PrivacyModelUpdate | null>(null);
  useEffect(() => {
    if (!isDesktopHost()) return;
    let cancelled = false;
    let unlisten: (() => void) | undefined;
    const accept = (value: unknown) => {
      try {
        if (!cancelled) setUpdate(parsePrivacyModelUpdate(value));
      } catch (error) {
        console.error("Invalid privacy model update", error);
      }
    };
    // Subscribe before reading to close the initial status/event race.
    void listen<unknown>("privacy-model-update", ({ payload }) =>
      accept(payload),
    )
      .then(async (stop) => {
        if (cancelled) {
          stop();
          return;
        }
        unlisten = stop;
        accept(await invoke("privacy_model_update_status"));
      })
      .catch((error) =>
        console.error("Unable to observe privacy model updates", error),
      );
    return () => {
      cancelled = true;
      unlisten?.();
    };
  }, []);
  return update;
}
