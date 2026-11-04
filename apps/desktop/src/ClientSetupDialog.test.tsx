// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  applyClientConfigs: vi.fn(),
  checkClientProxy: vi.fn(),
  copyAccessToken: vi.fn(),
  getClientCatalog: vi.fn(),
  getClientConfigStatus: vi.fn(),
  getRoutingSettings: vi.fn(),
  getService: vi.fn(),
  isCCSwitchInstalled: vi.fn(),
  listRequestRecords: vi.fn(),
  listServices: vi.fn(),
  openClientImportLink: vi.fn(),
  previewClientConfigChanges: vi.fn(),
  removeClientConfig: vi.fn(),
  updateService: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

const pricingMocks = vi.hoisted(() => ({
  getOfficialModelLimits: vi.fn(),
}));

vi.mock("./pricing-bridge", () => pricingMocks);

import type { AccessTokenSummary } from "./access-token-model";
import {
  clientConfigClients,
  type ClientCatalog,
  type ClientCatalogEntry,
  type ClientConfigClient,
  type ClientConfigItem,
  type ClientConfigOverview,
  type ClientConfigStatus,
} from "./client-config-model";
import { ClientSetupDialog } from "./ClientSetupDialog";
import type { ConversionEngineCapability } from "./core-model";
import { settleClosedLayer } from "./lib/test-layer-settle";
import type { Service } from "./service-model";

const token: AccessTokenSummary = {
  id: "token_01",
  name: "工作机",
  hint: "astr_…K8Q2",
  source: "user",
  created_at: "2026-07-24T10:30:00Z",
};

const otherToken: AccessTokenSummary = {
  id: "token_02",
  name: "家里",
  hint: "astr_…7HT4",
  source: "user",
  created_at: "2026-07-24T10:31:00Z",
};

/** The six clients as the host lists them, in rail order. */
const builtinEntries: ClientCatalogEntry[] = [
  ["claude", "Claude Code", "anthropic.messages", false],
  ["codex", "Codex", "openai.responses", false],
  ["gemini", "Gemini CLI", "google.generate_content", false],
  ["opencode", "OpenCode", "openai.chat", true],
  ["openclaw", "OpenClaw", "openai.chat", true],
  ["pi", "Pi", "openai.responses", true],
].map(([id, name, protocol, list]) => ({
  id: id as string,
  name: name as string,
  kind: "file" as const,
  targets: [],
  protocols: [protocol as string],
  list: list as boolean,
}));

/** The clients AstrLink only lists: one to fill in, one it cannot reach. */
const listedEntries: ClientCatalogEntry[] = [
  {
    id: "trae",
    name: "TRAE",
    kind: "guide",
    targets: [],
    protocols: ["anthropic.messages", "openai.responses", "openai.chat"],
    list: false,
  },
  {
    id: "cherry_studio",
    name: "Cherry Studio",
    kind: "link",
    targets: [],
    protocols: [
      "openai.chat",
      "openai.responses",
      "anthropic.messages",
      "google.generate_content",
    ],
    list: false,
  },
  {
    id: "cursor",
    name: "Cursor",
    kind: "unsupported",
    targets: [],
    protocols: [],
    list: false,
  },
];

const catalog: ClientCatalog = { clients: builtinEntries };

const dirs: Record<ClientConfigClient, string> = {
  claude: ".claude",
  codex: ".codex",
  gemini: ".gemini",
  opencode: ".config/opencode",
  openclaw: ".openclaw",
  pi: ".pi/agent",
};
const files: Record<ClientConfigClient, string[]> = {
  claude: ["settings.json"],
  codex: ["config.toml"],
  gemini: [".env", "settings.json"],
  opencode: ["opencode.jsonc"],
  openclaw: ["openclaw.json"],
  pi: ["models.json", "settings.json"],
};

function place(
  client: ClientConfigClient,
  overrides: Partial<ClientConfigStatus> = {},
  wsl: string | null = null,
): ClientConfigStatus {
  const home = wsl === null ? "/Users/me" : `\\\\wsl$\\${wsl}\\home\\me`;
  const dir = `${home}/${dirs[client]}`;
  return {
    client,
    wsl,
    target: null,
    detected: true,
    home,
    dir,
    paths: files[client].map((file) => `${dir}/${file}`),
    exists: files[client].map(() => false),
    state: "not_configured",
    token_id: null,
    models: {},
    notes: [],
    paste_since_unix: null,
    paste_into: [],
    ...overrides,
  };
}

/** This computer's six clients, with overrides by client. */
function overview(
  overrides: Partial<
    Record<ClientConfigClient, Partial<ClientConfigStatus>>
  > = {},
  extra: Partial<ClientConfigOverview> = {},
): ClientConfigOverview {
  return {
    clients: clientConfigClients.map((client) =>
      place(client, overrides[client]),
    ),
    wsl_unchecked: [],
    wsl_localhost: false,
    installed: [],
    ...extra,
  };
}

const ours = (
  state: ClientConfigStatus["state"],
  models: ClientConfigStatus["models"] = {},
): Partial<ClientConfigStatus> => ({ state, token_id: token.id, models });

/** One provider that takes every client's entry natively. */
const gateway: Service = {
  id: "service_all",
  name: "API 提供商",
  kind: "openai_compatible",
  enabled: true,
  models: ["claude-sonnet-5", "claude-opus-5-5", "gpt-5.6", "vendor/glm-5"],
  capabilities: [
    { protocol: "anthropic.messages", mode: "native", streaming: true },
    { protocol: "openai.responses", mode: "native", streaming: true },
    { protocol: "openai.chat", mode: "native", streaming: true },
    { protocol: "google.generate_content", mode: "native", streaming: true },
  ],
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
};

const chatOnly: Service = {
  id: "service_chat",
  name: "Chat provider",
  kind: "openai_compatible",
  enabled: true,
  models: ["chat-model", "claude-sonnet-5"],
  capabilities: [{ protocol: "openai.chat", mode: "native", streaming: true }],
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
};

const engine: ConversionEngineCapability = {
  name: "relaykit",
  version: null,
  available: true,
  edges: [
    "openai.responses",
    "anthropic.messages",
    "google.generate_content",
  ].map((from) => ({
    from,
    to: "openai.chat",
    quality: "good" as const,
    streaming: true,
  })),
};

function done(
  items: ClientConfigItem[],
  failed: Partial<Record<string, string>> = {},
) {
  return {
    status: "done" as const,
    results: items.map((item) => {
      const message = failed[item.client];
      return {
        client: item.client,
        wsl: item.wsl,
        target: item.target,
        action: item.action,
        ok: !message,
        error: message
          ? {
              message,
              path: `/Users/me/${dirs[item.client]}/settings.json`,
              line: 12,
            }
          : null,
      };
    }),
  };
}

function dialog(): HTMLElement {
  const match = document.querySelector<HTMLElement>('[role="dialog"]');
  if (!match) throw new Error("Missing dialog");
  return match;
}

function confirmation(): HTMLElement | null {
  return document.querySelector<HTMLElement>('[role="alertdialog"]');
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

function row(client: ClientConfigClient): HTMLElement {
  const match = dialog().querySelector<HTMLElement>(
    `[data-client="${client}"]`,
  );
  if (!match) throw new Error(`Missing row: ${client}`);
  return match;
}

function box(label: string): HTMLButtonElement {
  const match = dialog().querySelector<HTMLButtonElement>(
    `[role="checkbox"][aria-label="写入 ${label} 的配置"]`,
  );
  if (!match) throw new Error(`Missing checkbox: ${label}`);
  return match;
}

/** A rail group's fold button, by its title. */
function groupHeader(title: string): HTMLButtonElement {
  const match = [
    ...dialog().querySelectorAll<HTMLButtonElement>(
      "[data-group] button[aria-expanded]",
    ),
  ].find((candidate) => candidate.textContent?.startsWith(title));
  if (!match) throw new Error(`Missing group: ${title}`);
  return match;
}

const checked = (label: string) => box(label).getAttribute("aria-checked");

function pane(): HTMLElement {
  const match = dialog().querySelector<HTMLElement>(
    '[data-testid="client-setup-pane"]',
  );
  if (!match) throw new Error("Missing pane");
  return match;
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

async function open(client: ClientConfigClient): Promise<void> {
  const target = row(client).querySelector<HTMLButtonElement>(
    'button:not([role="checkbox"])',
  );
  if (!target) throw new Error(`Missing row button: ${client}`);
  await click(target);
}

async function setInput(selector: string, value: string): Promise<void> {
  const input = document.querySelector<HTMLInputElement>(selector);
  if (!input) throw new Error(`Missing input: ${selector}`);
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  await act(async () => {
    valueSetter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}

async function chooseOption(trigger: HTMLElement, option: string) {
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
  ].find((candidate) => candidate.textContent?.trim().startsWith(option));
  if (!item) throw new Error(`Missing option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
  await settleClosedLayer();
  await settle();
}

function lastItems(): ClientConfigItem[] {
  const call = bridgeMocks.applyClientConfigs.mock.lastCall;
  if (!call) throw new Error("applyClientConfigs was not called");
  return call[0].items;
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
    bridgeMocks.getClientCatalog.mockResolvedValue(catalog);
    bridgeMocks.getClientConfigStatus.mockResolvedValue(overview());
    bridgeMocks.copyAccessToken.mockResolvedValue(true);
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(false);
    bridgeMocks.listRequestRecords.mockResolvedValue({
      items: [],
      next_cursor: null,
    });
    bridgeMocks.listServices.mockResolvedValue({ items: [gateway] });
    bridgeMocks.getRoutingSettings.mockResolvedValue({ model_redirects: [] });
    bridgeMocks.applyClientConfigs.mockImplementation(async ({ items }) =>
      done(items),
    );
    bridgeMocks.previewClientConfigChanges.mockResolvedValue({
      files: [],
      replaced: 0,
      restored: 0,
    });
    bridgeMocks.checkClientProxy.mockResolvedValue({
      client: { route: "direct" },
      numeric: null,
    });
    pricingMocks.getOfficialModelLimits.mockResolvedValue(null);
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    vi.restoreAllMocks();
    container.remove();
  });

  const renderDialog = async ({
    inferenceURL = "http://127.0.0.1:8317",
    conversionEngine,
    attentionFirst = false,
  }: {
    inferenceURL?: string;
    conversionEngine?: ConversionEngineCapability | null;
    attentionFirst?: boolean;
  } = {}) => {
    await act(async () => {
      reactRoot.render(
        <ClientSetupDialog
          attentionFirst={attentionFirst}
          conversionEngine={conversionEngine}
          inferenceURL={inferenceURL}
          onChanged={onChanged}
          onClose={onClose}
          token={token}
          tokens={[token, otherToken]}
        />,
      );
      await Promise.resolve();
    });
    await settle();
  };

  it("lists every client and checks what this token configures or nobody set up", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({
        claude: ours("configured", { model: "claude-sonnet-5" }),
        codex: ours("outdated", { model: "gpt-5.6" }),
        opencode: ours("modified", { model: "gpt-5.6" }),
        openclaw: { detected: false },
        pi: {
          state: "configured",
          token_id: otherToken.id,
          models: { model: "gpt-5.6" },
        },
      }),
    );
    await renderDialog();

    expect(bridgeMocks.getClientConfigStatus).toHaveBeenCalledWith(
      "http://127.0.0.1:8317",
      catalog,
    );
    expect(
      [...dialog().querySelectorAll("[data-client]")].map((item) =>
        item.getAttribute("data-client"),
      ),
    ).toEqual(["claude", "codex", "gemini", "opencode", "pi"]);
    expect(checked("Claude Code")).toBe("true");
    expect(checked("Codex")).toBe("true");
    expect(checked("Gemini CLI")).toBe("true");
    // An edited config and another token keep what they have until the user
    // checks.
    expect(checked("OpenCode")).toBe("false");
    expect(checked("Pi")).toBe("false");
    expect(row("pi").textContent).toContain("正在使用「家里」");
    expect(row("pi").textContent).toContain("其他令牌");
    expect(row("codex").textContent).toContain("端口待更新");
    expect(row("gemini").textContent).toContain(
      "未配置 · 将写入 claude-sonnet-5",
    );
    // A client that is not installed waits in a folded group and names the
    // folder it looked for.
    expect(groupHeader("未安装").getAttribute("aria-expanded")).toBe("false");
    expect(groupHeader("未安装").textContent).toBe("未安装1");
    await click(groupHeader("未安装"));
    expect(box("OpenClaw").disabled).toBe(true);
    expect(row("openclaw").textContent).toContain("本机未找到 ~/.openclaw");
    expect(dialog().textContent).toContain(
      "将写入 Claude Code、Codex、Gemini CLI",
    );
    expect(button("写入 3 个客户端").disabled).toBe(false);
    expect(dialog().textContent).toContain("API 地址 http://127.0.0.1:8317");
  });

  it("writes every checked client at once and shows each result", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({
        claude: ours("configured", { model: "claude-sonnet-5" }),
        codex: ours("outdated", { model: "gpt-5.6" }),
        opencode: { detected: false },
        openclaw: { detected: false },
      }),
    );
    bridgeMocks.applyClientConfigs.mockImplementation(async ({ items }) =>
      done(items, { pi: "invalid JSON" }),
    );
    await renderDialog();

    await click(button("写入 4 个客户端"));
    expect(bridgeMocks.applyClientConfigs).toHaveBeenCalledOnce();
    const call = bridgeMocks.applyClientConfigs.mock.calls[0][0];
    expect(call).toMatchObject({
      tokenId: token.id,
      inferenceUrl: "http://127.0.0.1:8317",
      confirmed: false,
    });
    expect(call.items).toEqual([
      {
        client: "claude",
        wsl: null,
        target: null,
        action: "write",
        models: { model: "claude-sonnet-5" },
      },
      {
        client: "codex",
        wsl: null,
        target: null,
        action: "write",
        models: { model: "gpt-5.6" },
      },
      {
        client: "gemini",
        wsl: null,
        target: null,
        action: "write",
        models: { model: "claude-sonnet-5" },
      },
      {
        client: "pi",
        wsl: null,
        target: null,
        action: "write",
        models: {
          model: "claude-sonnet-5",
          models: [
            "claude-sonnet-5",
            "claude-opus-5-5",
            "gpt-5.6",
            "vendor/glm-5",
          ],
        },
      },
    ]);
    expect(onChanged).toHaveBeenCalledOnce();
    expect(row("claude").textContent).toContain("已更新");
    expect(row("codex").textContent).toContain("已更新端口");
    expect(row("gemini").textContent).toContain("已写入 · claude-sonnet-5");
    expect(row("pi").textContent).toContain("settings.json 格式有误");
    expect(row("pi").querySelector('[aria-label="失败"]')).not.toBeNull();
    expect(dialog().textContent).toContain(
      "已写入 3 个客户端，重新打开后生效。Pi 未写入。",
    );

    // The failed client opens with what went wrong; only it is written again.
    await open("pi");
    expect(pane().textContent).toContain(
      "~/.pi/agent/settings.json 第 12 行格式有误，两份文件都没有改动。修正后点「重新写入 Pi」。",
    );
    bridgeMocks.applyClientConfigs.mockImplementation(async ({ items }) =>
      done(items),
    );
    await click(button("重新写入 Pi"));
    expect(lastItems()).toEqual([
      expect.objectContaining({ client: "pi", action: "write" }),
    ]);
    expect(dialog().textContent).toContain(
      "已写入 4 个客户端，重新打开后生效。",
    );
    await click(button("完成"));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("confirms every replaced setting in one dialog before writing", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({
        claude: { detected: false },
        codex: { detected: false },
        openclaw: { detected: false },
        pi: { state: "configured", token_id: otherToken.id },
      }),
    );
    bridgeMocks.applyClientConfigs.mockResolvedValueOnce({
      status: "needs_confirmation",
      items: [
        {
          client: "gemini",
          wsl: null,
          dir: "/Users/me/.gemini",
          remove: false,
          keys: [
            {
              path: "/Users/me/.gemini/.env",
              key: "GEMINI_API_KEY",
              current: null,
            },
            {
              path: "/Users/me/.gemini/settings.json",
              key: "security.auth.selectedType",
              current: "oauth-personal",
            },
          ],
          gateway: null,
          token_id: null,
        },
        {
          client: "pi",
          wsl: null,
          dir: "/Users/me/.pi/agent",
          remove: false,
          keys: [],
          gateway: "http://localhost:18317",
          token_id: otherToken.id,
        },
      ],
    });
    await renderDialog();
    await click(box("Pi"));
    await click(button("写入 3 个客户端"));

    const prompt = confirmation();
    expect(prompt?.textContent).toContain("替换现有设置？");
    expect(prompt?.textContent).toContain(
      "以下设置不是 AstrLink 写入的。继续会替换它们；移除 AstrLink 的配置时，会恢复成现在的值。",
    );
    const groups = prompt?.querySelectorAll(
      '[data-slot="client-confirmation"]',
    );
    expect(groups).toHaveLength(2);
    expect(groups?.[0].textContent).toContain("~/.gemini");
    expect(groups?.[0].textContent).toContain(".env 里的 GEMINI_API_KEY");
    expect(groups?.[0].textContent).toContain(
      "settings.json 里的 security.auth.selectedType（现在是 oauth-personal）",
    );
    expect(groups?.[1].textContent).toContain(
      "改用「工作机」，「家里」不再用于 Pi",
    );
    expect(groups?.[1].textContent).toContain(
      "改连这个 AstrLink，http://localhost:18317 不再收到 Pi 的请求",
    );
    expect(prompt?.textContent).not.toContain("CC Switch");

    await click(button("替换并写入", prompt!));
    await settleClosedLayer();
    expect(bridgeMocks.applyClientConfigs).toHaveBeenCalledTimes(2);
    expect(bridgeMocks.applyClientConfigs.mock.lastCall?.[0]).toMatchObject({
      confirmed: true,
      items: bridgeMocks.applyClientConfigs.mock.calls[0][0].items,
    });
    expect(dialog().textContent).toContain("已写入 3 个客户端");
  });

  it("writes nothing when the confirmation is cancelled", async () => {
    bridgeMocks.applyClientConfigs.mockResolvedValue({
      status: "needs_confirmation",
      items: [
        {
          client: "claude",
          wsl: null,
          dir: "/Users/me/.claude",
          remove: false,
          keys: [
            {
              path: "/Users/me/.claude/settings.json",
              key: "env.ANTHROPIC_API_KEY",
              current: null,
            },
          ],
          gateway: null,
          token_id: null,
        },
      ],
    });
    await renderDialog();
    await click(button("写入 6 个客户端"));
    await click(button("取消", confirmation()!));
    await settleClosedLayer();
    expect(confirmation()).toBeNull();
    expect(bridgeMocks.applyClientConfigs).toHaveBeenCalledOnce();
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("removes a config this token wrote when the user unchecks it", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({
        claude: ours("configured", { model: "claude-sonnet-5" }),
        codex: { detected: false },
        gemini: { detected: false },
        opencode: { detected: false },
        openclaw: { detected: false },
        pi: { detected: false },
      }),
    );
    bridgeMocks.applyClientConfigs.mockImplementationOnce(
      async ({ items }) => ({
        status: "needs_confirmation",
        items: items.map((item: ClientConfigItem) => ({
          client: item.client,
          wsl: item.wsl,
          dir: "/Users/me/.claude",
          remove: true,
          keys: [],
          gateway: null,
          token_id: null,
        })),
      }),
    );
    await renderDialog();
    await click(box("Claude Code"));
    expect(row("claude").textContent).toContain("将移除配置");
    expect(dialog().textContent).toContain("将移除 Claude Code 的配置");

    await click(button("移除 1 个客户端的配置"));
    expect(lastItems()).toEqual([
      { client: "claude", wsl: null, target: null, action: "remove" },
    ]);
    const prompt = confirmation();
    expect(prompt?.textContent).toContain("移除 1 个客户端的配置？");
    expect(prompt?.textContent).toContain("写入时替换掉的设置会恢复成原来的值");
    expect(prompt?.textContent).toContain("移除 AstrLink 写入的配置");
    await click(button("移除", prompt!));
    await settleClosedLayer();
    expect(bridgeMocks.applyClientConfigs.mock.lastCall?.[0]).toMatchObject({
      confirmed: true,
      items: [{ client: "claude", wsl: null, target: null, action: "remove" }],
    });
    expect(row("claude").textContent).toContain("已移除");
  });

  it("re-reads every client when the user picks another token", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({
        pi: {
          state: "configured",
          token_id: otherToken.id,
          models: { model: "gpt-5.6" },
        },
      }),
    );
    await renderDialog();
    expect(checked("Pi")).toBe("false");
    expect(bridgeMocks.getClientConfigStatus).toHaveBeenCalledOnce();

    await chooseOption(
      document.querySelector<HTMLElement>("#client-setup-token")!,
      "家里",
    );
    expect(bridgeMocks.getClientConfigStatus).toHaveBeenCalledTimes(2);
    // Pi already uses the picked token, so it is checked and kept.
    expect(checked("Pi")).toBe("true");
    expect(row("pi").textContent).toContain("已配置");
    await click(button("写入 6 个客户端"));
    expect(bridgeMocks.applyClientConfigs.mock.lastCall?.[0].tokenId).toBe(
      otherToken.id,
    );
  });

  it("opens on what needs attention, listed first and checked", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({
        claude: ours("configured", { model: "claude-sonnet-5" }),
        opencode: ours("configured", {
          model: "gpt-5.6",
          models: ["gpt-5.6", "retired-model"],
        }),
        openclaw: ours("modified", { model: "gpt-5.6" }),
      }),
    );
    await renderDialog({ attentionFirst: true });

    const order = [...dialog().querySelectorAll("[data-client]")].map((item) =>
      item.getAttribute("data-client"),
    );
    expect(order.slice(0, 2)).toEqual(["opencode", "openclaw"]);
    expect(checked("OpenClaw")).toBe("true");
    expect(row("opencode").textContent).toContain("1 个模型已停用");
    expect(pane().querySelector("h3")?.textContent).toBe("OpenCode");
    expect(pane().textContent).toContain(
      "retired-model 已停用，写入时不再使用。",
    );
  });

  it("enables a missing entry once per provider before writing", async () => {
    const fresh = {
      ...chatOnly,
      capabilities: [
        ...chatOnly.capabilities,
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
    } as Service;
    bridgeMocks.listServices.mockResolvedValue({ items: [chatOnly] });
    bridgeMocks.getService.mockResolvedValue({
      service: fresh,
      etag: '"fresh-etag"',
    });
    bridgeMocks.updateService.mockImplementation(async (_id, _etag, patch) => ({
      service: { ...fresh, ...patch },
      etag: '"updated-etag"',
    }));
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({
        gemini: { detected: false },
        opencode: { detected: false },
        openclaw: { detected: false },
        pi: { detected: false },
      }),
    );
    await renderDialog({ conversionEngine: engine });
    await open("codex");
    expect(pane().textContent).toContain(
      "Chat provider 尚未开启 OpenAI Responses 入口，写入配置时会自动开启（转换为 OpenAI Chat Completions）。",
    );
    expect(bridgeMocks.updateService).not.toHaveBeenCalled();

    await click(button("写入 2 个客户端"));
    // One patch adds both clients' entries and keeps the concurrent edit.
    expect(bridgeMocks.updateService).toHaveBeenCalledOnce();
    expect(bridgeMocks.updateService).toHaveBeenCalledWith(
      chatOnly.id,
      '"fresh-etag"',
      {
        capabilities: [
          ...fresh.capabilities,
          {
            protocol: "anthropic.messages",
            mode: "native",
            streaming: true,
            convert_to: "openai.chat",
          },
          {
            protocol: "openai.responses",
            mode: "native",
            streaming: true,
            convert_to: "openai.chat",
          },
        ],
      },
    );
    expect(bridgeMocks.updateService.mock.invocationCallOrder[0]).toBeLessThan(
      bridgeMocks.applyClientConfigs.mock.invocationCallOrder[0],
    );
  });

  it("stops before writing when enabling an entry fails", async () => {
    bridgeMocks.listServices.mockResolvedValue({ items: [chatOnly] });
    bridgeMocks.getService.mockResolvedValue({
      service: chatOnly,
      etag: '"fresh-etag"',
    });
    bridgeMocks.updateService.mockRejectedValue(new Error("ETag conflict"));
    await renderDialog({ conversionEngine: engine });
    await click(button("写入 6 个客户端"));
    expect(dialog().textContent).toContain("无法为 Chat provider 开启");
    expect(dialog().textContent).toContain("ETag conflict");
    expect(bridgeMocks.applyClientConfigs).not.toHaveBeenCalled();
  });

  it("writes the models a list client switches between", async () => {
    await renderDialog({ conversionEngine: engine });
    await open("opencode");
    const list = pane().querySelector<HTMLElement>(
      '[data-slot="model-checklist"]',
    );
    expect(list?.getAttribute("aria-label")).toBe("在 OpenCode 里可切换的模型");
    expect(list?.textContent).toContain("已选 4 / 4");
    expect(row("opencode").textContent).toContain("未配置 · 将写入 4 个模型");
    // The default model stays in the list.
    expect(
      list?.querySelector<HTMLButtonElement>('[aria-label="claude-sonnet-5"]')
        ?.disabled,
    ).toBe(true);
    await click(
      list!.querySelector<HTMLElement>('[aria-label="vendor/glm-5"]')!,
    );
    expect(list?.textContent).toContain("已选 3 / 4");
    await click(button("写入 6 个客户端"));
    expect(
      lastItems().find((item) => item.client === "opencode")?.models,
    ).toEqual({
      model: "claude-sonnet-5",
      models: ["claude-sonnet-5", "claude-opus-5-5", "gpt-5.6"],
    });
  });

  it("offers Gemini CLI only models its URL path can carry", async () => {
    await renderDialog();
    await open("gemini");
    const input = document.querySelector<HTMLInputElement>(
      "#client-setup-model",
    )!;
    await act(async () => input.click());
    const options = [...document.querySelectorAll('[role="option"]')].map(
      (option) => option.getAttribute("aria-label"),
    );
    expect(options).not.toContain("vendor/glm-5");
    expect(options).toContain("gpt-5.6");
  });

  it("shows a place's changes before writing", async () => {
    bridgeMocks.previewClientConfigChanges.mockResolvedValue({
      files: [
        {
          path: "/Users/me/.gemini/.env",
          created: false,
          deleted: false,
          changes: 3,
          unit: "line",
          lines: [
            { kind: "context", text: "# Gemini CLI" },
            { kind: "remove", text: "GEMINI_API_KEY=AIza…k2Q" },
            { kind: "add", text: "GEMINI_API_KEY=astr_…K8Q2" },
          ],
        },
      ],
      replaced: 2,
      restored: 0,
    });
    await renderDialog();
    await open("gemini");
    await click(button("改动"));
    expect(bridgeMocks.previewClientConfigChanges).toHaveBeenCalledWith({
      tokenId: token.id,
      client: "gemini",
      wsl: null,
      target: null,
      inferenceUrl: "http://127.0.0.1:8317",
      action: "write",
      models: { model: "claude-sonnet-5" },
    });
    const text = pane().textContent;
    expect(text).toContain("~/.gemini/.env");
    expect(text).toContain("修改 3 行");
    expect(text).toContain("- GEMINI_API_KEY=AIza…k2Q");
    expect(text).toContain("+ GEMINI_API_KEY=astr_…K8Q2");
    expect(text).toContain("标红的 2 项不是 AstrLink 写入的");

    // Unchecked, it lists nothing.
    await click(box("Gemini CLI"));
    expect(pane().textContent).toContain(
      "勾选 Gemini CLI 后，这里会列出写入时的改动。",
    );
  });

  it("offers the reasoning efforts of Codex's model", async () => {
    pricingMocks.getOfficialModelLimits.mockImplementation(async (model) =>
      model === "gpt-5.6"
        ? {
            contextTokens: 400000,
            outputTokens: 128000,
            reasoningLevels: ["low", "medium", "high"],
          }
        : null,
    );
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({ codex: ours("configured", { model: "gpt-5.6" }) }),
    );
    await renderDialog();
    await open("codex");
    await chooseOption(
      document.querySelector<HTMLElement>("#client-setup-reasoningEffort")!,
      "high",
    );
    await click(button("写入 6 个客户端"));
    expect(lastItems().find((item) => item.client === "codex")?.models).toEqual(
      {
        model: "gpt-5.6",
        reasoningEffort: "high",
      },
    );
  });

  it("fills Pi's limits from the official model and writes what the user keeps", async () => {
    pricingMocks.getOfficialModelLimits.mockImplementation(async (model) =>
      model === "gpt-5.6"
        ? { contextTokens: 1050000, outputTokens: 128000 }
        : null,
    );
    await renderDialog();
    await open("pi");
    const field = (key: string) =>
      document.querySelector<HTMLInputElement>(`#client-setup-${key}`)!;
    expect(field("contextWindow").placeholder).toContain("128000");

    await setInput("#client-setup-model", "gpt-5.6");
    expect(field("contextWindow").value).toBe("1050000");
    expect(field("maxTokens").value).toBe("128000");

    await setInput("#client-setup-contextWindow", "400000");
    await setInput("#client-setup-maxTokens", "");
    await click(button("写入 6 个客户端"));
    expect(
      lastItems().find((item) => item.client === "pi")?.models,
    ).toMatchObject({ model: "gpt-5.6", contextWindow: 400000 });
    expect(
      lastItems().find((item) => item.client === "pi")?.models,
    ).not.toHaveProperty("maxTokens");

    // A number Pi cannot take blocks the write.
    await setInput("#client-setup-maxTokens", "12k");
    expect(pane().textContent).toContain("1 到 100000000");
    expect(button("写入 6 个客户端").disabled).toBe(true);
    expect(dialog().textContent).toContain("先填好 Pi 的设置");
  });

  it("removes one place's config from the pane after confirmation", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({ codex: ours("configured", { model: "gpt-5.6" }) }),
    );
    bridgeMocks.removeClientConfig.mockResolvedValue(undefined);
    await renderDialog();
    await open("codex");
    await click(button("移除", pane()));
    const prompt = confirmation();
    expect(prompt?.textContent).toContain("移除 Codex 中的配置？");
    expect(prompt?.textContent).toContain("写入时替换掉的设置会恢复成原来的值");
    await click(button("移除", prompt!));
    await settleClosedLayer();
    expect(bridgeMocks.removeClientConfig).toHaveBeenCalledWith(
      "codex",
      null,
      null,
    );
    expect(onChanged).toHaveBeenCalledOnce();
    // Removed on purpose, so the next write leaves it out.
    expect(checked("Codex")).toBe("false");
  });

  it("lists WSL places under their client and explains the WSL network", async () => {
    const wsl = "Ubuntu-24.04";
    bridgeMocks.getClientConfigStatus.mockResolvedValue({
      ...overview({ codex: { detected: false } }),
      clients: [
        ...overview({ codex: { detected: false } }).clients,
        ...clientConfigClients.map((client) =>
          place(client, { detected: client === "codex" }, wsl),
        ),
      ],
      wsl_unchecked: ["Debian"],
    });
    await renderDialog();

    const codex = row("codex").closest("li")!;
    const places = [...codex.querySelectorAll("[data-place]")].map((item) =>
      item.getAttribute("data-place"),
    );
    expect(places).toEqual(["codex:", `codex:${wsl}`]);
    expect(checked(`Codex（WSL · ${wsl}）`)).toBe("true");
    expect(checked("Codex")).toBe("true");
    expect(dialog().textContent).toContain("Debian 未运行");

    await click(
      codex.querySelector<HTMLElement>(
        `[data-place="codex:${wsl}"] button:not([role="checkbox"])`,
      )!,
    );
    expect(pane().querySelector("h3")?.textContent).toBe(
      `Codex（WSL · ${wsl}）`,
    );
    expect(pane().textContent).toContain("networkingMode=mirrored");
    expect(pane().textContent).toContain("http://127.0.0.1:8317/v1");
  });

  it("notes in the confirmation that CC Switch can overwrite the write", async () => {
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(true);
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      overview({
        codex: { detected: false },
        gemini: { detected: false },
        opencode: { detected: false },
        openclaw: { detected: false },
        pi: { detected: false },
      }),
    );
    bridgeMocks.applyClientConfigs.mockResolvedValueOnce({
      status: "needs_confirmation",
      items: [
        {
          client: "claude",
          wsl: null,
          dir: "/Users/me/.claude",
          remove: false,
          keys: [
            {
              path: "/Users/me/.claude/settings.json",
              key: "env.ANTHROPIC_BASE_URL",
              current: "https://api.example.com",
            },
          ],
          gateway: null,
          token_id: null,
        },
      ],
    });
    await renderDialog();
    // The client CC Switch wrote reads like any other config.
    expect(pane().textContent).not.toContain("CC Switch");
    await click(button("写入 1 个客户端"));
    expect(confirmation()?.textContent).toContain(
      "在 CC Switch 中切换供应商会覆盖这里写入的配置。",
    );
  });

  it("warns in the Codex pane when the system proxy blocks it", async () => {
    bridgeMocks.checkClientProxy.mockResolvedValue({
      client: { route: "blocked", proxy: "127.0.0.1:7892" },
      numeric: null,
    });
    await renderDialog({ inferenceURL: "http://localhost:8317" });
    expect(bridgeMocks.checkClientProxy).not.toHaveBeenCalled();

    await open("codex");
    expect(bridgeMocks.checkClientProxy).toHaveBeenCalledWith(
      "http://localhost:8317",
    );
    expect(pane().textContent).toContain(
      "系统代理 127.0.0.1:7892 会拦下 Codex 发往 AstrLink 的请求",
    );
    await open("claude");
    expect(pane().textContent).not.toContain("系统代理");
  });

  it("reports a failed status read", async () => {
    bridgeMocks.getClientConfigStatus.mockRejectedValue(new Error("offline"));
    await renderDialog();
    expect(dialog().textContent).toContain("无法读取客户端的配置状态");
    expect(button("写入配置").disabled).toBe(true);
  });
  describe("with a catalog beyond the six", () => {
    const workbuddy: ClientCatalogEntry = {
      id: "workbuddy",
      name: "WorkBuddy",
      kind: "file",
      targets: ["cn", "intl"],
      protocols: ["openai.chat", "openai.responses"],
      list: true,
    };
    const missingSix = Object.fromEntries(
      clientConfigClients.map((client) => [client, { detected: false }]),
    );

    function targetPlace(
      target: string,
      overrides: Partial<ClientConfigStatus> = {},
    ): ClientConfigStatus {
      const dir = `/Users/me/${target === "cn" ? ".workbuddy" : ".workbuddy-ai"}`;
      return {
        client: "workbuddy",
        wsl: null,
        target,
        detected: true,
        home: "/Users/me",
        dir,
        paths: [`${dir}/models.json`],
        exists: [false],
        state: "not_configured",
        token_id: null,
        models: {},
        notes: [],
        paste_since_unix: null,
        paste_into: [],
        ...overrides,
      };
    }

    const railRows = () =>
      [...dialog().querySelectorAll("[data-client]")].map((item) =>
        item.getAttribute("data-client"),
      );

    async function search(query: string) {
      await setInput('input[aria-label="搜索客户端"]', query);
    }

    it("groups clients by what AstrLink can do and checks only installed ones", async () => {
      bridgeMocks.getClientCatalog.mockResolvedValue({
        clients: [...builtinEntries, ...listedEntries],
      });
      bridgeMocks.getClientConfigStatus.mockResolvedValue(
        overview({
          opencode: { detected: false },
          openclaw: { detected: false },
          pi: { detected: false },
        }),
      );
      await renderDialog();

      expect(
        [...dialog().querySelectorAll("[data-group]")].map((item) =>
          item.getAttribute("data-group"),
        ),
      ).toEqual(["installed", "missing", "guide", "unsupported"]);
      expect(groupHeader("本机已安装").textContent).toBe("本机已安装3");
      expect(groupHeader("本机已安装").getAttribute("aria-expanded")).toBe(
        "true",
      );
      for (const [title, count] of [
        ["未安装", 4],
        ["在客户端里填写", 1],
        ["接不了本机网关", 1],
      ] as const) {
        expect(groupHeader(title).textContent).toBe(`${title}${count}`);
        expect(groupHeader(title).getAttribute("aria-expanded")).toBe("false");
      }
      expect(railRows()).toEqual(["claude", "codex", "gemini"]);

      // 全不选 and 全选 reach only the installed clients.
      await click(button("全不选"));
      expect(checked("Claude Code")).toBe("false");
      expect(checked("Gemini CLI")).toBe("false");
      expect(dialog().textContent).toContain("勾选要写入的客户端");
      await click(groupHeader("未安装"));
      await click(button("全选"));
      expect(checked("Claude Code")).toBe("true");
      expect(checked("Codex")).toBe("true");
      expect(checked("Pi")).toBe("false");
      expect(box("Pi").disabled).toBe(true);
      // Only clients AstrLink writes count toward the write.
      expect(button("写入 3 个客户端").disabled).toBe(false);
    });

    it("searches every group and opens the groups with matches", async () => {
      bridgeMocks.getClientCatalog.mockResolvedValue({
        clients: [...builtinEntries, ...listedEntries],
      });
      bridgeMocks.getClientConfigStatus.mockResolvedValue(
        overview({ pi: { detected: false } }),
      );
      await renderDialog();

      await search("cur");
      expect(railRows()).toEqual(["cursor"]);
      expect(groupHeader("接不了本机网关").textContent).toBe("接不了本机网关1");
      expect(dialog().querySelector('[data-group="installed"]')).toBeNull();

      // Spaces, case and separators do not matter.
      await search("claudecode");
      expect(railRows()).toEqual(["claude"]);
      await search("P");
      expect(railRows()).toEqual(["opencode", "openclaw", "pi"]);
      expect(groupHeader("未安装").getAttribute("aria-expanded")).toBe("true");

      // 全选 reaches only the installed clients the search shows.
      await search("codex");
      await click(button("全不选"));
      await search("");
      expect(checked("Codex")).toBe("false");
      expect(checked("Claude Code")).toBe("true");

      await search("zzz");
      expect(railRows()).toEqual([]);
      expect(dialog().textContent).toContain("没有匹配的客户端");

      // Clearing the search folds the groups back.
      await click(
        dialog().querySelector<HTMLButtonElement>(
          'button[aria-label="清除搜索"]',
        )!,
      );
      expect(railRows()).toEqual([
        "claude",
        "codex",
        "gemini",
        "opencode",
        "openclaw",
      ]);
    });

    it("writes a client with two targets once per checked target", async () => {
      bridgeMocks.getClientCatalog.mockResolvedValue({
        clients: [...builtinEntries, workbuddy],
      });
      bridgeMocks.getClientConfigStatus.mockResolvedValue({
        ...overview(missingSix),
        clients: [
          ...overview(missingSix).clients,
          targetPlace("cn"),
          targetPlace("intl", { detected: false }),
        ],
      });
      await renderDialog();

      expect(railRows()).toEqual(["workbuddy"]);
      expect(row("workbuddy").textContent).toContain("未配置 · 国内版");
      const text = pane().textContent;
      expect(text).toContain("写入位置");
      expect(text).toContain("WorkBuddy 要登录后才能用自定义模型");
      const intl = pane().querySelector<HTMLElement>(
        '[data-place="workbuddy::intl"]',
      )!;
      expect(intl.textContent).toContain("~/.workbuddy-ai/models.json");
      expect(intl.textContent).toContain("未安装");
      expect(intl.className).toContain("opacity-55");
      expect(checked("WorkBuddy（国内版）")).toBe("true");
      expect(box("WorkBuddy（国际版）").disabled).toBe(true);

      await click(button("写入 1 个客户端"));
      expect(lastItems()).toEqual([
        {
          client: "workbuddy",
          wsl: null,
          target: "cn",
          action: "write",
          models: {
            model: "claude-sonnet-5",
            models: [
              "claude-sonnet-5",
              "claude-opus-5-5",
              "gpt-5.6",
              "vendor/glm-5",
            ],
            // Each model takes the protocol the gateway serves it with best.
            modelProtocols: {
              "claude-sonnet-5": "openai.chat",
              "claude-opus-5-5": "openai.chat",
              "gpt-5.6": "openai.responses",
              "vendor/glm-5": "openai.chat",
            },
          },
        },
      ]);
      expect(dialog().textContent).toContain("已写入 1 个客户端");
    });

    it("previews each checked target under its name", async () => {
      bridgeMocks.getClientCatalog.mockResolvedValue({
        clients: [...builtinEntries, workbuddy],
      });
      bridgeMocks.getClientConfigStatus.mockResolvedValue({
        ...overview(missingSix),
        clients: [
          ...overview(missingSix).clients,
          targetPlace("cn"),
          targetPlace("intl"),
        ],
      });
      await renderDialog();
      expect(row("workbuddy").textContent).toContain("未配置 · 国内版、国际版");

      await click(button("改动"));
      expect(bridgeMocks.previewClientConfigChanges).toHaveBeenCalledTimes(2);
      expect(
        bridgeMocks.previewClientConfigChanges.mock.calls.map(
          ([request]) => request.target,
        ),
      ).toEqual(["cn", "intl"]);
      expect(
        [...pane().querySelectorAll("section[aria-label]")].map((item) =>
          item.getAttribute("aria-label"),
        ),
      ).toEqual(["WorkBuddy（国内版）", "WorkBuddy（国际版）"]);
    });

    it("lists what to fill in a guide client and copies the token through the host", async () => {
      pricingMocks.getOfficialModelLimits.mockImplementation(async (model) =>
        model === "claude-sonnet-5"
          ? { contextTokens: 1000000, outputTokens: 64000 }
          : null,
      );
      bridgeMocks.getClientCatalog.mockResolvedValue({
        clients: [...builtinEntries, ...listedEntries],
      });
      const writeText = vi
        .spyOn(navigator.clipboard, "writeText")
        .mockResolvedValue(undefined);
      await renderDialog();

      await click(groupHeader("在客户端里填写"));
      expect(row("trae").querySelector('[role="checkbox"]')).toBeNull();
      expect(row("trae").textContent).toContain("模型存在 TRAE 的服务器上");
      await open("trae");
      expect(pane().textContent).toContain(
        "在 TRAE 的「设置 → 模型 → 添加模型 → 自定义模型」里填写：",
      );
      const field = (key: string) =>
        pane().querySelector<HTMLElement>(`[data-field="${key}"]`);
      expect(field("type")?.textContent).toContain("Anthropic 兼容");
      expect(field("type")?.querySelector("button")).toBeNull();
      expect(field("url")?.textContent).toContain("http://127.0.0.1:8317");
      expect(field("url")?.textContent).not.toContain("/v1");
      expect(field("key")?.textContent).toContain(token.hint);
      expect(field("model")?.textContent).toContain("claude-sonnet-5");
      expect(field("context")?.textContent).toContain("1000000");
      expect(pane().textContent).toContain(
        "模型存在 TRAE 的服务器上，AstrLink 改不了。端口或令牌变了需要回来重新填。",
      );

      await click(field("key")!.querySelector("button")!);
      expect(bridgeMocks.copyAccessToken).toHaveBeenCalledWith(token.id);
      expect(writeText).not.toHaveBeenCalled();
      await click(field("url")!.querySelector("button")!);
      expect(writeText).toHaveBeenCalledWith("http://127.0.0.1:8317");

      // The type and address follow the chosen model's protocol.
      await setInput("#client-setup-guide-model", "gpt-5.6");
      expect(field("type")?.textContent).toContain("OpenAI Responses 兼容");
      expect(field("url")?.textContent).toContain("http://127.0.0.1:8317/v1");
      expect(field("context")).toBeNull();
      // A guide client is not written.
      expect(dialog().textContent).toContain(
        "将写入 Claude Code、Codex、Gemini CLI、OpenCode、OpenClaw、Pi",
      );
    });

    it("opens an import link through the host with the chosen protocol", async () => {
      bridgeMocks.getClientCatalog.mockResolvedValue({
        clients: [...builtinEntries, ...listedEntries],
      });
      bridgeMocks.getClientConfigStatus.mockResolvedValue(
        overview({}, { installed: ["cherry_studio"] }),
      );
      bridgeMocks.openClientImportLink.mockResolvedValue(undefined);
      await renderDialog();

      expect(
        row("cherry_studio").querySelector('[role="checkbox"]'),
      ).toBeNull();
      await open("cherry_studio");
      expect(pane().getAttribute("data-kind")).toBe("link");
      expect(pane().textContent).toContain(
        "在服务商列表里打开「AstrLink」的开关。",
      );
      expect(pane().textContent).not.toContain("没检测到");
      const chips = [
        ...pane().querySelectorAll<HTMLElement>('ul[aria-label="模型 ID"] li'),
      ].map((item) => item.textContent);
      expect(chips).toEqual([
        "claude-sonnet-5",
        "claude-opus-5-5",
        "gpt-5.6",
        "vendor/glm-5",
      ]);

      await chooseOption(
        pane().querySelector<HTMLElement>("#client-setup-link-protocol")!,
        "Anthropic 兼容",
      );
      expect(pane().textContent).toContain(
        "在服务商列表里打开「AstrLink Anthropic」的开关。",
      );
      await click(button("在 Cherry Studio 中导入"));
      expect(bridgeMocks.openClientImportLink).toHaveBeenCalledWith({
        tokenId: token.id,
        client: "cherry_studio",
        protocol: "anthropic.messages",
        inferenceUrl: "http://127.0.0.1:8317",
      });
      // A link client is not written.
      expect(dialog().textContent).toContain(
        "将写入 Claude Code、Codex、Gemini CLI、OpenCode、OpenClaw、Pi",
      );
    });

    it("waits until a paste client sends a request with the token", async () => {
      const zed: ClientCatalogEntry = {
        id: "zed",
        name: "Zed",
        kind: "paste",
        targets: [],
        protocols: ["openai.chat", "openai.responses", "anthropic.messages"],
        list: true,
      };
      bridgeMocks.getClientCatalog.mockResolvedValue({
        clients: [...builtinEntries, zed],
      });
      const written: ClientConfigStatus = {
        ...place("claude", ours("configured", { model: "gpt-5.6" })),
        client: "zed",
        dir: "/Users/me/.config/zed",
        paths: ["/Users/me/.config/zed/settings.json"],
        exists: [true],
        paste_since_unix: 1_790_000_000,
        paste_into: ["AstrLink", "AstrLink Anthropic"],
      };
      const statuses = overview();
      bridgeMocks.getClientConfigStatus.mockResolvedValue({
        ...statuses,
        clients: [...statuses.clients, written],
      });
      await renderDialog();

      expect(bridgeMocks.listRequestRecords).toHaveBeenCalledWith({
        local_access_token_ids: [token.id],
        client_type: "zed",
        from: "2026-09-21T14:13:20.000Z",
        limit: 1,
      });
      expect(row("zed").textContent).toContain("差一步");
      expect(row("zed").textContent).toContain("已写入 · 等你粘贴令牌");
      await open("zed");
      expect(pane().textContent).toContain(
        "在 Zed 的「Settings → AI → LLM Providers」里粘贴到 AstrLink、AstrLink Anthropic。",
      );
      await click(button("复制令牌"));
      expect(bridgeMocks.copyAccessToken).toHaveBeenCalledWith(token.id);
      expect(button("令牌已复制")).toBeTruthy();

      // Once the gateway records a request from Zed, the wait is over.
      bridgeMocks.listRequestRecords.mockResolvedValue({
        items: [{ id: "request_01" }],
        next_cursor: null,
      });
      await act(async () => reactRoot.unmount());
      reactRoot = createRoot(container);
      await renderDialog();
      expect(row("zed").textContent).not.toContain("差一步");
      expect(row("zed").textContent).toContain("已配置");
    });

    it("tells only why a client cannot reach the gateway", async () => {
      bridgeMocks.getClientCatalog.mockResolvedValue({
        clients: [...builtinEntries, ...listedEntries],
      });
      await renderDialog();

      await click(groupHeader("接不了本机网关"));
      const cursor = row("cursor");
      expect(cursor.className).toContain("opacity-55");
      expect(cursor.querySelector('[role="checkbox"]')).toBeNull();
      expect(cursor.textContent).toContain("请求从 Cursor 的服务器发出");
      await open("cursor");
      expect(pane().getAttribute("data-kind")).toBe("unsupported");
      expect(pane().querySelector("h3")?.textContent).toBe("Cursor");
      expect(
        [...pane().querySelectorAll("p")].map((item) => item.textContent),
      ).toEqual(["请求从 Cursor 的服务器发出，接不了这台电脑上的 AstrLink。"]);
      expect(pane().querySelector("button")).toBeNull();
    });
  });
});
