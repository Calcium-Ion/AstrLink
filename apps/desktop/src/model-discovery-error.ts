import { controlErrorCode } from "./control-error";
import { i18n } from "./i18n";
import { usageFailureReason } from "./subscription-usage-model";

/** The reason keys Core names in `details[].reason` of a failed model list. */
const reasonKeys: Record<string, string> = {
  upstream_unauthorized: "unauthorized",
  upstream_not_found: "notFound",
  upstream_rate_limited: "rateLimited",
  upstream_unavailable: "unavailable",
  upstream_unreachable: "unreachable",
  upstream_invalid_response: "invalidResponse",
  upstream_status: "status",
};

/** Cores without failure details still name the upstream status in the message. */
function reasonForStatus(status: number): string {
  if (status === 401 || status === 403) return "unauthorized";
  if (status === 404 || status === 405 || status === 501) return "notFound";
  if (status === 429) return "rateLimited";
  if (status >= 500) return "unavailable";
  return "status";
}

function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  return typeof error === "string" ? error : "";
}

/** The upstream HTTP status Core quotes as `(HTTP 401)`, if any. */
function upstreamStatus(text: string): number | null {
  const match = /\(HTTP (\d{3})\)/.exec(text);
  return match ? Number(match[1]) : null;
}

/**
 * Why fetching an upstream model list failed, in one line the operator can
 * act on: a wrong key, a wrong address, a timeout, and so on. Control API
 * errors never surface as their raw `POST … returned …` text.
 */
export function modelDiscoveryFailureReason(error: unknown): string {
  const code = controlErrorCode(error);
  const text = errorText(error);
  if (code === "upstream_model_discovery_timeout")
    return i18n.t("modelDiscoveryErrors.timeout");
  if (code === "model_discovery_unsupported")
    return i18n.t("modelDiscoveryErrors.unsupported");
  if (code === "credential_unavailable")
    return i18n.t("modelDiscoveryErrors.credential");
  if (code === "upstream_model_discovery_failed") {
    const status = upstreamStatus(text);
    const reason = usageFailureReason(error);
    const key =
      (reason ? reasonKeys[reason] : undefined) ??
      (status === null ? "generic" : reasonForStatus(status));
    return i18n.t(`modelDiscoveryErrors.${key}`, {
      status:
        status === null
          ? ""
          : i18n.t("modelDiscoveryErrors.httpStatus", { status }),
    });
  }
  // Other Control API errors carry JSON meant for logs, not for the page.
  if (code !== null || text === "" || text.includes("{"))
    return i18n.t("modelDiscoveryErrors.generic", { status: "" });
  return text;
}
