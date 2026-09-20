import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import capabilityFixture from "../../../contracts/examples/capabilities.alpha.json";

const invokeMock = vi.hoisted(() => vi.fn());
const downloadMocks = vi.hoisted(() => ({
  downloadTextFile: vi.fn(),
}));

vi.mock("@tauri-apps/api/core", () => ({
  invoke: invokeMock,
}));
vi.mock("./download-text-file", () => downloadMocks);

import {
  cancelPrivacyModelInstallation,
  pausePrivacyModelInstallation,
  resumePrivacyModelInstallation,
  createAccessToken,
  createRoute,
  createService,
  deleteAccessToken,
  deletePrivacyModelInstallation,
  deleteRoute,
  deleteService,
  dryRunPrivacyPolicy,
  getCoreStatus,
  getPrivacyModelCatalog,
  getPrivacyModelInstallation,
  getPrivacyPolicy,
  getRoute,
  getService,
  getServiceOrder,
  updateServiceOrder,
  getServiceAuthorization,
  getServiceUsage,
  resetServiceUsage,
  installPrivacyModel,
  listRoutes,
  listServices,
  listRequestSessions,
  listAccessTokens,
  listAccessTokenUsage,
  getUsageSummary,
  listPrivacyModelInstallations,
  listPrivacyPolicies,
  probeLocalPrivacyModel,
  probeDraftServiceModels,
  probeServiceModels,
  probePrivacyModel,
  revealAccessToken,
  restartCore,
  updateRoute,
  updateService,
  updatePrivacyPolicy,
  beginServiceAuthorization,
  cancelServiceAuthorization,
  logoutService,
  openAuthorizationURL,
  saveTextFile,
  getAgentDebugStatus,
  installAgentDebug,
  uninstallAgentDebug,
} from "./bridge";
import { defaultPrivacyKindRules } from "./privacy-policy-model";
import { emptyUsageTotals, resolveUsageWindow } from "./usage-range";

function validSnapshot(): Record<string, unknown> {
  return {
    app_version: "0.1.0",
    phase: "ready",
    pid: 1234,
    ready: {
      event: "ready",
      core_version: "0.1.0-dev",
      control_api_version: "v1",
      protocol_contract_version: "v1",
      inference_url: "http://127.0.0.1:8317",
      control_url: "http://127.0.0.1:49152",
    },
    last_error: null,
    inference_port_fallback: null,
    recovery_attempt: 0,
    recovery_scheduled_in_ms: null,
    health: { status: "ok" },
    version: {
      core_version: "0.1.0-dev",
      control_api_version: "v1",
      protocol_contract_version: "v1",
      build_commit: "unknown",
    },
    capabilities: structuredClone(capabilityFixture),
  };
}

describe("desktop bridge contract", () => {
  it("requests one aggregate for the complete usage window", async () => {
    const window = resolveUsageWindow("1d", new Date(2026, 8, 19, 12));
    invokeMock.mockResolvedValueOnce({ totals: emptyUsageTotals(), by_day: [], by_hour: [], by_service: [], by_model: [], scanned_records: 0 });
    const result = await getUsageSummary(window);
    expect(invokeMock).toHaveBeenCalledWith("get_usage_summary", {
      from: window.from, to: window.to, timeZone: window.time_zone, bucket: "hour",
    });
    expect(result.by_hour).toHaveLength(24);
    expect(result.capped).toBe(false);
  });
  it("roundtrips ordered service IDs and rejects malformed order responses", async () => {
    const record = { service_ids: ["service_b", "service_a"], etag: '"sha256:abc"' };
    invokeMock.mockResolvedValueOnce(record);
    await expect(getServiceOrder()).resolves.toEqual(record);
    expect(invokeMock).toHaveBeenLastCalledWith("get_service_order");
    invokeMock.mockResolvedValueOnce(record);
    await expect(updateServiceOrder(record.service_ids, record.etag)).resolves.toEqual(record);
    expect(invokeMock).toHaveBeenLastCalledWith("update_service_order", { serviceIds: record.service_ids, etag: record.etag });
    invokeMock.mockResolvedValueOnce({ ...record, service_ids: ["service_a", "service_a"] });
    await expect(getServiceOrder()).rejects.toThrow("Invalid service order");
    await expect(updateServiceOrder(["bad/path"], record.etag)).rejects.toThrow("Invalid service order");
  });
  beforeEach(() => {
    invokeMock.mockReset();
    downloadMocks.downloadTextFile.mockReset();
    vi.stubGlobal("window", { __TAURI_INTERNALS__: {} });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("forwards session kind and cursor through the native bridge", async () => {
    invokeMock.mockResolvedValue({ items: [], next_cursor: null });
    await listRequestSessions({ kind: "discovery", limit: 50, cursor: "older" });
    expect(invokeMock).toHaveBeenCalledWith("list_request_sessions", {
      query: { kind: "discovery", limit: 50, cursor: "older" },
    });
    await listRequestSessions();
    expect(invokeMock).toHaveBeenLastCalledWith("list_request_sessions", { query: {} });
  });

  it("returns the browser fallback only after native bridge detection", async () => {
    vi.stubGlobal("window", {});

    await expect(getCoreStatus()).resolves.toMatchObject({
      app_version: "Unknown",
      phase: "unavailable",
      pid: null,
    });
    expect(invokeMock).not.toHaveBeenCalled();
    await expect(restartCore()).rejects.toThrow("网关重启仅在 AstrLink 桌面应用中可用。");
  });

  it.each([
    ["core_status", getCoreStatus],
    ["restart_core", restartCore],
  ] as const)("parses the frozen fixture returned by %s", async (command, callBridge) => {
    const wireSnapshot = validSnapshot();
    invokeMock.mockResolvedValueOnce(wireSnapshot);

    const parsed = await callBridge();

    expect(invokeMock).toHaveBeenCalledWith(command);
    expect(parsed).toEqual(wireSnapshot);
    expect(parsed.capabilities?.protocols).toHaveLength(8);
  });

  it("keeps fallback metadata tied to the advertised inference address", async () => {
    const snapshot = validSnapshot();
    snapshot.inference_port_fallback = { requested_port: 9000, active_port: 8317 };
    invokeMock.mockResolvedValueOnce(snapshot);
    await expect(getCoreStatus()).resolves.toMatchObject({
      inference_port_fallback: { requested_port: 9000, active_port: 8317 },
    });
    for (const fallback of [
      { requested_port: 9000, active_port: 8318 },
      { requested_port: 8317, active_port: 8317 },
      { requested_port: 0, active_port: 8317 },
    ]) {
      invokeMock.mockResolvedValueOnce({ ...snapshot, inference_port_fallback: fallback });
      await expect(getCoreStatus()).rejects.toThrow("inference_port_fallback");
    }
  });

  it("rejects a snapshot with a missing frozen field", async () => {
    const wireSnapshot = validSnapshot();
    delete wireSnapshot.pid;
    invokeMock.mockResolvedValueOnce(wireSnapshot);

    await expect(getCoreStatus()).rejects.toThrow("$.pid: missing field");
  });

  it("rejects a ready URL with trailing data", async () => {
    const wireSnapshot = validSnapshot();
    (wireSnapshot.ready as any).control_url = "http://127.0.0.1:49152\n";
    invokeMock.mockResolvedValueOnce(wireSnapshot);

    await expect(getCoreStatus()).rejects.toThrow("canonical IPv4 loopback URL");
  });

  it.each([
    ["ready control version", (snapshot: any) => (snapshot.ready.control_api_version = "v2")],
    ["version contract version", (snapshot: any) => (snapshot.version.protocol_contract_version = "v2")],
    ["capability contract version", (snapshot: any) => (snapshot.capabilities.protocol_contract_version = "v2")],
  ])("rejects a mismatched %s", async (_name, mutate) => {
    const wireSnapshot = validSnapshot();
    mutate(wireSnapshot);
    invokeMock.mockResolvedValueOnce(wireSnapshot);

    await expect(getCoreStatus()).rejects.toThrow("unsupported version");
  });

  it("rejects missing required Alpha protocols but preserves unknown protocols", async () => {
    const invalid = validSnapshot();
    (invalid.capabilities as any).protocols.shift();
    invokeMock.mockResolvedValueOnce(invalid);
    await expect(getCoreStatus()).rejects.toThrow("expected at least 8 entries");

    const extended = validSnapshot();
    (extended.capabilities as any).protocols.push({
      id: "vendor.custom_protocol",
      phase: "post_alpha",
      primary: false,
      streaming: true,
    });
    invokeMock.mockResolvedValueOnce(extended);
    const parsed = await getCoreStatus();
    expect(parsed.capabilities?.protocols).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: "vendor.custom_protocol" }),
      ]),
    );
  });

  it.each([
    [
      "RelayKit available without version",
      (snapshot: any) => (snapshot.capabilities.conversion_engine.available = true),
      "non-empty string when available",
    ],
    [
      "RelayKit version while unavailable",
      (snapshot: any) => (snapshot.capabilities.conversion_engine.version = "0.1.0"),
      "must be null when unavailable",
    ],
    [
      "RelayKit edges while unavailable",
      (snapshot: any) => snapshot.capabilities.conversion_engine.edges.push({}),
      "must be empty when unavailable",
    ],
    [
      "native conversion",
      (snapshot: any) => (snapshot.capabilities.plan_types[0].uses_local_conversion = true),
      "Alpha",
    ],
  ])("rejects invalid capability semantics: %s", async (_name, mutate, detail) => {
    const wireSnapshot = validSnapshot();
    mutate(wireSnapshot);
    invokeMock.mockResolvedValueOnce(wireSnapshot);

    await expect(getCoreStatus()).rejects.toThrow(detail);
  });

  it("accepts an available RelayKit conversion engine descriptor", async () => {
    const wireSnapshot = validSnapshot();
    (wireSnapshot.capabilities as any).conversion_engine = {
      name: "relaykit",
      version: "v0.1.1",
      available: true,
      edges: [
        {
          from: "openai.chat",
          to: "openai.responses",
          quality: "good",
          streaming: true,
        },
      ],
    };
    invokeMock.mockResolvedValueOnce(wireSnapshot);

    await expect(getCoreStatus()).resolves.toMatchObject({
      capabilities: {
        conversion_engine: {
          name: "relaykit",
          version: "v0.1.1",
          available: true,
          edges: [
            {
              from: "openai.chat",
              to: "openai.responses",
              quality: "good",
              streaming: true,
            },
          ],
        },
      },
    });
  });

  it("proxies priority route CRUD through fixed native commands", async () => {
    const route = {
      id: "route_01",
      name: "Code alias",
      enabled: true,
      priority: 10,
      match: { protocol: "openai.responses", model: "team/code" },
      selection: { mode: "priority" as const },
      targets: [
        {
          service_id: "service_01",
          plan_type: "native" as const,
          upstream_protocol: "openai.responses",
          priority: 0,
          upstream_model: "gpt-5.2",
        },
      ],
    };
    const etag = `"sha256:${"c".repeat(64)}"`;
    invokeMock.mockResolvedValueOnce({ items: [route], next_cursor: null });
    await expect(listRoutes()).resolves.toEqual({
      items: [route],
      next_cursor: null,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("list_routes");

    invokeMock.mockResolvedValueOnce({ route, etag });
    await expect(getRoute(route.id)).resolves.toEqual({ route, etag });
    expect(invokeMock).toHaveBeenLastCalledWith("get_route", {
      routeId: route.id,
    });

    const { id: _id, enabled: _enabled, ...baseInput } = route;
    const input = { ...baseInput, enabled: true };
    invokeMock.mockResolvedValueOnce({ route, etag });
    await expect(createRoute(input)).resolves.toEqual({ route, etag });
    expect(invokeMock).toHaveBeenLastCalledWith("create_route", { input });

    invokeMock.mockResolvedValueOnce({
      route: { ...route, enabled: false },
      etag,
    });
    await updateRoute(route.id, etag, { enabled: false });
    expect(invokeMock).toHaveBeenLastCalledWith("update_route", {
      routeId: route.id,
      etag,
      patch: { enabled: false },
    });

    invokeMock.mockResolvedValueOnce(undefined);
    await deleteRoute(route.id, etag);
    expect(invokeMock).toHaveBeenLastCalledWith("delete_route", {
      routeId: route.id,
      etag,
    });
  });

  it("proxies access-token operations through fixed commands and strict parsers", async () => {
    const secret = `astr_${"A".repeat(43)}`;
    const token = {
      id: "token_01",
      name: "VS Code",
      hint: "astr_…K8Q2",
      created_at: "2026-07-24T10:30:00Z",
    };

    invokeMock.mockResolvedValueOnce({
      items: [token],
      next_cursor: null,
    });
    await expect(listAccessTokens()).resolves.toEqual({
      items: [token],
      next_cursor: null,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("list_access_tokens");

    const usage = { items: [{ token_id: token.id, today_tokens: 10, total_tokens: 100 }] };
    invokeMock.mockResolvedValueOnce(usage);
    await expect(listAccessTokenUsage("2026-09-19T00:00:00.000Z")).resolves.toEqual(usage);
    expect(invokeMock).toHaveBeenLastCalledWith("list_access_token_usage", {
      todayFrom: "2026-09-19T00:00:00.000Z",
    });

    invokeMock.mockResolvedValueOnce({
      token,
      access_token: secret,
    });
    await expect(createAccessToken("VS Code")).resolves.toEqual({
      token,
      access_token: secret,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("create_access_token", {
      name: "VS Code",
    });

    invokeMock.mockResolvedValueOnce({
      access_token: secret,
    });
    await expect(revealAccessToken("token_01")).resolves.toEqual({
      access_token: secret,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("reveal_access_token", {
      tokenId: "token_01",
    });

    invokeMock.mockResolvedValueOnce(undefined);
    await deleteAccessToken("token_01");
    expect(invokeMock).toHaveBeenLastCalledWith("delete_access_token", {
      tokenId: "token_01",
    });
  });

  it("proxies privacy policy and selectable model operations through strict commands", async () => {
    const installationId = "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    const policy = {
      id: "policy_privacy_default",
      name: "隐私保护",
      enabled: false,
      priority: 0,
      detector: "regex",
      local_model_id: null,
      min_confidence: 0.6,
      regex_source: "builtin",
      custom_regex_rules: [],
      request_action: "redact",
      response_action: "allow",
      response_restore: true,
      kind_rules: defaultPrivacyKindRules(),
      allowlist_rules: [{ type: "domain_suffix", value: "github.com" }],
      restore_tool_arguments: true,
      placeholder_notice: true,
      match: {},
    };
    const etag = `"sha256:${"b".repeat(64)}"`;
    const revision = "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088";
    const variant = {
      id: "cpu_int8",
      name: "CPU INT8",
      quantization: "int8",
      bytes_total: 180_000_000,
      estimated_ram_bytes: 420_000_000,
      recommended: true,
      supported: true,
      unsupported_reason: null,
    };
    const catalogModel = {
      id: "catalog_sheltron_ettin_32m",
      name: "Ettin Privacy 32M",
      summary: "轻量英文隐私实体检测模型。",
      source: "community",
      repo_id: "sheltron-ai/privacy-filter-ettin-32m",
      revision,
      license: "apache-2.0",
      languages: ["en"],
      adapter: "hf_token_classification",
      variants: [variant],
    };
    const installation = {
      id: installationId,
      source: "catalog",
      catalog_id: catalogModel.id,
      catalog_source: catalogModel.source,
      name: catalogModel.name,
      license: catalogModel.license,
      languages: catalogModel.languages,
      repo_id: catalogModel.repo_id,
      revision,
      variant_id: variant.id,
      variant_name: variant.name,
      quantization: variant.quantization,
      adapter: catalogModel.adapter,
      status: "downloading",
      bytes_downloaded: 0,
      bytes_total: variant.bytes_total,
      estimated_ram_bytes: variant.estimated_ram_bytes,
      error: null,
      label_mapping: {},
      installed_at: null,
    };

    invokeMock.mockResolvedValueOnce({ items: [policy], next_cursor: null });
    await expect(listPrivacyPolicies()).resolves.toEqual({
      items: [policy],
      next_cursor: null,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("list_privacy_policies");

    invokeMock.mockResolvedValueOnce({ policy, etag });
    await expect(getPrivacyPolicy()).resolves.toEqual({ policy, etag });
    expect(invokeMock).toHaveBeenLastCalledWith("get_privacy_policy");

    invokeMock.mockResolvedValueOnce({
      policy: { ...policy, request_action: "block" },
      etag,
    });
    await updatePrivacyPolicy(etag, { request_action: "block" });
    expect(invokeMock).toHaveBeenLastCalledWith("update_privacy_policy", {
      etag,
      patch: { request_action: "block" },
    });

    const dryRun = {
      decision: "redact",
      findings_summary: "email=1",
      findings: [
        {
          kind: "email",
          path: "/messages/0/content",
          start: 6,
          end: 23,
          confidence: 1,
        },
      ],
      suppressed_findings: [],
      redacted_body:
        '{"messages":[{"content":"email [REDACTED]","role":"user"}]}',
      inspected_body:
        '{"messages":[{"content":"email alice@example.com","role":"user"}]}',
    };
    invokeMock.mockResolvedValueOnce(dryRun);
    await expect(
      dryRunPrivacyPolicy({
        protocol: "openai.chat",
        sample_text: "email alice@example.com",
        policy: {
          enabled: true,
          detector: "regex",
          local_model_id: null,
          min_confidence: 0.8,
          request_action: "redact",
        },
      }),
    ).resolves.toEqual(dryRun);
    expect(invokeMock).toHaveBeenLastCalledWith("dry_run_privacy_policy", {
      input: {
        protocol: "openai.chat",
        sample_text: "email alice@example.com",
        policy: {
          enabled: true,
          detector: "regex",
          local_model_id: null,
          min_confidence: 0.8,
          request_action: "redact",
        },
      },
    });

    invokeMock.mockResolvedValueOnce({ items: [catalogModel] });
    await expect(getPrivacyModelCatalog()).resolves.toEqual({
      items: [catalogModel],
    });
    expect(invokeMock).toHaveBeenLastCalledWith("get_privacy_model_catalog");

    const probe = {
      repo_id: catalogModel.repo_id,
      requested_revision: "main",
      revision,
      name: catalogModel.name,
      license: catalogModel.license,
      languages: catalogModel.languages,
      adapter: catalogModel.adapter,
      variants: catalogModel.variants,
      labels: [{ label: "EMAIL", suggested_kind: "email" }],
      requires_label_mapping: false,
    };
    invokeMock.mockResolvedValueOnce(probe);
    await expect(
      probePrivacyModel({
        repo_id: catalogModel.repo_id,
        revision: "main",
      }),
    ).resolves.toEqual(probe);
    expect(invokeMock).toHaveBeenLastCalledWith("probe_privacy_model", {
      input: { repo_id: catalogModel.repo_id, revision: "main" },
    });

    const localProbe = {
      ...probe,
      repo_id: "local/model-aaaaaaaaaaaa",
      requested_revision: revision,
    };
    invokeMock.mockResolvedValueOnce(localProbe);
    await expect(
      probeLocalPrivacyModel({ path: "  /Volumes/models/privacy/model_int8.onnx  " }),
    ).resolves.toEqual(localProbe);
    expect(invokeMock).toHaveBeenLastCalledWith(
      "probe_local_privacy_model",
      { input: { path: "/Volumes/models/privacy/model_int8.onnx" } },
    );
    const callsBeforeInvalidLocalProbe = invokeMock.mock.calls.length;
    await expect(
      probeLocalPrivacyModel({ path: "smb://host/share/privacy/model.onnx" }),
    ).rejects.toThrow("not a URI");
    expect(invokeMock).toHaveBeenCalledTimes(callsBeforeInvalidLocalProbe);

    invokeMock.mockResolvedValueOnce({ items: [installation] });
    await expect(listPrivacyModelInstallations()).resolves.toEqual({
      items: [installation],
    });
    expect(invokeMock).toHaveBeenLastCalledWith(
      "list_privacy_model_installations",
    );

    const installInput = {
      repo_id: catalogModel.repo_id,
      revision,
      variant_id: variant.id,
      label_mapping: {},
    };
    invokeMock.mockResolvedValueOnce(installation);
    await expect(installPrivacyModel(installInput)).resolves.toEqual(
      installation,
    );
    expect(invokeMock).toHaveBeenLastCalledWith("install_privacy_model", {
      input: installInput,
    });

    invokeMock.mockResolvedValueOnce(installation);
    await expect(
      getPrivacyModelInstallation(installation.id),
    ).resolves.toEqual(installation);
    expect(invokeMock).toHaveBeenLastCalledWith(
      "get_privacy_model_installation",
      { installationId: installation.id },
    );

    invokeMock.mockResolvedValueOnce({ ...installation, status: "paused" });
    await expect(pausePrivacyModelInstallation(installation.id)).resolves.toMatchObject({ status: "paused" });
    expect(invokeMock).toHaveBeenLastCalledWith("pause_privacy_model_installation", { installationId: installation.id });
    invokeMock.mockResolvedValueOnce(installation);
    await expect(resumePrivacyModelInstallation(installation.id)).resolves.toEqual(installation);
    expect(invokeMock).toHaveBeenLastCalledWith("resume_privacy_model_installation", { installationId: installation.id });
    await expect(resumePrivacyModelInstallation("../invalid")).rejects.toThrow();
    invokeMock.mockResolvedValueOnce(undefined);
    await cancelPrivacyModelInstallation(installation.id);
    expect(invokeMock).toHaveBeenLastCalledWith(
      "delete_privacy_model_installation",
      { installationId: installation.id },
    );

    invokeMock.mockResolvedValueOnce(undefined);
    await deletePrivacyModelInstallation(installation.id);
    expect(invokeMock).toHaveBeenLastCalledWith(
      "delete_privacy_model_installation",
      { installationId: installation.id },
    );
  });

  it("uses one service IPC surface for Codex and gateway services", async () => {
    const service = {
      id: "service_codex_01",
      name: "Codex subscription",
      kind: "codex_subscription",
      enabled: true,
      models: [],
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
        {
          protocol: "openai.responses.compact",
          mode: "native",
          streaming: false,
        },
      ],
      subscription: {
        provider: "openai_codex",
        status: "disconnected",
      },
      created_at: "2026-07-28T08:00:00Z",
      updated_at: "2026-07-28T08:05:00Z",
    };
    const etag = `"sha256:${"d".repeat(64)}"`;
    invokeMock.mockResolvedValueOnce({ items: [service], next_cursor: null });
    await expect(listServices()).resolves.toEqual({
      items: [service],
      next_cursor: null,
    });

    invokeMock.mockResolvedValueOnce({ service, etag });
    await expect(getService(service.id)).resolves.toEqual({ service, etag });

    invokeMock.mockResolvedValueOnce({ service, etag });
    await expect(
      createService({
        name: "Codex subscription",
        kind: "codex_subscription",
      }),
    ).resolves.toEqual({ service, etag });

    invokeMock.mockResolvedValueOnce({ service, etag });
    await expect(
      updateService(service.id, etag, { name: "Codex personal" }),
    ).resolves.toEqual({ service, etag });

    const session = {
      id: "authorization_01",
      provider: "openai_codex",
      status: "pending",
      flow: "browser",
      service_id: "service_codex_01",
      authorization_url: "https://auth.example/oauth/authorize",
      expires_at: "2026-07-28T08:10:00Z",
      created_at: "2026-07-28T08:00:00Z",
      updated_at: "2026-07-28T08:00:00Z",
    };
    invokeMock.mockResolvedValueOnce({ kind: "session", session });
    await expect(
      beginServiceAuthorization(service.id, "browser"),
    ).resolves.toEqual({
      kind: "session",
      session,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("begin_service_authorization", {
      serviceId: service.id,
      flow: "browser",
    });

    invokeMock.mockResolvedValueOnce(session);
    await expect(getServiceAuthorization(service.id)).resolves.toEqual(session);

    const { authorization_url: _authorizationURL, ...terminalSession } = session;
    invokeMock.mockResolvedValueOnce({
      ...terminalSession,
      status: "cancelled",
    });
    await expect(cancelServiceAuthorization(service.id)).resolves.toMatchObject({
      status: "cancelled",
    });

    invokeMock.mockResolvedValueOnce({
      service,
      etag,
    });
    await expect(logoutService(service.id)).resolves.toMatchObject({
      service: { id: service.id },
    });

    const modelProbe = {
      service_id: service.id,
      protocol: "openai.models" as const,
      model_ids: ["gpt-5"],
    };
    invokeMock.mockResolvedValueOnce(modelProbe);
    await expect(
      probeServiceModels(service.id, "openai.models"),
    ).resolves.toEqual(modelProbe);
    expect(invokeMock).toHaveBeenLastCalledWith("probe_service_models", {
      serviceId: service.id,
      input: { protocol: "openai.models" },
    });

    const usage = {
      service_id: service.id,
      fetched_at: "2026-08-30T11:00:00Z",
      plan_type: "plus",
      primary: {
        used_percent: 34,
        limit_window_seconds: 18_000,
        reset_at: "2026-08-30T13:00:00Z",
      },
    };
    invokeMock.mockResolvedValueOnce(usage);
    await expect(getServiceUsage(service.id)).resolves.toEqual(usage);
    expect(invokeMock).toHaveBeenLastCalledWith("get_service_usage", {
      serviceId: service.id,
    });

    const reset = {
      service_id: service.id,
      outcome: "reset" as const,
      windows_reset: 2,
    };
    invokeMock.mockResolvedValueOnce(reset);
    await expect(resetServiceUsage(service.id)).resolves.toEqual(reset);
    expect(invokeMock).toHaveBeenLastCalledWith("reset_service_usage", {
      serviceId: service.id,
    });

    const draftProbe = {
      service_id: "service_gateway",
      kind: "openai" as const,
      http: {
        base_url: "https://api.example/v1",
        auth: { scheme: "bearer" as const },
      },
      protocol: "openai.models" as const,
    };
    invokeMock.mockResolvedValueOnce({
      service_id: draftProbe.service_id,
      protocol: draftProbe.protocol,
      model_ids: ["gpt-5"],
    });
    await expect(probeDraftServiceModels(draftProbe)).resolves.toMatchObject({
      model_ids: ["gpt-5"],
    });
    expect(invokeMock).toHaveBeenLastCalledWith(
      "probe_draft_service_models",
      { input: draftProbe },
    );

    invokeMock.mockResolvedValueOnce(undefined);
    await deleteService(service.id, etag);
    expect(invokeMock).toHaveBeenLastCalledWith("delete_service", {
      serviceId: service.id,
      etag,
    });

    invokeMock.mockResolvedValueOnce(undefined);
    await openAuthorizationURL("https://auth.openai.com/codex/device");
    expect(invokeMock).toHaveBeenLastCalledWith("open_authorization_url", {
      url: "https://auth.openai.com/codex/device",
    });
  });

  it("parses agent debug install status and receipt", async () => {
    const status = {
      canonical_skill: false,
      mcp_binary: false,
      mcp_command: "/tmp/astrlink-mcp",
      tools: [
        {
          id: "cursor",
          detected: true,
          skill_installed: false,
          mcp_installed: false,
        },
      ],
      preview_paths: ["/tmp/.agents/skills/astrlink-debug"],
    };
    invokeMock.mockResolvedValueOnce(status);
    await expect(getAgentDebugStatus()).resolves.toEqual(status);
    expect(invokeMock).toHaveBeenLastCalledWith("agent_debug_status");

    const receipt = {
      version: 1,
      bundle: "astrlink-debug",
      bundle_version: "0.1.0",
      installed_at_unix: 1,
      mcp_binary: "/tmp/astrlink-mcp",
      files: ["/tmp/a"],
    };
    invokeMock.mockResolvedValueOnce(receipt);
    await expect(installAgentDebug()).resolves.toEqual(receipt);
    expect(invokeMock).toHaveBeenLastCalledWith("install_agent_debug");

    invokeMock.mockResolvedValueOnce(undefined);
    await uninstallAgentDebug();
    expect(invokeMock).toHaveBeenLastCalledWith("uninstall_agent_debug");
  });

  it("saves text through the native dialog command", async () => {
    invokeMock.mockResolvedValueOnce("/Users/me/Downloads/astrlink-req_1.md");

    await expect(
      saveTextFile("astrlink-req_1.md", "# bundle"),
    ).resolves.toBe("/Users/me/Downloads/astrlink-req_1.md");
    expect(invokeMock).toHaveBeenCalledWith("save_text_file", {
      defaultFilename: "astrlink-req_1.md",
      contents: "# bundle",
    });
    expect(downloadMocks.downloadTextFile).not.toHaveBeenCalled();
  });

  it("falls back to a browser download when the native bridge is missing", async () => {
    vi.stubGlobal("window", {});

    await expect(saveTextFile("astrlink-req_1.txt", "plain")).resolves.toBe(
      "astrlink-req_1.txt",
    );
    expect(invokeMock).not.toHaveBeenCalled();
    expect(downloadMocks.downloadTextFile).toHaveBeenCalledWith(
      "astrlink-req_1.txt",
      "plain",
    );
  });
});
