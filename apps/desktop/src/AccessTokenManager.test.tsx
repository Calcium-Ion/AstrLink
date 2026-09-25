// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  createAccessToken: vi.fn(),
  deleteAccessToken: vi.fn(),
  listAccessTokenUsage: vi.fn(),
  revealAccessToken: vi.fn(),
  openCCSwitchImport: vi.fn(),
  listServices: vi.fn(),
  listRoutes: vi.fn(),
  getRoutingSettings: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import {
  AccessTokenManager,
  type AccessTokenCatalog,
} from "./AccessTokenManager";
import type { AccessTokenSummary } from "./access-token-model";
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
const secondSecret = `astr_${"B".repeat(43)}`;

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
    bridgeMocks.listServices.mockResolvedValue({ items: [] });
    bridgeMocks.listRoutes.mockResolvedValue({ items: [] });
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

  it("copies a token without rendering the secret and only keeps the latest copy", async () => {
    bridgeMocks.revealAccessToken
      .mockResolvedValueOnce({ access_token: firstSecret })
      .mockResolvedValueOnce({ access_token: secondSecret });
    await renderManager(readyCatalog([firstToken, secondToken]));

    await act(async () => {
      button("复制", row(firstToken.name)).click();
      await Promise.resolve();
    });
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(firstSecret);
    expect(container.textContent).not.toContain(firstSecret);
    expect(button("已复制", row(firstToken.name))).toBeTruthy();

    await act(async () => {
      button("复制", row(secondToken.name)).click();
      await Promise.resolve();
    });
    expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith(
      secondSecret,
    );
    expect(container.textContent).not.toContain(secondSecret);
    expect(button("已复制", row(secondToken.name))).toBeTruthy();
    expect(button("复制", row(firstToken.name))).toBeTruthy();
  });

  it("fills CC Switch with the selected token and edited model without revealing its secret", async () => {
    bridgeMocks.openCCSwitchImport.mockResolvedValueOnce(undefined);
    await renderManager(readyCatalog([firstToken, secondToken]));
    await act(async () => button("CC Switch", row(secondToken.name)).click());
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      "Terminal",
    );
    expect(
      document.querySelector<HTMLInputElement>("#cc-switch-model")?.value,
    ).toBe("");
    expect(button("填充到 CC Switch").disabled).toBe(false);
    await act(async () =>
      document
        .querySelector<HTMLButtonElement>('[role="radio"][aria-label="Codex"]')
        ?.click(),
    );
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      "http://127.0.0.1:8317/v1",
    );
    expect(button("填充到 CC Switch").disabled).toBe(true);
    await setInput("#cc-switch-model", "my-route");
    await act(async () => button("填充到 CC Switch").click());
    expect(bridgeMocks.openCCSwitchImport).toHaveBeenCalledExactlyOnceWith({
      tokenId: secondToken.id,
      client: "codex",
      name: "AstrLink · Terminal",
      models: { model: "my-route" },
      inferenceUrl: "http://127.0.0.1:8317",
    });
    expect(bridgeMocks.revealAccessToken).not.toHaveBeenCalled();
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("keeps failed imports retryable and hides native errors that may contain credentials", async () => {
    bridgeMocks.openCCSwitchImport
      .mockRejectedValueOnce(
        new Error(`failed ccswitch://test?apiKey=${firstSecret}`),
      )
      .mockResolvedValueOnce(undefined);
    await renderManager(readyCatalog([firstToken]));
    await act(async () => button("CC Switch", row(firstToken.name)).click());
    await setInput("#cc-switch-model", "my-route");
    await act(async () => button("填充到 CC Switch").click());
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      "无法打开 CC Switch",
    );
    expect(document.body.textContent).not.toContain(firstSecret);
    await act(async () => button("填充到 CC Switch").click());
    expect(bridgeMocks.openCCSwitchImport).toHaveBeenCalledTimes(2);
  });

  it("exports only the Claude model slots that were filled", async () => {
    bridgeMocks.openCCSwitchImport.mockResolvedValueOnce(undefined);
    await renderManager(readyCatalog([firstToken]));
    await act(async () => button("CC Switch", row(firstToken.name)).click());
    expect(
      document.querySelectorAll('[role="dialog"] input[role="combobox"]'),
    ).toHaveLength(4);
    await setInput("#cc-switch-haikuModel", "  ");
    await setInput("#cc-switch-sonnetModel", " sonnet-route ");
    await setInput("#cc-switch-opusModel", "opus-route");
    await act(async () => button("填充到 CC Switch").click());
    expect(bridgeMocks.openCCSwitchImport).toHaveBeenCalledExactlyOnceWith({
      tokenId: firstToken.id,
      client: "claude",
      name: "AstrLink · VS Code",
      models: { sonnetModel: "sonnet-route", opusModel: "opus-route" },
      inferenceUrl: "http://127.0.0.1:8317",
    });
  });

  it("allows all Claude model slots to be empty and keeps tier choices out of other clients", async () => {
    bridgeMocks.openCCSwitchImport.mockResolvedValue(undefined);
    await renderManager(readyCatalog([firstToken]));
    await act(async () => button("CC Switch", row(firstToken.name)).click());
    await act(async () => button("填充到 CC Switch").click());
    expect(bridgeMocks.openCCSwitchImport).toHaveBeenLastCalledWith(
      expect.objectContaining({ models: {} }),
    );
    await act(async () => button("CC Switch", row(firstToken.name)).click());
    await setInput("#cc-switch-opusModel", "opus-route");
    await act(async () =>
      document
        .querySelector<HTMLButtonElement>('[role="radio"][aria-label="Codex"]')!
        .click(),
    );
    expect(button("填充到 CC Switch").disabled).toBe(true);
    expect(document.querySelector("#cc-switch-opusModel")).toBeNull();
    await setInput("#cc-switch-model", "codex-route");
    await act(async () => button("填充到 CC Switch").click());
    expect(bridgeMocks.openCCSwitchImport).toHaveBeenLastCalledWith(
      expect.objectContaining({
        client: "codex",
        models: { model: "codex-route" },
      }),
    );
  });

  it("closes the import dialog when the Core session changes and blocks stale tokens", async () => {
    await renderManager(readyCatalog([firstToken]));
    await act(async () => button("CC Switch", row(firstToken.name)).click());
    await renderManager(
      { ...readyCatalog([firstToken]), stale: true },
      "session-2",
    );
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(button("CC Switch", row(firstToken.name)).disabled).toBe(true);
    expect(bridgeMocks.openCCSwitchImport).not.toHaveBeenCalled();
  });

  it("suggests compatible enabled models and route aliases for each CC Switch client", async () => {
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
    bridgeMocks.listRoutes.mockResolvedValue({
      items: [
        {
          enabled: true,
          match: { protocol: "anthropic.messages", model: "team-route" },
        },
        {
          enabled: true,
          match: { protocol: "anthropic.messages", model: "*" },
        },
        {
          enabled: false,
          match: { protocol: "anthropic.messages", model: "disabled-route" },
        },
      ],
    });
    await renderManager(readyCatalog([firstToken]));
    await act(async () => button("CC Switch", row(firstToken.name)).click());
    for (const [client, expected] of [
      ["Claude Code", ["model-0", "team-route"]],
      ["Codex", ["model-1"]],
      ["Gemini CLI", ["model-2"]],
      ["OpenCode", ["model-3"]],
      ["OpenClaw", ["model-3"]],
    ] as const) {
      await act(async () =>
        document
          .querySelector<HTMLButtonElement>(
            `[role="radio"][aria-label="${client}"]`,
          )!
          .click(),
      );
      await act(async () =>
        document.querySelector<HTMLInputElement>("#cc-switch-model")!.click(),
      );
      const suggestions = [...document.querySelectorAll('[role="option"]')];
      expect(
        suggestions.map((option) => option.getAttribute("aria-label")),
      ).toEqual(expected);
      expect(suggestions.every((option) => option.querySelector("svg"))).toBe(
        true,
      );
      await act(async () =>
        document
          .querySelector<HTMLInputElement>("#cc-switch-model")!
          .dispatchEvent(
            new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
          ),
      );
    }
  });

  it("suggests enabled redirect sources whose target a compatible service lists", async () => {
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
    await renderManager(readyCatalog([firstToken]));
    await act(async () => button("CC Switch", row(firstToken.name)).click());
    for (const [client, expected] of [
      ["Gemini CLI", ["gemini-2.5-pro", "gemini-pro"]],
      ["Codex", ["gpt-4o", "gpt-5"]],
    ] as const) {
      await act(async () =>
        document
          .querySelector<HTMLButtonElement>(
            `[role="radio"][aria-label="${client}"]`,
          )!
          .click(),
      );
      await act(async () =>
        document.querySelector<HTMLInputElement>("#cc-switch-model")!.click(),
      );
      expect(
        [...document.querySelectorAll('[role="option"]')].map((option) =>
          option.getAttribute("aria-label"),
        ),
      ).toEqual(expected);
      await act(async () =>
        document
          .querySelector<HTMLInputElement>("#cc-switch-model")!
          .dispatchEvent(
            new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
          ),
      );
    }
  });

  it("ignores a copy response from an old Core session", async () => {
    let resolveReveal: ((value: { access_token: string }) => void) | undefined;
    bridgeMocks.revealAccessToken.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveReveal = resolve;
      }),
    );
    await renderManager(readyCatalog([firstToken]));

    await act(async () => {
      button("复制", row(firstToken.name)).click();
      await Promise.resolve();
    });
    await renderManager(readyCatalog([firstToken]), "session-2");
    await act(async () => {
      resolveReveal?.({ access_token: firstSecret });
      await Promise.resolve();
    });

    expect(container.textContent).not.toContain(firstSecret);
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled();
    expect(button("复制", row(firstToken.name)).disabled).toBe(false);
  });

  it("cancels an in-flight copy before refreshing", async () => {
    const onRefresh = vi.fn();
    let resolveReveal: ((value: { access_token: string }) => void) | undefined;
    bridgeMocks.revealAccessToken.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveReveal = resolve;
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
      resolveReveal?.({ access_token: firstSecret });
      await Promise.resolve();
    });

    expect(container.textContent).not.toContain(firstSecret);
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled();
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
    expect(tokenRow.textContent).toContain("累计 Token90");
    expect(row(secondToken.name).textContent).toContain("今日 Token0");
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
    expect(row(firstToken.name).textContent).toContain("累计 Token0");
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
    expect(row(firstToken.name).textContent).toContain("累计 Token90");
  });
});
