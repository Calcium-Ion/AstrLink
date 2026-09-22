import { useState, type ReactNode } from "react";

import { Check, Copy } from "./icons";
import { copyButtonLabel, useCopyFeedback } from "@/copy-feedback";
import { useT } from "@/i18n";
import { Button } from "./ui/button";
import { MarkdownContent } from "./MarkdownContent";
import { Panel } from "./Panel";
import { SegmentedControl } from "./SegmentedControl";
import { FormMessage } from "./FormMessage";

/** A bounded response workspace. View changes only scroll the content pane. */
export function ResponseViewer({ content, rawContent, rawTruncated, contentType, label, notice, children }: {
  content?: string; rawContent?: string; rawTruncated?: boolean; contentType?: string; label: string; notice?: ReactNode; children?: ReactNode;
}) {
  const t = useT();
  const [view, setView] = useState<"preview" | "raw">("preview");
  const copy = useCopyFeedback();
  const hasResponse = content !== undefined || rawContent !== undefined;
  const visibleContent = view === "raw" ? rawContent : content;
  const copyKey = `${view}:${visibleContent ?? ""}`;
  return (
    <Panel className="flex min-h-0 flex-1 flex-col" aria-label={label}>
      <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b px-3">
        <span className="truncate text-xs font-medium text-text-secondary">{label}</span>
        {hasResponse ? <div className="flex items-center gap-1">
          <SegmentedControl label={t("responseViewer.view")} value={view} onValueChange={setView}
            options={[{ value: "preview", label: t("responseViewer.preview") }, { value: "raw", label: t("responseViewer.raw") }]} />
          <Button size="sm" variant="ghost" disabled={!visibleContent} aria-label={copyButtonLabel(copy, copyKey, t(view === "raw" ? "responseViewer.copyRaw" : "responseViewer.copy"))} onClick={() => { if (visibleContent) copy.copy(copyKey, visibleContent); }}>
            {copy.activeKey === copyKey && copy.state === "copied" ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
            {copyButtonLabel(copy, copyKey)}
          </Button>
          <span className="sr-only" role="status">{copy.activeKey === copyKey ? copyButtonLabel(copy, copyKey) : ""}</span>
        </div> : null}
      </div>
      <div className="min-h-0 flex-1 overflow-auto overscroll-contain p-4" data-slot="response-content" data-tab-scroller>
        {view === "raw" ? <>
          <p className="mb-3 break-words text-xs text-muted-foreground">{t("responseViewer.rawHint")}{contentType ? ` · ${contentType}` : ""}</p>
          {rawTruncated ? <FormMessage className="mb-3" tone="warning">{t("responseViewer.truncated")}</FormMessage> : null}
          {rawContent ? <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed">{rawContent}</pre>
            : <FormMessage>{t(rawContent === undefined ? "responseViewer.rawUnavailable" : "responseViewer.rawEmpty")}</FormMessage>}
        </> : <>{notice}{content ? <MarkdownContent content={content} /> : children}</>}
      </div>
    </Panel>
  );
}
