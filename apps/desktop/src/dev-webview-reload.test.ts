// @vitest-environment happy-dom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  isTauriRuntime,
  startDevWebviewReload,
} from "./dev-webview-reload";

describe("dev webview reload", () => {
  afterEach(() => {
    vi.useRealTimers();
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
  });

  it("detects the Tauri runtime", () => {
    expect(isTauriRuntime(window)).toBe(false);
    Object.defineProperty(window, "__TAURI_INTERNALS__", {
      configurable: true,
      value: {},
    });
    expect(isTauriRuntime(window)).toBe(true);
  });

  it("reloads after the build id changes and on Cmd+R", async () => {
    vi.useFakeTimers();
    let buildId = "1";
    const reload = vi.fn();
    const stop = startDevWebviewReload({
      fetchImpl: async () => new Response(buildId),
      intervalMs: 1000,
      reload,
    });

    await vi.advanceTimersByTimeAsync(0);
    expect(reload).not.toHaveBeenCalled();

    buildId = "2";
    await vi.advanceTimersByTimeAsync(1000);
    expect(reload).toHaveBeenCalledTimes(1);

    window.dispatchEvent(
      new KeyboardEvent("keydown", { key: "r", metaKey: true }),
    );
    expect(reload).toHaveBeenCalledTimes(2);
    stop();
  });
});
