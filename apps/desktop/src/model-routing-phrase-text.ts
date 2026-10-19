import { i18n } from "./i18n";
import type { ClassifierDimension } from "./classifier-settings-model";
import type { RoutingPhrase } from "./model-routing-editor-model";
import { modelRoutingFieldValueLabel } from "./request-model-routing-model";

/**
 * 短语的显示文字：规则行、试运行结果和路由组列表的摘要都从这里取，同一个
 * 条件在三处读起来一样。
 */

function text(key: string, fallback: string): string {
  return i18n.exists(key) ? i18n.t(key) : fallback;
}

/** 选择器里的字段名；自定义维度没有词条，直接用维度名。 */
export function routingFieldName(field: string): string {
  return text(`routingConditions.fieldNames.${field}`, field);
}

/** 选择器里字段名下面那一行说明；自定义维度取它自己的说明。 */
export function routingFieldHint(
  field: string,
  dimensions: readonly ClassifierDimension[] = [],
): string {
  const dimension = dimensions.find((item) => item.name === field);
  if (dimension) return dimension.description ?? "";
  return text(`routingConditions.fieldHints.${field}`, "");
}

export function routingValueLabel(field: string, value: string): string {
  return modelRoutingFieldValueLabel(field, value);
}

/** token 数按 K / M 写，正好整除时才缩写，避免把 64500 写成 64K。 */
export function formatRoutingCount(field: string, value: number): string {
  if (field === "estimated_input_context_tokens") {
    if (value >= 1_000_000 && value % 1_000_000 === 0)
      return `${value / 1_000_000}M`;
    if (value >= 1_000 && value % 1_000 === 0) return `${value / 1_000}K`;
  }
  return value.toLocaleString();
}

function withUnit(field: string, value: string): string {
  const unit = text(`routingConditions.units.${field}`, "");
  return unit ? `${value} ${unit}` : value;
}

export interface RoutingPhraseParts {
  /** 字段与比较方式，显示为浅色。布尔类短语没有这一段。 */
  label?: string;
  /** 取值，显示为深色。 */
  value: string;
  /** 取值还没填。 */
  empty: boolean;
}

export function routingPhraseParts(phrase: RoutingPhrase): RoutingPhraseParts {
  const notSet = i18n.t("routingConditions.phrase.notSet");
  const separator = i18n.t("routingConditions.phrase.listSeparator");
  const name = routingFieldName(phrase.field);
  switch (phrase.kind) {
    case "options":
      return {
        label: `${name} ${i18n.t(
          phrase.negate
            ? "routingConditions.phrase.isNot"
            : "routingConditions.phrase.is",
        )}`,
        value:
          phrase.values.length === 0
            ? notSet
            : phrase.values
                .map((value) => routingValueLabel(phrase.field, value))
                .join(separator),
        empty: phrase.values.length === 0,
      };
    case "boolean":
      return {
        value: text(
          `routingConditions.boolean.${phrase.field}.${phrase.value}`,
          `${name} ${i18n.t(
            phrase.value
              ? "routingConditions.phrase.is"
              : "routingConditions.phrase.isNot",
          )}`,
        ),
        empty: false,
      };
    case "count": {
      const short = text(`routingConditions.phraseNames.${phrase.field}`, name);
      const format = (value: number) => formatRoutingCount(phrase.field, value);
      if (phrase.min !== undefined && phrase.max !== undefined)
        return {
          label: short,
          value: withUnit(
            phrase.field,
            `${format(phrase.min)}–${format(phrase.max)}`,
          ),
          empty: false,
        };
      if (phrase.min !== undefined)
        return {
          label: `${short} ${i18n.t("routingConditions.phrase.atLeast")}`,
          value: withUnit(phrase.field, format(phrase.min)),
          empty: false,
        };
      if (phrase.max !== undefined)
        return {
          label: `${short} ${i18n.t("routingConditions.phrase.atMost")}`,
          value: withUnit(phrase.field, format(phrase.max)),
          empty: false,
        };
      return { label: short, value: notSet, empty: true };
    }
    case "keywords":
      return {
        label: i18n.t(
          phrase.negate
            ? "routingConditions.phrase.notContains"
            : "routingConditions.phrase.contains",
        ),
        value:
          phrase.keywords.length === 0
            ? notSet
            : phrase.keywords.join(separator),
        empty: phrase.keywords.length === 0,
      };
    case "time": {
      const complete = phrase.start !== "" && phrase.end !== "";
      return {
        label: name,
        value: complete ? `${phrase.start}–${phrase.end}` : notSet,
        empty: !complete,
      };
    }
  }
}

/** 一整句的纯文本，给无障碍名称与列表摘要用。 */
export function routingPhraseText(phrase: RoutingPhrase): string {
  const parts = routingPhraseParts(phrase);
  return parts.label ? `${parts.label} ${parts.value}` : parts.value;
}
