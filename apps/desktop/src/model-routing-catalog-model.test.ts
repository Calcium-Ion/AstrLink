import { describe, expect, it } from "vitest";

import {
  calculateDimensionUseIssues,
  calculateTaskTypeLabelIssues,
  modelRoutingCatalogLabelDomain,
  modelRoutingFieldKind,
  modelRoutingTaskTypeLabelsDiverge,
} from "./model-routing-catalog-model";
import type { ModelRoutingGroup } from "./model-routing-group-model";

function group(id: string, values: string[] = []): ModelRoutingGroup {
  return {
    id,
    enabled: true,
    members: [{ id: "a", service_id: "s", model: "m" }],
    default_member_id: "a",
    rules: values.length
      ? [
          {
            id: "r",
            priority: 10,
            target_member_id: "a",
            when: { not: { field: "task_type", in: values } },
          },
        ]
      : [],
  };
}

describe("model routing field shapes", () => {
  it("classifies the time field as time, not as a numeric range", () => {
    // `system_time` carries `within`, which is not a range operator. Deciding
    // time after the range check would hand it to the numeric editor and the
    // save would be rejected for an unsupported operator.
    expect(modelRoutingFieldKind("system_time")).toBe("time");
    expect(modelRoutingFieldKind("turn_count")).toBe("number");
    expect(modelRoutingFieldKind("task_type")).toBe("label");
    expect(modelRoutingFieldKind("streaming")).toBe("boolean");
  });
});

describe("task type option list", () => {
  it("prefers a group override and falls back to the built-in label domain", () => {
    expect(modelRoutingCatalogLabelDomain([])).toEqual([
      "general",
      "research",
      "coding",
      "architect",
    ]);
    // An empty override array is "not set" in the editor's own reading, so it
    // must not shadow the built-in domain with an empty one.
    expect(modelRoutingCatalogLabelDomain([[], undefined])).toEqual([
      "general",
      "research",
      "coding",
      "architect",
    ]);
    expect(
      modelRoutingCatalogLabelDomain([undefined, ["custom", "coding"]]),
    ).toEqual(["custom", "coding"]);
  });

  it("names the group whose rule still uses a dropped option, even under not", () => {
    expect(
      calculateTaskTypeLabelIssues(
        ["general", "research"],
        [group("g1", ["coding"])],
      ),
    ).toEqual([{ problem: "inUse", label: "coding", group: "g1" }]);
  });

  it("flags an empty list, blank and duplicate options", () => {
    expect(calculateTaskTypeLabelIssues([], [])).toEqual([{ problem: "none" }]);
    expect(calculateTaskTypeLabelIssues(["a", " ", "a"], [])).toEqual([
      { problem: "empty" },
      { problem: "duplicate", label: "a" },
    ]);
  });

  it("treats groups without an override as using the built-in list", () => {
    expect(modelRoutingTaskTypeLabelsDiverge([undefined, []])).toBe(false);
    expect(
      modelRoutingTaskTypeLabelsDiverge([
        undefined,
        ["general", "research", "coding", "architect"],
      ]),
    ).toBe(false);
    expect(modelRoutingTaskTypeLabelsDiverge([undefined, ["writing"]])).toBe(
      true,
    );
  });
});

describe("dimension removal", () => {
  const topic = {
    name: "topic",
    options: [{ name: "legal" }, { name: "billing" }],
  };
  const topical: ModelRoutingGroup = {
    ...group("g1"),
    rules: [
      {
        id: "r",
        priority: 10,
        target_member_id: "a",
        when: {
          all: [
            { field: "has_image", equals_bool: true },
            { not: { field: "topic", in: ["legal"] } },
          ],
        },
      },
    ],
  };

  it("names the group whose rule still uses a removed or renamed dimension", () => {
    expect(calculateDimensionUseIssues([topic], [], [topical])).toEqual([
      { dimension: "topic", group: "g1" },
    ]);
    expect(
      calculateDimensionUseIssues(
        [topic],
        [{ ...topic, name: "subject" }],
        [topical],
      ),
    ).toEqual([{ dimension: "topic", group: "g1" }]);
  });

  it("names the group whose rule still uses a removed or renamed option", () => {
    expect(
      calculateDimensionUseIssues(
        [topic],
        [{ name: "topic", options: [{ name: "law" }, { name: "billing" }] }],
        [topical],
      ),
    ).toEqual([{ dimension: "topic", option: "legal", group: "g1" }]);
  });

  it("accepts removing what no rule uses", () => {
    expect(
      calculateDimensionUseIssues(
        [topic, { name: "tone", options: [{ name: "calm" }] }],
        [{ name: "topic", options: [{ name: "legal" }] }],
        [topical, group("g2")],
      ),
    ).toEqual([]);
  });

  it("leaves references that were already dangling to Core", () => {
    // Nothing was removed in this draft, so an unrelated save goes through.
    expect(calculateDimensionUseIssues([], [], [topical])).toEqual([]);
  });
});
