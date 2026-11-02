import { beforeEach, describe, expect, it } from "vitest";

import { applyLocale } from "./i18n";
import {
  NO_PROJECT,
  bodySources,
  dayStart,
  itemText,
  memoryBody,
  memoryDay,
  memoryEntryFile,
  memoryWhen,
  notedRecently,
  projectKey,
  projectName,
  rulesNote,
  searchEntries,
  stripDays,
  toolboxGroup,
  writeText,
} from "./request-memory-model";
import type {
  MemoryEntry,
  MemoryEvent,
  RequestContextTexts,
} from "./request-record-model";

// A Friday afternoon, local time.
const now = new Date(2026, 9, 30, 15, 20);
const local = (day: number, hour = 9, minute = 0) =>
  new Date(2026, 9, day, hour, minute).toISOString();

describe("project memory model", () => {
  beforeEach(async () => {
    await applyLocale("zh-CN");
  });

  it("names projects and the day the reads count from", () => {
    expect(projectKey({ project: "~/GolandProjects/astrlink" })).toBe(
      "~/GolandProjects/astrlink",
    );
    expect(projectKey({})).toBe(NO_PROJECT);
    expect(projectName("~/GolandProjects/astrlink")).toBe("astrlink");
    expect(projectName("C:\\work\\api\\")).toBe("api");
    expect(projectName(undefined)).toBe("没有项目目录");
    expect(dayStart(now)).toStrictEqual(new Date(2026, 9, 30));
  });

  it("says days as the reader counts them", () => {
    expect(memoryDay(local(30), now)).toBe("今天");
    expect(memoryDay(local(29), now)).toBe("昨天");
    expect(memoryDay(local(21), now)).toBe("10月21日");
    expect(memoryDay(new Date(2025, 11, 31).toISOString(), now)).toBe(
      "2025年12月31日",
    );
    expect(memoryWhen(local(30, 15, 5), now)).toBe("今天 15:05");
    expect(memoryWhen(local(28, 21, 40), now)).toBe("10月28日 21:40");
  });

  it("draws an entry's days from the day it was noted", () => {
    const start = new Date(2026, 9, 30).toISOString();
    const reads = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 2];
    const drawn = (noted_at?: string) =>
      stripDays({ reads, noted_at }, start).map((day) =>
        day === null ? "." : `${day.reads}${day.noted ? "*" : ""}`,
      );
    // Noted the day before yesterday: blank before it, outlined on it.
    expect(drawn(local(28, 21, 40))).toStrictEqual([
      ...Array(11).fill("."),
      "1*",
      "0",
      "2",
    ]);
    // Noted today, after the day began.
    expect(drawn(local(30, 15, 5)).slice(12)).toStrictEqual([".", "2*"]);
    // Noted before the strip, or before the gateway saw it: drawn in full.
    expect(drawn(local(1))).toStrictEqual(reads.map(String));
    expect(drawn(undefined)).toStrictEqual(reads.map(String));
  });

  it("finds entries, new ones, and the rules' latest change", () => {
    const entry = (fields: Partial<MemoryEntry>): MemoryEntry => ({
      file: "a.md",
      reads: Array(14).fill(0),
      total_reads: 0,
      ...fields,
    });
    const entries = [
      entry({ file: "no-jitter.md", title: "No jitter-sensitive tests" }),
      entry({ file: "reply.md", hook: "回复一律用中文" }),
    ];
    expect(searchEntries(entries, " JITTER ")).toStrictEqual([entries[0]]);
    expect(searchEntries(entries, "中文")).toStrictEqual([entries[1]]);
    expect(searchEntries(entries, "reply.md")).toStrictEqual([entries[1]]);
    expect(searchEntries(entries, "")).toStrictEqual(entries);
    expect(notedRecently({ noted_at: local(24, 15, 30) }, now)).toBe(true);
    expect(notedRecently({ noted_at: local(23, 15, 20) }, now)).toBe(false);
    expect(notedRecently({}, now)).toBe(false);
    expect(
      [
        { changed_at: local(30, 14, 58) },
        { changed_at: local(24, 18, 30) },
        { first_seen_at: local(21) },
        {},
      ].map((rules) => rulesNote(rules, now)),
    ).toStrictEqual([
      { text: "今天 14:58 改了", fresh: true },
      { text: "10月24日改了", fresh: false },
      { text: "10月21日起没变过", fresh: false },
      { text: "", fresh: false },
    ]);
  });

  it("reads inline days in the sentence's case", async () => {
    await applyLocale("en");
    expect(rulesNote({ changed_at: local(30, 14, 58) }, now).text).toBe(
      "Changed today 14:58",
    );
    expect(memoryWhen(local(30, 15, 5), now)).toBe("Today 15:05");
  });

  it("ranks the toolbox's uses and names the idle ones", () => {
    const items = ["a", "b", "c", "d", "e", "f"].map((name, index) => ({
      name,
      uses: [0, 4, 1, 0, 9, 2][index]!,
    }));
    const { bars, moreUsed, idle } = toolboxGroup(items);
    expect(bars.map((item) => item.name)).toStrictEqual(["e", "b", "f"]);
    expect(moreUsed).toBe(1);
    expect(idle.map((item) => item.name)).toStrictEqual(["a", "d"]);
  });

  it("finds a memory's file and its text", () => {
    expect(
      memoryEntryFile({ kind: "memory", name: "MEMORY.md", entry: "a.md" }),
    ).toBe("a.md");
    expect(memoryEntryFile({ kind: "memory", name: "no-jitter.md" })).toBe(
      "no-jitter.md",
    );
    expect(memoryEntryFile({ kind: "memory", name: "MEMORY.md" })).toBeNull();
    expect(memoryEntryFile({ kind: "rules", name: "AGENTS.md" })).toBeNull();
    const event = (fields: Partial<MemoryEvent>): MemoryEvent => ({
      kind: "read",
      at: local(30),
      request_id: "req",
      ...fields,
    });
    // The newest captured read shows the whole file; the note comes last.
    expect(
      bodySources([
        event({ kind: "noted", request_id: "req_note", captured: true }),
        event({ request_id: "req_old", captured: true }),
        event({ request_id: "req_bare" }),
        event({ request_id: "req_new", captured: true }),
      ]).map((source) => source.request_id),
    ).toStrictEqual(["req_new", "req_old", "req_note"]);
    const texts: RequestContextTexts = {
      request_id: "req_new",
      items: [],
      writes: [
        { kind: "memory", name: "No-Jitter.md", content: "Wait on channels." },
      ],
      reads: [],
    };
    expect(memoryBody(texts, "no-jitter.md")).toBe("Wait on channels.");
    texts.reads = [
      {
        name: "no-jitter.md",
        content:
          "---\nname: no-jitter\ndescription: x\n---\n\nTests must not depend on deadlines.\n",
      },
    ];
    expect(memoryBody(texts, "no-jitter.md")).toBe(
      "Tests must not depend on deadlines.",
    );
    expect(memoryBody(texts, "other.md")).toBeNull();
  });

  it("finds a write's text and a file's text in what a call carried", () => {
    const texts: RequestContextTexts = {
      request_id: "req_1",
      items: [
        {
          kind: "rules",
          name: "AGENTS.md",
          path: "~/proj/AGENTS.md",
          text: "Run gofmt.",
        },
        {
          kind: "rules",
          name: "AGENTS.md",
          path: "~/proj/api/AGENTS.md",
          text: "Run go vet.",
        },
        { kind: "rules", name: "CLAUDE.md", text: "Reply briefly." },
      ],
      reads: [],
      writes: [
        {
          kind: "memory",
          name: "MEMORY.md",
          path: "~/.claude/projects/p/memory/MEMORY.md",
          content: "- [No jitter](no-jitter.md)\n",
        },
        {
          kind: "memory",
          name: "no-jitter.md",
          path: "~/.claude/projects/p/memory/no-jitter.md",
          content: "Wait on done channels.",
        },
      ],
    };
    // A new index line opens on the memory file it points to.
    expect(
      writeText(texts, {
        kind: "memory",
        name: "MEMORY.md",
        path: "~/.claude/projects/p/memory/MEMORY.md",
        entry: "no-jitter.md",
      })?.content,
    ).toBe("Wait on done channels.");
    expect(
      writeText(texts, { kind: "memory", name: "MEMORY.md", entry: "gone.md" })
        ?.content,
    ).toBe("- [No jitter](no-jitter.md)\n");
    expect(writeText(texts, { kind: "rules", name: "MEMORY.md" })).toBeNull();
    expect(
      itemText(texts, {
        kind: "rules",
        name: "AGENTS.md",
        path: "~/proj/api/AGENTS.md",
      })?.text,
    ).toBe("Run go vet.");
    // Two files of one name are told apart only by their paths.
    expect(itemText(texts, { kind: "rules", name: "AGENTS.md" })).toBeNull();
    expect(
      itemText(texts, { kind: "rules", name: "CLAUDE.md", path: "~/x" }),
    ).toBeNull();
    expect(itemText(texts, { kind: "rules", name: "CLAUDE.md" })?.text).toBe(
      "Reply briefly.",
    );
  });
});
