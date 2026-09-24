// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  cancelPrivacyModelInstallation: vi.fn(),
  createAccessToken: vi.fn(),
  createService: vi.fn(),
  createRoute: vi.fn(),
  deleteAccessToken: vi.fn(),
  deleteService: vi.fn(),
  deleteRoute: vi.fn(),
  deletePrivacyModelInstallation: vi.fn(),
  getAgentDebugStatus: vi.fn(),
  getAuditSettings: vi.fn(),
  getCoreStatus: vi.fn(),
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
  listRecoveryPaths: vi.fn(),
  installAgentDebug: vi.fn(),
  uninstallAgentDebug: vi.fn(),
  getService: vi.fn(),
  getServiceAuthorization: vi.fn(),
  getServiceUsage: vi.fn(),
  resetServiceUsage: vi.fn(),
  getRoute: vi.fn(),
  getPrivacyModelCatalog: vi.fn(),
  getPrivacyModelInstallation: vi.fn(),
  getPrivacyPolicy: vi.fn(),
  getRequestAuditContent: vi.fn(),
  installPrivacyModel: vi.fn(),
  listAccessTokens: vi.fn(),
  listAccessTokenUsage: vi.fn().mockResolvedValue({ items: [] }),
  listServices: vi.fn(),
  listRoutes: vi.fn(),
  listPrivacyModelInstallations: vi.fn(),
  listPrivacyPolicies: vi.fn(),
  listRequestRecords: vi.fn(),
  getUsageSummary: vi.fn(),
  listRequestSessions: vi.fn(),
  getRequestSession: vi.fn(),
  probePrivacyModel: vi.fn(),
  purgeRequestRecords: vi.fn(),
  revealAccessToken: vi.fn(),
  restartCore: vi.fn(),
  startCore: vi.fn(),
  stopCore: vi.fn(),
  updatePreferences: vi.fn(),
  updateAuditSettings: vi.fn(),
  updateService: vi.fn(),
  updateRoute: vi.fn(),
  updatePrivacyPolicy: vi.fn(),
  deleteRequestRecord: vi.fn(),
  getRequestRecord: vi.fn(),
  beginServiceAuthorization: vi.fn(),
  cancelServiceAuthorization: vi.fn(),
  logoutService: vi.fn(),
  openAuthorizationURL: vi.fn(),
  probeDraftServiceModels: vi.fn(),
  probeServiceModels: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import App from "./App";
import type { AppSnapshot } from "./core-model";
import { defaultTrayPreferences } from "./preferences-model";
import { defaultFailurePolicy } from "./failure-policy-model";
import { defaultPrivacyKindRules } from "./privacy-policy-model";

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

async function chooseOption(label: string, option: string): Promise<void> {
  const trigger = document.querySelector<HTMLButtonElement>(
    `button[role="combobox"][aria-label="${label}"]`,
  );
  if (!trigger) throw new Error(`Missing select trigger: ${label}`);
  await act(async () => {
    trigger.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    await Promise.resolve();
  });
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => {
    const label = candidate.cloneNode(true) as HTMLElement;
    label
      .querySelectorAll('[aria-hidden="true"]')
      .forEach((icon) => icon.remove());
    return label.textContent?.trim() === option;
  });
  if (!item) throw new Error(`Missing select option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

describe("App workspace navigation", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    bridgeMocks.getRoutingSettings.mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
    });
    bridgeMocks.listRecoveryPaths.mockResolvedValue([]);
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
    bridgeMocks.listRoutes.mockResolvedValue({
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
    });
    bridgeMocks.getAgentDebugStatus.mockResolvedValue({
      canonical_skill: false,
      mcp_binary: false,
      mcp_command: null,
      tools: [
        {
          id: "cursor",
          detected: true,
          skill_installed: false,
          mcp_installed: false,
          preview_paths: [],
        },
        {
          id: "claude",
          detected: false,
          skill_installed: false,
          mcp_installed: false,
          preview_paths: [],
        },
        {
          id: "codex",
          detected: true,
          skill_installed: true,
          mcp_installed: true,
          preview_paths: [],
        },
      ],
      shared_paths: [],
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
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

  it("keeps loaded page content visible while revisits revalidate slowly", async () => {
    await renderApp();
    const pages = [
      {
        nav: "API 提供商",
        read: bridgeMocks.getServiceOrder,
        content: "Primary gateway",
      },
      {
        nav: "路由",
        read: bridgeMocks.getRoutingSettings,
        content: "Codex 自动审查",
      },
      {
        nav: "安全策略",
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
    await act(async () => button("请求记录").click());
    const before = container.querySelector(
      '[data-slot="workspace"]',
    )?.textContent;
    await act(async () => button("概览").click());
    const calls = bridgeMocks.listRequestSessions.mock.calls.length;
    await act(async () => vi.advanceTimersByTimeAsync(5_000));
    expect(bridgeMocks.listRequestSessions).toHaveBeenCalledTimes(calls);
    bridgeMocks.listRequestSessions.mockReturnValueOnce(new Promise(() => {}));
    await act(async () => button("请求记录").click());
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
      },
      load_warning: null,
      autostart_actual: false,
      autostart_error: null,
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
    await act(async () => button("API 提供商").click());
    expect(container.textContent).toContain("Primary gateway");
    await act(async () => button("概览").click());
    bridgeMocks.getCoreStatus.mockResolvedValue({ ...readySnapshot, pid: 84 });
    await act(async () => vi.advanceTimersByTimeAsync(1_500));
    bridgeMocks.getServiceOrder.mockReturnValueOnce(new Promise(() => {}));
    await act(async () => button("API 提供商").click());
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
    expect(workspaceHeading().textContent).toBe("概览");
    expect(container.textContent).toContain("API 地址");
    expect(container.textContent).toContain("用量概览");
    expect(
      container.querySelector("[data-slot='activity-heatmap']"),
    ).not.toBeNull();
    expect(container.textContent).toContain("按 API 提供商");
    expect(container.textContent).toContain("按模型");
    expect(container.textContent).toContain("Primary gateway");

    await act(async () => {
      button("访问令牌").click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("访问令牌");
    expect(workspaceHeading().textContent).toBe("管理访问令牌");
    expect(container.textContent).toContain("VS Code");

    await act(async () => {
      button("安全策略").click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("安全策略");
    expect(workspaceHeading().textContent).toBe("隐私保护");
    expect(container.textContent).toContain("启用隐私保护");
    expect(container.textContent).toContain("Regex 覆盖邮箱");

    await act(async () => {
      button("API 提供商").click();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("API 提供商");
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
    expect(workspaceHeading().textContent).toBe("添加 API 提供商");
    expect(
      container.querySelector('[data-testid="service-form"]'),
    ).not.toBeNull();
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
      button("API 提供商").click();
      await Promise.resolve();
    });

    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("API 提供商");
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
      },
      load_warning: null,
      autostart_actual: false,
      autostart_error: null,
    });
    await renderApp();

    expect(button("路由").disabled).toBe(false);
    expect(button("安全策略").disabled).toBe(false);
    expect(button("请求记录").disabled).toBe(false);
    await act(async () => {
      button("设置").click();
      await Promise.resolve();
    });
    expect(workspaceHeading().textContent).toBe("设置");
    expect(container.textContent).toContain("推理入口");
    expect(container.textContent).toContain("检查并发");
    expect(container.textContent).toContain("响应头等待");
    expect(container.textContent).not.toContain("工具接入");
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
      },
      load_warning: null,
      autostart_actual: false,
      autostart_error: null,
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
    ).toEqual(["模型重定向", "恢复与重试", "错误规则", "会话粘性", "转发身份"]);
    expect(container.textContent).not.toContain("mmBERT");
    expect(bridgeMocks.listRoutes).not.toHaveBeenCalled();
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

  it("navigates to the request records page", async () => {
    await renderApp();

    await act(async () => {
      button("请求记录").click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("请求记录");
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
    await act(async () => button("访问令牌").click());

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

    await act(async () => button("API 提供商").click());
    await act(async () => button("添加 API 提供商").click());
    await chooseOption("API 提供商类型", "New API");
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
      button("API 提供商").click();
    });
    await act(async () => {
      button("添加 API 提供商").click();
    });
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
    expect(workspaceHeading().textContent).toBe("概览");
  });
});
