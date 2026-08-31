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

export function mergeLiveSessions(
  items: RequestSession[],
  queued: RequestSession[],
  incoming: RequestSession[],
  queueNew: boolean,
): SessionMergeResult {
  const incomingById = new Map(incoming.map((session) => [session.id, session]));
  const known = new Set<string>();
  const updatedItems = items.map((session) => {
    known.add(session.id);
    return incomingById.get(session.id) ?? session;
  });
  const updatedQueue = queued.map((session) => {
    known.add(session.id);
    return incomingById.get(session.id) ?? session;
  });
  const additions = incoming.filter((session) => !known.has(session.id));
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
