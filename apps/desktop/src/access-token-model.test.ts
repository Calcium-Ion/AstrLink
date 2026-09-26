import { describe, expect, it } from "vitest";

import {
  parseAccessTokenCreateResult,
  parseAccessTokenPage,
  parseAccessTokenRevealResult,
  parseAccessTokenUsageResponse,
} from "./access-token-model";

const token = {
  id: "token_01",
  name: "VS Code",
  hint: "astr_…K8Q2",
  created_at: "2026-07-24T10:30:00Z",
};

const accessToken = `astr_${"A".repeat(43)}`;

describe("access-token IPC contract", () => {
  it("parses token performance with the shared cache and speed contract", () => {
    const performance = {
      cache_hit_rate: 0.5,
      output_tokens_per_second: 80,
      cache_samples: 2,
      speed_samples: 3,
    };
    const usage = {
      token_id: token.id,
      today_tokens: 12,
      total_tokens: 1500,
      today_performance: performance,
      total_performance: {
        cache_hit_rate: null,
        output_tokens_per_second: null,
        cache_samples: 0,
        speed_samples: 0,
      },
    };
    expect(
      parseAccessTokenUsageResponse({ items: [usage] }).items[0]
        .today_performance,
    ).toEqual(performance);
    for (const bad of [
      null,
      { ...performance, cache_hit_rate: 1.1 },
      { ...performance, speed_samples: 0 },
    ]) {
      expect(() =>
        parseAccessTokenUsageResponse({
          items: [{ ...usage, today_performance: bad }],
        }),
      ).toThrow();
    }
  });
  it("parses compact usage and rejects invalid counts or duplicate tokens", () => {
    const usage = { token_id: token.id, today_tokens: 12, total_tokens: 1500 };
    expect(parseAccessTokenUsageResponse({ items: [usage] })).toEqual({
      items: [{ ...usage, today_billing: null, total_billing: null }],
    });
    expect(parseAccessTokenUsageResponse({ items: [] })).toEqual({ items: [] });
    for (const item of [
      { ...usage, today_tokens: -1 },
      { ...usage, total_tokens: 1.5 },
      { ...usage, total_tokens: "1500" },
      { ...usage, total_tokens: Number.MAX_SAFE_INTEGER + 1 },
      { ...usage, total_tokens: 11 },
      { ...usage, token_id: "bad/id" },
      { ...usage, access_token: accessToken },
    ]) {
      expect(() => parseAccessTokenUsageResponse({ items: [item] })).toThrow(
        "Invalid access-token IPC response",
      );
    }
    expect(() =>
      parseAccessTokenUsageResponse({ items: [usage, usage] }),
    ).toThrow("duplicate token ID");
  });

  it("parses decimal billing amounts and rejects malformed billing", () => {
    const amounts = {
      amount_usd: "0.000000001",
      priced: 1,
      unpriced: 0,
      pending: 0,
      revalued: 0,
      requests: 1,
    };
    const usage = {
      token_id: token.id,
      today_tokens: 1,
      total_tokens: 2,
      today_billing: amounts,
      total_billing: amounts,
    };
    expect(parseAccessTokenUsageResponse({ items: [usage] }).items[0]).toEqual(
      usage,
    );
    for (const bad of [
      { ...amounts, amount_usd: 1 },
      { ...amounts, amount_usd: "NaN" },
      { ...amounts, pending: -1 },
    ]) {
      expect(() =>
        parseAccessTokenUsageResponse({
          items: [{ ...usage, today_billing: bad }],
        }),
      ).toThrow();
    }
  });

  it("strictly parses list, create, and reveal responses", () => {
    expect(parseAccessTokenPage({ items: [token], next_cursor: null })).toEqual(
      { items: [token], next_cursor: null },
    );
    expect(
      parseAccessTokenCreateResult({
        token,
        access_token: accessToken,
      }),
    ).toEqual({ token, access_token: accessToken });
    expect(parseAccessTokenRevealResult({ access_token: accessToken })).toEqual(
      { access_token: accessToken },
    );
  });

  it.each([
    [
      "secret in a summary",
      {
        items: [{ ...token, secret: accessToken }],
        next_cursor: null,
      },
    ],
    [
      "hash in a summary",
      {
        items: [{ ...token, hash: "sha256" }],
        next_cursor: null,
      },
    ],
    [
      "unexpected cursor",
      {
        items: [token],
        next_cursor: "cursor",
      },
    ],
  ])("rejects %s", (_name, value) => {
    expect(() => parseAccessTokenPage(value)).toThrow(
      "Invalid access-token IPC response",
    );
  });

  it("rejects malformed timestamps, tokens, and extra secret fields", () => {
    expect(() =>
      parseAccessTokenPage({
        items: [{ ...token, created_at: "2026-07-24 10:30:00Z" }],
        next_cursor: null,
      }),
    ).toThrow("RFC 3339");
    expect(() =>
      parseAccessTokenPage({
        items: [{ ...token, name: "n".repeat(65) }],
        next_cursor: null,
      }),
    ).toThrow("1 to 64");
    expect(() =>
      parseAccessTokenRevealResult({ access_token: "short" }),
    ).toThrow("astr_ token");
    expect(() =>
      parseAccessTokenRevealResult({
        access_token: `other_${"A".repeat(42)}`,
      }),
    ).toThrow("astr_ token");
    expect(() =>
      parseAccessTokenRevealResult({
        access_token: `astr_${"A".repeat(42)}B`,
      }),
    ).toThrow("astr_ token");
    expect(() =>
      parseAccessTokenPage({
        items: [{ ...token, hint: accessToken }],
        next_cursor: null,
      }),
    ).toThrow("1 to 32");
    expect(() =>
      parseAccessTokenCreateResult({
        token,
        access_token: accessToken,
        secret: accessToken,
      }),
    ).toThrow("unexpected field");
  });
});
