// @vitest-environment happy-dom

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { RoutingModelsPreview } from "./RoutingModelsPreview";

describe("RoutingModelsPreview", () => {
  it("renders one unavailable mmBERT-small classifier and no alternatives", () => {
    const container = document.createElement("div");
    container.innerHTML = renderToStaticMarkup(<RoutingModelsPreview />);

    expect(
      container.querySelectorAll('[data-testid="routing-classifier-card"]'),
    ).toHaveLength(1);
    expect(container.textContent).toContain("mmBERT-small");
    expect(container.textContent).toContain("jhu-clsp/mmBERT-small");
    expect(container.textContent).toContain("约 140M 参数");
    expect(container.textContent).toContain("最多 512 tokens");
    expect(container.textContent).not.toContain("Granite");
    expect(container.textContent).not.toContain("multilingual-e5");

    const installButton = [
      ...container.querySelectorAll<HTMLButtonElement>("button"),
    ].find((candidate) =>
      candidate.textContent?.includes("下载并启用（开发中）"),
    );
    expect(installButton?.disabled).toBe(true);
    expect(container.querySelector("form")).toBeNull();
    expect(container.querySelector("a")).toBeNull();
  });

  it("marks every taxonomy category and model target as preview-only", () => {
    const container = document.createElement("div");
    container.innerHTML = renderToStaticMarkup(<RoutingModelsPreview />);

    expect(container.textContent).toContain("astrlink/auto");
    expect(container.textContent).toContain("astrlink-text-v1");
    expect(container.textContent).toContain(
      "不会保存、调用 Core 或影响任何请求",
    );

    for (const category of ["general", "code", "writing", "reasoning"]) {
      expect(container.textContent).toContain(category);
      expect(container.textContent).toContain(`demo/${category}-primary`);
      expect(container.textContent).toContain(`demo/${category}-backup`);
    }

    expect(container.querySelectorAll(".routing-category-card")).toHaveLength(
      4,
    );
    expect(container.querySelectorAll(".routing-category-card > header > span")).toHaveLength(
      4,
    );
  });
});
