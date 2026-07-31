import type {
  AuditContent,
  AuditContentPart,
  AuditHeader,
  RequestRecord,
} from "./request-record-model";
import { statusLabel } from "./request-record-model";

export interface RecordBundleOptions {
  includeBodies?: boolean;
  /** Human-readable service name resolved by the caller. */
  serviceLabel?: string | null;
}

/**
 * Wraps content in a markdown code fence that cannot be broken by fences
 * inside the content: the delimiter is one backtick longer than the longest
 * backtick run found in the body.
 */
export function fence(content: string, language = ""): string {
  let longest = 0;
  const matches = content.matchAll(/`+/g);
  for (const match of matches) {
    if (match[0].length > longest) longest = match[0].length;
  }
  const delimiter = "`".repeat(Math.max(3, longest + 1));
  return `${delimiter}${language}\n${content}\n${delimiter}`;
}

export function buildHeadersText(headers: AuditHeader[]): string {
  return headers
    .map((header) => `${header.name}: ${header.value}`)
    .join("\n");
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function fenceLanguage(mediaType: string): string {
  return mediaType.toLowerCase().includes("json") ? "json" : "";
}

function partSection(title: string, part: AuditContentPart | null): string[] {
  const lines = [`## ${title}`];
  if (part === null) {
    lines.push("（未捕获）");
    return lines;
  }
  const meta = [`${part.media_type} · ${formatBytes(part.captured_bytes)}`];
  if (part.truncated) {
    // An LLM given a truncated body without notice will confidently reason
    // about bytes that were never captured — always flag it inline.
    meta.push("⚠️ 已截断（超出捕获上限，以下内容不完整）");
  }
  lines.push(meta.join(" · "), "", fence(part.content, fenceLanguage(part.media_type)));
  return lines;
}

export function buildRecordBundle(
  record: RequestRecord,
  content: AuditContent | null,
  options: RecordBundleOptions = {},
): string {
  const includeBodies = options.includeBodies !== false;
  const lines: string[] = [`# AstrLink 请求记录 ${record.id}`, ""];

  const time = record.completed_at
    ? `${record.started_at} → ${record.completed_at}`
    : record.started_at;
  const latency =
    record.latency_ms !== null ? `（${record.latency_ms} ms）` : "";
  lines.push(`- 时间: ${time}${latency}`);
  const httpStatus =
    record.http_status !== null ? ` · HTTP ${record.http_status}` : "";
  lines.push(`- 状态: ${statusLabel(record.status)}${httpStatus}`);
  lines.push(
    `- 协议: ${record.input_protocol} · 模型: ${record.requested_model ?? "（未知）"} · 流式: ${record.streaming ? "是" : "否"}`,
  );
  const service = options.serviceLabel ?? record.service_id ?? "（未路由）";
  const route = record.route_id ? ` · 路由: ${record.route_id}` : "";
  lines.push(`- 服务: ${service}${route}`);
  if (record.usage) {
    const cached =
      record.usage.cached_input_tokens !== undefined
        ? `（缓存命中 ${record.usage.cached_input_tokens}）`
        : "";
    lines.push(
      `- Token: 输入 ${record.usage.input_tokens} / 输出 ${record.usage.output_tokens} / 总计 ${record.usage.total_tokens}${cached}`,
    );
  }
  if (record.privacy_restore) {
    const restore = record.privacy_restore;
    lines.push(
      `- 隐私还原: ${restore.enabled ? "已开启" : "已关闭"} · 映射 ${restore.mapping_count} · 已还原 ${restore.restored_count} · 安全降级 ${restore.fallback_count}`,
    );
  }

  if (record.error) {
    lines.push(
      "",
      "## 错误",
      `- 类别: ${record.error.category} · 代码: ${record.error.code} · 可重试: ${record.error.retryable ? "是" : "否"}`,
      `- 消息: ${record.error.message}`,
    );
  }

  const meta = content?.http_meta ?? null;
  if (meta === null) {
    // Explicit absence beats omission: the reader must know the envelope
    // was never captured rather than silently dropped from the bundle.
    lines.push("", "## HTTP", "（此记录未捕获 HTTP 元数据）");
  } else {
    lines.push(
      "",
      "## HTTP 请求",
      `${meta.method} ${meta.url} ${meta.http_version}`.trim(),
    );
    if (meta.request_headers.length > 0) {
      lines.push("", buildHeadersText(meta.request_headers));
    }
    lines.push("", "## HTTP 响应");
    lines.push(
      meta.response_status !== null
        ? `HTTP ${meta.response_status}`
        : "（无响应状态）",
    );
    if (meta.response_headers.length > 0) {
      lines.push("", buildHeadersText(meta.response_headers));
    }
  }

  if (includeBodies) {
    lines.push("", ...partSection("请求体", content?.request_body ?? null));
    lines.push("", ...partSection("响应内容", content?.response_content ?? null));
  }

  return lines.join("\n");
}
