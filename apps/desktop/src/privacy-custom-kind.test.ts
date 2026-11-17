import { describe, expect, it } from "vitest";

import {
  privacyKindLabel,
  rememberCustomPrivacyKindLabels,
  sortPrivacyKinds,
} from "./privacy-kind";
import {
  customPrivacyKindMarker,
  defaultPrivacyKindRules,
  isCustomPrivacyKind,
  parsePrivacyDryRunResult,
  parsePrivacyPolicyRecord,
  privacyKindRulesAreDefault,
  resetPrivacyKindRules,
} from "./privacy-policy-model";
import { privacyKindShares } from "./privacy-summary-model";
import { extractPrivacyHits } from "./request-trajectory-model";

const etag = `"sha256:${"a".repeat(64)}"`;
const companyRule = {
  kind: "custom_company",
  enabled: true,
  style: "token",
  notify: true,
  label: "公司名称",
} as const;
const policy = {
  id: "policy_privacy_default",
  name: "隐私保护",
  enabled: true,
  priority: 0,
  detector: "regex",
  local_model_id: null,
  min_confidence: 0.6,
  regex_source: "custom",
  custom_regex_rules: [{ kind: "custom_company", pattern: "ACME-[0-9]+" }],
  request_action: "redact",
  response_action: "allow",
  response_restore: true,
  kind_rules: [...defaultPrivacyKindRules(), companyRule],
  allowlist_rules: [],
  match: {},
};

function parse(overrides: Record<string, unknown>) {
  return parsePrivacyPolicyRecord({
    policy: { ...policy, ...overrides },
    etag,
  });
}

describe("custom privacy kinds", () => {
  it("parses a declared custom kind with its label and the rules that use it", () => {
    const parsed = parse({});
    expect(parsed.policy.kind_rules.at(-1)).toEqual(companyRule);
    expect(parsed.policy.custom_regex_rules).toEqual(policy.custom_regex_rules);
    expect(customPrivacyKindMarker("custom_company")).toBe(
      "<PRIVATE_CUSTOM_COMPANY_…>",
    );
    expect(isCustomPrivacyKind("custom_company")).toBe(true);
    expect(isCustomPrivacyKind("account")).toBe(false);
    expect(isCustomPrivacyKind("custom_Company")).toBe(false);
  });

  it("rejects a custom kind without a declaration, a label, or a token style", () => {
    expect(() => parse({ kind_rules: defaultPrivacyKindRules() })).toThrow(
      /not declared/,
    );
    const { label: _label, ...unnamed } = companyRule;
    expect(() =>
      parse({ kind_rules: [...defaultPrivacyKindRules(), unnamed] }),
    ).toThrow(/display name/);
    expect(() =>
      parse({
        kind_rules: [
          ...defaultPrivacyKindRules(),
          { ...companyRule, style: "natural" },
        ],
      }),
    ).toThrow(/token/);
    expect(() =>
      parse({
        kind_rules: [
          ...defaultPrivacyKindRules().map((rule) =>
            rule.kind === "email" ? { ...rule, label: "邮箱" } : rule,
          ),
          companyRule,
        ],
      }),
    ).toThrow(/no label/);
    expect(() =>
      parse({
        kind_rules: [
          ...defaultPrivacyKindRules(),
          companyRule,
          { ...companyRule, kind: "custom_firm" },
        ],
      }),
    ).toThrow(/duplicate custom kind label/);
    // A bare word is neither a built-in kind nor a custom one.
    expect(() =>
      parse({
        custom_regex_rules: [{ kind: "organization", pattern: "ACME" }],
      }),
    ).toThrow(/regex detector kind/);
  });

  it("resets switches but keeps custom kinds declared and on", () => {
    const rules = [
      ...defaultPrivacyKindRules(),
      { ...companyRule, enabled: false, notify: false },
    ];
    expect(privacyKindRulesAreDefault(rules)).toBe(false);
    const reset = resetPrivacyKindRules(rules);
    expect(reset.at(-1)).toEqual(companyRule);
    expect(privacyKindRulesAreDefault(reset)).toBe(true);
  });

  it("parses custom kinds in dry-run findings and redactions", () => {
    const result = parsePrivacyDryRunResult({
      decision: "redact",
      findings_summary: "custom_company=1",
      findings: [
        {
          kind: "custom_company",
          path: "/messages/0/content",
          start: 0,
          end: 9,
          confidence: 1,
        },
      ],
      suppressed_findings: [],
      redactions: [
        {
          placeholder: "<PRIVATE_CUSTOM_COMPANY_0123456789abcdef>",
          kind: "custom_company",
          value: "ACME-7731",
          style: "token",
        },
      ],
      inspected_body: "{}",
    });
    expect(result.findings[0].kind).toBe("custom_company");
    expect(result.redactions?.[0].kind).toBe("custom_company");
  });

  it("names custom kinds by their label in statistics and records", () => {
    rememberCustomPrivacyKindLabels([companyRule]);
    const { rows } = privacyKindShares([
      { kind: "custom_company", count: 3 },
      { kind: "account", count: 1 },
    ]);
    expect(rows.map((row) => row.label)).toEqual(["公司名称", "账号"]);
    // A kind the policy no longer declares still reads as its own.
    expect(privacyKindLabel("custom_agency")).toBe("自定义类型 agency");
    expect(sortPrivacyKinds(["unknown", "custom_company", "email"])).toEqual([
      "email",
      "custom_company",
      "unknown",
    ]);

    const hits = extractPrivacyHits(
      "ask <PRIVATE_CUSTOM_COMPANY_0123456789abcdef> and <PRIVATE_EMAIL_0123456789abcdef>",
    );
    expect(hits.map((hit) => [hit.kind, hit.label, hit.count])).toEqual([
      ["email", "邮箱", 1],
      ["custom_company", "公司名称", 1],
    ]);
    rememberCustomPrivacyKindLabels([]);
  });
});
