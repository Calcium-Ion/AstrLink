import {
  maxPiLimit,
  type BuiltinClient,
  serviceUsable,
  type ClientCatalogEntry,
  type ClientConfigClient,
  type ClientConfigModels,
  type ClientConfigStatus,
  type ClientConfigOverview,
} from "./client-config-model";
import type { ConversionEngineCapability } from "./core-model";
import {
  astrlinkAutoModelId,
  type ModelRedirect,
} from "./failure-policy-model";
import { bestConversionTarget, type Service } from "./service-model";

/**
 * The protocol each of the first six clients sends, for their manual
 * snippets. The dialog reads every client's protocols from the catalog.
 */
export const builtinClientProtocols: Record<BuiltinClient, string> = {
  claude: "anthropic.messages",
  codex: "openai.responses",
  gemini: "google.generate_content",
  opencode: "openai.chat",
  openclaw: "openai.chat",
  pi: "openai.responses",
};

/**
 * The address a client is given for a protocol: OpenAI-style protocols take
 * `/v1`; Anthropic and Gemini clients add their own version path.
 */
export function protocolBaseURL(protocol: string, inferenceURL: string) {
  return protocol.startsWith("openai.") ? `${inferenceURL}/v1` : inferenceURL;
}

/** Gemini puts the model in the URL path, which cannot carry "/". */
function protocolCarries(protocol: string, model: string): boolean {
  return protocol !== "google.generate_content" || !model.includes("/");
}

export interface ModelCatalog {
  services: Service[];
  redirects: ModelRedirect[];
}

export function hasEntry(service: Service, protocol: string): boolean {
  return service.capabilities.some((row) => row.protocol === protocol);
}

/** The upstream format a new entry would convert to, if the engine can. */
export function entryTarget(
  service: Service,
  protocol: string,
  engine: ConversionEngineCapability | null | undefined,
): string | undefined {
  return bestConversionTarget(
    protocol,
    service.capabilities
      .filter((row) => !row.convert_to)
      .map((row) => row.protocol),
    engine,
  );
}

/** A usable service that takes this entry now, or could once it is enabled. */
export function supportsEntry(
  service: Service,
  protocol: string,
  engine: ConversionEngineCapability | null | undefined,
): boolean {
  return (
    serviceUsable(service) &&
    (hasEntry(service, protocol) ||
      Boolean(entryTarget(service, protocol, engine)))
  );
}

/**
 * The service a model reaches through this entry, preferring one that takes
 * the entry already so the existing route is kept.
 */
export function modelService(
  catalog: ModelCatalog,
  modelID: string,
  protocol: string,
  engine: ConversionEngineCapability | null | undefined,
): Service | undefined {
  const upstreamModel =
    catalog.redirects.find(
      (redirect) => redirect.enabled && redirect.from === modelID,
    )?.to ?? modelID;
  const candidates = catalog.services.filter(
    (service) =>
      service.models.includes(upstreamModel) &&
      supportsEntry(service, protocol, engine),
  );
  return (
    candidates.find((service) => hasEntry(service, protocol)) ?? candidates[0]
  );
}

/** The entry a model needs enabled before a client can use it. */
export function requiredEntry(
  catalog: ModelCatalog,
  modelID: string,
  protocol: string,
  engine: ConversionEngineCapability | null | undefined,
): { service: Service; target: string } | undefined {
  const service = modelService(catalog, modelID.trim(), protocol, engine);
  if (!service || hasEntry(service, protocol)) return undefined;
  const target = entryTarget(service, protocol, engine);
  return target ? { service, target } : undefined;
}

/**
 * The models a client can call: those of usable services that take one of
 * its protocols natively or through a conversion, then enabled redirect
 * sources whose target one of them serves. Kept in service order, each
 * model once.
 */
export function modelCandidates(
  catalog: ModelCatalog,
  protocols: readonly string[],
  engine: ConversionEngineCapability | null | undefined,
): string[] {
  const reaches = (service: Service, model: string) =>
    protocols.some(
      (protocol) =>
        protocolCarries(protocol, model) &&
        supportsEntry(service, protocol, engine),
    );
  const models = [
    ...catalog.services.flatMap((service) =>
      service.models.filter((model) => reaches(service, model)),
    ),
    ...catalog.redirects
      .filter(
        (redirect) =>
          redirect.enabled &&
          redirect.from !== astrlinkAutoModelId &&
          catalog.services.some(
            (service) =>
              service.models.includes(redirect.to) &&
              reaches(service, redirect.from),
          ),
      )
      .map((redirect) => redirect.from),
  ];
  return [...new Set(models.filter(Boolean))];
}

/** The protocol a model family speaks natively, if it has one. */
function familyProtocol(model: string): string | undefined {
  const name = model.toLowerCase().split("/").pop() ?? "";
  if (/^claude\b/.test(name)) return "anthropic.messages";
  if (/^(gpt|o\d)\b/.test(name) || name.startsWith("codex")) {
    return "openai.responses";
  }
  return undefined;
}

/**
 * The protocol to write a model with, for a client that takes several: one
 * the gateway serves the model with natively, else one it takes already,
 * else one it can enable. Ties go to the model family's own protocol
 * (Anthropic for Claude, Responses for GPT), then OpenAI Chat, then the
 * client's order.
 */
export function modelProtocol(
  catalog: ModelCatalog,
  model: string,
  protocols: readonly string[],
  engine: ConversionEngineCapability | null | undefined,
): string {
  const id = model.trim();
  const preferred = [familyProtocol(id), "openai.chat", ...protocols].filter(
    (protocol, index, all): protocol is string =>
      protocol !== undefined &&
      protocols.includes(protocol) &&
      all.indexOf(protocol) === index,
  );
  const rank = (protocol: string) => {
    if (!protocolCarries(protocol, id)) return 4;
    const service = modelService(catalog, id, protocol, engine);
    if (!service) return 3;
    const row = service.capabilities.find((item) => item.protocol === protocol);
    return !row ? 2 : row.convert_to ? 1 : 0;
  };
  let best = preferred[0] ?? protocols[0] ?? "openai.chat";
  for (const protocol of preferred) {
    if (rank(protocol) < rank(best)) best = protocol;
  }
  return best;
}

type ClaudeFamily = "haiku" | "sonnet" | "opus";

/** The version numbers a model ID carries for a family, and its date. */
function familyVersion(
  model: string,
  family: ClaudeFamily,
): { version: number[]; date: number } | null {
  const parts = model.toLowerCase().split(/[^a-z0-9]+/);
  if (!parts.includes(family)) return null;
  const version = parts.filter((part) => /^\d{1,3}$/.test(part)).map(Number);
  const date = Number(parts.find((part) => /^\d{6,8}$/.test(part)) ?? 0);
  return { version, date };
}

function compareVersions(a: number[], b: number[]): number {
  for (let index = 0; index < Math.max(a.length, b.length); index += 1) {
    const difference = (a[index] ?? -1) - (b[index] ?? -1);
    if (difference !== 0) return difference;
  }
  return 0;
}

/**
 * The newest model of a Claude family by name. An alias without a date wins
 * over a dated snapshot of the same version, since it follows the newest.
 */
export function newestModel(
  candidates: readonly string[],
  family: ClaudeFamily,
): string | undefined {
  type Ranked = { model: string; version: number[]; date: number };
  // Positive when `a` is newer than `b`.
  const newer = (a: Ranked, b: Ranked) => {
    const byVersion = compareVersions(a.version, b.version);
    if (byVersion !== 0) return byVersion;
    if ((a.date === 0) !== (b.date === 0)) return a.date === 0 ? 1 : -1;
    return a.date - b.date || b.model.length - a.model.length;
  };
  let best: Ranked | undefined;
  for (const model of candidates) {
    const parsed = familyVersion(model, family);
    if (!parsed) continue;
    const ranked = { model, ...parsed };
    if (!best || newer(ranked, best) > 0) best = ranked;
  }
  return best?.model;
}

/** Claude Code's tiers filled with each family's newest model. */
export function autoFillClaude(
  candidates: readonly string[],
): Pick<ModelDraft, "model" | "haikuModel" | "sonnetModel" | "opusModel"> {
  const haiku = newestModel(candidates, "haiku") ?? "";
  const sonnet = newestModel(candidates, "sonnet") ?? "";
  const opus = newestModel(candidates, "opus") ?? "";
  return {
    model: sonnet || opus || haiku,
    haikuModel: haiku,
    sonnetModel: sonnet,
    opusModel: opus,
  };
}

/** The model fields as the form holds them. */
export interface ModelDraft {
  model: string;
  haikuModel: string;
  sonnetModel: string;
  opusModel: string;
  fableModel: string;
  subagentModel: string;
  /** Blank leaves Codex's default. */
  reasoningEffort: string;
  /** Null follows the default list; see `listSelection`. */
  models: string[] | null;
  contextWindow: string;
  maxTokens: string;
}

export const claudeTiers = [
  "haikuModel",
  "sonnetModel",
  "opusModel",
  "fableModel",
  "subagentModel",
] as const;

/**
 * The form a place starts from: the models its config holds now; else the
 * first candidate, and for Claude Code each family's newest model.
 */
export function initialDraft(
  status: Pick<ClientConfigStatus, "client" | "models">,
  candidates: readonly string[],
): ModelDraft {
  const current = status.models;
  const text = (value: number | undefined) => (value ? String(value) : "");
  const draft: ModelDraft = {
    model: current.model ?? "",
    haikuModel: current.haikuModel ?? "",
    sonnetModel: current.sonnetModel ?? "",
    opusModel: current.opusModel ?? "",
    fableModel: current.fableModel ?? "",
    subagentModel: current.subagentModel ?? "",
    reasoningEffort: current.reasoningEffort ?? "",
    models: null,
    contextWindow: text(current.contextWindow),
    maxTokens: text(current.maxTokens),
  };
  if (status.client === "claude") {
    const holds = ["model", ...claudeTiers].some(
      (field) => current[field as keyof ClientConfigModels],
    );
    return holds ? draft : { ...draft, ...autoFillClaude(candidates) };
  }
  if (!draft.model) draft.model = candidates[0] ?? "";
  return draft;
}

/**
 * The models a list client switches between: what the user picked, else
 * the list the config holds now, else every candidate. Models the catalog no
 * longer offers drop out once it is known.
 */
export function listSelection(
  draft: ModelDraft,
  status: Pick<ClientConfigStatus, "models">,
  candidates: readonly string[],
  catalogKnown: boolean,
): string[] {
  if (draft.models) return draft.models;
  const current = status.models.models;
  if (!current) return [...candidates];
  return catalogKnown
    ? current.filter((model) => candidates.includes(model))
    : current;
}

/** A typed limit in tokens: undefined when blank, null when invalid. */
export function piLimit(text: string): number | null | undefined {
  const value = text.trim();
  if (!value) return undefined;
  const tokens = /^[0-9]{1,9}$/.test(value) ? Number(value) : 0;
  return tokens >= 1 && tokens <= maxPiLimit ? tokens : null;
}

/** What keeps a place from being written. */
export function draftProblem(
  client: ClientConfigClient,
  draft: ModelDraft,
): "model" | "limits" | null {
  if (client !== "claude" && !draft.model.trim()) return "model";
  if (
    client === "pi" &&
    (piLimit(draft.contextWindow) === null || piLimit(draft.maxTokens) === null)
  ) {
    return "limits";
  }
  return null;
}

/** The models sent for a write; blank fields are left out. */
export function draftModels(
  entry: Pick<ClientCatalogEntry, "id" | "list" | "protocols">,
  draft: ModelDraft,
  list: readonly string[],
  protocolOf: (model: string) => string,
): ClientConfigModels {
  const client = entry.id;
  const model = draft.model.trim();
  if (client === "claude") {
    return Object.fromEntries(
      (["model", ...claudeTiers] as const)
        .map((key) => [key, draft[key].trim()])
        .filter(([, value]) => value),
    );
  }
  const models: ClientConfigModels = { model };
  if (client === "codex" && draft.reasoningEffort) {
    models.reasoningEffort = draft.reasoningEffort;
  }
  if (entry.list) models.models = [...list];
  if (client === "pi") {
    for (const key of ["contextWindow", "maxTokens"] as const) {
      const tokens = piLimit(draft[key]);
      if (tokens) models[key] = tokens;
    }
  }
  if (entry.protocols.length > 1) {
    models.modelProtocols = Object.fromEntries(
      [...new Set([model, ...(models.models ?? [])])]
        .filter(Boolean)
        .map((id) => [id, protocolOf(id)]),
    );
  }
  return models;
}

/** A place worth listing: this computer, or a WSL home that has the client. */
export function placeShown(status: ClientConfigStatus): boolean {
  return (
    status.wsl === null || status.detected || status.state !== "not_configured"
  );
}

/** A place a write can go to: the client is there and its file is readable. */
export function placeWritable(status: ClientConfigStatus): boolean {
  return (
    status.state !== "invalid" &&
    (status.detected || status.state !== "not_configured")
  );
}

/** A config this token wrote, which unchecking removes. */
export function ownedConfig(
  status: ClientConfigStatus,
  tokenId: string,
): boolean {
  return (
    status.token_id === tokenId &&
    ["configured", "outdated", "modified"].includes(status.state)
  );
}

/**
 * Whether a place starts checked: what this token configures, and installed
 * clients nobody set up. Other tokens, other gateways, other tools and edited
 * configs stay as they are unless the dialog opens on what needs attention.
 * A client that is open refuses writes until it quits.
 */
export function checkedByDefault(
  status: ClientConfigStatus,
  tokenId: string,
  needsAttention: boolean,
): boolean {
  if (!placeWritable(status) || status.notes.includes("running")) return false;
  if (needsAttention) return true;
  if (status.token_id === tokenId) {
    return status.state === "configured" || status.state === "outdated";
  }
  return status.state === "not_configured" && status.detected;
}

/** A place: a client in a home and, for a client with several, a target. */
export type PlaceRef = Pick<ClientConfigStatus, "client" | "wsl" | "target">;

export function placeKey(place: PlaceRef): string {
  return `${place.client}:${place.wsl ?? ""}:${place.target ?? ""}`;
}

/** A client in one home: every target written there together. */
export function homeKey(place: Pick<PlaceRef, "client" | "wsl">): string {
  return `${place.client}:${place.wsl ?? ""}`;
}

/** The rail's groups, in order. */
export const railGroups = [
  "installed",
  "missing",
  "guide",
  "unsupported",
] as const;
export type RailGroup = (typeof railGroups)[number];

/**
 * Where a client sits in the rail: found on this computer (a written client
 * with a detected or configured place, or a link client the host found), not
 * found, filled in the client itself, or unable to reach the gateway.
 */
export function railGroupOf(
  entry: Pick<ClientCatalogEntry, "id" | "kind">,
  overview: Pick<ClientConfigOverview, "clients" | "installed">,
): RailGroup {
  if (entry.kind === "guide") return "guide";
  if (entry.kind === "unsupported") return "unsupported";
  if (entry.kind === "link") {
    return overview.installed.includes(entry.id) ? "installed" : "missing";
  }
  return overview.clients.some(
    (status) =>
      status.client === entry.id &&
      (status.detected || status.state !== "not_configured"),
  )
    ? "installed"
    : "missing";
}

/** Whether a search matches a client by name or id, ignoring case and spaces. */
export function clientMatches(
  entry: Pick<ClientCatalogEntry, "id" | "name">,
  query: string,
): boolean {
  const flat = (text: string) =>
    text.toLocaleLowerCase().replace(/[\s_-]+/g, "");
  const wanted = flat(query);
  return (
    !wanted ||
    flat(entry.name).includes(wanted) ||
    flat(entry.id).includes(wanted)
  );
}

/**
 * The place a client's home shows its settings and state for: the one that
 * needs attention, else this token's config, else any config, else one a
 * write can go to. Checks do not move it, so the form keeps its values.
 */
export function leadPlace(
  places: readonly ClientConfigStatus[],
  tokenId: string,
  needsAttention: (status: ClientConfigStatus) => boolean,
): ClientConfigStatus | undefined {
  return (
    places.find(needsAttention) ??
    places.find((status) => ownedConfig(status, tokenId)) ??
    places.find((status) => status.state !== "not_configured") ??
    places.find(placeWritable) ??
    places[0]
  );
}
