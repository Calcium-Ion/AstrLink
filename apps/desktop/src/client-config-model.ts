/** Clients the setup dialog lists. */
export type ClientConfigClient =
  | "claude"
  | "codex"
  | "gemini"
  | "opencode"
  | "openclaw"
  | "pi";

/** Clients AstrLink writes itself; the others go through CC Switch. */
export const directClients = ["claude", "codex", "pi"] as const;
export type DirectClient = (typeof directClients)[number];

export function isDirectClient(
  client: ClientConfigClient,
): client is DirectClient {
  return (directClients as readonly string[]).includes(client);
}

/** CC Switch has no Pi app, so only AstrLink can configure Pi. */
export function ccSwitchImports(client: ClientConfigClient): boolean {
  return client !== "pi";
}

export interface ClientConfigModels {
  model?: string;
  haikuModel?: string;
  sonnetModel?: string;
  opusModel?: string;
  /** Claude Code only; CC Switch drops it. */
  fableModel?: string;
}

export type ClientConfigState =
  | "not_configured"
  | "configured"
  /** Still ours, pointing at a port the gateway no longer uses. */
  | "outdated"
  /** A connection setting changed since AstrLink wrote it. */
  | "modified"
  /** Moved to CC Switch, which now holds the connection. */
  | "cc_switch"
  /** The config file cannot be read as its format. */
  | "invalid";

export interface ClientConfigStatus {
  client: DirectClient;
  detected: boolean;
  paths: string[];
  state: ClientConfigState;
  token_id: string | null;
}

export type ClientConfigApplyOutcome =
  | { status: "applied" }
  /** Nothing was written: these keys hold values AstrLink did not write. */
  | { status: "needs_confirmation"; keys: string[] };

/** How Codex, which honours the system proxy, reaches one gateway address. */
export type ClientProxyRoute =
  | { route: "direct" }
  /** Through the proxy, which still hands requests back to the gateway. */
  | { route: "proxied"; proxy: string }
  /** Through the proxy, which never hands requests back to the gateway. */
  | { route: "blocked"; proxy: string };

export interface ClientProxyCheck {
  /** The address AstrLink writes into client configs. */
  client: ClientProxyRoute;
  /** `127.0.0.1` on the same port while the client address is localhost. */
  numeric: ClientProxyRoute | null;
}

type JsonObject = Record<string, unknown>;

const states: readonly ClientConfigState[] = [
  "not_configured",
  "configured",
  "outdated",
  "modified",
  "cc_switch",
  "invalid",
];
const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const accessTokenPattern = /astr_[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]/;
// eslint-disable-next-line no-control-regex -- Paths and key names must stay on one line.
const controlPattern = /[\u0000-\u001f\u007f]/;

function invalid(path: string, message: string): never {
  throw new Error(`Invalid client-config IPC response at ${path}: ${message}`);
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

function lineAt(value: unknown, path: string, max: number): string {
  if (
    typeof value !== "string" ||
    value.length === 0 ||
    [...value].length > max ||
    controlPattern.test(value)
  ) {
    return invalid(path, `expected 1 to ${max} characters on one line`);
  }
  return value;
}

function arrayAt(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) return invalid(path, "expected an array");
  return value;
}

function parseStatus(value: unknown, path: string): ClientConfigStatus {
  const status = objectAt(value, path);
  exactKeys(status, ["client", "detected", "paths", "state", "token_id"], path);
  if (
    typeof status.client !== "string" ||
    !(directClients as readonly string[]).includes(status.client)
  ) {
    invalid(`${path}.client`, "expected claude, codex, or pi");
  }
  if (typeof status.detected !== "boolean") {
    invalid(`${path}.detected`, "expected a boolean");
  }
  const paths = arrayAt(status.paths, `${path}.paths`).map((item, index) =>
    lineAt(item, `${path}.paths[${index}]`, 4096),
  );
  if (paths.length === 0) invalid(`${path}.paths`, "expected a path");
  if (!states.includes(status.state as ClientConfigState)) {
    invalid(`${path}.state`, "unknown state");
  }
  const state = status.state as ClientConfigState;
  let tokenID: string | null = null;
  if (status.token_id !== null) {
    tokenID = lineAt(status.token_id, `${path}.token_id`, 96);
    if (!resourceIDPattern.test(tokenID)) {
      invalid(`${path}.token_id`, "invalid token ID");
    }
  }
  // Only a config AstrLink wrote names its token. An unreadable file may or
  // may not have been written by AstrLink.
  const written = state !== "not_configured" && state !== "cc_switch";
  if (state !== "invalid" && written !== (tokenID !== null)) {
    invalid(`${path}.token_id`, "does not match the state");
  }
  return {
    client: status.client as DirectClient,
    detected: status.detected as boolean,
    paths,
    state,
    token_id: tokenID,
  };
}

/** Parses `client_config_status`: one entry per direct client, in order. */
export function parseClientConfigStatuses(
  value: unknown,
): ClientConfigStatus[] {
  const statuses = arrayAt(value, "$").map((item, index) =>
    parseStatus(item, `$[${index}]`),
  );
  if (
    statuses.length !== directClients.length ||
    statuses.some((status, index) => status.client !== directClients[index])
  ) {
    invalid("$", "expected claude, codex, and pi");
  }
  return statuses;
}

/** Parses `apply_client_config`. Conflicts are key names, never values. */
export function parseClientConfigApplyOutcome(
  value: unknown,
): ClientConfigApplyOutcome {
  const outcome = objectAt(value, "$");
  if (outcome.status === "applied") {
    exactKeys(outcome, ["status"], "$");
    return { status: "applied" };
  }
  if (outcome.status !== "needs_confirmation") {
    return invalid("$.status", "unknown status");
  }
  exactKeys(outcome, ["status", "keys"], "$");
  const keys = arrayAt(outcome.keys, "$.keys").map((key, index) =>
    lineAt(key, `$.keys[${index}]`, 512),
  );
  if (keys.length === 0) invalid("$.keys", "expected at least one key");
  return { status: "needs_confirmation", keys };
}

/** Parses `preview_client_config_snippet`, which must hold no token. */
export function parseClientConfigSnippet(value: unknown): string {
  if (typeof value !== "string" || value.trim() === "") {
    return invalid("$", "expected a config snippet");
  }
  if (accessTokenPattern.test(value)) {
    invalid("$", "must show the token hint, not the token");
  }
  return value;
}

/** Parses `copy_client_config_snippet`: false when the clipboard refused it. */
export function parseClientConfigCopied(value: unknown): boolean {
  if (typeof value !== "boolean") invalid("$", "expected a boolean");
  return value;
}

function parseProxyRoute(value: unknown, path: string): ClientProxyRoute {
  const route = objectAt(value, path);
  if (route.route === "direct") {
    exactKeys(route, ["route"], path);
    return { route: "direct" };
  }
  if (route.route !== "proxied" && route.route !== "blocked") {
    return invalid(`${path}.route`, "unknown route");
  }
  exactKeys(route, ["route", "proxy"], path);
  return {
    route: route.route,
    proxy: lineAt(route.proxy, `${path}.proxy`, 255),
  };
}

/** Parses `check_client_proxy`, which names the proxy by host and port only. */
export function parseClientProxyCheck(value: unknown): ClientProxyCheck {
  const check = objectAt(value, "$");
  exactKeys(check, ["client", "numeric"], "$");
  return {
    client: parseProxyRoute(check.client, "$.client"),
    numeric:
      check.numeric === null
        ? null
        : parseProxyRoute(check.numeric, "$.numeric"),
  };
}
