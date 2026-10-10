// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Input } from "./input";
import { Textarea } from "./textarea";

describe("text controls", () => {
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

  it("keeps typed text verbatim on macOS", async () => {
    await act(async () =>
      root.render(
        <>
          <Input aria-label="model" type="search" />
          <Input aria-label="key" type="password" />
          <Textarea aria-label="prompt" />
        </>,
      ),
    );
    for (const label of ["model", "key", "prompt"]) {
      const field = container.querySelector(`[aria-label="${label}"]`);
      expect(field?.getAttribute("autocapitalize")).toBe("off");
      expect(field?.getAttribute("autocorrect")).toBe("off");
      expect(field?.getAttribute("spellcheck")).toBe("false");
    }
  });

  it("lets a field opt back in", async () => {
    await act(async () =>
      root.render(
        <Textarea aria-label="notes" autoCapitalize="sentences" spellCheck />,
      ),
    );
    const field = container.querySelector('[aria-label="notes"]');
    expect(field?.getAttribute("autocapitalize")).toBe("sentences");
    expect(field?.getAttribute("spellcheck")).toBe("true");
  });
});
