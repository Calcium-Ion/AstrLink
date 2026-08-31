// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { ModelBrandIcon } from "./ModelBrandIcon";

describe("ModelBrandIcon", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("renders an svg for a known model id", async () => {
    await act(async () => {
      root.render(<ModelBrandIcon model="gpt-4o" />);
    });

    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("renders a fallback svg for an unknown model id", async () => {
    await act(async () => {
      root.render(<ModelBrandIcon model="custom-local-7b" />);
    });

    expect(container.querySelector("svg")).not.toBeNull();
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
