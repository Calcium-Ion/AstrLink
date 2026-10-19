import { describe, expect, it } from "vitest";

import { parseModelRoutingGroupVersion } from "./model-routing-version-model";

const group = {
  id: "program",
  enabled: true,
  members: [{ id: "fast", service_id: "service_a", model: "model_a" }],
  default_member_id: "fast",
  rules: [
    {
      id: "billing",
      priority: 1,
      // A dimension deleted after this version was saved.
      when: { field: "topic", in: ["billing"] },
      target_member_id: "fast",
    },
  ],
};

describe("parseModelRoutingGroupVersion", () => {
  it("reads an old configuration, even one naming a dimension that is gone", () => {
    const parsed = parseModelRoutingGroupVersion({
      group_id: "program",
      version: 3,
      latest_version: 5,
      created_at: "2026-10-18T18:20:00.000000000Z",
      group,
    });
    expect(parsed.version).toBe(3);
    expect(parsed.latest_version).toBe(5);
    expect(parsed.group.rules[0].when).toEqual({
      field: "topic",
      in: ["billing"],
    });
    expect(parsed.document).toEqual(group);
  });

  it("rejects a version that is not a positive number", () => {
    expect(() =>
      parseModelRoutingGroupVersion({
        group_id: "program",
        version: 0,
        latest_version: 1,
        created_at: "2026-10-18T18:20:00Z",
        group,
      }),
    ).toThrow();
  });
});
