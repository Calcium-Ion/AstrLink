import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import capabilityFixture from "../../../contracts/examples/capabilities.alpha.json";

const invokeMock = vi.hoisted(() => vi.fn());

vi.mock("@tauri-apps/api/core", () => ({
  invoke: invokeMock,
}));

import {
  cancelPrivacyModelInstallation,
  createAccessToken,
  createEndpoint,
  deleteAccessToken,
  deletePrivacyModelInstallation,
  dryRunPrivacyPolicy,
  getCoreStatus,
  getPrivacyModelCatalog,
  getPrivacyModelInstallation,
  getPrivacyPolicy,
  installPrivacyModel,
  listEndpoints,
  listAccessTokens,
  listPrivacyModelInstallations,
  listPrivacyPolicies,
  probePrivacyModel,
  revealAccessToken,
  restartCore,
  updateEndpoint,
  updatePrivacyPolicy,
} from "./bridge";

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
  beforeEach(() => {
    invokeMock.mockReset();
    vi.stubGlobal("window", { __TAURI_INTERNALS__: {} });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns the browser fallback only after native bridge detection", async () => {
    vi.stubGlobal("window", {});

    await expect(getCoreStatus()).resolves.toMatchObject({
      app_version: "Unknown",
      phase: "unavailable",
      pid: null,
    });
    expect(invokeMock).not.toHaveBeenCalled();
    await expect(restartCore()).rejects.toThrow("desktop app");
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
    ["RelayKit availability", (snapshot: any) => (snapshot.capabilities.conversion_engine.available = true)],
    ["RelayKit version", (snapshot: any) => (snapshot.capabilities.conversion_engine.version = "0.1.0")],
    ["RelayKit edges", (snapshot: any) => snapshot.capabilities.conversion_engine.edges.push({})],
    ["native conversion", (snapshot: any) => (snapshot.capabilities.plan_types[0].uses_local_conversion = true)],
  ])("rejects invalid Alpha capability semantics: %s", async (_name, mutate) => {
    const wireSnapshot = validSnapshot();
    mutate(wireSnapshot);
    invokeMock.mockResolvedValueOnce(wireSnapshot);

    await expect(getCoreStatus()).rejects.toThrow("Alpha");
  });

  it("proxies endpoint operations through fixed native commands and parses responses", async () => {
    const endpoint = {
      id: "endpoint_01",
      name: "Primary",
      kind: "openai",
      base_url: "https://api.example/v1",
      auth: { scheme: "bearer" },
      credential_ref: "local://endpoint/endpoint_01",
      enabled: true,
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
      ],
    };
    invokeMock.mockResolvedValueOnce({ items: [endpoint], next_cursor: null });
    await expect(listEndpoints()).resolves.toMatchObject({ items: [{ id: "endpoint_01" }] });
    expect(invokeMock).toHaveBeenLastCalledWith("list_endpoints");

    const record = { endpoint, etag: `"sha256:${"a".repeat(64)}"` };
    const input = {
      name: "Primary",
      kind: "openai" as const,
      base_url: "https://api.example/v1",
      auth: { scheme: "bearer" as const },
      credential: { secret: "write-only" },
      capabilities: endpoint.capabilities as [{
        protocol: string;
        mode: "native";
        streaming: boolean;
      }],
    };
    invokeMock.mockResolvedValueOnce(record);
    await expect(createEndpoint(input)).resolves.toMatchObject({ endpoint: { id: "endpoint_01" } });
    expect(invokeMock).toHaveBeenLastCalledWith("create_endpoint", { input });

    invokeMock.mockResolvedValueOnce(record);
    await updateEndpoint(endpoint.id, record.etag, { name: "Renamed" });
    expect(invokeMock).toHaveBeenLastCalledWith("update_endpoint", {
      endpointId: endpoint.id,
      etag: record.etag,
      patch: { name: "Renamed" },
    });
  });

  it("proxies access-token operations through fixed commands and strict parsers", async () => {
    const secret = `astr_${"A".repeat(43)}`;
    const token = {
      id: "token_01",
      name: "VS Code",
      hint: "astr_…K8Q2",
      source: "user",
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
      request_action: "redact",
      response_action: "allow",
      response_restore: true,
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
        },
      ],
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
});
