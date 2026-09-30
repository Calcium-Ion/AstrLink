// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getTrayState: vi.fn(),
  listRawAccess: vi.fn(),
  revokeRawGrant: vi.fn(),
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

  it("opens the popover when the host snapshot includes Antigravity usage", async () => {
    const snapshot = {
      ...readyTrayState,
      digest: {
        ...readyTrayState.digest,
        subscriptions: [
          ...readyTrayState.digest.subscriptions,
          {
            name: "Antigravity",
            kind: "antigravity_subscription",
            windows: [
              {
                label: "claude-sonnet-4-6",
                limit_window_seconds: null,
                secondary: false,
                used_percent: 0.01,
                reset_at: null,
              },
            ],
          },
        ],
      },
    };
    bridge.getTrayState.mockImplementation(async () =>
      parseTrayState(snapshot),
    );
    bridge.trayPopoverResize.mockResolvedValue(undefined);

    await act(async () => root.render(<TrayPopoverWindow />));

    expect(container.querySelector('[data-slot="tray-panel"]')).not.toBeNull();
    expect(container.textContent).toContain("Antigravity");
    expect(container.textContent).toContain("claude-sonnet-4-6");
  });

  it("lists running raw grants and revokes one", async () => {
    const running = {
      grant_id: "rawgrant_2222222222222222",
      request_id: "req_raw_01",
      status: "approved",
      decision: "window_5m",
      scope: "all_requests",
      reason: "debugging",
      client_name: "Codex",
      created_at: "2026-09-28T10:00:00Z",
      expires_at: new Date(Date.now() + 5 * 60_000).toISOString(),
    };
    bridge.getTrayState.mockResolvedValue(
      parseTrayState({
        ...readyTrayState,
        view: { ...readyTrayState.view, active_raw_grants: 1 },
      }),
    );
    bridge.listRawAccess
      .mockReset()
      .mockResolvedValueOnce({
        pending: [],
        active: [running],
        unlocked: false,
      })
      .mockResolvedValue({ pending: [], active: [], unlocked: false });
    bridge.revokeRawGrant
      .mockReset()
      .mockResolvedValue({ ...running, status: "revoked" });
    await act(async () => root.render(<TrayPopoverWindow />));
    await act(async () => {});

    const grants = container.querySelector('[data-slot="active-raw-grants"]');
    expect(grants?.textContent).toContain("可读取原文的 Agent");
    expect(grants?.textContent).toContain("Codex");
    expect(grants?.textContent).toContain("所有请求 · 剩余 ");

    await act(async () => grants!.querySelector("button")!.click());
    await act(async () => {});

    expect(bridge.revokeRawGrant).toHaveBeenCalledExactlyOnceWith(
      "rawgrant_2222222222222222",
    );
    expect(bridge.listRawAccess).toHaveBeenCalledTimes(2);
    expect(
      container.querySelector('[data-slot="active-raw-grants"]'),
    ).toBeNull();
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
    expect(
      document.querySelector(
        '[role="progressbar"][aria-label="Codex · 5 小时"]',
      ),
    ).not.toBeNull();
    expect(text).toContain("2 小时后");
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
    const labels = buttons();
    for (const label of [
      "请求",
      "提供商",
      "令牌",
      "安全",
      "路由",
      "Agent 工具",
    ]) {
      expect(labels).not.toContain(label);
    }
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
    click("设置");
    click("重启网关");
    click("打开 AstrLink");
    click("退出");
    expect(actions).toEqual([
      { kind: "copy_address" },
      { kind: "navigate", page: "settings" },
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
      kind: "minimax_coding",
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
    const toggle = [...document.querySelectorAll("button")].find(
      (button) => button.textContent?.trim() === "展开其余 2 条",
    );
    expect(toggle).toBeDefined();
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    act(() => toggle?.click());
    expect(rows()).toBe(12);
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
  });

  it("groups windows under one plan header and names the columns once", async () => {
    await render({
      ...readyTrayState,
      digest: {
        ...readyTrayState.digest,
        subscriptions: [
          readyTrayState.digest.subscriptions[0],
          {
            name: "MiniMax Coding Plan",
            kind: "minimax_coding",
            windows: [
              {
                label: null,
                limit_window_seconds: 18_000,
                secondary: false,
                used_percent: 0,
                reset_at: "2026-09-22T11:00:00Z",
              },
              {
                label: null,
                limit_window_seconds: 604_800,
                secondary: true,
                used_percent: 100,
                reset_at: "2026-09-26T10:00:00Z",
              },
            ],
          },
        ],
      },
    });
    const list = document.querySelector('[data-slot="tray-subscriptions"]')!;
    const text = list.textContent ?? "";
    // The plan name and its logo head the group once, not every row.
    expect(text.match(/MiniMax Coding Plan/g)).toHaveLength(1);
    expect(
      list.querySelector('[role="img"][aria-label="MiniMax Coding Plan"]'),
    ).not.toBeNull();
    // The mode and the reset verb live in the column headers.
    expect(text).toContain("剩余");
    expect(text).toContain("重置");
    expect(text).not.toContain("剩余 ");
    expect(text).not.toContain("后重置");
    const captions = (row: HTMLElement) => row.lastElementChild?.textContent;
    const rows = [...list.querySelectorAll<HTMLElement>("[title]")].filter(
      (row) => row.querySelector('[role="progressbar"]'),
    );
    expect(rows.map((row) => row.title)).toEqual([
      "Codex · 5 小时 · 剩余 38% · 2 小时后重置",
      "Codex · 7 天 · 剩余 82%",
      "MiniMax Coding Plan · 5 小时 · 剩余 100% · 59 分钟后重置",
      "MiniMax Coding Plan · 7 天 · 剩余 0% · 4 天后重置 · 额度已用尽",
    ]);
    // Every row fills the reset column; an unknown reset reads as a dash.
    expect(rows.map(captions)).toEqual([
      "2 小时后",
      "—",
      "59 分钟后",
      "4 天后",
    ]);

    // No reset anywhere: the column and its header disappear.
    await render({
      ...readyTrayState,
      digest: {
        ...readyTrayState.digest,
        subscriptions: [
          {
            ...readyTrayState.digest.subscriptions[0],
            windows: [readyTrayState.digest.subscriptions[0].windows[1]],
          },
        ],
      },
    });
    expect(
      document.querySelector('[data-slot="tray-subscriptions"]')?.textContent,
    ).not.toContain("重置");
  });

  it("labels provider-named limits and lists disabled plans too", async () => {
    await render({
      ...readyTrayState,
      digest: {
        ...readyTrayState.digest,
        subscriptions: [
          {
            name: "Kimi",
            kind: "kimi_coding",
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
    const meter = container.querySelector(
      '[role="progressbar"][aria-label="Kimi · Monthly"]',
    );
    expect(meter?.getAttribute("aria-valuetext")).toBe("剩余 59%");
    expect(container.textContent).toContain("Monthly");
    expect(container.textContent).toContain("59%");
  });

  it("flags an agent reading records through the CLI", async () => {
    await render({
      ...readyTrayState,
      view: { ...readyTrayState.view, observer_active: true },
    });
    const badge = document.querySelector('[data-slot="tray-observed"]');
    expect(badge?.textContent).toBe("Agent 正在通过 CLI 读取");
    expect(document.querySelector('[data-slot="tray-raw-pending"]')).toBeNull();
    // Cost is shown without an unpriced caveat.
    expect(container.textContent).toContain("$0.83");
    expect(container.textContent).not.toContain("待计价");
  });

  it("names approved raw reads and pending raw access requests", async () => {
    await render({
      ...readyTrayState,
      view: {
        ...readyTrayState.view,
        observer_active: true,
        observer_read_level: "raw",
        pending_raw_access: 2,
      },
    });
    expect(
      document.querySelector('[data-slot="tray-observed"]')?.textContent,
    ).toBe("Agent 正在读取已批准的原文");
    const pending = document.querySelector<HTMLButtonElement>(
      '[data-slot="tray-raw-pending"]',
    );
    expect(pending?.textContent).toBe("2 个原文申请待批准");
    // Requests are decided in the approval window, which this opens.
    await act(async () => pending!.click());
    expect(actions).toEqual([{ kind: "raw_access" }]);
  });

  it("points a missing raw password at the main window", async () => {
    await render(readyTrayState);
    expect(
      document.querySelector('[data-slot="tray-raw-password-required"]'),
    ).toBeNull();

    await render({
      ...readyTrayState,
      view: { ...readyTrayState.view, raw_password_required: true },
    });
    const hint = document.querySelector<HTMLButtonElement>(
      '[data-slot="tray-raw-password-required"]',
    );
    expect(hint?.textContent).toBe("请求原文还没有保护，点此设置");
    await act(async () => hint!.click());
    expect(actions).toEqual([{ kind: "open" }]);
  });

  it("points a raw key replaced outside the desktop at the main window", async () => {
    await render(readyTrayState);
    expect(
      document.querySelector('[data-slot="tray-raw-key-replaced"]'),
    ).toBeNull();

    await render({
      ...readyTrayState,
      view: { ...readyTrayState.view, raw_key_replaced: true },
    });
    const hint = document.querySelector<HTMLButtonElement>(
      '[data-slot="tray-raw-key-replaced"]',
    );
    expect(hint?.textContent).toBe(
      "原文密钥在 AstrLink 之外被更换，点此打开 AstrLink 查看",
    );
    await act(async () => hint!.click());
    expect(actions).toEqual([{ kind: "open" }]);
  });

  it("names the latest raw password or key change", async () => {
    await render({
      ...readyTrayState,
      view: {
        ...readyTrayState.view,
        raw_key_event: {
          kind: "raw_password_changed",
          at: "2026-09-22T09:57:00Z",
        },
      },
    });
    expect(
      document.querySelector('[data-slot="tray-raw-key-event"]')?.textContent,
    ).toBe("原文口令已更改 · 3 分钟前");

    await render({
      ...readyTrayState,
      view: {
        ...readyTrayState.view,
        raw_key_event: { kind: "raw_key_reset", at: "2026-09-22T10:00:00Z" },
      },
    });
    expect(
      document.querySelector('[data-slot="tray-raw-key-event"]')?.textContent,
    ).toBe("原文密钥已重置 · 刚刚");
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
    expect(labels).not.toContain("请求");
    expect(labels).toEqual(
      expect.arrayContaining(["设置", "退出", "打开 AstrLink"]),
    );
  });
});
