import { i18n } from "./i18n";
import { webText } from "./web-copy";

export const CONSOLE_SIGNED_OUT = "astrlink:console-signed-out";
export const RAW_CHANGED = "astrlink:web-raw-changed";
type Args = Record<string, unknown>;
type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export class ConsoleError extends Error {
  constructor(
    public status: number,
    public code: string,
    public retryAfter: number,
  ) {
    super(
      code === "oauth_loopback_callback_unavailable"
        ? webText("loopback")
        : code,
    );
  }
}

/** Never retry mutations, especially password proofs. Cookies stay same-origin. */
export async function consoleRequest(
  path: string,
  method: Method = "GET",
  body?: unknown,
  etag?: string,
) {
  if (!/^\/(?:control|console)\/v1\//.test(path))
    throw new Error("Invalid console path");
  const headers: Record<string, string> = {};
  if (method !== "GET") headers["X-AstrLink-Console"] = "1";
  if (body !== undefined)
    headers["Content-Type"] =
      method === "PATCH" ? "application/merge-patch+json" : "application/json";
  if (etag !== undefined) {
    if (!/^"[^"\r\n]+"$/.test(etag)) throw new Error("Invalid ETag");
    headers["If-Match"] = etag;
  }
  const options: RequestInit = {
    method,
    credentials: "same-origin",
    headers,
    redirect: "error",
    cache: "no-store",
  };
  if (method !== "GET" && body !== undefined)
    options.body = JSON.stringify(body);
  const response = await fetch(path, options);
  if (response.status === 401)
    window.dispatchEvent(new Event(CONSOLE_SIGNED_OUT));
  const value = response.status === 204 ? null : await response.json();
  if (!response.ok) {
    const error = value?.error;
    const retry =
      Number(response.headers.get("Retry-After")) ||
      error?.details?.find((v: Args) => v.retry_after_seconds)
        ?.retry_after_seconds ||
      1;
    throw new ConsoleError(
      response.status,
      error?.code ?? `HTTP ${response.status}`,
      Number(retry),
    );
  }
  return { value, etag: response.headers.get("ETag") };
}

function query(values: Args = {}): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === null) continue;
    for (const item of Array.isArray(value) ? value : [value]) {
      params.append(
        key === "local_access_token_ids" ? "local_access_token_id" : key,
        String(item),
      );
    }
  }
  const result = params.toString();
  return result ? `?${result}` : "";
}
function id(args: Args, key: string): string {
  const value = args[key];
  if (typeof value !== "string" || !/^[a-z][a-z0-9_-]{2,95}$/.test(value))
    throw new Error("Invalid resource ID");
  return value;
}
const service = (a: Args) => `/services/${id(a, "serviceId")}`;
const request = (a: Args) => `/requests/${id(a, "requestId")}`;
const session = (a: Args) => `/request-sessions/${id(a, "sessionId")}`;
const model = (a: Args) => `/privacy-models/${id(a, "installationId")}`;
const policy = "/policies/policy_privacy_default";
type Command = (args: Args) => Promise<unknown>;
function route(
  path: string | ((a: Args) => string),
  method: Method = "GET",
  body?: (a: Args) => unknown,
  wrap?: "service" | "policy" | "order",
): Command {
  return async (a) => {
    const result = await consoleRequest(
      `/control/v1${typeof path === "string" ? path : path(a)}`,
      method,
      body?.(a),
      a.etag as string | undefined,
    );
    if (!wrap) return result.value;
    if (!result.etag) throw new Error("Missing ETag");
    return wrap === "order"
      ? { ...result.value, etag: result.etag }
      : { [wrap]: result.value, etag: result.etag };
  };
}
const input = (a: Args) => a.input;
const patch = (a: Args) => a.patch;
const proof = (a: Args) =>
  a.proof ? { password: (a.proof as Args).password } : undefined;
async function raw(path: string, body: unknown, decided = false) {
  try {
    const { value } = await consoleRequest(
      `/control/v1/audit/${path}`,
      "POST",
      body,
    );
    window.dispatchEvent(new Event(RAW_CHANGED));
    return decided
      ? { outcome: "decided", grant: value }
      : { outcome: "sealing", status: value };
  } catch (error) {
    if (!(error instanceof ConsoleError)) throw error;
    if (error.status === 403 && error.code === "raw_password_invalid")
      return { outcome: "password_invalid" };
    if (error.status === 429 && error.code === "raw_password_backoff")
      return { outcome: "backoff", retry_after_seconds: error.retryAfter };
    if (
      decided &&
      ((error.status === 409 && error.code === "raw_access_not_pending") ||
        (error.status === 404 && error.code === "not_found"))
    )
      return { outcome: "not_pending" };
    if (decided && error.status === 422 && error.code === "raw_proof_required")
      return { outcome: "proof_required" };
    throw error;
  }
}
export function openWebURL(value: unknown) {
  if (typeof value !== "string") throw new Error("Invalid URL");
  const url = new URL(value);
  if (!["http:", "https:"].includes(url.protocol))
    throw new Error("Invalid URL");
  window.open(url.href, "_blank", "noopener,noreferrer");
}

/** Mirrors sidecar.rs, raw_access.rs and the host's response envelopes. */
const commands: Record<string, Command> = {
  core_status: async () => {
    const [health, version, capabilities] = await Promise.all([
      consoleRequest("/control/v1/health"),
      consoleRequest("/control/v1/version"),
      consoleRequest("/control/v1/capabilities"),
    ]);
    return {
      app_version: version.value.core_version,
      phase: "ready",
      pid: null,
      ready: {
        event: "ready",
        core_version: version.value.core_version,
        control_api_version: version.value.control_api_version,
        protocol_contract_version: version.value.protocol_contract_version,
        inference_url: window.location.origin,
        client_inference_url: window.location.origin,
        control_url: window.location.origin,
      },
      health: health.value,
      version: version.value,
      capabilities: capabilities.value,
      last_error: null,
      inference_port_fallback: null,
      inference_listen_active: null,
      recovery_attempt: 0,
      recovery_scheduled_in_ms: null,
    };
  },
  list_services: async () => {
    const items: unknown[] = [],
      seen = new Set<string>();
    let cursor = "";
    do {
      const { value } = await consoleRequest(
        `/control/v1/services${query({ limit: 200, cursor })}`,
      );
      if (!Array.isArray(value.items)) throw new Error("Invalid service page");
      items.push(...value.items);
      cursor = value.next_cursor ?? "";
      if (cursor && seen.has(cursor))
        throw new Error("Service pagination did not advance");
      seen.add(cursor);
    } while (cursor);
    return { items, next_cursor: null };
  },
  get_service_order: route("/service-order", "GET", undefined, "order"),
  update_service_order: route(
    "/service-order",
    "PUT",
    (a) => ({ service_ids: a.serviceIds }),
    "order",
  ),
  get_service: route(service, "GET", undefined, "service"),
  create_service: route("/services", "POST", input, "service"),
  update_service: route(service, "PATCH", patch, "service"),
  delete_service: route(service, "DELETE"),
  get_service_usage: route(
    (a) => `${service(a)}/usage${a.fresh ? "?refresh=1" : ""}`,
  ),
  get_service_reset_credits: route((a) => `${service(a)}/usage/reset-credits`),
  reset_service_usage: route((a) => `${service(a)}/usage/reset`, "POST"),
  test_service: route((a) => `${service(a)}/test`, "POST", input),
  probe_service_models: route(
    (a) => `${service(a)}/probe-models`,
    "POST",
    input,
  ),
  probe_draft_service_models: route("/service-model-probes", "POST", input),
  probe_service_proxy: route("/service-proxy-probes", "POST", input),
  begin_service_authorization: async (a) => {
    const value = (await route(
      (a) => `${service(a)}/authorization`,
      "POST",
      (a) => ({ flow: a.flow }),
    )(a)) as Args;
    const url =
      (value.device_code as Args | undefined)?.verification_url ??
      value.authorization_url;
    if (url) openWebURL(url);
    return { kind: "session", session: value };
  },
  get_service_authorization: route((a) => `${service(a)}/authorization`),
  complete_service_authorization: route(
    (a) => `${service(a)}/authorization`,
    "PUT",
    (a) => ({ session_id: id(a, "sessionId"), code: a.code }),
  ),
  cancel_service_authorization: route(
    (a) => `${service(a)}/authorization`,
    "DELETE",
  ),
  logout_service: route(
    (a) => `${service(a)}/logout`,
    "POST",
    undefined,
    "service",
  ),
  clear_service_risk: route(
    (a) => `${service(a)}/risk/clear`,
    "POST",
    undefined,
    "service",
  ),
  list_service_risk_events: route(
    (a) => `${service(a)}/risk-events${query({ limit: a.limit })}`,
  ),
  list_request_records: route((a) => `/requests${query(a.query as Args)}`),
  list_request_sessions: route(
    (a) => `/request-sessions${query(a.query as Args)}`,
  ),
  get_request_session: route(session),
  get_session_channel_bindings: route(
    (a) => `${session(a)}/channel-bindings${query({ before: a.before })}`,
  ),
  release_session_channel_bindings: route(
    (a) => `${session(a)}/channel-bindings`,
    "DELETE",
  ),
  get_request_record: route(request),
  list_request_record_children: route((a) => `${request(a)}/children`),
  delete_request_record: route(request, "DELETE"),
  purge_request_records: route("/requests/purge", "POST", input),
  get_request_audit_content: route((a) => `${request(a)}/audit`),
  list_raw_access: route("/audit/raw-access"),
  decide_raw_access: (a) =>
    raw(
      `raw-access/${id(a, "grantId")}/decision`,
      {
        decision: a.decision,
        proof: a.decision === "deny" ? undefined : proof(a),
      },
      true,
    ),
  revoke_raw_grant: route(
    (a) => `/audit/raw-access/${id(a, "grantId")}`,
    "DELETE",
  ),
  raw_sealing_status: route("/audit/raw-sealing"),
  unlock_raw: (a) => raw("raw-unlock", { proof: proof(a) }),
  acknowledge_raw_key: (a) => raw("raw-verify", { proof: proof(a) }),
  lock_raw: async (a) => {
    const result = await route("/audit/raw-lock", "POST")(a);
    window.dispatchEvent(new Event(RAW_CHANGED));
    return result;
  },
  set_raw_password: (a) => {
    if (a.action !== "change") throw new Error(webText("forgot"));
    return raw("raw-password", {
      action: a.action,
      password: a.password,
      proof: proof(a),
    });
  },
  get_audit_settings: route("/audit-settings"),
  update_audit_settings: route("/audit-settings", "PATCH", patch),
  local_data_status: route("/local-data"),
  list_access_tokens: route("/access-tokens"),
  list_network_addresses: route("/network-addresses"),
  list_access_token_usage: route(
    (a) => `/access-token-usage${query({ today_from: a.todayFrom })}`,
  ),
  get_usage_summary: route(
    (a) =>
      `/usage-summary${query({ from: a.from, to: a.to, time_zone: a.timeZone, bucket: a.bucket })}`,
  ),
  create_access_token: route("/access-tokens", "POST", (a) => ({
    name: a.name,
  })),
  copy_access_token: async (a) => {
    const { value } = await consoleRequest(
      `/control/v1/access-tokens/${id(a, "tokenId")}/secret`,
    );
    try {
      await navigator.clipboard.writeText(value.access_token);
      return true;
    } catch {
      return false;
    }
  },
  delete_access_token: route(
    (a) => `/access-tokens/${id(a, "tokenId")}`,
    "DELETE",
  ),
  list_privacy_policies: route("/policies"),
  get_privacy_policy: route(policy, "GET", undefined, "policy"),
  update_privacy_policy: route(policy, "PATCH", patch, "policy"),
  dry_run_privacy_policy: route(`${policy}/dry-run`, "POST", input),
  get_privacy_regex_builtin_rules: route("/privacy/regex-builtin-rules"),
  get_privacy_model_catalog: route("/privacy-model-catalog"),
  get_privacy_model_releases: route(
    (a) => `/privacy-model-catalog/releases${a.refresh ? "?refresh=1" : ""}`,
  ),
  probe_privacy_model: route("/privacy-models/probe", "POST", input),
  probe_local_privacy_model: route(
    "/privacy-models/local/probe",
    "POST",
    input,
  ),
  list_privacy_model_installations: route("/privacy-models"),
  install_privacy_model: route("/privacy-models", "POST", input),
  get_privacy_model_installation: route(model),
  pause_privacy_model_installation: route((a) => `${model(a)}/pause`, "POST"),
  resume_privacy_model_installation: route((a) => `${model(a)}/resume`, "POST"),
  delete_privacy_model_installation: route(model, "DELETE"),
  get_routing_settings: route("/routing-settings"),
  update_routing_settings: route("/routing-settings", "PATCH", patch),
  get_client_identities: route("/client-identities"),
  open_external_url: async (a) => openWebURL(a.url),
  open_authorization_url: async (a) => openWebURL(a.url),
  pricing: async (a) => {
    const base = "/pricing";
    switch (a.operation) {
      case "status":
      case "catalog":
        return route(`${base}/${a.operation}`)(a);
      case "sync":
        return route(`${base}/sync`, "POST")(a);
      case "summary":
        return route(`${base}/summary${query(a.input as Args)}`)(a);
      case "report":
        return route(`${base}/services/${id(a, "serviceId")}`)(a);
      case "configure":
        return route(`${base}/services/${id(a, "serviceId")}`, "PUT", input)(a);
      case "backfill":
        return route(
          `${base}/services/${id(a, "serviceId")}/backfill`,
          "POST",
        )(a);
      default:
        throw new Error("Unsupported pricing operation");
    }
  },
  builtin_tool_action: (a) => {
    if (a.kind !== "web_search" && a.kind !== "image_generation")
      throw new Error("Invalid builtin tool");
    const methods: Record<string, Method> = {
      status: "GET",
      save_key: "PUT",
      delete_key: "DELETE",
      test: "POST",
    };
    const method = methods[String(a.action)];
    if (!method) throw new Error("Invalid builtin tool action");
    return route(
      `/builtin-tools/${a.kind}/${a.action === "test" ? "test" : "credential"}`,
      method,
      input,
    )(a);
  },
};
export async function webInvoke<T>(
  command: string,
  args: Args = {},
): Promise<T> {
  if (!Object.hasOwn(commands, command))
    throw new Error(i18n.t("bridge.desktopOnly"));
  return (await commands[command](args)) as T;
}
