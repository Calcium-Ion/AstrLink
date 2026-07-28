// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const windowMocks = vi.hoisted(() => ({
  close: vi.fn(),
  invoke: vi.fn(),
  isFocused: vi.fn(),
  isFullscreen: vi.fn(),
  isMaximized: vi.fn(),
  minimize: vi.fn(),
  onFocusChanged: vi.fn(),
  onResized: vi.fn(),
  startResizeDragging: vi.fn(),
  toggleMaximize: vi.fn(),
  unlistenFocus: vi.fn(),
  unlistenResize: vi.fn(),
}));

vi.mock("@tauri-apps/api/core", () => ({
  invoke: windowMocks.invoke,
}));

vi.mock("@tauri-apps/api/window", () => ({
  getCurrentWindow: () => ({
    close: windowMocks.close,
    isFocused: windowMocks.isFocused,
    isFullscreen: windowMocks.isFullscreen,
    isMaximized: windowMocks.isMaximized,
    minimize: windowMocks.minimize,
    onFocusChanged: windowMocks.onFocusChanged,
    onResized: windowMocks.onResized,
    startResizeDragging: windowMocks.startResizeDragging,
    toggleMaximize: windowMocks.toggleMaximize,
  }),
}));

import { WindowChrome } from "./WindowChrome";

function control(container: HTMLElement, label: string): HTMLButtonElement {
  const match = container.querySelector<HTMLButtonElement>(
    `button[aria-label="${label}"]`,
  );
  if (!match) throw new Error(`Missing window control: ${label}`);
  return match;
}

describe("WindowChrome", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    windowMocks.close.mockResolvedValue(undefined);
    windowMocks.invoke.mockResolvedValue({ decoration_layout: null });
    windowMocks.isFocused.mockResolvedValue(true);
    windowMocks.isFullscreen.mockResolvedValue(false);
    windowMocks.isMaximized.mockResolvedValue(false);
    windowMocks.minimize.mockResolvedValue(undefined);
    windowMocks.onFocusChanged.mockResolvedValue(windowMocks.unlistenFocus);
    windowMocks.onResized.mockResolvedValue(windowMocks.unlistenResize);
    windowMocks.startResizeDragging.mockResolvedValue(undefined);
    windowMocks.toggleMaximize.mockResolvedValue(undefined);
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    Reflect.deleteProperty(
      document.documentElement.dataset,
      "windowFullscreen",
    );
  });

  it("does not render desktop chrome in a browser", async () => {
    await act(async () => {
      root.render(<WindowChrome platform="browser" />);
    });

    expect(container.childElementCount).toBe(0);
  });

  it("keeps macOS native controls and renders no duplicate branding", async () => {
    await act(async () => {
      root.render(<WindowChrome platform="macos" />);
    });

    expect(container.querySelector(".window-chrome")).not.toBeNull();
    expect(
      container.querySelector(
        ".window-chrome__drag[data-tauri-drag-region]",
      ),
    ).not.toBeNull();
    expect(container.querySelector("button")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("");
    expect(
      container.querySelector(
        ".window-chrome__drag[data-tauri-drag-region]",
      ),
    ).not.toBeNull();
  });

  it("runs Windows controls and border resize actions", async () => {
    await act(async () => {
      root.render(<WindowChrome platform="windows" />);
    });

    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("");

    await act(async () => {
      control(container, "最小化窗口").click();
      control(container, "最大化窗口").click();
      control(container, "关闭窗口").click();
    });

    expect(windowMocks.minimize).toHaveBeenCalledOnce();
    expect(windowMocks.toggleMaximize).toHaveBeenCalledOnce();
    expect(windowMocks.close).toHaveBeenCalledOnce();

    const southeast = container.querySelector<HTMLDivElement>(
      ".window-resize-handle--southeast",
    );
    expect(southeast).not.toBeNull();
    await act(async () => {
      southeast?.dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true, button: 0 }),
      );
    });
    expect(windowMocks.startResizeDragging).toHaveBeenCalledWith(
      "SouthEast",
    );
  });

  it("uses the GTK control sides without adding a title", async () => {
    windowMocks.invoke.mockResolvedValue({
      decoration_layout: "close:minimize,maximize",
    });

    await act(async () => {
      root.render(<WindowChrome platform="linux" />);
    });
    await act(async () => undefined);

    expect(windowMocks.invoke).toHaveBeenCalledWith(
      "window_chrome_preferences",
    );
    expect(
      container.querySelector(
        '.window-chrome__controls--start button[aria-label="关闭窗口"]',
      ),
    ).not.toBeNull();
    expect(
      container.querySelectorAll(
        ".window-chrome__controls--end button",
      ),
    ).toHaveLength(2);
    expect(container.textContent).toBe("");
  });

  it("reflects maximized state and cleans up native listeners", async () => {
    windowMocks.isMaximized.mockResolvedValue(true);

    await act(async () => {
      root.render(<WindowChrome platform="windows" />);
    });
    await act(async () => undefined);

    expect(control(container, "还原窗口")).not.toBeNull();
    expect(container.querySelector(".window-resize-handle")).toBeNull();

    await act(async () => root.unmount());
    expect(windowMocks.unlistenFocus).toHaveBeenCalledOnce();
    expect(windowMocks.unlistenResize).toHaveBeenCalledOnce();
    root = createRoot(container);
  });
});
