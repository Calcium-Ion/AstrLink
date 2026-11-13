// @vitest-environment happy-dom

import { defaultUpdatePreferences } from "./update-model";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getCoreStatus: vi.fn(),
  getPreferences: vi.fn(),
  getTrayState: vi.fn(),
  trayAction: vi.fn(),
  getRoutingSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
  restartCore: vi.fn(),
  startCore: vi.fn(),
  stopCore: vi.fn(),
  updatePreferences: vi.fn(),
  listNetworkAddresses: vi.fn(),
  getSyncConfig: vi.fn(),
  getSyncStatus: vi.fn(),
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
import { settleClosedLayer } from "./lib/test-layer-settle";
import { defaultTrayPreferences } from "./preferences-model";
import { SettingsCenter } from "./SettingsCenter";
import { applyQuotaDisplayMode } from "./quota-display";
import { applyDisplayCurrency, USD_DISPLAY } from "./currency-display";
import { SubscriptionQuotaMeter } from "./components/SubscriptionQuotaMeter";
import { applyTheme } from "./theme";

const snapshot = {
  phase: "ready",
  ready: {
    inference_url: "http://127.0.0.1:8317",
    client_inference_url: "http://localhost:8317",
  },
  inference_port_fallback: null,
  inference_listen_active: "loopback",
  restart_pending: false,
  recovery_attempt: 0,
  recovery_scheduled_in_ms: null,
  last_error: null,
} as AppSnapshot;

const settings = {
  values: {
    close_behavior: "hide_to_tray" as const,
    autostart: false,
    autostart_hidden: false,
    core_auto_start: true,
    core_auto_recover: true,
    use_system_proxy: true,
    inference_port: 9000,
    inference_listen: "loopback" as const,
    max_concurrent_inspections: 16,
    response_start_timeout_seconds: 0,
    max_request_body_mib: 0,
    theme: "system" as const,
    quota_display_mode: "remaining" as const,
    display_currency: "auto" as const,
    locale: "zh-CN" as const,
    tray: defaultTrayPreferences(),
    updates: defaultUpdatePreferences(),
  },
  load_warning: null,
  autostart_actual: false,
  autostart_error: null,
  data_backups: [],
  local_key_storage: null,
};

describe("SettingsCenter", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    applyTheme("system");
    applyQuotaDisplayMode("remaining");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    bridge.getRoutingSettings
      .mockReset()
      .mockResolvedValue({ codex_identity_enforcement: true });
    bridge.updateRoutingSettings.mockReset();
    bridge.getPreferences.mockReset().mockResolvedValue(settings);
    bridge.getTrayState
      .mockReset()
      .mockRejectedValue(new Error("tray unavailable in tests"));
    bridge.updatePreferences.mockReset().mockResolvedValue(settings);
    bridge.getCoreStatus
      .mockReset()
      .mockResolvedValue({ ...snapshot, restart_pending: true });
    bridge.restartCore.mockReset().mockResolvedValue(snapshot);
    bridge.listNetworkAddresses.mockReset().mockResolvedValue({
      addresses: [
        { interface: "en0", ip: "192.168.1.20" },
        { interface: "en0", ip: "fd7a:115c:a1e0::1" },
      ],
    });
    bridge.getSyncConfig.mockReset().mockRejectedValue(
      // The sync tab is a separate surface; these tests exercise the other tabs.
      new Error("sync_unavailable"),
    );
    bridge.getSyncStatus
      .mockReset()
      .mockRejectedValue(new Error("sync_unavailable"));
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

  const render = async (
    props: Partial<Parameters<typeof SettingsCenter>[0]> = {},
  ) =>
    act(async () => {
      root.render(
        <SettingsCenter
          onCoreSnapshot={vi.fn()}
          onDirtyChange={vi.fn()}
          snapshot={snapshot}
          {...props}
        />,
      );
      await Promise.resolve();
    });

  const buttonNamed = (text: string, scope: ParentNode = container) =>
    [...scope.querySelectorAll<HTMLButtonElement>("button")].find(
      (button) => button.textContent === text,
    );

  /** Radix activates a tab on mouse down, not on click. */
  const openTab = async (label: string) => {
    const trigger = [
      ...container.querySelectorAll<HTMLButtonElement>('[role="tab"]'),
    ].find((node) => node.textContent === label);
    if (!trigger) throw new Error(`missing tab ${label}`);
    await act(async () => {
      trigger.dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true, button: 0 }),
      );
      await Promise.resolve();
    });
  };

  const option = (label: string) => {
    const node = [
      ...container.querySelectorAll<HTMLButtonElement>('[role="radio"]'),
    ].find((item) => item.textContent === label);
    if (!node) throw new Error(`missing option ${label}`);
    return node;
  };

  const switchNamed = (label: string) => {
    const name = [...container.querySelectorAll("label")].find(
      (node) => node.textContent === label,
    );
    const toggle = container.querySelector<HTMLButtonElement>(
      `[role="switch"][id="${name?.htmlFor}"]`,
    );
    if (!toggle) throw new Error(`missing switch ${label}`);
    return toggle;
  };

  const numberInput = (label: string) => {
    const input = container.querySelector<HTMLInputElement>(
      `input[aria-label="${label}"]`,
    );
    if (!input) throw new Error(`missing input ${label}`);
    return input;
  };

  const portInput = () => {
    const name = [...container.querySelectorAll("label")].find(
      (node) => node.textContent === "端口",
    );
    const input = container.querySelector<HTMLInputElement>(
      `input[id="${name?.htmlFor}"]`,
    );
    if (!input) throw new Error("missing port input");
    return input;
  };

  const edit = async (input: HTMLInputElement, value: string) =>
    act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set?.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });

  const save = async () => {
    const button = buttonNamed("保存");
    if (!button) throw new Error("missing save button");
    await act(async () => {
      button.click();
      await Promise.resolve();
    });
  };

  it("keeps forwarding identity controls in Routing instead of desktop settings", async () => {
    await render();
    expect(bridge.getRoutingSettings).not.toHaveBeenCalled();
    expect(
      container.querySelector('[data-testid="upstream-identity-settings"]'),
    ).toBeNull();
  });

  it("points out data-directory backups without offering to delete them", async () => {
    bridge.getPreferences.mockResolvedValue({
      ...settings,
      data_backups: [
        {
          name: "astrlink.db.bak-20260924-migration-34-repair",
          size_bytes: 2 * 1024 * 1024 * 1024,
          modified_unix: 1_790_000_000,
        },
        { name: "notes.bak", size_bytes: 512, modified_unix: null },
      ],
    });
    await render();
    expect(container.textContent).toContain(
      "数据目录中有 2 个备份文件（共 2.0 GB",
    );
    expect(container.textContent).toContain("AstrLink 不会删除它们");
    expect(
      container.querySelector("button[aria-label='数据目录中的备份文件']"),
    ).not.toBeNull();
    expect(
      [...container.querySelectorAll("button")].some((button) =>
        button.textContent?.includes("删除"),
      ),
    ).toBe(false);
  });

  it("shows one status line only when the keychain could not hold the local key", async () => {
    const text = "无法使用 macOS 钥匙串";
    for (const [local_key_storage, shown] of [
      ["keychain_unavailable", true],
      ["keychain", false],
      ["file", false],
    ] as const) {
      bridge.getPreferences.mockResolvedValue({
        ...settings,
        local_key_storage,
      });
      await act(async () =>
        root.render(
          <SettingsCenter
            key={local_key_storage}
            snapshot={snapshot}
            onCoreSnapshot={() => {}}
            onDirtyChange={() => {}}
          />,
        ),
      );
      expect(container.textContent?.includes(text)).toBe(shown);
    }
  });

  it("replaces the loading screen with the desktop timeout error", async () => {
    const message = "桌面程序长时间未响应，请完全退出 AstrLink 后重新打开。";
    bridge.getPreferences.mockRejectedValueOnce(new Error(message));

    await render();

    expect(container.textContent).toContain("无法加载设置");
    expect(container.textContent).toContain(message);
    expect(container.textContent).not.toContain("正在读取桌面与系统设置");
  });

  it("keeps window settings in General and the gateway in its own tab", async () => {
    await render({ snapshot: { ...snapshot, restart_pending: false } });
    expect(
      [...container.querySelectorAll('[role="tab"]')].map(
        (node) => node.textContent,
      ),
    ).toEqual(["常规", "网关与网络", "托盘面板", "同步与备份"]);
    expect(
      [...container.querySelectorAll("h2")].map((node) => node.textContent),
    ).toEqual(["外观", "窗口与启动"]);
    expect(container.textContent).toContain(
      "跟随系统时随系统的浅色和深色模式切换。",
    );
    expect(container.textContent).not.toContain("使用系统代理");

    await openTab("网关与网络");
    expect(
      [...container.querySelectorAll("h2")].map((node) => node.textContent),
    ).toEqual(["网关", "连接与请求"]);
    expect(container.querySelector('[data-tone="positive"]')?.textContent).toBe(
      "已就绪",
    );
    expect(container.textContent).toContain("请求处理");
    expect(container.textContent).toContain("以上修改点保存后，重启网关生效。");
    // A running gateway offers stop and restart; start waits for a stop.
    expect(buttonNamed("启动")).toBeUndefined();
    expect(buttonNamed("停止")?.disabled).toBe(false);
    expect(buttonNamed("重启")?.disabled).toBe(false);

    await openTab("托盘面板");
    expect(container.textContent).toContain(
      "点击托盘图标（左键或右键）展开这个面板。",
    );
  });

  it("starts, stops, and restarts the gateway with busy labels and recovery details", async () => {
    let finishStop: (next: AppSnapshot) => void = () => {};
    bridge.stopCore.mockReset().mockImplementation(
      () =>
        new Promise<AppSnapshot>((resolve) => {
          finishStop = resolve;
        }),
    );
    bridge.startCore.mockReset().mockResolvedValue(snapshot);
    const onCoreSnapshot = vi.fn();
    await render({ onCoreSnapshot });
    await openTab("网关与网络");

    await act(async () => buttonNamed("停止")!.click());
    expect(bridge.stopCore).toHaveBeenCalledTimes(1);
    expect(buttonNamed("停止中…")?.disabled).toBe(true);
    expect(buttonNamed("重启")?.disabled).toBe(true);
    const stopped = {
      ...snapshot,
      phase: "error",
      ready: null,
      recovery_attempt: 2,
      recovery_scheduled_in_ms: 4_200,
      last_error: "core exited with status 1",
    } as AppSnapshot;
    await act(async () => {
      finishStop(stopped);
      await Promise.resolve();
    });
    expect(onCoreSnapshot).toHaveBeenCalledWith(stopped);
    expect(notifyMocks.success).toHaveBeenCalledWith("网关已停止。");

    await render({ onCoreSnapshot, snapshot: stopped });
    expect(container.querySelector('[data-tone="negative"]')?.textContent).toBe(
      "需要处理",
    );
    expect(container.textContent).toContain("第 2/5 次恢复将在约 5 秒后进行");
    expect(container.querySelector("code")?.textContent).toBe(
      "core exited with status 1",
    );
    expect(buttonNamed("停止")).toBeUndefined();
    await act(async () => {
      buttonNamed("启动")!.click();
      await Promise.resolve();
    });
    expect(bridge.startCore).toHaveBeenCalledTimes(1);
    expect(notifyMocks.success).toHaveBeenCalledWith("网关正在启动。");
  });

  it("shows the port in use beside a pending one and saves a validated draft", async () => {
    const onDirtyChange = vi.fn();
    const onCoreSnapshot = vi.fn();
    await render({
      onCoreSnapshot,
      onDirtyChange,
      snapshot: { ...snapshot, restart_pending: true },
    });
    await openTab("网关与网络");
    expect(container.textContent).toContain(
      "正在使用 8317。保存并重启网关后改用 9000。",
    );
    expect(container.textContent).toContain("已保存的设置要重启网关后生效");

    const input = portInput();
    expect(input.getAttribute("aria-describedby")).toBeTruthy();
    await edit(input, "9123");
    expect(container.textContent).toContain(
      "正在使用 8317。保存并重启网关后改用 9123。",
    );
    expect(container.textContent).toContain(
      "有未保存的修改。保存后重启网关生效。",
    );
    // The footer offers the restart only once nothing waits to be saved.
    expect(buttonNamed("立即重启")).toBeUndefined();
    await save();
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({
        inference_port: 9123,
        use_system_proxy: true,
        max_concurrent_inspections: 16,
      }),
    );
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "已保存，重启网关后生效。",
      expect.objectContaining({ label: "立即重启" }),
    );
    // The pending restart shows without waiting for the next status poll.
    expect(bridge.getCoreStatus).toHaveBeenCalledTimes(1);
    expect(onCoreSnapshot).toHaveBeenCalledWith(
      expect.objectContaining({ restart_pending: true }),
    );
    expect(onDirtyChange).toHaveBeenCalledWith(true);
  });

  it("offers the restart a saved launch setting is waiting for", async () => {
    const onCoreSnapshot = vi.fn();
    await render({
      onCoreSnapshot,
      snapshot: { ...snapshot, restart_pending: true },
    });
    await openTab("网关与网络");
    const restartButtons = () =>
      [...container.querySelectorAll("button")].filter(
        (button) => button.textContent === "立即重启",
      );
    expect(restartButtons()).toHaveLength(1);
    await act(async () => {
      restartButtons()[0]!.click();
      await Promise.resolve();
    });
    expect(bridge.restartCore).toHaveBeenCalledTimes(1);
    expect(onCoreSnapshot).toHaveBeenCalledWith(snapshot);

    // Nothing waits on a restart: the footer goes back to its usual hint.
    await render({ onCoreSnapshot });
    expect(container.textContent).not.toContain("已保存的设置要重启网关后生效");
    expect(container.textContent).toContain("以上修改点保存后，重启网关生效。");
    expect(restartButtons()).toHaveLength(0);
  });

  it("says a launch setting saved while the gateway is stopped applies at the next start", async () => {
    await render({ snapshot: { ...snapshot, phase: "stopped" } });
    await openTab("网关与网络");
    await edit(numberInput("同时分析的请求数"), "32");
    await save();
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "已保存，下次启动网关时生效。",
    );
    expect(bridge.getCoreStatus).not.toHaveBeenCalled();
  });

  it("opens to the same network only after the consequences are confirmed", async () => {
    const onDirtyChange = vi.fn();
    await render({ onDirtyChange });
    await openTab("网关与网络");
    expect(option("仅这台电脑").getAttribute("aria-checked")).toBe("true");
    expect(container.textContent).not.toContain("网关正在对网络开放");

    await act(async () => option("同一网络的设备").click());
    const dialog = document.querySelector('[role="alertdialog"]');
    if (!dialog) throw new Error("missing confirmation dialog");
    expect(dialog.textContent).toContain("允许同一网络的设备连接？");
    expect(dialog.textContent).toContain("端口 9000");
    expect(dialog.textContent).toContain("访问令牌");
    // Nothing changes while the question is open.
    expect(option("仅这台电脑").getAttribute("aria-checked")).toBe("true");
    expect(onDirtyChange).not.toHaveBeenCalledWith(true);

    const keep = buttonNamed("保持仅这台电脑", dialog);
    if (!keep) throw new Error("missing keep button");
    await act(async () => {
      keep.click();
      await Promise.resolve();
    });
    await settleClosedLayer();
    expect(option("仅这台电脑").getAttribute("aria-checked")).toBe("true");
    expect(bridge.updatePreferences).not.toHaveBeenCalled();

    await act(async () => option("同一网络的设备").click());
    const confirm = buttonNamed(
      "对网络开放",
      document.querySelector('[role="alertdialog"]')!,
    );
    if (!confirm) throw new Error("missing confirm button");
    await act(async () => {
      confirm.click();
      await Promise.resolve();
    });
    await settleClosedLayer();
    expect(option("同一网络的设备").getAttribute("aria-checked")).toBe("true");
    expect(onDirtyChange).toHaveBeenCalledWith(true);
    await save();
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({
        inference_listen: "all_interfaces",
        inference_port: 9000,
      }),
    );
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "已保存，重启网关后生效。",
      expect.objectContaining({ label: "立即重启" }),
    );
  });

  it("lists reachable addresses while open to the network and reverts in one click", async () => {
    const exposedSettings = {
      ...settings,
      values: {
        ...settings.values,
        inference_listen: "all_interfaces" as const,
      },
    };
    bridge.getPreferences.mockResolvedValue(exposedSettings);
    bridge.updatePreferences.mockResolvedValue(settings);
    const onCoreSnapshot = vi.fn();
    await render({
      onCoreSnapshot,
      snapshot: { ...snapshot, inference_listen_active: "all_interfaces" },
    });
    await act(async () => {
      await Promise.resolve();
    });
    await openTab("网关与网络");
    expect(option("同一网络的设备").getAttribute("aria-checked")).toBe("true");
    expect(container.textContent).toContain("网关正在对网络开放，端口 8317");
    expect(container.textContent).toContain("http://192.168.1.20:8317");
    expect(container.textContent).toContain("http://[fd7a:115c:a1e0::1]:8317");
    expect(bridge.listNetworkAddresses).toHaveBeenCalledTimes(1);

    const revert = buttonNamed("改回仅这台电脑");
    if (!revert) throw new Error("missing revert button");
    await act(async () => {
      revert.click();
      await Promise.resolve();
    });
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({ inference_listen: "loopback" }),
    );
    // Saved loopback, Core still exposed: the host reports the pending restart.
    expect(onCoreSnapshot).toHaveBeenCalledWith(
      expect.objectContaining({ restart_pending: true }),
    );
  });

  it("explains a fallback without claiming the saved port is a pending edit", async () => {
    await render({
      snapshot: {
        ...snapshot,
        inference_port_fallback: {
          requested_port: 9000,
          active_port: 8317,
        },
      },
    });
    expect(container.textContent).toContain("端口 9000 已被占用");
    // Clients are told the address AstrLink writes for them.
    expect(container.textContent).toContain("http://localhost:8317");
    expect(container.textContent).toContain("请同步修改客户端 API 地址");
    await openTab("网关与网络");
    expect(container.textContent).toContain(
      "客户端填写的地址是 http://localhost:8317。",
    );
    expect(container.textContent).not.toContain("改用 9000");
    expect(container.textContent).not.toContain("已保存的设置要重启网关后生效");
    expect(bridge.updatePreferences).not.toHaveBeenCalled();
  });

  it("applies desktop and core preferences immediately", async () => {
    const onDirtyChange = vi.fn();
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
      autostart_actual: values.autostart,
    }));
    await render({ onDirtyChange });

    await act(async () => {
      switchNamed("开机自动启动").click();
      await Promise.resolve();
    });
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ autostart: true, inference_port: 9000 }),
    );

    await openTab("网关与网络");
    for (const [label, key] of [
      ["随 AstrLink 启动网关", "core_auto_start"],
      ["异常退出后自动重启", "core_auto_recover"],
    ] as const) {
      const toggle = switchNamed(label);
      expect(toggle.getAttribute("aria-describedby")).toBeTruthy();
      await act(async () => {
        toggle.click();
        await Promise.resolve();
      });
      expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
        expect.objectContaining({ [key]: false, inference_port: 9000 }),
      );
      expect(toggle.getAttribute("aria-checked")).toBe("false");
    }
    expect(onDirtyChange).not.toHaveBeenCalledWith(true);
  });

  it("remembers hiding to tray while autostart is toggled off and on", async () => {
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
      autostart_actual: values.autostart,
    }));
    await render();

    const autostart = switchNamed("开机自动启动");
    const hidden = switchNamed("自动启动时不显示主窗口");
    expect(hidden.labels?.[0]?.textContent).toBe("自动启动时不显示主窗口");
    expect(hidden.disabled).toBe(true);
    expect(hidden.getAttribute("aria-checked")).toBe("false");
    expect(container.textContent).toContain("手动打开 AstrLink 时照常显示窗口");
    await act(async () => hidden.click());
    expect(bridge.updatePreferences).not.toHaveBeenCalled();

    await act(async () => autostart.click());
    expect(hidden.disabled).toBe(false);
    await act(async () => hidden.click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ autostart: true, autostart_hidden: true }),
    );
    expect(hidden.getAttribute("aria-checked")).toBe("true");

    await act(async () => autostart.click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ autostart: false, autostart_hidden: true }),
    );
    expect(hidden.disabled).toBe(true);
    expect(hidden.getAttribute("aria-checked")).toBe("true");

    await act(async () => autostart.click());
    expect(hidden.disabled).toBe(false);
    expect(hidden.getAttribute("aria-checked")).toBe("true");
    await act(async () => hidden.click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ autostart: true, autostart_hidden: false }),
    );
  });

  it("applies the close behavior immediately", async () => {
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
    }));
    await render();
    expect(option("隐藏到托盘").getAttribute("aria-checked")).toBe("true");
    await act(async () => option("退出 AstrLink").click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ close_behavior: "quit" }),
    );
    expect(option("退出 AstrLink").getAttribute("aria-checked")).toBe("true");
  });

  it("switches the interface language immediately", async () => {
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
      autostart_actual: settings.autostart_actual,
    }));

    await render();
    expect(container.textContent).toContain("外观");
    expect(container.textContent).toContain("日本語");

    await act(async () => {
      option("English").click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({ locale: "en" }),
    );
    expect(container.textContent).toContain("Appearance");
    expect(container.textContent).toContain("Gateway & network");
    await applyLocale("zh-CN");
  });

  it("saves and applies the theme without saving pending entry edits or restarting", async () => {
    bridge.restartCore.mockReset();
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
    }));
    await render();
    await openTab("网关与网络");
    await edit(portInput(), "9123");
    await openTab("常规");
    await act(async () => option("暗色").click());
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({ theme: "dark", inference_port: 9000 }),
    );
    expect(document.documentElement.dataset.theme).toBe("dark");
    await openTab("网关与网络");
    expect(portInput().value).toBe("9123");
    expect(bridge.restartCore).not.toHaveBeenCalled();
  });

  it("saves quota display mode and immediately updates meters, preserving the saved mode on failure", async () => {
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
    }));
    await act(async () =>
      root.render(
        <>
          <SettingsCenter
            snapshot={snapshot}
            onCoreSnapshot={vi.fn()}
            onDirtyChange={vi.fn()}
          />
          <SubscriptionQuotaMeter label="Quota preview" usedPercent={85} />
        </>,
      ),
    );
    const meter = () => container.querySelector('[role="progressbar"]')!;
    expect(option("剩余百分比").getAttribute("aria-checked")).toBe("true");
    expect(meter().getAttribute("aria-valuetext")).toBe("剩余 15%");
    await act(async () => option("已用百分比").click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ quota_display_mode: "used" }),
    );
    expect(meter().getAttribute("aria-valuetext")).toBe("已用 85%");
    bridge.updatePreferences.mockRejectedValueOnce(
      new Error("无法保存额度显示设置"),
    );
    await act(async () => option("剩余百分比").click());
    expect(option("已用百分比").getAttribute("aria-checked")).toBe("true");
    expect(meter().getAttribute("aria-valuetext")).toBe("已用 85%");
    expect(container.textContent).toContain("无法保存额度显示设置");
    await act(async () => option("剩余百分比").click());
    expect(meter().getAttribute("aria-valuetext")).toBe("剩余 15%");
  });

  it("saves the display currency and shows the rate in use", async () => {
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
    }));
    applyDisplayCurrency({
      code: "CNY",
      symbol: "¥",
      digits: 2,
      per_usd: 6.6564,
      rate_date: "2026-11-01",
      wanted: "CNY",
      system: "CNY",
    });
    try {
      await render();
      const trigger = container.querySelector<HTMLButtonElement>(
        'button[role="combobox"][aria-label="货币"]',
      )!;
      expect(trigger.textContent).toBe("跟随系统（人民币）");
      expect(container.textContent).toContain(
        "按 11月1日 汇率，1 美元 ≈ ¥6.66",
      );
      await act(async () => {
        trigger.dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        );
        await Promise.resolve();
      });
      const item = [
        ...document.querySelectorAll<HTMLElement>('[role="option"]'),
      ].find((candidate) => candidate.textContent?.trim() === "日元 JPY")!;
      await act(async () => {
        item.click();
        await Promise.resolve();
      });
      await settleClosedLayer();
      expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
        expect.objectContaining({ display_currency: "JPY" }),
      );
      // Host pushes the new currency once it has a rate; until then dollars.
      await act(async () =>
        applyDisplayCurrency({ ...USD_DISPLAY, wanted: "JPY", system: "CNY" }),
      );
      expect(container.textContent).toContain("还没取到汇率，暂按美元显示。");
    } finally {
      applyDisplayCurrency(USD_DISPLAY);
    }
  });

  it("retains the active theme and selection if saving fails", async () => {
    applyTheme("dark");
    bridge.getPreferences.mockResolvedValue({
      ...settings,
      values: { ...settings.values, theme: "dark" },
    });
    bridge.updatePreferences.mockRejectedValue(new Error("无法保存外观设置"));
    await render();
    await act(async () => option("浅色").click());
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(option("暗色").getAttribute("aria-checked")).toBe("true");
    expect(container.textContent).toContain("无法保存外观设置");
  });

  it("saves the system proxy switch with the other launch settings", async () => {
    bridge.restartCore.mockReset();
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
    }));
    const onDirtyChange = vi.fn();
    await render({ onDirtyChange });
    await openTab("网关与网络");
    const proxy = () => switchNamed("使用系统代理");
    expect(proxy().getAttribute("aria-checked")).toBe("true");
    expect(proxy().getAttribute("aria-describedby")).toBeTruthy();

    await act(async () => proxy().click());
    expect(bridge.updatePreferences).not.toHaveBeenCalled();
    expect(proxy().getAttribute("aria-checked")).toBe("false");
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    expect(container.textContent).toContain(
      "有未保存的修改。保存后重启网关生效。",
    );

    // An instant setting saves the stored proxy value, not the draft.
    await act(async () => switchNamed("随 AstrLink 启动网关").click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({
        core_auto_start: false,
        use_system_proxy: true,
      }),
    );
    expect(proxy().getAttribute("aria-checked")).toBe("false");

    await save();
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({
        core_auto_start: false,
        use_system_proxy: false,
        inference_port: 9000,
      }),
    );
    expect(proxy().getAttribute("aria-checked")).toBe("false");
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    expect(bridge.restartCore).not.toHaveBeenCalled();
    expect(notifyMocks.success).toHaveBeenCalledWith(
      "已保存，重启网关后生效。",
      expect.objectContaining({ label: "立即重启" }),
    );

    bridge.updatePreferences.mockRejectedValueOnce(new Error("write failed"));
    await act(async () => proxy().click());
    await save();
    expect(container.textContent).toContain("write failed");
    // The failed draft stays for another try.
    expect(proxy().getAttribute("aria-checked")).toBe("true");
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
  });

  it("saves a higher inspection concurrency for the next gateway start", async () => {
    await render();
    await openTab("网关与网络");
    const input = numberInput("同时分析的请求数");
    expect(input.min).toBe("4");
    expect(input.max).toBe("128");
    await edit(input, "32");
    await save();
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({
        inference_port: 9000,
        max_concurrent_inspections: 32,
      }),
    );
  });

  it("saves an explicit provider reply wait for the next gateway start", async () => {
    await render();
    await openTab("网关与网络");
    await edit(numberInput("等待提供商回复"), "300");
    await save();
    expect(bridge.updatePreferences).toHaveBeenCalledWith(
      expect.objectContaining({
        inference_port: 9000,
        response_start_timeout_seconds: 300,
      }),
    );
  });

  it("saves a body limit, preserves the draft during instant changes, and restores unlimited", async () => {
    const onDirtyChange = vi.fn();
    bridge.updatePreferences.mockImplementation(async (values) => ({
      ...settings,
      values,
    }));
    await render({ onDirtyChange });
    await openTab("网关与网络");
    const input = numberInput("单个请求大小上限");
    const saveButton = buttonNamed("保存");
    if (!saveButton) throw new Error("missing save button");
    expect(input.value).toBe("0");
    expect(input.getAttribute("aria-invalid")).toBeNull();
    await edit(input, "64");
    expect(onDirtyChange).toHaveBeenCalledWith(true);
    await act(async () => switchNamed("随 AstrLink 启动网关").click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ max_request_body_mib: 0 }),
    );
    expect(input.value).toBe("64");
    await act(async () => saveButton.click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ max_request_body_mib: 64 }),
    );
    expect(input.value).toBe("64");
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    for (const invalid of ["-1", "1.5", "4294967296"]) {
      await edit(input, invalid);
      expect(saveButton.disabled).toBe(true);
      expect(input.getAttribute("aria-invalid")).toBe("true");
    }
    await edit(input, "0");
    expect(input.getAttribute("aria-invalid")).toBeNull();
    await act(async () => saveButton.click());
    expect(bridge.updatePreferences).toHaveBeenLastCalledWith(
      expect.objectContaining({ max_request_body_mib: 0 }),
    );
  });
});
