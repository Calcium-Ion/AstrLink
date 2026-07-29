// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  beginServiceAuthorization: vi.fn(),
  cancelServiceAuthorization: vi.fn(),
  createService: vi.fn(),
  deleteService: vi.fn(),
  getService: vi.fn(),
  getServiceAuthorization: vi.fn(),
  logoutService: vi.fn(),
  openAuthorizationURL: vi.fn(),
  updateService: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { ServiceManager } from "./ServiceManager";
import type { Service } from "./service-model";

const timestamp = "2026-07-28T12:00:00Z";
const etag = `"sha256:${"a".repeat(64)}"`;

const codexService: Service = {
  id: "service_codex_personal",
  name: "Codex personal",
  kind: "codex_subscription",
  enabled: true,
  capabilities: [
    { protocol: "openai.responses", mode: "native", streaming: true },
  ],
  subscription: {
    provider: "openai_codex",
    status: "disconnected",
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
  capabilities: [
    { protocol: "openai.responses", mode: "delegated", streaming: true },
  ],
  http: {
    base_url: "https://gateway.example/v1",
    auth: { scheme: "bearer" },
    credential_ref: "local://service/service_gateway",
  },
  created_at: timestamp,
  updated_at: timestamp,
};

describe("ServiceManager", () => {
  let root: Root;
  let container: HTMLDivElement;

  beforeEach(() => {
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

    expect(container.querySelectorAll(".service-card")).toHaveLength(3);
    expect(container.textContent).toContain("Codex personal");
    expect(container.textContent).toContain("Codex work");
    expect(container.textContent).toContain("new-api");
    expect(container.textContent).toContain("全部服务");
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
      ".service-form__submit",
    );
    expect(submitButton?.disabled).toBe(true);
    const browserFlow = container.querySelector<HTMLInputElement>(
      'input[name="new-codex-login-flow"]',
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
    const kind = container.querySelector<HTMLSelectElement>(
      ".service-form select",
    );
    if (!kind) throw new Error("missing service kind select");
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLSelectElement.prototype,
      "value",
    )?.set;
    if (!valueSetter) throw new Error("missing select value setter");
    await act(async () => {
      valueSetter.call(kind, "anthropic");
      kind.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(kind.value).toBe("anthropic");
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

    expect(kind.value).toBe("anthropic");
    expect(
      container.querySelector<HTMLInputElement>("#service-name")?.value,
    ).toBe("Anthropic API");
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
    const deviceFlow = container.querySelectorAll<HTMLInputElement>(
      'input[name="new-codex-login-flow"]',
    )[1];
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
    expect(container.textContent).toContain(
      "Codex 服务已添加，可稍后从服务列表重新登录。",
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
    const login = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "登录",
    );
    await act(async () => login?.click());
    expect(bridgeMocks.beginServiceAuthorization).not.toHaveBeenCalled();
    const methodInputs = container.querySelectorAll<HTMLInputElement>(
      'input[name="existing-codex-login-flow"]',
    );
    expect(methodInputs).toHaveLength(2);
    expect([...methodInputs].every((input) => !input.checked)).toBe(true);

    await act(async () => methodInputs[0]?.click());
    const start = [...container.querySelectorAll("button")].find(
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
    expect(container.textContent).toContain("1455 和 1457 均不可用");
    expect(container.textContent).toContain("ABCD-EFGH");

    const copy = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "复制验证码",
    );
    await act(async () => {
      copy?.click();
      await Promise.resolve();
    });
    expect(writeText).toHaveBeenCalledWith("ABCD-EFGH");

    const reopen = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "重新打开登录页面",
    );
    await act(async () => {
      reopen?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.openAuthorizationURL).toHaveBeenCalledWith(
      "https://auth.openai.com/codex/device",
    );

    const cancel = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "取消登录",
    );
    await act(async () => {
      cancel?.click();
      await Promise.resolve();
    });
    expect(bridgeMocks.cancelServiceAuthorization).toHaveBeenCalledWith(
      codexService.id,
    );
    expect(container.querySelector(".device-code-dialog")).toBeNull();

    await act(async () => login?.click());
    const explicitDevice = container.querySelectorAll<HTMLInputElement>(
      'input[name="existing-codex-login-flow"]',
    )[1];
    await act(async () => explicitDevice?.click());
    const restart = [...container.querySelectorAll("button")].find(
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
    expect(container.querySelector(".device-code-dialog__fallback")).toBeNull();
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
    const deleteButton = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "删除",
    );
    await act(async () => deleteButton?.click());
    expect(container.querySelector('[role="dialog"]')).not.toBeNull();
    expect(bridgeMocks.deleteService).not.toHaveBeenCalled();

    const confirm = [...container.querySelectorAll("button")].find(
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
});
