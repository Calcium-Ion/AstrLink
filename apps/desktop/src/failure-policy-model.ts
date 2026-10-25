import { parseBuiltinTools, type BuiltinTools } from "./builtin-tools-model";
import {
  parseModelRoutingGroups,
  type ModelRoutingGroup,
} from "./model-routing-group-model";
import type { ClassifierDimension } from "./classifier-settings-model";
export type FailureAction =
  | "stop"
  | "retry"
  | "failover"
  | "retry_and_failover";
export type FailoverStrategy =
  | "retry_first"
  | "failover_first"
  | "failover_only";

export interface FailurePolicy {
  max_retries: number;
  initial_delay_ms: number;
  max_delay_ms: number;
  response_start_timeout_seconds?: number;
  thinking_signature_recovery?: boolean;
  openai_reasoning_recovery?: boolean;
  openai_function_output_recovery?: boolean;
  network_error: FailureAction;
  response_timeout: FailureAction;
  http_status: Record<string, FailureAction>;
}

export interface FailoverPolicy {
  enabled: boolean;
  strategy: FailoverStrategy;
  max_attempts: number;
}

export interface ChannelStickiness {
  enabled: boolean;
  ttl_seconds: number;
}

export const identitySettingKeys = [
  "codex_identity_enforcement",
  "claude_identity_enforcement",
  "grok_identity_enforcement",
] as const;
export type IdentitySettingKey = (typeof identitySettingKeys)[number];

export const subscriptionProtectionKeys = [
  "official_client_passthrough",
  "subscription_risk_protection",
  "codex_request_normalization",
  "claude_request_normalization",
  "subscription_session_isolation",
] as const;
export type SubscriptionProtectionKey =
  (typeof subscriptionProtectionKeys)[number];

export const identityLearningKeys = [
  "codex_identity_auto_learn",
  "claude_identity_auto_learn",
  "grok_identity_auto_learn",
] as const;
export type IdentityLearningKey = (typeof identityLearningKeys)[number];

export const identityVersionKeys = [
  "codex_identity_version",
  "claude_identity_version",
  "grok_identity_version",
] as const;
export type IdentityVersionKey = (typeof identityVersionKeys)[number];

// Every Forwarding identity switch is on unless the user turns it off.
const forwardingSwitchKeys = [
  ...identitySettingKeys,
  ...subscriptionProtectionKeys,
  ...identityLearningKeys,
] as const;

export const maxIdentityVersionLength = 64;
const identityVersionPattern =
  /^([0-9]+)\.([0-9]+)\.([0-9]+)(-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;
// The oldest Codex client the Codex backend accepts.
const minCodexIdentityVersion = [0, 144, 0];

/**
 * Mirrors the core's version override rules: a bare semantic version whose
 * release numbers fit 32 bits, and for Codex 0.144.0 or newer. An empty string
 * clears the override.
 */
export function validIdentityVersion(
  key: IdentityVersionKey,
  value: string,
): boolean {
  if (value === "") return true;
  const match =
    value.length <= maxIdentityVersionLength
      ? identityVersionPattern.exec(value)
      : null;
  if (!match) return false;
  const release = match.slice(1, 4).map(Number);
  if (release.some((part) => part > 0xffffffff)) return false;
  if (key !== "codex_identity_version") return true;
  for (const [index, part] of release.entries()) {
    if (part !== minCodexIdentityVersion[index])
      return part > minCodexIdentityVersion[index];
  }
  // A pre-release sorts before its release.
  return match[4] === undefined;
}

/**
 * How the gateway sets thinking: "passthrough" leaves it to each client,
 * "fixed" sends every request at the level, and "adaptive" sends the lower
 * end of the preset's range for a simple request and moves up a level for
 * each of a long conversation, many tools and a long last message.
 */
export const reasoningModes = ["passthrough", "fixed", "adaptive"] as const;
export type ReasoningMode = (typeof reasoningModes)[number];
/** Thinking levels from least to most; "max" is each model's highest. */
export const reasoningLevels = ["off", "low", "medium", "high", "max"] as const;
export type ReasoningLevel = (typeof reasoningLevels)[number];
/** The adaptive ranges; "custom" runs from adaptive_min to adaptive_max. */
export const adaptivePresets = ["everyday", "research", "custom"] as const;
export type AdaptivePreset = (typeof adaptivePresets)[number];
export type LevelRange = readonly [ReasoningLevel, ReasoningLevel];
/** The ranges of the presets that set their own. */
export const presetRanges = {
  everyday: ["low", "max"],
  research: ["medium", "max"],
} as const satisfies Record<Exclude<AdaptivePreset, "custom">, LevelRange>;

/** The range the adaptive mode uses with the settings' preset. */
export function adaptiveRange(
  settings: Pick<
    RoutingSettings,
    "adaptive_preset" | "adaptive_min" | "adaptive_max"
  >,
): LevelRange {
  const preset = settings.adaptive_preset ?? "everyday";
  if (preset !== "custom") return presetRanges[preset];
  return [settings.adaptive_min ?? "low", settings.adaptive_max ?? "max"];
}

/** The core's limit for the global prompt prefix and suffix, in code points. */
export const maxSystemPromptLength = 20000;
const systemPromptTextKeys = [
  "system_prompt_prefix",
  "system_prompt_suffix",
] as const;
export const thinkingPromptKeys = [
  "system_prompt_enabled",
  ...systemPromptTextKeys,
  "reasoning_mode",
  "reasoning_level",
  "adaptive_preset",
  "adaptive_min",
  "adaptive_max",
] as const;

/**
 * "lossless" keeps every pixel and re-encodes PNG and single-frame GIF images;
 * "lossy" encodes at the quality and also re-encodes JPEG images, sending a
 * PNG or GIF lossless when that is smaller.
 */
export const imageCompressionModes = ["lossless", "lossy"] as const;
export type ImageCompressionMode = (typeof imageCompressionModes)[number];

/**
 * Re-encodes the base64 images of inference requests as WebP before they are
 * forwarded. An image is replaced only when its WebP is smaller.
 */
export interface ImageCompression {
  enabled: boolean;
  mode: ImageCompressionMode;
  /** The lossy encoder's quality, 1–100; kept while lossless is chosen. */
  quality: number;
}

export function defaultImageCompression(): ImageCompression {
  return { enabled: false, mode: "lossless", quality: 85 };
}

export interface ModelRedirect {
  from: string;
  to: string;
  enabled: boolean;
}

export const maxModelRedirects = 200;
export const maxRedirectModelLength = 256;
export const astrlinkAutoModelId = "astrlink/auto";

/**
 * A built-in redirect row is always shown. Until the user changes it, it
 * uses its default target and switch state; a change is saved as a rule.
 */
export interface BuiltinModelRedirect {
  /** Key under `modelRedirect.builtin` in the locales. A row without one is labeled by its source model. */
  id?: "codexAutoReview";
  from: string;
  defaultTo: string;
  /** Routing-wide built-in rules start off; a provider's start on. */
  defaultEnabled?: boolean;
}

export const builtinModelRedirects: readonly BuiltinModelRedirect[] = [
  {
    id: "codexAutoReview",
    from: "codex-auto-review",
    defaultTo: "gpt-5.6-luna",
  },
];

export type ModelRedirectIssue =
  | "empty_from"
  | "empty_to"
  | "too_long"
  | "control_character"
  | "whitespace"
  | "same_model"
  | "auto_target"
  | "duplicate_from"
  | "chained_target";

// Go's unicode.IsSpace, which strings.TrimSpace uses; JavaScript's trim()
// differs at U+0085 and U+FEFF.
const goSpace =
  "\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000";
const edgeSpace = new RegExp(`^[${goSpace}]|[${goSpace}]$`, "u");

function hasControlCharacter(value: string) {
  for (const character of value) {
    const code = character.codePointAt(0)!;
    if (code < 32 && code !== 9) return true;
  }
  return false;
}

/** Mirrors contract.ValidateModelRedirects; the first issue per row wins. */
export function modelRedirectIssues(
  redirects: readonly ModelRedirect[],
): (ModelRedirectIssue | undefined)[] {
  const sources = new Map<string, number>();
  for (const redirect of redirects)
    sources.set(redirect.from, (sources.get(redirect.from) ?? 0) + 1);
  return redirects.map((redirect) => {
    const models = [redirect.from, redirect.to];
    if (!redirect.from) return "empty_from";
    if (!redirect.to) return "empty_to";
    if (models.some((model) => [...model].length > maxRedirectModelLength))
      return "too_long";
    if (models.some(hasControlCharacter)) return "control_character";
    if (models.some((model) => edgeSpace.test(model))) return "whitespace";
    if (redirect.from === redirect.to) return "same_model";
    if (redirect.to === astrlinkAutoModelId) return "auto_target";
    if ((sources.get(redirect.from) ?? 0) > 1) return "duplicate_from";
    if (sources.has(redirect.to)) return "chained_target";
    return undefined;
  });
}

export interface RoutingSettings {
  builtin_tools?: BuiltinTools;
  codex_identity_enforcement?: boolean;
  claude_identity_enforcement?: boolean;
  grok_identity_enforcement?: boolean;
  official_client_passthrough?: boolean;
  subscription_risk_protection?: boolean;
  codex_request_normalization?: boolean;
  claude_request_normalization?: boolean;
  subscription_session_isolation?: boolean;
  codex_identity_auto_learn?: boolean;
  claude_identity_auto_learn?: boolean;
  grok_identity_auto_learn?: boolean;
  /** Minimum declared client version; absent when no override is set. */
  codex_identity_version?: string;
  claude_identity_version?: string;
  grok_identity_version?: string;
  /**
   * Adds the prefix and suffix to the system prompt of inference requests when
   * on. Claude Code's Haiku requests keep their own prompt.
   */
  system_prompt_enabled?: boolean;
  system_prompt_prefix?: string;
  system_prompt_suffix?: string;
  reasoning_mode?: ReasoningMode;
  /**
   * The fixed mode's level. It and the adaptive fields are kept while another
   * mode is chosen, so switching back restores them.
   */
  reasoning_level?: ReasoningLevel;
  adaptive_preset?: AdaptivePreset;
  /** The custom range; the lower end is at most the upper end. */
  adaptive_min?: ReasoningLevel;
  adaptive_max?: ReasoningLevel;
  image_compression?: ImageCompression;
  model_redirects?: ModelRedirect[];
  /**
   * Model routing groups, replaced wholesale by one PATCH. Absent means none;
   * a present `null` is rejected, mirroring the control API.
   */
  model_routing_groups?: ModelRoutingGroup[];
  channel_stickiness?: ChannelStickiness;
  default_recovery_paths?: Record<string, string>;
  default_failure_policy: FailurePolicy;
  allow_unmatched_failover: boolean;
  strategy: FailoverStrategy;
  max_attempts: number;
}

export const failureActions: FailureAction[] = [
  "stop",
  "retry",
  "failover",
  "retry_and_failover",
];

export function defaultFailurePolicy(): FailurePolicy {
  return {
    max_retries: 1,
    initial_delay_ms: 500,
    max_delay_ms: 5000,
    network_error: "retry_and_failover",
    response_timeout: "retry_and_failover",
    http_status: Object.fromEntries([
      ...[408, 429, 500, 502, 503, 504, 529].map((code) => [
        String(code),
        "retry_and_failover",
      ]),
      ["401", "failover"],
      ["403", "failover"],
    ]) as Record<string, FailureAction>,
  };
}

export function defaultFailoverPolicy(): FailoverPolicy {
  return { enabled: true, strategy: "retry_first", max_attempts: 6 };
}

function object(value: unknown, path: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error(`${path}: expected an object`);
  return value as Record<string, unknown>;
}
function keys(
  value: Record<string, unknown>,
  required: string[],
  optional: string[],
  path: string,
) {
  if (
    required.some((key) => !Object.hasOwn(value, key)) ||
    Object.keys(value).some(
      (key) => !required.includes(key) && !optional.includes(key),
    )
  )
    throw new Error(`${path}: unexpected or missing field`);
}
function integer(
  value: unknown,
  min: number,
  max: number,
  path: string,
): number {
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < min ||
    value > max
  )
    throw new Error(`${path}: expected an integer from ${min} through ${max}`);
  return value;
}
function action(value: unknown, path: string): FailureAction {
  if (!failureActions.includes(value as FailureAction))
    throw new Error(`${path}: unknown failure action`);
  return value as FailureAction;
}

export function parseFailurePolicy(
  value: unknown,
  path = "failure_policy",
): FailurePolicy {
  const policy = object(value, path);
  keys(
    policy,
    [
      "max_retries",
      "initial_delay_ms",
      "max_delay_ms",
      "network_error",
      "response_timeout",
      "http_status",
    ],
    [
      "response_start_timeout_seconds",
      "thinking_signature_recovery",
      "openai_reasoning_recovery",
      "openai_function_output_recovery",
    ],
    path,
  );
  const initial = integer(
    policy.initial_delay_ms,
    0,
    60000,
    `${path}.initial_delay_ms`,
  );
  for (const key of [
    "thinking_signature_recovery",
    "openai_reasoning_recovery",
    "openai_function_output_recovery",
  ]) {
    if (Object.hasOwn(policy, key) && typeof policy[key] !== "boolean")
      throw new Error(`${path}.${key}: expected a boolean`);
  }
  const statuses = object(policy.http_status, `${path}.http_status`);
  const http_status: Record<string, FailureAction> = {};
  for (const [code, value] of Object.entries(statuses)) {
    if (!/^[45]\d{2}$/.test(code))
      throw new Error(
        `${path}.http_status: expected a status code from 400 through 599`,
      );
    http_status[code] = action(value, `${path}.http_status.${code}`);
  }
  return {
    max_retries: integer(policy.max_retries, 0, 5, `${path}.max_retries`),
    ...(Object.hasOwn(policy, "thinking_signature_recovery")
      ? {
          thinking_signature_recovery:
            policy.thinking_signature_recovery as boolean,
        }
      : {}),
    ...(Object.hasOwn(policy, "openai_reasoning_recovery")
      ? {
          openai_reasoning_recovery:
            policy.openai_reasoning_recovery as boolean,
        }
      : {}),
    ...(Object.hasOwn(policy, "openai_function_output_recovery")
      ? {
          openai_function_output_recovery:
            policy.openai_function_output_recovery as boolean,
        }
      : {}),
    initial_delay_ms: initial,
    max_delay_ms: integer(
      policy.max_delay_ms,
      initial,
      60000,
      `${path}.max_delay_ms`,
    ),
    ...(Object.hasOwn(policy, "response_start_timeout_seconds")
      ? {
          response_start_timeout_seconds: integer(
            policy.response_start_timeout_seconds,
            0,
            86400,
            `${path}.response_start_timeout_seconds`,
          ),
        }
      : {}),
    network_error: action(policy.network_error, `${path}.network_error`),
    response_timeout: action(
      policy.response_timeout,
      `${path}.response_timeout`,
    ),
    http_status,
  };
}

export function parseFailoverPolicy(
  value: unknown,
  path = "failover",
): FailoverPolicy {
  const policy = object(value, path);
  keys(policy, ["enabled", "strategy", "max_attempts"], [], path);
  if (
    typeof policy.enabled !== "boolean" ||
    (policy.strategy !== "retry_first" &&
      policy.strategy !== "failover_first" &&
      policy.strategy !== "failover_only")
  )
    throw new Error(`${path}: invalid switch or strategy`);
  return {
    enabled: policy.enabled,
    strategy: policy.strategy,
    max_attempts: integer(policy.max_attempts, 1, 20, `${path}.max_attempts`),
  };
}

export function parseModelRedirects(value: unknown): ModelRedirect[] {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > maxModelRedirects)
    throw Error("model_redirects: expected an array");
  const redirects = value.map((item, index) => {
    const path = `model_redirects[${index}]`;
    const redirect = object(item, path);
    keys(redirect, ["from", "to", "enabled"], [], path);
    if (
      typeof redirect.from !== "string" ||
      typeof redirect.to !== "string" ||
      typeof redirect.enabled !== "boolean"
    )
      throw Error(`${path}: invalid model redirect`);
    return {
      from: redirect.from,
      to: redirect.to,
      enabled: redirect.enabled,
    };
  });
  if (modelRedirectIssues(redirects).some(Boolean))
    throw Error("model_redirects: invalid model redirect");
  return redirects;
}

export type LearnedClient = "codex" | "claude" | "grok";

/** A client's version learned from official requests and its built-in one. */
export interface ClientIdentityStatus {
  /** Absent until an official request is learned, even while learning is off. */
  learned_version?: string;
  builtin_version: string;
}

export type ClientIdentities = Record<LearnedClient, ClientIdentityStatus>;

export function parseClientIdentities(value: unknown): ClientIdentities {
  const identities = object(value, "client_identities");
  keys(identities, ["codex", "claude", "grok"], [], "client_identities");
  const parse = (client: LearnedClient): ClientIdentityStatus => {
    const path = `client_identities.${client}`;
    const status = object(identities[client], path);
    keys(status, ["builtin_version"], ["learned_version"], path);
    for (const key of ["builtin_version", "learned_version"]) {
      if (!Object.hasOwn(status, key)) continue;
      const version = status[key];
      if (
        typeof version !== "string" ||
        version === "" ||
        !validIdentityVersion(`${client}_identity_version`, version)
      )
        throw Error(`${path}.${key}: invalid client version`);
    }
    return {
      ...(status.learned_version === undefined
        ? {}
        : { learned_version: status.learned_version as string }),
      builtin_version: status.builtin_version as string,
    };
  };
  return {
    codex: parse("codex"),
    claude: parse("claude"),
    grok: parse("grok"),
  };
}

/** Mirrors contract.ImageCompression; an older core without it reads as off. */
function parseImageCompression(value: unknown): ImageCompression {
  if (value === undefined) return defaultImageCompression();
  const settings = object(value, "image_compression");
  keys(settings, ["enabled", "mode", "quality"], [], "image_compression");
  if (typeof settings.enabled !== "boolean")
    throw Error("image_compression.enabled: expected a boolean");
  if (!imageCompressionModes.includes(settings.mode as ImageCompressionMode))
    throw Error("image_compression.mode: unknown mode");
  return {
    enabled: settings.enabled,
    mode: settings.mode as ImageCompressionMode,
    quality: integer(settings.quality, 1, 100, "image_compression.quality"),
  };
}

/**
 * `dimensions` are the custom dimensions the classifier settings declare, which
 * rules may name; `null` when they could not be read, which leaves every name
 * that is not a built-in field to Core rather than refusing the document.
 */
export function parseRoutingSettings(
  value: unknown,
  dimensions?: ClassifierDimension[] | null,
): RoutingSettings {
  const settings = object(value, "routing_settings");
  keys(
    settings,
    [
      "allow_unmatched_failover",
      "strategy",
      "max_attempts",
      "default_failure_policy",
    ],
    [
      "default_recovery_paths",
      "channel_stickiness",
      "model_redirects",
      "model_routing_groups",
      "builtin_tools",
      "image_compression",
      ...forwardingSwitchKeys,
      ...identityVersionKeys,
      ...thinkingPromptKeys,
    ],
    "routing_settings",
  );
  const imageCompression = parseImageCompression(settings.image_compression);
  const redirects = parseModelRedirects(settings.model_redirects);
  const modelRoutingGroups = parseModelRoutingGroups(
    settings.model_routing_groups,
    dimensions === null
      ? null
      : (dimensions ?? []).map((dimension) => dimension.name),
  );
  const parsed = parseFailoverPolicy({
    enabled: settings.allow_unmatched_failover,
    strategy: settings.strategy,
    max_attempts: settings.max_attempts,
  });
  for (const key of forwardingSwitchKeys) {
    if (Object.hasOwn(settings, key) && typeof settings[key] !== "boolean") {
      throw Error(`${key}: expected a boolean`);
    }
  }
  if (
    Object.hasOwn(settings, "system_prompt_enabled") &&
    typeof settings.system_prompt_enabled !== "boolean"
  )
    throw Error("system_prompt_enabled: expected a boolean");
  for (const key of systemPromptTextKeys) {
    if (!Object.hasOwn(settings, key)) continue;
    const text = settings[key];
    if (typeof text !== "string" || [...text].length > maxSystemPromptLength)
      throw Error(
        `${key}: expected text of at most ${maxSystemPromptLength} characters`,
      );
  }
  if (
    Object.hasOwn(settings, "reasoning_mode") &&
    !reasoningModes.includes(settings.reasoning_mode as ReasoningMode)
  )
    throw Error("reasoning_mode: unknown mode");
  for (const key of ["reasoning_level", "adaptive_min", "adaptive_max"]) {
    if (
      Object.hasOwn(settings, key) &&
      !reasoningLevels.includes(settings[key] as ReasoningLevel)
    )
      throw Error(`${key}: unknown level`);
  }
  if (
    Object.hasOwn(settings, "adaptive_preset") &&
    !adaptivePresets.includes(settings.adaptive_preset as AdaptivePreset)
  )
    throw Error("adaptive_preset: unknown preset");
  const adaptiveMin =
    (settings.adaptive_min as ReasoningLevel | undefined) ?? "low";
  const adaptiveMax =
    (settings.adaptive_max as ReasoningLevel | undefined) ?? "max";
  if (
    reasoningLevels.indexOf(adaptiveMin) > reasoningLevels.indexOf(adaptiveMax)
  )
    throw Error("adaptive_min: must be at most adaptive_max");
  // An empty override is only meaningful in a patch, where it clears the value.
  const versions: Partial<Record<IdentityVersionKey, string>> = {};
  for (const key of identityVersionKeys) {
    if (!Object.hasOwn(settings, key)) continue;
    const version = settings[key];
    if (typeof version !== "string" || !validIdentityVersion(key, version))
      throw Error(`${key}: invalid client version`);
    if (version !== "") versions[key] = version;
  }
  let stickiness: ChannelStickiness | undefined;
  if (settings.channel_stickiness !== undefined) {
    const value = object(settings.channel_stickiness, "channel_stickiness");
    keys(value, ["enabled", "ttl_seconds"], [], "channel_stickiness");
    if (
      typeof value.enabled !== "boolean" ||
      !Number.isInteger(value.ttl_seconds) ||
      (value.ttl_seconds as number) < 60 ||
      (value.ttl_seconds as number) > 86400
    )
      throw Error("invalid channel stickiness");
    stickiness = {
      enabled: value.enabled,
      ttl_seconds: value.ttl_seconds as number,
    };
  }
  const defaults =
    settings.default_recovery_paths === undefined
      ? undefined
      : object(settings.default_recovery_paths, "default_recovery_paths");
  if (defaults)
    for (const [protocol, id] of Object.entries(defaults)) {
      if (
        !/^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/.test(protocol) ||
        typeof id !== "string" ||
        !/^[a-z][a-z0-9_-]{2,95}$/.test(id)
      )
        throw Error("invalid default recovery path");
    }
  return {
    ...(Object.fromEntries(
      forwardingSwitchKeys.map((key) => [
        key,
        (settings[key] as boolean | undefined) ?? true,
      ]),
    ) as Record<(typeof forwardingSwitchKeys)[number], boolean>),
    ...versions,
    // Older cores omit these; filling the defaults keeps draft comparisons stable.
    system_prompt_enabled:
      (settings.system_prompt_enabled as boolean | undefined) ?? false,
    system_prompt_prefix:
      (settings.system_prompt_prefix as string | undefined) ?? "",
    system_prompt_suffix:
      (settings.system_prompt_suffix as string | undefined) ?? "",
    reasoning_mode:
      (settings.reasoning_mode as ReasoningMode | undefined) ?? "passthrough",
    reasoning_level:
      (settings.reasoning_level as ReasoningLevel | undefined) ?? "medium",
    adaptive_preset:
      (settings.adaptive_preset as AdaptivePreset | undefined) ?? "everyday",
    adaptive_min: adaptiveMin,
    adaptive_max: adaptiveMax,
    image_compression: imageCompression,
    model_redirects: redirects,
    model_routing_groups: modelRoutingGroups,
    ...(settings.builtin_tools !== undefined
      ? { builtin_tools: parseBuiltinTools(settings.builtin_tools) }
      : {}),
    ...(stickiness ? { channel_stickiness: stickiness } : {}),
    ...(defaults
      ? { default_recovery_paths: defaults as Record<string, string> }
      : {}),
    default_failure_policy: parseFailurePolicy(settings.default_failure_policy),
    allow_unmatched_failover: parsed.enabled,
    strategy: parsed.strategy,
    max_attempts: parsed.max_attempts,
  };
}
