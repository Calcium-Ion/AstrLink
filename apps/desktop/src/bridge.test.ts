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
  acknowledgeRawKey,
  decideRawAccess,
  getLocalDataStatus,
  getRawSealingStatus,
  listRawAccess,
  revokeRawGrant,
  lockRaw,
  setRawPassword,
  unlockRaw,
  cancelPrivacyModelInstallation,
  pausePrivacyModelInstallation,
  resumePrivacyModelInstallation,
  copyAccessToken,
  createAccessToken,
  createService,
  deleteAccessToken,
  deletePrivacyModelInstallation,
  deleteService,
  dryRunPrivacyPolicy,
  getCoreStatus,
  getPreferences,
  getPrivacyModelCatalog,
  getPrivacyModelReleases,
  getPrivacyModelInstallation,
  getPrivacyPolicy,
  getService,
  getServiceOrder,
  updateServiceOrder,
  getServiceAuthorization,
  getServiceUsage,
  resetServiceUsage,
  installPrivacyModel,
  listServices,
  listRequestSessions,
  listRequestRecords,
  listAccessTokens,
  listAccessTokenUsage,
  getUsageSummary,
  listPrivacyModelInstallations,
  listPrivacyPolicies,
  probeLocalPrivacyModel,
  probeDraftServiceModels,
  probeServiceModels,
  probeServiceProxy,
  testService,
  probePrivacyModel,
  restartCore,
  updateService,
  updatePrivacyPolicy,
  beginServiceAuthorization,
  cancelServiceAuthorization,
  clearServiceRisk,
  listServiceRiskEvents,
  logoutService,
  openAuthorizationURL,
  isCCSwitchInstalled,
  openCCSwitchImport,
  getClientConfigStatus,
  checkClientProxy,
  applyClientConfig,
  removeClientConfig,
  previewClientConfigSnippet,
  copyClientConfigSnippet,
  confirmProviderImport,
  dismissProviderImport,
  getPendingProviderImport,
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
      client_inference_url: "http://localhost:8317",
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
  it("lists raw access requests and forwards a decision's proof once", async () => {
    const grant = {
      grant_id: "rawgrant_0123456789abcdef",
      request_id: "request_1",
      status: "pending",
      reason: "the upstream rejected the email field",
      client_name: "claude-code",
      created_at: "2026-09-28T10:00:00Z",
      expires_at: "2026-09-28T10:10:00Z",
    };
    const running = {
      ...grant,
      grant_id: "rawgrant_fedcba9876543210",
      status: "approved",
      decision: "window_1h",
      scope: "all_requests",
    };
    invokeMock.mockResolvedValueOnce({
      items: [grant],
      active: [running],
      unlocked: true,
    });
    await expect(listRawAccess()).resolves.toEqual({
      pending: [{ ...grant, decision: null, scope: null }],
      active: [running],
      unlocked: true,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("list_raw_access");

    invokeMock.mockResolvedValueOnce({
      outcome: "decided",
      grant: { ...grant, status: "approved", decision: "once" },
    });
    await expect(
      decideRawAccess(grant.grant_id, "once", {
        kind: "password",
        password: "correct horse",
      }),
    ).resolves.toMatchObject({
      outcome: "decided",
      grant: { status: "approved", decision: "once" },
    });
    expect(invokeMock).toHaveBeenLastCalledWith("decide_raw_access", {
      grantId: grant.grant_id,
      decision: "once",
      proof: { kind: "password", password: "correct horse" },
    });

    // A denial never carries a proof, even if the caller passes one.
    invokeMock.mockResolvedValueOnce({ outcome: "not_pending" });
    await expect(
      decideRawAccess(grant.grant_id, "deny", {
        kind: "password",
        password: "correct horse",
      }),
    ).resolves.toEqual({ outcome: "not_pending" });
    expect(invokeMock).toHaveBeenLastCalledWith("decide_raw_access", {
      grantId: grant.grant_id,
      decision: "deny",
      proof: null,
    });

    invokeMock.mockResolvedValueOnce({
      outcome: "backoff",
      retry_after_seconds: 8,
    });
    await expect(
      decideRawAccess(grant.grant_id, "window_5m", {
        kind: "password",
        password: "wrong",
      }),
    ).resolves.toEqual({ outcome: "backoff", retry_after_seconds: 8 });

    // While Core is unlocked an approval carries no proof; Core asks for one
    // if it locked in the meantime.
    invokeMock.mockResolvedValueOnce({ outcome: "proof_required" });
    await expect(decideRawAccess(grant.grant_id, "window_1h")).resolves.toEqual(
      { outcome: "proof_required" },
    );
    expect(invokeMock).toHaveBeenLastCalledWith("decide_raw_access", {
      grantId: grant.grant_id,
      decision: "window_1h",
      proof: null,
    });

    invokeMock.mockResolvedValueOnce({ ...running, status: "revoked" });
    await expect(revokeRawGrant(running.grant_id)).resolves.toMatchObject({
      status: "revoked",
    });
    expect(invokeMock).toHaveBeenLastCalledWith("revoke_raw_grant", {
      grantId: running.grant_id,
    });

    invokeMock.mockResolvedValueOnce({
      items: [{ ...grant, grant_id: "x" }],
      active: [],
      unlocked: false,
    });
    await expect(listRawAccess()).rejects.toThrow("$.items[0].grant_id");
    invokeMock.mockResolvedValueOnce({ items: [], unlocked: false });
    await expect(listRawAccess()).rejects.toThrow("$.active");
    invokeMock.mockResolvedValueOnce({ outcome: "approved" });
    await expect(decideRawAccess(grant.grant_id, "deny")).rejects.toThrow(
      "$.outcome",
    );
  });

  it("reads raw sealing state and forwards unlock and password proofs", async () => {
    const status = {
      raw_available: true,
      configured: true,
      password_set: true,
      password_required: false,
      envelopes: ["password"],
      key_verified: true,
      unlocked: false,
      unlock_expires_at: null,
      unlock_idle_seconds: 900,
      retry_after_seconds: 0,
      password_min_length: 8,
      password_max_length: 128,
      key_replaced: false,
    };
    invokeMock.mockResolvedValueOnce(status);
    await expect(getRawSealingStatus()).resolves.toEqual(status);
    expect(invokeMock).toHaveBeenLastCalledWith("raw_sealing_status");

    const unlocked = {
      ...status,
      unlocked: true,
      unlock_expires_at: "2026-09-28T10:15:00Z",
    };
    invokeMock.mockResolvedValueOnce({ outcome: "sealing", status: unlocked });
    await expect(
      unlockRaw({ kind: "password", password: "correct horse" }),
    ).resolves.toEqual({
      outcome: "sealing",
      status: unlocked,
      reset: null,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("unlock_raw", {
      proof: { kind: "password", password: "correct horse" },
    });

    invokeMock.mockResolvedValueOnce(status);
    await expect(lockRaw()).resolves.toEqual(status);
    expect(invokeMock).toHaveBeenLastCalledWith("lock_raw");

    invokeMock.mockResolvedValueOnce({ outcome: "sealing", status });
    await expect(
      acknowledgeRawKey({ kind: "password", password: "terminal passphrase" }),
    ).resolves.toEqual({
      outcome: "sealing",
      status,
      reset: null,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("acknowledge_raw_key", {
      proof: { kind: "password", password: "terminal passphrase" },
    });

    invokeMock.mockResolvedValueOnce(status);
    await expect(getRawSealingStatus()).resolves.toMatchObject({
      key_replaced: false,
    });
    const { key_replaced: _verdict, ...withoutVerdict } = status;
    invokeMock.mockResolvedValueOnce(withoutVerdict);
    await expect(lockRaw()).resolves.toEqual(status);
    invokeMock.mockResolvedValueOnce({ ...status, key_replaced: "yes" });
    await expect(lockRaw()).rejects.toThrow("$.key_replaced");

    invokeMock.mockResolvedValueOnce({ outcome: "password_invalid" });
    await expect(
      setRawPassword("change", "new passphrase", {
        kind: "password",
        password: "old passphrase",
      }),
    ).resolves.toEqual({ outcome: "password_invalid" });
    expect(invokeMock).toHaveBeenLastCalledWith("set_raw_password", {
      action: "change",
      password: "new passphrase",
      proof: { kind: "password", password: "old passphrase" },
    });

    invokeMock.mockResolvedValueOnce({
      outcome: "sealing",
      status: { ...status, reset: { deleted_parts: 4, affected_records: 2 } },
    });
    await expect(setRawPassword("reset")).resolves.toEqual({
      outcome: "sealing",
      status,
      reset: { deleted_parts: 4, affected_records: 2 },
    });
    expect(invokeMock).toHaveBeenLastCalledWith("set_raw_password", {
      action: "reset",
      password: null,
      proof: null,
    });

    invokeMock.mockResolvedValueOnce({ ...status, envelopes: ["local"] });
    await expect(getRawSealingStatus()).rejects.toThrow("$.envelopes[0]");
    invokeMock.mockResolvedValueOnce({ outcome: "decided" });
    await expect(
      unlockRaw({ kind: "password", password: "correct horse" }),
    ).rejects.toThrow("$.outcome");
  });

  it("reads which saved data no longer decrypts", async () => {
    const status = {
      unreadable_credentials: 2,
      unreadable_access_tokens: 1,
      audit_key_missing: true,
    };
    invokeMock.mockResolvedValueOnce(status);
    await expect(getLocalDataStatus()).resolves.toEqual(status);
    expect(invokeMock).toHaveBeenLastCalledWith("local_data_status");

    invokeMock.mockResolvedValueOnce({ ...status, service_ids: ["a"] });
    await expect(getLocalDataStatus()).rejects.toThrow("service_ids");
  });

  it("validates proxy probe responses without persisting draft credentials", async () => {
    const input = {
      proxy: {
        mode: "custom" as const,
        url: "socks5://localhost:1080",
        credential: { username: "user", password: "" },
      },
      target_url: "https://provider.example",
    };
    invokeMock.mockResolvedValueOnce({ latency_ms: 10, status_code: 401 });
    await expect(probeServiceProxy(input)).resolves.toEqual({
      latency_ms: 10,
      status_code: 401,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("probe_service_proxy", {
      input,
    });
    for (const result of [
      { latency_ms: -1, status_code: 200 },
      { latency_ms: 2, status_code: 407 },
      { latency_ms: 2, status_code: "200" },
    ]) {
      invokeMock.mockResolvedValueOnce(result);
      await expect(probeServiceProxy(input)).rejects.toThrow(
        "Invalid proxy probe response",
      );
    }
  });
  it("requests one aggregate for the complete usage window", async () => {
    const window = resolveUsageWindow("1d", new Date(2026, 8, 19, 12));
    invokeMock.mockResolvedValueOnce({
      totals: emptyUsageTotals(),
      by_day: [],
      by_hour: [],
      by_service: [],
      by_model: [],
      by_token: [],
      scanned_records: 0,
    });
    const result = await getUsageSummary(window);
    expect(invokeMock).toHaveBeenCalledWith("get_usage_summary", {
      from: window.from,
      to: window.to,
      timeZone: window.time_zone,
      bucket: "hour",
    });
    expect(result.by_hour).toHaveLength(24);
    expect(result.capped).toBe(false);
  });
  it("roundtrips ordered service IDs and rejects malformed order responses", async () => {
    const record = {
      service_ids: ["service_b", "service_a"],
      etag: '"sha256:abc"',
    };
    invokeMock.mockResolvedValueOnce(record);
    await expect(getServiceOrder()).resolves.toEqual(record);
    expect(invokeMock).toHaveBeenLastCalledWith("get_service_order");
    invokeMock.mockResolvedValueOnce(record);
    await expect(
      updateServiceOrder(record.service_ids, record.etag),
    ).resolves.toEqual(record);
    expect(invokeMock).toHaveBeenLastCalledWith("update_service_order", {
      serviceIds: record.service_ids,
      etag: record.etag,
    });
    invokeMock.mockResolvedValueOnce({
      ...record,
      service_ids: ["service_a", "service_a"],
    });
    await expect(getServiceOrder()).rejects.toThrow("Invalid service order");
    await expect(updateServiceOrder(["bad/path"], record.etag)).rejects.toThrow(
      "Invalid service order",
    );
  });
  beforeEach(() => {
    invokeMock.mockReset();
    downloadMocks.downloadTextFile.mockReset();
    vi.stubGlobal("window", { __TAURI_INTERNALS__: {} });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it.each([
    ["core_status", getCoreStatus],
    ["get_preferences", getPreferences],
  ] as const)(
    "times out a stuck %s and ignores its late reply",
    async (command, read) => {
      vi.useFakeTimers();
      let resolveNative!: (value: unknown) => void;
      invokeMock.mockReturnValueOnce(
        new Promise((resolve) => {
          resolveNative = resolve;
        }),
      );
      const result = read();
      const settled = vi.fn();
      void result.then(settled, settled);
      const failure = expect(result).rejects.toThrow("桌面程序长时间未响应");

      await vi.advanceTimersByTimeAsync(9_999);
      expect(settled).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      await failure;
      expect(invokeMock).toHaveBeenCalledWith(command);
      expect(settled).toHaveBeenCalledTimes(1);
      expect(vi.getTimerCount()).toBe(0);

      resolveNative(validSnapshot());
      await vi.advanceTimersByTimeAsync(0);
      expect(settled).toHaveBeenCalledTimes(1);
      await expect(result).rejects.toThrow("桌面程序长时间未响应");
    },
  );

  it.each([
    ["core_status", getCoreStatus],
    ["get_preferences", getPreferences],
  ] as const)(
    "preserves native %s errors and clears its deadline",
    async (_command, read) => {
      vi.useFakeTimers();
      invokeMock.mockRejectedValueOnce("native read failed");

      await expect(read()).rejects.toThrow("native read failed");
      expect(vi.getTimerCount()).toBe(0);
    },
  );

  it("can read a fresh status after an earlier read timed out", async () => {
    vi.useFakeTimers();
    invokeMock.mockReturnValueOnce(new Promise(() => {}));
    const failure =
      expect(getCoreStatus()).rejects.toThrow("桌面程序长时间未响应");
    await vi.advanceTimersByTimeAsync(10_000);
    await failure;

    invokeMock.mockResolvedValueOnce(validSnapshot());
    await expect(getCoreStatus()).resolves.toMatchObject({ phase: "ready" });
    expect(vi.getTimerCount()).toBe(0);
  });

  it("does not apply local read deadlines to gateway restarts", async () => {
    vi.useFakeTimers();
    let resolveNative!: (value: unknown) => void;
    invokeMock.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveNative = resolve;
      }),
    );
    const result = restartCore();

    await vi.advanceTimersByTimeAsync(15_000);
    resolveNative(validSnapshot());
    await expect(result).resolves.toMatchObject({ phase: "ready" });
  });

  it("forwards session kind and cursor through the native bridge", async () => {
    invokeMock.mockResolvedValue({ items: [], next_cursor: null });
    await listRequestSessions({
      kind: "discovery",
      limit: 50,
      cursor: "older",
    });
    expect(invokeMock).toHaveBeenCalledWith("list_request_sessions", {
      query: { kind: "discovery", limit: 50, cursor: "older" },
    });
    await listRequestSessions();
    expect(invokeMock).toHaveBeenLastCalledWith("list_request_sessions", {
      query: {},
    });
  });

  it("forwards multi-token record and session filters without changing detail fields", async () => {
    invokeMock.mockResolvedValue({ items: [], next_cursor: null });
    const query = {
      limit: 20,
      service_id: "service_a",
      local_access_token_ids: ["token_a", "token_b"],
      status: "failed" as const,
    };
    await listRequestRecords(query);
    expect(invokeMock).toHaveBeenLastCalledWith("list_request_records", {
      query,
    });
    await listRequestSessions({ ...query, kind: "inference" });
    expect(invokeMock).toHaveBeenLastCalledWith("list_request_sessions", {
      query: { ...query, kind: "inference" },
    });
  });

  it("returns the browser fallback only after native bridge detection", async () => {
    vi.stubGlobal("window", {});

    await expect(getCoreStatus()).resolves.toMatchObject({
      app_version: "Unknown",
      phase: "unavailable",
      pid: null,
    });
    expect(invokeMock).not.toHaveBeenCalled();
    await expect(restartCore()).rejects.toThrow(
      "网关重启仅在 AstrLink 桌面应用中可用。",
    );
  });

  it.each([
    ["core_status", getCoreStatus],
    ["restart_core", restartCore],
  ] as const)(
    "parses the frozen fixture returned by %s",
    async (command, callBridge) => {
      const wireSnapshot = validSnapshot();
      invokeMock.mockResolvedValueOnce(wireSnapshot);

      const parsed = await callBridge();

      expect(invokeMock).toHaveBeenCalledWith(command);
      expect(parsed).toEqual(wireSnapshot);
      expect(parsed.capabilities?.protocols).toHaveLength(8);
    },
  );

  it("keeps fallback metadata tied to the advertised inference address", async () => {
    const snapshot = validSnapshot();
    snapshot.inference_port_fallback = {
      requested_port: 9000,
      active_port: 8317,
    };
    invokeMock.mockResolvedValueOnce(snapshot);
    await expect(getCoreStatus()).resolves.toMatchObject({
      inference_port_fallback: { requested_port: 9000, active_port: 8317 },
    });
    for (const fallback of [
      { requested_port: 9000, active_port: 8318 },
      { requested_port: 8317, active_port: 8317 },
      { requested_port: 0, active_port: 8317 },
    ]) {
      invokeMock.mockResolvedValueOnce({
        ...snapshot,
        inference_port_fallback: fallback,
      });
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

    await expect(getCoreStatus()).rejects.toThrow(
      "canonical IPv4 loopback URL",
    );
  });

  it.each([
    "http://localhost:8318",
    "http://[::1]:8317",
    "http://localhost:8317/",
    "http://127.0.0.1:49152",
  ])(
    "rejects a client inference URL %s off the inference port",
    async (url) => {
      const wireSnapshot = validSnapshot();
      (wireSnapshot.ready as any).client_inference_url = url;
      invokeMock.mockResolvedValueOnce(wireSnapshot);

      await expect(getCoreStatus()).rejects.toThrow("client_inference_url");
    },
  );

  it("accepts an IPv4-only client inference URL", async () => {
    const wireSnapshot = validSnapshot();
    (wireSnapshot.ready as any).client_inference_url = "http://127.0.0.1:8317";
    invokeMock.mockResolvedValueOnce(wireSnapshot);

    await expect(getCoreStatus()).resolves.toMatchObject({
      ready: { client_inference_url: "http://127.0.0.1:8317" },
    });
  });

  it.each([
    [
      "ready control version",
      (snapshot: any) => (snapshot.ready.control_api_version = "v2"),
    ],
    [
      "version contract version",
      (snapshot: any) => (snapshot.version.protocol_contract_version = "v2"),
    ],
    [
      "capability contract version",
      (snapshot: any) =>
        (snapshot.capabilities.protocol_contract_version = "v2"),
    ],
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
    await expect(getCoreStatus()).rejects.toThrow(
      "expected at least 8 entries",
    );

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
      (snapshot: any) =>
        (snapshot.capabilities.conversion_engine.available = true),
      "non-empty string when available",
    ],
    [
      "RelayKit version while unavailable",
      (snapshot: any) =>
        (snapshot.capabilities.conversion_engine.version = "0.1.0"),
      "must be null when unavailable",
    ],
    [
      "RelayKit edges while unavailable",
      (snapshot: any) => snapshot.capabilities.conversion_engine.edges.push({}),
      "must be empty when unavailable",
    ],
    [
      "native conversion",
      (snapshot: any) =>
        (snapshot.capabilities.plan_types[0].uses_local_conversion = true),
      "Alpha",
    ],
  ])(
    "rejects invalid capability semantics: %s",
    async (_name, mutate, detail) => {
      const wireSnapshot = validSnapshot();
      mutate(wireSnapshot);
      invokeMock.mockResolvedValueOnce(wireSnapshot);

      await expect(getCoreStatus()).rejects.toThrow(detail);
    },
  );

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

    const usage = {
      items: [{ token_id: token.id, today_tokens: 10, total_tokens: 100 }],
    };
    invokeMock.mockResolvedValueOnce(usage);
    await expect(
      listAccessTokenUsage("2026-09-19T00:00:00.000Z"),
    ).resolves.toEqual({
      items: usage.items.map((item) => ({
        ...item,
        today_billing: null,
        total_billing: null,
      })),
    });
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

    invokeMock.mockResolvedValueOnce(true);
    await expect(copyAccessToken("token_01")).resolves.toBe(true);
    expect(invokeMock).toHaveBeenLastCalledWith("copy_access_token", {
      tokenId: "token_01",
    });

    invokeMock.mockResolvedValueOnce(true);
    await expect(isCCSwitchInstalled()).resolves.toBe(true);
    expect(invokeMock).toHaveBeenLastCalledWith("cc_switch_installed");
    invokeMock.mockResolvedValueOnce("yes");
    await expect(isCCSwitchInstalled()).resolves.toBe(false);

    const ccSwitchInput = {
      tokenId: "token_01",
      client: "gemini" as const,
      models: { model: "gemini-2.5-pro" },
      inferenceUrl: "http://127.0.0.1:8317",
    };
    invokeMock.mockResolvedValueOnce(undefined);
    await openCCSwitchImport(ccSwitchInput);
    expect(invokeMock).toHaveBeenLastCalledWith(
      "open_cc_switch_import",
      ccSwitchInput,
    );

    const statuses = [
      {
        client: "claude",
        detected: true,
        paths: ["/Users/me/.claude/settings.json"],
        state: "configured",
        token_id: "token_01",
      },
      {
        client: "codex",
        detected: false,
        paths: ["/Users/me/.codex/config.toml"],
        state: "not_configured",
        token_id: null,
      },
      {
        client: "pi",
        detected: true,
        paths: [
          "/Users/me/.pi/agent/models.json",
          "/Users/me/.pi/agent/settings.json",
        ],
        state: "not_configured",
        token_id: null,
      },
    ];
    invokeMock.mockResolvedValueOnce(statuses);
    await expect(
      getClientConfigStatus("http://127.0.0.1:8317"),
    ).resolves.toEqual(statuses);
    expect(invokeMock).toHaveBeenLastCalledWith("client_config_status", {
      inferenceUrl: "http://127.0.0.1:8317",
    });
    invokeMock.mockResolvedValueOnce([statuses[1], statuses[0]]);
    await expect(getClientConfigStatus(null)).rejects.toThrow(
      "Invalid client-config IPC response",
    );

    const proxyCheck = {
      client: { route: "blocked", proxy: "127.0.0.1:7892" },
      numeric: null,
    };
    invokeMock.mockResolvedValueOnce(proxyCheck);
    await expect(checkClientProxy("http://localhost:8317")).resolves.toEqual(
      proxyCheck,
    );
    expect(invokeMock).toHaveBeenLastCalledWith("check_client_proxy", {
      inferenceUrl: "http://localhost:8317",
    });
    invokeMock.mockResolvedValueOnce({ client: { route: "direct" } });
    await expect(checkClientProxy("http://localhost:8317")).rejects.toThrow(
      "Invalid client-config IPC response",
    );

    const target = {
      tokenId: "token_01",
      client: "codex" as const,
      models: { model: "gpt-5" },
      inferenceUrl: "http://127.0.0.1:8317",
    };
    invokeMock.mockResolvedValueOnce({
      status: "needs_confirmation",
      keys: ["model_provider"],
    });
    await expect(
      applyClientConfig({ ...target, replace: false }),
    ).resolves.toEqual({
      status: "needs_confirmation",
      keys: ["model_provider"],
    });
    expect(invokeMock).toHaveBeenLastCalledWith("apply_client_config", {
      ...target,
      replace: false,
    });
    invokeMock.mockResolvedValueOnce({ status: "applied" });
    await expect(
      applyClientConfig({ ...target, replace: true }),
    ).resolves.toEqual({ status: "applied" });
    invokeMock.mockResolvedValueOnce({ status: "applied", keys: [] });
    await expect(
      applyClientConfig({ ...target, replace: true }),
    ).rejects.toThrow("unexpected field");

    invokeMock.mockResolvedValueOnce(undefined);
    await removeClientConfig("claude");
    expect(invokeMock).toHaveBeenLastCalledWith("remove_client_config", {
      client: "claude",
    });

    invokeMock.mockResolvedValueOnce(
      'experimental_bearer_token = "astr_…abcd"',
    );
    await expect(previewClientConfigSnippet(target)).resolves.toContain(
      "astr_…abcd",
    );
    expect(invokeMock).toHaveBeenLastCalledWith(
      "preview_client_config_snippet",
      target,
    );
    invokeMock.mockResolvedValueOnce(`token = "${secret}"`);
    await expect(previewClientConfigSnippet(target)).rejects.toThrow(
      "must show the token hint",
    );
    invokeMock.mockResolvedValueOnce(false);
    await expect(copyClientConfigSnippet(target)).resolves.toBe(false);
    expect(invokeMock).toHaveBeenLastCalledWith(
      "copy_client_config_snippet",
      target,
    );

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
      skip_tool_declarations: false,
      inspect_additional_tools: false,
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
      version: null,
      recommended: false,
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

    const release = { ...catalogModel, version: "0.2.0", recommended: true };
    invokeMock.mockResolvedValueOnce({ items: [release] });
    await expect(getPrivacyModelReleases()).resolves.toEqual({
      items: [release],
    });
    expect(invokeMock).toHaveBeenLastCalledWith("get_privacy_model_releases");
    invokeMock.mockResolvedValueOnce({
      items: [{ ...release, version: "v0.2.0" }],
    });
    await expect(getPrivacyModelReleases()).rejects.toThrow("version");

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
      probeLocalPrivacyModel({
        path: "  /Volumes/models/privacy/model_int8.onnx  ",
      }),
    ).resolves.toEqual(localProbe);
    expect(invokeMock).toHaveBeenLastCalledWith("probe_local_privacy_model", {
      input: { path: "/Volumes/models/privacy/model_int8.onnx" },
    });
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
    await expect(getPrivacyModelInstallation(installation.id)).resolves.toEqual(
      installation,
    );
    expect(invokeMock).toHaveBeenLastCalledWith(
      "get_privacy_model_installation",
      { installationId: installation.id },
    );

    invokeMock.mockResolvedValueOnce({ ...installation, status: "paused" });
    await expect(
      pausePrivacyModelInstallation(installation.id),
    ).resolves.toMatchObject({ status: "paused" });
    expect(invokeMock).toHaveBeenLastCalledWith(
      "pause_privacy_model_installation",
      { installationId: installation.id },
    );
    invokeMock.mockResolvedValueOnce(installation);
    await expect(
      resumePrivacyModelInstallation(installation.id),
    ).resolves.toEqual(installation);
    expect(invokeMock).toHaveBeenLastCalledWith(
      "resume_privacy_model_installation",
      { installationId: installation.id },
    );
    await expect(
      resumePrivacyModelInstallation("../invalid"),
    ).rejects.toThrow();
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

  it("clears a subscription risk and reads its history through strict parsers", async () => {
    const service = {
      id: "service_claude_01",
      name: "Claude subscription",
      kind: "claude_subscription",
      enabled: true,
      models: [],
      capabilities: [
        { protocol: "anthropic.messages", mode: "native", streaming: true },
      ],
      subscription: { provider: "claude_code", status: "connected" },
      created_at: "2026-07-28T08:00:00Z",
      updated_at: "2026-07-28T08:05:00Z",
    };
    const etag = `"sha256:${"e".repeat(64)}"`;
    invokeMock.mockResolvedValueOnce({ service, etag });
    await expect(clearServiceRisk(service.id)).resolves.toEqual({
      service,
      etag,
    });
    expect(invokeMock).toHaveBeenLastCalledWith("clear_service_risk", {
      serviceId: service.id,
    });

    invokeMock.mockResolvedValueOnce({
      service: { ...service, risk: 1 },
      etag,
    });
    await expect(clearServiceRisk(service.id)).rejects.toThrow(
      /Invalid Service IPC response/,
    );

    const event = {
      id: 3,
      service_id: service.id,
      kind: "suspended",
      code: "organization_disabled",
      http_status: 403,
      observed_at: "2026-07-28T08:01:00Z",
    };
    invokeMock.mockResolvedValueOnce({ items: [event] });
    await expect(listServiceRiskEvents(service.id, 20)).resolves.toEqual([
      event,
    ]);
    expect(invokeMock).toHaveBeenLastCalledWith("list_service_risk_events", {
      serviceId: service.id,
      limit: 20,
    });

    invokeMock.mockResolvedValueOnce({ items: [] });
    await expect(listServiceRiskEvents(service.id)).resolves.toEqual([]);
    expect(invokeMock).toHaveBeenLastCalledWith("list_service_risk_events", {
      serviceId: service.id,
      limit: null,
    });

    invokeMock.mockResolvedValueOnce({
      items: [{ ...event, service_id: "service_other" }],
    });
    await expect(listServiceRiskEvents(service.id)).rejects.toThrow(
      /belongs to another service/,
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

    const importID = "0123456789abcdef0123456789abcdef";
    const importInput = {
      name: "Relay",
      kind: "openai_compatible" as const,
      enabled: true,
      models: [],
      http: {
        base_url: "https://relay.example/v1",
        auth: { scheme: "bearer" as const },
      },
      capabilities: [
        { protocol: "openai.chat", mode: "native" as const, streaming: true },
      ],
    };
    invokeMock.mockResolvedValueOnce({ service, etag });
    await expect(confirmProviderImport(importID, importInput)).resolves.toEqual(
      { service, etag },
    );
    expect(invokeMock).toHaveBeenLastCalledWith("confirm_provider_import", {
      id: importID,
      input: importInput,
    });
    invokeMock.mockResolvedValueOnce(null);
    await expect(getPendingProviderImport()).resolves.toBeNull();
    invokeMock.mockResolvedValueOnce({
      status: "invalid",
      id: importID,
      reason: "missing_parameter",
      field: "base_url",
    });
    await expect(getPendingProviderImport()).resolves.toEqual({
      status: "invalid",
      id: importID,
      reason: "missing_parameter",
      field: "base_url",
    });
    expect(invokeMock).toHaveBeenLastCalledWith("pending_provider_import");
    invokeMock.mockResolvedValueOnce(undefined);
    await dismissProviderImport(importID);
    expect(invokeMock).toHaveBeenLastCalledWith("dismiss_provider_import", {
      id: importID,
    });

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

    const { authorization_url: _authorizationURL, ...terminalSession } =
      session;
    invokeMock.mockResolvedValueOnce({
      ...terminalSession,
      status: "cancelled",
    });
    await expect(cancelServiceAuthorization(service.id)).resolves.toMatchObject(
      {
        status: "cancelled",
      },
    );

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
      fresh: false,
    });
    invokeMock.mockResolvedValueOnce(usage);
    await expect(getServiceUsage(service.id, { fresh: true })).resolves.toEqual(
      usage,
    );
    expect(invokeMock).toHaveBeenLastCalledWith("get_service_usage", {
      serviceId: service.id,
      fresh: true,
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
    expect(invokeMock).toHaveBeenLastCalledWith("probe_draft_service_models", {
      input: draftProbe,
    });

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
      cli_binary: false,
      tools: [
        {
          id: "cursor",
          detected: true,
          skills: [
            {
              id: "astrlink-debug",
              installed: false,
              preview_paths: [
                "/tmp/.cursor/skills/astrlink-debug",
                "/tmp/.astrlink/bin/astrlink",
              ],
            },
            {
              id: "redaction-placeholders",
              installed: false,
              preview_paths: ["/tmp/.cursor/skills/redaction-placeholders"],
            },
          ],
          cli_access: "prompt",
          cli_access_installed: false,
          guard: "skill_only",
          guard_installed: false,
        },
      ],
      shared_paths: ["/tmp/.astrlink/agent-installs.json"],
    };
    invokeMock.mockResolvedValueOnce(status);
    await expect(getAgentDebugStatus()).resolves.toEqual(status);
    expect(invokeMock).toHaveBeenLastCalledWith("agent_debug_status");

    const receipt = {
      version: 2,
      skills: [{ id: "redaction-placeholders", version: "0.1.0" }],
      installed_at_unix: 1,
      cli_binary: null,
      files: ["/tmp/a"],
    };
    invokeMock.mockResolvedValueOnce(receipt);
    await expect(
      installAgentDebug(["redaction-placeholders"], ["grok"]),
    ).resolves.toEqual(receipt);
    expect(invokeMock).toHaveBeenLastCalledWith("install_agent_debug", {
      skillIds: ["redaction-placeholders"],
      toolIds: ["grok"],
    });

    invokeMock.mockResolvedValueOnce(undefined);
    await uninstallAgentDebug();
    expect(invokeMock).toHaveBeenLastCalledWith("uninstall_agent_debug");
  });

  it("saves text through the native dialog command", async () => {
    invokeMock.mockResolvedValueOnce("/Users/me/Downloads/astrlink-req_1.md");

    await expect(saveTextFile("astrlink-req_1.md", "# bundle")).resolves.toBe(
      "/Users/me/Downloads/astrlink-req_1.md",
    );
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

describe("provider test bridge", () => {
  it("passes the selected provider and validates its result", async () => {
    vi.stubGlobal("window", { __TAURI_INTERNALS__: {} });
    const input = {
      protocol: "openai.chat" as const,
      model: "test-model",
      stream: false,
    };
    const result = {
      ...input,
      service_id: "service_test",
      ok: true,
      status_code: 200,
      duration_ms: 100,
      output: "OK",
    };
    invokeMock.mockResolvedValueOnce(result);
    expect(await testService("service_test", input)).toEqual(result);
    expect(invokeMock).toHaveBeenLastCalledWith("test_service", {
      serviceId: "service_test",
      input,
    });
    invokeMock.mockResolvedValueOnce({ ...result, duration_ms: -1 });
    await expect(testService("service_test", input)).rejects.toThrow(
      "Invalid provider test result",
    );
    vi.unstubAllGlobals();
  });
});
