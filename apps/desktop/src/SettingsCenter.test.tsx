// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getPreferences: vi.fn(),
  getRoutingSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
  restartCore: vi.fn(),
  startCore: vi.fn(),
  stopCore: vi.fn(),
  updatePreferences: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

const notifyMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));
vi.mock("./notify", () => ({ notify: notifyMocks }));

import { applyLocale } from "./i18n";
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
    max_concurrent_inspections: 16,
    response_start_timeout_seconds: 0,
    locale: "zh-CN" as const,
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
    bridge.getRoutingSettings.mockReset();
    bridge.updateRoutingSettings.mockReset();
    bridge.getPreferences.mockReset().mockResolvedValue(settings);
    bridge.updatePreferences.mockReset().mockResolvedValue(settings);
    notifyMocks.success.mockReset();
    notifyMocks.error.mockReset();
    notifyMocks.warning.mockReset();
  });

  afterEach(async () => {
    await applyLocale("zh-CN");
    await act(async () => root.unmount());
    container.remove();
  });

  it("does not mount or read routing settings", async () => {
    await act(async () => root.render(<SettingsCenter snapshot={snapshot} onCoreSnapshot={() => {}} onDirtyChange={() => {}} />));
    expect(bridge.getRoutingSettings).not.toHaveBeenCalled();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(container.querySelector('[data-testid="routing-defaults-panel"]')).toBeNull();
    expect(container.textContent).not.toContain("默认失败处理");
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
    expect(container.textContent).toContain("入口修改尚未生效");

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
      (button) => button.textContent === "保存",
    );
    if (!save) throw new Error("missing save button");
    await act(async () => {
      save.click();
      await Promise.resolve();
    });
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({
        inference_port: 9123,
        max_concurrent_inspections: 16,
      }),
    );
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "入口设置已保存。重启网关后生效。",
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

    const toggles = container.querySelectorAll<HTMLButtonElement>('[role="switch"]');
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

  it("switches the interface language immediately", async () => {
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
      autostart_actual: settings.autostart_actual,
    }));

    await act(async () => {
      root.render(
        <SettingsCenter
          onCoreSnapshot={vi.fn()}
          onDirtyChange={vi.fn()}
          snapshot={snapshot}
        />,
      );
      await Promise.resolve();
    });
    expect(container.textContent).toContain("推理入口");

    const english = [...container.querySelectorAll('[role="radio"]')].find(
      (node) => node.getAttribute("aria-label") === "English",
    );
    if (!english) throw new Error("missing English language option");
    await act(async () => {
      english.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({ locale: "en" }),
    );
    expect(container.textContent).toContain("Inference entry");
    await applyLocale("zh-CN");
  });

  it("saves a higher inspection concurrency for the next gateway start", async () => {
    await act(async () => {
      root.render(
        <SettingsCenter
          onCoreSnapshot={vi.fn()}
          onDirtyChange={vi.fn()}
          snapshot={snapshot}
        />,
      );
      await Promise.resolve();
    });
    const input = container.querySelector<HTMLInputElement>(
      'input[aria-label="检查并发"]',
    );
    if (!input) throw new Error("missing concurrency input");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      setter?.call(input, "32");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const save = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "保存",
    );
    if (!save) throw new Error("missing save button");
    await act(async () => {
      save.click();
      await Promise.resolve();
    });
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({
        inference_port: 9000,
        max_concurrent_inspections: 32,
      }),
    );
  });

  it("saves an explicit response-header wait for the next gateway start", async () => {
    await act(async () => {
      root.render(
        <SettingsCenter
          onCoreSnapshot={vi.fn()}
          onDirtyChange={vi.fn()}
          snapshot={snapshot}
        />,
      );
      await Promise.resolve();
    });
    const input = container.querySelector<HTMLInputElement>(
      'input[aria-label="响应头等待"]',
    );
    if (!input) throw new Error("missing response-header wait input");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      setter?.call(input, "300");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const save = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "保存",
    );
    if (!save) throw new Error("missing save button");
    await act(async () => {
      save.click();
      await Promise.resolve();
    });
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({
        inference_port: 9000,
        response_start_timeout_seconds: 300,
      }),
    );
  });
});
