// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import type { ClientConfigStatus } from "../client-config-model";
import { rememberClientNames } from "../client-setup-clients";
import { TokenClientMarks } from "./TokenClientMarks";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

function config(
  client: string,
  overrides: Partial<ClientConfigStatus> = {},
): ClientConfigStatus {
  return {
    client,
    wsl: null,
    target: null,
    detected: true,
    home: "/Users/me",
    dir: `/Users/me/.${client}`,
    paths: [`/Users/me/.${client}/config.json`],
    exists: [true],
    state: "configured",
    token_id: "token_01",
    models: {},
    notes: [],
    paste_since_unix: null,
    paste_into: [],
    ...overrides,
  };
}

describe("TokenClientMarks", () => {
  it("folds clients past six into +N and lists every place on hover", async () => {
    rememberClientNames({
      clients: [
        ["workbuddy", "WorkBuddy"],
        ["kimi_code", "Kimi Code"],
        ["chatbox", "Chatbox"],
      ].map(([id, name]) => ({
        id,
        name,
        kind: "file" as const,
        targets: id === "workbuddy" ? ["cn", "intl"] : [],
        protocols: ["openai.chat"],
        list: false,
      })),
    });
    const configs = [
      ...["claude", "codex", "gemini", "opencode", "openclaw"].map((client) =>
        config(client),
      ),
      config("workbuddy", { target: "cn" }),
      config("workbuddy", { target: "intl" }),
      config("kimi_code", { state: "modified" }),
      // Not configured, so not marked.
      config("chatbox", { state: "not_configured", token_id: null }),
      // Another token's config is that token's mark.
      config("pi", { token_id: "token_02" }),
    ];
    await act(async () => {
      root.render(
        <TokenClientMarks
          available={null}
          configs={configs}
          tokenId="token_01"
          tokenName="工作机"
        />,
      );
    });

    const marks = container.querySelector<HTMLElement>(
      '[data-slot="token-client-marks"]',
    )!;
    expect(
      [...marks.querySelectorAll("[data-client]")].map((item) =>
        item.getAttribute("data-client"),
      ),
    ).toEqual(["claude", "codex", "gemini", "opencode", "openclaw"]);
    const more = marks.querySelector<HTMLElement>(
      '[data-slot="token-client-more"]',
    )!;
    expect(more.textContent).toBe("+2");
    // Kimi Code's edited config is folded in, so +N carries its dot.
    expect(more.getAttribute("data-attention")).toBe("true");
    expect(marks.getAttribute("aria-label")).toContain("WorkBuddy");
    expect(marks.getAttribute("aria-label")).toContain("Kimi Code");
    expect(marks.getAttribute("aria-label")).not.toContain("Chatbox");

    await act(async () => {
      marks.focus();
      await Promise.resolve();
    });
    const tooltip = document.body.textContent ?? "";
    expect(tooltip).toContain("「工作机」已配置到 7 个客户端");
    expect(tooltip).toContain("WorkBuddy（国内版）");
    expect(tooltip).toContain("WorkBuddy（国际版）");
    expect(tooltip).toContain("Kimi Code");
    expect(tooltip).toContain("配置已被改动");
  });

  it("shows up to six marks without folding", async () => {
    await act(async () => {
      root.render(
        <TokenClientMarks
          available={null}
          configs={[
            "claude",
            "codex",
            "gemini",
            "opencode",
            "openclaw",
            "pi",
          ].map((client) => config(client))}
          tokenId="token_01"
          tokenName="工作机"
        />,
      );
    });
    expect(container.querySelectorAll("[data-client]")).toHaveLength(6);
    expect(
      container.querySelector('[data-slot="token-client-more"]'),
    ).toBeNull();
  });
});
