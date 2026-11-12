import { describe, expect, it } from "vitest";

import type { AccessTokenSummary } from "./access-token-model";
import type { ClientConfigStatus } from "./client-config-model";
import { anyTokenConfigured } from "./use-client-configs";
import { onboardingToken } from "./use-onboarding";

function token(
  id: string,
  created_at: string,
  source: AccessTokenSummary["source"] = "user",
): AccessTokenSummary {
  return {
    id,
    name: id,
    hint: "astr_…0000",
    source,
    created_at,
    service_scope: "all",
    service_ids: [],
  };
}

function config(
  token_id: string | null,
  state: ClientConfigStatus["state"],
): ClientConfigStatus {
  return {
    client: "claude",
    wsl: null,
    target: null,
    detected: true,
    home: "/Users/me",
    dir: "/Users/me/.claude",
    paths: ["/Users/me/.claude/settings.json"],
    exists: [true],
    state,
    token_id,
    models: {},
    notes: [],
    paste_since_unix: null,
    paste_into: [],
  };
}

describe("onboardingToken", () => {
  it("takes the bootstrap token wherever the list puts it", () => {
    const tokens = [
      token("laptop", "2026-10-25T08:00:00Z"),
      token("bootstrap", "2026-10-24T08:00:00Z", "system_default"),
    ];
    expect(onboardingToken(tokens)?.id).toBe("bootstrap");
  });

  it("takes the oldest token once the bootstrap token is gone", () => {
    const tokens = [
      token("new", "2026-10-26T08:00:00Z"),
      token("old", "2026-10-24T08:00:00Z"),
    ];
    expect(onboardingToken(tokens)?.id).toBe("old");
    expect(onboardingToken([])).toBeUndefined();
  });
});

describe("anyTokenConfigured", () => {
  const tokens = [token("mine", "2026-10-24T08:00:00Z")];

  it("counts a client that holds a listed token", () => {
    expect(anyTokenConfigured([config("mine", "configured")], tokens)).toBe(
      true,
    );
    expect(anyTokenConfigured([config("mine", "outdated")], tokens)).toBe(true);
  });

  it("ignores unconfigured clients and another installation's tokens", () => {
    expect(
      anyTokenConfigured(
        [config(null, "not_configured"), config("elsewhere", "configured")],
        tokens,
      ),
    ).toBe(false);
  });
});
