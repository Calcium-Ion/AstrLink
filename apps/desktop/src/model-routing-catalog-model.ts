/**
 * 规则字段的形状，以及「分类器」页签上任务类型选项的视图模型。
 *
 * 与 `./model-groups.ts` 无关：那个文件是把模型名按厂商分桶的列表浏览助手。
 * 权威定义在 `./model-routing-group-model.ts` 的操作符矩阵里（它镜像 Go）。
 * 本文件**不新增第三份枚举**：字段形状与取值域都从那里派生。
 *
 * 任务类型的选项存在每个路由组的 `task_type_labels` 上，「分类器」页签把它们
 * 当作一份共享列表编辑，保存时写进每个组；这里提供读出这份列表与保存前预检。
 */

import {
  fieldOperators,
  isModelRoutingRangeField,
  isModelRoutingTimeField,
  modelRoutingFieldValues,
  maxModelRoutingTaskTypeLabels,
  type ModelRoutingCondition,
  type ModelRoutingField,
  type ModelRoutingGroup,
  type ModelRoutingValueCondition,
} from "./model-routing-group-model";

/** 字段的值形状：标签、数值、是否或时间，决定规则编辑器给哪种控件。 */
export type ModelRoutingFieldKind = "label" | "number" | "boolean" | "time";

/**
 * 判定顺序与 Go 的比较点一致：时间字段既不是区间也不是列表，必须**先判时间**，
 * 否则 `within` 会被 `isModelRoutingRangeField` 的 `gte`/`lte` 分支吃掉。
 */
export function modelRoutingFieldKind(
  field: ModelRoutingField,
): ModelRoutingFieldKind {
  if (isModelRoutingTimeField(field)) return "time";
  if (isModelRoutingRangeField(field)) return "number";
  const operators = fieldOperators(field);
  return operators.includes("equals_bool") ? "boolean" : "label";
}

/**
 * `task_type` 的**当前生效**选项：第一个设置了 `task_type_labels` 的组为准，
 * 都没设置时是内建默认。各组不一致时由 `modelRoutingTaskTypeLabelsDiverge` 指出。
 */
export function modelRoutingCatalogLabelDomain(
  overrides: readonly (readonly string[] | undefined)[],
): readonly string[] {
  for (const labels of overrides) {
    if (labels && labels.length > 0) return labels;
  }
  return modelRoutingFieldValues("task_type") ?? [];
}

/** 标签编辑的一个问题；`label` 为出问题的标签，`group` 为引用它的组。 */
export interface TaskTypeLabelIssue {
  problem: "empty" | "tooLong" | "duplicate" | "tooMany" | "none" | "inUse";
  label?: string;
  group?: string;
}

/** 与 Go `MaxModelRoutingValueRunes` 一致：标签也是一个规则取值。 */
export const maxModelRoutingTaskTypeLabelRunes = 128;

function referencedTaskTypeLabels(condition: ModelRoutingCondition): string[] {
  if ("field" in condition) {
    if (condition.field !== "task_type") return [];
    const value = condition as ModelRoutingValueCondition;
    return [
      ...(value.in ?? []),
      ...(value.not_in ?? []),
      ...(value.equals ? [value.equals] : []),
      ...(value.not_equals ? [value.not_equals] : []),
    ];
  }
  if ("not" in condition) return referencedTaskTypeLabels(condition.not);
  const children =
    "all" in condition ? (condition.all ?? []) : (condition.any ?? []);
  return children.flatMap(referencedTaskTypeLabels);
}

/**
 * 「分类器」页签上编辑的 task_type 选项列表的预检，镜像 Go 的
 * `validateModelRoutingTaskTypeLabels` 与「规则取值必须在组标签集合里」两条。
 *
 * 第二条是这里特有的：页签把同一份列表写进每个组，删掉某个组的规则仍在用的
 * 选项会让整份路由设置被后端拒收，所以在本地就指出是哪个组在用。
 */
export function calculateTaskTypeLabelIssues(
  labels: readonly string[],
  groups: readonly ModelRoutingGroup[],
): TaskTypeLabelIssue[] {
  const issues: TaskTypeLabelIssue[] = [];
  if (labels.length === 0) issues.push({ problem: "none" });
  if (labels.length > maxModelRoutingTaskTypeLabels)
    issues.push({ problem: "tooMany" });
  const seen = new Set<string>();
  for (const label of labels) {
    if (label.trim() === "") issues.push({ problem: "empty" });
    else if ([...label].length > maxModelRoutingTaskTypeLabelRunes)
      issues.push({ problem: "tooLong", label });
    else if (seen.has(label)) issues.push({ problem: "duplicate", label });
    seen.add(label);
  }
  for (const group of groups) {
    const used = new Set(
      group.rules.flatMap((rule) => referencedTaskTypeLabels(rule.when)),
    );
    for (const label of used)
      if (!seen.has(label))
        issues.push({ problem: "inUse", label, group: group.id });
  }
  return issues;
}

/**
 * 维度编辑的一个问题：草稿删掉或改名了已保存的维度或它的某个选项，`group` 的
 * 规则却还在用。`option` 缺省时说的是整个维度。
 */
export interface DimensionUseIssue {
  dimension: string;
  option?: string;
  group: string;
}

interface DimensionShape {
  name: string;
  options: readonly { name: string }[];
}

/** 规则里每个字段用到的取值，用来判断维度和它的选项是否还被引用。 */
function collectValues(
  condition: ModelRoutingCondition,
  into: Map<string, Set<string>>,
): void {
  if ("field" in condition) {
    const leaf = condition as ModelRoutingValueCondition;
    const values = into.get(condition.field) ?? new Set<string>();
    for (const value of [
      ...(leaf.in ?? []),
      ...(leaf.not_in ?? []),
      ...(leaf.equals ? [leaf.equals] : []),
      ...(leaf.not_equals ? [leaf.not_equals] : []),
    ])
      values.add(value);
    into.set(condition.field, values);
    return;
  }
  if ("not" in condition) {
    collectValues(condition.not, into);
    return;
  }
  const children =
    "all" in condition ? (condition.all ?? []) : (condition.any ?? []);
  for (const child of children) collectValues(child, into);
}

/**
 * 「分类器」页签保存维度前的预检。和 Go 的 `ValidateClassifierDimensionRemoval`
 * 一样只看这次相对已保存版本删掉或改名的部分，不为早已存在的引用拦下与之无关
 * 的保存（比如只改了密钥）。
 *
 * 删掉仍被引用的维度会被后端拒收（`classifier_dimension_in_use`）：路由设置每
 * 次读取都按已声明的维度校验，悬空的规则会让所有请求失败。删掉仍被引用的选项
 * 后端不拦，但那条规则从此再也不会命中。两种都在本地指出是哪个组在用。
 */
export function calculateDimensionUseIssues(
  saved: readonly DimensionShape[],
  draft: readonly DimensionShape[],
  groups: readonly ModelRoutingGroup[],
): DimensionUseIssue[] {
  const drafted = new Map(
    draft.map((dimension) => [
      dimension.name,
      new Set(dimension.options.map((option) => option.name)),
    ]),
  );
  const issues: DimensionUseIssue[] = [];
  for (const group of groups) {
    const used = new Map<string, Set<string>>();
    for (const rule of group.rules) collectValues(rule.when, used);
    for (const dimension of saved) {
      const values = used.get(dimension.name);
      if (values === undefined) continue;
      const options = drafted.get(dimension.name);
      if (options === undefined) {
        issues.push({ dimension: dimension.name, group: group.id });
        continue;
      }
      for (const option of dimension.options)
        if (values.has(option.name) && !options.has(option.name))
          issues.push({
            dimension: dimension.name,
            option: option.name,
            group: group.id,
          });
    }
  }
  return issues;
}

/** 各组是否已经共用同一份标签（未覆盖的组按内建默认域计）。 */
export function modelRoutingTaskTypeLabelsDiverge(
  overrides: readonly (readonly string[] | undefined)[],
): boolean {
  const fallback = modelRoutingFieldValues("task_type") ?? [];
  const keys = new Set(
    overrides.map((labels) =>
      JSON.stringify(labels && labels.length > 0 ? labels : fallback),
    ),
  );
  return keys.size > 1;
}
