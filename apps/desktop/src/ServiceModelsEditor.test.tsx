// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { settleClosedLayer } from "./lib/test-layer-settle";
import {
  serviceBuiltinModelProtocol,
  serviceModelFormats,
  withModelProtocols,
  type ModelFormat,
  type ModelProtocol,
  type ServiceKind,
} from "./service-model";
import { ServiceModelsEditor } from "./ServiceModelsEditor";

const protocolChanges =
  vi.fn<(models: string[], protocol: ModelFormat | null) => void>();
const removals = vi.fn<(models: string[]) => void>();

/** Keeps the editor's lists in state, the way the service editor does. */
function Harness({
  kind,
  models: initialModels,
  protocols: initialProtocols = [],
}: {
  kind: ServiceKind;
  models: string[];
  protocols?: ModelProtocol[];
}) {
  const [models, setModels] = useState(initialModels);
  const [protocols, setProtocols] = useState(initialProtocols);
  return (
    <ServiceModelsEditor
      compact={kind === "vertex"}
      builtinProtocol={(model) => serviceBuiltinModelProtocol(kind, model)}
      formats={serviceModelFormats(kind)}
      modelEditor=""
      modelProtocols={protocols}
      models={models}
      probingModels={false}
      onAddModels={() => {}}
      onClearModels={() => {
        setModels([]);
        setProtocols([]);
      }}
      onModelEditorChange={() => {}}
      onModelProtocolsChange={(changed, protocol) => {
        protocolChanges(changed, protocol);
        setProtocols((current) =>
          withModelProtocols(current, changed, protocol),
        );
      }}
      onRemoveModels={(removed) => {
        removals(removed);
        setModels((current) =>
          current.filter((model) => !removed.includes(model)),
        );
      }}
    />
  );
}

async function press(element: HTMLElement): Promise<void> {
  await act(async () => {
    element.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    await Promise.resolve();
  });
}

async function click(element: HTMLElement): Promise<void> {
  await act(async () => {
    element.click();
    await Promise.resolve();
  });
}

function textOf(element: Element): string {
  const copy = element.cloneNode(true) as HTMLElement;
  copy
    .querySelectorAll('[aria-hidden="true"]')
    .forEach((icon) => icon.remove());
  return copy.textContent?.trim() ?? "";
}

function button(label: string, scope: ParentNode = document): HTMLElement {
  const match = [...scope.querySelectorAll<HTMLElement>("button")].find(
    (candidate) => textOf(candidate) === label,
  );
  if (!match) throw new Error(`Missing button: ${label}`);
  return match;
}

function formatMenu(model: string): HTMLButtonElement {
  const trigger = document.querySelector<HTMLButtonElement>(
    `button[aria-label="指定 ${model} 发给上游的格式"]`,
  );
  if (!trigger) throw new Error(`Missing format menu: ${model}`);
  return trigger;
}

/** The tag beside a model's name, which only a model with its own format has. */
function formatTag(model: string): string | undefined {
  const row = [
    ...document.querySelectorAll('[data-testid="service-model-row"]'),
  ].find((candidate) => candidate.querySelector("code")?.textContent === model);
  if (!row) throw new Error(`Missing row: ${model}`);
  return (
    row.querySelector('[data-testid="service-model-format"]')?.textContent ??
    undefined
  );
}

function formatChoices(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>('[role="menuitemradio"]')];
}

async function chooseFormat(model: string, option: string): Promise<void> {
  await press(formatMenu(model));
  const item = formatChoices().find(
    (candidate) => textOf(candidate) === option,
  );
  if (!item) throw new Error(`Missing format option: ${option}`);
  await click(item);
  await settleClosedLayer();
}

async function chooseMenuItem(label: string): Promise<void> {
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="menuitem"]'),
  ].find((candidate) => textOf(candidate) === label);
  if (!item) throw new Error(`Missing menu item: ${label}`);
  await click(item);
  await settleClosedLayer();
}

function rowCheckbox(model: string): HTMLButtonElement {
  const checkbox = document.querySelector<HTMLButtonElement>(
    `button[role="checkbox"][aria-label="选择 ${model}"]`,
  );
  if (!checkbox) throw new Error(`Missing row checkbox: ${model}`);
  return checkbox;
}

describe("ServiceModelsEditor upstream formats", () => {
  let root: Root;
  let container: HTMLDivElement;

  const headerActions = () =>
    container.querySelector<HTMLElement>(
      '[data-testid="service-models-actions"]',
    )!;
  const selection = () =>
    container.querySelector<HTMLElement>(
      '[data-testid="service-models-selection"]',
    );

  async function render(
    kind: ServiceKind,
    models: string[],
    protocols?: ModelProtocol[],
  ): Promise<void> {
    await act(async () =>
      root.render(
        <Harness kind={kind} models={models} protocols={protocols} />,
      ),
    );
  }

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("pins a model to a format from its row and tags only pinned models", async () => {
    await render("opencode_go", ["gemini-4-argon-contributor", "glm-5.3"]);
    expect(formatTag("gemini-4-argon-contributor")).toBeUndefined();
    expect(formatTag("glm-5.3")).toBeUndefined();

    // The default choice names the format the kind's table gives the model.
    await press(formatMenu("glm-5.3"));
    expect(formatChoices().map(textOf)).toEqual([
      "默认（Chat）",
      "Responses",
      "Messages",
      "Chat",
      "Gemini",
    ]);
    expect(
      formatChoices()
        .filter((item) => item.getAttribute("aria-checked") === "true")
        .map(textOf),
    ).toEqual(["默认（Chat）"]);
    await click(formatChoices().find((item) => textOf(item) === "Messages")!);
    await settleClosedLayer();
    expect(protocolChanges).toHaveBeenLastCalledWith(
      ["glm-5.3"],
      "anthropic.messages",
    );
    expect(formatTag("glm-5.3")).toBe("Messages 格式");

    await chooseFormat("glm-5.3", "默认（Chat）");
    expect(protocolChanges).toHaveBeenLastCalledWith(["glm-5.3"], null);
    expect(formatTag("glm-5.3")).toBeUndefined();

    await chooseFormat("gemini-4-argon-contributor", "Chat");
    expect(formatTag("gemini-4-argon-contributor")).toBe("Chat 格式");
    await press(formatMenu("gemini-4-argon-contributor"));
    expect(formatChoices().map(textOf)[0]).toBe("默认（Gemini）");
  });

  it.each(["newapi", "vertex"] as const)(
    "offers entry settings as the default for %s",
    async (kind) => {
      await render(kind, ["gemini-3.1-pro"]);
      await chooseFormat("gemini-3.1-pro", "Gemini");
      expect(protocolChanges).toHaveBeenLastCalledWith(
        ["gemini-3.1-pro"],
        "google.generate_content",
      );
      expect(formatTag("gemini-3.1-pro")).toBe("Gemini 格式");
      await chooseFormat("gemini-3.1-pro", "按入口协议设置");
      expect(protocolChanges).toHaveBeenLastCalledWith(
        ["gemini-3.1-pro"],
        null,
      );
      expect(formatTag("gemini-3.1-pro")).toBeUndefined();
    },
  );

  it("sets and clears Vertex formats in bulk without offering Responses", async () => {
    const models = ["claude-test", "gemini-test"];
    await render("vertex", models);
    await press(formatMenu(models[0]));
    expect(formatChoices().map(textOf)).toEqual([
      "按入口协议设置",
      "Messages",
      "Chat",
      "Gemini",
    ]);
    await click(formatChoices()[0]);
    await settleClosedLayer();
    for (const format of ["Messages", "按入口协议设置"]) {
      await click(button("多选", headerActions()));
      for (const model of models) await click(rowCheckbox(model));
      await press(button("指定格式", selection()!));
      expect(
        [...document.querySelectorAll('[role="menuitem"]')].map(textOf),
      ).toEqual(["按入口协议设置", "Messages", "Chat", "Gemini"]);
      await chooseMenuItem(format);
      expect(protocolChanges).toHaveBeenLastCalledWith(
        models,
        format === "Messages" ? "anthropic.messages" : null,
      );
      for (const model of models)
        expect(formatTag(model)).toBe(
          format === "Messages" ? "Messages 格式" : undefined,
        );
    }
  });

  it("sets one format for the checked models from selection mode", async () => {
    await render(
      "newapi",
      ["claude-opus-5-5", "gemini-3.1-pro", "gpt-5.5"],
      [{ model: "gemini-3.1-pro", protocol: "google.generate_content" }],
    );
    const actions = headerActions();
    const enter = button("多选", actions);
    expect(selection()).toBeNull();

    await click(enter);

    // The original actions stay in place, hidden, so the row keeps its size.
    expect(headerActions()).toBe(actions);
    expect(actions.className).toContain("invisible");
    expect(actions.contains(enter)).toBe(true);
    expect(button("折叠全部", actions)).toBeTruthy();
    expect(
      actions.querySelector('button[aria-label="更多模型操作"]'),
    ).not.toBeNull();
    expect(selection()?.querySelector('[role="status"]')?.textContent).toBe(
      "已选 0 个模型",
    );
    // Focus moves to the selection controls that replaced the button.
    expect(document.activeElement?.parentElement?.textContent).toBe("全选");
    expect(button("指定格式", selection()!).hasAttribute("disabled")).toBe(
      true,
    );

    await click(rowCheckbox("gpt-5.5"));
    await click(rowCheckbox("claude-opus-5-5"));
    expect(selection()?.querySelector('[role="status"]')?.textContent).toBe(
      "已选 2 个模型",
    );
    expect(rowCheckbox("gpt-5.5").getAttribute("aria-checked")).toBe("true");
    expect(rowCheckbox("gemini-3.1-pro").getAttribute("aria-checked")).toBe(
      "false",
    );

    await press(button("指定格式", selection()!));
    expect(
      [...document.querySelectorAll('[role="menuitem"]')].map(textOf),
    ).toEqual(["按入口协议设置", "Responses", "Messages", "Chat", "Gemini"]);
    await chooseMenuItem("Chat");

    expect(protocolChanges).toHaveBeenCalledTimes(1);
    expect(protocolChanges).toHaveBeenCalledWith(
      ["claude-opus-5-5", "gpt-5.5"],
      "openai.chat",
    );
    // Applying ends selection mode and gives the actions back.
    expect(selection()).toBeNull();
    expect(headerActions().className).not.toContain("invisible");
    expect(document.activeElement).toBe(button("多选", headerActions()));
    expect(formatTag("claude-opus-5-5")).toBe("Chat 格式");
    expect(formatTag("gpt-5.5")).toBe("Chat 格式");
    expect(formatTag("gemini-3.1-pro")).toBe("Gemini 格式");
  });

  it("checks every model the search shows, collapsed groups included", async () => {
    const models = [
      ...Array.from({ length: 6 }, (_, index) => `claude-opus-4-${index}`),
      ...Array.from({ length: 6 }, (_, index) => `claude-sonnet-4-${index}`),
    ];
    await render("newapi", models);
    // A list this long opens with its groups collapsed.
    expect(
      container.querySelectorAll('[data-testid="service-model-row"]'),
    ).toHaveLength(0);

    await click(button("多选", headerActions()));
    const selectAll = [
      ...selection()!.querySelectorAll<HTMLElement>('[role="checkbox"]'),
    ].find((checkbox) => checkbox.parentElement?.textContent === "全选")!;
    await click(selectAll);
    expect(selection()?.querySelector('[role="status"]')?.textContent).toBe(
      "已选 12 个模型",
    );

    await press(button("指定格式", selection()!));
    await chooseMenuItem("Messages");
    expect(protocolChanges).toHaveBeenCalledWith(models, "anthropic.messages");

    // With a search, only the matching models count.
    const search = container.querySelector<HTMLInputElement>(
      'input[aria-label="搜索已配置模型"]',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(search, "sonnet");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await click(button("多选", headerActions()));
    await click(
      [...selection()!.querySelectorAll<HTMLElement>('[role="checkbox"]')].find(
        (checkbox) => checkbox.parentElement?.textContent === "全选",
      )!,
    );
    expect(selection()?.querySelector('[role="status"]')?.textContent).toBe(
      "已选 6 个模型",
    );
    await press(button("指定格式", selection()!));
    await chooseMenuItem("按入口协议设置");
    expect(protocolChanges).toHaveBeenLastCalledWith(
      models.filter((model) => model.includes("sonnet")),
      null,
    );
  });

  it("deletes the checked models after the confirm dialog", async () => {
    await render("newapi", ["claude-opus-5-5", "gpt-5.5", "gpt-5.5-mini"]);
    await click(button("多选", headerActions()));
    await click(rowCheckbox("gpt-5.5"));
    await click(rowCheckbox("gpt-5.5-mini"));
    await click(button("删除", selection()!));

    const dialog = document.querySelector<HTMLElement>('[role="alertdialog"]');
    expect(dialog?.textContent).toContain("删除选中的模型？");
    expect(dialog?.textContent).toContain("将从白名单彻底移除 2 个模型。");
    expect(removals).not.toHaveBeenCalled();

    await click(button("确认删除", dialog!));
    await settleClosedLayer();
    expect(removals).toHaveBeenCalledWith(["gpt-5.5", "gpt-5.5-mini"]);
    expect(selection()).toBeNull();
    expect(
      [
        ...container.querySelectorAll('[data-testid="service-model-row"] code'),
      ].map((code) => code.textContent),
    ).toEqual(["claude-opus-5-5"]);
  });

  it("keeps only deletion for a kind with one format", async () => {
    await render("codex_subscription", ["gpt-5.5", "gpt-5.5-mini"]);
    expect(
      container.querySelector('button[aria-label$="发给上游的格式"]'),
    ).toBeNull();

    await click(button("多选", headerActions()));
    expect(
      [...selection()!.querySelectorAll("button")].map(textOf),
    ).not.toContain("指定格式");
    await click(rowCheckbox("gpt-5.5-mini"));
    await click(button("删除", selection()!));
    await click(
      button(
        "确认删除",
        document.querySelector<HTMLElement>('[role="alertdialog"]')!,
      ),
    );
    await settleClosedLayer();
    expect(removals).toHaveBeenCalledWith(["gpt-5.5-mini"]);

    await click(button("多选", headerActions()));
    await click(button("取消", selection()!));
    expect(selection()).toBeNull();
    expect(protocolChanges).not.toHaveBeenCalled();
  });
});
