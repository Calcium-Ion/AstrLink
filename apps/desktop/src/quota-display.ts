import { useSyncExternalStore } from "react";

export const QUOTA_DISPLAY_MODES = ["remaining", "used"] as const;
export type QuotaDisplayMode = (typeof QUOTA_DISPLAY_MODES)[number];
export const QUOTA_DISPLAY_EVENT = "quota-display-mode-changed";

export function isQuotaDisplayMode(value: unknown): value is QuotaDisplayMode {
  return value === "remaining" || value === "used";
}

let mode: QuotaDisplayMode = "remaining";
const listeners = new Set<() => void>();

export function applyQuotaDisplayMode(next: QuotaDisplayMode): void {
  if (mode === next) return;
  mode = next;
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useQuotaDisplayMode(): QuotaDisplayMode {
  return useSyncExternalStore(
    subscribe,
    () => mode,
    () => "remaining",
  );
}
