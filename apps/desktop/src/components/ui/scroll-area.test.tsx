// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ScrollArea } from "./scroll-area";

describe("ScrollArea auto-hide", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.useFakeTimers();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.useRealTimers();
  });

  it.each(["vertical", "horizontal"] as const)(
    "hides an idle %s scrollbar even with the pointer inside the content",
    async (orientation) => {
      await act(async () => {
        root.render(<ScrollArea orientation={orientation}>Content</ScrollArea>);
      });
      const area = container.querySelector<HTMLElement>(
        '[data-slot="scroll-area"]',
      )!;
      const viewport = container.querySelector<HTMLElement>(
        '[data-slot="scroll-area-viewport"]',
      )!;
      const scrollbar = () =>
        container.querySelector('[data-slot="scroll-area-scrollbar"]');
      await act(async () => {
        area.dispatchEvent(new PointerEvent("pointerenter"));
      });
      expect(scrollbar()).toBeNull();
      await act(async () => {
        viewport[orientation === "vertical" ? "scrollTop" : "scrollLeft"] = 50;
        viewport.dispatchEvent(new Event("scroll"));
      });
      expect(scrollbar()?.getAttribute("data-state")).toBe("visible");
      await act(async () => vi.advanceTimersByTimeAsync(100));
      await act(async () => vi.advanceTimersByTimeAsync(1000));
      expect(scrollbar()).toBeNull();
    },
  );
});
