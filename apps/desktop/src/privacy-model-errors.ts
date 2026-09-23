import { i18n } from "./i18n";

export interface PrivacyModelOperationError {
  message: string;
  details: string | null;
}

const controlErrors = {
  privacy_model_metadata_unavailable: "metadata",
  invalid_privacy_model: "unsupported",
  invalid_privacy_model_probe: "source",
  invalid_privacy_model_install: "unsupported",
  privacy_model_local_probe_required: "probeAgain",
  privacy_model_local_source_unavailable: "localSource",
  privacy_model_already_installed: "installed",
  privacy_model_busy: "busy",
  privacy_model_limit: "limit",
  privacy_model_not_found: "notFound",
  privacy_model_selected: "selected",
} as const;

export function privacyModelOperationError(
  error: unknown,
  fallback: string,
): PrivacyModelOperationError {
  const details =
    error instanceof Error
      ? error.message.trim()
      : typeof error === "string"
        ? error.trim()
        : "";
  let reason: string | undefined;
  // The native bridge wraps the control API's JSON error in an HTTP summary.
  const code = /"code"\s*:\s*"([a-z_]+)"/.exec(details)?.[1];
  if (code && Object.hasOwn(controlErrors, code)) {
    reason = controlErrors[code as keyof typeof controlErrors];
  } else if (
    /^privacy model (?:probe|catalog|installation|adapter)\b/i.test(details) ||
    details.startsWith("Invalid privacy-policy IPC response")
  ) {
    reason = "response";
  } else if (/timed? out|timeout/i.test(details)) {
    reason = "timeout";
  } else if (
    /error sending request|connection (?:refused|reset|closed)|core is not ready/i.test(
      details,
    )
  ) {
    reason = "connection";
  }
  return {
    message: reason
      ? i18n.t(`safety.modelErrors.${reason}`)
      : i18n.t("safety.modelErrors.fallback", { operation: fallback }),
    details: details || null,
  };
}
