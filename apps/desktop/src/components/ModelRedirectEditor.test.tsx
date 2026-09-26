// @vitest-environment happy-dom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ModelRedirect } from "@/failure-policy-model";
import { applyLocale } from "@/i18n";

import { ModelRedirectEditor } from "./ModelRedirectEditor";

describe("ModelRedirectEditor", () => {
  let container: HTMLDivElement;
  let root: Root;
  beforeEach(async () => {
    await applyLocale("zh-CN");
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

  function Harness({
    initial = [],
    change = () => {},
    disabled,
    showAllIssues,
    models = ["claude-sonnet-4-5", "gpt-5", "astrlink/auto"],
  }: {
    initial?: ModelRedirect[];
    change?: (value: ModelRedirect[]) => void;
    disabled?: boolean;
    showAllIssues?: boolean;
    models?: string[];
  }) {
    const [value, setValue] = useState(initial);
    return (
      <ModelRedirectEditor
        value={value}
        modelOptions={models}
        disabled={disabled}
        showAllIssues={showAllIssues}
        onChange={(next) => {
          setValue(next);
          change(next);
        }}
      />
    );
  }

  const button = (label: string) =>
    [...document.querySelectorAll<HTMLButtonElement>("button")].find(
      (element) =>
        element.textContent === label ||
        element.getAttribute("aria-label") === label,
    );
  const field = (label: string) =>
    container.querySelector<HTMLInputElement>(
      `input[role="combobox"][aria-label="${label}"]`,
    )!;
  const alerts = () =>
    [...container.querySelectorAll('[role="alert"]')].map(
      (element) => element.textContent,
    );
  const statuses = () =>
    [...container.querySelectorAll('p[role="status"]')].map(
      (element) => element.textContent,
    );
  async function type(label: string, value: string) {
    await act(async () => {
      const input = field(label);
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it("adds, edits, toggles, and deletes rules with focus kept in the editor", async () => {
    const change = vi.fn();
    await act(async () => root.render(<Harness change={change} />));
    expect(container.textContent).toContain("Codex 自动审查");
    expect(container.querySelector("table")).not.toBeNull();

    await act(async () => button("添加重定向")!.click());
    expect(change).toHaveBeenLastCalledWith([
      { from: "", to: "", enabled: true },
    ]);
    expect(document.activeElement).toBe(field("第 1 条规则的请求模型"));
    // A new blank row is incomplete, not an error, until a save attempt.
    expect(alerts()).toEqual([]);
    expect(
      container.querySelector('[aria-label="删除 第 1 条规则 的重定向"]'),
    ).toBeTruthy();

    await type("第 1 条规则的请求模型", "gpt-4o");
    await type("第 1 条规则的目标模型", "gpt-5");
    expect(change).toHaveBeenLastCalledWith([
      { from: "gpt-4o", to: "gpt-5", enabled: true },
    ]);
    const toggle = container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="启用 gpt-4o 的重定向"]',
    )!;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await act(async () => toggle.click());
    expect(change).toHaveBeenLastCalledWith([
      { from: "gpt-4o", to: "gpt-5", enabled: false },
    ]);
    expect(toggle.getAttribute("aria-checked")).toBe("false");

    await act(async () => button("添加重定向")!.click());
    expect(container.querySelectorAll("tbody tr")).toHaveLength(3);
    expect(document.activeElement).toBe(field("第 2 条规则的请求模型"));
    await type("第 2 条规则的请求模型", "claude-3-opus");
    await type("第 2 条规则的目标模型", "claude-sonnet-4-5");

    await act(async () => button("删除 gpt-4o 的重定向")!.click());
    expect(change).toHaveBeenLastCalledWith([
      { from: "claude-3-opus", to: "claude-sonnet-4-5", enabled: true },
    ]);
    expect(field("第 1 条规则的请求模型").value).toBe("claude-3-opus");
    expect(document.activeElement).toBe(button("删除 claude-3-opus 的重定向"));
    await act(async () => button("删除 claude-3-opus 的重定向")!.click());
    expect(change).toHaveBeenLastCalledWith([]);
    expect(container.textContent).toContain("Codex 自动审查");
    expect(document.activeElement).toBe(button("添加重定向"));
  });

  it("reports each invalid rule on its own row", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            { from: "same", to: "same", enabled: true },
            { from: "old", to: "gpt-5", enabled: true },
            { from: "old", to: "claude-sonnet-4-5", enabled: false },
            { from: "legacy", to: "old", enabled: true },
            { from: "astrlink/auto", to: "astrlink/auto", enabled: true },
            { from: " padded", to: "gpt-5", enabled: true },
            { from: "x".repeat(257), to: "gpt-5", enabled: true },
          ]}
        />,
      ),
    );
    expect(alerts()).toEqual([
      "目标模型不能与请求模型相同。",
      "已有其他规则使用这个请求模型。",
      "已有其他规则使用这个请求模型。",
      "目标模型是另一条规则的请求模型；重定向只生效一次，不能串联。",
      "目标模型不能与请求模型相同。",
      "模型名开头和结尾不能有空格。",
      "模型名最多 256 个字符。",
    ]);
    const messageRow = container.querySelector(
      '[data-redirect-row="2"]',
    )!.nextElementSibling!;
    expect(messageRow.textContent).toBe("已有其他规则使用这个请求模型。");
    await type("第 1 条规则的目标模型", "astrlink/auto");
    expect(alerts()[0]).toBe("不能重定向到 astrlink/auto。");
    await type("第 1 条规则的目标模型", "gpt-5");
    expect(alerts()).not.toContain("不能重定向到 astrlink/auto。");
  });

  it("reports blank fields only after a save attempt", async () => {
    const initial = [
      { from: "", to: "gpt-5", enabled: true },
      { from: "gpt-4o", to: "", enabled: true },
    ];
    await act(async () => root.render(<Harness initial={initial} />));
    expect(alerts()).toEqual([]);
    await act(async () =>
      root.render(<Harness initial={initial} showAllIssues />),
    );
    expect(alerts()).toEqual(["请填写请求模型。", "请填写目标模型。"]);
  });

  it("warns without blocking when no enabled provider lists the target", async () => {
    await act(async () =>
      root.render(
        <Harness initial={[{ from: "gpt-4o", to: "gpt-6", enabled: true }]} />,
      ),
    );
    expect(alerts()).toEqual([]);
    expect(statuses()).toEqual([
      "已启用的 API 提供商都未列出 gpt-6，请确认有 API 提供商支持该模型。",
    ]);
    await type("第 1 条规则的目标模型", "gpt-5");
    expect(statuses()).toEqual([]);
  });

  it("offers astrlink/auto only as a source", async () => {
    await act(async () =>
      root.render(<Harness initial={[{ from: "", to: "", enabled: true }]} />),
    );
    const options = () =>
      [...document.querySelectorAll('[role="option"]')].map((option) =>
        option.getAttribute("aria-label"),
      );
    await act(async () => field("第 1 条规则的请求模型").click());
    expect(options()).toEqual(["claude-sonnet-4-5", "gpt-5", "astrlink/auto"]);
    await act(async () =>
      field("第 1 条规则的请求模型").dispatchEvent(
        new KeyboardEvent("keydown", { key: "Tab", bubbles: true }),
      ),
    );
    await act(async () => field("第 1 条规则的目标模型").click());
    expect(options()).toEqual(["claude-sonnet-4-5", "gpt-5"]);
  });

  const builtinToggle = () =>
    container.querySelector<HTMLButtonElement>(
      '[role="switch"][aria-label="启用 codex-auto-review 的重定向"]',
    )!;

  it("always shows the built-in rule disabled without changing the saved configuration", async () => {
    const change = vi.fn();
    await act(async () => root.render(<Harness change={change} />));
    expect(container.textContent).toContain("Codex 自动审查");
    expect(container.textContent).toContain("内置");
    expect(button("使用预设")).toBeUndefined();
    expect(builtinToggle().getAttribute("aria-checked")).toBe("false");
    expect(field("Codex 自动审查的目标模型").value).toBe("gpt-5.6-luna");
    expect(
      container.querySelector('input[value="codex-auto-review"]'),
    ).toBeNull();
    expect(button("删除 codex-auto-review 的重定向")).toBeUndefined();
    expect(change).not.toHaveBeenCalled();
    expect(statuses()).toEqual([]);

    await act(async () => builtinToggle().click());
    expect(change).toHaveBeenLastCalledWith([
      { from: "codex-auto-review", to: "gpt-5.6-luna", enabled: true },
    ]);
    expect(statuses()).toHaveLength(1);
    expect(
      container.querySelectorAll(
        '[aria-label="启用 codex-auto-review 的重定向"]',
      ),
    ).toHaveLength(1);
    await act(async () => builtinToggle().click());
    expect(change).toHaveBeenLastCalledWith([
      { from: "codex-auto-review", to: "gpt-5.6-luna", enabled: false },
    ]);
    expect(statuses()).toEqual([]);
    expect(container.textContent).toContain("Codex 自动审查");
  });

  it("can configure the built-in target before enabling it", async () => {
    const change = vi.fn();
    await act(async () => root.render(<Harness change={change} />));
    await type("Codex 自动审查的目标模型", "gpt-5");
    expect(change).toHaveBeenLastCalledWith([
      { from: "codex-auto-review", to: "gpt-5", enabled: false },
    ]);
    await act(async () => builtinToggle().click());
    expect(change).toHaveBeenLastCalledWith([
      { from: "codex-auto-review", to: "gpt-5", enabled: true },
    ]);
  });

  it.each([true, false])(
    "preserves a saved built-in rule with enabled=%s",
    async (enabled) => {
      const change = vi.fn();
      await act(async () =>
        root.render(
          <Harness
            change={change}
            initial={[
              { from: "old", to: "gpt-5", enabled: true },
              { from: "codex-auto-review", to: "claude-sonnet-4-5", enabled },
            ]}
          />,
        ),
      );
      expect(builtinToggle().getAttribute("aria-checked")).toBe(
        String(enabled),
      );
      expect(field("Codex 自动审查的目标模型").value).toBe("claude-sonnet-4-5");
      expect(
        container.querySelector("[data-redirect-row]")?.textContent,
      ).toContain("Codex 自动审查");
      expect(change).not.toHaveBeenCalled();
      await act(async () => button("删除 old 的重定向")!.click());
      expect(change).toHaveBeenLastCalledWith([
        { from: "codex-auto-review", to: "claude-sonnet-4-5", enabled },
      ]);
      expect(document.activeElement).toBe(button("添加重定向"));
    },
  );

  it("keeps duplicate imported sources visible so they can be fixed", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            { from: "codex-auto-review", to: "gpt-5", enabled: false },
            {
              from: "codex-auto-review",
              to: "claude-sonnet-4-5",
              enabled: true,
            },
          ]}
        />,
      ),
    );
    expect(alerts()).toHaveLength(2);
    expect(field("第 2 条规则的请求模型").value).toBe("codex-auto-review");
    await act(async () => button("删除 codex-auto-review 的重定向")!.click());
    expect(alerts()).toEqual([]);
    expect(button("删除 codex-auto-review 的重定向")).toBeUndefined();
  });

  it("disables every control while disabled", async () => {
    await act(async () =>
      root.render(
        <Harness
          disabled
          initial={[{ from: "gpt-4o", to: "gpt-5", enabled: true }]}
        />,
      ),
    );
    expect(button("添加重定向")!.disabled).toBe(true);
    expect(button("删除 gpt-4o 的重定向")!.disabled).toBe(true);
    expect(field("第 1 条规则的请求模型").disabled).toBe(true);
    expect(field("第 1 条规则的目标模型").disabled).toBe(true);
    expect(
      container.querySelector<HTMLButtonElement>('[role="switch"]')!.disabled,
    ).toBe(true);
    expect(field("Codex 自动审查的目标模型").disabled).toBe(true);
  });

  it("stops adding rules at the limit", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={Array.from({ length: 200 }, (_, index) => ({
            from: `model-${index}`,
            to: "gpt-5",
            enabled: true,
          }))}
        />,
      ),
    );
    expect(container.textContent).toContain("已达上限 200 条");
    expect(button("添加重定向")!.disabled).toBe(true);
    expect(builtinToggle().disabled).toBe(true);
    expect(field("Codex 自动审查的目标模型").disabled).toBe(true);
  });
});
