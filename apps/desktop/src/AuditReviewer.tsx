import { useEffect, useMemo, useState } from "react";

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
          <Button
            className="h-auto px-0 text-[10px]"
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
            variant="link"
          >
            {copyButtonLabel(copyFeedback, copyKey)}
          </Button>
        ) : null
      }
      title={title}
    >
      {meta === null ? (
        <p className="text-[10px] leading-6 text-muted-foreground">
          此记录未捕获 HTTP 元数据（记录创建时捕获未开启，或来自旧版本）。
        </p>
      ) : (
        <div className="grid gap-3">
          <code className="[overflow-wrap:anywhere] block rounded-lg bg-muted px-2.5 py-2 text-[10px] leading-6 text-text-secondary">
            {meta.method} {meta.url} {meta.http_version}
          </code>
          <HeaderList headers={meta.request_headers} title="请求头" />
          <code className="[overflow-wrap:anywhere] block rounded-lg bg-muted px-2.5 py-2 text-[10px] leading-6 text-text-secondary">
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
      <div>
        <h4 className="mb-1.5 text-[10px] font-bold text-text-secondary">{title}</h4>
        <p className="text-[10px] leading-6 text-muted-foreground">（无）</p>
      </div>
    );
  }
  return (
    <div>
      <h4 className="mb-1.5 text-[10px] font-bold text-text-secondary">{title}</h4>
      <ul className="grid list-none gap-1 p-0 font-mono text-[9.5px] leading-6 text-text-secondary">
        {headers.map((header, index) => (
          <li key={`${header.name}:${index}`}>
            <span className="font-bold text-foreground">{header.name}:</span>{" "}
            <span
              className={header.redacted ? "text-warning-foreground" : undefined}
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
          <Button
            className="h-auto px-0 text-[10px]"
            onClick={() => copyFeedback.copy(sectionKey, part.content)}
            type="button"
            variant="link"
          >
            {copyButtonLabel(copyFeedback, sectionKey)}
          </Button>
        ) : null
      }
      title={title}
    >
      {part === null ? (
        <p className="text-[10px] leading-6 text-muted-foreground">
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
    <section className="rounded-[11px] border bg-card p-3.5">
      <header className="mb-2.5 flex items-center justify-between gap-3">
        <h3 className="text-[11px] font-bold">{title}</h3>
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
    <div>
      <div className="mb-2.5 flex flex-wrap items-center gap-2 text-[9px] text-muted-foreground">
        <span>{part.media_type}</span>
        <span>{formatBytes(part.captured_bytes)}</span>
        {part.truncated ? <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">已截断</Badge> : null}
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
    <Tabs value={mode} onValueChange={(value) => setMode(value as StreamViewMode)}>
      <div className="mb-2.5 flex items-center justify-between gap-3 max-[720px]:items-stretch max-[720px]:flex-col">
        <TabsList aria-label="流内容视图">
          <ModeTab
            active={mode === "raw"}
            label="原文"
            value="raw"
          />
          <ModeTab
            active={mode === "events"}
            label={parseState === "idle" ? "事件" : `事件 · ${events.length}`}
            value="events"
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
    <TabsTrigger
      aria-selected={active}
      value={value}
    >
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
  if (state === "parsing") {
    return (
      <Badge variant="secondary" role="status">
        解析中 {Math.round(progress * 100)}%
      </Badge>
    );
  }
  if (state === "error") {
    return <Badge className="bg-danger-wash text-danger-foreground" variant="secondary">解析失败，可查看原文</Badge>;
  }
  if (state === "cancelled") {
    return <Badge variant="secondary">解析已取消</Badge>;
  }
  if (summary.invalidJsonCount > 0 || summary.incompleteLastEvent) {
    return (
      <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
        {summary.invalidJsonCount > 0
          ? `${summary.invalidJsonCount} 个无效 JSON`
          : ""}
        {summary.invalidJsonCount > 0 && summary.incompleteLastEvent ? " · " : ""}
        {summary.incompleteLastEvent ? "末尾事件不完整" : ""}
      </Badge>
    );
  }
  return <Badge variant="secondary">解析完成</Badge>;
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
    <div>
      <div className="mb-3 flex items-end gap-2.5 max-[720px]:items-stretch max-[720px]:flex-col">
        <div className="grid gap-1.5">
          <Label htmlFor="audit-event-search">搜索事件</Label>
          <Input
            id="audit-event-search"
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder="类型或内容"
            type="search"
            value={query}
          />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="audit-event-type">事件类型</Label>
          <Select
            onValueChange={(value) => setType(value === "__all__" ? "" : value)}
            value={type}
          >
            <SelectTrigger id="audit-event-type" className="min-w-36 max-[720px]:w-full">
              <SelectValue placeholder="全部类型" />
            </SelectTrigger>
            <SelectContent>
            <SelectItem value="__all__">全部类型</SelectItem>
            {types.map((eventType) => (
              <SelectItem key={eventType} value={eventType}>
                {eventType}
              </SelectItem>
            ))}
            </SelectContent>
          </Select>
        </div>
        <span className="ml-auto pb-2 text-[9px] text-muted-foreground max-[720px]:ml-0 max-[720px]:pb-0">
          {filtered.length} 个匹配{parsing ? " · 仍在解析" : ""}
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
          再显示 {Math.min(EVENT_RENDER_BATCH, filtered.length - renderLimit)} 个事件
        </Button>
      ) : null}
    </div>
  );
}

function EventCard({ event }: { event: SSEEvent }) {
  return (
    <details className="group overflow-hidden rounded-[9px] border bg-card" data-testid="audit-event">
      <summary className="grid cursor-pointer list-none grid-cols-[44px_minmax(0,1fr)_auto_auto] items-center gap-2 px-2.5 py-2 text-[9px] [&::-webkit-details-marker]:hidden max-[720px]:grid-cols-[36px_minmax(0,1fr)_auto]">
        <span className="text-muted-foreground">#{event.index}</span>
        <strong className="overflow-hidden text-[10px] text-ellipsis whitespace-nowrap">{event.type}</strong>
        {event.invalidJson ? <em className="text-warning-foreground not-italic max-[720px]:hidden">JSON 无效</em> : null}
        {event.incomplete ? <em className="text-warning-foreground not-italic max-[720px]:hidden">事件不完整</em> : null}
        <small className="text-muted-foreground">{event.data.length.toLocaleString()} 字符</small>
      </summary>
      <pre className="max-h-[440px] overflow-auto border-t bg-muted p-3 font-mono text-[9.5px] leading-[1.55] whitespace-pre-wrap">
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
    <Tabs value={mode} onValueChange={(value) => setMode(value as DocumentViewMode)}>
      <div className="mb-2.5 flex items-center justify-between gap-3">
        <TabsList aria-label="内容视图">
          <TabsTrigger disabled={formatted === null} value="formatted">格式化</TabsTrigger>
          <TabsTrigger value="raw">原文</TabsTrigger>
        </TabsList>
        {formatted === null && canFormat ? (
          <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">JSON 无效</Badge>
        ) : null}
      </div>
      <TabsContent value="formatted">
        {formatted !== null ? (
        <pre className="max-h-[520px] overflow-auto rounded-lg bg-muted p-3 font-mono text-[9.5px] leading-[1.55] whitespace-pre-wrap">{formatted}</pre>
        ) : null}
      </TabsContent>
      <TabsContent value="raw">
        <RawSegmentView content={part.content} />
      </TabsContent>
    </Tabs>
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
    <div data-testid="audit-raw">
      <div className="mb-2 flex items-center justify-between gap-3 text-[9px] text-muted-foreground max-[720px]:items-start max-[720px]:flex-col">
        <span>
          完整原文 · {content.length.toLocaleString()} 字符 · {totalSegments} 段
        </span>
        {totalSegments > 1 ? <span>每段最多 256KB，按需渲染</span> : null}
      </div>
      {segments.map((segment) => (
        <section className="mt-2 overflow-hidden rounded-lg border" data-testid="audit-raw-segment" key={segment.index}>
          {totalSegments > 1 ? (
            <header className="border-b bg-muted px-3 py-2 text-[9px] text-muted-foreground">
              第 {segment.index + 1} 段 · 字符 {segment.start.toLocaleString()}–
              {segment.end.toLocaleString()}
            </header>
          ) : null}
          <pre className="max-h-[520px] overflow-auto bg-muted/40 p-3 font-mono text-[9.5px] leading-[1.55] whitespace-pre-wrap">{segment.text}</pre>
        </section>
      ))}
      {visibleSegments < totalSegments ? (
        <Button
          className="mt-3 w-full"
          variant="outline"
          onClick={() => setVisibleSegments((current) => current + 1)}
          type="button"
        >
          加载下一段
        </Button>
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
