import { RecoveryDetails } from "./components/RecoveryDetails";
import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronRight, MapPin as Pin, MapPinOff as PinOff } from "@/components/icons";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { AuditPartSection } from "./AuditReviewer";
import type { CopyFeedback } from "./copy-feedback";
import { i18n, useT } from "./i18n";
import type { AuditContent, RequestRecord } from "./request-record-model";
import {
  clientDisconnectNote,
  extractPrivacyHits,
  inspectorChainRows,
  inspectorPart,
  inspectorTitle,
  recordedPrivacyHits,
  type PrivacyHitGroup,
  type TrajectoryChip,
  type TrajectoryRow,
} from "./request-trajectory-model";
import { protocolEntryPath } from "./service-presets";
import { CHIP_BADGE_CLASS, chipToneClass } from "./trajectory-chip";

/**
 * One selected call. Header chips are tabs; only the active section body is
 * mounted. The pane fills whatever it is put in: its own window on the
 * desktop, an overlay above the list in the browser preview.
 *
 * `onClose` is set only where the host has no window controls of its own, and
 * `onTogglePin` only where there is a window to pin. Clicking a chip here
 * only switches the tab.
 */
export function TrajectoryInspector({
  row,
  record,
  auditContent,
  auditLoading,
  auditError,
  copyFeedback,
  pinned = false,
  onTogglePin,
  onClose,
}: {
  row: TrajectoryRow;
  record: RequestRecord;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  copyFeedback: CopyFeedback;
  pinned?: boolean;
  onTogglePin?: (pinned: boolean) => void;
  onClose?: () => void;
}) {
  const t = useT();
  const chain = useMemo(() => inspectorChainRows(record), [record]);
  const requestedTab = tabChip(row.chip);
  const [focusChip, setFocusChip] = useState(requestedTab);
  useEffect(() => {
    setFocusChip(
      chain.some((item) => item.chip === requestedTab)
        ? requestedTab
        : (chain[0]?.chip ?? requestedTab),
    );
  }, [chain, record.id, requestedTab]);
  const focusRow =
    chain.find((item) => item.chip === focusChip) ??
    chain.find((item) => item.chip === requestedTab) ??
    chain[0] ??
    null;
  const client = chain.find((item) => item.chip === "CLIENT");
  const result =
    chain.find((item) => item.chip === "RESULT") ?? chain[chain.length - 1];
  const title =
    client?.summary ??
    record.requested_model ??
    t("records.unspecifiedModel");
  const outcome = result?.result ?? "";
  const hideRestoreBody = chain.some((item) => item.chip === "RESULT");

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col bg-card"
      data-focus-chip={row.chip}
      data-pinned={pinned}
      data-request-id={record.id}
      data-testid="trajectory-inspector"
    >
      <header className="flex shrink-0 items-center justify-between gap-2 border-b px-3 py-2">
        <div className="flex min-w-0 items-baseline gap-2">
          <strong className="truncate text-xs font-medium">{title}</strong>
          {outcome ? (
            <span className="shrink-0 font-mono text-micro text-muted-foreground">
              → {outcome}
            </span>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {onTogglePin ? (
            <Button
              aria-label={pinned ? t("trajectory.unpin") : t("trajectory.pin")}
              aria-pressed={pinned}
              data-testid="trajectory-inspector-pin"
              onClick={() => onTogglePin(!pinned)}
              size="icon-sm"
              title={pinned ? t("trajectory.unpinHint") : t("trajectory.pinHint")}
              type="button"
              variant={pinned ? "default" : "outline"}
            >
              {pinned ? <PinOff /> : <Pin />}
            </Button>
          ) : null}
          {onClose ? (
            <Button
              className="h-7"
              data-testid="trajectory-inspector-close"
              onClick={onClose}
              size="sm"
              type="button"
              variant="outline"
            >
              {t("common.close")}
            </Button>
          ) : null}
        </div>
      </header>
      <div
        className="flex shrink-0 flex-wrap gap-1 border-b px-3 py-1.5"
        data-testid="inspector-tabs"
        role="tablist"
      >
        {chain.map((item) => {
          const selected = item.chip === focusChip;
          return (
            <Button variant="ghost"
              aria-selected={selected}
              className={cn(
                CHIP_BADGE_CLASS,
                chipToneClass(item.chip, item.tone),
                selected && "ring-2 ring-ring ring-offset-1 ring-offset-card",
              )}
              data-chip={item.chip}
              data-testid="inspector-tab"
              key={item.id}
              onClick={() => setFocusChip(item.chip)}
              role="tab"
              type="button"
            >
              {t(`trajectory.chips.${item.chip}`)}
            </Button>
          );
        })}
      </div>
      {auditError ? (
        <p className="shrink-0 px-3 pt-2 text-xs text-danger-foreground" role="alert">
          {auditError}
        </p>
      ) : null}
      {auditLoading ? (
        <p className="shrink-0 px-3 pt-2 text-xs text-muted-foreground" role="status">
          {t("records.decrypting")}
        </p>
      ) : null}
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-3">
        {focusRow ? (
          <InspectorSection
            auditContent={auditContent}
            auditLoading={auditLoading}
            copyFeedback={copyFeedback}
            omitCapturedBody={focusRow.chip === "RESTORE" && hideRestoreBody}
            record={record}
            row={focusRow}
          />
        ) : null}
      </div>
    </div>
  );
}

function tabChip(chip: TrajectoryChip): TrajectoryChip {
  return chip === "TURN" ? "CLIENT" : chip;
}

function InspectorSection({
  row,
  record,
  auditContent,
  auditLoading,
  copyFeedback,
  omitCapturedBody,
}: {
  row: TrajectoryRow;
  record: RequestRecord;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  copyFeedback: CopyFeedback;
  omitCapturedBody: boolean;
}) {
  const part = inspectorPart(row.chip);
  const t = i18n.t.bind(i18n);
  const title =
    row.chip === "POLICY"
      ? recordedPrivacyHits(record.privacy_restore).length > 0
        ? t("trajectory.hit")
        : t("trajectory.miss")
      : inspectorTitle(row.chip);
  const captured =
    part === "route" || omitCapturedBody ? null : auditPart(auditContent, part);
  const httpStatus = inspectorHttpStatus(row.chip, record, auditContent);
  const disconnectNote = clientDisconnectNote(record);
  return (
    <section
      className="space-y-2"
      data-chip={row.chip}
      data-testid="inspector-section"
    >
      {disconnectNote ? (
        <p
          className="rounded-sm bg-warning-wash px-2 py-1.5 text-xs text-warning-foreground"
          data-testid="trajectory-cancel-note"
          role="status"
        >
          {disconnectNote}
        </p>
      ) : null}
      <header className="flex min-w-0 items-center gap-2">
        <strong className="truncate text-xs font-medium">{title}</strong>
        {httpStatus !== null ? (
          <span
            className={cn(
              "shrink-0 font-mono text-micro",
              httpStatus >= 400 ? "text-destructive" : "text-muted-foreground",
            )}
          >
            HTTP {httpStatus}
          </span>
        ) : null}
        {captured ? (
          <span className="shrink-0 text-micro text-muted-foreground">
            {formatCapturedBytes(captured.captured_bytes)}
          </span>
        ) : null}
      </header>
      {part === "route" ? (
        <><RouteInspector record={record} row={row} /><RecoveryDetails value={record.recovery} /></>
      ) : (
        <BodyInspector
          auditContent={auditContent}
          auditLoading={auditLoading}
          copyFeedback={copyFeedback}
          omitCapturedBody={omitCapturedBody}
          part={part}
          record={record}
          row={row}
        />
      )}
    </section>
  );
}

function formatCapturedBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KB`;
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
  omitCapturedBody,
}: {
  part: Exclude<ReturnType<typeof inspectorPart>, "route">;
  row: TrajectoryRow;
  record: RequestRecord;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  copyFeedback: CopyFeedback;
  omitCapturedBody: boolean;
}) {
  const captured = omitCapturedBody ? null : auditPart(auditContent, part);
  const unrestoredHits = omitCapturedBody
    ? extractPrivacyHits(auditPart(auditContent, part)?.content ?? "")
    : captured
      ? extractPrivacyHits(captured.content)
      : [];
  const sectionKey = `trajectory-${row.chip}-${part}`;
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
        <RestoreSummary hits={unrestoredHits} record={record} />
      ) : null}
      {!omitCapturedBody && !auditLoading && !captured ? (
        <p
          className="text-xs leading-6 text-muted-foreground"
          data-testid="inspector-missing-body"
        >
          {missingBodyHint(record)}
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
          <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-xs font-medium text-muted-foreground [&::-webkit-details-marker]:hidden">
            <ChevronRight className="size-3.5 group-open:rotate-90" />
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

function missingBodyHint(record: RequestRecord): string {
  if (record.status === "pending") {
    return i18n.t("trajectory.pendingCaptureHint");
  }
  return i18n.t("trajectory.uncapturedHint");
}

function bodySectionTitle(chip: TrajectoryChip): string {
  switch (chip) {
    case "TURN":
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
