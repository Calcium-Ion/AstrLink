// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { ModelIcon } from "@lobehub/icons";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { ModelBrandIcon } from "./ModelBrandIcon";

describe("ModelBrandIcon", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it.each([
    "gpt-4o",
    "claude-sonnet-4",
    "gemini-2.5-pro",
    "kimi-k2",
    "glm-4.5",
    "minimax-m2",
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
    expect(rendered.querySelector("svg")?.outerHTML).toBe(
      original.querySelector("svg")?.outerHTML,
    );
    expect(container.querySelector("[data-animated-icon]")).toBeNull();
  });

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
