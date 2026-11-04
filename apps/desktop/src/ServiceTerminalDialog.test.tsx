// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  getClientCatalog: vi.fn(),
  getClientConfigStatus: vi.fn(),
  listTerminalApps: vi.fn(),
  openServiceTerminal: vi.fn(),
  pickTerminalDirectory: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import {
  clientConfigClients,
  type ClientConfigOverview,
} from "./client-config-model";
import { settleClosedLayer } from "./lib/test-layer-settle";
import { notify } from "./notify";
import type { Service } from "./service-model";
import {
  SERVICE_TERMINAL_STORAGE_KEY,
  readTerminalMemory,
} from "./service-terminal-model";
import { ServiceTerminalDialog } from "./ServiceTerminalDialog";

const inferenceURL = "http://127.0.0.1:18317";

function service(overrides: Partial<Service> = {}): Service {
  return {
    id: "service_claude_two",
    name: "Claude 2",
    kind: "claude_subscription",
    enabled: true,
    models: ["claude-opus-5-5", "claude-haiku-5-5"],
    capabilities: [
      { protocol: "anthropic.messages", mode: "native", streaming: true },
    ],
    subscription: {
      provider: "claude_code",
      status: "connected",
      credential_ref: "local://subscription/service_claude_two",
    },
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  } as Service;
}

function installed(...clients: string[]): ClientConfigOverview {
  return {
    clients: clientConfigClients.map((client) => ({
      client,
      wsl: null,
      target: null,
      detected: clients.includes(client),
      home: "/Users/me",
      dir: `/Users/me/.${client}`,
      paths: [`/Users/me/.${client}/config`],
      exists: [false],
      state: "not_configured" as const,
      token_id: null,
      models: {},
      notes: [],
      paste_since_unix: null,
      paste_into: [],
    })),
    wsl_unchecked: [],
    wsl_localhost: false,
    installed: [],
  };
}

function dialog(): HTMLElement {
  const match = document.querySelector<HTMLElement>('[role="dialog"]');
  if (!match) throw new Error("Missing dialog");
  return match;
}

function button(label: string): HTMLButtonElement {
  const match = [...dialog().querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

function card(label: string): HTMLButtonElement {
  const match = document.querySelector<HTMLButtonElement>(
    `[role="radio"][aria-label="${label}"]`,
  );
  if (!match) throw new Error(`Missing client card: ${label}`);
  return match;
}

function modelInput(): HTMLInputElement {
  const input = document.querySelector<HTMLInputElement>(
    "#service-terminal-model",
  );
  if (!input) throw new Error("Missing model input");
  return input;
}

function trigger(label: string): HTMLButtonElement {
  const match = document.querySelector<HTMLButtonElement>(
    `button[role="combobox"][aria-label="${label}"]`,
  );
  if (!match) throw new Error(`Missing select: ${label}`);
  return match;
}

async function chooseOption(label: string, option: string): Promise<void> {
  await act(async () => {
    trigger(label).dispatchEvent(
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
  ].find((candidate) => candidate.textContent?.trim() === option);
  if (!item) throw new Error(`Missing option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
  await settleClosedLayer();
}

/** Lets the bridge calls a step started settle before checking the result. */
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => setTimeout(resolve, 0));
  });
}

async function click(target: HTMLElement): Promise<void> {
  await act(async () => {
    target.click();
    await Promise.resolve();
  });
  await settle();
}

describe("ServiceTerminalDialog", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;
  const onClose = vi.fn();

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    localStorage.clear();
    bridgeMocks.listTerminalApps.mockResolvedValue([
      { id: "terminal", name: "Terminal" },
      { id: "warp", name: "Warp" },
    ]);
    bridgeMocks.getClientCatalog.mockResolvedValue({ clients: [] });
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      installed("claude", "codex"),
    );
    bridgeMocks.openServiceTerminal.mockResolvedValue(undefined);
    bridgeMocks.pickTerminalDirectory.mockResolvedValue("/Users/me/astrlink");
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    vi.restoreAllMocks();
    container.remove();
  });

  const renderDialog = async (provider = service()) => {
    await act(async () => {
      reactRoot.render(
        <ServiceTerminalDialog
          service={provider}
          inferenceURL={inferenceURL}
          onClose={onClose}
        />,
      );
      await Promise.resolve();
    });
    await settle();
  };

  it("opens Claude Code through the provider in the folder the user picks", async () => {
    const success = vi.spyOn(notify, "success").mockImplementation(() => {});
    await renderDialog();

    expect(dialog().textContent).toContain("在终端打开「Claude 2」");
    expect(card("Claude Code").getAttribute("aria-checked")).toBe("true");
    // A Claude subscription serves Claude Code's own models.
    expect(modelInput().value).toBe("");
    expect(modelInput().placeholder).toBe("Claude Code 自带的模型");
    expect(trigger("终端").textContent).toBe("Terminal");
    // A client starts in a folder the user chose.
    expect(button("打开终端").disabled).toBe(true);

    await click(button("选择文件夹…"));
    expect(bridgeMocks.pickTerminalDirectory).toHaveBeenCalledWith(null);
    // The folder's name leads, so a long path still shows which project.
    expect(trigger("启动目录").textContent).toBe("astrlink/Users/me");
    await chooseOption("终端", "Warp");
    await click(button("打开终端"));

    expect(bridgeMocks.openServiceTerminal).toHaveBeenCalledWith({
      service_id: "service_claude_two",
      service_name: "Claude 2",
      client: "claude",
      directory: "/Users/me/astrlink",
      model: null,
      terminal: "warp",
      inference_url: inferenceURL,
    });
    expect(success).toHaveBeenCalledWith("已在 Warp 打开 Claude Code");
    expect(onClose).toHaveBeenCalledOnce();
    expect(readTerminalMemory()).toEqual({
      terminal: "warp",
      directories: ["/Users/me/astrlink"],
      clients: { service_claude_two: "claude" },
      models: { "service_claude_two:claude": "" },
    });
  });

  it("starts from the last choices and the provider's own models", async () => {
    vi.spyOn(notify, "success").mockImplementation(() => {});
    localStorage.setItem(
      SERVICE_TERMINAL_STORAGE_KEY,
      JSON.stringify({
        terminal: "warp",
        directories: ["/Users/me/astrlink", "/Users/me/other"],
        clients: {},
        models: {},
      }),
    );
    bridgeMocks.getClientConfigStatus.mockResolvedValue(installed("claude"));
    await renderDialog(
      service({
        id: "service_glm",
        name: "GLM",
        kind: "openai_compatible",
        models: ["glm-5", "glm-4.6"],
        subscription: undefined,
      }),
    );

    // Codex is not installed here, and GLM has no Claude models to fall back on.
    expect(card("Codex").disabled).toBe(true);
    expect(card("Codex").closest("label")?.textContent).toContain("未安装");
    expect(modelInput().value).toBe("glm-5");
    expect(trigger("启动目录").textContent).toBe("astrlink/Users/me");
    expect(trigger("终端").textContent).toBe("Warp");
    await chooseOption("启动目录", "other/Users/me");
    await click(button("打开终端"));

    expect(bridgeMocks.openServiceTerminal).toHaveBeenCalledWith(
      expect.objectContaining({
        service_id: "service_glm",
        client: "claude",
        directory: "/Users/me/other",
        model: "glm-5",
        terminal: "warp",
      }),
    );
    expect(readTerminalMemory().directories).toEqual([
      "/Users/me/other",
      "/Users/me/astrlink",
    ]);
  });

  it("keeps the dialog open with the reason a terminal did not open", async () => {
    localStorage.setItem(
      SERVICE_TERMINAL_STORAGE_KEY,
      JSON.stringify({ directories: ["/Users/me/astrlink"] }),
    );
    bridgeMocks.openServiceTerminal.mockRejectedValue(
      new Error("Warp is not installed"),
    );
    await renderDialog(service({ enabled: false }));

    expect(dialog().textContent).toContain(
      "这个 API 提供商已关闭。打开它之前，终端里的请求都会失败。",
    );
    await click(card("Codex"));
    expect(modelInput().placeholder).toBe("Codex 设置里的模型");
    // Claude models are not Codex's own, so Codex starts on the first one.
    expect(modelInput().value).toBe("claude-opus-5-5");
    await click(button("打开终端"));

    expect(dialog().textContent).toContain("打不开终端：Warp is not installed");
    expect(button("打开终端").disabled).toBe(false);
    expect(onClose).not.toHaveBeenCalled();
    expect(readTerminalMemory().clients).toEqual({});
  });

  it("explains a computer without a terminal or a client to open", async () => {
    bridgeMocks.listTerminalApps.mockResolvedValue([]);
    bridgeMocks.getClientConfigStatus.mockResolvedValue(installed());
    await renderDialog();

    expect(dialog().textContent).toContain(
      "这台电脑上没找到 AstrLink 能打开的终端。",
    );
    expect(dialog().textContent).toContain("先安装 Claude Code 或 Codex。");
    expect(document.querySelector("#service-terminal-model")).toBeNull();
    expect(button("打开终端").disabled).toBe(true);
  });
});
