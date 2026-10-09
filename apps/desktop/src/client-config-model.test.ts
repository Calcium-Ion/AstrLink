import { describe, expect, it } from "vitest";

import {
  parseClientConfigApplyOutcome,
  parseClientConfigCopied,
  parseClientConfigSnippet,
  parseClientConfigStatuses,
  parseClientProxyCheck,
} from "./client-config-model";

function statuses(
  claude: Record<string, unknown> = {},
  codex: Record<string, unknown> = {},
  pi: Record<string, unknown> = {},
): unknown[] {
  return [
    {
      client: "claude",
      detected: true,
      paths: ["/Users/me/.claude/settings.json"],
      state: "not_configured",
      token_id: null,
      ...claude,
    },
    {
      client: "codex",
      detected: false,
      paths: ["/Users/me/.codex/config.toml"],
      state: "not_configured",
      token_id: null,
      ...codex,
    },
    {
      client: "pi",
      detected: true,
      paths: [
        "/Users/me/.pi/agent/models.json",
        "/Users/me/.pi/agent/settings.json",
      ],
      state: "not_configured",
      token_id: null,
      ...pi,
    },
  ];
}

describe("client-config IPC parsing", () => {
  it("accepts one status per direct client in order", () => {
    const value = statuses(
      { state: "configured", token_id: "token_01" },
      { state: "outdated", token_id: "token_02" },
      { state: "modified", token_id: "token_01" },
    );
    expect(parseClientConfigStatuses(value)).toEqual(value);
    // A client moved to CC Switch holds no config AstrLink wrote.
    expect(
      parseClientConfigStatuses(statuses({ state: "cc_switch" }))[0],
    ).toMatchObject({ state: "cc_switch", token_id: null });
    // An unreadable file may or may not have a record behind it.
    for (const tokenID of [null, "token_01"]) {
      expect(
        parseClientConfigStatuses(
          statuses({ state: "invalid", token_id: tokenID }),
        )[0],
      ).toMatchObject({ state: "invalid", token_id: tokenID });
    }
  });

  it.each([
    ["a missing client", statuses().slice(0, 1)],
    ["clients out of order", statuses().reverse()],
    ["a client it cannot write", statuses({ client: "gemini" })],
    ["an unknown state", statuses({ state: "stale" })],
    ["an unexpected field", statuses({ token: "astr_x" })],
    ["a non-boolean detection", statuses({ detected: "yes" })],
    ["no config path", statuses({ paths: [] })],
    ["a multi-line path", statuses({ paths: ["/tmp/a\n/tmp/b"] })],
    ["a malformed token ID", statuses({ state: "configured", token_id: "X" })],
    ["a configured client without a token", statuses({ state: "modified" })],
    ["an unconfigured client with a token", statuses({ token_id: "token_01" })],
    [
      "a client moved to CC Switch with a token",
      statuses({ state: "cc_switch", token_id: "token_01" }),
    ],
  ])("rejects %s", (_, value) => {
    expect(() => parseClientConfigStatuses(value)).toThrow(
      "Invalid client-config IPC response",
    );
  });

  it("accepts applied writes and key-only conflicts", () => {
    expect(parseClientConfigApplyOutcome({ status: "applied" })).toEqual({
      status: "applied",
    });
    expect(
      parseClientConfigApplyOutcome({
        status: "needs_confirmation",
        keys: ["env.ANTHROPIC_API_KEY", "model_providers.astrlink"],
      }),
    ).toEqual({
      status: "needs_confirmation",
      keys: ["env.ANTHROPIC_API_KEY", "model_providers.astrlink"],
    });
    for (const value of [
      { status: "needs_confirmation", keys: [] },
      { status: "needs_confirmation", keys: ["a\nb"] },
      { status: "needs_confirmation", keys: ["x".repeat(513)] },
      { status: "needs_confirmation" },
      { status: "applied", keys: ["model"] },
      { status: "skipped" },
      null,
    ]) {
      expect(() => parseClientConfigApplyOutcome(value)).toThrow(
        "Invalid client-config IPC response",
      );
    }
  });

  it("accepts previews that show only the token hint", () => {
    const preview = '[model_providers.astrlink]\ntoken = "astr_…K8Q2"\n';
    expect(parseClientConfigSnippet(preview)).toBe(preview);
    for (const value of ["", "  \n", 42, `token = "astr_${"A".repeat(43)}"`]) {
      expect(() => parseClientConfigSnippet(value)).toThrow(
        "Invalid client-config IPC response",
      );
    }
    expect(parseClientConfigCopied(false)).toBe(false);
    expect(() => parseClientConfigCopied("true")).toThrow(
      "Invalid client-config IPC response",
    );
  });

  it("accepts each proxy route, naming the proxy by host and port", () => {
    const value = {
      client: { route: "direct" },
      numeric: { route: "blocked", proxy: "127.0.0.1:7892" },
    };
    expect(parseClientProxyCheck(value)).toEqual(value);
    expect(
      parseClientProxyCheck({
        client: { route: "proxied", proxy: "proxy.lan:8080" },
        numeric: null,
      }),
    ).toEqual({
      client: { route: "proxied", proxy: "proxy.lan:8080" },
      numeric: null,
    });
  });

  it.each([
    ["a missing numeric route", { client: { route: "direct" } }],
    ["an unknown route", { client: { route: "tunnel" }, numeric: null }],
    [
      "a direct route naming a proxy",
      { client: { route: "direct", proxy: "127.0.0.1:7892" }, numeric: null },
    ],
    [
      "a blocked route without a proxy",
      { client: { route: "blocked" }, numeric: null },
    ],
    [
      "a multi-line proxy",
      { client: { route: "blocked", proxy: "a\nb" }, numeric: null },
    ],
    [
      "an unexpected field",
      { client: { route: "direct" }, numeric: null, url: "http://x" },
    ],
  ])("rejects %s", (_, value) => {
    expect(() => parseClientProxyCheck(value)).toThrow(
      "Invalid client-config IPC response",
    );
  });
});
