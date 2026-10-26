import { invalidData, type DataProblem } from "./ipc-data-error";

/**
 * An agent's request to read raw audit parts (plan §5.11). It names the
 * request the agent asked about; a lasting approval reaches every request.
 */
export interface RawAccessGrant {
  grant_id: string;
  request_id: string;
  status: RawAccessGrantStatus;
  /** How the operator decided; absent while the request is pending. */
  decision: RawAccessDecision | null;
  /** What an approval reaches; absent until one is given. */
  scope: RawAccessScope | null;
  /** Why the agent asked, in its own words. Untrusted display text. */
  reason: string;
  /** The agent's self-reported name (the CLI's `--agent`); it authorizes nothing. */
  client_name: string;
  created_at: string;
  /** Null for an approved `until_lock` grant, which only a lock ends. */
  expires_at: string | null;
}

export type RawAccessGrantStatus =
  | "pending"
  | "approved"
  | "denied"
  | "expired"
  | "consumed"
  | "revoked";

/**
 * `until_lock` runs until the screen locks, the computer sleeps, the
 * operator locks, or AstrLink quits; `window_1h` also ends after an hour.
 */
export type RawAccessDecision = "once" | "until_lock" | "window_1h" | "deny";

/** `request` for a one-time approval, `all_requests` for a lasting one. */
export type RawAccessScope = "request" | "all_requests";

/** What the approval window shows, read in one call. */
export interface RawAccessList {
  /** Requests awaiting the operator. */
  pending: RawAccessGrant[];
  /** Lasting grants still running. */
  active: RawAccessGrant[];
}

/**
 * What a decision ended in. Refusals the window can recover from are
 * outcomes, not errors, so it can ask for the password and try again.
 */
export type RawAccessProofOutcome =
  | { outcome: "decided"; grant: RawAccessGrant }
  | { outcome: "proof_required" }
  | { outcome: "password_invalid" }
  | { outcome: "backoff"; retry_after_seconds: number }
  | { outcome: "not_pending" }
  | { outcome: "biometric_cancelled" }
  | { outcome: "biometric_unavailable" };

type JsonObject = Record<string, unknown>;

const statuses = new Set<RawAccessGrantStatus>([
  "pending",
  "approved",
  "denied",
  "expired",
  "consumed",
  "revoked",
]);
const decisions = new Set<RawAccessDecision>([
  "once",
  "until_lock",
  "window_1h",
  "deny",
]);
const scopes = new Set<RawAccessScope>(["request", "all_requests"]);
const grantIDPattern = /^rawgrant_[0-9a-f]{16}$/;

function invalid(path: string, problem: DataProblem): never {
  return invalidData("rawAccess", path, problem);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "object");
  }
  return value as JsonObject;
}

function stringAt(value: unknown, path: string, maxLength: number): string {
  if (typeof value !== "string") return invalid(path, "string");
  if (value.length > maxLength) return invalid(path, "tooLong");
  return value;
}

function timestampAt(value: unknown, path: string): string {
  const text = stringAt(value, path, 64);
  if (Number.isNaN(Date.parse(text))) invalid(path, "timestamp");
  return text;
}

function parseGrant(value: unknown, path: string): RawAccessGrant {
  const grant = objectAt(value, path);
  const grantID = stringAt(grant.grant_id, `${path}.grant_id`, 64);
  if (!grantIDPattern.test(grantID)) invalid(`${path}.grant_id`, "badFormat");
  const status = stringAt(grant.status, `${path}.status`, 32);
  if (!statuses.has(status as RawAccessGrantStatus)) {
    invalid(`${path}.status`, "unknownStatus");
  }
  let decision: RawAccessDecision | null = null;
  if (grant.decision !== undefined && grant.decision !== "") {
    const raw = stringAt(grant.decision, `${path}.decision`, 32);
    if (!decisions.has(raw as RawAccessDecision)) {
      invalid(`${path}.decision`, "unknownDecision");
    }
    decision = raw as RawAccessDecision;
  }
  let scope: RawAccessScope | null = null;
  if (grant.scope !== undefined && grant.scope !== "") {
    const raw = stringAt(grant.scope, `${path}.scope`, 32);
    if (!scopes.has(raw as RawAccessScope)) {
      invalid(`${path}.scope`, "unknownScope");
    }
    scope = raw as RawAccessScope;
  }
  return {
    grant_id: grantID,
    request_id: stringAt(grant.request_id, `${path}.request_id`, 128),
    status: status as RawAccessGrantStatus,
    decision,
    scope,
    reason: stringAt(grant.reason, `${path}.reason`, 4096),
    client_name: stringAt(grant.client_name, `${path}.client_name`, 256),
    created_at: timestampAt(grant.created_at, `${path}.created_at`),
    expires_at:
      grant.expires_at === null
        ? null
        : timestampAt(grant.expires_at, `${path}.expires_at`),
  };
}

export function parseRawAccessList(value: unknown): RawAccessList {
  const list = objectAt(value, "$");
  if (!Array.isArray(list.items)) invalid("$.items", "array");
  if (!Array.isArray(list.active)) invalid("$.active", "array");
  return {
    pending: list.items.map((item, index) =>
      parseGrant(item, `$.items[${index}]`),
    ),
    active: list.active.map((item, index) =>
      parseGrant(item, `$.active[${index}]`),
    ),
  };
}

/** A running grant as the operator revoked it. */
export function parseRawAccessGrant(value: unknown): RawAccessGrant {
  return parseGrant(value, "$");
}

export function parseRawAccessProofOutcome(
  value: unknown,
): RawAccessProofOutcome {
  const outcome = objectAt(value, "$");
  switch (outcome.outcome) {
    case "decided":
      return {
        outcome: "decided",
        grant: parseGrant(outcome.grant, "$.grant"),
      };
    case "proof_required":
    case "password_invalid":
    case "not_pending":
    case "biometric_cancelled":
    case "biometric_unavailable":
      return { outcome: outcome.outcome };
    case "backoff": {
      const seconds = outcome.retry_after_seconds;
      if (
        typeof seconds !== "number" ||
        !Number.isInteger(seconds) ||
        seconds < 1
      ) {
        invalid("$.retry_after_seconds", "positiveInteger");
      }
      return { outcome: "backoff", retry_after_seconds: seconds };
    }
    default:
      return invalid("$.outcome", "unknownOutcome");
  }
}

/** The oldest request still awaiting the operator, if any. */
export function oldestPendingGrant(
  grants: RawAccessGrant[],
): RawAccessGrant | null {
  let oldest: RawAccessGrant | null = null;
  for (const grant of grants) {
    if (grant.status !== "pending") continue;
    if (
      !oldest ||
      Date.parse(grant.created_at) < Date.parse(oldest.created_at)
    ) {
      oldest = grant;
    }
  }
  return oldest;
}

/** Whether a running grant still runs at `now`; one without an end does. */
export function grantRunning(grant: RawAccessGrant, now: number): boolean {
  return grant.expires_at === null || Date.parse(grant.expires_at) > now;
}

/** Time left on a grant as `m:ss`, or `h:mm:ss` from an hour up. */
export function formatRemaining(expiresAt: string, now: number): string {
  const total = Math.max(0, Math.ceil((Date.parse(expiresAt) - now) / 1000));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = String(total % 60).padStart(2, "0");
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}`
    : `${minutes}:${seconds}`;
}
