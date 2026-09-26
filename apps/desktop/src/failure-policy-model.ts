import { parseBuiltinTools, type BuiltinTools } from "./builtin-tools-model";
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
] as const;
export type IdentityLearningKey = (typeof identityLearningKeys)[number];

export const identityVersionKeys = [
  "codex_identity_version",
  "claude_identity_version",
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

export interface ModelRedirect {
  from: string;
  to: string;
  enabled: boolean;
}

export const maxModelRedirects = 200;
export const maxRedirectModelLength = 256;
export const astrlinkAutoModelId = "astrlink/auto";

/** Built-in redirects are always shown; missing entries default to disabled. */
export interface BuiltinModelRedirect {
  /** Key under `modelRedirect.builtin` in the locales. */
  id: "codexAutoReview";
  from: string;
  /** Default target; only forwarded after the user enables and saves the rule. */
  defaultTo: string;
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
  /** Minimum declared client version; absent when no override is set. */
  codex_identity_version?: string;
  claude_identity_version?: string;
  model_redirects?: ModelRedirect[];
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

function parseModelRedirects(value: unknown): ModelRedirect[] {
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

export function parseRoutingSettings(value: unknown): RoutingSettings {
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
      "builtin_tools",
      ...forwardingSwitchKeys,
      ...identityVersionKeys,
    ],
    "routing_settings",
  );
  const redirects = parseModelRedirects(settings.model_redirects);
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
    model_redirects: redirects,
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
