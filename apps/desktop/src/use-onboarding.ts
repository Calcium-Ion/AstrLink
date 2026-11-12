import { useEffect, useState } from "react";

import type { AccessTokenSummary } from "./access-token-model";
import type { AccessTokenCatalog } from "./AccessTokenManager";
import { i18n } from "./i18n";
import { notify } from "./notify";
import type { ServiceCatalog } from "./Overview";
import { anyTokenConfigured, useClientConfigs } from "./use-client-configs";
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

/**
 * The token the guide sets clients up with: the bootstrap token while it
 * exists, else the oldest one. Core lists tokens oldest first; a token
 * created a moment ago may sit first until the list is read again.
 */
export function onboardingToken(
  tokens: readonly AccessTokenSummary[],
): AccessTokenSummary | undefined {
  return (
    tokens.find((token) => token.source === "system_default") ??
    [...tokens].sort((a, b) => a.created_at.localeCompare(b.created_at))[0]
  );
}

/**
 * The first-run guide. Its last step is done once a client holds one of this
 * installation's tokens, as the token list marks it, or a request succeeded.
 * With every step done the guide closes by itself unless something holds it
 * open. A guide the user reopens closes so only after they finish a step it
 * still had open; opened with every step done, it stays until they leave.
 */
export function useOnboarding({
  isReady,
  catalog,
  tokenCatalog,
  usage,
  inferenceURL,
  enabled,
}: {
  isReady: boolean;
  catalog: ServiceCatalog;
  tokenCatalog: AccessTokenCatalog;
  usage: UsageState;
  /** The address clients reach the gateway at, as the client configs name it. */
  inferenceURL: string;
  /** False in the web edition, which has no guide and no local clients. */
  enabled: boolean;
}) {
  const [status, setStatus] = useState(readStatus);
  // Set while the guide shows something the user still reads.
  const [held, setHeld] = useState(false);
  // Whether the guide may close by itself. A guide left open from an earlier
  // run may; one the user reopens waits until it sees a step still open once
  // every source was read, so a finished guide can be looked at again.
  const [armed, setArmed] = useState(status === "active");
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
  // Core issues a bootstrap token on its first start, so a fresh install
  // already lists one; only tokens someone created show earlier setup.
  const firstVisit =
    status === null &&
    catalogsReady &&
    usage.status === "ready" &&
    usage.summary !== null &&
    !hasActivity &&
    catalog.items.length === 0 &&
    tokenCatalog.items.every((token) => token.source === "system_default");

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
  const active = enabled && (status === "active" || firstVisit);
  // Read only while the guide is open; the token lists read their own.
  const { configs: clientConfigs, loaded: clientsLoaded } = useClientConfigs(
    inferenceURL,
    active,
  );
  const clientReady = anyTokenConfigured(clientConfigs, tokenCatalog.items);
  const requestReady =
    usage.status === "ready" && (usage.summary?.totals.requests ?? 0) > 0;
  const connected = clientReady || requestReady;
  const step = !serviceReady ? 0 : !tokenReady ? 1 : 2;
  const finished = catalogsReady && serviceReady && tokenReady && connected;

  const settled =
    catalogsReady &&
    clientsLoaded &&
    (usage.status === "ready" || usage.status === "error");

  useEffect(() => {
    if (!active) setArmed(false);
    else if (settled && !finished) setArmed(true);
  }, [active, settled, finished]);
  useEffect(() => {
    if (!active || !armed || !finished || held) return;
    save("complete");
    notify.success(i18n.t("onboarding.done"));
  }, [active, armed, finished, held]);

  return {
    active,
    catalogsReady,
    serviceReady,
    tokenReady,
    connected,
    finished,
    step,
    /** Keeps a finished guide open while `true`. */
    hold: setHeld,
    showResumeHint,
    dismissResumeHint: () => setShowResumeHint(false),
    open: () => {
      setShowResumeHint(false);
      setArmed(false);
      save("active");
    },
    dismiss: () => {
      save("dismissed");
      setShowResumeHint(true);
    },
    complete: () => save("complete"),
  };
}
