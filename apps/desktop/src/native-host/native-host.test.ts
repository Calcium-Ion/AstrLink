import { afterEach, describe, expect, it, vi } from "vitest";

const runtime = vi.hoisted(() => ({
  isMyGo: vi.fn(() => true),
  windowActive: vi.fn(() => true),
  onWindowActive: vi.fn(),
  currentWindow: {
    close: vi.fn(),
    minimize: vi.fn(),
    toggleMaximize: vi.fn(),
    isMaximized: vi.fn(async () => true),
  },
}));
const generated = vi.hoisted(() => ({
  coreStatus: vi.fn(),
  getService: vi.fn(),
  windowState: vi.fn(),
  resizedOn: vi.fn(),
  themeOn: vi.fn(),
}));

vi.mock("mygo-runtime", async (importOriginal) => ({
  ...(await importOriginal<typeof import("mygo-runtime")>()),
  ...runtime,
}));
vi.mock("../mygo", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../mygo")>();
  return {
    ...actual,
    Core: { ...actual.Core, coreStatus: generated.coreStatus },
    Services: { ...actual.Services, getService: generated.getService },
    Window: { ...actual.Window, windowState: generated.windowState },
    events: {
      ...actual.events,
      windowResized: { on: generated.resizedOn },
      themePreferenceChanged: { on: generated.themeOn },
    },
  };
});

import { invoke, isDesktopHost } from "./core";
import { listen } from "./event";
import { hasNativeHost } from "./presence";
import { getCurrentWindow } from "./window";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("invoke", () => {
  it("passes the camelCase arguments to the generated client", async () => {
    generated.getService.mockResolvedValue({ etag: "e", service: {} });
    await expect(invoke("get_service", { serviceId: "svc" })).resolves.toEqual({
      etag: "e",
      service: {},
    });
    expect(generated.getService).toHaveBeenCalledWith("svc");
  });

  it("rejects with the message string, as Tauri commands did", async () => {
    generated.coreStatus.mockRejectedValue(
      new Error("AstrLink is not running"),
    );
    await expect(invoke("core_status")).rejects.toBe("AstrLink is not running");
    await expect(invoke("no_such_command")).rejects.toBe(
      "unknown desktop command: no_such_command",
    );
  });
});

describe("listen", () => {
  it("hands the handler the payload in an event object", async () => {
    const unsubscribe = vi.fn();
    generated.themeOn.mockReturnValue(unsubscribe);
    const handler = vi.fn();
    const unlisten = await listen<string>("theme-preference-changed", handler);
    generated.themeOn.mock.calls[0][0]("dark");
    expect(handler).toHaveBeenCalledWith({ payload: "dark" });
    unlisten();
    expect(unsubscribe).toHaveBeenCalled();
    await expect(listen("no-such-event", vi.fn())).rejects.toThrow(
      "unknown desktop event: no-such-event",
    );
  });
});

describe("the host", () => {
  it("is present in a MyGo window only", () => {
    expect(hasNativeHost()).toBe(false);
    vi.stubGlobal("window", {});
    expect(hasNativeHost()).toBe(false);
    vi.stubGlobal("window", { mygo: {} });
    expect(hasNativeHost()).toBe(true);
    expect(isDesktopHost()).toBe(true);
  });
});

describe("getCurrentWindow", () => {
  it("reads the label the preload script set and the window's state", async () => {
    vi.stubGlobal("window", { __ASTRLINK_WINDOW_LABEL__: "tray-popover" });
    generated.windowState.mockResolvedValue({
      focused: true,
      full_screen: true,
      maximized: false,
    });
    const appWindow = getCurrentWindow();
    expect(appWindow.label).toBe("tray-popover");
    await expect(appWindow.isFullscreen()).resolves.toBe(true);
    await expect(appWindow.isMaximized()).resolves.toBe(true);
    await expect(appWindow.isFocused()).resolves.toBe(true);

    const focus = vi.fn();
    runtime.onWindowActive.mockReturnValue(() => undefined);
    await appWindow.onFocusChanged(focus);
    runtime.onWindowActive.mock.calls[0][0](false);
    expect(focus).toHaveBeenCalledWith({ payload: false });

    const resized = vi.fn();
    generated.resizedOn.mockReturnValue(() => undefined);
    await appWindow.onResized(resized);
    generated.resizedOn.mock.calls[0][0]({});
    expect(resized).toHaveBeenCalled();

    await appWindow.toggleMaximize();
    expect(runtime.currentWindow.toggleMaximize).toHaveBeenCalled();
  });

  it("has no label before the preload script ran and throws outside MyGo", () => {
    vi.stubGlobal("window", {});
    expect(getCurrentWindow().label).toBe("");
    runtime.isMyGo.mockReturnValueOnce(false);
    expect(() => getCurrentWindow()).toThrow();
  });
});
