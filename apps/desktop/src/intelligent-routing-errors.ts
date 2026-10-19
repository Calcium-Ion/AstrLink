/**
 * The控制面 error codes the 智能路由 panels can receive, and their text.
 *
 * The Rust host reports a failed control-API call as one summary string
 * (`PUT /control/v1/classifier/settings returned 422: {"error":{"code":…}}`),
 * and the `message` inside that envelope is English by construction: Core has
 * no locale and must not grow one, because the same API serves the CLI and
 * other clients. The `code` is the stable part, so it is what gets translated.
 *
 * Only the codes these four panels can actually reach are listed — the
 * classifier settings resource, the routing settings resource, and the shared
 * decode/store helpers both go through. A code that is missing here still
 * renders as localized prose (see `intelligentRoutingErrorText`), so a Core
 * that grows a new code cannot leak English; `routingErrorCodes` is the list
 * `intelligent-routing-errors.test.ts` pins against the Go sources.
 */

import { i18n } from "./i18n";

/**
 * Wire codes the classifier and routing endpoints emit, mapped to the message
 * key suffix. Each is reachable from one of the four panels:
 *
 * - `classifier_settings.go` — the classifier settings singleton, its
 *   credential sub-resource, and the dry run.
 * - `routing_settings.go` — the routing settings document.
 * - `control_helpers.go` — body decoding and the store-error translation both
 *   resources share.
 */
export const routingErrorCodes = {
  // Classifier settings.
  classifier_settings_unavailable: "classifierUnavailable",
  classifier_source_not_supported: "sourceNotSupported",
  classifier_dimension_in_use: "dimensionInUse",
  invalid_classifier_settings: "invalidSettings",
  invalid_credential: "invalidCredential",
  credential_unavailable: "credentialUnavailable",
  unavailable: "credentialStoreUnavailable",
  dry_run_busy: "dryRunBusy",
  dry_run_timeout: "dryRunTimeout",
  invalid_dry_run: "invalidDryRun",
  unknown_dry_run_protocol: "unknownDryRunProtocol",
  // Routing settings.
  routing_settings_unavailable: "routingUnavailable",
  invalid_routing_settings: "invalidRouting",
  unsupported_routing_field: "unsupportedField",
  routing_feature_retired: "featureRetired",
  // Shared body decoding and store translation.
  invalid_json: "invalidJson",
  unsupported_media_type: "unsupportedMediaType",
  validation_failed: "validationFailed",
  not_found: "notFound",
  conflict: "conflict",
  precondition_failed: "preconditionFailed",
  invalid_cursor: "invalidCursor",
  invalid_resource: "invalidResource",
  persisted_state_invalid: "persistedStateInvalid",
  storage_unavailable: "storageUnavailable",
  method_not_allowed: "methodNotAllowed",
} as const;

const knownCodes = new Set<string>(Object.keys(routingErrorCodes));

function errorText(error: unknown): string {
  if (error instanceof Error) return error.message.trim();
  return typeof error === "string" ? error.trim() : "";
}

/** The envelope's `error.code`, or null when the failure never reached Core. */
function errorCode(details: string): string | null {
  return /"code"\s*:\s*"([a-z_]+)"/.exec(details)?.[1] ?? null;
}

/** The HTTP status the bridge reported, when it reported one. */
function errorStatus(details: string): number | null {
  const match = /returned (\d{3})\b/.exec(details);
  return match === null ? null : Number(match[1]);
}

/**
 * Transport failures the Rust host reports before any envelope exists. Matched
 * on the wording that host emits, which is why they are matched here rather
 * than parsed out of a code.
 */
function transportReason(details: string): string | null {
  if (/timed? out|timeout/i.test(details)) return "timeout";
  if (
    /error sending request|connection (?:refused|reset|closed)|core is not ready/i.test(
      details,
    )
  ) {
    return "connection";
  }
  return null;
}

/**
 * The message for a failed call in one of the 智能路由 panels.
 *
 * `fallback` is the caller's own "could not load" / "could not save" wording,
 * used when there is no error text at all.
 *
 * A recognised control-API envelope is always rendered from its code, never
 * from the server's English. A code with no entry above still gets localized
 * prose carrying the code for diagnosis, which is deliberate: the alternative
 * is showing `{"error":{"code":…}}` to a user reading Chinese.
 *
 * Anything that is not an envelope — a host bug, a parser message — passes
 * through unchanged, because that text describes something the reader has to
 * report rather than a condition with a known meaning.
 */
export function intelligentRoutingErrorText(
  error: unknown,
  fallback: string,
): string {
  const details = errorText(error);
  if (details === "") return fallback;

  const code = errorCode(details);
  if (code !== null) {
    if (knownCodes.has(code)) {
      return i18n.t(
        `intelligentRouting.errors.${routingErrorCodes[code as keyof typeof routingErrorCodes]}`,
      );
    }
    return i18n.t("intelligentRouting.errors.codeUnknown", { code });
  }

  const transport = transportReason(details);
  if (transport !== null) {
    return i18n.t(`intelligentRouting.errors.${transport}`);
  }

  const status = errorStatus(details);
  if (status !== null) {
    return i18n.t("intelligentRouting.errors.status", { status });
  }

  return details;
}
