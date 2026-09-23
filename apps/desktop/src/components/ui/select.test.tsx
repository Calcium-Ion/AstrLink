// @vitest-environment happy-dom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";

import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./select";

function Options() {
  return (
    <SelectContent>
      <SelectGroup>
        <SelectItem value="one">First option</SelectItem>
      </SelectGroup>
    </SelectContent>
  );
}

describe("Select initial label", () => {
  it("includes the selected label before effects or portals mount", () => {
    const html = renderToString(
      <Select value="one">
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <>
              <SelectItem value="one">First option</SelectItem>
            </>
          </SelectGroup>
        </SelectContent>
      </Select>,
    );
    const container = document.createElement("div");
    container.innerHTML = html;
    expect(
      container.querySelector('[data-slot="select-value"]')?.textContent,
    ).toBe("First option");
  });

  it("preserves placeholders and explicit value content", () => {
    for (const [value, children, expected] of [
      ["", undefined, "Choose"],
      ["one", "Custom label", "Custom label"],
    ] as const) {
      const container = document.createElement("div");
      container.innerHTML = renderToString(
        <Select value={value}>
          <SelectTrigger>
            <SelectValue placeholder="Choose">{children}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="one">First option</SelectItem>
          </SelectContent>
        </Select>,
      );
      expect(
        container.querySelector('[data-slot="select-value"]')?.textContent,
      ).toBe(expected);
    }
  });

  it("updates labels and falls back to Radix for opaque option components", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    try {
      for (const value of ["one", "two"]) {
        await act(async () =>
          root.render(
            <Select value={value}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="one">First option</SelectItem>
                <SelectItem value="two">Second option</SelectItem>
              </SelectContent>
            </Select>,
          ),
        );
        expect(
          container.querySelector('[data-slot="select-value"]')?.textContent,
        ).toBe(value === "one" ? "First option" : "Second option");
      }
      await act(async () =>
        root.render(
          <Select defaultValue="one" key="opaque">
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <Options />
          </Select>,
        ),
      );
      expect(
        container.querySelector('[data-slot="select-value"]')?.textContent,
      ).toBe("First option");
    } finally {
      await act(async () => root.unmount());
      container.remove();
    }
  });
});
