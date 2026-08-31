import { describe, expect, it } from "vitest";

import {
  parseAccessTokenCreateResult,
  parseAccessTokenPage,
  parseAccessTokenRevealResult,
} from "./access-token-model";

const token = {
  id: "token_01",
  name: "VS Code",
  hint: "astr_…K8Q2",
  created_at: "2026-07-24T10:30:00Z",
};

const accessToken = `astr_${"A".repeat(43)}`;

describe("access-token IPC contract", () => {
  it("strictly parses list, create, and reveal responses", () => {
    expect(
      parseAccessTokenPage({ items: [token], next_cursor: null }),
    ).toEqual({ items: [token], next_cursor: null });
    expect(
      parseAccessTokenCreateResult({
        token,
        access_token: accessToken,
      }),
    ).toEqual({ token, access_token: accessToken });
    expect(
      parseAccessTokenRevealResult({ access_token: accessToken }),
    ).toEqual({ access_token: accessToken });
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
