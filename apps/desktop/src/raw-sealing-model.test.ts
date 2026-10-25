import { describe, expect, it } from "vitest";

import {
  newPasswordIssue,
  parseRawSealingOutcome,
  parseRawSealingStatus,
  passwordIsShort,
  passwordLength,
  rawPasswordUnset,
  rawProofMode,
  rawProtection,
  type RawSealingState,
} from "./raw-sealing-model";

function status(overrides: Record<string, unknown> = {}) {
  return {
    raw_available: true,
    configured: true,
    password_set: true,
    password_required: false,
    envelopes: ["password"],
    key_verified: true,
    unlocked: false,
    unlock_expires_at: null,
    unlock_idle_seconds: 900,
    retry_after_seconds: 0,
    password_min_length: 8,
    password_max_length: 128,
    ...overrides,
  };
}

function state(overrides: Partial<RawSealingState> = {}): RawSealingState {
  return {
    ...parseRawSealingStatus(status()),
    ...overrides,
  };
}

describe("raw sealing status", () => {
  it("reads the desktop's replaced-key verdict, absent meaning none", () => {
    expect(
      parseRawSealingStatus(status({ key_replaced: true })).key_replaced,
    ).toBe(true);
    expect(
      parseRawSealingStatus(status({ key_replaced: false })).key_replaced,
    ).toBe(false);
    expect(parseRawSealingStatus(status()).key_replaced).toBe(false);
    const outcome = parseRawSealingOutcome({
      outcome: "sealing",
      status: status({ key_replaced: true }),
    });
    expect(outcome.outcome === "sealing" && outcome.status.key_replaced).toBe(
      true,
    );
  });

  it("rejects malformed fields with the offending path", () => {
    expect(() => parseRawSealingStatus(status({ configured: "yes" }))).toThrow(
      "原文封存数据无效（$.configured）：应为布尔值",
    );
    expect(() =>
      parseRawSealingStatus(status({ envelopes: ["password", "password"] })),
    ).toThrow("原文封存数据无效（$.envelopes）：过长");
    expect(() =>
      parseRawSealingStatus(status({ envelopes: ["cloud"] })),
    ).toThrow("$.envelopes[0]");
    expect(() =>
      parseRawSealingStatus(status({ password_required: undefined })),
    ).toThrow("$.password_required");
    expect(() =>
      parseRawSealingStatus(status({ unlock_expires_at: "soon" })),
    ).toThrow("$.unlock_expires_at");
    expect(() =>
      parseRawSealingStatus(status({ unlock_idle_seconds: 0 })),
    ).toThrow("$.unlock_idle_seconds");
    expect(() =>
      parseRawSealingStatus(status({ retry_after_seconds: -1 })),
    ).toThrow("$.retry_after_seconds");
    expect(() =>
      parseRawSealingStatus(
        status({ password_min_length: 12, password_max_length: 8 }),
      ),
    ).toThrow("$.password_max_length");
    expect(() => parseRawSealingStatus(status({ key_replaced: 1 }))).toThrow(
      "$.key_replaced",
    );
    expect(() =>
      parseRawSealingOutcome({
        outcome: "sealing",
        status: status({ key_replaced: null }),
      }),
    ).toThrow("$.status.key_replaced");
    expect(() => parseRawSealingStatus([])).toThrow("（$）：应为对象");
  });
});

describe("raw sealing outcomes", () => {
  it("parses a new status and an optional reset summary", () => {
    expect(
      parseRawSealingOutcome({ outcome: "sealing", status: status() }),
    ).toEqual({
      outcome: "sealing",
      status: parseRawSealingStatus(status()),
      reset: null,
    });
    expect(
      parseRawSealingOutcome({
        outcome: "sealing",
        status: status({ reset: { deleted_parts: 3, affected_records: 2 } }),
      }),
    ).toMatchObject({ reset: { deleted_parts: 3, affected_records: 2 } });
    expect(() =>
      parseRawSealingOutcome({
        outcome: "sealing",
        status: status({ reset: { deleted_parts: -1, affected_records: 0 } }),
      }),
    ).toThrow("$.status.reset.deleted_parts");
  });

  it("parses refusals and rejects unknown results", () => {
    expect(parseRawSealingOutcome({ outcome: "password_invalid" })).toEqual({
      outcome: "password_invalid",
    });
    expect(
      parseRawSealingOutcome({ outcome: "backoff", retry_after_seconds: 5 }),
    ).toEqual({ outcome: "backoff", retry_after_seconds: 5 });
    expect(() =>
      parseRawSealingOutcome({ outcome: "backoff", retry_after_seconds: 0 }),
    ).toThrow("$.retry_after_seconds");
    expect(() => parseRawSealingOutcome({ outcome: "decided" })).toThrow(
      "$.outcome",
    );
  });
});

describe("raw proof mode", () => {
  it("asks for the password, else a plain confirmation", () => {
    expect(rawProofMode(state())).toBe("password");
    expect(rawProofMode(state({ password_set: false }))).toBe("confirm");
  });

  it("reads raw content through the raw password only", () => {
    expect(rawPasswordUnset(state())).toBe(false);
    expect(
      rawPasswordUnset(state({ configured: false, password_set: false })),
    ).toBe(true);
  });

  it("summarizes how raw content is protected", () => {
    expect(
      rawProtection(state({ configured: false, password_set: false })),
    ).toBe("unset");
    expect(rawProtection(state())).toBe("password");
  });
});

describe("new raw passwords", () => {
  const policy = { password_min_length: 8, password_max_length: 10 };

  it("checks length in characters and the confirmation", () => {
    expect(passwordLength("口令🔑")).toBe(3);
    expect(newPasswordIssue("short", "short", policy)).toBe("too_short");
    expect(newPasswordIssue("elevenchars", "elevenchars", policy)).toBe(
      "too_long",
    );
    // Ten characters, though more UTF-16 code units.
    expect(
      newPasswordIssue("🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑", "🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑", policy),
    ).toBe(null);
    expect(newPasswordIssue("eightchr", "eightchx", policy)).toBe("mismatch");
    expect(newPasswordIssue("eightchr", "eightchr", policy)).toBe(null);
  });

  it("suggests a longer passphrase without refusing a short one", () => {
    expect(passwordIsShort("")).toBe(false);
    expect(passwordIsShort("eightchr")).toBe(true);
    expect(passwordIsShort("twelve chars")).toBe(false);
  });
});
