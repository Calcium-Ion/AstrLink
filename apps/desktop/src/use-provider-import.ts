import { useEffect, useMemo, useRef, useState } from "react";
import { isTauri } from "@tauri-apps/api/core";

import {
  dismissProviderImport,
  getPendingProviderImport,
  listenProviderImport,
} from "./bridge";
import { i18n } from "./i18n";
import { notify } from "./notify";
import {
  planProviderImport,
  type ProviderImportLink,
  type ProviderImportNotice,
  type ProviderImportPlan,
  type ProviderImportReason,
} from "./provider-import-model";
import type { ProtocolDescriptor } from "./service-presets";

const fieldKeys = new Set([
  "kind",
  "name",
  "base_url",
  "api_key",
  "auth",
  "auth_header",
  "protocols",
  "models",
]);

/** Why a link was refused, in the words of the fields the user can see. */
export function providerImportRejection(
  reason: ProviderImportReason,
  field: string | null,
): string {
  const t = i18n.t.bind(i18n);
  let detail: string;
  if (reason === "malformed") {
    detail = t("providerImport.reasons.malformed");
  } else if (reason === "unknown_parameter") {
    detail = field
      ? t("providerImport.reasons.unknownParameter", { field })
      : t("providerImport.reasons.unknownParameterUnnamed");
  } else if (field === null || !fieldKeys.has(field)) {
    detail = t("providerImport.reasons.unsupported");
  } else {
    const label = t(`providerImport.fields.${field}`);
    detail =
      reason === "unsupported"
        ? t("providerImport.reasons.unsupportedField", { field: label })
        : reason === "duplicate_parameter"
          ? t("providerImport.reasons.duplicateParameter", { field: label })
          : reason === "missing_parameter"
            ? t("providerImport.reasons.missingParameter", { field: label })
            : t("providerImport.reasons.invalidParameter", { field: label });
  }
  return t("providerImport.rejected", { reason: detail });
}

function refuse(
  id: string,
  reason: ProviderImportReason,
  field: string | null,
) {
  notify.error(providerImportRejection(reason, field));
  dismissProviderImport(id).catch((cause: unknown) =>
    console.error("Unable to dismiss the provider link", cause),
  );
}

/**
 * Follows `astrlink://` provider links. A link waits for `enabled` (Core
 * ready) before its confirmation opens.
 */
export function useProviderImport({
  enabled,
  protocols,
}: {
  enabled: boolean;
  protocols: readonly ProtocolDescriptor[];
}) {
  const [pending, setPending] = useState<{
    id: string;
    provider: ProviderImportLink;
  } | null>(null);
  const refused = useRef<string | null>(null);

  useEffect(() => {
    if (!isTauri()) return;
    let cancelled = false;
    let stop: (() => void) | null = null;
    const receive = (notice: ProviderImportNotice | null) => {
      if (cancelled || notice === null) return;
      if (notice.status === "pending") {
        setPending((current) => (current?.id === notice.id ? current : notice));
        return;
      }
      // The event and the launch catch-up can both deliver one link.
      if (refused.current === notice.id) return;
      refused.current = notice.id;
      setPending(null);
      refuse(notice.id, notice.reason, notice.field);
    };
    listenProviderImport(receive)
      .then((unlisten) => {
        if (cancelled) unlisten();
        else stop = unlisten;
      })
      .catch((error: unknown) =>
        console.error("Unable to observe AstrLink provider links", error),
      );
    // A link that launched the app arrived before this listener.
    getPendingProviderImport()
      .then(receive)
      .catch((error: unknown) =>
        console.error("Unable to read the pending provider link", error),
      );
    return () => {
      cancelled = true;
      stop?.();
    };
  }, []);

  const planned = useMemo(
    () =>
      pending && enabled
        ? {
            id: pending.id,
            result: planProviderImport(pending.provider, protocols),
          }
        : null,
    [enabled, pending, protocols],
  );

  useEffect(() => {
    if (planned?.result.ok !== false) return;
    setPending(null);
    refuse(planned.id, planned.result.reason, planned.result.field);
  }, [planned]);

  const active: { id: string; plan: ProviderImportPlan } | null =
    planned?.result.ok === true
      ? { id: planned.id, plan: planned.result.plan }
      : null;
  return {
    active,
    close: () => setPending(null),
  };
}
