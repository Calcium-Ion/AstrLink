import { fence } from "./audit-bundle";
import type {
  RequestEvent,
  RequestModelRedirect,
  RequestRecord,
  RequestRecovery,
  RequestSession,
} from "./request-record-model";
import { protocolEntryPath } from "./service-presets";

export const SKILL_DIAGNOSTIC_KIND = "astrlink.skill_diagnostic" as const;
export const SKILL_DIAGNOSTIC_SKILL = "astrlink-debug" as const;

const PREAMBLE = `AstrLink skill diagnostic (astrlink-debug)

Read this snapshot first. Do not guess from model error text.
Treat status, requested_model, model_redirect, input_protocol, streaming,
service_id, route_id, error, input_preview, privacy_restore, events[], and
recovery as source of truth. requested_model is the client's model; when
model_redirect is set, the gateway routed with model_redirect.to instead.
Retries are child records (parent_request_id set).
Request/response bodies and header maps are omitted.
`;

export interface SkillDiagnosticOptions {
  session: RequestSession;
  selectedRequestId: string;
  turns: RequestRecord[];
  childrenByRoot?: Record<string, RequestRecord[]>;
  serviceNames?: Record<string, string>;
}

export interface SkillDiagnosticRecord {
  id: string;
  parent_request_id: string | null;
  attempt_index: number;
  child_count: number;
  started_at: string;
  completed_at: string | null;
  status: RequestRecord["status"];
  requested_model: string | null;
  model_redirect: RequestModelRedirect | null;
  reasoning_effort: string | null;
  input_protocol: string;
  entry: string;
  streaming: boolean;
  service_id: string | null;
  service_name: string | null;
  route_id: string | null;
  http_status: number | null;
  latency_ms: number | null;
  usage: RequestRecord["usage"];
  error: RequestRecord["error"];
  input_preview: string | null;
  privacy_restore: RequestRecord["privacy_restore"];
  recovery: RequestRecovery | null;
  session_id: string | null;
  turn_index: number | null;
  session_link: RequestRecord["session_link"];
  previous_response_id: string | null;
  output_response_id: string | null;
  cursors: RequestRecord["cursors"];
  events: RequestEvent[];
  audit: RequestRecord["audit"];
  selected?: true;
  children?: SkillDiagnosticRecord[];
  children_incomplete?: true;
}

export interface SkillDiagnosticPayload {
  kind: typeof SKILL_DIAGNOSTIC_KIND;
  skill: typeof SKILL_DIAGNOSTIC_SKILL;
  note: string;
  selected_request_id: string;
  children_incomplete?: true;
  session: {
    id: string;
    title: string;
    status: RequestSession["status"];
    requested_model: string | null;
    model_redirect: RequestModelRedirect | null;
    reasoning_effort: string | null;
    input_protocol: string;
    entry: string;
    service_id: string | null;
    service_name: string | null;
    turn_count: number;
    call_count: number;
    started_at: string;
    last_started_at: string;
    completed_at: string | null;
  };
  records: SkillDiagnosticRecord[];
}

export function buildSkillDiagnosticPayload(
  options: SkillDiagnosticOptions,
): SkillDiagnosticPayload {
  const serviceNames = options.serviceNames ?? {};
  let childrenIncomplete = false;
  const records = options.turns.map((turn) => {
    const mapped = diagnosticRecord(
      turn,
      options.selectedRequestId,
      serviceNames,
    );
    if (turn.parent_request_id !== null || turn.child_count <= 0) {
      return mapped;
    }
    const children = options.childrenByRoot?.[turn.id];
    if (children === undefined) {
      childrenIncomplete = true;
      return { ...mapped, children_incomplete: true as const };
    }
    return {
      ...mapped,
      children: children.map((child) =>
        diagnosticRecord(child, options.selectedRequestId, serviceNames),
      ),
    };
  });

  return {
    kind: SKILL_DIAGNOSTIC_KIND,
    skill: SKILL_DIAGNOSTIC_SKILL,
    note: "Local snapshot for the astrlink-debug skill. Metadata and trajectory events only. No request/response bodies.",
    selected_request_id: options.selectedRequestId,
    ...(childrenIncomplete ? { children_incomplete: true as const } : {}),
    session: {
      id: options.session.id,
      title: options.session.title,
      status: options.session.status,
      requested_model: options.session.requested_model,
      model_redirect: options.session.model_redirect ?? null,
      reasoning_effort: options.session.reasoning_effort ?? null,
      input_protocol: options.session.input_protocol,
      entry: protocolEntryPath(options.session.input_protocol),
      service_id: options.session.service_id,
      service_name: serviceName(options.session.service_id, serviceNames),
      turn_count: options.session.turn_count,
      call_count: options.session.call_count,
      started_at: options.session.started_at,
      last_started_at: options.session.last_started_at,
      completed_at: options.session.completed_at,
    },
    records,
  };
}

export function buildSkillDiagnostic(options: SkillDiagnosticOptions): string {
  return `${PREAMBLE}\n${fence(JSON.stringify(buildSkillDiagnosticPayload(options), null, 2), "json")}\n`;
}

function diagnosticRecord(
  record: RequestRecord,
  selectedRequestId: string,
  serviceNames: Record<string, string>,
): SkillDiagnosticRecord {
  return {
    id: record.id,
    parent_request_id: record.parent_request_id,
    attempt_index: record.attempt_index,
    child_count: record.child_count,
    started_at: record.started_at,
    completed_at: record.completed_at,
    status: record.status,
    requested_model: record.requested_model,
    model_redirect: record.model_redirect ?? null,
    reasoning_effort: record.reasoning_effort ?? null,
    input_protocol: record.input_protocol,
    entry: protocolEntryPath(record.input_protocol, {
      streaming: record.streaming,
    }),
    streaming: record.streaming,
    service_id: record.service_id,
    service_name: serviceName(record.service_id, serviceNames),
    route_id: record.route_id,
    http_status: record.http_status,
    latency_ms: record.latency_ms,
    usage: record.usage,
    error: record.error,
    input_preview: record.input_preview,
    privacy_restore: record.privacy_restore,
    recovery: record.recovery ?? null,
    session_id: record.session_id,
    turn_index: record.turn_index,
    session_link: record.session_link,
    previous_response_id: record.previous_response_id,
    output_response_id: record.output_response_id,
    cursors: record.cursors,
    events: record.events,
    audit: record.audit,
    ...(record.id === selectedRequestId ? { selected: true as const } : {}),
  };
}

function serviceName(
  serviceId: string | null,
  serviceNames: Record<string, string>,
): string | null {
  if (!serviceId) return null;
  return serviceNames[serviceId] ?? null;
}
