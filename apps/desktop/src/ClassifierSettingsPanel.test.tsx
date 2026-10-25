// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getClassifierSettings: vi.fn(),
  updateClassifierSettings: vi.fn(),
  classifierCredentialAction: vi.fn(),
  getRoutingSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

import {
  classifierPromptProblem,
  classifierProtocolPresets,
  defaultClassifierPromptFor,
  defaultClassifierSettings,
  legacyDefaultClassifierPrompts,
  type ClassifierDimension,
  type ClassifierSettings,
} from "./classifier-settings-model";
import { settleClosedLayer } from "./lib/test-layer-settle";
import { ClassifierSettingsPanel } from "./ClassifierSettingsPanel";
import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale } from "./i18n";
import type { ModelRoutingGroup } from "./model-routing-group-model";
import type { RoutableService } from "./service-model";
import {
  useWorkspaceSnapshot,
  WorkspaceSnapshotProvider,
} from "./workspace-snapshots";

const services: RoutableService[] = [
  {
    id: "svc-deepseek",
    name: "DeepSeek",
    kind: "deepseek",
    enabled: true,
    models: ["deepseek-chat", "deepseek-reasoner"],
    capabilities: [],
  },
];

function routing(groups: ModelRoutingGroup[] = []) {
  return {
    default_failure_policy: defaultFailurePolicy(),
    allow_unmatched_failover: false,
    strategy: "retry_first" as const,
    max_attempts: 6,
    model_routing_groups: groups,
  };
}

function group(id: string, usedLabels: string[] = []): ModelRoutingGroup {
  return {
    id,
    enabled: true,
    members: [{ id: "a", service_id: "svc-deepseek", model: "deepseek-chat" }],
    default_member_id: "a",
    rules: usedLabels.length
      ? [
          {
            id: "r",
            priority: 10,
            target_member_id: "a",
            when: { field: "task_type", in: usedLabels },
          },
        ]
      : [],
  };
}

function settings(
  patch: Partial<ClassifierSettings> = {},
  dimensions: ClassifierDimension[] = [],
): ClassifierSettings {
  return { ...defaultClassifierSettings(), dimensions, ...patch };
}

/** Popovers render in a portal, so controls are looked up in the document. */
function input(label: string): HTMLInputElement {
  const match = document.querySelector<HTMLInputElement>(
    `input[aria-label="${label}"]`,
  );
  if (match === null) throw new Error(`Missing input: ${label}`);
  return match;
}

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (element) =>
      element.textContent?.trim() === label ||
      element.getAttribute("aria-label") === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

/** React tracks input values, so set through the native setter before dispatch. */
async function type(
  element: HTMLInputElement | HTMLTextAreaElement,
  value: string,
): Promise<void> {
  await act(async () => {
    const prototype =
      element instanceof HTMLTextAreaElement
        ? HTMLTextAreaElement.prototype
        : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, "value")?.set?.call(
      element,
      value,
    );
    element.dispatchEvent(new Event("input", { bubbles: true }));
    await Promise.resolve();
  });
}

/**
 * A pointer press, then the click. The press is what tells an open popover the
 * user interacted outside it; without it Radix treats the close as programmatic
 * and hands focus back to that popover's trigger, which a real click never does.
 */
async function click(element: HTMLElement): Promise<void> {
  await act(async () => {
    element.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    element.click();
    await Promise.resolve();
    await Promise.resolve();
  });
}

async function flush(): Promise<void> {
  await act(async () => {
    for (let index = 0; index < 6; index += 1) await Promise.resolve();
  });
}

/**
 * The editor of something just added opens a timer tick later, once a closing
 * popover has handed focus back (see the panel's `openAfterClose`).
 */
async function settle(): Promise<void> {
  await settleClosedLayer();
  await settleClosedLayer();
}

/** Opens the `?` with this name and returns what it explains. */
async function helpText(label: string): Promise<string> {
  await click(button(label));
  return (
    [...document.querySelectorAll('[data-slot="popover-content"]')].at(-1)
      ?.textContent ?? ""
  );
}

async function add(label: string): Promise<void> {
  await click(button(label));
  await settle();
}

describe("classifier settings panel", () => {
  let container: HTMLDivElement;
  let root: Root;
  let dirty: boolean[];

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    dirty = [];
    bridge.getClassifierSettings.mockReset().mockResolvedValue(settings());
    bridge.classifierCredentialAction
      .mockReset()
      .mockResolvedValue({ configured: false });
    bridge.updateClassifierSettings
      .mockReset()
      .mockImplementation(async (next: ClassifierSettings) => next);
    bridge.getRoutingSettings.mockReset().mockResolvedValue(routing());
    bridge.updateRoutingSettings
      .mockReset()
      .mockImplementation(async (patch) => ({ ...routing(), ...patch }));
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    document.body.innerHTML = "";
  });

  async function render(
    props: Partial<Parameters<typeof ClassifierSettingsPanel>[0]> = {},
  ): Promise<void> {
    await act(async () => {
      root.render(
        <ClassifierSettingsPanel
          ready
          services={services}
          onDirtyChange={(next) => dirty.push(next)}
          {...props}
        />,
      );
    });
    await flush();
  }

  async function save(): Promise<void> {
    await click(button("保存"));
    await flush();
  }

  async function enable(): Promise<void> {
    await click(container.querySelector<HTMLButtonElement>('[role="switch"]')!);
  }

  async function chooseSource(label: string): Promise<void> {
    await click(
      container.querySelector<HTMLButtonElement>(
        `[role="radio"][aria-label="${label}"]`,
      )!,
    );
  }

  describe("where the classifier runs", () => {
    it("loads the settings, the credential status and the groups together", async () => {
      await render();

      expect(bridge.getClassifierSettings).toHaveBeenCalled();
      expect(bridge.classifierCredentialAction).toHaveBeenCalledWith("status");
      expect(bridge.getRoutingSettings).toHaveBeenCalled();
      expect(input("模型名").value).toBe(classifierProtocolPresets.jev.model);
      // Loading is not an edit.
      expect(dirty.at(-1)).toBe(false);
      expect(container.textContent).toContain("所有修改已保存");
    });

    it("shows only the fields of the chosen source", async () => {
      await render();
      expect(
        container.querySelector('[data-classifier-source-fields="inline"]'),
      ).not.toBeNull();
      expect(
        container.querySelector('input[aria-label="分类模型"]'),
      ).toBeNull();

      await chooseSource("复用已接入的模型");

      expect(
        container.querySelector('[data-classifier-source-fields="inline"]'),
      ).toBeNull();
      expect(container.querySelector('input[aria-label="端点"]')).toBeNull();
      expect(container.querySelector('input[aria-label="API Key"]')).toBeNull();
      expect(
        container.querySelector('input[aria-label="分类模型"]'),
      ).not.toBeNull();
    });

    it("saves a reused provider model picked from one list", async () => {
      await render();
      await enable();
      await chooseSource("复用已接入的模型");
      await click(input("分类模型"));
      await click(
        document.querySelector<HTMLElement>(
          '[data-option-value="svc-deepseek\u001fdeepseek-chat"]',
        )!,
      );
      expect(input("分类模型").value).toBe("deepseek-chat");

      await save();

      expect(bridge.updateClassifierSettings).toHaveBeenCalledTimes(1);
      const submitted = bridge.updateClassifierSettings.mock
        .calls[0][0] as ClassifierSettings;
      expect(submitted.classifier_source).toBe("existing_model");
      expect(submitted.existing_model).toMatchObject({
        service_id: "svc-deepseek",
        model: "deepseek-chat",
      });
      // Reusing a provider needs no key of its own.
      expect(bridge.classifierCredentialAction).not.toHaveBeenCalledWith(
        "save_key",
        expect.anything(),
      );
    });

    it("blocks an enabled reuse with no model picked", async () => {
      await render();
      await enable();
      await chooseSource("复用已接入的模型");

      await save();

      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
      expect(container.textContent).toContain("此项不能为空。");
    });

    it("leaves user input alone when the protocol switches", async () => {
      await render();
      await type(input("端点"), "https://gateway.internal/jev");

      await click(button("OpenAI Decisions"));

      expect(input("端点").value).toBe("https://gateway.internal/jev");
      expect(input("模型名").value).toBe(
        classifierProtocolPresets.openai_decisions.model,
      );
    });

    it("blocks the save and marks the endpoint when it embeds credentials", async () => {
      await render();
      // Enable first: the disabled classifier saves an incomplete draft on purpose.
      await enable();
      await type(input("端点"), "https://user:pass@api.typesafe.ai");

      await save();

      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
      expect(input("端点").getAttribute("aria-invalid")).toBe("true");
      expect(container.textContent).toContain(
        "端点不得内含凭据（user:pass@）。",
      );
    });

    it("blocks an enabled inline classifier that has no key", async () => {
      await render();
      await enable();

      await save();

      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
      expect(input("API Key").getAttribute("aria-invalid")).toBe("true");
    });

    it("stores the key through the credential endpoint before the settings", async () => {
      await render();
      await enable();
      await type(input("API Key"), "sk-secret-value");

      await save();

      expect(bridge.classifierCredentialAction).toHaveBeenCalledWith(
        "save_key",
        "sk-secret-value",
      );
      const keyCall = bridge.classifierCredentialAction.mock.calls.findIndex(
        ([action]) => action === "save_key",
      );
      expect(
        bridge.classifierCredentialAction.mock.invocationCallOrder[keyCall],
      ).toBeLessThan(
        bridge.updateClassifierSettings.mock.invocationCallOrder[0],
      );
      const submitted = bridge.updateClassifierSettings.mock.calls[0][0];
      expect(JSON.stringify(submitted)).not.toContain("sk-secret-value");
      expect(input("API Key").value).toBe("");
    });

    it("does not re-send a key that is already configured", async () => {
      bridge.classifierCredentialAction.mockResolvedValue({ configured: true });
      await render();
      await enable();

      await save();

      expect(bridge.updateClassifierSettings).toHaveBeenCalled();
      expect(
        bridge.classifierCredentialAction.mock.calls.filter(
          ([action]) => action === "save_key",
        ),
      ).toEqual([]);
    });

    it("clears the drafted key after deleting it so it cannot come back", async () => {
      bridge.classifierCredentialAction.mockImplementation(
        async (action: "status" | "save_key" | "delete_key") =>
          action === "status" ? { configured: true } : { configured: false },
      );
      await render();
      await type(input("API Key"), "sk-still-drafted");

      await click(button("删除密钥"));
      await flush();

      expect(bridge.classifierCredentialAction).toHaveBeenCalledWith(
        "delete_key",
      );
      expect(input("API Key").value).toBe("");
      expect(button("保存").disabled).toBe(true);
    });

    it("treats a saved key as configured even when the settings save fails", async () => {
      bridge.updateClassifierSettings.mockRejectedValue(
        new Error("classifier settings rejected"),
      );
      await render();
      await enable();
      await type(input("API Key"), "sk-half-saved");
      let reply = { configured: false };
      bridge.classifierCredentialAction.mockImplementation(
        async (action: "status" | "save_key" | "delete_key") => {
          if (action === "save_key") reply = { configured: true };
          return reply;
        },
      );

      await save();

      expect(container.textContent).toContain("已配置密钥");
      expect(container.textContent).toContain("分类器配置未保存。");
      expect(input("API Key").getAttribute("aria-invalid")).toBe("false");
    });
  });

  describe("classification dimensions", () => {
    it("adds a dimension, names it and its option, and saves the list", async () => {
      await render();
      await add("添加维度");
      // The new dimension opens its editor straight away.
      await type(input("维度名"), "topic");
      await type(input("维度说明"), "用户问题所属的业务主题");
      // The editor shows how the prompt references each part of it.
      for (const token of [
        "{{dim.topic.name}}",
        "{{dim.topic.description}}",
        "{{dim.topic.options}}",
      ])
        expect(document.body.textContent).toContain(token);

      await add("给「topic」添加选项");
      await type(input("选项"), "billing");
      await type(input("说明"), "账单、发票、付款");
      expect(dirty.at(-1)).toBe(true);

      await save();

      expect(bridge.updateClassifierSettings).toHaveBeenCalledTimes(1);
      const submitted = bridge.updateClassifierSettings.mock
        .calls[0][0] as ClassifierSettings;
      expect(submitted.dimensions).toEqual([
        {
          name: "topic",
          description: "用户问题所属的业务主题",
          options: [{ name: "billing", description: "账单、发票、付款" }],
        },
      ]);
      // The rest of the singleton travels with the save untouched; the
      // default prompt is stored empty so it keeps following the built-in one.
      expect(submitted.inline).toEqual({
        ...defaultClassifierSettings().inline,
        prompt: "",
      });
      // Labels were not touched, so the groups are not rewritten.
      expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    });

    it("refuses a dimension without options", async () => {
      await render();
      await add("添加维度");
      await type(input("维度名"), "topic");

      await save();

      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
      expect(container.textContent).toContain("请至少添加一个选项");
    });

    it("refuses the reserved task_type name and a duplicate name", async () => {
      bridge.getClassifierSettings.mockResolvedValue(
        settings({}, [{ name: "topic", options: [{ name: "a" }] }]),
      );
      await render();
      await add("添加维度");
      await type(input("维度名"), "task_type");
      await add("给「task_type」添加选项");
      await type(input("选项"), "x");

      await save();

      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
      expect(container.textContent).toContain("task_type 是内置维度");

      await click(button("编辑维度 task_type"));
      await type(input("维度名"), "topic");
      await save();
      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
      expect(container.textContent).toContain("维度名重复");
    });

    it("removes an option and a whole dimension from their editors", async () => {
      bridge.getClassifierSettings.mockResolvedValue(
        settings({}, [
          {
            name: "topic",
            description: "",
            options: [
              { name: "billing", description: "" },
              { name: "tech", description: "" },
            ],
          },
          { name: "tone", description: "", options: [{ name: "calm" }] },
        ]),
      );
      await render();

      await click(button("编辑选项 billing"));
      await click(button("删除选项"));
      expect(
        container.querySelector('[data-classifier-option="billing"]'),
      ).toBeNull();
      // Radix hands focus back a timer tick after a popover closes; opening the
      // next one before that would let the late focus dismiss it.
      await settle();

      await click(button("编辑维度 tone"));
      await click(button("删除维度"));
      await save();

      const submitted = bridge.updateClassifierSettings.mock
        .calls[0][0] as ClassifierSettings;
      expect(submitted.dimensions).toEqual([
        {
          name: "topic",
          description: "",
          options: [{ name: "tech", description: "" }],
        },
      ]);
    });

    it("refuses to remove a dimension a rule still uses", async () => {
      bridge.getClassifierSettings.mockResolvedValue(
        settings({}, [
          { name: "topic", options: [{ name: "legal" }] },
          { name: "tone", options: [{ name: "calm" }] },
        ]),
      );
      bridge.getRoutingSettings.mockResolvedValue(
        routing([
          {
            ...group("g1"),
            rules: [
              {
                id: "r",
                priority: 10,
                target_member_id: "a",
                when: { not: { field: "topic", in: ["legal"] } },
              },
            ],
          },
        ]),
      );
      await render();

      await click(button("编辑维度 topic"));
      await click(button("删除维度"));
      expect(container.textContent).toContain(
        "路由组 g1 的规则还在用维度 topic",
      );
      await save();
      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();

      // A dimension no rule names still goes.
      await settle();
      await click(button("编辑维度 tone"));
      await click(button("删除维度"));
      expect(container.textContent).toContain(
        "路由组 g1 的规则还在用维度 topic",
      );
    });

    it("refuses to rename an option a rule still uses", async () => {
      bridge.getClassifierSettings.mockResolvedValue(
        settings({}, [
          { name: "topic", options: [{ name: "legal" }, { name: "other" }] },
        ]),
      );
      bridge.getRoutingSettings.mockResolvedValue(
        routing([
          {
            ...group("g1"),
            rules: [
              {
                id: "r",
                priority: 10,
                target_member_id: "a",
                when: { field: "topic", in: ["legal"] },
              },
            ],
          },
        ]),
      );
      await render();

      await click(button("编辑选项 legal"));
      await type(input("选项"), "law");
      expect(container.textContent).toContain(
        "路由组 g1 的规则还在用维度 topic 的选项 legal",
      );
      await save();
      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
    });

    it("stops offering new dimensions at five custom ones", async () => {
      bridge.getClassifierSettings.mockResolvedValue(
        settings(
          {},
          Array.from({ length: 5 }, (_, index) => ({
            name: `dim_${index}`,
            options: [{ name: "a" }],
          })),
        ),
      );
      await render();

      expect(button("添加维度").disabled).toBe(true);
      expect(container.textContent).toContain("自定义 5 / 5");
      expect(container.textContent).not.toContain("最多 5 个自定义维度");
      expect(await helpText("自定义维度的上限")).toContain(
        "最多 5 个自定义维度",
      );
    });
  });

  describe("task type options", () => {
    it("adds an option and writes the list into every group", async () => {
      const stored = routing([group("g1", ["coding"]), group("g2")]);
      bridge.getRoutingSettings.mockResolvedValue(stored);
      await render();

      await add("给「任务类型」添加选项");
      await type(input("选项"), "writing");
      expect(dirty.at(-1)).toBe(true);

      // A group saved elsewhere since this tab loaded must survive the write.
      const latest = routing([
        group("g1", ["coding"]),
        group("g2"),
        group("g3"),
      ]);
      bridge.getRoutingSettings.mockResolvedValue(latest);
      await save();

      expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
      expect(bridge.updateRoutingSettings).toHaveBeenCalledTimes(1);
      const [patch] = bridge.updateRoutingSettings.mock.calls[0] as [
        { model_routing_groups: ModelRoutingGroup[] },
      ];
      const expected = [
        "general",
        "research",
        "coding",
        "architect",
        "writing",
      ];
      expect(
        patch.model_routing_groups.map((item) => [
          item.id,
          item.task_type_labels,
        ]),
      ).toEqual([
        ["g1", expected],
        ["g2", expected],
        ["g3", expected],
      ]);
      // Rules travel back untouched.
      expect(patch.model_routing_groups[0].rules).toEqual(
        stored.model_routing_groups[0].rules,
      );
    });

    it("refuses to drop an option a rule still uses", async () => {
      bridge.getRoutingSettings.mockResolvedValue(
        routing([group("g1", ["coding"])]),
      );
      await render();

      await click(button("编辑选项 编码"));
      await click(button("删除选项"));
      expect(container.textContent).toContain("路由组 g1 的规则还在用 coding");

      await save();

      expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    });

    it("cannot edit the options before a routing group exists", async () => {
      await render();

      expect(button("给「任务类型」添加选项").disabled).toBe(true);
      expect(await helpText("关于任务类型的选项")).toContain(
        "请先新建一个路由组",
      );
    });

    it("says when the groups disagree on their options", async () => {
      bridge.getRoutingSettings.mockResolvedValue(
        routing([
          { ...group("g1"), task_type_labels: ["general", "writing"] },
          group("g2"),
        ]),
      );
      await render();

      expect(
        container.querySelector('[data-classifier-option="writing"]'),
      ).not.toBeNull();
      expect(await helpText("关于任务类型的选项")).toContain("目前不一致");
    });
  });

  describe("advanced settings", () => {
    it("pre-fills the built-in prompt and restores it on request", async () => {
      await render();
      const prompt = () =>
        container.querySelector<HTMLTextAreaElement>(
          'textarea[aria-label="分类器提示词"]',
        )!;
      expect(prompt().value).toBe(defaultClassifierPromptFor([]));
      expect(container.textContent).toContain("提示词使用默认");
      expect(button("恢复默认提示词").disabled).toBe(true);

      await type(prompt(), "Pick one of {{labels}}");
      expect(container.textContent).toContain("提示词已自定义");
      await click(button("恢复默认提示词"));

      expect(prompt().value).toBe(defaultClassifierPromptFor([]));
      expect(dirty.at(-1)).toBe(false);
    });

    it("shows a stored copy of the previous default as the default, and saves the default as empty", async () => {
      bridge.getClassifierSettings.mockResolvedValue(
        settings({
          inline: {
            ...defaultClassifierSettings().inline,
            prompt: legacyDefaultClassifierPrompts[0],
          },
        }),
      );
      await render();
      const prompt = container.querySelector<HTMLTextAreaElement>(
        'textarea[aria-label="分类器提示词"]',
      )!;
      expect(prompt.value).toBe(defaultClassifierPromptFor([]));
      expect(container.textContent).toContain("提示词使用默认");
      expect(dirty.at(-1)).toBe(false);

      await type(input("低置信阈值"), "0.6");
      await save();

      const submitted = bridge.updateClassifierSettings.mock
        .calls[0][0] as ClassifierSettings;
      expect(submitted.inline.prompt).toBe("");
      expect(submitted.inline.low_confidence_threshold).toBe(0.6);
      expect(prompt.value).toBe(defaultClassifierPromptFor([]));
      expect(dirty.at(-1)).toBe(false);
    });

    it("inserts a placeholder for any part of any dimension where the caret is", async () => {
      bridge.getClassifierSettings.mockResolvedValue(
        settings({}, [
          {
            name: "topic",
            description: "编程任务主题",
            options: [{ name: "前端" }, { name: "后端" }],
          },
        ]),
      );
      await render();
      const prompt = () =>
        container.querySelector<HTMLTextAreaElement>(
          'textarea[aria-label="分类器提示词"]',
        )!;
      await type(prompt(), "Pick  now");
      await act(async () => {
        prompt().focus();
        prompt().setSelectionRange(5, 5);
        prompt().dispatchEvent(
          new KeyboardEvent("keyup", { key: "ArrowLeft", bubbles: true }),
        );
        await Promise.resolve();
      });

      await click(
        container.querySelector<HTMLButtonElement>(
          "[data-classifier-insert-placeholder]",
        )!,
      );
      expect(
        [
          ...document.querySelectorAll(
            "[data-classifier-placeholder-dimension]",
          ),
        ].map((item) => item.textContent),
      ).toEqual(["任务类型", "topic"]);
      await click(
        document.querySelector<HTMLElement>(
          '[data-classifier-placeholder-dimension="topic"]',
        )!,
      );
      expect(
        [...document.querySelectorAll("[data-classifier-placeholder]")].map(
          (item) => item.getAttribute("data-classifier-placeholder"),
        ),
      ).toEqual([
        "{{dim.topic.name}}",
        "{{dim.topic.description}}",
        "{{dim.topic.options}}",
        "{{dim.topic}}",
      ]);
      await click(
        document.querySelector<HTMLElement>(
          '[data-classifier-placeholder="{{dim.topic.options}}"]',
        )!,
      );
      await settleClosedLayer();

      expect(prompt().value).toBe("Pick {{dim.topic.options}} now");
      expect(document.activeElement).toBe(prompt());
      expect(prompt().selectionStart).toBe("Pick {{dim.topic.options}}".length);
    });

    it("writes every dimension into the default prompt and keeps it following them", async () => {
      await render();
      const prompt = () =>
        container.querySelector<HTMLTextAreaElement>(
          'textarea[aria-label="分类器提示词"]',
        )!;
      expect(prompt().value).toBe(defaultClassifierPromptFor([]));

      await add("添加维度");
      await type(input("维度名"), "topic");
      expect(prompt().value).toBe(
        defaultClassifierPromptFor([{ name: "topic", options: [] }]),
      );
      expect(prompt().value).toContain(
        "{{dim.topic.name}}: {{dim.topic.description}}\nOptions: {{dim.topic.options}}",
      );
      expect(container.textContent).toContain("提示词使用默认");

      // A prompt the user wrote stays as written when the dimensions change.
      await type(prompt(), "My own prompt");
      await type(input("维度名"), "subject");
      expect(prompt().value).toBe("My own prompt");
      expect(container.textContent).toContain("提示词已自定义");
    });

    it("appends a placeholder after a space when the prompt has no caret yet", async () => {
      await render();
      const prompt = container.querySelector<HTMLTextAreaElement>(
        'textarea[aria-label="分类器提示词"]',
      )!;
      await click(
        container.querySelector<HTMLButtonElement>(
          "[data-classifier-insert-placeholder]",
        )!,
      );
      await click(
        document.querySelector<HTMLElement>(
          '[data-classifier-placeholder-dimension="task_type"]',
        )!,
      );
      await click(
        document.querySelector<HTMLElement>(
          '[data-classifier-placeholder="{{dim.task_type.description}}"]',
        )!,
      );
      await settleClosedLayer();

      expect(prompt.value).toBe(
        `${defaultClassifierPromptFor([])} {{dim.task_type.description}}`,
      );
    });

    it("accepts a {{dim.<name>}} placeholder in the prompt", () => {
      expect(classifierPromptProblem("Pick {{dim.topic}} and {{labels}}")).toBe(
        null,
      );
      expect(
        classifierPromptProblem(
          "{{dim.task_type.name}} from {{dim.task_type.options}}; {{dim.topic.description}}",
        ),
      ).toBe(null);
      expect(classifierPromptProblem("Pick {{bogus}}")).toBe(
        "unknownPlaceholder",
      );
    });

    it("reads placeholders exactly as Core does", () => {
      // Only the name inside {{dim.…}} is trimmed; the rest is exact.
      expect(classifierPromptProblem("Pick {{dim. topic }}")).toBe(null);
      for (const prompt of [
        "Pick {{ labels }}",
        "Pick {{ dim.topic }}",
        "Pick {{dim.}}",
        "Pick {{a}b}}",
      ]) {
        expect(classifierPromptProblem(prompt), prompt).toBe(
          "unknownPlaceholder",
        );
      }
    });

    it("edits the timeout budget of the chosen source", async () => {
      await render();
      await type(input("超时（毫秒）"), "2500");
      await chooseSource("复用已接入的模型");
      expect(input("超时（毫秒）").value).toBe("15000");
      await type(input("超时（毫秒）"), "30000");
      await type(input("低置信阈值"), "0.7");

      await save();

      const submitted = bridge.updateClassifierSettings.mock
        .calls[0][0] as ClassifierSettings;
      expect(submitted.inline.timeout_ms).toBe(2500);
      expect(submitted.existing_model.timeout_ms).toBe(30000);
      expect(submitted.inline.low_confidence_threshold).toBe(0.7);
    });
  });

  describe("integration with the page", () => {
    it("puts the save status and button into the tab row slot while active", async () => {
      const slot = document.createElement("div");
      document.body.append(slot);
      await render({ actionsSlot: slot, active: true });

      expect(slot.querySelector("[data-classifier-save]")).not.toBeNull();
      expect(container.querySelector("[data-classifier-save]")).toBeNull();

      await render({ actionsSlot: slot, active: false });
      expect(slot.querySelector("[data-classifier-save]")).toBeNull();
      expect(container.querySelector("[data-classifier-save]")).toBeNull();
    });

    it("shares the saved settings and dimensions with the routing editor", async () => {
      const seen: { enabled?: boolean; dimensions: string[] }[] = [];
      function Probe() {
        const [saved] = useWorkspaceSnapshot<ClassifierSettings | null>(
          "classifier-settings",
          null,
        );
        const [dimensions] = useWorkspaceSnapshot<ClassifierDimension[]>(
          "classifier-dimensions",
          [],
        );
        seen.push({
          enabled: saved?.enabled,
          dimensions: dimensions.map((item) => item.name),
        });
        return null;
      }
      await act(async () => {
        root.render(
          <WorkspaceSnapshotProvider sessionKey="test">
            <ClassifierSettingsPanel
              ready
              services={services}
              onDirtyChange={() => {}}
            />
            <Probe />
          </WorkspaceSnapshotProvider>,
        );
      });
      await flush();
      expect(seen.at(-1)).toEqual({ enabled: false, dimensions: [] });

      await add("添加维度");
      await type(input("维度名"), "topic");
      await add("给「topic」添加选项");
      await type(input("选项"), "billing");
      // A draft is not shared until it is saved.
      expect(seen.at(-1)?.dimensions).toEqual([]);
      await save();

      expect(seen.at(-1)?.dimensions).toEqual(["topic"]);
    });
  });
});
