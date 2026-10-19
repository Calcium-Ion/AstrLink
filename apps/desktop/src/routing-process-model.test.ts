import { beforeAll, describe, expect, it } from "vitest";

import type { ClassifierDryRunField } from "./classifier-settings-model";
import { applyLocale } from "./i18n";
import type { ModelRoutingRule } from "./model-routing-group-model";
import type { ModelRoutingDecision } from "./request-record-model";
import {
  recordRuleVerdicts,
  routingFactLine,
  ruleKeywords,
} from "./routing-process-model";

function observed(
  field: string,
  value: string | null,
  match_count = 0,
): ClassifierDryRunField {
  return {
    field,
    source: "request",
    known: true,
    match_count,
    value,
    classifier_confidence: null,
    classifier_id: null,
    skip_reason: null,
    error_code: null,
    candidates: [],
  };
}

const keywordRule: ModelRoutingRule = {
  id: "translate",
  priority: 1,
  when: {
    all: [
      { field: "literal_keyword", contains_any: ["翻译"] },
      { not: { field: "literal_keyword", contains_any: ["润色"] } },
    ],
  },
  target_member_id: "translator",
};

describe("routing process text", () => {
  beforeAll(async () => {
    await applyLocale("zh-CN");
  });

  it("reads keywords from every literal_keyword leaf", () => {
    expect(ruleKeywords(keywordRule.when)).toEqual(["翻译", "润色"]);
  });

  it("writes request facts the way the rule editor does", () => {
    expect(routingFactLine(observed("has_image", "false"), undefined)).toBe(
      "含图片：否",
    );
    expect(
      routingFactLine(
        observed("estimated_input_context_tokens", "12400"),
        undefined,
      ),
    ).toContain("12,400");
    expect(
      routingFactLine(observed("protocol", "openai.responses"), undefined),
    ).toContain("OpenAI Responses");
  });

  it("says whether the rule's keywords appeared, never the text", () => {
    const single: ModelRoutingRule = {
      ...keywordRule,
      when: { field: "literal_keyword", contains_any: ["翻译"] },
    };
    expect(routingFactLine(observed("literal_keyword", null, 0), single)).toBe(
      "用户消息里没有出现「翻译」",
    );
    expect(routingFactLine(observed("literal_keyword", null, 1), single)).toBe(
      "用户消息里出现了「翻译」",
    );
    expect(
      routingFactLine(observed("literal_keyword", null, 1), keywordRule),
    ).toBe("用户消息里出现了其中 1 个关键词");
  });

  it("leaves unknown facts to the unknown-reason line", () => {
    expect(
      routingFactLine(
        { ...observed("has_image", null), known: false },
        undefined,
      ),
    ).toBeNull();
  });

  it("turns a recorded walk into the dry run's view", () => {
    const decision = {
      rules: [
        {
          rule_id: "translate",
          priority: 1,
          target_member_id: "translator",
          state: "true",
          selected: true,
          fields: [
            {
              field: "literal_keyword",
              source: "local_text",
              known: true,
              match_count: 1,
            },
          ],
        },
        {
          rule_id: "never",
          priority: 2,
          target_member_id: "strong",
          state: "not_evaluated",
          selected: false,
        },
      ],
    } as unknown as ModelRoutingDecision;
    const rules = recordRuleVerdicts(decision);
    expect(rules[0]).toMatchObject({
      target_model: "",
      target_missing: false,
      state: "true",
    });
    expect(rules[0].fields[0]).toMatchObject({ value: null, match_count: 1 });
    expect(rules[1].fields).toEqual([]);
  });
});
