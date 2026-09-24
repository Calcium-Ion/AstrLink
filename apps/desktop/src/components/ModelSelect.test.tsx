// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ModelSelect } from "./ModelSelect";

describe("ModelSelect", () => {
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
  const options = [
    "gpt-5",
    "claude-sonnet-4-5",
    "gemini-2.5-pro",
    "custom-route",
    "gpt-5",
  ];
  function Harness({
    strict = false,
    change = () => {},
    commit,
  }: {
    strict?: boolean;
    change?: (value: string) => void;
    commit?: (value: string) => void;
  }) {
    const [value, setValue] = useState("");
    return (
      <ModelSelect
        aria-label="model"
        value={value}
        options={options}
        allowCustomValue={!strict}
        onValueCommit={commit}
        onValueChange={(next) => {
          setValue(next);
          change(next);
        }}
      />
    );
  }
  const input = () =>
    container.querySelector<HTMLInputElement>('[role="combobox"]')!;
  const suggestions = () => [...document.querySelectorAll('[role="option"]')];
  async function type(value: string) {
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input(), value);
      input().dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it("filters case-insensitively, renders option and selected icons, and supports keyboard selection", async () => {
    await act(async () => root.render(<Harness />));
    await act(async () => input().click());
    expect(suggestions()).toHaveLength(4);
    expect(suggestions().every((option) => option.querySelector("svg"))).toBe(
      true,
    );
    await type(" CLAUDE");
    expect(
      suggestions().map((option) => option.getAttribute("aria-label")),
    ).toEqual(["claude-sonnet-4-5"]);
    await act(async () =>
      input().dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      ),
    );
    expect(input().getAttribute("aria-activedescendant")).toBe(
      suggestions()[0].id,
    );
    await act(async () =>
      input().dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      ),
    );
    expect(input().value).toBe("claude-sonnet-4-5");
    expect(suggestions()).toHaveLength(0);
    expect(
      container.querySelector('[data-slot="combobox"] > span svg'),
    ).toBeTruthy();
    await act(async () => input().click());
    expect(suggestions()).toHaveLength(4);
  });

  it("retains custom model input and clears the filter", async () => {
    await act(async () => root.render(<Harness />));
    await type("my-new-route");
    expect(input().value).toBe("my-new-route");
    expect(suggestions()).toHaveLength(0);
    expect(document.querySelector('[role="status"]')).toBeTruthy();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[aria-label="清除搜索"]')!
        .click(),
    );
    expect(input().value).toBe("");
    expect(suggestions()).toHaveLength(4);
  });

  it("keeps search text out of strict selections until an option is chosen", async () => {
    const change = vi.fn();
    await act(async () => root.render(<Harness strict change={change} />));
    await type("gpt");
    expect(change).not.toHaveBeenCalled();
    await act(async () => (suggestions()[0] as HTMLElement).click());
    expect(change).toHaveBeenCalledExactlyOnceWith("gpt-5");
    expect(input().value).toBe("gpt-5");
  });
  it("commits selections, Enter and blur without committing partial typing", async () => {
    const commit = vi.fn();
    await act(async () => root.render(<Harness commit={commit} />));
    await type("custom-");
    expect(commit).not.toHaveBeenCalled();
    await type("custom-model");
    await act(async () =>
      input().dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      ),
    );
    expect(commit).toHaveBeenLastCalledWith("custom-model");
    await type("gpt");
    await act(async () => (suggestions()[0] as HTMLElement).click());
    expect(commit).toHaveBeenLastCalledWith("gpt-5");
    await type("another-model");
    await act(async () =>
      input().dispatchEvent(new FocusEvent("focusout", { bubbles: true })),
    );
    expect(commit).toHaveBeenLastCalledWith("another-model");
  });
});
