import { RecoveryDetails } from "./components/RecoveryDetails";
import { ConversionDiagnosticsDetails } from "./components/ConversionDiagnosticsDetails";
import { RoutingSteps } from "./components/RoutingSteps";
import { useEffect, useMemo, useRef, useState } from "react";

import { LockKeyhole } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { ConversationIndicator } from "@/components/ConversationIndicator";
import { ModelLabel } from "@/components/ModelLabel";
import { RequestServiceLabel } from "@/components/RequestServiceLabel";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { cn } from "@/lib/utils";

import { AuditPartSection, withheldHint } from "./AuditReviewer";
import type { CopyFeedback } from "./copy-feedback";
import { i18n, useT } from "./i18n";
import {
  holdsLockedPart,
  type AuditContent,
  type RequestModelRedirect,
  type RequestRecord,
  type RequestStatus,
} from "./request-record-model";
import { routingSteps } from "./request-routing-model";
import {
  requestServiceIdentity,
  type RequestServiceIdentity,
  type RequestServiceMap,
} from "./request-service-model";
import {
  clientDisconnectNote,
  extractPrivacyHits,
  inspectorChainRows,
  inspectorPart,
  inspectorTitle,
  policyDecision,
  recordedPrivacyHits,
  type InspectorPart,
  type PolicyDecision,
  type PrivacyHitGroup,
  type TrajectoryChip,
  type TrajectoryRow,
} from "./request-trajectory-model";
import { protocolEntryPath } from "./service-presets";
import { chipDotClass } from "./trajectory-chip";
import {
  CapturePane,
  EndpointLine,
  InspectorFindShortcut,
  ResultInspector,
  UpstreamInspector,
} from "./TrajectoryResponse";

/** The parts backed by a captured body, as opposed to record metadata. */
type BodyPart = Exclude<InspectorPart, "route" | "redirect">;

/**
 * One selected call. Header chips are tabs; only the active section body is
 * mounted. The pane fills whatever it is put in: its own window on the
 * desktop, an overlay above the list in the browser preview.
 *
 * `onClose` is set only where the host has no window controls of its own. A
 * detached window keeps its pin in the title bar and passes `pinned` in.
 * Clicking a chip here only switches the tab. `onUnlockRaw` puts the unlock
 * in the header while any part of this call is sealed away. `findShortcut`
 * gives ⌘F to the body on screen, for a host whose window is the inspector.
 */
export function TrajectoryInspector({
  row,
  record,
  service = requestServiceIdentity(record),
  services,
  auditContent,
  auditLoading,
  auditError,
  copyFeedback,
  pinned = false,
  findShortcut = false,
  onClose,
  onUnlockRaw,
}: {
  row: TrajectoryRow;
  record: RequestRecord;
  service?: RequestServiceIdentity;
  services?: RequestServiceMap;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  copyFeedback: CopyFeedback;
  pinned?: boolean;
  findShortcut?: boolean;
  onClose?: () => void;
  onUnlockRaw?: () => void;
}) {
  const t = useT();
  const locked = auditContent !== null && holdsLockedPart(auditContent);
  const chain = useMemo(() => inspectorChainRows(record), [record]);
  const tabs = useMemo(() => inspectorTabs(chain), [chain]);
  const requestedTab = tabChip(row.chip);
  const [focusChip, setFocusChip] = useState(requestedTab);
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;
  // Only a different phase moves the tab. A poll hands down a fresh record for
  // the same one, and a running call grows new tabs; neither may pull the
  // operator off the tab they chose.
  useEffect(() => {
    const current = tabsRef.current;
    setFocusChip(
      current.some((item) => item.chip === requestedTab)
        ? requestedTab
        : (current[0]?.chip ?? requestedTab),
    );
  }, [record.id, row.id, requestedTab]);
  const focusRow =
    tabs.find((item) => item.chip === focusChip) ??
    tabs.find((item) => item.chip === requestedTab) ??
    tabs[0] ??
    null;
  const routes = useMemo(
    () => chain.filter((item) => item.chip === "ROUTE"),
    [chain],
  );
  const client = chain.find((item) => item.chip === "CLIENT");
  const result =
    chain.find((item) => item.chip === "RESULT") ?? chain[chain.length - 1];
  const title =
    client?.summary ?? record.requested_model ?? t("records.unspecifiedModel");
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
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1">
          <RequestServiceLabel
            className="text-xs font-medium"
            service={service}
          />
          <span className="flex min-w-0 max-w-full items-center gap-1.5">
            <strong
              className="min-w-0 truncate text-xs font-medium"
              title={title}
            >
              {title}
            </strong>
            {record.session_link ? (
              <ConversationIndicator kind="continuation" />
            ) : null}
          </span>
          {outcome ? (
            <span className="shrink-0 font-mono text-micro text-muted-foreground">
              → {outcome}
            </span>
          ) : null}
        </div>
        {locked && onUnlockRaw ? (
          <Button
            className="h-7"
            data-testid="trajectory-inspector-unlock"
            onClick={onUnlockRaw}
            size="sm"
            type="button"
            variant="outline"
          >
            <LockKeyhole aria-hidden="true" className="size-3.5" />
            {t("rawSealing.unlockTitle")}
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
      </header>
      <div
        className="flex shrink-0 gap-3.5 overflow-x-auto border-b px-3 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
        data-testid="inspector-tabs"
        role="tablist"
      >
        {tabs.map((item) => {
          const selected = item.chip === focusChip;
          return (
            <Button
              variant="ghost"
              aria-selected={selected}
              className={cn(
                "relative h-9 shrink-0 gap-1.5 rounded-none px-0 text-xs font-medium text-foreground/60 hover:bg-transparent hover:text-foreground",
                "after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:rounded-full after:bg-foreground after:opacity-0 after:transition-opacity",
                selected && "text-foreground after:opacity-100",
              )}
              data-chip={item.chip}
              data-testid="inspector-tab"
              key={item.id}
              onClick={() => setFocusChip(item.chip)}
              role="tab"
              type="button"
            >
              <span
                aria-hidden="true"
                className={cn(
                  "size-1.5 shrink-0 rounded-full",
                  chipDotClass(item.chip, item.tone),
                )}
              />
              {t(`trajectory.chips.${item.chip}`)}
            </Button>
          );
        })}
      </div>
      {auditError ? (
        <p
          className="shrink-0 px-3 pt-2 text-xs text-danger-foreground"
          role="alert"
        >
          {auditError}
        </p>
      ) : null}
      {auditLoading ? (
        <p
          className="shrink-0 px-3 pt-2 text-xs text-muted-foreground"
          role="status"
        >
          {t("records.decrypting")}
        </p>
      ) : null}
      <div
        className={cn(
          "min-h-0 flex-1 p-3",
          focusRow && ownsScroller(focusRow.chip) && "flex flex-col",
          "overflow-y-auto overscroll-contain",
        )}
      >
        {focusRow ? (
          <InspectorFindShortcut.Provider value={findShortcut}>
            <InspectorSection
              key={`${record.id}:${focusRow.chip}`}
              auditContent={auditContent}
              auditLoading={auditLoading}
              copyFeedback={copyFeedback}
              omitCapturedBody={focusRow.chip === "RESTORE" && hideRestoreBody}
              record={record}
              routes={routes}
              service={service}
              services={services}
              row={focusRow}
            />
          </InspectorFindShortcut.Provider>
        ) : null}
      </div>
    </div>
  );
}

function tabChip(chip: TrajectoryChip): TrajectoryChip {
  return chip === "TURN" ? "CLIENT" : chip;
}

// Body panes carry their own outcome line and a bounded scroller, so the
// generic section header would only repeat them. The body still scrolls once
// a short window can no longer fit the pane's minimum height.
function ownsScroller(chip: TrajectoryChip): boolean {
  return (
    chip === "CLIENT" ||
    chip === "POLICY" ||
    chip === "RESULT" ||
    chip === "UPSTREAM" ||
    chip === "RETRY"
  );
}

// One tab per phase. A repeated phase keeps its first position and shows its
// last row, the one the record's outcome came from.
function inspectorTabs(chain: TrajectoryRow[]): TrajectoryRow[] {
  const tabs: TrajectoryRow[] = [];
  for (const row of chain) {
    const index = tabs.findIndex((tab) => tab.chip === row.chip);
    if (index === -1) tabs.push(row);
    else tabs[index] = row;
  }
  return tabs;
}

function InspectorSection({
  row,
  record,
  routes,
  service,
  services,
  auditContent,
  auditLoading,
  copyFeedback,
  omitCapturedBody,
}: {
  row: TrajectoryRow;
  record: RequestRecord;
  routes: TrajectoryRow[];
  service: RequestServiceIdentity;
  services?: RequestServiceMap;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  copyFeedback: CopyFeedback;
  omitCapturedBody: boolean;
}) {
  const part = inspectorPart(row.chip);
  const title = inspectorTitle(row.chip);
  const captured =
    part === "route" || part === "redirect" || omitCapturedBody
      ? null
      : auditPart(auditContent, part);
  const disconnectNote = clientDisconnectNote(record);
  const pane = ownsScroller(row.chip);
  return (
    <section
      className={pane ? "flex flex-1 flex-col gap-2" : "space-y-2"}
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
      {pane ? null : (
        <header className="flex min-w-0 items-center gap-2">
          <strong className="truncate text-xs font-medium">{title}</strong>
          {captured ? (
            <span className="shrink-0 text-micro text-muted-foreground">
              {formatCapturedBytes(captured.captured_bytes)}
            </span>
          ) : null}
        </header>
      )}
      {part === "route" ? (
        <>
          <RouteInspector
            record={record}
            routes={routes}
            service={service}
            services={services}
          />
          <RecoveryDetails value={record.recovery} />
        </>
      ) : part === "redirect" ? (
        <RedirectInspector redirect={row.redirect ?? record.model_redirect} />
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
  routes,
  service,
  services = {},
}: {
  record: RequestRecord;
  routes: TrajectoryRow[];
  service: RequestServiceIdentity;
  services?: RequestServiceMap;
}) {
  const t = i18n.t.bind(i18n);
  // The listed names win: an identity resolved without the list is just the ID.
  const names: RequestServiceMap = service.id
    ? { [service.id]: { id: service.id, name: service.name }, ...services }
    : services;
  const stepped = routingSteps(routes, record.routing_decision).length > 0;
  return (
    <>
      <RoutingSteps
        decision={record.routing_decision}
        routes={routes}
        selectedServiceId={record.service_id}
        serviceNames={Object.fromEntries(
          Object.entries(names).map(([id, item]) => [id, item.name]),
        )}
      />
      <dl className="grid gap-2 text-xs">
        {record.model_redirect ? (
          <ModelRedirectFields redirect={record.model_redirect} />
        ) : null}
        {stepped ? null : (
          <InspectorField label={t("records.provider")} value={service.name} />
        )}
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
        {service.id ? (
          <InspectorField
            code
            label={`${t("trajectory.service")} ID`}
            value={service.id}
          />
        ) : null}
        {record.route_id ? (
          <InspectorField
            code
            label={t("trajectory.route")}
            value={record.route_id}
          />
        ) : null}
      </dl>
      <ConversionDiagnosticsDetails value={record.conversion_diagnostics} />
    </>
  );
}

/** The client's model and the one the gateway routed with, one field each. */
function ModelRedirectFields({ redirect }: { redirect: RequestModelRedirect }) {
  const t = i18n.t.bind(i18n);
  return (
    <>
      <div data-testid="inspector-requested-model">
        <dt className="text-muted-foreground">
          {t("trajectory.requestedModel")}
        </dt>
        <dd className="mt-0.5 text-foreground">
          <ModelLabel model={redirect.from} />
        </dd>
      </div>
      <div data-testid="inspector-redirect-target">
        <dt className="text-muted-foreground">
          {t("trajectory.redirectTarget")}
        </dt>
        <dd className="mt-0.5 text-foreground">
          <ModelLabel model={redirect.to} />
        </dd>
      </div>
    </>
  );
}

function RedirectInspector({
  redirect,
}: {
  redirect: RequestModelRedirect | undefined;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <div className="space-y-2" data-testid="redirect-inspector">
      {redirect ? (
        <dl className="grid gap-2 text-xs">
          <ModelRedirectFields redirect={redirect} />
        </dl>
      ) : null}
      <p className="text-xs leading-6 text-muted-foreground">
        {t("trajectory.modelRedirectHint")}
      </p>
    </div>
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
  part: BodyPart;
  row: TrajectoryRow;
  record: RequestRecord;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  copyFeedback: CopyFeedback;
  omitCapturedBody: boolean;
}) {
  const captured = omitCapturedBody ? null : auditPart(auditContent, part);
  // Only the restore summary lists these; scanning a large body on every
  // render of the other tabs would stall them.
  const restoreSource =
    row.chip === "RESTORE"
      ? (auditPart(auditContent, part)?.content ?? "")
      : "";
  const unrestoredHits = useMemo(
    () => extractPrivacyHits(restoreSource),
    [restoreSource],
  );
  const sectionKey = `trajectory-${row.chip}-${part}`;
  const missingHint = auditLoading ? null : missingBodyHint(record);
  // Tabs with a view switch pick each view's own withheld hint.
  const partHint =
    withheldHint(omitCapturedBody ? null : auditContent?.withheld[part]) ??
    missingHint;

  if (row.chip === "CLIENT" || row.chip === "TURN") {
    return (
      <ClientInspector
        auditContent={auditContent}
        copyFeedback={copyFeedback}
        missingHint={missingHint}
        record={record}
      />
    );
  }

  if (row.chip === "POLICY") {
    return (
      <PolicyInspector
        auditContent={auditContent}
        copyFeedback={copyFeedback}
        missingHint={missingHint}
        record={record}
        row={row}
      />
    );
  }

  if (row.chip === "RESULT") {
    return (
      <ResultInspector missingHint={partHint} part={captured} record={record} />
    );
  }

  if (row.chip === "UPSTREAM" || row.chip === "RETRY") {
    return (
      <UpstreamInspector
        auditContent={auditContent}
        copyFeedback={copyFeedback}
        missingHint={missingHint}
        record={record}
        row={row}
      />
    );
  }

  return (
    <>
      {row.chip === "RESTORE" ? (
        <RestoreSummary hits={unrestoredHits} record={record} />
      ) : null}
      {!omitCapturedBody && !auditLoading && !captured ? (
        <p
          className="text-xs leading-6 text-muted-foreground"
          data-testid="inspector-missing-body"
        >
          {partHint}
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

/**
 * What the client sent: its request line and size, then the body and the
 * HTTP envelope in the same pane the upstream tab uses.
 */
function ClientInspector({
  record,
  auditContent,
  copyFeedback,
  missingHint,
}: {
  record: RequestRecord;
  auditContent: AuditContent | null;
  copyFeedback: CopyFeedback;
  missingHint: string | null;
}) {
  const t = i18n.t.bind(i18n);
  const body = auditContent?.request_body ?? null;
  const meta = auditContent?.http_meta ?? null;
  return (
    <div className="flex flex-1 flex-col gap-2">
      <div className="flex min-w-0 shrink-0 items-center gap-3 text-xs">
        <EndpointLine
          method={meta?.method ?? null}
          testId="inspector-client-endpoint"
          url={
            meta?.url ??
            protocolEntryPath(record.input_protocol, {
              streaming: record.streaming,
            })
          }
        />
        {body ? (
          <span
            className="shrink-0 font-mono text-micro text-muted-foreground tabular-nums"
            data-testid="inspector-client-size"
          >
            {formatCapturedBytes(body.captured_bytes)}
          </span>
        ) : null}
      </div>
      <CapturePane
        copyFeedback={copyFeedback}
        copyKey={`client:${record.id}`}
        label={t("trajectory.clientBody")}
        missingHint={missingHint}
        testId="inspector-client-body"
        viewsLabel={t("audit.clientView")}
        views={[
          {
            value: "request",
            label: t("audit.upstreamViews.request"),
            body,
            withheld: auditContent?.withheld.request_body,
          },
          { value: "http", label: "HTTP", meta },
        ]}
      />
    </div>
  );
}

/**
 * What the privacy policy did to the request. Only a redaction changes what
 * goes upstream, so only then does the tab carry a body: the request as sent,
 * with each replaced value marked. Any other outcome would repeat the client
 * tab's body under another name.
 */
function PolicyInspector({
  row,
  record,
  auditContent,
  copyFeedback,
  missingHint,
}: {
  row: TrajectoryRow;
  record: RequestRecord;
  auditContent: AuditContent | null;
  copyFeedback: CopyFeedback;
  missingHint: string | null;
}) {
  const t = i18n.t.bind(i18n);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const decision = policyDecision(row, record.privacy_restore);
  const redacted = decision === "redact";
  const hits = recordedPrivacyHits(record.privacy_restore);
  const mappings = record.privacy_restore?.mapping_count ?? 0;
  const segments = row.summary.split(" · ");
  const body = redacted ? (auditContent?.upstream_request_body ?? null) : null;
  // Progress and failure codes are Core's own words; the badge says the rest.
  const detail =
    decision === "inspecting" || decision === "unfinished" ? row.summary : null;
  const hint = policyHint(decision);
  const revealKind = (kind: string) => {
    scrollerRef.current
      ?.querySelector<HTMLElement>(`mark[data-kind="${kind}"]`)
      ?.scrollIntoView({ block: "center" });
  };
  return (
    <div
      className={redacted ? "flex flex-1 flex-col gap-2" : "space-y-2"}
      data-decision={decision}
      data-testid="policy-inspector"
    >
      <div
        className="flex min-w-0 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs"
        data-testid="policy-outcome"
      >
        <StatusBadge tone={policyTone(decision, row.status)}>
          {t(`trajectory.policyDecisions.${decision}`)}
        </StatusBadge>
        {redacted && mappings > 0 ? (
          <span className="text-foreground">
            {t("trajectory.policyReplaced", { count: mappings })}
          </span>
        ) : null}
        {hits.length > 0 ? (
          <PrivacyHitList
            hits={hits}
            inline
            onSelectKind={body ? revealKind : undefined}
          />
        ) : null}
        {segments.includes("notice") ? (
          <span className="text-muted-foreground">
            {t("trajectory.policyNotice")}
          </span>
        ) : null}
        {detail ? (
          <code className="min-w-0 truncate font-mono text-micro text-muted-foreground">
            {detail}
          </code>
        ) : null}
      </div>
      {hint ? (
        <p
          className="text-xs leading-6 text-muted-foreground"
          data-testid="policy-hint"
        >
          {hint}
        </p>
      ) : null}
      {redacted ? (
        <CapturePane
          copyFeedback={copyFeedback}
          copyKey={`policy:${record.id}`}
          label={t("trajectory.redactedRequest")}
          missingHint={missingHint}
          revealPrivacy
          scrollerRef={scrollerRef}
          testId="inspector-policy-body"
          views={[
            {
              value: "request",
              label: t("trajectory.redactedRequest"),
              body,
              withheld: redacted
                ? auditContent?.withheld.upstream_request_body
                : null,
            },
          ]}
        />
      ) : null}
    </div>
  );
}

function policyTone(
  decision: PolicyDecision,
  status: RequestStatus,
): StatusTone {
  switch (decision) {
    case "redact":
    case "warn":
    case "inspecting":
      return "pending";
    case "block":
      return "blocked";
    case "allow":
      return "neutral";
    case "unfinished":
      return status === "failed" ? "negative" : "neutral";
  }
}

function policyHint(decision: PolicyDecision): string | null {
  switch (decision) {
    case "allow":
      return i18n.t("trajectory.policyAllowHint");
    case "warn":
      return i18n.t("trajectory.policyWarnHint");
    case "block":
      return i18n.t("trajectory.policyBlockHint");
    default:
      return null;
  }
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
        <p
          className="text-xs text-muted-foreground"
          data-testid="restore-channels"
        >
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

/** `inline` flows the kinds in one wrapping row, for hits without values. */
function PrivacyHitList({
  hits,
  inline = false,
  onSelectKind,
}: {
  hits: PrivacyHitGroup[];
  inline?: boolean;
  onSelectKind?: (kind: string) => void;
}) {
  return (
    <ul
      className={inline ? "flex flex-wrap gap-x-3 gap-y-1" : "grid gap-2"}
      data-testid="privacy-hits"
    >
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

function missingBodyHint(record: RequestRecord): string {
  if (record.status === "pending") {
    return i18n.t("trajectory.pendingCaptureHint");
  }
  return i18n.t("trajectory.uncapturedHint");
}

function bodySectionTitle(chip: TrajectoryChip): string {
  switch (chip) {
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

function auditPart(content: AuditContent | null, part: BodyPart) {
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
