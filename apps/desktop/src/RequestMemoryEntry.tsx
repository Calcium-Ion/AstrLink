import { useEffect, useState, type ReactNode } from "react";

import {
  getProjectMemoryEntry,
  getRequestContextTexts,
  type ProjectMemoryQuery,
} from "./bridge";
import { useT } from "./i18n";
import { withheldText } from "./RequestContextText";
import { bodySources, memoryBody, memoryWhen } from "./request-memory-model";
import type {
  MemoryEntry,
  MemoryEntryDetail,
  MemoryEvent,
  RequestContextTexts,
} from "./request-record-model";
import { ChevronRight, FileText } from "@/components/icons";
import { MarkdownContent } from "@/components/MarkdownContent";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

/** The memory a dialog opens: its view's scope, its file, what the list had. */
export interface MemoryEntryTarget {
  query: ProjectMemoryQuery;
  file: string;
  entry?: MemoryEntry;
}

/** How many captured calls the dialog reads before it says the text is gone. */
const BODY_TRIES = 3;

type Loaded =
  | { state: "loading" }
  | { state: "error"; message: string; gone: boolean }
  | { state: "ready"; detail: MemoryEntryDetail; body: Body };

type Body =
  | { kind: "text"; text: string; truncated: boolean }
  | { kind: "none"; note: string };

/**
 * One memory in an in-app dialog: its title and line, where it lives, its
 * text from a captured call, and each turn that noted or read it.
 */
export function MemoryEntryDialog({
  target,
  onClose,
  onOpenCall,
}: {
  target: MemoryEntryTarget | null;
  onClose: () => void;
  /** Opens the conversation at one call of the memory's history. */
  onOpenCall: (sessionId: string, requestId: string) => void;
}) {
  return (
    <Dialog open={target !== null} onOpenChange={(open) => !open && onClose()}>
      {target ? (
        <DialogContent
          className="top-[calc(50%+var(--window-chrome-height)/2)] flex max-h-[calc(100dvh-var(--window-chrome-height)-2rem)] max-w-[calc(100%-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-150"
          data-testid="memory-entry-dialog"
        >
          <EntryContent
            onClose={onClose}
            onOpenCall={onOpenCall}
            target={target}
          />
        </DialogContent>
      ) : null}
    </Dialog>
  );
}

function EntryContent({
  target,
  onClose,
  onOpenCall,
}: {
  target: MemoryEntryTarget;
  onClose: () => void;
  onOpenCall: (sessionId: string, requestId: string) => void;
}) {
  const t = useT();
  const loaded = useEntry(target);
  const entry =
    loaded.state === "ready" ? loaded.detail.entry : (target.entry ?? null);
  const title = entry?.title || target.file;
  return (
    <>
      <DialogHeader className="shrink-0 gap-1.5 px-5.5 pt-5 pr-12 pb-2 text-left">
        <DialogTitle className="leading-snug">{title}</DialogTitle>
        <DialogDescription className={cn("text-xs", !entry?.hook && "sr-only")}>
          {entry?.hook || target.file}
        </DialogDescription>
        {entry?.path ? (
          <p
            className="flex min-w-0 items-center gap-1.5 font-mono text-micro text-muted-foreground"
            title={entry.path}
          >
            <FileText
              animateOnHover={false}
              aria-hidden="true"
              className="size-3.25 shrink-0"
            />
            <span className="truncate">{entry.path}</span>
          </p>
        ) : null}
      </DialogHeader>
      <div
        className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto px-5.5 pt-1 pb-4.5"
        data-testid="memory-entry-body"
      >
        {loaded.state === "loading" ? (
          <Note>{t("common.loading")}</Note>
        ) : loaded.state === "error" ? (
          <Note>{loaded.message}</Note>
        ) : (
          <>
            <EntryBody body={loaded.body} />
            <h3 className="mt-1.5 text-xs font-semibold">
              {t("memoryView.history")}
            </h3>
            {loaded.detail.events.length === 0 ? (
              <Note>{t("memoryView.historyNone")}</Note>
            ) : (
              <ol>
                {loaded.detail.events.map((event) => (
                  <EventRow
                    event={event}
                    key={`${event.kind}:${event.request_id}`}
                    onOpen={() => {
                      onClose();
                      onOpenCall(
                        event.session_id ?? event.request_id,
                        event.request_id,
                      );
                    }}
                  />
                ))}
              </ol>
            )}
          </>
        )}
      </div>
    </>
  );
}

function EntryBody({ body }: { body: Body }) {
  const t = useT();
  if (body.kind === "none") return <Note>{body.note}</Note>;
  return (
    <>
      {body.truncated ? <Note>{t("memoryView.truncated")}</Note> : null}
      <div className="rounded-lg bg-muted px-3.5 py-2.75">
        <MarkdownContent
          className="text-xs leading-relaxed"
          content={body.text}
        />
      </div>
    </>
  );
}

function EventRow({
  event,
  onOpen,
}: {
  event: MemoryEvent;
  onOpen: () => void;
}) {
  const t = useT();
  const noted = event.kind === "noted";
  return (
    <li className="border-b last:border-b-0" data-testid="memory-event">
      <Button
        aria-label={`${t("memoryView.openCall")}: ${memoryWhen(event.at, new Date())}`}
        className="grid h-8.5 w-full grid-cols-[0.75rem_2.25rem_minmax(0,1fr)_auto_0.875rem] items-center gap-x-2.5 rounded-none bg-transparent px-0 text-left text-xs font-normal text-foreground hover:bg-muted/60 hover:text-foreground"
        onClick={onOpen}
        title={t("memoryView.openCall")}
        type="button"
        variant="ghost"
      >
        <span
          aria-hidden="true"
          className={cn(
            "size-1.75 rounded-full border-[1.25px]",
            noted ? "border-tide bg-tide" : "border-input",
          )}
        />
        <span className="font-medium">
          {noted ? t("memoryView.eventNoted") : t("memoryView.eventRead")}
        </span>
        <span className="min-w-0 truncate text-text-secondary">
          {event.prompt ? (
            <>
              <span className="mr-1 text-muted-foreground">
                {t("memoryView.youSaid")}
              </span>
              {t("memoryView.quoted", { text: event.prompt })}
            </>
          ) : null}
        </span>
        <time
          className="text-muted-foreground tabular-nums"
          dateTime={event.at}
        >
          {memoryWhen(event.at, new Date())}
        </time>
        <ChevronRight
          animateOnHover={false}
          aria-hidden="true"
          className="size-3.5 text-input"
        />
      </Button>
    </li>
  );
}

function Note({ children }: { children: ReactNode }) {
  return <p className="text-xs leading-5 text-muted-foreground">{children}</p>;
}

/**
 * Reads the memory and then its text: the captured calls that may hold it,
 * the newest read first, until one does.
 */
function useEntry(target: MemoryEntryTarget): Loaded {
  const t = useT();
  const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
  const queryKey = JSON.stringify(target.query);
  useEffect(() => {
    let current = true;
    setLoaded({ state: "loading" });
    const query = JSON.parse(queryKey) as ProjectMemoryQuery;
    getProjectMemoryEntry({ ...query, file: target.file })
      .then(async (detail) => {
        const body = await readBody(detail, target.file, t);
        if (current) setLoaded({ state: "ready", detail, body });
      })
      .catch((error: unknown) => {
        if (!current) return;
        const message = error instanceof Error ? error.message : "";
        const gone = /\b404\b|not[ _]found/i.test(message);
        setLoaded({
          state: "error",
          gone,
          message: gone
            ? t("memoryView.entryGone")
            : message || t("memoryView.entryFailed"),
        });
      });
    return () => {
      current = false;
    };
  }, [queryKey, target.file, t]);
  return loaded;
}

async function readBody(
  detail: MemoryEntryDetail,
  file: string,
  t: ReturnType<typeof useT>,
): Promise<Body> {
  const sources = bodySources(detail.events).slice(0, BODY_TRIES);
  if (sources.length === 0) {
    return { kind: "none", note: t("memoryView.bodyNotCaptured") };
  }
  let withheld: RequestContextTexts | null = null;
  for (const source of sources) {
    let texts: RequestContextTexts;
    try {
      texts = await getRequestContextTexts(source.request_id);
    } catch {
      continue;
    }
    if (texts.withheld) {
      withheld ??= texts;
      continue;
    }
    const text = memoryBody(texts, file);
    if (text) return { kind: "text", text, truncated: !!texts.truncated };
  }
  return {
    kind: "none",
    note: withheld ? withheldText(withheld) : t("memoryView.textMissing"),
  };
}
