import { useEffect, useState } from "react";

import type { AccessTokenCatalog } from "./AccessTokenManager";
import type { ServiceCatalog } from "./Overview";
import type { UsageState } from "./usage-range";

export const ONBOARDING_STORAGE_KEY = "astrlink.onboarding.v1";
type OnboardingStatus = "active" | "dismissed" | "complete";

function readStatus(): OnboardingStatus | null {
  try {
    const value = localStorage.getItem(ONBOARDING_STORAGE_KEY);
    return value === "active" || value === "dismissed" || value === "complete"
      ? value
      : null;
  } catch {
    return null;
  }
}

export function useOnboarding({
  isReady,
  catalog,
  tokenCatalog,
  usage,
}: {
  isReady: boolean;
  catalog: ServiceCatalog;
  tokenCatalog: AccessTokenCatalog;
  usage: UsageState;
}) {
  const [status, setStatus] = useState(readStatus);
  const [showResumeHint, setShowResumeHint] = useState(false);
  useEffect(() => {
    if (!showResumeHint) return;
    const timer = window.setTimeout(() => setShowResumeHint(false), 6500);
    return () => window.clearTimeout(timer);
  }, [showResumeHint]);
  const catalogsReady =
    isReady &&
    catalog.status === "ready" &&
    !catalog.stale &&
    tokenCatalog.status === "ready" &&
    !tokenCatalog.stale;
  const hasActivity = Boolean(
    usage.summary &&
      (usage.summary.scanned_records ||
        usage.summary.totals.requests ||
        usage.summary.totals.failed_requests ||
        usage.summary.totals.total_tokens ||
        usage.summary.by_service.length ||
        usage.summary.by_model.length),
  );
  const firstVisit =
    status === null &&
    catalogsReady &&
    usage.status === "ready" &&
    usage.summary !== null &&
    !hasActivity &&
    catalog.items.length === 0 &&
    tokenCatalog.items.length === 0;

  const save = (next: OnboardingStatus) => {
    setStatus(next);
    try {
      localStorage.setItem(ONBOARDING_STORAGE_KEY, next);
    } catch {
      // The current session still works when persistent storage is unavailable.
    }
  };
  useEffect(() => {
    if (firstVisit) save("active");
  }, [firstVisit]);

  const serviceReady = catalog.items.some(
    (service) =>
      service.enabled &&
      service.models.length > 0 &&
      (service.subscription
        ? service.subscription.status === "connected"
        : Boolean(service.http)),
  );
  const tokenReady = tokenCatalog.items.length > 0;
  const requestReady =
    usage.status === "ready" && (usage.summary?.totals.requests ?? 0) > 0;
  const step = !serviceReady ? 0 : !tokenReady ? 1 : 2;

  return {
    active: status === "active" || firstVisit,
    catalogsReady,
    serviceReady,
    tokenReady,
    requestReady,
    step,
    showResumeHint,
    dismissResumeHint: () => setShowResumeHint(false),
    open: () => {
      setShowResumeHint(false);
      save("active");
    },
    dismiss: () => {
      save("dismissed");
      setShowResumeHint(true);
    },
    complete: () => save("complete"),
  };
}
