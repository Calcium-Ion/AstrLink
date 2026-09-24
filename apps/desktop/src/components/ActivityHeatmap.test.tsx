// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ActivityHeatmap, type ActivityCell } from "./ActivityHeatmap";

describe("ActivityHeatmap tooltip lifecycle", () => {
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
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  async function render() {
    const cells: ActivityCell[] = Array.from({ length: 365 }, (_, index) => {
      const date = new Date(Date.UTC(2025, 0, index + 1))
        .toISOString()
        .slice(0, 10);
      return {
        key: date,
        date,
        label: date,
        value: index,
        detail: vi.fn(() => <span>Details for {date}</span>),
      };
    });
    await act(async () =>
      root.render(
        <ActivityHeatmap
          cells={cells}
          label="Activity"
          caption="One year"
          emptyLabel="Empty"
          lessLabel="Less"
          moreLabel="More"
          locale="en"
        />,
      ),
    );
    const buttons = [
      ...container.querySelectorAll<HTMLButtonElement>(
        '[data-slot="activity-cell"]',
      ),
    ];
    return { cells, buttons };
  }

  it("mounts no unused tooltip contents and retains the original focused cells", async () => {
    const { cells, buttons } = await render();
    expect(buttons).toHaveLength(365);
    expect(document.querySelector('[role="tooltip"]')).toBeNull();
    cells.forEach((cell) => expect(cell.detail).not.toHaveBeenCalled());
    await act(async () => buttons[0].focus());
    expect(document.activeElement).toBe(buttons[0]);
    expect(document.querySelectorAll('[role="tooltip"]')).toHaveLength(1);
    expect(cells[0].detail).toHaveBeenCalled();
    cells
      .slice(1)
      .forEach((cell) => expect(cell.detail).not.toHaveBeenCalled());
    const describedBy = buttons[0].getAttribute("aria-describedby");
    expect(document.getElementById(describedBy!)?.textContent).toContain(
      "2025-01-01",
    );
    const surface = document.querySelector('[data-slot="tooltip-content"]')!;
    expect(surface.className).toContain("bg-foreground");
    expect(surface.getAttribute("data-side")).toBe("top");
    expect(surface.querySelector("svg")).not.toBeNull();

    await act(async () =>
      buttons[0].dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
      ),
    );
    expect(document.activeElement).toBe(buttons[7]);
    expect(document.querySelectorAll('[role="tooltip"]')).toHaveLength(1);
    expect(document.querySelector('[role="tooltip"]')?.textContent).toContain(
      "2025-01-08",
    );
    await act(async () =>
      buttons[7].dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      ),
    );
    expect(document.querySelector('[role="tooltip"]')).toBeNull();
    expect(document.activeElement).toBe(buttons[7]);
  });

  it("keeps the hover delay and cancels work when the pointer or page leaves", async () => {
    vi.useFakeTimers();
    const { cells, buttons } = await render();
    const enter = () =>
      buttons[1].dispatchEvent(
        new PointerEvent("pointerover", {
          bubbles: true,
          pointerType: "mouse",
        }),
      );
    await act(async () => {
      enter();
      await vi.advanceTimersByTimeAsync(149);
    });
    expect(cells[1].detail).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(document.querySelector('[role="tooltip"]')?.textContent).toContain(
      "2025-01-02",
    );
    await act(async () =>
      buttons[1].dispatchEvent(
        new PointerEvent("pointerout", {
          bubbles: true,
          pointerType: "mouse",
          relatedTarget: document.body,
        }),
      ),
    );
    expect(document.querySelector('[role="tooltip"]')).toBeNull();
    await act(async () => enter());
    await act(async () => root.render(null));
    await act(async () => vi.advanceTimersByTimeAsync(500));
    expect(document.querySelector('[role="tooltip"]')).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("starts at recent weeks and reveals keyboard targets without scrolling the page", async () => {
    vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockReturnValue(898);
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(300);
    const { buttons } = await render();
    const viewport = container.querySelector<HTMLDivElement>(
      '[data-slot="scroll-area-viewport"]',
    )!;
    expect(viewport.scrollLeft).toBe(598);
    vi.spyOn(viewport, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 300, 160),
    );
    vi.spyOn(buttons[0], "getBoundingClientRect").mockImplementation(
      () => new DOMRect(-viewport.scrollLeft, 0, 14, 14),
    );
    vi.spyOn(buttons[364], "getBoundingClientRect").mockImplementation(
      () => new DOMRect(884 - viewport.scrollLeft, 0, 14, 14),
    );
    container.scrollTop = 80;
    await act(async () => buttons[364].focus());
    await act(async () =>
      buttons[364].dispatchEvent(
        new KeyboardEvent("keydown", { key: "Home", bubbles: true }),
      ),
    );
    expect(document.activeElement).toBe(buttons[0]);
    expect(viewport.scrollLeft).toBe(0);
    await act(async () => viewport.dispatchEvent(new Event("scroll")));
    expect(document.querySelector('[role="tooltip"]')?.textContent).toContain(
      "2025-01-01",
    );
    expect(container.scrollTop).toBe(80);
    await act(async () =>
      buttons[0].dispatchEvent(
        new KeyboardEvent("keydown", { key: "End", bubbles: true }),
      ),
    );
    expect(document.activeElement).toBe(buttons[364]);
    expect(viewport.scrollLeft).toBe(598);
    expect(container.scrollTop).toBe(80);
    await act(async () => {
      viewport.scrollLeft = 0;
      viewport.dispatchEvent(new Event("scroll"));
    });
    expect(document.querySelector('[role="tooltip"]')).toBeNull();
  });

  it("keeps the latest dates visible when the scrollport shrinks after layout", async () => {
    const resize = new Map<Element, () => void>();
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(private callback: () => void) {}
        observe(node: Element) {
          resize.set(node, this.callback);
        }
        unobserve(node: Element) {
          resize.delete(node);
        }
        disconnect() {}
      },
    );
    let visibleWidth = 1000;
    vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockReturnValue(898);
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(
      () => visibleWidth,
    );
    await render();
    const viewport = container.querySelector<HTMLDivElement>(
      '[data-slot="scroll-area-viewport"]',
    )!;
    const calendar = container.querySelector(
      '[data-slot="activity-calendar"]',
    )!;
    expect(viewport.scrollLeft).toBe(0);
    await act(async () => {
      visibleWidth = 600;
      resize.get(calendar)!();
    });
    expect(viewport.scrollLeft).toBe(298);
    await act(async () => {
      viewport.scrollLeft = 0;
      viewport.dispatchEvent(new Event("scroll"));
    });
    expect(viewport.scrollLeft).toBe(0);
    await act(async () => {
      visibleWidth = 300;
      resize.get(calendar)!();
    });
    expect(viewport.scrollLeft).toBe(598);
    await act(async () => {
      visibleWidth = 1000;
      resize.get(calendar)!();
    });
    expect(viewport.scrollLeft).toBe(0);
  });
});
