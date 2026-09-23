import type { ExportEnvironment } from "./export-environment";
import { i18n } from "./i18n";
import type {
  AuditContent,
  AuditContentPart,
  AuditHeader,
  RequestRecord,
  RequestSession,
} from "./request-record-model";
import { displayRequestStatus, statusLabel } from "./request-record-model";
import { formatDuration, liveDurationMs } from "./request-live-model";
import { missingServiceLabel } from "./request-service-model";
import {
  inspectorChainRows,
  recordTrajectoryRows,
  type TrajectoryRow,
} from "./request-trajectory-model";
import { buildSkillDiagnosticPayload } from "./skill-diagnostic";

export type BundleFormat = "markdown" | "txt";

export interface RecordBundleOptions {
  includeBodies?: boolean;
  /** Human-readable service name resolved by the caller. */
  serviceLabel?: string | null;
  format?: BundleFormat;
  /** Defaults to now; pending durations are measured up to this instant. */
  exportedAt?: Date;
  session?: RequestSession;
  /** The session's calls, oldest first, as the detail loaded them. */
  turns?: RequestRecord[];
  childrenByRoot?: Record<string, RequestRecord[]>;
  serviceNames?: Record<string, string>;
  environment?: ExportEnvironment;
}

// The calls around the exported one that a diagnosis usually needs: the
// failures that led up to it and the retries the client made after.
const SESSION_CALLS_BEFORE = 10;
const SESSION_CALLS_AFTER = 5;

export function bundleFilename(recordId: string, format: BundleFormat): string {
  const ext = format === "markdown" ? "md" : "txt";
  const safe = recordId
    .replace(/[^A-Za-z0-9._-]+/g, "_")
    .replace(/^_+|_+$/g, "");
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
  return headers.map((header) => `${header.name}: ${header.value}`).join("\n");
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
  missing = i18n.t("audit.noHttp"),
): string[] {
  if (meta === null) {
    return ["", heading(title, 2, format), missing];
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

function elapsedMs(startedAt: string, endedAt: string | null, nowMs: number) {
  const started = Date.parse(startedAt);
  if (Number.isNaN(started)) return 0;
  const ended = endedAt === null ? nowMs : Date.parse(endedAt);
  return Math.max(0, (Number.isNaN(ended) ? nowMs : ended) - started);
}

function exportRows(
  record: RequestRecord,
  childrenByRoot: Record<string, RequestRecord[]>,
): TrajectoryRow[] {
  const rows =
    record.parent_request_id === null
      ? recordTrajectoryRows(record, childrenByRoot)
      : inspectorChainRows(record);
  return rows
    .map((row, index) => ({ row, index }))
    .sort(
      (left, right) =>
        Date.parse(left.row.startedAt) - Date.parse(right.row.startedAt) ||
        left.index - right.index,
    )
    .map(({ row }) => row);
}

function rowDuration(row: TrajectoryRow, nowMs: number): string {
  const duration = formatDuration(elapsedMs(row.startedAt, row.endedAt, nowMs));
  return row.endedAt === null
    ? i18n.t("audit.openDuration", { duration })
    : duration;
}

// The newest phase still waiting is where a pending request is stuck.
function currentStage(rows: TrajectoryRow[]): TrajectoryRow | null {
  for (let index = rows.length - 1; index >= 0; index--) {
    const row = rows[index];
    if (row.endedAt === null && row.status === "pending") return row;
  }
  return null;
}

function trajectorySection(
  record: RequestRecord,
  rows: TrajectoryRow[],
  nowMs: number,
  format: BundleFormat,
): string[] {
  const lines = ["", heading(i18n.t("audit.trajectoryTitle"), 2, format)];
  if (rows.length === 0) {
    lines.push(i18n.t("audit.none"));
    return lines;
  }
  for (const row of rows) {
    const offset = formatDuration(
      elapsedMs(record.started_at, row.startedAt, nowMs),
    );
    lines.push(
      bullet(
        [
          `+${offset}`,
          i18n.t(`trajectory.chips.${row.chip}`),
          row.result,
          rowDuration(row, nowMs),
          row.summary,
        ].join(" · "),
        format,
      ),
    );
  }
  return lines;
}

function sessionWindow(
  record: RequestRecord,
  turns: RequestRecord[],
): { calls: RequestRecord[]; from: number } {
  const rootId = record.parent_request_id ?? record.id;
  const found = turns.findIndex((turn) => turn.id === rootId);
  const selected = found === -1 ? turns.length - 1 : found;
  const from = Math.max(0, selected - SESSION_CALLS_BEFORE);
  return {
    calls: turns.slice(from, selected + SESSION_CALLS_AFTER + 1),
    from,
  };
}

function sessionSection(
  record: RequestRecord,
  turns: RequestRecord[],
  window: { calls: RequestRecord[]; from: number },
  serviceNames: Record<string, string>,
  nowMs: number,
  format: BundleFormat,
): string[] {
  const lines = [
    "",
    heading(
      i18n.t("audit.sessionTitle", {
        from: window.from + 1,
        to: window.from + window.calls.length,
        total: turns.length,
      }),
      2,
      format,
    ),
  ];
  const rootId = record.parent_request_id ?? record.id;
  for (const call of window.calls) {
    const parts = [
      call.turn_index !== null
        ? i18n.t("trajectory.turnHeader", { index: call.turn_index })
        : i18n.t("trajectory.turnHeaderUnnumbered"),
      call.started_at,
      statusLabel(displayRequestStatus(call.status, call.http_status)),
      i18n.t("audit.sessionAttempts", {
        attempt: call.attempt_index,
        children: call.child_count,
      }),
      serviceLabel(call, serviceNames),
      formatDuration(liveDurationMs(call, nowMs)),
    ];
    if (call.error) parts.push(`${call.error.category} · ${call.error.code}`);
    parts.push(call.id);
    const marker = call.id === rootId ? "▶ " : "  ";
    lines.push(bullet(marker + parts.join(" · "), format));
  }
  return lines;
}

function serviceLabel(
  record: RequestRecord,
  serviceNames: Record<string, string>,
): string {
  if (!record.service_id) return i18n.t(missingServiceLabel(record.status));
  return serviceNames[record.service_id] ?? record.service_id;
}

function environmentSection(
  environment: ExportEnvironment,
  format: BundleFormat,
): string[] {
  const unavailable = i18n.t("audit.environmentUnavailable");
  const onOff = (value: boolean) =>
    value ? i18n.t("records.on") : i18n.t("records.off");
  const { privacy, limits, routing, capture } = environment;
  return [
    "",
    heading(i18n.t("audit.environmentTitle"), 2, format),
    bullet(
      privacy
        ? i18n.t("audit.privacyConfigLine", {
            enabled: onOff(privacy.enabled),
            detector: privacy.detector,
            model:
              privacy.detector === "local_model"
                ? (privacy.local_model_name ??
                  privacy.local_model_id ??
                  i18n.t("audit.none"))
                : i18n.t("audit.none"),
            action: privacy.request_action,
            restore: onOff(privacy.response_restore),
          })
        : i18n.t("audit.privacyConfigLine", {
            enabled: unavailable,
            detector: unavailable,
            model: unavailable,
            action: unavailable,
            restore: unavailable,
          }),
      format,
    ),
    bullet(
      privacy
        ? i18n.t("audit.toolDeclarationsLine", {
            tools: onOff(privacy.skip_tool_declarations),
            additionalTools: onOff(!privacy.inspect_additional_tools),
          })
        : i18n.t("audit.toolDeclarationsLine", {
            tools: unavailable,
            additionalTools: unavailable,
          }),
      format,
    ),
    bullet(
      limits
        ? i18n.t("audit.limitsLine", {
            timeout: limits.response_start_timeout_seconds,
            inspections: limits.max_concurrent_inspections,
            body: limits.max_request_body_mib,
          })
        : i18n.t("audit.limitsLine", {
            timeout: unavailable,
            inspections: unavailable,
            body: unavailable,
          }),
      format,
    ),
    bullet(
      routing
        ? i18n.t("audit.routingLine", {
            strategy: routing.strategy,
            attempts: routing.max_attempts,
          })
        : i18n.t("audit.routingLine", {
            strategy: unavailable,
            attempts: unavailable,
          }),
      format,
    ),
    bullet(
      capture
        ? i18n.t("audit.captureLine", {
            request: onOff(capture.request_body_enabled),
            response: onOff(capture.response_content_enabled),
            http: onOff(capture.http_meta_enabled),
          })
        : i18n.t("audit.captureLine", {
            request: unavailable,
            response: unavailable,
            http: unavailable,
          }),
      format,
    ),
  ];
}

export function buildRecordBundle(
  record: RequestRecord,
  content: AuditContent | null,
  options: RecordBundleOptions = {},
): string {
  const includeBodies = options.includeBodies !== false;
  const format = options.format ?? "markdown";
  const exportedAt = options.exportedAt ?? new Date();
  const nowMs = exportedAt.getTime();
  const serviceNames = options.serviceNames ?? {};
  const childrenByRoot = options.childrenByRoot ?? {};
  const lines: string[] = [
    heading(i18n.t("audit.bundleTitle", { id: record.id }), 1, format),
    "",
  ];
  const isChild = record.parent_request_id !== null;
  const pending = record.status === "pending";
  const rows = exportRows(record, childrenByRoot);

  lines.push(
    bullet(
      i18n.t("audit.exportedAtLine", {
        time: exportedAt.toISOString(),
        elapsed: pending
          ? i18n.t("audit.elapsedPart", {
              duration: formatDuration(liveDurationMs(record, nowMs)),
            })
          : "",
      }),
      format,
    ),
  );
  const version = options.environment?.version;
  if (version) {
    lines.push(
      bullet(
        i18n.t("audit.versionLine", {
          app: version.app,
          core: version.core ?? i18n.t("audit.environmentUnavailable"),
          commit:
            version.build_commit ?? i18n.t("audit.environmentUnavailable"),
        }),
        format,
      ),
    );
  }

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
        streaming: record.streaming
          ? i18n.t("common.yes")
          : i18n.t("common.no"),
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
  const service =
    options.serviceLabel ??
    record.service_id ??
    i18n.t(missingServiceLabel(record.status));
  const route = record.route_id
    ? i18n.t("audit.routeLine", { id: record.route_id })
    : "";
  lines.push(bullet(i18n.t("audit.serviceLine", { service, route }), format));
  if (record.usage) {
    const cacheParts: string[] = [];
    if (record.usage.cache_read_tokens !== undefined) {
      cacheParts.push(
        i18n.t("audit.cacheReadPart", {
          count: record.usage.cache_read_tokens,
        }),
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

  const stage = pending ? currentStage(rows) : null;
  if (stage) {
    lines.push(
      bullet(
        i18n.t("audit.currentStageLine", {
          stage: i18n.t(`audit.stages.${stage.chip}`),
          duration: formatDuration(
            elapsedMs(stage.startedAt, stage.endedAt, nowMs),
          ),
          summary: stage.summary,
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
      bullet(
        i18n.t("audit.errorMessage", { message: record.error.message }),
        format,
      ),
    );
  }

  lines.push(...trajectorySection(record, rows, nowMs, format));
  const turns = options.turns ?? [];
  const window = sessionWindow(record, turns);
  if (window.calls.length > 0) {
    lines.push(
      ...sessionSection(record, turns, window, serviceNames, nowMs, format),
    );
  }
  if (options.environment) {
    lines.push(...environmentSection(options.environment, format));
  }

  if (!isChild) {
    lines.push(
      ...httpSection(
        i18n.t("records.clientHttp"),
        content?.http_meta ?? null,
        format,
      ),
    );
  }
  lines.push(
    ...httpSection(
      i18n.t("records.upstreamHttp"),
      content?.upstream_http_meta ?? null,
      format,
      // Metadata was not lost: the gateway never sent the request.
      record.attempt_index === 0
        ? i18n.t("audit.noUpstreamRequest")
        : i18n.t("audit.noHttp"),
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

  if (options.session) {
    const payload = {
      ...buildSkillDiagnosticPayload({
        session: options.session,
        selectedRequestId: record.id,
        turns: window.calls.length > 0 ? window.calls : [record],
        childrenByRoot: options.childrenByRoot,
        serviceNames,
      }),
      exported_at: exportedAt.toISOString(),
      environment: options.environment ?? null,
    };
    const json = JSON.stringify(payload, null, 2);
    lines.push(
      "",
      heading(i18n.t("audit.diagnosticTitle"), 2, format),
      format === "txt" ? json : fence(json, "json"),
    );
  }

  return lines.join("\n");
}
