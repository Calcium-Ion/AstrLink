// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { applyLocale } from "../i18n";
import { CompactCount } from "./CompactCount";

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
  await applyLocale("zh-CN");
});

describe("CompactCount", () => {
  it("abbreviates Chinese token counts and keeps the exact value in the title", async () => {
    await act(async () => {
      root.render(<CompactCount value={1_743_331} />);
    });

    const node = container.querySelector("span");
    expect(node?.textContent).toBe("174.33万");
    expect(node?.getAttribute("title")).toBe("1,743,331");
  });

  it("renders the placeholder when the value is missing", async () => {
    await act(async () => {
      root.render(<CompactCount value={null} />);
    });

    const node = container.querySelector("span");
    expect(node?.textContent).toBe("—");
    expect(node?.getAttribute("title")).toBeNull();
  });

  it("uses a custom placeholder while token usage is loading", async () => {
    await act(async () => {
      root.render(<CompactCount placeholder="…" value={null} />);
    });

    expect(container.textContent).toBe("…");
  });
});
