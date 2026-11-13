// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { SubscriptionUsageMeter } from "./SubscriptionUsageMeter";
import type { SubscriptionUsage } from "./subscription-usage-model";
import { settleClosedLayer } from "./lib/test-layer-settle";

describe("subscription usage summary", () => {
  let container: HTMLDivElement;
  let root: Root;
  const now = new Date("2026-09-30T06:00:00Z");
  const snapshot = {
    service_id: "service_antigravity",
    fetched_at: now.toISOString(),
  };

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  const render = async (usage: SubscriptionUsage) => {
    await act(async () =>
      root.render(
        <SubscriptionUsageMeter now={now} status="ready" usage={usage} />,
      ),
    );
  };
  const labels = (element: Element) =>
    [...element.querySelectorAll('[role="progressbar"]')].map((meter) =>
      meter.getAttribute("aria-label"),
    );

  it("shows two consumed quotas first and opens all quotas without expanding the row", async () => {
    const extras = Array.from({ length: 33 }, (_, index) => ({
      limit_name: `gemini-${index}`,
      primary: {
        used_percent: index === 8 ? 25 : index === 20 ? 70 : 0,
        reset_at: "2026-09-30T08:00:00Z",
      },
    }));
    await render({ ...snapshot, additional_rate_limits: extras });
    expect(labels(container)).toEqual(["gemini-8", "gemini-20"]);
    expect(extras[0].limit_name).toBe("gemini-0");
    const trigger = container.querySelector<HTMLButtonElement>("button")!;
    expect(trigger.getAttribute("aria-label")).toBe("查看全部（33）");
    expect(trigger.textContent).toBe("");
    const summary = document.getElementById(
      trigger.getAttribute("aria-describedby")!,
    )!;
    expect(labels(summary)).toEqual(["gemini-8", "gemini-20"]);
    await act(async () => trigger.click());
    const popup = document.querySelector('[role="dialog"]')!;
    expect(popup.getAttribute("aria-labelledby")).toBeTruthy();
    expect(labels(popup)).toHaveLength(33);
    expect(labels(popup).slice(0, 2)).toEqual(["gemini-8", "gemini-20"]);
    expect(popup.textContent).toContain("2 小时后重置");
    expect(labels(container)).toHaveLength(2);
    await act(async () => {
      popup.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
      );
    });
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    await settleClosedLayer();
    expect(document.activeElement).toBe(trigger);
  });

  it("counts primary, secondary and model windows toward the same two-item limit", async () => {
    await render({
      ...snapshot,
      primary: { used_percent: 0, limit_window_seconds: 18000 },
      secondary: { used_percent: 10, limit_window_seconds: 604800 },
      additional_rate_limits: [
        {
          limit_name: "Extra",
          primary: { used_percent: 20 },
          secondary: { used_percent: 0 },
        },
      ],
    });
    expect(labels(container)).toEqual(["每周", "Extra"]);
    expect(container.querySelector("button")?.getAttribute("aria-label")).toBe(
      "查看全部（4）",
    );
  });

  it("dims the last reading under a refresh notice after a failed refresh", async () => {
    let refreshes = 0;
    await act(async () =>
      root.render(
        <SubscriptionUsageMeter
          error="new-api usage unavailable: status 401"
          now={now}
          onRefresh={() => refreshes++}
          status="error"
          usage={{
            ...snapshot,
            quota: {
              unlimited: false,
              used_usd: "0",
              remaining_usd: "300",
              total_usd: "300",
            },
          }}
        />,
      ),
    );
    const meter = container.querySelector('[role="progressbar"]')!;
    expect(meter.closest(".opacity-60")).not.toBeNull();
    const notice = container.querySelector("details")!;
    expect(notice.closest(".opacity-60")).toBeNull();
    expect(notice.querySelector("summary")?.textContent).toBe("额度刷新失败");
    expect(notice.textContent).toContain(
      "new-api usage unavailable: status 401",
    );
    expect(notice.textContent).toMatch(/仍显示 \d{2}:\d{2} 读取的额度。/);
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('button[aria-label="刷新"]')!
        .click(),
    );
    expect(refreshes).toBe(1);

    await render({
      ...snapshot,
      quota: {
        unlimited: false,
        used_usd: "0",
        remaining_usd: "300",
        total_usd: "300",
      },
    });
    expect(container.querySelector(".opacity-60")).toBeNull();
    expect(container.querySelector("details")).toBeNull();
  });

  it.each([0, 1, 2])(
    "keeps %i unused quotas in source order without a popover",
    async (count) => {
      await render({
        ...snapshot,
        additional_rate_limits: Array.from({ length: count }, (_, index) => ({
          limit_name: `model-${index}`,
          primary: { used_percent: 0 },
        })),
      });
      expect(labels(container)).toEqual(
        Array.from({ length: count }, (_, index) => `model-${index}`),
      );
      expect(container.querySelector("button")).toBeNull();
    },
  );
});
