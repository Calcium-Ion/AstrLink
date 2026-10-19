/**
 * 路由组编辑器的视图模型：把 wire 条件读成「短语」，把对短语的改动写回 wire。
 *
 * 草稿始终是 wire 形状（`ModelRoutingGroup`），短语只在渲染时派生。没被改动的
 * 规则原样保存，所以旧写法（`equals`、`not`、`gt`……）不会因为打开页面就被改写；
 * 用户改了哪一行，哪一行才按新写法写回。
 *
 * 成员不再由用户维护：默认模型和每条规则直接选「提供商 · 模型」，这里负责找到
 * 或生成对应成员。已有成员 ID 一律复用，会话粘性依赖它们保持不变。
 */

import {
  fieldOperators,
  isModelRoutingRangeField,
  isModelRoutingTimeField,
  isModelRoutingTimeOfDay,
  maxModelRoutingKeywords,
  maxModelRoutingListItems,
  maxModelRoutingMemberIDRunes,
  maxModelRoutingMembersPerGroup,
  maxModelRoutingRulesPerGroup,
  maxModelRoutingValueRunes,
  modelRoutingFields,
  modelRoutingPriorityStep,
  reservedModelRoutingSlugs,
  type ModelRoutingCondition,
  type ModelRoutingField,
  type ModelRoutingGroup,
  type ModelRoutingLeafCondition,
  type ModelRoutingMember,
  type ModelRoutingRangeCondition,
  type ModelRoutingRule,
  type ModelRoutingTimeCondition,
  type ModelRoutingValueCondition,
} from "./model-routing-group-model";

// ---------------------------------------------------------------------------
// 短语
// ---------------------------------------------------------------------------

/** 条件按取值形状分五类，每类只有一种写法和一个编辑器。 */
export type RoutingPhraseKind =
  | "options"
  | "boolean"
  | "count"
  | "keywords"
  | "time";

/**
 * 规则里的一个条件，界面上是一个短语。
 *
 * 取反并入各类型自己的说法（不是、不含、都不包含、≤），没有单独的 NOT；
 * 数量的边界一律是闭区间，计数都是整数，所以 `gt n` 与 `gte n+1` 等价。
 */
export type RoutingPhrase =
  | { kind: "options"; field: string; negate: boolean; values: string[] }
  | { kind: "boolean"; field: string; value: boolean }
  | { kind: "count"; field: string; min?: number; max?: number }
  | { kind: "keywords"; field: string; negate: boolean; keywords: string[] }
  | { kind: "time"; field: string; start: string; end: string };

/** 字段决定短语类型；自定义分类维度与 `task_type` 同属选项类。 */
export function routingPhraseKind(field: ModelRoutingField): RoutingPhraseKind {
  if (isModelRoutingTimeField(field)) return "time";
  if (isModelRoutingRangeField(field)) return "count";
  const operators = fieldOperators(field);
  if (operators.includes("equals_bool")) return "boolean";
  if (operators.includes("contains_any")) return "keywords";
  return "options";
}

/** 该字段的值由分类器给出：`task_type` 与所有自定义维度。 */
export function isClassifierField(field: string): boolean {
  return (
    field === "task_type" ||
    !(modelRoutingFields as readonly string[]).includes(field)
  );
}

/**
 * 单个叶子（可能被 `not` 包住）读成短语。写不成一个短语时返回 `null`，
 * 调用方把整条规则显示成只读句子，而不是悄悄改写它。
 *
 * 这些换算在三值逻辑下等价：字段取不到时，原写法和新写法都判为未知。
 */
function phraseFromLeaf(
  leaf: ModelRoutingLeafCondition,
  negated: boolean,
): RoutingPhrase | null {
  const field = leaf.field;
  switch (routingPhraseKind(field)) {
    case "time": {
      const [start = "", end = ""] =
        (leaf as ModelRoutingTimeCondition).within ?? [];
      if (!negated) return { kind: "time", field, start, end };
      // 半开区间 [a, b) 的补集正好是 [b, a)；起止相同是一个瞬间，补集写不成窗口。
      if (start === "" || end === "" || start === end) return null;
      return { kind: "time", field, start: end, end: start };
    }
    case "count": {
      const range = leaf as ModelRoutingRangeCondition;
      const min =
        range.gte ?? (range.gt === undefined ? undefined : range.gt + 1);
      const max =
        range.lte ?? (range.lt === undefined ? undefined : range.lt - 1);
      // `lt 0` 永远不成立，写不成闭区间。
      if (max !== undefined && max < 0) return null;
      if (!negated) return { kind: "count", field, min, max };
      // 区间取反是两段，写不成一个短语。
      if (min !== undefined && max !== undefined) return null;
      if (min !== undefined)
        return min === 0 ? null : { kind: "count", field, max: min - 1 };
      if (max !== undefined) return { kind: "count", field, min: max + 1 };
      return null;
    }
    case "boolean": {
      const value = (leaf as ModelRoutingValueCondition).equals_bool;
      if (value === undefined) return null;
      return { kind: "boolean", field, value: negated ? !value : value };
    }
    case "keywords": {
      const keywords = leaf as ModelRoutingValueCondition;
      if (keywords.contains_any)
        return {
          kind: "keywords",
          field,
          negate: negated,
          keywords: [...keywords.contains_any],
        };
      if (keywords.not_contains_any)
        return {
          kind: "keywords",
          field,
          negate: !negated,
          keywords: [...keywords.not_contains_any],
        };
      return null;
    }
    case "options": {
      const options = leaf as ModelRoutingValueCondition;
      if (options.in)
        return {
          kind: "options",
          field,
          negate: negated,
          values: [...options.in],
        };
      if (options.not_in)
        return {
          kind: "options",
          field,
          negate: !negated,
          values: [...options.not_in],
        };
      if (options.equals !== undefined)
        return {
          kind: "options",
          field,
          negate: negated,
          values: options.equals === "" ? [] : [options.equals],
        };
      if (options.not_equals !== undefined)
        return {
          kind: "options",
          field,
          negate: !negated,
          values: options.not_equals === "" ? [] : [options.not_equals],
        };
      return null;
    }
  }
}

/**
 * 一条规则的 `when` 读成短语列表（行内恒为「且」）。`{ all: [] }` 是刚加的空白
 * 规则，读成空列表；`any`、多层 `not` 等写不成短语的形状返回 `null`。
 */
export function phrasesFromWhen(
  when: ModelRoutingCondition,
): RoutingPhrase[] | null {
  if ("field" in when) {
    const phrase = phraseFromLeaf(when as ModelRoutingLeafCondition, false);
    return phrase ? [phrase] : null;
  }
  if ("not" in when) {
    if (!("field" in when.not)) return null;
    const phrase = phraseFromLeaf(when.not as ModelRoutingLeafCondition, true);
    return phrase ? [phrase] : null;
  }
  if ("any" in when) return null;
  const phrases: RoutingPhrase[] = [];
  for (const child of when.all ?? []) {
    const nested = phrasesFromWhen(child);
    if (nested === null) return null;
    phrases.push(...nested);
  }
  return phrases;
}

/** 短语写回 wire 叶子；写出的只有新写法。 */
export function conditionFromPhrase(
  phrase: RoutingPhrase,
): ModelRoutingLeafCondition {
  switch (phrase.kind) {
    case "options":
      return phrase.negate
        ? { field: phrase.field, not_in: [...phrase.values] }
        : { field: phrase.field, in: [...phrase.values] };
    case "boolean":
      return { field: phrase.field, equals_bool: phrase.value };
    case "count":
      return {
        field: phrase.field,
        ...(phrase.min === undefined ? {} : { gte: phrase.min }),
        ...(phrase.max === undefined ? {} : { lte: phrase.max }),
      };
    case "keywords":
      return phrase.negate
        ? { field: phrase.field, not_contains_any: [...phrase.keywords] }
        : { field: phrase.field, contains_any: [...phrase.keywords] };
    case "time":
      return {
        field: phrase.field,
        within:
          phrase.start === "" && phrase.end === ""
            ? []
            : [phrase.start, phrase.end],
      };
  }
}

/** 短语列表写回 `when`：一个直接写叶子，多个包成 `all`，空列表是待填的空白规则。 */
export function whenFromPhrases(
  phrases: readonly RoutingPhrase[],
): ModelRoutingCondition {
  if (phrases.length === 1) return conditionFromPhrase(phrases[0]);
  return { all: phrases.map(conditionFromPhrase) };
}

export type RoutingPhraseIssue = "empty" | "range" | "tooMany" | "tooLong";

/** 短语自身的问题；有问题的短语不能保存。 */
export function routingPhraseIssue(
  phrase: RoutingPhrase,
): RoutingPhraseIssue | null {
  switch (phrase.kind) {
    case "options":
      if (phrase.values.length === 0) return "empty";
      return phrase.values.length > maxModelRoutingListItems ? "tooMany" : null;
    case "keywords":
      if (phrase.keywords.length === 0) return "empty";
      if (phrase.keywords.length > maxModelRoutingKeywords) return "tooMany";
      return phrase.keywords.some(
        (keyword) => [...keyword].length > maxModelRoutingValueRunes,
      )
        ? "tooLong"
        : null;
    case "count":
      if (phrase.min === undefined && phrase.max === undefined) return "empty";
      return phrase.min !== undefined &&
        phrase.max !== undefined &&
        phrase.min > phrase.max
        ? "range"
        : null;
    case "time":
      return isModelRoutingTimeOfDay(phrase.start) &&
        isModelRoutingTimeOfDay(phrase.end)
        ? null
        : "empty";
    case "boolean":
      return null;
  }
}

/** 数量字段新加时的起始值，取一个常见的分界，用户再改。 */
const countDefaults: Record<string, number> = {
  estimated_input_context_tokens: 64_000,
  input_text_runes: 10_000,
  input_bytes: 100_000,
  turn_count: 10,
  user_message_count: 10,
  assistant_message_count: 10,
  tool_message_count: 5,
  tool_declaration_count: 1,
  tool_call_count: 1,
};

/** 从字段选择器新加一个条件时的初始短语。取值待填的类型保持为空，由浮层接着填。 */
export function defaultRoutingPhrase(field: string): RoutingPhrase {
  switch (routingPhraseKind(field)) {
    case "boolean":
      return { kind: "boolean", field, value: true };
    case "count":
      return { kind: "count", field, min: countDefaults[field] ?? 1 };
    case "keywords":
      return { kind: "keywords", field, negate: false, keywords: [] };
    case "time":
      return { kind: "time", field, start: "09:00", end: "18:00" };
    case "options":
      return { kind: "options", field, negate: false, values: [] };
  }
}

// ---------------------------------------------------------------------------
// 字段选择器与常用场景
// ---------------------------------------------------------------------------

export type RoutingFieldGroupID =
  | "classifier"
  | "content"
  | "size"
  | "request"
  | "time";

/**
 * 「添加条件」里直接可见的 13 个字段，按用途分组；分类器那一组运行期还会
 * 追加自定义维度。其余 8 个收进「更多条件」。
 */
export const routingFieldGroups: readonly {
  id: RoutingFieldGroupID;
  fields: readonly string[];
}[] = [
  { id: "classifier", fields: ["task_type"] },
  {
    id: "content",
    fields: [
      "has_image",
      "has_audio",
      "has_video",
      "has_tools",
      "literal_keyword",
    ],
  },
  {
    id: "size",
    fields: [
      "estimated_input_context_tokens",
      "turn_count",
      "input_text_runes",
    ],
  },
  { id: "request", fields: ["protocol", "streaming", "continuation"] },
  { id: "time", fields: ["system_time"] },
];

export const routingMoreFields: readonly string[] = [
  "media_kind",
  "has_media",
  "user_message_count",
  "assistant_message_count",
  "tool_message_count",
  "tool_declaration_count",
  "tool_call_count",
  "input_bytes",
];

export type RoutingTemplateID =
  | "complexCoding"
  | "image"
  | "longContext"
  | "tools"
  | "night"
  | "keywords";

export const routingTemplateIDs: readonly RoutingTemplateID[] = [
  "complexCoding",
  "image",
  "longContext",
  "tools",
  "night",
  "keywords",
];

/**
 * 常用场景的条件。编码/架构只取当前标签集合里存在的那几个：自定义了任务类型
 * 标签的组可能没有它们，这时留空，让用户在浮层里挑。
 */
export function routingTemplatePhrases(
  template: RoutingTemplateID,
  taskTypeLabels: readonly string[],
): RoutingPhrase[] {
  switch (template) {
    case "complexCoding":
      return [
        {
          kind: "options",
          field: "task_type",
          negate: false,
          values: ["coding", "architect"].filter((label) =>
            taskTypeLabels.includes(label),
          ),
        },
      ];
    case "image":
      return [{ kind: "boolean", field: "has_image", value: true }];
    case "longContext":
      return [
        { kind: "count", field: "estimated_input_context_tokens", min: 64_000 },
      ];
    case "tools":
      return [{ kind: "boolean", field: "has_tools", value: true }];
    case "night":
      return [
        { kind: "time", field: "system_time", start: "22:00", end: "08:00" },
      ];
    case "keywords":
      return [
        {
          kind: "keywords",
          field: "literal_keyword",
          negate: false,
          keywords: [],
        },
      ];
  }
}

// ---------------------------------------------------------------------------
// 目标模型与成员
// ---------------------------------------------------------------------------

/** 规则或「其他情况」选用的模型：哪个提供商的哪个模型名。 */
export interface RoutingTarget {
  serviceId: string;
  model: string;
}

export function memberTarget(
  group: ModelRoutingGroup,
  memberID: string,
): RoutingTarget | undefined {
  const member = group.members.find((item) => item.id === memberID);
  return member
    ? { serviceId: member.service_id, model: member.model }
    : undefined;
}

const routingIDPattern = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/;

/** 模型名转成成员 ID 的形状：小写字母数字与连字符，1–64 字符。 */
export function routingSlug(text: string, fallback = "model"): string {
  const slug = text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, maxModelRoutingMemberIDRunes)
    .replace(/-+$/g, "");
  return slug === "" ? fallback : slug;
}

/** `base`、`base-2`、`base-3`……中第一个没被占用的，长度不超过 64。 */
export function uniqueRoutingID(base: string, taken: Iterable<string>): string {
  const used = new Set(taken);
  if (!used.has(base)) return base;
  for (let index = 2; ; index += 1) {
    const suffix = `-${index}`;
    const head = base
      .slice(0, maxModelRoutingMemberIDRunes - suffix.length)
      .replace(/-+$/g, "");
    const candidate = `${head}${suffix}`;
    if (!used.has(candidate)) return candidate;
  }
}

/**
 * 指向 `target` 的成员 ID：已有同一提供商、同一模型的成员就复用它（包括本次
 * 编辑里暂时没被引用的旧成员），否则按模型名生成一个新成员。
 */
export function ensureRoutingMember(
  group: ModelRoutingGroup,
  target: RoutingTarget,
): { group: ModelRoutingGroup; memberID: string } {
  const existing = group.members.find(
    (member) =>
      member.service_id === target.serviceId && member.model === target.model,
  );
  if (existing) return { group, memberID: existing.id };
  const memberID = uniqueRoutingID(
    routingSlug(target.model),
    group.members.map((member) => member.id),
  );
  const member: ModelRoutingMember = {
    id: memberID,
    service_id: target.serviceId,
    model: target.model,
  };
  return { group: { ...group, members: [...group.members, member] }, memberID };
}

/** 保存前去掉既不是默认、也没有规则指向的成员：它们本来就不会被选中。 */
export function pruneRoutingMembers(
  group: ModelRoutingGroup,
): ModelRoutingGroup {
  const used = new Set([
    group.default_member_id,
    ...group.rules.map((rule) => rule.target_member_id),
  ]);
  const members = group.members.filter((member) => used.has(member.id));
  return members.length === group.members.length
    ? group
    : { ...group, members };
}

export function setDefaultRoutingTarget(
  group: ModelRoutingGroup,
  target: RoutingTarget,
): ModelRoutingGroup {
  const ensured = ensureRoutingMember(group, target);
  return { ...ensured.group, default_member_id: ensured.memberID };
}

export function setRuleRoutingTarget(
  group: ModelRoutingGroup,
  ruleIndex: number,
  target: RoutingTarget,
): ModelRoutingGroup {
  const ensured = ensureRoutingMember(group, target);
  return {
    ...ensured.group,
    rules: ensured.group.rules.map((rule, index) =>
      index === ruleIndex
        ? { ...rule, target_member_id: ensured.memberID }
        : rule,
    ),
  };
}

/** 一个组实际会用到的模型，去重后按出现顺序：先默认，再各条规则。 */
export function routingGroupTargets(group: ModelRoutingGroup): RoutingTarget[] {
  const seen = new Set<string>();
  const targets: RoutingTarget[] = [];
  for (const memberID of [
    group.default_member_id,
    ...group.rules.map((rule) => rule.target_member_id),
  ]) {
    const target = memberTarget(group, memberID);
    if (!target) continue;
    const key = `${target.serviceId}\u001f${target.model}`;
    if (seen.has(key)) continue;
    seen.add(key);
    targets.push(target);
  }
  return targets;
}

// ---------------------------------------------------------------------------
// 规则
// ---------------------------------------------------------------------------

/** 行序即优先级：第 i 行是 `(i+1) * 10`，任何规则改动后都整组重算。 */
export function withRowPriorities(
  rules: readonly ModelRoutingRule[],
): ModelRoutingRule[] {
  return rules.map((rule, index) => ({
    ...rule,
    priority: (index + 1) * modelRoutingPriorityStep,
  }));
}

/** 新规则的 ID：`rule-n`，不与组内已有规则重复。 */
export function nextRuleID(group: ModelRoutingGroup): string {
  const taken = new Set(group.rules.map((rule) => rule.id));
  for (let index = group.rules.length + 1; ; index += 1) {
    const candidate = `rule-${index}`;
    if (!taken.has(candidate)) return candidate;
  }
}

/** 在末尾加一条规则，目标先指向默认模型。 */
export function addRoutingRule(
  group: ModelRoutingGroup,
  phrases: readonly RoutingPhrase[],
): ModelRoutingGroup {
  const rule: ModelRoutingRule = {
    id: nextRuleID(group),
    priority: 0,
    when: whenFromPhrases(phrases),
    target_member_id: group.default_member_id,
  };
  return { ...group, rules: withRowPriorities([...group.rules, rule]) };
}

export function removeRoutingRule(
  group: ModelRoutingGroup,
  ruleIndex: number,
): ModelRoutingGroup {
  return {
    ...group,
    rules: withRowPriorities(
      group.rules.filter((_, index) => index !== ruleIndex),
    ),
  };
}

export function moveRoutingRule(
  group: ModelRoutingGroup,
  from: number,
  to: number,
): ModelRoutingGroup {
  const count = group.rules.length;
  if (from === to || from < 0 || to < 0 || from >= count || to >= count)
    return group;
  const rules = [...group.rules];
  const [moved] = rules.splice(from, 1);
  rules.splice(to, 0, moved);
  return { ...group, rules: withRowPriorities(rules) };
}

export function setRuleRoutingPhrases(
  group: ModelRoutingGroup,
  ruleIndex: number,
  phrases: readonly RoutingPhrase[],
): ModelRoutingGroup {
  return {
    ...group,
    rules: group.rules.map((rule, index) =>
      index === ruleIndex ? { ...rule, when: whenFromPhrases(phrases) } : rule,
    ),
  };
}

/** 一条规则的短语合成一个 `all`，Go 限制它最多这么多个子条件。 */
export const maxRoutingRulePhrases = maxModelRoutingListItems;

export interface RoutingRuleIssue {
  ruleIndex: number;
  /** 出问题的短语；问题在整条规则上时缺省。 */
  phraseIndex?: number;
  problem: RoutingPhraseIssue | "noConditions" | "tooManyConditions";
}

/**
 * 保存前的检查：空白规则、条件过多与没填好的短语。写不成短语的旧规则原样保存，
 * 不在此列。
 */
export function routingRuleIssues(
  group: ModelRoutingGroup,
): RoutingRuleIssue[] {
  const issues: RoutingRuleIssue[] = [];
  group.rules.forEach((rule, ruleIndex) => {
    const phrases = phrasesFromWhen(rule.when);
    if (phrases === null) return;
    if (phrases.length === 0) {
      issues.push({ ruleIndex, problem: "noConditions" });
      return;
    }
    if (phrases.length > maxRoutingRulePhrases)
      issues.push({ ruleIndex, problem: "tooManyConditions" });
    phrases.forEach((phrase, phraseIndex) => {
      const problem = routingPhraseIssue(phrase);
      if (problem) issues.push({ ruleIndex, phraseIndex, problem });
    });
  });
  return issues;
}

export type RoutingGroupIssue = "tooManyRules" | "tooManyModels";

/**
 * 整组的上限，镜像 Go：规则条数，以及保存时按规则和兜底用到的模型重建出的
 * 成员数。
 */
export function routingGroupIssues(
  group: ModelRoutingGroup,
): RoutingGroupIssue[] {
  const issues: RoutingGroupIssue[] = [];
  if (group.rules.length > maxModelRoutingRulesPerGroup)
    issues.push("tooManyRules");
  if (
    pruneRoutingMembers(group).members.length > maxModelRoutingMembersPerGroup
  )
    issues.push("tooManyModels");
  return issues;
}

// ---------------------------------------------------------------------------
// 路由组
// ---------------------------------------------------------------------------

export type RoutingGroupNameIssue = "empty" | "invalid" | "reserved" | "taken";

/** 组名（即 `astrlink/` 后面那段）的问题；`currentID` 是改名前的名字。 */
export function routingGroupNameIssue(
  name: string,
  existing: readonly string[],
  currentID?: string,
): RoutingGroupNameIssue | null {
  if (name === "") return "empty";
  if (!routingIDPattern.test(name)) return "invalid";
  if (reservedModelRoutingSlugs.includes(name)) return "reserved";
  if (name !== currentID && existing.includes(name)) return "taken";
  return null;
}

/** 新建对话框里预填的名字：第一个没被占用的 `group-n`。 */
export function suggestRoutingGroupName(existing: readonly string[]): string {
  const taken = new Set(existing);
  for (let index = existing.length + 1; ; index += 1) {
    const candidate = `group-${index}`;
    if (!taken.has(candidate)) return candidate;
  }
}

/**
 * 新建的组：一个默认模型、没有规则。任务类型标签沿用其他组当前的那份，
 * 「分类器」页把同一份标签写给每个组，新组也不能例外。
 */
export function newRoutingGroup(
  name: string,
  target: RoutingTarget,
  taskTypeLabels?: readonly string[],
): ModelRoutingGroup {
  const memberID = routingSlug(target.model);
  return {
    id: name,
    enabled: true,
    members: [
      { id: memberID, service_id: target.serviceId, model: target.model },
    ],
    default_member_id: memberID,
    rules: [],
    ...(taskTypeLabels && taskTypeLabels.length > 0
      ? { task_type_labels: [...taskTypeLabels] }
      : {}),
  };
}

/** 规则里出现过的分类器字段：用来提示「分类器未启用时这些条件不会生效」。 */
export function routingGroupUsesClassifier(group: ModelRoutingGroup): boolean {
  const visit = (condition: ModelRoutingCondition): boolean => {
    if ("field" in condition)
      return isClassifierField((condition as ModelRoutingLeafCondition).field);
    if ("not" in condition) return visit(condition.not);
    const children =
      "all" in condition ? (condition.all ?? []) : (condition.any ?? []);
    return children.some(visit);
  };
  return group.rules.some((rule) => visit(rule.when));
}
