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

import {
  WindowChrome,
  WindowChromeAccessory,
  WindowChromeProvider,
} from "./WindowChrome";

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
    Reflect.deleteProperty(document.documentElement.dataset, "windowControls");
  });

  it("does not render desktop chrome in a browser", async () => {
    await act(async () => {
      root.render(<WindowChrome platform="browser" />);
    });

    expect(container.childElementCount).toBe(0);
    expect(document.documentElement.dataset.windowControls).toBeUndefined();
  });

  it("keeps macOS native controls and renders no duplicate branding", async () => {
    await act(async () => {
      root.render(<WindowChrome platform="macos" />);
    });

    expect(
      container.querySelector('header[aria-label="窗口控制栏"]'),
    ).not.toBeNull();
    expect(
      container.querySelector(
        '[data-slot="window-drag-region"][data-tauri-drag-region]',
      ),
    ).not.toBeNull();
    expect(container.querySelector("button")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("");
    // The traffic lights take the start of the bar.
    expect(document.documentElement.dataset.windowControls).toBe("start");
    expect(
      container.querySelector(
        '[data-slot="window-drag-region"][data-tauri-drag-region]',
      ),
    ).not.toBeNull();
  });

  it("runs Windows controls and border resize actions", async () => {
    await act(async () => {
      root.render(<WindowChrome platform="windows" />);
    });

    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toBe("");
    expect(document.documentElement.dataset.windowControls).toBe("end");
    // Caption glyphs: a bar, a box, and a cross.
    expect(
      control(container, "最小化窗口").querySelector("path")?.getAttribute("d"),
    ).toBe("M3 8.5h10");
    expect(
      control(container, "最大化窗口").querySelectorAll("rect, path"),
    ).toHaveLength(1);
    expect(
      control(container, "关闭窗口").querySelector("path")?.getAttribute("d"),
    ).toBe("m3.5 3.5 9 9m0-9-9 9");

    await act(async () => {
      control(container, "最小化窗口").click();
      control(container, "最大化窗口").click();
      control(container, "关闭窗口").click();
    });

    expect(windowMocks.minimize).toHaveBeenCalledOnce();
    expect(windowMocks.toggleMaximize).toHaveBeenCalledOnce();
    expect(windowMocks.close).toHaveBeenCalledOnce();

    const southeast = container.querySelector<HTMLDivElement>(
      '[data-slot="window-resize-handle"][data-direction="southeast"]',
    );
    expect(southeast).not.toBeNull();
    await act(async () => {
      southeast?.dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true, button: 0 }),
      );
    });
    expect(windowMocks.startResizeDragging).toHaveBeenCalledWith("SouthEast");
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
        '[data-slot="window-controls"][data-placement="start"] button[aria-label="关闭窗口"]',
      ),
    ).not.toBeNull();
    expect(
      container.querySelectorAll(
        '[data-slot="window-controls"][data-placement="end"] button',
      ),
    ).toHaveLength(2);
    expect(container.textContent).toBe("");
    expect(document.documentElement.dataset.windowControls).toBe("start");
  });

  it("frees the start of the bar when GTK puts every button at the end", async () => {
    await act(async () => {
      root.render(<WindowChrome platform="linux" />);
    });
    await act(async () => undefined);

    expect(
      container.querySelector(
        '[data-slot="window-controls"][data-placement="start"]',
      ),
    ).toBeNull();
    expect(document.documentElement.dataset.windowControls).toBe("end");
  });

  it("draws a surface's accessory in the title bar, before the controls", async () => {
    await act(async () => {
      root.render(
        <WindowChromeProvider>
          <WindowChrome platform="windows" />
          <main>
            <WindowChromeAccessory>
              <button type="button">置顶</button>
            </WindowChromeAccessory>
          </main>
        </WindowChromeProvider>,
      );
    });

    const accessory = container.querySelector(
      'header [data-slot="window-accessory"]',
    );
    expect(accessory?.textContent).toBe("置顶");
    expect(container.querySelector("main")?.childElementCount).toBe(0);
    // Its own element rather than a child of the drag region, so a click on it
    // does not start dragging the window.
    expect(accessory?.hasAttribute("data-tauri-drag-region")).toBe(false);
    expect(
      accessory?.compareDocumentPosition(
        container.querySelector('[data-slot="window-controls"]')!,
      ),
    ).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it("drops an accessory where there is no title bar", async () => {
    await act(async () => {
      root.render(
        <WindowChromeAccessory>
          <button type="button">置顶</button>
        </WindowChromeAccessory>,
      );
    });

    expect(container.textContent).toBe("");
  });

  it("reflects maximized state and cleans up native listeners", async () => {
    windowMocks.isMaximized.mockResolvedValue(true);

    await act(async () => {
      root.render(<WindowChrome platform="windows" />);
    });
    await act(async () => undefined);

    // Two stacked boxes while maximized.
    expect(
      control(container, "还原窗口").querySelectorAll("rect, path"),
    ).toHaveLength(2);
    expect(
      container.querySelector('[data-slot="window-resize-handle"]'),
    ).toBeNull();

    await act(async () => root.unmount());
    expect(windowMocks.unlistenFocus).toHaveBeenCalledOnce();
    expect(windowMocks.unlistenResize).toHaveBeenCalledOnce();
    expect(document.documentElement.dataset.windowControls).toBeUndefined();
    root = createRoot(container);
  });
});
