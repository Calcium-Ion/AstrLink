// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { applyLocale } from "./i18n";
import { emptyUsageSummary, resolveUsageWindow } from "./usage-range";
import { UsageStarTrail } from "./UsageStarTrail";

describe("UsageStarTrail", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("runs today's progress arc and now dot up to the local time", async () => {
    const at = new Date(2026, 10, 2, 18, 0);
    const summary = emptyUsageSummary(resolveUsageWindow("7d", at));
    await act(async () =>
      root.render(<UsageStarTrail now={at} status="ready" summary={summary} />),
    );
    const progress = container.querySelector(
      "[data-slot='star-trail-progress']",
    );
    // 18:00 is three quarters round, so the arc ends at the 18 o'clock mark.
    expect(progress?.getAttribute("d")).toMatch(
      /A113,113 0 1 1 19\.00,132\.00$/,
    );
    const dot = container.querySelector("[data-slot='star-trail-now'] circle");
    expect(dot?.getAttribute("cx")).toBe("19");

    // Past midnight the outer ring is no longer today until the summary moves.
    const tomorrow = new Date(2026, 10, 3, 0, 30);
    await act(async () =>
      root.render(
        <UsageStarTrail now={tomorrow} status="ready" summary={summary} />,
      ),
    );
    expect(
      container.querySelector("[data-slot='star-trail-progress']"),
    ).toBeNull();
    expect(container.querySelector("[data-slot='star-trail-now']")).toBeNull();
  });

  it("shows why the disc is missing when the gateway is not ready", async () => {
    await act(async () =>
      root.render(<UsageStarTrail status="blocked" summary={null} />),
    );
    expect(container.textContent).toContain("网关就绪后显示用量趋势");
    expect(
      container.querySelector("[data-slot='star-trail-chart']"),
    ).toBeNull();
  });
});
