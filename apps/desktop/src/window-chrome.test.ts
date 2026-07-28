// @vitest-environment happy-dom

import { afterEach, describe, expect, it } from "vitest";

import {
  getDesktopPlatform,
  parseLinuxDecorationLayout,
} from "./window-chrome";

afterEach(() => {
  Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  Reflect.deleteProperty(window, "__ASTRLINK_DESKTOP_PLATFORM__");
});

describe("desktop platform detection", () => {
  it("keeps browser previews free of desktop chrome", () => {
    window.__ASTRLINK_DESKTOP_PLATFORM__ = "windows";
    expect(getDesktopPlatform()).toBe("browser");
  });

  it.each(["linux", "macos", "windows"] as const)(
    "accepts the injected %s Tauri platform",
    (platform) => {
      Object.defineProperty(window, "__TAURI_INTERNALS__", {
        configurable: true,
        value: {},
      });
      window.__ASTRLINK_DESKTOP_PLATFORM__ = platform;

      expect(getDesktopPlatform()).toBe(platform);
    },
  );

  it("fails closed when an unknown platform is injected", () => {
    Object.defineProperty(window, "__TAURI_INTERNALS__", {
      configurable: true,
      value: {},
    });
    window.__ASTRLINK_DESKTOP_PLATFORM__ = "unknown";

    expect(getDesktopPlatform()).toBe("browser");
  });
});

describe("Linux decoration layout", () => {
  it("keeps the GTK side and order for supported controls", () => {
    expect(
      parseLinuxDecorationLayout("close:minimize,maximize"),
    ).toEqual({
      start: ["close"],
      end: ["minimize", "maximize"],
    });
  });

  it("ignores unsupported GTK widgets and duplicate controls", () => {
    expect(
      parseLinuxDecorationLayout(
        "menu,close:appmenu,minimize,maximize,close",
      ),
    ).toEqual({
      start: ["close"],
      end: ["minimize", "maximize"],
    });
  });

  it.each([null, "", "menu:appmenu", "invalid"])(
    "uses a conventional fallback for %s",
    (layout) => {
      expect(parseLinuxDecorationLayout(layout)).toEqual({
        start: [],
        end: ["minimize", "maximize", "close"],
      });
    },
  );
});
