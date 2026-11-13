import {
  planProviderImport,
  type ProviderImportPlan,
} from "./provider-import-model";
import type {
  HTTPServiceKind,
  ModelDiscoveryProtocol,
  Service,
  ServiceCapability,
} from "./service-model";
import {
  codingPlanPresetIDs,
  httpServicePreset,
  httpServicePresetIDs,
  type ProtocolDescriptor,
} from "./service-presets";

/** Agent tools whose config files the local import reads. */
export type LocalImportTool = "claude" | "codex" | "pi";

export const localImportToolNames: Record<LocalImportTool, string> = {
  claude: "Claude Code",
  codex: "Codex",
  pi: "Pi",
};

/** One tool in one home; `wsl` names the distribution of a WSL home. */
export interface LocalImportSource {
  id: string;
  tool: LocalImportTool;
  wsl: string | null;
  /** Files a read opens, with the home shown as `~`. */
  paths: string[];
}

export interface LocalImportOpened {
  session: string;
  sources: LocalImportSource[];
}

export type LocalImportKey =
  | { status: "found"; hint: string | null }
  /** Named by a variable AstrLink's environment lacks. */
  | { status: "env"; name: string }
  /** Printed by a command, which AstrLink never runs. */
  | { status: "command" }
  | { status: "missing" };

export interface LocalImportPlace {
  tool: LocalImportTool;
  wsl: string | null;
}

/** A provider the host found; its key stays in the host. */
export interface LocalImportCandidate {
  id: string;
  name: string | null;
  address: string;
  protocols: string[];
  models: string[];
  key: LocalImportKey;
  sources: LocalImportPlace[];
}

export type LocalImportSkipReason =
  | "chatgpt_login"
  | "astrlink"
  | "missing"
  | "unreadable"
  | "invalid"
  | "invalid_address"
  | "unsupported"
  | "sign_in";

export interface LocalImportSkipped extends LocalImportPlace {
  reason: LocalImportSkipReason;
  name: string | null;
  path: string | null;
  detail: string | null;
}

export interface LocalImportRead {
  candidates: LocalImportCandidate[];
  skipped: LocalImportSkipped[];
}

type JsonObject = Record<string, unknown>;

const sessionPattern = /^[0-9a-f]{32}$/;
const tools = new Set<string>(["claude", "codex", "pi"]);
const skipReasons = new Set<string>([
  "chatgpt_login",
  "astrlink",
  "missing",
  "unreadable",
  "invalid",
  "invalid_address",
  "unsupported",
  "sign_in",
]);

function invalid(path: string, message: string): never {
  throw new Error(`Invalid local-import IPC response at ${path}: ${message}`);
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

function stringAt(value: unknown, path: string): string {
  if (typeof value !== "string" || value === "") {
    return invalid(path, "expected a non-empty string");
  }
  return value;
}

function nullableStringAt(value: unknown, path: string): string | null {
  return value === null ? null : stringAt(value, path);
}

function arrayAt(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) return invalid(path, "expected an array");
  return value;
}

function stringsAt(value: unknown, path: string): string[] {
  return arrayAt(value, path).map((item, index) =>
    stringAt(item, `${path}[${index}]`),
  );
}

function idAt(value: unknown, prefix: string, path: string): string {
  const id = stringAt(value, path);
  if (!new RegExp(`^${prefix}\\d{1,4}$`).test(id)) invalid(path, "invalid id");
  return id;
}

function toolAt(value: unknown, path: string): LocalImportTool {
  if (typeof value !== "string" || !tools.has(value)) {
    return invalid(path, "unknown tool");
  }
  return value as LocalImportTool;
}

function placeAt(value: unknown, path: string): LocalImportPlace {
  const place = objectAt(value, path);
  exactKeys(place, ["tool", "wsl"], path);
  return {
    tool: toolAt(place.tool, `${path}.tool`),
    wsl: nullableStringAt(place.wsl, `${path}.wsl`),
  };
}

export function parseLocalImportOpened(
  value: unknown,
  path = "$",
): LocalImportOpened {
  const opened = objectAt(value, path);
  exactKeys(opened, ["session", "sources"], path);
  const session = stringAt(opened.session, `${path}.session`);
  if (!sessionPattern.test(session)) invalid(`${path}.session`, "invalid id");
  return {
    session,
    sources: arrayAt(opened.sources, `${path}.sources`).map((item, index) => {
      const at = `${path}.sources[${index}]`;
      const source = objectAt(item, at);
      exactKeys(source, ["id", "tool", "wsl", "paths"], at);
      return {
        id: idAt(source.id, "s", `${at}.id`),
        tool: toolAt(source.tool, `${at}.tool`),
        wsl: nullableStringAt(source.wsl, `${at}.wsl`),
        paths: stringsAt(source.paths, `${at}.paths`),
      };
    }),
  };
}

function keyAt(value: unknown, path: string): LocalImportKey {
  const key = objectAt(value, path);
  switch (key.status) {
    case "found": {
      exactKeys(key, ["status", "hint"], path);
      const hint = nullableStringAt(key.hint, `${path}.hint`);
      // Core's hint shape: an ellipsis and the key's last four characters.
      if (hint !== null && (!hint.startsWith("…") || [...hint].length !== 5)) {
        invalid(`${path}.hint`, "must be a non-secret display hint");
      }
      return { status: "found", hint };
    }
    case "env":
      exactKeys(key, ["status", "name"], path);
      return { status: "env", name: stringAt(key.name, `${path}.name`) };
    case "command":
    case "missing":
      exactKeys(key, ["status"], path);
      return { status: key.status };
    default:
      return invalid(`${path}.status`, "unknown key status");
  }
}

export function parseLocalImportRead(
  value: unknown,
  path = "$",
): LocalImportRead {
  const read = objectAt(value, path);
  exactKeys(read, ["candidates", "skipped"], path);
  return {
    candidates: arrayAt(read.candidates, `${path}.candidates`).map(
      (item, index) => {
        const at = `${path}.candidates[${index}]`;
        const candidate = objectAt(item, at);
        exactKeys(
          candidate,
          ["id", "name", "address", "protocols", "models", "key", "sources"],
          at,
        );
        return {
          id: idAt(candidate.id, "c", `${at}.id`),
          name: nullableStringAt(candidate.name, `${at}.name`),
          address: stringAt(candidate.address, `${at}.address`),
          protocols: stringsAt(candidate.protocols, `${at}.protocols`),
          models: stringsAt(candidate.models, `${at}.models`),
          key: keyAt(candidate.key, `${at}.key`),
          sources: arrayAt(candidate.sources, `${at}.sources`).map(
            (source, sourceIndex) =>
              placeAt(source, `${at}.sources[${sourceIndex}]`),
          ),
        };
      },
    ),
    skipped: arrayAt(read.skipped, `${path}.skipped`).map((item, index) => {
      const at = `${path}.skipped[${index}]`;
      const skipped = objectAt(item, at);
      exactKeys(
        skipped,
        ["reason", "tool", "wsl", "name", "path", "detail"],
        at,
      );
      if (
        typeof skipped.reason !== "string" ||
        !skipReasons.has(skipped.reason)
      ) {
        invalid(`${at}.reason`, "unknown reason");
      }
      return {
        reason: skipped.reason as LocalImportSkipReason,
        tool: toolAt(skipped.tool, `${at}.tool`),
        wsl: nullableStringAt(skipped.wsl, `${at}.wsl`),
        name: nullableStringAt(skipped.name, `${at}.name`),
        path: nullableStringAt(skipped.path, `${at}.path`),
        detail: nullableStringAt(skipped.detail, `${at}.detail`),
      };
    }),
  };
}

function trimSlashes(path: string): string {
  return path.replace(/\/+$/, "");
}

function parseURL(address: string): URL | null {
  try {
    return new URL(address.trim());
  } catch {
    return null;
  }
}

/**
 * Scheme, host, port and path, ignoring a trailing slash and `/v1`, which
 * clients append differently for the same endpoint. Mirrors the host's merge.
 */
export function normalizedAddress(address: string): string | null {
  const url = parseURL(address);
  if (!url) return null;
  const path = trimSlashes(trimSlashes(url.pathname).replace(/\/v1$/, ""));
  return `${url.protocol}//${url.host}${path}`;
}

/** Plain http to another machine: the key would cross the network unencrypted. */
export function insecureAddress(address: string): boolean {
  const url = parseURL(address);
  if (!url || url.protocol !== "http:") return false;
  const host = url.hostname;
  return !(
    host === "localhost" ||
    host.endsWith(".localhost") ||
    host === "[::1]" ||
    /^127(?:\.\d{1,3}){3}$/.test(host)
  );
}

/** Kinds a preset site can identify; the rest need an address of their own. */
const detectableKinds = httpServicePresetIDs.filter(
  (kind) => !["newapi", "openai_compatible", "custom", "vertex"].includes(kind),
);

function siteFor(kind: HTTPServiceKind, url: URL) {
  return httpServicePreset(kind).sites.find(
    (site) =>
      new URL(site.baseURL).host === url.host ||
      site.aliases?.includes(url.host),
  );
}

/**
 * The provider types an address fits, the default first. A vendor host whose
 * path names one preset gives that preset; a host several presets share
 * offers them all with the Coding Plan first; any other address is a relay.
 */
export function detectKinds(address: string): HTTPServiceKind[] {
  const url = parseURL(address);
  if (!url) return ["newapi"];
  const path = trimSlashes(url.pathname);
  const near: HTTPServiceKind[] = [];
  const exact: HTTPServiceKind[] = [];
  for (const kind of detectableKinds) {
    const site = siteFor(kind, url);
    if (!site) continue;
    near.push(kind);
    if (trimSlashes(new URL(site.baseURL).pathname) === path) exact.push(kind);
  }
  const kinds = exact.length > 0 ? exact : near;
  if (kinds.length === 0) return ["newapi"];
  const plan = (kind: HTTPServiceKind) =>
    codingPlanPresetIDs.includes(kind) ? 0 : 1;
  return [...kinds].sort((a, b) => plan(a) - plan(b));
}

/**
 * Where the new provider points: the preset's documented path on the host the
 * tool used, or the tool's own address for a relay. The host never changes,
 * so the key only goes where it was configured to.
 */
export function saveAddress(address: string, kind: HTTPServiceKind): string {
  const url = parseURL(address);
  const site = url ? siteFor(kind, url) : undefined;
  if (!url || !site) return trimSlashes(address.trim());
  return `${url.protocol}//${url.host}${trimSlashes(new URL(site.baseURL).pathname)}`;
}

export function discoveryProtocol(
  capabilities: readonly ServiceCapability[],
): ModelDiscoveryProtocol | null {
  for (const protocol of ["openai.models", "google.models"] as const) {
    if (capabilities.some((capability) => capability.protocol === protocol)) {
      return protocol;
    }
  }
  return null;
}

/**
 * The service a candidate becomes as `kind`. A preset keeps its own formats;
 * a relay serves the formats the tools used, plus its model list.
 */
export function planCandidate(
  candidate: LocalImportCandidate,
  kind: HTTPServiceKind,
  discovered: readonly ProtocolDescriptor[],
): ProviderImportPlan | null {
  const preset = httpServicePreset(kind, discovered);
  const protocols =
    preset.sites.length === 0 && candidate.protocols.length > 0
      ? [
          ...candidate.protocols,
          ...(discoveryProtocol(preset.capabilities) &&
          !candidate.protocols.includes("openai.models")
            ? ["openai.models"]
            : []),
        ]
      : [];
  const result = planProviderImport(
    {
      kind,
      name: preset.sites.length > 0 ? null : candidate.name,
      base_url: saveAddress(candidate.address, kind),
      auth: null,
      auth_header: null,
      protocols,
      models: candidate.models,
      has_api_key: candidate.key.status === "found",
      api_key_hint:
        candidate.key.status === "found" ? candidate.key.hint : null,
    },
    discovered,
  );
  return result.ok ? result.plan : null;
}

/** The addresses an existing provider would share with the candidate. */
function candidateAddresses(candidate: LocalImportCandidate): Set<string> {
  const addresses = new Set<string>();
  const add = (address: string) => {
    const normalized = normalizedAddress(address);
    if (normalized) addresses.add(normalized);
  };
  add(candidate.address);
  for (const kind of detectKinds(candidate.address)) {
    add(saveAddress(candidate.address, kind));
  }
  return addresses;
}

function sharesAddress(service: Service, addresses: Set<string>): boolean {
  const normalized = service.http
    ? normalizedAddress(service.http.base_url)
    : null;
  return normalized !== null && addresses.has(normalized);
}

/** Existing providers whose saved-key hint decides whether one is a repeat. */
export function servicesSharingAddresses(
  candidates: readonly LocalImportCandidate[],
  services: readonly Service[],
): Service[] {
  const addresses = new Set(
    candidates.flatMap((candidate) => [...candidateAddresses(candidate)]),
  );
  return services.filter((service) => sharesAddress(service, addresses));
}

export interface ExistingMatch {
  service: Service;
  /** Same address and the same key hint: already added. */
  sameKey: boolean;
}

export function existingMatch(
  candidate: LocalImportCandidate,
  services: readonly Service[],
  hints: ReadonlyMap<string, string | null>,
): ExistingMatch | null {
  const addresses = candidateAddresses(candidate);
  const matches = services.filter((service) =>
    sharesAddress(service, addresses),
  );
  if (matches.length === 0) return null;
  const hint = candidate.key.status === "found" ? candidate.key.hint : null;
  const same =
    hint === null
      ? undefined
      : matches.find((service) => hints.get(service.id) === hint);
  return same
    ? { service: same, sameKey: true }
    : { service: matches[0], sameKey: false };
}

export type ModelOrigin = "fetched" | "config" | "preset";

/**
 * The models a new provider saves: the fetched list with the configured
 * models kept, else the configured ones, else the preset's. Null when there
 * are none, since a provider without models serves nothing.
 */
export function importModels(
  fetched: readonly string[] | null,
  candidate: LocalImportCandidate,
  plan: ProviderImportPlan,
): { models: string[]; origin: ModelOrigin } | null {
  if (fetched && fetched.length > 0) {
    return {
      models: [...new Set([...fetched, ...candidate.models])],
      origin: "fetched",
    };
  }
  if (candidate.models.length > 0) {
    return { models: [...candidate.models], origin: "config" };
  }
  if (plan.models.length > 0) {
    return { models: [...plan.models], origin: "preset" };
  }
  return null;
}

export type ProbeFailure = "timeout" | "auth" | "notFound" | "empty" | "other";

/** A short reason for a failed model-list request; the message stays out. */
export function probeFailure(cause: unknown): ProbeFailure {
  const message = cause instanceof Error ? cause.message : String(cause);
  if (/timed?\s*out|deadline/i.test(message)) return "timeout";
  if (/\b40[13]\b|unauthori[sz]ed|forbidden/i.test(message)) return "auth";
  if (/\b404\b/.test(message)) return "notFound";
  return "other";
}

/** The host message for an import that sat idle or was replaced. */
export function closedImport(cause: unknown): boolean {
  return (
    cause instanceof Error && cause.message.startsWith("this import has closed")
  );
}
