import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const invokeMock = vi.hoisted(() => vi.fn());
vi.mock("@tauri-apps/api/core", () => ({ invoke: invokeMock }));

import { getRoutingSettings, updateRoutingSettings } from "./bridge";
import { defaultFailurePolicy } from "./failure-policy-model";

const routingDocument = {
  default_failure_policy: defaultFailurePolicy(),
  strategy: "failover_only",
  max_attempts: 6,
  allow_unmatched_failover: false,
  model_routing_groups: [
    {
      id: "superclaude",
      enabled: true,
      members: [
        { id: "fast", service_id: "anthropic", model: "claude-haiku" },
        { id: "strong", service_id: "anthropic", model: "claude-sonnet" },
      ],
      default_member_id: "fast",
      rules: [
        {
          id: "billing",
          priority: 1,
          when: { field: "topic", in: ["billing"] },
          target_member_id: "strong",
        },
      ],
    },
  ],
};

const classifierDocument = {
  classifier_source: "inline",
  dimensions: [{ name: "topic", options: [{ name: "billing" }] }],
};

function route(classifier: unknown) {
  invokeMock.mockImplementation(async (command: string) => {
    if (command === "get_classifier_settings") {
      if (classifier instanceof Error) throw classifier;
      return classifier;
    }
    return routingDocument;
  });
}

// A rule naming a custom dimension is stored by Core; the bridge must parse
// it against the current dimension list, or the routing tab refuses to load
// and every save surfaces as a validation failure.
describe("routing settings with custom dimensions", () => {
  beforeEach(() => {
    invokeMock.mockReset();
    vi.stubGlobal("window", { __TAURI_INTERNALS__: {} });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("loads and saves a rule on a declared dimension", async () => {
    route(classifierDocument);
    const loaded = await getRoutingSettings();
    expect(loaded.model_routing_groups?.[0]?.rules[0]?.when).toEqual({
      field: "topic",
      in: ["billing"],
    });
    const saved = await updateRoutingSettings({});
    expect(saved.model_routing_groups?.[0]?.rules).toHaveLength(1);
  });

  it("stays strict when the classifier read fails", async () => {
    route(new Error("core offline"));
    await expect(getRoutingSettings()).rejects.toThrow();
  });
});
