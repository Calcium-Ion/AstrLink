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

import { applyLocale } from "./i18n";
import type { AccessTokenCatalog } from "./AccessTokenManager";
import { browserSnapshot, type AppSnapshot } from "./core-model";
import { Overview, type ServiceCatalog } from "./Overview";
import {
  DEFAULT_OVERVIEW_LAYOUT,
  OVERVIEW_LAYOUT_STORAGE_KEY,
} from "./overview-layout";
import type { Service } from "./service-model";
import {
  aggregateUsage,
  emptyUsageSummary,
  emptyUsageTotals,
  resolveUsageWindow,
  type UsageGroup,
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

const orderedModules = DEFAULT_OVERVIEW_LAYOUT.filter((id) => id !== "system");

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
    localStorage.removeItem(OVERVIEW_LAYOUT_STORAGE_KEY);
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
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    localStorage.removeItem(OVERVIEW_LAYOUT_STORAGE_KEY);
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
          onOpenTokenRecords={() => undefined}
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

  const moduleOrder = () =>
    [...container.querySelectorAll<HTMLElement>("[data-ordered-item]")].map(
      (item) => item.dataset.orderedItem,
    );
  const layoutButton = (label: string) =>
    container.querySelector<HTMLButtonElement>(
      `button[aria-label="${label}"]`,
    )!;

  it("keeps the default layout free of reorder controls and moves providers and models together", async () => {
    await renderOverview();
    expect(moduleOrder()).toEqual(orderedModules);
    expect(layoutButton("API 提供商与模型")).toBeNull();
    const providerPanel = container.querySelector(
      "[aria-labelledby='usage-by-service-heading']",
    );
    const modelPanel = container.querySelector(
      "[aria-labelledby='usage-by-model-heading']",
    );
    expect(providerPanel?.closest("[data-ordered-item]")).toBe(
      modelPanel?.closest("[data-ordered-item]"),
    );
    await act(async () => layoutButton("自定义布局").click());
    await act(async () =>
      layoutButton("API 提供商与模型").dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      ),
    );
    expect(moduleOrder()).toEqual([
      "usage",
      "tokens",
      "providers-models",
      "access",
    ]);
    expect(
      container.querySelector("[aria-labelledby='usage-by-service-heading']"),
    ).toBe(providerPanel);
    expect(
      container.querySelector("[aria-labelledby='usage-by-model-heading']"),
    ).toBe(modelPanel);
    expect(document.activeElement).not.toBe(document.body);
    expect(
      JSON.parse(localStorage.getItem(OVERVIEW_LAYOUT_STORAGE_KEY)!),
    ).toEqual({ order: [...moduleOrder(), "system"], hidden: [] });
    await act(async () => layoutButton("完成布局").click());
    expect(layoutButton("API 提供商与模型")).toBeNull();
    await act(async () => root.unmount());
    root = createRoot(container);
    await renderOverview();
    expect(moduleOrder()[1]).toBe("tokens");
    await act(async () => layoutButton("自定义布局").click());
    await act(async () => layoutButton("恢复默认布局").click());
    expect(moduleOrder()).toEqual(orderedModules);
    expect(
      JSON.parse(localStorage.getItem(OVERVIEW_LAYOUT_STORAGE_KEY)!),
    ).toEqual({ order: DEFAULT_OVERVIEW_LAYOUT, hidden: [] });
  });

  it("persists module visibility and preserves hidden modules in the editable order", async () => {
    await renderOverview();
    expect(container.querySelector('[role="switch"]')).toBeNull();
    await act(async () => layoutButton("自定义布局").click());
    await act(async () => layoutButton("显示API 提供商与模型").click());
    expect(
      layoutButton("显示API 提供商与模型").getAttribute("aria-checked"),
    ).toBe("false");
    expect(
      container.querySelector("[aria-labelledby='usage-by-service-heading']"),
    ).toBeNull();
    expect(
      container.querySelector("[aria-labelledby='usage-by-model-heading']"),
    ).toBeNull();
    expect(moduleOrder()).toEqual(orderedModules);
    await act(async () =>
      layoutButton("API 提供商与模型").dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      ),
    );
    expect(
      JSON.parse(localStorage.getItem(OVERVIEW_LAYOUT_STORAGE_KEY)!),
    ).toEqual({
      order: ["usage", "tokens", "providers-models", "access", "system"],
      hidden: ["providers-models"],
    });
    await act(async () => layoutButton("完成布局").click());
    expect(moduleOrder()).toEqual(["usage", "tokens", "access"]);
    await act(async () => root.unmount());
    root = createRoot(container);
    await renderOverview();
    expect(moduleOrder()).toEqual(["usage", "tokens", "access"]);
    await act(async () => layoutButton("自定义布局").click());
    expect(moduleOrder()[2]).toBe("providers-models");
    await act(async () => layoutButton("显示API 提供商与模型").click());
    expect(
      container.querySelector("[aria-labelledby='usage-by-service-heading']"),
    ).not.toBeNull();
    expect(
      container.querySelector("[aria-labelledby='usage-by-model-heading']"),
    ).not.toBeNull();
    expect(moduleOrder()[2]).toBe("providers-models");
  });

  it("keeps customization reachable when every module is hidden and resets visibility with order", async () => {
    await renderOverview();
    await act(async () => layoutButton("自定义布局").click());
    for (const toggle of container.querySelectorAll<HTMLButtonElement>(
      '[role="switch"]',
    )) {
      await act(async () => toggle.click());
    }
    expect(moduleOrder()).toEqual(orderedModules);
    expect(
      [...container.querySelectorAll('[role="switch"]')].every(
        (toggle) => toggle.getAttribute("aria-checked") === "false",
      ),
    ).toBe(true);
    await act(async () => layoutButton("完成布局").click());
    expect(moduleOrder()).toEqual([]);
    expect(container.textContent).toContain("所有模块已隐藏");
    await act(async () => button("自定义布局").click());
    expect(moduleOrder()).toEqual(orderedModules);
    expect(layoutButton("恢复默认布局").disabled).toBe(false);
    await act(async () => layoutButton("恢复默认布局").click());
    expect(
      [...container.querySelectorAll('[role="switch"]')].every(
        (toggle) => toggle.getAttribute("aria-checked") === "true",
      ),
    ).toBe(true);
    expect(
      JSON.parse(localStorage.getItem(OVERVIEW_LAYOUT_STORAGE_KEY)!),
    ).toEqual({ order: DEFAULT_OVERVIEW_LAYOUT, hidden: [] });
    await act(async () => layoutButton("完成布局").click());
    expect(moduleOrder()).toEqual(orderedModules);
  });

  it("loads legacy orders with every module visible", async () => {
    const order = [...DEFAULT_OVERVIEW_LAYOUT].reverse();
    localStorage.setItem(OVERVIEW_LAYOUT_STORAGE_KEY, JSON.stringify(order));
    await renderOverview();
    expect(moduleOrder()).toEqual(order.filter((id) => id !== "system"));
    await act(async () => layoutButton("自定义布局").click());
    expect(
      [...container.querySelectorAll('[role="switch"]')].every(
        (toggle) => toggle.getAttribute("aria-checked") === "true",
      ),
    ).toBe(true);
  });

  it("ignores unknown hidden modules and shows newly added modules", async () => {
    localStorage.setItem(
      OVERVIEW_LAYOUT_STORAGE_KEY,
      JSON.stringify({
        order: ["tokens", "tokens", "obsolete"],
        hidden: ["tokens", "tokens", "obsolete", null],
      }),
    );
    await renderOverview();
    expect(moduleOrder()).toEqual(["usage", "providers-models", "access"]);
    await act(async () => layoutButton("自定义布局").click());
    expect(moduleOrder()).toEqual([
      "tokens",
      "usage",
      "providers-models",
      "access",
    ]);
    expect(
      container.querySelectorAll('[role="switch"][aria-checked="false"]'),
    ).toHaveLength(1);
  });

  it("collapses system details by default and expands them from the heading", async () => {
    await renderOverview();
    const details = container
      .querySelector("#system-details-heading")
      ?.closest("details");
    expect(details?.open).toBe(false);
    await act(async () => details?.querySelector("summary")?.click());
    expect(details?.open).toBe(true);
    expect(details?.textContent).toContain("协议能力");
  });

  it("lets an empty workspace restore its hidden system module", async () => {
    localStorage.setItem(
      OVERVIEW_LAYOUT_STORAGE_KEY,
      JSON.stringify({ order: DEFAULT_OVERVIEW_LAYOUT, hidden: ["system"] }),
    );
    await renderOverview({ catalog: emptyCatalog, tokenCatalog: emptyTokens });
    expect(container.querySelector("#system-details-heading")).toBeNull();
    await act(async () => layoutButton("自定义布局").click());
    await act(async () => layoutButton("显示系统详情").click());
    await act(async () => layoutButton("完成布局").click());
    expect(container.querySelector("#system-details-heading")).not.toBeNull();
    expect(container.querySelector("#usage-heading")).not.toBeNull();
  });

  it.each(["not json", "{}", '["tokens","tokens","obsolete"]'])(
    "recovers missing or invalid saved modules: %s",
    async (saved) => {
      localStorage.setItem(OVERVIEW_LAYOUT_STORAGE_KEY, saved);
      await renderOverview();
      expect(new Set(moduleOrder())).toEqual(new Set(orderedModules));
      expect(moduleOrder()).toHaveLength(orderedModules.length);
      expect(moduleOrder()[0]).toBe(
        saved.includes("tokens") ? "tokens" : "usage",
      );
    },
  );

  it("keeps reordering usable and reports unavailable storage", async () => {
    vi.stubGlobal("localStorage", {
      getItem: () => {
        throw new Error("storage unavailable");
      },
      setItem: () => {
        throw new Error("storage unavailable");
      },
    });
    await renderOverview();
    expect(moduleOrder()).toEqual(orderedModules);
    await act(async () => layoutButton("自定义布局").click());
    await act(async () =>
      layoutButton("API 提供商与模型").dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      ),
    );
    expect(moduleOrder()[1]).toBe("tokens");
    expect(container.querySelector("[role='alert']")?.textContent).toContain(
      "无法保存到本机",
    );
    await act(async () => layoutButton("显示API 提供商与模型").click());
    expect(
      container.querySelector("[aria-labelledby='usage-by-service-heading']"),
    ).toBeNull();
    expect(
      layoutButton("显示API 提供商与模型").getAttribute("aria-checked"),
    ).toBe("false");
    expect(container.querySelector("[role='alert']")?.textContent).toContain(
      "无法保存到本机",
    );
  });

  it.each(["drop", "drop-down", "Escape", "pointercancel"])(
    "handles dragging modules of different heights: %s",
    async (finish) => {
      await renderOverview();
      await act(async () => layoutButton("自定义布局").click());
      const list = container.querySelector<HTMLOListElement>(
        'ol[aria-label="自定义布局"]',
      )!;
      const rows = () => [
        ...list.querySelectorAll<HTMLElement>("[data-ordered-item]"),
      ];
      const heights: Record<string, number> = {
        usage: 380,
        "providers-models": 440,
        tokens: 240,
        access: 80,
        system: 200,
      };
      const scroller = container.querySelector<HTMLElement>(
        '[data-slot="overview-content"]',
      )!;
      scroller.style.overflowY = "auto";
      scroller.scrollTop = 400;
      Object.defineProperty(scroller, "clientHeight", {
        configurable: true,
        value: 600,
      });
      vi.spyOn(scroller, "getBoundingClientRect").mockReturnValue(
        new DOMRect(0, 100, 800, 600),
      );
      list.setPointerCapture = vi.fn();
      list.hasPointerCapture = () => false;
      vi.spyOn(list, "getBoundingClientRect").mockImplementation(
        () => new DOMRect(0, 100 - scroller.scrollTop, 800, 1400),
      );
      for (const row of rows()) {
        Object.defineProperty(row, "offsetHeight", {
          configurable: true,
          get: () =>
            list.dataset.sorting === "true"
              ? Math.min(154, heights[row.dataset.orderedItem!])
              : heights[row.dataset.orderedItem!],
        });
        Object.defineProperty(row, "offsetTop", {
          configurable: true,
          get: () =>
            (parseFloat(list.style.paddingTop) || 0) +
            rows()
              .slice(0, rows().indexOf(row))
              .reduce((total, other) => total + other.offsetHeight + 12, 0),
        });
        vi.spyOn(row, "getBoundingClientRect").mockImplementation(
          () =>
            new DOMRect(
              0,
              100 - scroller.scrollTop + row.offsetTop,
              800,
              row.offsetHeight,
            ),
        );
        vi.spyOn(
          row.querySelector("button")!,
          "getBoundingClientRect",
        ).mockImplementation(
          () =>
            new DOMRect(0, 100 - scroller.scrollTop + row.offsetTop, 28, 28),
        );
      }
      const handle = layoutButton("按访问令牌");
      await act(async () =>
        handle.dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            pointerId: 1,
            button: 0,
            clientX: 15,
            clientY: handle.getBoundingClientRect().top + 10,
          }),
        ),
      );
      const originalContent = container.querySelector(
        "[data-testid='token-usage-panel']",
      );
      await act(async () =>
        list.dispatchEvent(
          new PointerEvent("pointermove", {
            bubbles: true,
            pointerId: 1,
            clientX: 15,
            clientY: handle.getBoundingClientRect().top + 16,
          }),
        ),
      );
      expect(moduleOrder()).toEqual(orderedModules);
      expect(list.style.paddingTop).toBe("0px");
      expect(scroller.scrollTop).toBe(0);
      expect(
        [
          ...container.querySelectorAll<HTMLElement>(
            '[data-slot="ordered-module-content"]',
          ),
        ].every((element) => element.classList.contains("max-h-28")),
      ).toBe(true);
      expect(container.querySelector("[data-testid='token-usage-panel']")).toBe(
        originalContent,
      );
      await act(async () =>
        list.dispatchEvent(
          new PointerEvent("pointermove", {
            bubbles: true,
            pointerId: 1,
            clientX: 15,
            clientY:
              finish === "drop-down"
                ? rows().at(-1)!.getBoundingClientRect().bottom
                : rows()[0].getBoundingClientRect().top + 5,
          }),
        ),
      );
      expect(
        finish === "drop-down" ? moduleOrder().at(-1) : moduleOrder()[0],
      ).toBe("tokens");
      expect(localStorage.getItem(OVERVIEW_LAYOUT_STORAGE_KEY)).toBeNull();
      await act(async () => {
        if (finish === "Escape") {
          handle.dispatchEvent(
            new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
          );
        } else {
          list.dispatchEvent(
            new PointerEvent(
              finish.startsWith("drop") ? "pointerup" : "pointercancel",
              { bubbles: true, pointerId: 1 },
            ),
          );
        }
      });
      expect(moduleOrder()[0]).toBe(finish === "drop" ? "tokens" : "usage");
      if (finish === "drop-down") expect(moduleOrder().at(-1)).toBe("tokens");
      expect(container.querySelector("[data-drop-slot]")).toBeNull();
      if (!finish.startsWith("drop")) expect(scroller.scrollTop).toBe(400);
      expect(
        [
          ...container.querySelectorAll<HTMLElement>(
            '[data-slot="ordered-module-content"]',
          ),
        ].every((element) => !element.classList.contains("max-h-28")),
      ).toBe(true);
      expect(container.querySelector("[data-testid='token-usage-panel']")).toBe(
        originalContent,
      );
      expect(localStorage.getItem(OVERVIEW_LAYOUT_STORAGE_KEY) !== null).toBe(
        finish.startsWith("drop"),
      );
    },
  );

  it("keeps normal overview modules for an empty workspace", async () => {
    const { onAddService } = await renderOverview({
      catalog: emptyCatalog,
      tokenCatalog: emptyTokens,
    });
    expect(moduleOrder()).toEqual(orderedModules);
    expect(container.querySelector("#usage-heading")).not.toBeNull();
    expect(container.textContent).not.toContain("欢迎使用 AstrLink");
    await act(async () => button("添加 API 提供商").click());
    expect(onAddService).toHaveBeenCalledOnce();
  });

  it("previews the normal overview without a separate welcome page", async () => {
    await renderOverview({
      catalog: { ...emptyCatalog, status: "blocked" },
      tokenCatalog: { ...emptyTokens, status: "blocked" },
      snapshot: browserSnapshot(),
      isReady: false,
      isNativeApp: false,
      usage: { status: "blocked", summary: null, error: null },
    });
    expect(moduleOrder()).toEqual(orderedModules);
    expect(container.textContent).toContain("浏览器预览");
    expect(container.textContent).not.toContain("欢迎使用 AstrLink");
    expect(container.textContent).not.toContain("重启网关");
  });

  it("shows catalog loading inside the normal overview", async () => {
    await renderOverview({
      catalog: { ...emptyCatalog, status: "loading" },
      tokenCatalog: { ...emptyTokens, status: "loading" },
    });
    expect(moduleOrder()).toEqual(orderedModules);
    expect(container.querySelector("[data-slot='loading-state']")).toBeTruthy();
    expect(container.textContent).not.toContain("欢迎使用 AstrLink");
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
    expect(container.textContent).not.toContain("欢迎使用 AstrLink");
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
    expect(container.querySelector("#usage-heading")).toBeTruthy();
    expect(
      container.querySelector("[data-slot='metric-group']")?.textContent,
    ).toContain("12");
  });

  it("shows catalog and usage errors in the normal overview", async () => {
    await renderOverview({
      catalog: { ...emptyCatalog, status: "error", error: "目录读取失败" },
      tokenCatalog: emptyTokens,
      usage: { status: "error", summary: null, error: "用量读取失败" },
    });
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
    expect(container.textContent).toContain("按 API 提供商");
    expect(container.textContent).toContain("按模型");
    expect(container.textContent).toContain("Primary gateway");
    expect(container.textContent).toContain("gpt-4o");
    expect(container.textContent).toContain("未知模型");
    expect(container.textContent).not.toContain("上游服务");
    expect(container.textContent).not.toContain("连接 AstrLink");
  });

  it("supports single-condition token sorting with icon controls", async () => {
    const tokenCatalog: AccessTokenCatalog = {
      ...readyTokens,
      items: [
        readyTokens.items[0],
        {
          id: "token_02",
          name: "Terminal",
          hint: "astr_…T2",
          created_at: "2026-07-25T10:30:00Z",
        },
        {
          id: "token_03",
          name: "CI",
          hint: "astr_…C3",
          created_at: "2026-07-26T10:30:00Z",
        },
      ],
    };
    const summary = readySummary({
      by_token: [
        group("token_01", { total_tokens: 100, requests: 1 }),
        group("token_02", { total_tokens: 100, requests: 3 }),
        group("token_03", { total_tokens: 50, requests: 5 }),
      ],
    });
    await renderOverview({
      tokenCatalog,
      usage: { status: "ready", summary, error: null },
    });

    const panel = container.querySelector<HTMLElement>(
      "[data-testid='token-usage-panel']",
    )!;
    const control = (key: string) =>
      panel.querySelector<HTMLButtonElement>(
        `[data-testid='token-sort-${key}']`,
      )!;
    const rowNames = () =>
      [
        ...panel.querySelectorAll<HTMLElement>(
          "[data-slot='paginated-list-items'] > div > button [title]",
        ),
      ].map((node) => node.getAttribute("title"));

    expect(control("tokens").dataset.active).toBe("true");
    expect(control("tokens").getAttribute("aria-pressed")).toBe("true");
    expect(control("fee").dataset.active).toBe("false");
    expect(control("requests").dataset.active).toBe("false");
    expect(rowNames()).toEqual(["Terminal", "VS Code", "CI"]);

    await act(async () => control("requests").click());
    expect(control("tokens").dataset.active).toBe("false");
    expect(control("requests").dataset.active).toBe("true");
    expect(rowNames()).toEqual(["CI", "Terminal", "VS Code"]);

    await act(async () => control("fee").click());
    expect(control("requests").dataset.active).toBe("false");
    expect(control("fee").dataset.active).toBe("true");
    expect(rowNames()).toEqual(["Terminal", "VS Code", "CI"]);

    await act(async () => control("tokens").click());
    expect(control("tokens").dataset.active).toBe("true");
    expect(control("fee").dataset.active).toBe("false");
  });

  it("reports an unread token catalog as unknown instead of empty", async () => {
    await renderOverview({
      tokenCatalog: { status: "blocked", items: [], error: null, stale: false },
      usage: {
        status: "ready",
        summary: readySummary({
          by_token: [group("token_01", { total_tokens: 100, requests: 1 })],
        }),
        error: null,
      },
    });

    const panel = container.querySelector<HTMLElement>(
      "[data-testid='token-usage-panel']",
    )!;
    expect(panel.textContent).toContain("网关就绪后显示访问令牌用量。");
    expect(panel.textContent).toContain("当前没有可用的访问令牌目录数据。");
    expect(panel.textContent).not.toContain("该区间没有访问令牌用量");
  });

  it("suppresses retained token figures after a failed refresh", async () => {
    await renderOverview({
      usage: {
        status: "error",
        summary: readySummary({
          by_token: [group("token_01", { total_tokens: 100, requests: 2 })],
        }),
        error: "boom",
      },
    });

    const panel = container.querySelector<HTMLElement>(
      "[data-testid='token-usage-panel']",
    )!;
    expect(panel.textContent).toContain("等待刷新");
    expect(panel.textContent).toContain("—");
    expect(panel.textContent).not.toContain("100");
    expect(panel.textContent).not.toContain("2 次请求");
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

  it("pages the two usage cards independently without dropping rows or rescaling bars", async () => {
    const services = Array.from({ length: 7 }, (_, index) => ({
      ...gateway,
      id: `service_${index}`,
      name: `Provider ${index + 1}`,
    }));
    const models = Array.from({ length: 14 }, (_, index) =>
      group(`model-${index + 1}`, {
        requests: 14 - index,
        total_tokens: (14 - index) * 100,
      }),
    );
    await renderOverview({
      catalog: { ...readyCatalog, items: services },
      usage: {
        status: "ready",
        error: null,
        summary: readySummary({
          by_service: services.map((service, index) =>
            group(service.id, {
              requests: 7 - index,
              total_tokens: (7 - index) * 100,
            }),
          ),
          by_model: models,
        }),
      },
    });

    const serviceCard = container.querySelector<HTMLElement>(
      '[aria-labelledby="usage-by-service-heading"]',
    )!;
    const modelCard = container.querySelector<HTMLElement>(
      '[aria-labelledby="usage-by-model-heading"]',
    )!;
    const labels = (card: HTMLElement) =>
      [
        ...card.querySelectorAll(
          '[data-slot="paginated-list-items"] strong[title]',
        ),
      ].map((node) => node.textContent);
    const next = (card: HTMLElement) =>
      card.querySelector<HTMLButtonElement>('button[aria-label="下一页"]')!;

    expect(labels(serviceCard)).toEqual(
      services.slice(0, 5).map((service) => service.name),
    );
    expect(labels(modelCard)).toEqual(
      models.slice(0, 5).map((model) => model.id),
    );
    const allModelLabels = labels(modelCard);
    await act(async () => next(modelCard).click());
    allModelLabels.push(...labels(modelCard));
    expect(labels(serviceCard)).toEqual(
      services.slice(0, 5).map((service) => service.name),
    );
    const firstBar = modelCard.querySelector<HTMLElement>(
      '[data-slot="paginated-list-items"] [aria-hidden="true"] > span[style]',
    )!;
    expect(Number.parseFloat(firstBar.style.width)).toBeCloseTo(
      (900 / 1400) * 100,
      0,
    );
    await act(async () => next(modelCard).click());
    allModelLabels.push(...labels(modelCard));
    expect(allModelLabels).toEqual(models.map((model) => model.id));
    expect(next(modelCard).disabled).toBe(true);
    await act(async () => next(serviceCard).click());
    expect(labels(serviceCard)).toEqual(
      services.slice(5).map((service) => service.name),
    );
    expect(labels(modelCard)).toEqual(
      models.slice(10).map((model) => model.id),
    );
    expect(next(serviceCard).disabled).toBe(true);
    expect(button("热力图").getAttribute("data-state")).toBe("on");
  });

  it("keeps card pages valid after refresh and resets them when the usage range changes", async () => {
    const models = Array.from({ length: 12 }, (_, index) =>
      group(`model-${index + 1}`, {
        requests: 12 - index,
        total_tokens: 1200 - index * 100,
      }),
    );
    const usage = {
      status: "ready" as const,
      error: null,
      summary: readySummary({ by_model: models }),
    };
    await renderOverview({ usage });
    const card = () =>
      container.querySelector<HTMLElement>(
        '[aria-labelledby="usage-by-model-heading"]',
      )!;
    const next = () =>
      card().querySelector<HTMLButtonElement>('button[aria-label="下一页"]')!;
    await act(async () => next().click());
    await act(async () => next().click());
    expect(card().textContent).toContain("model-12");

    await renderOverview({
      usage: {
        ...usage,
        summary: readySummary({ by_model: models.slice(0, 2) }),
      },
    });
    expect(card().textContent).toContain("model-1");
    expect(card().querySelector("nav")).toBeNull();
    await renderOverview({ usage });
    expect(card().textContent).not.toContain("model-12");
    await act(async () => next().click());
    expect(card().textContent).toContain("model-6");

    await renderOverview({ usage, usagePreset: "30d" });
    expect(card().textContent).toContain("model-1");
    expect(card().textContent).not.toContain("model-6");
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

  it("refreshes cached calendar labels when language or summary data changes", async () => {
    const summary = emptyUsageSummary(resolveUsageWindow("1y", now));
    const usage = { status: "ready" as const, summary, error: null };
    await renderOverview({ usage, usagePreset: "1y" });
    const firstLabel = () =>
      container
        .querySelector('[data-slot="activity-cell"]')
        ?.getAttribute("aria-label");
    expect(firstLabel()).toContain("2025年9月5日");
    try {
      await act(async () => applyLocale("en"));
      expect(firstLabel()).toContain("Sep 5, 2025");
      await renderOverview({
        usage: {
          ...usage,
          summary: {
            ...summary,
            by_day: summary.by_day.map((day, index) =>
              index === 0 ? { ...day, total_tokens: 999 } : day,
            ),
          },
        },
        usagePreset: "1y",
      });
      expect(firstLabel()).toContain("999 Token");
    } finally {
      await act(async () => applyLocale("zh-CN"));
    }
    expect(firstLabel()).toContain("2025年9月5日");
    expect(firstLabel()).toContain("999 Token");
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
    expect(cells[0].style.gridColumn).toBe("1");
    expect(cells[3].style.gridRow).toBe("2");
    expect(cells[3].style.gridColumn).toBe("2");
    await act(async () => {
      cells[0].focus();
      cells[0].dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
      );
    });
    expect(document.activeElement).toBe(cells[7]);
    expect(cells.filter((cell) => cell.tabIndex === 0)).toHaveLength(1);
  });

  it.each([1600, 1000, 700, 320])(
    "keeps one readable year calendar at %i px without losing dates or keyboard navigation",
    async (width) => {
      const summary = emptyUsageSummary(resolveUsageWindow("1y", now));
      const measure = vi
        .spyOn(HTMLElement.prototype, "getBoundingClientRect")
        .mockReturnValue(new DOMRect(0, 0, width, 160));
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
        ...container.querySelectorAll<HTMLElement>(
          "[data-slot='activity-calendar']",
        ),
      ];
      expect(bands).toHaveLength(1);
      for (const band of bands) {
        const cellSize = Number(
          band.style.gridTemplateRows.match(/repeat\(7, ([\d.]+)px\)/)?.[1],
        );
        expect(cellSize).toBeGreaterThanOrEqual(14);
        expect(cellSize).toBeLessThanOrEqual(18);
      }
      const cells = [
        ...container.querySelectorAll<HTMLButtonElement>(
          "[data-slot='activity-cell']",
        ),
      ];
      expect(cells.map((cell) => cell.dataset.date)).toEqual(
        summary.by_day.map((day) => day.date),
      );
      const target = cells[7];
      const previousWeek = cells[cells.indexOf(target) - 7];
      await act(async () => {
        previousWeek.focus();
        previousWeek.dispatchEvent(
          new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }),
        );
      });
      expect(document.activeElement).toBe(target);
      expect(cells.filter((cell) => cell.tabIndex === 0)).toHaveLength(1);
    },
  );

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
    expect(
      container.querySelector('[aria-label="用量区间"]')?.textContent,
    ).toBe("近 24 小时");
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
