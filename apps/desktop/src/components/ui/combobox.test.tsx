// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Combobox, MultiCombobox } from "./combobox";

const options = [
  { value: "general", label: "General" },
  { value: "coding", label: "Coding" },
];

describe("MultiCombobox", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    vi.restoreAllMocks();
    container.remove();
  });

  /** 渲染一次多选控件；`props` 覆盖默认值，便于逐条改一个行为。 */
  const render = async (
    props: Partial<Parameters<typeof MultiCombobox>[0]> = {},
  ) => {
    const onChange = props.onChange ?? vi.fn();
    await act(async () =>
      root.render(
        <MultiCombobox
          values={[]}
          options={options}
          onChange={onChange}
          max={5}
          removeLabel={(value) => `Remove ${value}`}
          emptyMessage="Nothing left"
          createLabel={(query) => `Create ${query}`}
          aria-label="Values"
          {...props}
        />,
      ),
    );
    return { onChange };
  };

  const input = () =>
    container.querySelector<HTMLInputElement>('input[role="combobox"]')!;
  const chips = () =>
    container.querySelectorAll("[data-multi-combobox-chip]").length;
  const rowValues = () =>
    [...document.querySelectorAll("[role='option']")].map((row) =>
      row.getAttribute("data-option-value"),
    );

  /** 在输入框里真实输入，让受控输入与列表过滤都跟着走。 */
  const type = async (text: string) => {
    await act(async () => {
      const element = input();
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(element, text);
      element.dispatchEvent(new Event("input", { bubbles: true }));
    });
  };

  const press = async (key: string) => {
    await act(async () => {
      input().dispatchEvent(
        new KeyboardEvent("keydown", { key, bubbles: true }),
      );
    });
  };

  it("keeps selections as chips inside the input row", async () => {
    await render({ values: ["general"] });
    expect(chips()).toBe(1);
    // 没有 `valueLabel` 时 chip 直接显示值本身。
    expect(
      container
        .querySelector("[data-multi-combobox-chip]")
        ?.textContent?.trim(),
    ).toContain("general");
    // 已选值不占输入框的值 —— 输入框永远是新建槽位。
    expect(input().value).toBe("");
  });

  it("fills the chips with the label while writing the value", async () => {
    const { onChange } = await render({ valueLabel: (value) => `«${value}»` });
    await act(async () => {
      input().click();
    });
    await act(async () => {
      document
        .querySelector<HTMLElement>('[data-option-value="coding"]')!
        .click();
    });
    expect(onChange).toHaveBeenCalledWith(["coding"]);
  });

  it("drops the trailing chip on Backspace and never an earlier one", async () => {
    const { onChange } = await render({ values: ["general", "coding"] });
    await press("Backspace");
    expect(onChange).toHaveBeenCalledWith(["general"]);
    // 输入框非空时 Backspace 只改文本，不该删 chip。
    (onChange as ReturnType<typeof vi.fn>).mockClear();
    await type("gen");
    await press("Backspace");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("走 ↑↓ 与 Enter 选中候选", async () => {
    const { onChange } = await render();
    await act(async () => {
      input().click();
    });
    await press("ArrowDown");
    await press("ArrowDown");
    await press("Enter");
    expect(onChange).toHaveBeenCalledWith(["coding"]);
  });

  it("creates a typed value when the field allows it", async () => {
    const { onChange } = await render();
    await type("custom-one");
    await press("Enter");
    expect(onChange).toHaveBeenCalledWith(["custom-one"]);
  });

  it("offers no create row when the field is a closed set", async () => {
    const { onChange } = await render({ allowCustomValue: false });
    await type("custom-one");
    expect(document.querySelector("[data-multi-combobox-create]")).toBeNull();
    await press("Enter");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not offer to create a value that already exists", async () => {
    await render();
    await type("general");
    // 候选里已有的值只作为普通候选项出现，不重复成「新建」。
    expect(document.querySelector("[data-multi-combobox-create]")).toBeNull();
    expect(rowValues()).toEqual(["general"]);
  });

  it("closes on Escape and restores the query", async () => {
    const { onChange } = await render();
    await type("cus");
    await press("Escape");
    expect(input().value).toBe("");
    expect(input().getAttribute("aria-expanded")).toBe("false");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("stops accepting values at the cap", async () => {
    await render({ values: ["general", "coding"], max: 2 });
    expect(input().disabled).toBe(true);
    expect(chips()).toBe(2);
  });

  it("removes a chip through its own button", async () => {
    const { onChange } = await render({ values: ["general", "coding"] });
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          "[data-multi-combobox-chip] [data-multi-combobox-remove]",
        )!
        .click();
    });
    expect(onChange).toHaveBeenCalledWith(["coding"]);
  });

  it("names each chip's remove button after its label", async () => {
    await render({
      values: ["general"],
      valueLabel: (value) => (value === "general" ? "常规" : value),
    });
    expect(
      container
        .querySelector("[data-multi-combobox-remove]")
        ?.getAttribute("aria-label"),
    ).toBe("Remove 常规");
  });
});

describe("Combobox groups, descriptions and hint", () => {
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
    document.body.innerHTML = "";
  });

  const grouped = [
    { value: "a/chat", label: "deepseek-chat", group: "DeepSeek" },
    { value: "a/reasoner", label: "deepseek-reasoner", group: "DeepSeek" },
    { value: "b/gpt", label: "gpt-4o", group: "OpenAI", description: "fast" },
  ];

  const render = async (
    props: Partial<Parameters<typeof Combobox>[0]> = {},
  ) => {
    const onValueChange = vi.fn();
    await act(async () =>
      root.render(
        <Combobox
          value=""
          options={grouped}
          onValueChange={onValueChange}
          emptyMessage="None"
          allowCustomValue={false}
          aria-label="Model"
          {...props}
        />,
      ),
    );
    return { onValueChange };
  };

  const input = () =>
    container.querySelector<HTMLInputElement>('input[role="combobox"]')!;

  const openList = async () => {
    await act(async () => {
      input().click();
      await Promise.resolve();
    });
  };

  const type = async (text: string) => {
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(input(), text);
      input().dispatchEvent(new Event("input", { bubbles: true }));
    });
  };

  it("puts one heading before each run of a group", async () => {
    await render({ renderGroup: (group) => `[${group}]` });
    await openList();
    const listbox = document.querySelector("[role='listbox']")!;
    const lines = [...listbox.children].map((child) =>
      child.getAttribute("role") === "option"
        ? child.getAttribute("data-option-value")
        : child.textContent,
    );
    expect(lines).toEqual([
      "[DeepSeek]",
      "a/chat",
      "a/reasoner",
      "[OpenAI]",
      "b/gpt",
    ]);
  });

  it("shows the description and matches it, the label and the group", async () => {
    await render();
    await openList();
    const gpt = document.querySelector("[data-option-value='b/gpt']")!;
    expect(gpt.textContent).toContain("fast");
    expect(gpt.getAttribute("aria-label")).toBe("gpt-4o fast");
    await type("openai");
    expect(
      [...document.querySelectorAll("[role='option']")].map((row) =>
        row.getAttribute("data-option-value"),
      ),
    ).toEqual(["b/gpt"]);
    await type("FAST");
    expect(document.querySelectorAll("[role='option']")).toHaveLength(1);
  });

  it("selects by value even when the list is grouped", async () => {
    const { onValueChange } = await render();
    await openList();
    await act(async () => {
      document
        .querySelector<HTMLElement>("[data-option-value='a/reasoner']")!
        .click();
    });
    expect(onValueChange).toHaveBeenCalledWith("a/reasoner");
  });

  it("shows the hint only while the list is closed", async () => {
    await render({ value: "b/gpt", displayValue: "gpt-4o", hint: "OpenAI" });
    expect(container.textContent).toContain("OpenAI");
    await openList();
    expect(container.textContent).not.toContain("OpenAI");
  });
});
