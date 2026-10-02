import {
  parseServiceTestResult,
  type ServiceTestInput,
  type ServiceTestResult,
} from "./service-test-model";
import { parseChannelBindingAudit } from "./channel-binding-model";
import {
  parseClientIdentities,
  parseRoutingSettings,
  type ClientIdentities,
  type RoutingSettings,
} from "./failure-policy-model";
import { invoke as invokeCommand } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import {
  parseServiceProxyProbe,
  type ServiceProxyProbeInput,
  type ServiceProxyProbeResult,
} from "./service-proxy-model";

import { i18n } from "./i18n";
import {
  parseProviderImportNotice,
  type ProviderImportNotice,
} from "./provider-import-model";
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
  parseSubscriptionRiskEvents,
  type HTTPServiceCreateInput,
  type ServiceCreateInput,
  type ServicePage,
  type ServicePatchInput,
  type ServiceRecord,
  type ServiceModelProbe,
  type DraftServiceModelProbeInput,
  type ModelDiscoveryProtocol,
  type SubscriptionRiskEvent,
} from "./service-model";
import {
  parseAccessTokenCreateResult,
  parseAccessTokenCopied,
  parseAccessTokenPage,
  parseAccessTokenUsageResponse,
  type AccessTokenCreateResult,
  type AccessTokenPage,
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
  parseRawAccessGrant,
  parseRawAccessList,
  parseRawAccessProofOutcome,
  type RawAccessDecision,
  type RawAccessGrant,
  type RawAccessList,
  type RawAccessProofOutcome,
} from "./raw-access-model";
import {
  parseRawSealingOutcome,
  parseRawSealingState,
  parseRawSealingStatus,
  type RawPasswordAction,
  type RawProof,
  type RawSealingOutcome,
  type RawSealingState,
  type RawSealingStatus,
} from "./raw-sealing-model";
import {
  parseAuditSettings,
  type AuditSettings,
  type AuditSettingsPatch,
} from "./audit-settings-model";
import { parseLocalDataStatus, type LocalDataStatus } from "./local-data-model";
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
  parseResetCreditsDetails,
  type ResetCreditsDetails,
  type SubscriptionUsage,
  type SubscriptionUsageReset,
} from "./subscription-usage-model";
import {
  parseSettingsSnapshot,
  type Preferences,
  type SettingsSnapshot,
  type TrayPreferences,
} from "./preferences-model";
import { parseTrayState, type TrayAction, type TrayState } from "./tray-model";
import { downloadTextFile } from "./download-text-file";
import {
  parseClientConfigApplyOutcome,
  parseClientConfigCopied,
  parseClientConfigSnippet,
  parseClientConfigStatuses,
  parseClientProxyCheck,
  type ClientConfigApplyOutcome,
  type ClientConfigClient,
  type ClientConfigModels,
  type ClientConfigStatus,
  type ClientProxyCheck,
  type DirectClient,
} from "./client-config-model";
import {
  parseAgentInstallReceipt,
  parseAgentInstallStatus,
  type AgentInstallReceipt,
  type AgentInstallStatus,
  type AgentSkillId,
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

/**
 * The snapshot the tray popover renders. Settings pass a draft of the tray
 * preferences to preview the panel exactly as the tray would show it.
 */
export async function getTrayState(tray?: TrayPreferences): Promise<TrayState> {
  requireNativeBridge();
  return parseTrayState(
    await invoke<unknown>("tray_state", { tray: tray ?? null }),
  );
}

export async function trayAction(action: TrayAction): Promise<void> {
  requireNativeBridge();
  await invoke<void>("tray_action", { action });
}

export async function trayPopoverResize(height: number): Promise<void> {
  requireNativeBridge();
  await invoke<void>("tray_popover_resize", { height });
}

export async function trayPopoverHide(): Promise<void> {
  requireNativeBridge();
  await invoke<void>("tray_popover_hide");
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

/**
 * `fresh` makes Core query the provider instead of serving its 30s quota
 * snapshot; pass it only for an operator's explicit refresh.
 */
export async function getServiceUsage(
  serviceId: string,
  options: { fresh?: boolean } = {},
): Promise<SubscriptionUsage> {
  requireNativeBridge();
  return parseSubscriptionUsage(
    await invoke<unknown>("get_service_usage", {
      serviceId,
      fresh: options.fresh ?? false,
    }),
  );
}

export async function getServiceResetCredits(
  serviceId: string,
): Promise<ResetCreditsDetails> {
  requireNativeBridge();
  return parseResetCreditsDetails(
    await invoke<unknown>("get_service_reset_credits", { serviceId }),
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

export interface CustomModelListInput {
  proxy?: import("./service-proxy-model").ServiceProxyInput | null;
  service_id?: string;
  kind: import("./service-model").HTTPServiceKind;
  http: {
    base_url: string;
    auth: import("./service-model").ServiceAuth;
    credential?: { secret: string };
    model_list_path: string;
  };
}

export interface CustomModelListResult {
  model_ids: string[];
  warnings?: string[];
}

export async function fetchCustomModelList(
  input: CustomModelListInput,
): Promise<CustomModelListResult> {
  // Route through the existing probe_draft_service_models command rather than
  // a separate Tauri command. The prober already handles custom kind with any
  // OpenAI-compatible /v1/models endpoint via ProtocolOpenAIModels.
  const probe = await probeDraftServiceModels({
    ...(input.proxy !== undefined ? { proxy: input.proxy } : {}),
    ...(input.service_id !== undefined ? { service_id: input.service_id } : {}),
    kind: input.kind,
    http: input.http,
    protocol: "openai.models",
  });
  return { model_ids: probe.model_ids };
}

export async function probeServiceProxy(
  input: ServiceProxyProbeInput,
): Promise<ServiceProxyProbeResult> {
  requireNativeBridge();
  return parseServiceProxyProbe(await invoke("probe_service_proxy", { input }));
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

/**
 * Restores scheduling for a subscription paused by an upstream risk signal.
 * Credentials are kept; the provider may pause the account again.
 */
export async function clearServiceRisk(
  serviceId: string,
): Promise<ServiceRecord> {
  requireNativeBridge();
  return parseServiceRecord(
    await invoke<unknown>("clear_service_risk", { serviceId }),
  );
}

/** Recent upstream risk history of a subscription, newest first. */
export async function listServiceRiskEvents(
  serviceId: string,
  limit?: number,
): Promise<SubscriptionRiskEvent[]> {
  requireNativeBridge();
  return parseSubscriptionRiskEvents(
    await invoke<unknown>("list_service_risk_events", {
      serviceId,
      limit: limit ?? null,
    }),
    serviceId,
  );
}

function compactQuery(
  query: RequestRecordListQuery,
): Record<string, string | number | string[]> {
  const compact: Record<string, string | number | string[]> = {};
  if (query.limit !== undefined) compact.limit = query.limit;
  if (query.cursor !== undefined) compact.cursor = query.cursor;
  if (query.from !== undefined) compact.from = query.from;
  if (query.to !== undefined) compact.to = query.to;
  if (query.protocol !== undefined) compact.protocol = query.protocol;
  if (query.service_id !== undefined) compact.service_id = query.service_id;
  if (
    query.local_access_token_ids !== undefined &&
    query.local_access_token_ids.length > 0
  ) {
    compact.local_access_token_ids = query.local_access_token_ids;
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

export async function listRawAccess(): Promise<RawAccessList> {
  requireNativeBridge();
  return parseRawAccessList(await invoke<unknown>("list_raw_access"));
}

/**
 * Decides one agent raw access request. While Core is unlocked an approval
 * needs no proof; otherwise it carries the raw password, which the host
 * forwards once and keeps no copy of. Denying carries none.
 */
export async function decideRawAccess(
  grantId: string,
  decision: RawAccessDecision,
  proof?: RawProof,
): Promise<RawAccessProofOutcome> {
  requireNativeBridge();
  return parseRawAccessProofOutcome(
    await invoke<unknown>("decide_raw_access", {
      grantId,
      decision,
      proof: decision === "deny" ? null : (proof ?? null),
    }),
  );
}

/** Ends a running timed grant before it expires. */
export async function revokeRawGrant(grantId: string): Promise<RawAccessGrant> {
  requireNativeBridge();
  return parseRawAccessGrant(
    await invoke<unknown>("revoke_raw_grant", { grantId }),
  );
}

export async function getRawSealingStatus(): Promise<RawSealingState> {
  requireNativeBridge();
  return parseRawSealingState(await invoke<unknown>("raw_sealing_status"));
}

/** Opens the operator's raw unlock session; Core ends it after idling. */
export async function unlockRaw(proof: RawProof): Promise<RawSealingOutcome> {
  requireNativeBridge();
  return parseRawSealingOutcome(await invoke<unknown>("unlock_raw", { proof }));
}

/**
 * Accepts a raw key replaced outside the desktop, such as by the operator's
 * own `astrlink-core raw-password`. It takes that key's password; Core only
 * checks it, so raw content stays locked.
 */
export async function acknowledgeRawKey(
  proof: RawProof,
): Promise<RawSealingOutcome> {
  requireNativeBridge();
  return parseRawSealingOutcome(
    await invoke<unknown>("acknowledge_raw_key", { proof }),
  );
}

export async function lockRaw(): Promise<RawSealingStatus> {
  requireNativeBridge();
  return parseRawSealingStatus(await invoke<unknown>("lock_raw"));
}

/**
 * Calls `onChange` whenever any window locks, unlocks, or changes the raw
 * password. Core's own idle lock sends nothing; watch the unlock expiry too.
 */
export async function listenRawSealingChanged(
  onChange: () => void,
): Promise<() => void> {
  if (!hasNativeBridge()) return () => {};
  return listen("raw-sealing-changed", () => onChange());
}

/**
 * Sets, changes, or resets the raw password. `password` is the new one;
 * `proof` opens the existing key where Core needs it.
 */
export async function setRawPassword(
  action: RawPasswordAction,
  password?: string,
  proof?: RawProof,
): Promise<RawSealingOutcome> {
  requireNativeBridge();
  return parseRawSealingOutcome(
    await invoke<unknown>("set_raw_password", {
      action,
      password: password ?? null,
      proof: proof ?? null,
    }),
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

/** Counts of saved data this device can no longer decrypt. */
export async function getLocalDataStatus(): Promise<LocalDataStatus> {
  requireNativeBridge();
  return parseLocalDataStatus(await invoke<unknown>("local_data_status"));
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

/**
 * Has the host copy the token to the clipboard; the token never reaches the
 * webview. False when the clipboard refused it.
 */
export async function copyAccessToken(tokenId: string): Promise<boolean> {
  requireNativeBridge();
  return parseAccessTokenCopied(
    await invoke<unknown>("copy_access_token", { tokenId }),
  );
}

export async function deleteAccessToken(tokenId: string): Promise<void> {
  requireNativeBridge();
  await invoke("delete_access_token", { tokenId });
}

export interface ClientConfigTarget {
  tokenId: string;
  client: DirectClient;
  models: ClientConfigModels;
  inferenceUrl: string;
}

/**
 * What AstrLink wrote to Claude Code and Codex. Outside the desktop there are
 * no local clients to configure.
 */
export async function getClientConfigStatus(
  inferenceUrl: string | null,
): Promise<ClientConfigStatus[]> {
  if (!hasNativeBridge()) return [];
  return parseClientConfigStatuses(
    await invoke<unknown>("client_config_status", { inferenceUrl }),
  );
}

/**
 * Whether the system proxy keeps Codex from reaching the gateway. Read-only:
 * nothing on the system changes. Outside the desktop there is no Codex.
 */
export async function checkClientProxy(
  inferenceUrl: string,
): Promise<ClientProxyCheck | null> {
  if (!hasNativeBridge()) return null;
  return parseClientProxyCheck(
    await invoke<unknown>("check_client_proxy", { inferenceUrl }),
  );
}

/** Writes the connection, or names the keys that need `replace` first. */
export async function applyClientConfig(
  input: ClientConfigTarget & { replace: boolean },
): Promise<ClientConfigApplyOutcome> {
  requireNativeBridge();
  return parseClientConfigApplyOutcome(
    await invoke<unknown>("apply_client_config", { ...input }),
  );
}

export async function removeClientConfig(client: DirectClient): Promise<void> {
  requireNativeBridge();
  await invoke("remove_client_config", { client });
}

/** The config for a fresh setup, with the token shown as its hint. */
export async function previewClientConfigSnippet(
  input: ClientConfigTarget,
): Promise<string> {
  requireNativeBridge();
  return parseClientConfigSnippet(
    await invoke<unknown>("preview_client_config_snippet", { ...input }),
  );
}

/**
 * Has the host copy the config with the real token to the clipboard. False
 * when the clipboard refused it.
 */
export async function copyClientConfigSnippet(
  input: ClientConfigTarget,
): Promise<boolean> {
  requireNativeBridge();
  return parseClientConfigCopied(
    await invoke<unknown>("copy_client_config_snippet", { ...input }),
  );
}

/** Whether the OS has an app registered for CC Switch's import links. */
export async function isCCSwitchInstalled(): Promise<boolean> {
  if (!hasNativeBridge()) return false;
  return (await invoke<unknown>("cc_switch_installed")) === true;
}

/** CC Switch names the provider after the token. */
export async function openCCSwitchImport(input: {
  tokenId: string;
  client: ClientConfigClient;
  models: ClientConfigModels;
  inferenceUrl: string;
}): Promise<void> {
  requireNativeBridge();
  await invoke("open_cc_switch_import", input);
}

/** The provider link waiting for confirmation, e.g. the one that launched the app. */
export async function getPendingProviderImport(): Promise<ProviderImportNotice | null> {
  if (!hasNativeBridge()) return null;
  const value = await invoke<unknown>("pending_provider_import");
  return value === null ? null : parseProviderImportNotice(value);
}

export async function listenProviderImport(
  onNotice: (notice: ProviderImportNotice) => void,
): Promise<() => void> {
  if (!hasNativeBridge()) return () => {};
  return listen<unknown>("provider-import", ({ payload }) => {
    onNotice(parseProviderImportNotice(payload));
  });
}

/** Creates the linked provider; the host attaches the link's API key. */
export async function confirmProviderImport(
  id: string,
  input: HTTPServiceCreateInput,
): Promise<ServiceRecord> {
  requireNativeBridge();
  return parseServiceRecord(
    await invoke<unknown>("confirm_provider_import", { id, input }),
  );
}

export async function dismissProviderImport(id: string): Promise<void> {
  if (!hasNativeBridge()) return;
  await invoke("dismiss_provider_import", { id });
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

/** Newest compatible releases of catalog models, pinned to their commits. */
export async function getPrivacyModelReleases(): Promise<PrivacyModelCatalog> {
  requireNativeBridge();
  return parsePrivacyModelCatalog(
    await invoke<unknown>("get_privacy_model_releases"),
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
  skillIds: AgentSkillId[],
  toolIds: AgentToolId[],
): Promise<AgentInstallReceipt> {
  requireNativeBridge();
  return parseAgentInstallReceipt(
    await invoke<unknown>("install_agent_debug", { skillIds, toolIds }),
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
export async function getClientIdentities(): Promise<ClientIdentities> {
  requireNativeBridge();
  return parseClientIdentities(await invoke<unknown>("get_client_identities"));
}

export async function builtinToolAction(
  kind: import("./builtin-tools-model").BuiltinToolKind,
  action: "status" | "save_key" | "delete_key" | "test",
  input?: unknown,
): Promise<Record<string, unknown>> {
  requireNativeBridge();
  return invoke<Record<string, unknown>>("builtin_tool_action", {
    kind,
    action,
    input,
  });
}
