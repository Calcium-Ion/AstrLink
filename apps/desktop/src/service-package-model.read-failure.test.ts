import { describe, expect, it } from "vitest";

import { readFailureReason } from "./service-package-model";

describe("readFailureReason", () => {
  it.each([
    ["key_rejected", { key: "keyRejected" }],
    ["no_plan", { key: "noPlan" }],
    ["wrong_address", { key: "wrongAddress" }],
    ["unreachable", { key: "unreachable" }],
    ["timeout", { key: "timeout" }],
    ["provider_down", { key: "providerDown" }],
    ["unexpected_reply", { key: "unexpectedReply" }],
    ["no_quota", { key: "noQuota" }],
  ])("words Core's %s reason", (reason, expected) => {
    expect(
      readFailureReason(
        "subscription_usage_failed: usage unavailable: status 401",
        reason,
      ),
    ).toEqual(expected);
  });

  it("quotes the provider for its own error", () => {
    expect(
      readFailureReason(
        "subscription_usage_failed: usage unavailable: Invalid Authentication",
        "provider_error",
      ),
    ).toEqual({ key: "providerSaid", detail: "Invalid Authentication" });
    expect(readFailureReason(undefined, "provider_error")).toEqual({
      key: "other",
    });
  });

  it.each([
    [
      "subscription_usage_failed: kimi usage unavailable: status 401",
      { key: "keyRejected" },
    ],
    [
      "subscription_usage_failed: minimax usage unavailable: status 404",
      { key: "wrongAddress" },
    ],
    [
      "subscription_usage_failed: kimi usage unavailable: status 502",
      { key: "providerDown" },
    ],
    [
      'subscription_usage_failed: newapi usage unavailable: Get "http://localhost:3000/api/usage/token/": dial tcp [::1]:3000: connect: connection refused',
      { key: "unreachable" },
    ],
    [
      "service_credential_unavailable: coding plan usage needs the service API key",
      { key: "noKey" },
    ],
    [
      "subscription_usage_timeout: subscription usage lookup timed out",
      { key: "timeout" },
    ],
    [
      "subscription_usage_failed: kimi usage unavailable: invalid payload",
      { key: "unexpectedReply" },
    ],
    [
      "subscription_usage_failed: kimi usage unavailable: no usage windows",
      { key: "noQuota" },
    ],
    [
      "Quota temporarily unavailable",
      { key: "providerSaid", detail: "Quota temporarily unavailable" },
    ],
    ["", { key: "other" }],
    [undefined, { key: "other" }],
  ])("reads a message without a reason: %s", (message, expected) => {
    expect(readFailureReason(message)).toEqual(expected);
  });
});
