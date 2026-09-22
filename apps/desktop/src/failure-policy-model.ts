export type FailureAction =
  "stop" | "retry" | "failover" | "retry_and_failover";
export type FailoverStrategy = "retry_first" | "failover_first" | "failover_only";

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

export interface ChannelStickiness { enabled: boolean; ttl_seconds: number; }

export interface RoutingSettings {
  channel_stickiness?: ChannelStickiness;
 default_recovery_paths?: Record<string,string>;
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
    ["response_start_timeout_seconds", "thinking_signature_recovery", "openai_reasoning_recovery", "openai_function_output_recovery"],
    path,
  );
  const initial = integer(
    policy.initial_delay_ms,
    0,
    60000,
    `${path}.initial_delay_ms`,
  );
  for (const key of ["thinking_signature_recovery", "openai_reasoning_recovery", "openai_function_output_recovery"]) {
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
      ? { thinking_signature_recovery: policy.thinking_signature_recovery as boolean }
      : {}),
    ...(Object.hasOwn(policy, "openai_reasoning_recovery")
      ? { openai_reasoning_recovery: policy.openai_reasoning_recovery as boolean }
      : {}),
    ...(Object.hasOwn(policy, "openai_function_output_recovery")
      ? { openai_function_output_recovery: policy.openai_function_output_recovery as boolean }
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
    (policy.strategy !== "retry_first" && policy.strategy !== "failover_first" && policy.strategy !== "failover_only")
  )
    throw new Error(`${path}: invalid switch or strategy`);
  return {
    enabled: policy.enabled,
    strategy: policy.strategy,
    max_attempts: integer(policy.max_attempts, 1, 20, `${path}.max_attempts`),
  };
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
    ["default_recovery_paths", "channel_stickiness"],
    "routing_settings",
  );
  const parsed = parseFailoverPolicy({
    enabled: settings.allow_unmatched_failover,
    strategy: settings.strategy,
    max_attempts: settings.max_attempts,
  });
  let stickiness: ChannelStickiness | undefined;
  if (settings.channel_stickiness !== undefined) {
    const value = object(settings.channel_stickiness, "channel_stickiness");
    keys(value, ["enabled", "ttl_seconds"], [], "channel_stickiness");
    if (typeof value.enabled !== "boolean" || !Number.isInteger(value.ttl_seconds) || (value.ttl_seconds as number) < 60 || (value.ttl_seconds as number) > 86400) throw Error("invalid channel stickiness");
    stickiness = { enabled: value.enabled, ttl_seconds: value.ttl_seconds as number };
  }
  const defaults = settings.default_recovery_paths===undefined?undefined:object(settings.default_recovery_paths,"default_recovery_paths");
 if(defaults)for(const [protocol,id] of Object.entries(defaults)){if(!/^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/.test(protocol)||typeof id!=="string"||!/^[a-z][a-z0-9_-]{2,95}$/.test(id))throw Error("invalid default recovery path")}
 return {
 ...(stickiness ? { channel_stickiness: stickiness } : {}),
 ...(defaults ? {default_recovery_paths:defaults as Record<string,string>}:{}),
    default_failure_policy: parseFailurePolicy(settings.default_failure_policy),
    allow_unmatched_failover: parsed.enabled,
    strategy: parsed.strategy,
    max_attempts: parsed.max_attempts,
  };
}
