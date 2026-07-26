// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  cancelPrivacyModelInstallation: vi.fn(),
  createAccessToken: vi.fn(),
  createEndpoint: vi.fn(),
  deleteAccessToken: vi.fn(),
  deleteEndpoint: vi.fn(),
  deletePrivacyModelInstallation: vi.fn(),
  getAuditSettings: vi.fn(),
  getCoreStatus: vi.fn(),
  getEndpoint: vi.fn(),
  getPrivacyModelCatalog: vi.fn(),
  getPrivacyModelInstallation: vi.fn(),
  getPrivacyPolicy: vi.fn(),
  getRequestAuditContent: vi.fn(),
  installPrivacyModel: vi.fn(),
  listAccessTokens: vi.fn(),
  listEndpoints: vi.fn(),
  listPrivacyModelInstallations: vi.fn(),
  listPrivacyPolicies: vi.fn(),
  listRequestRecords: vi.fn(),
  probePrivacyModel: vi.fn(),
  purgeRequestRecords: vi.fn(),
  revealAccessToken: vi.fn(),
  restartCore: vi.fn(),
  updateAuditSettings: vi.fn(),
  updateEndpoint: vi.fn(),
  updatePrivacyPolicy: vi.fn(),
  deleteRequestRecord: vi.fn(),
  getRequestRecord: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import App from "./App";
import type { AppSnapshot } from "./core-model";

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
};

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
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
    bridgeMocks.getCoreStatus.mockResolvedValue(readySnapshot);
    bridgeMocks.listEndpoints.mockResolvedValue({
      items: [
        {
          id: "endpoint_01",
          name: "Primary gateway",
          kind: "newapi",
          base_url: "https://gateway.example",
          auth: { scheme: "bearer" },
          credential_ref: "local://endpoint/endpoint_01",
          enabled: true,
          capabilities: [
            {
              protocol: "openai.responses",
              mode: "delegated",
              streaming: true,
            },
          ],
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
          source: "user",
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
        request_action: "redact",
        response_action: "allow",
        response_restore: true,
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
    bridgeMocks.getAuditSettings.mockResolvedValue({
      request_body_enabled: false,
      response_content_enabled: false,
      request_body_max_bytes: 4096,
      response_content_max_bytes: 8192,
      metadata_retention_days: 30,
      content_retention_days: 7,
    });
    window.confirm = vi.fn(() => true);
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

  it("switches between overview, token manager, safety, service list, and create pages", async () => {
    await renderApp();

    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("概览");
    expect(container.textContent).toContain("连接 AstrLink");
    expect(container.textContent).toContain("今日用量");

    await act(async () => {
      button("访问令牌").click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("访问令牌");
    expect(container.textContent).toContain("VS Code");

    await act(async () => {
      button("安全策略").click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("安全策略");
    expect(container.textContent).toContain("全局隐私保护");
    expect(container.textContent).toContain("Regex 覆盖限制");

    await act(async () => {
      button("API 服务").click();
    });
    expect(
      document.querySelector('[aria-current="page"]')?.textContent,
    ).toContain("API 服务");
    expect(container.textContent).toContain("Primary gateway");

    await act(async () => {
      button("添加服务").click();
    });
    expect(container.querySelector(".workspace-header h1")?.textContent).toBe(
      "添加服务",
    );
    expect(container.querySelector("form.endpoint-form")).not.toBeNull();

    const back = container.querySelector<HTMLButtonElement>(
      'button[aria-label="返回服务列表"]',
    );
    expect(back).not.toBeNull();
    await act(async () => {
      back?.click();
    });
    expect(container.querySelector(".workspace-header h1")?.textContent).toBe(
      "API 服务",
    );
  });

  it("keeps future navigation visibly disabled", async () => {
    await renderApp();

    expect(button("路由与模型即将推出").disabled).toBe(true);
    expect(button("安全策略").disabled).toBe(false);
    expect(button("请求记录").disabled).toBe(false);
    expect(button("设置即将推出").disabled).toBe(true);
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
    expect(container.querySelector(".workspace-header h1")?.textContent).toBe(
      "请求记录",
    );
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
            source: "user";
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
            source: "user",
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
            source: "user",
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

  it("confirms before leaving an editor with unsaved changes", async () => {
    await renderApp();

    await act(async () => {
      button("API 服务").click();
    });
    await act(async () => {
      button("添加服务").click();
    });
    await setInput("#endpoint-name", "Unfinished service");

    const confirm = vi.mocked(window.confirm);
    confirm.mockReturnValue(false);
    const back = container.querySelector<HTMLButtonElement>(
      'button[aria-label="返回服务列表"]',
    );
    await act(async () => {
      back?.click();
    });

    expect(confirm).toHaveBeenCalledWith("当前修改尚未保存，确定要离开吗？");
    expect(container.querySelector(".workspace-header h1")?.textContent).toBe(
      "添加服务",
    );

    confirm.mockReturnValue(true);
    await act(async () => {
      back?.click();
    });
    expect(container.querySelector(".workspace-header h1")?.textContent).toBe(
      "API 服务",
    );
  });
});
