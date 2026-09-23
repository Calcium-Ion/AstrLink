// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getTrayState: vi.fn(),
  trayAction: vi.fn(),
  trayPopoverHide: vi.fn(),
  trayPopoverResize: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("@tauri-apps/api/event", () => ({
  listen: vi.fn().mockResolvedValue(() => {}),
}));

import { applyQuotaDisplayMode } from "./quota-display";
import { applyLocale } from "./i18n";
import { defaultTrayPreferences } from "./preferences-model";
import { parseTrayState, type TrayAction } from "./tray-model";
import { readyTrayState } from "./tray-model.test";
import { TrayPopoverPanel, TrayPopoverWindow } from "./TrayPopover";

const now = new Date("2026-09-22T10:00:20Z");

function buttons(): string[] {
  return [...document.querySelectorAll("button")].map(
    (button) =>
      button.getAttribute("aria-label") ?? button.textContent?.trim() ?? "",
  );
}

describe("TrayPopoverPanel", () => {
  let container: HTMLDivElement;
  let root: Root;
  let actions: TrayAction[];

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    await applyLocale("zh-CN");
    applyQuotaDisplayMode("remaining");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    actions = [];
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function render(
    state: Parameters<typeof parseTrayState>[0],
    tray = defaultTrayPreferences(),
  ) {
    const parsed = parseTrayState(state);
    await act(async () => {
      root.render(
        <TrayPopoverPanel
          now={now}
          onAction={(action) => actions.push(action)}
          state={parsed}
          tray={tray}
        />,
      );
    });
  }

  it("hides without quitting through Close, Escape, or the transparent margin", async () => {
    bridge.getTrayState.mockResolvedValue(parseTrayState(readyTrayState));
    bridge.trayPopoverHide.mockReset().mockResolvedValue(undefined);
    bridge.trayAction.mockClear();
    await act(async () => root.render(<TrayPopoverWindow />));

    const panel = container.querySelector('[data-slot="tray-panel"]')!;
    await act(async () => {
      panel.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    });
    expect(bridge.trayPopoverHide).not.toHaveBeenCalled();

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>('button[aria-label="关闭"]')!
        .click();
    });
    expect(bridge.trayPopoverHide).toHaveBeenCalledTimes(1);

    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
      panel.parentElement!.dispatchEvent(
        new Event("pointerdown", { bubbles: true }),
      );
    });
    expect(bridge.trayPopoverHide).toHaveBeenCalledTimes(3);
    expect(bridge.trayAction).not.toHaveBeenCalled();
  });

  it("shows the gateway, today's numbers and subscription windows by default", async () => {
    await render(readyTrayState);
    const text = container.textContent ?? "";
    expect(text).toContain("网关运行中");
    expect(text).toContain("127.0.0.1:8317");
    expect(text).toContain("128");
    expect(text).toContain("1.2M");
    expect(text).toContain("$0.83");
    expect(text).toContain("3 次失败");
    expect(text).toContain("claude-sonnet-4 · 62%");
    expect(text).toContain("Codex · 5 小时");
    expect(text).toContain("2 小时后重置");
    // Off by default.
    expect(text).not.toContain("Cursor");
    expect(text).not.toContain("比昨天");
    expect(
      document.querySelector('[role="img"][aria-label="今日各小时 tokens"]'),
    ).not.toBeNull();
    expect(buttons()).toEqual(
      expect.arrayContaining([
        "复制 API 地址",
        "设置",
        "请求记录",
        "API 提供商",
        "访问令牌",
        "重启网关",
        "停止网关",
        "退出",
        "打开 AstrLink",
      ]),
    );
  });

  it("uses the shared quota mode for tray windows and updates already mounted meters", async () => {
    await render(readyTrayState);
    const meter = () =>
      container.querySelector(
        '[role="progressbar"][aria-label="Codex · 5 小时"]',
      )!;
    expect(meter().getAttribute("aria-valuenow")).toBe("38");
    expect(meter().getAttribute("aria-valuetext")).toBe("剩余 38%");
    await act(async () => applyQuotaDisplayMode("used"));
    expect(meter().getAttribute("aria-valuenow")).toBe("62");
    expect(meter().getAttribute("aria-valuetext")).toBe("已用 62%");
  });

  it("renders every optional card when enabled", async () => {
    const tray = defaultTrayPreferences();
    for (const key of Object.keys(tray.usage) as Array<keyof typeof tray.usage>)
      tray.usage[key] = true;
    tray.pages = [
      "records",
      "services",
      "tokens",
      "safety",
      "routing",
      "agent_tools",
    ];
    await render(readyTrayState, tray);
    const text = container.textContent ?? "";
    expect(text).toContain("比昨天↑ 23%");
    expect(text).toContain("缓存命中41%");
    expect(text).toContain("活跃客户端Cursor · 71%");
    expect(text).toContain("上次请求刚刚 · gpt-5 · 2.1 s");
    expect(text).toContain("本月48M tokens");
    expect(buttons()).toEqual(
      expect.arrayContaining(["安全策略", "路由", "Agent 工具"]),
    );
  });

  it("routes clicks to host actions", async () => {
    await render(readyTrayState);
    const click = (label: string) => {
      const button = [...document.querySelectorAll("button")].find(
        (candidate) =>
          (candidate.getAttribute("aria-label") ??
            candidate.textContent?.trim()) === label,
      );
      if (!button) throw new Error(`Missing button: ${label}`);
      act(() => button.click());
    };
    click("复制 API 地址");
    click("请求记录");
    click("API 提供商");
    click("重启网关");
    click("打开 AstrLink");
    click("退出");
    expect(actions).toEqual([
      { kind: "copy_address" },
      { kind: "navigate", page: "records" },
      { kind: "navigate", page: "list" },
      { kind: "core", op: "restart" },
      { kind: "open" },
      { kind: "quit" },
    ]);
  });

  it("offers a start button and hides usage while the gateway is down", async () => {
    await render({
      ...readyTrayState,
      view: {
        ...readyTrayState.view,
        phase: "error",
        inference_url: null,
        last_error: "astrlink-core exited with status 1",
        recovery_attempt: 2,
        recovery_scheduled: true,
      },
      digest: null,
      digest_age_ms: null,
    });
    const text = container.textContent ?? "";
    expect(text).toContain("网关异常退出");
    expect(text).toContain("正在自动恢复（第 2 次）");
    expect(text).toContain("astrlink-core exited with status 1");
    expect(text).toContain("网关运行后这里会显示今日用量。");
    expect(text).not.toContain("1.2M");
    expect(text).not.toContain("订阅额度");
    expect(buttons()).toContain("启动网关");
    expect(buttons()).not.toContain("停止网关");
    const copy = [...document.querySelectorAll("button")].find(
      (button) => button.getAttribute("aria-label") === "复制 API 地址",
    );
    expect(copy?.disabled).toBe(true);
  });

  it("folds subscription windows past ten rows and expands on demand", async () => {
    const subscriptions = Array.from({ length: 6 }, (_, index) => ({
      name: `Plan ${index + 1}`,
      windows: [
        {
          label: null,
          limit_window_seconds: 18_000,
          secondary: false,
          used_percent: 10 * index,
          reset_at: null,
        },
        {
          label: null,
          limit_window_seconds: 604_800,
          secondary: true,
          used_percent: 5 * index,
          reset_at: null,
        },
      ],
    }));
    await render({
      ...readyTrayState,
      digest: { ...readyTrayState.digest, subscriptions },
    });
    const rows = () =>
      document.querySelectorAll(
        '[data-slot="tray-subscriptions"] [data-slot="progress"]',
      ).length;
    expect(rows()).toBe(10);
    expect(container.textContent).toContain("10/12");
    const toggle = [...document.querySelectorAll("button")].find(
      (button) => button.textContent?.trim() === "展开其余 2 条",
    );
    expect(toggle).toBeDefined();
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    act(() => toggle?.click());
    expect(rows()).toBe(12);
    expect(container.textContent).toContain("12/12");
    const collapse = [...document.querySelectorAll("button")].find(
      (button) => button.textContent?.trim() === "收起",
    );
    expect(collapse?.getAttribute("aria-expanded")).toBe("true");
    act(() => collapse?.click());
    expect(rows()).toBe(10);

    // Ten rows or fewer never show the toggle.
    await render({
      ...readyTrayState,
      digest: {
        ...readyTrayState.digest,
        subscriptions: subscriptions.slice(0, 5),
      },
    });
    expect(rows()).toBe(10);
    expect(container.textContent).not.toContain("展开其余");
    expect(container.textContent).not.toContain("10/10");
  });

  it("labels provider-named limits and lists disabled plans too", async () => {
    await render({
      ...readyTrayState,
      digest: {
        ...readyTrayState.digest,
        subscriptions: [
          {
            name: "Kimi",
            windows: [
              {
                label: "Monthly",
                limit_window_seconds: 2_592_000,
                secondary: false,
                used_percent: 41.5,
                reset_at: null,
              },
            ],
          },
        ],
      },
    });
    expect(container.textContent).toContain("Kimi · Monthly");
    expect(container.textContent).toContain("剩余 59%");
  });

  it("flags an agent reading records through MCP", async () => {
    await render({
      ...readyTrayState,
      view: { ...readyTrayState.view, observer_active: true },
    });
    const badge = document.querySelector('[data-slot="tray-observed"]');
    expect(badge?.textContent).toBe("Agent 正在通过 MCP 读取");
    // Cost is shown without an unpriced caveat.
    expect(container.textContent).toContain("$0.83");
    expect(container.textContent).not.toContain("待计价");
  });

  it("hides what the preferences switch off", async () => {
    const tray = {
      ...defaultTrayPreferences(),
      copy_address: false,
      gateway_controls: false,
      pages: [] as never[],
    };
    await render(readyTrayState, tray);
    const labels = buttons();
    expect(labels).not.toContain("复制 API 地址");
    expect(labels).not.toContain("重启网关");
    expect(labels).not.toContain("请求记录");
    expect(labels).toEqual(
      expect.arrayContaining(["设置", "退出", "打开 AstrLink"]),
    );
  });
});
