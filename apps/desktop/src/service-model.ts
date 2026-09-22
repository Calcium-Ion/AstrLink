import { parseServiceProxy, type ServiceProxy, type ServiceProxyInput } from "./service-proxy-model";
import { parseFailurePolicy, type FailurePolicy } from "./failure-policy-model";
import { i18n } from "./i18n";
import type {
  SubscriptionError,
  SubscriptionProvider,
  SubscriptionStatus,
} from "./subscription-model";

export type HTTPServiceKind =
  | "opencode_go"
  | "opencode_zen"
  | "kimi_coding"
  | "glm_coding"
  | "minimax_coding"
  | "newapi"
  | "openai"
  | "anthropic"
  | "gemini"
  | "deepseek"
  | "qwen"
  | "moonshot"
  | "glm"
  | "minimax"
  | "doubao"
  | "xai"
  | "openai_compatible"
  | "custom";

export type ServiceAuthScheme =
  | "none"
  | "bearer"
  | "anthropic_api_key"
  | "google_api_key"
  | "custom_header";

export interface ServiceAuth {
  scheme: ServiceAuthScheme;
  header_name?: string;
}

export interface ServiceCapability {
  protocol: string;
  mode: "native" | "delegated";
  streaming: boolean;
  convert_to?: string;
}

export type ModelDiscoveryProtocol = "openai.models" | "google.models";

export type SubscriptionServiceKind =
  | "codex_subscription"
  | "claude_subscription"
  | "grok_subscription";
export type ServiceKind = SubscriptionServiceKind | HTTPServiceKind;

/** Provider owning each subscription kind; mirrors contract.ServiceKind.SubscriptionProvider. */
export const subscriptionKindProviders: Record<SubscriptionServiceKind, SubscriptionProvider> = {
  codex_subscription: "openai_codex",
  claude_subscription: "claude_code",
  grok_subscription: "xai_grok",
};

export const subscriptionKinds = Object.keys(
  subscriptionKindProviders,
) as readonly SubscriptionServiceKind[];

export function isSubscriptionKind(kind: unknown): kind is SubscriptionServiceKind {
  return typeof kind === "string" && Object.hasOwn(subscriptionKindProviders, kind);
}

/**
 * API-key coding plans whose provider publishes a first-party quota route
 * (mirrors core codingplan.Supports). OpenCode Zen is pay-as-you-go and has
 * no usage API, so it is deliberately absent.
 */
export const codingPlanUsageKinds: ReadonlySet<ServiceKind> = new Set<ServiceKind>([
  "opencode_go",
  "kimi_coding",
  "glm_coding",
  "minimax_coding",
]);

/** True when the service row can show a live plan quota meter. */
export function hasPlanUsage(service: {
  kind: ServiceKind;
  subscription?: { status: SubscriptionStatus } | null;
  http?: unknown;
}): boolean {
  if (service.subscription) return service.subscription.status === "connected";
  return codingPlanUsageKinds.has(service.kind) && service.http != null;
}

export interface HTTPServiceConnection {
  base_url: string;
  auth: ServiceAuth;
  credential_ref?: string;
}

export interface SubscriptionServiceConnection {
  provider: SubscriptionProvider;
  status: SubscriptionStatus;
  account_hint?: string;
  provider_account_id?: string;
  credential_ref?: string;
  authorization_boundary?: string;
  token_expires_at?: string;
  last_refresh_at?: string;
  last_error?: SubscriptionError;
}

export interface Service {
  proxy?: ServiceProxy;
  responses_websocket_enabled?: boolean;
  failure_policy?: FailurePolicy;
  id: string;
  name: string;
  kind: ServiceKind;
  enabled: boolean;
  models: string[];
  capabilities: ServiceCapability[];
  http?: HTTPServiceConnection;
  subscription?: SubscriptionServiceConnection;
  created_at: string;
  updated_at: string;
}

export type RoutableService = Pick<
  Service,
  "id" | "name" | "enabled" | "models" | "capabilities" | "failure_policy"
>;

export interface ServicePage {
  items: Service[];
  next_cursor: string | null;
}

export interface ServiceRecord {
  service: Service;
  etag: string;
}

export type SubscriptionServiceCreateInput = {
  proxy?: ServiceProxyInput | null;
  responses_websocket_enabled?: boolean;
  failure_policy?: FailurePolicy;
  name: string;
  kind: SubscriptionServiceKind;
  enabled?: boolean;
  models?: string[];
};

export type HTTPServiceCreateInput = {
  proxy?: ServiceProxyInput | null;
  responses_websocket_enabled?: boolean;
  failure_policy?: FailurePolicy;
  name: string;
  kind: HTTPServiceKind;
  enabled?: boolean;
  models?: string[];
  http: {
    base_url: string;
    auth: ServiceAuth;
    credential?: { secret: string };
  };
  capabilities: ServiceCapability[];
};

export type ServiceCreateInput =
  | SubscriptionServiceCreateInput
  | HTTPServiceCreateInput;

export type ServicePatchInput = {
  proxy?: ServiceProxyInput | null;
  responses_websocket_enabled?: boolean;
  failure_policy?: FailurePolicy | null;
  name?: string;
  enabled?: boolean;
  models?: string[];
  http?: {
    base_url?: string;
    auth?: ServiceAuth;
    credential?: { secret: string } | null;
  };
  capabilities?: ServiceCapability[];
};

export interface ServiceModelProbe {
  service_id?: string;
  protocol: ModelDiscoveryProtocol;
  model_ids: string[];
}

export interface DraftServiceModelProbeInput {
  proxy?: ServiceProxyInput | null;
  service_id?: string;
  kind: HTTPServiceKind;
  http: HTTPServiceCreateInput["http"];
  protocol: ModelDiscoveryProtocol;
}

type JsonObject = Record<string, unknown>;

const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const protocolIDPattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const headerNamePattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
const localCredentialRefPattern =
  /^local:\/\/service\/[a-z][a-z0-9_-]{2,95}$/;
const keyringCredentialRefPattern =
  /^keyring:\/\/[A-Za-z0-9._~-]+\/[A-Za-z0-9._~!$&'()*+,;=:@/-]*[A-Za-z0-9._~!$&'()*+,;=:@-]$/;
const rfc3339Pattern =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;
const etagPattern = /^"sha256:[0-9a-f]{64}"$/;
const errorCodePattern = /^[a-z][a-z0-9_]{1,63}$/;
const credentialLeakPattern =
  /(?:Bearer\s+[A-Za-z0-9._~+/=-]{12,}|code_verifier=[A-Za-z0-9._~-]{20,}|eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,})/i;

const httpKinds = new Set<HTTPServiceKind>([
  "opencode_go", "opencode_zen", "kimi_coding", "glm_coding", "minimax_coding",
  "newapi",
  "openai",
  "anthropic",
  "gemini",
  "deepseek",
  "qwen",
  "moonshot",
  "glm",
  "minimax",
  "doubao",
  "xai",
  "openai_compatible",
  "custom",
]);
const authSchemes = new Set<ServiceAuthScheme>([
  "none",
  "bearer",
  "anthropic_api_key",
  "google_api_key",
  "custom_header",
]);
const subscriptionStatuses = new Set<SubscriptionStatus>([
  "disconnected",
  "authorizing",
  "connected",
  "needs_reauth",
  "error",
]);

function invalid(path: string, message: string): never {
  throw new Error(`Invalid Service IPC response at ${path}: ${message}`);
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

function stringAt(value: unknown, path: string, min: number, max: number): string {
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

function parseAuth(value: unknown, path: string): ServiceAuth {
  const auth = objectAt(value, path);
  keysAt(auth, ["scheme"], ["header_name"], path);
  if (
    typeof auth.scheme !== "string" ||
    !authSchemes.has(auth.scheme as ServiceAuthScheme)
  ) {
    invalid(`${path}.scheme`, "unknown authentication scheme");
  }
  const scheme = auth.scheme as ServiceAuthScheme;
  if (scheme === "custom_header") {
    const headerName = stringAt(auth.header_name, `${path}.header_name`, 1, 128);
    if (!headerNamePattern.test(headerName)) {
      invalid(`${path}.header_name`, "invalid header name");
    }
    return { scheme, header_name: headerName };
  }
  if (Object.hasOwn(auth, "header_name")) {
    invalid(`${path}.header_name`, "only custom_header may set header_name");
  }
  return { scheme };
}

function parseCapability(value: unknown, path: string): ServiceCapability {
  const capability = objectAt(value, path);
  keysAt(capability, ["protocol", "mode", "streaming"], ["convert_to"], path);
  const protocol = stringAt(capability.protocol, `${path}.protocol`, 3, 96);
  if (!protocolIDPattern.test(protocol)) invalid(`${path}.protocol`, "invalid protocol ID");
  if (capability.mode !== "native" && capability.mode !== "delegated") {
    invalid(`${path}.mode`, "unknown capability mode");
  }
  if (typeof capability.streaming !== "boolean") {
    invalid(`${path}.streaming`, "expected a boolean");
  }
  const parsed: ServiceCapability = {
    protocol,
    mode: capability.mode,
    streaming: capability.streaming,
  };
  if (Object.hasOwn(capability, "convert_to")) {
    const convertTo = stringAt(capability.convert_to, `${path}.convert_to`, 3, 96);
    if (!protocolIDPattern.test(convertTo)) {
      invalid(`${path}.convert_to`, "invalid protocol ID");
    }
    if (convertTo === protocol) {
      invalid(`${path}.convert_to`, "must change protocol");
    }
    parsed.convert_to = convertTo;
  }
  return parsed;
}

function parseModels(value: unknown, path: string, maximum = 2_000): string[] {
  if (!Array.isArray(value) || value.length > maximum) {
    return invalid(path, `expected an array with at most ${maximum} items`);
  }
  const models = value.map((model, index) =>
    stringAt(model, `${path}[${index}]`, 1, 256),
  );
  if (new Set(models).size !== models.length) invalid(path, "duplicate model");
  return models;
}

function parseHTTPConnection(value: unknown, path: string): HTTPServiceConnection {
  const connection = objectAt(value, path);
  keysAt(connection, ["base_url", "auth"], ["credential_ref"], path);
  const baseURL = stringAt(connection.base_url, `${path}.base_url`, 1, 2048);
  let parsed: URL;
  try {
    parsed = new URL(baseURL);
  } catch {
    return invalid(`${path}.base_url`, "invalid URL");
  }
  if (
    (parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
    parsed.username !== "" ||
    parsed.password !== "" ||
    parsed.search !== "" ||
    parsed.hash !== ""
  ) {
    invalid(`${path}.base_url`, "unsafe upstream URL");
  }
  let credentialRef: string | undefined;
  if (Object.hasOwn(connection, "credential_ref")) {
    credentialRef = stringAt(connection.credential_ref, `${path}.credential_ref`, 1, 512);
    if (!localCredentialRefPattern.test(credentialRef)) {
      invalid(`${path}.credential_ref`, "must use local://service/<id>");
    }
  }
  return {
    base_url: baseURL,
    auth: parseAuth(connection.auth, `${path}.auth`),
    ...(credentialRef ? { credential_ref: credentialRef } : {}),
  };
}

function parseSubscriptionError(value: unknown, path: string): SubscriptionError {
  const error = objectAt(value, path);
  keysAt(error, ["code", "message"], [], path);
  const code = stringAt(error.code, `${path}.code`, 2, 64);
  const message = stringAt(error.message, `${path}.message`, 1, 240);
  if (!errorCodePattern.test(code)) invalid(`${path}.code`, "invalid error code");
  if (credentialLeakPattern.test(message)) {
    invalid(`${path}.message`, "must not contain credential material");
  }
  return { code, message };
}

function parseSubscriptionConnection(
  value: unknown,
  path: string,
): SubscriptionServiceConnection {
  const subscription = objectAt(value, path);
  keysAt(
    subscription,
    ["provider", "status"],
    [
      "account_hint",
      "provider_account_id",
      "credential_ref",
      "authorization_boundary",
      "token_expires_at",
      "last_refresh_at",
      "last_error",
    ],
    path,
  );
  if (
    subscription.provider !== "openai_codex" &&
    subscription.provider !== "claude_code" &&
    subscription.provider !== "xai_grok"
  ) {
    invalid(`${path}.provider`, "unknown subscription provider");
  }
  if (
    typeof subscription.status !== "string" ||
    !subscriptionStatuses.has(subscription.status as SubscriptionStatus)
  ) {
    invalid(`${path}.status`, "unknown subscription status");
  }
  const status = subscription.status as SubscriptionStatus;
  const result: SubscriptionServiceConnection = {
    provider: subscription.provider,
    status,
  };
  for (const [field, max] of [
    ["account_hint", 128],
    ["provider_account_id", 256],
    ["authorization_boundary", 240],
  ] as const) {
    if (!Object.hasOwn(subscription, field)) continue;
    const text = stringAt(subscription[field], `${path}.${field}`, 1, max);
    if (credentialLeakPattern.test(text)) {
      invalid(`${path}.${field}`, "must not contain credential material");
    }
    result[field] = text;
  }
  if (Object.hasOwn(subscription, "credential_ref")) {
    const credentialRef = stringAt(
      subscription.credential_ref,
      `${path}.credential_ref`,
      1,
      512,
    );
    if (!keyringCredentialRefPattern.test(credentialRef)) {
      invalid(`${path}.credential_ref`, "must use keyring://");
    }
    result.credential_ref = credentialRef;
  }
  if (Object.hasOwn(subscription, "token_expires_at")) {
    result.token_expires_at = timestampAt(
      subscription.token_expires_at,
      `${path}.token_expires_at`,
    );
  }
  if (Object.hasOwn(subscription, "last_refresh_at")) {
    result.last_refresh_at = timestampAt(
      subscription.last_refresh_at,
      `${path}.last_refresh_at`,
    );
  }
  if (Object.hasOwn(subscription, "last_error")) {
    result.last_error = parseSubscriptionError(
      subscription.last_error,
      `${path}.last_error`,
    );
  }
  return result;
}

export function parseService(value: unknown, path = "$"): Service {
  const service = objectAt(value, path);
  keysAt(
    service,
    ["id", "name", "kind", "enabled", "models", "capabilities", "created_at", "updated_at"],
    ["http", "subscription", "failure_policy", "responses_websocket_enabled", "proxy"],
    path,
  );
  const id = stringAt(service.id, `${path}.id`, 3, 96);
  if (!resourceIDPattern.test(id)) invalid(`${path}.id`, "invalid service ID");
  const name = stringAt(service.name, `${path}.name`, 1, 128);
  if (
    !isSubscriptionKind(service.kind) &&
    (typeof service.kind !== "string" ||
      !httpKinds.has(service.kind as HTTPServiceKind))
  ) {
    invalid(`${path}.kind`, "unknown service kind");
  }
  if (typeof service.enabled !== "boolean") invalid(`${path}.enabled`, "expected a boolean");
  if (Object.hasOwn(service, "responses_websocket_enabled") && typeof service.responses_websocket_enabled !== "boolean") {
    invalid(`${path}.responses_websocket_enabled`, "expected a boolean");
  }
  const websocketSetting = Object.hasOwn(service, "responses_websocket_enabled")
    ? { responses_websocket_enabled: service.responses_websocket_enabled as boolean } : {};
  const models = parseModels(service.models, `${path}.models`);
  if (!Array.isArray(service.capabilities)) {
    invalid(`${path}.capabilities`, "expected an array");
  }
  const capabilities = service.capabilities.map((capability, index) =>
    parseCapability(capability, `${path}.capabilities[${index}]`),
  );
  const createdAt = timestampAt(service.created_at, `${path}.created_at`);
  const updatedAt = timestampAt(service.updated_at, `${path}.updated_at`);
  if (Date.parse(updatedAt) < Date.parse(createdAt)) {
    invalid(`${path}.updated_at`, "must not precede created_at");
  }

  if (isSubscriptionKind(service.kind)) {
    if (!Object.hasOwn(service, "subscription") || Object.hasOwn(service, "http")) {
      invalid(path, "subscription service requires only subscription");
    }
    const subscription = parseSubscriptionConnection(service.subscription, `${path}.subscription`);
    if (subscription.provider !== subscriptionKindProviders[service.kind]) {
      invalid(`${path}.subscription.provider`, "provider does not match service kind");
    }
    return {
      id,
      name,
      kind: service.kind,
      enabled: service.enabled,
      ...websocketSetting,
      ...(service.proxy !== undefined ? { proxy: parseServiceProxy(service.proxy, id) } : {}),
      models,
      capabilities,
      subscription,
      created_at: createdAt,
      updated_at: updatedAt,
      ...(Object.hasOwn(service, "failure_policy") ? { failure_policy: parseFailurePolicy(service.failure_policy, `${path}.failure_policy`) } : {}),
    };
  }
  if (!Object.hasOwn(service, "http") || Object.hasOwn(service, "subscription")) {
    invalid(path, "HTTP service requires only http");
  }
  return {
    id,
    name,
    kind: service.kind as HTTPServiceKind,
    enabled: service.enabled,
    ...websocketSetting,
      ...(service.proxy !== undefined ? { proxy: parseServiceProxy(service.proxy, id) } : {}),
    models,
    capabilities,
    http: parseHTTPConnection(service.http, `${path}.http`),
    created_at: createdAt,
    updated_at: updatedAt,
      ...(Object.hasOwn(service, "failure_policy") ? { failure_policy: parseFailurePolicy(service.failure_policy, `${path}.failure_policy`) } : {}),
  };
}

export function parseServiceModelProbe(value: unknown): ServiceModelProbe {
  const probe = objectAt(value, "$");
  keysAt(probe, ["protocol", "model_ids"], ["service_id"], "$");
  if (probe.protocol !== "openai.models" && probe.protocol !== "google.models") {
    invalid("$.protocol", "unknown model discovery protocol");
  }
  const result: ServiceModelProbe = {
    protocol: probe.protocol,
    model_ids: parseModels(probe.model_ids, "$.model_ids", 10_000),
  };
  if (Object.hasOwn(probe, "service_id")) {
    const serviceID = stringAt(probe.service_id, "$.service_id", 3, 96);
    if (!resourceIDPattern.test(serviceID)) invalid("$.service_id", "invalid service ID");
    result.service_id = serviceID;
  }
  return result;
}

export function parseServicePage(value: unknown): ServicePage {
  const page = objectAt(value, "$");
  keysAt(page, ["items", "next_cursor"], [], "$");
  if (!Array.isArray(page.items)) invalid("$.items", "expected an array");
  const nextCursor =
    page.next_cursor === null
      ? null
      : stringAt(page.next_cursor, "$.next_cursor", 1, 512);
  return {
    items: page.items.map((service, index) =>
      parseService(service, `$.items[${index}]`),
    ),
    next_cursor: nextCursor,
  };
}

export function parseServiceRecord(value: unknown): ServiceRecord {
  const record = objectAt(value, "$");
  keysAt(record, ["service", "etag"], [], "$");
  const etag = stringAt(record.etag, "$.etag", 3, 128);
  if (!etagPattern.test(etag)) invalid("$.etag", "invalid strong entity tag");
  return { service: parseService(record.service, "$.service"), etag };
}

export function serviceKindLabel(kind: ServiceKind): string {
  return i18n.t(`kind.${kind}`);
}

export function serviceStatusLabel(service: Service): string {
  if (!service.enabled) return i18n.t("common.disabled");
  if (!service.subscription) return i18n.t("common.enabled");
  return i18n.t(`subscription.${service.subscription.status}`);
}

export function supportsResponsesWebSocket(service: Pick<Service, "kind" | "capabilities">): boolean {
  return service.kind === "codex_subscription" || service.capabilities.some(capability =>
    capability.protocol === "openai.responses" && capability.streaming && !capability.convert_to);
}

export function responsesWebSocketEnabled(service: Pick<Service, "kind" | "responses_websocket_enabled">): boolean {
  return service.responses_websocket_enabled ?? service.kind === "codex_subscription";
}
