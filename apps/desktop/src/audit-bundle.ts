import { i18n } from "./i18n";
import type {
  AuditContent,
  AuditContentPart,
  AuditHeader,
  RequestRecord,
} from "./request-record-model";
import { displayRequestStatus, statusLabel } from "./request-record-model";

export type BundleFormat = "markdown" | "txt";

export interface RecordBundleOptions {
  includeBodies?: boolean;
  /** Human-readable service name resolved by the caller. */
  serviceLabel?: string | null;
  format?: BundleFormat;
}

export function bundleFilename(recordId: string, format: BundleFormat): string {
  const ext = format === "markdown" ? "md" : "txt";
  const safe = recordId.replace(/[^A-Za-z0-9._-]+/g, "_").replace(/^_+|_+$/g, "");
  return `astrlink-${safe || "record"}.${ext}`;
}

function heading(title: string, level: 1 | 2, format: BundleFormat): string {
  return format === "txt" ? title : `${"#".repeat(level)} ${title}`;
}

function bullet(text: string, format: BundleFormat): string {
  return format === "txt" ? text : `- ${text}`;
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

function partSection(
  title: string,
  part: AuditContentPart | null,
  format: BundleFormat,
): string[] {
  const lines = [heading(title, 2, format)];
  if (part === null) {
    lines.push(i18n.t("audit.uncaptured"));
    return lines;
  }
  const meta = [`${part.media_type} · ${formatBytes(part.captured_bytes)}`];
  if (part.truncated) {
    // An LLM given a truncated body without notice will confidently reason
    // about bytes that were never captured — always flag it inline.
    meta.push(i18n.t("audit.truncatedNote"));
  }
  const body =
    format === "txt"
      ? part.content
      : fence(part.content, fenceLanguage(part.media_type));
  lines.push(meta.join(" · "), "", body);
  return lines;
}

function httpSection(
  title: string,
  meta: NonNullable<AuditContent["http_meta"]> | null,
  format: BundleFormat,
): string[] {
  if (meta === null) {
    return ["", heading(title, 2, format), i18n.t("audit.noHttp")];
  }
  const lines = [
    "",
    heading(i18n.t("audit.requestTitle", { title }), 2, format),
    `${meta.method} ${meta.url} ${meta.http_version}`.trim(),
  ];
  if (meta.request_headers.length > 0) {
    lines.push("", buildHeadersText(meta.request_headers));
  }
  lines.push("", heading(i18n.t("audit.responseTitle", { title }), 2, format));
  lines.push(
    meta.response_status !== null
      ? `HTTP ${meta.response_status}`
      : i18n.t("audit.noStatus"),
  );
  if (meta.response_headers.length > 0) {
    lines.push("", buildHeadersText(meta.response_headers));
  }
  return lines;
}

export function buildRecordBundle(
  record: RequestRecord,
  content: AuditContent | null,
  options: RecordBundleOptions = {},
): string {
  const includeBodies = options.includeBodies !== false;
  const format = options.format ?? "markdown";
  const lines: string[] = [
    heading(i18n.t("audit.bundleTitle", { id: record.id }), 1, format),
    "",
  ];
  const isChild = record.parent_request_id !== null;

  const time = record.completed_at
    ? `${record.started_at} → ${record.completed_at}`
    : record.started_at;
  const latency =
    record.latency_ms !== null
      ? i18n.t("audit.latencyPart", { ms: record.latency_ms })
      : "";
  lines.push(bullet(i18n.t("audit.timeLine", { time, latency }), format));
  const httpStatus =
    record.http_status !== null
      ? i18n.t("audit.httpPart", { status: record.http_status })
      : "";
  lines.push(
    bullet(
      i18n.t("audit.statusLine", {
        status: statusLabel(
          displayRequestStatus(record.status, record.http_status),
        ),
        http: httpStatus,
      }),
      format,
    ),
  );
  lines.push(
    bullet(
      i18n.t("audit.protocolLine", {
        protocol: record.input_protocol,
        model: record.requested_model ?? i18n.t("audit.unknownModel"),
        streaming: record.streaming ? i18n.t("common.yes") : i18n.t("common.no"),
      }),
      format,
    ),
  );
  lines.push(
    bullet(
      i18n.t("audit.attemptLine", {
        attempt:
          record.attempt_index === 0
            ? i18n.t("records.neverReachedUpstream")
            : record.attempt_index,
      }) +
        (isChild
          ? i18n.t("audit.parentLine", { id: record.parent_request_id })
          : i18n.t("audit.childrenLine", { count: record.child_count })),
      format,
    ),
  );
  const service = options.serviceLabel ?? record.service_id ?? i18n.t("audit.unrouted");
  const route = record.route_id
    ? i18n.t("audit.routeLine", { id: record.route_id })
    : "";
  lines.push(bullet(i18n.t("audit.serviceLine", { service, route }), format));
  if (record.usage) {
    const cacheParts: string[] = [];
    if (record.usage.cache_read_tokens !== undefined) {
      cacheParts.push(
        i18n.t("audit.cacheReadPart", { count: record.usage.cache_read_tokens }),
      );
    }
    if (record.usage.cache_write_tokens !== undefined) {
      cacheParts.push(
        i18n.t("audit.cacheWritePart", {
          count: record.usage.cache_write_tokens,
        }),
      );
    }
    const cached =
      cacheParts.length > 0
        ? i18n.t("audit.cachedPart", { parts: cacheParts.join(" / ") })
        : "";
    lines.push(
      bullet(
        i18n.t("audit.tokenLine", {
          input: record.usage.input_tokens,
          output: record.usage.output_tokens,
          total: record.usage.total_tokens,
          cached,
        }),
        format,
      ),
    );
  }
  if (record.privacy_restore) {
    const restore = record.privacy_restore;
    lines.push(
      bullet(
        i18n.t("audit.restoreLine", {
          enabled: restore.enabled
            ? i18n.t("records.on")
            : i18n.t("records.off"),
          mappings: restore.mapping_count,
          restored: restore.restored_count,
          fallback: restore.fallback_count,
        }),
        format,
      ),
    );
  }

  if (record.error) {
    lines.push(
      "",
      heading(i18n.t("records.error"), 2, format),
      bullet(
        i18n.t("audit.errorLine", {
          category: record.error.category,
          code: record.error.code,
          retryable: record.error.retryable
            ? i18n.t("common.yes")
            : i18n.t("common.no"),
        }),
        format,
      ),
      bullet(i18n.t("audit.errorMessage", { message: record.error.message }), format),
    );
  }

  if (!isChild) {
    lines.push(
      ...httpSection(i18n.t("records.clientHttp"), content?.http_meta ?? null, format),
    );
  }
  lines.push(
    ...httpSection(
      i18n.t("records.upstreamHttp"),
      content?.upstream_http_meta ?? null,
      format,
    ),
  );

  if (includeBodies) {
    if (!isChild) {
      lines.push(
        "",
        ...partSection(
          i18n.t("records.clientBody"),
          content?.request_body ?? null,
          format,
        ),
      );
      lines.push(
        "",
        ...partSection(
          i18n.t("records.clientResponseContent"),
          content?.response_content ?? null,
          format,
        ),
      );
    }
    lines.push(
      "",
      ...partSection(
        i18n.t("records.upstreamBody"),
        content?.upstream_request_body ?? null,
        format,
      ),
    );
    lines.push(
      "",
      ...partSection(
        i18n.t("records.upstreamResponseContent"),
        content?.upstream_response_content ?? null,
        format,
      ),
    );
  }

  return lines.join("\n");
}
