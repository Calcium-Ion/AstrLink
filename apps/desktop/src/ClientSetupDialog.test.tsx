// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  applyClientConfig: vi.fn(),
  checkClientProxy: vi.fn(),
  getClientConfigStatus: vi.fn(),
  getRoutingSettings: vi.fn(),
  isCCSwitchInstalled: vi.fn(),
  listServices: vi.fn(),
  getService: vi.fn(),
  updateService: vi.fn(),
  openCCSwitchImport: vi.fn(),
  removeClientConfig: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import type { ConversionEngineCapability } from "./core-model";
import type { Service } from "./service-model";
import { ClientSetupDialog } from "./ClientSetupDialog";
import type { AccessTokenSummary } from "./access-token-model";
import type {
  ClientConfigState,
  ClientConfigStatus,
} from "./client-config-model";

const token: AccessTokenSummary = {
  id: "token_01",
  name: "VS Code",
  hint: "astr_…K8Q2",
  created_at: "2026-07-24T10:30:00Z",
};

const otherToken: AccessTokenSummary = {
  id: "token_02",
  name: "Terminal",
  hint: "astr_…7HT4",
  created_at: "2026-07-24T10:31:00Z",
};

const secret = `astr_${"A".repeat(43)}`;

function statuses(
  claude: Partial<ClientConfigStatus> = {},
  codex: Partial<ClientConfigStatus> = {},
  pi: Partial<ClientConfigStatus> = {},
): ClientConfigStatus[] {
  return [
    {
      client: "claude",
      detected: true,
      paths: ["/Users/me/.claude/settings.json"],
      state: "not_configured",
      token_id: null,
      ...claude,
    },
    {
      client: "codex",
      detected: true,
      paths: ["/Users/me/.codex/config.toml"],
      state: "not_configured",
      token_id: null,
      ...codex,
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
      ...pi,
    },
  ];
}

function configured(
  state: Exclude<ClientConfigState, "not_configured">,
  tokenId = token.id,
): Partial<ClientConfigStatus> {
  return { state, token_id: tokenId };
}

function dialog(): HTMLElement {
  const match = document.querySelector<HTMLElement>('[role="dialog"]');
  if (!match) throw new Error("Missing dialog");
  return match;
}

function confirmation(): HTMLElement {
  const match = document.querySelector<HTMLElement>('[role="alertdialog"]');
  if (!match) throw new Error("Missing confirmation");
  return match;
}

function button(label: string, root: ParentNode = dialog()): HTMLButtonElement {
  const match = [...root.querySelectorAll("button")].find(
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

function cardText(label: string): string {
  return card(label).closest("label")?.textContent ?? "";
}

async function setInput(selector: string, value: string): Promise<void> {
  const input = document.querySelector<HTMLInputElement>(selector);
  if (!input) throw new Error(`Missing input: ${selector}`);
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  if (!valueSetter) throw new Error("Missing input value setter");
  await act(async () => {
    valueSetter.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function suggestions(): Promise<string[]> {
  const input = document.querySelector<HTMLInputElement>("#client-setup-model");
  if (!input) throw new Error("Missing model input");
  await act(async () => input.click());
  const options = [...document.querySelectorAll('[role="option"]')].map(
    (option) => option.getAttribute("aria-label") ?? "",
  );
  await act(async () =>
    input.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    ),
  );
  return options;
}

describe("ClientSetupDialog", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;
  const onClose = vi.fn();
  const onChanged = vi.fn();

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    bridgeMocks.getClientConfigStatus.mockResolvedValue(statuses());
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(false);
    bridgeMocks.listServices.mockResolvedValue({ items: [] });
    bridgeMocks.getRoutingSettings.mockResolvedValue({ model_redirects: [] });
    bridgeMocks.checkClientProxy.mockResolvedValue({
      client: { route: "direct" },
      numeric: null,
    });
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    vi.restoreAllMocks();
    container.remove();
  });

  const renderDialog = async (
    inferenceURL = "http://127.0.0.1:8317",
    conversionEngine?: ConversionEngineCapability | null,
  ) => {
    await act(async () => {
      reactRoot.render(
        <ClientSetupDialog
          conversionEngine={conversionEngine}
          token={token}
          tokens={[token, otherToken]}
          inferenceURL={inferenceURL}
          onClose={onClose}
          onChanged={onChanged}
        />,
      );
      await Promise.resolve();
    });
  };

  const engine: ConversionEngineCapability = {
    name: "relaykit",
    version: null,
    available: true,
    edges: [
      {
        from: "openai.responses",
        to: "openai.chat",
        quality: "good",
        streaming: true,
      },
      {
        from: "anthropic.messages",
        to: "openai.chat",
        quality: "fair",
        streaming: true,
      },
      {
        from: "google.generate_content",
        to: "openai.chat",
        quality: "fair",
        streaming: true,
      },
    ],
  };
  const chatOnly: Service = {
    id: "service_chat",
    name: "Chat provider",
    kind: "openai_compatible",
    enabled: true,
    models: ["chat-model"],
    capabilities: [
      { protocol: "openai.chat", mode: "native", streaming: true },
    ],
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
  };
  function mockChatProvider(service = chatOnly) {
    bridgeMocks.listServices.mockResolvedValue({ items: [service] });
    bridgeMocks.getService.mockResolvedValue({ service, etag: '"fresh-etag"' });
    bridgeMocks.updateService.mockImplementation(async (_id, _etag, patch) => {
      const updated = { ...service, ...patch };
      bridgeMocks.listServices.mockResolvedValue({ items: [updated] });
      return { service: updated, etag: '"updated-etag"' };
    });
    bridgeMocks.applyClientConfig.mockResolvedValue({ status: "applied" });
  }

  it.each([false, true])(
    "enables a missing Codex entry before writing (redirect=%s)",
    async (redirect) => {
      mockChatProvider();
      bridgeMocks.getRoutingSettings.mockResolvedValue({
        model_redirects: redirect
          ? [{ from: "codex-alias", to: "chat-model", enabled: true }]
          : [],
      });
      // An unrelated concurrent capability edit must survive the patch.
      const fresh = {
        ...chatOnly,
        capabilities: [
          ...chatOnly.capabilities,
          { protocol: "openai.models", mode: "native", streaming: false },
        ],
      };
      bridgeMocks.getService.mockResolvedValue({
        service: fresh,
        etag: '"fresh-etag"',
      });
      await renderDialog(undefined, engine);
      await act(async () => card("Codex").click());
      const selected = redirect ? "codex-alias" : "chat-model";
      await act(async () =>
        document
          .querySelector<HTMLInputElement>("#client-setup-model")!
          .click(),
      );
      const option = document.querySelector<HTMLElement>(
        `[role="option"][aria-label="${selected}"]`,
      )!;
      expect(option.textContent).toContain("需开启入口");
      expect(bridgeMocks.updateService).not.toHaveBeenCalled();
      await act(async () => option.click());
      expect(dialog().textContent).toContain(
        "Chat provider 尚未开启 OpenAI Responses 入口，写入配置时将自动开启（转换为 OpenAI Chat Completions）",
      );
      await act(async () => button("写入配置").click());
      expect(bridgeMocks.updateService).toHaveBeenCalledWith(
        chatOnly.id,
        '"fresh-etag"',
        {
          capabilities: [
            ...fresh.capabilities,
            {
              protocol: "openai.responses",
              mode: "native",
              streaming: true,
              convert_to: "openai.chat",
            },
          ],
        },
      );
      expect(
        bridgeMocks.updateService.mock.invocationCallOrder[0],
      ).toBeLessThan(bridgeMocks.applyClientConfig.mock.invocationCallOrder[0]);
      expect(bridgeMocks.applyClientConfig).toHaveBeenCalledWith(
        expect.objectContaining({ models: { model: selected } }),
      );
      expect(bridgeMocks.listServices).toHaveBeenCalledTimes(2);
      expect(dialog().textContent).not.toContain("尚未开启");
      await act(async () =>
        document
          .querySelector<HTMLInputElement>("#client-setup-model")!
          .click(),
      );
      expect(
        document.querySelector(`[role="option"][aria-label="${selected}"]`)!
          .textContent,
      ).not.toContain("需开启入口");
    },
  );

  it("stops before writing when enabling the entry fails", async () => {
    mockChatProvider();
    bridgeMocks.updateService.mockRejectedValue(new Error("ETag conflict"));
    await renderDialog(undefined, engine);
    await act(async () => card("Codex").click());
    await setInput("#client-setup-model", "chat-model");
    await act(async () => button("写入配置").click());
    expect(dialog().textContent).toContain(
      "无法为 Chat provider 开启 OpenAI Responses 入口",
    );
    expect(bridgeMocks.applyClientConfig).not.toHaveBeenCalled();
  });

  it("leaves an existing entry untouched", async () => {
    mockChatProvider({
      ...chatOnly,
      capabilities: [
        ...chatOnly.capabilities,
        { protocol: "openai.responses", mode: "native", streaming: true },
      ],
    });
    await renderDialog(undefined, engine);
    await act(async () => card("Codex").click());
    expect(await suggestions()).toEqual(["chat-model"]);
    await setInput("#client-setup-model", "chat-model");
    expect(dialog().textContent).not.toContain("尚未开启");
    await act(async () => button("写入配置").click());
    expect(bridgeMocks.getService).not.toHaveBeenCalled();
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
    expect(bridgeMocks.applyClientConfig).toHaveBeenCalledOnce();
  });

  it("does not duplicate an entry enabled by a concurrent edit", async () => {
    mockChatProvider();
    bridgeMocks.getService.mockResolvedValue({
      service: {
        ...chatOnly,
        capabilities: [
          ...chatOnly.capabilities,
          { protocol: "openai.responses", mode: "native", streaming: true },
        ],
      },
      etag: '"new-etag"',
    });
    await renderDialog(undefined, engine);
    await act(async () => card("Codex").click());
    await setInput("#client-setup-model", "chat-model");
    await act(async () => button("写入配置").click());
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();
    expect(bridgeMocks.applyClientConfig).toHaveBeenCalledOnce();
  });

  it.each([
    null,
    { ...engine, available: false },
    { ...engine, edges: [] },
    {
      ...engine,
      edges: engine.edges.map((edge) => ({
        ...edge,
        quality: "discouraged" as const,
      })),
    },
  ])("does not suggest unsupported conversions (%j)", async (snapshot) => {
    mockChatProvider();
    await renderDialog(undefined, snapshot);
    await act(async () => card("Codex").click());
    expect(await suggestions()).toEqual([]);
  });

  it("enables Claude tier model entries once per provider", async () => {
    mockChatProvider();
    await renderDialog(undefined, engine);
    await setInput("#client-setup-sonnetModel", "chat-model");
    await setInput("#client-setup-opusModel", "chat-model");
    expect(dialog().textContent).toContain("尚未开启 Anthropic Messages");
    await act(async () => button("写入配置").click());
    expect(bridgeMocks.updateService).toHaveBeenCalledOnce();
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      chatOnly.id,
      '"fresh-etag"',
      {
        capabilities: [
          ...chatOnly.capabilities,
          {
            protocol: "anthropic.messages",
            mode: "native",
            streaming: true,
            convert_to: "openai.chat",
          },
        ],
      },
    );
    expect(bridgeMocks.applyClientConfig).toHaveBeenCalledOnce();
  });

  it("also enables the entry before CC Switch import", async () => {
    mockChatProvider();
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(true);
    await renderDialog(undefined, engine);
    await act(async () => card("Gemini CLI").click());
    await setInput("#client-setup-model", "chat-model");
    await act(async () => button("改用 CC Switch 导入").click());
    expect(bridgeMocks.updateService).toHaveBeenCalledOnce();
    expect(bridgeMocks.updateService.mock.invocationCallOrder[0]).toBeLessThan(
      bridgeMocks.openCCSwitchImport.mock.invocationCallOrder[0],
    );
  });

  it("marks each client's state and disables what it cannot configure", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      statuses(configured("configured", otherToken.id), { detected: false }),
    );
    await renderDialog();

    expect(bridgeMocks.getClientConfigStatus).toHaveBeenCalledWith(
      "http://127.0.0.1:8317",
    );
    expect(card("Claude Code").getAttribute("aria-checked")).toBe("true");
    expect(cardText("Claude Code")).toContain("已配置");
    expect(card("Codex").disabled).toBe(true);
    expect(cardText("Codex")).toContain("未安装");
    for (const label of ["Gemini CLI", "OpenCode", "OpenClaw"]) {
      expect(card(label).disabled).toBe(true);
      expect(cardText(label)).toContain("暂不支持");
    }
    const text = dialog().textContent;
    expect(text).toContain("安装 CC Switch 后可通过它导入");
    expect(text).toContain("/Users/me/.claude/settings.json");
    expect(text).toContain(
      "Claude Code 当前使用“Terminal”，更新后改用“VS Code”。",
    );
    expect(text).not.toContain(secret);
    expect(button("更新配置").disabled).toBe(false);
    expect(button("移除配置")).toBeTruthy();
    expect(text).not.toContain("改用 CC Switch 导入");
  });

  it("shows ports awaiting an update, edited configs, and unreadable files", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      statuses(configured("invalid"), configured("modified")),
    );
    await renderDialog();

    expect(cardText("Claude Code")).toContain("无法读取配置");
    expect(cardText("Codex")).toContain("配置已被改动");
    // An unreadable file can be neither overwritten nor cleaned up.
    expect(button("更新配置").disabled).toBe(true);
    expect(dialog().textContent).not.toContain("移除配置");

    await act(async () => card("Codex").click());
    expect(button("移除配置")).toBeTruthy();

    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      statuses(configured("outdated")),
    );
    await act(async () => reactRoot.unmount());
    reactRoot = createRoot(container);
    await renderDialog();
    expect(cardText("Claude Code")).toContain("端口待更新");
  });

  it("confirms replacing settings it did not write before writing them", async () => {
    bridgeMocks.applyClientConfig
      .mockResolvedValueOnce({
        status: "needs_confirmation",
        keys: ["env.ANTHROPIC_API_KEY", "apiKeyHelper"],
      })
      .mockResolvedValueOnce({ status: "applied" });
    await renderDialog();

    expect(dialog().querySelectorAll('input[role="combobox"]')).toHaveLength(5);
    await setInput("#client-setup-sonnetModel", " sonnet-route ");
    await setInput("#client-setup-fableModel", "fable-route");
    await setInput("#client-setup-haikuModel", "  ");
    await act(async () => button("写入配置").click());
    const target = {
      tokenId: token.id,
      client: "claude",
      models: { sonnetModel: "sonnet-route", fableModel: "fable-route" },
      inferenceUrl: "http://127.0.0.1:8317",
    };
    expect(bridgeMocks.applyClientConfig).toHaveBeenLastCalledWith({
      ...target,
      replace: false,
    });
    expect(confirmation().textContent).toContain("env.ANTHROPIC_API_KEY");
    expect(confirmation().textContent).toContain("apiKeyHelper");
    expect(onChanged).not.toHaveBeenCalled();

    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      statuses(configured("configured")),
    );
    await act(async () => button("替换并写入", confirmation()).click());
    expect(bridgeMocks.applyClientConfig).toHaveBeenLastCalledWith({
      ...target,
      replace: true,
    });
    expect(onChanged).toHaveBeenCalledOnce();
    expect(onClose).not.toHaveBeenCalled();
    expect(cardText("Claude Code")).toContain("已配置");
    expect(button("更新配置")).toBeTruthy();
  });

  it("writes nothing when the replacement is cancelled", async () => {
    bridgeMocks.applyClientConfig.mockResolvedValue({
      status: "needs_confirmation",
      keys: ["model_provider"],
    });
    await renderDialog();

    await act(async () => card("Codex").click());
    expect(dialog().textContent).toContain("http://127.0.0.1:8317/v1");
    expect(dialog().textContent).toContain("/Users/me/.codex/config.toml");
    expect(document.querySelector("#client-setup-opusModel")).toBeNull();
    expect(button("写入配置").disabled).toBe(true);
    await setInput("#client-setup-model", "gpt-5");
    await act(async () => button("写入配置").click());
    await act(async () => button("取消", confirmation()).click());
    expect(bridgeMocks.applyClientConfig).toHaveBeenCalledOnce();
    expect(bridgeMocks.applyClientConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        client: "codex",
        models: { model: "gpt-5" },
        replace: false,
      }),
    );
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("shows why a write failed without closing", async () => {
    bridgeMocks.applyClientConfig.mockRejectedValue(
      new Error("unable to parse /Users/me/.claude/settings.json"),
    );
    await renderDialog();

    await act(async () => button("写入配置").click());
    const text = dialog().textContent;
    expect(text).toContain("无法写入配置。");
    expect(text).toContain("unable to parse /Users/me/.claude/settings.json");
    expect(button("写入配置").disabled).toBe(false);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("removes a client's config after confirmation", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      statuses(configured("configured")),
    );
    bridgeMocks.removeClientConfig.mockResolvedValue(undefined);
    await renderDialog();

    await act(async () => button("移除配置").click());
    expect(confirmation().textContent).toContain("移除 Claude Code 中的配置？");
    bridgeMocks.getClientConfigStatus.mockResolvedValue(statuses());
    await act(async () => button("移除配置", confirmation()).click());
    expect(bridgeMocks.removeClientConfig).toHaveBeenCalledExactlyOnceWith(
      "claude",
    );
    expect(onChanged).toHaveBeenCalledOnce();
    expect(button("写入配置")).toBeTruthy();
    expect(dialog().textContent).not.toContain("移除配置");
  });

  it("offers CC Switch only while it is installed, without Fable or native errors", async () => {
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(true);
    bridgeMocks.openCCSwitchImport
      .mockRejectedValueOnce(
        new Error(`failed ccswitch://test?apiKey=${secret}`),
      )
      .mockResolvedValueOnce(undefined);
    await renderDialog();

    expect(dialog().textContent).toContain(
      "通过 CC Switch 导入时不包含 Fable。",
    );
    expect(dialog().textContent).not.toContain("安装 CC Switch 后可通过它导入");
    expect(button("改用 CC Switch 导入")).toBeTruthy();
    expect(cardText("Gemini CLI")).toContain("通过 CC Switch");

    await act(async () => card("Gemini CLI").click());
    expect(dialog().textContent).not.toContain("写入配置");
    expect(dialog().textContent).not.toContain(".claude/settings.json");
    expect(button("改用 CC Switch 导入").disabled).toBe(true);
    await setInput("#client-setup-model", "gemini-2.5-pro");
    await act(async () => button("改用 CC Switch 导入").click());
    expect(dialog().textContent).toContain("无法打开 CC Switch");
    expect(document.body.textContent).not.toContain(secret);

    await act(async () => button("改用 CC Switch 导入").click());
    expect(bridgeMocks.openCCSwitchImport).toHaveBeenLastCalledWith({
      tokenId: token.id,
      client: "gemini",
      models: { model: "gemini-2.5-pro" },
      inferenceUrl: "http://127.0.0.1:8317",
    });
    expect(onClose).toHaveBeenCalledOnce();
    expect(bridgeMocks.applyClientConfig).not.toHaveBeenCalled();
  });

  it("writes Pi itself, even while CC Switch is installed", async () => {
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(true);
    bridgeMocks.applyClientConfig.mockResolvedValue({ status: "applied" });
    bridgeMocks.listServices.mockResolvedValue({
      items: [
        {
          enabled: true,
          models: ["gpt-5"],
          capabilities: [{ protocol: "openai.responses" }],
        },
      ],
    });
    await renderDialog();

    expect(cardText("Pi")).not.toContain("CC Switch");
    await act(async () => card("Pi").click());
    const text = dialog().textContent;
    expect(text).toContain("http://127.0.0.1:8317/v1");
    expect(text).toContain("/Users/me/.pi/agent/models.json");
    expect(text).toContain("/Users/me/.pi/agent/settings.json");
    expect(text).not.toContain("改用 CC Switch 导入");
    expect(button("写入配置").disabled).toBe(true);

    await setInput("#client-setup-model", "gpt-5");
    await act(async () => button("写入配置").click());
    expect(bridgeMocks.applyClientConfig).toHaveBeenCalledExactlyOnceWith({
      tokenId: token.id,
      client: "pi",
      models: { model: "gpt-5" },
      inferenceUrl: "http://127.0.0.1:8317",
      replace: false,
    });
    expect(bridgeMocks.openCCSwitchImport).not.toHaveBeenCalled();
    expect(onChanged).toHaveBeenCalledOnce();

    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      statuses({}, {}, { detected: false }),
    );
    await act(async () => reactRoot.unmount());
    reactRoot = createRoot(container);
    await renderDialog();
    expect(card("Pi").disabled).toBe(true);
    expect(cardText("Pi")).toContain("未安装");
  });

  it("suggests only compatible enabled models for each client", async () => {
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(true);
    const protocols = [
      "anthropic.messages",
      "openai.responses",
      "google.generate_content",
      "openai.chat",
    ];
    bridgeMocks.listServices.mockResolvedValue({
      items: protocols
        .map((protocol, index) => ({
          enabled: true,
          models: [`model-${index}`],
          capabilities: [{ protocol }],
        }))
        .concat([
          {
            enabled: false,
            models: ["disabled-model"],
            capabilities: [{ protocol: "anthropic.messages" }],
          },
        ]),
    });
    await renderDialog();

    for (const [client, expected] of [
      ["Claude Code", ["model-0"]],
      ["Codex", ["model-1"]],
      ["Gemini CLI", ["model-2"]],
      ["OpenCode", ["model-3"]],
      ["OpenClaw", ["model-3"]],
      ["Pi", ["model-1"]],
    ] as const) {
      await act(async () => card(client).click());
      expect(await suggestions()).toEqual(expected);
      expect(dialog().textContent).toContain(
        client === "Claude Code"
          ? "按需填写；留空的模型项不会写入。"
          : "可搜索当前客户端兼容的模型，或输入模型名。",
      );
    }
  });

  it("suggests enabled redirect sources whose target a compatible service lists", async () => {
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(true);
    bridgeMocks.listServices.mockResolvedValue({
      items: [
        {
          enabled: true,
          models: ["gemini-2.5-pro"],
          capabilities: [{ protocol: "google.generate_content" }],
        },
        {
          enabled: true,
          models: ["gpt-5"],
          capabilities: [{ protocol: "openai.responses" }],
        },
      ],
    });
    bridgeMocks.getRoutingSettings.mockResolvedValue({
      model_redirects: [
        { from: "gemini-pro", to: "gemini-2.5-pro", enabled: true },
        { from: "openrouter/gemini-pro", to: "gemini-2.5-pro", enabled: true },
        { from: "gpt-4o", to: "gpt-5", enabled: true },
        { from: "retired-model", to: "gpt-5", enabled: false },
        { from: "astrlink/auto", to: "gpt-5", enabled: true },
        { from: "orphan-model", to: "unlisted-model", enabled: true },
      ],
    });
    await renderDialog();

    await act(async () => card("Gemini CLI").click());
    expect(await suggestions()).toEqual(["gemini-2.5-pro", "gemini-pro"]);
    await act(async () => card("Codex").click());
    expect(await suggestions()).toEqual(["gpt-4o", "gpt-5"]);
  });

  it("keeps service suggestions when optional redirect sources fail", async () => {
    bridgeMocks.listServices.mockResolvedValue({
      items: [
        {
          enabled: true,
          models: ["claude-sonnet"],
          capabilities: [{ protocol: "anthropic.messages" }],
        },
      ],
    });
    bridgeMocks.getRoutingSettings.mockRejectedValue(new Error("offline"));
    await renderDialog();

    expect(await suggestions()).toEqual(["claude-sonnet"]);
    expect(dialog().textContent).not.toContain("部分模型未能读取");
  });

  it("reports failed model suggestions and status reads", async () => {
    bridgeMocks.listServices.mockRejectedValue(new Error("offline"));
    bridgeMocks.getClientConfigStatus.mockRejectedValue(new Error("offline"));
    await renderDialog();
    await act(async () => Promise.resolve());

    const text = dialog().textContent;
    expect(text).toContain("无法读取客户端的配置状态，请稍后重试。");
    expect(text).not.toContain("未安装");
    expect(card("Claude Code").disabled).toBe(true);
    expect(dialog().querySelector("#client-setup-model")).toBeNull();

    bridgeMocks.getClientConfigStatus.mockResolvedValue(statuses());
    await act(async () => reactRoot.unmount());
    reactRoot = createRoot(container);
    await renderDialog();
    await act(async () => Promise.resolve());
    expect(dialog().textContent).toContain(
      "部分模型未能读取，可手动输入模型 ID。",
    );
  });

  it("warns on the Codex card when the system proxy blocks it", async () => {
    bridgeMocks.checkClientProxy.mockResolvedValue({
      client: { route: "blocked", proxy: "127.0.0.1:7892" },
      numeric: null,
    });
    await renderDialog("http://localhost:8317");

    // Claude Code does not follow the system proxy, so it is not checked.
    expect(bridgeMocks.checkClientProxy).not.toHaveBeenCalled();
    expect(dialog().textContent).not.toContain("系统代理");

    await act(async () => card("Codex").click());
    expect(bridgeMocks.checkClientProxy).toHaveBeenCalledWith(
      "http://localhost:8317",
    );
    const text = dialog().textContent;
    expect(text).toContain(
      "系统代理 127.0.0.1:7892 会拦下 Codex 发往 AstrLink 的请求",
    );
    expect(text).toContain("localhost、127.0.0.1 和 ::1");
    expect(text).toContain("AstrLink 不会改动这些设置。");

    await act(async () => card("Claude Code").click());
    expect(dialog().textContent).not.toContain("系统代理");
  });

  it.each([
    [
      "a proxy that still reaches the gateway",
      { client: { route: "proxied", proxy: "127.0.0.1:7892" }, numeric: null },
      "Codex 的请求会先经过系统代理 127.0.0.1:7892 再回到 AstrLink，目前可以连通。",
    ],
    [
      "a proxy that blocks only the numeric address",
      {
        client: { route: "direct" },
        numeric: { route: "blocked", proxy: "127.0.0.1:7892" },
      },
      "AstrLink 写入的配置使用 http://localhost:8317/v1，不受影响",
    ],
  ])("notes %s on the Codex card", async (_, check, message) => {
    bridgeMocks.checkClientProxy.mockResolvedValue(check);
    await renderDialog("http://localhost:8317");
    await act(async () => card("Codex").click());

    expect(dialog().textContent).toContain(message);
  });

  it.each([
    [
      "direct routes",
      () =>
        Promise.resolve({
          client: { route: "direct" },
          numeric: { route: "proxied", proxy: "127.0.0.1:7892" },
        }),
    ],
    ["a failed check", () => Promise.reject(new Error("offline"))],
  ])("shows no proxy hint for %s", async (_, check) => {
    bridgeMocks.checkClientProxy.mockImplementation(check);
    await renderDialog("http://localhost:8317");
    await act(async () => card("Codex").click());

    expect(bridgeMocks.checkClientProxy).toHaveBeenCalledOnce();
    expect(dialog().textContent).not.toContain("系统代理");
    expect(dialog().querySelector('[role="alert"]')).toBeNull();
  });
});
