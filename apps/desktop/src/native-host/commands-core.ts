import {
  Audit,
  Core,
  Preferences,
  Pricing,
  Privacy,
  Raw,
  Records,
  Routing,
  Services,
  Shell,
  Sync,
  Tokens,
  Usage,
} from "../mygo";
import type { CommandTable } from "./types";

// Commands bound in commands_core.go: Core's lifecycle, preferences, links,
// raw content and the control API commands. Optional arguments the
// frontend leaves out arrive as null.
export const coreCommands = {
  core_status: () => Core.coreStatus(),
  start_core: () => Core.startCore(),
  stop_core: () => Core.stopCore(),
  restart_core: () => Core.restartCore(),

  get_preferences: () => Preferences.getPreferences(),
  update_preferences: (a: { input: unknown }) =>
    Preferences.updatePreferences(a.input),

  open_external_url: (a: { url: string }) => Shell.openExternalURL(a.url),
  open_authorization_url: (a: { url: string }) =>
    Shell.openAuthorizationURL(a.url),

  list_services: () => Services.listServices(),
  get_service_order: () => Services.getServiceOrder(),
  update_service_order: (a: { serviceIds: string[]; etag: string }) =>
    Services.updateServiceOrder(a.serviceIds, a.etag),
  get_service_packages: () => Services.getServicePackages(),
  update_service_package: (a: { packageId: string; strategy: string }) =>
    Services.updateServicePackage(a.packageId, a.strategy),
  get_service: (a: { serviceId: string }) => Services.getService(a.serviceId),
  create_service: (a: { input: unknown }) => Services.createService(a.input),
  update_service: (a: { serviceId: string; etag: string; patch: unknown }) =>
    Services.updateService(a.serviceId, a.etag, a.patch),
  delete_service: (a: { serviceId: string; etag: string }) =>
    Services.deleteService(a.serviceId, a.etag),
  get_service_usage: (a: { serviceId: string; fresh?: boolean | null }) =>
    Services.getServiceUsage(a.serviceId, a.fresh ?? null),
  get_service_reset_credits: (a: { serviceId: string }) =>
    Services.getServiceResetCredits(a.serviceId),
  reset_service_usage: (a: { serviceId: string }) =>
    Services.resetServiceUsage(a.serviceId),
  test_service: (a: {
    serviceId: string;
    input: unknown;
    testId?: string | null;
  }) => Services.testService(a.serviceId, a.input, a.testId ?? null),
  cancel_service_test: (a: { serviceId: string; testId: string }) =>
    Services.cancelServiceTest(a.serviceId, a.testId),
  probe_service_models: (a: { serviceId: string; input: unknown }) =>
    Services.probeServiceModels(a.serviceId, a.input),
  probe_draft_service_models: (a: { input: unknown }) =>
    Services.probeDraftServiceModels(a.input),
  probe_service_proxy: (a: { input: unknown }) =>
    Services.probeServiceProxy(a.input),
  begin_service_authorization: (a: { serviceId: string; flow: string }) =>
    Services.beginServiceAuthorization(a.serviceId, a.flow),
  get_service_authorization: (a: { serviceId: string }) =>
    Services.getServiceAuthorization(a.serviceId),
  complete_service_authorization: (a: {
    serviceId: string;
    sessionId: string;
    code: string;
  }) => Services.completeServiceAuthorization(a.serviceId, a.sessionId, a.code),
  cancel_service_authorization: (a: { serviceId: string }) =>
    Services.cancelServiceAuthorization(a.serviceId),
  get_claude_cli: () => Services.getClaudeCLI(),
  logout_service: (a: { serviceId: string }) =>
    Services.logoutService(a.serviceId),
  clear_service_risk: (a: { serviceId: string }) =>
    Services.clearServiceRisk(a.serviceId),
  list_service_risk_events: (a: { serviceId: string; limit?: number | null }) =>
    Services.listServiceRiskEvents(a.serviceId, a.limit ?? null),

  pricing: (a: {
    operation: string;
    serviceId?: string | null;
    input?: unknown;
  }) => Pricing.pricing(a.operation, a.serviceId ?? null, a.input ?? null),
  sync_config: (a: { operation: string; input?: unknown }) =>
    Sync.syncConfig(a.operation, a.input ?? null),

  list_request_records: (a: { query: unknown }) =>
    Records.listRequestRecords(a.query),
  list_request_sessions: (a: { query: unknown }) =>
    Records.listRequestSessions(a.query),
  get_request_activity: (a: { since: string }) =>
    Records.getRequestActivity(a.since),
  get_project_memory: (a: { query: unknown }) =>
    Records.getProjectMemory(a.query),
  get_project_memory_entry: (a: { query: unknown }) =>
    Records.getProjectMemoryEntry(a.query),
  get_request_session: (a: { sessionId: string }) =>
    Records.getRequestSession(a.sessionId),
  get_session_channel_bindings: (a: {
    sessionId: string;
    before?: number | null;
  }) => Records.getSessionChannelBindings(a.sessionId, a.before ?? null),
  release_session_channel_bindings: (a: { sessionId: string }) =>
    Records.releaseSessionChannelBindings(a.sessionId),
  get_request_record: (a: { requestId: string }) =>
    Records.getRequestRecord(a.requestId),
  list_request_record_children: (a: { requestId: string }) =>
    Records.listRequestRecordChildren(a.requestId),
  delete_request_record: (a: { requestId: string }) =>
    Records.deleteRequestRecord(a.requestId),
  purge_request_records: (a: { input: unknown }) =>
    Records.purgeRequestRecords(a.input),
  get_request_audit_content: (a: { requestId: string }) =>
    Records.getRequestAuditContent(a.requestId),
  get_request_context_texts: (a: { requestId: string }) =>
    Records.getRequestContextTexts(a.requestId),

  list_raw_access: () => Raw.listRawAccess(),
  decide_raw_access: (a: {
    grantId: string;
    decision: string;
    proof?: RawProofArg | null;
  }) => Raw.decideRawAccess(a.grantId, a.decision, a.proof ?? null),
  revoke_raw_grant: (a: { grantId: string }) => Raw.revokeRawGrant(a.grantId),
  raw_sealing_status: () => Raw.rawSealingStatus(),
  unlock_raw: (a: { proof: RawProofArg }) => Raw.unlockRaw(a.proof),
  lock_raw: () => Raw.lockRaw(),
  acknowledge_raw_key: (a: { proof: RawProofArg }) =>
    Raw.acknowledgeRawKey(a.proof),
  set_raw_password: (a: {
    action: string;
    password?: string | null;
    proof?: RawProofArg | null;
  }) => Raw.setRawPassword(a.action, a.password ?? null, a.proof ?? null),
  enroll_raw_biometric: (a: { password: string }) =>
    Raw.enrollRawBiometric(a.password),
  remove_raw_biometric: () => Raw.removeRawBiometric(),

  get_audit_settings: () => Audit.getAuditSettings(),
  update_audit_settings: (a: { patch: unknown }) =>
    Audit.updateAuditSettings(a.patch),
  local_data_status: () => Audit.localDataStatus(),

  list_access_tokens: () => Tokens.listAccessTokens(),
  list_network_addresses: () => Tokens.listNetworkAddresses(),
  create_access_token: (a: {
    name: string;
    serviceScope?: string | null;
    serviceIds?: string[] | null;
  }) =>
    Tokens.createAccessToken(
      a.name,
      a.serviceScope ?? null,
      a.serviceIds ?? [],
    ),
  update_access_token: (a: {
    tokenId: string;
    name: string;
    serviceScope: string;
    serviceIds: string[];
  }) =>
    Tokens.updateAccessToken(a.tokenId, a.name, a.serviceScope, a.serviceIds),
  delete_access_token: (a: { tokenId: string }) =>
    Tokens.deleteAccessToken(a.tokenId),
  copy_access_token: (a: { tokenId: string }) =>
    Tokens.copyAccessToken(a.tokenId),

  get_usage_summary: (a: SummaryArgs) =>
    Usage.getUsageSummary(a.from, a.to, a.timeZone, a.bucket),
  get_privacy_summary: (a: SummaryArgs) =>
    Usage.getPrivacySummary(a.from, a.to, a.timeZone, a.bucket),
  get_image_compression_totals: () => Usage.getImageCompressionTotals(),
  list_access_token_usage: (a: { todayFrom: string }) =>
    Usage.listAccessTokenUsage(a.todayFrom),

  builtin_tool_action: (a: { kind: string; action: string; input?: unknown }) =>
    Routing.builtinToolAction(a.kind, a.action, a.input ?? null),
  get_routing_settings: () => Routing.getRoutingSettings(),
  update_routing_settings: (a: { patch: unknown }) =>
    Routing.updateRoutingSettings(a.patch),
  get_client_identities: () => Routing.getClientIdentities(),

  list_privacy_policies: () => Privacy.listPrivacyPolicies(),
  get_privacy_policy: () => Privacy.getPrivacyPolicy(),
  update_privacy_policy: (a: { etag: string; patch: unknown }) =>
    Privacy.updatePrivacyPolicy(a.etag, a.patch),
  dry_run_privacy_policy: (a: { input: unknown }) =>
    Privacy.dryRunPrivacyPolicy(a.input),
  get_privacy_regex_builtin_rules: () => Privacy.getPrivacyRegexBuiltinRules(),
  get_privacy_model_catalog: () => Privacy.getPrivacyModelCatalog(),
  get_privacy_model_releases: (a: { refresh?: boolean | null }) =>
    Privacy.getPrivacyModelReleases(a.refresh ?? null),
  probe_privacy_model: (a: { input: unknown }) =>
    Privacy.probePrivacyModel(a.input),
  probe_local_privacy_model: (a: { input: unknown }) =>
    Privacy.probeLocalPrivacyModel(a.input),
  list_privacy_model_installations: () =>
    Privacy.listPrivacyModelInstallations(),
  install_privacy_model: (a: { input: unknown }) =>
    Privacy.installPrivacyModel(a.input),
  get_privacy_model_installation: (a: { installationId: string }) =>
    Privacy.getPrivacyModelInstallation(a.installationId),
  pause_privacy_model_installation: (a: { installationId: string }) =>
    Privacy.pausePrivacyModelInstallation(a.installationId),
  resume_privacy_model_installation: (a: { installationId: string }) =>
    Privacy.resumePrivacyModelInstallation(a.installationId),
  delete_privacy_model_installation: (a: { installationId: string }) =>
    Privacy.deletePrivacyModelInstallation(a.installationId),
} satisfies CommandTable;

type RawProofArg = Parameters<typeof Raw.unlockRaw>[0];

interface SummaryArgs {
  from: string;
  to: string;
  timeZone: string;
  bucket: string;
}
