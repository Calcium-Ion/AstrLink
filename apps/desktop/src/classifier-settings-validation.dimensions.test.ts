import { describe, expect, it } from "vitest";

import {
  classifierDimensionNameMaxRunes,
  classifierMaxCustomDimensions,
  type ClassifierDimension,
} from "./classifier-settings-model";
import { calculateClassifierDimensionIssues } from "./classifier-settings-validation";

/**
 * These rules mirror `ClassifierSettings.validateDimensions` and
 * `ClassifierDimension.Validate` in `core/contract/classifier_settings.go`. The
 * point of the mirror is a message that names the offending input rather than
 * one line of Go text, so the cases below are the Go rejections, one each.
 */
const dimension = (
  patch: Partial<ClassifierDimension> = {},
): ClassifierDimension => ({
  name: "topic",
  options: [{ name: "billing" }],
  ...patch,
});

const problems = (dimensions: ClassifierDimension[]) =>
  calculateClassifierDimensionIssues(dimensions).map((issue) => issue.problem);

describe("classifier dimension validation", () => {
  it("accepts a well-formed list", () => {
    expect(calculateClassifierDimensionIssues([dimension()])).toEqual([]);
    expect(
      calculateClassifierDimensionIssues([
        dimension({ name: "topic" }),
        dimension({
          name: "urgency",
          options: [{ name: "high" }, { name: "low" }],
        }),
      ]),
    ).toEqual([]);
  });

  it("mirrors the 5-custom-dimension ceiling", () => {
    // 5 custom + the built-in task_type is the confirmed 6-dimension total, so
    // the sixth custom entry is the first rejected one.
    const accepted = Array.from(
      { length: classifierMaxCustomDimensions },
      (_, index) => dimension({ name: `dim_${index}` }),
    );
    expect(problems(accepted)).toEqual([]);
    expect(
      problems([...accepted, dimension({ name: "one_too_many" })]),
    ).toEqual(["dimensionLimitReached"]);
  });

  it("rejects an empty name", () => {
    expect(problems([dimension({ name: "" })])).toEqual(["nameEmpty"]);
    expect(problems([dimension({ name: "   " })])).toEqual(["nameEmpty"]);
  });

  it("rejects a name over the rune budget, counting code points", () => {
    expect(
      problems([
        dimension({ name: "x".repeat(classifierDimensionNameMaxRunes) }),
      ]),
    ).toEqual([]);
    expect(
      problems([
        dimension({ name: "x".repeat(classifierDimensionNameMaxRunes + 1) }),
      ]),
    ).toEqual(["nameTooLong"]);
    // Go counts runes, so a 64-character CJK name fits even though it is 192
    // UTF-16 units; counting `.length` here would reject a legal name.
    expect(problems([dimension({ name: "字".repeat(64) })])).toEqual([]);
  });

  it("rejects braces and surrounding whitespace in a name", () => {
    // Braces delimit {{dim.<name>}}, so a name containing one is unaddressable.
    expect(problems([dimension({ name: "to{pic" })])).toEqual(["nameBraces"]);
    expect(problems([dimension({ name: "topic}" })])).toEqual(["nameBraces"]);
    // The token grammar trims the name it reads, so the padded name would be
    // stored but never match a placeholder. Interior spaces are fine.
    expect(problems([dimension({ name: " topic" })])).toEqual([
      "nameWhitespace",
    ]);
    expect(problems([dimension({ name: "my topic" })])).toEqual([]);
  });

  it("rejects a name ending in a placeholder part, as Go does", () => {
    // {{dim.topic.options}} addresses a part of topic, so a dimension named
    // "topic.options" could never be addressed as a whole.
    for (const name of ["topic.name", "topic.description", "topic.options"]) {
      expect(problems([dimension({ name })])).toEqual(["nameFieldSuffix"]);
    }
    expect(problems([dimension({ name: "options" })])).toEqual([]);
    expect(problems([dimension({ name: "a. options" })])).toEqual([]);
  });

  it("rejects a name that a built-in would shadow", () => {
    expect(problems([dimension({ name: "task_type" })])).toEqual([
      "nameReservedTaskType",
    ]);
    // Every other built-in field resolves before dimensions in all three
    // runtimes, so a rule on it would read the request, not the classifier.
    expect(problems([dimension({ name: "protocol" })])).toEqual([
      "nameReservedBuiltin",
    ]);
    expect(problems([dimension({ name: "system_time" })])).toEqual([
      "nameReservedBuiltin",
    ]);
  });

  it("rejects a duplicate dimension name on the second entry", () => {
    const issues = calculateClassifierDimensionIssues([
      dimension({ name: "topic" }),
      dimension({ name: "topic" }),
    ]);
    expect(issues).toEqual([
      { index: 1, target: "name", problem: "nameDuplicate" },
    ]);
  });

  it("rejects a dimension with no options at all", () => {
    expect(problems([dimension({ options: [] })])).toEqual(["optionsEmpty"]);
  });

  it("rejects an empty, over-long or duplicated option name", () => {
    expect(problems([dimension({ options: [{ name: "" }] })])).toEqual([
      "optionNameEmpty",
    ]);
    expect(
      problems([dimension({ options: [{ name: "x".repeat(65) }] })]),
    ).toEqual(["optionNameTooLong"]);
    expect(
      problems([dimension({ options: [{ name: "a" }, { name: "a" }] })]),
    ).toEqual(["optionNameDuplicate"]);
  });

  it("points every problem at the offending input", () => {
    // The anchor is what the panel turns into an inline marker, so a problem
    // without one would be silently invisible.
    const issues = calculateClassifierDimensionIssues([
      dimension({ name: "topic", options: [{ name: "a" }, { name: "a" }] }),
      dimension({ name: "protocol", options: [{ name: "" }] }),
    ]);
    expect(issues).toEqual([
      {
        index: 0,
        optionIndex: 1,
        target: "optionName",
        problem: "optionNameDuplicate",
      },
      { index: 1, target: "name", problem: "nameReservedBuiltin" },
      {
        index: 1,
        optionIndex: 0,
        target: "optionName",
        problem: "optionNameEmpty",
      },
    ]);
  });

  it("reports an over-full option list rather than accepting it", () => {
    const options = Array.from({ length: 17 }, (_, index) => ({
      name: `option_${index}`,
    }));
    expect(problems([dimension({ options })])).toEqual(["optionsTooMany"]);
  });
});
