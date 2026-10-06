import {
  createContext,
  useCallback,
  useContext,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type Ref,
} from "react";

import { Check, Copy } from "@/components/icons";
import { ActionGroup } from "@/components/ActionGroup";
import { FindBar } from "@/components/FindBar";
import { FormMessage } from "@/components/FormMessage";
import { ModelLabel } from "@/components/ModelLabel";
import { Panel } from "@/components/Panel";
import { SegmentedControl } from "@/components/SegmentedControl";
import { StatusBadge } from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import {
  AuditResultSection,
  AuditWireView,
  HTTPMetaDetails,
  httpMetaText,
  responseErrors,
  useResponsePreview,
  wireStructuredLabel,
  withheldHint,
  type WireViewMode,
} from "./AuditReviewer";
import { copyButtonLabel, type CopyFeedback } from "./copy-feedback";
import {
  findShortcutLabel,
  findStepShortcut,
  isFindShortcut,
  stepFind,
  type FindRequest,
  type FindResult,
} from "./find-model";
import { formatExactNumber } from "./format-compact-number";
import { i18n, useT } from "./i18n";
import {
  statusLabel,
  statusTone,
  type AuditContent,
  type AuditContentPart,
  type AuditHTTPMeta,
  type AuditWithheldPart,
  type RequestRecord,
  type RequestStatus,
} from "./request-record-model";
import { formatDuration } from "./request-live-model";
import type { ResponseOutput } from "./response-preview-model";
import type { TrajectoryRow } from "./request-trajectory-model";

// The body pane takes the remaining height but keeps enough to read and to
// reach its view controls; below that the inspector body scrolls instead. A
// fixed basis keeps the pane's content from sizing the scrolled section.
const PANE_CLASS = "flex min-h-60 grow basis-60 flex-col";

/**
 * True where the inspector owns its window, so ⌘F finds in the body on
 * screen. Docked over the trajectory list, the list's own find keeps it.
 */
export const InspectorFindShortcut = createContext(false);

/**
 * What the client got: the outcome in one line, then the response pane led by
 * the reason it failed and the reconstructed output. The reason leads with
 * what the provider reported; the gateway's classification follows as
 * context, not as a second alarm.
 */
export function ResultInspector({
  record,
  part,
  missingHint,
}: {
  record: RequestRecord;
  part: AuditContentPart | null;
  /** Shown instead of the output when nothing was captured; null while loading. */
  missingHint: string | null;
}) {
  const preview = useResponsePreview(part);
  const failed = isUnsuccessful(record.status);
  const diagnosis = (
    <Diagnosis
      errors={responseErrors(preview)}
      record={record}
      tone={failed ? "error" : "warning"}
    />
  );
  return (
    <div className="flex flex-1 flex-col gap-2">
      <OutcomeFacts
        facts={[
          httpFact(record.http_status),
          record.latency_ms !== null
            ? { label: "duration", value: formatDuration(record.latency_ms) }
            : null,
          record.first_token_ms != null
            ? { label: "ttft", value: formatDuration(record.first_token_ms) }
            : null,
          tokenFact(record),
          part ? sizeFact(part) : null,
        ]}
        status={record.status}
      />
      {part ? (
        <div className={PANE_CLASS}>
          <AuditResultSection
            failed={failed}
            hideErrors
            lead={diagnosis}
            part={part}
            preview={preview}
          />
        </div>
      ) : (
        <>
          {diagnosis}
          {missingHint ? (
            <p
              className="text-xs leading-6 text-muted-foreground"
              data-testid="inspector-missing-body"
            >
              {missingHint}
            </p>
          ) : null}
        </>
      )}
    </div>
  );
}

/**
 * The exchange with the provider: its status and timing, then one bounded
 * pane for the response (led by the error it sent), the request and the HTTP
 * metadata.
 */
export function UpstreamInspector({
  record,
  row,
  auditContent,
  copyFeedback,
  missingHint,
}: {
  record: RequestRecord;
  row: TrajectoryRow;
  auditContent: AuditContent | null;
  copyFeedback: CopyFeedback;
  missingHint: string | null;
}) {
  const t = useT();
  const response = auditContent?.upstream_response_content ?? null;
  const request = auditContent?.upstream_request_body ?? null;
  const meta = auditContent?.upstream_http_meta ?? null;
  const preview = useResponsePreview(response);
  const errors = responseErrors(preview);
  const upstreamModel =
    record.recovery?.upstream_model ?? record.model_redirect?.to ?? null;
  const same = sameContent(response, auditContent?.response_content ?? null);

  return (
    <div className="flex flex-1 flex-col gap-2">
      <OutcomeFacts
        facts={[
          httpFact(meta?.response_status ?? record.http_status),
          rowDuration(row),
          response ? sizeFact(response) : null,
        ]}
        note={same ? t("audit.sameAsClient") : null}
        status={row.status}
      />
      {meta || upstreamModel ? (
        <div className="flex min-w-0 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          {upstreamModel && upstreamModel !== record.requested_model ? (
            <span className="flex min-w-0 items-center gap-1.5">
              <span className="shrink-0 text-muted-foreground">
                {t("failure.upstreamModel")}
              </span>
              <ModelLabel model={upstreamModel} />
            </span>
          ) : null}
          {meta ? (
            <EndpointLine
              method={meta.method}
              testId="inspector-upstream-endpoint"
              url={meta.url}
            />
          ) : null}
        </div>
      ) : null}
      <CapturePane
        copyFeedback={copyFeedback}
        copyKey={`upstream:${record.id}`}
        label={t("trajectory.upstreamResponse")}
        missingHint={missingHint}
        testId="inspector-upstream-body"
        viewsLabel={t("audit.upstreamView")}
        views={[
          {
            value: "response",
            label: t("audit.upstreamViews.response"),
            body: response,
            withheld: auditContent?.withheld.upstream_response_content,
            lead:
              errors.length > 0 ? (
                <Diagnosis errors={errors} tone="error" />
              ) : null,
          },
          {
            value: "request",
            label: t("audit.upstreamViews.request"),
            body: request,
            withheld: auditContent?.withheld.upstream_request_body,
          },
          { value: "http", label: "HTTP", meta },
        ]}
      />
    </div>
  );
}

/** The request line of one side of the exchange, clipped to the row. */
export function EndpointLine({
  method,
  url,
  testId,
}: {
  method: string | null;
  url: string;
  testId: string;
}) {
  const line = method ? `${method} ${url}` : url;
  return (
    <code
      className="min-w-0 flex-1 truncate font-mono text-micro text-muted-foreground"
      data-testid={testId}
      title={line}
    >
      {method ? (
        <>
          <span className="font-medium text-foreground">{method}</span>{" "}
        </>
      ) : null}
      {url}
    </code>
  );
}

export type CaptureView =
  | {
      value: string;
      label: string;
      body: AuditContentPart | null;
      /** Why a captured body is left out of this read, if it is. */
      withheld?: AuditWithheldPart | null;
      /** Shown above the body, such as the error the provider sent. */
      lead?: ReactNode;
    }
  | { value: string; label: string; meta: AuditHTTPMeta | null };

/**
 * Captured bodies and HTTP metadata in one bounded pane: a view switch when
 * there is more than one, the structured/raw toggle, find, copy, and the
 * only scroller. Each inspector tab that shows a body uses this frame so the
 * tabs read the same way. Find goes straight to the place in the body: the
 * JSON node, the stretch of original text, or the stream event.
 */
export function CapturePane({
  label,
  viewsLabel = label,
  views,
  copyKey,
  copyFeedback,
  missingHint,
  testId,
  scrollerRef,
  revealPrivacy,
}: {
  label: string;
  /** Names the view switch; defaults to the pane's label. */
  viewsLabel?: string;
  views: readonly CaptureView[];
  copyKey: string;
  copyFeedback: CopyFeedback;
  /** Shown instead of an absent body; null while loading. */
  missingHint: string | null;
  testId: string;
  scrollerRef?: Ref<HTMLDivElement>;
  /** Unfolds JSON strings that carry privacy placeholders, for finding them. */
  revealPrivacy?: boolean;
}) {
  const t = useT();
  const [value, setValue] = useState(views[0]?.value ?? "");
  const [mode, setMode] = useState<WireViewMode>("structured");
  const view = views.find((item) => item.value === value) ?? views[0];
  const body = view && "body" in view ? view.body : null;
  const find = usePaneFind(body !== null);
  if (!view) return null;
  const meta = "meta" in view ? view.meta : null;
  const hint = ("body" in view && withheldHint(view.withheld)) || missingHint;
  const structuredLabel = body ? wireStructuredLabel(body) : null;
  const viewCopyKey = `${copyKey}:${view.value}`;
  const copyValue =
    "meta" in view ? (meta ? httpMetaText(meta) : "") : (body?.content ?? "");
  const copied =
    copyFeedback.activeKey === viewCopyKey && copyFeedback.state === "copied";

  return (
    <Panel aria-label={label} className={PANE_CLASS}>
      <div className="flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b px-2 py-1.5">
        {views.length > 1 ? (
          <SegmentedControl
            label={viewsLabel}
            onValueChange={(next) => {
              setValue(next);
              find.restart();
            }}
            options={views.map((item) => ({
              value: item.value,
              label: item.label,
            }))}
            value={view.value}
          />
        ) : (
          <span className="px-1 text-xs font-medium text-muted-foreground">
            {view.label}
          </span>
        )}
        {/* Always on screen, so find is seen rather than remembered. It
            stays in place over the HTTP view, unavailable, so switching
            views does not shift the toolbar. */}
        <div
          className="min-w-36 flex-1 basis-40"
          data-tour-target="inspector-find"
        >
          <FindBar
            active={find.active}
            disabled={!body}
            inputRef={find.inputRef}
            label={t("find.label")}
            onQueryChange={find.setQuery}
            onStep={find.step}
            placeholder={t("find.contentPlaceholder")}
            query={find.query}
            result={find.result}
            shortcut={find.shortcut ? findShortcutLabel() : undefined}
          />
        </div>
        <ActionGroup className="gap-1">
          {structuredLabel ? (
            <SegmentedControl
              label={t("audit.contentView")}
              onValueChange={(next) => {
                setMode(next);
                find.restart();
              }}
              options={[
                { value: "structured", label: structuredLabel },
                { value: "raw", label: t("audit.original") },
              ]}
              value={mode}
            />
          ) : null}
          <Button
            aria-label={copyButtonLabel(
              copyFeedback,
              viewCopyKey,
              t("responseViewer.copyRaw"),
            )}
            disabled={!copyValue}
            onClick={() => copyFeedback.copy(viewCopyKey, copyValue)}
            size="icon-sm"
            title={t("responseViewer.copyRaw")}
            type="button"
            variant="ghost"
          >
            {copied ? (
              <Check aria-hidden="true" />
            ) : (
              <Copy aria-hidden="true" />
            )}
          </Button>
        </ActionGroup>
      </div>
      <div
        className="min-h-0 flex-1 space-y-3 overflow-auto overscroll-contain p-3"
        data-tab-scroller
        data-testid={testId}
        data-view={view.value}
        ref={scrollerRef}
      >
        {"lead" in view ? view.lead : null}
        {"meta" in view ? (
          <HTTPMetaDetails meta={meta} />
        ) : body ? (
          <AuditWireView
            find={find.request}
            key={`${view.value}:${mode}`}
            mode={structuredLabel ? mode : "raw"}
            part={body}
            revealPrivacy={revealPrivacy}
          />
        ) : hint ? (
          <p
            className="text-xs leading-6 text-muted-foreground"
            data-testid="inspector-missing-body"
          >
            {hint}
          </p>
        ) : null}
      </div>
    </Panel>
  );
}

/**
 * Find state for one pane. The query survives switching views, as a
 * browser's does; the position starts over with each new search. The view
 * searches a deferred copy of the query, so typing into a multi-megabyte
 * body stays responsive.
 */
function usePaneFind(available: boolean) {
  const shortcut = useContext(InspectorFindShortcut);
  const [query, setQueryState] = useState("");
  const [active, setActive] = useState(0);
  const [seq, setSeq] = useState(0);
  // Tagged with the query it answers, so a count never outlives its query.
  const [result, setResult] = useState<(FindResult & { query: string }) | null>(
    null,
  );
  const deferredQuery = useDeferredValue(query);
  const inputRef = useRef<HTMLInputElement>(null);
  const onResult = useCallback(
    (next: FindResult) => setResult({ ...next, query: deferredQuery }),
    [deferredQuery],
  );

  const restart = useCallback(() => {
    setActive(0);
    setSeq((current) => current + 1);
  }, []);
  const setQuery = useCallback(
    (next: string) => {
      setQueryState(next);
      restart();
    },
    [restart],
  );
  const current = result?.query === query ? result : null;
  const count = current?.count ?? 0;
  const step = useCallback(
    (direction: 1 | -1) => {
      if (count === 0) return;
      setActive((current) => stepFind(current, count, direction));
      setSeq((current) => current + 1);
    },
    [count],
  );

  const stepRef = useRef(step);
  stepRef.current = step;
  useEffect(() => {
    if (!shortcut || !available) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (isFindShortcut(event)) {
        event.preventDefault();
        inputRef.current?.focus();
        inputRef.current?.select();
        return;
      }
      const direction = findStepShortcut(event);
      if (direction === null) return;
      event.preventDefault();
      stepRef.current(direction);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [available, shortcut]);

  const request = useMemo<FindRequest | undefined>(
    () =>
      available ? { query: deferredQuery, active, seq, onResult } : undefined,
    [active, available, deferredQuery, onResult, seq],
  );

  return {
    shortcut,
    query,
    active,
    result: current,
    request,
    inputRef,
    setQuery,
    step,
    restart,
  };
}

type FactLabel = "http" | "duration" | "ttft" | "tokens" | "size";

interface Fact {
  label: FactLabel;
  value: string;
  title?: string;
  danger?: boolean;
}

/**
 * Status first, then the numbers an operator compares across calls, in one
 * flow so a narrow window wraps them instead of stacking a column.
 */
function OutcomeFacts({
  status,
  facts,
  note,
}: {
  status: RequestStatus;
  facts: (Fact | null)[];
  note?: string | null;
}) {
  const t = useT();
  return (
    <dl
      className="flex min-w-0 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs"
      data-testid="inspector-outcome"
    >
      <div className="flex">
        <dt className="sr-only">{t("records.status")}</dt>
        <dd>
          <StatusBadge tone={statusTone(status)}>
            {statusLabel(status)}
          </StatusBadge>
        </dd>
      </div>
      {facts.map((fact) =>
        fact ? (
          <div
            className="flex items-baseline gap-1"
            key={fact.label}
            title={fact.title}
          >
            {/* The status code already reads as "HTTP 200". */}
            <dt
              className={
                fact.label === "http" ? "sr-only" : "text-muted-foreground"
              }
            >
              {t(`audit.facts.${fact.label}`)}
            </dt>
            <dd
              className={cn(
                "font-mono tabular-nums",
                fact.danger ? "text-destructive" : "text-foreground",
              )}
              data-testid={fact.label === "http" ? "inspector-http" : undefined}
            >
              {fact.value}
            </dd>
          </div>
        ) : null,
      )}
      {note ? (
        <div className="ml-auto" data-testid="inspector-same-response">
          <dt className="sr-only">{t("trajectory.upstreamResponse")}</dt>
          <dd className="text-micro text-muted-foreground">{note}</dd>
        </div>
      ) : null}
    </dl>
  );
}

/**
 * One card per failure. The provider's own code and message lead; the
 * gateway's verdict follows in the same card, then why an HTTP 200 can fail.
 */
function Diagnosis({
  errors,
  record,
  tone,
}: {
  errors: ResponseOutput[];
  record?: RequestRecord;
  tone: "error" | "warning";
}) {
  const t = useT();
  const gateway = record?.error ?? null;
  const lead = errors[0];
  const headline = lead ? (lead.name ?? null) : (gateway?.code ?? null);
  const message = lead ? lead.text : (gateway?.message ?? "");
  if (!headline && !message) return null;
  // A gateway code that repeats the provider's adds nothing.
  const verdict =
    lead && gateway && gateway.code !== lead.name ? gateway : null;
  const retryable = gateway?.retryable ? ` · ${t("records.retryable")}` : "";
  const streamNote =
    record !== undefined &&
    isUnsuccessful(record.status) &&
    record.http_status !== null &&
    record.http_status < 400;
  return (
    <FormMessage
      className="grid gap-1 break-words"
      data-testid="inspector-diagnosis"
      tone={tone}
    >
      {headline ? (
        <span className="block">
          <span className="font-mono font-medium">{headline}</span>
          {verdict ? null : (
            <span className="text-micro opacity-85">{retryable}</span>
          )}
        </span>
      ) : null}
      {message ? <span className="block">{message}</span> : null}
      {errors.slice(1).map((error, index) => (
        <span className="block" key={index}>
          {error.name ? (
            <span className="font-mono font-medium">{error.name} · </span>
          ) : null}
          {error.text}
        </span>
      ))}
      {verdict || streamNote ? (
        <span className="mt-1 block border-t border-current/15 pt-1.5 text-micro opacity-85">
          {verdict ? (
            <span
              className="block"
              data-testid="inspector-gateway-verdict"
              title={verdict.message || undefined}
            >
              {t("audit.gatewayVerdict")}{" "}
              <span className="font-mono">{verdict.code}</span>
              {retryable}
              {/* The stream note already says this, in the reader's language. */}
              {verdict.message && !streamNote ? (
                <span className="block">{verdict.message}</span>
              ) : null}
            </span>
          ) : null}
          {streamNote ? (
            <span className="block">{t("audit.httpNotCompletion")}</span>
          ) : null}
        </span>
      ) : null}
    </FormMessage>
  );
}

function isUnsuccessful(status: RequestStatus): boolean {
  return status === "failed" || status === "blocked" || status === "cancelled";
}

function httpFact(status: number | null): Fact | null {
  if (status === null) return null;
  return { label: "http", value: `HTTP ${status}`, danger: status >= 400 };
}

function sizeFact(part: AuditContentPart): Fact {
  return { label: "size", value: formatBytes(part.captured_bytes) };
}

function tokenFact(record: RequestRecord): Fact | null {
  const usage = record.usage;
  if (!usage) return null;
  const t = i18n.t;
  const cached = usage.cache_read_tokens
    ? ` · ${t("audit.cacheReadPart", { count: formatExactNumber(usage.cache_read_tokens) })}`
    : "";
  return {
    label: "tokens",
    value: `${formatExactNumber(usage.input_tokens)} → ${formatExactNumber(usage.output_tokens)}`,
    title: `${t("records.inputTokens")} ${formatExactNumber(usage.input_tokens)} · ${t("records.outputTokens")} ${formatExactNumber(usage.output_tokens)}${cached}`,
  };
}

function rowDuration(row: TrajectoryRow): Fact | null {
  if (!row.endedAt) return null;
  const elapsed = Date.parse(row.endedAt) - Date.parse(row.startedAt);
  if (!Number.isFinite(elapsed) || elapsed < 0) return null;
  return { label: "duration", value: formatDuration(elapsed) };
}

function sameContent(
  left: AuditContentPart | null,
  right: AuditContentPart | null,
): boolean {
  return (
    left !== null &&
    right !== null &&
    left.content === right.content &&
    left.media_type === right.media_type &&
    left.truncated === right.truncated &&
    left.captured_bytes === right.captured_bytes
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
