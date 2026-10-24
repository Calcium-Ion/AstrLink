import { describe, expect, it } from "vitest";

import {
  addRoutingRule,
  conditionFromPhrase,
  defaultRoutingPhrase,
  ensureRoutingMember,
  isClassifierField,
  moveRoutingRule,
  newRoutingGroup,
  nextRuleID,
  phrasesFromWhen,
  pruneRoutingMembers,
  maxRoutingRulePhrases,
  removeRoutingRule,
  routingFieldGroups,
  routingGroupIssues,
  routingGroupNameIssue,
  routingGroupTargets,
  routingGroupUsesClassifier,
  routingMoreFields,
  routingPhraseIssue,
  routingPhraseKind,
  routingRuleIssues,
  routingSlug,
  routingTemplatePhrases,
  setDefaultRoutingTarget,
  setRuleRoutingPhrases,
  setRuleRoutingTarget,
  suggestRoutingGroupName,
  uniqueRoutingID,
  whenFromPhrases,
  type RoutingPhrase,
} from "./model-routing-editor-model";
import {
  maxModelRoutingMembersPerGroup,
  maxModelRoutingRulesPerGroup,
  maxModelRoutingValueRunes,
  modelRoutingFields,
  type ModelRoutingCondition,
  type ModelRoutingGroup,
} from "./model-routing-group-model";

function group(overrides: Partial<ModelRoutingGroup> = {}): ModelRoutingGroup {
  return {
    id: "coding-auto",
    enabled: true,
    members: [
      { id: "fast", service_id: "svc-deepseek", model: "deepseek-chat" },
      { id: "strong", service_id: "svc-claude", model: "claude-sonnet-4-5" },
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

describe("phrase kinds and field lists", () => {
  it("sorts every built-in field into exactly one kind", () => {
    expect(routingPhraseKind("task_type")).toBe("options");
    expect(routingPhraseKind("protocol")).toBe("options");
    expect(routingPhraseKind("has_image")).toBe("boolean");
    expect(routingPhraseKind("estimated_input_context_tokens")).toBe("count");
    expect(routingPhraseKind("literal_keyword")).toBe("keywords");
    expect(routingPhraseKind("system_time")).toBe("time");
    // Custom dimensions are string fields, read like task_type.
    expect(routingPhraseKind("topic")).toBe("options");
  });

  it("lists 13 common fields and 8 more, covering every built-in field once", () => {
    const common = routingFieldGroups.flatMap((group) => group.fields);
    expect(common).toHaveLength(13);
    expect(routingMoreFields).toHaveLength(8);
    expect([...common, ...routingMoreFields].sort()).toEqual(
      [...modelRoutingFields].sort(),
    );
  });

  it("treats task_type and custom dimensions as classifier fields", () => {
    expect(isClassifierField("task_type")).toBe(true);
    expect(isClassifierField("topic")).toBe(true);
    expect(isClassifierField("has_image")).toBe(false);
  });
});

describe("reading wire conditions as phrases", () => {
  const read = (when: ModelRoutingCondition) => phrasesFromWhen(when);

  it("reads the new forms directly", () => {
    expect(read({ field: "task_type", in: ["coding", "architect"] })).toEqual([
      {
        kind: "options",
        field: "task_type",
        negate: false,
        values: ["coding", "architect"],
      },
    ]);
    expect(read({ field: "has_image", equals_bool: false })).toEqual([
      { kind: "boolean", field: "has_image", value: false },
    ]);
    expect(read({ field: "turn_count", gte: 3, lte: 10 })).toEqual([
      { kind: "count", field: "turn_count", min: 3, max: 10 },
    ]);
    expect(
      read({ field: "literal_keyword", not_contains_any: ["退款"] }),
    ).toEqual([
      {
        kind: "keywords",
        field: "literal_keyword",
        negate: true,
        keywords: ["退款"],
      },
    ]);
    expect(read({ field: "system_time", within: ["22:00", "08:00"] })).toEqual([
      { kind: "time", field: "system_time", start: "22:00", end: "08:00" },
    ]);
  });

  it("maps the retired operators to their equivalents", () => {
    expect(read({ field: "task_type", equals: "coding" })).toEqual([
      {
        kind: "options",
        field: "task_type",
        negate: false,
        values: ["coding"],
      },
    ]);
    expect(read({ field: "task_type", not_equals: "general" })).toEqual([
      {
        kind: "options",
        field: "task_type",
        negate: true,
        values: ["general"],
      },
    ]);
    // Counts are integers, so a strict bound is the closed one next to it.
    expect(read({ field: "turn_count", gt: 3, lt: 10 })).toEqual([
      { kind: "count", field: "turn_count", min: 4, max: 9 },
    ]);
  });

  it("folds a negated leaf into the opposite wording", () => {
    expect(read({ not: { field: "task_type", in: ["coding"] } })).toEqual([
      { kind: "options", field: "task_type", negate: true, values: ["coding"] },
    ]);
    expect(read({ not: { field: "task_type", not_in: ["coding"] } })).toEqual([
      {
        kind: "options",
        field: "task_type",
        negate: false,
        values: ["coding"],
      },
    ]);
    expect(read({ not: { field: "has_tools", equals_bool: true } })).toEqual([
      { kind: "boolean", field: "has_tools", value: false },
    ]);
    expect(read({ not: { field: "input_bytes", gte: 100 } })).toEqual([
      { kind: "count", field: "input_bytes", max: 99 },
    ]);
    expect(read({ not: { field: "input_bytes", lte: 100 } })).toEqual([
      { kind: "count", field: "input_bytes", min: 101 },
    ]);
    expect(
      read({ not: { field: "literal_keyword", contains_any: ["a"] } }),
    ).toEqual([
      {
        kind: "keywords",
        field: "literal_keyword",
        negate: true,
        keywords: ["a"],
      },
    ]);
    // The complement of the half-open window [22:00, 08:00) is [08:00, 22:00).
    expect(
      read({ not: { field: "system_time", within: ["22:00", "08:00"] } }),
    ).toEqual([
      { kind: "time", field: "system_time", start: "08:00", end: "22:00" },
    ]);
  });

  it("flattens nested all and returns an empty list for a blank rule", () => {
    expect(
      read({
        all: [
          { field: "has_image", equals_bool: true },
          { all: [{ not: { field: "streaming", equals_bool: true } }] },
        ],
      }),
    ).toEqual([
      { kind: "boolean", field: "has_image", value: true },
      { kind: "boolean", field: "streaming", value: false },
    ]);
    expect(read({ all: [] })).toEqual([]);
  });

  it("refuses shapes a row of phrases cannot express", () => {
    expect(
      read({ any: [{ field: "has_image", equals_bool: true }] }),
    ).toBeNull();
    expect(
      read({ not: { all: [{ field: "has_image", equals_bool: true }] } }),
    ).toBeNull();
    // A negated two-sided range is two ranges.
    expect(read({ not: { field: "turn_count", gte: 1, lte: 3 } })).toBeNull();
    // NOT (>= 0) never matches; < 0 is not a count.
    expect(read({ not: { field: "turn_count", gte: 0 } })).toBeNull();
    expect(read({ field: "turn_count", lt: 0 })).toBeNull();
    // The complement of a single instant is not one window.
    expect(
      read({ not: { field: "system_time", within: ["09:00", "09:00"] } }),
    ).toBeNull();
    expect(
      read({ all: [{ field: "has_image", equals_bool: true }, { any: [] }] }),
    ).toBeNull();
  });
});

describe("writing phrases back", () => {
  it("writes only the new operators", () => {
    expect(
      conditionFromPhrase({
        kind: "options",
        field: "task_type",
        negate: true,
        values: ["a"],
      }),
    ).toEqual({ field: "task_type", not_in: ["a"] });
    expect(
      conditionFromPhrase({ kind: "count", field: "turn_count", min: 2 }),
    ).toEqual({ field: "turn_count", gte: 2 });
    expect(
      conditionFromPhrase({
        kind: "keywords",
        field: "literal_keyword",
        negate: false,
        keywords: ["x"],
      }),
    ).toEqual({ field: "literal_keyword", contains_any: ["x"] });
    expect(
      conditionFromPhrase({
        kind: "time",
        field: "system_time",
        start: "",
        end: "",
      }),
    ).toEqual({ field: "system_time", within: [] });
  });

  it("writes one leaf bare and several under all", () => {
    const one: RoutingPhrase[] = [
      { kind: "boolean", field: "has_image", value: true },
    ];
    expect(whenFromPhrases(one)).toEqual({
      field: "has_image",
      equals_bool: true,
    });
    expect(whenFromPhrases([])).toEqual({ all: [] });
    expect(
      whenFromPhrases([
        ...one,
        { kind: "boolean", field: "has_tools", value: false },
      ]),
    ).toEqual({
      all: [
        { field: "has_image", equals_bool: true },
        { field: "has_tools", equals_bool: false },
      ],
    });
  });

  it("round-trips every phrase kind", () => {
    const phrases: RoutingPhrase[] = [
      { kind: "options", field: "topic", negate: false, values: ["billing"] },
      { kind: "boolean", field: "continuation", value: true },
      {
        kind: "count",
        field: "estimated_input_context_tokens",
        min: 64_000,
        max: 128_000,
      },
      {
        kind: "keywords",
        field: "literal_keyword",
        negate: true,
        keywords: ["退款", "发票"],
      },
      { kind: "time", field: "system_time", start: "09:00", end: "18:00" },
    ];
    expect(phrasesFromWhen(whenFromPhrases(phrases))).toEqual(phrases);
  });
});

describe("phrase issues and defaults", () => {
  it("flags what cannot be saved yet", () => {
    expect(routingPhraseIssue(defaultRoutingPhrase("task_type"))).toBe("empty");
    expect(routingPhraseIssue(defaultRoutingPhrase("literal_keyword"))).toBe(
      "empty",
    );
    expect(routingPhraseIssue({ kind: "count", field: "turn_count" })).toBe(
      "empty",
    );
    expect(
      routingPhraseIssue({
        kind: "count",
        field: "turn_count",
        min: 5,
        max: 2,
      }),
    ).toBe("range");
    expect(
      routingPhraseIssue({
        kind: "time",
        field: "system_time",
        start: "09:00",
        end: "",
      }),
    ).toBe("empty");
    // Core caps each keyword at 128 characters, counting code points.
    expect(
      routingPhraseIssue({
        kind: "keywords",
        field: "literal_keyword",
        negate: false,
        keywords: ["短", "长".repeat(maxModelRoutingValueRunes + 1)],
      }),
    ).toBe("tooLong");
    expect(
      routingPhraseIssue({
        kind: "keywords",
        field: "literal_keyword",
        negate: false,
        keywords: ["长".repeat(maxModelRoutingValueRunes)],
      }),
    ).toBeNull();
    expect(routingPhraseIssue(defaultRoutingPhrase("has_image"))).toBeNull();
    expect(routingPhraseIssue(defaultRoutingPhrase("system_time"))).toBeNull();
    expect(
      routingPhraseIssue(
        defaultRoutingPhrase("estimated_input_context_tokens"),
      ),
    ).toBeNull();
  });

  it("starts the common cases filled in", () => {
    expect(
      routingTemplatePhrases("complexCoding", [
        "general",
        "coding",
        "architect",
      ]),
    ).toEqual([
      {
        kind: "options",
        field: "task_type",
        negate: false,
        values: ["coding", "architect"],
      },
    ]);
    // A group with its own labels may lack them; the condition then waits for a pick.
    expect(
      routingTemplatePhrases("complexCoding", ["chat", "code"])[0],
    ).toMatchObject({
      values: [],
    });
    expect(routingTemplatePhrases("night", [])).toEqual([
      { kind: "time", field: "system_time", start: "22:00", end: "08:00" },
    ]);
  });
});

describe("members follow the chosen models", () => {
  it("reuses a member that already points at the model", () => {
    const result = ensureRoutingMember(group(), {
      serviceId: "svc-claude",
      model: "claude-sonnet-4-5",
    });
    expect(result.memberID).toBe("strong");
    expect(result.group.members).toHaveLength(2);
  });

  it("creates a member named after the model, avoiding taken names", () => {
    const base = group({
      members: [
        ...group().members,
        { id: "gpt-4o", service_id: "svc-proxy", model: "gpt-4o" },
      ],
    });
    const result = ensureRoutingMember(base, {
      serviceId: "svc-openai",
      model: "GPT-4o",
    });
    expect(result.memberID).toBe("gpt-4o-2");
    expect(result.group.members.at(-1)).toEqual({
      id: "gpt-4o-2",
      service_id: "svc-openai",
      model: "GPT-4o",
    });
  });

  it("points rules and the fallback at targets and prunes unused members on save", () => {
    let next = setRuleRoutingTarget(group(), 0, {
      serviceId: "svc-qwen",
      model: "qwen3-coder-plus",
    });
    expect(next.rules[0].target_member_id).toBe("qwen3-coder-plus");
    // The old member stays in the draft so switching back keeps its ID.
    expect(next.members.map((member) => member.id)).toContain("strong");
    next = setRuleRoutingTarget(next, 0, {
      serviceId: "svc-claude",
      model: "claude-sonnet-4-5",
    });
    expect(next.rules[0].target_member_id).toBe("strong");
    next = setDefaultRoutingTarget(next, {
      serviceId: "svc-claude",
      model: "claude-sonnet-4-5",
    });
    expect(next.default_member_id).toBe("strong");
    expect(
      pruneRoutingMembers(next).members.map((member) => member.id),
    ).toEqual(["strong"]);
  });

  it("lists the models a group uses, fallback first, without repeats", () => {
    expect(routingGroupTargets(group())).toEqual([
      { serviceId: "svc-deepseek", model: "deepseek-chat" },
      { serviceId: "svc-claude", model: "claude-sonnet-4-5" },
    ]);
  });

  it("makes member IDs that satisfy the wire pattern", () => {
    expect(routingSlug("Qwen/Qwen3-Coder:480B")).toBe("qwen-qwen3-coder-480b");
    expect(routingSlug("---")).toBe("model");
    expect(routingSlug("a".repeat(80))).toHaveLength(64);
    expect(uniqueRoutingID("a".repeat(64), ["a".repeat(64)])).toBe(
      `${"a".repeat(62)}-2`,
    );
  });
});

describe("rules keep row order as priority", () => {
  it("adds, moves and removes rules with priorities 10, 20, 30", () => {
    let next = addRoutingRule(group(), [
      { kind: "boolean", field: "has_image", value: true },
    ]);
    expect(next.rules.map((rule) => [rule.id, rule.priority])).toEqual([
      ["complex", 10],
      ["rule-2", 20],
    ]);
    // A new rule starts on the fallback model.
    expect(next.rules[1].target_member_id).toBe("fast");
    next = moveRoutingRule(next, 1, 0);
    expect(next.rules.map((rule) => [rule.id, rule.priority])).toEqual([
      ["rule-2", 10],
      ["complex", 20],
    ]);
    next = removeRoutingRule(next, 0);
    expect(next.rules.map((rule) => [rule.id, rule.priority])).toEqual([
      ["complex", 10],
    ]);
  });

  it("never reuses a taken rule ID", () => {
    const base = group({
      rules: [
        {
          id: "rule-2",
          priority: 10,
          when: { field: "has_image", equals_bool: true },
          target_member_id: "fast",
        },
      ],
    });
    expect(nextRuleID(base)).toBe("rule-3");
  });

  it("leaves an untouched legacy rule exactly as stored", () => {
    const legacy = group({
      rules: [
        {
          id: "old",
          priority: 10,
          when: { not: { field: "task_type", equals: "general" } },
          target_member_id: "strong",
        },
        {
          id: "other",
          priority: 20,
          when: { field: "has_image", equals_bool: true },
          target_member_id: "fast",
        },
      ],
    });
    const next = setRuleRoutingPhrases(legacy, 1, [
      { kind: "boolean", field: "has_image", value: false },
    ]);
    expect(next.rules[0].when).toEqual({
      not: { field: "task_type", equals: "general" },
    });
    expect(next.rules[1].when).toEqual({
      field: "has_image",
      equals_bool: false,
    });
  });

  it("reports blank rules and unfinished phrases, but not legacy rules", () => {
    const base = group({
      rules: [
        { id: "a", priority: 10, when: { all: [] }, target_member_id: "fast" },
        {
          id: "b",
          priority: 20,
          when: {
            all: [
              { field: "has_image", equals_bool: true },
              { field: "task_type", in: [] },
            ],
          },
          target_member_id: "fast",
        },
        {
          id: "c",
          priority: 30,
          when: { any: [{ field: "has_image", equals_bool: true }] },
          target_member_id: "fast",
        },
      ],
    });
    expect(routingRuleIssues(base)).toEqual([
      { ruleIndex: 0, problem: "noConditions" },
      { ruleIndex: 1, phraseIndex: 1, problem: "empty" },
    ]);
  });

  it("caps a rule at the conditions one all can hold", () => {
    const leaves = (count: number): ModelRoutingCondition => ({
      all: Array.from({ length: count }, (_, index) => ({
        field: "turn_count",
        gte: index,
      })),
    });
    const rule = (when: ModelRoutingCondition) =>
      group({
        rules: [{ id: "a", priority: 10, when, target_member_id: "fast" }],
      });
    expect(routingRuleIssues(rule(leaves(maxRoutingRulePhrases)))).toEqual([]);
    expect(routingRuleIssues(rule(leaves(maxRoutingRulePhrases + 1)))).toEqual([
      { ruleIndex: 0, problem: "tooManyConditions" },
    ]);
  });

  it("caps a group at Core's rule and model limits", () => {
    const rules = (count: number, models: number) =>
      Array.from({ length: count }, (_, index) => ({
        id: `r${index}`,
        priority: (index + 1) * 10,
        when: { field: "has_image", equals_bool: true },
        target_member_id: `m${index % models}`,
      }));
    const members = (count: number) =>
      Array.from({ length: count }, (_, index) => ({
        id: `m${index}`,
        service_id: "svc",
        model: `model-${index}`,
      }));
    const atLimit = group({
      members: members(maxModelRoutingMembersPerGroup),
      default_member_id: "m0",
      rules: rules(
        maxModelRoutingRulesPerGroup,
        maxModelRoutingMembersPerGroup,
      ),
    });
    expect(routingGroupIssues(atLimit)).toEqual([]);
    expect(
      routingGroupIssues({
        ...atLimit,
        members: members(maxModelRoutingMembersPerGroup + 1),
        rules: rules(
          maxModelRoutingRulesPerGroup + 1,
          maxModelRoutingMembersPerGroup + 1,
        ),
      }),
    ).toEqual(["tooManyRules", "tooManyModels"]);
    // Members no rule points at are dropped on save, so they do not count.
    expect(
      routingGroupIssues({
        ...atLimit,
        members: members(maxModelRoutingMembersPerGroup + 5),
      }),
    ).toEqual([]);
  });
});

describe("groups", () => {
  it("checks the model name", () => {
    expect(routingGroupNameIssue("", [])).toBe("empty");
    expect(routingGroupNameIssue("Bad Name", [])).toBe("invalid");
    expect(routingGroupNameIssue("-x", [])).toBe("invalid");
    expect(routingGroupNameIssue("auto", [])).toBe("reserved");
    expect(routingGroupNameIssue("taken", ["taken"])).toBe("taken");
    // Keeping the current name is not a clash with itself.
    expect(routingGroupNameIssue("taken", ["taken"], "taken")).toBeNull();
    expect(routingGroupNameIssue("support-bot", ["taken"])).toBeNull();
  });

  it("suggests a free name", () => {
    expect(suggestRoutingGroupName([])).toBe("group-1");
    expect(suggestRoutingGroupName(["group-2", "x"])).toBe("group-3");
    expect(suggestRoutingGroupName(["group-2"])).toBe("group-3");
  });

  it("creates a valid group from a name and a default model", () => {
    expect(
      newRoutingGroup(
        "support-bot",
        { serviceId: "svc-deepseek", model: "deepseek-chat" },
        ["chat", "code"],
      ),
    ).toEqual({
      id: "support-bot",
      enabled: true,
      members: [
        {
          id: "deepseek-chat",
          service_id: "svc-deepseek",
          model: "deepseek-chat",
        },
      ],
      default_member_id: "deepseek-chat",
      rules: [],
      task_type_labels: ["chat", "code"],
    });
    expect(
      newRoutingGroup("x", { serviceId: "s", model: "m" }).task_type_labels,
    ).toBeUndefined();
  });

  it("knows when a group depends on the classifier", () => {
    expect(routingGroupUsesClassifier(group())).toBe(true);
    expect(
      routingGroupUsesClassifier(
        group({
          rules: [
            {
              id: "a",
              priority: 10,
              when: { not: { field: "topic", in: ["x"] } },
              target_member_id: "fast",
            },
          ],
        }),
      ),
    ).toBe(true);
    expect(routingGroupUsesClassifier(group({ rules: [] }))).toBe(false);
  });
});
