import { isWebEdition } from "./edition";
import { webInvoke, RAW_CHANGED } from "./web-transport";
import {
  parseServiceTestResult,
  type ServiceTestInput,
  type ServiceTestResult,
} from "./service-test-model";
import { parseChannelBindingAudit } from "./channel-binding-model";
import {
  parseTerminalChoices,
  type ServiceTerminalRequest,
  type TerminalChoice,
} from "./service-terminal-model";
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
import {
  parseLocalImportOpened,
  parseLocalImportRead,
  type LocalImportOpened,
  type LocalImportRead,
} from "./local-import-model";
import { parseUsageSummary } from "./usage-summary-model";
import { parseDisplayCurrency, type DisplayCurrency } from "./currency-display";
import {
  parsePrivacySummary,
  type PrivacySummary,
} from "./privacy-summary-model";
import {
  hourlyUsageWindow,
  type UsageSummary,
  type UsageWindow,
} from "./usage-range";

import {
  browserSnapshot,
  parseAppSnapshot,
  parseNetworkAddresses,
  type AppSnapshot,
  type NetworkAddressesResponse,
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
  parseAccessTokenSummary,
  parseAccessTokenUsageResponse,
  type AccessTokenCreateResult,
  type AccessTokenPage,
  type AccessTokenServices,
  type AccessTokenSummary,
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
  parseMemoryEntryDetail,
  parseProjectMemory,
  parsePurgeResult,
  parseRequestContextTexts,
  parseRequestRecord,
  parseRequestActivity,
  parseRequestRecordPage,
  parseRequestSessionDetail,
  parseRequestSessionPage,
  type AuditContent,
  type ClientType,
  type MemoryEntryDetail,
  type ProjectMemory,
  type RequestActivity,
  type RequestContextTexts,
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
  parseClaudeCLIStatus,
  type AuthorizationFlow,
  type AuthorizationSession,
  type BeginCodexAuthorizationResult,
  type ClaudeCLIStatus,
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
  parseClientCatalog,
  parseClientConfigApplyOutcome,
  parseClientConfigCopied,
  parseClientConfigPreview,
  parseClientConfigSnippet,
  parseClientConfigOverview,
  parseClientProxyCheck,
  type ClientCatalog,
  type ClientConfigAction,
  type ClientConfigApplyOutcome,
  type ClientConfigClient,
  type ClientConfigItem,
  type ClientConfigModels,
  type ClientConfigOverview,
  type ClientConfigPreview,
  type ClientConfigSnippetFile,
  type ClientProxyCheck,
} from "./client-config-model";
import {
  parseAgentInstallReceipt,
  parseAgentInstallStatus,
  type AgentInstallReceipt,
  type AgentInstallStatus,
  type AgentSkillId,
  type AgentToolTarget,
} from "./agent-install-model";
import {
  parseSyncConfig,
  parseSyncPassphraseResult,
  parseSyncStatus,
  syncConfigPayload,
  type SyncConfig,
  type SyncStatus,
} from "./sync-model";

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
    if (isWebEdition)
      return await webInvoke<T>(
        call[0],
        call[1] as Record<string, unknown> | undefined,
      );
    return await invokeCommand<T>(...call);
  } catch (error) {
    if (typeof error === "string") {
      throw new Error(error);
    }
    throw error;
  }
}

const pendingHistoryReads = new Map<string, Promise<unknown>>();

// Several panels can ask for the same history snapshot at once. Share only
// in-flight reads; settled results and failures must never delay a refresh.
function invokeHistoryRead(
  command: "get_usage_summary" | "get_request_session",
  args: Record<string, unknown>,
): Promise<unknown> {
  const key = JSON.stringify([command, args]);
  const existing = pendingHistoryReads.get(key);
  if (existing) return existing;
  const pending = invoke<unknown>(command, args).finally(() => {
    if (pendingHistoryReads.get(key) === pending)
      pendingHistoryReads.delete(key);
  });
  pendingHistoryReads.set(key, pending);
  return pending;
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
  if (!isWebEdition && !hasNativeBridge()) {
    return browserSnapshot();
  }

  return parseAppSnapshot(await invokeDesktopRead("core_status"));
}

export async function restartCore(): Promise<AppSnapshot> {
  if (!hasNativeBridge()) {
    throw new Error(i18n.t("bridge.restartDesktopOnly"));
  }

  pendingHistoryReads.clear();
  return parseAppSnapshot(await invoke<unknown>("restart_core"));
}

export async function startCore(): Promise<AppSnapshot> {
  requireNativeBridge();
  pendingHistoryReads.clear();
  return parseAppSnapshot(await invoke<unknown>("start_core"));
}

export async function stopCore(): Promise<AppSnapshot> {
  requireNativeBridge();
  pendingHistoryReads.clear();
  return parseAppSnapshot(await invoke<unknown>("stop_core"));
}

export async function getPreferences(): Promise<SettingsSnapshot> {
  requireNativeBridge();
  return parseSettingsSnapshot(await invokeDesktopRead("get_preferences"));
}

/** The currency amounts show in, with today's rate; see `currency.rs`. */
export async function getDisplayCurrency(): Promise<DisplayCurrency> {
  requireNativeBridge();
  return parseDisplayCurrency(await invoke<unknown>("display_currency"));
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

export async function trayPopoverResize(
  height: number,
  devicePixelRatio: number,
): Promise<void> {
  requireNativeBridge();
  await invoke<void>("tray_popover_resize", { height, devicePixelRatio });
}

export async function trayPopoverHide(): Promise<void> {
  requireNativeBridge();
  await invoke<void>("tray_popover_hide");
}

/** The page the tray left for the main window, handed over once. */
export async function takeTrayNavigation(): Promise<string | null> {
  if (!hasNativeBridge()) return null;
  return invoke<string | null>("take_tray_navigation");
}

/**
 * Unsaved edits keep the hidden main window alive; otherwise the host
 * releases it after a while in the tray and builds a fresh one on return.
 */
export async function setMainWindowUnsaved(unsaved: boolean): Promise<void> {
  if (!hasNativeBridge()) return;
  await invoke<void>("set_main_window_unsaved", { unsaved });
}

function requireNativeBridge(): void {
  if (!isWebEdition && !hasNativeBridge()) {
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

export const PACKAGE_STRATEGIES = [
  "auto",
  "soonest_reset",
  "most_remaining",
] as const;
export type PackageStrategy = (typeof PACKAGE_STRATEGIES)[number];
/**
 * Accounts of one provider and plan, or a single service. The core derives
 * membership; `service_ids` keep their routing order.
 */
export interface ServicePackage {
  id: string;
  kind: "subscription" | "service";
  provider?: string;
  plan_type?: string;
  service_ids: string[];
  strategy: PackageStrategy;
}
// Plan keys are whatever the provider reports, lowercased, so only the prefix
// and the absence of control characters are checked here.
const packageIDPattern = /^(service|subscription):\P{Cc}{1,400}$/u;
export function isPackageStrategy(value: unknown): value is PackageStrategy {
  return PACKAGE_STRATEGIES.includes(value as PackageStrategy);
}
export function parseServicePackage(value: unknown): ServicePackage {
  if (!value || typeof value !== "object")
    throw new Error("Invalid service package");
  const { id, kind, provider, plan_type, service_ids, strategy } =
    value as ServicePackage;
  if (
    typeof id !== "string" ||
    !packageIDPattern.test(id) ||
    (kind !== "subscription" && kind !== "service") ||
    (provider !== undefined && typeof provider !== "string") ||
    (plan_type !== undefined && typeof plan_type !== "string") ||
    !Array.isArray(service_ids) ||
    service_ids.length === 0 ||
    service_ids.some(
      (serviceID) =>
        typeof serviceID !== "string" ||
        !/^[a-z][a-z0-9_-]{2,95}$/.test(serviceID),
    ) ||
    new Set(service_ids).size !== service_ids.length ||
    !isPackageStrategy(strategy)
  )
    throw new Error("Invalid service package");
  return {
    id,
    kind,
    ...(provider ? { provider } : {}),
    ...(plan_type ? { plan_type } : {}),
    service_ids,
    strategy,
  };
}
export function parseServicePackages(value: unknown): ServicePackage[] {
  const packages = (value as { packages?: unknown } | null)?.packages;
  if (!Array.isArray(packages)) throw new Error("Invalid service packages");
  const parsed = packages.map(parseServicePackage);
  const members = parsed.flatMap((item) => item.service_ids);
  if (
    new Set(parsed.map((item) => item.id)).size !== parsed.length ||
    new Set(members).size !== members.length
  )
    throw new Error("Invalid service packages");
  return parsed;
}
export async function getServicePackages(): Promise<ServicePackage[]> {
  requireNativeBridge();
  return parseServicePackages(await invoke("get_service_packages"));
}
export async function updateServicePackageStrategy(
  packageId: string,
  strategy: PackageStrategy,
): Promise<ServicePackage> {
  requireNativeBridge();
  if (!packageIDPattern.test(packageId) || !isPackageStrategy(strategy))
    throw new Error("Invalid service package");
  return parseServicePackage(
    await invoke("update_service_package", { packageId, strategy }),
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

export {
  SERVICE_TEST_CANCELLED_ERROR,
  SERVICE_TEST_ALREADY_RUNNING_ERROR,
  SERVICE_TEST_INVALID_ID_ERROR,
} from "./service-test-cancellation";

export async function testService(
  serviceId: string,
  input: ServiceTestInput,
  testId?: string,
): Promise<ServiceTestResult> {
  requireNativeBridge();
  return parseServiceTestResult(
    await invoke<unknown>("test_service", {
      serviceId,
      input,
      ...(testId ? { testId } : {}),
    }),
  );
}

export async function cancelServiceTest(
  serviceId: string,
  testId: string,
): Promise<boolean> {
  requireNativeBridge();
  return invoke<boolean>("cancel_service_test", { serviceId, testId });
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

/** The claude CLI official Claude accounts run. */
export async function getClaudeCLI(): Promise<ClaudeCLIStatus> {
  requireNativeBridge();
  return parseClaudeCLIStatus(await invoke<unknown>("get_claude_cli"));
}

/** Starts the official installer; only the server edition offers it. */
export async function installClaudeCLI(): Promise<ClaudeCLIStatus> {
  requireNativeBridge();
  return parseClaudeCLIStatus(await invoke<unknown>("install_claude_cli"));
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
  if (query.models !== undefined && query.models.length > 0) {
    compact.models = query.models;
  }
  if (query.status !== undefined) compact.status = query.status;
  if (query.client_type !== undefined) {
    compact.client_type = query.client_type;
  }
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

/**
 * The spans of model calls running at or after `since`, for the live board.
 * It covers every model call, whatever the request list is filtered to.
 */
export async function getRequestActivity(
  since: string,
): Promise<RequestActivity> {
  requireNativeBridge();
  return parseRequestActivity(
    await invoke<unknown>("get_request_activity", { since }),
  );
}

export async function getRequestSession(
  sessionId: string,
): Promise<RequestSessionDetail> {
  requireNativeBridge();
  return parseRequestSessionDetail(
    await invokeHistoryRead("get_request_session", { sessionId }),
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
  input:
    | { scope: "all" }
    | { scope: "before"; before: string }
    | { scope: "sessions"; session_ids: string[] },
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

/** What the texts of a call's captured body read: rules, memory, summary. */
export async function getRequestContextTexts(
  requestId: string,
): Promise<RequestContextTexts> {
  requireNativeBridge();
  return parseRequestContextTexts(
    await invoke<unknown>("get_request_context_texts", { requestId }),
  );
}

/**
 * Which project's memory to read: a project, or the calls that named none;
 * without either, the project of the most recent call. Without a client, the
 * one that called there last. `day_start` is when today began here, so the
 * daily reads follow the local day.
 */
export type ProjectMemoryQuery = {
  client_type?: ClientType;
  day_start?: string;
} & (
  | { project?: string; no_project?: never }
  | { no_project: true; project?: never }
);

export async function getProjectMemory(
  query: ProjectMemoryQuery,
): Promise<ProjectMemory> {
  requireNativeBridge();
  return parseProjectMemory(
    await invoke<unknown>("get_project_memory", { query }),
  );
}

/** One memory of a project's view and each turn that noted or read it. */
export async function getProjectMemoryEntry(
  query: ProjectMemoryQuery & { file: string },
): Promise<MemoryEntryDetail> {
  requireNativeBridge();
  return parseMemoryEntryDetail(
    await invoke<unknown>("get_project_memory_entry", { query }),
  );
}

export async function listRawAccess(): Promise<RawAccessList> {
  requireNativeBridge();
  return parseRawAccessList(await invoke<unknown>("list_raw_access"));
}

/**
 * Decides one agent raw access request. An approval always carries a proof:
 * the raw password, which the host forwards once and keeps no copy of, or a
 * request for Touch ID, which the host prompts for itself. Denying carries
 * none.
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

/** Ends a running lasting grant before it ends on its own. */
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

/**
 * Opens the operator's raw unlock session. On the desktop it lasts until the
 * screen locks, the computer sleeps, or the operator locks; Core ends an idle
 * one where it keeps that timeout.
 */
export async function unlockRaw(proof: RawProof): Promise<RawSealingOutcome> {
  requireNativeBridge();
  return parseRawSealingOutcome(await invoke<unknown>("unlock_raw", { proof }));
}

/**
 * Lets Touch ID open raw content beside the raw password, which this takes
 * once. The host keeps the key it generates in the keychain.
 */
export async function enrollRawBiometric(
  password: string,
): Promise<RawSealingOutcome> {
  requireNativeBridge();
  return parseRawSealingOutcome(
    await invoke<unknown>("enroll_raw_biometric", { password }),
  );
}

/** Stops Touch ID from opening raw content; the raw password keeps working. */
export async function removeRawBiometric(): Promise<RawSealingStatus> {
  requireNativeBridge();
  return parseRawSealingStatus(await invoke<unknown>("remove_raw_biometric"));
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
  if (isWebEdition) {
    window.addEventListener(RAW_CHANGED, onChange);
    return () => window.removeEventListener(RAW_CHANGED, onChange);
  }
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

/** Addresses other devices reach this machine by, read from Core on each call. */
export async function listNetworkAddresses(): Promise<NetworkAddressesResponse> {
  requireNativeBridge();
  return parseNetworkAddresses(await invoke<unknown>("list_network_addresses"));
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
    await invokeHistoryRead("get_usage_summary", {
      from: window.from,
      to: window.to,
      timeZone: window.time_zone || "UTC",
      bucket: hourlyUsageWindow(window) ? "hour" : "day",
    }),
    window,
  );
}

/** What the privacy policy did over the window, in the usage summary's buckets. */
export async function getPrivacySummary(
  window: UsageWindow,
): Promise<PrivacySummary> {
  requireNativeBridge();
  return parsePrivacySummary(
    await invoke<unknown>("get_privacy_summary", {
      from: window.from,
      to: window.to,
      timeZone: window.time_zone || "UTC",
      bucket: hourlyUsageWindow(window) ? "hour" : "day",
    }),
    window,
  );
}

export async function createAccessToken(
  name: string,
  services?: AccessTokenServices,
): Promise<AccessTokenCreateResult> {
  requireNativeBridge();
  return parseAccessTokenCreateResult(
    await invoke<unknown>("create_access_token", {
      name,
      serviceScope: services?.service_scope ?? null,
      serviceIds: services?.service_ids ?? null,
    }),
  );
}

/** Renames a token and replaces the providers it may use. */
export async function updateAccessToken(
  tokenId: string,
  name: string,
  services: AccessTokenServices,
): Promise<AccessTokenSummary> {
  requireNativeBridge();
  return parseAccessTokenSummary(
    await invoke<unknown>("update_access_token", {
      tokenId,
      name,
      serviceScope: services.service_scope,
      serviceIds: services.service_ids,
    }),
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
  client: ClientConfigClient;
  models: ClientConfigModels;
  inferenceUrl: string;
}

/**
 * Every client the setup dialog lists and how AstrLink reaches each. Outside
 * the desktop there are no local clients.
 */
export async function getClientCatalog(): Promise<ClientCatalog> {
  if (!hasNativeBridge()) return { clients: [] };
  return parseClientCatalog(await invoke<unknown>("client_catalog"));
}

/**
 * What AstrLink wrote to each client of `catalog`, on this computer and in
 * running WSL distributions. Outside the desktop there are no local clients.
 */
export async function getClientConfigStatus(
  inferenceUrl: string | null,
  catalog: ClientCatalog,
): Promise<ClientConfigOverview> {
  if (!hasNativeBridge())
    return {
      clients: [],
      wsl_unchecked: [],
      wsl_localhost: false,
      installed: [],
    };
  return parseClientConfigOverview(
    await invoke<unknown>("client_config_status", { inferenceUrl }),
    catalog,
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

/**
 * What writing or removing one client's config would change, file by file.
 * The token stays in the host; the diff shows its hint.
 */
export async function previewClientConfigChanges(input: {
  tokenId: string;
  client: ClientConfigClient;
  /** The WSL distribution to write in; `null` is this computer. */
  wsl: string | null;
  /** The write target of a client with several; `null` otherwise. */
  target: string | null;
  action: ClientConfigAction;
  models: ClientConfigModels;
  inferenceUrl: string;
}): Promise<ClientConfigPreview> {
  requireNativeBridge();
  return parseClientConfigPreview(
    await invoke<unknown>("preview_client_config_changes", { ...input }),
  );
}

/**
 * Writes or removes several clients' configs. Unless `confirmed`, nothing is
 * written while any item replaces settings AstrLink did not write, switches
 * tokens or gateways, or removes a config; those come back to confirm.
 */
export async function applyClientConfigs(input: {
  tokenId: string | null;
  inferenceUrl: string;
  items: ClientConfigItem[];
  confirmed: boolean;
}): Promise<ClientConfigApplyOutcome> {
  requireNativeBridge();
  return parseClientConfigApplyOutcome(
    await invoke<unknown>("apply_client_configs", { ...input }),
    input.items,
  );
}

/**
 * Opens a client's import link for a token. The host builds the link, so
 * the token never reaches the webview.
 */
export async function openClientImportLink(input: {
  tokenId: string;
  client: ClientConfigClient;
  protocol: string;
  inferenceUrl: string;
}): Promise<void> {
  requireNativeBridge();
  await invoke("open_client_import_link", { ...input });
}

/** Removes AstrLink's config and restores the values it replaced. */
export async function removeClientConfig(
  client: ClientConfigClient,
  wsl: string | null,
  target: string | null,
): Promise<void> {
  requireNativeBridge();
  await invoke("remove_client_config", { client, wsl, target });
}

/** The config for a fresh setup, file by file, with the token's hint. */
export async function previewClientConfigSnippet(
  input: ClientConfigTarget,
): Promise<ClientConfigSnippetFile[]> {
  requireNativeBridge();
  return parseClientConfigSnippet(
    await invoke<unknown>("preview_client_config_snippet", { ...input }),
  );
}

/**
 * Has the host copy one file of the config, `file` indexing the preview's
 * files, with the real token to the clipboard. False when the clipboard
 * refused it.
 */
export async function copyClientConfigSnippet(
  input: ClientConfigTarget & { file: number },
): Promise<boolean> {
  requireNativeBridge();
  return parseClientConfigCopied(
    await invoke<unknown>("copy_client_config_snippet", { ...input }),
  );
}

/** Terminal apps installed here, the platform's own first. */
export async function listTerminalApps(): Promise<TerminalChoice[]> {
  if (!hasNativeBridge()) return [];
  return parseTerminalChoices(await invoke<unknown>("list_terminal_apps"));
}

/** The folder the user picked, or null when they closed the picker. */
export async function pickTerminalDirectory(
  start: string | null,
): Promise<string | null> {
  requireNativeBridge();
  const picked = await invoke<unknown>("pick_terminal_directory", { start });
  return typeof picked === "string" ? picked : null;
}

/** Opens a terminal whose client reaches the gateway through one provider. */
export async function openServiceTerminal(
  request: ServiceTerminalRequest,
): Promise<void> {
  requireNativeBridge();
  await invoke("open_service_terminal", { request });
}

/** Whether the OS has an app registered for CC Switch's links. */
export async function isCCSwitchInstalled(): Promise<boolean> {
  if (!hasNativeBridge()) return false;
  return (await invoke<unknown>("cc_switch_installed")) === true;
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

function requireLocalFiles(): void {
  if (!hasNativeBridge()) throw new Error(i18n.t("bridge.desktopOnly"));
}

/** Finds the agent tools' config files and starts an import; reads none. */
export async function openLocalImport(): Promise<LocalImportOpened> {
  requireLocalFiles();
  return parseLocalImportOpened(await invoke<unknown>("local_import_sources"));
}

/** Reads the checked sources; keys come back only as hints. */
export async function readLocalImport(
  session: string,
  sources: string[],
): Promise<LocalImportRead> {
  requireLocalFiles();
  return parseLocalImportRead(
    await invoke<unknown>("read_local_import", { session, sources }),
  );
}

/** The one model-list request a found provider gets; the host adds its key. */
export async function probeLocalImportModels(
  session: string,
  candidate: string,
  input: DraftServiceModelProbeInput,
): Promise<ServiceModelProbe> {
  requireLocalFiles();
  return parseServiceModelProbe(
    await invoke<unknown>("probe_local_import_models", {
      session,
      candidate,
      input,
    }),
  );
}

/** Creates a found provider; the host attaches the key it read. */
export async function confirmLocalImport(
  session: string,
  candidate: string,
  input: HTTPServiceCreateInput,
): Promise<ServiceRecord> {
  requireLocalFiles();
  return parseServiceRecord(
    await invoke<unknown>("confirm_local_import", {
      session,
      candidate,
      input,
    }),
  );
}

/** Ends the import; the host drops every key it read. */
export async function dismissLocalImport(session: string): Promise<void> {
  if (!hasNativeBridge()) return;
  await invoke("dismiss_local_import", { session });
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

/**
 * Newest compatible releases of catalog models, pinned to their commits.
 * `refresh` skips Core's hour-long cache; only an operator's refresh sets it.
 */
export async function getPrivacyModelReleases(
  refresh = false,
): Promise<PrivacyModelCatalog> {
  requireNativeBridge();
  return parsePrivacyModelCatalog(
    await invoke<unknown>("get_privacy_model_releases", { refresh }),
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
  tools: AgentToolTarget[],
): Promise<AgentInstallReceipt> {
  requireNativeBridge();
  return parseAgentInstallReceipt(
    await invoke<unknown>("install_agent_debug", { skillIds, tools }),
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

/** The configuration-sync surface, over the host's `sync_config` command. */
export async function getSyncStatus(): Promise<SyncStatus> {
  requireNativeBridge();
  return parseSyncStatus(
    await invoke<unknown>("sync_config", { operation: "status" }),
  );
}

export async function getSyncConfig(): Promise<SyncConfig> {
  requireNativeBridge();
  return parseSyncConfig(
    await invoke<unknown>("sync_config", { operation: "config" }),
  );
}

export async function configureSync(input: SyncConfig): Promise<SyncConfig> {
  requireNativeBridge();
  return parseSyncConfig(
    await invoke<unknown>("sync_config", {
      operation: "configure",
      input: syncConfigPayload(input),
    }),
  );
}

export async function setSyncPassphrase(passphrase: string): Promise<boolean> {
  requireNativeBridge();
  const result = parseSyncPassphraseResult(
    await invoke<unknown>("sync_config", {
      operation: "passphrase",
      input: { passphrase },
    }),
  );
  return result.passphrase_set;
}

/** Forgets the passphrase locally; the remote package is left alone. */
export async function forgetSyncPassphrase(): Promise<void> {
  requireNativeBridge();
  await invoke("sync_config", { operation: "forget" });
}

export async function runSync(): Promise<SyncStatus> {
  requireNativeBridge();
  // A run does several remote round trips and may retry the merge, so the host
  // gives it minutes rather than the usual control-request timeout.
  return parseSyncStatus(
    await invoke<unknown>("sync_config", { operation: "run" }),
  );
}
