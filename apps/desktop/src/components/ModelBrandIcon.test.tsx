// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { ModelIcon } from "@lobehub/icons";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { ModelBrandIcon, modelVendor } from "./ModelBrandIcon";
import { ServiceKindIcon } from "./ServiceKindIcon";

describe("ModelBrandIcon", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  // Kimi and GLM are absent on purpose: @lobehub/icons draws them with the
  // Moonshot and Z.ai marks, while this app shows the vendor marks used by the
  // Kimi and 智谱 provider kinds (see the provider-kind test below).
  it.each([
    "gpt-4o",
    "claude-sonnet-4",
    "gemini-2.5-pro",
    "minimax-m2",
    "deepseek-v3.1",
    "qwen3-coder-plus",
    "doubao-seed-1.6",
    "grok-4",
    "o3-mini",
  ])("preserves the original brand artwork for %s", async (model) => {
    await act(async () => {
      root.render(<ModelBrandIcon model={model} />);
    });

    const original = document.createElement("div");
    original.innerHTML = renderToStaticMarkup(
      <ModelIcon model={model} size={14} type="color" />,
    );
    const rendered = document.createElement("div");
    rendered.innerHTML = renderToStaticMarkup(<ModelBrandIcon model={model} />);
    // ModelIcon also spreads its avatar color `type` onto the SVG as an inert
    // attribute; the artwork itself must match.
    expect(rendered.querySelector("svg")?.innerHTML).toBe(
      original.querySelector("svg")?.innerHTML,
    );
    expect(rendered.querySelector("svg")?.getAttribute("viewBox")).toBe(
      original.querySelector("svg")?.getAttribute("viewBox"),
    );
    expect(container.querySelector("[data-animated-icon]")).toBeNull();
  });

  it.each([
    ["gpt-4o", "openai"],
    ["claude-sonnet-4", "claude_subscription"],
    ["gemini-2.5-pro", "gemini"],
    ["kimi-k2", "kimi_coding"],
    ["moonshot-v1-8k", "moonshot"],
    ["glm-4.5", "glm"],
    ["glm-5.3-flash", "glm_coding"],
    ["minimax-m2", "minimax"],
    ["deepseek-v3.1", "deepseek"],
    ["qwen3-coder-plus", "qwen"],
    ["doubao-seed-1.6", "doubao"],
    ["grok-4", "xai"],
  ] as const)(
    "draws %s with the same mark as the %s provider kind",
    (model, kind) => {
      const modelMark = document.createElement("div");
      modelMark.innerHTML = renderToStaticMarkup(
        <ModelBrandIcon model={model} size={16} />,
      );
      const kindMark = document.createElement("div");
      kindMark.innerHTML = renderToStaticMarkup(
        <ServiceKindIcon kind={kind} size={16} />,
      );

      expect(modelVendor(model)).not.toBeNull();
      expect(modelMark.querySelector("svg")?.innerHTML).toBe(
        kindMark.querySelector("svg")?.innerHTML,
      );
    },
  );

  it("renders a fallback svg for an unknown model id", async () => {
    await act(async () => {
      root.render(<ModelBrandIcon model="custom-local-7b" />);
    });

    expect(container.querySelector("svg")).not.toBeNull();
    expect(
      container.querySelector('svg[data-animated-icon="brain"]'),
    ).not.toBeNull();
  });

  it("renders nothing when the model id is empty", async () => {
    await act(async () => {
      root.render(
        <>
          <ModelBrandIcon model="" />
          <ModelBrandIcon model="   " />
          <ModelBrandIcon model={null} />
        </>,
      );
    });

    expect(container.querySelector("svg")).toBeNull();
    expect(container.textContent).toBe("");
  });
});
