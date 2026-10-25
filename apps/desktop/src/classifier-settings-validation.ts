import {
  classifierDimensionNameMaxRunes,
  classifierEndpointProblem,
  classifierExistingModelTimeoutMaxMS,
  classifierExistingModelTimeoutMinMS,
  classifierMaxCustomDimensions,
  classifierPlaceholderParts,
  classifierPromptProblem,
  classifierTimeoutMaxMS,
  type ClassifierDimension,
  type ClassifierSettings,
} from "./classifier-settings-model";
import {
  maxModelRoutingListItems,
  modelRoutingFields,
} from "./model-routing-group-model";

/**
 * A field-level problem found before saving. `field` doubles as the wire
 * selector for the message and as the anchor for the inline marker.
 */
export interface ClassifierSettingsIssue {
  field:
    | "protocol"
    | "endpoint"
    | "model"
    | "apiKey"
    | "prompt"
    | "timeout_ms"
    | "low_confidence_threshold";
  problem: string;
}

/**
 * Field-level gate run before saving. Kept out of the model module so the
 * validation rules and their message keys are read together.
 *
 * The disabled classifier saves an incomplete draft on purpose — turning it on
 * later must not require filling everything in first.
 */
export function calculateClassifierSettingsIssues(
  settings: ClassifierSettings,
  options: { credentialConfigured: boolean; credentialDraft: string },
): ClassifierSettingsIssue[] {
  if (!settings.enabled) return [];
  const issues: ClassifierSettingsIssue[] = [];
  // Only `inline` and `existing_model` are settable from the panel. A stored
  // `provider_ref` document still reaches here (the wire accepts it), and the
  // backend rejects it with 422 while enabled, so the panel must say so rather
  // than silently rewriting the user's stored source.
  if (settings.classifier_source === "provider_ref") {
    issues.push({ field: "protocol", problem: "unsupportedSource" });
    return issues;
  }
  if (settings.classifier_source === "existing_model") {
    const { existing_model: existing } = settings;
    if (existing.service_id.trim().length === 0) {
      issues.push({ field: "model", problem: "empty" });
    }
    if (existing.model.trim().length === 0) {
      issues.push({ field: "model", problem: "empty" });
    }
    if (
      !Number.isFinite(existing.timeout_ms) ||
      existing.timeout_ms < classifierExistingModelTimeoutMinMS ||
      existing.timeout_ms > classifierExistingModelTimeoutMaxMS
    ) {
      issues.push({ field: "timeout_ms", problem: "outOfRange" });
    }
    return issues;
  }
  const { inline } = settings;
  const endpointProblem = classifierEndpointProblem(inline.endpoint);
  if (endpointProblem !== null) {
    issues.push({ field: "endpoint", problem: endpointProblem });
  }
  if (inline.model.trim().length === 0) {
    issues.push({ field: "model", problem: "empty" });
  }
  if (!options.credentialConfigured && options.credentialDraft.trim() === "") {
    issues.push({ field: "apiKey", problem: "empty" });
  }
  if (
    !Number.isFinite(inline.timeout_ms) ||
    inline.timeout_ms <= 0 ||
    inline.timeout_ms > classifierTimeoutMaxMS
  ) {
    issues.push({ field: "timeout_ms", problem: "outOfRange" });
  }
  if (
    !Number.isFinite(inline.low_confidence_threshold) ||
    inline.low_confidence_threshold < 0 ||
    inline.low_confidence_threshold > 1
  ) {
    issues.push({ field: "low_confidence_threshold", problem: "outOfRange" });
  }
  const promptProblem = classifierPromptProblem(inline.prompt);
  if (promptProblem !== null) {
    issues.push({ field: "prompt", problem: promptProblem });
  }
  return issues;
}

/**
 * 维度问题的定位锚：`index` 为维度下标，`optionIndex` 为选项下标（若适用）。
 * 与 `ClassifierSettingsIssue.field` 是同一套「问题自带定位」的思路，只是
 * 维度是列表，需要两级下标才能指向一个具体的输入框。
 */
export interface ClassifierDimensionIssue {
  index: number;
  optionIndex?: number;
  target:
    | "name"
    | "description"
    | "options"
    | "optionName"
    | "optionDescription";
  problem: string;
}

/**
 * 维度定义的字段级校验，镜像 Go 的 `ClassifierSettings.validateDimensions`
 * （`core/contract/classifier_settings.go:268`）与 `ClassifierDimension.Validate`。
 *
 * 为什么前端要做一遍：Go 侧拒绝时只回一句 `dimensions[0].name ...`，用户拿不到
 * 是哪个输入框错了。这里给出同样的判定，但带上定位锚，让输入框就地标红。
 * **权威仍然是 Go**：这里放行的写法不会比后端更宽，只会更早报错。
 */
export function calculateClassifierDimensionIssues(
  dimensions: readonly ClassifierDimension[],
): ClassifierDimensionIssue[] {
  const issues: ClassifierDimensionIssue[] = [];
  if (dimensions.length > classifierMaxCustomDimensions) {
    issues.push({
      index: classifierMaxCustomDimensions,
      target: "name",
      problem: "dimensionLimitReached",
    });
  }
  const seen = new Map<string, number>();
  dimensions.forEach((dimension, index) => {
    const name = dimension.name.trim();
    if (name.length === 0) {
      issues.push({ index, target: "name", problem: "nameEmpty" });
    } else if (runes(dimension.name) > classifierDimensionNameMaxRunes) {
      issues.push({ index, target: "name", problem: "nameTooLong" });
    } else if (dimension.name.includes("{") || dimension.name.includes("}")) {
      issues.push({ index, target: "name", problem: "nameBraces" });
    } else if (
      classifierPlaceholderParts.some(
        (part) => part !== "" && name.endsWith(`.${part}`),
      )
    ) {
      // {{dim.<name>.options}} and its siblings address one part of a
      // dimension, so such a name would read as a part of another one.
      issues.push({ index, target: "name", problem: "nameFieldSuffix" });
    } else if (dimension.name !== name) {
      issues.push({ index, target: "name", problem: "nameWhitespace" });
    } else if (name === "task_type") {
      issues.push({ index, target: "name", problem: "nameReservedTaskType" });
    } else if ((modelRoutingFields as readonly string[]).includes(name)) {
      issues.push({ index, target: "name", problem: "nameReservedBuiltin" });
    } else if (seen.has(name)) {
      issues.push({ index, target: "name", problem: "nameDuplicate" });
    }
    if (!seen.has(name)) seen.set(name, index);

    if (dimension.options.length === 0) {
      issues.push({ index, target: "options", problem: "optionsEmpty" });
      return;
    }
    if (dimension.options.length > maxModelRoutingListItems) {
      issues.push({ index, target: "options", problem: "optionsTooMany" });
    }
    const seenOptions = new Set<string>();
    dimension.options.forEach((option, optionIndex) => {
      if (option.name.trim().length === 0) {
        issues.push({
          index,
          optionIndex,
          target: "optionName",
          problem: "optionNameEmpty",
        });
        return;
      }
      if (runes(option.name) > classifierDimensionNameMaxRunes) {
        issues.push({
          index,
          optionIndex,
          target: "optionName",
          problem: "optionNameTooLong",
        });
      }
      if (seenOptions.has(option.name)) {
        issues.push({
          index,
          optionIndex,
          target: "optionName",
          problem: "optionNameDuplicate",
        });
      }
      seenOptions.add(option.name);
    });
  });
  return issues;
}

/** 与 Go 的 `utf8.RuneCountInString` 对齐：按码点计数，不是 UTF-16 单元。 */
function runes(value: string): number {
  return [...value].length;
}
