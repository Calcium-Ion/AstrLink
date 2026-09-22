import { i18n } from "./i18n";
import type { RequestSession, RequestStatus } from "./request-record-model";
import type { RecordFilters } from "./request-live-model";

export function sessionMatchesFilters(
  session: RequestSession,
  filters: RecordFilters,
): boolean {
  return (
    (!filters.status || session.status === filters.status) &&
    (!filters.serviceId || session.service_id === filters.serviceId) &&
    (!filters.protocol || session.input_protocol === filters.protocol)
  );
}

export interface SessionMergeResult {
  items: RequestSession[];
  queued: RequestSession[];
  added: number;
}

/**
 * A poll of an idle gateway returns the same bytes it returned a second ago.
 * Handing those back as fresh objects re-rendered the monitor list, the open
 * session detail and every trajectory row behind it once a second, so a merge
 * that changed nothing has to be indistinguishable from no merge at all.
 */
function sameSession(left: RequestSession, right: RequestSession): boolean {
  return (
    left.title === right.title &&
    left.started_at === right.started_at &&
    left.last_started_at === right.last_started_at &&
    left.completed_at === right.completed_at &&
    left.duration_ms === right.duration_ms &&
    left.tool_duration_ms === right.tool_duration_ms &&
    left.average_ttft_ms === right.average_ttft_ms &&
    left.output_tokens_per_second === right.output_tokens_per_second &&
    left.active_request_starts.length === right.active_request_starts.length &&
    left.active_request_starts.every((started, index) => started === right.active_request_starts[index]) &&
    left.turn_count === right.turn_count &&
    left.call_count === right.call_count &&
    left.status === right.status &&
    left.requested_model === right.requested_model &&
    (left.reasoning_effort ?? null) === (right.reasoning_effort ?? null) &&
    left.input_protocol === right.input_protocol &&
    left.service_id === right.service_id &&
    left.local_access_token_id === right.local_access_token_id
  );
}

export function mergeLiveSessions(
  items: RequestSession[],
  queued: RequestSession[],
  incoming: RequestSession[],
  queueNew: boolean,
): SessionMergeResult {
  const incomingById = new Map(incoming.map((session) => [session.id, session]));
  const known = new Set<string>();
  let moved = false;
  const adopt = (session: RequestSession): RequestSession => {
    known.add(session.id);
    const next = incomingById.get(session.id);
    if (!next || sameSession(next, session)) return session;
    moved = true;
    return next;
  };
  const updatedItems = items.map(adopt);
  const updatedQueue = queued.map(adopt);
  const additions = incoming.filter((session) => !known.has(session.id));
  if (!moved && additions.length === 0) {
    return { items, queued, added: 0 };
  }
  if (queueNew) {
    return {
      items: updatedItems,
      queued: sortSessionsNewestFirst([...additions, ...updatedQueue]),
      added: additions.length,
    };
  }
  return {
    items: sortSessionsNewestFirst([...additions, ...updatedItems]),
    queued: updatedQueue,
    added: additions.length,
  };
}

export function applyQueuedSessions(
  items: RequestSession[],
  queued: RequestSession[],
): RequestSession[] {
  return sortSessionsNewestFirst([...queued, ...items]);
}

export function sortSessionsNewestFirst(
  sessions: RequestSession[],
): RequestSession[] {
  return [...sessions].sort((left, right) => {
    const timeDifference =
      Date.parse(right.last_started_at) - Date.parse(left.last_started_at);
    return timeDifference || right.id.localeCompare(left.id);
  });
}

export interface SessionDateGroup {
  key: string;
  label: string;
  sessions: RequestSession[];
}

export function groupSessionsByDate(
  sessions: RequestSession[],
  now = new Date(),
): SessionDateGroup[] {
  const today = localDateKey(now);
  const yesterdayDate = new Date(now);
  yesterdayDate.setDate(yesterdayDate.getDate() - 1);
  const yesterday = localDateKey(yesterdayDate);
  const groups = new Map<string, RequestSession[]>();
  for (const session of sessions) {
    const parsed = new Date(session.last_started_at);
    const key = Number.isNaN(parsed.getTime())
      ? session.last_started_at.slice(0, 10)
      : localDateKey(parsed);
    const group = groups.get(key);
    if (group) group.push(session);
    else groups.set(key, [session]);
  }
  return [...groups.entries()].map(([key, grouped]) => ({
    key,
    label:
      key === today
        ? i18n.t("common.today")
        : key === yesterday
          ? i18n.t("common.yesterday")
          : formatDateLabel(grouped[0]?.last_started_at ?? key),
    sessions: grouped,
  }));
}

function localDateKey(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function formatDateLabel(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(i18n.language === "zh-CN" ? "zh-CN" : "en", {
    year: "numeric",
    month: "long",
    day: "numeric",
  }).format(date);
}

export function sessionStatusLabel(status: RequestStatus): string {
  return i18n.t(`status.${status}`);
}
