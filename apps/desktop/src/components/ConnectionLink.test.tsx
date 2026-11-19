// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { ConnectionLink, type ConnectionLinkState } from "./ConnectionLink";

describe("ConnectionLink", () => {
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

  async function render(state: ConnectionLinkState, playKey: number) {
    await act(async () => {
      root.render(
        <ConnectionLink
          badge={<span>{state}</span>}
          detail="Memory 1.4.0 · 3 ms"
          from={{ mark: <span />, name: "AstrLink", caption: "gateway" }}
          playKey={playKey}
          state={state}
          to={{ mark: <span />, name: "Memory", caption: "memory.test" }}
        />,
      );
    });
    return container.querySelector('[data-slot="connection-link"]');
  }

  it("shows both ends, the state and the last test", async () => {
    const link = await render("off", 0);
    expect(link?.getAttribute("data-state")).toBe("off");
    expect(link?.textContent).toBe(
      "AstrLinkgatewayoffMemory 1.4.0 · 3 msMemorymemory.test",
    );
    // A switched-off link draws no line, and its far end is dimmed.
    expect(link?.querySelector('[data-part="fill"]')?.className).toContain(
      "hidden",
    );
    expect(link?.querySelector('[data-part="to"]')?.className).toContain(
      "grayscale",
    );
  });

  it("plays the joining when asked and keeps every word on screen", async () => {
    await render("off", 0);
    const link = await render("connected", 1);
    expect(link?.getAttribute("data-state")).toBe("connected");
    expect(link?.querySelector('[data-part="fill"]')?.className).toContain(
      "bg-success",
    );
    expect(link?.querySelectorAll('[data-part="particle"]')).toHaveLength(8);
    expect(link?.textContent).toContain("connected");
  });
});
