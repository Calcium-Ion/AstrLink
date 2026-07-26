// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  createAccessToken: vi.fn(),
  deleteAccessToken: vi.fn(),
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
  source: "user",
  created_at: "2026-07-24T10:30:00Z",
};

const secondToken: AccessTokenSummary = {
  id: "token_02",
  name: "Terminal",
  hint: "astr_…7HT4",
  source: "system_default",
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
  const match = [...document.querySelectorAll<HTMLElement>(".token-row")].find(
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
          isReady
          onRefresh={() => undefined}
          onTokenCreated={() => undefined}
          onTokenDeleted={() => undefined}
        />,
      );
      await Promise.resolve();
    });
  };

  it("keeps only one inline token revealed and copies only that value", async () => {
    bridgeMocks.revealAccessToken
      .mockResolvedValueOnce({ access_token: firstSecret })
      .mockResolvedValueOnce({ access_token: secondSecret });
    await renderManager(readyCatalog([firstToken, secondToken]));

    await act(async () => {
      button("显示", row(firstToken.name)).click();
      await Promise.resolve();
    });
    expect(container.textContent).toContain(firstSecret);

    await act(async () => {
      button("显示", row(secondToken.name)).click();
      await Promise.resolve();
    });
    expect(container.textContent).not.toContain(firstSecret);
    expect(container.textContent).toContain(secondSecret);

    await act(async () => {
      button("复制", row(secondToken.name)).click();
      await Promise.resolve();
    });
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(secondSecret);
  });

  it("clears secrets and ignores a reveal response from an old Core session", async () => {
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
      button("显示", row(firstToken.name)).click();
      await Promise.resolve();
    });
    await renderManager(readyCatalog([firstToken]), "session-2");
    await act(async () => {
      resolveReveal?.({ access_token: firstSecret });
      await Promise.resolve();
    });

    expect(container.textContent).not.toContain(firstSecret);
    expect(button("显示", row(firstToken.name)).disabled).toBe(false);
  });

  it("hides a revealed secret before refreshing", async () => {
    const onRefresh = vi.fn();
    bridgeMocks.revealAccessToken.mockResolvedValueOnce({
      access_token: firstSecret,
    });
    await act(async () => {
      reactRoot.render(
        <AccessTokenManager
          catalog={readyCatalog([firstToken])}
          coreSessionKey="session-1"
          isReady
          onRefresh={onRefresh}
          onTokenCreated={() => undefined}
          onTokenDeleted={() => undefined}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      button("显示", row(firstToken.name)).click();
      await Promise.resolve();
    });
    expect(container.textContent).toContain(firstSecret);

    await act(async () => button("刷新").click());

    expect(container.textContent).not.toContain(firstSecret);
    expect(onRefresh).toHaveBeenCalledOnce();
  });

  it("hides a revealed secret as soon as deletion is confirmed", async () => {
    let finishDelete: (() => void) | undefined;
    bridgeMocks.revealAccessToken.mockResolvedValueOnce({
      access_token: firstSecret,
    });
    bridgeMocks.deleteAccessToken.mockReturnValueOnce(
      new Promise<void>((resolve) => {
        finishDelete = resolve;
      }),
    );
    await renderManager(readyCatalog([firstToken]));
    await act(async () => {
      button("显示", row(firstToken.name)).click();
      await Promise.resolve();
    });
    expect(container.textContent).toContain(firstSecret);

    await act(async () => {
      button("删除", row(firstToken.name)).click();
      await Promise.resolve();
    });

    expect(container.textContent).not.toContain(firstSecret);
    expect(container.querySelector(".token-row__secret")).toBeNull();
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
    expect(container.textContent).toContain(firstSecret);

    await act(async () => {
      button("删除", row(firstToken.name)).click();
      await Promise.resolve();
    });
    expect(window.confirm).toHaveBeenCalledWith(
      expect.stringContaining("最后一个访问令牌"),
    );
    expect(bridgeMocks.deleteAccessToken).toHaveBeenCalledWith(firstToken.id);
    expect(container.querySelector(".token-row")).toBeNull();
  });

  it("renders usage placeholders without starting any usage request", async () => {
    await renderManager(readyCatalog([firstToken]));
    const tokenRow = row(firstToken.name);
    expect(tokenRow.textContent).toContain("今日 Token—");
    expect(tokenRow.textContent).toContain("累计 Token—");
    expect(container.textContent).toContain("统计待接入");
    expect(bridgeMocks.createAccessToken).not.toHaveBeenCalled();
    expect(bridgeMocks.revealAccessToken).not.toHaveBeenCalled();
    expect(bridgeMocks.deleteAccessToken).not.toHaveBeenCalled();
  });
});
