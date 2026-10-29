import { currentLocale, i18n } from "./i18n";
import type {
  ClientType,
  RequestRecord,
  RequestSession,
  RequestStatus,
  SessionStatus,
} from "./request-record-model";
import type { UsageRange } from "./usage-range";

/** What another page opens the request records on. */
export interface RecordsTarget {
  tokenId?: string;
  serviceId?: string;
  models?: string[];
  /** The days to list; all of history when absent. */
  range?: UsageRange;
  /**
   * Only conversations with a call that ended this way within the range,
   * whatever the conversation's own outcome.
   */
  status?: RequestStatus;
}

export interface RecordFilters {
  // The monitor list is a session stream, so the status filter spans the
  // session scale and can name interrupted, which no record carries.
  status: SessionStatus | "";
  serviceId: string;
  protocol: string;
  // Rows without a detected client show as unknown, so they filter as unknown.
  clientType: ClientType | "";
  localAccessTokenIds: string[];
}

export interface LiveMergeResult {
  items: RequestRecord[];
  queued: RequestRecord[];
  added: number;
}

export function recordMatchesFilters(
  record: RequestRecord,
  filters: RecordFilters,
): boolean {
  return (
    (!filters.status || record.status === filters.status) &&
    (!filters.serviceId || record.service_id === filters.serviceId) &&
    (!filters.protocol || record.input_protocol === filters.protocol) &&
    (!filters.clientType ||
      (record.client_type ?? "unknown") === filters.clientType) &&
    (!filters.localAccessTokenIds?.length ||
      (record.local_access_token_id !== null &&
        filters.localAccessTokenIds.includes(record.local_access_token_id)))
  );
}

export function mergeLivePage(
  items: RequestRecord[],
  queued: RequestRecord[],
  incoming: RequestRecord[],
  queueNew: boolean,
): LiveMergeResult {
  const incomingById = new Map(incoming.map((record) => [record.id, record]));
  const known = new Set<string>();
  const updatedItems = items.map((record) => {
    known.add(record.id);
    return incomingById.get(record.id) ?? record;
  });
  const updatedQueue = queued.map((record) => {
    known.add(record.id);
    return incomingById.get(record.id) ?? record;
  });
  const additions = incoming.filter((record) => !known.has(record.id));
  if (queueNew) {
    return {
      items: updatedItems,
      queued: sortNewestFirst([...additions, ...updatedQueue]),
      added: additions.length,
    };
  }
  return {
    items: sortNewestFirst([...additions, ...updatedItems]),
    queued: updatedQueue,
    added: additions.length,
  };
}

export function applyQueuedRecords(
  items: RequestRecord[],
  queued: RequestRecord[],
): RequestRecord[] {
  return sortNewestFirst([...queued, ...items]);
}

export function sortNewestFirst(records: RequestRecord[]): RequestRecord[] {
  return [...records].sort((left, right) => {
    const timeDifference =
      Date.parse(right.started_at) - Date.parse(left.started_at);
    return timeDifference || right.id.localeCompare(left.id);
  });
}

export interface RecordDateGroup {
  key: string;
  label: string;
  records: RequestRecord[];
}

export function groupRecordsByDate(
  records: RequestRecord[],
  now = new Date(),
): RecordDateGroup[] {
  const today = localDateKey(now);
  const yesterdayDate = new Date(now);
  yesterdayDate.setDate(yesterdayDate.getDate() - 1);
  const yesterday = localDateKey(yesterdayDate);
  const groups = new Map<string, RequestRecord[]>();
  for (const record of records) {
    const parsed = new Date(record.started_at);
    const key = Number.isNaN(parsed.getTime())
      ? record.started_at.slice(0, 10)
      : localDateKey(parsed);
    const group = groups.get(key);
    if (group) group.push(record);
    else groups.set(key, [record]);
  }
  return [...groups.entries()].map(([key, grouped]) => ({
    key,
    label:
      key === today
        ? i18n.t("common.today")
        : key === yesterday
          ? i18n.t("common.yesterday")
          : formatDateLabel(grouped[0]?.started_at ?? key),
    records: grouped,
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
  return new Intl.DateTimeFormat(currentLocale(), {
    year: "numeric",
    month: "long",
    day: "numeric",
  }).format(date);
}

export function liveDurationMs(record: RequestRecord, nowMs: number): number {
  if (record.latency_ms !== null) return Math.max(0, record.latency_ms);
  const started = Date.parse(record.started_at);
  if (Number.isNaN(started)) return 0;
  const completed = record.completed_at
    ? Date.parse(record.completed_at)
    : nowMs;
  return Math.max(0, completed - started);
}

/**
 * Model time: the union of finished call runtimes plus, while calls run, the
 * time since the one merged running block started. Every running call shares
 * that block, so the value grows once per second however many run at once.
 */
export function sessionRuntimeMs(
  session: Pick<RequestSession, "duration_ms" | "running_since">,
  nowMs: number,
): number {
  if (session.running_since === null) return Math.max(0, session.duration_ms);
  const runningSince = Date.parse(session.running_since);
  const running = Number.isFinite(runningSince)
    ? Math.max(0, nowMs - runningSince)
    : 0;
  return Math.max(0, session.duration_ms) + running;
}

export function formatDuration(milliseconds: number): string {
  if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`;
  if (milliseconds < 60_000) {
    return `${(milliseconds / 1000).toFixed(1)} s`;
  }
  if (milliseconds < 3_600_000) {
    const minutes = Math.floor(milliseconds / 60_000);
    const seconds = Math.floor((milliseconds % 60_000) / 1000);
    return `${minutes}m ${String(seconds).padStart(2, "0")}s`;
  }
  const hours = Math.floor(milliseconds / 3_600_000);
  const minutes = Math.floor((milliseconds % 3_600_000) / 60_000);
  return `${hours}h ${String(minutes).padStart(2, "0")}m`;
}
