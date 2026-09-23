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
});
