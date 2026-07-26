export const SUPPORTED_CONTROL_API_VERSION = "v1";
export const SUPPORTED_PROTOCOL_CONTRACT_VERSION = "v1";

export type CorePhase =
  | "stopped"
  | "spawning"
  | "waiting_for_ready"
  | "handshaking"
  | "ready"
  | "stopping"
  | "exited"
  | "error"
  | "unavailable";

export interface ReadyAnnouncement {
  event: "ready";
  core_version: string;
  control_api_version: string;
  protocol_contract_version: string;
  inference_url: string;
  control_url: string;
}

export interface HealthResponse {
  status: string;
}

export interface VersionResponse {
  core_version: string;
  control_api_version: string;
  protocol_contract_version: string;
  build_commit: string;
}

export interface ProtocolCapability {
  id: string;
  phase: "alpha" | "post_alpha";
  primary: boolean;
  streaming: boolean;
}

export interface PlanTypeCapability {
  id: "native" | "delegated" | "relaykit";
  available_in_alpha: boolean;
  uses_local_conversion: boolean;
}

export interface ConversionEngineCapability {
  name: "relaykit";
  version: string | null;
  available: boolean;
  edges: unknown[];
}

export interface CapabilitiesResponse {
  protocol_contract_version: string;
  protocols: ProtocolCapability[];
  plan_types: PlanTypeCapability[];
  conversion_engine: ConversionEngineCapability;
}

export interface CoreSnapshot {
  phase: CorePhase;
  pid: number | null;
  ready: ReadyAnnouncement | null;
  health: HealthResponse | null;
  version: VersionResponse | null;
  capabilities: CapabilitiesResponse | null;
  last_error: string | null;
}

export interface AppSnapshot extends CoreSnapshot {
  app_version: string;
}

type JsonObject = Record<string, unknown>;

const nativeCorePhases = new Set<CorePhase>([
  "stopped",
  "spawning",
  "waiting_for_ready",
  "handshaking",
  "ready",
  "stopping",
  "exited",
  "error",
]);

const protocolIDPattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const contractVersionPattern = /^[0-9A-Za-z][0-9A-Za-z._+-]*$/;
const buildCommitPattern = /^(?:[0-9a-f]{7,64}|unknown)$/;
const loopbackURLPattern =
  /^http:\/\/127\.0\.0\.1:(?:[1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])(?![\s\S])/;

const requiredAlphaProtocols = [
  { id: "openai.responses", primary: true, streaming: true },
  { id: "openai.responses.compact", primary: false, streaming: false },
  { id: "anthropic.messages", primary: false, streaming: true },
  { id: "google.generate_content", primary: false, streaming: true },
  { id: "openai.chat", primary: false, streaming: true },
  { id: "openai.completions", primary: false, streaming: true },
  { id: "openai.models", primary: false, streaming: false },
  { id: "google.models", primary: false, streaming: false },
] as const;

function invalid(path: string, detail: string): never {
  throw new Error(`Invalid AstrLink IPC snapshot at ${path}: ${detail}`);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as JsonObject;
}

function exactKeys(value: JsonObject, expected: readonly string[], path: string): void {
  const expectedSet = new Set(expected);
  for (const key of Object.keys(value)) {
    if (!expectedSet.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of expected) {
    if (!Object.hasOwn(value, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function stringAt(value: unknown, path: string, maxLength = 256): string {
  if (typeof value !== "string" || value.length === 0 || value.length > maxLength) {
    return invalid(path, `expected a string containing 1 to ${maxLength} characters`);
  }
  return value;
}

function booleanAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") return invalid(path, "expected a boolean");
  return value;
}

function arrayAt(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) return invalid(path, "expected an array");
  return value;
}

function nullable<T>(
  value: unknown,
  path: string,
  parser: (nested: unknown, nestedPath: string) => T,
): T | null {
  return value === null ? null : parser(value, path);
}

function contractVersionAt(value: unknown, path: string): string {
  const version = stringAt(value, path, 64);
  if (!contractVersionPattern.test(version)) invalid(path, "invalid contract version");
  return version;
}

function supportedVersionAt(value: unknown, path: string, supported: string): string {
  const version = contractVersionAt(value, path);
  if (version !== supported) invalid(path, `unsupported version ${JSON.stringify(version)}`);
  return version;
}

function parseReady(value: unknown, path: string): ReadyAnnouncement {
  const ready = objectAt(value, path);
  exactKeys(
    ready,
    [
      "event",
      "core_version",
      "control_api_version",
      "protocol_contract_version",
      "inference_url",
      "control_url",
    ],
    path,
  );
  if (ready.event !== "ready") invalid(`${path}.event`, 'expected "ready"');
  const coreVersion = stringAt(ready.core_version, `${path}.core_version`, 64);
  const controlVersion = supportedVersionAt(
    ready.control_api_version,
    `${path}.control_api_version`,
    SUPPORTED_CONTROL_API_VERSION,
  );
  const protocolVersion = supportedVersionAt(
    ready.protocol_contract_version,
    `${path}.protocol_contract_version`,
    SUPPORTED_PROTOCOL_CONTRACT_VERSION,
  );
  const inferenceURL = stringAt(ready.inference_url, `${path}.inference_url`, 128);
  const controlURL = stringAt(ready.control_url, `${path}.control_url`, 128);
  if (!loopbackURLPattern.test(inferenceURL)) {
    invalid(`${path}.inference_url`, "expected a canonical IPv4 loopback URL");
  }
  if (!loopbackURLPattern.test(controlURL)) {
    invalid(`${path}.control_url`, "expected a canonical IPv4 loopback URL");
  }
  return {
    event: "ready",
    core_version: coreVersion,
    control_api_version: controlVersion,
    protocol_contract_version: protocolVersion,
    inference_url: inferenceURL,
    control_url: controlURL,
  };
}

function parseHealth(value: unknown, path: string): HealthResponse {
  const health = objectAt(value, path);
  exactKeys(health, ["status"], path);
  return { status: stringAt(health.status, `${path}.status`, 64) };
}

function parseVersion(value: unknown, path: string): VersionResponse {
  const version = objectAt(value, path);
  exactKeys(
    version,
    [
      "core_version",
      "control_api_version",
      "protocol_contract_version",
      "build_commit",
    ],
    path,
  );
  const buildCommit = stringAt(version.build_commit, `${path}.build_commit`, 64);
  if (!buildCommitPattern.test(buildCommit)) {
    invalid(`${path}.build_commit`, "expected unknown or a lowercase hexadecimal commit");
  }
  return {
    core_version: stringAt(version.core_version, `${path}.core_version`, 64),
    control_api_version: supportedVersionAt(
      version.control_api_version,
      `${path}.control_api_version`,
      SUPPORTED_CONTROL_API_VERSION,
    ),
    protocol_contract_version: supportedVersionAt(
      version.protocol_contract_version,
      `${path}.protocol_contract_version`,
      SUPPORTED_PROTOCOL_CONTRACT_VERSION,
    ),
    build_commit: buildCommit,
  };
}

function parseProtocol(value: unknown, path: string): ProtocolCapability {
  const protocol = objectAt(value, path);
  exactKeys(protocol, ["id", "phase", "primary", "streaming"], path);
  const id = stringAt(protocol.id, `${path}.id`, 96);
  if (id.length < 3 || !protocolIDPattern.test(id)) {
    invalid(`${path}.id`, "invalid protocol ID");
  }
  if (protocol.phase !== "alpha" && protocol.phase !== "post_alpha") {
    invalid(`${path}.phase`, 'expected "alpha" or "post_alpha"');
  }
  return {
    id,
    phase: protocol.phase,
    primary: booleanAt(protocol.primary, `${path}.primary`),
    streaming: booleanAt(protocol.streaming, `${path}.streaming`),
  };
}

function parsePlan(value: unknown, path: string): PlanTypeCapability {
  const plan = objectAt(value, path);
  exactKeys(plan, ["id", "available_in_alpha", "uses_local_conversion"], path);
  if (plan.id !== "native" && plan.id !== "delegated" && plan.id !== "relaykit") {
    invalid(`${path}.id`, "unknown plan type");
  }
  return {
    id: plan.id,
    available_in_alpha: booleanAt(plan.available_in_alpha, `${path}.available_in_alpha`),
    uses_local_conversion: booleanAt(
      plan.uses_local_conversion,
      `${path}.uses_local_conversion`,
    ),
  };
}

function parseConversionEngine(
  value: unknown,
  path: string,
): ConversionEngineCapability {
  const engine = objectAt(value, path);
  exactKeys(engine, ["name", "version", "available", "edges"], path);
  if (engine.name !== "relaykit") invalid(`${path}.name`, 'expected "relaykit"');
  if (engine.version !== null) invalid(`${path}.version`, "must be null in Alpha");
  if (engine.available !== false) invalid(`${path}.available`, "must be false in Alpha");
  const edges = arrayAt(engine.edges, `${path}.edges`);
  if (edges.length !== 0) invalid(`${path}.edges`, "must be empty in Alpha");
  return { name: "relaykit", version: null, available: false, edges: [] };
}

function parseCapabilities(value: unknown, path: string): CapabilitiesResponse {
  const capabilities = objectAt(value, path);
  exactKeys(
    capabilities,
    ["protocol_contract_version", "protocols", "plan_types", "conversion_engine"],
    path,
  );
  const protocolContractVersion = supportedVersionAt(
    capabilities.protocol_contract_version,
    `${path}.protocol_contract_version`,
    SUPPORTED_PROTOCOL_CONTRACT_VERSION,
  );
  const rawProtocols = arrayAt(capabilities.protocols, `${path}.protocols`);
  if (rawProtocols.length < requiredAlphaProtocols.length) {
    invalid(`${path}.protocols`, `expected at least ${requiredAlphaProtocols.length} entries`);
  }
  const protocols = rawProtocols.map((protocol, index) =>
    parseProtocol(protocol, `${path}.protocols[${index}]`),
  );
  const byID = new Map<string, ProtocolCapability>();
  for (const protocol of protocols) {
    if (byID.has(protocol.id)) invalid(`${path}.protocols`, `duplicate protocol ${protocol.id}`);
    byID.set(protocol.id, protocol);
  }
  for (const expected of requiredAlphaProtocols) {
    const protocol = byID.get(expected.id);
    if (!protocol) invalid(`${path}.protocols`, `missing required protocol ${expected.id}`);
    if (
      protocol.phase !== "alpha" ||
      protocol.primary !== expected.primary ||
      protocol.streaming !== expected.streaming
    ) {
      invalid(`${path}.protocols`, `invalid Alpha descriptor for ${expected.id}`);
    }
  }

  const rawPlans = arrayAt(capabilities.plan_types, `${path}.plan_types`);
  if (rawPlans.length !== 3) invalid(`${path}.plan_types`, "expected exactly three plans");
  const plans = rawPlans.map((plan, index) =>
    parsePlan(plan, `${path}.plan_types[${index}]`),
  );
  const plansByID = new Map(plans.map((plan) => [plan.id, plan]));
  if (plansByID.size !== 3) invalid(`${path}.plan_types`, "plan IDs must be unique");
  const expectedPlans = {
    native: { available: true, converts: false },
    delegated: { available: true, converts: false },
    relaykit: { available: false, converts: true },
  } as const;
  for (const [id, expected] of Object.entries(expectedPlans)) {
    const plan = plansByID.get(id as PlanTypeCapability["id"]);
    if (
      !plan ||
      plan.available_in_alpha !== expected.available ||
      plan.uses_local_conversion !== expected.converts
    ) {
      invalid(`${path}.plan_types`, `invalid Alpha semantics for ${id}`);
    }
  }

  return {
    protocol_contract_version: protocolContractVersion,
    protocols,
    plan_types: plans,
    conversion_engine: parseConversionEngine(
      capabilities.conversion_engine,
      `${path}.conversion_engine`,
    ),
  };
}

function parsePID(value: unknown, path: string): number | null {
  if (value === null) return null;
  if (typeof value !== "number" || !Number.isInteger(value) || value < 1 || value > 0xffff_ffff) {
    return invalid(path, "expected null or a positive 32-bit process ID");
  }
  return value;
}

function parseLastError(value: unknown, path: string): string | null {
  if (value === null) return null;
  if (typeof value !== "string") return invalid(path, "expected null or a string");
  return value;
}

export function parseAppSnapshot(value: unknown): AppSnapshot {
  const path = "$";
  const snapshot = objectAt(value, path);
  exactKeys(
    snapshot,
    [
      "app_version",
      "phase",
      "pid",
      "ready",
      "last_error",
      "health",
      "version",
      "capabilities",
    ],
    path,
  );
  if (typeof snapshot.phase !== "string" || !nativeCorePhases.has(snapshot.phase as CorePhase)) {
    invalid("$.phase", "unknown native Core phase");
  }

  const parsed: AppSnapshot = {
    app_version: stringAt(snapshot.app_version, "$.app_version", 64),
    phase: snapshot.phase as CorePhase,
    pid: parsePID(snapshot.pid, "$.pid"),
    ready: nullable(snapshot.ready, "$.ready", parseReady),
    last_error: parseLastError(snapshot.last_error, "$.last_error"),
    health: nullable(snapshot.health, "$.health", parseHealth),
    version: nullable(snapshot.version, "$.version", parseVersion),
    capabilities: nullable(snapshot.capabilities, "$.capabilities", parseCapabilities),
  };

  if (parsed.ready && parsed.version && parsed.ready.core_version !== parsed.version.core_version) {
    invalid("$.version.core_version", "does not match the ready announcement");
  }
  if (
    parsed.phase === "ready" &&
    (!parsed.pid || !parsed.ready || !parsed.health || !parsed.version || !parsed.capabilities)
  ) {
    invalid("$", "the ready phase requires pid, ready, health, version, and capabilities");
  }
  return parsed;
}

export const browserSnapshot = (): AppSnapshot => ({
  app_version: "Unknown",
  phase: "unavailable",
  pid: null,
  ready: null,
  health: null,
  version: null,
  capabilities: null,
  last_error: "The native bridge is unavailable. Open this UI with Tauri.",
});

export function failedSnapshot(
  current: AppSnapshot | null,
  message: string,
): AppSnapshot {
  return {
    app_version: current?.app_version ?? "Unknown",
    phase: "error",
    pid: null,
    ready: null,
    health: null,
    version: null,
    capabilities: null,
    last_error: message,
  };
}

export function phaseLabel(phase: CorePhase): string {
  switch (phase) {
    case "ready":
      return "已就绪";
    case "spawning":
      return "启动中";
    case "waiting_for_ready":
      return "等待 Core";
    case "handshaking":
      return "校验中";
    case "stopping":
      return "停止中";
    case "exited":
      return "已退出";
    case "error":
      return "需要处理";
    case "unavailable":
      return "浏览器预览";
    default:
      return "已停止";
  }
}

export function phaseTone(
  phase: CorePhase,
): "positive" | "pending" | "negative" | "neutral" {
  if (phase === "ready") return "positive";
  if (
    phase === "spawning" ||
    phase === "waiting_for_ready" ||
    phase === "handshaking" ||
    phase === "stopping"
  ) {
    return "pending";
  }
  if (phase === "error" || phase === "exited") return "negative";
  return "neutral";
}

export function alphaPlanCount(capabilities: CapabilitiesResponse | null): number {
  return (
    capabilities?.plan_types.filter((plan) => plan.available_in_alpha).length ?? 0
  );
}
