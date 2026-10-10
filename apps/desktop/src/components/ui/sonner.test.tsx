// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { toast } from "sonner";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { notify } from "@/notify";

import { Toaster } from "./sonner";

describe("Toaster", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root.render(<Toaster position="bottom-right" />));
  });

  afterEach(async () => {
    await act(async () => toast.dismiss());
    await act(async () => root.unmount());
    vi.restoreAllMocks();
    container.remove();
  });

  // Sonner inserts toasts from a timer callback, so wait for the element.
  const toastWithText = (text: string) =>
    vi.waitFor(() => {
      const item = [
        ...document.querySelectorAll<HTMLElement>("[data-sonner-toast]"),
      ].find((candidate) => candidate.textContent?.includes(text));
      if (!item) throw new Error(`toast "${text}" not rendered`);
      return item;
    });

  const movePointer = (clientX: number, clientY: number) =>
    document.body.dispatchEvent(
      new PointerEvent("pointermove", { bubbles: true, clientX, clientY }),
    );

  it("marks toasts under the pointer so they fade out of the way", async () => {
    await act(async () => {
      toast.success("API 提供商已启用。");
    });
    const item = await toastWithText("API 提供商已启用。");
    vi.spyOn(item, "getBoundingClientRect").mockReturnValue(
      new DOMRect(900, 600, 356, 52),
    );

    movePointer(1000, 620);
    await vi.waitFor(() =>
      expect(item.hasAttribute("data-pointer-over")).toBe(true),
    );

    movePointer(400, 620);
    await vi.waitFor(() =>
      expect(item.hasAttribute("data-pointer-over")).toBe(false),
    );

    movePointer(1000, 620);
    await vi.waitFor(() =>
      expect(item.hasAttribute("data-pointer-over")).toBe(true),
    );
    document.body.dispatchEvent(
      new PointerEvent("pointerout", { bubbles: true, relatedTarget: null }),
    );
    await vi.waitFor(() =>
      expect(item.hasAttribute("data-pointer-over")).toBe(false),
    );
  });

  it("renders a success action as a button that runs it", async () => {
    const onClick = vi.fn();
    await act(async () => {
      notify.success("已保存，重启网关后生效。", {
        label: "立即重启",
        onClick,
      });
    });
    const item = await toastWithText("已保存，重启网关后生效。");
    // globals.css keeps toasts with a [data-button] reachable by the pointer.
    const action = item.querySelector<HTMLButtonElement>("button[data-button]");
    expect(action?.textContent).toBe("立即重启");
    await act(async () => action?.click());
    expect(onClick).toHaveBeenCalledOnce();
  });
});
