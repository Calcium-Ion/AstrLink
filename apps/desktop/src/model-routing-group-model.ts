/**
 * Model Routing Group 的严格 wire grammar（V1）。
 *
 * 这是「组合多个模型为一个对外网关模型」的配置，与 `./model-groups.ts` 的
 * `ModelGroup`/`groupModels()` 毫无关系 —— 那个文件只是把模型名按厂商/家族分桶的
 * 列表浏览助手。两者不得互相引用。
 *
 * 后端 `RoutingSettings.Validate()` 仍是权威校验；这里镜像同一套字段名、枚举值、
 * optional 规则和大小限制，让编辑器在提交前就拒绝明显非法的草稿。任何此处放行、
 * 后端拒绝的输入都会以 server error 形式回到界面，不覆盖用户的 dirty draft。
 */

/** 对外虚拟模型的命名空间前缀；调用方不能指定第二个 namespace。 */
export const modelRoutingModelPrefix = "astrlink/";

export const maxModelRoutingGroups = 64;
export const maxModelRoutingMembersPerGroup = 32;
export const maxModelRoutingRulesPerGroup = 128;
export const maxModelRoutingConditionDepth = 4;
export const maxModelRoutingConditionNodes = 64;
export const maxModelRoutingListItems = 16;
export const maxModelRoutingKeywords = 32;
export const maxModelRoutingTaskTypeLabels = maxModelRoutingListItems;
export const maxModelRoutingValueRunes = 128;
export const maxModelRoutingRuleIDRunes = 64;
export const maxModelRoutingMemberIDRunes = 64;
export const maxModelRoutingPriority = 1_000_000_000;
/** member.model 沿用既有 256 字符上限。 */
export const maxModelRoutingModelLength = 256;

/** 保留字：既不作为 slug，也不作为公共 ID 出现。 */
export const reservedModelRoutingSlugs: readonly string[] = ["auto", "text-v1"];
export const reservedModelRoutingIDs: readonly string[] = [
  `${modelRoutingModelPrefix}auto`,
  "astrlink-text-v1",
];

/**
 * slug、member ID 与 rule ID 共用一套形状：小写 ASCII 字母数字与连字符，
 * 1–64 字符，不以连字符开头或结尾。
 */
const routingIDPattern = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/;

/** `astrlink/` 加 64 字符 slug。 */
export const maxModelRoutingPublicIDLength =
  modelRoutingModelPrefix.length + maxModelRoutingMemberIDRunes;

/** 对外公共 ID 的唯一生成入口。 */
export function modelRoutingPublicModelID(groupID: string): string {
  return `${modelRoutingModelPrefix}${groupID}`;
}

/** 把公共 ID 还原成 slug；不是本 namespace 的精确形态时返回 null。 */
export function modelRoutingGroupSlug(modelID: string): string | null {
  if (!modelID.startsWith(modelRoutingModelPrefix)) return null;
  const slug = modelID.slice(modelRoutingModelPrefix.length);
  return routingIDPattern.test(slug) ? slug : null;
}

// ---------------------------------------------------------------------------
// 规则字段与操作符矩阵
// ---------------------------------------------------------------------------

export type ModelRoutingFieldSource = "local" | "classifier";

/**
 * 每个 V1 字段允许的操作符。凡是 `domains`、`intent`、`workload_complexity`
 * 之类的语义字段都**不在这里** —— V1 后端会在保存时以
 * `unsupported_routing_field` 拒绝，界面也就不提供目录选择或编辑入口。
 *
 * 数值字段用比较操作符表达边界：`gt`/`gte` 是下界，`lt`/`lte` 是上界，
 * 下界与上界可以同时出现（即区间）。旧的 `min`/`max` 键只在**读取**时作为
 * 别名接受（见 `rangeBounds`），解析后统一归一化成 `gte`/`lte`，因此保存
 * 回来的就是新格式，旧文档在首次保存时完成迁移。
 */
export const modelRoutingFieldOperators = {
  protocol: ["in", "not_in", "equals", "not_equals"],
  media_kind: ["in", "not_in", "equals", "not_equals"],
  task_type: ["in", "not_in", "equals", "not_equals"],
  streaming: ["equals_bool"],
  continuation: ["equals_bool"],
  has_tools: ["equals_bool"],
  has_media: ["equals_bool"],
  has_image: ["equals_bool"],
  has_audio: ["equals_bool"],
  has_video: ["equals_bool"],
  turn_count: ["gt", "gte", "lt", "lte"],
  user_message_count: ["gt", "gte", "lt", "lte"],
  assistant_message_count: ["gt", "gte", "lt", "lte"],
  tool_message_count: ["gt", "gte", "lt", "lte"],
  tool_declaration_count: ["gt", "gte", "lt", "lte"],
  tool_call_count: ["gt", "gte", "lt", "lte"],
  input_bytes: ["gt", "gte", "lt", "lte"],
  input_text_runes: ["gt", "gte", "lt", "lte"],
  estimated_input_context_tokens: ["gt", "gte", "lt", "lte"],
  literal_keyword: ["contains_any", "not_contains_any"],
  system_time: ["within"],
} as const;

export type BuiltinModelRoutingField = keyof typeof modelRoutingFieldOperators;

/**
 * 规则字段 = 内建字段 ∪ 运行期自定义分类维度名。
 *
 * `(string & {})` 而不是裸 `string`：它保留内建字段的字面量补全与拼写检查，
 * 同时允许维度名通过。**代价**是任何按字段索引操作符矩阵的地方都可能拿到
 * `undefined`，因此必须走 `fieldOperators` 兜底，不能直接索引矩阵。
 */
export type ModelRoutingField = BuiltinModelRoutingField | (string & {});

export type ModelRoutingOperator =
  (typeof modelRoutingFieldOperators)[BuiltinModelRoutingField][number];

/** 数值字段的比较操作符；它们不是「单操作符」叶子，而是可组合的边界。 */
export const modelRoutingRangeOperators = ["gt", "gte", "lt", "lte"] as const;

export type ModelRoutingRangeOperator =
  (typeof modelRoutingRangeOperators)[number];

/** 单操作符值型字段的操作符（不含数值字段的比较边界）。 */
export type ModelRoutingValueOperator = Exclude<
  ModelRoutingOperator,
  ModelRoutingRangeOperator
>;

/** 值型叶子的操作符集合，顺序即编辑器下拉的展示顺序。 */
export const modelRoutingValueOperators: readonly ModelRoutingValueOperator[] =
  [
    "in",
    "not_in",
    "equals",
    "not_equals",
    "equals_bool",
    "contains_any",
    "not_contains_any",
  ];

export const modelRoutingFields = Object.keys(
  modelRoutingFieldOperators,
) as BuiltinModelRoutingField[];

/** 自定义维度的操作符集：与内建 string 类字段同形，顺序与矩阵声明序一致。 */
export const modelRoutingCustomDimensionOperators = [
  "in",
  "not_in",
  "equals",
  "not_equals",
] as const;

/** 内建字段的操作符；未注册字段（自定义分类维度）一律视为 string 类。 */
export function fieldOperators(field: ModelRoutingField): readonly string[] {
  if (Object.hasOwn(modelRoutingFieldOperators, field)) {
    return modelRoutingFieldOperators[field as BuiltinModelRoutingField];
  }
  return modelRoutingCustomDimensionOperators;
}

/** 计数字段用比较边界表达，而不是单一操作符。 */
export function isModelRoutingRangeField(field: ModelRoutingField): boolean {
  const operators = fieldOperators(field);
  return operators.includes("gte");
}

/**
 * `system_time` 是第三种字段形状：既不是比较边界（`gt`/`gte`/`lt`/`lte` 对它
 * 无意义，Go 会以 `does not support that operator` 拒绝），也不是列表/单值型
 * 叶子。它只有一个操作符 `within`，取值是恰好两个 `HH:mm` 组成的半开窗口。
 */
export function isModelRoutingTimeField(field: ModelRoutingField): boolean {
  const operators = fieldOperators(field);
  return operators.includes("within");
}

/** `within` 的取值形状：恰好两个 `HH:mm`。 */
export const modelRoutingTimeWindowLength = 2;

/**
 * 与 Go 的 `validRoutingTimeOfDay` 同形：00–23 时、00–59 分。
 * `24:00` 不是钟表时间，拒绝。
 */
const routingTimeOfDayPattern = /^(?:[01][0-9]|2[0-3]):[0-5][0-9]$/;

export function isModelRoutingTimeOfDay(value: unknown): value is string {
  return typeof value === "string" && routingTimeOfDayPattern.test(value);
}

/** 编辑器 time input 的步长：分钟粒度与 `HH:mm` 一致。 */
export const modelRoutingTimeStepSeconds = 60;

/**
 * 行序生成的优先级步长：第 i 行（0 基）的优先级是 `(i+1) * step`。
 * 留出间隔是为了让手工插入一行时仍能挤出空位，同时保持升序唯一。
 */
export const modelRoutingPriorityStep = 10;

/** 该字段的策略来源：只有 task_type 依赖分类器，其余来自本地解析。 */
export function modelRoutingFieldSource(
  field: ModelRoutingField,
): ModelRoutingFieldSource {
  if (field === "task_type") return "classifier";
  if (!(modelRoutingFields as readonly string[]).includes(field)) {
    return "classifier";
  }
  return "local";
}

/** `task_type` 继承冻结的四个标签；其他语义分类输出不属于 V1。 */
export const modelRoutingTaskTypes: readonly string[] = [
  "general",
  "research",
  "coding",
  "architect",
];

export const modelRoutingMediaKinds: readonly string[] = [
  "none",
  "image",
  "audio",
  "video",
  "mixed",
  "unknown",
];

/**
 * 维度类型的真相源在 `./classifier-settings-model.ts`（它镜像 Go 的
 * `ClassifierDimension`）。这里只转出，不重复声明 —— 两份结构相同的接口
 * 会各自漂移，而编辑器同时读这两边。
 */
import type { ClassifierDimension } from "./classifier-settings-model";

export type { ClassifierDimension };

/**
 * 值的取值域仅用于编辑器候选与提示；后端权威校验接受任意合法字符串。
 *
 * `protocol` 的取值域是**运行期传入**的 `protocols`（core 的 capabilities），
 * 而不是这里再写一份 id 列表。原因有两条：
 *
 *  1. 这里不能静态 import `./service-presets.ts` —— 那条链是
 *     `service-presets → service-model → failure-policy-model → 本文件`，
 *     静态引入会形成环，模块初始化时拿到的是未初始化的数组。
 *  2. capabilities 里就是「AstrLink 当前实际支持的协议」，正是用户写规则时
 *     要选的集合。post-alpha 的保留 id 不在其中：它们此刻不可执行，列出来会
 *     诱导用户写出永远不会命中的规则 —— 与 `language` 被删掉是同一个问题。
 *
 * 未传入时 `protocol` 没有候选集，编辑器回退为自由输入（测试与旧调用点即此路径）。
 */
export function modelRoutingFieldValues(
  field: ModelRoutingField,
  dimensions?: readonly ClassifierDimension[],
  protocols?: readonly string[],
): readonly string[] | undefined {
  if (field === "task_type") return modelRoutingTaskTypes;
  if (field === "media_kind") return modelRoutingMediaKinds;
  if (field === "protocol") return protocols;
  if (dimensions) {
    const dim = dimensions.find((d) => d.name === field);
    if (dim) return dim.options.map((opt) => opt.name);
  }
  return undefined;
}

// ---------------------------------------------------------------------------
// 条件树
// ---------------------------------------------------------------------------

/**
 * 条件树保存为 **wire 形状本身**，不做内部归一化：一个值型叶子就是
 * `{ field, in: [...] }` / `{ field, not_in: [...] }` / `{ field, equals: "x" }`
 * / `{ field, not_equals: "x" }` / `{ field, equals_bool: true }`
 * / `{ field, contains_any: [...] }` / `{ field, not_contains_any: [...] }`，
 * 数值字段则是下界（`gt`/`gte`）与上界（`lt`/`lte`）的任意组合。
 *
 * 这样 parser 与 serializer 互为恒等，编辑器改动的就是将要发送的 JSON，
 * 不会出现「内部表示与 wire 表示漂移」的第二套映射。
 */
export interface ModelRoutingValueCondition {
  field: ModelRoutingField;
  in?: string[];
  not_in?: string[];
  equals?: string;
  not_equals?: string;
  equals_bool?: boolean;
  contains_any?: string[];
  not_contains_any?: string[];
}

/** 数值字段的范围叶子：下界与上界各至多一个，且至少有一个。 */
export interface ModelRoutingRangeCondition {
  field: ModelRoutingField;
  gt?: number;
  gte?: number;
  lt?: number;
  lte?: number;
}

/**
 * `system_time` 的时间叶子：`within` 是恰好两个 `HH:mm`，命名一个半开
 * `[起, 止)` 窗口。起点晚于终点即跨午夜；两端相等命名一个瞬间。
 * 排除语义走 `not` 节点，没有集合型否定操作符。
 */
export interface ModelRoutingTimeCondition {
  field: ModelRoutingField;
  within?: string[];
}

export interface ModelRoutingBranchCondition {
  all?: ModelRoutingCondition[];
  any?: ModelRoutingCondition[];
}

export interface ModelRoutingNotCondition {
  not: ModelRoutingCondition;
}

export type ModelRoutingCondition =
  | ModelRoutingBranchCondition
  | ModelRoutingNotCondition
  | ModelRoutingValueCondition
  | ModelRoutingRangeCondition
  | ModelRoutingTimeCondition;

export type ModelRoutingLeafCondition =
  | ModelRoutingValueCondition
  | ModelRoutingRangeCondition
  | ModelRoutingTimeCondition;

export function isModelRoutingLeaf(
  condition: ModelRoutingCondition,
): condition is ModelRoutingLeafCondition {
  return "field" in condition;
}

/** 范围叶子没有操作符键；编辑器据此选不同的控件。 */
export function isModelRoutingRange(
  condition: ModelRoutingCondition,
): condition is ModelRoutingRangeCondition {
  if (!("field" in condition)) return false;
  if (isModelRoutingTimeField(condition.field)) return false;
  return !modelRoutingValueOperators.some((operator) =>
    Object.hasOwn(condition, operator),
  );
}

/** 时间叶子只携带 `within`；编辑器据此选时间窗口控件。 */
export function isModelRoutingTime(
  condition: ModelRoutingCondition,
): condition is ModelRoutingTimeCondition {
  return "field" in condition && isModelRoutingTimeField(condition.field);
}

/** 值型叶子实际使用的那个操作符；范围叶子与时间叶子返回 null。 */
export function modelRoutingConditionOperator(
  condition: ModelRoutingCondition,
): ModelRoutingValueOperator | null {
  for (const operator of modelRoutingValueOperators) {
    if (Object.hasOwn(condition, operator)) return operator;
  }
  return null;
}

export interface ModelRoutingMember {
  id: string;
  service_id: string;
  model: string;
}

export interface ModelRoutingRule {
  id: string;
  priority: number;
  when: ModelRoutingCondition;
  target_member_id: string;
}

export interface ModelRoutingGroup {
  id: string;
  enabled: boolean;
  members: ModelRoutingMember[];
  default_member_id: string;
  rules: ModelRoutingRule[];
  /** Non-empty labels replace the built-in task_type domain for this group. */
  task_type_labels?: string[];
}

/** 保存前面向编辑器草稿的跨字段校验错误。 */
export type ModelRoutingDraftIssueKind = "empty-values";

export interface ModelRoutingDraftIssue {
  kind: ModelRoutingDraftIssueKind;
  groupIndex: number;
  ruleIndex: number;
  /** 根叶子为空数组；组合节点按 child index 记录路径，not 只有一个 child。 */
  conditionPath: number[];
}

const modelRoutingListOperators = [
  "in",
  "not_in",
  "contains_any",
  "not_contains_any",
] as const;

/**
 * 编辑器草稿允许列表暂时为空，但空列表不能进入严格 wire parser。
 * 这里同时递归检查 task_type，不能只看规则根节点：not/all/any 都可能包住它。
 */
export function validateModelRoutingDraft(
  groups: readonly ModelRoutingGroup[],
): ModelRoutingDraftIssue[] {
  const issues: ModelRoutingDraftIssue[] = [];
  for (const [groupIndex, group] of groups.entries()) {
    const visit = (
      condition: ModelRoutingCondition,
      ruleIndex: number,
      conditionPath: number[],
    ): void => {
      if ("field" in condition) {
        if (isModelRoutingTimeField(condition.field)) {
          const within = (condition as ModelRoutingTimeCondition).within;
          if (
            !Array.isArray(within) ||
            within.length !== modelRoutingTimeWindowLength ||
            within.some((value) => !isModelRoutingTimeOfDay(value))
          ) {
            issues.push({
              kind: "empty-values",
              groupIndex,
              ruleIndex,
              conditionPath,
            });
          }
          return;
        }
        for (const operator of modelRoutingListOperators) {
          if (!Object.hasOwn(condition, operator)) continue;
          const values = (condition as ModelRoutingValueCondition)[operator];
          if (
            !Array.isArray(values) ||
            values.length === 0 ||
            values.some((value) => value.trim() === "")
          ) {
            issues.push({
              kind: "empty-values",
              groupIndex,
              ruleIndex,
              conditionPath,
            });
          }
        }
        for (const operator of ["equals", "not_equals"] as const) {
          if (
            Object.hasOwn(condition, operator) &&
            (condition as ModelRoutingValueCondition)[operator]?.trim() === ""
          ) {
            issues.push({
              kind: "empty-values",
              groupIndex,
              ruleIndex,
              conditionPath,
            });
          }
        }
        return;
      }
      if ("not" in condition) {
        visit(condition.not, ruleIndex, [...conditionPath, 0]);
        return;
      }
      const operator = "all" in condition ? "all" : "any";
      const children = condition[operator] ?? [];
      children.forEach((child, index) =>
        visit(child, ruleIndex, [...conditionPath, index]),
      );
    };

    group.rules.forEach((rule, ruleIndex) => visit(rule.when, ruleIndex, []));
  }
  return issues;
}

export function emptyModelRoutingGroup(id = ""): ModelRoutingGroup {
  return {
    id,
    enabled: true,
    members: [],
    default_member_id: "",
    rules: [],
  };
}

// ---------------------------------------------------------------------------
// 解析
// ---------------------------------------------------------------------------

class ModelRoutingParseError extends Error {}

function fail(path: string, message: string): never {
  throw new ModelRoutingParseError(`${path}: ${message}`);
}

function objectAt(value: unknown, path: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    fail(path, "应为对象");
  return value as Record<string, unknown>;
}

/** 严格字段集：缺必需字段或多出未知字段都拒绝，与后端 strict decode 一致。 */
function keysAt(
  value: Record<string, unknown>,
  required: readonly string[],
  optional: readonly string[],
  path: string,
) {
  if (required.some((key) => !Object.hasOwn(value, key)))
    fail(path, "缺少必需字段");
  if (
    Object.keys(value).some(
      (key) => !required.includes(key) && !optional.includes(key),
    )
  )
    fail(path, "存在未知字段");
}

function textAt(
  value: unknown,
  path: string,
  maxRunes = maxModelRoutingValueRunes,
): string {
  if (typeof value !== "string") fail(path, "应为字符串");
  if ([...value].length > maxRunes) fail(path, "字符串过长");
  return value;
}

function boolAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") fail(path, "应为布尔值");
  return value;
}

function countAt(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    fail(path, "应为非负整数");
  return value;
}

function routingIDAt(value: unknown, path: string): string {
  const id = textAt(value, path, maxModelRoutingMemberIDRunes);
  if (!routingIDPattern.test(id)) fail(path, "ID 形状无效");
  return id;
}

function stringListAt(value: unknown, path: string): string[] {
  if (!Array.isArray(value)) fail(path, "应为数组");
  if (value.length === 0) fail(path, "不得为空数组");
  if (value.length > maxModelRoutingListItems) fail(path, "列表过长");
  const items = value.map((item, index) => textAt(item, `${path}[${index}]`));
  if (new Set(items).size !== items.length) fail(path, "列表项重复");
  return items;
}

function keywordListAt(value: unknown, path: string): string[] {
  if (!Array.isArray(value)) fail(path, "应为数组");
  if (value.length === 0) fail(path, "不得为空数组");
  if (value.length > maxModelRoutingKeywords) fail(path, "关键词过多");
  const items = value.map((item, index) => {
    const keyword = textAt(item, `${path}[${index}]`);
    if (!keyword) fail(`${path}[${index}]`, "不得为空");
    return keyword;
  });
  if (new Set(items).size !== items.length) fail(path, "关键词重复");
  return items;
}

interface ParsedCondition {
  condition: ModelRoutingCondition;
  nodes: number;
}

/**
 * 分类器设置里已声明的自定义维度名称。`null` 表示没读到分类器设置：这时不对内置
 * 字段以外的名称下结论，一律当作自定义维度，交给 Core 判定——这份文档本来就是
 * Core 按它自己的维度表校验过才保存的，读不到维度不该让整页路由设置打不开。
 */
export type KnownRoutingDimensions = readonly string[] | null;

function conditionAt(
  value: unknown,
  path: string,
  depth: number,
  knownDimensions: KnownRoutingDimensions = [],
): ParsedCondition {
  if (depth > maxModelRoutingConditionDepth) fail(path, "条件过深");
  const node = objectAt(value, path);
  const present = ["all", "any", "not", "field"].filter((key) =>
    Object.hasOwn(node, key),
  );
  if (present.length !== 1) fail(path, "条件形状无效");

  if (present[0] === "all" || present[0] === "any") {
    const op = present[0] as "all" | "any";
    keysAt(node, [op], [], path);
    const children = node[op];
    if (!Array.isArray(children)) fail(path, "应为数组");
    if (children.length === 0) fail(path, "不得为空数组");
    if (children.length > maxModelRoutingListItems) fail(path, "子节点过多");
    const parsed = children.map((child, index) =>
      conditionAt(child, `${path}.${op}[${index}]`, depth + 1, knownDimensions),
    );
    return {
      condition: { [op]: parsed.map((item) => item.condition) },
      nodes: 1 + parsed.reduce((total, item) => total + item.nodes, 0),
    };
  }

  if (present[0] === "not") {
    keysAt(node, ["not"], [], path);
    const child = conditionAt(
      node.not,
      `${path}.not`,
      depth + 1,
      knownDimensions,
    );
    return { condition: { not: child.condition }, nodes: 1 + child.nodes };
  }

  return { condition: leafConditionAt(node, path, knownDimensions), nodes: 1 };
}

function leafConditionAt(
  node: Record<string, unknown>,
  path: string,
  knownDimensions: KnownRoutingDimensions = [],
): ModelRoutingLeafCondition {
  const field = textAt(node.field, `${path}.field`);
  const isBuiltin = (modelRoutingFields as readonly string[]).includes(field);
  const isKnownDimension =
    knownDimensions === null || knownDimensions.includes(field);
  if (!isBuiltin && !isKnownDimension) fail(`${path}.field`, "字段不属于 V1");
  const typed = field as ModelRoutingField;

  // 时间字段只有一个 `within` 形状，既不是比较边界也不是列表型叶子。
  if (isModelRoutingTimeField(typed)) {
    keysAt(node, ["field", "within"], [], path);
    return { field: typed, within: withinAt(node.within, `${path}.within`) };
  }

  // 数值字段用比较边界表达上下界，两者可同时出现，先于单操作符分支处理。
  if (isModelRoutingRangeField(typed)) {
    keysAt(node, ["field"], ["gt", "gte", "lt", "lte", "min", "max"], path);
    return { field: typed, ...rangeBounds(node, path) };
  }

  const allowed = fieldOperators(typed);
  const operators = allowed.filter((operator) => Object.hasOwn(node, operator));
  if (operators.length !== 1) fail(path, "操作符与字段不匹配");
  const operator = operators[0] as ModelRoutingValueOperator;
  keysAt(node, ["field", operator], [], path);
  const value = node[operator];

  switch (operator) {
    case "equals_bool":
      return {
        field: typed,
        equals_bool: boolAt(value, `${path}.equals_bool`),
      };
    case "equals":
      return { field: typed, equals: textAt(value, `${path}.equals`) };
    case "not_equals":
      return {
        field: typed,
        not_equals: textAt(value, `${path}.not_equals`),
      };
    case "in":
      return { field: typed, in: stringListAt(value, `${path}.in`) };
    case "not_in":
      return { field: typed, not_in: stringListAt(value, `${path}.not_in`) };
    case "contains_any":
      return {
        field: typed,
        contains_any: keywordListAt(value, `${path}.contains_any`),
      };
    case "not_contains_any":
      return {
        field: typed,
        not_contains_any: keywordListAt(value, `${path}.not_contains_any`),
      };
    case "within":
      return { field: typed, within: withinAt(value, `${path}.within`) };
  }
}

/**
 * 数值边界：下界（`gt`/`gte`）与上界（`lt`/`lte`）各至多一个，至少一个，
 * 均为非负整数且下界不超过上界。
 *
 * 旧 `min`/`max` 是 `gte`/`lte` 的别名，只在读取时接受；解析结果一律归一化
 * 成新键，因此旧文档首次保存即升级，且区间语义不变（min/max 都是闭区间）。
 * `min` 与 `gte`（或 `max` 与 `lte`）同时出现属于重复下界，直接拒绝。
 */
/**
 * `within` 的形状：恰好两个 `HH:mm`。两端相等是合法的「单点瞬时」，不是错误
 * —— 语义由后端求值器裁定，编辑器只做形状与格式校验。
 */
function withinAt(value: unknown, path: string): string[] {
  if (!Array.isArray(value)) fail(path, "应为数组");
  if (value.length !== modelRoutingTimeWindowLength)
    fail(path, `必须恰好 ${modelRoutingTimeWindowLength} 个 HH:mm`);
  return value.map((item, index) => {
    if (!isModelRoutingTimeOfDay(item)) fail(`${path}[${index}]`, "应为 HH:mm");
    return item;
  });
}

function rangeBounds(
  node: Record<string, unknown>,
  path: string,
): Pick<ModelRoutingRangeCondition, "gt" | "gte" | "lt" | "lte"> {
  const lower = (["gt", "gte", "min"] as const).filter((key) =>
    Object.hasOwn(node, key),
  );
  const upper = (["lt", "lte", "max"] as const).filter((key) =>
    Object.hasOwn(node, key),
  );
  if (lower.length > 1) fail(path, "下界只能有一个");
  if (upper.length > 1) fail(path, "上界只能有一个");
  if (lower.length === 0 && upper.length === 0) fail(path, "至少需要一个边界");

  const bounds: Pick<ModelRoutingRangeCondition, "gt" | "gte" | "lt" | "lte"> =
    {};
  let low: number | undefined;
  let high: number | undefined;

  const lowerKey = lower[0];
  if (lowerKey !== undefined) {
    // `min` 归一化为 `gte`：两者都是闭下界。
    bounds[lowerKey === "min" ? "gte" : lowerKey] = countAt(
      node[lowerKey],
      `${path}.${lowerKey}`,
    );
    low = bounds[lowerKey === "min" ? "gte" : lowerKey];
  }
  const upperKey = upper[0];
  if (upperKey !== undefined) {
    // `max` 归一化为 `lte`：两者都是闭上界。
    bounds[upperKey === "max" ? "lte" : upperKey] = countAt(
      node[upperKey],
      `${path}.${upperKey}`,
    );
    high = bounds[upperKey === "max" ? "lte" : upperKey];
  }
  if (low !== undefined && high !== undefined && low > high)
    fail(path, "下界不得大于上界");
  return bounds;
}

function memberAt(value: unknown, path: string): ModelRoutingMember {
  const member = objectAt(value, path);
  keysAt(member, ["id", "service_id", "model"], [], path);
  const model = textAt(
    member.model,
    `${path}.model`,
    maxModelRoutingModelLength,
  );
  if (model === "" || model.trim() !== model)
    fail(`${path}.model`, "模型名无效");
  for (const character of model) {
    const code = character.codePointAt(0)!;
    if (code < 32) fail(`${path}.model`, "模型名含控制字符");
  }
  return {
    id: routingIDAt(member.id, `${path}.id`),
    service_id: textAt(member.service_id, `${path}.service_id`),
    model,
  };
}

function ruleAt(
  value: unknown,
  path: string,
  memberIDs: ReadonlySet<string>,
  knownDimensions: KnownRoutingDimensions,
): ModelRoutingRule {
  const rule = objectAt(value, path);
  keysAt(rule, ["id", "priority", "when", "target_member_id"], [], path);
  const target = routingIDAt(rule.target_member_id, `${path}.target_member_id`);
  // 目标必须指向本组成员，不能写成公共模型 ID 绕过白名单。
  if (!memberIDs.has(target))
    fail(`${path}.target_member_id`, "目标成员不存在");
  const priority = countAt(rule.priority, `${path}.priority`);
  if (priority > maxModelRoutingPriority)
    fail(`${path}.priority`, "优先级过大");
  const { condition, nodes } = conditionAt(
    rule.when,
    `${path}.when`,
    1,
    knownDimensions,
  );
  if (nodes > maxModelRoutingConditionNodes)
    fail(`${path}.when`, "条件节点过多");
  return {
    id: routingIDAt(rule.id, `${path}.id`),
    priority,
    when: condition,
    target_member_id: target,
  };
}

function taskTypeLabelsAt(
  group: Record<string, unknown>,
  path: string,
): Pick<ModelRoutingGroup, "task_type_labels"> {
  if (!Object.hasOwn(group, "task_type_labels")) return {};
  const value = group.task_type_labels;
  if (!Array.isArray(value)) fail(`${path}.task_type_labels`, "应为数组");
  if (value.length === 0) return {};
  if (value.length > maxModelRoutingTaskTypeLabels)
    fail(`${path}.task_type_labels`, "标签过多");
  const labels = value.map((item, index) => {
    const label = textAt(item, `${path}.task_type_labels[${index}]`);
    if (!label.trim()) fail(`${path}.task_type_labels[${index}]`, "不得为空");
    return label;
  });
  if (new Set(labels).size !== labels.length)
    fail(`${path}.task_type_labels`, "标签重复");
  return { task_type_labels: labels };
}

function validateTaskTypeConditionLabels(
  condition: ModelRoutingCondition,
  labels: readonly string[],
  path: string,
): void {
  if ("field" in condition) {
    if (condition.field !== "task_type") return;
    const valueCondition = condition as ModelRoutingValueCondition;
    const values = [
      ...(valueCondition.in ?? []),
      ...(valueCondition.not_in ?? []),
      ...(valueCondition.equals ? [valueCondition.equals] : []),
      ...(valueCondition.not_equals ? [valueCondition.not_equals] : []),
    ];
    for (const value of values) {
      if (!labels.includes(value))
        fail(
          `${path}.field`,
          `task_type ${JSON.stringify(value)} 不在当前组标签集合中`,
        );
    }
    return;
  }
  if ("not" in condition) {
    validateTaskTypeConditionLabels(condition.not, labels, `${path}.not`);
    return;
  }
  const children =
    "all" in condition ? (condition.all ?? []) : (condition.any ?? []);
  children.forEach((child, index) =>
    validateTaskTypeConditionLabels(
      child,
      labels,
      `${path}.${"all" in condition ? "all" : "any"}[${index}]`,
    ),
  );
}

function groupAt(
  value: unknown,
  path: string,
  knownDimensions: KnownRoutingDimensions,
): ModelRoutingGroup {
  const group = objectAt(value, path);
  keysAt(
    group,
    ["id", "enabled", "members", "default_member_id", "rules"],
    ["task_type_labels"],
    path,
  );
  const id = routingIDAt(group.id, `${path}.id`);
  if (reservedModelRoutingSlugs.includes(id)) fail(`${path}.id`, "保留 ID");
  const taskTypeLabelResult = taskTypeLabelsAt(group, path);
  const taskTypeLabels =
    taskTypeLabelResult.task_type_labels ?? modelRoutingTaskTypes;
  const enabled = boolAt(group.enabled, `${path}.enabled`);
  if (!Array.isArray(group.members)) fail(`${path}.members`, "应为数组");
  if (group.members.length === 0) fail(`${path}.members`, "至少需要一个成员");
  if (group.members.length > maxModelRoutingMembersPerGroup)
    fail(`${path}.members`, "成员过多");
  const members = group.members.map((member, index) =>
    memberAt(member, `${path}.members[${index}]`),
  );
  assertUniqueIDs(
    members.map((member) => member.id),
    `${path}.members`,
  );
  const defaultMemberID = routingIDAt(
    group.default_member_id,
    `${path}.default_member_id`,
  );
  const memberIDs = new Set(members.map((member) => member.id));
  if (!memberIDs.has(defaultMemberID))
    fail(`${path}.default_member_id`, "默认成员不存在");
  if (!Array.isArray(group.rules)) fail(`${path}.rules`, "应为数组");
  if (group.rules.length > maxModelRoutingRulesPerGroup)
    fail(`${path}.rules`, "规则过多");
  const rules = group.rules.map((rule, index) =>
    ruleAt(rule, `${path}.rules[${index}]`, memberIDs, knownDimensions),
  );
  rules.forEach((rule, index) =>
    validateTaskTypeConditionLabels(
      rule.when,
      taskTypeLabels,
      `${path}.rules[${index}].when`,
    ),
  );
  assertUniqueIDs(
    rules.map((rule) => rule.id),
    `${path}.rules`,
  );
  const priorities = rules.map((rule) => rule.priority);
  if (new Set(priorities).size !== priorities.length)
    fail(`${path}.rules`, "priority 必须唯一");
  return {
    id,
    enabled,
    members,
    default_member_id: defaultMemberID,
    rules: [...rules].sort((left, right) => left.priority - right.priority),
    ...taskTypeLabelResult,
  };
}

function assertUniqueIDs(ids: readonly string[], path: string) {
  if (new Set(ids).size !== ids.length) fail(path, "ID 必须唯一");
}

/**
 * `model_routing_groups` 的严格 parser。`undefined`/`null` 规范化为空数组
 * （旧文档没有这个 key），其余任何非法输入抛错。
 *
 * @param knownDimensions - 分类器已配置的自定义维度名称列表，用于验证规则条件中的字段引用；
 *   `null` 表示没读到，见 {@link KnownRoutingDimensions}
 */
export function parseModelRoutingGroups(
  value: unknown,
  knownDimensions: KnownRoutingDimensions = [],
): ModelRoutingGroup[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) fail("model_routing_groups", "应为数组");
  if (value.length > maxModelRoutingGroups)
    fail("model_routing_groups", "组过多");
  const groups = value.map((group, index) =>
    groupAt(group, `model_routing_groups[${index}]`, knownDimensions),
  );
  assertUniqueIDs(
    groups.map((group) => group.id),
    "model_routing_groups",
  );
  return groups;
}
