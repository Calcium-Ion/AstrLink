import { describe, expect, it } from "vitest";

import {
  filterModels,
  groupModels,
  groupModelsByCategory,
  modelCategoryKey,
  modelGroupKey,
} from "./model-groups";

describe("modelCategoryKey", () => {
  it("uses the org for provider-prefixed IDs", () => {
    expect(modelCategoryKey("openai/gpt-4o")).toBe("openai");
  });

  it("uses the first hyphen segment for product families", () => {
    expect(modelCategoryKey("claude-sonnet-4-5")).toBe("claude");
    expect(modelCategoryKey("claude-fable-preview")).toBe("claude");
    expect(modelCategoryKey("gpt-5.6-sol")).toBe("gpt");
    expect(modelCategoryKey("gemini-3.6-flash")).toBe("gemini");
  });
});

describe("modelGroupKey", () => {
  it("groups provider-prefixed IDs by org", () => {
    expect(modelGroupKey("openai/gpt-4o")).toBe("openai");
  });

  it("keeps version-like second segments in the family key", () => {
    expect(modelGroupKey("gemini-3.6-flash")).toBe("gemini-3.6");
    expect(modelGroupKey("gpt-5.6-sol")).toBe("gpt-5.6");
  });

  it("groups alphabetic product families", () => {
    expect(modelGroupKey("claude-sonnet-4-5-20250929-thinking")).toBe(
      "claude-sonnet",
    );
    expect(modelGroupKey("claude-haiku-4-5-20251001")).toBe("claude-haiku");
    expect(modelGroupKey("claude-opus-4-7")).toBe("claude-opus");
  });
});

describe("filterModels", () => {
  it("matches case-insensitively and returns a copy when empty", () => {
    const models = ["GPT-4o", "claude-sonnet"];
    expect(filterModels(models, "gpt")).toEqual(["GPT-4o"]);
    expect(filterModels(models, "  ")).toEqual(models);
    expect(filterModels(models, "  ")).not.toBe(models);
  });
});

describe("groupModels", () => {
  it("buckets and sorts groups", () => {
    expect(
      groupModels([
        "claude-opus-4-7",
        "claude-sonnet-4-5",
        "claude-opus-4-6",
        "gpt-5",
      ]),
    ).toEqual([
      { key: "claude-opus", models: ["claude-opus-4-6", "claude-opus-4-7"] },
      { key: "claude-sonnet", models: ["claude-sonnet-4-5"] },
      { key: "gpt-5", models: ["gpt-5"] },
    ]);
  });
});

describe("groupModelsByCategory", () => {
  it("nests product families under a vendor category", () => {
    expect(
      groupModelsByCategory([
        "claude-fable-preview",
        "claude-opus-4-7",
        "claude-sonnet-4-5",
        "gpt-5",
        "gpt-5-mini",
        "openai/gpt-4o",
      ]),
    ).toEqual([
      {
        key: "claude",
        groups: [
          { key: "claude-fable", models: ["claude-fable-preview"] },
          { key: "claude-opus", models: ["claude-opus-4-7"] },
          { key: "claude-sonnet", models: ["claude-sonnet-4-5"] },
        ],
      },
      {
        key: "gpt",
        groups: [{ key: "gpt-5", models: ["gpt-5", "gpt-5-mini"] }],
      },
      {
        key: "openai",
        groups: [{ key: "openai", models: ["openai/gpt-4o"] }],
      },
    ]);
  });
});
