export type EndpointKind =
  | "newapi"
  | "openai"
  | "anthropic"
  | "gemini"
  | "openai_compatible"
  | "custom";

export type AuthScheme =
  | "none"
  | "bearer"
  | "anthropic_api_key"
  | "google_api_key"
  | "custom_header";

export interface EndpointAuth {
  scheme: AuthScheme;
  header_name?: string;
}

export interface EndpointCapability {
  protocol: string;
  mode: "native" | "delegated";
  streaming: boolean;
  models?: string[];
}

export interface Endpoint {
  id: string;
  name: string;
  kind: EndpointKind;
  base_url: string;
  auth: EndpointAuth;
  credential_ref?: string;
  enabled: boolean;
  capabilities: EndpointCapability[];
}

export interface EndpointPage {
  items: Endpoint[];
  next_cursor: string | null;
}

export interface EndpointRecord {
  endpoint: Endpoint;
  etag: string;
}

export interface EndpointCreateInput {
  name: string;
  kind: EndpointKind;
  base_url: string;
  auth: EndpointAuth;
  enabled?: boolean;
  credential?: { secret: string };
  capabilities: EndpointCapability[];
}

export type EndpointPatchInput = Partial<
  Pick<EndpointCreateInput, "name" | "kind" | "base_url" | "auth" | "enabled" | "capabilities">
> & { credential?: { secret: string } | null };

type JsonObject = Record<string, unknown>;

const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const protocolIDPattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const credentialRefPattern =
  /^(?:local:\/\/endpoint\/[a-z][a-z0-9_-]{2,95}|keyring:\/\/[A-Za-z0-9._~-]+\/[A-Za-z0-9._~!$&'()*+,;=:@/-]*[A-Za-z0-9._~!$&'()*+,;=:@-])$/;
const headerNamePattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
const etagPattern = /^"sha256:[0-9a-f]{64}"$/;
const endpointKinds = new Set<EndpointKind>([
  "newapi",
  "openai",
  "anthropic",
  "gemini",
  "openai_compatible",
  "custom",
]);
const authSchemes = new Set<AuthScheme>([
  "none",
  "bearer",
  "anthropic_api_key",
  "google_api_key",
  "custom_header",
]);

function invalid(path: string, message: string): never {
  throw new Error(`Invalid Endpoint IPC response at ${path}: ${message}`);
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
  const characterCount = [...value].length;
  if (characterCount < min || characterCount > max) {
    return invalid(path, `expected ${min} to ${max} characters`);
  }
  return value;
}

function parseAuth(value: unknown, path: string): EndpointAuth {
  const auth = objectAt(value, path);
  keysAt(auth, ["scheme"], ["header_name"], path);
  if (typeof auth.scheme !== "string" || !authSchemes.has(auth.scheme as AuthScheme)) {
    invalid(`${path}.scheme`, "unknown authentication scheme");
  }
  const scheme = auth.scheme as AuthScheme;
  if (scheme === "custom_header") {
    const headerName = stringAt(auth.header_name, `${path}.header_name`, 1, 128);
    if (!headerNamePattern.test(headerName)) invalid(`${path}.header_name`, "invalid header name");
    return { scheme, header_name: headerName };
  }
  if (Object.hasOwn(auth, "header_name")) {
    invalid(`${path}.header_name`, "only custom_header may set header_name");
  }
  return { scheme };
}

function parseCapability(value: unknown, path: string): EndpointCapability {
  const capability = objectAt(value, path);
  keysAt(capability, ["protocol", "mode", "streaming"], ["models"], path);
  const protocol = stringAt(capability.protocol, `${path}.protocol`, 3, 96);
  if (!protocolIDPattern.test(protocol)) invalid(`${path}.protocol`, "invalid protocol ID");
  if (capability.mode !== "native" && capability.mode !== "delegated") {
    invalid(`${path}.mode`, "unknown endpoint capability mode");
  }
  if (typeof capability.streaming !== "boolean") {
    invalid(`${path}.streaming`, "expected a boolean");
  }
  let models: string[] | undefined;
  if (Object.hasOwn(capability, "models")) {
    if (!Array.isArray(capability.models)) invalid(`${path}.models`, "expected an array");
    models = capability.models.map((model, index) =>
      stringAt(model, `${path}.models[${index}]`, 1, 256),
    );
    if (new Set(models).size !== models.length) invalid(`${path}.models`, "duplicate model");
  }
  return {
    protocol,
    mode: capability.mode,
    streaming: capability.streaming,
    ...(models ? { models } : {}),
  };
}

export function parseEndpoint(value: unknown, path = "$"): Endpoint {
  const endpoint = objectAt(value, path);
  keysAt(
    endpoint,
    ["id", "name", "kind", "base_url", "auth", "enabled", "capabilities"],
    ["credential_ref"],
    path,
  );
  const id = stringAt(endpoint.id, `${path}.id`, 3, 96);
  if (!resourceIDPattern.test(id)) invalid(`${path}.id`, "invalid endpoint ID");
  const name = stringAt(endpoint.name, `${path}.name`, 1, 128);
  if (typeof endpoint.kind !== "string" || !endpointKinds.has(endpoint.kind as EndpointKind)) {
    invalid(`${path}.kind`, "unknown endpoint kind");
  }
  const baseURL = stringAt(endpoint.base_url, `${path}.base_url`, 1, 2048);
  let parsedURL: URL;
  try {
    parsedURL = new URL(baseURL);
  } catch {
    return invalid(`${path}.base_url`, "invalid URL");
  }
  if (
    (parsedURL.protocol !== "http:" && parsedURL.protocol !== "https:") ||
    parsedURL.username !== "" ||
    parsedURL.password !== "" ||
    parsedURL.search !== "" ||
    parsedURL.hash !== ""
  ) {
    invalid(`${path}.base_url`, "unsafe upstream URL");
  }
  if (typeof endpoint.enabled !== "boolean") invalid(`${path}.enabled`, "expected a boolean");
  if (!Array.isArray(endpoint.capabilities)) invalid(`${path}.capabilities`, "expected an array");
  const capabilities = endpoint.capabilities.map((capability, index) =>
    parseCapability(capability, `${path}.capabilities[${index}]`),
  );
  let credentialRef: string | undefined;
  if (Object.hasOwn(endpoint, "credential_ref")) {
    credentialRef = stringAt(endpoint.credential_ref, `${path}.credential_ref`, 1, 512);
    if (!credentialRefPattern.test(credentialRef)) {
      invalid(`${path}.credential_ref`, "invalid credential reference");
    }
  }
  return {
    id,
    name,
    kind: endpoint.kind as EndpointKind,
    base_url: baseURL,
    auth: parseAuth(endpoint.auth, `${path}.auth`),
    ...(credentialRef ? { credential_ref: credentialRef } : {}),
    enabled: endpoint.enabled,
    capabilities,
  };
}

export function parseEndpointPage(value: unknown): EndpointPage {
  const page = objectAt(value, "$");
  keysAt(page, ["items", "next_cursor"], [], "$");
  if (!Array.isArray(page.items)) invalid("$.items", "expected an array");
  const nextCursor =
    page.next_cursor === null
      ? null
      : stringAt(page.next_cursor, "$.next_cursor", 1, 512);
  return {
    items: page.items.map((endpoint, index) => parseEndpoint(endpoint, `$.items[${index}]`)),
    next_cursor: nextCursor,
  };
}

export function parseEndpointRecord(value: unknown): EndpointRecord {
  const record = objectAt(value, "$");
  keysAt(record, ["endpoint", "etag"], [], "$");
  const etag = stringAt(record.etag, "$.etag", 3, 128);
  if (!etagPattern.test(etag)) invalid("$.etag", "invalid strong entity tag");
  return { endpoint: parseEndpoint(record.endpoint, "$.endpoint"), etag };
}
