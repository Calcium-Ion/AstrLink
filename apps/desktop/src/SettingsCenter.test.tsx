// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getPreferences: vi.fn(),
  restartCore: vi.fn(),
  startCore: vi.fn(),
  stopCore: vi.fn(),
  updatePreferences: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

import type { AppSnapshot } from "./core-model";
import { SettingsCenter } from "./SettingsCenter";

const snapshot = {
  phase: "ready",
  ready: { inference_url: "http://127.0.0.1:8317" },
  recovery_attempt: 0,
  recovery_scheduled_in_ms: null,
  last_error: null,
} as AppSnapshot;

const settings = {
  values: {
    close_behavior: "hide_to_tray" as const,
    autostart: false,
    core_auto_start: true,
    core_auto_recover: true,
    inference_port: 9000,
  },
  load_warning: null,
  autostart_actual: false,
  autostart_error: null,
};

describe("SettingsCenter", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    bridge.getPreferences.mockReset().mockResolvedValue(settings);
    bridge.updatePreferences.mockReset().mockResolvedValue(settings);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("shows active and saved ports truthfully and saves a validated draft", async () => {
    const onDirtyChange = vi.fn();
    await act(async () => {
      root.render(
        <SettingsCenter
          onCoreSnapshot={vi.fn()}
          onDirtyChange={onDirtyChange}
          snapshot={snapshot}
        />,
      );
      await Promise.resolve();
    });
    expect(container.textContent).toMatch(/正在使用[\s\S]*8317/);
    expect(container.textContent).toMatch(/已保存[\s\S]*9000/);
    expect(container.textContent).toContain("端口修改尚未生效");

    const input = container.querySelector<HTMLInputElement>('input[type="number"]');
    if (!input) throw new Error("missing port input");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      setter?.call(input, "9123");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const save = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "保存端口",
    );
    if (!save) throw new Error("missing save button");
    await act(async () => {
      save.click();
      await Promise.resolve();
    });
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({ inference_port: 9123 }),
    );
    expect(onDirtyChange).toHaveBeenCalledWith(true);
  });

  it("applies desktop and core preferences immediately", async () => {
    const onDirtyChange = vi.fn();
    bridge.updatePreferences.mockResolvedValue({
      ...settings,
      values: { ...settings.values, autostart: true },
      autostart_actual: true,
    });

    await act(async () => {
      root.render(
        <SettingsCenter
          onCoreSnapshot={vi.fn()}
          onDirtyChange={onDirtyChange}
          snapshot={snapshot}
        />,
      );
      await Promise.resolve();
    });

    const toggles = container.querySelectorAll<HTMLInputElement>(
      '.settings-toggle input[type="checkbox"]',
    );
    const autostart = toggles[0];
    if (!autostart) throw new Error("missing autostart toggle");

    await act(async () => {
      autostart.click();
      await Promise.resolve();
    });

    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({
        autostart: true,
        inference_port: 9000,
      }),
    );
    expect(onDirtyChange).not.toHaveBeenCalledWith(true);
  });
});
