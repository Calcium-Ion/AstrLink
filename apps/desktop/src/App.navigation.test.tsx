// @vitest-environment happy-dom

import {
  browserUpdateSnapshot,
  type UpdateSnapshot,
  defaultUpdatePreferences,
} from "./update-model";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  builtinToolAction: vi.fn().mockResolvedValue({ configured: false }),
  cancelPrivacyModelInstallation: vi.fn(),
  createAccessToken: vi.fn(),
  decideRawAccess: vi.fn(),
  createService: vi.fn(),
  deleteAccessToken: vi.fn(),
  deleteService: vi.fn(),
  deletePrivacyModelInstallation: vi.fn(),
  getAgentDebugStatus: vi.fn(),
  getAuditSettings: vi.fn(),
  getCoreStatus: vi.fn(),
  getLocalDataStatus: vi.fn(),
  getPreferences: vi.fn(),
  getTrayState: vi
    .fn()
    .mockRejectedValue(new Error("tray unavailable in tests")),
  trayAction: vi.fn().mockRejectedValue(new Error("tray unavailable in tests")),
  getRoutingSettings: vi.fn(),
  getServiceOrder: vi
    .fn()
    .mockResolvedValue({ service_ids: [], etag: '"order"' }),
  updateServiceOrder: vi.fn(),
  installAgentDebug: vi.fn(),
  isCCSwitchInstalled: vi.fn().mockResolvedValue(false),
  getClientConfigStatus: vi.fn().mockResolvedValue([]),
  previewClientConfigSnippet: vi.fn().mockResolvedValue("{}"),
  copyClientConfigSnippet: vi.fn(),
  uninstallAgentDebug: vi.fn(),
  getService: vi.fn(),
  getServiceAuthorization: vi.fn(),
  getServiceUsage: vi.fn(),
  resetServiceUsage: vi.fn(),
  getPrivacyModelCatalog: vi.fn(),
  getPrivacyModelInstallation: vi.fn(),
  getPrivacyPolicy: vi.fn(),
  getRequestAuditContent: vi.fn(),
  installPrivacyModel: vi.fn(),
  listAccessTokens: vi.fn(),
  listRawAccess: vi.fn().mockResolvedValue([]),
  listAccessTokenUsage: vi.fn().mockResolvedValue({ items: [] }),
  listServices: vi.fn(),
  listPrivacyModelInstallations: vi.fn(),
  listPrivacyPolicies: vi.fn(),
  listRequestRecords: vi.fn(),
  getUsageSummary: vi.fn(),
  listRequestSessions: vi.fn(),
  getRequestSession: vi.fn(),
  probePrivacyModel: vi.fn(),
  purgeRequestRecords: vi.fn(),
  copyAccessToken: vi.fn(),
  restartCore: vi.fn(),
  startCore: vi.fn(),
  stopCore: vi.fn(),
  updatePreferences: vi.fn(),
  updateAuditSettings: vi.fn(),
  updateService: vi.fn(),
  updatePrivacyPolicy: vi.fn(),
  deleteRequestRecord: vi.fn(),
  getRequestRecord: vi.fn(),
  beginServiceAuthorization: vi.fn(),
  cancelServiceAuthorization: vi.fn(),
  clearServiceRisk: vi.fn(),
  listServiceRiskEvents: vi.fn(),
  logoutService: vi.fn(),
  openAuthorizationURL: vi.fn(),
  probeDraftServiceModels: vi.fn(),
  probeServiceModels: vi.fn(),
  getRawSealingStatus: vi.fn(),
  listenRawSealingChanged: vi.fn(async () => () => {}),
  lockRaw: vi.fn(),
  setRawPassword: vi.fn(),
  unlockRaw: vi.fn(),
}));

const updateMocks = vi.hoisted(() => ({
  get: vi.fn(),
  listen: vi.fn(),
  info: vi.fn(),
}));
vi.mock("./update-bridge", () => ({
  getAppUpdateStatus: updateMocks.get,
  listenAppUpdates: updateMocks.listen,
  checkAppUpdate: vi.fn(),
  downloadAppUpdate: vi.fn(),
  installAppUpdate: vi.fn(),
  saveUpdatePreferences: vi.fn(),
}));
vi.mock("sonner", async (importOriginal) => {
  const original = await importOriginal<typeof import("sonner")>();
  return { ...original, toast: { ...original.toast, info: updateMocks.info } };
});

vi.mock("./bridge", () => bridgeMocks);

import App from "./App";
import type { AppSnapshot } from "./core-model";
import { defaultTrayPreferences } from "./preferences-model";
import { defaultFailurePolicy } from "./failure-policy-model";
import { defaultPrivacyKindRules } from "./privacy-policy-model";
import { ONBOARDING_STORAGE_KEY } from "./use-onboarding";

const readySnapshot: AppSnapshot = {
  app_version: "0.1.0",
  phase: "ready",
  pid: 42,
  ready: {
    event: "ready",
    core_version: "0.1.0",
    control_api_version: "v1",
    protocol_contract_version: "v1",
    inference_url: "http://127.0.0.1:8317",
    client_inference_url: "http://localhost:8317",
    control_url: "http://127.0.0.1:43117",
  },
  health: { status: "ok" },
  version: {
    core_version: "0.1.0",
    control_api_version: "v1",
    protocol_contract_version: "v1",
    build_commit: "unknown",
  },
  capabilities: {
    protocol_contract_version: "v1",
    protocols: [
      {
        id: "openai.responses",
        phase: "alpha",
        primary: true,
        streaming: true,
      },
    ],
    plan_types: [
      {
        id: "native",
        available_in_alpha: true,
        uses_local_conversion: false,
      },
    ],
    conversion_engine: {
      name: "relaykit",
      version: null,
      available: false,
      edges: [],
    },
  },
  last_error: null,
  inference_port_fallback: null,
  recovery_attempt: 0,
  recovery_scheduled_in_ms: null,
};

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) =>
      (candidate.getAttribute("aria-label") ??
        candidate.textContent?.trim()) === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

function workspaceHeading(): HTMLHeadingElement {
  const headings = [
    ...document.querySelectorAll<HTMLHeadingElement>(
      '[data-slot="workspace"] h1',
    ),
  ];
  if (headings.length !== 1) {
    throw new Error(`Expected one workspace heading, found ${headings.length}`);
  }
  return headings[0];
}

async function setInput(selector: string, value: string): Promise<void> {
  const input = document.querySelector<HTMLInputElement>(selector);
  if (!input) throw new Error(`Missing input: ${selector}`);
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  if (!valueSetter) throw new Error("Missing HTMLInputElement value setter");
  await act(async () => {
    valueSetter.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

/** Picks a card in the open provider-type dialog. */
async function chooseKindCard(option: string): Promise<void> {
  const card = [
    ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
  ].find(
    (candidate) =>
      candidate.querySelector('[data-slot="dialog-picker-label"]')
        ?.textContent === option,
  );
  if (!card) throw new Error(`Missing API provider type: ${option}`);
  await act(async () => {
    card.click();
    await Promise.resolve();
  });
}

/** A raw password unless `overrides` says otherwise. */
function rawSealing(overrides: Record<string, unknown> = {}) {
  return {
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
    ...overrides,
  };
}

/** Nothing opens the raw key yet: no password, no keychain key. */
const noRawPassword = rawSealing({
  raw_available: false,
  configured: false,
  password_set: false,
  password_required: true,
  envelopes: [],
  key_verified: false,
});

function proofDialog(): HTMLElement | null {
  return document.querySelector('[data-slot="proof-confirm-dialog"]');
}

function queryButton(label: string, scope: ParentNode): HTMLElement | null {
  return (
    [...scope.querySelectorAll("button")].find(
      (candidate) => candidate.textContent?.trim() === label,
    ) ?? null
  );
}

async function typeNewPassword(password: string): Promise<void> {
  const inputs = document.querySelectorAll<HTMLInputElement>(
    'input[autocomplete="new-password"]',
  );
  if (inputs.length !== 2) throw new Error("Missing new password fields");
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  if (!valueSetter) throw new Error("Missing HTMLInputElement value setter");
  for (const input of inputs) {
    await act(async () => {
      valueSetter.call(input, password);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }
}

async function pressEscape(): Promise<void> {
  await act(async () => {
    document.activeElement?.dispatchEvent(
      new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
    );
  });
}

describe("App workspace navigation", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    updateMocks.get.mockReset().mockResolvedValue(browserUpdateSnapshot());
    updateMocks.listen.mockReset().mockResolvedValue(() => {});
    updateMocks.info.mockReset();

    localStorage.removeItem(ONBOARDING_STORAGE_KEY);
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    bridgeMocks.getLocalDataStatus.mockResolvedValue({
      unreadable_credentials: 0,
      unreadable_access_tokens: 0,
      audit_key_missing: false,
    });
    // A raw password is set, so the required dialog stays away (D11).
    bridgeMocks.getRawSealingStatus.mockResolvedValue(rawSealing());
    bridgeMocks.getRoutingSettings.mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
    });
    bridgeMocks.getCoreStatus.mockResolvedValue(readySnapshot);
    bridgeMocks.listServices.mockResolvedValue({
      items: [
        {
          id: "service_gateway_01",
          name: "Primary gateway",
          kind: "newapi",
          enabled: true,
          models: ["gpt-5"],
          capabilities: [
            {
              protocol: "openai.responses",
              mode: "delegated",
              streaming: true,
            },
          ],
          http: {
            base_url: "https://gateway.example",
            auth: { scheme: "bearer" },
            credential_ref: "local://service/service_gateway_01",
          },
          created_at: "2026-07-28T08:00:00Z",
          updated_at: "2026-07-28T08:00:00Z",
        },
        {
          id: "service_codex_01",
          name: "Codex 订阅",
          kind: "codex_subscription",
          enabled: true,
          models: [],
          capabilities: [
            {
              protocol: "openai.responses",
              mode: "native",
              streaming: true,
            },
          ],
          subscription: {
            provider: "openai_codex",
            status: "disconnected",
          },
          created_at: "2026-07-28T08:00:00Z",
          updated_at: "2026-07-28T08:00:00Z",
        },
      ],
      next_cursor: null,
    });
    bridgeMocks.listAccessTokens.mockResolvedValue({
      items: [
        {
          id: "token_01",
          name: "VS Code",
          hint: "astr_…K8Q2",
          created_at: "2026-07-24T10:30:00Z",
        },
      ],
      next_cursor: null,
    });
    bridgeMocks.getPrivacyPolicy.mockResolvedValue({
      policy: {
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
        allowlist_rules: [],
        restore_tool_arguments: true,
        placeholder_notice: true,
        skip_tool_declarations: false,
        inspect_additional_tools: false,
        match: {},
      },
      etag: `"sha256:${"a".repeat(64)}"`,
    });
    bridgeMocks.getPrivacyModelCatalog.mockResolvedValue({
      items: [
        {
          id: "catalog_sheltron_ettin_32m",
          name: "Ettin Privacy 32M",
          summary: "轻量隐私检测模型。",
          source: "community",
          repo_id: "sheltron-ai/privacy-filter-ettin-32m",
          revision: "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
          license: "apache-2.0",
          languages: ["en"],
          adapter: "hf_token_classification",
          variants: [
            {
              id: "cpu_int8",
              name: "CPU INT8",
              quantization: "int8",
              bytes_total: 180_000_000,
              estimated_ram_bytes: 420_000_000,
              recommended: true,
              supported: true,
              unsupported_reason: null,
            },
          ],
        },
      ],
    });
    bridgeMocks.listPrivacyModelInstallations.mockResolvedValue({ items: [] });
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.getUsageSummary.mockImplementation(async (window) => ({
      window,
      totals: {
        requests: 0,
        failed_requests: 0,
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 0,
        cache_read_tokens: 0,
        cache_write_tokens: 0,
      },
      by_day: [],
      by_hour: [],
      by_service: [],
      by_model: [],
      by_token: [],
      scanned_records: 0,
      capped: false,
    }));
    bridgeMocks.listRequestSessions.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.getServiceAuthorization.mockRejectedValue(
      new Error("no active authorization session"),
    );
    bridgeMocks.getAuditSettings.mockResolvedValue({
      request_body_enabled: false,
      response_content_enabled: false,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
      agent_raw_access_enabled: true,
    });
    const agentSkills = (debug: boolean) => [
      { id: "astrlink-debug", installed: debug, preview_paths: [] },
      { id: "redaction-placeholders", installed: false, preview_paths: [] },
    ];
    bridgeMocks.getAgentDebugStatus.mockResolvedValue({
      cli_binary: false,
      tools: [
        {
          id: "cursor",
          detected: true,
          skills: agentSkills(false),
          cli_access: "prompt",
          cli_access_installed: false,
          guard: "skill_only",
          guard_installed: false,
        },
        {
          id: "claude",
          detected: false,
          skills: agentSkills(false),
          cli_access: "allow_rules",
          cli_access_installed: false,
          guard: "deny_rules",
          guard_installed: false,
        },
        {
          id: "codex",
          detected: true,
          skills: agentSkills(true),
          cli_access: "exec_policy",
          cli_access_installed: true,
          guard: "instructions",
          guard_installed: true,
        },
      ],
      shared_paths: [],
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    localStorage.removeItem(ONBOARDING_STORAGE_KEY);
    await act(async () => {
      root.unmount();
    });
    vi.restoreAllMocks();
    vi.useRealTimers();
    container.remove();
  });

  async function renderApp(): Promise<void> {
    await act(async () => {
      root.render(<App />);
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });
  }

  it("opens About independently of gateway readiness and settings", async () => {
    await renderApp();
    await act(async () => button("关于").click());
    expect(workspaceHeading().textContent).toBe("关于");
    expect(
      container.querySelector('[data-slot="about-update-panel"]'),
    ).not.toBeNull();
    const appUpdates = container.querySelector(
      '[data-slot="about-update-panel"]',
    )!;
    expect(appUpdates.querySelector("h2")?.textContent).toBe("AstrLink");
    expect(appUpdates.textContent).toContain("更新设置");
    const localClients = container.querySelector(
      '[data-slot="local-clients"]',
    )!;
    expect(appUpdates.contains(localClients)).toBe(false);
    expect(
      appUpdates.compareDocumentPosition(localClients) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).not.toBe(0);
    expect(container.querySelector('[role="tablist"]')).toBeNull();
  });

  it("keeps receiving updates across navigation and routes ready notifications to About", async () => {
    let listener: ((snapshot: UpdateSnapshot) => void) | undefined;
    updateMocks.listen.mockImplementation(async (callback) => {
      listener = callback;
      return () => {};
    });
    await renderApp();
    await act(async () => button("关于").click());
    await act(async () => button("概览").click());
    const ready: UpdateSnapshot = {
      ...browserUpdateSnapshot(),
      revision: 2,
      phase: "ready",
      release: {
        version: "1.1.0",
        notes: "New release",
        published_at: null,
        url: "https://github.com/Calcium-Ion/AstrLink/releases/tag/v1.1.0",
      },
    };
    await act(async () => listener!(ready));
    expect(updateMocks.info).toHaveBeenCalledOnce();
    expect(button("关于").querySelector('[role="status"]')).not.toBeNull();
    await act(async () => listener!({ ...ready, revision: 3 }));
    expect(updateMocks.info).toHaveBeenCalledOnce();
    await act(async () => updateMocks.info.mock.calls[0][1].action.onClick());
    expect(workspaceHeading().textContent).toBe("关于");
    expect(container.textContent).toContain("1.1.0");
    // Late command results must not overwrite a newer event.
    await act(async () =>
      listener!({ ...ready, revision: 1, phase: "downloading" }),
    );
    expect(container.textContent).toContain("更新已就绪");
  });

  it("announces downloads and manual upgrades that wait on the operator", async () => {
    let listener: ((snapshot: UpdateSnapshot) => void) | undefined;
    updateMocks.listen.mockImplementation(async (callback) => {
      listener = callback;
      return () => {};
    });
    await renderApp();
    const release = {
      version: "1.1.0",
      notes: "New release",
      published_at: null,
      url: "https://github.com/Calcium-Ion/AstrLink/releases/tag/v1.1.0",
    };
    const base = browserUpdateSnapshot();
    // Automatic download moves on to "ready" by itself; nothing to announce yet.
    await act(async () =>
      listener!({ ...base, revision: 2, phase: "available", release }),
    );
    expect(updateMocks.info).not.toHaveBeenCalled();
    expect(button("关于").querySelector('[role="status"]')).toBeNull();
    const waiting: UpdateSnapshot = {
      ...base,
      revision: 3,
      phase: "available",
      release,
      preferences: { ...base.preferences, auto_download: false },
    };
    await act(async () => listener!(waiting));
    expect(updateMocks.info).toHaveBeenCalledOnce();
    expect(updateMocks.info.mock.calls[0][0]).toContain("可以下载");
    expect(
      button("关于")
        .querySelector('[role="status"]')
        ?.getAttribute("aria-label"),
    ).toBe("发现新版本");
    // A periodic re-check of the same version stays quiet.
    await act(async () => listener!({ ...waiting, revision: 4 }));
    expect(updateMocks.info).toHaveBeenCalledOnce();
    await act(async () =>
      listener!({
        ...base,
        revision: 5,
        phase: "manual",
        release: { ...release, version: "1.2.0" },
      }),
    );
    expect(updateMocks.info).toHaveBeenCalledTimes(2);
    expect(updateMocks.info.mock.calls[1][0]).toContain("版本页面");
    expect(
      button("关于")
        .querySelector('[role="status"]')
        ?.getAttribute("aria-label"),
    ).toBe("有新版本可用");
  });

  it("does not toast an update state already shown on About", async () => {
    let listener: ((snapshot: UpdateSnapshot) => void) | undefined;
    updateMocks.listen.mockImplementation(async (callback) => {
      listener = callback;
      return () => {};
    });
    await renderApp();
    await act(async () => button("关于").click());
    const ready: UpdateSnapshot = {
      ...browserUpdateSnapshot(),
      revision: 2,
      phase: "ready",
      release: {
        version: "1.1.0",
        notes: "New release",
        published_at: null,
        url: "https://github.com/Calcium-Ion/AstrLink/releases/tag/v1.1.0",
      },
    };
    await act(async () => listener!(ready));
    await act(async () => button("概览").click());
    expect(updateMocks.info).not.toHaveBeenCalled();
    expect(button("关于").querySelector('[role="status"]')).not.toBeNull();
  });

  it("starts a confirmed empty workspace with a resumable guide", async () => {
    bridgeMocks.listServices.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.listAccessTokens.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    await renderApp();
    expect(workspaceHeading().textContent).toBe("开始使用 AstrLink");
    // The raw password is set, so the guide opens on the provider step.
    expect(container.textContent).toContain("已完成 1 / 4 步");
    expect(container.querySelector("#usage-heading")).toBeNull();
    expect(localStorage.getItem(ONBOARDING_STORAGE_KEY)).toBe("active");
    await act(async () => button("添加 API 提供商").click());
    expect(container.querySelector('[data-page="create"]')).toBeNull();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      "选择 API 提供商类型",
    );
    await chooseKindCard("Codex 订阅");
    expect(container.querySelector('[data-page="create"]')).not.toBeNull();
    await act(async () => button("返回上手引导").click());
    expect(workspaceHeading().textContent).toBe("开始使用 AstrLink");
    await act(async () => button("稍后设置").click());
    expect(workspaceHeading().textContent).toBe("运行概览");
    expect(localStorage.getItem(ONBOARDING_STORAGE_KEY)).toBe("dismissed");
    expect(container.querySelector("#usage-heading")).not.toBeNull();
    expect(container.textContent).not.toContain("欢迎使用 AstrLink");
    const footer = container.querySelector('[data-slot="overview-footer"]');
    expect(footer?.contains(button("上手引导"))).toBe(true);
    expect(footer?.querySelector("#system-details-heading")).not.toBeNull();
    expect(document.body.textContent).toContain("稍后可点击右下角");
    await act(async () => button("上手引导").click());
    expect(document.body.textContent).not.toContain("稍后可点击右下角");
    expect(workspaceHeading().textContent).toBe("开始使用 AstrLink");
    vi.useFakeTimers();
    await act(async () => button("稍后设置").click());
    expect(document.body.textContent).toContain("稍后可点击右下角");
    await act(async () => vi.advanceTimersByTime(6500));
    expect(document.body.textContent).not.toContain("稍后可点击右下角");
    expect(button("上手引导")).toBeTruthy();
  });

  it("asks a first launch for the raw password before anything else", async () => {
    bridgeMocks.listServices.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.listAccessTokens.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.getRawSealingStatus.mockResolvedValue(noRawPassword);
    bridgeMocks.setRawPassword.mockResolvedValue({
      outcome: "sealing",
      status: rawSealing(),
      reset: null,
    });
    await renderApp();

    expect(workspaceHeading().textContent).toBe("开始使用 AstrLink");
    expect(container.textContent).toContain("已完成 0 / 4 步");
    expect(
      container.querySelector('[aria-current="step"]')?.textContent,
    ).toContain("保护请求原文");
    // The guide asks in its own step, so the required dialog waits.
    expect(proofDialog()).toBeNull();

    await act(async () => button("开始设置").click());
    expect(proofDialog()?.textContent).toContain("保护请求原文");
    await act(async () => button("取消").click());
    expect(proofDialog()).toBeNull();

    // Skipping the guide does not skip the password.
    await act(async () => button("稍后设置").click());
    expect(workspaceHeading().textContent).toBe("运行概览");
    expect(proofDialog()?.textContent).toContain("设置口令并继续");
    expect(queryButton("取消", proofDialog()!)).toBeNull();

    await typeNewPassword("correct horse");
    await act(async () => button("设置口令并继续").click());
    expect(bridgeMocks.setRawPassword).toHaveBeenCalledExactlyOnceWith(
      "set",
      "correct horse",
      undefined,
    );
    expect(proofDialog()).toBeNull();
    await act(async () => button("上手引导").click());
    expect(container.textContent).toContain("已完成 1 / 4 步");
    expect(
      container.querySelector('[aria-current="step"]')?.textContent,
    ).toContain("接入 API 提供商");
  });

  it("keeps an upgraded workspace behind the raw password until one is set", async () => {
    localStorage.setItem(ONBOARDING_STORAGE_KEY, "complete");
    bridgeMocks.getRawSealingStatus.mockResolvedValue(noRawPassword);
    bridgeMocks.setRawPassword.mockResolvedValue({
      outcome: "sealing",
      status: rawSealing(),
      reset: null,
    });
    await renderApp();

    // The workspace is there behind it; the gateway keeps serving.
    expect(workspaceHeading().textContent).toBe("运行概览");
    const dialog = proofDialog();
    expect(dialog?.textContent).toContain("设置口令并继续");
    expect(dialog?.textContent).toContain("新请求的原文不会保存");
    expect(dialog?.textContent).toContain("忘记后只能重置");
    expect(queryButton("取消", dialog!)).toBeNull();
    await pressEscape();
    await act(async () => {
      document
        .querySelector('[data-slot="alert-dialog-overlay"]')
        ?.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    });
    expect(proofDialog()).not.toBeNull();

    await typeNewPassword("correct horse");
    await act(async () => button("设置口令并继续").click());
    expect(bridgeMocks.setRawPassword).toHaveBeenCalledExactlyOnceWith(
      "set",
      "correct horse",
      undefined,
    );
    expect(proofDialog()).toBeNull();
  });

  it("resumes at token creation and continues to client setup after saving", async () => {
    localStorage.setItem(ONBOARDING_STORAGE_KEY, "active");
    bridgeMocks.listAccessTokens.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    const created = {
      id: "token_setup",
      name: "My assistant",
      hint: "astr_…setup",
      created_at: "2026-09-25T08:00:00Z",
    };
    // The saved token is listed by later reads, like the real control API.
    bridgeMocks.createAccessToken.mockImplementation(async () => {
      bridgeMocks.listAccessTokens.mockResolvedValue({
        items: [created],
        next_cursor: null,
      });
      return { token: created, access_token: "test-secret" };
    });
    await renderApp();
    expect(
      container.querySelector('[aria-current="step"]')?.textContent,
    ).toContain("创建访问令牌");
    await act(async () => button("前往创建访问令牌").click());
    await act(async () => button("创建令牌").click());
    await setInput("#access-token-name", "My assistant");
    const form = document.querySelector('[role="dialog"] form');
    expect(form).not.toBeNull();
    await act(async () =>
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      ),
    );
    expect(bridgeMocks.createAccessToken).toHaveBeenCalledWith("My assistant");
    expect(workspaceHeading().textContent).toBe("开始使用 AstrLink");
    expect(
      container.querySelector('[aria-current="step"]')?.textContent,
    ).toContain("连接客户端");
    expect(button("配置客户端")).toBeTruthy();
    expect(bridgeMocks.previewClientConfigSnippet).toHaveBeenLastCalledWith(
      expect.objectContaining({ tokenId: "token_setup", client: "claude" }),
    );
    expect(container.textContent).not.toContain("test-secret");
  });

  it("uses protocol-specific client URLs and only completes after success", async () => {
    localStorage.setItem(ONBOARDING_STORAGE_KEY, "active");
    await renderApp();
    await act(async () => button("其他工具").click());
    expect(container.textContent).toContain("http://localhost:8317/v1");
    await act(async () => button("Anthropic 兼容").click());
    expect(container.textContent).toContain("http://localhost:8317");
    expect(container.textContent).not.toContain("http://localhost:8317/v1");
    const summary = await bridgeMocks.getUsageSummary.mock.results[0].value;
    bridgeMocks.getUsageSummary.mockResolvedValue({
      ...summary,
      scanned_records: 1,
      totals: { ...summary.totals, failed_requests: 1 },
    });
    await act(async () => button("检查连接结果").click());
    expect(container.textContent).toContain("已收到请求，但尚未成功");
    expect(container.textContent).not.toContain("进入运行概览");
    bridgeMocks.getUsageSummary.mockResolvedValue({
      ...summary,
      scanned_records: 2,
      totals: { ...summary.totals, requests: 1, failed_requests: 1 },
    });
    await act(async () => button("检查连接结果").click());
    expect(container.textContent).toContain("第一条请求已成功");
    await act(async () => button("进入运行概览").click());
    expect(localStorage.getItem(ONBOARDING_STORAGE_KEY)).toBe("complete");
    expect(workspaceHeading().textContent).toBe("运行概览");
  });

  it("does not mistake an unread or failed catalog for a first launch", async () => {
    bridgeMocks.listServices.mockRejectedValue(
      new Error("catalog unavailable"),
    );
    bridgeMocks.listAccessTokens.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    await renderApp();
    expect(workspaceHeading().textContent).toBe("运行概览");
    expect(localStorage.getItem(ONBOARDING_STORAGE_KEY)).toBeNull();
  });

  it("preserves a skipped guide across remounts", async () => {
    localStorage.setItem(ONBOARDING_STORAGE_KEY, "dismissed");
    bridgeMocks.listServices.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.listAccessTokens.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    await renderApp();
    expect(workspaceHeading().textContent).toBe("运行概览");
    expect(button("上手引导")).toBeTruthy();
    expect(document.body.textContent).not.toContain("稍后可点击右下角");
  });

  it.each(["disabled", "no-models", "unauthorized"])(
    "keeps an %s provider on the first setup step",
    async (state) => {
      localStorage.setItem(ONBOARDING_STORAGE_KEY, "active");
      const result = await bridgeMocks.listServices();
      const service =
        state === "unauthorized" ? result.items[1] : result.items[0];
      bridgeMocks.listServices.mockResolvedValue({
        items: [
          {
            ...service,
            enabled: state !== "disabled",
            models: state === "no-models" ? [] : ["model"],
          },
        ],
        next_cursor: null,
      });
      await renderApp();
      expect(
        container.querySelector('[aria-current="step"]')?.textContent,
      ).toContain("接入 API 提供商");
      expect(container.textContent).toContain("提供商还未准备好");
      expect(container.textContent).not.toContain("复制访问令牌");
    },
  );

  it("has the host copy a setup token without verification", async () => {
    localStorage.setItem(ONBOARDING_STORAGE_KEY, "active");
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.spyOn(navigator.clipboard, "writeText").mockImplementation(writeText);
    bridgeMocks.copyAccessToken.mockResolvedValue(true);
    bridgeMocks.getRawSealingStatus.mockResolvedValue(rawSealing());
    await renderApp();
    expect(bridgeMocks.copyAccessToken).not.toHaveBeenCalled();
    await act(async () => button("其他工具").click());
    await act(async () => button("复制访问令牌").click());
    expect(
      document.querySelector('[data-slot="proof-confirm-dialog"]'),
    ).toBeNull();
    expect(bridgeMocks.copyAccessToken).toHaveBeenCalledExactlyOnceWith(
      "token_01",
    );
    expect(writeText).not.toHaveBeenCalled();
    expect(localStorage.getItem(ONBOARDING_STORAGE_KEY)).toBe("active");
  });

  it("has the host fill the token into a client's config snippet", async () => {
    localStorage.setItem(ONBOARDING_STORAGE_KEY, "active");
    bridgeMocks.getRawSealingStatus.mockResolvedValue(rawSealing());
    bridgeMocks.previewClientConfigSnippet.mockImplementation(
      async ({ client }: { client: string }) =>
        client === "codex"
          ? 'model_provider = "astrlink"\n'
          : '{ "env": { "ANTHROPIC_AUTH_TOKEN": "astr_…abcd" } }',
    );
    bridgeMocks.copyClientConfigSnippet.mockResolvedValue(true);
    await renderApp();
    expect(container.textContent).toContain("ANTHROPIC_AUTH_TOKEN");
    expect(container.textContent).toContain("~/.claude/settings.json");
    await act(async () => button("Codex").click());
    expect(container.textContent).toContain('model_provider = "astrlink"');
    expect(bridgeMocks.previewClientConfigSnippet).toHaveBeenLastCalledWith({
      tokenId: "token_01",
      client: "codex",
      models: { model: "gpt-5" },
      inferenceUrl: "http://localhost:8317",
    });
    await act(async () => button("复制配置").click());
    expect(bridgeMocks.copyClientConfigSnippet).toHaveBeenCalledExactlyOnceWith(
      {
        tokenId: "token_01",
        client: "codex",
        models: { model: "gpt-5" },
        inferenceUrl: "http://localhost:8317",
      },
    );
  });

  it("keeps loaded page content visible while revisits revalidate slowly", async () => {
    await renderApp();
    const pages = [
      {
        nav: "提供商",
        read: bridgeMocks.getServiceOrder,
        content: "Primary gateway",
      },
      {
        nav: "路由",
        read: bridgeMocks.getRoutingSettings,
        content: "Codex 自动审查",
      },
      {
        nav: "安全",
        read: bridgeMocks.getPrivacyPolicy,
        content: "Regex 覆盖邮箱",
      },
      {
        nav: "Agent 工具",
        read: bridgeMocks.getAgentDebugStatus,
        content: "Cursor",
      },
    ];
    for (const page of pages) {
      await act(async () => button(page.nav).click());
      expect(
        container.querySelector('[data-slot="workspace"]')?.textContent,
      ).toContain(page.content);
      const calls = page.read.mock.calls.length;
      await act(async () => button("概览").click());
      page.read.mockReturnValueOnce(new Promise(() => {}));
      await act(async () => button(page.nav).click());
      expect(page.read).toHaveBeenCalledTimes(calls + 1);
      expect(
        container.querySelector('[data-slot="workspace"]')?.textContent,
      ).toContain(page.content);
      expect(
        container.querySelector('[data-slot="workspace"]')?.textContent,
      ).not.toContain("加载中");
      await act(async () => button("概览").click());
    }
  });

  it("keeps a cached empty records list stable and stops polling when away", async () => {
    vi.useFakeTimers();
    await renderApp();
    await act(async () => button("请求").click());
    const before = container.querySelector(
      '[data-slot="workspace"]',
    )?.textContent;
    await act(async () => button("概览").click());
    const calls = bridgeMocks.listRequestSessions.mock.calls.length;
    await act(async () => vi.advanceTimersByTimeAsync(5_000));
    expect(bridgeMocks.listRequestSessions).toHaveBeenCalledTimes(calls);
    bridgeMocks.listRequestSessions.mockReturnValueOnce(new Promise(() => {}));
    await act(async () => button("请求").click());
    expect(
      container.querySelector('[data-slot="workspace"]')?.textContent,
    ).toBe(before);
    expect(bridgeMocks.listRequestSessions).toHaveBeenCalledTimes(calls + 1);
  });

  it("restores saved preferences, discards abandoned drafts, and protects edits during revalidation", async () => {
    const preferences = {
      values: {
        close_behavior: "hide_to_tray",
        autostart: false,
        core_auto_start: true,
        core_auto_recover: true,
        use_system_proxy: true,
        inference_port: 8317,
        max_concurrent_inspections: 16,
        response_start_timeout_seconds: 0,
        max_request_body_mib: 0,
        locale: "zh-CN",
        theme: "system",
        quota_display_mode: "remaining" as const,
        tray: defaultTrayPreferences(),
        updates: defaultUpdatePreferences(),
      },
      load_warning: null,
      autostart_actual: false,
      autostart_error: null,
      data_backups: [],
      local_key_storage: null,
    };
    bridgeMocks.getPreferences.mockResolvedValue(preferences);
    await renderApp();
    await act(async () => button("设置").click());
    await setInput('input[type="number"]', "9123");
    await act(async () => button("概览").click());
    await act(async () => button("放弃修改并离开").click());
    let finishRefresh!: (value: typeof preferences) => void;
    bridgeMocks.getPreferences.mockReturnValueOnce(
      new Promise((resolve) => {
        finishRefresh = resolve;
      }),
    );
    await act(async () => button("设置").click());
    const port = container.querySelector<HTMLInputElement>(
      'input[type="number"]',
    );
    expect(port?.value).toBe("8317");
    await setInput('input[type="number"]', "9000");
    await act(async () => finishRefresh(preferences));
    expect(port?.value).toBe("9000");
    await act(async () => button("概览").click());
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
  });

  it("invalidates page snapshots when Core changes sessions", async () => {
    vi.useFakeTimers();
    await renderApp();
    await act(async () => button("提供商").click());
    expect(container.textContent).toContain("Primary gateway");
    await act(async () => button("概览").click());
    bridgeMocks.getCoreStatus.mockResolvedValue({ ...readySnapshot, pid: 84 });
    await act(async () => vi.advanceTimersByTimeAsync(1_500));
    bridgeMocks.getServiceOrder.mockReturnValueOnce(new Promise(() => {}));
    await act(async () => button("提供商").click());
    expect(
      container.querySelector('[data-testid="service-list-scroller"]'),
    ).toBeNull();
  });

  it("switches between overview, token manager, safety, service list, and create pages", async () => {
    await renderApp();

    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("概览");
    expect(
      container.querySelectorAll('[data-slot="page-header"]'),
    ).toHaveLength(1);
    expect(workspaceHeading().textContent).toBe("运行概览");
    expect(container.textContent).toContain("API 地址");
    expect(container.textContent).toContain("用量概览");
    expect(
      container.querySelector("[data-slot='activity-heatmap']"),
    ).not.toBeNull();
    expect(container.textContent).toContain("按 API 提供商");
    expect(container.textContent).toContain("按模型");
    expect(container.textContent).toContain("Primary gateway");

    await act(async () => {
      button("令牌").click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("令牌");
    expect(workspaceHeading().textContent).toBe("管理访问令牌");
    expect(container.textContent).toContain("VS Code");

    await act(async () => {
      button("安全").click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("安全");
    expect(workspaceHeading().textContent).toBe("隐私保护");
    expect(container.textContent).toContain("启用隐私保护");
    expect(container.textContent).toContain("Regex 覆盖邮箱");

    await act(async () => {
      button("提供商").click();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("提供商");
    expect(workspaceHeading().textContent).toBe("API 提供商");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("Codex 订阅");
    expect(
      container.querySelector('[aria-label="更多 Codex 订阅 操作"]'),
    ).not.toBeNull();
    expect(container.textContent).not.toContain("ADR 0009");

    await act(async () => {
      button("添加 API 提供商").click();
    });
    expect(workspaceHeading().textContent).toBe("API 提供商");
    await chooseKindCard("New API");
    expect(workspaceHeading().textContent).toBe("添加 API 提供商");
    expect(
      container.querySelector('[data-testid="service-form"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('button[aria-label="API 提供商类型"]')
        ?.textContent,
    ).toContain("New API");
    expect(
      container.querySelector('[data-slot="workspace"]')?.className,
    ).toContain("overflow-hidden");

    const back = container.querySelector<HTMLButtonElement>(
      'button[aria-label="返回 API 提供商列表"]',
    );
    expect(back).not.toBeNull();
    await act(async () => {
      back?.click();
    });
    expect(workspaceHeading().textContent).toBe("API 提供商");
  });

  it("opens a service editor from the overview usage list", async () => {
    bridgeMocks.getService.mockResolvedValue({
      service: {
        id: "service_gateway_01",
        name: "Primary gateway",
        kind: "newapi",
        enabled: true,
        models: ["gpt-5"],
        capabilities: [
          {
            protocol: "openai.responses",
            mode: "delegated",
            streaming: true,
          },
        ],
        http: {
          base_url: "https://gateway.example",
          auth: { scheme: "bearer" },
          credential_ref: "local://service/service_gateway_01",
        },
        created_at: "2026-07-28T08:00:00Z",
        updated_at: "2026-07-28T08:00:00Z",
      },
      etag: `"sha256:${"c".repeat(64)}"`,
    });
    await renderApp();

    const serviceRow = [...container.querySelectorAll("button")].find(
      (candidate) =>
        candidate.textContent?.includes("Primary gateway") &&
        candidate.textContent?.includes("次"),
    );
    if (!(serviceRow instanceof HTMLButtonElement)) {
      throw new Error("Missing overview service usage row");
    }
    await act(async () => {
      serviceRow.click();
      await Promise.resolve();
    });

    expect(workspaceHeading().textContent).toBe("编辑 API 提供商");
    expect(bridgeMocks.getService).toHaveBeenCalledWith("service_gateway_01");
  });

  it("keeps Codex subscription inside API services instead of the sidebar", async () => {
    await renderApp();

    expect(
      container.querySelector('[data-slot="sidebar-navigation"]')?.textContent,
    ).not.toContain("Codex 订阅");

    await act(async () => {
      button("提供商").click();
      await Promise.resolve();
    });

    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("提供商");
    expect(workspaceHeading().textContent).toBe("API 提供商");
    expect(container.textContent).toContain("Codex 订阅");
    expect(
      container.querySelector('[aria-label="更多 Codex 订阅 操作"]'),
    ).not.toBeNull();
    expect(container.textContent).not.toContain("ADR 0009");
    expect(bridgeMocks.listServices).toHaveBeenCalled();
  });

  it("opens the desktop settings center", async () => {
    bridgeMocks.getPreferences.mockResolvedValue({
      values: {
        close_behavior: "hide_to_tray",
        autostart: false,
        core_auto_start: true,
        core_auto_recover: true,
        use_system_proxy: true,
        inference_port: 8317,
        max_concurrent_inspections: 16,
        response_start_timeout_seconds: 0,
        max_request_body_mib: 0,
        locale: "zh-CN",
        theme: "system",
        quota_display_mode: "remaining" as const,
        tray: defaultTrayPreferences(),
        updates: defaultUpdatePreferences(),
      },
      load_warning: null,
      autostart_actual: false,
      autostart_error: null,
      data_backups: [],
      local_key_storage: null,
    });
    await renderApp();

    expect(button("路由").disabled).toBe(false);
    expect(button("安全").disabled).toBe(false);
    expect(button("请求").disabled).toBe(false);
    await act(async () => {
      button("设置").click();
      await Promise.resolve();
    });
    expect(workspaceHeading().textContent).toBe("设置");
    expect(container.textContent).toContain("推理入口");
    expect(container.textContent).toContain("检查并发");
    expect(container.textContent).toContain("响应头等待");
    expect(container.textContent).not.toContain("工具接入");
    expect(
      container.querySelector('[data-slot="local-data-notice"]'),
    ).toBeNull();
  });

  it("points to providers when saved credentials no longer decrypt", async () => {
    bridgeMocks.getPreferences.mockResolvedValue({
      values: {
        close_behavior: "hide_to_tray",
        autostart: false,
        core_auto_start: true,
        core_auto_recover: true,
        use_system_proxy: true,
        inference_port: 8317,
        max_concurrent_inspections: 16,
        response_start_timeout_seconds: 0,
        max_request_body_mib: 0,
        locale: "zh-CN",
        theme: "system",
        quota_display_mode: "remaining" as const,
        tray: defaultTrayPreferences(),
        updates: defaultUpdatePreferences(),
      },
      load_warning: null,
      autostart_actual: false,
      autostart_error: null,
      data_backups: [],
      local_key_storage: null,
    });
    bridgeMocks.getLocalDataStatus.mockResolvedValue({
      unreadable_credentials: 3,
      unreadable_access_tokens: 1,
      audit_key_missing: true,
    });
    await renderApp();
    const openSettings = async () => {
      await act(async () => {
        button("设置").click();
        await Promise.resolve();
        await Promise.resolve();
      });
    };
    const notice = () =>
      container.querySelector('[data-slot="local-data-notice"]');

    await openSettings();
    expect(notice()?.textContent).toContain("有 3 项本地凭据无法解密");
    expect(notice()?.textContent).not.toContain("密钥");
    await act(async () => {
      button("前往提供商").click();
      await Promise.resolve();
    });
    expect(workspaceHeading().textContent).toBe("API 提供商");

    await openSettings();
    await act(async () => {
      button("本次不再提示").click();
      await Promise.resolve();
    });
    expect(notice()).toBeNull();
    const reads = bridgeMocks.getLocalDataStatus.mock.calls.length;
    // Hiding lasts across pages until the app restarts.
    await act(async () => {
      button("提供商").click();
      await Promise.resolve();
    });
    await openSettings();
    expect(notice()).toBeNull();
    expect(bridgeMocks.getLocalDataStatus).toHaveBeenCalledTimes(reads);
  });

  it("opens the agent tools page from the system nav", async () => {
    await renderApp();

    await act(async () => {
      button("Agent 工具").click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("Agent 工具");
    expect(workspaceHeading().textContent).toBe("Agent 工具");
    expect(container.textContent).toContain("工具接入");
    expect(container.textContent).toContain("Cursor");
    expect(container.textContent).toContain("Claude Code");
    expect(container.textContent).toContain("Codex");
    expect(bridgeMocks.getAgentDebugStatus).toHaveBeenCalled();
  });

  it("protects unsaved desktop preferences during navigation", async () => {
    bridgeMocks.getPreferences.mockResolvedValue({
      values: {
        close_behavior: "hide_to_tray",
        autostart: false,
        core_auto_start: true,
        core_auto_recover: true,
        use_system_proxy: true,
        inference_port: 8317,
        max_concurrent_inspections: 16,
        response_start_timeout_seconds: 0,
        max_request_body_mib: 0,
        locale: "zh-CN",
        theme: "system",
        quota_display_mode: "remaining" as const,
        tray: defaultTrayPreferences(),
        updates: defaultUpdatePreferences(),
      },
      load_warning: null,
      autostart_actual: false,
      autostart_error: null,
      data_backups: [],
      local_key_storage: null,
    });
    await renderApp();
    await act(async () => {
      button("设置").click();
      await Promise.resolve();
    });
    const port = container.querySelector<HTMLInputElement>(
      'input[type="number"]',
    );
    if (!port) throw new Error("missing settings port input");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      setter?.call(port, "9123");
      port.dispatchEvent(new Event("input", { bubbles: true }));
      port.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await act(async () => button("概览").click());
    expect(document.body.textContent).toContain("放弃未保存的修改？");
    expect(workspaceHeading().textContent).toBe("设置");
  });

  it("opens default routing policy without retired routing tabs", async () => {
    await renderApp();
    const serviceCalls = bridgeMocks.listServices.mock.calls.length;
    const requestCalls = bridgeMocks.getUsageSummary.mock.calls.length;

    await act(async () => {
      button("路由").click();
      await Promise.resolve();
    });

    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("路由");
    expect(workspaceHeading().textContent).toBe("路由");
    expect(
      container.querySelector('[data-testid="routing-defaults-panel"]'),
    ).not.toBeNull();
    expect(container.textContent).not.toContain("astrlink/auto");
    expect(container.textContent).not.toContain("通过验收前不可启用");
    expect(container.textContent).not.toContain("训练中 · 不可启用");
    expect(container.textContent).not.toContain("固定路由与别名");
    expect(container.textContent).not.toContain("还没有固定路由");
    expect(
      [...container.querySelectorAll('[role="tab"]')].map(
        (tab) => tab.textContent,
      ),
    ).toEqual(["模型与工具", "恢复与重试", "错误规则", "会话粘性", "转发身份"]);
    expect(container.textContent).not.toContain("mmBERT");
    expect(bridgeMocks.listServices).toHaveBeenCalledTimes(serviceCalls);
    expect(bridgeMocks.getUsageSummary).toHaveBeenCalledTimes(requestCalls);
  });

  it("summarizes usage over the default yearly window", async () => {
    await renderApp();

    const query = bridgeMocks.getUsageSummary.mock.calls[0]?.[0];
    expect(bridgeMocks.getUsageSummary).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.listRequestRecords).not.toHaveBeenCalled();
    expect(query).toMatchObject({ preset: "1y" });
    const from = new Date(query.from as string);
    const to = new Date(query.to as string);
    expect(from.getHours()).toBe(0);
    expect(from.getMinutes()).toBe(0);
    expect(to.getHours()).toBe(0);
    // 365 inclusive local days, ending today.
    expect(Math.round((to.getTime() - from.getTime()) / 86_400_000)).toBe(365);

    const today = new Date();
    expect(to.getDate()).toBe(
      new Date(
        today.getFullYear(),
        today.getMonth(),
        today.getDate() + 1,
      ).getDate(),
    );
  });

  it("revalidates the overview silently on return, on a timer, and when shown again", async () => {
    vi.useFakeTimers();
    await renderApp();
    const base = await bridgeMocks.getUsageSummary.mock.results[0].value;
    const usageSection = () =>
      container.querySelector('[aria-labelledby="usage-heading"]');
    const calls = () => ({
      usage: bridgeMocks.getUsageSummary.mock.calls.length,
      services: bridgeMocks.listServices.mock.calls.length,
      tokens: bridgeMocks.listAccessTokens.mock.calls.length,
    });
    const tick = () =>
      act(async () => {
        await vi.advanceTimersByTimeAsync(60_000);
      });
    let before = calls();
    expect(before.usage).toBe(1);

    // An in-flight background read keeps the panels exactly as they were.
    let resolveSummary: (value: unknown) => void = () => {};
    bridgeMocks.getUsageSummary.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveSummary = resolve;
      }),
    );
    await tick();
    expect(calls()).toEqual({
      usage: before.usage + 1,
      services: before.services + 1,
      tokens: before.tokens + 1,
    });
    expect(usageSection()?.getAttribute("aria-busy")).toBe("false");
    expect(container.querySelector('[data-slot="usage-loading"]')).toBeNull();
    expect(container.textContent).not.toContain("等待刷新");
    await act(async () => {
      resolveSummary({ ...base, totals: { ...base.totals, requests: 987 } });
    });
    expect(usageSection()?.textContent).toContain("987");

    // A failed background read keeps the last good numbers without an error.
    bridgeMocks.getUsageSummary.mockRejectedValueOnce(new Error("offline"));
    await tick();
    expect(usageSection()?.textContent).toContain("987");
    expect(container.textContent).not.toContain("offline");

    // A hidden window skips the timer and catches up once it is shown.
    before = calls();
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    await tick();
    expect(calls()).toEqual(before);
    hidden.mockReturnValue(false);
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(calls().usage).toBe(before.usage + 1);

    // Other pages stop the timer; returning to the overview reads again.
    await act(async () => button("请求").click());
    before = calls();
    await tick();
    expect(calls()).toEqual(before);
    await act(async () => button("概览").click());
    expect(calls()).toEqual({
      usage: before.usage + 1,
      services: before.services + 1,
      tokens: before.tokens + 1,
    });
  });

  it("refreshes services and tokens with the usage from the overview header", async () => {
    await renderApp();
    const usage = bridgeMocks.getUsageSummary.mock.calls.length;
    const services = bridgeMocks.listServices.mock.calls.length;
    const tokens = bridgeMocks.listAccessTokens.mock.calls.length;

    await act(async () => button("刷新").click());

    expect(bridgeMocks.getUsageSummary).toHaveBeenCalledTimes(usage + 1);
    expect(bridgeMocks.listServices).toHaveBeenCalledTimes(services + 1);
    expect(bridgeMocks.listAccessTokens).toHaveBeenCalledTimes(tokens + 1);
  });

  it("navigates to the request records page", async () => {
    await renderApp();

    await act(async () => {
      button("请求").click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("请求");
    expect(workspaceHeading().textContent).toBe("请求记录");
  });

  it("ignores an access-token catalog response from an old Core session", async () => {
    vi.useFakeTimers();
    const secondSession: AppSnapshot = {
      ...readySnapshot,
      pid: 84,
      ready: {
        ...readySnapshot.ready!,
        control_url: "http://127.0.0.1:43118",
      },
    };
    bridgeMocks.getCoreStatus
      .mockResolvedValueOnce(readySnapshot)
      .mockResolvedValue(secondSession);

    let resolveOldList:
      | ((value: {
          items: Array<{
            id: string;
            name: string;
            hint: string;
            created_at: string;
          }>;
          next_cursor: null;
        }) => void)
      | undefined;
    bridgeMocks.listAccessTokens
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveOldList = resolve;
        }),
      )
      .mockResolvedValue({
        items: [
          {
            id: "token_new",
            name: "New session token",
            hint: "astr_…NEW2",
            created_at: "2026-07-24T10:32:00Z",
          },
        ],
        next_cursor: null,
      });

    await renderApp();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
      await Promise.resolve();
    });
    await act(async () => {
      resolveOldList?.({
        items: [
          {
            id: "token_old",
            name: "Old session token",
            hint: "astr_…OLD1",
            created_at: "2026-07-24T10:30:00Z",
          },
        ],
        next_cursor: null,
      });
      await Promise.resolve();
    });
    await act(async () => button("令牌").click());

    expect(container.textContent).toContain("New session token");
    expect(container.textContent).not.toContain("Old session token");
  });

  it("returns to the service list without an unsaved-changes dialog after saving", async () => {
    bridgeMocks.createService.mockResolvedValue({
      service: {
        id: "service_newapi_saved",
        name: "new-api",
        kind: "newapi",
        enabled: true,
        models: ["gpt-5"],
        capabilities: [
          {
            protocol: "openai.responses",
            mode: "delegated",
            streaming: true,
          },
        ],
        http: {
          base_url: "https://saved.example",
          auth: { scheme: "bearer" },
          credential_ref: "local://service/service_newapi_saved",
        },
        created_at: "2026-07-28T09:00:00Z",
        updated_at: "2026-07-28T09:00:00Z",
      },
      etag: `"sha256:${"b".repeat(64)}"`,
    });
    await renderApp();

    await act(async () => button("提供商").click());
    await act(async () => button("添加 API 提供商").click());
    await chooseKindCard("New API");
    await setInput(
      '[data-testid="service-form"] input[type="url"]',
      "https://saved.example",
    );
    await setInput(
      '[data-testid="service-form"] input[type="password"]',
      "secret-key",
    );
    await act(async () => {
      button("保存 API 提供商").click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridgeMocks.createService).toHaveBeenCalledOnce();
    expect(workspaceHeading().textContent).toBe("API 提供商");
    expect(container.textContent).not.toContain("放弃未保存的修改？");
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
  });

  it("uses an in-app dialog before leaving an editor with unsaved changes", async () => {
    await renderApp();

    await act(async () => {
      button("提供商").click();
    });
    await act(async () => {
      button("添加 API 提供商").click();
    });
    await chooseKindCard("Codex 订阅");
    await setInput("#service-name", "Unfinished service");

    const back = container.querySelector<HTMLButtonElement>(
      'button[aria-label="返回 API 提供商列表"]',
    );
    await act(async () => {
      back?.click();
    });

    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
    expect(document.body.textContent).toContain("放弃未保存的修改？");
    expect(workspaceHeading().textContent).toBe("添加 API 提供商");

    await act(async () => {
      button("继续编辑").click();
    });
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(workspaceHeading().textContent).toBe("添加 API 提供商");

    await act(async () => {
      button("路由").click();
    });
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();

    await act(async () => {
      button("放弃修改并离开").click();
    });
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(workspaceHeading().textContent).toBe("路由");
  });
  it("protects default-policy drafts when leaving routing", async () => {
    await renderApp();
    await act(async () => button("路由").click());
    await act(async () => button("恢复与重试").click());
    const label = "最多重试几次";
    const input = [...container.querySelectorAll("label")]
      .find(
        (item) => item.querySelector(":scope > span")?.textContent === label,
      )
      ?.querySelector("input");
    if (!input) throw new Error(`Missing input: ${label}`);
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "4");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => button("概览").click());
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
    expect(workspaceHeading().textContent).toBe("路由");
    await act(async () => button("继续编辑").click());
    expect(input.value).toBe("4");
    await act(async () => button("概览").click());
    await act(async () => button("放弃修改并离开").click());
    expect(workspaceHeading().textContent).toBe("运行概览");
  });
});
