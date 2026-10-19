import { describe, expect, it } from "vitest";

import {
  maxModelRoutingGroups,
  maxModelRoutingKeywords,
  maxModelRoutingListItems,
  maxModelRoutingMembersPerGroup,
  maxModelRoutingPriority,
  maxModelRoutingRulesPerGroup,
  modelRoutingConditionOperator,
  modelRoutingGroupSlug,
  modelRoutingPublicModelID,
  isModelRoutingRange,
  isModelRoutingTime,
  modelRoutingFields,
  parseModelRoutingGroups,
  validateModelRoutingDraft,
  type ModelRoutingCondition,
  type ModelRoutingGroup,
} from "./model-routing-group-model";
import { modelRoutingFieldKind } from "./model-routing-catalog-model";

/** A valid two-member group; each test perturbs one aspect of it. */
function group(patch: Partial<ModelRoutingGroup> = {}): ModelRoutingGroup {
  return {
    id: "superclaude",
    enabled: true,
    members: [
      { id: "fast", service_id: "anthropic", model: "claude-haiku" },
      { id: "strong", service_id: "anthropic", model: "claude-sonnet" },
    ],
    default_member_id: "fast",
    rules: [],
    ...patch,
  };
}

/** A group carrying exactly one rule whose condition is `when`. */
function groupWithRule(when: unknown, target = "fast"): ModelRoutingGroup {
  return group({
    rules: [
      {
        id: "r",
        priority: 1,
        when: when as ModelRoutingCondition,
        target_member_id: target,
      },
    ],
  });
}

describe("model routing group grammar", () => {
  it("accepts the shape the design froze", () => {
    const parsed = parseModelRoutingGroups([
      groupWithRule({ field: "task_type", in: ["coding"] }, "strong"),
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0].members.map((member) => member.id)).toEqual([
      "fast",
      "strong",
    ]);
    expect(parsed[0].rules[0].when).toEqual({
      field: "task_type",
      in: ["coding"],
    });
  });

  it("validates empty draft lists recursively and accepts any task_type rule", () => {
    const issues = validateModelRoutingDraft([
      groupWithRule({
        not: {
          all: [
            { field: "streaming", equals_bool: true },
            {
              not: {
                any: [
                  { field: "language", in: [] },
                  { field: "task_type", in: ["coding"] },
                ],
              },
            },
          ],
        },
      }),
    ]);
    expect(issues).toEqual([
      {
        kind: "empty-values",
        groupIndex: 0,
        ruleIndex: 0,
        conditionPath: [0, 1, 0, 0],
      },
    ]);
    // task_type no longer depends on any group-scoped classifier configuration.
    expect(
      validateModelRoutingDraft([
        groupWithRule({ field: "task_type", in: ["coding"] }),
      ]),
    ).toEqual([]);
  });

  it("treats a missing key as no groups and rejects a non-array", () => {
    expect(parseModelRoutingGroups(undefined)).toEqual([]);
    expect(parseModelRoutingGroups(null)).toEqual([]);
    expect(() => parseModelRoutingGroups({})).toThrow();
  });

  it("sorts rules by priority so array order does not matter", () => {
    const rules = [
      groupWithRule({ field: "streaming", equals_bool: true }, "strong")
        .rules[0],
      {
        id: "a",
        priority: 10,
        when: {
          field: "streaming",
          equals_bool: false,
        } as ModelRoutingCondition,
        target_member_id: "fast",
      },
    ];
    const [forward] = parseModelRoutingGroups([group({ rules })]);
    const [reversed] = parseModelRoutingGroups([
      group({ rules: [...rules].reverse() }),
    ]);
    // priority 1 (r) sorts ahead of priority 10 (a), either way round.
    expect(forward.rules.map((rule) => rule.id)).toEqual(["r", "a"]);
    expect(reversed.rules.map((rule) => rule.id)).toEqual(["r", "a"]);
  });

  it("rejects duplicate rule priority", () => {
    const rules = [
      {
        id: "a",
        priority: 10,
        when: {
          field: "streaming",
          equals_bool: true,
        } as ModelRoutingCondition,
        target_member_id: "fast",
      },
      {
        id: "b",
        priority: 10,
        when: {
          field: "streaming",
          equals_bool: false,
        } as ModelRoutingCondition,
        target_member_id: "strong",
      },
    ];
    expect(() => parseModelRoutingGroups([group({ rules })])).toThrow(
      /priority/,
    );
  });

  it("rejects an unknown field, an unknown operator, and a misplaced operator", () => {
    // domains/intent/workload_complexity are not V1 fields.
    expect(() =>
      parseModelRoutingGroups([groupWithRule({ field: "intent", in: ["x"] })]),
    ).toThrow(/不属于 V1/);
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "task_type", contains_any: ["x"] }),
      ]),
    ).toThrow();
    // A boolean field cannot carry `in`.
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "streaming", in: ["true"] }),
      ]),
    ).toThrow();
    // Two operators on one leaf is not a valid shape.
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "task_type", in: ["coding"], equals: "coding" }),
      ]),
    ).toThrow();
  });

  it("accepts both numeric bounds and enforces their ordering", () => {
    expect(
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", gte: 2, lte: 8 }),
      ])[0].rules[0].when,
    ).toEqual({ field: "turn_count", gte: 2, lte: 8 });
    // Either bound may stand alone.
    expect(
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", gt: 2 }),
      ])[0].rules[0].when,
    ).toEqual({ field: "turn_count", gt: 2 });
    expect(
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", lt: 8 }),
      ])[0].rules[0].when,
    ).toEqual({ field: "turn_count", lt: 8 });
    // A zero-width range is still a range.
    expect(
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", gt: 5, lte: 5 }),
      ])[0].rules[0].when,
    ).toEqual({ field: "turn_count", gt: 5, lte: 5 });
    expect(() =>
      parseModelRoutingGroups([groupWithRule({ field: "turn_count" })]),
    ).toThrow(/至少需要一个边界/);
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", gte: 9, lte: 2 }),
      ]),
    ).toThrow(/下界不得大于上界/);
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", gte: -1 }),
      ]),
    ).toThrow();
  });

  it("reads the retired min/max keys as gte/lte and normalizes them", () => {
    // min and max were both inclusive, so the rename is lossless.
    expect(
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", min: 2, max: 8 }),
      ])[0].rules[0].when,
    ).toEqual({ field: "turn_count", gte: 2, lte: 8 });
    expect(
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", min: 1 }),
      ])[0].rules[0].when,
    ).toEqual({ field: "turn_count", gte: 1 });
    expect(
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", max: 4 }),
      ])[0].rules[0].when,
    ).toEqual({ field: "turn_count", lte: 4 });
    // A legacy key next to its replacement is a duplicate bound, not a merge.
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", min: 1, gte: 2 }),
      ]),
    ).toThrow(/下界只能有一个/);
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", max: 1, lte: 2 }),
      ]),
    ).toThrow(/上界只能有一个/);
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", gt: 1, gte: 2 }),
      ]),
    ).toThrow(/下界只能有一个/);
    // The legacy pair keeps its ordering check across the rename.
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "turn_count", min: 9, max: 2 }),
      ]),
    ).toThrow(/下界不得大于上界/);
  });

  it("bounds branches, keys, lists and keywords", () => {
    expect(() => parseModelRoutingGroups([groupWithRule({ all: [] })])).toThrow(
      /不得为空数组/,
    );
    // An unknown key inside a condition is not silently ignored.
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({
          all: [{ field: "streaming", equals_bool: true }],
          extra: 1,
        }),
      ]),
    ).toThrow(/未知字段/);
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({
          field: "literal_keyword",
          contains_any: Array.from(
            { length: maxModelRoutingKeywords + 1 },
            (_, index) => `k${index}`,
          ),
        }),
      ]),
    ).toThrow(/关键词过多/);
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({
          field: "task_type",
          in: Array.from(
            { length: maxModelRoutingListItems + 1 },
            (_, index) => `t${index}`,
          ),
        }),
      ]),
    ).toThrow(/列表过长/);
  });

  it("rejects a condition tree deeper than the bound", () => {
    // Five nested `all` levels exceed the depth-4 limit.
    let when: unknown = { field: "streaming", equals_bool: true };
    for (let level = 0; level < 5; level += 1) when = { all: [when] };
    expect(() => parseModelRoutingGroups([groupWithRule(when)])).toThrow(
      /条件过深/,
    );
  });

  it("requires a member default and a target that is a member", () => {
    expect(() =>
      parseModelRoutingGroups([group({ default_member_id: "missing" })]),
    ).toThrow(/默认成员不存在/);
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "streaming", equals_bool: true }, "other"),
      ]),
    ).toThrow(/目标成员不存在/);
    expect(() => parseModelRoutingGroups([group({ members: [] })])).toThrow(
      /至少需要一个成员/,
    );
  });

  it("rejects duplicate member and rule ids", () => {
    expect(() =>
      parseModelRoutingGroups([
        group({
          members: [
            { id: "fast", service_id: "anthropic", model: "claude-haiku" },
            { id: "fast", service_id: "anthropic", model: "claude-sonnet" },
          ],
        }),
      ]),
    ).toThrow(/ID 必须唯一/);
  });

  it("rejects reserved slugs and malformed ids", () => {
    for (const id of ["auto", "text-v1"]) {
      expect(() => parseModelRoutingGroups([group({ id })])).toThrow(/保留 ID/);
    }
    for (const id of ["-lead", "trail-", "Upper", "has space", ""]) {
      expect(() => parseModelRoutingGroups([group({ id })])).toThrow();
    }
  });

  it("rejects excess group and member counts", () => {
    const many = Array.from({ length: maxModelRoutingGroups + 1 }, (_, index) =>
      group({ id: `g${index}` }),
    );
    expect(() => parseModelRoutingGroups(many)).toThrow(/组过多/);
    const members = Array.from(
      { length: maxModelRoutingMembersPerGroup + 1 },
      (_, index) => ({
        id: `m${index}`,
        service_id: "anthropic",
        model: "claude-haiku",
      }),
    );
    expect(() => parseModelRoutingGroups([group({ members })])).toThrow(
      /成员过多/,
    );
  });

  // The rule cap is its own limit (128), not the member cap (32): a group with
  // 33..128 rules is legal and must load.
  it("bounds rules by the rule cap, not the member cap", () => {
    const rules = (count: number) =>
      Array.from({ length: count }, (_, index) => ({
        id: `r${index}`,
        priority: index + 1,
        when: { field: "streaming", equals_bool: true },
        target_member_id: "strong",
      }));
    expect(() =>
      parseModelRoutingGroups([
        group({ rules: rules(maxModelRoutingMembersPerGroup + 1) }),
      ]),
    ).not.toThrow();
    expect(() =>
      parseModelRoutingGroups([
        group({ rules: rules(maxModelRoutingRulesPerGroup) }),
      ]),
    ).not.toThrow();
    expect(() =>
      parseModelRoutingGroups([
        group({ rules: rules(maxModelRoutingRulesPerGroup + 1) }),
      ]),
    ).toThrow(/规则过多/);
  });

  it("rejects excess priority and a malformed member model", () => {
    expect(() =>
      parseModelRoutingGroups([
        group({
          rules: [
            {
              id: "r",
              priority: maxModelRoutingPriority + 1,
              when: { field: "streaming", equals_bool: true },
              target_member_id: "fast",
            },
          ],
        }),
      ]),
    ).toThrow(/优先级过大/);
    expect(() =>
      parseModelRoutingGroups([
        group({
          members: [{ id: "fast", service_id: "anthropic", model: " padded " }],
          default_member_id: "fast",
        }),
      ]),
    ).toThrow(/模型名无效/);
  });
});

describe("model routing time leaf", () => {
  it("accepts a two-value within window and round-trips it", () => {
    const parsed = parseModelRoutingGroups([
      groupWithRule({ field: "system_time", within: ["09:00", "12:00"] }),
    ]);
    expect(parsed[0].rules[0].when).toEqual({
      field: "system_time",
      within: ["09:00", "12:00"],
    });
  });

  it("accepts a window that crosses midnight and one that names an instant", () => {
    // Both shapes are legal wire values; the half-open/crossing semantics are
    // the evaluator's call, so the parser must not reject either.
    for (const within of [
      ["22:00", "02:00"],
      ["09:00", "09:00"],
    ]) {
      const parsed = parseModelRoutingGroups([
        groupWithRule({ field: "system_time", within }),
      ]);
      expect(parsed[0].rules[0].when).toEqual({ field: "system_time", within });
    }
  });

  it("requires exactly two values", () => {
    for (const within of [[], ["09:00"], ["09:00", "12:00", "13:00"]]) {
      expect(() =>
        parseModelRoutingGroups([
          groupWithRule({ field: "system_time", within }),
        ]),
      ).toThrow(/within/);
    }
  });

  it("rejects a value that is not HH:mm and refuses 24:00", () => {
    // 24:00 is not a wall-clock time: the Go validator refuses it, so the
    // strict parser has to refuse it too rather than letting it reach a save.
    for (const value of ["9:00", "09:60", "24:00", "09-00", "0900", "09:00 "]) {
      expect(() =>
        parseModelRoutingGroups([
          groupWithRule({ field: "system_time", within: [value, "12:00"] }),
        ]),
      ).toThrow(/HH:mm/);
    }
  });

  it("refuses within on a non-time field and a system_time leaf without it", () => {
    expect(() =>
      parseModelRoutingGroups([
        groupWithRule({ field: "task_type", within: ["09:00", "12:00"] }),
      ]),
    ).toThrow();
    expect(() =>
      parseModelRoutingGroups([groupWithRule({ field: "system_time" })]),
    ).toThrow();
  });

  it("refuses an empty window draft and clears the issue once both ends are set", () => {
    const empty = validateModelRoutingDraft([
      groupWithRule({ field: "system_time" }),
    ]);
    expect(empty.map((issue) => issue.kind)).toEqual(["empty-values"]);

    const complete = validateModelRoutingDraft([
      groupWithRule({ field: "system_time", within: ["22:00", "02:00"] }),
    ]);
    expect(complete).toEqual([]);
  });

  it("recognizes a time leaf and never treats it as a range leaf", () => {
    // `within` is not one of the numeric bounds, so the range predicate would
    // otherwise claim this leaf and hand it the wrong editor.
    const time: ModelRoutingCondition = {
      field: "system_time",
      within: ["09:00", "12:00"],
    };
    expect(isModelRoutingTime(time)).toBe(true);
    expect(isModelRoutingRange(time)).toBe(false);
    expect(modelRoutingConditionOperator(time)).toBeNull();
  });
});

describe("model routing condition helpers", () => {
  it("distinguishes a value leaf from a range leaf", () => {
    expect(
      modelRoutingConditionOperator({ field: "task_type", in: ["coding"] }),
    ).toBe("in");
    expect(
      modelRoutingConditionOperator({ field: "streaming", equals_bool: true }),
    ).toBe("equals_bool");
    // A range leaf carries no operator key.
    expect(
      modelRoutingConditionOperator({ field: "turn_count", gte: 1 }),
    ).toBeNull();
  });
});

describe("model routing public ids", () => {
  it("builds the one namespace and reads a slug back", () => {
    expect(modelRoutingPublicModelID("superclaude")).toBe(
      "astrlink/superclaude",
    );
    expect(modelRoutingGroupSlug("astrlink/superclaude")).toBe("superclaude");
    // astrlink/auto is retired, astrlink-text-v1 is a classifier id.
    expect(modelRoutingGroupSlug("astrlink/auto")).toBe("auto");
    expect(modelRoutingGroupSlug("astrlink-text-v1")).toBeNull();
    expect(modelRoutingGroupSlug("astrlink/")).toBeNull();
    expect(modelRoutingGroupSlug("claude-sonnet")).toBeNull();
  });
});

it("round-trips a group-scoped custom task_type label set", () => {
  const parsed = parseModelRoutingGroups([
    group({
      task_type_labels: ["general", "custom-review"],
      rules: [
        {
          id: "r",
          priority: 1,
          when: { field: "task_type", in: ["custom-review"] },
          target_member_id: "strong",
        },
      ],
    }),
  ]);
  expect(parsed[0].task_type_labels).toEqual(["general", "custom-review"]);
  expect(parsed[0].rules[0].when).toEqual({
    field: "task_type",
    in: ["custom-review"],
  });
});

it("treats an empty task_type_labels list as the legacy built-in domain", () => {
  const parsed = parseModelRoutingGroups([group({ task_type_labels: [] })]);
  expect(parsed[0].task_type_labels).toBeUndefined();
  expect(() =>
    parseModelRoutingGroups([
      group({
        task_type_labels: ["general"],
        rules: [
          {
            id: "r",
            priority: 1,
            when: { field: "task_type", in: ["coding"] },
            target_member_id: "fast",
          },
        ],
      }),
    ]),
  ).toThrow(/不在当前组标签集合中/);
});

/**
 * 内建字段表的冻结断言。期望值**故意**是 Go 侧
 * `core/contract/model_routing_test.go:TestModelRoutingFieldSetFrozen` 同一份
 * 字面量文本的拷贝，Rust 侧 `failure_policy.rs` 的 `routing_field_kind_frozen`
 * 也是同一份。三侧任一侧漏改 ⇒ 至少一侧变红。
 *
 * `modelRoutingFields` 的顺序即操作符矩阵的声明序，也就是 Go 表里逐行写下的
 * 顺序，因此这里断言的是有序全表而不只是集合。
 *
 * 自定义维度**刻意不进这张表**：它以运行期维度名列表的形式传入
 * `fieldOperators` / `parseModelRoutingGroups`，所以用户加维度不会让三份表漂移。
 */
describe("built-in routing field set is frozen", () => {
  const frozenFields = [
    "protocol",
    "media_kind",
    "task_type",
    "streaming",
    "continuation",
    "has_tools",
    "has_media",
    "has_image",
    "has_audio",
    "has_video",
    "turn_count",
    "user_message_count",
    "assistant_message_count",
    "tool_message_count",
    "tool_declaration_count",
    "tool_call_count",
    "input_bytes",
    "input_text_runes",
    "estimated_input_context_tokens",
    "literal_keyword",
    "system_time",
  ] as const;

  /** 与 Go 表逐项对应的 kind，词汇是 TS 侧自己的（label/boolean 对应 Go 的 string/bool）。 */
  const frozenKinds: Record<string, string> = {
    protocol: "label",
    media_kind: "label",
    task_type: "label",
    streaming: "boolean",
    continuation: "boolean",
    has_tools: "boolean",
    has_media: "boolean",
    has_image: "boolean",
    has_audio: "boolean",
    has_video: "boolean",
    turn_count: "number",
    user_message_count: "number",
    assistant_message_count: "number",
    tool_message_count: "number",
    tool_declaration_count: "number",
    tool_call_count: "number",
    input_bytes: "number",
    input_text_runes: "number",
    estimated_input_context_tokens: "number",
    literal_keyword: "label",
    system_time: "time",
  };

  it("has exactly the frozen field list, in order", () => {
    expect(modelRoutingFields).toEqual([...frozenFields]);
  });

  it("resolves every frozen field to its frozen kind", () => {
    for (const field of frozenFields) {
      expect(modelRoutingFieldKind(field)).toBe(frozenKinds[field]);
    }
  });
});
