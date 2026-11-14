import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type MutableRefObject,
  type ReactNode,
  type RefObject,
} from "react";

import { getProjectMemory, type ProjectMemoryQuery } from "./bridge";
import { formatCompactNumber } from "./format-compact-number";
import { useT } from "./i18n";
import {
  ContextTextDialog,
  type ContextTextTarget,
} from "./RequestContextText";
import {
  MemoryEntryDialog,
  type MemoryEntryTarget,
} from "./RequestMemoryEntry";
import {
  NO_PROJECT,
  dayStart,
  memoryDay,
  memoryWhen,
  notedRecently,
  projectKey,
  projectName,
  rulesNote,
  searchEntries,
  stripDays,
  toolboxGroup,
  type MemoryFocus,
} from "./request-memory-model";
import type {
  ClientType,
  MemoryEntry,
  MemoryRules,
  MemoryStory,
  MemoryToolbox,
  ProjectMemory,
} from "./request-record-model";
import { BreakdownLine, breakdownShare } from "@/components/BreakdownRow";
import { ClientTypeIcon, clientTypeName } from "@/components/ClientTypeIcon";
import { DayStrip, DayStripSwatch } from "@/components/DayStrip";
import { EmptyState } from "@/components/EmptyState";
import { FilterSelect } from "@/components/FilterSelect";
import { IconButton } from "@/components/IconButton";
import { Brain, ChevronRight } from "@/components/icons";
import { SearchInput } from "@/components/SearchInput";
import { StoryCard } from "@/components/StoryCard";
import { ToolTile } from "@/components/ToolTile";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type Loaded =
  | { state: "loading"; memory: ProjectMemory | null }
  | { state: "ready"; memory: ProjectMemory }
  | { state: "error"; message: string };

/** How often an open view looks for what the agents noted since. */
const LIVE_REFRESH_MS = 10_000;
const STORIES_PER_PAGE = 2;
/** Below this width two story cards cut their titles off; one is shown. */
const STORY_PAIR_MIN_WIDTH = 400;

const storyKey = (story: MemoryStory) =>
  `${story.request_id ?? story.at}\u0000${story.file}`;

/**
 * What one client's agents remembered in one project, told as it happened:
 * what the user said and what the agent noted, every memory with the days
 * it was read again, the rules every call carries, and the tools it used.
 * Read from the calls that passed through the gateway.
 */
export function RequestMemory({
  isReady,
  live = true,
  focus = null,
  onOpenCall,
  reloadRef,
}: {
  isReady: boolean;
  /** The view is on screen, so it looks for new memories as they come. */
  live?: boolean;
  /** A memory to open, with its project and client; a new key opens it again. */
  focus?: (MemoryFocus & { key: number }) | null;
  /** Opens the conversation at one call. */
  onOpenCall: (sessionId: string, requestId: string) => void;
  /** Receives this view's reload, for the page's refresh button. */
  reloadRef?: MutableRefObject<(() => void) | null>;
}) {
  const t = useT();
  // Null until the reader picks one: Core shows the most recent project, and
  // the client that called in it last.
  const [project, setProject] = useState<string | null>(null);
  const [client, setClient] = useState<ClientType | "">("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(0);
  const [loaded, setLoaded] = useState<Loaded>({
    state: "loading",
    memory: null,
  });
  const [fresh, setFresh] = useState<ReadonlySet<string>>(new Set());
  const [reloads, setReloads] = useState(0);
  const [entryTarget, setEntryTarget] = useState<MemoryEntryTarget | null>(
    null,
  );
  const [textTarget, setTextTarget] = useState<ContextTextTarget | null>(null);
  const generationRef = useRef(0);
  const queryRef = useRef<ProjectMemoryQuery>({});
  // The stories each scope has shown, so only those noted since fade in.
  const seenRef = useRef<{ scope: string; keys: Set<string> } | null>(null);

  const fetchMemory = useCallback(
    (quiet: boolean) => {
      const generation = ++generationRef.current;
      const query = queryRef.current;
      if (!quiet) {
        setLoaded((current) => ({
          state: "loading",
          memory: current.state === "error" ? null : current.memory,
        }));
      }
      getProjectMemory({
        ...query,
        day_start: dayStart(new Date()).toISOString(),
      }).then(
        (memory) => {
          if (generationRef.current !== generation) return;
          const scope = `${memory.project ?? NO_PROJECT}\u0000${memory.client_type ?? ""}`;
          const keys = memory.stories.map(storyKey);
          const seen = seenRef.current;
          if (seen?.scope === scope) {
            const added = keys.filter((key) => !seen.keys.has(key));
            added.forEach((key) => seen.keys.add(key));
            if (added.length > 0) {
              setFresh(new Set(added));
              setPage(0);
            }
          } else {
            seenRef.current = { scope, keys: new Set(keys) };
            setFresh(new Set());
          }
          setLoaded({ state: "ready", memory });
        },
        (error: unknown) => {
          // A refresh that fails keeps what is on screen.
          if (generationRef.current !== generation || quiet) return;
          setLoaded({
            state: "error",
            message:
              error instanceof Error && error.message
                ? error.message
                : t("memoryView.loadFailed"),
          });
        },
      );
    },
    [t],
  );

  useEffect(() => {
    if (!isReady) return;
    queryRef.current = {
      ...(project === null
        ? {}
        : project === NO_PROJECT
          ? { no_project: true }
          : { project }),
      ...(client ? { client_type: client } : {}),
    };
    setPage(0);
    fetchMemory(false);
  }, [isReady, project, client, reloads, fetchMemory]);

  useEffect(() => {
    if (!isReady || !live) return;
    const timer = window.setInterval(() => fetchMemory(true), LIVE_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [isReady, live, fetchMemory]);

  const reload = useCallback(() => setReloads((count) => count + 1), []);
  useEffect(() => {
    if (!reloadRef) return;
    reloadRef.current = reload;
    return () => {
      if (reloadRef.current === reload) reloadRef.current = null;
    };
  }, [reloadRef, reload]);

  // A memory another page asked for opens in its own project and client.
  useEffect(() => {
    if (!focus) return;
    setProject(focus.project ?? NO_PROJECT);
    setClient(focus.clientType);
    setSearch("");
    setEntryTarget({
      query: {
        ...(focus.project === undefined
          ? { no_project: true }
          : { project: focus.project }),
        client_type: focus.clientType,
        day_start: dayStart(new Date()).toISOString(),
      },
      file: focus.file,
    });
  }, [focus]);

  const memory = loaded.state === "error" ? null : loaded.memory;
  const projects = memory?.projects ?? [];
  const shown =
    project ??
    (memory && projects.length > 0
      ? (memory.project ?? NO_PROJECT)
      : (projects[0] && projectKey(projects[0])) || "");
  const shownProject = projects.find((entry) => projectKey(entry) === shown);
  const clients = shownProject?.client_types ?? [];
  const shownClient = client || memory?.client_type || "";

  const openEntry = (file: string, entry?: MemoryEntry) => {
    if (!memory) return;
    setEntryTarget({
      query: {
        ...(memory.project === undefined
          ? { no_project: true }
          : { project: memory.project }),
        ...(memory.client_type ? { client_type: memory.client_type } : {}),
        day_start: memory.day_start,
      },
      file,
      entry,
    });
  };

  if (!isReady) {
    return (
      <EmptyState
        className="my-4 min-h-64"
        title={t("records.waitingReady")}
        description={t("records.waitingHint")}
      />
    );
  }
  const empty = memory && !memory.has_memory && !memory.has_rules;
  return (
    <div className="@container/memory flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-2 py-2">
        <FilterSelect
          ariaLabel={t("records.filter", { label: t("memoryView.project") })}
          className="w-75 min-w-40 shrink"
          disabled={projects.length === 0}
          label={t("memoryView.project")}
          onChange={(next) => {
            // An options list that loads late reports a blank pick.
            if (!next || next === shown) return;
            setClient("");
            setSearch("");
            setProject(next);
          }}
          options={projects.map((entry) => ({
            value: projectKey(entry),
            label: entry.project
              ? `${projectName(entry.project)} ${entry.project}`
              : t("memoryView.noProject"),
            displayLabel: (
              <span className="flex min-w-0 items-baseline gap-2">
                <span className="shrink-0">{projectName(entry.project)}</span>
                {entry.project ? (
                  <span className="truncate font-mono text-micro text-muted-foreground">
                    {entry.project}
                  </span>
                ) : null}
              </span>
            ),
          }))}
          value={shown}
          variant="filled"
        />
        <FilterSelect
          ariaLabel={t("records.filter", { label: t("memoryView.client") })}
          className="w-55 min-w-36 shrink"
          disabled={clients.length === 0}
          label={t("memoryView.client")}
          onChange={(next) => {
            if (!next || next === shownClient) return;
            setSearch("");
            setClient(next as ClientType);
          }}
          options={clients.map((clientType) => ({
            label: clientTypeName(clientType),
            value: clientType,
            displayLabel: (
              <span className="inline-flex min-w-0 items-center gap-2">
                <ClientTypeIcon clientType={clientType} decorative size={16} />
                <span className="truncate">{clientTypeName(clientType)}</span>
              </span>
            ),
          }))}
          value={shownClient}
        />
        {memory && !empty && memory.has_memory ? (
          <span
            className="hidden shrink-0 px-2 text-xs whitespace-nowrap text-text-secondary @[60rem]/memory:block"
            data-testid="memory-count"
          >
            {memory.recent > 0
              ? t("memoryView.countLine", {
                  count: memory.entries.length,
                  recent: memory.recent,
                })
              : t("memoryView.countLineQuiet", {
                  count: memory.entries.length,
                })}
          </span>
        ) : null}
        {memory && !empty && memory.has_memory ? (
          <SearchInput
            className="ml-auto w-56 min-w-36 shrink"
            clearLabel={t("memoryView.searchClear")}
            label={t("memoryView.search")}
            onValueChange={setSearch}
            placeholder={t("memoryView.search")}
            value={search}
          />
        ) : null}
      </div>
      {loaded.state === "error" ? (
        <EmptyState
          className="my-4 min-h-64"
          title={t("memoryView.loadFailed")}
          description={loaded.message}
        />
      ) : !memory ? (
        <p className="pt-4 text-xs text-muted-foreground">
          {t("common.loading")}
        </p>
      ) : projects.length === 0 ? (
        <EmptyState
          className="my-4 min-h-64"
          title={t("memoryView.emptyTitle")}
          description={t("memoryView.emptyHint")}
        />
      ) : empty ? (
        <EmptyClient memory={memory} />
      ) : (
        <div
          aria-busy={loaded.state === "loading"}
          className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_13rem] gap-5 pt-1 pb-3.5 @[44rem]/memory:grid-cols-[minmax(0,1fr)_15rem] @[44rem]/memory:gap-6 @[62rem]/memory:grid-cols-[minmax(0,1fr)_18.75rem] @[62rem]/memory:gap-9"
          data-testid="request-memory"
        >
          <section className="flex min-h-0 min-w-0 flex-col">
            <Stories
              fresh={fresh}
              onOpen={(story) => openEntry(story.file)}
              onPage={setPage}
              page={page}
              stories={memory.stories}
            />
            <Entries
              dayStartAt={memory.day_start}
              entries={memory.entries}
              onOpen={(entry) => openEntry(entry.file, entry)}
              search={search}
            />
          </section>
          <aside
            className="min-h-0 min-w-0 overflow-y-auto overscroll-contain"
            data-testid="memory-aside"
          >
            <Rules onView={setTextTarget} rules={memory.rules} />
            <Toolbox toolbox={memory.toolbox} />
          </aside>
        </div>
      )}
      <MemoryEntryDialog
        onClose={() => setEntryTarget(null)}
        onOpenCall={onOpenCall}
        target={entryTarget}
      />
      <ContextTextDialog
        onClose={() => setTextTarget(null)}
        target={textTarget}
      />
    </div>
  );
}

function Header({
  title,
  children,
  aside,
}: {
  title: string;
  children?: ReactNode;
  aside?: ReactNode;
}) {
  return (
    <div className="flex h-6.5 min-w-0 shrink-0 items-center gap-2.5 text-xs whitespace-nowrap text-muted-foreground">
      <h2 className="shrink-0 text-sm font-semibold text-foreground">
        {title}
      </h2>
      {children ? <span className="min-w-0 truncate">{children}</span> : null}
      {aside ? (
        <span className="ml-auto flex shrink-0 items-center gap-1">
          {aside}
        </span>
      ) : null}
    </div>
  );
}

function Stories({
  stories,
  page,
  fresh,
  onPage,
  onOpen,
}: {
  stories: MemoryStory[];
  page: number;
  fresh: ReadonlySet<string>;
  onPage: (page: number) => void;
  onOpen: (story: MemoryStory) => void;
}) {
  const t = useT();
  const now = new Date();
  const sectionRef = useRef<HTMLElement>(null);
  const perPage = useStoriesPerPage(sectionRef);
  const pages = Math.max(1, Math.ceil(stories.length / perPage));
  const current = Math.min(page, pages - 1);
  const from = current * perPage;
  const shown = stories.slice(from, from + perPage);
  return (
    <section className="shrink-0" data-testid="memory-stories" ref={sectionRef}>
      <Header
        aside={
          stories.length > perPage ? (
            <>
              <span className="mr-1 tabular-nums">
                {shown.length > 1
                  ? t("memoryView.storiesPage", {
                      from: from + 1,
                      to: from + shown.length,
                      total: stories.length,
                    })
                  : t("memoryView.storiesPageOne", {
                      from: from + 1,
                      total: stories.length,
                    })}
              </span>
              <IconButton
                className="size-6 border border-input"
                disabled={current === 0}
                label={t("memoryView.storiesPrev")}
                onClick={() => onPage(current - 1)}
                size="icon-xs"
                type="button"
              >
                <ChevronRight
                  animateOnHover={false}
                  aria-hidden="true"
                  className="size-3.25 rotate-180"
                />
              </IconButton>
              <IconButton
                className="size-6 border border-input"
                disabled={current >= pages - 1}
                label={t("memoryView.storiesNext")}
                onClick={() => onPage(current + 1)}
                size="icon-xs"
                type="button"
              >
                <ChevronRight
                  animateOnHover={false}
                  aria-hidden="true"
                  className="size-3.25"
                />
              </IconButton>
            </>
          ) : null
        }
        title={t("memoryView.storiesTitle")}
      >
        {t("memoryView.storiesHint")}
      </Header>
      {stories.length === 0 ? (
        <p className="mt-1.5 mb-4.5 text-xs text-muted-foreground">
          {t("memoryView.storiesNone")}
        </p>
      ) : (
        <div
          className={cn(
            "mt-1.5 mb-4.5 grid gap-3.5",
            perPage > 1 ? "grid-cols-2" : "grid-cols-1",
          )}
        >
          {shown.map((story) => (
            <StoryCard
              dateTime={story.at}
              fresh={fresh.has(storyKey(story))}
              hook={story.hook}
              key={storyKey(story)}
              onOpen={() => onOpen(story)}
              prompt={story.prompt}
              promptMissing={t("memoryView.promptMissing")}
              time={memoryWhen(story.at, now)}
              title={story.title || story.file}
            />
          ))}
        </div>
      )}
    </section>
  );
}

/** Two stories side by side, or one where the column is too narrow. */
function useStoriesPerPage(ref: RefObject<HTMLElement | null>): number {
  const [perPage, setPerPage] = useState(STORIES_PER_PAGE);
  useLayoutEffect(() => {
    const node = ref.current;
    if (!node) return;
    const measure = () => {
      // A hidden tab measures 0; it keeps the count it had.
      if (node.clientWidth > 0) {
        setPerPage(
          node.clientWidth < STORY_PAIR_MIN_WIDTH ? 1 : STORIES_PER_PAGE,
        );
      }
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [ref]);
  return perPage;
}

function Entries({
  entries,
  dayStartAt,
  search,
  onOpen,
}: {
  entries: MemoryEntry[];
  dayStartAt: string;
  search: string;
  onOpen: (entry: MemoryEntry) => void;
}) {
  const t = useT();
  const now = new Date();
  const shown = useMemo(
    () => searchEntries(entries, search),
    [entries, search],
  );
  return (
    <section
      className="flex min-h-0 flex-1 flex-col"
      data-testid="memory-entries"
    >
      <Header
        aside={
          <span className="hidden items-center gap-1.5 @[44rem]/memory:flex">
            {t("memoryView.legendDays")}
            <span className="w-1.5" />
            <DayStripSwatch />
            {t("memoryView.legendRead")}
            <span className="w-1.5" />
            <DayStripSwatch count={0} marked />
            {t("memoryView.legendNoted")}
          </span>
        }
        title={t("memoryView.entriesTitle")}
      >
        {t("memoryView.entriesCount", { count: entries.length })}
      </Header>
      <div
        className="min-h-0 flex-1 overflow-y-auto overscroll-contain border-t"
        data-testid="memory-entries-scroll"
      >
        {shown.length === 0 ? (
          <p className="py-2 text-xs text-muted-foreground">
            {search.trim()
              ? t("memoryView.searchNone", { query: search.trim() })
              : t("memoryView.entriesNone")}
          </p>
        ) : (
          <ul>
            {shown.map((entry) => (
              <EntryRow
                dayStartAt={dayStartAt}
                entry={entry}
                isNew={notedRecently(entry, now)}
                key={entry.file}
                onOpen={() => onOpen(entry)}
              />
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

function EntryRow({
  entry,
  dayStartAt,
  isNew,
  onOpen,
}: {
  entry: MemoryEntry;
  dayStartAt: string;
  isNew: boolean;
  onOpen: () => void;
}) {
  const t = useT();
  const days = stripDays(entry, dayStartAt).map((day) =>
    day ? { count: day.reads, marked: day.noted } : null,
  );
  const recentReads = entry.reads.reduce((sum, count) => sum + count, 0);
  return (
    <li className="border-b" data-testid="memory-entry">
      <Button
        className="grid h-auto w-full grid-cols-[0.5rem_minmax(0,1fr)_3.5rem] items-center gap-x-3.5 rounded-none bg-transparent py-1.75 pr-2 pl-1.5 text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 hover:text-foreground @[44rem]/memory:grid-cols-[0.5rem_minmax(0,1fr)_10.3125rem_3.5rem]"
        onClick={onOpen}
        type="button"
        variant="ghost"
      >
        {isNew ? (
          <span
            aria-label={t("memoryView.newEntry")}
            className="size-1.5 rounded-full bg-tide"
            role="img"
          />
        ) : (
          <span aria-hidden="true" />
        )}
        <span className="min-w-0">
          <span className="block truncate text-sm leading-normal font-medium">
            {entry.title || entry.file}
          </span>
          {entry.hook ? (
            <span className="block truncate text-xs leading-normal text-muted-foreground">
              {entry.hook}
            </span>
          ) : null}
        </span>
        <DayStrip
          className="hidden @[44rem]/memory:flex"
          days={days}
          label={t("memoryView.stripLabel", { count: recentReads })}
        />
        <span
          className={cn(
            "text-right text-xs whitespace-nowrap tabular-nums",
            entry.total_reads > 0
              ? "text-text-secondary"
              : "text-muted-foreground",
          )}
        >
          {entry.total_reads > 0
            ? t("memoryView.reads", { count: entry.total_reads })
            : t("memoryView.neverRead")}
        </span>
      </Button>
    </li>
  );
}

function scopeLabel(scope: MemoryRules["scope"]): string | null {
  switch (scope) {
    case "global":
      return "memoryView.scopeGlobal";
    case "project":
      return "memoryView.scopeProject";
    case "local":
      return "memoryView.scopeLocal";
    default:
      return null;
  }
}

function Rules({
  rules,
  onView,
}: {
  rules: MemoryRules[];
  onView: (target: ContextTextTarget) => void;
}) {
  const t = useT();
  const now = new Date();
  return (
    <section className="mb-5" data-testid="memory-rules">
      <Header title={t("memoryView.rulesTitle")}>
        {t("memoryView.rulesHint")}
      </Header>
      {rules.length === 0 ? (
        <p className="py-1.5 text-xs text-muted-foreground">
          {t("memoryView.rulesNone")}
        </p>
      ) : (
        rules.map((file) => {
          const note = rulesNote(file, now);
          const scope = scopeLabel(file.scope);
          const comparable =
            file.captured && file.request_id && file.before_request_id;
          return (
            <div
              className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-2 border-b pt-1.75 pb-2 text-xs"
              data-testid="memory-rules-file"
              key={`${file.path ?? ""}\u0000${file.name}`}
            >
              <span className="min-w-0 truncate" title={file.path}>
                <code className="font-mono">{file.name}</code>
                {scope ? (
                  <span className="ml-1.5 text-muted-foreground">
                    {t(scope)}
                  </span>
                ) : null}
              </span>
              {comparable ? (
                <Button
                  className="h-auto px-0 text-xs text-accent-foreground"
                  onClick={() =>
                    onView({
                      kind: "change",
                      requestId: file.request_id!,
                      beforeRequestId: file.before_request_id!,
                      title: t("memoryView.changeDialog", { name: file.name }),
                      item: { kind: "rules", name: file.name, path: file.path },
                    })
                  }
                  type="button"
                  variant="link"
                >
                  {t("memoryView.viewChange")}
                </Button>
              ) : (
                <span />
              )}
              {note.text ? (
                <span
                  className={cn(
                    "col-span-full",
                    note.fresh
                      ? "text-accent-foreground"
                      : "text-muted-foreground",
                  )}
                >
                  {note.text}
                </span>
              ) : null}
            </div>
          );
        })
      )}
    </section>
  );
}

/** Names in a quiet line, cut short where the line ends. */
function IdleLine({ lead, names }: { lead: string; names: readonly string[] }) {
  const t = useT();
  const separator = t("memoryView.nameSeparator");
  return (
    <p
      className="mt-1 truncate text-xs text-muted-foreground"
      title={names.join(separator)}
    >
      {lead}
      {names.map((name, index) => (
        <span key={name}>
          {index > 0 ? separator : null}
          <code className="font-mono text-micro">{name}</code>
        </span>
      ))}
    </p>
  );
}

function ToolGroup({
  title,
  count,
  items,
}: {
  title: string;
  count: string;
  items: readonly { name: string; uses: number; carried: boolean }[];
}) {
  const t = useT();
  const { bars, moreUsed, idle } = toolboxGroup(items);
  const uses = bars.map((item) => item.uses);
  const idleNames = idle.filter((item) => item.carried).map((i) => i.name);
  const lead =
    idleNames.length > 0
      ? `${moreUsed > 0 ? t("memoryView.moreUsedLead", { count: moreUsed }) : ""}${t("memoryView.idleLead", { count: idleNames.length })}`
      : moreUsed > 0
        ? t("memoryView.moreUsedOnly", { count: moreUsed })
        : null;
  return (
    <div className="border-b pt-2 pb-2.25" data-testid="memory-toolbox-group">
      <div className="mb-0.75 flex items-baseline gap-2 text-xs whitespace-nowrap">
        <strong className="font-medium">{title}</strong>
        <span className="text-muted-foreground">{count}</span>
      </div>
      {bars.map((item) => (
        <BreakdownLine
          key={item.name}
          label={
            <code className="truncate font-mono text-micro text-foreground">
              {item.name}
            </code>
          }
          labelTitle={item.name}
          share={breakdownShare(item.uses, uses)}
          size="dense"
          value={item.uses}
        />
      ))}
      {lead !== null ? <IdleLine lead={lead} names={idleNames} /> : null}
    </div>
  );
}

/** The days the toolbox counts, from the day they begin. */
function ToolboxDays({ since }: { since: string }) {
  const t = useT();
  return (
    <span
      data-testid="memory-toolbox-days"
      title={t("memoryView.toolboxDaysHint")}
    >
      {t("memoryView.toolboxDays", {
        date: memoryDay(since, new Date(), true),
      })}
    </span>
  );
}

function Toolbox({ toolbox }: { toolbox: MemoryToolbox }) {
  const t = useT();
  const carriedSkills = toolbox.skills.filter((skill) => skill.carried).length;
  const empty = toolbox.skills.length === 0 && toolbox.mcp.length === 0;
  return (
    <section data-testid="memory-toolbox">
      <Header
        aside={<ToolboxDays since={toolbox.since} />}
        title={t("memoryView.toolboxTitle")}
      />
      {empty ? (
        <p className="py-1.5 text-xs text-muted-foreground">
          {t("memoryView.toolboxNone")}
        </p>
      ) : (
        <>
          {toolbox.skills.length > 0 ? (
            <ToolGroup
              count={t("memoryView.groupCount", { count: carriedSkills })}
              items={toolbox.skills}
              title={t("memoryView.skillsGroup")}
            />
          ) : null}
          {toolbox.mcp.map((server) => (
            <ToolGroup
              count={t("memoryView.mcpCount", {
                count: server.tools.filter((tool) => tool.carried).length,
              })}
              items={server.tools}
              key={server.server}
              title={t("memoryView.mcpGroup", { server: server.server })}
            />
          ))}
          {toolbox.idle_tokens > 0 ? (
            <p className="mt-2.25 text-xs leading-relaxed text-muted-foreground">
              {t("memoryView.idleNote", {
                tokens: formatCompactNumber(toolbox.idle_tokens),
              })}
            </p>
          ) : null}
        </>
      )}
    </section>
  );
}

/**
 * A client that keeps no memory and carries no rules here: what it carries
 * instead, each skill a tile, the used ones first.
 */
function EmptyClient({ memory }: { memory: ProjectMemory }) {
  const t = useT();
  const name = memory.client_type
    ? clientTypeName(memory.client_type)
    : t("memoryView.client");
  const skills = [...memory.toolbox.skills].sort(
    (left, right) => right.uses - left.uses,
  );
  const carried = skills.filter((skill) => skill.carried).length;
  const used = skills.filter((skill) => skill.uses > 0).length;
  const tileNote = (uses: number) =>
    uses > 0
      ? t("memoryView.usedTimes", { count: uses })
      : t("memoryView.untouched");
  return (
    <div
      className="min-h-0 flex-1 overflow-y-auto overscroll-contain pt-1 pb-3.5"
      data-testid="memory-empty-client"
    >
      <EmptyState
        className="mt-1.5 mb-4.5"
        description={t("memoryView.emptyClientHint")}
        illustration={
          <Brain animateOnHover={false} aria-hidden="true" className="size-4" />
        }
        title={t("memoryView.emptyClientTitle", { client: name })}
        variant="inline"
      />
      <Header
        aside={<ToolboxDays since={memory.toolbox.since} />}
        title={t("memoryView.toolboxTitle")}
      >
        {skills.length > 0
          ? t("memoryView.toolboxSummary", { count: carried, used })
          : null}
      </Header>
      {skills.length === 0 && memory.toolbox.mcp.length === 0 ? (
        <p className="py-1.5 text-xs text-muted-foreground">
          {t("memoryView.toolboxNone")}
        </p>
      ) : null}
      {skills.length > 0 ? (
        <div className="mt-1.5 grid grid-cols-[repeat(auto-fill,minmax(13rem,1fr))] gap-2.5">
          {skills.map((skill) => (
            <ToolTile
              active={skill.uses > 0}
              description={skill.description}
              key={skill.name}
              name={skill.name}
              note={tileNote(skill.uses)}
            />
          ))}
        </div>
      ) : null}
      {memory.toolbox.mcp.map((server) => (
        <div className="mt-4" key={server.server}>
          <Header title={t("memoryView.mcpGroup", { server: server.server })}>
            {t("memoryView.mcpCount", { count: server.tools.length })}
          </Header>
          <div className="mt-1.5 grid grid-cols-[repeat(auto-fill,minmax(13rem,1fr))] gap-2.5">
            {[...server.tools]
              .sort((left, right) => right.uses - left.uses)
              .map((tool) => (
                <ToolTile
                  active={tool.uses > 0}
                  key={tool.name}
                  name={tool.name}
                  note={tileNote(tool.uses)}
                />
              ))}
          </div>
        </div>
      ))}
    </div>
  );
}
