import {
  parseServiceTestResult,
  type ServiceTestInput,
  type ServiceTestResult,
} from "./service-test-model";
import { parseChannelBindingAudit } from "./channel-binding-model";
import {
  parseRecoveryPath,
  parseRecoveryPathRecord,
  parseRecoveryPathPage,
  parseRecoveryPreview,
  type RecoveryPathInput,
  type RecoveryPreviewInput,
} from "./recovery-path-model";
import {
  parseRoutingSettings,
  type RoutingSettings,
} from "./failure-policy-model";
import { invoke as invokeCommand } from "@tauri-apps/api/core";

import { i18n } from "./i18n";
import { parseUsageSummary } from "./usage-summary-model";
import type { UsageSummary, UsageWindow } from "./usage-range";

import {
  browserSnapshot,
  parseAppSnapshot,
  type AppSnapshot,
} from "./core-model";
import {
  parseServicePage,
  parseServiceModelProbe,
  parseServiceRecord,
  type ServiceCreateInput,
  type ServicePage,
  type ServicePatchInput,
  type ServiceRecord,
  type ServiceModelProbe,
  type DraftServiceModelProbeInput,
  type ModelDiscoveryProtocol,
} from "./service-model";
import {
  parseAccessTokenCreateResult,
  parseAccessTokenPage,
  parseAccessTokenRevealResult,
  parseAccessTokenUsageResponse,
  type AccessTokenCreateResult,
  type AccessTokenPage,
  type AccessTokenRevealResult,
  type AccessTokenUsageResponse,
} from "./access-token-model";
import {
  parsePrivacyDryRunResult,
  parsePrivacyModelCatalog,
  parsePrivacyModelInstallation,
  parsePrivacyModelInstallationList,
  parsePrivacyModelProbe,
  parsePrivacyPolicyPage,
  parsePrivacyPolicyRecord,
  parsePrivacyRegexBuiltinRules,
  validateLocalProbeInput,
  validatePrivacyDryRunInput,
  validatePrivacyModelInstallationID,
  validatePrivacyModelInstallInput,
  validatePrivacyModelProbeInput,
  type PrivacyDryRunInput,
  type PrivacyDryRunResult,
  type LocalProbeInput,
  type PrivacyModelCatalog,
  type PrivacyModelInstallation,
  type PrivacyModelInstallationList,
  type PrivacyModelInstallInput,
  type PrivacyModelProbe,
  type PrivacyModelProbeInput,
  type PrivacyPolicyPage,
  type PrivacyPolicyPatch,
  type PrivacyPolicyRecord,
  type PrivacyRegexBuiltinRules,
} from "./privacy-policy-model";
import {
  parseAuditContent,
  parsePurgeResult,
  parseRequestRecord,
  parseRequestRecordPage,
  parseRequestSessionDetail,
  parseRequestSessionPage,
  type AuditContent,
  type RequestRecord,
  type RequestRecordListQuery,
  type RequestRecordPage,
  type RequestSessionDetail,
  type RequestSessionListQuery,
  type RequestSessionPage,
} from "./request-record-model";
import {
  parseAuditSettings,
  type AuditSettings,
  type AuditSettingsPatch,
} from "./audit-settings-model";
import {
  parseRoutePage,
  parseRouteRecord,
  type RouteCreateInput,
  type RoutePage,
  type RoutePatchInput,
  type RouteRecord,
} from "./route-model";
import {
  parseAuthorizationSession,
  parseBeginCodexAuthorizationResult,
  type AuthorizationFlow,
  type AuthorizationSession,
  type BeginCodexAuthorizationResult,
} from "./subscription-model";
import {
  parseSubscriptionUsage,
  parseSubscriptionUsageReset,
  type SubscriptionUsage,
  type SubscriptionUsageReset,
} from "./subscription-usage-model";
import {
  parseSettingsSnapshot,
  type Preferences,
  type SettingsSnapshot,
} from "./preferences-model";
import { downloadTextFile } from "./download-text-file";
import {
  parseAgentInstallReceipt,
  parseAgentInstallStatus,
  type AgentInstallReceipt,
  type AgentInstallStatus,
  type AgentToolId,
} from "./agent-install-model";

function hasNativeBridge(): boolean {
  return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
}

// A Tauri command that returns Err(String) rejects with the bare string, which
// every screen's error handler discards in favour of its generic fallback. The
// diagnosis then reads "无法读取…" no matter whether Core was unreachable or
// returned a field the interface refused. Carrying the reason across keeps the
// specific message on screen.
async function invoke<T>(
  ...call: Parameters<typeof invokeCommand>
): Promise<T> {
  try {
    return await invokeCommand<T>(...call);
  } catch (error) {
    if (typeof error === "string") {
      throw new Error(error);
    }
    throw error;
  }
}

// These local reads should finish promptly. A stuck native event loop must not
// leave settings loading forever or keep displaying an old ready snapshot.
async function invokeDesktopRead(
  command: "core_status" | "get_preferences",
): Promise<unknown> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      invoke<unknown>(command),
      new Promise<never>((_, reject) => {
        timer = setTimeout(
          () => reject(new Error(i18n.t("bridge.desktopUnresponsive"))),
          10_000,
        );
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

export async function getCoreStatus(): Promise<AppSnapshot> {
  if (!hasNativeBridge()) {
    return browserSnapshot();
  }

  return parseAppSnapshot(await invokeDesktopRead("core_status"));
}

export async function restartCore(): Promise<AppSnapshot> {
  if (!hasNativeBridge()) {
    throw new Error(i18n.t("bridge.restartDesktopOnly"));
  }

  return parseAppSnapshot(await invoke<unknown>("restart_core"));
}

export async function startCore(): Promise<AppSnapshot> {
  requireNativeBridge();
  return parseAppSnapshot(await invoke<unknown>("start_core"));
}

export async function stopCore(): Promise<AppSnapshot> {
  requireNativeBridge();
  return parseAppSnapshot(await invoke<unknown>("stop_core"));
}

export async function getPreferences(): Promise<SettingsSnapshot> {
  requireNativeBridge();
  return parseSettingsSnapshot(await invokeDesktopRead("get_preferences"));
}

export async function updatePreferences(
  input: Preferences,
): Promise<SettingsSnapshot> {
  requireNativeBridge();
  return parseSettingsSnapshot(
    await invoke<unknown>("update_preferences", { input }),
  );
}

function requireNativeBridge(): void {
  if (!hasNativeBridge()) {
    throw new Error(i18n.t("bridge.desktopOnly"));
  }
}

export async function listServices(): Promise<ServicePage> {
  requireNativeBridge();
  return parseServicePage(await invoke<unknown>("list_services"));
}

export interface ServiceOrderRecord {
  service_ids: string[];
  etag: string;
}
export function parseServiceOrder(value: unknown): ServiceOrderRecord {
  if (!value || typeof value !== "object")
    throw new Error("Invalid service order");
  const { service_ids, etag } = value as ServiceOrderRecord;
  if (
    !Array.isArray(service_ids) ||
    service_ids.some(
      (id) => typeof id !== "string" || !/^[a-z][a-z0-9_-]{2,95}$/.test(id),
    ) ||
    new Set(service_ids).size !== service_ids.length ||
    typeof etag !== "string" ||
    !/^"[^"\r\n]+"$/.test(etag)
  )
    throw new Error("Invalid service order");
  return { service_ids, etag };
}
export async function getServiceOrder(): Promise<ServiceOrderRecord> {
  requireNativeBridge();
  return parseServiceOrder(await invoke("get_service_order"));
}
export async function updateServiceOrder(
  serviceIds: string[],
  etag: string,
): Promise<ServiceOrderRecord> {
  requireNativeBridge();
  parseServiceOrder({ service_ids: serviceIds, etag });
  return parseServiceOrder(
    await invoke("update_service_order", { serviceIds, etag }),
  );
}

export async function getService(serviceId: string): Promise<ServiceRecord> {
  requireNativeBridge();
  return parseServiceRecord(
    await invoke<unknown>("get_service", { serviceId }),
  );
}

export async function createService(
  input: ServiceCreateInput,
): Promise<ServiceRecord> {
  requireNativeBridge();
  return parseServiceRecord(await invoke<unknown>("create_service", { input }));
}

export async function updateService(
  serviceId: string,
  etag: string,
  patch: ServicePatchInput,
): Promise<ServiceRecord> {
  requireNativeBridge();
  return parseServiceRecord(
    await invoke<unknown>("update_service", { serviceId, etag, patch }),
  );
}

export async function deleteService(
  serviceId: string,
  etag: string,
): Promise<void> {
  requireNativeBridge();
  await invoke("delete_service", { serviceId, etag });
}

export async function getServiceUsage(
  serviceId: string,
): Promise<SubscriptionUsage> {
  requireNativeBridge();
  return parseSubscriptionUsage(
    await invoke<unknown>("get_service_usage", { serviceId }),
  );
}

export async function resetServiceUsage(
  serviceId: string,
): Promise<SubscriptionUsageReset> {
  requireNativeBridge();
  return parseSubscriptionUsageReset(
    await invoke<unknown>("reset_service_usage", { serviceId }),
  );
}

export async function testService(
  serviceId: string,
  input: ServiceTestInput,
): Promise<ServiceTestResult> {
  requireNativeBridge();
  return parseServiceTestResult(
    await invoke<unknown>("test_service", { serviceId, input }),
  );
}

export async function probeServiceModels(
  serviceId: string,
  protocol: ModelDiscoveryProtocol,
): Promise<ServiceModelProbe> {
  requireNativeBridge();
  return parseServiceModelProbe(
    await invoke<unknown>("probe_service_models", {
      serviceId,
      input: { protocol },
    }),
  );
}

export async function probeDraftServiceModels(
  input: DraftServiceModelProbeInput,
): Promise<ServiceModelProbe> {
  requireNativeBridge();
  return parseServiceModelProbe(
    await invoke<unknown>("probe_draft_service_models", { input }),
  );
}

export async function beginServiceAuthorization(
  serviceId: string,
  flow: AuthorizationFlow,
): Promise<BeginCodexAuthorizationResult> {
  requireNativeBridge();
  return parseBeginCodexAuthorizationResult(
    await invoke<unknown>("begin_service_authorization", { serviceId, flow }),
  );
}

export async function openAuthorizationURL(url: string): Promise<void> {
  requireNativeBridge();
  await invoke("open_authorization_url", { url });
}

export async function openExternalURL(url: string): Promise<void> {
  requireNativeBridge();
  await invoke("open_external_url", { url });
}

export async function completeServiceAuthorization(
  serviceId: string,
  sessionId: string,
  code: string,
): Promise<AuthorizationSession> {
  requireNativeBridge();
  return parseAuthorizationSession(
    await invoke<unknown>("complete_service_authorization", {
      serviceId,
      sessionId,
      code,
    }),
  );
}

export async function getServiceAuthorization(
  serviceId: string,
): Promise<AuthorizationSession> {
  requireNativeBridge();
  return parseAuthorizationSession(
    await invoke<unknown>("get_service_authorization", { serviceId }),
  );
}

export async function cancelServiceAuthorization(
  serviceId: string,
): Promise<AuthorizationSession> {
  requireNativeBridge();
  return parseAuthorizationSession(
    await invoke<unknown>("cancel_service_authorization", { serviceId }),
  );
}

export async function logoutService(serviceId: string): Promise<ServiceRecord> {
  requireNativeBridge();
  return parseServiceRecord(
    await invoke<unknown>("logout_service", { serviceId }),
  );
}

export async function listRoutes(): Promise<RoutePage> {
  requireNativeBridge();
  return parseRoutePage(await invoke<unknown>("list_routes"));
}

export async function getRoute(routeId: string): Promise<RouteRecord> {
  requireNativeBridge();
  return parseRouteRecord(await invoke<unknown>("get_route", { routeId }));
}

export async function createRoute(
  input: RouteCreateInput,
): Promise<RouteRecord> {
  requireNativeBridge();
  return parseRouteRecord(await invoke<unknown>("create_route", { input }));
}

export async function updateRoute(
  routeId: string,
  etag: string,
  patch: RoutePatchInput,
): Promise<RouteRecord> {
  requireNativeBridge();
  return parseRouteRecord(
    await invoke<unknown>("update_route", { routeId, etag, patch }),
  );
}

export async function deleteRoute(
  routeId: string,
  etag: string,
): Promise<void> {
  requireNativeBridge();
  await invoke("delete_route", { routeId, etag });
}

function compactQuery(
  query: RequestRecordListQuery,
): Record<string, string | number> {
  const compact: Record<string, string | number> = {};
  if (query.limit !== undefined) compact.limit = query.limit;
  if (query.cursor !== undefined) compact.cursor = query.cursor;
  if (query.from !== undefined) compact.from = query.from;
  if (query.to !== undefined) compact.to = query.to;
  if (query.protocol !== undefined) compact.protocol = query.protocol;
  if (query.service_id !== undefined) compact.service_id = query.service_id;
  if (query.local_access_token_id !== undefined) {
    compact.local_access_token_id = query.local_access_token_id;
  }
  if (query.status !== undefined) compact.status = query.status;
  return compact;
}

export async function listRequestSessions(
  query: RequestSessionListQuery = {},
): Promise<RequestSessionPage> {
  requireNativeBridge();
  return parseRequestSessionPage(
    await invoke<unknown>("list_request_sessions", {
      query: {
        ...compactQuery(query),
        ...(query.kind === undefined ? {} : { kind: query.kind }),
      },
    }),
  );
}

export async function getRequestSession(
  sessionId: string,
): Promise<RequestSessionDetail> {
  requireNativeBridge();
  return parseRequestSessionDetail(
    await invoke<unknown>("get_request_session", { sessionId }),
  );
}

export async function getSessionChannelBindings(
  sessionId: string,
  before?: number,
) {
  requireNativeBridge();
  return parseChannelBindingAudit(
    await invoke<unknown>("get_session_channel_bindings", {
      sessionId,
      ...(before === undefined ? {} : { before }),
    }),
  );
}

export async function releaseSessionChannelBindings(sessionId: string) {
  requireNativeBridge();
  return parseChannelBindingAudit(
    await invoke<unknown>("release_session_channel_bindings", { sessionId }),
  );
}

export async function listRequestRecords(
  query: RequestRecordListQuery = {},
): Promise<RequestRecordPage> {
  requireNativeBridge();
  return parseRequestRecordPage(
    await invoke<unknown>("list_request_records", {
      query: compactQuery(query),
    }),
  );
}

export async function getRequestRecord(
  requestId: string,
): Promise<RequestRecord> {
  requireNativeBridge();
  return parseRequestRecord(
    await invoke<unknown>("get_request_record", { requestId }),
  );
}

export async function listRequestRecordChildren(
  requestId: string,
): Promise<RequestRecordPage> {
  requireNativeBridge();
  return parseRequestRecordPage(
    await invoke<unknown>("list_request_record_children", { requestId }),
  );
}

export async function deleteRequestRecord(requestId: string): Promise<void> {
  requireNativeBridge();
  await invoke("delete_request_record", { requestId });
}

export async function purgeRequestRecords(
  input: { scope: "all" } | { scope: "before"; before: string },
): Promise<{ deleted_records: number; deleted_audit_blobs: number }> {
  requireNativeBridge();
  return parsePurgeResult(
    await invoke<unknown>("purge_request_records", {
      input: { ...input, confirm: true },
    }),
  );
}

export async function getRequestAuditContent(
  requestId: string,
): Promise<AuditContent> {
  requireNativeBridge();
  return parseAuditContent(
    await invoke<unknown>("get_request_audit_content", { requestId }),
  );
}

export async function getAuditSettings(): Promise<AuditSettings> {
  requireNativeBridge();
  return parseAuditSettings(await invoke<unknown>("get_audit_settings"));
}

export async function updateAuditSettings(
  patch: AuditSettingsPatch,
): Promise<AuditSettings> {
  requireNativeBridge();
  return parseAuditSettings(
    await invoke<unknown>("update_audit_settings", { patch }),
  );
}

export async function listAccessTokens(): Promise<AccessTokenPage> {
  requireNativeBridge();
  return parseAccessTokenPage(await invoke<unknown>("list_access_tokens"));
}

export async function listAccessTokenUsage(
  todayFrom: string,
): Promise<AccessTokenUsageResponse> {
  requireNativeBridge();
  return parseAccessTokenUsageResponse(
    await invoke<unknown>("list_access_token_usage", { todayFrom }),
  );
}

export async function getUsageSummary(
  window: UsageWindow,
): Promise<UsageSummary> {
  requireNativeBridge();
  return parseUsageSummary(
    await invoke<unknown>("get_usage_summary", {
      from: window.from,
      to: window.to,
      timeZone: window.time_zone || "UTC",
      bucket: window.preset === "1d" ? "hour" : "day",
    }),
    window,
  );
}

export async function createAccessToken(
  name: string,
): Promise<AccessTokenCreateResult> {
  requireNativeBridge();
  return parseAccessTokenCreateResult(
    await invoke<unknown>("create_access_token", { name }),
  );
}

export async function revealAccessToken(
  tokenId: string,
): Promise<AccessTokenRevealResult> {
  requireNativeBridge();
  return parseAccessTokenRevealResult(
    await invoke<unknown>("reveal_access_token", { tokenId }),
  );
}

export async function deleteAccessToken(tokenId: string): Promise<void> {
  requireNativeBridge();
  await invoke("delete_access_token", { tokenId });
}

export async function listPrivacyPolicies(): Promise<PrivacyPolicyPage> {
  requireNativeBridge();
  return parsePrivacyPolicyPage(await invoke<unknown>("list_privacy_policies"));
}

export async function getPrivacyPolicy(): Promise<PrivacyPolicyRecord> {
  requireNativeBridge();
  return parsePrivacyPolicyRecord(await invoke<unknown>("get_privacy_policy"));
}

export async function updatePrivacyPolicy(
  etag: string,
  patch: PrivacyPolicyPatch,
): Promise<PrivacyPolicyRecord> {
  requireNativeBridge();
  return parsePrivacyPolicyRecord(
    await invoke<unknown>("update_privacy_policy", { etag, patch }),
  );
}

export async function dryRunPrivacyPolicy(
  input: PrivacyDryRunInput,
): Promise<PrivacyDryRunResult> {
  requireNativeBridge();
  const validated = validatePrivacyDryRunInput(input);
  return parsePrivacyDryRunResult(
    await invoke<unknown>("dry_run_privacy_policy", { input: validated }),
  );
}

export async function getPrivacyRegexBuiltinRules(): Promise<PrivacyRegexBuiltinRules> {
  requireNativeBridge();
  return parsePrivacyRegexBuiltinRules(
    await invoke<unknown>("get_privacy_regex_builtin_rules"),
  );
}

export async function getPrivacyModelCatalog(): Promise<PrivacyModelCatalog> {
  requireNativeBridge();
  return parsePrivacyModelCatalog(
    await invoke<unknown>("get_privacy_model_catalog"),
  );
}

export async function probePrivacyModel(
  input: PrivacyModelProbeInput,
): Promise<PrivacyModelProbe> {
  requireNativeBridge();
  const validated = validatePrivacyModelProbeInput(input);
  return parsePrivacyModelProbe(
    await invoke<unknown>("probe_privacy_model", { input: validated }),
  );
}

export async function probeLocalPrivacyModel(
  input: LocalProbeInput,
): Promise<PrivacyModelProbe> {
  requireNativeBridge();
  const validated = validateLocalProbeInput(input);
  return parsePrivacyModelProbe(
    await invoke<unknown>("probe_local_privacy_model", { input: validated }),
  );
}

export async function listPrivacyModelInstallations(): Promise<PrivacyModelInstallationList> {
  requireNativeBridge();
  return parsePrivacyModelInstallationList(
    await invoke<unknown>("list_privacy_model_installations"),
  );
}

export async function installPrivacyModel(
  input: PrivacyModelInstallInput,
): Promise<PrivacyModelInstallation> {
  requireNativeBridge();
  const validated = validatePrivacyModelInstallInput(input);
  return parsePrivacyModelInstallation(
    await invoke<unknown>("install_privacy_model", { input: validated }),
  );
}

export async function getPrivacyModelInstallation(
  installationId: string,
): Promise<PrivacyModelInstallation> {
  requireNativeBridge();
  const validated = validatePrivacyModelInstallationID(installationId);
  return parsePrivacyModelInstallation(
    await invoke<unknown>("get_privacy_model_installation", {
      installationId: validated,
    }),
  );
}

export async function pausePrivacyModelInstallation(
  installationId: string,
): Promise<PrivacyModelInstallation> {
  requireNativeBridge();
  const validated = validatePrivacyModelInstallationID(installationId);
  return parsePrivacyModelInstallation(
    await invoke<unknown>("pause_privacy_model_installation", {
      installationId: validated,
    }),
  );
}

export async function resumePrivacyModelInstallation(
  installationId: string,
): Promise<PrivacyModelInstallation> {
  requireNativeBridge();
  const validated = validatePrivacyModelInstallationID(installationId);
  return parsePrivacyModelInstallation(
    await invoke<unknown>("resume_privacy_model_installation", {
      installationId: validated,
    }),
  );
}

async function removePrivacyModelInstallation(
  installationId: string,
): Promise<void> {
  requireNativeBridge();
  const validated = validatePrivacyModelInstallationID(installationId);
  await invoke("delete_privacy_model_installation", {
    installationId: validated,
  });
}

export async function cancelPrivacyModelInstallation(
  installationId: string,
): Promise<void> {
  await removePrivacyModelInstallation(installationId);
}

export async function deletePrivacyModelInstallation(
  installationId: string,
): Promise<void> {
  await removePrivacyModelInstallation(installationId);
}

export async function getAgentDebugStatus(): Promise<AgentInstallStatus> {
  requireNativeBridge();
  return parseAgentInstallStatus(await invoke<unknown>("agent_debug_status"));
}

export async function installAgentDebug(
  toolIds: AgentToolId[],
): Promise<AgentInstallReceipt> {
  requireNativeBridge();
  return parseAgentInstallReceipt(
    await invoke<unknown>("install_agent_debug", { toolIds }),
  );
}

export async function uninstallAgentDebug(): Promise<void> {
  requireNativeBridge();
  await invoke("uninstall_agent_debug");
}

export async function saveTextFile(
  defaultFilename: string,
  contents: string,
): Promise<string | null> {
  if (!hasNativeBridge()) {
    downloadTextFile(defaultFilename, contents);
    return defaultFilename;
  }
  return invoke<string | null>("save_text_file", {
    defaultFilename,
    contents,
  });
}

export async function getRoutingSettings(): Promise<RoutingSettings> {
  requireNativeBridge();
  return parseRoutingSettings(await invoke<unknown>("get_routing_settings"));
}
export async function updateRoutingSettings(
  patch: Partial<RoutingSettings>,
): Promise<RoutingSettings> {
  requireNativeBridge();
  return parseRoutingSettings(
    await invoke<unknown>("update_routing_settings", { patch }),
  );
}

export async function listRecoveryPaths() {
  requireNativeBridge();
  return parseRecoveryPathPage(
    await invoke("recovery_paths", { operation: "list" }),
  );
}
export async function getRecoveryPath(id: string) {
  requireNativeBridge();
  return parseRecoveryPathRecord(
    await invoke("recovery_paths", { operation: "get", id }),
  );
}
export async function createRecoveryPath(input: RecoveryPathInput) {
  requireNativeBridge();
  parseRecoveryPath({ ...input, id: "path_validation" });
  return parseRecoveryPathRecord(
    await invoke("recovery_paths", { operation: "create", input }),
  );
}
export async function updateRecoveryPath(
  id: string,
  etag: string,
  input: RecoveryPathInput,
) {
  requireNativeBridge();
  parseRecoveryPath({ ...input, id });
  const patch = {
    targets: null,
    steps: null,
    strategy: null,
    max_attempts: null,
    failure_policy: null,
    ...input,
  };
  return parseRecoveryPathRecord(
    await invoke("recovery_paths", {
      operation: "update",
      id,
      etag,
      input: patch,
    }),
  );
}
export async function deleteRecoveryPath(id: string, etag: string) {
  requireNativeBridge();
  await invoke("recovery_paths", { operation: "delete", id, etag });
}
export async function previewRecoveryPath(input: RecoveryPreviewInput) {
  requireNativeBridge();
  parseRecoveryPath(input.path);
  return parseRecoveryPreview(
    await invoke("recovery_paths", { operation: "preview", input }),
  );
}
