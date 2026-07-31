import { useEffect, useMemo, useState } from "react";

import { buildHeadersText } from "./audit-bundle";
import { copyButtonLabel, type CopyFeedback } from "./copy-feedback";
import type {
  AuditContentPart,
  AuditHTTPMeta,
} from "./request-record-model";
import {
  parseSSEIncremental,
  SSEParseCancelledError,
  type SSEEvent,
} from "./sse-review-model";

const RAW_SEGMENT_SIZE = 256 * 1024;
const EVENT_RENDER_BATCH = 300;

type StreamViewMode = "raw" | "events";
type DocumentViewMode = "formatted" | "raw";

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
          <button
            className="text-button"
            onClick={() =>
              copyFeedback.copy(
                copyKey,
                [
                  `${meta.method} ${meta.url} ${meta.http_version}`.trim(),
                  "",
                  buildHeadersText(meta.request_headers),
                  "",
                  meta.response_status !== null
                    ? `HTTP ${meta.response_status}`
                    : "",
                  buildHeadersText(meta.response_headers),
                ].join("\n"),
              )
            }
            type="button"
          >
            {copyButtonLabel(copyFeedback, copyKey)}
          </button>
        ) : null
      }
      title={title}
    >
      {meta === null ? (
        <p className="record-http-meta__missing">
          此记录未捕获 HTTP 元数据（记录创建时捕获未开启，或来自旧版本）。
        </p>
      ) : (
        <div className="record-http-meta">
          <code className="record-http-meta__line">
            {meta.method} {meta.url} {meta.http_version}
          </code>
          <HeaderList headers={meta.request_headers} title="请求头" />
          <code className="record-http-meta__line">
            {meta.response_status !== null
              ? `HTTP ${meta.response_status}`
              : "（无响应状态）"}
          </code>
          <HeaderList headers={meta.response_headers} title="响应头" />
        </div>
      )}
    </DetailBlock>
  );
}

function HeaderList({
  headers,
  title,
}: {
  headers: AuditHTTPMeta["request_headers"];
  title: string;
}) {
  if (headers.length === 0) {
    return (
      <div className="record-http-meta__group">
        <h4>{title}</h4>
        <p className="record-http-meta__missing">（无）</p>
      </div>
    );
  }
  return (
    <div className="record-http-meta__group">
      <h4>{title}</h4>
      <ul className="record-http-meta__headers">
        {headers.map((header, index) => (
          <li key={`${header.name}:${index}`}>
            <span className="record-http-meta__name">{header.name}:</span>{" "}
            <span
              className={
                header.redacted
                  ? "record-http-meta__value is-redacted"
                  : "record-http-meta__value"
              }
            >
              {header.value}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function AuditPartSection({
  title,
  part,
  protocol,
  sectionKey,
  copyFeedback,
}: {
  title: string;
  part: AuditContentPart | null;
  protocol: string;
  sectionKey: string;
  copyFeedback: CopyFeedback;
}) {
  return (
    <DetailBlock
      actions={
        part ? (
          <button
            className="text-button"
            onClick={() => copyFeedback.copy(sectionKey, part.content)}
            type="button"
          >
            {copyButtonLabel(copyFeedback, sectionKey)}
          </button>
        ) : null
      }
      title={title}
    >
      {part === null ? (
        <p className="record-http-meta__missing">
          未捕获（捕获未开启，或内容已按保留期清理）。
        </p>
      ) : (
        <AuditPartView part={part} protocol={protocol} />
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
    <section className="record-detail-section audit-inline-section">
      <header className="audit-inline-section__header">
        <h3>{title}</h3>
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
  const isStream = part.media_type.toLowerCase().includes("text/event-stream");
  return (
    <div className="audit-part">
      <div className="audit-part__meta">
        <span>{part.media_type}</span>
        <span>{formatBytes(part.captured_bytes)}</span>
        {part.truncated ? <strong>已截断</strong> : null}
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

  return (
    <>
      <div className="audit-view-toolbar">
        <div className="audit-view-tabs" role="tablist" aria-label="流内容视图">
          <ModeTab
            active={mode === "raw"}
            label="原文"
            onClick={() => setMode("raw")}
          />
          <ModeTab
            active={mode === "events"}
            label={parseState === "idle" ? "事件" : `事件 · ${events.length}`}
            onClick={() => setMode("events")}
          />
        </div>
        {parseState !== "idle" ? (
          <ParseStatus
            progress={parseProgress}
            state={parseState}
            summary={parseSummary}
          />
        ) : null}
      </div>

      {mode === "raw" ? (
        <RawSegmentView content={part.content} />
      ) : (
        <EventsView events={events} parsing={parseState === "parsing"} />
      )}
    </>
  );
}

function ModeTab({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      aria-selected={active}
      className={active ? "is-active" : ""}
      onClick={onClick}
      role="tab"
      type="button"
    >
      {label}
    </button>
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
  if (state === "parsing") {
    return (
      <span className="audit-parse-status" role="status">
        解析中 {Math.round(progress * 100)}%
      </span>
    );
  }
  if (state === "error") {
    return <span className="audit-parse-status is-error">解析失败，可查看原文</span>;
  }
  if (state === "cancelled") {
    return <span className="audit-parse-status">解析已取消</span>;
  }
  if (summary.invalidJsonCount > 0 || summary.incompleteLastEvent) {
    return (
      <span className="audit-parse-status is-warning">
        {summary.invalidJsonCount > 0
          ? `${summary.invalidJsonCount} 个无效 JSON`
          : ""}
        {summary.invalidJsonCount > 0 && summary.incompleteLastEvent ? " · " : ""}
        {summary.incompleteLastEvent ? "末尾事件不完整" : ""}
      </span>
    );
  }
  return <span className="audit-parse-status">解析完成</span>;
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

  return (
    <div className="audit-events">
      <div className="audit-events__filters">
        <label>
          <span>搜索事件</span>
          <input
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder="类型或内容"
            type="search"
            value={query}
          />
        </label>
        <label>
          <span>事件类型</span>
          <select
            onChange={(event) => setType(event.currentTarget.value)}
            value={type}
          >
            <option value="">全部类型</option>
            {types.map((eventType) => (
              <option key={eventType} value={eventType}>
                {eventType}
              </option>
            ))}
          </select>
        </label>
        <span>
          {filtered.length} 个匹配{parsing ? " · 仍在解析" : ""}
        </span>
      </div>
      <div className="audit-event-list">
        {filtered.slice(0, renderLimit).map((event) => (
          <EventCard event={event} key={event.index} />
        ))}
      </div>
      {renderLimit < filtered.length ? (
        <button
          className="btn-secondary audit-load-more"
          onClick={() =>
            setRenderLimit((current) => current + EVENT_RENDER_BATCH)
          }
          type="button"
        >
          再显示 {Math.min(EVENT_RENDER_BATCH, filtered.length - renderLimit)} 个事件
        </button>
      ) : null}
    </div>
  );
}

function EventCard({ event }: { event: SSEEvent }) {
  return (
    <details className="audit-event-card">
      <summary>
        <span>#{event.index}</span>
        <strong>{event.type}</strong>
        {event.invalidJson ? <em>JSON 无效</em> : null}
        {event.incomplete ? <em>事件不完整</em> : null}
        <small>{event.data.length.toLocaleString()} 字符</small>
      </summary>
      <pre>
        {event.json === null
          ? event.data || "（空 data）"
          : JSON.stringify(event.json, null, 2)}
      </pre>
    </details>
  );
}

function DocumentInspector({ part }: { part: AuditContentPart }) {
  const canFormat =
    part.content.length <= 1024 * 1024 &&
    (part.media_type.toLowerCase().includes("json") ||
      looksLikeJson(part.content));
  const formatted = useMemo(() => {
    if (!canFormat) return null;
    try {
      return JSON.stringify(JSON.parse(part.content), null, 2);
    } catch {
      return null;
    }
  }, [canFormat, part.content]);
  const [mode, setMode] = useState<DocumentViewMode>(
    formatted === null ? "raw" : "formatted",
  );

  return (
    <>
      <div className="audit-view-toolbar">
        <div className="audit-view-tabs" role="tablist" aria-label="内容视图">
          <button
            aria-selected={mode === "formatted"}
            className={mode === "formatted" ? "is-active" : ""}
            disabled={formatted === null}
            onClick={() => setMode("formatted")}
            role="tab"
            type="button"
          >
            格式化
          </button>
          <button
            aria-selected={mode === "raw"}
            className={mode === "raw" ? "is-active" : ""}
            onClick={() => setMode("raw")}
            role="tab"
            type="button"
          >
            原文
          </button>
        </div>
        {formatted === null && canFormat ? (
          <span className="audit-parse-status is-warning">JSON 无效</span>
        ) : null}
      </div>
      {mode === "formatted" && formatted !== null ? (
        <pre className="audit-document">{formatted}</pre>
      ) : (
        <RawSegmentView content={part.content} />
      )}
    </>
  );
}

function RawSegmentView({ content }: { content: string }) {
  const totalSegments = Math.max(1, Math.ceil(content.length / RAW_SEGMENT_SIZE));
  const [visibleSegments, setVisibleSegments] = useState(1);
  const segments = [];
  for (let index = 0; index < Math.min(totalSegments, visibleSegments); index += 1) {
    const start = index * RAW_SEGMENT_SIZE;
    segments.push({
      index,
      start,
      end: Math.min(content.length, start + RAW_SEGMENT_SIZE),
      text: content.slice(start, start + RAW_SEGMENT_SIZE),
    });
  }
  return (
    <div className="audit-raw">
      <div className="audit-raw__summary">
        <span>
          完整原文 · {content.length.toLocaleString()} 字符 · {totalSegments} 段
        </span>
        {totalSegments > 1 ? <span>每段最多 256KB，按需渲染</span> : null}
      </div>
      {segments.map((segment) => (
        <section className="audit-raw__segment" key={segment.index}>
          {totalSegments > 1 ? (
            <header>
              第 {segment.index + 1} 段 · 字符 {segment.start.toLocaleString()}–
              {segment.end.toLocaleString()}
            </header>
          ) : null}
          <pre>{segment.text}</pre>
        </section>
      ))}
      {visibleSegments < totalSegments ? (
        <button
          className="btn-secondary audit-load-more"
          onClick={() => setVisibleSegments((current) => current + 1)}
          type="button"
        >
          加载下一段
        </button>
      ) : null}
    </div>
  );
}

function looksLikeJson(content: string): boolean {
  const trimmed = content.trimStart();
  return trimmed.startsWith("{") || trimmed.startsWith("[");
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
