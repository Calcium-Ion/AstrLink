import { useEffect, useMemo, useState, type ReactNode } from "react";

import { getRequestContextTexts } from "./bridge";
import { i18n, useT } from "./i18n";
import { foldDiff, lineDiff } from "./line-diff";
import { itemText, writeText } from "./request-memory-model";
import type {
  ContextItemText,
  MemoryWrite,
  RequestContextTexts,
} from "./request-record-model";
import {
  CodeBlock,
  CodeBlockActions,
  CodeBlockContent,
  CodeBlockCopyButton,
  CodeBlockHeader,
  CodeBlockLine,
  CodeBlockTitle,
} from "@/components/ui/code-block";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

/**
 * What a captured call's body shows: a memory a tool call wrote, a file
 * between two calls that carried it, or the summary a compaction left.
 */
export type ContextTextTarget = { title: string; description?: string } & (
  | {
      kind: "write";
      requestId: string;
      write: Pick<MemoryWrite, "kind" | "name" | "path" | "entry">;
    }
  | {
      kind: "change";
      requestId: string;
      beforeRequestId: string;
      item: Pick<ContextItemText, "kind" | "name" | "path">;
    }
  | { kind: "summary"; requestId: string }
);

type Loaded =
  | { state: "loading" }
  | { state: "error"; message: string }
  | {
      state: "ready";
      texts: RequestContextTexts;
      before?: RequestContextTexts;
    };

/** Shows one target's text in an in-app dialog; null keeps it closed. */
export function ContextTextDialog({
  target,
  onClose,
}: {
  target: ContextTextTarget | null;
  onClose: () => void;
}) {
  const t = useT();
  return (
    <Dialog open={target !== null} onOpenChange={(open) => !open && onClose()}>
      {target ? (
        <DialogContent className="top-[calc(50%+var(--window-chrome-height)/2)] flex max-h-[calc(100dvh-var(--window-chrome-height)-2rem)] max-w-[calc(100%-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl">
          <DialogHeader className="shrink-0 gap-1.5 border-b px-5 py-3 pr-12 text-left">
            <DialogTitle className="truncate leading-normal">
              {target.title}
            </DialogTitle>
            <DialogDescription className="text-xs">
              {target.description ?? t("memoryView.fromRequest")}
            </DialogDescription>
          </DialogHeader>
          <div
            className="min-h-0 flex-1 overflow-y-auto px-5 py-4"
            data-testid="context-text-body"
          >
            <ContextTextBody target={target} />
          </div>
        </DialogContent>
      ) : null}
    </Dialog>
  );
}

function ContextTextBody({ target }: { target: ContextTextTarget }) {
  const t = useT();
  const loaded = useTexts(target);
  if (loaded.state === "loading") {
    return <Note>{t("common.loading")}</Note>;
  }
  if (loaded.state === "error") return <Note>{loaded.message}</Note>;
  const { texts, before } = loaded;
  const withheld = [texts, before].find((read) => read?.withheld);
  if (withheld) return <Note>{withheldText(withheld)}</Note>;
  const truncated = texts.truncated || before?.truncated;
  let body: ReactNode = null;
  if (target.kind === "write") {
    const write = writeText(texts, target.write);
    if (write) {
      body = (
        <TextBlock title={write.path ?? write.name} text={write.content} />
      );
    }
  } else if (target.kind === "summary") {
    if (texts.summary) {
      body = (
        <TextBlock title={t("memoryView.summaryTitle")} text={texts.summary} />
      );
    }
  } else {
    const after = itemText(texts, target.item);
    const earlier = before ? itemText(before, target.item) : null;
    if (after && earlier) {
      body = (
        <DiffBlock
          after={after.text}
          before={earlier.text}
          title={after.path ?? after.name ?? ""}
        />
      );
    } else if (after) {
      body = (
        <>
          <Note>{t("memoryView.beforeMissing")}</Note>
          <TextBlock title={after.path ?? after.name ?? ""} text={after.text} />
        </>
      );
    }
  }
  return (
    <div className="flex flex-col gap-3">
      {truncated ? <Note>{t("memoryView.truncated")}</Note> : null}
      {body ?? <Note>{t("memoryView.textMissing")}</Note>}
    </div>
  );
}

function useTexts(target: ContextTextTarget): Loaded {
  const t = useT();
  const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
  const beforeId = target.kind === "change" ? target.beforeRequestId : null;
  useEffect(() => {
    let current = true;
    setLoaded({ state: "loading" });
    Promise.all([
      getRequestContextTexts(target.requestId),
      beforeId ? getRequestContextTexts(beforeId) : Promise.resolve(undefined),
    ]).then(
      ([texts, before]) => {
        if (current) setLoaded({ state: "ready", texts, before });
      },
      (error: unknown) => {
        if (!current) return;
        setLoaded({
          state: "error",
          message:
            error instanceof Error && error.message
              ? error.message
              : t("memoryView.textFailed"),
        });
      },
    );
    return () => {
      current = false;
    };
  }, [target.requestId, beforeId, t]);
  return loaded;
}

/** Why a call's texts cannot be read here, in a line. */
export function withheldText(texts: RequestContextTexts): string {
  const t = i18n.t.bind(i18n);
  switch (texts.reason) {
    case undefined:
      return t("memoryView.notCaptured");
    case "raw_locked":
      return t("rawSealing.lockedDetail");
    case "raw_not_kept":
      return t("rawSealing.notKeptDetail");
    default:
      return t("rawSealing.withheldDetail");
  }
}

function Note({ children }: { children: ReactNode }) {
  return <p className="text-xs leading-5 text-muted-foreground">{children}</p>;
}

function TextBlock({ title, text }: { title: string; text: string }) {
  return (
    <CodeBlock className="data-[size=sm]:text-micro" size="sm">
      <CodeBlockHeader>
        <CodeBlockTitle className="font-mono" title={title}>
          {title}
        </CodeBlockTitle>
        <CodeBlockActions>
          <CodeBlockCopyButton value={text} />
        </CodeBlockActions>
      </CodeBlockHeader>
      <CodeBlockContent wrap>{text}</CodeBlockContent>
    </CodeBlock>
  );
}

function DiffBlock({
  title,
  before,
  after,
}: {
  title: string;
  before: string;
  after: string;
}) {
  const t = useT();
  const lines = useMemo(
    () => foldDiff(lineDiff(before, after)),
    [before, after],
  );
  const changed = lines.some(
    (line) => line.kind === "added" || line.kind === "removed",
  );
  if (!changed) return <Note>{t("memoryView.diffSame")}</Note>;
  return (
    <CodeBlock
      className="data-[size=sm]:text-micro"
      data-testid="context-text-diff"
      size="sm"
    >
      <CodeBlockHeader>
        <CodeBlockTitle className="font-mono" title={title}>
          {title}
        </CodeBlockTitle>
      </CodeBlockHeader>
      <CodeBlockContent wrap>
        {lines.map((line, index) =>
          line.kind === "fold" ? (
            <CodeBlockLine
              className="font-sans text-muted-foreground"
              key={index}
            >
              {t("memoryView.diffFold", { count: line.count })}
            </CodeBlockLine>
          ) : (
            <CodeBlockLine
              data-diff={line.kind}
              key={index}
              variant={line.kind === "same" ? "default" : line.kind}
            >
              {line.text}
            </CodeBlockLine>
          ),
        )}
      </CodeBlockContent>
    </CodeBlock>
  );
}
