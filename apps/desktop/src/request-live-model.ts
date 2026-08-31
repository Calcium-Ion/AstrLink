import { i18n } from "./i18n";
import type {
  RequestRecord,
  RequestSession,
  RequestStatus,
} from "./request-record-model";

export interface RecordFilters {
  status: RequestStatus | "";
  serviceId: string;
  protocol: string;
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
    (!filters.protocol || record.input_protocol === filters.protocol)
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
  return new Intl.DateTimeFormat(i18n.language === "zh-CN" ? "zh-CN" : "en", {
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

export function sessionElapsedMs(
  session: Pick<RequestSession, "started_at" | "completed_at">,
  nowMs: number,
): number {
  const start = Date.parse(session.started_at);
  if (Number.isNaN(start)) return 0;
  if (session.completed_at) {
    const end = Date.parse(session.completed_at);
    if (!Number.isNaN(end)) return Math.max(0, end - start);
  }
  return Math.max(0, nowMs - start);
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
