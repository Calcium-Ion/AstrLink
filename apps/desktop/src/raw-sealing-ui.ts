import type { ProofInput, ProofResult } from "@/components/ProofConfirmDialog";

import { controlErrorCode } from "./control-error";
import { i18n } from "./i18n";
import type { RawProof, RawSealingOutcome } from "./raw-sealing-model";

const errorKeys: Record<string, string> = {
  raw_access_unavailable: "rawSealing.errors.notConfigured",
  raw_password_not_set: "rawSealing.errors.notConfigured",
  raw_sealing_unavailable: "rawSealing.errors.unavailable",
  raw_password_already_set: "rawSealing.errors.alreadySet",
  raw_proof_required: "rawSealing.errors.proofRequired",
  raw_sealing_changed: "rawSealing.errors.changed",
  validation_failed: "rawSealing.errors.policy",
  raw_vault_unavailable: "rawSealing.errors.vault",
};

/** A readable message for a failed raw sealing call. */
export function rawSealingErrorMessage(error: unknown): string {
  const code = controlErrorCode(error);
  const key = code ? errorKeys[code] : undefined;
  if (key) return i18n.t(key);
  const message = error instanceof Error ? error.message : String(error);
  return message || i18n.t("proofDialog.failed");
}

/**
 * The proof a dialog input carries to the host; a plain confirmation carries
 * none, and Touch ID only asks the host to prompt.
 */
export function rawProofOf(input: ProofInput): RawProof | undefined {
  switch (input.kind) {
    case "password":
      return { kind: "password", password: input.password };
    case "biometric":
      return { kind: "biometric" };
    case "confirm":
      return undefined;
  }
}

/** A refusal the proof dialog can recover from by asking again. */
export type RawRefusal = Exclude<RawSealingOutcome, { outcome: "sealing" }>;

/** How the proof dialog shows a refusal it can recover from. */
export function refusalResult(outcome: RawRefusal): ProofResult {
  switch (outcome.outcome) {
    case "password_invalid":
      return { kind: "password_invalid" };
    case "backoff":
      return {
        kind: "backoff",
        retryAfterSeconds: outcome.retry_after_seconds,
      };
    case "biometric_cancelled":
      return {
        kind: "error",
        message: i18n.t("rawSealing.biometric.cancelled"),
      };
    case "biometric_unavailable":
      return {
        kind: "error",
        message: i18n.t("rawSealing.biometric.unavailable"),
      };
  }
}
