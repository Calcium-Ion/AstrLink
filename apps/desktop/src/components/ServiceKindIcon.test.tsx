// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { kindMarkIsShared, ServiceKindIcon } from "./ServiceKindIcon";

describe("ServiceKindIcon", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it.each([
    ["newapi", "New API"],
    ["magpie", "Magpie"],
    ["codex_subscription", "Codex 订阅"],
    ["grok_subscription", "Grok 订阅"],
    ["antigravity_subscription", "Antigravity 订阅"],
    ["copilot_subscription", "GitHub Copilot 订阅"],
    ["custom", "自定义 API"],
  ] as const)("renders a labeled icon for %s", async (kind, label) => {
    await act(async () => {
      root.render(<ServiceKindIcon kind={kind} />);
    });

    const icon = container.querySelector(`[role="img"][aria-label="${label}"]`);
    expect(icon).not.toBeNull();
    expect(icon?.querySelector("svg, img")).not.toBeNull();
  });

  it.each([
    ["glm_coding", true],
    ["kimi_coding", true],
    ["grok_subscription", true],
    ["codex_subscription", false],
    ["claude_subscription", false],
    ["antigravity_subscription", false],
    ["copilot_subscription", false],
  ] as const)("reports whether %s shares its logo", (kind, shared) => {
    expect(kindMarkIsShared(kind)).toBe(shared);
  });

  it("uses the New API asset's built-in padding inside a fixed box", async () => {
    await act(async () => {
      root.render(<ServiceKindIcon kind="newapi" size={20} />);
    });

    const icon = container.querySelector('[role="img"]') as HTMLElement;
    const image = icon.querySelector("img");
    expect(icon.style.width).toBe("20px");
    expect(icon.style.height).toBe("20px");
    expect(image?.getAttribute("width")).toBe("20");
    expect(image?.getAttribute("height")).toBe("20");
  });
});
