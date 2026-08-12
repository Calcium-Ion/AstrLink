// @vitest-environment happy-dom

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { AutoRoutingShowcase } from "./AutoRoutingShowcase";
import { RoutingModelsPreview } from "./RoutingModelsPreview";

describe("RoutingModelsPreview", () => {
  it("renders a user-facing auto showcase without classifier internals", () => {
    const container = document.createElement("div");
    container.innerHTML = renderToStaticMarkup(<RoutingModelsPreview />);

    expect(
      container.querySelector('[data-testid="auto-routing-showcase"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("astrlink/auto");
    expect(container.textContent).toContain("理解任务");
    expect(container.textContent).toContain("匹配分类模型池");
    expect(container.textContent).toContain("按优先级尝试");
    expect(container.textContent).not.toContain("mmBERT");
    expect(container.textContent).not.toContain("jhu-clsp");
    expect(container.textContent).not.toContain("ONNX");
    expect(container.querySelector('[data-testid="routing-classifier-card"]')).toBeNull();
    expect(container.querySelector("form")).toBeNull();
    expect(container.querySelector("a")).toBeNull();
  });

  it("marks taxonomy categories as preview-only without fake model targets", () => {
    const container = document.createElement("div");
    container.innerHTML = renderToStaticMarkup(<AutoRoutingShowcase />);

    expect(container.textContent).toContain("astrlink/auto");
    expect(container.textContent).toContain("示意，不可配置");
    expect(container.textContent).not.toContain("demo/");

    for (const category of ["general", "code", "writing", "reasoning"]) {
      expect(container.textContent).toContain(category);
    }

    expect(container.querySelectorAll('[data-testid="routing-category"]')).toHaveLength(4);
  });
});
