// @vitest-environment happy-dom

import { readFileSync } from "node:fs";
import { URL as NodeURL } from "node:url";
import { runInNewContext } from "node:vm";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { applyTheme, initializeTheme } from "./theme";
import { THEME_STORAGE_KEY } from "./theme-model";

describe("desktop theme", () => {
  let media: MediaQueryList;
  let stop: (() => void) | undefined;

  function changeSystem(dark: boolean) {
    Object.defineProperty(media, "matches", { configurable: true, value: dark });
    media.dispatchEvent(new Event("change"));
  }

  beforeEach(() => {
    localStorage.clear();
    media = Object.assign(new EventTarget(), { matches: false }) as MediaQueryList;
    vi.spyOn(window, "matchMedia").mockReturnValue(media);
  });

  afterEach(() => {
    stop?.();
    stop = undefined;
    vi.restoreAllMocks();
    localStorage.clear();
    delete document.documentElement.dataset.theme;
    document.documentElement.style.colorScheme = "";
  });

  it("defaults to system and follows changes while open", () => {
    stop = initializeTheme();
    expect(document.documentElement.dataset.theme).toBe("light");
    changeSystem(true);
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.documentElement.style.colorScheme).toBe("dark");
    changeSystem(false);
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("preserves explicit choices across system changes and reloads", () => {
    stop = initializeTheme();
    applyTheme("dark");
    changeSystem(false);
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark");
    stop();
    stop = initializeTheme();
    expect(document.documentElement.dataset.theme).toBe("dark");
    applyTheme("light");
    changeSystem(true);
    expect(document.documentElement.dataset.theme).toBe("light");
    applyTheme("system");
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("synchronizes other windows and removes observers on cleanup", () => {
    stop = initializeTheme();
    window.dispatchEvent(new StorageEvent("storage", { key: THEME_STORAGE_KEY, newValue: "dark" }));
    expect(document.documentElement.dataset.theme).toBe("dark");
    window.dispatchEvent(new StorageEvent("storage", { key: THEME_STORAGE_KEY, newValue: "system" }));
    stop();
    changeSystem(true);
    window.dispatchEvent(new StorageEvent("storage", { key: THEME_STORAGE_KEY, newValue: "dark" }));
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("survives invalid or unavailable browser storage", () => {
    localStorage.setItem(THEME_STORAGE_KEY, "unknown");
    changeSystem(true);
    stop = initializeTheme();
    expect(document.documentElement.dataset.theme).toBe("dark");
    stop();
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    stop = initializeTheme();
    expect(() => applyTheme("light")).not.toThrow();
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it.each([
    [null, true, "dark"],
    ["system", false, "light"],
    ["light", true, "light"],
    ["dark", false, "dark"],
    ["invalid", true, "dark"],
  ])("applies %s before the app paints (system dark: %s)", (cached, systemDark, expected) => {
    const root = { dataset: {} as Record<string, string>, style: { colorScheme: "" } };
    runInNewContext(readFileSync(new NodeURL("../public/theme-init.js", import.meta.url), "utf8"), {
      document: { documentElement: root },
      localStorage: { getItem: () => cached },
      window: { matchMedia: () => ({ matches: systemDark }) },
    });
    expect(root.dataset.theme).toBe(expected);
    expect(root.style.colorScheme).toBe(expected);
  });
});
