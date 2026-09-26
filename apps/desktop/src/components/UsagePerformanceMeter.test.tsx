// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { UsagePerformanceMeter } from "./UsagePerformanceMeter";
import { performanceDetailWindows } from "../use-performance-details";
import { emptyUsageTotals, type UsageWindow } from "../usage-range";

const bridge = vi.hoisted(() => ({ getUsageSummary: vi.fn() }));
vi.mock("../bridge", () => bridge);
const performance = {
  cache_hit_rate: 0.25,
  output_tokens_per_second: 40,
  cache_samples: 2,
  speed_samples: 2,
};
const group = (id: string, rate: number) => ({
  id,
  ...emptyUsageTotals(),
  requests: 4,
  performance: { ...performance, cache_hit_rate: rate },
});

describe("performance detail popover", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.getUsageSummary.mockReset();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });
  const render = async (kind: "service" | "token", id = "target_one") => {
    await act(async () =>
      root.render(
        <UsagePerformanceMeter
          performance={performance}
          status="ready"
          periodLabel="近 7 天"
          scopeDescription="所选对象的统计"
          ready
          target={{ kind, id, name: "Test target" }}
        />,
      ),
    );
  };
  const open = async () => {
    await act(async () =>
      host
        .querySelector<HTMLButtonElement>(
          'button[aria-label="查看 Test target 的缓存率与 TPS"]',
        )!
        .click(),
    );
  };

  it("loads today, seven and thirty days on demand for the selected provider", async () => {
    bridge.getUsageSummary.mockImplementation(async (window: UsageWindow) => ({
      by_service: [
        group("another_provider", 0.99),
        group(
          "target_one",
          window.preset === "1d" ? 0 : window.preset === "7d" ? 0.5 : 0.75,
        ),
      ],
      by_token: [],
    }));
    await render("service");
    expect(bridge.getUsageSummary).not.toHaveBeenCalled();
    await open();
    expect(bridge.getUsageSummary).toHaveBeenCalledTimes(3);
    const dialog = document.querySelector('[role="dialog"]')!;
    expect(
      dialog.querySelector('[data-period="today"]')?.textContent,
    ).toContain("0.0%");
    expect(dialog.querySelector('[data-period="7d"]')?.textContent).toContain(
      "50.0%",
    );
    expect(dialog.querySelector('[data-period="30d"]')?.textContent).toContain(
      "75.0%",
    );
    expect(dialog.textContent).not.toContain("99.0%");
    expect(dialog.querySelector("details")?.open).toBe(false);
    expect(dialog.textContent).toContain("4 次成功请求");
    expect(dialog.textContent).toContain("2 个样本");
    const today = bridge.getUsageSummary.mock.calls[0][0] as UsageWindow;
    expect(new Date(today.from).getHours()).toBe(0);
  });

  it("uses token groups and keeps successful periods when one fails, with retry", async () => {
    bridge.getUsageSummary.mockImplementation(async (window: UsageWindow) => {
      if (window.preset === "7d") throw new Error("Unavailable");
      return {
        by_service: [group("target_one", 0.99)],
        by_token: window.preset === "30d" ? [] : [group("target_one", 0.2)],
      };
    });
    await render("token");
    await open();
    const dialog = () => document.querySelector('[role="dialog"]')!;
    expect(
      dialog().querySelector('[data-period="today"]')?.textContent,
    ).toContain("20.0%");
    expect(dialog().querySelector('[data-period="7d"]')?.textContent).toContain(
      "统计加载失败",
    );
    expect(
      dialog().querySelector('[data-period="30d"]')?.textContent,
    ).toContain("—");
    bridge.getUsageSummary.mockResolvedValue({
      by_token: [group("target_one", 0.4)],
      by_service: [],
    });
    await act(async () =>
      dialog()
        .querySelector<HTMLButtonElement>('button[aria-label="刷新"]')!
        .click(),
    );
    expect(dialog().querySelector('[data-period="7d"]')?.textContent).toContain(
      "40.0%",
    );
  });

  it("ignores late results after switching the target", async () => {
    const finish: Array<(value: unknown) => void> = [];
    bridge.getUsageSummary.mockImplementation(
      () => new Promise((resolve) => finish.push(resolve)),
    );
    await render("service");
    await open();
    bridge.getUsageSummary.mockResolvedValue({
      by_service: [group("target_two", 0.6)],
      by_token: [],
    });
    await render("service", "target_two");
    await act(async () =>
      finish.forEach((resolve) =>
        resolve({ by_service: [group("target_one", 0.99)], by_token: [] }),
      ),
    );
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(
      "60.0%",
    );
    expect(
      document.querySelector('[role="dialog"]')?.textContent,
    ).not.toContain("99.0%");
  });

  it("uses local calendar days instead of relabeling a rolling 24-hour window", () => {
    const now = new Date(2026, 8, 25, 17, 38);
    const windows = performanceDetailWindows(now);
    expect(
      windows.map(({ window }) => new Date(window.from).getDate()),
    ).toEqual([25, 19, 27]);
    for (const { window } of windows) {
      expect(new Date(window.from).getHours()).toBe(0);
      expect(new Date(window.to).getDate()).toBe(26);
      expect(new Date(window.to).getHours()).toBe(0);
    }
  });
});
