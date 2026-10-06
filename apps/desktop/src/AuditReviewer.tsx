import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ChevronRight } from "@/components/icons";
import { EmptyState } from "@/components/EmptyState";
import { markText, useFindScroll } from "@/components/FindBar";
import { FormMessage } from "@/components/FormMessage";
import { JsonTreeView } from "@/components/JsonTreeView";
import { MarkdownContent } from "@/components/MarkdownContent";
import { ResponseViewer } from "@/components/ResponseViewer";
import { StatusBadge } from "@/components/StatusBadge";
import { cn } from "@/lib/utils";

import { buildHeadersText } from "./audit-bundle";
import {
  FIND_HIT_LIMIT,
  findMatcher,
  findMatches,
  findNeedle,
  findOffsets,
  type FindRequest,
} from "./find-model";
import { i18n } from "./i18n";
import { copyButtonLabel, type CopyFeedback } from "./copy-feedback";
import {
  JsonTreeCancelledError,
  detectBase64,
  jsonKeyText,
  parseJsonTree,
  parseJsonTreeIncremental,
  type JsonNode,
  type JsonTreeParse,
} from "./json-tree-model";
import type {
  AuditContentPart,
  AuditHTTPMeta,
  AuditWithheldPart,
} from "./request-record-model";
import { splitPrivacyHighlights } from "./request-trajectory-model";
import {
  parseResponsePreview,
  type ResponseOutput,
  type ResponsePreview,
} from "./response-preview-model";
import {
  parseSSEIncremental,
  SSEParseCancelledError,
  type SSEEvent,
} from "./sse-review-model";

const RAW_SEGMENT_SIZE = 256 * 1024;
// JSON bodies up to this size parse at once; larger ones parse in slices.
const JSON_TREE_SYNC_CHARS = 256 * 1024;
// The request capture ceiling; beyond it only the original text is offered.
const JSON_TREE_MAX_CHARS = 16 * 1024 * 1024;
const EVENT_RENDER_BATCH = 300;
// Short streams read better unfiltered; the search box earns its row later.
const EVENT_FILTER_THRESHOLD = 12;

type StreamViewMode = "raw" | "events";
type DocumentViewMode = "formatted" | "raw";

export interface ResponsePreviewState {
  ready: boolean;
  preview: ResponsePreview | null;
}

/**
 * Parse a captured response once. `null` skips parsing, so a caller that
 * already holds the state can pass it down instead of parsing twice.
 */
export function useResponsePreview(
  part: AuditContentPart | null,
): ResponsePreviewState {
  const [state, setState] = useState<{
    part: AuditContentPart;
    preview: ResponsePreview | null;
  } | null>(null);
  useEffect(() => {
    if (!part) return;
    const controller = new AbortController();
    void parseResponsePreview(part, controller.signal).then(
      (preview) => {
        if (!controller.signal.aborted) setState({ part, preview });
      },
      () => {
        if (!controller.signal.aborted) setState({ part, preview: null });
      },
    );
    return () => controller.abort();
  }, [part]);
  const ready = part !== null && state?.part === part;
  return { ready, preview: ready ? state.preview : null };
}

/** Errors the response itself reported, as opposed to the gateway's verdict. */
export function responseErrors(state: ResponsePreviewState): ResponseOutput[] {
  return (
    state.preview?.outputs.filter((output) => output.kind === "error") ?? []
  );
}

/**
 * Client outcome, reconstructed from the captured client response only.
 * `hideErrors` is for hosts that already lead with the reported error.
 */
export function AuditResultSection({
  part,
  preview: providedState,
  failed = false,
  hideErrors = false,
  lead,
}: {
  part: AuditContentPart;
  preview?: ResponsePreviewState;
  failed?: boolean;
  hideErrors?: boolean;
  /** Leads the preview, such as the failure reason that replaces hidden errors. */
  lead?: ReactNode;
}) {
  const t = i18n.t.bind(i18n);
  const ownState = useResponsePreview(providedState ? null : part);
  const { ready, preview } = providedState ?? ownState;
  const outputs =
    preview?.outputs.filter(
      (output) => !hideErrors || output.kind !== "error",
    ) ?? [];
  const copyText = outputs
    .map((output) => [output.name, output.text].filter(Boolean).join("\n"))
    .join("\n\n");
  const structuredLabel = wireStructuredLabel(part);
  return (
    <ResponseViewer
      content={copyText}
      rawContent={part.content}
      rawTruncated={part.truncated}
      contentType={part.media_type}
      label={t("trajectory.clientResponse")}
      rawView={<RawSegmentView bounded={false} content={part.content} />}
      rawHint={t("audit.clientWireHint")}
      structured={
        structuredLabel
          ? {
              label: structuredLabel,
              view: <AuditWireView mode="structured" part={part} />,
            }
          : undefined
      }
      previewContent={
        <div className="space-y-3" data-testid="audit-result-preview">
          {lead}
          {!ready ? (
            <p className="text-xs text-muted-foreground" role="status">
              {t("audit.parsingPercent", { percent: 0 })}
            </p>
          ) : null}
          {ready && !preview ? (
            <FormMessage tone="warning">{t("audit.parseFailed")}</FormMessage>
          ) : null}
          {preview?.unparsed ? (
            <FormMessage tone="warning">
              {t("audit.unparsedOutput")}
            </FormMessage>
          ) : null}
          {preview && outputs.length === 0 && failed && lead ? (
            <p
              className="text-xs text-muted-foreground"
              data-testid="audit-no-output"
            >
              {t("audit.noOutputFailed")}
            </p>
          ) : preview && outputs.length === 0 ? (
            <EmptyState
              className="py-8"
              description={
                failed ? t("audit.noOutputFailedHint") : t("audit.noOutput")
              }
              title={
                failed ? t("audit.noOutputFailed") : t("audit.noOutputTitle")
              }
            />
          ) : null}
          <ResponseOutputList outputs={outputs} />
          {preview?.incomplete && outputs.length > 0 ? (
            <p
              className="border-t border-dashed pt-2 text-xs text-warning-foreground"
              data-testid="audit-output-cut"
            >
              {part.truncated
                ? t("audit.outputTruncated")
                : t("audit.outputInterrupted")}
            </p>
          ) : null}
        </div>
      }
    />
  );
}

/** Reply text reads as the answer; thinking and tool calls fold beside it. */
function ResponseOutputList({ outputs }: { outputs: ResponseOutput[] }) {
  const t = i18n.t.bind(i18n);
  return outputs.map((output, index) => {
    if (output.kind === "text")
      return <ResultText key={index} content={output.text} />;
    if (output.kind === "error")
      return (
        <FormMessage className="break-words" key={index} tone="error">
          {output.name ? (
            <span className="block font-medium">{output.name}</span>
          ) : null}
          {output.text}
        </FormMessage>
      );
    if (output.kind === "reasoning")
      return (
        <details className="group" data-output-kind="reasoning" key={index}>
          <summary className="flex cursor-pointer list-none items-center gap-1.5 py-0.5 text-xs font-medium text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
            <ChevronRight className="size-3.5 group-open:rotate-90" />
            {t("audit.outputKinds.reasoning")}
            <span className="font-normal tabular-nums">
              {t("audit.charCount", {
                count: output.text.length.toLocaleString(),
              })}
            </span>
          </summary>
          <div className="mt-1.5 ml-1.5 border-l-2 pl-3 text-muted-foreground">
            <ResultText content={output.text} />
          </div>
        </details>
      );
    const body = prettyJson(output.text);
    return (
      <details
        className="group overflow-hidden rounded-md border bg-card"
        data-output-kind={output.kind}
        key={index}
      >
        <summary className="flex cursor-pointer list-none items-center gap-2 px-2.5 py-1.5 text-xs [&::-webkit-details-marker]:hidden">
          <ChevronRight className="size-3.5 shrink-0 text-muted-foreground group-open:rotate-90" />
          <span className="shrink-0 text-muted-foreground">
            {t(`audit.outputKinds.${output.kind}`)}
          </span>
          {output.name ? (
            <code className="min-w-0 shrink-0 truncate font-mono font-medium">
              {output.name}
            </code>
          ) : null}
          <span className="min-w-0 flex-1 truncate font-mono text-micro text-muted-foreground group-open:invisible">
            {output.text.replace(/\s+/g, " ")}
          </span>
        </summary>
        <pre className="border-t bg-muted/40 px-3 py-2 font-mono text-xs leading-relaxed whitespace-pre-wrap break-words">
          {body}
        </pre>
      </details>
    );
  });
}

function prettyJson(text: string): string {
  if (!looksLikeJson(text) || text.length > 1024 * 1024) return text;
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

function ResultText({ content }: { content: string }) {
  // Captures can span multiple MB. Keep rich-text parsing bounded as well as
  // the wire view; the full reconstructed output remains available to copy.
  const [limit, setLimit] = useState(64 * 1024);
  return (
    <>
      <MarkdownContent content={content.slice(0, limit)} />
      {limit < content.length ? (
        <Button
          className="mt-3"
          variant="outline"
          onClick={() => setLimit((current) => current + 64 * 1024)}
        >
          {i18n.t("audit.loadNextSegment")}
        </Button>
      ) : null}
    </>
  );
}

/** The request line, status and headers, as plain text for the clipboard. */
export function httpMetaText(meta: AuditHTTPMeta): string {
  return [
    `${meta.method} ${meta.url} ${meta.http_version}`.trim(),
    "",
    buildHeadersText(meta.request_headers),
    "",
    meta.response_status !== null ? `HTTP ${meta.response_status}` : "",
    buildHeadersText(meta.response_headers),
  ].join("\n");
}

export function HTTPMetaSection({
  meta,
  copyFeedback,
  title = "HTTP",
  copyKey = "http-meta",
}: {
  meta: AuditHTTPMeta | null;
  copyFeedback: CopyFeedback;
  title?: string;
  copyKey?: string;
}) {
  return (
    <DetailBlock
      actions={
        meta ? (
          <Button
            className="h-auto px-0 text-xs"
            onClick={() => copyFeedback.copy(copyKey, httpMetaText(meta))}
            type="button"
            variant="link"
          >
            {copyButtonLabel(copyFeedback, copyKey)}
          </Button>
        ) : null
      }
      title={title}
    >
      <HTTPMetaDetails meta={meta} />
    </DetailBlock>
  );
}

/** Request line, headers and status without a frame of their own. */
export function HTTPMetaDetails({ meta }: { meta: AuditHTTPMeta | null }) {
  const t = i18n.t.bind(i18n);
  if (meta === null) {
    return (
      <p className="text-xs leading-6 text-muted-foreground">
        {t("audit.noHttpDetail")}
      </p>
    );
  }
  return (
    <div className="grid gap-3">
      <code className="[overflow-wrap:anywhere] block rounded-lg bg-muted px-2.5 py-2 text-xs leading-6 text-text-secondary">
        {meta.method} {meta.url} {meta.http_version}
      </code>
      <HeaderList
        headers={meta.request_headers}
        title={t("audit.requestHeaders")}
      />
      <code className="[overflow-wrap:anywhere] block rounded-lg bg-muted px-2.5 py-2 text-xs leading-6 text-text-secondary">
        {meta.response_status !== null
          ? `HTTP ${meta.response_status}`
          : t("audit.noStatus")}
      </code>
      <HeaderList
        headers={meta.response_headers}
        title={t("audit.responseHeaders")}
      />
    </div>
  );
}

function HeaderList({
  headers,
  title,
}: {
  headers: AuditHTTPMeta["request_headers"];
  title: string;
}) {
  const t = i18n.t.bind(i18n);
  if (headers.length === 0) {
    return (
      <div>
        <h4 className="mb-1.5 text-xs font-medium text-text-secondary">
          {title}
        </h4>
        <p className="text-xs leading-6 text-muted-foreground">
          {t("audit.none")}
        </p>
      </div>
    );
  }
  return (
    <div>
      <h4 className="mb-1.5 text-xs font-medium text-text-secondary">
        {title}
      </h4>
      <ul className="grid list-none gap-1 p-0 font-mono text-xs leading-6 [overflow-wrap:anywhere] text-text-secondary">
        {headers.map((header, index) => (
          <li key={`${header.name}:${index}`}>
            <span className="font-medium text-foreground">{header.name}:</span>{" "}
            <span
              className={
                header.redacted ? "text-warning-foreground" : undefined
              }
              data-redacted={header.redacted || undefined}
            >
              {header.value}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * The hint for a captured body this read leaves out: a raw part waits for
 * an unlock or a raw password, or was never kept without one, so the
 * capture switch is not the reason it is missing.
 */
export function withheldHint(
  withheld: AuditWithheldPart | null | undefined,
): string | null {
  if (!withheld) return null;
  switch (withheld.reason) {
    case "raw_locked":
      return i18n.t("rawSealing.lockedDetail");
    case "raw_not_kept":
      return i18n.t("rawSealing.notKeptDetail");
    default:
      return i18n.t("rawSealing.withheldDetail");
  }
}

export function AuditPartSection({
  title,
  part,
  protocol,
  sectionKey,
  copyFeedback,
  withheld = null,
  onUnlock,
}: {
  title: string;
  part: AuditContentPart | null;
  protocol: string;
  sectionKey: string;
  copyFeedback: CopyFeedback;
  /** Why a captured part is missing from this read, if it is. */
  withheld?: AuditWithheldPart | null;
  /** Opens the raw unlock for a part sealed with the raw key. */
  onUnlock?: () => void;
}) {
  const t = i18n.t.bind(i18n);
  const reason = part === null ? withheld?.reason : undefined;
  const locked = reason === "raw_locked";
  return (
    <DetailBlock
      actions={
        part ? (
          <Button
            className="h-auto px-0 text-xs"
            onClick={() => copyFeedback.copy(sectionKey, part.content)}
            type="button"
            variant="link"
          >
            {copyButtonLabel(copyFeedback, sectionKey)}
          </Button>
        ) : locked && onUnlock ? (
          <Button
            className="h-auto px-0 text-xs"
            onClick={onUnlock}
            type="button"
            variant="link"
          >
            {t("rawSealing.unlock")}
          </Button>
        ) : null
      }
      title={title}
    >
      {part !== null ? (
        <AuditPartView part={part} protocol={protocol} />
      ) : withheld ? (
        <div
          className="flex flex-wrap items-center gap-2 text-xs leading-6 text-muted-foreground"
          data-slot="audit-part-withheld"
        >
          {locked ? (
            <StatusBadge tone="neutral">{t("rawSealing.locked")}</StatusBadge>
          ) : reason === "raw_not_kept" ? (
            <StatusBadge tone="neutral">{t("rawSealing.notKept")}</StatusBadge>
          ) : null}
          <span>{withheldHint(withheld)}</span>
          <span>{formatBytes(withheld.captured_bytes)}</span>
        </div>
      ) : (
        <p className="text-xs leading-6 text-muted-foreground">
          {t("audit.uncapturedDetail")}
        </p>
      )}
    </DetailBlock>
  );
}

function DetailBlock({
  title,
  actions,
  children,
}: {
  title: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-md border bg-card p-3.5">
      <header className="mb-2.5 flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium">{title}</h3>
        {actions}
      </header>
      {children}
    </section>
  );
}

function AuditPartView({
  part,
  protocol,
}: {
  part: AuditContentPart;
  protocol: string;
}) {
  const t = i18n.t.bind(i18n);
  const isStream = part.media_type.toLowerCase().includes("text/event-stream");
  return (
    <div>
      <div className="mb-2.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>{part.media_type}</span>
        <span>{formatBytes(part.captured_bytes)}</span>
        {part.truncated ? (
          <Badge
            className="bg-warning-wash text-warning-foreground"
            variant="secondary"
          >
            {t("audit.truncatedBadge")}
          </Badge>
        ) : null}
      </div>
      {isStream ? (
        <StreamInspector part={part} protocol={protocol} />
      ) : (
        <DocumentInspector part={part} />
      )}
    </div>
  );
}

function StreamInspector({
  part,
}: {
  part: AuditContentPart;
  protocol: string;
}) {
  const [mode, setMode] = useState<StreamViewMode>("raw");
  const [events, setEvents] = useState<SSEEvent[]>([]);
  const [parseState, setParseState] = useState<
    "idle" | "parsing" | "ready" | "cancelled" | "error"
  >("idle");
  const [parseProgress, setParseProgress] = useState(0);
  const [parseSummary, setParseSummary] = useState({
    invalidJsonCount: 0,
    incompleteLastEvent: false,
  });

  // Events parse lazily: the raw view is the default and must not pay the
  // multi-MB parse cost, so parsing starts only when the tab is opened.
  useEffect(() => {
    if (mode !== "events" || parseState !== "idle") return;
    const controller = new AbortController();
    setParseState("parsing");
    setParseProgress(0);
    void parseSSEIncremental(part.content, {
      signal: controller.signal,
      truncated: part.truncated,
      onProgress: (progress) => {
        setEvents(progress.events);
        setParseProgress(
          progress.totalCharacters === 0
            ? 1
            : progress.processedCharacters / progress.totalCharacters,
        );
      },
    })
      .then((result) => {
        if (controller.signal.aborted) return;
        setEvents(result.events);
        setParseSummary({
          invalidJsonCount: result.invalidJsonCount,
          incompleteLastEvent: result.incompleteLastEvent,
        });
        setParseProgress(1);
        setParseState("ready");
      })
      .catch((error: unknown) => {
        if (error instanceof SSEParseCancelledError) {
          setParseState("cancelled");
          return;
        }
        setParseState("error");
      });
    return () => controller.abort();
  }, [mode, parseState, part.content, part.truncated]);

  const t = i18n.t.bind(i18n);
  return (
    <Tabs
      value={mode}
      onValueChange={(value) => setMode(value as StreamViewMode)}
    >
      <div className="mb-2.5 flex items-center justify-between gap-3 max-[720px]:items-stretch max-[720px]:flex-col">
        <TabsList aria-label={t("audit.streamView")}>
          <ModeTab
            active={mode === "events"}
            label={t("audit.formatted")}
            value="events"
          />
          <ModeTab
            active={mode === "raw"}
            label={t("audit.original")}
            value="raw"
          />
        </TabsList>
        {parseState !== "idle" ? (
          <ParseStatus
            progress={parseProgress}
            state={parseState}
            summary={parseSummary}
          />
        ) : null}
      </div>
      <TabsContent value="raw">
        <RawSegmentView content={part.content} />
      </TabsContent>
      <TabsContent value="events">
        <EventsView events={events} parsing={parseState === "parsing"} />
      </TabsContent>
    </Tabs>
  );
}

function ModeTab({
  active,
  label,
  value,
}: {
  active: boolean;
  label: string;
  value: string;
}) {
  return (
    <TabsTrigger aria-selected={active} value={value}>
      {label}
    </TabsTrigger>
  );
}

function ParseStatus({
  state,
  progress,
  summary,
}: {
  state: "idle" | "parsing" | "ready" | "cancelled" | "error";
  progress: number;
  summary: { invalidJsonCount: number; incompleteLastEvent: boolean };
}) {
  const t = i18n.t.bind(i18n);
  if (state === "parsing") {
    return (
      <Badge variant="secondary" role="status">
        {t("audit.parsingPercent", { percent: Math.round(progress * 100) })}
      </Badge>
    );
  }
  if (state === "error") {
    return (
      <Badge
        className="bg-danger-wash text-danger-foreground"
        variant="secondary"
      >
        {t("audit.parseFailed")}
      </Badge>
    );
  }
  if (state === "cancelled") {
    return <Badge variant="secondary">{t("audit.parseCancelled")}</Badge>;
  }
  if (summary.invalidJsonCount > 0 || summary.incompleteLastEvent) {
    return (
      <Badge
        className="bg-warning-wash text-warning-foreground"
        variant="secondary"
      >
        {summary.invalidJsonCount > 0
          ? t("audit.invalidJson", { count: summary.invalidJsonCount })
          : ""}
        {summary.invalidJsonCount > 0 && summary.incompleteLastEvent
          ? " · "
          : ""}
        {summary.incompleteLastEvent ? t("audit.incompleteTail") : ""}
      </Badge>
    );
  }
  return <Badge variant="secondary">{t("audit.parseDone")}</Badge>;
}

function EventsView({
  events,
  parsing,
}: {
  events: SSEEvent[];
  parsing: boolean;
}) {
  const [query, setQuery] = useState("");
  const [type, setType] = useState("");
  const [renderLimit, setRenderLimit] = useState(EVENT_RENDER_BATCH);
  const types = useMemo(
    () => [...new Set(events.map((event) => event.type))].sort(),
    [events],
  );
  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return events.filter(
      (event) =>
        (!type || event.type === type) &&
        (!normalized ||
          event.type.toLowerCase().includes(normalized) ||
          event.data.toLowerCase().includes(normalized)),
    );
  }, [events, query, type]);

  useEffect(() => setRenderLimit(EVENT_RENDER_BATCH), [query, type]);

  const t = i18n.t.bind(i18n);
  return (
    <div>
      <div className="mb-3 flex items-end gap-2.5 max-[720px]:items-stretch max-[720px]:flex-col">
        <div className="grid gap-1.5">
          <Label htmlFor="audit-event-search">{t("audit.searchEvents")}</Label>
          <Input
            id="audit-event-search"
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder={t("audit.typeOrContent")}
            type="search"
            value={query}
          />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="audit-event-type">{t("audit.eventType")}</Label>
          <Select
            onValueChange={(value) => setType(value === "__all__" ? "" : value)}
            value={type}
          >
            <SelectTrigger
              id="audit-event-type"
              className="min-w-36 max-[720px]:w-full"
            >
              <SelectValue placeholder={t("audit.allTypes")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all__">{t("audit.allTypes")}</SelectItem>
              {types.map((eventType) => (
                <SelectItem key={eventType} value={eventType}>
                  {eventType}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <span className="ml-auto pb-2 text-xs text-muted-foreground max-[720px]:ml-0 max-[720px]:pb-0">
          {t("audit.matchCount", { count: filtered.length })}
          {parsing ? t("audit.stillParsing") : ""}
        </span>
      </div>
      <div className="grid gap-2">
        {filtered.slice(0, renderLimit).map((event) => (
          <EventCard event={event} key={event.index} />
        ))}
      </div>
      {renderLimit < filtered.length ? (
        <Button
          className="mt-3 w-full"
          variant="outline"
          onClick={() =>
            setRenderLimit((current) => current + EVENT_RENDER_BATCH)
          }
          type="button"
        >
          {t("audit.showMoreEvents", {
            count: Math.min(EVENT_RENDER_BATCH, filtered.length - renderLimit),
          })}
        </Button>
      ) : null}
    </div>
  );
}

function EventCard({ event }: { event: SSEEvent }) {
  const t = i18n.t.bind(i18n);
  const [open, setOpen] = useState(false);
  return (
    <details
      className="group overflow-hidden rounded-md border bg-card"
      data-testid="audit-event"
      onToggle={(change) => setOpen(change.currentTarget.open)}
    >
      <summary className="grid cursor-pointer list-none grid-cols-[44px_minmax(0,1fr)_auto_auto] items-center gap-2 px-2.5 py-2 text-xs [&::-webkit-details-marker]:hidden max-[720px]:grid-cols-[36px_minmax(0,1fr)_auto]">
        <span className="text-muted-foreground">#{event.index}</span>
        <strong className="overflow-hidden text-xs text-ellipsis whitespace-nowrap">
          {event.type}
        </strong>
        {event.invalidJson ? (
          <em className="text-warning-foreground not-italic max-[720px]:hidden">
            {t("audit.invalidJsonBadge")}
          </em>
        ) : null}
        {event.incomplete ? (
          <em className="text-warning-foreground not-italic max-[720px]:hidden">
            {t("audit.incompleteEvent")}
          </em>
        ) : null}
        <small className="text-muted-foreground">
          {t("audit.charCount", { count: event.data.length.toLocaleString() })}
        </small>
      </summary>
      {open ? (
        <EventData
          className="max-h-[440px] overflow-auto border-t bg-muted/40 p-3"
          event={event}
        />
      ) : null}
    </details>
  );
}

export type WireViewMode = "structured" | "raw";

/**
 * Label for the structured view of a body, or null when only raw applies.
 * A stream and a JSON document share the name: both lay their JSON out as
 * the same tree, a stream one event at a time.
 */
export function wireStructuredLabel(part: AuditContentPart): string | null {
  // Decided without parsing: the host asks on every render.
  return isEventStream(part) || jsonCandidate(part)
    ? i18n.t("audit.formatted")
    : null;
}

/**
 * One captured body inside a host that owns the only scroller: stream events
 * as compact rows, JSON as a foldable tree, or the original text. `find`
 * brings its hits into view in whichever of those is showing.
 */
export function AuditWireView({
  part,
  mode,
  revealPrivacy = false,
  find,
}: {
  part: AuditContentPart;
  mode: WireViewMode;
  /** Unfolds the strings that carry privacy placeholders. */
  revealPrivacy?: boolean;
  find?: FindRequest;
}) {
  const t = i18n.t.bind(i18n);
  const structured = mode === "structured";
  const stream = isEventStream(part);
  const tree = useJsonTree(part.content, structured && jsonCandidate(part));
  const root = usableTreeRoot(tree);
  return (
    <div className="grid gap-2">
      {part.truncated ? (
        <FormMessage tone="warning">{t("audit.truncatedNote")}</FormMessage>
      ) : null}
      {structured && stream ? (
        <StreamEventList find={find} part={part} />
      ) : tree?.status === "parsing" ? (
        <JsonParseProgress progress={tree.progress} />
      ) : root ? (
        <JsonTreeView
          className="rounded-lg bg-muted/40 p-3"
          find={find}
          renderText={privacyText}
          revealText={revealPrivacy ? hasPrivacyHighlight : undefined}
          root={root}
        />
      ) : (
        <>
          {tree ? <InvalidJsonBadge /> : null}
          <RawSegmentView bounded={false} content={part.content} find={find} />
        </>
      )}
    </div>
  );
}

/**
 * With `find`, the list narrows to the events that hold the query, as its
 * own filter does, and steps through them one event at a time: the active
 * event opens with its hits marked.
 */
function StreamEventList({
  part,
  find,
}: {
  part: AuditContentPart;
  find?: FindRequest;
}) {
  const [events, setEvents] = useState<SSEEvent[]>([]);
  const [parseState, setParseState] = useState<
    "parsing" | "ready" | "cancelled" | "error"
  >("parsing");
  const [parseProgress, setParseProgress] = useState(0);
  const [parseSummary, setParseSummary] = useState({
    invalidJsonCount: 0,
    incompleteLastEvent: false,
  });
  const [query, setQuery] = useState("");
  const [renderLimit, setRenderLimit] = useState(EVENT_RENDER_BATCH);

  useEffect(() => {
    const controller = new AbortController();
    setParseState("parsing");
    setParseProgress(0);
    setEvents([]);
    void parseSSEIncremental(part.content, {
      signal: controller.signal,
      truncated: part.truncated,
      onProgress: (progress) => {
        setEvents(progress.events);
        setParseProgress(
          progress.totalCharacters === 0
            ? 1
            : progress.processedCharacters / progress.totalCharacters,
        );
      },
    })
      .then((result) => {
        if (controller.signal.aborted) return;
        setEvents(result.events);
        setParseSummary({
          invalidJsonCount: result.invalidJsonCount,
          incompleteLastEvent: result.incompleteLastEvent,
        });
        setParseProgress(1);
        setParseState("ready");
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setParseState(
          error instanceof SSEParseCancelledError ? "cancelled" : "error",
        );
      });
    return () => controller.abort();
  }, [part.content, part.truncated]);

  const activeQuery = find ? find.query : query;
  const matcher = useMemo(() => findMatcher(activeQuery), [activeQuery]);
  const filtered = useMemo(() => {
    if (!matcher) return events;
    return events.filter((event) => eventMatches(event, matcher));
  }, [events, matcher]);
  useEffect(() => setRenderLimit(EVENT_RENDER_BATCH), [activeQuery]);

  const onResult = find?.onResult;
  const finding = find !== undefined && matcher !== null;
  useEffect(() => {
    if (!finding) return;
    onResult?.({ count: filtered.length, capped: false });
  }, [filtered, finding, onResult]);
  const activePosition = finding
    ? Math.min(find.active, Math.max(0, filtered.length - 1))
    : -1;
  const activeEvent = filtered[activePosition] ?? null;
  // The active event has to be rendered before it can be opened.
  useEffect(() => {
    if (activePosition < 0) return;
    setRenderLimit((current) =>
      activePosition < current
        ? current
        : Math.ceil((activePosition + 1) / EVENT_RENDER_BATCH) *
          EVENT_RENDER_BATCH,
    );
  }, [activePosition]);
  const listRef = useRef<HTMLOListElement>(null);
  useFindScroll(listRef, activeEvent, find?.seq ?? 0);
  // The active event marks its own hits and brings the first into view.
  const seq = find?.seq ?? 0;
  const eventFind = useMemo<FindRequest | undefined>(
    () =>
      finding
        ? { query: activeQuery, active: 0, seq, onResult: ignoreFindResult }
        : undefined,
    [activeQuery, finding, seq],
  );

  const t = i18n.t.bind(i18n);
  const showStatus =
    parseState !== "ready" ||
    parseSummary.invalidJsonCount > 0 ||
    parseSummary.incompleteLastEvent;
  return (
    <div className="grid gap-2">
      {!find && events.length > EVENT_FILTER_THRESHOLD ? (
        <div className="flex items-center gap-2">
          <Input
            aria-label={t("audit.searchEvents")}
            className="h-7 min-w-0 flex-1 text-xs"
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder={t("audit.filterEvents")}
            type="search"
            value={query}
          />
          <span className="shrink-0 text-micro text-muted-foreground tabular-nums">
            {query.trim()
              ? `${filtered.length.toLocaleString()} / ${events.length.toLocaleString()}`
              : events.length.toLocaleString()}
          </span>
        </div>
      ) : null}
      {showStatus ? (
        <div>
          <ParseStatus
            progress={parseProgress}
            state={parseState}
            summary={parseSummary}
          />
        </div>
      ) : null}
      {parseState === "ready" && events.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t("audit.noEvents")}</p>
      ) : null}
      {filtered.length > 0 ? (
        <ol
          className="divide-y overflow-hidden rounded-md border"
          ref={listRef}
        >
          {filtered.slice(0, renderLimit).map((event) => (
            <EventRow
              active={event === activeEvent}
              event={event}
              find={event === activeEvent ? eventFind : undefined}
              key={event.index}
              hitLength={findNeedle(activeQuery).length}
              matcher={finding ? matcher : null}
            />
          ))}
        </ol>
      ) : null}
      {renderLimit < filtered.length ? (
        <Button
          className="w-full"
          variant="outline"
          onClick={() =>
            setRenderLimit((current) => current + EVENT_RENDER_BATCH)
          }
          type="button"
        >
          {t("audit.showMoreEvents", {
            count: Math.min(EVENT_RENDER_BATCH, filtered.length - renderLimit),
          })}
        </Button>
      ) : null}
    </div>
  );
}

function ignoreFindResult() {}

/** Whether find has something to show in an event: its type or its data. */
function eventMatches(event: SSEEvent, matcher: RegExp): boolean {
  if (findMatches(event.type, matcher)) return true;
  return event.json === null
    ? findMatches(eventText(event), matcher)
    : jsonValueMatches(event.json, matcher);
}

/**
 * Searched the way the tree shows it: each key and each value on its own,
 * unescaped, with inline files left out.
 */
function jsonValueMatches(value: unknown, matcher: RegExp): boolean {
  if (Array.isArray(value))
    return value.some((item) => jsonValueMatches(item, matcher));
  if (value !== null && typeof value === "object")
    return Object.entries(value).some(
      ([key, item]) =>
        findMatches(jsonKeyText(key), matcher) ||
        jsonValueMatches(item, matcher),
    );
  if (typeof value === "string")
    return detectBase64(value) === null && findMatches(value, matcher);
  return findMatches(String(value), matcher);
}

function eventText(event: SSEEvent): string {
  return event.data || i18n.t("audit.emptyData");
}

/**
 * An opened event's data: the same JSON tree a JSON body gets, or the data
 * as sent when it is not JSON. Built on opening, so a long stream pays only
 * for the events someone reads.
 */
function EventData({
  event,
  find,
  className,
}: {
  event: SSEEvent;
  find?: FindRequest;
  className?: string;
}) {
  const root = useMemo(
    () => (event.json === null ? null : parseJsonTree(event.data).root),
    [event],
  );
  if (root)
    return (
      <JsonTreeView
        className={className}
        find={find}
        renderText={privacyText}
        root={root}
        toolbar={false}
      />
    );
  const text = eventText(event);
  const matcher = find ? findMatcher(find.query) : null;
  const hits = matcher ? findOffsets(text, matcher, FIND_HIT_LIMIT) : undefined;
  return (
    <pre
      className={cn(
        "font-mono text-xs leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]",
        className,
      )}
    >
      {markText(
        text,
        hits,
        findNeedle(find?.query ?? "").length,
        hits?.[0] ?? null,
      )}
    </pre>
  );
}

/**
 * `active` holds the event open while find is on it; moving on lets it
 * close again, so stepping through hits does not leave a trail of open rows.
 */
function EventRow({
  event,
  active = false,
  find,
  matcher = null,
  hitLength = 0,
}: {
  event: SSEEvent;
  active?: boolean;
  /** Marks the hits in the data of the event find is on. */
  find?: FindRequest;
  matcher?: RegExp | null;
  hitLength?: number;
}) {
  const t = i18n.t.bind(i18n);
  const [opened, setOpened] = useState(false);
  const failure = isFailureEvent(event);
  const preview = eventPreview(event);
  const hits = (text: string) =>
    matcher ? findOffsets(text, matcher, FIND_HIT_LIMIT) : undefined;
  return (
    <li>
      <details
        className="group"
        data-find-active={active ? "true" : undefined}
        data-testid="audit-event"
        onToggle={(change) => setOpened(change.currentTarget.open)}
        open={active || undefined}
      >
        <summary
          className="flex cursor-pointer list-none items-center gap-2 px-2.5 py-1.5 text-xs hover:bg-muted/50 [&::-webkit-details-marker]:hidden"
          title={event.type}
        >
          <span className="w-6 shrink-0 text-right text-micro text-muted-foreground tabular-nums">
            {event.index}
          </span>
          <code
            className={cn(
              "min-w-0 max-w-[55%] shrink-0 truncate font-mono font-medium",
              failure && "text-danger-foreground",
            )}
          >
            {markText(event.type, hits(event.type), hitLength)}
          </code>
          <span
            className={cn(
              "min-w-0 flex-1 truncate text-muted-foreground",
              failure && "text-danger-foreground",
            )}
          >
            {markText(preview, hits(preview), hitLength)}
          </span>
          {event.invalidJson || event.incomplete ? (
            <span className="shrink-0 text-micro text-warning-foreground">
              {event.invalidJson
                ? t("audit.invalidJsonBadge")
                : t("audit.incompleteEvent")}
            </span>
          ) : null}
        </summary>
        {active || opened ? (
          <EventData
            className="border-t bg-muted/40 px-3 py-2"
            event={event}
            find={find}
          />
        ) : null}
      </details>
    </li>
  );
}

type JsonObject = Record<string, unknown>;
const jsonObject = (value: unknown): JsonObject =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as JsonObject)
    : {};
const jsonString = (value: unknown): string =>
  typeof value === "string" ? value : "";

function eventError(event: SSEEvent): JsonObject {
  const data = jsonObject(event.json);
  return jsonObject(data.error ?? jsonObject(data.response).error);
}

function isFailureEvent(event: SSEEvent): boolean {
  return (
    /(?:^|[._])(?:error|failed)$/.test(event.type) ||
    Object.keys(eventError(event)).length > 0
  );
}

/** The one line of an event worth reading before expanding it. */
function eventPreview(event: SSEEvent): string {
  if (event.done) return "[DONE]";
  if (event.json === null) return event.data;
  const data = jsonObject(event.json);
  const error = eventError(event);
  const message = jsonString(error.message) || jsonString(data.message);
  if (message) {
    const code = jsonString(error.code) || jsonString(error.type);
    return code ? `${code}: ${message}` : message;
  }
  const delta = data.delta;
  const deltaObject = jsonObject(delta);
  const choiceDelta = jsonObject(
    jsonObject((Array.isArray(data.choices) ? data.choices : [])[0]).delta,
  );
  const item = jsonObject(data.item ?? data.content_block);
  const response = jsonObject(data.response);
  const text =
    jsonString(delta) ||
    jsonString(deltaObject.text) ||
    jsonString(deltaObject.thinking) ||
    jsonString(deltaObject.partial_json) ||
    jsonString(choiceDelta.content) ||
    jsonString(choiceDelta.reasoning_content) ||
    jsonString(data.text) ||
    jsonString(data.arguments);
  if (text) return text.replace(/\s+/g, " ");
  if (item.type)
    return [jsonString(item.type), jsonString(item.name)]
      .filter(Boolean)
      .join(" · ");
  if (response.status) return jsonString(response.status);
  return event.data.replace(/\s+/g, " ").slice(0, 200);
}

function isEventStream(part: AuditContentPart): boolean {
  return part.media_type.toLowerCase().includes("text/event-stream");
}

/** A body worth offering as a JSON tree, judged without parsing it. */
function jsonCandidate(part: AuditContentPart): boolean {
  return (
    !isEventStream(part) &&
    part.content.length <= JSON_TREE_MAX_CHARS &&
    (part.media_type.toLowerCase().includes("json") ||
      looksLikeJson(part.content))
  );
}

type JsonTreeState =
  | { status: "parsing"; progress: number }
  | { status: "ready"; parse: JsonTreeParse }
  | { status: "failed" };

/**
 * Parses a JSON body into a tree: at once when small, in slices when large
 * so the window keeps responding. Null when `enabled` is off.
 */
function useJsonTree(content: string, enabled: boolean): JsonTreeState | null {
  const small = enabled && content.length <= JSON_TREE_SYNC_CHARS;
  const parsed = useMemo(
    () => (small ? parseJsonTree(content) : null),
    [content, small],
  );
  const [sliced, setSliced] = useState<{
    content: string;
    state: JsonTreeState;
  } | null>(null);
  useEffect(() => {
    if (!enabled || small) return;
    const controller = new AbortController();
    void parseJsonTreeIncremental(content, {
      signal: controller.signal,
      onProgress: (progress) =>
        setSliced({ content, state: { status: "parsing", progress } }),
    })
      .then((parse) => {
        if (controller.signal.aborted) return;
        setSliced({ content, state: { status: "ready", parse } });
      })
      .catch((error: unknown) => {
        if (error instanceof JsonTreeCancelledError) return;
        setSliced({ content, state: { status: "failed" } });
      });
    return () => controller.abort();
  }, [content, enabled, small]);
  if (!enabled) return null;
  if (parsed) return { status: "ready", parse: parsed };
  return sliced?.content === content
    ? sliced.state
    : { status: "parsing", progress: 0 };
}

/** The tree to show, or null when the text stopped being JSON before its end. */
function usableTreeRoot(state: JsonTreeState | null): JsonNode | null {
  if (state?.status !== "ready" || state.parse.errorAt !== null) return null;
  return state.parse.root;
}

function JsonParseProgress({ progress }: { progress: number }) {
  return (
    <Badge className="justify-self-start" role="status" variant="secondary">
      {i18n.t("audit.parsingPercent", { percent: Math.round(progress * 100) })}
    </Badge>
  );
}

function InvalidJsonBadge() {
  return (
    <Badge
      className="justify-self-start bg-warning-wash text-warning-foreground"
      variant="secondary"
    >
      {i18n.t("audit.invalidJsonBadge")}
    </Badge>
  );
}

function DocumentInspector({ part }: { part: AuditContentPart }) {
  const candidate = jsonCandidate(part);
  const tree = useJsonTree(part.content, candidate);
  const root = usableTreeRoot(tree);
  const invalid = tree !== null && tree.status !== "parsing" && root === null;
  const [chosen, setChosen] = useState<DocumentViewMode>(
    candidate ? "formatted" : "raw",
  );
  const mode = invalid ? "raw" : chosen;

  const t = i18n.t.bind(i18n);
  return (
    <Tabs
      value={mode}
      onValueChange={(value) => setChosen(value as DocumentViewMode)}
    >
      <div className="mb-2.5 flex items-center justify-between gap-3">
        <TabsList aria-label={t("audit.contentView")}>
          <TabsTrigger disabled={!candidate || invalid} value="formatted">
            {t("audit.formatted")}
          </TabsTrigger>
          <TabsTrigger value="raw">{t("audit.original")}</TabsTrigger>
        </TabsList>
        {invalid ? (
          <InvalidJsonBadge />
        ) : tree?.status === "parsing" ? (
          <JsonParseProgress progress={tree.progress} />
        ) : null}
      </div>
      <TabsContent value="formatted">
        {root ? (
          <JsonTreeView
            className="max-h-[520px] overflow-auto rounded-lg bg-muted/40 p-3"
            renderText={privacyText}
            root={root}
          />
        ) : null}
      </TabsContent>
      <TabsContent value="raw">
        <RawSegmentView content={part.content} />
      </TabsContent>
    </Tabs>
  );
}

/**
 * `bounded` caps each segment with its own scroller for hosts that stack
 * several sections; a host that already scrolls one pane turns it off.
 * `find` searches every segment and loads the one its active hit is in.
 */
function RawSegmentView({
  content,
  bounded = true,
  find,
}: {
  content: string;
  bounded?: boolean;
  find?: FindRequest;
}) {
  const totalSegments = Math.max(
    1,
    Math.ceil(content.length / RAW_SEGMENT_SIZE),
  );
  const [visibleSegments, setVisibleSegments] = useState(1);
  const query = find?.query ?? "";
  const length = findNeedle(query).length;
  const matcher = useMemo(() => findMatcher(query), [query]);
  const hits = useMemo(
    () => (matcher ? findOffsets(content, matcher, FIND_HIT_LIMIT + 1) : null),
    [content, matcher],
  );
  const onResult = find?.onResult;
  useEffect(() => {
    if (!hits) return;
    onResult?.({
      count: Math.min(hits.length, FIND_HIT_LIMIT),
      capped: hits.length > FIND_HIT_LIMIT,
    });
  }, [hits, onResult]);
  const hitCount = Math.min(hits?.length ?? 0, FIND_HIT_LIMIT);
  const activeHit =
    hitCount > 0 ? hits![Math.min(find?.active ?? 0, hitCount - 1)]! : null;
  useEffect(() => {
    if (activeHit === null) return;
    const segment = Math.floor(activeHit / RAW_SEGMENT_SIZE) + 1;
    setVisibleSegments((current) => Math.max(current, segment));
  }, [activeHit]);
  const rawRef = useRef<HTMLDivElement>(null);
  useFindScroll(rawRef, activeHit, find?.seq ?? 0);
  const segments = [];
  for (
    let index = 0;
    index < Math.min(totalSegments, visibleSegments);
    index += 1
  ) {
    const start = index * RAW_SEGMENT_SIZE;
    segments.push({
      index,
      start,
      end: Math.min(content.length, start + RAW_SEGMENT_SIZE),
      text: content.slice(start, start + RAW_SEGMENT_SIZE),
    });
  }
  const t = i18n.t.bind(i18n);
  return (
    <div data-testid="audit-raw" ref={rawRef}>
      <div className="mb-2 flex items-center justify-between gap-3 text-xs text-muted-foreground max-[720px]:items-start max-[720px]:flex-col">
        <span>
          {t("audit.rawFull", {
            chars: content.length.toLocaleString(),
            segments: totalSegments,
          })}
        </span>
        {totalSegments > 1 ? <span>{t("audit.segmentHint")}</span> : null}
      </div>
      {segments.map((segment) => (
        <section
          className="mt-2 overflow-hidden rounded-lg border"
          data-testid="audit-raw-segment"
          key={segment.index}
        >
          {totalSegments > 1 ? (
            <header className="border-b bg-muted px-3 py-2 text-xs text-muted-foreground">
              {t("audit.segmentHeader", {
                index: segment.index + 1,
                start: segment.start.toLocaleString(),
                end: segment.end.toLocaleString(),
              })}
            </header>
          ) : null}
          <HighlightedAuditText
            className={cn(
              "bg-muted/40 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap",
              bounded
                ? "max-h-[520px] overflow-auto"
                : "[overflow-wrap:anywhere]",
            )}
            content={segment.text}
            hitLength={length}
            hits={hits
              ?.slice(0, FIND_HIT_LIMIT)
              .filter((hit) => hit >= segment.start && hit < segment.end)
              .map((hit) => hit - segment.start)}
            activeHit={
              activeHit !== null &&
              activeHit >= segment.start &&
              activeHit < segment.end
                ? activeHit - segment.start
                : null
            }
          />
        </section>
      ))}
      {visibleSegments < totalSegments ? (
        <Button
          className="mt-3 w-full"
          variant="outline"
          onClick={() => setVisibleSegments((current) => current + 1)}
          type="button"
        >
          {t("audit.loadNextSegment")}
        </Button>
      ) : null}
    </div>
  );
}

function HighlightedAuditText({
  content,
  className = "max-h-[520px] overflow-auto rounded-lg bg-muted p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap",
  hits,
  hitLength = 0,
  activeHit = null,
}: {
  content: string;
  className?: string;
  /** Find hits, as offsets into `content`. */
  hits?: number[];
  hitLength?: number;
  activeHit?: number | null;
}) {
  return (
    <pre className={className}>
      {markText(content, hits, hitLength, activeHit, privacyText)}
    </pre>
  );
}

/** Text with each privacy placeholder marked so the reviewer can find it. */
function privacyText(text: string): ReactNode {
  return splitPrivacyHighlights(text).map((span, index) =>
    span.kind ? (
      <mark
        className="rounded-sm bg-warning-wash px-0.5 text-warning-foreground"
        data-kind={span.kind}
        data-placeholder={span.text}
        data-testid="privacy-mark"
        key={`${span.text}:${index}`}
      >
        {span.text}
      </mark>
    ) : (
      <span key={index}>{span.text}</span>
    ),
  );
}

function hasPrivacyHighlight(text: string): boolean {
  return splitPrivacyHighlights(text).some((span) => span.kind !== undefined);
}

function looksLikeJson(content: string): boolean {
  return /^\s*[{[]/.test(content);
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
