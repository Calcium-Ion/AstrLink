/**
 * A client id from the host's catalog, such as `claude` or `workbuddy`. The
 * host lists which clients exist; the UI keys icons and copy by id.
 */
export type ClientConfigClient = string;

/**
 * The six clients AstrLink wrote first. They keep their own settings (Claude
 * Code's tiers, Codex's reasoning effort, Pi's limits) and manual snippets.
 */
export const clientConfigClients = [
  "claude",
  "codex",
  "gemini",
  "opencode",
  "openclaw",
  "pi",
] as const;
export type BuiltinClient = (typeof clientConfigClients)[number];

export function isBuiltinClient(client: string): client is BuiltinClient {
  return (clientConfigClients as readonly string[]).includes(client);
}

/** How AstrLink reaches a client. */
export const clientKinds = [
  /** AstrLink writes the client's config files. */
  "file",
  /** Written like `file`; the user pastes the token in the client once. */
  "paste",
  /** The client imports AstrLink through its own link. */
  "link",
  /** AstrLink lists what to fill in the client's own settings. */
  "guide",
  /** The client cannot reach a gateway on this computer. */
  "unsupported",
] as const;
export type ClientKind = (typeof clientKinds)[number];

/** The gateway protocols a client can be written with. */
export const clientProtocolIds = [
  "anthropic.messages",
  "openai.responses",
  "openai.chat",
  "google.generate_content",
] as const;

export interface ClientCatalogEntry {
  id: ClientConfigClient;
  /** The product name, never translated. */
  name: string;
  kind: ClientKind;
  /** Write targets of a client with several, such as `cn` and `intl`. */
  targets: string[];
  /** The protocols the client can be written with, preferred first. */
  protocols: string[];
  /** The client switches between a written list of models. */
  list: boolean;
}

/** Every client the dialog lists, in rail order. */
export interface ClientCatalog {
  clients: ClientCatalogEntry[];
}

/** Clients whose config AstrLink writes, so the status lists their places. */
export function writesConfig(entry: Pick<ClientCatalogEntry, "kind">) {
  return entry.kind === "file" || entry.kind === "paste";
}

/** Every reasoning effort Codex's `model_reasoning_effort` takes. */
export const codexReasoningEfforts = [
  "none",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
  "ultra",
  "persistent",
] as const;

/** The efforts offered while the catalog names none for the model. */
export const defaultReasoningEfforts = [
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
] as const;

/**
 * The efforts to offer for a Codex model: the official levels Codex takes,
 * else the common ones, plus the config's own value so it stays visible.
 */
export function reasoningEffortOptions(
  official: readonly string[] | undefined,
  current: string,
): string[] {
  const known = (official ?? []).filter((level) =>
    (codexReasoningEfforts as readonly string[]).includes(level),
  );
  const options: string[] =
    known.length > 0 ? known : [...defaultReasoningEfforts];
  return current && !options.includes(current)
    ? [...options, current]
    : options;
}

export interface ClientConfigModels {
  /** Required for every client but Claude Code. */
  model?: string;
  haikuModel?: string;
  sonnetModel?: string;
  opusModel?: string;
  fableModel?: string;
  /** Claude Code's `CLAUDE_CODE_SUBAGENT_MODEL`. */
  subagentModel?: string;
  /** Codex's `model_reasoning_effort`; absent leaves Codex's default. */
  reasoningEffort?: string;
  /** The models a list client switches between. */
  models?: string[];
  /**
   * Per model id, the protocol to write it with, for a client that takes
   * several. A model left out uses the client's first protocol.
   */
  modelProtocols?: Record<string, string>;
  /**
   * Pi only: the context window and output limit of the default model, in
   * tokens. Pi's own defaults apply where they are absent.
   */
  contextWindow?: number;
  maxTokens?: number;
}

/** What a config holds now: the keys it sets, in the same shape. */
export type CurrentModels = ClientConfigModels;

/** The largest limit a Pi entry takes, in tokens. */
export const maxPiLimit = 100_000_000;

/** The most models a list may hold. */
export const maxListModels = 500;

const claudeFields = [
  "model",
  "haikuModel",
  "sonnetModel",
  "opusModel",
  "fableModel",
  "subagentModel",
] as const satisfies readonly (keyof ClientConfigModels)[];

const builtinModelFields: Record<
  BuiltinClient,
  readonly (keyof ClientConfigModels)[]
> = {
  claude: claudeFields,
  codex: ["model", "reasoningEffort"],
  gemini: ["model"],
  opencode: ["model", "models"],
  openclaw: ["model", "models"],
  pi: ["model", "models", "contextWindow", "maxTokens"],
};

/** The model fields a client's config holds. */
export function clientModelFields(
  entry: Pick<ClientCatalogEntry, "id" | "list">,
): readonly (keyof ClientConfigModels)[] {
  if (isBuiltinClient(entry.id)) return builtinModelFields[entry.id];
  return entry.list ? ["model", "models"] : ["model"];
}

export type ClientConfigState =
  | "not_configured"
  | "configured"
  /** Still ours, pointing at a port the gateway no longer uses. */
  | "outdated"
  /** Another AstrLink on this computer wrote it for its own address. */
  | "elsewhere"
  /** A connection setting changed since AstrLink wrote it. */
  | "modified"
  /** The config file cannot be read as its format. */
  | "invalid";

export interface ClientConfigStatus {
  client: ClientConfigClient;
  /** The WSL distribution the client runs in; `null` for this computer. */
  wsl: string | null;
  /** The write target of a client with several; `null` otherwise. */
  target: string | null;
  detected: boolean;
  /** The home folder the paths live under, shown as `~`. */
  home: string;
  /** The client's config folder, such as `<home>/.openclaw`. */
  dir: string;
  /** The config files, the main one first. */
  paths: string[];
  /** Per path: whether the file exists now. */
  exists: boolean[];
  state: ClientConfigState;
  token_id: string | null;
  /**
   * The models a config AstrLink wrote holds now. The client may have picked
   * them itself since, like with Codex's `/model`.
   */
  models: CurrentModels;
  /** Situations the dialog explains for this place, by id. */
  notes: string[];
  /**
   * For a written `paste` client: since when, in Unix seconds, a request
   * from it with the token shows the paste worked; `null` otherwise.
   */
  paste_since_unix: number | null;
  /** The names the token is pasted under in the client. */
  paste_into: string[];
}

export interface ClientConfigOverview {
  /** This computer's clients, then each running WSL distribution's. */
  clients: ClientConfigStatus[];
  /** WSL distributions whose clients went unchecked, usually stopped ones. */
  wsl_unchecked: string[];
  /** Whether WSL clients reach the gateway at localhost (mirrored networking). */
  wsl_localhost: boolean;
  /** Link clients found on this computer. */
  installed: string[];
}

export type ClientConfigAction = "write" | "remove";

export interface ClientConfigPreviewLine {
  kind: "context" | "add" | "remove" | "gap";
  text: string;
}

export interface ClientConfigPreviewFile {
  path: string;
  /** The write creates the file. */
  created: boolean;
  /** The removal deletes the file. */
  deleted: boolean;
  /** Keys or lines changed in this file. */
  changes: number;
  unit: "line" | "key";
  /** A unified diff with two lines of context; secrets shown masked. */
  lines: ClientConfigPreviewLine[];
}

/** What a write or removal changes, file by file. */
export interface ClientConfigPreview {
  files: ClientConfigPreviewFile[];
  /** Values AstrLink did not write that this replaces or removes. */
  replaced: number;
  /** Removal: earlier values that come back. */
  restored: number;
}

/** One place in an `apply_client_configs` call. */
export interface ClientConfigItem {
  client: ClientConfigClient;
  wsl: string | null;
  target: string | null;
  action: ClientConfigAction;
  /** Required for a write; ignored for a removal. */
  models?: ClientConfigModels;
}

/** Why a client needs confirmation before anything is written. */
export interface ClientConfigConfirmation {
  client: ClientConfigClient;
  wsl: string | null;
  target: string | null;
  /** Shown beside the client's name. */
  dir: string;
  /** A removal, for a client the user unchecked. */
  remove: boolean;
  /** Values AstrLink did not write; `current` only for non-secret values. */
  keys: { path: string; key: string; current: string | null }[];
  /** The config connects to another AstrLink at this base URL now. */
  gateway: string | null;
  /** The config uses this other token of this gateway now. */
  token_id: string | null;
}

export interface ClientConfigResult {
  client: ClientConfigClient;
  wsl: string | null;
  target: string | null;
  action: ClientConfigAction;
  ok: boolean;
  error: { message: string; path: string | null; line: number | null } | null;
}

export type ClientConfigApplyOutcome =
  /** Nothing was written. */
  | { status: "needs_confirmation"; items: ClientConfigConfirmation[] }
  /** Each item was written on its own, in the order asked. */
  | { status: "done"; results: ClientConfigResult[] };

/** One file of a config snippet, its path shown with `~`. */
export interface ClientConfigSnippetFile {
  path: string;
  contents: string;
}

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
  "elsewhere",
  "modified",
  "invalid",
];
const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const accessTokenPattern = /astr_[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]/;
// eslint-disable-next-line no-control-regex -- Paths and key names must stay on one line.
const controlPattern = /[\u0000-\u001f\u007f]/;
// eslint-disable-next-line no-control-regex -- A diff line keeps its tabs only.
const diffLineControlPattern = /[\u0000-\u0008\u000a-\u001f\u007f]/;

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

/** Config text shows the token as its hint, never the token itself. */
function noToken(value: string, path: string): string {
  if (accessTokenPattern.test(value)) {
    invalid(path, "must show the token hint, not the token");
  }
  return value;
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
  return noToken(value, path);
}

function booleanAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") invalid(path, "expected a boolean");
  return value as boolean;
}

function countAt(value: unknown, path: string, min: number, max: number) {
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < min ||
    value > max
  ) {
    return invalid(path, `expected a whole number from ${min} to ${max}`);
  }
  return value;
}

function arrayAt(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) return invalid(path, "expected an array");
  return value;
}

const clientIDPattern = /^[a-z][a-z0-9_]{1,31}$/;
const targetPattern = /^[a-z][a-z0-9_]{0,31}$/;

function clientAt(value: unknown, path: string): ClientConfigClient {
  if (typeof value !== "string" || !clientIDPattern.test(value)) {
    return invalid(path, "expected a client id");
  }
  return value;
}

function targetAt(value: unknown, path: string): string | null {
  if (value === null) return null;
  if (typeof value !== "string" || !targetPattern.test(value)) {
    return invalid(path, "expected a write target");
  }
  return value;
}

function wslAt(value: unknown, path: string): string | null {
  return value === null ? null : lineAt(value, path, 255);
}

function tokenIDAt(value: unknown, path: string): string | null {
  if (value === null) return null;
  const tokenID = lineAt(value, path, 96);
  if (!resourceIDPattern.test(tokenID)) invalid(path, "invalid token ID");
  return tokenID;
}

/** A list of distinct strings, each checked by `item`. */
function distinctAt(
  value: unknown,
  path: string,
  max: number,
  item: (value: unknown, path: string) => string,
): string[] {
  const list = arrayAt(value, path);
  if (list.length > max) invalid(path, "too many entries");
  const items = list.map((entry, index) => item(entry, `${path}[${index}]`));
  if (new Set(items).size !== items.length) invalid(path, "expected each once");
  return items;
}

/**
 * Parses `client_catalog`: every client the dialog lists, in rail order.
 * Targets belong to clients AstrLink writes; every client but an unsupported
 * one names the protocols it can be written with.
 */
export function parseClientCatalog(value: unknown): ClientCatalog {
  const root = objectAt(value, "$");
  exactKeys(root, ["clients"], "$");
  const list = arrayAt(root.clients, "$.clients");
  if (list.length > 200) invalid("$.clients", "too many clients");
  const clients = list.map((item, index): ClientCatalogEntry => {
    const at = `$.clients[${index}]`;
    const entry = objectAt(item, at);
    exactKeys(
      entry,
      ["id", "name", "kind", "targets", "protocols", "list"],
      at,
    );
    if (!(clientKinds as readonly unknown[]).includes(entry.kind)) {
      invalid(`${at}.kind`, "unknown kind");
    }
    const kind = entry.kind as ClientKind;
    const targets = distinctAt(
      entry.targets,
      `${at}.targets`,
      8,
      (target, p) =>
        targetAt(target, p) ?? invalid(p, "expected a write target"),
    );
    if (targets.length === 1) invalid(`${at}.targets`, "expected two or more");
    if (targets.length > 0 && !writesConfig({ kind })) {
      invalid(`${at}.targets`, "only written clients have targets");
    }
    const protocols = distinctAt(
      entry.protocols,
      `${at}.protocols`,
      clientProtocolIds.length,
      (protocol, p) => {
        if (!(clientProtocolIds as readonly unknown[]).includes(protocol)) {
          invalid(p, "unknown protocol");
        }
        return protocol as string;
      },
    );
    if (kind !== "unsupported" && protocols.length === 0) {
      invalid(`${at}.protocols`, "expected a protocol");
    }
    return {
      id: clientAt(entry.id, `${at}.id`),
      name: lineAt(entry.name, `${at}.name`, 64),
      kind,
      targets,
      protocols,
      list: booleanAt(entry.list, `${at}.list`),
    };
  });
  if (new Set(clients.map(({ id }) => id)).size !== clients.length) {
    invalid("$.clients", "expected each client once");
  }
  return { clients };
}

function parseModels(
  value: unknown,
  entry: ClientCatalogEntry,
  path: string,
): CurrentModels {
  const fields: readonly string[] = clientModelFields(entry);
  const models: CurrentModels = {};
  for (const [key, item] of Object.entries(objectAt(value, path))) {
    const at = `${path}.${key}`;
    if (!fields.includes(key)) invalid(at, "unexpected field");
    if (key === "contextWindow" || key === "maxTokens") {
      models[key] = countAt(item, at, 1, maxPiLimit);
    } else if (key === "models") {
      const list = arrayAt(item, at);
      if (list.length > maxListModels) invalid(at, "too many models");
      models.models = list.map((model, index) =>
        lineAt(model, `${at}[${index}]`, 256),
      );
    } else if (key === "reasoningEffort") {
      const effort = lineAt(item, at, 32);
      if (!/^[a-z]+$/.test(effort)) invalid(at, "expected a reasoning effort");
      models.reasoningEffort = effort;
    } else {
      models[key as (typeof claudeFields)[number]] = lineAt(item, at, 256);
    }
  }
  return models;
}

/** One status slot of a home: a written client, or one of its targets. */
interface StatusSlot {
  entry: ClientCatalogEntry;
  target: string | null;
}

function parseStatus(
  value: unknown,
  slot: StatusSlot,
  path: string,
): ClientConfigStatus {
  const status = objectAt(value, path);
  exactKeys(
    status,
    [
      "client",
      "wsl",
      "target",
      "detected",
      "home",
      "dir",
      "paths",
      "exists",
      "state",
      "token_id",
      "models",
      "notes",
      "paste_since_unix",
      "paste_into",
    ],
    path,
  );
  const client = clientAt(status.client, `${path}.client`);
  const target = targetAt(status.target, `${path}.target`);
  if (client !== slot.entry.id || target !== slot.target) {
    invalid(path, "expected every written client and target per home");
  }
  const paths = arrayAt(status.paths, `${path}.paths`).map((item, index) =>
    lineAt(item, `${path}.paths[${index}]`, 4096),
  );
  if (paths.length === 0) invalid(`${path}.paths`, "expected a path");
  const exists = arrayAt(status.exists, `${path}.exists`).map((item, index) =>
    booleanAt(item, `${path}.exists[${index}]`),
  );
  if (exists.length !== paths.length) {
    invalid(`${path}.exists`, "expected one entry per path");
  }
  if (!states.includes(status.state as ClientConfigState)) {
    invalid(`${path}.state`, "unknown state");
  }
  const state = status.state as ClientConfigState;
  const tokenID = tokenIDAt(status.token_id, `${path}.token_id`);
  // Only a config AstrLink wrote names its token. An unreadable file may or
  // may not have been written by AstrLink.
  const written = state !== "not_configured";
  if (state !== "invalid" && written !== (tokenID !== null)) {
    invalid(`${path}.token_id`, "does not match the state");
  }
  return {
    client,
    wsl: wslAt(status.wsl, `${path}.wsl`),
    target,
    detected: booleanAt(status.detected, `${path}.detected`),
    home: lineAt(status.home, `${path}.home`, 4096),
    dir: lineAt(status.dir, `${path}.dir`, 4096),
    paths,
    exists,
    state,
    token_id: tokenID,
    models: parseModels(status.models, slot.entry, `${path}.models`),
    notes: arrayAt(status.notes, `${path}.notes`).map((item, index) =>
      noteAt(item, `${path}.notes[${index}]`),
    ),
    paste_since_unix:
      status.paste_since_unix === null
        ? null
        : countAt(
            status.paste_since_unix,
            `${path}.paste_since_unix`,
            0,
            Number.MAX_SAFE_INTEGER,
          ),
    paste_into: arrayAt(status.paste_into, `${path}.paste_into`).map(
      (item, index) => lineAt(item, `${path}.paste_into[${index}]`, 64),
    ),
  };
}

/** A note id, such as `running`. */
function noteAt(value: unknown, path: string): string {
  if (typeof value !== "string" || !/^[a-z][a-z_]{0,31}$/.test(value)) {
    invalid(path, "expected a note id");
  }
  return value;
}

/**
 * Parses `client_config_status` against the catalog: per home, one entry
 * for every written client in catalog order and, for a client with targets,
 * one per target in order; this computer first, then each WSL home.
 */
export function parseClientConfigOverview(
  value: unknown,
  catalog: ClientCatalog,
): ClientConfigOverview {
  const root = objectAt(value, "$");
  exactKeys(
    root,
    ["clients", "wsl_unchecked", "wsl_localhost", "installed"],
    "$",
  );
  const slots: StatusSlot[] = catalog.clients
    .filter(writesConfig)
    .flatMap((entry): StatusSlot[] =>
      entry.targets.length > 0
        ? entry.targets.map((target) => ({ entry, target }))
        : [{ entry, target: null }],
    );
  const list = arrayAt(root.clients, "$.clients");
  if (slots.length === 0 ? list.length > 0 : list.length % slots.length !== 0) {
    invalid("$.clients", "expected every written client per home");
  }
  const clients = list.map((item, index) =>
    parseStatus(item, slots[index % slots.length], `$.clients[${index}]`),
  );
  const homes: (string | null)[] = [];
  clients.forEach((status, index) => {
    if (index % slots.length === 0) homes.push(status.wsl);
    if (status.wsl !== homes[homes.length - 1]) {
      invalid(`$.clients[${index}]`, "expected every client per home");
    }
  });
  if (
    (clients.length > 0 && homes[0] !== null) ||
    homes.slice(1).some((home) => home === null) ||
    new Set(homes).size !== homes.length
  ) {
    invalid("$.clients", "expected this computer first, then each WSL home");
  }
  const links = new Set(
    catalog.clients
      .filter((entry) => entry.kind === "link")
      .map(({ id }) => id),
  );
  return {
    clients,
    wsl_unchecked: arrayAt(root.wsl_unchecked, "$.wsl_unchecked").map(
      (item, index) => lineAt(item, `$.wsl_unchecked[${index}]`, 255),
    ),
    wsl_localhost: booleanAt(root.wsl_localhost, "$.wsl_localhost"),
    installed: distinctAt(root.installed, "$.installed", 200, (id, p) => {
      const client = clientAt(id, p);
      if (!links.has(client)) invalid(p, "expected a link client");
      return client;
    }),
  };
}

const lineKinds = ["context", "add", "remove", "gap"] as const;

function parsePreviewFile(
  value: unknown,
  path: string,
): ClientConfigPreviewFile {
  const file = objectAt(value, path);
  exactKeys(
    file,
    ["path", "created", "deleted", "changes", "unit", "lines"],
    path,
  );
  const created = booleanAt(file.created, `${path}.created`);
  const deleted = booleanAt(file.deleted, `${path}.deleted`);
  if (created && deleted) invalid(path, "cannot both create and delete");
  if (file.unit !== "line" && file.unit !== "key") {
    invalid(`${path}.unit`, "expected line or key");
  }
  const lines = arrayAt(file.lines, `${path}.lines`);
  if (lines.length > 20_000) invalid(`${path}.lines`, "too many lines");
  return {
    path: lineAt(file.path, `${path}.path`, 4096),
    created,
    deleted,
    changes: countAt(file.changes, `${path}.changes`, 0, 1_000_000),
    unit: file.unit,
    lines: lines.map((item, index) => {
      const at = `${path}.lines[${index}]`;
      const line = objectAt(item, at);
      exactKeys(line, ["kind", "text"], at);
      if (!(lineKinds as readonly unknown[]).includes(line.kind)) {
        invalid(`${at}.kind`, "unknown kind");
      }
      if (
        typeof line.text !== "string" ||
        line.text.length > 65_536 ||
        diffLineControlPattern.test(line.text)
      ) {
        invalid(`${at}.text`, "expected one line of text");
      }
      return {
        kind: line.kind as ClientConfigPreviewLine["kind"],
        text: noToken(line.text as string, `${at}.text`),
      };
    }),
  };
}

/** Parses `preview_client_config_changes`, which must hold no token. */
export function parseClientConfigPreview(value: unknown): ClientConfigPreview {
  const preview = objectAt(value, "$");
  exactKeys(preview, ["files", "replaced", "restored"], "$");
  return {
    files: arrayAt(preview.files, "$.files").map((file, index) =>
      parsePreviewFile(file, `$.files[${index}]`),
    ),
    replaced: countAt(preview.replaced, "$.replaced", 0, 1_000_000),
    restored: countAt(preview.restored, "$.restored", 0, 1_000_000),
  };
}

function parseConfirmation(
  value: unknown,
  path: string,
): ClientConfigConfirmation {
  const item = objectAt(value, path);
  exactKeys(
    item,
    ["client", "wsl", "target", "dir", "remove", "keys", "gateway", "token_id"],
    path,
  );
  const keys = arrayAt(item.keys, `${path}.keys`).map((entry, index) => {
    const at = `${path}.keys[${index}]`;
    const key = objectAt(entry, at);
    exactKeys(key, ["path", "key", "current"], at);
    return {
      path: lineAt(key.path, `${at}.path`, 4096),
      key: lineAt(key.key, `${at}.key`, 512),
      current:
        key.current === null ? null : lineAt(key.current, `${at}.current`, 512),
    };
  });
  const remove = booleanAt(item.remove, `${path}.remove`);
  const gateway =
    item.gateway === null ? null : lineAt(item.gateway, `${path}.gateway`, 512);
  const tokenID = tokenIDAt(item.token_id, `${path}.token_id`);
  if (!remove && keys.length === 0 && gateway === null && tokenID === null) {
    invalid(path, "expected a reason to confirm");
  }
  return {
    client: clientAt(item.client, `${path}.client`),
    wsl: wslAt(item.wsl, `${path}.wsl`),
    target: targetAt(item.target, `${path}.target`),
    dir: lineAt(item.dir, `${path}.dir`, 4096),
    remove,
    keys,
    gateway,
    token_id: tokenID,
  };
}

function parseResult(value: unknown, path: string): ClientConfigResult {
  const result = objectAt(value, path);
  exactKeys(result, ["client", "wsl", "target", "action", "ok", "error"], path);
  if (result.action !== "write" && result.action !== "remove") {
    invalid(`${path}.action`, "expected write or remove");
  }
  const ok = booleanAt(result.ok, `${path}.ok`);
  let error: ClientConfigResult["error"] = null;
  if (result.error !== null) {
    const at = `${path}.error`;
    const detail = objectAt(result.error, at);
    exactKeys(detail, ["message", "path", "line"], at);
    if (
      typeof detail.message !== "string" ||
      detail.message.trim() === "" ||
      detail.message.length > 4096
    ) {
      invalid(`${at}.message`, "expected a message");
    }
    error = {
      message: noToken(detail.message as string, `${at}.message`),
      path:
        detail.path === null ? null : lineAt(detail.path, `${at}.path`, 4096),
      line:
        detail.line === null
          ? null
          : countAt(detail.line, `${at}.line`, 1, 100_000_000),
    };
  }
  if (ok !== (error === null)) invalid(`${path}.error`, "does not match ok");
  return {
    client: clientAt(result.client, `${path}.client`),
    wsl: wslAt(result.wsl, `${path}.wsl`),
    target: targetAt(result.target, `${path}.target`),
    action: result.action,
    ok,
    error,
  };
}

/**
 * Parses `apply_client_configs`. Results answer the asked items one by one,
 * in order; confirmations name only the items that need one.
 */
export function parseClientConfigApplyOutcome(
  value: unknown,
  asked: readonly Pick<
    ClientConfigItem,
    "client" | "wsl" | "target" | "action"
  >[],
): ClientConfigApplyOutcome {
  const outcome = objectAt(value, "$");
  if (outcome.status === "needs_confirmation") {
    exactKeys(outcome, ["status", "items"], "$");
    const items = arrayAt(outcome.items, "$.items").map((item, index) =>
      parseConfirmation(item, `$.items[${index}]`),
    );
    if (items.length === 0) invalid("$.items", "expected an item");
    items.forEach((item, index) => {
      if (
        !asked.some(
          (request) =>
            request.client === item.client &&
            request.wsl === item.wsl &&
            request.target === item.target &&
            (request.action === "remove") === item.remove,
        )
      ) {
        invalid(`$.items[${index}]`, "not an item that was asked");
      }
    });
    return { status: "needs_confirmation", items };
  }
  if (outcome.status !== "done") return invalid("$.status", "unknown status");
  exactKeys(outcome, ["status", "results"], "$");
  const results = arrayAt(outcome.results, "$.results").map((item, index) =>
    parseResult(item, `$.results[${index}]`),
  );
  if (
    results.length !== asked.length ||
    results.some(
      (result, index) =>
        result.client !== asked[index].client ||
        result.wsl !== asked[index].wsl ||
        result.target !== asked[index].target ||
        result.action !== asked[index].action,
    )
  ) {
    invalid("$.results", "expected one result per item, in order");
  }
  return { status: "done", results };
}

/** Parses `preview_client_config_snippet`, which must hold no token. */
export function parseClientConfigSnippet(
  value: unknown,
): ClientConfigSnippetFile[] {
  const files = arrayAt(value, "$").map((item, index) => {
    const at = `$[${index}]`;
    const file = objectAt(item, at);
    exactKeys(file, ["path", "contents"], at);
    if (typeof file.contents !== "string" || file.contents.trim() === "") {
      invalid(`${at}.contents`, "expected a config snippet");
    }
    return {
      path: lineAt(file.path, `${at}.path`, 4096),
      contents: noToken(file.contents as string, `${at}.contents`),
    };
  });
  if (files.length === 0) invalid("$", "expected a file");
  return files;
}

/** Parses `copy_client_config_snippet`: false when the clipboard refused it. */
export function parseClientConfigCopied(value: unknown): boolean {
  return booleanAt(value, "$");
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

/** The part of a service the model catalog needs. */
export interface CatalogService {
  enabled: boolean;
  models: readonly string[];
  subscription?: { status: string } | null;
}

/** A service that can take requests: enabled and, for a subscription, connected. */
export function serviceUsable(service: CatalogService): boolean {
  return (
    service.enabled &&
    (!service.subscription || service.subscription.status === "connected")
  );
}

/**
 * The models the gateway serves now: those of usable services, and enabled
 * redirect sources whose target one of them serves.
 */
export function availableModels(
  services: readonly CatalogService[],
  redirects: readonly { from: string; to: string; enabled: boolean }[],
): Set<string> {
  const served = new Set(
    services.filter(serviceUsable).flatMap((service) => service.models),
  );
  const available = new Set(served);
  for (const redirect of redirects) {
    if (redirect.enabled && served.has(redirect.to)) {
      available.add(redirect.from);
    }
  }
  return available;
}

/** Every model a config names, the default first. */
export function configModels(models: CurrentModels): string[] {
  return [
    ...new Set(
      [
        models.model,
        ...claudeFields.slice(1).map((field) => models[field]),
        ...(models.models ?? []),
      ].filter((model): model is string => Boolean(model)),
    ),
  ];
}

/**
 * The models a config names that the gateway no longer serves. Read from the
 * local catalog only; nothing is asked upstream.
 */
export function missingModels(
  status: Pick<ClientConfigStatus, "models">,
  available: ReadonlySet<string>,
): string[] {
  return configModels(status.models).filter((model) => !available.has(model));
}

/** Why a config this token holds needs the user, most pressing first. */
export type ClientConfigAttention =
  | "invalid"
  | "modified"
  | "model_missing"
  | "outdated";

/**
 * What a config written for `tokenId` needs, if anything. `available` is
 * null while the model catalog is unknown, which hides `model_missing`.
 */
export function configAttention(
  status: ClientConfigStatus,
  tokenId: string,
  available: ReadonlySet<string> | null,
): { kind: ClientConfigAttention; missing: string[] } | null {
  if (status.token_id !== tokenId) return null;
  if (status.state === "invalid") return { kind: "invalid", missing: [] };
  if (status.state === "modified") return { kind: "modified", missing: [] };
  const missing =
    available && ["configured", "outdated"].includes(status.state)
      ? missingModels(status, available)
      : [];
  if (missing.length > 0) return { kind: "model_missing", missing };
  if (status.state === "outdated") return { kind: "outdated", missing: [] };
  return null;
}
