import { describe, expect, it } from "vitest";
import { i18n } from "./i18n";
import { privacyModelOperationError } from "./privacy-model-errors";

describe("privacy model operation errors", () => {
  it.each([
    [
      'POST /control/v1/privacy/models/probe returned 502 Bad Gateway: {"error":{"code":"privacy_model_metadata_unavailable","message":"public model metadata could not be verified"}}',
      "metadata",
    ],
    ['{"error":{"code":"invalid_privacy_model"}}', "unsupported"],
    ['{"error":{"code":"privacy_model_already_installed"}}', "installed"],
    ['{"error":{"code":"privacy_model_local_probe_required"}}', "probeAgain"],
    [
      '{"error":{"code":"privacy_model_local_source_unavailable"}}',
      "localSource",
    ],
    ["privacy model probe label has missing or unexpected fields", "response"],
    [
      "Invalid privacy-policy IPC response at $.labels: unexpected key",
      "response",
    ],
    ["error sending request: operation timed out", "timeout"],
    ["error sending request: connection refused", "connection"],
  ])("gives actionable guidance for %s", (details, reason) => {
    expect(privacyModelOperationError(new Error(details), "Failed.")).toEqual({
      message: i18n.t(`safety.modelErrors.${reason}`),
      details,
    });
  });

  it("keeps unknown diagnostics out of the primary message", () => {
    const details = "unexpected low-level failure";
    const result = privacyModelOperationError(
      details,
      i18n.t("safety.installFailed"),
    );
    expect(result.message).toContain(i18n.t("safety.installFailed"));
    expect(result.message).not.toContain(details);
    expect(result.details).toBe(details);
    expect(privacyModelOperationError(null, "Failed.").details).toBeNull();
  });
});
