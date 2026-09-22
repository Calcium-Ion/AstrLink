// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { ServiceKindIcon } from "./ServiceKindIcon";

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
    ["codex_subscription", "Codex 订阅"],
    ["grok_subscription", "Grok 订阅"],
    ["custom", "自定义 API"],
  ] as const)("renders a labeled icon for %s", async (kind, label) => {
    await act(async () => {
      root.render(<ServiceKindIcon kind={kind} />);
    });

    const icon = container.querySelector(`[role="img"][aria-label="${label}"]`);
    expect(icon).not.toBeNull();
    expect(icon?.querySelector("svg")).not.toBeNull();
  });

  it("keeps NewAPI smaller inside a fixed box so it matches padded brand marks", async () => {
    await act(async () => {
      root.render(<ServiceKindIcon kind="newapi" size={20} />);
    });

    const icon = container.querySelector('[role="img"]') as HTMLElement;
    const svg = icon.querySelector("svg");
    expect(icon.style.width).toBe("20px");
    expect(icon.style.height).toBe("20px");
    expect(svg?.getAttribute("width")).toBe("15");
    expect(svg?.getAttribute("height")).toBe("15");
  });
});
