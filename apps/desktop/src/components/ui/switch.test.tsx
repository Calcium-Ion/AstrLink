// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Switch } from "./switch";

describe("Switch", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    vi.restoreAllMocks();
    container.remove();
  });

  it("skips transitions on the first paint and arms them after rAF", async () => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
      frames.push(callback);
      return 1;
    });

    await act(async () => {
      root.render(<Switch aria-label="demo" checked />);
    });

    const control = container.querySelector("[data-slot='switch']");
    const thumb = container.querySelector("[data-slot='switch-thumb']");
    expect(control?.className.split(/\s+/)).toContain("transition-none");
    expect(control?.className.split(/\s+/)).not.toContain("transition-all");
    expect(thumb?.className.split(/\s+/)).toContain("transition-none");
    expect(thumb?.className.split(/\s+/)).not.toContain("transition-transform");

    await act(async () => {
      for (const frame of frames) {
        frame(0);
      }
    });

    expect(control?.className.split(/\s+/)).toContain("transition-all");
    expect(control?.className.split(/\s+/)).not.toContain("transition-none");
    expect(thumb?.className.split(/\s+/)).toContain("transition-transform");
    expect(thumb?.className.split(/\s+/)).not.toContain("transition-none");
  });
});
