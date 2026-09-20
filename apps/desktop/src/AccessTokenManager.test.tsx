// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  createAccessToken: vi.fn(),
  deleteAccessToken: vi.fn(),
  listAccessTokenUsage: vi.fn(),
  revealAccessToken: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import {
  AccessTokenManager,
  type AccessTokenCatalog,
} from "./AccessTokenManager";
import type { AccessTokenSummary } from "./access-token-model";

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
  const match = [...document.querySelectorAll<HTMLElement>('[data-testid="access-token-row"]')].find(
    (candidate) => candidate.textContent?.includes(name),
  );
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
    expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith(secondSecret);
    expect(container.textContent).not.toContain(secondSecret);
    expect(button("已复制", row(secondToken.name))).toBeTruthy();
    expect(button("复制", row(firstToken.name))).toBeTruthy();
  });

  it("ignores a copy response from an old Core session", async () => {
    let resolveReveal:
      | ((value: { access_token: string }) => void)
      | undefined;
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
    let resolveReveal:
      | ((value: { access_token: string }) => void)
      | undefined;
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
            setItems((current) => current.filter((token) => token.id !== tokenId))
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
    expect(container.querySelector('[data-testid="revealed-access-token"]')).toBeNull();

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
            setItems((current) => current.filter((token) => token.id !== tokenId))
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
    expect(container.querySelector('[data-testid="revealed-access-token"]')).toBeNull();

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
    expect(container.querySelector('[data-testid="access-token-row"]')).toBeNull();
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
    const todayFrom = new Date(bridgeMocks.listAccessTokenUsage.mock.calls[0][0]);
    expect(todayFrom.getHours()).toBe(0);
    expect(todayFrom.getMinutes()).toBe(0);
    expect(todayFrom.getSeconds()).toBe(0);
  });

  it("ignores usage from an old Core session", async () => {
    let finish: ((value: unknown) => void) | undefined;
    bridgeMocks.listAccessTokenUsage.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    await renderManager(readyCatalog([firstToken]));
    expect(row(firstToken.name).textContent).toContain("今日 Token…");
    await renderManager(readyCatalog([firstToken]), "session-2");
    await act(async () => {
      finish?.({ items: [{ token_id: firstToken.id, today_tokens: 999, total_tokens: 999 }] });
    });
    expect(row(firstToken.name).textContent).toContain("累计 Token0");
    expect(row(firstToken.name).textContent).not.toContain("999");
  });

  it("keeps the last usage on refresh failure instead of displaying a false zero", async () => {
    bridgeMocks.listAccessTokenUsage.mockResolvedValueOnce({
      items: [{ token_id: firstToken.id, today_tokens: 30, total_tokens: 90 }],
    });
    await renderManager(readyCatalog([firstToken]));
    bridgeMocks.listAccessTokenUsage.mockRejectedValueOnce(new Error("unavailable"));
    await renderManager(readyCatalog([firstToken]));
    expect(row(firstToken.name).textContent).toContain("累计 Token90");
  });
});
