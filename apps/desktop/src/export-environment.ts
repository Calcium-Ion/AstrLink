import { useEffect, useMemo, useState } from "react";

import type { AuditSettings } from "./audit-settings-model";
import {
  getCoreStatus,
  getPreferences,
  getPrivacyPolicy,
  getRoutingSettings,
  listPrivacyModelInstallations,
} from "./bridge";
import type { PrivacyAction, PrivacyDetector } from "./privacy-policy-model";

/**
 * Gateway settings in effect when a record is exported. A record alone cannot
 * say that a local privacy model or a short response timeout shaped it, and
 * whoever diagnoses the export cannot open the sender's settings.
 */
export interface ExportEnvironment {
  version: {
    app: string;
    core: string | null;
    build_commit: string | null;
  } | null;
  privacy: {
    enabled: boolean;
    detector: PrivacyDetector;
    local_model_id: string | null;
    local_model_name: string | null;
    request_action: PrivacyAction;
    response_restore: boolean;
    restore_tool_arguments: boolean;
  } | null;
  limits: {
    response_start_timeout_seconds: number;
    max_concurrent_inspections: number;
    max_request_body_mib: number;
  } | null;
  routing: {
    strategy: string;
    max_attempts: number;
  } | null;
  capture: {
    request_body_enabled: boolean;
    response_content_enabled: boolean;
    http_meta_enabled: boolean;
  } | null;
}

export const EMPTY_EXPORT_ENVIRONMENT: ExportEnvironment = {
  version: null,
  privacy: null,
  limits: null,
  routing: null,
  capture: null,
};

// A section that cannot be read stays null; the export must still work.
async function settle<T>(load: () => Promise<T>): Promise<T | null> {
  try {
    return await load();
  } catch {
    return null;
  }
}

export async function loadExportEnvironment(): Promise<
  Omit<ExportEnvironment, "capture">
> {
  const [version, privacy, limits, routing] = await Promise.all([
    settle(async () => {
      const status = await getCoreStatus();
      return {
        app: status.app_version,
        core: status.version?.core_version ?? null,
        build_commit: status.version?.build_commit ?? null,
      };
    }),
    settle(async () => {
      const { policy } = await getPrivacyPolicy();
      const installations = policy.local_model_id
        ? await settle(listPrivacyModelInstallations)
        : null;
      const model = installations?.items.find(
        (item) => item.id === policy.local_model_id,
      );
      return {
        enabled: policy.enabled,
        detector: policy.detector,
        local_model_id: policy.local_model_id,
        local_model_name: model
          ? `${model.name} · ${model.variant_name}`
          : null,
        request_action: policy.request_action,
        response_restore: policy.response_restore,
        restore_tool_arguments: policy.restore_tool_arguments,
      };
    }),
    settle(async () => {
      const { values } = await getPreferences();
      return {
        response_start_timeout_seconds: values.response_start_timeout_seconds,
        max_concurrent_inspections: values.max_concurrent_inspections,
        max_request_body_mib: values.max_request_body_mib,
      };
    }),
    settle(async () => {
      const settings = await getRoutingSettings();
      return {
        strategy: settings.strategy,
        max_attempts: settings.max_attempts,
      };
    }),
  ]);
  return { version, privacy, limits, routing };
}

/**
 * Prefetched when a detail opens: copying to the clipboard has to finish
 * inside the click, so the export cannot wait on IPC.
 */
export function useExportEnvironment(
  auditSettings: AuditSettings | null,
): ExportEnvironment {
  const [loaded, setLoaded] = useState<Omit<ExportEnvironment, "capture">>(
    EMPTY_EXPORT_ENVIRONMENT,
  );
  useEffect(() => {
    let cancelled = false;
    void loadExportEnvironment().then((environment) => {
      if (!cancelled) setLoaded(environment);
    });
    return () => {
      cancelled = true;
    };
  }, []);
  return useMemo(
    () => ({
      ...loaded,
      capture: auditSettings
        ? {
            request_body_enabled: auditSettings.request_body_enabled,
            response_content_enabled: auditSettings.response_content_enabled,
            http_meta_enabled: auditSettings.http_meta_enabled,
          }
        : null,
    }),
    [loaded, auditSettings],
  );
}
