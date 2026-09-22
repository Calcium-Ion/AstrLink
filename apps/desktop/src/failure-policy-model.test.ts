import { describe, expect, it } from "vitest";
import {
  defaultFailurePolicy,
  identitySettingKeys,
  parseFailurePolicy,
  parseFailoverPolicy,
  parseRoutingSettings,
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
});
