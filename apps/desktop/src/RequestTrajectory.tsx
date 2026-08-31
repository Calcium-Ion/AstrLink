import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { AuditPartSection } from "./AuditReviewer";
import type { CopyFeedback } from "./copy-feedback";
import { i18n } from "./i18n";
import type { AuditContent, RequestRecord } from "./request-record-model";
import {
  extractPrivacyHits,
  inspectorPart,
  inspectorTitle,
  recordedPrivacyHits,
  trajectoryLanes,
  trajectoryRows,
  type PrivacyHitGroup,
  type TrajectoryChip,
  type TrajectoryLane,
  type TrajectoryRow,
  type TrajectoryTone,
} from "./request-trajectory-model";
import { protocolEntryPath } from "./service-presets";

const chipClass: Record<TrajectoryChip, string> = {
  CLIENT: "bg-primary text-primary-foreground",
  POLICY: "bg-warning text-primary-foreground",
  ROUTE: "bg-tide text-primary-foreground",
  UPSTREAM: "bg-warning text-primary-foreground",
  RETRY: "bg-warning-wash text-warning-foreground",
  RESTORE: "bg-violet text-primary-foreground",
  RESULT: "bg-success text-primary-foreground",
};

const failedPhaseChips = new Set<TrajectoryChip>([
  "RESULT",
  "UPSTREAM",
  "RETRY",
]);

function chipToneClass(chip: TrajectoryChip, tone: TrajectoryTone): string {
  if (tone === "failed" && failedPhaseChips.has(chip)) {
    return "bg-destructive text-destructive-foreground";
  }
  if (tone === "blocked" && (chip === "RESULT" || chip === "POLICY")) {
    return "bg-warning text-primary-foreground";
  }
  if (tone === "cancelled" && chip === "RESULT") {
    return "bg-muted text-muted-foreground";
  }
  if (tone === "pending" && chip === "RESULT") {
    return "bg-warning text-primary-foreground";
  }
  return chipClass[chip];
}

function laneToneClass(lane: TrajectoryLane, tone: TrajectoryTone): string {
  if (tone === "failed") return "bg-destructive";
  if (tone === "blocked") return "bg-warning";
  if (tone === "pending") return "bg-warning/70";
  return laneClass[lane];
}

const laneClass: Record<TrajectoryLane, string> = {
  client: "bg-primary/70",
  gateway: "bg-tide/80",
  upstream: "bg-warning",
};

const laneLabel: Record<TrajectoryLane, string> = {
  client: "Client",
  gateway: "Gateway",
  upstream: "Upstream",
};

export function RequestTrajectory({
  turns,
  childrenByRoot,
  nowMs,
  selectedRequestId,
  onSelectRequest,
  auditContent,
  auditLoading,
  auditError,
  copyFeedback,
}: {
  turns: RequestRecord[];
  childrenByRoot: Record<string, RequestRecord[]>;
  nowMs: number;
  selectedRequestId: string | null;
  onSelectRequest: (requestId: string) => void;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  copyFeedback: CopyFeedback;
}) {
  const rows = useMemo(
    () => trajectoryRows(turns, childrenByRoot),
    [childrenByRoot, turns],
  );
  const lanes = trajectoryLanes(rows, nowMs);
  const turnKey = turns.map((turn) => turn.id).join(",");
  const [selectedRowId, setSelectedRowId] = useState<string | null>(null);
  const [narrowOpen, setNarrowOpen] = useState(true);

  useEffect(() => {
    setSelectedRowId(rows[rows.length - 1]?.id ?? null);
    setNarrowOpen(true);
  }, [turnKey]);

  const selectedRow =
    rows.find((row) => row.id === selectedRowId) ?? rows[rows.length - 1] ?? null;
  const selectedRecord = selectedRow
    ? findRecord(turns, childrenByRoot, selectedRow.requestId)
    : null;

  return (
    <div className="@container flex h-full min-h-0 min-w-0 flex-1 flex-col">
      <div className="shrink-0 space-y-1.5 border-b py-3">
        {(["client", "gateway", "upstream"] as TrajectoryLane[]).map((lane) => (
          <div className="flex items-center gap-2" key={lane}>
            <span className="w-16 shrink-0 text-micro font-medium text-muted-foreground">
              {laneLabel[lane]}
            </span>
            <div className="relative h-2.5 flex-1 overflow-hidden rounded-sm bg-muted">
              {lanes.segments
                .filter((segment) => segment.lane === lane)
                .map((segment, index) => (
                  <span
                    className={cn(
                      "absolute top-0 h-full rounded-sm",
                      laneToneClass(lane, segment.tone),
                    )}
                    key={`${lane}-${index}-${segment.startMs}`}
                    style={{
                      left: `${(segment.startMs / lanes.durationMs) * 100}%`,
                      width: `${Math.max(1.2, ((segment.endMs - segment.startMs) / lanes.durationMs) * 100)}%`,
                    }}
                  />
                ))}
            </div>
          </div>
        ))}
      </div>
      <div className="relative flex min-h-0 min-w-0 flex-1">
        <ol
          className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
          data-testid="trajectory-list"
        >
          {rows.map((row) => (
            <TrajectoryRowView
              key={row.id}
              onSelect={() => {
                setSelectedRowId(row.id);
                setNarrowOpen(true);
                onSelectRequest(row.requestId);
              }}
              row={row}
              selected={row.id === selectedRow?.id}
            />
          ))}
        </ol>
        {selectedRow && selectedRecord ? (
          <TrajectoryInspector
            auditContent={
              auditContent?.request_id === selectedRow.requestId
                ? auditContent
                : null
            }
            auditError={
              selectedRequestId === selectedRow.requestId ? auditError : null
            }
            auditLoading={
              selectedRequestId === selectedRow.requestId &&
              (auditLoading ||
                auditContent?.request_id !== selectedRow.requestId)
            }
            copyFeedback={copyFeedback}
            onClose={() => setNarrowOpen(false)}
            record={selectedRecord}
            row={selectedRow}
            visible={narrowOpen}
          />
        ) : null}
      </div>
    </div>
  );
}

function findRecord(
  turns: RequestRecord[],
  childrenByRoot: Record<string, RequestRecord[]>,
  requestId: string,
): RequestRecord | null {
  for (const turn of turns) {
    if (turn.id === requestId) return turn;
    const child = (childrenByRoot[turn.id] ?? []).find(
      (item) => item.id === requestId,
    );
    if (child) return child;
  }
  return null;
}

function TrajectoryInspector({
  row,
  record,
  auditContent,
  auditLoading,
  auditError,
  copyFeedback,
  visible,
  onClose,
}: {
  row: TrajectoryRow;
  record: RequestRecord;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  copyFeedback: CopyFeedback;
  visible: boolean;
  onClose: () => void;
}) {
  const part = inspectorPart(row.chip);
  const t = i18n.t.bind(i18n);
  const title =
    row.chip === "POLICY"
      ? recordedPrivacyHits(record.privacy_restore).length > 0
        ? t("trajectory.hit")
        : t("trajectory.miss")
      : inspectorTitle(row.chip);
  return (
    <aside
      className={cn(
        "flex min-h-0 w-[40%] min-w-[15rem] max-w-md shrink-0 flex-col border-l bg-card",
        "@max-[720px]:absolute @max-[720px]:inset-y-0 @max-[720px]:right-0 @max-[720px]:z-10 @max-[720px]:w-[min(100%,22rem)] @max-[720px]:shadow-lg",
        !visible && "@max-[720px]:hidden",
      )}
      data-chip={row.chip}
      data-testid="trajectory-inspector"
    >
      <header className="flex shrink-0 items-center justify-between gap-2 border-b px-3 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <span
            className={cn(
              "inline-flex h-5 items-center justify-center rounded-sm px-1 text-micro font-semibold tracking-wide",
              chipToneClass(row.chip, row.tone),
            )}
          >
            {row.chip}
          </span>
          <strong className="truncate text-xs font-medium">{title}</strong>
        </div>
        <Button
          className="hidden h-7 @max-[720px]:inline-flex"
          data-testid="trajectory-inspector-close"
          onClick={onClose}
          size="sm"
          type="button"
          variant="outline"
        >
          {t("common.close")}
        </Button>
      </header>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain p-3">
        {auditError ? (
          <p className="text-xs text-danger-foreground" role="alert">
            {auditError}
          </p>
        ) : null}
        {auditLoading ? (
          <p className="text-xs text-muted-foreground" role="status">
            {t("records.decrypting")}
          </p>
        ) : null}
        {part === "route" ? (
          <RouteInspector record={record} row={row} />
        ) : (
          <BodyInspector
            auditContent={auditContent}
            auditLoading={auditLoading}
            copyFeedback={copyFeedback}
            part={part}
            record={record}
            row={row}
          />
        )}
      </div>
    </aside>
  );
}

function RouteInspector({
  record,
  row,
}: {
  record: RequestRecord;
  row: TrajectoryRow;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <dl className="grid gap-2 text-xs">
      <InspectorField label={t("trajectory.summary")} value={row.summary} />
      <InspectorField
        code
        label={t("trajectory.entry")}
        value={protocolEntryPath(record.input_protocol, {
          streaming: record.streaming,
        })}
      />
      <InspectorField
        code
        label={t("trajectory.protocol")}
        value={record.input_protocol}
      />
      <InspectorField
        label={t("trajectory.service")}
        value={record.service_id ?? "—"}
      />
      <InspectorField
        label={t("trajectory.route")}
        value={record.route_id ?? "—"}
      />
    </dl>
  );
}

function BodyInspector({
  part,
  row,
  record,
  auditContent,
  auditLoading,
  copyFeedback,
}: {
  part: Exclude<ReturnType<typeof inspectorPart>, "route">;
  row: TrajectoryRow;
  record: RequestRecord;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  copyFeedback: CopyFeedback;
}) {
  const t = i18n.t.bind(i18n);
  const captured = auditPart(auditContent, part);
  const sectionKey = `trajectory-${row.chip}-${part}`;
  const hits = captured ? extractPrivacyHits(captured.content) : [];
  const httpStatus = inspectorHttpStatus(row.chip, record, auditContent);

  if (row.chip === "POLICY") {
    return (
      <PolicyInspector
        auditLoading={auditLoading}
        captured={captured}
        copyFeedback={copyFeedback}
        hits={recordedPrivacyHits(record.privacy_restore)}
        protocol={record.input_protocol}
        sectionKey={sectionKey}
      />
    );
  }

  return (
    <>
      {httpStatus !== null &&
      (row.chip === "UPSTREAM" ||
        row.chip === "RETRY" ||
        row.chip === "RESULT") ? (
        <HttpStatusLine failed={httpStatus >= 400} status={httpStatus} />
      ) : null}
      {row.chip === "RESTORE" ? (
        <RestoreSummary hits={hits} record={record} />
      ) : null}
      {!auditLoading && !captured ? (
        <p className="text-xs leading-6 text-muted-foreground">
          {t("trajectory.uncapturedHint")}
        </p>
      ) : null}
      {captured ? (
        <AuditPartSection
          copyFeedback={copyFeedback}
          part={captured}
          protocol={record.input_protocol}
          sectionKey={sectionKey}
          title={bodySectionTitle(row.chip)}
        />
      ) : null}
    </>
  );
}

function PolicyInspector({
  hits,
  captured,
  auditLoading,
  protocol,
  sectionKey,
  copyFeedback,
}: {
  hits: PrivacyHitGroup[];
  captured: ReturnType<typeof auditPart>;
  auditLoading: boolean;
  protocol: string;
  sectionKey: string;
  copyFeedback: CopyFeedback;
}) {
  const t = i18n.t.bind(i18n);
  const detailsRef = useRef<HTMLDetailsElement>(null);
  const revealKind = (kind: string) => {
    const details = detailsRef.current;
    if (!details) return;
    details.open = true;
    requestAnimationFrame(() => {
      details
        .querySelector<HTMLElement>(`mark[data-kind="${kind}"]`)
        ?.scrollIntoView({ block: "center" });
    });
  };
  return (
    <>
      {!auditLoading && hits.length === 0 ? (
        <p className="text-xs leading-6 text-muted-foreground">
          {t("trajectory.miss")}
        </p>
      ) : null}
      {hits.length > 0 ? (
        <PrivacyHitList
          hits={hits}
          onSelectKind={captured ? revealKind : undefined}
        />
      ) : null}
      {captured ? (
        <details
          className="group rounded-md border bg-card"
          data-testid="redacted-request-details"
          ref={detailsRef}
        >
          <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-xs font-medium text-muted-foreground before:text-base before:leading-none before:content-['›'] group-open:before:rotate-90 [&::-webkit-details-marker]:hidden">
            {t("trajectory.redactedRequest")}
          </summary>
          <div className="border-t">
            <AuditPartSection
              copyFeedback={copyFeedback}
              part={captured}
              protocol={protocol}
              sectionKey={sectionKey}
              title={t("trajectory.redactedRequest")}
            />
          </div>
        </details>
      ) : null}
    </>
  );
}

function RestoreSummary({
  record,
  hits,
}: {
  record: RequestRecord;
  hits: PrivacyHitGroup[];
}) {
  const t = i18n.t.bind(i18n);
  const restore = record.privacy_restore;
  const channels =
    restore === null || restore === undefined
      ? null
      : t("trajectory.restoreCounts", {
          visible: restore.visible_restored_count,
          tools: restore.tool_argument_restored_count,
        });
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        {restore
          ? t("trajectory.restoreRatio", {
              restored: restore.restored_count,
              mapped: restore.mapping_count,
            })
          : t("trajectory.restoreChip")}
      </p>
      {channels !== null ? (
        <p className="text-xs text-muted-foreground" data-testid="restore-channels">
          {channels}
        </p>
      ) : null}
      {hits.length > 0 ? (
        <div>
          <p className="mb-1.5 text-xs text-muted-foreground">
            {t("trajectory.unrestoredPlaceholders")}
          </p>
          <PrivacyHitList hits={hits} />
        </div>
      ) : null}
    </div>
  );
}

function PrivacyHitList({
  hits,
  onSelectKind,
}: {
  hits: PrivacyHitGroup[];
  onSelectKind?: (kind: string) => void;
}) {
  return (
    <ul className="grid gap-2" data-testid="privacy-hits">
      {hits.map((hit) => (
        <li key={hit.kind}>
          {onSelectKind ? (
            <Button
              className="h-auto px-0 text-xs font-medium"
              data-kind={hit.kind}
              onClick={() => onSelectKind(hit.kind)}
              type="button"
              variant="link"
            >
              {hit.label} ×{hit.count}
            </Button>
          ) : (
            <div className="text-xs font-medium">
              {hit.label} ×{hit.count}
            </div>
          )}
          {hit.placeholders.length > 0 ? (
            <ul className="mt-0.5 grid gap-0.5">
              {hit.placeholders.map((placeholder) => (
                <li key={placeholder}>
                  <code className="rounded-sm bg-warning-wash px-0.5 font-mono text-micro text-warning-foreground">
                    {placeholder}
                  </code>
                </li>
              ))}
            </ul>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

function HttpStatusLine({
  status,
  failed,
}: {
  status: number;
  failed: boolean;
}) {
  return (
    <p
      className={cn(
        "font-mono text-xs",
        failed ? "text-destructive" : "text-muted-foreground",
      )}
      data-testid="inspector-http"
    >
      HTTP {status}
    </p>
  );
}

function inspectorHttpStatus(
  chip: TrajectoryChip,
  record: RequestRecord,
  auditContent: AuditContent | null,
): number | null {
  if (chip === "UPSTREAM" || chip === "RETRY") {
    return (
      auditContent?.upstream_http_meta?.response_status ?? record.http_status
    );
  }
  if (chip === "RESULT") {
    return record.http_status;
  }
  return null;
}

function bodySectionTitle(chip: TrajectoryChip): string {
  switch (chip) {
    case "CLIENT":
      return i18n.t("trajectory.clientBody");
    case "POLICY":
      return i18n.t("trajectory.redactedRequest");
    case "UPSTREAM":
    case "RETRY":
      return i18n.t("trajectory.upstreamResponse");
    case "RESTORE":
    case "RESULT":
      return i18n.t("trajectory.clientResponse");
    default:
      return inspectorTitle(chip);
  }
}

function auditPart(
  content: AuditContent | null,
  part: Exclude<ReturnType<typeof inspectorPart>, "route">,
) {
  if (!content) return null;
  switch (part) {
    case "request_body":
      return content.request_body;
    case "upstream_request_body":
      return content.upstream_request_body;
    case "upstream_response_content":
      return content.upstream_response_content;
    case "response_content":
      return content.response_content;
  }
}

function InspectorField({
  label,
  value,
  code,
}: {
  label: string;
  value: string;
  code?: boolean;
}) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 text-foreground">
        {code ? <code className="font-mono">{value}</code> : value}
      </dd>
    </div>
  );
}

function TrajectoryRowView({
  row,
  selected,
  onSelect,
}: {
  row: TrajectoryRow;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <li>
      <Button
        className={cn(
          "grid h-auto w-full shrink-0 grid-cols-[4.5rem_minmax(0,1fr)] items-center gap-2 rounded-none border-b bg-transparent px-0 py-2 text-left text-xs text-foreground shadow-none hover:bg-muted",
          row.tone === "failed" && "bg-danger-wash/60 hover:bg-danger-wash",
          row.tone === "blocked" && "bg-warning-wash/60 hover:bg-warning-wash",
          selected && "bg-accent hover:bg-accent",
        )}
        data-chip={row.chip}
        data-request-id={row.requestId}
        data-testid="trajectory-row"
        data-tone={row.tone}
        onClick={onSelect}
        type="button"
        variant="ghost"
      >
        <span
          className={cn(
            "inline-flex h-5 items-center justify-center rounded-sm px-1 text-micro font-semibold tracking-wide",
            chipToneClass(row.chip, row.tone),
          )}
        >
          {row.chip}
        </span>
        <span className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 font-mono">
          <span className="truncate text-foreground">{row.summary}</span>
          <span
            className={cn(
              "shrink-0 text-muted-foreground",
              row.tone === "failed" && "text-destructive",
              row.tone === "blocked" && "text-warning-foreground",
            )}
          >
            → {row.result}
          </span>
        </span>
      </Button>
    </li>
  );
}
