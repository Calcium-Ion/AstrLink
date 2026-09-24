import { describe, expect, it } from "vitest";
import {
  defaultFailurePolicy,
  identitySettingKeys,
  modelRedirectIssues,
  parseFailurePolicy,
  parseFailoverPolicy,
  parseRoutingSettings,
  type ModelRedirect,
} from "./failure-policy-model";

describe("failure policies", () => {
  it("defaults identity enforcement on for older settings and preserves explicit opt-out", () => {
    const settings = {
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: true,
      strategy: "failover_only",
      max_attempts: 6,
    };
    for (const key of identitySettingKeys) {
      expect(parseRoutingSettings(settings)[key]).toBe(true);
      expect(parseRoutingSettings({ ...settings, [key]: false })[key]).toBe(
        false,
      );
      for (const value of [null, "false", 0]) {
        expect(() =>
          parseRoutingSettings({ ...settings, [key]: value }),
        ).toThrow();
      }
    }
  });
  it("preserves the optional thinking signature recovery switch", () => {
    for (const enabled of [false, true]) {
      const policy = {
        ...defaultFailurePolicy(),
        thinking_signature_recovery: enabled,
      };
      expect(parseFailurePolicy(policy)).toEqual(policy);
    }
  });
  it("keeps the OpenAI repair switch independent from Claude repair", () => {
    for (const enabled of [false, true]) {
      const policy = {
        ...defaultFailurePolicy(),
        thinking_signature_recovery: false,
        openai_reasoning_recovery: enabled,
      };
      expect(parseFailurePolicy(policy)).toEqual(policy);
    }
  });
  it("keeps encrypted function-output repair opt-in and independent", () => {
    for (const enabled of [false, true]) {
      const policy = {
        ...defaultFailurePolicy(),
        openai_reasoning_recovery: false,
        openai_function_output_recovery: enabled,
      };
      expect(parseFailurePolicy(policy)).toEqual(policy);
    }
  });
  it("defines one global policy and treats complete exceptions as replacements", () => {
    const policy = defaultFailurePolicy();
    expect(parseFailurePolicy(policy)).toEqual(policy);
    expect(policy.max_retries).toBe(1);
    expect(policy.http_status["401"]).toBe("failover");
    expect(policy.http_status["529"]).toBe("retry_and_failover");
    expect(
      parseFailurePolicy({ ...policy, http_status: { "418": "retry" } })
        .http_status,
    ).toEqual({ "418": "retry" });
    expect(
      parseRoutingSettings({
        default_failure_policy: policy,
        allow_unmatched_failover: false,
        strategy: "retry_first",
        max_attempts: 6,
      }).default_failure_policy,
    ).toEqual(policy);
  });
  it("rejects malformed policies consistently at the desktop boundary", () => {
    for (const patch of [
      { max_retries: 6 },
      { max_retries: -1 },
      { max_retries: 1.5 },
      { max_delay_ms: 100 },
      { response_start_timeout_seconds: null },
      { thinking_signature_recovery: null },
      { thinking_signature_recovery: "true" },
      { openai_reasoning_recovery: null },
      { openai_reasoning_recovery: "true" },
      { openai_function_output_recovery: null },
      { openai_function_output_recovery: "true" },
      { network_error: "ignore" },
      { http_status: { "200": "retry" } },
      { http_status: { "429": "unknown" } },
    ]) {
      expect(() =>
        parseFailurePolicy({ ...defaultFailurePolicy(), ...patch }),
      ).toThrow();
    }
    expect(() => parseFailurePolicy({ max_retries: 1 })).toThrow();
    expect(() =>
      parseFailoverPolicy({
        enabled: true,
        strategy: "retry_first",
        max_attempts: 21,
      }),
    ).toThrow();
  });

  describe("model redirects", () => {
    const settings = {
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: true,
      strategy: "failover_only",
      max_attempts: 6,
    };
    const rule = (from: string, to: string, enabled = true) => ({
      from,
      to,
      enabled,
    });

    it("parses an absent list as empty and preserves saved rules in order", () => {
      expect(parseRoutingSettings(settings).model_redirects).toEqual([]);
      const model_redirects = [
        rule("gpt-4o", "gpt-5"),
        rule("astrlink/auto", "claude-sonnet-4-5", false),
      ];
      expect(
        parseRoutingSettings({ ...settings, model_redirects }).model_redirects,
      ).toEqual(model_redirects);
      expect(
        parseRoutingSettings({
          ...settings,
          model_redirects: Array.from({ length: 200 }, (_, index) =>
            rule(`model-${index}`, "gpt-5"),
          ),
        }).model_redirects,
      ).toHaveLength(200);
    });

    it("rejects malformed and invalid redirect lists", () => {
      for (const model_redirects of [
        null,
        {},
        [null],
        [{ from: "a", to: "b" }],
        [{ ...rule("a", "b"), extra: true }],
        [{ ...rule("a", "b"), enabled: "true" }],
        [{ ...rule("a", "b"), from: 1 }],
        [rule("a", "a")],
        [rule("a", "astrlink/auto")],
        [rule("a", "b"), rule("a", "c", false)],
        [rule("a", "b"), rule("b", "c")],
        Array.from({ length: 201 }, (_, index) =>
          rule(`model-${index}`, "gpt-5"),
        ),
      ]) {
        expect(() =>
          parseRoutingSettings({ ...settings, model_redirects }),
        ).toThrow();
      }
    });

    it("mirrors the gateway validation rules with the first issue per row", () => {
      const cases: [ModelRedirect[], ReturnType<typeof modelRedirectIssues>][] =
        [
          [[rule("", "gpt-5")], ["empty_from"]],
          [[rule("gpt-4o", "")], ["empty_to"]],
          [[rule("", "")], ["empty_from"]],
          [[rule("x".repeat(257), "gpt-5")], ["too_long"]],
          [[rule("\u{1F600}".repeat(256), "gpt-5")], [undefined]],
          [[rule("gpt-4o", "gpt\n5")], ["control_character"]],
          [[rule("gpt\u00004o", "gpt-5")], ["control_character"]],
          [[rule("gpt\t4o", "gpt-5")], [undefined]],
          [[rule(" gpt-4o", "gpt-5")], ["whitespace"]],
          [[rule("gpt-4o", "gpt-5\u0085")], ["whitespace"]],
          [[rule("gpt-4o", "\u3000gpt-5")], ["whitespace"]],
          // Go's TrimSpace keeps U+FEFF, unlike String.prototype.trim.
          [[rule("\ufeffgpt-4o", "gpt-5")], [undefined]],
          [[rule("gpt-5", "gpt-5")], ["same_model"]],
          [[rule("GPT-5", "gpt-5")], [undefined]],
          [[rule("gpt-4o", "astrlink/auto")], ["auto_target"]],
          [[rule("astrlink/auto", "gpt-5")], [undefined]],
          [
            [rule("gpt-4o", "gpt-5"), rule("gpt-4o", "gpt-5-mini", false)],
            ["duplicate_from", "duplicate_from"],
          ],
          [
            [rule("gpt-4o", "gpt-5"), rule("gpt-4", "gpt-4o", false)],
            [undefined, "chained_target"],
          ],
        ];
      for (const [redirects, issues] of cases)
        expect(modelRedirectIssues(redirects)).toEqual(issues);
    });
  });
});
