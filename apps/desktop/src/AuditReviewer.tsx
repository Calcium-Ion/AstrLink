import { useEffect, useMemo, useState } from "react";

import type {
  AuditContent,
  AuditContentPart,
  RequestRecord,
} from "./request-record-model";
import {
  buildSemanticTimeline,
  parseSSEIncremental,
  SSEParseCancelledError,
  type SSEEvent,
  type TimelineItem,
} from "./sse-review-model";

const LARGE_STREAM_SIZE = 8 * 1024 * 1024;
const RAW_SEGMENT_SIZE = 256 * 1024;
const EVENT_RENDER_BATCH = 300;

type AuditDirection = "request" | "response";
type StreamViewMode = "timeline" | "events" | "raw";
type DocumentViewMode = "formatted" | "raw";

export function AuditReviewer({
  record,
  content,
  onBack,
  onClear,
}: {
  record: RequestRecord;
  content: AuditContent;
  onBack: () => void;
  onClear: () => void;
}) {
  const initialDirection: AuditDirection = content.response_content
    ? "response"
    : "request";
  const [direction, setDirection] =
    useState<AuditDirection>(initialDirection);
  const part =
    direction === "request" ? content.request_body : content.response_content;

  return (
    <section
      aria-labelledby="audit-review-heading"
      className="workspace-card records-surface audit-reviewer"
    >
      <header className="records-page-header">
        <div>
          <button className="records-back" onClick={onBack} type="button">
            <span aria-hidden="true">←</span>
            记录详情
          </button>
          <span className="section-kicker">本机解密 · 仅保存在内存</span>
          <h2 id="audit-review-heading">内容审查</h2>
          <p>
            <code>{record.id}</code>
          </p>
        </div>
        <button className="btn-secondary" onClick={onClear} type="button">
          清除解密内容
        </button>
      </header>

      <p className="audit-memory-warning" role="status">
        已解密内容只存在于当前 Core 会话的内存中；切换记录或返回监控会立即清除。
      </p>

      <div className="audit-direction-tabs" role="tablist" aria-label="内容方向">
        <DirectionTab
          active={direction === "request"}
          available={content.request_body !== null}
          label="请求体"
          onClick={() => setDirection("request")}
        />
        <DirectionTab
          active={direction === "response"}
          available={content.response_content !== null}
          label="响应内容"
          onClick={() => setDirection("response")}
        />
      </div>

      {part ? (
        <AuditPartView
          key={`${record.id}:${direction}`}
          part={part}
          protocol={record.input_protocol}
        />
      ) : (
        <div className="records-empty">
          <strong>这个方向没有已捕获内容</strong>
          <span>返回详情可查看捕获状态和截断信息。</span>
        </div>
      )}
    </section>
  );
}

function DirectionTab({
  active,
  available,
  label,
  onClick,
}: {
  active: boolean;
  available: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      aria-selected={active}
      className={active ? "is-active" : ""}
      disabled={!available}
      onClick={onClick}
      role="tab"
      type="button"
    >
      {label}
      {!available ? <span>未捕获</span> : null}
    </button>
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
  protocol,
}: {
  part: AuditContentPart;
  protocol: string;
}) {
  const [events, setEvents] = useState<SSEEvent[]>([]);
  const [timeline, setTimeline] = useState<TimelineItem[]>([]);
  const [parseState, setParseState] = useState<
    "parsing" | "ready" | "cancelled" | "error"
  >("parsing");
  const [parseProgress, setParseProgress] = useState(0);
  const [parseSummary, setParseSummary] = useState({
    invalidJsonCount: 0,
    incompleteLastEvent: false,
  });
  const [mode, setMode] = useState<StreamViewMode>(
    part.content.length > LARGE_STREAM_SIZE ? "events" : "timeline",
  );

  useEffect(() => {
    const controller = new AbortController();
    setEvents([]);
    setTimeline([]);
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
        setTimeline(buildSemanticTimeline(protocol, result.events));
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
  }, [part.content, part.truncated, protocol]);

  return (
    <>
      <div className="audit-view-toolbar">
        <div className="audit-view-tabs" role="tablist" aria-label="流内容视图">
          <ModeTab
            active={mode === "timeline"}
            label="时间线"
            onClick={() => setMode("timeline")}
          />
          <ModeTab
            active={mode === "events"}
            label={`事件 · ${events.length}`}
            onClick={() => setMode("events")}
          />
          <ModeTab
            active={mode === "raw"}
            label="原文"
            onClick={() => setMode("raw")}
          />
        </div>
        <ParseStatus
          progress={parseProgress}
          state={parseState}
          summary={parseSummary}
        />
      </div>

      {mode === "timeline" ? (
        <TimelineView
          parsing={parseState === "parsing"}
          timeline={timeline}
        />
      ) : mode === "events" ? (
        <EventsView events={events} parsing={parseState === "parsing"} />
      ) : (
        <RawSegmentView content={part.content} />
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
  state: "parsing" | "ready" | "cancelled" | "error";
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

function TimelineView({
  timeline,
  parsing,
}: {
  timeline: TimelineItem[];
  parsing: boolean;
}) {
  if (timeline.length === 0) {
    return (
      <div className="records-empty">
        <strong>{parsing ? "正在生成语义时间线…" : "没有可聚合的事件"}</strong>
        <span>{parsing ? "大内容会分批处理，期间可先查看事件或原文。" : "请切换到事件视图人工检查。"}</span>
      </div>
    );
  }
  return (
    <ol className="audit-timeline">
      {timeline.map((item) => (
        <li className={`audit-timeline__item is-${item.kind}`} key={item.id}>
          <span className="audit-timeline__marker" aria-hidden="true" />
          <article>
            <header>
              <strong>{item.title}</strong>
              <span>
                #{item.firstEvent}
                {item.lastEvent === item.firstEvent ? "" : `–#${item.lastEvent}`}
              </span>
            </header>
            {item.text ? (
              <div className="audit-timeline__content">{item.text}</div>
            ) : (
              <em>无内容</em>
            )}
          </article>
        </li>
      ))}
    </ol>
  );
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
        <span>每段最多 256KB，按需渲染</span>
      </div>
      {segments.map((segment) => (
        <section className="audit-raw__segment" key={segment.index}>
          <header>
            第 {segment.index + 1} 段 · 字符 {segment.start.toLocaleString()}–
            {segment.end.toLocaleString()}
          </header>
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
