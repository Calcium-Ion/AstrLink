import { currentLocale, i18n } from "./i18n";
import type {
  ClientType,
  ContextItemText,
  MemoryEntry,
  MemoryEvent,
  MemoryProject,
  MemoryRules,
  MemoryWrite,
  MemoryWriteText,
  RequestContextTexts,
} from "./request-record-model";

/**
 * One memory the page opens in the memory tab, in the project and client
 * whose calls hold it. Project is absent for the calls that named none.
 */
export interface MemoryFocus {
  project?: string;
  clientType: ClientType;
  file: string;
}

/**
 * The memory file a write stands for: the one a new index line links, or a
 * memory file written itself. Null for rules files and for index lines that
 * link no file.
 */
export function memoryEntryFile(
  write: Pick<MemoryWrite, "kind" | "name" | "entry">,
): string | null {
  if (write.kind !== "memory") return null;
  if (write.entry) return write.entry;
  return write.name.toLocaleLowerCase() === "memory.md" ? null : write.name;
}

/** The project select's value for the calls that named no project. */
export const NO_PROJECT = ":none";

export function projectKey(project: Pick<MemoryProject, "project">): string {
  return project.project ?? NO_PROJECT;
}

/** A project as its select names it: the directory's last part. */
export function projectName(project: string | undefined): string {
  if (project === undefined) return i18n.t("memoryView.noProject");
  const parts = project.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? project;
}

/** When today began here, so the days of the read counts are the reader's. */
export function dayStart(now: Date): Date {
  const start = new Date(now);
  start.setHours(0, 0, 0, 0);
  return start;
}

function sameDay(left: Date, right: Date): boolean {
  return (
    left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
  );
}

/**
 * A day as the memory view names it: today, yesterday, or its date. Inline
 * days sit inside a sentence, so they read in its case.
 */
export function memoryDay(value: string, now: Date, inline = false): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const scope = inline ? "memoryView" : "common";
  if (sameDay(date, now)) return i18n.t(`${scope}.today`);
  const yesterday = new Date(now);
  yesterday.setDate(yesterday.getDate() - 1);
  if (sameDay(date, yesterday)) return i18n.t(`${scope}.yesterday`);
  return new Intl.DateTimeFormat(currentLocale(), {
    ...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }),
    month: "short",
    day: "numeric",
  }).format(date);
}

export function memoryClock(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(currentLocale(), {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(date);
}

/** A change's time: its day, then the clock. */
export function memoryWhen(value: string, now: Date): string {
  const clock = memoryClock(value);
  return clock ? `${memoryDay(value, now)} ${clock}` : memoryDay(value, now);
}

const DAY_MS = 24 * 60 * 60 * 1000;
const RECENT_DAYS = 7;

/**
 * One day of an entry's strip: how many turns read the file, and whether the
 * agent noted it that day. Null for the days before it was noted, which the
 * strip leaves blank.
 */
export type StripDay = { reads: number; noted: boolean } | null;

/**
 * An entry's days, the oldest first, as its strip draws them. An entry noted
 * before the first day, or before the gateway saw it, fills the strip.
 */
export function stripDays(
  entry: Pick<MemoryEntry, "reads" | "noted_at">,
  start: string,
): StripDay[] {
  const last = entry.reads.length - 1;
  let noted = -1;
  if (entry.noted_at) {
    const noteMs = new Date(entry.noted_at).getTime();
    const startMs = new Date(start).getTime();
    if (!Number.isNaN(noteMs) && !Number.isNaN(startMs)) {
      noted = last - Math.max(0, Math.ceil((startMs - noteMs) / DAY_MS));
    }
  }
  return entry.reads.map((reads, index) =>
    index < noted ? null : { reads, noted: index === noted },
  );
}

/** The entry was noted in the last seven days. */
export function notedRecently(
  entry: Pick<MemoryEntry, "noted_at">,
  now: Date,
): boolean {
  if (!entry.noted_at) return false;
  const at = new Date(entry.noted_at).getTime();
  return now.getTime() - at < RECENT_DAYS * DAY_MS;
}

/** The entries whose title, line or file holds the query, any case. */
export function searchEntries(
  entries: readonly MemoryEntry[],
  query: string,
): MemoryEntry[] {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return [...entries];
  return entries.filter((entry) =>
    [entry.title, entry.hook, entry.file].some((text) =>
      text?.toLocaleLowerCase().includes(needle),
    ),
  );
}

/**
 * When a rules file last changed: the time today, else the day; a file that
 * never changed says since when it has not. `fresh` marks a change today.
 */
export function rulesNote(
  rules: Pick<MemoryRules, "changed_at" | "first_seen_at">,
  now: Date,
): { text: string; fresh: boolean } {
  const t = i18n.t.bind(i18n);
  if (rules.changed_at) {
    const today = sameDay(new Date(rules.changed_at), now);
    const day = memoryDay(rules.changed_at, now, true);
    return today
      ? {
          text: t("memoryView.changedAt", {
            when: `${day} ${memoryClock(rules.changed_at)}`,
          }),
          fresh: true,
        }
      : { text: t("memoryView.changedOn", { when: day }), fresh: false };
  }
  if (rules.first_seen_at) {
    return {
      text: t("memoryView.steadySince", {
        when: memoryDay(rules.first_seen_at, now, true),
      }),
      fresh: false,
    };
  }
  return { text: "", fresh: false };
}

/** How many uses the toolbox draws as bars before it sums up the rest. */
export const TOOLBOX_BARS = 3;

/**
 * One group of the toolbox: the most used drawn as bars, how many more were
 * used, and the names carried but not used.
 */
export function toolboxGroup<Item extends { name: string; uses: number }>(
  items: readonly Item[],
): { bars: Item[]; moreUsed: number; idle: Item[] } {
  const used = items
    .filter((item) => item.uses > 0)
    .sort((left, right) => right.uses - left.uses);
  return {
    bars: used.slice(0, TOOLBOX_BARS),
    moreUsed: Math.max(0, used.length - TOOLBOX_BARS),
    idle: items.filter((item) => item.uses === 0),
  };
}

/**
 * The calls whose captured bodies may hold a memory's text, in the order to
 * try them: the newest reads, which show the whole file, then the note.
 */
export function bodySources(events: readonly MemoryEvent[]): MemoryEvent[] {
  const captured = events.filter((event) => event.captured);
  return [
    ...captured.filter((event) => event.kind === "read").reverse(),
    ...captured.filter((event) => event.kind === "noted"),
  ];
}

/**
 * A memory file's text in what a call carried: the file a tool read, else
 * the file or lines a tool wrote. The front matter is left out.
 */
export function memoryBody(
  texts: RequestContextTexts,
  file: string,
): string | null {
  const name = file.toLocaleLowerCase();
  const read = texts.reads.find(
    (entry) => entry.name.toLocaleLowerCase() === name,
  );
  const write = read
    ? null
    : texts.writes.find(
        (entry) =>
          entry.kind === "memory" && entry.name.toLocaleLowerCase() === name,
      );
  const content = read?.content ?? write?.content;
  if (content === undefined) return null;
  return content.replace(/^---\r?\n[\s\S]*?\r?\n---[ \t]*(\r?\n|$)/, "").trim();
}

/**
 * The write a memory line stands for among what its call's history wrote:
 * the memory file a new index line points to, else the index line itself.
 */
export function writeText(
  texts: RequestContextTexts,
  write: Pick<MemoryWrite, "kind" | "name" | "path" | "entry">,
): MemoryWriteText | null {
  const writes = texts.writes.filter((entry) => entry.kind === write.kind);
  if (write.entry) {
    const file = writes.find((entry) => entry.name === write.entry);
    if (file) return file;
  }
  return (
    writes.find(
      (entry) =>
        entry.name === write.name &&
        (write.path === undefined || entry.path === write.path),
    ) ??
    writes.find((entry) => entry.name === write.name) ??
    null
  );
}

/**
 * The text a call carried of one file, matched as the records name it: by
 * its path, or by its name alone when the call carried only one so named.
 */
export function itemText(
  texts: RequestContextTexts,
  item: { kind: ContextItemText["kind"]; name?: string; path?: string },
): ContextItemText | null {
  const named = texts.items.filter(
    (entry) => entry.kind === item.kind && entry.name === item.name,
  );
  if (item.path !== undefined) {
    return named.find((entry) => entry.path === item.path) ?? null;
  }
  return named.length === 1 ? named[0]! : null;
}
