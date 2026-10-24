// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  classifierDryRun: vi.fn(),
  getRoutingSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

import { ClassifierDryRunPanel } from "./ClassifierDryRunPanel";
import type { ClassifierDryRunResult } from "./classifier-settings-model";
import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale } from "./i18n";
import type { ModelRoutingGroup } from "./model-routing-group-model";
import type { RoutableService } from "./service-model";

const services: RoutableService[] = [
  {
    id: "svc-deepseek",
    name: "DeepSeek",
    kind: "deepseek",
    enabled: true,
    models: ["deepseek-chat"],
    capabilities: [],
  },
  {
    id: "svc-claude",
    name: "Claude 订阅",
    kind: "claude_subscription",
    enabled: true,
    models: ["claude-sonnet-4-5"],
    capabilities: [],
  },
];

const codingGroup: ModelRoutingGroup = {
  id: "coding-auto",
  enabled: true,
  members: [
    { id: "fast", service_id: "svc-deepseek", model: "deepseek-chat" },
    { id: "vision", service_id: "svc-openai", model: "gpt-4o" },
    { id: "strong", service_id: "svc-claude", model: "claude-sonnet-4-5" },
    { id: "long", service_id: "svc-qwen", model: "qwen3-coder-plus" },
  ],
  default_member_id: "fast",
  rules: [
    {
      id: "night",
      priority: 10,
      when: {
        all: [
          { field: "system_time", within: ["22:00", "08:00"] },
          { field: "has_image", equals_bool: false },
        ],
      },
      target_member_id: "fast",
    },
    {
      id: "image",
      priority: 20,
      when: { field: "has_image", equals_bool: true },
      target_member_id: "vision",
    },
    {
      id: "coding",
      priority: 30,
      when: { field: "task_type", in: ["coding", "architect"] },
      target_member_id: "strong",
    },
    {
      id: "long",
      priority: 40,
      when: { field: "estimated_input_context_tokens", gte: 64000 },
      target_member_id: "long",
    },
  ],
};

const disabledGroup: ModelRoutingGroup = {
  id: "research",
  enabled: false,
  members: [{ id: "deep", service_id: "svc-deepseek", model: "deepseek-chat" }],
  default_member_id: "deep",
  rules: [],
};

function routingSettings(groups: ModelRoutingGroup[]) {
  return {
    default_failure_policy: defaultFailurePolicy(),
    allow_unmatched_failover: false,
    strategy: "retry_first" as const,
    max_attempts: 6,
    model_routing_groups: groups,
  };
}

function field(name: string, known: boolean, skip: string | null = null) {
  return {
    field: name,
    source: "",
    known,
    match_count: 0,
    value: null,
    classifier_confidence: null,
    classifier_id: null,
    skip_reason: skip,
    error_code: null,
    candidates: [],
  };
}

function classified(
  overrides: Partial<ClassifierDryRunResult> = {},
): ClassifierDryRunResult {
  return {
    configured: true,
    category: "coding",
    confidence: 0.9,
    fallback_reason: null,
    duration_ms: 42,
    timeout_ms: 1000,
    sanitized_preview: "write a parser for [EMAIL_1]",
    instruction: null,
    decision: null,
    // 桥接层用 parseClassifierDryRunResult 解析响应，所以这些键在这里总是数组。
    fields: [],
    rules: [],
    dimensions: [],
    ...overrides,
  };
}

/** The run against `codingGroup`: rule 3 wins, rule 2 cannot be judged. */
function routedResult(): ClassifierDryRunResult {
  return classified({
    confidence: 0.86,
    duration_ms: 812,
    decision: {
      member_id: "strong",
      service_id: "svc-claude",
      model: "claude-sonnet-4-5",
      rule_id: "coding",
      used_default: false,
    },
    rules: [
      {
        rule_id: "night",
        priority: 10,
        target_member_id: "fast",
        target_model: "deepseek-chat",
        state: "false",
        selected: false,
        target_missing: false,
        fields: [field("system_time", true)],
      },
      {
        rule_id: "image",
        priority: 20,
        target_member_id: "vision",
        target_model: "gpt-4o",
        state: "unknown",
        selected: false,
        target_missing: false,
        fields: [field("has_image", false, "unknown")],
      },
      {
        rule_id: "coding",
        priority: 30,
        target_member_id: "strong",
        target_model: "claude-sonnet-4-5",
        state: "true",
        selected: true,
        target_missing: false,
        fields: [field("task_type", true)],
      },
      {
        rule_id: "long",
        priority: 40,
        target_member_id: "long",
        target_model: "qwen3-coder-plus",
        state: "not_evaluated",
        selected: false,
        target_missing: false,
        fields: [],
      },
    ],
  });
}

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (element) => element.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement))
    throw new Error(`Missing button: ${label}`);
  return match;
}

async function type(
  element: HTMLTextAreaElement | HTMLInputElement,
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

async function chooseOption(selector: string, option: string): Promise<void> {
  const trigger = document.querySelector<HTMLButtonElement>(selector);
  if (trigger === null) throw new Error(`Missing select trigger: ${selector}`);
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
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => candidate.textContent?.trim() === option);
  if (!item) throw new Error(`Missing select option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

async function flush(): Promise<void> {
  await act(async () => {
    for (let index = 0; index < 5; index += 1) await Promise.resolve();
  });
}

describe("dry run tab", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.classifierDryRun.mockReset().mockResolvedValue(classified());
    bridge.getRoutingSettings
      .mockReset()
      .mockResolvedValue(routingSettings([]));
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
    props: Partial<Parameters<typeof ClassifierDryRunPanel>[0]> = {},
  ): Promise<void> {
    await act(async () => {
      root.render(
        <ClassifierDryRunPanel ready services={services} {...props} />,
      );
    });
    await flush();
  }

  function textarea(): HTMLTextAreaElement {
    const element = container.querySelector<HTMLTextAreaElement>("textarea");
    if (element === null) throw new Error("Missing textarea");
    return element;
  }

  async function runOnce(text = "write a parser"): Promise<void> {
    await type(textarea(), text);
    await act(async () => {
      button("开始测试").click();
    });
    await flush();
  }

  it("does not call the classifier until the test starts", async () => {
    await render();
    expect(bridge.classifierDryRun).not.toHaveBeenCalled();
    // Opens on a sample so the first run needs no typing.
    expect(textarea().value).toContain("React");
    expect(container.textContent).toContain("还没有试运行");
  });

  it("keeps the button disabled while the text is empty", async () => {
    await render();
    await type(textarea(), "   ");
    expect(button("开始测试").disabled).toBe(true);
  });

  it("shows only the classifier's answers when no group is chosen", async () => {
    await render();
    await runOnce();

    expect(bridge.classifierDryRun).toHaveBeenCalledWith({
      text: "write a parser",
    });
    const answers = container.querySelector<HTMLElement>(
      "[data-classifier-dry-run-answers]",
    );
    expect(answers?.textContent).toContain("编码");
    expect(answers?.textContent).toContain("coding");
    expect(answers?.textContent).toContain("90%");
    expect(answers?.textContent).toContain("可用");
    expect(container.textContent).toContain("[EMAIL_1]");
    expect(
      container.querySelector("[data-classifier-dry-run-no-group]")
        ?.textContent,
    ).toContain("没有选路由组");
    expect(
      container.querySelector("[data-classifier-dry-run-rules]"),
    ).toBeNull();
    expect(
      container.querySelector("[data-classifier-dry-run-outcome]"),
    ).toBeNull();
  });

  it("opens on the first enabled group and reports it", async () => {
    bridge.getRoutingSettings.mockResolvedValue(
      routingSettings([disabledGroup, codingGroup]),
    );
    const onGroupChange = vi.fn();
    await render({ onGroupChange });

    expect(onGroupChange).toHaveBeenCalledWith("coding-auto");
    expect(
      container.querySelector("[data-dry-run-group]")?.textContent,
    ).toContain("astrlink/coding-auto");
  });

  it("leaves a group chosen by the editor alone", async () => {
    bridge.getRoutingSettings.mockResolvedValue(
      routingSettings([codingGroup, { ...disabledGroup, enabled: true }]),
    );
    const onGroupChange = vi.fn();
    await render({ group: "research", onGroupChange });

    expect(onGroupChange).not.toHaveBeenCalled();
    expect(
      container.querySelector("[data-dry-run-group]")?.textContent,
    ).toContain("astrlink/research");
  });

  it("puts the outcome first and lists every rule's verdict", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    bridge.classifierDryRun.mockResolvedValue(routedResult());
    await render({ group: "coding-auto" });
    await runOnce();

    expect(bridge.classifierDryRun).toHaveBeenCalledWith({
      text: "write a parser",
      group_id: "coding-auto",
    });
    // The group is read again for the run, so the sentences match what the
    // server evaluated.
    expect(bridge.getRoutingSettings).toHaveBeenCalledTimes(2);

    const outcome = container.querySelector<HTMLElement>(
      "[data-classifier-dry-run-outcome]",
    );
    expect(outcome?.textContent).toContain(
      "会选用 claude-sonnet-4-5（Claude 订阅），由第 3 条规则选出",
    );
    const answers = container.querySelector(
      "[data-classifier-dry-run-answers]",
    );
    if (outcome === null || answers === null)
      throw new Error("Missing the outcome or the classifier answers");
    // The conclusion comes before the details that explain it.
    expect(
      outcome.compareDocumentPosition(answers) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    const rows = [
      ...container.querySelectorAll<HTMLElement>(
        "[data-classifier-dry-run-rule]",
      ),
    ];
    expect(rows.map((row) => row.dataset.classifierDryRunRule)).toEqual([
      "night",
      "image",
      "coding",
      "long",
    ]);
    expect(rows.map((row) => row.dataset.ruleSelected)).toEqual([
      "false",
      "false",
      "true",
      "false",
    ]);
    // Rules read back as the same phrases the editor shows.
    expect(rows[0].textContent).toContain("时间段");
    expect(rows[0].textContent).toContain("22:00–08:00");
    expect(rows[0].textContent).toContain("不满足");
    expect(rows[1].textContent).toContain("条件未知");
    expect(rows[1].querySelector("[data-rule-reason]")?.textContent).toBe(
      "含图片：试运行里还不能提供这个条件",
    );
    expect(rows[2].textContent).toContain("任务类型 是");
    expect(rows[2].textContent).toContain("满足 · 选用");
    expect(rows[2].querySelector("[data-rule-reason]")?.textContent).toBe(
      "任务类型：编码（86%）",
    );
    expect(rows[3].textContent).toContain("未检查");
    expect(rows[3].textContent).toContain("qwen3-coder-plus");

    const fallback = container.querySelector<HTMLElement>(
      "[data-classifier-dry-run-fallback-row]",
    );
    expect(fallback?.textContent).toContain("deepseek-chat");
    expect(fallback?.textContent).toContain("未用到");
    expect(container.textContent).toContain("812 ms");
  });

  it("says when no rule matched and the Otherwise model was used", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        category: "general",
        decision: {
          member_id: "fast",
          service_id: "svc-deepseek",
          model: "deepseek-chat",
          rule_id: null,
          used_default: true,
        },
      }),
    );
    await render({ group: "coding-auto" });
    await runOnce();

    expect(
      container.querySelector("[data-classifier-dry-run-outcome]")?.textContent,
    ).toContain(
      "会选用 deepseek-chat（DeepSeek），没有规则满足，使用其他情况的模型",
    );
    const fallback = container.querySelector<HTMLElement>(
      "[data-classifier-dry-run-fallback-row]",
    );
    expect(fallback?.dataset.ruleSelected).toBe("true");
    expect(fallback?.textContent).toContain("选用");
  });

  it("names a provider, rule and model that are gone without their IDs", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        decision: {
          member_id: "member-gone",
          service_id: "svc-gone",
          model: "deepseek-chat",
          rule_id: "rule-gone",
          used_default: false,
        },
        rules: [
          {
            rule_id: "rule-gone",
            priority: 5,
            target_member_id: "member-gone",
            target_model: "",
            state: "true",
            selected: true,
            target_missing: false,
            fields: [],
          },
        ],
      }),
    );
    await render({ group: "coding-auto" });
    await runOnce();

    const outcome = container.querySelector(
      "[data-classifier-dry-run-outcome]",
    );
    expect(outcome?.textContent).toContain(
      "会选用 deepseek-chat（已删除的 API 提供商），由一条规则选出",
    );
    const row = container.querySelector(
      '[data-classifier-dry-run-rule="rule-gone"]',
    );
    expect(row?.textContent).toContain("已删除的规则");
    expect(row?.textContent).toContain("已移出路由组的模型");
    for (const id of ["svc-gone", "rule-gone", "member-gone"])
      expect(container.textContent).not.toContain(id);
  });

  it("warns when a matching rule points at a member the group lost", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    const result = routedResult();
    bridge.classifierDryRun.mockResolvedValue({
      ...result,
      decision: { ...result.decision!, used_default: true, rule_id: null },
      rules: result.rules.map((rule) =>
        rule.rule_id === "coding"
          ? { ...rule, selected: false, target_missing: true }
          : rule,
      ),
    });
    await render({ group: "coding-auto" });
    await runOnce();

    expect(
      container.querySelector("[data-classifier-dry-run-target-missing]")
        ?.textContent,
    ).toContain("第 3 条规则满足，但它指向的模型已不在这个路由组里");
  });

  it("judges the rules without a classifier and says none was called", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        configured: false,
        category: "",
        confidence: null,
        fallback_reason: "classifier_unavailable",
        duration_ms: 0,
        timeout_ms: null,
        decision: {
          member_id: "fast",
          service_id: "svc-deepseek",
          model: "deepseek-chat",
          rule_id: null,
          used_default: true,
        },
        rules: [
          {
            rule_id: "coding",
            priority: 30,
            target_member_id: "strong",
            target_model: "claude-sonnet-4-5",
            state: "unknown",
            selected: false,
            target_missing: false,
            fields: [field("task_type", false, "classifier_unavailable")],
          },
        ],
      }),
    );
    await render({ group: "coding-auto" });
    await runOnce();

    expect(
      container.querySelector("[data-classifier-dry-run-outcome]")?.textContent,
    ).toContain(
      "会选用 deepseek-chat（DeepSeek），没有规则满足，使用其他情况的模型",
    );
    expect(
      container.querySelector("[data-classifier-dry-run-not-called]")
        ?.textContent,
    ).toBe(
      "分类器未启用或没配置完整，这次没有调用分类器；依赖它的规则按未知处理。",
    );
    expect(
      container.querySelector("[data-classifier-dry-run-fallback]"),
    ).toBeNull();
    expect(
      container.querySelector("[data-classifier-dry-run-rules]")?.textContent,
    ).toContain("分类器没有给出可用的结果");
    // Nothing was called: the header shows neither the call nor a duration.
    expect(container.textContent).not.toContain("会调用一次分类器");
    expect(container.textContent).not.toContain("0 ms");
  });

  it("shows the instruction the classifier received", async () => {
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        instruction:
          "Classify the text.\n\ntopic: 编程任务主题\nOptions: 前端 | 后端",
      }),
    );
    await render();
    await runOnce();

    expect(container.textContent).toContain("分类器收到的指令");
    expect(
      container.querySelector("[data-classifier-dry-run-instruction]")
        ?.textContent,
    ).toBe("Classify the text.\n\ntopic: 编程任务主题\nOptions: 前端 | 后端");
  });

  it("explains a run the privacy check stopped before any rule", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        category: "",
        confidence: null,
        fallback_reason: "privacy_blocked",
        duration_ms: 0,
        timeout_ms: null,
        sanitized_preview: "",
      }),
    );
    await render({ group: "coding-auto" });
    await runOnce();

    expect(
      container.querySelector("[data-classifier-dry-run-outcome]")?.textContent,
    ).toContain("隐私检查没有通过");
    expect(
      container.querySelector("[data-classifier-dry-run-rules]"),
    ).toBeNull();
  });

  it("reports a classifier timeout as an error naming the configured limit", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        category: "",
        confidence: null,
        fallback_reason: "classifier_timeout",
        duration_ms: 1004,
        timeout_ms: 1000,
      }),
    );
    await render({ group: "coding-auto" });
    await runOnce();

    const timeout = container.querySelector<HTMLElement>(
      "[data-classifier-dry-run-timeout]",
    );
    expect(timeout?.textContent).toBe(
      "分类器超时：1000 ms 内没有回答。依赖它的规则按未知处理。",
    );
    expect(
      container.querySelector("[data-classifier-dry-run-fallback]"),
    ).toBeNull();
  });

  it("reports a dry run that ran out of time as an explicit error", async () => {
    bridge.classifierDryRun.mockRejectedValue(
      new Error(
        'POST /control/v1/routing/classifier/dry-run returned 504: {"error":{"code":"dry_run_timeout","message":"dry-run did not finish within the classifier timeout plus 5s"}}',
      ),
    );
    await render();
    await runOnce();

    expect(container.textContent).toContain(
      "试运行超过「分类器超时时间 + 5 秒」仍没有完成，已停止。",
    );
    expect(bridge.classifierDryRun).toHaveBeenCalledTimes(1);
  });

  it("sends only the request facts that were set", async () => {
    await render();
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>("[data-dry-run-facts-trigger]")
        ?.click();
    });
    await flush();
    const streaming = [
      ...document.querySelectorAll<HTMLButtonElement>(
        '[data-slot="segmented-control"][aria-label="流式"] button',
      ),
    ].find((item) => item.textContent === "是");
    if (!streaming) throw new Error("Missing the streaming toggle");
    await act(async () => {
      streaming.click();
    });
    const time = document.querySelector<HTMLInputElement>(
      'input[aria-label="评估时刻"]',
    );
    if (time === null) throw new Error("Missing the time input");
    await type(time, "23:30");
    expect(
      container.querySelector("[data-dry-run-facts-trigger]")?.textContent,
    ).toContain("2");

    await runOnce();
    expect(bridge.classifierDryRun).toHaveBeenCalledWith({
      text: "write a parser",
      streaming: true,
      system_time: "23:30",
    });
  });

  it("loads a sample text", async () => {
    await render();
    await chooseOption('[aria-label="载入示例文本"]', "账单问题");
    expect(textarea().value).toBe(
      "上个月的发票还没收到，能帮我查一下扣款记录吗？",
    );
    // Editing the text turns the picker back into "custom".
    await type(textarea(), "something else");
    expect(
      container.querySelector("[data-dry-run-samples]")?.textContent,
    ).toContain("自定义文本");
  });

  it("switches to classifier-only from the group picker", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    const onGroupChange = vi.fn();
    await render({ group: "coding-auto", onGroupChange });
    await chooseOption(
      "[data-dry-run-group]",
      "不选路由组（只看分类器的回答）",
    );

    expect(onGroupChange).toHaveBeenLastCalledWith("");
    await runOnce();
    expect(bridge.classifierDryRun).toHaveBeenCalledWith({
      text: "write a parser",
    });
  });

  it("follows a group handed down by the editor", async () => {
    bridge.getRoutingSettings.mockResolvedValue(
      routingSettings([codingGroup, { ...disabledGroup, enabled: true }]),
    );
    await render({ group: "coding-auto" });
    await render({ group: "research" });
    await runOnce();
    expect(bridge.classifierDryRun).toHaveBeenCalledWith({
      text: "write a parser",
      group_id: "research",
    });
  });

  it("says the run uses the saved version of a group with unsaved edits", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    await render({ group: "coding-auto", unsavedGroups: ["coding-auto"] });
    expect(
      container.querySelector("[data-classifier-dry-run-unsaved]")?.textContent,
    ).toBe("astrlink/coding-auto 有未保存的修改，这次用的是已保存的版本。");
  });

  it("opens the group's editor from the verdict list", async () => {
    bridge.getRoutingSettings.mockResolvedValue(routingSettings([codingGroup]));
    bridge.classifierDryRun.mockResolvedValue(routedResult());
    const onEditGroup = vi.fn();
    await render({ group: "coding-auto", onEditGroup });
    await runOnce();
    await act(async () => {
      button("编辑规则").click();
    });
    expect(onEditGroup).toHaveBeenCalledWith("coding-auto");
  });

  it("starts a run with Cmd or Ctrl and Enter", async () => {
    await render();
    await act(async () => {
      textarea().dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Enter",
          metaKey: true,
          bubbles: true,
        }),
      );
    });
    await flush();
    expect(bridge.classifierDryRun).toHaveBeenCalledTimes(1);
  });

  it("surfaces a failed run instead of claiming a result", async () => {
    bridge.classifierDryRun.mockRejectedValue(new Error("core unreachable"));
    await render();
    await runOnce();
    expect(container.textContent).toContain("core unreachable");
  });

  const candidateRows = () => [
    ...container.querySelectorAll<HTMLElement>(
      "[data-classifier-dry-run-candidate]",
    ),
  ];

  it("shows each candidate's rank, value, score and whether it won", async () => {
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        fields: [
          {
            ...field("task_type", true),
            classifier_confidence: 0.95,
            classifier_id: "cloud:jev",
            candidates: [
              { value: "coding", score: 0.95, rank: 0, chosen: false },
              { value: "research", score: 0.7, rank: 1, chosen: true },
            ],
          },
        ],
      }),
    );
    await render();
    await runOnce();

    const rows = candidateRows();
    expect(rows.map((row) => row.dataset.candidateChosen)).toEqual([
      "false",
      "true",
    ]);
    // rank 是 0-based 的 wire 口径，界面显示 rank + 1。
    expect(rows.map((row) => row.dataset.candidateRank)).toEqual(["0", "1"]);
    expect(rows[0].textContent).toContain("coding");
    expect(rows[0].textContent).toContain("0.95");
    expect(rows[0].textContent).toContain("淘汰");
    expect(rows[1].textContent).toContain("2");
    expect(rows[1].textContent).toContain("0.70");
    expect(rows[1].textContent).toContain("采用");
    // 名次最高的那条写着"淘汰"，没有解释会被当成 bug。
    expect(
      container.querySelector('[aria-label="为什么高分候选不一定被采用"]'),
    ).not.toBeNull();
  });

  it("renders no candidate block when no field carried candidates", async () => {
    // `omitempty`：低置信或回退场景下 Go 侧根本不带 `fields`。
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        category: "",
        confidence: null,
        fallback_reason: "classifier_low_confidence",
      }),
    );
    await render();
    await runOnce();

    expect(candidateRows()).toHaveLength(0);
    expect(container.textContent).not.toContain("候选");
    expect(container.textContent).toContain("classifier_low_confidence");
    expect(
      container.querySelector("[data-classifier-dry-run-task-type]")
        ?.textContent,
    ).toContain("低于阈值");
  });

  it("renders a candidate table only for fields that have candidates", async () => {
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        fields: [
          field("protocol", false),
          {
            ...field("task_type", true),
            candidates: [
              { value: "coding", score: 0.9, rank: 0, chosen: true },
            ],
          },
        ],
      }),
    );
    await render();
    await runOnce();

    expect(
      [
        ...container.querySelectorAll<HTMLElement>(
          "[data-classifier-dry-run-candidates]",
        ),
      ].map((table) => table.dataset.classifierDryRunCandidates),
    ).toEqual(["task_type"]);
    expect(candidateRows()).toHaveLength(1);
  });

  it("reports the classifier's answer for each custom dimension", async () => {
    bridge.classifierDryRun.mockResolvedValue(
      classified({
        dimensions: [
          {
            name: "branch",
            label: "后端",
            score: 0.93,
            known: true,
            produced: true,
          },
          {
            name: "topic",
            label: "billing",
            score: 0.2,
            known: false,
            produced: true,
          },
          {
            name: "region",
            label: "",
            score: 0,
            known: false,
            produced: false,
          },
        ],
      }),
    );
    await render();
    await runOnce();

    const row = (name: string) =>
      container.querySelector<HTMLElement>(
        `[data-classifier-dry-run-dimension="${name}"]`,
      );
    expect(row("branch")?.dataset.dimensionKnown).toBe("true");
    expect(row("branch")?.textContent).toContain("后端");
    // Same scale as the task type confidence above it.
    expect(row("branch")?.textContent).toContain("93%");
    expect(row("branch")?.textContent).toContain("可用");
    // Skipped and below-threshold need different fixes, so they read differently.
    expect(row("topic")?.textContent).toContain("低于阈值");
    expect(row("region")?.textContent).toContain("未作答");
  });

  it("renders no dimension rows when only the built-in dimension exists", async () => {
    await render();
    await runOnce();
    expect(
      container.querySelector("[data-classifier-dry-run-dimensions]"),
    ).toBeNull();
  });
});
