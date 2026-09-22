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
import { applyTheme } from "./theme";

const snapshot = {
  phase: "ready",
  ready: { inference_url: "http://127.0.0.1:8317" },
  inference_port_fallback: null,
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
    use_system_proxy: true,
    inference_port: 9000,
    max_concurrent_inspections: 16,
    response_start_timeout_seconds: 0,
    max_request_body_mib: 0,
    theme: "system" as const,
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
    applyTheme("system");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    bridge.getRoutingSettings.mockReset().mockResolvedValue({ codex_identity_enforcement: true });
    bridge.updateRoutingSettings.mockReset();
    bridge.getPreferences.mockReset().mockResolvedValue(settings);
    bridge.updatePreferences.mockReset().mockResolvedValue(settings);
    notifyMocks.success.mockReset();
    notifyMocks.error.mockReset();
    notifyMocks.warning.mockReset();
  });

  afterEach(async () => {
    applyTheme("system");
    await applyLocale("zh-CN");
    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps forwarding identity controls in Routing instead of desktop settings", async () => {
    await act(async () => root.render(<SettingsCenter snapshot={snapshot} onCoreSnapshot={() => {}} onDirtyChange={() => {}} />));
    expect(bridge.getRoutingSettings).not.toHaveBeenCalled();
    expect(container.querySelector('[data-testid="upstream-identity-settings"]')).toBeNull();
  });

  it("replaces the loading screen with the desktop timeout error", async () => {
    const message = "桌面程序长时间未响应，请完全退出 AstrLink 后重新打开。";
    bridge.getPreferences.mockRejectedValueOnce(new Error(message));

    await act(async () => root.render(
      <SettingsCenter snapshot={snapshot} onCoreSnapshot={vi.fn()} onDirtyChange={vi.fn()} />,
    ));

    expect(container.textContent).toContain("无法加载设置");
    expect(container.textContent).toContain(message);
    expect(container.textContent).not.toContain("正在读取桌面与系统设置");
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

  it("explains a fallback without claiming the saved port is a pending edit", async () => {
    await act(async () => root.render(
      <SettingsCenter
        snapshot={{ ...snapshot, inference_port_fallback: { requested_port: 9000, active_port: 8317 } }}
        onCoreSnapshot={vi.fn()}
        onDirtyChange={vi.fn()}
      />,
    ));
    expect(container.textContent).toContain("端口 9000 已被占用");
    expect(container.textContent).toContain("http://127.0.0.1:8317");
    expect(container.textContent).toContain("请同步修改客户端 API 地址");
    expect(container.textContent).not.toContain("入口修改尚未生效");
    expect(bridge.updatePreferences).not.toHaveBeenCalled();
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

  it("saves and applies the theme without saving pending entry edits or restarting", async () => {
    bridge.restartCore.mockReset();
    bridge.updatePreferences.mockImplementation(async (values) => ({ ...settings, values }));
    await act(async () => root.render(
      <SettingsCenter snapshot={snapshot} onCoreSnapshot={vi.fn()} onDirtyChange={vi.fn()} />,
    ));
    const input = container.querySelector<HTMLInputElement>('input[type="number"]')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, "9123");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await act(async () => container.querySelector<HTMLButtonElement>('[role="radio"][aria-label="暗色"]')!.click());
    expect(bridge.updatePreferences).toHaveBeenCalledWith(expect.objectContaining({ theme: "dark", inference_port: 9000 }));
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(input.value).toBe("9123");
    expect(bridge.restartCore).not.toHaveBeenCalled();
  });

  it("retains the active theme and selection if saving fails", async () => {
    applyTheme("dark");
    bridge.getPreferences.mockResolvedValue({ ...settings, values: { ...settings.values, theme: "dark" } });
    bridge.updatePreferences.mockRejectedValue(new Error("无法保存外观设置"));
    await act(async () => root.render(
      <SettingsCenter snapshot={snapshot} onCoreSnapshot={vi.fn()} onDirtyChange={vi.fn()} />,
    ));
    await act(async () => container.querySelector<HTMLButtonElement>('[role="radio"][aria-label="浅色"]')!.click());
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(container.querySelector('[role="radio"][aria-label="暗色"]')?.getAttribute("aria-checked")).toBe("true");
    expect(container.textContent).toContain("无法保存外观设置");
  });

  it("persists the system proxy switch without interrupting the running gateway", async () => {
    bridge.restartCore.mockReset();
    bridge.updatePreferences.mockImplementation(async (values) => ({ ...settings, values }));
    await act(async () => root.render(
      <SettingsCenter snapshot={snapshot} onCoreSnapshot={vi.fn()} onDirtyChange={vi.fn()} />,
    ));
    const label = [...container.querySelectorAll("label")].find((node) => node.textContent?.startsWith("使用系统代理"));
    const toggle = container.querySelector<HTMLButtonElement>(`[id="${label?.htmlFor}"]`);
    if (!toggle) throw new Error("missing system proxy toggle");
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await act(async () => toggle.click());
    expect(bridge.updatePreferences).toHaveBeenCalledWith(expect.objectContaining({ use_system_proxy: false }));
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    expect(bridge.restartCore).not.toHaveBeenCalled();
    expect(notifyMocks.success).toHaveBeenCalledWith("已保存，重启网关后生效。");

    bridge.updatePreferences.mockRejectedValueOnce(new Error("write failed"));
    await act(async () => toggle.click());
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    expect(container.textContent).toContain("write failed");
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
  it("saves a body limit, preserves the draft during instant changes, and restores unlimited", async () => {
    const onDirtyChange = vi.fn();
    bridge.updatePreferences.mockImplementation(async (values) => ({ ...settings, values }));
    await act(async () => root.render(
      <SettingsCenter snapshot={snapshot} onCoreSnapshot={vi.fn()} onDirtyChange={onDirtyChange} />,
    ));
    const input = container.querySelector<HTMLInputElement>('input[aria-label="请求体大小上限（MiB）"]');
    const save = [...container.querySelectorAll("button")].find((button) => button.textContent === "保存");
    if (!input || !save) throw new Error("missing body limit controls");
    expect(input.value).toBe("0");
    expect(container.textContent).toContain("不限制");
    const edit = async (value: string) => act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await edit("64");
    expect(onDirtyChange).toHaveBeenCalledWith(true);
    const toggle = container.querySelector<HTMLButtonElement>('[role="switch"]')!;
    await act(async () => toggle.click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(expect.objectContaining({ max_request_body_mib: 0 }));
    expect(input.value).toBe("64");
    await act(async () => save.click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(expect.objectContaining({ max_request_body_mib: 64 }));
    expect(container.textContent).toContain("64 MiB");
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    for (const invalid of ["-1", "1.5", "4294967296"]) {
      await edit(invalid);
      expect(save.disabled).toBe(true);
      expect(input.getAttribute("aria-invalid")).toBe("true");
    }
    await edit("0");
    await act(async () => save.click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(expect.objectContaining({ max_request_body_mib: 0 }));
    expect(container.textContent).toContain("不限制");
  });

});
