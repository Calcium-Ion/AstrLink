import type {
  ClassifierDryRunDimension,
  ClassifierDryRunField,
  ClassifierDryRunRule,
} from "./classifier-settings-model";
import { i18n } from "./i18n";
import { modelRoutingFieldKind } from "./model-routing-catalog-model";
import {
  isModelRoutingLeaf,
  type ModelRoutingCondition,
  type ModelRoutingField,
  type ModelRoutingGroup,
  type ModelRoutingRule,
} from "./model-routing-group-model";
import { routingFieldName } from "./model-routing-phrase-text";
import type {
  ModelRoutingDecision,
  ModelRoutingFieldObservation,
} from "./request-record-model";
import { modelRoutingFieldValueLabel } from "./request-model-routing-model";

/**
 * 试运行和请求记录用同一套画法讲路由过程。两边的数据形状不同，这里把请求记录
 * 换成试运行的视图类型，再提供两边共用的文字：规则行下面写的依据、分类器回答的取值。
 */

export function percent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

/** 规则里 `literal_keyword` 叶子写的关键词，按书写顺序。 */
export function ruleKeywords(condition: ModelRoutingCondition): string[] {
  if (isModelRoutingLeaf(condition)) {
    if (condition.field !== "literal_keyword") return [];
    const leaf = condition as {
      contains_any?: string[];
      not_contains_any?: string[];
    };
    return [...(leaf.contains_any ?? []), ...(leaf.not_contains_any ?? [])];
  }
  if ("not" in condition) return ruleKeywords(condition.not);
  return [...(condition.all ?? []), ...(condition.any ?? [])].flatMap(
    ruleKeywords,
  );
}

/** 记录下来的取值，用规则编辑器的说法：是 / 否、12,400、OpenAI Responses。 */
export function routingFactValueText(field: string, value: string): string {
  let kind = "label";
  try {
    kind = modelRoutingFieldKind(field as ModelRoutingField);
  } catch {
    // 自定义维度不在内建目录里，按标签处理。
  }
  if (kind === "boolean")
    return i18n.t(
      value === "true" ? "routingProcess.yes" : "routingProcess.no",
    );
  if (kind === "number") {
    const number = Number(value);
    return Number.isFinite(number)
      ? number.toLocaleString(i18n.language)
      : value;
  }
  if (kind === "time") return value;
  return modelRoutingFieldValueLabel(field, value);
}

/**
 * 一条规则判定下面写的依据：条件读到的取值。关键词条件只说规则里的关键词出没出现，
 * 记录里本来也没有请求原文。分类器字段由调用方按分类器的回答单独给出（带置信度）。
 */
export function routingFactLine(
  field: ClassifierDryRunField,
  rule: ModelRoutingRule | undefined,
): string | null {
  if (!field.known) return null;
  if (field.field === "literal_keyword") {
    const keywords = rule ? [...new Set(ruleKeywords(rule.when))] : [];
    if (keywords.length === 1)
      return i18n.t(
        field.match_count > 0
          ? "routingProcess.keywordFound"
          : "routingProcess.keywordMissing",
        { keyword: keywords[0] },
      );
    return field.match_count > 0
      ? i18n.t("routingProcess.keywordsFound", { count: field.match_count })
      : i18n.t("routingProcess.keywordsMissing");
  }
  if (field.value === null) return null;
  return i18n.t("dryRunTab.classifierValue", {
    field: routingFieldName(field.field),
    value: routingFactValueText(field.field, field.value),
  });
}

/** 分类器字段在规则行下面显示的取值，例如「通用（95%）」。 */
export function classifierValueTexts(
  taskType: string,
  confidence: number | null,
  dimensions: readonly ClassifierDryRunDimension[],
): Map<string, string> {
  const values = new Map<string, string>();
  if (taskType !== "")
    values.set(
      "task_type",
      confidence === null
        ? modelRoutingFieldValueLabel("task_type", taskType)
        : i18n.t("dryRunTab.valueWithScore", {
            value: modelRoutingFieldValueLabel("task_type", taskType),
            score: percent(confidence),
          }),
    );
  for (const dimension of dimensions)
    if (dimension.known) values.set(dimension.name, dimension.label);
  return values;
}

function recordField(
  field: ModelRoutingFieldObservation,
): ClassifierDryRunField {
  return {
    field: field.field,
    source: field.source,
    known: field.known,
    match_count: field.match_count ?? 0,
    value: field.value ?? null,
    classifier_confidence: field.classifier_confidence ?? null,
    classifier_id: field.classifier_id ?? null,
    skip_reason: field.skip_reason ?? null,
    error_code: field.error_code ?? null,
    candidates: [],
  };
}

/** 请求记录里的逐条判定，换成试运行的视图类型。 */
export function recordRuleVerdicts(
  decision: ModelRoutingDecision,
): ClassifierDryRunRule[] {
  return (decision.rules ?? []).map((rule) => ({
    rule_id: rule.rule_id,
    priority: rule.priority,
    target_member_id: rule.target_member_id,
    target_model: rule.target_model ?? "",
    state: rule.state,
    selected: rule.selected,
    target_missing: rule.target_missing ?? false,
    fields: (rule.fields ?? []).map(recordField),
  }));
}

/** 请求记录里分类器对自定义维度的回答。 */
export function recordDimensions(
  decision: ModelRoutingDecision,
): ClassifierDryRunDimension[] {
  return (decision.classifier?.dimensions ?? []).map((dimension) => ({
    name: dimension.name,
    label: dimension.label ?? "",
    score: dimension.score ?? 0,
    known: dimension.known,
    produced: dimension.produced,
  }));
}

/** 一条规则在组配置里的位置（从 1 开始），找不到时为 null。 */
export function ruleNumber(
  group: ModelRoutingGroup | null,
  ruleID: string | undefined,
): number | null {
  if (!group || !ruleID) return null;
  const index = group.rules.findIndex((rule) => rule.id === ruleID);
  return index < 0 ? null : index + 1;
}

/** 组里某个成员的模型名；成员不在组里时为空。 */
export function memberModel(
  group: ModelRoutingGroup | null,
  memberID: string,
): string {
  return group?.members.find((member) => member.id === memberID)?.model ?? "";
}
