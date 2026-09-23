// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { applyLocale } from "../i18n";
import { applyQuotaDisplayMode, type QuotaDisplayMode } from "../quota-display";
import { SubscriptionQuotaMeter } from "./SubscriptionQuotaMeter";

describe("subscription quota display", () => {
  let container: HTMLDivElement;
  let root: Root;
  beforeEach(async () => {
    await applyLocale("zh-CN");
    applyQuotaDisplayMode("remaining");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it.each([
    [0, 100, "success"],
    [34, 66, "success"],
    [85, 15, "warning"],
    [100, 0, "destructive"],
    [120, 0, "destructive"],
  ])(
    "shows remaining quota for %s used and retains depletion colors",
    async (used, remaining, tone) => {
      await act(async () =>
        root.render(
          <SubscriptionQuotaMeter label="Quota" usedPercent={used as number} />,
        ),
      );
      const meter = container.querySelector('[role="progressbar"]')!;
      expect(meter.getAttribute("aria-valuenow")).toBe(String(remaining));
      expect(meter.getAttribute("aria-valuetext")).toBe(`剩余 ${remaining}%`);
      expect(meter.className).toContain(`bg-${tone}`);
      expect(
        container.querySelector('[aria-label="额度已用尽"]') !== null,
      ).toBe((used as number) >= 100);
    },
  );

  it.each(["remaining", "used"] as QuotaDisplayMode[])(
    "keeps provider limit warnings in %s mode",
    async (mode) => {
      applyQuotaDisplayMode(mode);
      await act(async () =>
        root.render(
          <SubscriptionQuotaMeter
            label="Quota"
            usedPercent={40}
            limitReached
          />,
        ),
      );
      expect(
        container.querySelector('[aria-label="额度已用尽"]'),
      ).not.toBeNull();
      expect(
        container.querySelector('[role="progressbar"]')!.className,
      ).toContain("bg-destructive");
    },
  );
});
