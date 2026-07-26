import { invoke } from "@tauri-apps/api/core";

import {
  browserSnapshot,
  parseAppSnapshot,
  type AppSnapshot,
} from "./core-model";
import {
  parseEndpointPage,
  parseEndpointRecord,
  type EndpointCreateInput,
  type EndpointPage,
  type EndpointPatchInput,
  type EndpointRecord,
} from "./endpoint-model";
import {
  parseAccessTokenCreateResult,
  parseAccessTokenPage,
  parseAccessTokenRevealResult,
  type AccessTokenCreateResult,
  type AccessTokenPage,
  type AccessTokenRevealResult,
} from "./access-token-model";
import {
  parsePrivacyDryRunResult,
  parsePrivacyModelCatalog,
  parsePrivacyModelInstallation,
  parsePrivacyModelInstallationList,
  parsePrivacyModelProbe,
  parsePrivacyPolicyPage,
  parsePrivacyPolicyRecord,
  validatePrivacyDryRunInput,
  validatePrivacyModelInstallationID,
  validatePrivacyModelInstallInput,
  validatePrivacyModelProbeInput,
  type PrivacyDryRunInput,
  type PrivacyDryRunResult,
  type PrivacyModelCatalog,
  type PrivacyModelInstallation,
  type PrivacyModelInstallationList,
  type PrivacyModelInstallInput,
  type PrivacyModelProbe,
  type PrivacyModelProbeInput,
  type PrivacyPolicyPage,
  type PrivacyPolicyPatch,
  type PrivacyPolicyRecord,
} from "./privacy-policy-model";
import {
  parseAuditContent,
  parsePurgeResult,
  parseRequestRecord,
  parseRequestRecordPage,
  type AuditContent,
  type RequestRecord,
  type RequestRecordListQuery,
  type RequestRecordPage,
} from "./request-record-model";
import {
  parseAuditSettings,
  type AuditSettings,
  type AuditSettingsPatch,
} from "./audit-settings-model";

function hasNativeBridge(): boolean {
  return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
}

export async function getCoreStatus(): Promise<AppSnapshot> {
  if (!hasNativeBridge()) {
    return browserSnapshot();
  }

  return parseAppSnapshot(await invoke<unknown>("core_status"));
}

export async function restartCore(): Promise<AppSnapshot> {
  if (!hasNativeBridge()) {
    throw new Error("Core restart is only available in the AstrLink desktop app.");
  }

  return parseAppSnapshot(await invoke<unknown>("restart_core"));
}

function requireNativeBridge(): void {
  if (!hasNativeBridge()) {
    throw new Error("该操作仅可在 AstrLink 桌面应用中使用。");
  }
}

export async function listEndpoints(): Promise<EndpointPage> {
  requireNativeBridge();
  return parseEndpointPage(await invoke<unknown>("list_endpoints"));
}

export async function getEndpoint(endpointId: string): Promise<EndpointRecord> {
  requireNativeBridge();
  return parseEndpointRecord(
    await invoke<unknown>("get_endpoint", { endpointId }),
  );
}

export async function createEndpoint(
  input: EndpointCreateInput,
): Promise<EndpointRecord> {
  requireNativeBridge();
  return parseEndpointRecord(await invoke<unknown>("create_endpoint", { input }));
}

export async function updateEndpoint(
  endpointId: string,
  etag: string,
  patch: EndpointPatchInput,
): Promise<EndpointRecord> {
  requireNativeBridge();
  return parseEndpointRecord(
    await invoke<unknown>("update_endpoint", { endpointId, etag, patch }),
  );
}

export async function deleteEndpoint(endpointId: string, etag: string): Promise<void> {
  requireNativeBridge();
  await invoke("delete_endpoint", { endpointId, etag });
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
  if (query.endpoint_id !== undefined) compact.endpoint_id = query.endpoint_id;
  if (query.status !== undefined) compact.status = query.status;
  return compact;
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
  return parsePrivacyPolicyPage(
    await invoke<unknown>("list_privacy_policies"),
  );
}

export async function getPrivacyPolicy(): Promise<PrivacyPolicyRecord> {
  requireNativeBridge();
  return parsePrivacyPolicyRecord(
    await invoke<unknown>("get_privacy_policy"),
  );
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
