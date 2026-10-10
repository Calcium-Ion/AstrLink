// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  createAccessToken: vi.fn(),
  deleteAccessToken: vi.fn(),
  listAccessTokenUsage: vi.fn(),
  copyAccessToken: vi.fn(),
  getClientConfigStatus: vi.fn(),
  removeClientConfig: vi.fn(),
  applyClientConfig: vi.fn(),
  isCCSwitchInstalled: vi.fn(),
  openCCSwitchImport: vi.fn(),
  listServices: vi.fn(),
  getRoutingSettings: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import {
  AccessTokenManager,
  type AccessTokenCatalog,
} from "./AccessTokenManager";
import type { AccessTokenSummary } from "./access-token-model";
import type {
  ClientConfigOverview,
  ClientConfigStatus,
} from "./client-config-model";
import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";

const firstToken: AccessTokenSummary = {
  id: "token_01",
  name: "VS Code",
  hint: "astr_…K8Q2",
  created_at: "2026-07-24T10:30:00Z",
};

const secondToken: AccessTokenSummary = {
  id: "token_02",
  name: "Terminal",
  hint: "astr_…7HT4",
  created_at: "2026-07-24T10:31:00Z",
};

const firstSecret = `astr_${"A".repeat(43)}`;

function clientStatuses(
  claudeToken: string | null,
  codexToken: string | null = null,
  wsl: ClientConfigStatus[] = [],
): ClientConfigOverview {
  const clients: ClientConfigStatus[] = [
    {
      client: "claude",
      wsl: null,
      detected: true,
      paths: ["/Users/me/.claude/settings.json"],
      state: claudeToken ? "configured" : "not_configured",
      token_id: claudeToken,
    },
    {
      client: "codex",
      wsl: null,
      detected: true,
      paths: ["/Users/me/.codex/config.toml"],
      state: codexToken ? "modified" : "not_configured",
      token_id: codexToken,
    },
  ];
  return {
    clients: [...clients, ...wsl],
    wsl_unchecked: [],
    wsl_localhost: false,
  };
}

function readyCatalog(items: AccessTokenSummary[]): AccessTokenCatalog {
  return {
    status: "ready",
    items,
    error: null,
    stale: false,
  };
}

function button(label: string, root: ParentNode = document): HTMLButtonElement {
  const match = [...root.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

function row(name: string): HTMLElement {
  const match = [
    ...document.querySelectorAll<HTMLElement>(
      '[data-testid="access-token-row"]',
    ),
  ].find((candidate) => candidate.textContent?.includes(name));
  if (!match) throw new Error(`Missing token row: ${name}`);
  return match;
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

describe("AccessTokenManager", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    window.confirm = vi.fn(() => true);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    bridgeMocks.listAccessTokenUsage.mockResolvedValue({ items: [] });
    bridgeMocks.getClientConfigStatus.mockResolvedValue(clientStatuses(null));
    bridgeMocks.isCCSwitchInstalled.mockResolvedValue(false);
    bridgeMocks.listServices.mockResolvedValue({ items: [] });
    bridgeMocks.getRoutingSettings.mockResolvedValue({ model_redirects: [] });
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    vi.restoreAllMocks();
    container.remove();
  });

  const renderManager = async (
    catalog: AccessTokenCatalog,
    session = "session-1",
  ) => {
    await act(async () => {
      reactRoot.render(
        <AccessTokenManager
          catalog={catalog}
          coreSessionKey={session}
          inferenceURL="http://127.0.0.1:8317"
          isReady
          onRefresh={() => undefined}
          onTokenCreated={() => undefined}
          onTokenDeleted={() => undefined}
        />,
      );
      await Promise.resolve();
    });
  };

  it("has the host copy a token without verification and only keeps the latest copy", async () => {
    bridgeMocks.copyAccessToken
      .mockResolvedValueOnce(true)
      .mockResolvedValueOnce(true);
    await renderManager(readyCatalog([firstToken, secondToken]));

    await act(async () => {
      button("复制", row(firstToken.name)).click();
      await Promise.resolve();
    });
    expect(
      document.querySelector('[data-slot="proof-confirm-dialog"]'),
    ).toBeNull();
    expect(bridgeMocks.copyAccessToken).toHaveBeenCalledExactlyOnceWith(
      firstToken.id,
    );
    // The host writes the clipboard; the secret never reaches the webview.
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled();
    expect(button("已复制", row(firstToken.name))).toBeTruthy();

    await act(async () => {
      button("复制", row(secondToken.name)).click();
      await Promise.resolve();
    });
    expect(bridgeMocks.copyAccessToken).toHaveBeenLastCalledWith(
      secondToken.id,
    );
    expect(button("已复制", row(secondToken.name))).toBeTruthy();
    expect(button("复制", row(firstToken.name))).toBeTruthy();
  });

  it("asks for a manual copy when the host cannot write the clipboard", async () => {
    bridgeMocks.copyAccessToken.mockResolvedValueOnce(false);
    await renderManager(readyCatalog([firstToken]));

    await act(async () => {
      button("复制", row(firstToken.name)).click();
      await Promise.resolve();
    });
    expect(container.textContent).toContain("无法自动复制，请手动选择令牌。");
    expect(button("复制", row(firstToken.name)).disabled).toBe(false);
  });

  it("offers client setup for every token without CC Switch", async () => {
    await renderManager(readyCatalog([firstToken, secondToken]));
    expect(bridgeMocks.isCCSwitchInstalled).not.toHaveBeenCalled();
    expect(button("配置客户端", row(firstToken.name))).toBeTruthy();

    await act(async () => button("配置客户端", row(secondToken.name)).click());
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain("Terminal");
    expect(dialog?.textContent).toContain("astr_…7HT4");
    expect(dialog?.textContent).not.toContain(firstSecret);
    expect(bridgeMocks.getClientConfigStatus).toHaveBeenCalledWith(
      "http://127.0.0.1:8317",
    );
    expect(bridgeMocks.copyAccessToken).not.toHaveBeenCalled();
  });

  it("marks the clients each token is configured in and refreshes on focus", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      clientStatuses(firstToken.id),
    );
    await renderManager(readyCatalog([firstToken, secondToken]));
    const mark = row(firstToken.name).querySelector('[role="img"]');
    expect(mark?.getAttribute("aria-label")).toBe("已配置到 Claude Code");
    expect(mark?.querySelectorAll("svg")).toHaveLength(1);
    expect(row(secondToken.name).querySelector('[role="img"]')).toBeNull();

    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      clientStatuses(firstToken.id, secondToken.id),
    );
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
    });
    expect(
      row(secondToken.name)
        .querySelector('[role="img"]')
        ?.getAttribute("aria-label"),
    ).toBe("已配置到 Codex");

    bridgeMocks.getClientConfigStatus.mockRejectedValue(new Error("failed"));
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
    });
    expect(container.querySelector('[role="img"]')).toBeNull();
  });

  it("removes a deleted token's client configs unless asked to keep them", async () => {
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      clientStatuses(firstToken.id, firstToken.id, [
        {
          client: "claude",
          wsl: "Ubuntu",
          detected: true,
          paths: [String.raw`\\wsl$\Ubuntu\home\me\.claude\settings.json`],
          state: "configured",
          token_id: firstToken.id,
        },
      ]),
    );
    bridgeMocks.deleteAccessToken.mockResolvedValue(undefined);
    bridgeMocks.removeClientConfig
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error("unable to write config.toml"))
      .mockResolvedValueOnce(undefined);
    await renderManager(readyCatalog([firstToken, secondToken]));

    await act(async () => button("删除", row(firstToken.name)).click());
    const confirmation = document.querySelector('[role="alertdialog"]');
    expect(confirmation?.textContent).toContain(
      "以下客户端正在使用“VS Code”：Claude Code、Codex。",
    );
    const checkbox = confirmation?.querySelector('[role="checkbox"]');
    expect(checkbox?.getAttribute("aria-checked")).toBe("true");
    await act(async () => {
      button("确认删除").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deleteAccessToken).toHaveBeenCalledWith(firstToken.id);
    expect(bridgeMocks.removeClientConfig.mock.calls).toEqual([
      ["claude", null],
      ["codex", null],
      ["claude", "Ubuntu"],
    ]);

    bridgeMocks.removeClientConfig.mockClear();
    bridgeMocks.getClientConfigStatus.mockResolvedValue(
      clientStatuses(secondToken.id),
    );
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
    });
    await act(async () => button("删除", row(secondToken.name)).click());
    await act(async () =>
      document
        .querySelector<HTMLButtonElement>(
          '[role="alertdialog"] [role="checkbox"]',
        )!
        .click(),
    );
    await act(async () => {
      button("确认删除").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deleteAccessToken).toHaveBeenLastCalledWith(
      secondToken.id,
    );
    expect(bridgeMocks.removeClientConfig).not.toHaveBeenCalled();
  });

  it("closes client setup when the Core session changes and blocks stale tokens", async () => {
    await renderManager(readyCatalog([firstToken]));

    await act(async () => button("配置客户端", row(firstToken.name)).click());
    expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    await renderManager(
      { ...readyCatalog([firstToken]), stale: true },
      "session-2",
    );
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(button("配置客户端", row(firstToken.name)).disabled).toBe(true);
    expect(bridgeMocks.applyClientConfig).not.toHaveBeenCalled();
  });

  it("ignores a copy response from an old Core session", async () => {
    let resolveCopy: ((value: boolean) => void) | undefined;
    bridgeMocks.copyAccessToken.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveCopy = resolve;
      }),
    );
    await renderManager(readyCatalog([firstToken]));

    await act(async () => {
      button("复制", row(firstToken.name)).click();
      await Promise.resolve();
    });
    await renderManager(readyCatalog([firstToken]), "session-2");
    await act(async () => {
      resolveCopy?.(true);
      await Promise.resolve();
    });

    expect(button("复制", row(firstToken.name)).disabled).toBe(false);
  });

  it("cancels an in-flight copy before refreshing", async () => {
    const onRefresh = vi.fn();
    let resolveCopy: ((value: boolean) => void) | undefined;
    bridgeMocks.copyAccessToken.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveCopy = resolve;
      }),
    );
    await act(async () => {
      reactRoot.render(
        <AccessTokenManager
          catalog={readyCatalog([firstToken])}
          coreSessionKey="session-1"
          inferenceURL="http://127.0.0.1:8317"
          isReady
          onRefresh={onRefresh}
          onTokenCreated={() => undefined}
          onTokenDeleted={() => undefined}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      button("复制", row(firstToken.name)).click();
      await Promise.resolve();
    });

    await act(async () => button("刷新").click());
    await act(async () => {
      resolveCopy?.(true);
      await Promise.resolve();
    });

    expect(button("复制", row(firstToken.name))).toBeTruthy();
    expect(onRefresh).toHaveBeenCalledOnce();
  });

  it("does not render a newly created secret and still confirms deletion", async () => {
    let finishDelete: (() => void) | undefined;
    bridgeMocks.createAccessToken.mockResolvedValueOnce({
      token: firstToken,
      access_token: firstSecret,
    });
    bridgeMocks.deleteAccessToken.mockReturnValueOnce(
      new Promise<void>((resolve) => {
        finishDelete = resolve;
      }),
    );

    function Harness() {
      const [items, setItems] = useState<AccessTokenSummary[]>([]);
      return (
        <AccessTokenManager
          catalog={readyCatalog(items)}
          coreSessionKey="session-1"
          inferenceURL="http://127.0.0.1:8317"
          isReady
          onRefresh={() => undefined}
          onTokenCreated={(token) => setItems((current) => [token, ...current])}
          onTokenDeleted={(tokenId) =>
            setItems((current) =>
              current.filter((token) => token.id !== tokenId),
            )
          }
        />
      );
    }

    await act(async () => {
      reactRoot.render(<Harness />);
      await Promise.resolve();
    });
    await act(async () => button("创建令牌").click());
    await setInput("#access-token-name", "VS Code");
    await act(async () => {
      button("创建").click();
      await Promise.resolve();
    });
    expect(container.textContent).not.toContain(firstSecret);
    expect(
      container.querySelector('[data-testid="revealed-access-token"]'),
    ).toBeNull();

    await act(async () => {
      button("删除", row(firstToken.name)).click();
      await Promise.resolve();
    });

    expect(container.textContent).not.toContain(firstSecret);
    expect(bridgeMocks.deleteAccessToken).not.toHaveBeenCalled();

    await act(async () => {
      button("确认删除").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deleteAccessToken).toHaveBeenCalledWith(firstToken.id);

    await act(async () => {
      finishDelete?.();
      await Promise.resolve();
    });
  });

  it("adds a created token immediately and strongly confirms deleting the last token", async () => {
    bridgeMocks.createAccessToken.mockResolvedValueOnce({
      token: firstToken,
      access_token: firstSecret,
    });
    bridgeMocks.deleteAccessToken.mockResolvedValueOnce(undefined);

    function Harness() {
      const [items, setItems] = useState<AccessTokenSummary[]>([]);
      return (
        <AccessTokenManager
          catalog={readyCatalog(items)}
          coreSessionKey="session-1"
          inferenceURL="http://127.0.0.1:8317"
          isReady
          onRefresh={() => undefined}
          onTokenCreated={(token) => setItems((current) => [token, ...current])}
          onTokenDeleted={(tokenId) =>
            setItems((current) =>
              current.filter((token) => token.id !== tokenId),
            )
          }
        />
      );
    }

    await act(async () => {
      reactRoot.render(<Harness />);
      await Promise.resolve();
    });
    await act(async () => button("创建令牌").click());
    await setInput("#access-token-name", " VS Code ");
    await act(async () => {
      button("创建").click();
      await Promise.resolve();
    });

    expect(bridgeMocks.createAccessToken).toHaveBeenCalledWith("VS Code");
    expect(container.textContent).toContain(firstToken.name);
    expect(container.textContent).not.toContain(firstSecret);
    expect(
      container.querySelector('[data-testid="revealed-access-token"]'),
    ).toBeNull();

    await act(async () => {
      button("删除", row(firstToken.name)).click();
      await Promise.resolve();
    });
    expect(document.body.textContent).toContain("最后一个访问令牌");
    expect(window.confirm).not.toHaveBeenCalled();
    await act(async () => {
      button("确认删除").click();
      await Promise.resolve();
    });
    expect(bridgeMocks.deleteAccessToken).toHaveBeenCalledWith(firstToken.id);
    expect(
      container.querySelector('[data-testid="access-token-row"]'),
    ).toBeNull();
  });

  describe("while the create dialog closes", () => {
    let removeDialogAnimations: () => void;
    const createDialog = () => document.querySelector('[role="dialog"]')!;
    const nameInput = () =>
      document.querySelector<HTMLInputElement>("#access-token-name");

    beforeEach(async () => {
      removeDialogAnimations = installDialogAnimations();
      await renderManager(readyCatalog([firstToken]));
      await act(async () => button("创建令牌").click());
      await setInput("#access-token-name", "CI");
      await act(async () => button("取消", createDialog()).click());
    });

    afterEach(() => removeDialogAnimations());

    it("keeps the cancelled name and starts empty next time", async () => {
      expect(createDialog().getAttribute("data-state")).toBe("closed");
      expect(nameInput()?.value).toBe("CI");

      await finishExitAnimations();
      expect(document.querySelector('[role="dialog"]')).toBeNull();
      await act(async () => button("创建令牌").click());
      expect(nameInput()?.value).toBe("");
    });

    it("ignores a submission from the closing frame", async () => {
      await act(async () => button("创建", createDialog()).click());

      expect(bridgeMocks.createAccessToken).not.toHaveBeenCalled();
      expect(document.body.textContent).not.toContain("请输入令牌名称。");
    });
  });

  it("loads all token totals in one call and fills unused tokens with zero", async () => {
    bridgeMocks.listAccessTokenUsage.mockResolvedValue({
      items: [{ token_id: firstToken.id, today_tokens: 30, total_tokens: 90 }],
    });

    await renderManager(readyCatalog([firstToken, secondToken]));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    const tokenRow = row(firstToken.name);
    expect(tokenRow.textContent).toContain("今日 Token30");
    expect(row(secondToken.name).textContent).toContain("今日 Token0");
    await act(async () => button("累计").click());
    expect(row(firstToken.name).textContent).toContain("累计 Token90");
    expect(row(secondToken.name).textContent).toContain("累计 Token0");
    expect(bridgeMocks.listAccessTokenUsage).toHaveBeenCalledOnce();
    const todayFrom = new Date(
      bridgeMocks.listAccessTokenUsage.mock.calls[0][0],
    );
    expect(todayFrom.getHours()).toBe(0);
    expect(todayFrom.getMinutes()).toBe(0);
    expect(todayFrom.getSeconds()).toBe(0);
  });

  it("ignores usage from an old Core session", async () => {
    let finish: ((value: unknown) => void) | undefined;
    bridgeMocks.listAccessTokenUsage.mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    await renderManager(readyCatalog([firstToken]));
    expect(row(firstToken.name).textContent).toContain("今日 Token…");
    await renderManager(readyCatalog([firstToken]), "session-2");
    await act(async () => {
      finish?.({
        items: [
          { token_id: firstToken.id, today_tokens: 999, total_tokens: 999 },
        ],
      });
    });
    expect(row(firstToken.name).textContent).toContain("今日 Token0");
    expect(row(firstToken.name).textContent).not.toContain("999");
  });

  it("keeps the last usage on refresh failure instead of displaying a false zero", async () => {
    bridgeMocks.listAccessTokenUsage.mockResolvedValueOnce({
      items: [{ token_id: firstToken.id, today_tokens: 30, total_tokens: 90 }],
    });
    await renderManager(readyCatalog([firstToken]));
    bridgeMocks.listAccessTokenUsage.mockRejectedValueOnce(
      new Error("unavailable"),
    );
    await renderManager(readyCatalog([firstToken]));
    expect(row(firstToken.name).textContent).toContain("今日 Token30");
  });
  it("switches all usage periods together and keeps token details and actions visible", async () => {
    const billed = {
      amount_usd: "1.250000000",
      priced: 2,
      unpriced: 0,
      pending: 0,
      revalued: 0,
      requests: 2,
    };
    bridgeMocks.listAccessTokenUsage.mockResolvedValue({
      items: [
        {
          token_id: firstToken.id,
          today_tokens: 30,
          total_tokens: 90,
          today_billing: billed,
          total_billing: { ...billed, amount_usd: "325.750000000" },
          today_performance: {
            cache_hit_rate: 0,
            output_tokens_per_second: 49,
            cache_samples: 2,
            speed_samples: 3,
          },
          total_performance: {
            cache_hit_rate: 0.26,
            output_tokens_per_second: 60,
            cache_samples: 4,
            speed_samples: 5,
          },
        },
      ],
    });
    await renderManager(readyCatalog([firstToken, secondToken]));
    expect(row(firstToken.name).textContent).toContain("今日消耗 · USD$1.25");
    expect(row(firstToken.name).textContent).toContain("缓存率0.0%TPS49.0");
    expect(row(secondToken.name).textContent).toContain("缓存率—TPS—");
    expect(row(firstToken.name).textContent).toContain("创建时间");
    expect(row(firstToken.name).textContent).toContain("配置客户端");
    expect(row(firstToken.name).textContent).toContain("删除");
    expect(row(secondToken.name).textContent).toContain("今日消耗 · USD$0.00");
    await act(async () => button("累计").click());
    expect(row(firstToken.name).textContent).toContain("累计消耗 · USD$325.75");
    expect(row(firstToken.name).textContent).toContain("累计 Token90");
    expect(row(firstToken.name).textContent).toContain("缓存率26.0%TPS60.0");
    expect(row(firstToken.name).textContent).not.toContain("今日 Token");
    expect(bridgeMocks.listAccessTokenUsage).toHaveBeenCalledOnce();

    expect(row(firstToken.name).textContent).toContain("创建时间");
    expect(button("配置客户端", row(firstToken.name))).toBeTruthy();
    expect(button("删除", row(firstToken.name))).toBeTruthy();
  });

  it("distinguishes unpriced costs from zero and retains costs after refresh failure", async () => {
    const unpriced = {
      amount_usd: "0",
      priced: 0,
      unpriced: 3,
      pending: 1,
      revalued: 0,
      requests: 4,
    };
    bridgeMocks.listAccessTokenUsage.mockResolvedValueOnce({
      items: [
        {
          token_id: firstToken.id,
          today_tokens: 30,
          total_tokens: 90,
          today_billing: unpriced,
          total_billing: { ...unpriced, priced: 2, amount_usd: "42.500000000" },
        },
      ],
    });
    await renderManager(readyCatalog([firstToken]));
    expect(row(firstToken.name).textContent).toContain("今日消耗 · USD—");
    await act(async () => button("累计").click());
    bridgeMocks.listAccessTokenUsage.mockRejectedValueOnce(
      new Error("offline"),
    );
    await renderManager(readyCatalog([firstToken]));
    expect(row(firstToken.name).textContent).toContain("累计消耗 · USD$42.50");
    const help = row(firstToken.name).querySelector<HTMLButtonElement>(
      '[aria-label="统计状态"]',
    )!;
    await act(async () => help.click());
    expect(document.body.textContent).toContain("统计更新失败");
  });

  it("shows unknown billing for older Core responses", async () => {
    bridgeMocks.listAccessTokenUsage.mockResolvedValueOnce({
      items: [
        {
          token_id: firstToken.id,
          today_tokens: 30,
          total_tokens: 90,
          today_billing: null,
          total_billing: null,
        },
      ],
    });
    await renderManager(readyCatalog([firstToken]));
    expect(row(firstToken.name).textContent).toContain("今日消耗 · USD—");
    expect(row(firstToken.name).textContent).toContain("今日 Token30");
  });
});
