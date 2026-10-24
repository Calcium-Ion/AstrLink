// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getRoutingSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
  getClassifierSettings: vi.fn(),
  getService: vi.fn(),
  updateService: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({
  notify: { success: vi.fn(), error: vi.fn() },
}));

import { defaultClassifierSettings } from "./classifier-settings-model";
import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale } from "./i18n";
import type { ModelRoutingGroup } from "./model-routing-group-model";
import { ModelRoutingGroupsPanel } from "./ModelRoutingGroupsPanel";
import type { RoutableService } from "./service-model";
import type { ConversionEngineCapability } from "./core-model";
import { settleClosedLayer } from "./lib/test-layer-settle";

const services: RoutableService[] = [
  {
    id: "svc-deepseek",
    name: "DeepSeek",
    kind: "deepseek",
    enabled: true,
    models: ["deepseek-chat", "deepseek-reasoner"],
    capabilities: [],
  },
  {
    id: "svc-openai",
    name: "OpenAI",
    kind: "openai",
    enabled: true,
    models: ["gpt-4o", "gpt-5-mini"],
    capabilities: [],
  },
];

function codingGroup(
  overrides: Partial<ModelRoutingGroup> = {},
): ModelRoutingGroup {
  return {
    id: "coding-auto",
    enabled: true,
    members: [
      { id: "fast", service_id: "svc-deepseek", model: "deepseek-chat" },
      { id: "strong", service_id: "svc-openai", model: "gpt-5-mini" },
    ],
    default_member_id: "fast",
    rules: [
      {
        id: "complex",
        priority: 10,
        when: { field: "task_type", in: ["coding"] },
        target_member_id: "strong",
      },
    ],
    ...overrides,
  };
}

let stored: ModelRoutingGroup[] = [];

function settings() {
  return {
    default_failure_policy: defaultFailurePolicy(),
    allow_unmatched_failover: false,
    strategy: "retry_first" as const,
    max_attempts: 6,
    model_routing_groups: structuredClone(stored),
  };
}

function lastWrite(): ModelRoutingGroup[] {
  const calls = bridge.updateRoutingSettings.mock.calls;
  return calls[calls.length - 1][0].model_routing_groups;
}

describe("routing groups panel", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    stored = [codingGroup()];
    bridge.getRoutingSettings
      .mockReset()
      .mockImplementation(async () => settings());
    bridge.updateRoutingSettings
      .mockReset()
      .mockImplementation(
        async (patch: { model_routing_groups: ModelRoutingGroup[] }) => {
          stored = structuredClone(patch.model_routing_groups);
          return settings();
        },
      );
    bridge.getClassifierSettings
      .mockReset()
      .mockResolvedValue({ ...defaultClassifierSettings(), enabled: true });
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

  async function flush() {
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });
  }

  async function render(
    props: Partial<Parameters<typeof ModelRoutingGroupsPanel>[0]> = {},
  ) {
    const handlers = {
      onDirtyChange: vi.fn(),
      onDryRun: vi.fn(),
      onOpenClassifier: vi.fn(),
      onUnsavedChange: vi.fn(),
      onEditingChange: vi.fn(),
      onGroupIDChange: vi.fn(),
    };
    await act(async () =>
      root.render(
        <ModelRoutingGroupsPanel
          services={services}
          ready
          {...handlers}
          {...props}
        />,
      ),
    );
    await flush();
    return handlers;
  }

  function button(
    text: string,
    scope: ParentNode = document,
  ): HTMLButtonElement {
    const found = [...scope.querySelectorAll<HTMLButtonElement>("button")].find(
      (element) => element.textContent?.trim() === text,
    );
    if (!found) throw new Error(`Missing button: ${text}`);
    return found;
  }

  async function click(element: Element) {
    await act(async () => {
      (element as HTMLElement).click();
      await Promise.resolve();
    });
    await flush();
  }

  async function openMenu(trigger: Element) {
    await act(async () => {
      trigger.dispatchEvent(
        new PointerEvent("pointerdown", {
          bubbles: true,
          button: 0,
          pointerType: "mouse",
        }),
      );
      await Promise.resolve();
    });
  }

  async function chooseMenuItem(selector: string) {
    const item = document.querySelector<HTMLElement>(selector);
    if (!item) throw new Error(`Missing menu item: ${selector}`);
    await click(item);
  }

  /** Opens a target combobox by its accessible name and picks `service/model`. */
  async function chooseTarget(label: string, serviceId: string, model: string) {
    const input = document.querySelector<HTMLInputElement>(
      `input[role="combobox"][aria-label="${label}"]`,
    );
    if (!input) throw new Error(`Missing target select: ${label}`);
    await click(input);
    const option = [
      ...document.querySelectorAll<HTMLElement>('[role="option"]'),
    ].find(
      (element) =>
        element.getAttribute("data-option-value") ===
        `${serviceId}\u001f${model}`,
    );
    if (!option) throw new Error(`Missing option ${serviceId}/${model}`);
    await click(option);
  }

  async function openEditor(id = "coding-auto") {
    const name = container.querySelector<HTMLButtonElement>(
      `[aria-label="编辑 astrlink/${id}"]`,
    );
    if (!name) throw new Error(`Missing group ${id}`);
    await click(name);
  }

  function rows() {
    return [...container.querySelectorAll<HTMLElement>("[data-routing-rule]")];
  }

  it("lists each group with its rules in a sentence, models and default", async () => {
    stored = [
      codingGroup(),
      codingGroup({ id: "research", enabled: false, rules: [] }),
    ];
    await render();
    const row = container.querySelector('[data-routing-group="coding-auto"]')!;
    expect(row.textContent).toContain("astrlink/coding-auto");
    expect(row.textContent).toContain("任务类型 是 编码 → gpt-5-mini");
    expect(row.textContent).toContain("2 个模型");
    expect(row.textContent).toContain("deepseek-chat");
    expect(row.textContent).toContain("分类器");
    const off = container.querySelector('[data-routing-group="research"]')!;
    expect(off.textContent).toContain("已停用");
    expect(off.textContent).toContain("没有规则，全部使用默认模型");
  });

  it("shows the empty state with a way to create the first group", async () => {
    stored = [];
    await render();
    expect(container.textContent).toContain("还没有路由组");
    await click(
      button(
        "新建路由组",
        container.querySelector('[data-slot="empty-state"]')!,
      ),
    );
    expect(
      document.querySelector('[data-testid="routing-create-dialog"]'),
    ).not.toBeNull();
  });

  it("filters the list by name or model", async () => {
    stored = [codingGroup(), codingGroup({ id: "research" })];
    await render();
    const search = container.querySelector<HTMLInputElement>(
      'input[type="search"]',
    )!;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(search, "resea");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(
      container.querySelector('[data-routing-group="coding-auto"]'),
    ).toBeNull();
    expect(
      container.querySelector('[data-routing-group="research"]'),
    ).not.toBeNull();
    // The name as the list shows and copies it finds the group too.
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(search, "astrlink/research");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(
      container.querySelector('[data-routing-group="research"]'),
    ).not.toBeNull();
  });

  it("saves the enable switch right away, reading the latest settings first", async () => {
    await render();
    await click(
      container.querySelector(
        '[role="switch"][aria-label="启用 astrlink/coding-auto"]',
      )!,
    );
    expect(bridge.getRoutingSettings).toHaveBeenCalledTimes(2);
    expect(lastWrite()[0].enabled).toBe(false);
  });

  it("creates a group from a name and a default model, then opens it", async () => {
    stored = [codingGroup({ task_type_labels: ["general", "coding"] })];
    const handlers = await render();
    await click(button("新建路由组"));
    const dialog = document.querySelector(
      '[data-testid="routing-create-dialog"]',
    )!;
    const name = dialog.querySelector<HTMLInputElement>("input.font-mono")!;
    expect(name.value).toBe("group-2");
    expect(
      dialog.querySelector<HTMLButtonElement>("[data-routing-create]")!
        .disabled,
    ).toBe(true);
    await chooseTarget("默认模型", "svc-deepseek", "deepseek-reasoner");
    await click(dialog.querySelector("[data-routing-create]")!);
    expect(lastWrite()[1]).toEqual({
      id: "group-2",
      enabled: true,
      members: [
        {
          id: "deepseek-reasoner",
          service_id: "svc-deepseek",
          model: "deepseek-reasoner",
        },
      ],
      default_member_id: "deepseek-reasoner",
      rules: [],
      // A new group joins the shared label list the classifier tab writes.
      task_type_labels: ["general", "coding"],
    });
    expect(
      container.querySelector('[data-testid="routing-group-editor"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("还没有规则");
    expect(handlers.onEditingChange).toHaveBeenLastCalledWith(true);
  });

  it("refuses a taken or reserved name when creating", async () => {
    await render();
    await click(button("新建路由组"));
    const dialog = document.querySelector(
      '[data-testid="routing-create-dialog"]',
    )!;
    const name = dialog.querySelector<HTMLInputElement>("input.font-mono")!;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(name, "auto");
      name.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await chooseTarget("默认模型", "svc-deepseek", "deepseek-chat");
    await click(dialog.querySelector("[data-routing-create]")!);
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(dialog.textContent).toContain("这个名字是保留字");
  });

  it("shows rules as sentences ending in the fallback row", async () => {
    await render();
    await openEditor();
    const editor = container.querySelector(
      '[data-testid="routing-group-editor"]',
    )!;
    expect(editor.querySelector("h1")?.textContent).toBe(
      "astrlink/coding-auto",
    );
    expect(rows()).toHaveLength(1);
    expect(rows()[0].textContent).toContain("当");
    expect(
      rows()[0].querySelector('[data-slot="condition-chip"]')?.textContent,
    ).toBe("任务类型 是编码");
    const fallback = editor.querySelector("[data-routing-fallback]")!;
    expect(fallback.textContent).toContain("其他情况");
    expect(
      fallback.querySelector<HTMLInputElement>('input[role="combobox"]')!.value,
    ).toBe("deepseek-chat");
    expect(button("保存").disabled).toBe(true);
  });

  it("adds a rule from a common case and saves members derived from the models", async () => {
    const handlers = await render();
    await openEditor();
    await openMenu(container.querySelector("[data-routing-add-rule]")!);
    await chooseMenuItem('[data-routing-template="image"]');
    expect(rows()).toHaveLength(2);
    expect(rows()[1].textContent).toContain("含图片");
    // A new rule starts on the fallback model; pick another one for it.
    await chooseTarget("第 2 条规则使用的模型", "svc-openai", "gpt-4o");
    expect(handlers.onDirtyChange).toHaveBeenLastCalledWith(true);
    expect(handlers.onUnsavedChange).toHaveBeenLastCalledWith("coding-auto");
    await click(button("保存"));
    const saved = lastWrite()[0];
    expect(saved.rules).toEqual([
      {
        id: "complex",
        priority: 10,
        when: { field: "task_type", in: ["coding"] },
        target_member_id: "strong",
      },
      {
        id: "rule-2",
        priority: 20,
        when: { field: "has_image", equals_bool: true },
        target_member_id: "gpt-4o",
      },
    ]);
    expect(saved.members.map((member) => member.id)).toEqual([
      "fast",
      "strong",
      "gpt-4o",
    ]);
    expect(handlers.onDirtyChange).toHaveBeenLastCalledWith(false);
    expect(container.textContent).toContain("所有修改已保存");
  });

  it("drops members nothing points at any more when saving", async () => {
    await render();
    await openEditor();
    await chooseTarget(
      "第 1 条规则使用的模型",
      "svc-deepseek",
      "deepseek-chat",
    );
    await click(button("保存"));
    expect(lastWrite()[0].members).toEqual([
      { id: "fast", service_id: "svc-deepseek", model: "deepseek-chat" },
    ]);
  });

  it("edits a condition in its popover and removes it", async () => {
    await render();
    await openEditor();
    const chip = rows()[0].querySelector<HTMLButtonElement>(
      '[data-slot="condition-chip"]',
    )!;
    await click(chip);
    const architect = document.querySelector(
      '[data-routing-option="architect"] [role="checkbox"]',
    )!;
    await click(architect);
    expect(chip.textContent).toBe("任务类型 是编码、架构");
    // Switch to the negative wording.
    const noneOf = [
      ...document.querySelectorAll<HTMLButtonElement>(
        '[data-slot="segmented-control-item"]',
      ),
    ].find((item) => item.textContent === "不是其中之一")!;
    await click(noneOf);
    expect(chip.textContent).toBe("任务类型 不是编码、架构");
    await click(button("保存"));
    expect(lastWrite()[0].rules[0].when).toEqual({
      field: "task_type",
      not_in: ["coding", "architect"],
    });
  });

  it("adds a condition from the field picker and blocks saving until it is filled", async () => {
    await render();
    await openEditor();
    await openMenu(rows()[0].querySelector("[data-routing-add-condition]")!);
    await chooseMenuItem('[data-routing-field="literal_keyword"]');
    // The new condition's editor opens and stays open once the menu has
    // closed and would have handed focus back to its trigger.
    await settleClosedLayer();
    await settleClosedLayer();
    expect(
      document.querySelector('[data-slot="popover-content"]')?.textContent,
    ).toContain("包含关键词");
    const chips = rows()[0].querySelectorAll('[data-slot="condition-chip"]');
    expect(chips).toHaveLength(2);
    expect(chips[1].getAttribute("aria-invalid")).toBe("true");
    expect(button("保存").disabled).toBe(true);
    expect(container.textContent).toContain("还有 1 处条件需要修改");
  });

  it("reorders rules only from a rule's own drag", async () => {
    stored = [
      codingGroup({
        rules: [
          codingGroup().rules[0],
          {
            id: "images",
            priority: 20,
            when: { field: "has_image", equals_bool: true },
            target_member_id: "fast",
          },
        ],
      }),
    ];
    await render();
    await openEditor();
    const drag = async (target: Element, type: string) =>
      act(async () => {
        target.dispatchEvent(new Event(type, { bubbles: true }));
        await Promise.resolve();
      });
    // A drag released outside the list leaves nothing behind.
    await drag(
      container.querySelector('[data-routing-drag-handle="1"]')!,
      "dragstart",
    );
    await drag(
      container.querySelector('[data-routing-drag-handle="1"]')!,
      "dragend",
    );
    // Text or a file dropped on a row later must not move a rule.
    await drag(rows()[0], "dragover");
    await drag(rows()[0], "drop");
    expect(rows()[0].textContent).toContain("编码");
    expect(button("保存").disabled).toBe(true);
    // A real rule drag still moves it.
    await drag(
      container.querySelector('[data-routing-drag-handle="1"]')!,
      "dragstart",
    );
    await drag(rows()[0], "dragover");
    await drag(rows()[0], "drop");
    expect(rows()[0].textContent).toContain("含图片");
  });

  it("does not offer an unknown media kind, but keeps one a rule names", async () => {
    const mediaRule = (values: string[]) =>
      codingGroup({
        rules: [
          {
            id: "media",
            priority: 10,
            when: { field: "media_kind", in: values },
            target_member_id: "strong",
          },
        ],
      });
    stored = [mediaRule(["image"])];
    await render();
    await openEditor();
    await click(rows()[0].querySelector('[data-slot="condition-chip"]')!);
    expect(
      document.querySelector('[data-routing-option="image"]'),
    ).not.toBeNull();
    expect(
      document.querySelector('[data-routing-option="unknown"]'),
    ).toBeNull();

    await act(async () => root.unmount());
    root = createRoot(container);
    stored = [mediaRule(["unknown"])];
    await render();
    await openEditor();
    await click(rows()[0].querySelector('[data-slot="condition-chip"]')!);
    expect(
      document.querySelector('[data-routing-option="unknown"]'),
    ).not.toBeNull();
  });

  it("asks for a first condition on a blank rule", async () => {
    await render();
    await openEditor();
    await openMenu(container.querySelector("[data-routing-add-rule]")!);
    await chooseMenuItem('[data-routing-template="blank"]');
    expect(rows()[1].textContent).toContain("至少添加一个条件");
    expect(button("保存").disabled).toBe(true);
  });

  it("keeps a rule it cannot show as phrases exactly as stored", async () => {
    const legacy = {
      id: "old",
      priority: 20,
      when: {
        any: [
          { field: "has_image", equals_bool: true },
          { field: "has_video", equals_bool: true },
        ],
      },
      target_member_id: "strong",
    };
    stored = [codingGroup({ rules: [...codingGroup().rules, legacy] })];
    await render();
    await openEditor();
    expect(
      rows()[1].querySelector("[data-routing-legacy-rule]"),
    ).not.toBeNull();
    expect(rows()[1].textContent).toContain("重新设置条件");
    // Edit another row; the legacy rule travels untouched.
    await click(container.querySelector('[role="switch"][aria-label="启用"]')!);
    await click(button("保存"));
    expect(lastWrite()[0].rules[1]).toEqual(legacy);
  });

  it("deletes a rule without asking and can discard the change", async () => {
    await render();
    await openEditor();
    await click(container.querySelector('[data-routing-remove-rule="0"]')!);
    expect(rows()).toHaveLength(0);
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    await click(button("放弃更改"));
    expect(rows()).toHaveLength(1);
  });

  it("asks before leaving unsaved edits", async () => {
    const handlers = await render();
    await openEditor();
    await click(container.querySelector('[data-routing-remove-rule="0"]')!);
    await click(button("智能路由"));
    const confirm = document.querySelector('[role="alertdialog"]')!;
    expect(confirm.textContent).toContain("放弃未保存的修改？");
    await click(button("放弃并返回", confirm));
    expect(
      container.querySelector('[data-testid="routing-group-list"]'),
    ).not.toBeNull();
    expect(handlers.onDirtyChange).toHaveBeenLastCalledWith(false);
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
  });

  it("deletes the group after confirmation", async () => {
    stored = [codingGroup(), codingGroup({ id: "research" })];
    const handlers = await render();
    await openEditor();
    await click(button("删除路由组"));
    const confirm = document.querySelector('[role="alertdialog"]')!;
    expect(confirm.textContent).toContain("astrlink/coding-auto");
    await click(button("删除", confirm));
    expect(lastWrite().map((group) => group.id)).toEqual(["research"]);
    expect(
      container.querySelector('[data-testid="routing-group-list"]'),
    ).not.toBeNull();
    expect(handlers.onGroupIDChange).toHaveBeenCalledWith("coding-auto", null);
  });

  it("checks a rename before saving it", async () => {
    stored = [codingGroup(), codingGroup({ id: "research" })];
    const handlers = await render();
    await openEditor();
    await click(container.querySelector("[data-routing-rename]")!);
    const input = document.querySelector<HTMLInputElement>(
      '[data-slot="popover-content"] input',
    )!;
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!;
    await act(async () => {
      setter.call(input, "research");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(container.textContent).toContain("已有同名的路由组");
    expect(button("保存").disabled).toBe(true);
    await act(async () => {
      setter.call(input, "coding-v2");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await click(button("保存"));
    expect(lastWrite().map((group) => group.id)).toEqual([
      "coding-v2",
      "research",
    ]);
    expect(container.querySelector("h1")?.textContent).toBe(
      "astrlink/coding-v2",
    );
    expect(handlers.onGroupIDChange).toHaveBeenCalledWith(
      "coding-auto",
      "coding-v2",
    );
  });

  it("keeps task type labels another tab saved meanwhile", async () => {
    await render();
    await openEditor();
    await click(container.querySelector('[role="switch"][aria-label="启用"]')!);
    // The classifier tab rewrote the shared labels after this editor opened.
    stored = [
      codingGroup({ task_type_labels: ["general", "coding", "writing"] }),
    ];
    await click(button("保存"));
    expect(lastWrite()[0]).toMatchObject({
      enabled: false,
      task_type_labels: ["general", "coding", "writing"],
    });
  });

  it("warns when classifier conditions cannot apply and links to the classifier", async () => {
    bridge.getClassifierSettings.mockResolvedValue({
      ...defaultClassifierSettings(),
      enabled: false,
    });
    const handlers = await render();
    await openEditor();
    expect(container.textContent).toContain("分类器没有启用");
    await click(button("去开启"));
    expect(handlers.onOpenClassifier).toHaveBeenCalledTimes(1);
  });

  it("points out a model that refuses some request formats and opens its conversion", async () => {
    const all = ["anthropic.messages", "openai.chat", "openai.responses"].map(
      (protocol) => ({ protocol, mode: "native" as const, streaming: true }),
    );
    const claudeOnly = [
      {
        protocol: "anthropic.messages",
        mode: "native" as const,
        streaming: true,
      },
    ];
    const formatServices: RoutableService[] = [
      { ...services[0], capabilities: all },
      { ...services[1], name: "Claude 订阅", capabilities: claudeOnly },
    ];
    const engine: ConversionEngineCapability = {
      name: "relaykit",
      version: null,
      available: true,
      edges: ["openai.responses", "openai.chat"].map((from) => ({
        from,
        to: "anthropic.messages",
        quality: "good" as const,
        streaming: true,
      })),
    };
    bridge.getService.mockReset().mockResolvedValue({
      service: { ...formatServices[1], capabilities: claudeOnly },
      etag: "etag-1",
    });
    bridge.updateService
      .mockReset()
      .mockResolvedValue({ service: formatServices[1], etag: "etag-2" });
    const onServicesChanged = vi.fn();
    await render({
      services: formatServices,
      conversionEngine: engine,
      onServicesChanged,
      onOpenService: vi.fn(),
    });
    await openEditor();

    const notice = document.querySelector("[data-routing-format-notice]");
    expect(notice?.textContent).toContain("有模型不兼容部分请求格式");
    expect(
      notice?.querySelector('[data-routing-format-row="openai.responses"]')
        ?.textContent,
    ).toContain("gpt-5-mini · Claude 订阅 · 第 1 条");
    expect(
      notice?.querySelector('[data-routing-format-row="anthropic.messages"]')
        ?.textContent,
    ).toContain("全部兼容");
    // The rule's model says which requests it would fail, on hover.
    const warning = document.querySelector<HTMLElement>(
      '[data-routing-target="0"] [data-combobox-warning]',
    );
    expect(warning?.title).toContain("Claude 订阅 只接受 Anthropic Messages");
    expect(
      document.querySelector(
        '[data-routing-target="fallback"] [data-combobox-warning]',
      ),
    ).toBeNull();

    await click(
      document.querySelector("[data-routing-format-enable]") as Element,
    );
    expect(bridge.updateService).toHaveBeenCalledTimes(2);
    expect(bridge.updateService).toHaveBeenCalledWith("svc-openai", "etag-1", {
      capabilities: [
        ...claudeOnly,
        {
          protocol: "openai.responses",
          mode: "native",
          streaming: true,
          convert_to: "anthropic.messages",
        },
      ],
    });
    expect(onServicesChanged).toHaveBeenCalled();
  });

  it("hands the saved name to the dry run", async () => {
    const handlers = await render();
    await openEditor();
    await click(container.querySelector("[data-routing-dry-run]")!);
    expect(handlers.onDryRun).toHaveBeenCalledWith("coding-auto");
  });

  it("opens the group another tab asks to edit", async () => {
    await render();
    await act(async () =>
      root.render(
        <ModelRoutingGroupsPanel
          services={services}
          ready
          onDirtyChange={() => {}}
          editRequest={{ id: "coding-auto", seq: 1 }}
        />,
      ),
    );
    await flush();
    expect(
      container.querySelector('[data-testid="routing-group-editor"]'),
    ).not.toBeNull();
  });

  it("opens a requested group once the groups have loaded", async () => {
    // A link from a request record mounts the panel with the request already
    // set, before the first settings read has answered.
    let answer: (value: ReturnType<typeof settings>) => void = () => {};
    bridge.getRoutingSettings.mockReset().mockImplementation(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );
    await act(async () =>
      root.render(
        <ModelRoutingGroupsPanel
          services={services}
          ready
          onDirtyChange={() => {}}
          editRequest={{ id: "coding-auto", seq: 1 }}
        />,
      ),
    );
    await flush();
    expect(
      container.querySelector('[data-testid="routing-group-editor"]'),
    ).toBeNull();
    await act(async () => answer(settings()));
    await flush();
    expect(
      container.querySelector('[data-testid="routing-group-editor"]'),
    ).not.toBeNull();
  });

  it("puts its toolbar into the tabs row when given a slot", async () => {
    const slot = document.createElement("div");
    document.body.append(slot);
    await render({ actionsSlot: slot, active: true });
    expect(slot.textContent).toContain("新建路由组");
    await render({ actionsSlot: slot, active: false });
    expect(slot.textContent).toBe("");
  });
});
