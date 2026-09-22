// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  beginServiceAuthorization: vi.fn(),
  cancelServiceAuthorization: vi.fn(),
  completeServiceAuthorization: vi.fn(),
  createService: vi.fn(),
  deleteService: vi.fn(),
  getService: vi.fn(),
  getServiceOrder: vi.fn().mockResolvedValue({ service_ids: [], etag: '"order"' }),
  updateServiceOrder: vi.fn(),
  getRoutingSettings: vi.fn(),
  getServiceAuthorization: vi.fn(),
  getServiceUsage: vi.fn(),
  logoutService: vi.fn(),
  resetServiceUsage: vi.fn(),
  openAuthorizationURL: vi.fn(),
  probeDraftServiceModels: vi.fn(),
  probeServiceModels: vi.fn(),
  testService: vi.fn(),
  updateService: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

import { defaultFailurePolicy } from "./failure-policy-model";
import { ServiceManager } from "./ServiceManager";
import { SERVICE_ORDER_GUIDE_KEY } from "./ServiceOrderHelp";
import { parseService, type Service } from "./service-model";
import { httpServicePreset } from "./service-presets";

const timestamp = "2026-07-28T12:00:00Z";
const etag = `"sha256:${"a".repeat(64)}"`;

const codexService: Service = {
  id: "service_codex_personal",
  name: "Codex personal",
  kind: "codex_subscription",
  enabled: true,
  models: [],
  capabilities: [
    { protocol: "openai.responses", mode: "native", streaming: true },
  ],
  subscription: {
    provider: "openai_codex",
    status: "disconnected",
    authorization_boundary:
      "Logout is local-only: AstrLink removes locally stored credentials; remote revocation is unavailable.",
  },
  created_at: timestamp,
  updated_at: timestamp,
};

const secondCodexService: Service = {
  ...codexService,
  id: "service_codex_work",
  name: "Codex work",
};

const gatewayService: Service = {
  id: "service_gateway",
  name: "new-api",
  kind: "newapi",
  enabled: true,
  models: ["gpt-5"],
  capabilities: [
    { protocol: "openai.responses", mode: "delegated", streaming: true },
    { protocol: "openai.models", mode: "delegated", streaming: false },
  ],
  http: {
    base_url: "https://gateway.example/v1",
    auth: { scheme: "bearer" },
    credential_ref: "local://service/service_gateway",
  },
  created_at: timestamp,
  updated_at: timestamp,
};

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
  const item = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(
    (candidate) => {
      const label = candidate.cloneNode(true) as HTMLElement;
      // Decorative brand SVGs contain titles that are not part of the option name.
      label.querySelectorAll('[aria-hidden="true"]').forEach((icon) => icon.remove());
      return label.textContent?.trim() === option;
    },
  );
  if (!item) throw new Error(`Missing select option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

async function openServiceOverflow(name: string): Promise<void> {
  const trigger = document.querySelector<HTMLButtonElement>(
    `button[aria-label="更多 ${name} 操作"]`,
  );
  if (!trigger) throw new Error(`Missing overflow menu: ${name}`);
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
}

async function chooseMenuItem(label: string): Promise<void> {
  const item = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!item) throw new Error(`Missing menu item: ${label}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

function previewModelCheckbox(model: string): HTMLButtonElement {
  const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
  if (!dialog) throw new Error("Missing model preview dialog");
  const row = [...dialog.querySelectorAll<HTMLElement>("label")].find(
    (label) => label.querySelector("code")?.textContent === model,
  );
  const checkbox = row?.querySelector<HTMLButtonElement>('[role="checkbox"]');
  if (!checkbox) throw new Error(`Missing preview model: ${model}`);
  return checkbox;
}

async function togglePreviewModel(model: string): Promise<void> {
  const checkbox = previewModelCheckbox(model);
  await act(async () => {
    checkbox.click();
    await Promise.resolve();
  });
}

function applySelectedButton(): HTMLButtonElement {
  const apply = [...document.querySelectorAll("button")].find((button) =>
    button.textContent?.startsWith("应用所选模型"),
  );
  if (!apply) throw new Error("Missing apply button");
  return apply;
}

describe("ServiceManager", () => {
  let root: Root;
  let container: HTMLDivElement;

  beforeEach(() => {
    // Existing editor/action tests represent returning users.
    localStorage.setItem(SERVICE_ORDER_GUIDE_KEY, "seen");
    bridgeMocks.getRoutingSettings.mockResolvedValue({ default_failure_policy: defaultFailurePolicy(), allow_unmatched_failover: false, strategy: "retry_first", max_attempts: 6 });
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("tests the selected saved provider without changing its configuration", async () => {
    let finish!: (value: unknown) => void;
    bridgeMocks.testService.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    await act(async () => root.render(
      <ServiceManager catalogError={null} catalogStatus="ready" isReady
        services={[gatewayService]} protocols={[]} view={{ kind: "list" }}
        onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}}
        onServiceSaved={() => {}} onViewChange={() => {}} />,
    ));
    await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="测试 new-api"]')!.click());
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(dialog.textContent).toContain("额度");
    expect(dialog.querySelector<HTMLInputElement>('input[aria-label="测试模型"]')!.value).toBe("gpt-5");
    const start = [...dialog.querySelectorAll("button")].find(button => button.textContent === "开始测试")!;
    await act(async () => { start.click(); start.click(); });
    expect(bridgeMocks.testService).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.testService).toHaveBeenCalledWith("service_gateway", { protocol: "openai.responses", model: "gpt-5", stream: true });
    expect(start.disabled).toBe(true);
    expect(dialog.querySelector<HTMLInputElement>('input[aria-label="测试模型"]')!.disabled).toBe(true);
    await act(async () => finish({ service_id: "service_gateway", protocol: "openai.responses", model: "gpt-5", stream: true, ok: true, status_code: 200, duration_ms: 124, output: "**OK**" }));
    expect(dialog.textContent).toContain("测试成功");
    expect(dialog.textContent).toContain("0.12 s");
    expect(dialog.textContent).toContain("HTTP 200");
    await act(async () => { await vi.dynamicImportSettled(); });
    expect(dialog.querySelector('[data-slot="markdown-content"] strong')?.textContent).toBe("OK");
    bridgeMocks.testService.mockResolvedValueOnce({ service_id: "service_gateway", protocol: "openai.responses", model: "gpt-5", stream: true, ok: false, status_code: 429, duration_ms: 50, output: "", error_code: "upstream_error", message: "Quota exceeded" });
    await act(async () => start.click());
    expect(dialog.textContent).toContain("测试失败");
    expect(dialog.textContent).toContain("Quota exceeded");
    expect(dialog.querySelector("pre")).toBeNull();
    bridgeMocks.testService.mockRejectedValueOnce(new Error("Core unavailable"));
    await act(async () => start.click());
    expect(dialog.textContent).toContain("Core unavailable");
    expect(start.disabled).toBe(false);
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
  });

  it("requires a connected subscription before testing", async () => {
    await act(async () => root.render(
      <ServiceManager catalogError={null} catalogStatus="ready" isReady
        services={[codexService]} protocols={[]} view={{ kind: "list" }}
        onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}}
        onServiceSaved={() => {}} onViewChange={() => {}} />,
    ));
    await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="测试 Codex personal"]')!.click());
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(dialog.textContent).toContain("请先登录此订阅");
    expect([...dialog.querySelectorAll("button")].find(button => button.textContent === "开始测试")!.disabled).toBe(true);
    expect(dialog.querySelector<HTMLButtonElement>('[role="switch"]')!.disabled).toBe(true);
    expect(bridgeMocks.testService).not.toHaveBeenCalled();
  });

  it("shows channel WebSocket state and saves an explicit Codex opt-out", async () => {
    const saved = { ...codexService, responses_websocket_enabled: false };
    bridgeMocks.getService.mockResolvedValue({ service: codexService, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: saved, etag });
    const onServiceSaved = vi.fn();
    const props = { isReady: true, protocols: [], services: [codexService, gatewayService], catalogStatus: "ready" as const,
      catalogError: null, onRefresh: vi.fn(), onViewChange: vi.fn(), onServiceSaved, onServiceRemoved: vi.fn(), onDirtyChange: vi.fn() };
    await act(async () => root.render(<ServiceManager {...props} view={{ kind: "list" }} />));
    expect(container.querySelectorAll('[role="img"][aria-label="WebSocket 已开启"]')).toHaveLength(1);
    expect(container.querySelector('[role="switch"][aria-label*="WebSocket"]')).toBeNull();
    expect(container.querySelector('[data-testid="service-card"][aria-label="new-api"]')?.textContent).not.toContain("WebSocket");
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
    await act(async () => root.render(<ServiceManager {...props} view={{ kind: "edit", serviceId: codexService.id }} />));
    const toggle = container.querySelector<HTMLButtonElement>('[role="switch"][aria-label="Responses WebSocket"]')!;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await act(async () => toggle.click());
    await act(async () => container.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(codexService.id, etag, expect.objectContaining({ responses_websocket_enabled: false }));
    expect(onServiceSaved).toHaveBeenCalledWith(saved);
  });

  it("waits for saved priority before showing rows on every page entry", async () => {
    for (let entry = 0; entry < 2; entry++) {
      let resolveOrder!: (value: unknown) => void;
      bridgeMocks.getServiceOrder.mockReturnValueOnce(new Promise((resolve) => {
        resolveOrder = resolve;
      }));
      await act(async () => root.render(
        <ServiceManager catalogError={null} catalogStatus="ready" isReady
          services={[codexService, gatewayService]} protocols={[]} view={{ kind: "list" }}
          onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}}
          onServiceSaved={() => {}} onViewChange={() => {}} />,
      ));
      expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(0);
      await act(async () => resolveOrder({
        service_ids: [gatewayService.id, codexService.id], etag: '"saved-order"',
      }));
      expect([...container.querySelectorAll('[data-testid="service-card"]')].map(row => row.getAttribute("aria-label")))
        .toEqual([gatewayService.name, codexService.name]);
      await act(async () => root.render(null));
    }
  });

  it("uses compact supplier rows during dragging and restores details when cancelled", async () => {
    bridgeMocks.getServiceOrder.mockResolvedValueOnce({
      service_ids: [gatewayService.id, codexService.id], etag: '"order"',
    });
    await act(async () => root.render(
      <ServiceManager catalogError={null} catalogStatus="ready" isReady
        services={[gatewayService, codexService]} protocols={[]} view={{ kind: "list" }}
        onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}}
        onServiceSaved={() => {}} onViewChange={() => {}} />,
    ));
    const list = container.querySelector("ol")!;
    list.setPointerCapture = vi.fn();
    list.hasPointerCapture = () => false;
    const handle = list.querySelector("button")!;
    await act(async () => handle.dispatchEvent(new PointerEvent("pointerdown", {
      bubbles: true, button: 0, pointerId: 1, clientY: 20,
    })));
    await act(async () => list.dispatchEvent(new PointerEvent("pointermove", {
      bubbles: true, pointerId: 1, clientY: 30,
    })));
    expect(container.querySelectorAll('[data-testid="service-card"][data-sorting="true"]')).toHaveLength(2);
    expect(container.textContent).toContain("new-api");
    expect(container.textContent).toContain("Codex personal");
    expect(container.textContent).not.toContain(gatewayService.http!.base_url);
    expect(list.querySelector('[role="switch"]')).toBeNull();
    await act(async () => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    expect(container.querySelector('[data-testid="service-card"][data-sorting="true"]')).toBeNull();
    expect(container.textContent).toContain(gatewayService.http!.base_url);
    expect(bridgeMocks.updateServiceOrder).not.toHaveBeenCalled();
  });

  async function openEditorTab(
    tab: "connection" | "models" | "protocols" | "failure",
  ): Promise<void> {
    const trigger = container.querySelector<HTMLButtonElement>(
      `[data-testid="service-editor-tab-${tab}"]`,
    );
    if (!trigger) throw new Error(`missing editor tab: ${tab}`);
    await act(async () => {
      trigger.click();
      await Promise.resolve();
    });
  }

  it("creates a Claude subscription with its own authorization flow", async () => {
    const claude: Service = { ...codexService, id: "service_claude", name: "Claude Code", kind: "claude_subscription",
      subscription: { provider: "claude_code", status: "disconnected" } };
    bridgeMocks.createService.mockResolvedValue({ service: claude, etag });
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({ kind: "session", session: {
      id: "authorization_claude", provider: "claude_code", status: "pending", flow: "authorization_code",
      authorization_url: "https://claude.com/cai/oauth/authorize", service_id: claude.id,
      created_at: timestamp, updated_at: timestamp, expires_at: "2099-09-18T00:00:00Z",
    }});
    await act(async () => root.render(<ServiceManager catalogError={null} catalogStatus="ready" isReady
      onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}} onServiceSaved={() => {}}
      onViewChange={() => {}} protocols={[]} services={[]} view={{ kind: "create" }} />));
    await chooseOption("API 提供商类型", "Claude Code 订阅");
    expect(container.querySelector<HTMLInputElement>("#service-name")?.value).toBe("Claude Code");
    expect(container.querySelector('[role="radio"][aria-label="Device Code"]')).toBeNull();
    await act(async () => container.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(bridgeMocks.createService).toHaveBeenCalledWith({ name: "Claude Code", kind: "claude_subscription", enabled: true, responses_websocket_enabled: false, models: [] });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith("service_claude", "authorization_code");
  });

  it("creates a Grok subscription that only offers Device Code", async () => {
    const grok: Service = { ...codexService, id: "service_grok", name: "Grok 订阅", kind: "grok_subscription",
      capabilities: [{ protocol: "openai.responses", mode: "native", streaming: true }, { protocol: "openai.chat", mode: "native", streaming: true }],
      subscription: { provider: "xai_grok", status: "disconnected" } };
    bridgeMocks.createService.mockResolvedValue({ service: grok, etag });
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({ kind: "session", session: {
      id: "authorization_grok", provider: "xai_grok", status: "pending", flow: "device_code",
      device_code: { verification_url: "https://accounts.x.ai/oauth2/device?user_code=GROK-CODE", user_code: "GROK-CODE" },
      service_id: grok.id, created_at: timestamp, updated_at: timestamp, expires_at: "2099-09-18T00:00:00Z",
    }});
    bridgeMocks.getServiceAuthorization.mockResolvedValue({
      id: "authorization_grok", provider: "xai_grok", status: "pending", flow: "device_code",
      device_code: { verification_url: "https://accounts.x.ai/oauth2/device?user_code=GROK-CODE", user_code: "GROK-CODE" },
      service_id: grok.id, created_at: timestamp, updated_at: timestamp, expires_at: "2099-09-18T00:00:00Z",
    });
    await act(async () => root.render(<ServiceManager catalogError={null} catalogStatus="ready" isReady
      onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}} onServiceSaved={() => {}}
      onViewChange={() => {}} protocols={[]} services={[]} view={{ kind: "create" }} />));
    await chooseOption("API 提供商类型", "Grok 订阅（xAI OAuth）");
    expect(container.querySelector<HTMLInputElement>("#service-name")?.value).toBe("Grok 订阅");
    expect(container.querySelector('[role="radio"][aria-label="Device Code"]')).not.toBeNull();
    expect(container.querySelector('[role="radio"][aria-label="浏览器 OAuth"]')).toBeNull();
    expect(container.textContent).toContain("Device Code 登录");
    await act(async () => container.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(bridgeMocks.createService).toHaveBeenCalledWith({ name: "Grok 订阅", kind: "grok_subscription", enabled: true, responses_websocket_enabled: false, models: [] });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith("service_grok", "device_code");
  });

  it("signs a Grok subscription in with a Device Code dialog that never mentions OpenAI", async () => {
    const grok: Service = { ...codexService, id: "service_grok", name: "Grok 订阅", kind: "grok_subscription",
      subscription: { provider: "xai_grok", status: "disconnected" } };
    const session = {
      id: "authorization_grok", provider: "xai_grok", status: "pending", flow: "device_code",
      device_code: { verification_url: "https://accounts.x.ai/oauth2/device?user_code=GROK-CODE", user_code: "GROK-CODE" },
      service_id: grok.id, created_at: timestamp, updated_at: timestamp, expires_at: "2099-09-18T00:00:00Z",
    };
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({ kind: "session", session });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(session);
    await act(async () => root.render(<ServiceManager catalogError={null} catalogStatus="ready" isReady
      onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}} onServiceSaved={() => {}}
      onViewChange={() => {}} protocols={[]} services={[grok]} view={{ kind: "list" }} />));
    expect(container.textContent).toContain("xAI Grok OAuth");
    await openServiceOverflow("Grok 订阅");
    await chooseMenuItem("登录");
    const choice = document.querySelector('[role="dialog"]');
    expect(choice?.querySelector('[role="radio"][aria-label="Device Code"]')).not.toBeNull();
    expect(choice?.querySelector('[role="radio"][aria-label="浏览器 OAuth"]')).toBeNull();
    expect(choice?.textContent).not.toContain("OpenAI");
    const start = [...document.querySelectorAll("button")].find((button) => button.textContent === "开始登录");
    expect(start?.hasAttribute("disabled")).toBe(false);
    await act(async () => start!.click());
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith("service_grok", "device_code");
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain("GROK-CODE");
    expect(dialog?.textContent).toContain("xAI 登录页面");
    expect(dialog?.textContent).not.toContain("OpenAI");
  });

  it("submits a Claude code only to its active service and clears the input", async () => {
    const claude: Service = { ...codexService, id: "service_claude", name: "Claude Code", kind: "claude_subscription",
      subscription: { provider: "claude_code", status: "disconnected" } };
    const session = { id: "authorization_claude", provider: "claude_code", status: "pending", flow: "authorization_code",
      authorization_url: "https://claude.com/cai/oauth/authorize", service_id: claude.id,
      created_at: timestamp, updated_at: timestamp, expires_at: "2099-09-18T00:00:00Z" };
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({ kind: "session", session });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(session);
    bridgeMocks.completeServiceAuthorization.mockResolvedValue({ ...session, status: "completed", authorization_url: undefined });
    await act(async () => root.render(<ServiceManager catalogError={null} catalogStatus="ready" isReady
      onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}} onServiceSaved={() => {}}
      onViewChange={() => {}} protocols={[]} services={[claude]} view={{ kind: "list" }} />));
    await openServiceOverflow("Claude Code");
    await chooseMenuItem("登录");
    const start = [...document.querySelectorAll("button")].find((button) => button.textContent === "开始登录");
    expect(start).toBeDefined();
    await act(async () => start!.click());
    const input = document.querySelector<HTMLInputElement>("#claude-authorization-code")!;
    expect(input.type).toBe("password");
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "code-secret#state");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => input.closest("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(bridgeMocks.completeServiceAuthorization).toHaveBeenCalledWith("service_claude", "authorization_claude", "code-secret#state");
    expect(document.querySelector("#claude-authorization-code")).toBeNull();
    expect(document.body.textContent).not.toContain("code-secret");
  });

  it("renders multiple Codex subscriptions and a gateway in one service list", async () => {
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[codexService, gatewayService, secondCodexService]}
          view={{ kind: "list" }}
        />,
      );
    });

    expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(3);
    expect(container.textContent).toContain("Codex personal");
    expect(container.textContent).toContain("Codex work");
    expect(container.textContent).toContain("new-api");
    expect(
      container.querySelector('[data-testid="service-card"] [aria-label="New API"]'),
    ).not.toBeNull();
    expect(
      container.querySelectorAll(
        '[data-testid="service-card"] [aria-label="Codex 订阅"]',
      ),
    ).toHaveLength(2);
    expect(container.textContent).not.toContain("Logout is local-only");
    expect(bridgeMocks.getServiceUsage).not.toHaveBeenCalled();
    expect(container.querySelector('[data-testid="subscription-usage"]')).toBeNull();
  });

  it("combines status and search filters and clears both from an empty result", async () => {
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService, { ...codexService, enabled: false }]}
          view={{ kind: "list" }}
        />,
      );
    });
    const disabledFilter = [...container.querySelectorAll<HTMLButtonElement>('button[role="radio"]')]
      .find((button) => button.textContent?.startsWith("已停用"));
    if (!disabledFilter) throw new Error("Missing disabled filter");
    await act(async () => disabledFilter.click());
    expect(disabledFilter.getAttribute("aria-checked")).toBe("true");
    expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(1);
    expect(container.querySelector('[data-testid="service-card"]')?.textContent).toContain("Codex personal");

    const search = container.querySelector<HTMLInputElement>('input[aria-label="搜索 API 提供商"]');
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    if (!search || !setter) throw new Error("Missing service search");
    await act(async () => {
      setter.call(search, "gateway.example");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(0);
    const clear = [...container.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "清除筛选");
    if (!clear) throw new Error("Missing clear filters");
    await act(async () => clear.click());
    expect(search.value).toBe("");
    expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(2);

    await act(async () => {
      setter.call(search, "gateway.example");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(1);
    expect(container.querySelector('[data-testid="service-card"]')?.textContent).toContain("new-api");
  });

  it.each([true, false])("reorders services in the enabled=%s status filter without moving hidden services", async (enabled) => {
    const services = Array.from({ length: 5 }, (_, index): Service => ({
      ...gatewayService,
      id: `service_${index}`,
      name: `Gateway ${index}`,
      enabled: index % 2 === 1 ? enabled : !enabled,
    }));
    bridgeMocks.getServiceOrder.mockResolvedValueOnce({
      service_ids: services.map((service) => service.id), etag,
    });
    bridgeMocks.updateServiceOrder.mockImplementationOnce(async (service_ids) => ({ service_ids, etag }));
    await act(async () => root.render(
      <ServiceManager catalogError={null} catalogStatus="ready" isReady
        services={services} protocols={[]} view={{ kind: "list" }}
        onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}}
        onServiceSaved={() => {}} onViewChange={() => {}} />,
    ));
    const statusFilter = [...container.querySelectorAll<HTMLButtonElement>('button[role="radio"]')]
      .find((button) => button.textContent?.startsWith(enabled ? "已启用" : "已停用"))!;
    await act(async () => statusFilter.click());
    const handle = container.querySelector<HTMLButtonElement>('[data-ordered-item="service_3"] button')!;
    expect(handle.disabled).toBe(false);
    await act(async () => handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true })));
    expect(bridgeMocks.updateServiceOrder).toHaveBeenCalledExactlyOnceWith(
      ["service_0", "service_3", "service_2", "service_1", "service_4"], etag,
    );
    expect([...container.querySelectorAll('[data-testid="service-card"]')].map((row) => row.getAttribute("aria-label")))
      .toEqual(["Gateway 3", "Gateway 1"]);
    const all = [...container.querySelectorAll<HTMLButtonElement>('button[role="radio"]')]
      .find((button) => button.textContent?.startsWith("全部"))!;
    await act(async () => all.click());
    expect([...container.querySelectorAll('[data-testid="service-card"]')].map((row) => row.getAttribute("aria-label")))
      .toEqual(["Gateway 0", "Gateway 3", "Gateway 2", "Gateway 1", "Gateway 4"]);
  });

  it("combines model, status and service search filters, saves their order and clears all filters", async () => {
    const services: Service[] = [
      { ...gatewayService, id: "service_other_model", name: "GPT-5 name only", models: ["claude-sonnet-4-5"] },
      { ...gatewayService, id: "service_first", name: "First gateway" },
      { ...gatewayService, id: "service_disabled", enabled: false },
      { ...gatewayService, id: "service_second", name: "Second gateway", models: ["gpt-5.4", "claude-sonnet-4-5"] },
      { ...codexService, name: "GPT-5 unconfigured" },
    ];
    bridgeMocks.getServiceOrder.mockResolvedValueOnce({
      service_ids: services.map((service) => service.id), etag,
    });
    bridgeMocks.updateServiceOrder.mockImplementationOnce(async (service_ids) => ({ service_ids, etag }));
    await act(async () => root.render(
      <ServiceManager catalogError={null} catalogStatus="ready" isReady
        services={services} protocols={[]} view={{ kind: "list" }}
        onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}}
        onServiceSaved={() => {}} onViewChange={() => {}} />,
    ));
    const modelSearch = container.querySelector<HTMLInputElement>('input[aria-label="按模型名筛选 API 提供商"]')!;
    const serviceSearch = container.querySelector<HTMLInputElement>('input[aria-label="搜索 API 提供商"]')!;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
    await act(async () => {
      setter.call(modelSearch, "  GPT-5  ");
      modelSearch.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect([...container.querySelectorAll("[data-ordered-item]")].map((row) => row.getAttribute("data-ordered-item")))
      .toEqual(["service_first", "service_disabled", "service_second"]);
    const enabled = [...container.querySelectorAll<HTMLButtonElement>('button[role="radio"]')]
      .find((button) => button.textContent?.startsWith("已启用"))!;
    await act(async () => enabled.click());
    await act(async () => {
      setter.call(serviceSearch, "gateway.example");
      serviceSearch.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(2);
    const handle = container.querySelector<HTMLButtonElement>('[data-ordered-item="service_second"] button')!;
    expect(handle.disabled).toBe(false);
    await act(async () => handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true })));
    expect(bridgeMocks.updateServiceOrder).toHaveBeenCalledExactlyOnceWith(
      ["service_other_model", "service_second", "service_disabled", "service_first", codexService.id], etag,
    );
    await act(async () => {
      setter.call(modelSearch, "no-such-model");
      modelSearch.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(0);
    const clear = [...container.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "清除筛选")!;
    await act(async () => clear.click());
    expect(modelSearch.value).toBe("");
    expect(serviceSearch.value).toBe("");
    expect(container.querySelectorAll('[data-testid="service-card"]')).toHaveLength(5);
    expect([...container.querySelectorAll("[data-ordered-item]")].map((row) => row.getAttribute("data-ordered-item")))
      .toEqual(["service_other_model", "service_second", "service_disabled", "service_first", codexService.id]);
  });

  it("shows the provider plan quota on a Kimi coding plan row", async () => {
    const kimi: Service = {
      id: "service_kimi_plan",
      name: "Kimi Coding",
      kind: "kimi_coding",
      enabled: true,
      models: ["kimi-k2-thinking"],
      capabilities: [{ protocol: "anthropic.messages", mode: "native", streaming: true }],
      http: {
        base_url: "https://api.kimi.com/coding",
        auth: { scheme: "anthropic_api_key" },
        credential_ref: "local://service/service_kimi_plan",
      },
      created_at: timestamp,
      updated_at: timestamp,
    };
    bridgeMocks.getServiceUsage.mockResolvedValue({
      service_id: kimi.id,
      fetched_at: "2026-09-22T11:00:00Z",
      limit_reached: false,
      primary: { used_percent: 25, limit_window_seconds: 18_000, reset_at: "2026-09-22T15:00:00Z" },
      secondary: { used_percent: 10, limit_window_seconds: 604_800, reset_at: "2026-09-25T00:00:00Z" },
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[kimi, gatewayService, codexService]}
          view={{ kind: "list" }}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });

    // Only the coding plan row queries usage: the gateway has no quota API and
    // the Codex row is disconnected.
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledWith(kimi.id);
    expect(container.querySelectorAll('[data-testid="subscription-usage"]')).toHaveLength(1);
    const rollingQuota = container.querySelector('[role="progressbar"][aria-label="5 小时"]');
    expect(rollingQuota?.getAttribute("aria-valuenow")).toBe("75");
    const weeklyQuota = container.querySelector('[role="progressbar"][aria-label="7 天"]');
    expect(weeklyQuota?.getAttribute("aria-valuenow")).toBe("90");
    expect(container.querySelector('[data-testid="subscription-plan"]')).toBeNull();
    expect(container.querySelector('[data-testid="subscription-usage-reset"]')).toBeNull();
  });

  it.each([
    ["codex_subscription", "openai_codex", "pro", "Pro 20x"],
    ["claude_subscription", "claude_code", "pro", "Pro"],
    ["grok_subscription", "xai_grok", "supergrok_heavy", "SuperGrok Heavy"],
  ] as const)("shows the %s plan badge", async (kind, provider, planType, label) => {
    const connected: Service = {
      ...codexService,
      kind,
      subscription: { provider, status: "connected" },
    };
    bridgeMocks.getServiceUsage.mockResolvedValue({
      service_id: connected.id,
      fetched_at: timestamp,
      plan_type: planType,
      primary: { used_percent: 25 },
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[connected]}
          view={{ kind: "list" }}
        />,
      );
    });
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledWith(connected.id);
    expect(container.querySelector('[data-testid="subscription-plan"]')?.textContent).toBe(label);
    expect(container.querySelector('[data-testid="subscription-usage-reset"]')).toBeNull();
  });

  it("shows rolling quota and reset on a connected Codex row", async () => {
    const connected: Service = {
      ...codexService,
      enabled: false,
      subscription: {
        provider: "openai_codex",
        status: "connected",
        account_hint: "b03f***80",
      },
    };
    bridgeMocks.getServiceUsage.mockResolvedValue({
      service_id: connected.id,
      fetched_at: "2026-08-30T11:00:00Z",
      plan_type: "plus",
      limit_reached: false,
      primary: {
        used_percent: 34,
        limit_window_seconds: 18_000,
        reset_at: "2026-08-30T13:00:00Z",
      },
      secondary: {
        used_percent: 12,
        limit_window_seconds: 604_800,
        reset_at: "2026-09-05T12:00:00Z",
      },
      additional_rate_limits: [
        {
          limit_name: "GPT-5.3-Codex-Spark",
          primary: { used_percent: 0, limit_window_seconds: 18_000 },
          secondary: { used_percent: 0, limit_window_seconds: 604_800 },
        },
        {
          limit_name: "gpt-reserve",
          primary: { used_percent: 0, limit_window_seconds: 604_800 },
        },
      ],
      rate_limit_reset_credits: { available_count: 2 },
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[connected, gatewayService]}
          view={{ kind: "list" }}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });

    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledTimes(1);
    expect(bridgeMocks.getServiceUsage).toHaveBeenCalledWith(connected.id);
    expect(container.querySelector('[data-testid="subscription-plan"]')?.textContent).toBe(
      "Plus",
    );
    expect(container.textContent).toContain("5 小时");
    const rollingQuota = container.querySelector('[role="progressbar"][aria-label="5 小时"]');
    expect(rollingQuota?.getAttribute("aria-valuenow")).toBe("66");
    expect(rollingQuota?.getAttribute("aria-valuetext")).toBe("剩余 66%");
    expect(container.textContent).toContain("7 天");
    const weeklyQuota = container.querySelector('[role="progressbar"][aria-label="7 天"]');
    expect(weeklyQuota?.getAttribute("aria-valuenow")).toBe("88");
    expect(weeklyQuota?.getAttribute("aria-valuetext")).toBe("剩余 88%");
    expect(container.textContent).toMatch(/重置/);
    expect(container.textContent).toContain("重置 ×2");
    expect(container.textContent).toContain("GPT-5.3-Codex-Spark");
    expect(container.textContent).toContain("gpt-reserve");
    expect(container.querySelectorAll('[data-testid="subscription-usage"]')).toHaveLength(1);
    expect(container.querySelector('[data-tone="ok"]')).not.toBeNull();
    expect(container.querySelectorAll('[role="progressbar"]')).toHaveLength(5);
  });

  it("shows extra limits inline and confirms a manual reset", async () => {
    const connected: Service = {
      ...codexService,
      subscription: {
        provider: "openai_codex",
        status: "connected",
        account_hint: "b03f***80",
      },
    };
    bridgeMocks.getServiceUsage.mockResolvedValue({
      service_id: connected.id,
      fetched_at: "2026-08-30T11:00:00Z",
      plan_type: "pro",
      primary: {
        used_percent: 34,
        limit_window_seconds: 18_000,
        reset_at: "2026-08-30T13:00:00Z",
      },
      additional_rate_limits: [
        {
          limit_name: "GPT-5.3-Codex-Spark",
          primary: { used_percent: 0, limit_window_seconds: 18_000 },
        },
      ],
      rate_limit_reset_credits: { available_count: 1 },
    });
    bridgeMocks.resetServiceUsage.mockResolvedValue({
      service_id: connected.id,
      outcome: "reset",
      windows_reset: 2,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[connected]}
          view={{ kind: "list" }}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });

    const extras = container.querySelector<HTMLElement>(
      '[data-testid="subscription-usage-extras"]',
    );
    if (!extras) throw new Error("missing extra limits");
    expect(extras.textContent).toContain("GPT-5.3-Codex-Spark");
    expect(extras.textContent).toContain("5 小时");
    expect(container.querySelector('[data-testid="subscription-usage-extras"] button')).toBeNull();

    const reset = container.querySelector<HTMLButtonElement>(
      '[data-testid="subscription-usage-reset"]',
    );
    if (!reset) throw new Error("missing reset button");
    await act(async () => {
      reset.click();
    });
    expect(bridgeMocks.resetServiceUsage).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("重置这个账户的额度？");
    expect(document.body.textContent).toContain("将消耗 1 次官方额度重置");
    const confirm = [...document.querySelectorAll<HTMLButtonElement>("button")].find(
      (button) => button.textContent?.trim() === "重置",
    );
    if (!confirm) throw new Error("missing reset confirm");
    await act(async () => {
      confirm.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.resetServiceUsage).toHaveBeenCalledWith(connected.id);
    expect(notifyMocks.success).toHaveBeenCalledWith("额度已重置。");
  });

  it("logs and shows the usage failure instead of swallowing it", async () => {
    const connected: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "connected" },
    };
    const logged = vi.spyOn(console, "error").mockImplementation(() => {});
    bridgeMocks.getServiceUsage.mockRejectedValue(
      new Error(
        `GET /control/v1/services/${connected.id}/usage returned 502 Bad Gateway: {"error":{"code":"subscription_usage_failed","message":"codex usage unavailable: status 403"}}`,
      ),
    );

    try {
      await act(async () => {
        root.render(
          <ServiceManager
            catalogError={null}
            catalogStatus="ready"
            isReady
            onDirtyChange={() => {}}
            onRefresh={() => {}}
            onServiceRemoved={() => {}}
            onServiceSaved={() => {}}
            onViewChange={() => {}}
            protocols={[]}
            services={[connected]}
            view={{ kind: "list" }}
          />,
        );
        await Promise.resolve();
      });
      await act(async () => {
        await Promise.resolve();
      });

      expect(logged).toHaveBeenCalled();
      expect(String(logged.mock.calls[0]?.[0])).toContain(
        "AstrLink failed to load subscription usage",
      );
      expect(container.textContent).toContain("无法读取额度");
      expect(container.textContent).toContain(
        "subscription_usage_failed: codex usage unavailable: status 403",
      );
    } finally {
      logged.mockRestore();
    }
  });

  it.each([
    ["deepseek", "DeepSeek API"],
    ["qwen", "通义千问（百炼） API"],
    ["moonshot", "Kimi（Moonshot） API"],
    ["glm", "智谱 GLM API"],
    ["minimax", "MiniMax API"],
    ["doubao", "豆包（火山方舟） API"],
    ["xai", "xAI（Grok） API"],
    ["gemini", "Gemini API"],
  ] as const)("creates %s with API credentials and preserves its kind on reload", async (kind, label) => {
    const preset = httpServicePreset(kind);
    const service = parseService({
      ...gatewayService, name: preset.defaultName, kind, models: [],
      http: { base_url: preset.baseURL, auth: { scheme: preset.authScheme }, credential_ref: "local://service/service_gateway" },
      capabilities: preset.capabilities,
    });
    bridgeMocks.createService.mockResolvedValue({ service, etag });
    await act(async () => root.render(
      <ServiceManager catalogError={null} catalogStatus="ready" isReady
        onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}}
        onServiceSaved={() => {}} onViewChange={() => {}} protocols={[]} services={[]}
        view={{ kind: "create" }} />,
    ));
    await chooseOption("API 提供商类型", label);
    expect(container.querySelector<HTMLInputElement>("#service-name")?.value).toBe(preset.defaultName);
    await openEditorTab("protocols");
    const protocolRows = [...container.querySelectorAll('[data-testid="service-capability-row"]')];
    const entryRow = protocolRows.find((row) => row.textContent?.includes(kind === "gemini" ? "OpenAI Chat Completions" : "Anthropic Messages"));
    expect(entryRow?.querySelector('[role="checkbox"]')?.getAttribute("aria-checked")).toBe("true");
    expect(entryRow?.textContent).toContain(kind === "gemini" ? "Gemini Generate Content" : "原样转发");
    await openEditorTab("connection");
    const secret = container.querySelector<HTMLInputElement>('input[type="password"]');
    if (!secret) throw new Error("missing API key input");
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(secret, "test-api-key");
      secret.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const submit = container.querySelector<HTMLButtonElement>('[data-testid="service-submit"]');
    expect(submit?.disabled).toBe(false);
    await act(async () => {
      container.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(bridgeMocks.createService).toHaveBeenCalledWith(expect.objectContaining({
      kind, name: preset.defaultName, models: [], capabilities: preset.capabilities,
      http: { base_url: preset.baseURL, auth: { scheme: preset.authScheme }, credential: { secret: "test-api-key" } },
    }));
    if (kind === "gemini") {
      expect(bridgeMocks.createService.mock.calls[0]?.[0].capabilities).toContainEqual({
        protocol: "openai.chat", mode: "native", streaming: true, convert_to: "google.generate_content",
      });
    }
    expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="service-editor-tab-models"]')!.click());
    const fetchButton = [...container.querySelectorAll("button")].find((button) => button.textContent === "获取模型列表");
    expect(Boolean(fetchButton)).toBe(!["qwen", "glm", "doubao"].includes(kind));
  });

  it("creates a Codex service through the unified add form and targets its OAuth", async () => {
    bridgeMocks.createService.mockResolvedValue({
      service: codexService,
      etag,
    });
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: {
        id: "authorization_01",
        provider: "openai_codex",
        status: "pending",
        flow: "browser",
        service_id: codexService.id,
        authorization_url: "https://auth.example/oauth/authorize",
        expires_at: "2026-07-28T12:10:00Z",
        created_at: timestamp,
        updated_at: timestamp,
      },
    });
    const saved = vi.fn();
    const changed = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={saved}
          onViewChange={changed}
          protocols={[]}
          services={[]}
          view={{ kind: "create" }}
        />,
      );
    });
    const name = container.querySelector<HTMLInputElement>("#service-name");
    if (!name) throw new Error("missing service name input");
    const submitButton = container.querySelector<HTMLButtonElement>(
      '[data-testid="service-submit"]',
    );
    expect(submitButton?.disabled).toBe(true);
    const browserFlow = container.querySelector<HTMLButtonElement>(
      '[role="radio"][aria-label="浏览器 OAuth"]',
    );
    if (!browserFlow) throw new Error("missing browser login choice");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    await act(async () => {
      valueSetter.call(name, "Personal Codex");
      name.dispatchEvent(new Event("input", { bubbles: true }));
      browserFlow.click();
    });
    expect(submitButton?.disabled).toBe(false);
    const form = container.querySelector<HTMLFormElement>("form");
    if (!form) throw new Error("missing service form");
    await act(async () => {
      form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      await Promise.resolve();
    });

    expect(bridgeMocks.createService).toHaveBeenCalledWith({
      name: "Personal Codex",
      kind: "codex_subscription",
      enabled: true,
      responses_websocket_enabled: true,
      models: [],
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
      "browser",
    );
    expect(saved).toHaveBeenCalledWith(codexService);
    expect(changed).toHaveBeenCalledWith({ kind: "list" });
  });

  it("keeps the selected service kind when Core protocol capabilities refresh", async () => {
    const view = { kind: "create" } as const;
    const onDirtyChange = vi.fn();
    const onRefresh = vi.fn();
    const onServiceRemoved = vi.fn();
    const onServiceSaved = vi.fn();
    const onViewChange = vi.fn();
    const render = (
      protocols: Array<{
        id: string;
        phase: "alpha" | "post_alpha";
        primary: boolean;
        streaming: boolean;
      }>,
    ) =>
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={onDirtyChange}
          onRefresh={onRefresh}
          onServiceRemoved={onServiceRemoved}
          onServiceSaved={onServiceSaved}
          onViewChange={onViewChange}
          protocols={protocols}
          services={[]}
          view={view}
        />,
      );

    await act(async () => render([]));
    await chooseOption("API 提供商类型", "Anthropic API");
    const kind = container.querySelector<HTMLButtonElement>('[aria-label="API 提供商类型"]');
    expect(kind?.textContent).toContain("Anthropic API");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("Anthropic API");

    await act(async () =>
      render([
        {
          id: "anthropic.messages",
          phase: "alpha",
          primary: false,
          streaming: true,
        },
      ]),
    );

    expect(kind?.textContent).toContain("Anthropic API");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("Anthropic API");
  });

  it("previews upstream models and reuses the saved credential for an edited service", async () => {
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: gatewayService.id,
      protocol: "openai.models",
      model_ids: ["gpt-4.1", "gpt-5"],
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    const fetchModels = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      fetchModels?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledWith({
      service_id: gatewayService.id,
      kind: "newapi",
      http: {
        base_url: gatewayService.http?.base_url,
        auth: { scheme: "bearer" },
      },
      protocol: "openai.models",
    });
    expect(document.body.textContent).toContain("选择 API 提供商支持的模型");
    // Only the already allow-listed model opens checked; the discovery is opt-in.
    expect(previewModelCheckbox("gpt-5").getAttribute("aria-checked")).toBe("true");
    expect(previewModelCheckbox("gpt-4.1").getAttribute("aria-checked")).toBe(
      "false",
    );
    expect(applySelectedButton().textContent).toBe("应用所选模型（1）");

    await togglePreviewModel("gpt-4.1");
    const apply = applySelectedButton();
    expect(apply.textContent).toBe("应用所选模型（2）");
    await act(async () => apply.click());
    expect(container.textContent).toContain("2 个模型");
  });

  it("preselects every model when the service has no allowlist yet", async () => {
    const emptyGateway: Service = { ...gatewayService, models: [] };
    bridgeMocks.getService.mockResolvedValue({ service: emptyGateway, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: emptyGateway.id,
      protocol: "openai.models",
      model_ids: ["gpt-4.1", "gpt-5"],
    });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[emptyGateway]}
          view={{ kind: "edit", serviceId: emptyGateway.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    const fetchModels = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      fetchModels?.click();
      await Promise.resolve();
    });

    expect(previewModelCheckbox("gpt-4.1").getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(previewModelCheckbox("gpt-5").getAttribute("aria-checked")).toBe("true");
    const apply = applySelectedButton();
    expect(apply.textContent).toBe("应用所选模型（2）");
    await act(async () => apply.click());
    expect(container.textContent).toContain("2 个模型");
  });

  it("merges New API discovery results and keeps successful results after a partial failure", async () => {
    const multiProtocolGateway: Service = {
      ...gatewayService,
      models: ["current-model"],
      capabilities: [
        ...gatewayService.capabilities,
        { protocol: "google.models", mode: "delegated", streaming: false },
      ],
    };
    bridgeMocks.getService.mockResolvedValue({
      service: multiProtocolGateway,
      etag,
    });
    bridgeMocks.probeDraftServiceModels.mockImplementation(
      ({ protocol }: { protocol: string }) =>
        protocol === "openai.models"
          ? Promise.resolve({
              service_id: gatewayService.id,
              protocol,
              model_ids: ["openai-model"],
            })
          : Promise.reject(new Error("Gemini 上游暂不可用")),
    );

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[multiProtocolGateway]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    const fetchModels = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      fetchModels?.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.probeDraftServiceModels).toHaveBeenCalledTimes(2);
    expect(document.body.textContent).toContain("部分协议获取失败");
    expect(document.body.textContent).toContain("current-model");
    expect(document.body.textContent).toContain("openai-model");
    expect(
      previewModelCheckbox("current-model").getAttribute("aria-checked"),
    ).toBe("true");
    expect(previewModelCheckbox("openai-model").getAttribute("aria-checked")).toBe(
      "false",
    );
    const apply = applySelectedButton();
    await act(async () => apply.click());
    expect(container.textContent).toContain("1 个模型");
  });

  it("groups allow-listed models and supports search plus clear", async () => {
    const listed: Service = {
      ...gatewayService,
      models: [
        "claude-opus-4-7",
        "claude-sonnet-4-5",
        "gpt-5",
        "gpt-5-mini",
      ],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    expect(container.textContent).toContain("claude-opus");
    expect(container.textContent).toContain("claude-sonnet");
    expect(container.textContent).toContain("4 个模型");
    expect(container.textContent).toContain("1 类 · 3 组 · 4 个模型");
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(4);
    expect(container.textContent).toContain("claude-opus-4-7");

    const search = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索已配置模型"]',
    );
    if (!search) throw new Error("missing model search");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    await act(async () => {
      valueSetter.call(search, "sonnet");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.textContent).toContain("匹配 1 / 4");
    expect(container.textContent).toContain("claude-sonnet-4-5");
    expect(container.textContent).not.toContain("claude-opus-4-7");
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(1);

    const clear = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "清空",
    );
    await act(async () => clear?.click());
    expect(document.body.textContent).toContain("清空支持模型？");
    const confirm = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "确认删除",
    );
    await act(async () => confirm?.click());
    expect(container.textContent).toContain("还没有模型 · API 提供商不会参与路由");
    expect(container.textContent).toContain("0 个模型");
  });

  it("deletes a model from the allowlist instead of remembering it as disabled", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["gpt-5", "gpt-5-mini"],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.updateService.mockResolvedValue({
      service: listed,
      etag: `"sha256:${"b".repeat(64)}"`,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    expect(container.textContent).toContain("2 个模型");
    const chips = [
      ...container.querySelectorAll('[data-testid="service-model-row"]'),
    ];
    expect(chips).toHaveLength(2);

    const remove = container.querySelector<HTMLButtonElement>(
      'button[aria-label="删除 gpt-5-mini"]',
    );
    await act(async () => remove?.click());
    expect(container.textContent).toContain("1 个模型");
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(1);

    const form = container.querySelector("form");
    await act(async () => {
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      listed.id,
      etag,
      expect.objectContaining({
        models: ["gpt-5"],
      }),
    );
    expect(bridgeMocks.updateService.mock.calls[0]?.[2]).not.toHaveProperty(
      "disabled_models",
    );
  });

  it("adds a model by hand to the allowlist", async () => {
    const listed: Service = {
      ...gatewayService,
      models: [],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.updateService.mockResolvedValue({
      service: listed,
      etag: `"sha256:${"b".repeat(64)}"`,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    expect(container.textContent).toContain("还没有模型 · API 提供商不会参与路由");

    const open = container.querySelector<HTMLButtonElement>(
      'button[aria-label="添加模型"]',
    );
    await act(async () => open?.click());
    const input = container.querySelector<HTMLInputElement>(
      'input[aria-label="待添加模型 ID"]',
    );
    if (!input) throw new Error("missing model input");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    await act(async () => {
      valueSetter.call(input, "gpt-4o");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const add = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "添加",
    );
    await act(async () => add?.click());

    expect(container.textContent).toContain("1 个模型");

    const form = container.querySelector("form");
    await act(async () => {
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      listed.id,
      etag,
      expect.objectContaining({ models: ["gpt-4o"] }),
    );
    expect(bridgeMocks.updateService.mock.calls[0]?.[2]).not.toHaveProperty(
      "disabled_models",
    );
  });

  it("lets a probe re-select a model that is not on the current allowlist", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["gpt-5"],
      capabilities: [
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.probeDraftServiceModels.mockResolvedValue({
      service_id: listed.id,
      protocol: "openai.models",
      model_ids: ["gpt-4o", "gpt-5", "gpt-5-codex"],
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    const discover = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      discover?.click();
      await Promise.resolve();
    });

    expect(document.body.textContent).toContain("选择 API 提供商支持的模型");
    expect(document.body.textContent).toContain("gpt-4o");
    expect(document.body.textContent).not.toContain("此前已停用");
    expect(previewModelCheckbox("gpt-4o").getAttribute("aria-checked")).toBe(
      "false",
    );

    const selectAll = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "全选",
    );
    await act(async () => {
      selectAll?.click();
      await Promise.resolve();
    });
    const apply = applySelectedButton();
    expect(apply.textContent).toBe("应用所选模型（3）");
    await act(async () => apply.click());

    expect(container.textContent).toContain("3 个模型");
    expect(
      container.querySelector('[data-testid="service-models-filter-disabled"]'),
    ).toBeNull();
  });

  it("collapses large allow-lists until a group or search expands them", async () => {
    const models = Array.from({ length: 12 }, (_, index) => {
      const family = index < 6 ? "claude-opus" : "claude-sonnet";
      return `${family}-4-${index}`;
    });
    const listed: Service = { ...gatewayService, models };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");

    expect(container.textContent).toContain("claude");
    expect(container.textContent).toContain("12 个模型");
    expect(container.textContent).toContain("1 类 · 2 组 · 12 个模型");
    expect(container.textContent).toContain("展开全部");
    expect(container.textContent).not.toContain("claude-opus");
    expect(container.textContent).not.toContain("claude-opus-4-0");
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(0);

    const expandClaude = [...container.querySelectorAll("button")].find(
      (button) =>
        button.dataset.testid === "service-model-category-toggle" &&
        button.textContent?.includes("claude"),
    );
    await act(async () => expandClaude?.click());
    expect(container.textContent).toContain("claude-opus");
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(0);

    const expandOpus = [...container.querySelectorAll("button")].find(
      (button) =>
        button.dataset.testid === "service-model-group-toggle" &&
        button.textContent?.includes("claude-opus"),
    );
    await act(async () => expandOpus?.click());
    expect(container.textContent).toContain("claude-opus-4-0");
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(6);
    expect(container.textContent).not.toContain("claude-sonnet-4-6");
    expect(container.textContent).toContain("折叠全部");

    const foldAll = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "折叠全部",
    );
    await act(async () => foldAll?.click());
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(0);
    expect(container.textContent).toContain("展开全部");

    const search = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索已配置模型"]',
    );
    if (!search) throw new Error("missing model search");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing input value setter");
    await act(async () => {
      valueSetter.call(search, "sonnet-4-6");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.textContent).toContain("claude-sonnet-4-6");
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(1);
  });

  it("keeps a saved API key when connection settings change without explicit removal", async () => {
    const updatedService: Service = {
      ...gatewayService,
      http: {
        ...gatewayService.http!,
        auth: { scheme: "none" },
      },
    };
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    bridgeMocks.updateService.mockResolvedValue({
      service: updatedService,
      etag: `"sha256:${"b".repeat(64)}"`,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("connection");
    await chooseOption("认证方式", "无需认证");
    const form = container.querySelector<HTMLFormElement>("form");
    await act(async () => {
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
      {
        name: gatewayService.name,
        enabled: true,
        responses_websocket_enabled: false,
        failure_policy: null,
        models: ["gpt-5"],
        http: {
          base_url: gatewayService.http?.base_url,
          auth: { scheme: "none" },
        },
        capabilities: gatewayService.capabilities.map((capability) => ({
          ...capability,
          mode: "native" as const,
        })),
      },
    );
  });

  it("keeps a newly created Codex service when login cannot start", async () => {
    bridgeMocks.createService.mockResolvedValue({
      service: codexService,
      etag,
    });
    bridgeMocks.beginServiceAuthorization.mockRejectedValue(
      new Error("Device Code login is unavailable"),
    );
    const saved = vi.fn();
    const changed = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={saved}
          onViewChange={changed}
          protocols={[]}
          services={[]}
          view={{ kind: "create" }}
        />,
      );
    });
    const deviceFlow = container.querySelector<HTMLButtonElement>(
      '[role="radiogroup"][aria-label="新 API 提供商登录方式"] [role="radio"][aria-label="Device Code"]',
    );
    await act(async () => deviceFlow?.click());
    const form = container.querySelector<HTMLFormElement>("form");
    await act(async () => {
      form?.dispatchEvent(
        new Event("submit", { bubbles: true, cancelable: true }),
      );
      await Promise.resolve();
    });

    expect(bridgeMocks.createService).toHaveBeenCalled();
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
      "device_code",
    );
    expect(saved).toHaveBeenCalledWith(codexService);
    expect(changed).toHaveBeenCalledWith({ kind: "list" });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={saved}
          onViewChange={changed}
          protocols={[]}
          services={[codexService]}
          view={{ kind: "list" }}
        />,
      );
    });
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "订阅 API 提供商已添加，可稍后从 API 提供商列表重新登录。",
    );
  });

  it("asks for a login method and shows Device Code after browser fallback", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const deviceSession = {
      id: "authorization_device",
      provider: "openai_codex",
      status: "pending",
      flow: "device_code",
      service_id: codexService.id,
      device_code: {
        verification_url: "https://auth.openai.com/codex/device",
        user_code: "ABCD-EFGH",
      },
      expires_at: "2026-07-28T12:15:00Z",
      created_at: timestamp,
      updated_at: timestamp,
    };
    bridgeMocks.beginServiceAuthorization.mockResolvedValue({
      kind: "session",
      session: deviceSession,
    });
    bridgeMocks.getServiceAuthorization.mockResolvedValue(deviceSession);

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[codexService]}
          view={{ kind: "list" }}
        />,
      );
    });
    await openServiceOverflow(codexService.name);
    await chooseMenuItem("登录");
    expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();
    const methodInputs = document.querySelectorAll<HTMLButtonElement>(
      '[role="radiogroup"][aria-label="登录方式"] [role="radio"]',
    );
    expect(methodInputs).toHaveLength(2);
    expect([...methodInputs].every((input) => input.getAttribute("aria-checked") === "false")).toBe(true);

    await act(async () => methodInputs[0]?.click());
    const start = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    );
    await act(async () => {
      start?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
      "browser",
    );
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "回调端口 1455 和 1457 均不可用，已切换为 Device Code 登录。",
    );
    expect(document.body.textContent).toContain("ABCD-EFGH");

    const copy = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "复制验证码",
    );
    await act(async () => {
      copy?.click();
      await Promise.resolve();
    });
    expect(writeText).toHaveBeenCalledWith("ABCD-EFGH");

    const reopen = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "重新打开登录页面",
    );
    await act(async () => {
      reopen?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.openAuthorizationURL).toHaveBeenCalledWith(
      "https://auth.openai.com/codex/device",
    );

    const cancel = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "取消登录",
    );
    await act(async () => {
      cancel?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.cancelServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
    );
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    await openServiceOverflow(codexService.name);
    await chooseMenuItem("登录");
    const explicitDevice = document.querySelector<HTMLButtonElement>(
      '[role="radiogroup"][aria-label="登录方式"] [role="radio"][aria-label="Device Code"]',
    );
    await act(async () => explicitDevice?.click());
    const restart = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "开始登录",
    );
    await act(async () => {
      restart?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.beginServiceAuthorization).toHaveBeenLastCalledWith(
      codexService.id,
      "device_code",
    );
    expect(document.body.textContent).not.toContain("1455 和 1457 均不可用");
  });

  it("toggles a service from the list without opening the editor", async () => {
    const disabledService: Service = { ...gatewayService, enabled: false };
    bridgeMocks.getService.mockResolvedValue({
      service: gatewayService,
      etag,
    });
    bridgeMocks.updateService.mockResolvedValue({
      service: disabledService,
      etag: `"sha256:${"b".repeat(64)}"`,
    });
    const saved = vi.fn();
    const changed = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={saved}
          onViewChange={changed}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "list" }}
        />,
      );
    });
    const toggle = container.querySelector<HTMLButtonElement>(
      `[role="switch"][aria-label="启用 ${gatewayService.name}"]`,
    );
    expect(toggle?.getAttribute("aria-checked")).toBe("true");

    await act(async () => {
      toggle?.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.getService).toHaveBeenCalledWith(gatewayService.id);
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
      { enabled: false },
    );
    expect(saved).toHaveBeenCalledWith(disabledService);
    expect(changed).not.toHaveBeenCalled();
    expect(notifyMocks.success).toHaveBeenCalledWith("API 提供商已停用。");
  });

  it("reenables a stopped service from the list", async () => {
    const disabledService: Service = { ...gatewayService, enabled: false };
    bridgeMocks.getService.mockResolvedValue({
      service: disabledService,
      etag,
    });
    bridgeMocks.updateService.mockResolvedValue({
      service: gatewayService,
      etag: `"sha256:${"b".repeat(64)}"`,
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[disabledService]}
          view={{ kind: "list" }}
        />,
      );
    });
    const toggle = container.querySelector<HTMLButtonElement>(
      `[role="switch"][aria-label="启用 ${gatewayService.name}"]`,
    );
    expect(toggle?.getAttribute("aria-checked")).toBe("false");
    expect(container.textContent).toContain("已停用");

    await act(async () => {
      toggle?.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
      { enabled: true },
    );
    expect(notifyMocks.success).toHaveBeenCalledWith("API 提供商已启用。");
  });

  it("uses an in-app confirmation before deleting a service", async () => {
    bridgeMocks.getService.mockResolvedValue({
      service: gatewayService,
      etag,
    });
    bridgeMocks.deleteService.mockResolvedValue(undefined);
    const removed = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={removed}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "list" }}
        />,
      );
    });
    await openServiceOverflow(gatewayService.name);
    await chooseMenuItem("删除");
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
    expect(bridgeMocks.deleteService).not.toHaveBeenCalled();

    const confirm = [...document.querySelectorAll("button")].find(
      (button) => button.textContent === "确认",
    );
    await act(async () => {
      confirm?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deleteService).toHaveBeenCalledWith(
      gatewayService.id,
      etag,
    );
    expect(removed).toHaveBeenCalledWith(gatewayService.id);
  });

  it("configures local conversion instead of native versus delegated", async () => {
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[gatewayService]}
          view={{ kind: "edit", serviceId: gatewayService.id }}
        />,
      );
      await Promise.resolve();
    });
    expect(
      [...container.querySelectorAll("summary")].some((item) =>
        item.textContent?.includes("API 能力"),
      ),
    ).toBe(false);
    await openEditorTab("protocols");
    expect(container.textContent).toContain("接受的入口协议与格式转换");
    expect(
      container.querySelectorAll('[data-testid="service-capability-row"]').length,
    ).toBeGreaterThan(0);
    expect(container.textContent).toContain("原样转发");
    expect(container.textContent).toContain("本地格式转换尚未启用");
    expect(container.textContent).not.toContain("由网关路由");
    expect(container.textContent).not.toContain("原生协议");
    expect(container.textContent).not.toContain("上游实际协议");
  });

  it("shows conversion quality on a flattened protocol row when the engine is available", async () => {
    const converting: Service = {
      ...gatewayService,
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
        {
          protocol: "anthropic.messages",
          mode: "native",
          streaming: true,
          convert_to: "openai.chat",
        },
      ],
    };
    bridgeMocks.getService.mockResolvedValue({ service: converting, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          conversionEngine={{
            name: "relaykit",
            version: "v0.1.1",
            available: true,
            edges: [
              {
                from: "anthropic.messages",
                to: "openai.chat",
                quality: "fair",
                streaming: true,
              },
              {
                from: "openai.responses",
                to: "openai.chat",
                quality: "fair",
                streaming: true,
              },
            ],
          }}
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[converting]}
          view={{ kind: "edit", serviceId: converting.id }}
        />,
      );
      await Promise.resolve();
    });

    await openEditorTab("protocols");
    expect(container.textContent).not.toContain("上游实际协议");
    expect(container.textContent).toContain("转换质量一般");
    expect(
      container.querySelector('[data-testid="apply-upstream-protocol"]'),
    ).toBeNull();
  });

  it("keeps models and protocols on separate editor tabs", async () => {
    const listed: Service = {
      ...gatewayService,
      models: ["claude-opus-4-7", "gpt-5"],
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });

    expect(container.querySelector("h1")?.textContent).toBe("编辑 API 提供商");
    expect(container.textContent).toContain("返回 API 提供商列表");
    expect(container.textContent).not.toContain("服务类型不可变");
    expect(
      container.querySelector('[aria-label="API 提供商类型"]')?.textContent,
    ).toContain("New API");
    expect(
      container.querySelector('[aria-label="API 提供商类型"]')?.textContent,
    ).not.toContain("new-api");
    expect(
      container.querySelector('[data-testid="service-editor-tab-connection"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[data-testid="service-editor-tab-panel"]')
        ?.className,
    ).toContain("overflow-y-auto");
    expect(
      [...container.querySelectorAll('[data-testid="service-editor-tab-panel"]')].every(
        (panel) => panel.className.includes("pr-4"),
      ),
    ).toBe(true);
    expect(container.textContent).toContain("API 地址");
    expect(
      container.querySelector('input[aria-label="搜索已配置模型"]'),
    ).toBeNull();
    expect(
      container.querySelector('[data-testid="service-capability-row"]'),
    ).toBeNull();
    expect(container.textContent).not.toContain("接受的入口协议与格式转换");

    expect(
      container.querySelector('[data-testid="service-form"]')?.className,
    ).toContain("overflow-hidden");

    await openEditorTab("models");
    expect(
      container.querySelector('input[aria-label="搜索已配置模型"]'),
    ).not.toBeNull();
    expect(container.textContent).not.toContain("API 地址");
    expect(
      [...container.querySelectorAll('[data-testid="service-editor-tab-panel"]')].every(
        (panel) => panel.className.includes("pr-4"),
      ),
    ).toBe(true);

    await openEditorTab("protocols");
    expect(
      container.querySelectorAll('[data-testid="service-capability-row"]').length,
    ).toBeGreaterThan(0);
    expect(container.textContent).toContain("接受的入口协议与格式转换");
    expect(
      container.querySelector('input[aria-label="搜索已配置模型"]'),
    ).toBeNull();
    expect(container.querySelectorAll('[data-testid="service-model-row"]')).toHaveLength(
      0,
    );
    expect(
      container.querySelector('[data-testid="service-editor-tab-panel"]')
        ?.className,
    ).toContain("overflow-y-auto");
  });

  it("does not offer a protocol tab for Codex subscriptions", async () => {
    bridgeMocks.getService.mockResolvedValue({ service: codexService, etag });
    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[codexService]}
          view={{ kind: "edit", serviceId: codexService.id }}
        />,
      );
      await Promise.resolve();
    });

    expect(
      container.querySelector('[data-testid="service-editor-tab-protocols"]'),
    ).toBeNull();
    expect(container.textContent).not.toContain("入口协议");
    expect(
      container.querySelector('[data-testid="service-editor-tab-connection"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("连接");
    expect(
      container.querySelector('input[aria-label="搜索已配置模型"]'),
    ).toBeNull();

    await openEditorTab("models");
    expect(
      container.querySelector('input[aria-label="搜索已配置模型"]'),
    ).not.toBeNull();
  });

  it("probes saved Codex models through the connected subscription", async () => {
    const listed: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "connected" },
    };
    bridgeMocks.getService.mockResolvedValue({ service: listed, etag });
    bridgeMocks.probeServiceModels.mockResolvedValue({
      service_id: listed.id,
      protocol: "openai.models",
      model_ids: ["gpt-5", "gpt-5-codex"],
    });

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[listed]}
          view={{ kind: "edit", serviceId: listed.id }}
        />,
      );
      await Promise.resolve();
    });
    await openEditorTab("models");
    const fetchModels = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "获取模型列表",
    );
    await act(async () => {
      fetchModels?.click();
      await Promise.resolve();
    });

    expect(bridgeMocks.probeServiceModels).toHaveBeenCalledWith(
      listed.id,
      "openai.models",
    );
    expect(bridgeMocks.probeDraftServiceModels).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("选择 API 提供商支持的模型");
  });

  it("imports Codex models after login completes", async () => {
    const authorizing: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "authorizing" },
      models: ["gpt-5"],
    };
    bridgeMocks.getServiceAuthorization.mockResolvedValue({
      id: "authorization_01",
      provider: "openai_codex",
      status: "completed",
      flow: "browser",
      service_id: authorizing.id,
      authorization_url: "https://auth.example/oauth/authorize",
      expires_at: "2026-07-28T12:10:00Z",
      created_at: timestamp,
      updated_at: timestamp,
    });
    bridgeMocks.getService.mockResolvedValue({ service: authorizing, etag });
    bridgeMocks.probeServiceModels.mockResolvedValue({
      service_id: authorizing.id,
      protocol: "openai.models",
      model_ids: ["gpt-4o", "gpt-5", "gpt-5-codex"],
    });
    bridgeMocks.updateService.mockResolvedValue({
      service: {
        ...authorizing,
        models: ["gpt-4o", "gpt-5", "gpt-5-codex"],
      },
      etag: `"sha256:${"b".repeat(64)}"`,
    });
    const onRefresh = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={onRefresh}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[authorizing]}
          view={{ kind: "list" }}
        />,
      );
    });
    await vi.waitFor(() => {
      expect(bridgeMocks.probeServiceModels).toHaveBeenCalledWith(
        authorizing.id,
        "openai.models",
      );
    });
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      authorizing.id,
      etag,
      expect.objectContaining({
        models: ["gpt-4o", "gpt-5", "gpt-5-codex"],
      }),
    );
    expect(bridgeMocks.updateService.mock.calls[0]?.[2]).not.toHaveProperty(
      "disabled_models",
    );
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "“Codex personal”已登录，已获取 3 个模型。",
    );
    expect(onRefresh).toHaveBeenCalled();
  });

  it("does not import Codex models when login ends without completing", async () => {
    const authorizing: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "authorizing" },
    };
    bridgeMocks.getServiceAuthorization.mockResolvedValue({
      id: "authorization_01",
      provider: "openai_codex",
      status: "cancelled",
      flow: "browser",
      service_id: authorizing.id,
      expires_at: "2026-07-28T12:10:00Z",
      created_at: timestamp,
      updated_at: timestamp,
    });
    const onRefresh = vi.fn();

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={onRefresh}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[authorizing]}
          view={{ kind: "list" }}
        />,
      );
    });
    await vi.waitFor(() => {
      expect(onRefresh).toHaveBeenCalled();
    });
    expect(bridgeMocks.probeServiceModels).not.toHaveBeenCalled();
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
  });

  it("warns when login succeeds but Codex model import fails", async () => {
    const authorizing: Service = {
      ...codexService,
      subscription: { provider: "openai_codex", status: "authorizing" },
    };
    bridgeMocks.getServiceAuthorization.mockResolvedValue({
      id: "authorization_01",
      provider: "openai_codex",
      status: "completed",
      flow: "browser",
      service_id: authorizing.id,
      authorization_url: "https://auth.example/oauth/authorize",
      expires_at: "2026-07-28T12:10:00Z",
      created_at: timestamp,
      updated_at: timestamp,
    });
    bridgeMocks.getService.mockResolvedValue({ service: authorizing, etag });
    bridgeMocks.probeServiceModels.mockRejectedValue(
      new Error("codex models returned status 401"),
    );

    await act(async () => {
      root.render(
        <ServiceManager
          catalogError={null}
          catalogStatus="ready"
          isReady
          onDirtyChange={() => {}}
          onRefresh={() => {}}
          onServiceRemoved={() => {}}
          onServiceSaved={() => {}}
          onViewChange={() => {}}
          protocols={[]}
          services={[authorizing]}
          view={{ kind: "list" }}
        />,
      );
    });
    await vi.waitFor(() => {
      expect(notifyMocks.warning).toHaveBeenCalledWith(
        "codex models returned status 401",
      );
    });
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
  });
  it("inherits global settings until an optional service exception is enabled", async () => {
    const policy = { ...defaultFailurePolicy(), max_retries: 3 };
    bridgeMocks.getRoutingSettings.mockResolvedValue({ default_failure_policy: policy, allow_unmatched_failover: false, strategy: "retry_first", max_attempts: 6 });
    bridgeMocks.getService.mockResolvedValue({ service: gatewayService, etag });
    bridgeMocks.updateService.mockResolvedValue({ service: gatewayService, etag });
    await act(async () => root.render(<ServiceManager catalogError={null} catalogStatus="ready" isReady onDirtyChange={() => {}} onRefresh={() => {}} onServiceRemoved={() => {}} onServiceSaved={() => {}} onViewChange={() => {}} protocols={[]} services={[gatewayService]} view={{ kind: "edit", serviceId: gatewayService.id }} />));
    await openEditorTab("failure");
    const toggle = [...container.querySelectorAll<HTMLButtonElement>('button[role="switch"]')].find(button => button.closest("label")?.textContent?.includes("为这个 API 提供商单独设置"))!;
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    expect(container.textContent).toContain("默认跟随路由页面的默认策略");
    expect(container.textContent).not.toContain("遇到这些错误时");
    await act(async () => toggle.click());
    const retries = container.querySelector<HTMLInputElement>('[data-testid="service-editor-tab-panel"][data-state="active"] input[type="number"]');
    expect(retries?.value).toBe("3");
    await act(async () => toggle.click());
    expect(container.querySelector('[data-testid="service-form"]')?.className).toContain("overflow-hidden");
    await openEditorTab("models");
    await openEditorTab("failure");
    expect(container.querySelector('[data-testid="service-editor-tab-panel"][data-state="active"]')?.className).toContain("overflow-y-auto");
    await act(async () => { container.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(gatewayService.id, etag, expect.objectContaining({ failure_policy: null }));
  });

});
