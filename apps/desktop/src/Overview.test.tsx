// @vitest-environment happy-dom

import {
  act,
  cloneElement,
  isValidElement,
  type ComponentProps,
  type ReactElement,
  type ReactNode,
} from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("recharts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("recharts")>();
  return {
    ...actual,
    // happy-dom never lays out, so give BarChart a fixed plot box.
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => (
      <div data-slot="chart-frame" style={{ width: 320, height: 160 }}>
        {isValidElement(children)
          ? cloneElement(
              children as ReactElement<{ width?: number; height?: number }>,
              { width: 320, height: 160 },
            )
          : children}
      </div>
    ),
  };
});

import type { AccessTokenCatalog } from "./AccessTokenManager";
import { browserSnapshot, type AppSnapshot } from "./core-model";
import { Overview, type ServiceCatalog } from "./Overview";
import type { Service } from "./service-model";
import {
  aggregateUsage,
  emptyUsageSummary,
  emptyUsageTotals,
  resolveUsageWindow,
  type UsageGroup,
  type UsageRangePreset,
  type UsageState,
  type UsageSummary,
} from "./usage-range";

const readySnapshot: AppSnapshot = {
  app_version: "0.1.0",
  phase: "ready",
  pid: 42,
  ready: {
    event: "ready",
    core_version: "0.1.0",
    control_api_version: "v1",
    protocol_contract_version: "v1",
    inference_url: "http://127.0.0.1:8317",
    control_url: "http://127.0.0.1:43117",
  },
  health: { status: "ok" },
  version: {
    core_version: "0.1.0",
    control_api_version: "v1",
    protocol_contract_version: "v1",
    build_commit: "unknown",
  },
  capabilities: {
    protocol_contract_version: "v1",
    protocols: [
      {
        id: "openai.responses",
        phase: "alpha",
        primary: true,
        streaming: true,
      },
    ],
    plan_types: [],
    conversion_engine: {
      name: "relaykit",
      version: null,
      available: false,
      edges: [],
    },
  },
  last_error: null,
  inference_port_fallback: null,
  recovery_attempt: 0,
  recovery_scheduled_in_ms: null,
};

const gateway: Service = {
  id: "service_gateway_01",
  name: "Primary gateway",
  kind: "newapi",
  enabled: true,
  models: ["gpt-5"],
  capabilities: [
    {
      protocol: "openai.responses",
      mode: "delegated",
      streaming: true,
    },
  ],
  http: {
    base_url: "https://gateway.example",
    auth: { scheme: "bearer" },
  },
  created_at: "2026-07-28T08:00:00Z",
  updated_at: "2026-07-28T08:00:00Z",
};

const readyCatalog: ServiceCatalog = {
  status: "ready",
  items: [gateway],
  error: null,
  stale: false,
};

const readyTokens: AccessTokenCatalog = {
  status: "ready",
  items: [
    {
      id: "token_01",
      name: "VS Code",
      hint: "astr_…K8Q2",
      created_at: "2026-07-24T10:30:00Z",
    },
  ],
  error: null,
  stale: false,
};

const now = new Date(2026, 8, 4, 21, 0, 0);
const sevenDayWindow = resolveUsageWindow("7d", now);
const oneDayWindow = resolveUsageWindow("1d", now);

function group(id: string | null, overrides: Partial<UsageGroup>): UsageGroup {
  return { ...emptyUsageTotals(), id, ...overrides };
}

function readySummary(overrides: Partial<UsageSummary> = {}): UsageSummary {
  return { ...emptyUsageSummary(sevenDayWindow), ...overrides };
}

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

describe("Overview", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
  });

  async function renderOverview(
    overrides: Partial<ComponentProps<typeof Overview>> = {},
  ): Promise<{
    onOpenService: ReturnType<typeof vi.fn>;
    onUsagePresetChange: ReturnType<typeof vi.fn>;
    onAddService: ReturnType<typeof vi.fn>;
    onManageServices: ReturnType<typeof vi.fn>;
    onManageTokens: ReturnType<typeof vi.fn>;
    onRestart: ReturnType<typeof vi.fn>;
  }> {
    const onOpenService = overrides.onOpenService
      ? vi.fn(overrides.onOpenService)
      : vi.fn();
    const onUsagePresetChange = overrides.onUsagePresetChange
      ? vi.fn(overrides.onUsagePresetChange)
      : vi.fn();
    const onAddService = vi.fn();
    const onManageServices = vi.fn();
    const onManageTokens = vi.fn();
    const onRestart = vi.fn();
    await act(async () => {
      root.render(
        <Overview
          catalog={overrides.catalog ?? readyCatalog}
          copyError={null}
          copyFeedback={null}
          isNativeApp={overrides.isNativeApp ?? true}
          isReady={overrides.isReady ?? true}
          isRestarting={overrides.isRestarting ?? false}
          onAddService={onAddService}
          onCopy={() => undefined}
          onManageServices={onManageServices}
          onManageTokens={onManageTokens}
          onOpenService={onOpenService}
          onRefreshServices={() => undefined}
          onRefreshUsage={() => undefined}
          onRestart={onRestart}
          onUsagePresetChange={onUsagePresetChange}
          snapshot={
            overrides.snapshot === undefined
              ? readySnapshot
              : overrides.snapshot
          }
          tokenCatalog={overrides.tokenCatalog ?? readyTokens}
          usage={
            overrides.usage ?? {
              status: "ready",
              summary: emptyUsageSummary(resolveUsageWindow("1y", now)),
              error: null,
            }
          }
          usagePreset={overrides.usagePreset ?? "1y"}
        />,
      );
    });
    return {
      onOpenService,
      onUsagePresetChange,
      onAddService,
      onManageServices,
      onManageTokens,
      onRestart,
    };
  }

  const emptyCatalog: ServiceCatalog = {
    status: "ready",
    items: [],
    error: null,
    stale: false,
  };
  const emptyTokens: AccessTokenCatalog = {
    status: "ready",
    items: [],
    error: null,
    stale: false,
  };

  it("offers working setup actions for a confirmed empty workspace", async () => {
    const { onAddService, onManageTokens } = await renderOverview({
      catalog: emptyCatalog,
      tokenCatalog: emptyTokens,
    });
    expect(
      container.querySelector("[data-slot='overview-welcome']"),
    ).toBeTruthy();
    expect(container.querySelector("#usage-heading")).toBeNull();
    expect(container.textContent).toContain("工作区已就绪");
    await act(async () => {
      button("添加服务").click();
      button("创建访问令牌").click();
    });
    expect(onAddService).toHaveBeenCalledOnce();
    expect(onManageTokens).toHaveBeenCalledOnce();
  });

  it("keeps browser preview distinct from gateway startup and offers navigation", async () => {
    const { onManageServices } = await renderOverview({
      catalog: { ...emptyCatalog, status: "blocked" },
      tokenCatalog: { ...emptyTokens, status: "blocked" },
      snapshot: browserSnapshot(),
      isReady: false,
      isNativeApp: false,
      usage: { status: "blocked", summary: null, error: null },
    });
    expect(container.textContent).toContain("当前为浏览器预览");
    expect(container.textContent).not.toContain("尚未配置");
    expect(container.textContent).not.toContain("创建访问令牌");
    expect(container.textContent).not.toContain("重启网关");
    await act(async () => button("查看 API 服务").click());
    expect(onManageServices).toHaveBeenCalledOnce();
  });

  it("waits for catalogs before claiming a workspace is empty", async () => {
    await renderOverview({
      catalog: { ...emptyCatalog, status: "loading" },
      tokenCatalog: { ...emptyTokens, status: "loading" },
    });
    expect(container.textContent).toContain("正在读取工作区");
    expect(container.textContent).not.toContain("工作区已就绪");
    expect(container.textContent).not.toContain("添加服务");
    expect(container.querySelector("[data-slot='loading-state']")).toBeTruthy();
  });

  it("shows gateway failure and preserves the restart action", async () => {
    const { onRestart } = await renderOverview({
      catalog: { ...emptyCatalog, status: "blocked" },
      tokenCatalog: { ...emptyTokens, status: "blocked" },
      snapshot: {
        ...readySnapshot,
        phase: "error",
        ready: null,
        last_error: "连接失败",
      },
      isReady: false,
    });
    expect(container.querySelector("[role='alert']")?.textContent).toBe(
      "连接失败",
    );
    expect(container.textContent).not.toContain("创建访问令牌");
    await act(async () => button("重启网关").click());
    expect(onRestart).toHaveBeenCalledOnce();
    await renderOverview({
      catalog: { ...emptyCatalog, status: "blocked" },
      tokenCatalog: { ...emptyTokens, status: "blocked" },
      snapshot: { ...readySnapshot, phase: "stopping", ready: null },
      isReady: false,
      isRestarting: true,
    });
    expect(button("重启中…").disabled).toBe(true);
  });

  it("keeps historical usage visible after services and tokens are removed", async () => {
    await renderOverview({
      catalog: emptyCatalog,
      tokenCatalog: emptyTokens,
      usage: {
        status: "ready",
        summary: readySummary({
          totals: { ...emptyUsageTotals(), requests: 12 },
        }),
        error: null,
      },
    });
    expect(
      container.querySelector("[data-slot='overview-welcome']"),
    ).toBeNull();
    expect(container.querySelector("#usage-heading")).toBeTruthy();
    expect(
      container.querySelector("[data-slot='metric-group']")?.textContent,
    ).toContain("12");
  });

  it("keeps catalog and usage errors out of the welcome state", async () => {
    await renderOverview({
      catalog: { ...emptyCatalog, status: "error", error: "目录读取失败" },
      tokenCatalog: emptyTokens,
      usage: { status: "error", summary: null, error: "用量读取失败" },
    });
    expect(
      container.querySelector("[data-slot='overview-welcome']"),
    ).toBeNull();
    expect(container.textContent).toContain("目录读取失败");
    expect(container.textContent).toContain("用量读取失败");
    expect(button("重试").disabled).toBe(false);
  });

  it("shows range usage with compact metrics and billing", async () => {
    await renderOverview({
      usage: {
        status: "ready",
        summary: readySummary({
          totals: {
            ...emptyUsageTotals(),
            requests: 4,
            failed_requests: 2,
            input_tokens: 100,
            output_tokens: 20,
            total_tokens: 120,
            cache_read_tokens: 40,
          },
          by_service: [
            group("service_gateway_01", {
              requests: 4,
              input_tokens: 100,
              output_tokens: 20,
              total_tokens: 120,
            }),
          ],
          by_model: [
            group("gpt-4o", {
              requests: 3,
              input_tokens: 80,
              output_tokens: 16,
              total_tokens: 96,
            }),
            group(null, {
              requests: 1,
              input_tokens: 20,
              output_tokens: 4,
              total_tokens: 24,
            }),
          ],
        }),
        error: null,
      },
    });

    expect(container.textContent).toContain("用量");
    expect(container.textContent).toContain("请求数");
    expect(container.textContent).toContain("总 Token");
    expect(container.textContent).toContain("输入 / 输出");
    expect(container.textContent).toContain("缓存命中");
    expect(container.textContent).toContain("100 / 20");
    expect(container.textContent).toContain("40%");
    expect(
      container.querySelector("[data-testid='billing-overview']"),
    ).toBeTruthy();
    expect(container.textContent).toContain("按服务");
    expect(container.textContent).toContain("按模型");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("gpt-4o");
    expect(container.textContent).toContain("未知模型");
    expect(container.textContent).not.toContain("上游服务");
    expect(container.textContent).not.toContain("连接 AstrLink");
  });

  it("defaults to a yearly heatmap and reports range switches", async () => {
    const { onUsagePresetChange } = await renderOverview();
    expect(button("热力图").getAttribute("data-state")).toBe("on");
    expect(
      container.querySelectorAll("[data-slot='activity-cell']"),
    ).toHaveLength(365);

    const trigger = container.querySelector<HTMLButtonElement>(
      '[role="combobox"][aria-label="用量区间"]',
    )!;
    expect(trigger.textContent).toBe("近一年");
    await act(async () => {
      trigger.dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      );
    });
    const options = [
      ...document.querySelectorAll<HTMLElement>('[role="option"]'),
    ];
    expect(options.map((option) => option.textContent)).toEqual([
      "近 30 天",
      "近 90 天",
      "近一年",
    ]);
    await act(async () => {
      options.find((option) => option.textContent === "近 30 天")!.click();
    });
    expect(onUsagePresetChange).toHaveBeenCalledWith("30d");
  });

  it("switches between heatmap and chart without changing the selected range", async () => {
    const summary = emptyUsageSummary(resolveUsageWindow("30d", now));
    summary.by_day[0] = {
      ...summary.by_day[0],
      requests: 2,
      total_tokens: 100,
    };
    summary.by_day[1] = { ...summary.by_day[1], failed_requests: 4 };
    const { onUsagePresetChange } = await renderOverview({
      usagePreset: "30d",
      usage: { status: "ready", summary, error: null },
    });
    await act(async () => button("热力图").click());
    const cells = [
      ...container.querySelectorAll<HTMLButtonElement>(
        "[data-slot='activity-cell']",
      ),
    ];
    expect(cells).toHaveLength(30);
    expect(cells[0].getAttribute("data-level")).toBe("4");
    expect(cells[1].getAttribute("data-level")).toBe("0");
    expect(cells[0].getAttribute("aria-label")).toContain("100 Token");
    await act(async () => button("请求数").click());
    expect(cells[1].getAttribute("data-level")).toBe("4");
    expect(container.textContent).toContain("2 / 30 天有活动");
    const footer = container.querySelector("[data-slot='activity-detail']")!;
    const caption = footer.textContent;
    await act(async () => cells[0].focus());
    await act(async () => {
      cells[0].dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      );
    });
    expect(document.activeElement).toBe(cells[1]);
    expect(document.querySelector('[role="tooltip"]')?.textContent).toContain(
      "4 次失败",
    );
    expect(footer.textContent).toBe(caption);
    await act(async () => button("图表").click());
    expect(
      container.querySelector("[data-slot='activity-heatmap']"),
    ).toBeNull();
    expect(
      container.querySelectorAll(
        "[data-testid='usage-day'][data-series='input']",
      ),
    ).toHaveLength(30);
    expect(onUsagePresetChange).not.toHaveBeenCalled();
  });

  it("aligns a full year by weekday and supports keyboard navigation across weeks", async () => {
    const summary = emptyUsageSummary(resolveUsageWindow("1y", now));
    await renderOverview({
      usagePreset: "1y",
      usage: { status: "ready", summary, error: null },
    });
    await act(async () => button("热力图").click());
    const cells = [
      ...container.querySelectorAll<HTMLButtonElement>(
        "[data-slot='activity-cell']",
      ),
    ];
    expect(cells).toHaveLength(365);
    expect(cells[0].style.gridRow).toBe("6"); // Friday, 2025-09-05; weeks start Monday.
    expect(cells[0].style.gridColumn).toBe("2");
    expect(cells[3].style.gridRow).toBe("2");
    expect(cells[3].style.gridColumn).toBe("3");
    await act(async () => {
      cells[0].focus();
      cells[0].dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
      );
    });
    expect(document.activeElement).toBe(cells[7]);
    expect(cells.filter((cell) => cell.tabIndex === 0)).toHaveLength(1);
  });

  it("wraps a narrow year into complete weeks without losing dates or keyboard navigation", async () => {
    const summary = emptyUsageSummary(resolveUsageWindow("1y", now));
    const measure = vi
      .spyOn(HTMLElement.prototype, "getBoundingClientRect")
      .mockReturnValue(new DOMRect(0, 0, 320, 160));
    try {
      await renderOverview({
        usagePreset: "1y",
        usage: { status: "ready", summary, error: null },
      });
      await act(async () => button("热力图").click());
    } finally {
      measure.mockRestore();
    }
    const bands = [
      ...container.querySelectorAll("[data-slot='activity-calendar']"),
    ];
    expect(bands).toHaveLength(3);
    const cells = [
      ...container.querySelectorAll<HTMLButtonElement>(
        "[data-slot='activity-cell']",
      ),
    ];
    expect(cells.map((cell) => cell.dataset.date)).toEqual(
      summary.by_day.map((day) => day.date),
    );
    const secondBandStart = bands[1].querySelector<HTMLButtonElement>(
      "[data-slot='activity-cell']",
    )!;
    expect(secondBandStart.style.gridRow).toBe("2");
    const previousWeek = cells[cells.indexOf(secondBandStart) - 7];
    await act(async () => {
      previousWeek.focus();
      previousWeek.dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
      );
    });
    expect(document.activeElement).toBe(secondBandStart);
    expect(cells.filter((cell) => cell.tabIndex === 0)).toHaveLength(1);
  });

  it("switches hourly heatmaps to a yearly calendar and hides cells after failure", async () => {
    const { onUsagePresetChange } = await renderOverview({
      usagePreset: "1d",
      usage: {
        status: "ready",
        summary: emptyUsageSummary(oneDayWindow),
        error: null,
      },
    });
    // A retained short chart range remains usable when returning to Overview.
    expect(button("图表").getAttribute("data-state")).toBe("on");
    expect(container.querySelector('[aria-label="用量区间"]')?.textContent).toBe("近 24 小时");
    await act(async () => button("热力图").click());
    expect(onUsagePresetChange).toHaveBeenCalledWith("1y");
    await renderOverview({
      usagePreset: "1y",
      usage: {
        status: "ready",
        summary: emptyUsageSummary(resolveUsageWindow("1y", now)),
        error: null,
      },
    });
    expect(
      container.querySelectorAll("[data-slot='activity-cell']"),
    ).toHaveLength(365);
    expect(container.textContent).toContain("所选区间暂无请求");
    await renderOverview({
      usage: { status: "error", summary: readySummary(), error: "加载失败" },
    });
    expect(
      container.querySelectorAll("[data-slot='activity-cell']"),
    ).toHaveLength(0);
    expect(container.textContent).toContain("暂时无法加载用量趋势");
  });

  it("charts one bar group per day in the window", async () => {
    await renderOverview({
      usage: {
        status: "ready",
        summary: aggregateUsage([], sevenDayWindow, false),
        error: null,
      },
    });
    await act(async () => button("图表").click());

    const days = [
      ...container.querySelectorAll(
        "[data-testid='usage-day'][data-series='input']",
      ),
    ];
    expect(days).toHaveLength(7);
    expect(days.map((day) => day.getAttribute("data-date"))).toEqual([
      "2026-08-29",
      "2026-08-30",
      "2026-08-31",
      "2026-09-01",
      "2026-09-02",
      "2026-09-03",
      "2026-09-04",
    ]);
    // No traffic yet, so every bar sits on the baseline.
    expect(days.every((day) => Number(day.getAttribute("height")) === 0)).toBe(
      true,
    );
    expect(container.textContent).toContain("按日 Token");
    expect(container.textContent).toContain("输入");
    expect(container.textContent).toContain("输出");
    expect(container.textContent).toContain("缓存读取");
    expect(container.textContent).toContain("缓存写入");
    expect(container.textContent).toContain("所选区间暂无请求");
  });

  it("charts one stacked bar per hour when the range is today", async () => {
    await renderOverview({
      usagePreset: "1d",
      usage: {
        status: "ready",
        summary: emptyUsageSummary(oneDayWindow),
        error: null,
      },
    });
    await act(async () => button("图表").click());

    const hours = [
      ...container.querySelectorAll(
        "[data-testid='usage-day'][data-series='input']",
      ),
    ];
    expect(hours).toHaveLength(24);
    expect(hours.map((hour) => hour.getAttribute("data-hour"))).toEqual([
      "22",
      "23",
      ...[...Array(22).keys()].map(String),
    ]);
    expect(hours[0]?.getAttribute("data-date")).toBe("2026-09-03");
    expect(hours.at(-1)?.getAttribute("data-date")).toBe("2026-09-04");
    expect(hours.at(-1)?.getAttribute("data-hour")).toBe("21");
    expect(
      hours.every((hour) => Number(hour.getAttribute("height")) === 0),
    ).toBe(true);
    expect(container.textContent).toContain("按小时 Token");
    expect(container.textContent).not.toContain("按日 Token");
    await act(async () =>
      (
        container.querySelector(
          'button[aria-label="统计说明"]',
        ) as HTMLButtonElement
      ).click(),
    );
    expect(document.body.textContent).toContain(
      "统计过去 24 小时内已记录的请求与 Token",
    );
    expect(container.textContent).not.toContain("按本机日历日统计");
  });

  it("shows an hour detail card on hover", async () => {
    const hours = emptyUsageSummary(oneDayWindow).by_hour.map((bucket) =>
      bucket.hour === 9
        ? {
            ...bucket,
            requests: 21,
            failed_requests: 2,
            input_tokens: 2_117_100,
            output_tokens: 15_700,
            cache_read_tokens: 1_905_390,
            cache_write_tokens: 8_000,
            total_tokens: 2_132_800,
          }
        : bucket,
    );
    await renderOverview({
      usagePreset: "1d",
      usage: {
        status: "ready",
        summary: {
          ...emptyUsageSummary(oneDayWindow),
          totals: {
            ...emptyUsageTotals(),
            requests: 21,
            failed_requests: 2,
            input_tokens: 2_117_100,
            output_tokens: 15_700,
            cache_read_tokens: 1_905_390,
            cache_write_tokens: 8_000,
            total_tokens: 2_132_800,
          },
          by_hour: hours,
        },
        error: null,
      },
    });

    await act(async () => button("图表").click());
    const hour = container.querySelector(
      "[data-testid='usage-day'][data-series='input'][data-date='2026-09-04'][data-hour='9']",
    );
    if (!(hour instanceof SVGElement)) {
      throw new Error("Missing busy hour bar");
    }
    expect(Number(hour.getAttribute("height"))).toBeGreaterThan(0);

    const hoverX =
      Number(hour.getAttribute("x")) + Number(hour.getAttribute("width")) / 2;
    const hoverY =
      Number(hour.getAttribute("y")) +
      Math.max(Number(hour.getAttribute("height")) / 2, 4);
    const surface = container.querySelector(".recharts-surface");
    if (!surface) throw new Error("Missing chart surface");

    await act(async () => {
      surface.dispatchEvent(
        new MouseEvent("mousemove", {
          bubbles: true,
          clientX: hoverX,
          clientY: hoverY,
        }),
      );
      await Promise.resolve();
    });

    const card = document.querySelector("[data-slot='usage-day-tooltip']");
    expect(card?.textContent).toContain("2026年9月4日 09:00");
    expect(card?.textContent).toContain("213.28万 Token（2,132,800）");
    expect(card?.textContent).toContain("21 次请求");
    expect(card?.textContent).toContain("2 次失败");
  });

  it("shows a day detail card on hover", async () => {
    const days = emptyUsageSummary(sevenDayWindow).by_day.map((bucket) =>
      bucket.date === "2026-09-04"
        ? {
            ...bucket,
            requests: 362,
            failed_requests: 3,
            input_tokens: 46_350_581,
            output_tokens: 296_114,
            cache_read_tokens: 18_540_232,
            cache_write_tokens: 800_000,
            total_tokens: 46_646_695,
          }
        : bucket,
    );
    await renderOverview({
      usage: {
        status: "ready",
        summary: readySummary({
          totals: {
            ...emptyUsageTotals(),
            requests: 362,
            failed_requests: 3,
            input_tokens: 46_350_581,
            output_tokens: 296_114,
            total_tokens: 46_646_695,
          },
          by_day: days,
        }),
        error: null,
      },
    });

    await act(async () => button("图表").click());
    const segments = [
      ...container.querySelectorAll(
        "[data-testid='usage-day'][data-date='2026-09-04']",
      ),
    ];
    expect(
      segments.map((segment) => segment.getAttribute("data-series")),
    ).toEqual(["input", "output", "cache_write", "cache_read"]);
    expect(segments.map((segment) => segment.getAttribute("fill"))).toEqual([
      "var(--primary)",
      "var(--violet)",
      "var(--warning)",
      "var(--success)",
    ]);
    const day = segments.find(
      (segment) => segment.getAttribute("data-series") === "input",
    );
    if (!(day instanceof SVGElement)) {
      throw new Error("Missing busy day bar");
    }
    expect(Number(day.getAttribute("height"))).toBeGreaterThan(0);

    // Recharts maps client coordinates onto the plot; happy-dom reports the
    // surface at (0, 0), so the bar's SVG x/y is the hover point.
    const hoverX =
      Number(day.getAttribute("x")) + Number(day.getAttribute("width")) / 2;
    const hoverY =
      Number(day.getAttribute("y")) +
      Math.max(Number(day.getAttribute("height")) / 2, 4);
    const surface = container.querySelector(".recharts-surface");
    if (!surface) throw new Error("Missing chart surface");

    await act(async () => {
      surface.dispatchEvent(
        new MouseEvent("mousemove", {
          bubbles: true,
          clientX: hoverX,
          clientY: hoverY,
        }),
      );
      await Promise.resolve();
    });

    const card = document.querySelector("[data-slot='usage-day-tooltip']");
    expect(card?.textContent).toContain("2026年9月4日");
    expect(card?.closest("[data-slot='panel']")).toBeNull();
    expect(document.body.contains(card)).toBe(true);
    expect(card).toBeInstanceOf(HTMLElement);
    if (card instanceof HTMLElement) {
      expect(card.style.position).toBe("fixed");
      expect(card.style.transform).toBe("");
      expect(card.className).toContain("w-max");
    }
    expect(card?.textContent).toContain("4664.67万 Token（46,646,695）");
    expect(card?.textContent).toContain("362 次请求");
    expect(card?.textContent).toContain("3 次失败");
    expect(card?.textContent).toContain("输入: 4635.06万");
    expect(card?.textContent).toContain("输出: 29.61万");
    expect(card?.textContent).toContain("缓存读取: 1854.02万");
    expect(card?.textContent).toContain("缓存写入: 80万");
    expect(card?.textContent).toContain("缓存命中率: 40%");
  });

  it("localizes large numbers and keeps the exact value in the title", async () => {
    await renderOverview({
      usage: {
        status: "ready",
        summary: readySummary({
          totals: {
            ...emptyUsageTotals(),
            requests: 362,
            input_tokens: 46_350_581,
            output_tokens: 296_114,
            total_tokens: 46_646_695,
          },
          by_day: [
            {
              date: "2026-09-04",
              ...emptyUsageTotals(),
              requests: 362,
              total_tokens: 46_646_695,
            },
          ],
          by_model: [
            group("claude-fable-5", {
              requests: 105,
              total_tokens: 22_714_604,
            }),
          ],
        }),
        error: null,
      },
    });

    expect(container.textContent).toContain("4664.67万");
    expect(container.textContent).toContain("4635.06万 / 29.61万");
    expect(container.textContent).toContain("2271.46万");
    expect(container.textContent).toContain("105 次");

    // Grouped digits belong in the hover title, never in the rendered value.
    const values = [...container.querySelectorAll("strong, span.tabular-nums")];
    expect(values.map((node) => node.textContent)).not.toContain("46,646,695");
    const totalTokens = values.find((node) => node.textContent === "4664.67万");
    expect(totalTokens?.getAttribute("title")).toBe("46,646,695");
  });

  it("covers retained usage with a loading overlay while refreshing", async () => {
    await renderOverview({
      usage: {
        status: "loading",
        summary: readySummary({
          totals: {
            ...emptyUsageTotals(),
            requests: 1380,
            total_tokens: 85_178_700,
          },
        }),
        error: null,
      },
    });

    const overlay = container.querySelector("[data-slot='usage-loading']");
    expect(overlay?.textContent).toContain("统计中");
    expect(container.querySelector("[data-slot='loading-state']")).toBeTruthy();
    expect(container.textContent).toContain("1,380");
    expect(container.textContent).toContain("8517.87万");
    expect(
      [...container.querySelectorAll("[data-slot='badge']")].some((node) =>
        node.textContent?.includes("统计中"),
      ),
    ).toBe(false);
  });

  it("shows a usage skeleton on the first load", async () => {
    await renderOverview({
      usage: { status: "loading", summary: null, error: null },
    });

    expect(
      container.querySelector("[data-slot='usage-skeleton']"),
    ).toBeTruthy();
    expect(container.querySelector("[data-testid='usage-day']")).toBeNull();
    expect(container.querySelector("[data-slot='usage-loading']")).toBeNull();
  });

  it("replaces retained metrics and the chart with unavailable states after a refresh fails", async () => {
    await renderOverview({
      usage: {
        status: "error",
        summary: readySummary({
          totals: {
            ...emptyUsageTotals(),
            requests: 747,
            total_tokens: 44_062_250,
          },
          by_model: [
            group("gpt-5", { requests: 747, total_tokens: 44_062_250 }),
          ],
        }),
        error: "统计服务暂时不可用",
      },
    });

    expect(container.querySelector("[role='alert']")?.textContent).toBe(
      "统计服务暂时不可用",
    );
    expect(container.textContent).toContain("暂时无法加载用量趋势");
    expect(
      container.querySelector("[data-slot='metric-group']")?.textContent,
    ).not.toContain("747");
    expect(container.querySelector("[data-testid='usage-day']")).toBeNull();
    expect(container.textContent).not.toContain("所选区间暂无请求");
    expect(container.textContent).not.toContain("1 个模型");
  });

  it("reports how many records a capped range scanned", async () => {
    await renderOverview({
      usage: {
        status: "ready",
        summary: readySummary({ capped: true, scanned_records: 4000 }),
        error: null,
      },
    });

    expect(container.textContent).toContain("仅统计最近 4,000 条");
  });

  it("shows an empty model state when the range has no successful requests", async () => {
    await renderOverview();

    expect(container.textContent).toContain("所选区间没有成功请求");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("0 次");
  });

  it("opens a catalog service from the usage breakdown", async () => {
    const { onOpenService } = await renderOverview();
    const serviceRow = [...document.querySelectorAll("button")].find(
      (candidate) => candidate.textContent?.includes("Primary gateway"),
    );
    if (!(serviceRow instanceof HTMLButtonElement)) {
      throw new Error("Missing service usage row");
    }

    await act(async () => {
      serviceRow.click();
    });

    expect(onOpenService).toHaveBeenCalledWith("service_gateway_01");
  });

  it("does not surface a failed-request chip on the usage header", async () => {
    await renderOverview({
      usage: {
        status: "ready",
        summary: readySummary({
          totals: { ...emptyUsageTotals(), failed_requests: 3 },
        }),
        error: null,
      },
    });

    expect(
      [...container.querySelectorAll("button")].some(
        (candidate) => candidate.textContent?.trim() === "3 次失败",
      ),
    ).toBe(false);
  });
});
