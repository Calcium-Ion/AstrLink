export type AccessTokenSource = "system_default" | "user";

export interface AccessTokenSummary {
  id: string;
  name: string;
  hint: string;
  source: AccessTokenSource;
  created_at: string;
}

export interface AccessTokenPage {
  items: AccessTokenSummary[];
  next_cursor: null;
}

export interface AccessTokenCreateResult {
  token: AccessTokenSummary;
  access_token: string;
}

export interface AccessTokenRevealResult {
  access_token: string;
}

type JsonObject = Record<string, unknown>;

const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const rfc3339Pattern =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;
const accessTokenPattern =
  /^astr_[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/;
const sources = new Set<AccessTokenSource>(["system_default", "user"]);

function invalid(path: string, message: string): never {
  throw new Error(`Invalid access-token IPC response at ${path}: ${message}`);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as JsonObject;
}

function exactKeys(
  object: JsonObject,
  expected: readonly string[],
  path: string,
): void {
  const expectedSet = new Set(expected);
  for (const key of Object.keys(object)) {
    if (!expectedSet.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of expected) {
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

function parseAccessToken(
  value: unknown,
  path: string,
): AccessTokenSummary {
  const token = objectAt(value, path);
  exactKeys(token, ["id", "name", "hint", "source", "created_at"], path);

  const id = stringAt(token.id, `${path}.id`, 3, 96);
  if (!resourceIDPattern.test(id)) invalid(`${path}.id`, "invalid token ID");
  const name = stringAt(token.name, `${path}.name`, 1, 64);
  const hint = stringAt(token.hint, `${path}.hint`, 1, 32);
  if (accessTokenPattern.test(hint)) {
    invalid(`${path}.hint`, "must be a non-secret display hint");
  }
  if (
    typeof token.source !== "string" ||
    !sources.has(token.source as AccessTokenSource)
  ) {
    invalid(`${path}.source`, "unknown token source");
  }
  const createdAt = stringAt(token.created_at, `${path}.created_at`, 20, 64);
  if (!rfc3339Pattern.test(createdAt) || Number.isNaN(Date.parse(createdAt))) {
    invalid(`${path}.created_at`, "expected an RFC 3339 timestamp");
  }

  return {
    id,
    name,
    hint,
    source: token.source as AccessTokenSource,
    created_at: createdAt,
  };
}

function accessTokenAt(value: unknown, path: string): string {
  if (typeof value !== "string" || !accessTokenPattern.test(value)) {
    return invalid(path, "expected an astr_ token containing 32 random bytes");
  }
  return value;
}

export function parseAccessTokenPage(value: unknown): AccessTokenPage {
  const page = objectAt(value, "$");
  exactKeys(page, ["items", "next_cursor"], "$");
  if (!Array.isArray(page.items)) invalid("$.items", "expected an array");
  if (page.next_cursor !== null) invalid("$.next_cursor", "expected null");
  return {
    items: page.items.map((token, index) =>
      parseAccessToken(token, `$.items[${index}]`),
    ),
    next_cursor: null,
  };
}

export function parseAccessTokenCreateResult(
  value: unknown,
): AccessTokenCreateResult {
  const result = objectAt(value, "$");
  exactKeys(result, ["token", "access_token"], "$");
  return {
    token: parseAccessToken(result.token, "$.token"),
    access_token: accessTokenAt(result.access_token, "$.access_token"),
  };
}

export function parseAccessTokenRevealResult(
  value: unknown,
): AccessTokenRevealResult {
  const result = objectAt(value, "$");
  exactKeys(result, ["access_token"], "$");
  return {
    access_token: accessTokenAt(result.access_token, "$.access_token"),
  };
}
