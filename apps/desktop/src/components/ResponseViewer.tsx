import { useState, type ReactNode } from "react";

import { Check, Copy } from "./icons";
import { copyButtonLabel, useCopyFeedback } from "@/copy-feedback";
import { useT } from "@/i18n";
import { Button } from "./ui/button";
import { MarkdownContent } from "./MarkdownContent";
import { Panel } from "./Panel";
import { SegmentedControl } from "./SegmentedControl";
import { FormMessage } from "./FormMessage";
import { ActionGroup } from "./ActionGroup";

/** A bounded response workspace. View changes only scroll the content pane. */
export function ResponseViewer({
  content,
  rawContent,
  rawTruncated,
  contentType,
  label,
  notice,
  previewContent,
  rawView,
  rawHint,
  structured,
  children,
}: {
  content?: string;
  rawContent?: string;
  rawTruncated?: boolean;
  contentType?: string;
  label: string;
  notice?: ReactNode;
  /** Structured output; content remains the plain-text copy value. */
  previewContent?: ReactNode;
  /** Optional bounded inspector for large captured responses. */
  rawView?: ReactNode;
  rawHint?: string;
  /**
   * The raw body laid out by its format, such as a JSON tree or stream
   * events, offered between the preview and the original text. It brings
   * its own notices; copying from it copies the original text.
   */
  structured?: { label: string; view: ReactNode };
  children?: ReactNode;
}) {
  const t = useT();
  const [view, setView] = useState<"preview" | "structured" | "raw">("preview");
  const copy = useCopyFeedback();
  const hasResponse = content !== undefined || rawContent !== undefined;
  const showStructured = view === "structured" && structured !== undefined;
  const wire = view === "raw" || showStructured;
  const visibleContent = wire ? rawContent : content;
  const copyKey = `${view}:${visibleContent ?? ""}`;
  return (
    <Panel className="flex min-h-0 flex-1 flex-col" aria-label={label}>
      <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b px-3">
        <span className="truncate text-xs font-medium text-text-secondary">
          {label}
        </span>
        {hasResponse ? (
          <ActionGroup className="gap-1">
            <Button
              size="sm"
              variant="ghost"
              disabled={!visibleContent}
              aria-label={copyButtonLabel(
                copy,
                copyKey,
                t(wire ? "responseViewer.copyRaw" : "responseViewer.copy"),
              )}
              onClick={() => {
                if (visibleContent) copy.copy(copyKey, visibleContent);
              }}
            >
              {copy.activeKey === copyKey && copy.state === "copied" ? (
                <Check aria-hidden="true" />
              ) : (
                <Copy aria-hidden="true" />
              )}
              {copyButtonLabel(copy, copyKey)}
            </Button>
            <SegmentedControl
              label={t("responseViewer.view")}
              value={view}
              onValueChange={setView}
              options={[
                { value: "preview", label: t("responseViewer.preview") },
                ...(structured
                  ? [{ value: "structured" as const, label: structured.label }]
                  : []),
                { value: "raw", label: t("responseViewer.raw") },
              ]}
            />
            <span className="sr-only" role="status">
              {copy.activeKey === copyKey ? copyButtonLabel(copy, copyKey) : ""}
            </span>
          </ActionGroup>
        ) : null}
      </div>
      <div
        className="min-h-0 flex-1 overflow-auto overscroll-contain p-4"
        data-slot="response-content"
        data-tab-scroller
      >
        {wire ? (
          <>
            <p className="mb-3 break-words text-xs text-muted-foreground">
              {rawHint ?? t("responseViewer.rawHint")}
              {contentType ? ` · ${contentType}` : ""}
            </p>
            {showStructured ? (
              structured.view
            ) : (
              <>
                {rawTruncated ? (
                  <FormMessage className="mb-3" tone="warning">
                    {t("responseViewer.truncated")}
                  </FormMessage>
                ) : null}
                {rawView ??
                  (rawContent ? (
                    <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed">
                      {rawContent}
                    </pre>
                  ) : (
                    <FormMessage>
                      {t(
                        rawContent === undefined
                          ? "responseViewer.rawUnavailable"
                          : "responseViewer.rawEmpty",
                      )}
                    </FormMessage>
                  ))}
              </>
            )}
          </>
        ) : (
          <>
            {notice}
            {previewContent ??
              (content ? <MarkdownContent content={content} /> : children)}
          </>
        )}
      </div>
    </Panel>
  );
}
