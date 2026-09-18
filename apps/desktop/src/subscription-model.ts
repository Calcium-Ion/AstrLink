export type SubscriptionProvider = "openai_codex" | "claude_code";

export type SubscriptionStatus =
  | "disconnected"
  | "authorizing"
  | "connected"
  | "needs_reauth"
  | "error";

export type AuthorizationSessionStatus =
  | "pending"
  | "completed"
  | "cancelled"
  | "expired"
  | "failed";

export type AuthorizationFlow = "browser" | "device_code" | "authorization_code";

export interface SubscriptionError {
  code: string;
  message: string;
}

export interface AuthorizationDeviceCode {
  verification_url: string;
  user_code: string;
}

export interface AuthorizationSession {
  id: string;
  provider: SubscriptionProvider;
  status: AuthorizationSessionStatus;
  flow: AuthorizationFlow;
  authorization_url?: string;
  device_code?: AuthorizationDeviceCode;
  service_id: string;
  expires_at: string;
  error?: SubscriptionError;
  created_at: string;
  updated_at: string;
}

export type BeginCodexAuthorizationResult = {
  kind: "session";
  session: AuthorizationSession;
};

type JsonObject = Record<string, unknown>;

const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const subscriptionErrorCodePattern = /^[a-z][a-z0-9_]{1,63}$/;
const rfc3339Pattern =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;
const credentialLeakPattern =
  /(?:Bearer\s+[A-Za-z0-9._~+/=-]{12,}|(?:access_token|refresh_token|id_token|device_auth_id|code_verifier|authorization_code)["']?\s*[:=]\s*["']?[A-Za-z0-9._~+/=-]{8,}|eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,})/i;

const providers = new Set<SubscriptionProvider>(["openai_codex", "claude_code"]);
const authorizationSessionStatuses = new Set<AuthorizationSessionStatus>([
  "pending",
  "completed",
  "cancelled",
  "expired",
  "failed",
]);
const authorizationFlows = new Set<AuthorizationFlow>([
  "authorization_code",
  "browser",
  "device_code",
]);

function invalid(path: string, message: string): never {
  throw new Error(`Invalid subscription IPC response at ${path}: ${message}`);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as JsonObject;
}

function keysAt(
  object: JsonObject,
  required: readonly string[],
  optional: readonly string[],
  path: string,
): void {
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(object)) {
    if (!allowed.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of required) {
    if (!Object.hasOwn(object, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function stringAt(
  value: unknown,
  path: string,
  min: number,
  max: number,
): string {
  if (typeof value !== "string") {
    return invalid(path, `expected ${min} to ${max} characters`);
  }
  const length = [...value].length;
  if (length < min || length > max) {
    return invalid(path, `expected ${min} to ${max} characters`);
  }
  return value;
}

function timestampAt(value: unknown, path: string): string {
  const timestamp = stringAt(value, path, 20, 64);
  if (!rfc3339Pattern.test(timestamp) || Number.isNaN(Date.parse(timestamp))) {
    invalid(path, "expected an RFC 3339 timestamp");
  }
  return timestamp;
}

function rejectCredentialLeak(value: string, path: string): void {
  if (credentialLeakPattern.test(value)) {
    invalid(path, "must not contain credential material");
  }
}

function validateAuthorizationURL(value: string, path: string): void {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    invalid(path, "invalid authorization URL");
  }
  if (parsed.protocol === "https:") {
    if (!parsed.hostname) invalid(path, "authorization URL must be absolute https");
    return;
  }
  if (parsed.protocol === "http:") {
    const host = parsed.hostname.toLowerCase();
    if (host !== "127.0.0.1" && host !== "localhost") {
      invalid(path, "authorization URL http is only allowed on loopback");
    }
    return;
  }
  invalid(path, "authorization URL must use https");
}

function parseSubscriptionError(value: unknown, path: string): SubscriptionError {
  const error = objectAt(value, path);
  keysAt(error, ["code", "message"], [], path);
  const code = stringAt(error.code, `${path}.code`, 1, 64);
  if (!subscriptionErrorCodePattern.test(code)) {
    invalid(`${path}.code`, "invalid subscription error code");
  }
  const message = stringAt(error.message, `${path}.message`, 1, 240);
  rejectCredentialLeak(message, `${path}.message`);
  return { code, message };
}

function parseAuthorizationDeviceCode(
  value: unknown,
  path: string,
): AuthorizationDeviceCode {
  const device = objectAt(value, path);
  keysAt(device, ["verification_url", "user_code"], [], path);
  const verificationURL = stringAt(
    device.verification_url,
    `${path}.verification_url`,
    8,
    4096,
  );
  validateAuthorizationURL(verificationURL, `${path}.verification_url`);
  const userCode = stringAt(device.user_code, `${path}.user_code`, 1, 128);
  if (/[\r\n\u0000]/u.test(userCode) || userCode.trim() === "") {
    invalid(`${path}.user_code`, "invalid Device Code");
  }
  return {
    verification_url: verificationURL,
    user_code: userCode,
  };
}

export function parseAuthorizationSession(value: unknown): AuthorizationSession {
  const session = objectAt(value, "$");
  keysAt(
    session,
    [
      "id",
      "provider",
      "status",
      "flow",
      "service_id",
      "expires_at",
      "created_at",
      "updated_at",
    ],
    ["authorization_url", "device_code", "error"],
    "$",
  );

  const id = stringAt(session.id, "$.id", 3, 96);
  if (!resourceIDPattern.test(id)) invalid("$.id", "invalid authorization session ID");
  if (
    typeof session.provider !== "string" ||
    !providers.has(session.provider as SubscriptionProvider)
  ) {
    invalid("$.provider", "unknown subscription provider");
  }
  if (
    typeof session.status !== "string" ||
    !authorizationSessionStatuses.has(session.status as AuthorizationSessionStatus)
  ) {
    invalid("$.status", "unknown authorization session status");
  }
  const status = session.status as AuthorizationSessionStatus;
  if (
    typeof session.flow !== "string" ||
    !authorizationFlows.has(session.flow as AuthorizationFlow)
  ) {
    invalid("$.flow", "unknown authorization flow");
  }
  const flow = session.flow as AuthorizationFlow;
  if ((session.provider === "claude_code") !== (flow === "authorization_code")) {
    invalid("$.flow", "authorization flow is unsupported by provider");
  }

  let authorizationURL: string | undefined;
  if (Object.hasOwn(session, "authorization_url")) {
    authorizationURL = stringAt(session.authorization_url, "$.authorization_url", 8, 4096);
    validateAuthorizationURL(authorizationURL, "$.authorization_url");
  }
  const deviceCode = Object.hasOwn(session, "device_code")
    ? parseAuthorizationDeviceCode(session.device_code, "$.device_code")
    : undefined;
  if (status === "pending" && (flow === "browser" || flow === "authorization_code")) {
    if (!authorizationURL) {
      invalid("$.authorization_url", "pending browser session requires authorization_url");
    }
    if (deviceCode) {
      invalid("$.device_code", "browser session must not include Device Code");
    }
  } else if (status === "pending" && flow === "device_code") {
    if (!deviceCode) {
      invalid("$.device_code", "pending Device Code session requires device_code");
    }
    if (authorizationURL) {
      invalid("$.authorization_url", "Device Code session must not include authorization_url");
    }
  } else if (authorizationURL || deviceCode) {
    invalid("$", "terminal session must not include login instructions");
  }

  const serviceID = stringAt(session.service_id, "$.service_id", 3, 96);
  if (!resourceIDPattern.test(serviceID)) invalid("$.service_id", "invalid service ID");

  let error: SubscriptionError | undefined;
  if (Object.hasOwn(session, "error")) {
    error = parseSubscriptionError(session.error, "$.error");
  }

  const expiresAt = timestampAt(session.expires_at, "$.expires_at");
  const createdAt = timestampAt(session.created_at, "$.created_at");
  const updatedAt = timestampAt(session.updated_at, "$.updated_at");

  return {
    id,
    provider: session.provider as SubscriptionProvider,
    status,
    flow,
    service_id: serviceID,
    expires_at: expiresAt,
    created_at: createdAt,
    updated_at: updatedAt,
    ...(authorizationURL ? { authorization_url: authorizationURL } : {}),
    ...(deviceCode ? { device_code: deviceCode } : {}),
    ...(error ? { error } : {}),
  };
}

export function parseBeginCodexAuthorizationResult(
  value: unknown,
): BeginCodexAuthorizationResult {
  const result = objectAt(value, "$");
  if (result.kind === "session") {
    keysAt(result, ["kind", "session"], [], "$");
    return {
      kind: "session",
      session: parseAuthorizationSession(result.session),
    };
  }
  invalid("$.kind", "unknown begin authorization result kind");
}
