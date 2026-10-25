import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { SessionChannelBindings } from "./SessionChannelBindings";
import { RecoveryChain, RecoveryDetails } from "./components/RecoveryDetails";
import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import {
  Brain,
  ChevronDown,
  ChevronUp,
  Copy,
  SlidersHorizontal as ListFilter,
  Ellipsis,
  LoaderCircle,
  MessageSquare,
  RefreshCw,
  SquarePen,
  SlidersHorizontal as Settings2,
  Shredder as Trash2,
  X,
} from "@/components/icons";

import { ConfirmDialog as AppConfirmDialog } from "@/components/ConfirmDialog";
import { ToolKindIcon } from "@/components/ToolKindIcon";
import { ClientTypeIcon, clientTypeName } from "@/components/ClientTypeIcon";
import { DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { ScrollWorkspace } from "@/components/ScrollWorkspace";
import { FilterSelect } from "@/components/FilterSelect";
import {
  FilterChips,
  FilterMenu,
  type FilterChip,
} from "@/components/FilterMenu";
import { ServiceSelect } from "@/components/ServiceSelect";
import { FormMessage } from "@/components/FormMessage";
import { MultiFilterSelect } from "@/components/MultiFilterSelect";
import {
  customRangeLabel,
  UsageRangePicker,
} from "@/components/UsageRangePicker";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { ModelLabel } from "@/components/ModelLabel";
import { RequestServiceLabel } from "@/components/RequestServiceLabel";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox, CheckboxMark } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ActivitySlot } from "@/components/ui/activity-slot";
import { cn } from "@/lib/utils";

import { AuditPartSection, HTTPMetaSection } from "./AuditReviewer";
import { AuditSettingsDialog } from "./AuditSettingsDialog";
import {
  buildRecordBundle,
  bundleFilename,
  type BundleFormat,
} from "./audit-bundle";
import { buildSkillDiagnostic } from "./skill-diagnostic";
import type { AuditSettings, AuditSettingsPatch } from "./audit-settings-model";
import { useExportEnvironment } from "./export-environment";
import {
  deleteRequestRecord,
  getAuditSettings,
  getRawSealingStatus,
  getRequestAuditContent,
  getRequestSession,
  listenRawSealingChanged,
  listRequestRecordChildren,
  listRequestSessions,
  lockRaw,
  purgeRequestRecords,
  saveTextFile,
  updateAuditSettings,
} from "./bridge";
import { copyButtonLabel, useCopyFeedback } from "./copy-feedback";
import { compactTitle } from "./file-path-spans";
import { formatCompactNumber } from "./format-compact-number";
import { currentLocale, i18n, useT } from "./i18n";
import { LIVE_CLOCK_INTERVAL_MS, useLiveClock } from "./live-clock";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";
import {
  RawSealingDialogs,
  rawPasswordMissing,
  unlockIdleMinutes,
  type RawDialog,
} from "./RawSealingControls";
import {
  rawPasswordUnset,
  unlockCheckDelay,
  type RawSealingState,
} from "./raw-sealing-model";
import { rawSealingErrorMessage } from "./raw-sealing-ui";
import type { AccessTokenSummary } from "./access-token-model";
import type { RoutableService } from "./service-model";
import {
  requestServiceIdentity,
  type RequestService,
  type RequestServiceMap,
} from "./request-service-model";
import {
  formatDuration,
  liveDurationMs,
  sessionRuntimeMs,
  type RecordFilters,
  type RecordsTarget,
} from "./request-live-model";
import {
  CLIENT_TYPES,
  displayRequestStatus,
  holdsRawPart,
  isModelDiscoveryProtocol,
  statusLabel,
  statusTone,
  type AuditContent,
  type ClientType,
  type RequestActivityCall,
  type RequestRecord,
  type RequestSession,
  type RequestSessionDetail,
  type RequestSessionKind,
  type RequestSessionListQuery,
  type RequestStatus,
  type SessionStatus,
} from "./request-record-model";
import {
  RequestActivityBoard,
  useRequestActivityFeed,
} from "./RequestActivityBoard";
import {
  conversationActivity,
  laneAction,
  laneTrail,
  splitActiveSessions,
  type LaneAction,
  type LaneStep,
} from "./request-activity-model";
import { RequestConversation } from "./RequestConversation";
import { RequestPrivacyAudit } from "./RequestPrivacyAudit";
import { RequestTrajectory } from "./RequestTrajectory";
import {
  applyQueuedSessions,
  groupSessionsByDate,
  listOnlyFiltersActive,
  mergeLiveSessions,
  removeSessions,
  replaceSession,
  reuseUnchangedSessions,
  sessionMatchesFilters,
  sessionRunning,
  sessionSummary,
} from "./session-live-model";
import { protocolEntryPath } from "./service-presets";
import {
  isCustomUsageRange,
  resolveUsageWindow,
  usageRangeKey,
  USAGE_RANGE_PRESETS,
  type UsageRange,
} from "./usage-range";
import type { ConversationContentCache } from "./use-conversation-content";

const PAGE_LIMIT = 50;
const POLL_INTERVAL_MS = 1000;
// Core's contract.MaxPurgeSessionIDs: one sessions purge names at most this many.
const MAX_PURGE_SESSIONS = 1000;
const AUDIT_CACHE_MAX_RECORDS = 5;
const AUDIT_CACHE_MAX_BYTES = 32 * 1024 * 1024;
const STATUSES: SessionStatus[] = [
  "pending",
  "succeeded",
  "failed",
  "cancelled",
  "interrupted",
  "blocked",
];
const EMPTY_FILTERS: RecordFilters = {
  status: "",
  serviceId: "",
  protocol: "",
  clientType: "",
  localAccessTokenIds: [],
};

/** The list filters a target sets; the rest start cleared. */
function targetFilters(target: RecordsTarget | undefined): RecordFilters {
  return {
    ...EMPTY_FILTERS,
    serviceId: target?.serviceId ?? "",
    localAccessTokenIds: target?.tokenId ? [target.tokenId] : [],
  };
}

function sameFilters(left: RecordFilters, right: RecordFilters): boolean {
  return (
    left.status === right.status &&
    left.serviceId === right.serviceId &&
    left.protocol === right.protocol &&
    left.clientType === right.clientType &&
    left.localAccessTokenIds.length === right.localAccessTokenIds.length &&
    left.localAccessTokenIds.every(
      (id, index) => id === right.localAccessTokenIds[index],
    )
  );
}

type RecordsView = "monitor" | "detail";
type RecordsKind = RequestSessionKind | "all";
type DetailTab =
  | "conversation"
  | "trajectory"
  | "content"
  | "audit"
  | "binding";
// Copies started from a menu lose their button when it closes, so their
// outcome is announced beside the actions instead.
const MENU_COPY_KEYS: ReadonlySet<string> = new Set([
  "bundle-meta",
  "skill-diagnostic",
  "session-id",
]);
const DETAIL_TABS: readonly DetailTab[] = [
  "conversation",
  "trajectory",
  "content",
  "audit",
  "binding",
];
type PendingConfirm =
  | { kind: "audit-risk"; patch: AuditSettingsPatch }
  | { kind: "delete"; requestId: string }
  // Whole sessions, from the list selection or the open detail.
  | { kind: "delete-sessions"; sessionIds: string[]; calls: number }
  | { kind: "purge-all" }
  | { kind: "purge-before"; before: string };

interface LiveState {
  items: RequestSession[];
  queued: RequestSession[];
  nextCursor: string | null;
}

function auditContentBytes(content: AuditContent): number {
  return (
    (content.request_body?.captured_bytes ?? 0) +
    (content.response_content?.captured_bytes ?? 0) +
    (content.upstream_request_body?.captured_bytes ?? 0) +
    (content.upstream_response_content?.captured_bytes ?? 0)
  );
}

/** Everything the list row shows, so the detail only reloads when it moved. */
/** The session beside `id` in the list: -1 is newer, 1 is older. */
function neighborSession(
  items: RequestSession[],
  id: string | null,
  direction: -1 | 1,
): RequestSession | null {
  const index = id === null ? -1 : items.findIndex((item) => item.id === id);
  if (index < 0) return null;
  return items[index + direction] ?? null;
}

function sessionRow(
  scroller: HTMLElement | null,
  sessionId: string,
): HTMLButtonElement | null {
  const escaped =
    typeof CSS !== "undefined" && typeof CSS.escape === "function"
      ? CSS.escape(sessionId)
      : sessionId.replace(/["\\]/g, "\\$&");
  const row = scroller?.querySelector(`[data-session-id="${escaped}"]`);
  return row instanceof HTMLButtonElement ? row : null;
}

function sessionSummaryKey(session: RequestSession): string {
  return [
    session.id,
    session.status,
    session.client_type ?? "",
    session.requested_model ?? "",
    session.model_redirect?.from ?? "",
    session.model_redirect?.to ?? "",
    session.reasoning_effort ?? "",
    session.turn_count,
    session.call_count,
    session.last_started_at,
    session.completed_at ?? "",
    session.duration_ms,
    session.running_since ?? "",
    session.tool_duration_ms ?? "",
    session.average_ttft_ms ?? "",
    session.output_tokens_per_second ?? "",
    session.privacy_warned ? "warned" : "",
    ...session.active_request_starts,
  ].join("|");
}

/**
 * Everything the detail view draws from a conversation. Replacing an unchanged
 * detail rebuilds every trajectory row and refires the retry-children fetch for
 * every root, so an idle poll has to be able to prove nothing moved.
 */
function sessionDetailKey(detail: RequestSessionDetail): string {
  const parts: Array<string | number> = [
    sessionSummaryKey(detail),
    detail.turns.length,
  ];
  for (const turn of detail.turns) {
    parts.push(
      turn.id,
      turn.status,
      turn.client_type ?? "",
      turn.requested_model ?? "",
      turn.model_redirect?.from ?? "",
      turn.model_redirect?.to ?? "",
      turn.reasoning_effort ?? "",
      turn.http_status ?? "",
      turn.latency_ms ?? "",
      turn.completed_at ?? "",
      turn.child_count,
      turn.events.length,
      turn.audit.request_body_captured ? "1" : "0",
      turn.audit.response_content_captured ? "1" : "0",
      turn.audit.upstream_request_body_captured ? "1" : "0",
      turn.audit.upstream_response_content_captured ? "1" : "0",
    );
    for (const event of turn.events) {
      parts.push(event.kind, event.status, event.ended_at ?? "");
    }
  }
  return parts.join("|");
}

export function RequestRecords({
  accessTokens,
  accessTokensReady,
  coreSessionKey,
  initialDetailTab = "conversation",
  isReady,
  services,
  target,
}: {
  accessTokens: AccessTokenSummary[];
  accessTokensReady: boolean;
  coreSessionKey: string | null;
  /** The reading tab a session opens on until the operator picks one. */
  initialDetailTab?: "conversation" | "trajectory";
  isReady: boolean;
  services: (RoutableService & RequestService)[];
  /**
   * The narrowing another page opened the list on. Each navigation is a new
   * object, so opening the same target again restores what was cleared.
   */
  target?: RecordsTarget;
}) {
  const t = i18n.t.bind(i18n);
  const servicesById = useMemo(
    () => Object.fromEntries(services.map((service) => [service.id, service])),
    [services],
  );
  const accessTokenOptions = useMemo(
    () => accessTokens.map((token) => ({ value: token.id, label: token.name })),
    [accessTokens],
  );
  const [view, setView] = useState<RecordsView>("monitor");
  const moreFiltersTriggerRef = useRef<HTMLButtonElement>(null);
  const [kind, setKind] = useState<RecordsKind>("inference");
  const [filters, setFilters] = useState<RecordFilters>(() =>
    targetFilters(target),
  );
  const [models, setModels] = useState<string[]>(() => target?.models ?? []);
  const [dateRange, setDateRange] = useState<UsageRange | null>(
    () => target?.range ?? null,
  );
  // Core matches the status of each call, not a conversation's outcome, so a
  // conversation that failed and then recovered still lists. That keeps every
  // call the tray or the overview counted on screen.
  const [callStatus, setCallStatus] = useState<RequestStatus | null>(
    () => target?.status ?? null,
  );
  const callStatusLabel = callStatus
    ? t("records.callStatus", { status: statusLabel(callStatus) })
    : "";
  // Core applies the token, model, time and call status filters, so each
  // combination retains its own list.
  const serverFilterKey = [
    filters.localAccessTokenIds.join("\u0000"),
    models.join("\u0000"),
    dateRange === null ? "" : usageRangeKey(dateRange),
    callStatus ?? "",
  ].join("\u0001");
  // A preset range moves with the clock, so every read resolves it again.
  const sessionQuery = (cursor?: string): RequestSessionListQuery => {
    const window =
      dateRange === null ? null : resolveUsageWindow(dateRange, new Date());
    return {
      limit: PAGE_LIMIT,
      kind: kind === "all" ? undefined : kind,
      local_access_token_ids: filters.localAccessTokenIds.length
        ? filters.localAccessTokenIds
        : undefined,
      ...(models.length ? { models } : {}),
      ...(window ? { from: window.from, to: window.to } : {}),
      ...(callStatus ? { status: callStatus } : {}),
      ...(cursor === undefined ? {} : { cursor }),
    };
  };
  const [live, setLive] = useWorkspaceSnapshot<LiveState>(
    `request-list:${coreSessionKey}:${kind}:${serverFilterKey}`,
    {
      items: [],
      queued: [],
      nextCursor: null,
    },
  );
  const [listStatus, setListStatus] = useWorkspaceSnapshot<
    "blocked" | "loading" | "ready" | "error"
  >(
    `request-list-status:${coreSessionKey}:${kind}:${serverFilterKey}`,
    "blocked",
  );
  const [listError, setListError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [syncWarning, setSyncWarning] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // A session whose detail is still on the way; see openDetail.
  const [openingId, setOpeningId] = useState<string | null>(null);
  const [selectedTurnId, setSelectedTurnId] = useState<string | null>(null);
  const [overlaySessions, setOverlaySessions] = useState<
    Record<string, RequestSessionDetail>
  >({});
  const [overlayRecords, setOverlayRecords] = useState<
    Record<string, RequestRecord>
  >({});
  const [auditContent, setAuditContent] = useState<AuditContent | null>(null);
  const [auditLoading, setAuditLoading] = useState(false);
  const [auditError, setAuditError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settings, setSettings] = useState<AuditSettings | null>(null);
  const [settingsDraft, setSettingsDraft] = useState<AuditSettings | null>(
    null,
  );
  const [settingsBusy, setSettingsBusy] = useState(false);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  const [settingsNotice, setSettingsNotice] = useState<string | null>(null);
  const [purgeOpen, setPurgeOpen] = useState(false);
  const [purgeBefore, setPurgeBefore] = useState("");
  const [purgeBusy, setPurgeBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);
  // Selection mode, started from the purge button, makes list rows checkboxes.
  const [selecting, setSelecting] = useState(false);
  const [checkedIds, setCheckedIds] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [pendingConfirm, setPendingConfirm] = useState<PendingConfirm | null>(
    null,
  );
  const [rawSealing, setRawSealing] = useState<RawSealingState | null>(null);
  const [rawSealingError, setRawSealingError] = useState<string | null>(null);
  const [rawDialog, setRawDialog] = useState<RawDialog | null>(null);
  // Bumped to read the open record's audit content again, e.g. after an
  // unlock changed which raw parts Core may return.
  const [auditNonce, setAuditNonce] = useState(0);

  const generationRef = useRef(0);
  const listGenerationRef = useRef(0);
  const auditGenerationRef = useRef(0);
  const auditCacheRef = useRef<Map<string, AuditContent>>(new Map());
  // Goes wherever the audit cache goes: both hold what was decrypted.
  const conversationCacheRef = useRef<ConversationContentCache>(new Map());
  const pollInFlightRef = useRef(false);
  const pollFailureRef = useRef(0);
  const manualPollRef = useRef<(() => void) | null>(null);
  const atTopRef = useRef(true);
  const viewRef = useRef<RecordsView>("monitor");
  const selectingRef = useRef(false);
  const monitorScrollRef = useRef<HTMLDivElement | null>(null);
  // The list's offset when a session opened. WKWebView drops the offset of a
  // size container hidden with display: none, so the list restores its own.
  const monitorScrollTopRef = useRef<number | null>(null);
  const selectedFocusRef = useRef<string | null>(null);
  const openGenerationRef = useRef(0);
  const selectedIdRef = useRef(selectedId);
  const overlaySessionsRef = useRef(overlaySessions);
  // A session openDetail has just fetched, so selecting it need not again.
  const freshDetailRef = useRef<string | null>(null);

  viewRef.current = view;
  selectingRef.current = selecting;
  selectedIdRef.current = selectedId;
  overlaySessionsRef.current = overlaySessions;

  const allRecords = useMemo(
    () => [...live.queued, ...live.items],
    [live.items, live.queued],
  );
  const visibleItems = useMemo(
    () =>
      live.items.filter((session) => sessionMatchesFilters(session, filters)),
    [filters, live.items],
  );
  const queuedVisibleCount = useMemo(
    () =>
      live.queued.filter((session) => sessionMatchesFilters(session, filters))
        .length,
    [filters, live.queued],
  );
  // Core pages ignore the list-only filters, so a narrowed list keeps reading
  // earlier pages until it shows `target` matches or reaches the oldest one.
  // Otherwise a rare status matched nothing among the loaded pages and read as
  // no match at all. `asked` is the cursor last read: a page that failed is
  // read again only from load earlier. A new filter or list starts over.
  const narrowed = listOnlyFiltersActive(filters);
  const fillKey = [
    coreSessionKey,
    kind,
    serverFilterKey,
    filters.status,
    filters.serviceId,
    filters.protocol,
    filters.clientType,
  ].join("\u0000");
  const [fill, setFill] = useState<{
    key: string;
    target: number;
    asked: string | null;
  }>({ key: fillKey, target: PAGE_LIMIT, asked: null });
  const fillTarget = fill.key === fillKey ? fill.target : PAGE_LIMIT;
  const fillAsked = fill.key === fillKey ? fill.asked : null;
  const filling =
    narrowed &&
    listStatus === "ready" &&
    live.nextCursor !== null &&
    live.nextCursor !== fillAsked &&
    visibleItems.length < fillTarget;
  // Only rows on screen count, so a filter change never deletes sessions the
  // operator can no longer see.
  const selectableItems = useMemo(
    () => visibleItems.filter((session) => !sessionRunning(session)),
    [visibleItems],
  );
  const checkedItems = useMemo(
    () => selectableItems.filter((session) => checkedIds.has(session.id)),
    [checkedIds, selectableItems],
  );
  const allChecked =
    selectableItems.length > 0 &&
    checkedItems.length === selectableItems.length;
  const selectedSession = useMemo(() => {
    if (!selectedId) return null;
    return (
      overlaySessions[selectedId] ??
      allRecords.find((session) => session.id === selectedId) ??
      null
    );
  }, [allRecords, overlaySessions, selectedId]);
  const selectedTurns = useMemo(
    () => overlaySessions[selectedId ?? ""]?.turns ?? [],
    [overlaySessions, selectedId],
  );
  const selected = useMemo(() => {
    if (!selectedTurnId) return selectedTurns[selectedTurns.length - 1] ?? null;
    return (
      selectedTurns.find((record) => record.id === selectedTurnId) ??
      overlayRecords[selectedTurnId] ??
      selectedTurns[selectedTurns.length - 1] ??
      null
    );
  }, [overlayRecords, selectedTurnId, selectedTurns]);

  // The detail carries every root turn of the conversation, so it must not be
  // refetched just because the session list poll handed back a new array. It
  // reloads when the summary says the conversation moved, and keeps its own 1 s
  // loop while a call is in flight so the running turn's phases stay live.
  const selectedSummaryKey = useMemo(() => {
    const summary = selectedId
      ? allRecords.find((session) => session.id === selectedId)
      : undefined;
    return summary ? sessionSummaryKey(summary) : null;
  }, [allRecords, selectedId]);
  const detailIsLive =
    selectedId !== null &&
    (selectedSession?.status === "pending" ||
      selectedTurns.some((turn) => turn.status === "pending"));

  useEffect(() => {
    if (!selectedId) return;
    let cancelled = false;
    let running = false;
    const load = async () => {
      if (cancelled || running) return;
      running = true;
      try {
        const detail = await getRequestSession(selectedId);
        if (cancelled) return;
        setOverlaySessions((current) => {
          const previous = current[detail.id];
          if (
            previous &&
            sessionDetailKey(previous) === sessionDetailKey(detail)
          ) {
            return current;
          }
          return { ...current, [detail.id]: detail };
        });
        setSelectedTurnId((current) => {
          if (current && detail.turns.some((turn) => turn.id === current)) {
            return current;
          }
          return detail.turns[detail.turns.length - 1]?.id ?? null;
        });
      } catch {
        /* detail view shows missing via selectedSession === null */
      } finally {
        running = false;
      }
    };
    const fresh = freshDetailRef.current === selectedId;
    freshDetailRef.current = null;
    if (!fresh) void load();
    if (!detailIsLive) {
      return () => {
        cancelled = true;
      };
    }
    const timer = window.setInterval(() => {
      if (!document.hidden) void load();
    }, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [selectedId, selectedSummaryKey, detailIsLive]);
  const protocolOptions = useMemo(() => {
    const protocols = new Set<string>();
    services.forEach((service) =>
      service.capabilities.forEach((capability) =>
        protocols.add(capability.protocol),
      ),
    );
    allRecords.forEach((session) => protocols.add(session.input_protocol));
    return [...protocols]
      .filter(
        (protocol) =>
          kind === "all" ||
          isModelDiscoveryProtocol(protocol) === (kind === "discovery"),
      )
      .sort();
  }, [allRecords, services, kind]);
  // Only clients that actually sent traffic are worth offering; unknown
  // traffic is the catch-all, so it trails the named clients.
  const clientOptions = useMemo(() => {
    const seen = new Set<ClientType>(
      allRecords.map((session) => session.client_type ?? "unknown"),
    );
    if (filters.clientType) seen.add(filters.clientType);
    return [
      ...CLIENT_TYPES.filter(
        (clientType) => clientType !== "unknown" && seen.has(clientType),
      ),
      ...(seen.has("unknown") ? (["unknown"] as const) : []),
    ];
  }, [allRecords, filters.clientType]);
  // Every model a loaded call named. Core narrows the list to the chosen
  // models, so names an earlier list showed stay: choosing one model must not
  // drop the others from the menu or move the rows below them.
  const [calledModels, setCalledModels] = useWorkspaceSnapshot<
    readonly string[]
  >(`request-models:${coreSessionKey}`, []);
  useEffect(() => {
    setCalledModels((current) => {
      const names = new Set(current);
      allRecords.forEach((session) => {
        if (session.requested_model) names.add(session.requested_model);
        if (session.model_redirect?.to) names.add(session.model_redirect.to);
      });
      return names.size === current.length ? current : [...names];
    });
  }, [allRecords, setCalledModels]);
  // The models providers list, plus any a loaded call named and the ones
  // chosen, so a choice never drops out of its own list.
  const modelOptions = useMemo(() => {
    const names = new Set([...models, ...calledModels]);
    services.forEach((service) =>
      service.models.forEach((model) => names.add(model)),
    );
    return [...names]
      .sort((left, right) => left.localeCompare(right))
      .map((model) => ({ value: model, label: model }));
  }, [calledModels, models, services]);

  useEffect(() => {
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    auditGenerationRef.current += 1;
    auditCacheRef.current.clear();
    conversationCacheRef.current.clear();
    pollFailureRef.current = 0;
    pollInFlightRef.current = false;
    atTopRef.current = true;
    viewRef.current = "monitor";
    monitorScrollTopRef.current = null;
    openGenerationRef.current += 1;
    setOpeningId(null);
    setView("monitor");
    setSelectedId(null);
    setSelectedTurnId(null);
    setOverlaySessions({});
    setOverlayRecords({});
    setAuditContent(null);
    setAuditLoading(false);
    setAuditError(null);
    const initialFilters = targetFilters(target);
    setFilters((current) =>
      sameFilters(current, initialFilters) ? current : initialFilters,
    );
    const initialModels = target?.models ?? [];
    setModels((current) =>
      current.join("\u0000") === initialModels.join("\u0000")
        ? current
        : initialModels,
    );
    setDateRange(target?.range ?? null);
    setCallStatus(target?.status ?? null);
    // The counts a target comes from cover model calls only.
    if (target?.status || target?.models?.length) setKind("inference");
    setSettingsOpen(false);
    setSettings(null);
    setSettingsDraft(null);
    setSettingsError(null);
    setSettingsNotice(null);
    setPurgeOpen(false);
    setPendingConfirm(null);
    setSelecting(false);
    setCheckedIds(new Set());
    setRawSealing(null);
    setRawSealingError(null);
    setRawDialog(null);
    setError(null);
    setSyncWarning(null);

    if (!isReady) return;
    void getAuditSettings()
      .then((current) => {
        if (generationRef.current !== generation) return;
        setSettings({ ...current });
        setSettingsDraft({ ...current });
      })
      .catch((requestError: unknown) => {
        if (generationRef.current !== generation) return;
        setError(messageOf(requestError, i18n.t("records.auditReadFailed")));
      });
    void getRawSealingStatus()
      .then((current) => {
        if (generationRef.current !== generation) return;
        setRawSealing(current);
      })
      .catch((requestError: unknown) => {
        if (generationRef.current !== generation) return;
        setRawSealingError(rawSealingErrorMessage(requestError));
      });
  }, [coreSessionKey, isReady, target]);

  useEffect(() => {
    if (!accessTokensReady) return;
    const valid = new Set(accessTokens.map((token) => token.id));
    setFilters((current) => {
      const next = current.localAccessTokenIds.filter((id) => valid.has(id));
      if (next.length === current.localAccessTokenIds.length) return current;
      return { ...current, localAccessTokenIds: next };
    });
  }, [accessTokens, accessTokensReady]);

  useEffect(() => {
    const generation = ++listGenerationRef.current;
    pollInFlightRef.current = false;
    pollFailureRef.current = 0;
    atTopRef.current = true;
    monitorScrollTopRef.current = null;
    if (monitorScrollRef.current) monitorScrollRef.current.scrollTop = 0;
    if (!isReady) setLive({ items: [], queued: [], nextCursor: null });
    setLoadingMore(false);
    setListError(null);
    setSyncWarning(null);
    setListStatus((current) =>
      isReady ? (current === "ready" ? "ready" : "loading") : "blocked",
    );
    if (!isReady) return;
    pollInFlightRef.current = true;
    void listRequestSessions(sessionQuery())
      .then((page) => {
        if (listGenerationRef.current !== generation) return;
        // Coming back to the page usually finds the list it left. Keeping
        // those objects spares every mounted row a second render.
        setLive((current) => {
          const items = reuseUnchangedSessions(current.items, page.items);
          if (
            items === current.items &&
            current.queued.length === 0 &&
            page.next_cursor === current.nextCursor
          ) {
            return current;
          }
          return { items, queued: [], nextCursor: page.next_cursor };
        });
        setListStatus("ready");
      })
      .catch((requestError: unknown) => {
        if (listGenerationRef.current !== generation) return;
        setListStatus("error");
        setListError(listErrorMessage(requestError));
      })
      .finally(() => {
        if (listGenerationRef.current === generation) {
          pollInFlightRef.current = false;
        }
      });
    return () => {
      listGenerationRef.current += 1;
    };
  }, [coreSessionKey, isReady, kind, serverFilterKey]);

  useEffect(() => {
    if (!isReady) return;
    const generation = listGenerationRef.current;
    const poll = async (manual = false) => {
      if (
        pollInFlightRef.current ||
        (!manual && document.visibilityState === "hidden")
      ) {
        return;
      }
      pollInFlightRef.current = true;
      try {
        const page = await listRequestSessions(sessionQuery());
        if (listGenerationRef.current !== generation) return;
        setLive((current) => {
          // Rows must not shift under a pointer about to check one.
          const queueNew =
            current.queued.length > 0 ||
            viewRef.current !== "monitor" ||
            selectingRef.current ||
            !atTopRef.current;
          const merged = mergeLiveSessions(
            current.items,
            current.queued,
            page.items,
            queueNew,
          );
          const nextCursor =
            current.items.length === 0 ? page.next_cursor : current.nextCursor;
          // Keeping the old state object when the poll brought nothing new is
          // what stops the whole page from re-rendering every second.
          if (
            merged.items === current.items &&
            merged.queued === current.queued &&
            nextCursor === current.nextCursor
          ) {
            return current;
          }
          return {
            items: merged.items,
            queued: merged.queued,
            nextCursor,
          };
        });
        pollFailureRef.current = 0;
        setSyncWarning(null);
        setListError(null);
        setListStatus("ready");
        if (manual) notify.success(i18n.t("records.synced"));
      } catch (requestError: unknown) {
        if (listGenerationRef.current !== generation) return;
        pollFailureRef.current += 1;
        if (manual) {
          setError(
            isControlTransportError(requestError)
              ? i18n.t("records.controlUnavailable")
              : messageOf(requestError, i18n.t("records.refreshFailed")),
          );
        }
        if (pollFailureRef.current >= 3) {
          setSyncWarning(i18n.t("records.liveInterrupted"));
        }
      } finally {
        if (listGenerationRef.current === generation) {
          pollInFlightRef.current = false;
        }
      }
    };
    const timer = window.setInterval(() => void poll(), POLL_INTERVAL_MS);
    manualPollRef.current = () => {
      setError(null);
      void poll(true);
    };
    const onVisibilityChange = () => {
      if (document.visibilityState !== "hidden") void poll();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
      manualPollRef.current = null;
    };
  }, [coreSessionKey, isReady, kind, serverFilterKey]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      // Menus and dialogs consume Escape before it reaches the page.
      if (event.key !== "Escape" || event.defaultPrevented) return;
      if (pendingConfirm) {
        setPendingConfirm(null);
        return;
      }
      if (settingsOpen) {
        setSettingsOpen(false);
        return;
      }
      if (purgeOpen) {
        setPurgeOpen(false);
        return;
      }
      if (viewRef.current === "detail") {
        returnToMonitor();
        return;
      }
      if (selecting) exitSelection();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  });

  useEffect(
    () => () => {
      auditGenerationRef.current += 1;
      auditCacheRef.current.clear();
      conversationCacheRef.current.clear();
    },
    [],
  );

  const setViewAndRef = (next: RecordsView) => {
    viewRef.current = next;
    setView(next);
  };

  const cacheInsert = (id: string, content: AuditContent) => {
    const cache = auditCacheRef.current;
    cache.delete(id);
    cache.set(id, content);
    let totalBytes = 0;
    for (const value of cache.values()) totalBytes += auditContentBytes(value);
    while (
      cache.size > AUDIT_CACHE_MAX_RECORDS ||
      (totalBytes > AUDIT_CACHE_MAX_BYTES && cache.size > 1)
    ) {
      const oldest = cache.keys().next().value;
      if (oldest === undefined) break;
      const evicted = cache.get(oldest);
      if (evicted) totalBytes -= auditContentBytes(evicted);
      cache.delete(oldest);
    }
  };

  // Detail auto-decrypt: cached content shows instantly; a generation
  // counter drops stale responses when the selected turn changes quickly.
  // Pending records are never cached so the content refreshes once the
  // record reaches a terminal status. Raw parts under a raw key are never
  // cached either: they must not outlive the unlock that returned them.
  const selectedIsPending = selected?.status === "pending";
  const selectedAuditKey = selected
    ? [
        selected.audit.request_body_captured,
        selected.audit.response_content_captured,
        selected.audit.upstream_request_body_captured,
        selected.audit.upstream_response_content_captured,
      ].join(":")
    : "";
  useEffect(() => {
    if (view !== "detail" || !selected) return;
    const cached = auditCacheRef.current.get(selected.id);
    if (cached) {
      setAuditContent(cached);
      setAuditError(null);
      setAuditLoading(false);
      return;
    }
    const generation = ++auditGenerationRef.current;
    const cacheable = !selectedIsPending;
    setAuditLoading(true);
    setAuditError(null);
    setAuditContent(null);
    void getRequestAuditContent(selected.id)
      .then((content) => {
        if (auditGenerationRef.current !== generation) return;
        // Raw parts show only while unlocked.
        if (cacheable && !holdsRawPart(content)) {
          cacheInsert(selected.id, content);
        }
        setAuditContent(content);
      })
      .catch((requestError: unknown) => {
        if (auditGenerationRef.current !== generation) return;
        const message = messageOf(
          requestError,
          i18n.t("records.auditContentFailed"),
        );
        setAuditError(
          message.includes("409") ? i18n.t("records.auditKeyBroken") : message,
        );
      })
      .finally(() => {
        if (auditGenerationRef.current === generation) setAuditLoading(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    view,
    selectedId,
    selected?.id,
    selectedIsPending,
    selectedAuditKey,
    auditNonce,
  ]);

  // Core locks an idle unlock session on its own; a raw part that comes back
  // locked means the unlock this page remembers has ended.
  const rawLockedShown =
    auditContent !== null &&
    Object.values(auditContent.withheld).some(
      (part) => part?.reason === "raw_locked",
    );
  const rawUnlockedShown = rawSealing?.unlocked === true;
  useEffect(() => {
    if (!rawLockedShown || !rawUnlockedShown) return;
    void refreshRawSealing();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rawLockedShown, rawUnlockedShown]);

  // Check again when the unlock this page knows of ends. Reads push Core's
  // idle deadline back, so a session still open arms the next check; one
  // that ended takes the raw parts off the screen.
  const unlockExpiresAt = rawSealing?.unlocked
    ? rawSealing.unlock_expires_at
    : null;
  const [unlockCheck, setUnlockCheck] = useState(0);
  useEffect(() => {
    if (!unlockExpiresAt) return;
    const timer = window.setTimeout(() => {
      void refreshRawSealing().then((current) => {
        if (current?.unlocked) {
          setUnlockCheck((count) => count + 1);
          return;
        }
        reloadAudit();
      });
    }, unlockCheckDelay(unlockExpiresAt));
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [unlockExpiresAt, unlockCheck]);

  // The password gate, the Security page, or another window may set, reset,
  // unlock, or lock the raw key.
  useEffect(() => {
    if (!isReady) return;
    let active = true;
    let stop: (() => void) | null = null;
    listenRawSealingChanged(() => {
      if (active) void refreshRawSealing();
    }).then(
      (unlisten) => {
        if (active) stop = unlisten;
        else unlisten();
      },
      (error: unknown) => {
        console.error("AstrLink cannot watch the raw sealing state", error);
      },
    );
    return () => {
      active = false;
      stop?.();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [coreSessionKey, isReady]);

  // Raw protection, set up here or anywhere else, turns the parts that
  // waited for it into locked ones.
  const rawProtected =
    rawSealing === null ? undefined : !rawSealing.password_required;
  const rawProtectedRef = useRef(rawProtected);
  useEffect(() => {
    const previous = rawProtectedRef.current;
    rawProtectedRef.current = rawProtected;
    if (previous === false && rawProtected === true) reloadAudit();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rawProtected]);

  // Stable, so the memoized list rows skip the page's re-renders. A session
  // opens once its detail is in hand: switching first blanked the whole
  // workspace, or dropped the session being read, until the detail arrived.
  const openDetail = useCallback((sessionId: string) => {
    const generation = ++openGenerationRef.current;
    const show = (turnId: string | null) => {
      if (viewRef.current === "monitor") {
        monitorScrollTopRef.current = monitorScrollRef.current?.scrollTop ?? 0;
      }
      selectedFocusRef.current = sessionId;
      setOpeningId(null);
      setSelectedId(sessionId);
      setSelectedTurnId(turnId);
      viewRef.current = "detail";
      setView("detail");
    };
    if (overlaySessionsRef.current[sessionId]) {
      show(null);
      return;
    }
    setOpeningId(sessionId);
    getRequestSession(sessionId).then(
      (detail) => {
        if (openGenerationRef.current !== generation) return;
        if (selectedIdRef.current !== sessionId) {
          freshDetailRef.current = sessionId;
        }
        setOverlaySessions((current) => ({ ...current, [detail.id]: detail }));
        show(detail.turns[detail.turns.length - 1]?.id ?? null);
      },
      (requestError: unknown) => {
        if (openGenerationRef.current !== generation) return;
        setOpeningId(null);
        notify.error(messageOf(requestError, i18n.t("records.readFailed")));
      },
    );
  }, []);

  // Before paint, so going back never flashes the top of the list.
  useLayoutEffect(() => {
    const scroller = monitorScrollRef.current;
    const top = monitorScrollTopRef.current;
    if (view !== "monitor" || !scroller || top === null) return;
    monitorScrollTopRef.current = null;
    scroller.scrollTop = top;
    // Paging through sessions in the detail can leave the last one read
    // outside the restored view.
    const requestId = selectedFocusRef.current;
    const row = requestId ? sessionRow(scroller, requestId) : null;
    if (!row) return;
    const port = scroller.getBoundingClientRect();
    const box = row.getBoundingClientRect();
    if (box.top < port.top) scroller.scrollTop += box.top - port.top;
    else if (box.bottom > port.bottom) {
      scroller.scrollTop += box.bottom - port.bottom;
    }
  }, [view]);

  const returnToMonitor = () => {
    openGenerationRef.current += 1;
    setOpeningId(null);
    setViewAndRef("monitor");
    window.requestAnimationFrame(() => {
      const requestId = selectedFocusRef.current;
      if (!requestId) return;
      sessionRow(monitorScrollRef.current, requestId)?.focus({
        preventScroll: true,
      });
    });
  };

  const clearDecrypted = () => {
    auditCacheRef.current.clear();
    conversationCacheRef.current.clear();
    auditGenerationRef.current += 1;
    setAuditContent(null);
    setAuditError(null);
    setAuditLoading(false);
    notify.success(i18n.t("records.clearedMemory"));
    returnToMonitor();
  };

  const applyQueue = () => {
    setLive((current) => ({
      ...current,
      items: applyQueuedSessions(current.items, current.queued),
      queued: [],
    }));
    atTopRef.current = true;
    monitorScrollRef.current?.scrollTo({ top: 0, behavior: "smooth" });
  };

  const loadMore = async () => {
    if (!live.nextCursor || loadingMore) return;
    const generation = listGenerationRef.current;
    const cursor = live.nextCursor;
    setLoadingMore(true);
    try {
      const page = await listRequestSessions(sessionQuery(cursor));
      if (listGenerationRef.current !== generation) return;
      setLive((current) => {
        // A first page that landed meanwhile restarted the list from a newer
        // cursor; this page would leave a gap behind it.
        if (current.nextCursor !== cursor) return current;
        const known = new Set(
          [...current.items, ...current.queued].map((session) => session.id),
        );
        return {
          ...current,
          items: [
            ...current.items,
            ...page.items.filter((session) => !known.has(session.id)),
          ],
          nextCursor: page.next_cursor,
        };
      });
    } catch (requestError: unknown) {
      if (listGenerationRef.current !== generation) return;
      setError(messageOf(requestError, i18n.t("records.loadMoreFailed")));
    } finally {
      if (listGenerationRef.current === generation) setLoadingMore(false);
    }
  };

  // Reads the next page now; a narrowed list then reads on until it shows a
  // page more of matches.
  const loadEarlier = () => {
    setError(null);
    setFill({
      key: fillKey,
      target: visibleItems.length + PAGE_LIMIT,
      asked: live.nextCursor,
    });
    void loadMore();
  };

  // Reading back on its own leaves the error banner alone, so it never hides
  // a failure the operator has not seen yet.
  useEffect(() => {
    if (!filling || loadingMore) return;
    setFill({ key: fillKey, target: fillTarget, asked: live.nextCursor });
    void loadMore();
  }, [filling, loadingMore, live.nextCursor]);

  /** Drops deleted conversations from the list without reloading its pages. */
  const dropFromList = (gone: ReadonlySet<string>) => {
    setLive((current) => {
      const items = removeSessions(current.items, gone);
      const queued = removeSessions(current.queued, gone);
      return items === current.items && queued === current.queued
        ? current
        : { ...current, items, queued };
    });
  };

  // Deleting one call keeps its conversation open while calls remain, so the
  // next one can go without finding the conversation in the list again.
  const commitDelete = async (requestId: string) => {
    const generation = listGenerationRef.current;
    const sessionId = selectedId;
    const parentId =
      selected?.id === requestId ? selected.parent_request_id : null;
    setDeleting(true);
    try {
      await deleteRequestRecord(requestId);
    } catch (requestError: unknown) {
      setDeleting(false);
      notify.error(messageOf(requestError, i18n.t("records.deleteFailed")));
      return;
    }
    auditCacheRef.current.delete(requestId);
    conversationCacheRef.current.clear();
    setOverlayRecords((current) =>
      Object.fromEntries(
        Object.entries(current).filter(
          ([id, record]) =>
            id !== requestId && record.parent_request_id !== requestId,
        ),
      ),
    );
    let remaining: RequestSessionDetail | null = null;
    if (sessionId) {
      try {
        const detail = await getRequestSession(sessionId);
        if (detail.turns.length > 0) remaining = detail;
      } catch {
        // The conversation went with its last call.
      }
    }
    setDeleting(false);
    if (sessionId) {
      if (listGenerationRef.current === generation) {
        if (remaining) {
          const summary = sessionSummary(remaining);
          setLive((current) => ({
            ...current,
            items: replaceSession(current.items, summary),
            queued: replaceSession(current.queued, summary),
          }));
        } else {
          dropFromList(new Set([sessionId]));
        }
      }
      setOverlaySessions((current) => {
        const next = { ...current };
        if (remaining) next[sessionId] = remaining;
        else delete next[sessionId];
        return next;
      });
    }
    if (remaining) {
      const turns = remaining.turns;
      setSelectedTurnId(
        parentId !== null && turns.some((turn) => turn.id === parentId)
          ? parentId
          : (turns[turns.length - 1]?.id ?? null),
      );
    } else {
      returnToMonitor();
    }
    notify.success(i18n.t("records.deleted"));
  };

  const commitDeleteSessions = async (sessionIds: string[]) => {
    const generation = listGenerationRef.current;
    // The detail deletes the session it shows and has nothing left to show.
    const fromDetail = viewRef.current === "detail";
    setDeleting(true);
    setError(null);
    let deleted = 0;
    try {
      for (
        let start = 0;
        start < sessionIds.length;
        start += MAX_PURGE_SESSIONS
      ) {
        const chunk = sessionIds.slice(start, start + MAX_PURGE_SESSIONS);
        await purgeRequestRecords({ scope: "sessions", session_ids: chunk });
        deleted += chunk.length;
        const gone = new Set(chunk);
        if (listGenerationRef.current === generation) dropFromList(gone);
        setOverlaySessions((current) =>
          Object.fromEntries(
            Object.entries(current).filter(([id]) => !gone.has(id)),
          ),
        );
        if (selectedIdRef.current && gone.has(selectedIdRef.current)) {
          setSelectedId(null);
          setSelectedTurnId(null);
        }
      }
      if (fromDetail) returnToMonitor();
      else exitSelection();
      notify.success(i18n.t("records.sessionsDeleted", { count: deleted }));
    } catch (requestError: unknown) {
      const message = messageOf(requestError, i18n.t("records.deleteFailed"));
      if (fromDetail) notify.error(message);
      else setError(message);
    } finally {
      auditCacheRef.current.clear();
      conversationCacheRef.current.clear();
      setDeleting(false);
    }
  };

  const startSelection = () => {
    setCheckedIds(new Set());
    setSelecting(true);
  };

  const exitSelection = () => {
    setSelecting(false);
    setCheckedIds(new Set());
    // Selection held new sessions back; at the top they arrive as usual.
    if (atTopRef.current) {
      setLive((current) =>
        current.queued.length === 0
          ? current
          : {
              ...current,
              items: applyQueuedSessions(current.items, current.queued),
              queued: [],
            },
      );
    }
  };

  // Stable, so checking one row re-renders only that row.
  const toggleChecked = useCallback((sessionId: string) => {
    setCheckedIds((current) => {
      const next = new Set(current);
      if (!next.delete(sessionId)) next.add(sessionId);
      return next;
    });
  }, []);

  const toggleAllChecked = () => {
    setCheckedIds(
      allChecked
        ? new Set()
        : new Set(selectableItems.map((session) => session.id)),
    );
  };

  const confirmDeleteChecked = () => {
    if (checkedItems.length === 0) return;
    setPendingConfirm({
      kind: "delete-sessions",
      sessionIds: checkedItems.map((session) => session.id),
      calls: checkedItems.reduce((sum, session) => sum + session.call_count, 0),
    });
  };

  const commitPurge = async (
    input: { scope: "all" } | { scope: "before"; before: string },
  ) => {
    const generation = listGenerationRef.current;
    setPurgeBusy(true);
    setError(null);
    try {
      const result = await purgeRequestRecords(input);
      auditCacheRef.current.clear();
      conversationCacheRef.current.clear();
      setPurgeOpen(false);
      notify.success(
        i18n.t("records.purged", {
          records: result.deleted_records,
          blobs: result.deleted_audit_blobs,
        }),
      );
      const page = await listRequestSessions(sessionQuery());
      if (listGenerationRef.current !== generation) return;
      setLive({
        items: page.items,
        queued: [],
        nextCursor: page.next_cursor,
      });
      setOverlaySessions({});
    } catch (requestError: unknown) {
      setError(messageOf(requestError, i18n.t("records.purgeFailed")));
    } finally {
      setPurgeBusy(false);
    }
  };

  const openSettings = async () => {
    setSettingsOpen(true);
    setSettingsBusy(true);
    setSettingsError(null);
    setSettingsNotice(null);
    void refreshRawSealing();
    try {
      const current = await getAuditSettings();
      setSettings({ ...current });
      setSettingsDraft({ ...current });
    } catch (requestError: unknown) {
      setSettingsError(
        messageOf(requestError, i18n.t("records.auditReadFailed")),
      );
    } finally {
      setSettingsBusy(false);
    }
  };

  const commitSettings = async (
    patch: AuditSettingsPatch,
    successNotice = i18n.t("records.auditSaved"),
  ) => {
    setSettingsBusy(true);
    setSettingsError(null);
    setSettingsNotice(null);
    try {
      const updated = await updateAuditSettings(patch);
      setSettings({ ...updated });
      setSettingsDraft({ ...updated });
      if (settingsOpen) {
        setSettingsNotice(successNotice);
      } else {
        notify.success(successNotice);
      }
    } catch (requestError: unknown) {
      const message = messageOf(
        requestError,
        i18n.t("records.auditSaveFailed"),
      );
      if (settingsOpen) {
        setSettingsError(message);
      } else {
        setError(message);
      }
    } finally {
      setSettingsBusy(false);
    }
  };

  const saveSettings = () => {
    if (!settings || !settingsDraft) {
      setSettingsError(i18n.t("records.auditNotReady"));
      return;
    }
    const patch = diffSettings(settings, settingsDraft);
    if (Object.keys(patch).length === 0) {
      setSettingsNotice(i18n.t("records.auditNoChanges"));
      return;
    }
    void commitSettings(patch);
  };

  const bodyCaptureEnabled = Boolean(
    settings?.request_body_enabled || settings?.response_content_enabled,
  );
  const rawPasswordNeeded = rawPasswordMissing(rawSealing);

  const toggleBodyCapture = (enabled: boolean) => {
    if (!settings || settingsBusy) return;
    if (enabled) {
      if (settings.request_body_enabled && settings.response_content_enabled) {
        return;
      }
      void confirmCaptureEnable();
      return;
    }
    if (!settings.request_body_enabled && !settings.response_content_enabled) {
      return;
    }
    void commitSettings(
      {
        request_body_enabled: false,
        response_content_enabled: false,
      },
      i18n.t("records.captureOff"),
    );
  };

  /** Reads the sealing state again; null when it failed or went stale. */
  const refreshRawSealing = async (): Promise<RawSealingState | null> => {
    const generation = generationRef.current;
    try {
      const current = await getRawSealingStatus();
      if (generationRef.current !== generation) return null;
      setRawSealing(current);
      setRawSealingError(null);
      return current;
    } catch (requestError: unknown) {
      if (generationRef.current !== generation) return null;
      setRawSealingError(rawSealingErrorMessage(requestError));
      return null;
    }
  };

  const reloadAudit = () => {
    auditCacheRef.current.clear();
    conversationCacheRef.current.clear();
    auditGenerationRef.current += 1;
    // The detail view fetches the content again. Away from it nothing would,
    // and reopening it would paint the old raw parts before that fetch.
    if (viewRef.current !== "detail") {
      setAuditContent(null);
      setAuditError(null);
      setAuditLoading(false);
    }
    setAuditNonce((current) => current + 1);
  };

  // Without a raw password, turning capture on must set one up in the same
  // confirmation. This decides on Core's state, not the platform.
  const confirmCaptureEnable = async () => {
    const generation = generationRef.current;
    setSettingsBusy(true);
    const current = await refreshRawSealing();
    setSettingsBusy(false);
    if (generationRef.current !== generation) return;
    // Without a fresh sealing state this cannot be decided; refuse rather
    // than decide on the last one, which may predate a reset.
    if (!current) {
      const message = i18n.t("records.captureSealingUnknown");
      if (settingsOpen) {
        setSettingsError(message);
      } else {
        notify.error(message);
      }
      return;
    }
    if (rawPasswordMissing(current)) {
      setRawDialog({ kind: "capture" });
      return;
    }
    setPendingConfirm({
      kind: "audit-risk",
      patch: {
        request_body_enabled: true,
        response_content_enabled: true,
        audit_risk_acknowledged: true,
      },
    });
  };

  const closeRawDialog = (done: boolean) => {
    const closing = rawDialog;
    setRawDialog(null);
    if (!closing) return;
    if (closing.kind === "capture") {
      if (done) {
        void commitSettings(
          {
            request_body_enabled: true,
            response_content_enabled: true,
            audit_risk_acknowledged: true,
          },
          i18n.t("records.captureOn"),
        );
        return;
      }
      const cancelled = i18n.t("records.captureCancelled");
      if (settingsOpen) {
        setSettingsNotice(cancelled);
      } else {
        notify.success(cancelled);
      }
      return;
    }
    // An unlock shows raw parts; a reset deleted them.
    if (done && (closing.kind === "unlock" || closing.kind === "reset")) {
      reloadAudit();
    }
  };

  const unlockRawContent = async () => {
    const generation = generationRef.current;
    const current = await refreshRawSealing();
    if (generationRef.current !== generation) return;
    // The last state read may be stale: it could offer a proof that no longer
    // applies, or reload parts as if an ended unlock were still open.
    if (!current) {
      notify.error(i18n.t("records.unlockSealingUnknown"));
      return;
    }
    if (current.unlocked) {
      reloadAudit();
      return;
    }
    // Only the raw password unlocks raw content.
    setRawDialog({ kind: rawPasswordUnset(current) ? "set" : "unlock" });
  };

  const lockRawContent = async () => {
    try {
      const current = await lockRaw();
      setRawSealing(current);
    } catch (requestError: unknown) {
      notify.error(
        i18n.t("rawSealing.lockFailed", {
          message: rawSealingErrorMessage(requestError),
        }),
      );
      return;
    }
    notify.success(i18n.t("rawSealing.lockedNow"));
    reloadAudit();
  };

  const resolveConfirm = () => {
    const pending = pendingConfirm;
    setPendingConfirm(null);
    if (!pending) return;
    if (pending.kind === "audit-risk") {
      void commitSettings(pending.patch, i18n.t("records.captureOn"));
    } else if (pending.kind === "delete") {
      void commitDelete(pending.requestId);
    } else if (pending.kind === "delete-sessions") {
      void commitDeleteSessions(pending.sessionIds);
    } else if (pending.kind === "purge-all") {
      void commitPurge({ scope: "all" });
    } else {
      void commitPurge({ scope: "before", before: pending.before });
    }
  };

  const clearFilters = () => {
    setFilters(EMPTY_FILTERS);
    setModels([]);
    setDateRange(null);
    setCallStatus(null);
  };
  const advancedFilterCount =
    Number(Boolean(filters.serviceId)) +
    Number(Boolean(filters.protocol)) +
    filters.localAccessTokenIds.length;
  const activeChips: FilterChip[] = [];
  const addChip = (
    id: string,
    label: string,
    value: string,
    onRemove: () => void,
  ) => {
    const text = `${label}: ${value}`;
    activeChips.push({
      id,
      label: text,
      removeLabel: t("records.clearFilterNamed", { label: text }),
      onRemove,
    });
  };
  if (filters.clientType)
    addChip(
      "client",
      t("records.client"),
      clientTypeName(filters.clientType),
      () => setFilters((current) => ({ ...current, clientType: "" })),
    );
  if (dateRange !== null)
    addChip(
      "time",
      t("records.time"),
      isCustomUsageRange(dateRange)
        ? customRangeLabel(dateRange, t("usageRange.today"))
        : t(`overview.range.${dateRange}`),
      () => setDateRange(null),
    );
  if (filters.status)
    addChip("status", t("records.status"), statusLabel(filters.status), () =>
      setFilters((current) => ({ ...current, status: "" })),
    );
  if (filters.serviceId)
    addChip(
      "service",
      t("records.providerFilter"),
      serviceLabel(filters.serviceId, services) ?? filters.serviceId,
      () => setFilters((current) => ({ ...current, serviceId: "" })),
    );
  if (models.length > 0)
    addChip(
      "models",
      t("records.model"),
      models.length === 1
        ? models[0]
        : t("records.selectedModels", { count: models.length }),
      () => setModels([]),
    );
  if (filters.protocol)
    addChip(
      "protocol",
      t("records.protocol"),
      protocolEntryPath(filters.protocol),
      () => setFilters((current) => ({ ...current, protocol: "" })),
    );
  if (filters.localAccessTokenIds.length > 0)
    addChip(
      "tokens",
      t("records.accessToken"),
      filters.localAccessTokenIds.length === 1
        ? (accessTokenOptions.find(
            (option) => option.value === filters.localAccessTokenIds[0],
          )?.label ?? t("records.selectedTokens", { count: 1 }))
        : t("records.selectedTokens", {
            count: filters.localAccessTokenIds.length,
          }),
      () => setFilters((current) => ({ ...current, localAccessTokenIds: [] })),
    );

  return (
    <>
      <div className="flex h-full min-h-0 min-w-0 flex-1">
        <Tabs
          aria-labelledby="request-records-heading"
          className="flex h-full min-h-0 min-w-0 flex-1 flex-col gap-0 overflow-hidden"
          hidden={view !== "monitor"}
          value={kind}
          onValueChange={(value) => {
            setKind(value as RecordsKind);
            setFilters((current) => ({ ...current, protocol: "" }));
            // The counts cover model calls only. Kept on another list, the
            // narrowing would add model discovery they never counted.
            if (value !== "inference") setCallStatus(null);
            // Model discovery names no model, so a model filter empties it.
            if (value === "discovery") setModels([]);
            setCheckedIds(new Set());
            setError(null);
          }}
        >
          <ScrollWorkspace
            className="gap-0"
            contentAsChild
            headerClassName="gap-0"
            header={
              <>
                {view === "monitor" ? (
                  <PageHeader
                    actions={
                      // Selection takes the actions' place without changing
                      // their size, so nothing on the page moves.
                      <div className="relative">
                        <div
                          className={cn(
                            "flex flex-wrap items-center justify-end gap-2",
                            selecting && "invisible",
                          )}
                        >
                          <Label className="mr-2 inline-flex cursor-pointer items-center gap-2 text-xs font-normal text-text-secondary">
                            <span>{t("records.captureTitle")}</span>
                            {settings ? (
                              <Switch
                                aria-label={t("records.captureTitle")}
                                checked={bodyCaptureEnabled}
                                disabled={!isReady || settingsBusy}
                                onCheckedChange={toggleBodyCapture}
                                size="sm"
                              />
                            ) : (
                              <span
                                aria-hidden
                                className="inline-block h-5 w-9"
                              />
                            )}
                          </Label>
                          <div className="inline-flex">
                            <Button
                              className="rounded-r-none"
                              disabled={!isReady}
                              onClick={startSelection}
                              size="sm"
                              type="button"
                              variant="outline"
                            >
                              <Trash2 aria-hidden="true" />
                              {t("records.cleanUp")}
                            </Button>
                            <DropdownMenu>
                              <DropdownMenuTrigger asChild>
                                <Button
                                  aria-label={t("records.moreCleanup")}
                                  className="-ml-px rounded-l-none px-1.5"
                                  disabled={!isReady}
                                  size="sm"
                                  type="button"
                                  variant="outline"
                                >
                                  <ChevronDown aria-hidden="true" />
                                </Button>
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align="end">
                                <DropdownMenuItem
                                  onSelect={() => setPurgeOpen(true)}
                                >
                                  {t("records.purgeBeforeOption")}
                                </DropdownMenuItem>
                                <DropdownMenuSeparator />
                                <DropdownMenuItem
                                  onSelect={() =>
                                    setPendingConfirm({ kind: "purge-all" })
                                  }
                                  variant="destructive"
                                >
                                  {t("records.purgeAllOption")}
                                </DropdownMenuItem>
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </div>
                          <Button
                            variant="outline"
                            disabled={!isReady}
                            onClick={() => void openSettings()}
                            size="sm"
                            title={
                              rawPasswordNeeded
                                ? t("rawSealing.hint.unset")
                                : undefined
                            }
                            type="button"
                          >
                            <Settings2 aria-hidden="true" />
                            {t("records.auditSettings")}
                            {rawPasswordNeeded ? (
                              <StatusDot
                                data-slot="raw-password-dot"
                                tone="pending"
                              />
                            ) : null}
                          </Button>
                        </div>
                        {selecting ? (
                          <div
                            className="absolute inset-y-0 right-0 flex items-center gap-2 whitespace-nowrap"
                            data-testid="request-selection-actions"
                          >
                            <span
                              className="mr-1 text-xs tabular-nums text-muted-foreground"
                              role="status"
                            >
                              {t("records.selectedSessions", {
                                count: checkedItems.length,
                              })}
                            </span>
                            <Label className="mr-1 inline-flex cursor-pointer items-center gap-2 text-xs font-normal text-text-secondary">
                              <Checkbox
                                checked={allChecked}
                                disabled={
                                  selectableItems.length === 0 || deleting
                                }
                                onCheckedChange={toggleAllChecked}
                              />
                              {t("records.selectAllSessions")}
                            </Label>
                            <Button
                              disabled={deleting}
                              onClick={exitSelection}
                              size="sm"
                              type="button"
                              variant="outline"
                            >
                              {t("common.cancel")}
                            </Button>
                            <Button
                              disabled={checkedItems.length === 0 || deleting}
                              onClick={confirmDeleteChecked}
                              size="sm"
                              type="button"
                              variant={
                                checkedItems.length > 0
                                  ? "destructive"
                                  : "outline"
                              }
                            >
                              <Trash2 aria-hidden="true" />
                              {t("common.delete")}
                            </Button>
                          </div>
                        ) : null}
                      </div>
                    }
                    className="mb-0 flex-wrap"
                    description={t("records.description")}
                    title={t("records.title")}
                    titleId="request-records-heading"
                    titleSuffix={
                      <span className="inline-flex shrink-0 items-center gap-1.5 text-micro whitespace-nowrap text-muted-foreground">
                        <StatusDot
                          tone={
                            !isReady
                              ? "neutral"
                              : syncWarning || listStatus === "error"
                                ? "pending"
                                : "positive"
                          }
                        />
                        {t("records.syncEverySecond")}
                      </span>
                    }
                  />
                ) : null}
                <div className="py-3 [@media(max-height:680px)]:py-2">
                  <RequestActivityBoard
                    coreSessionKey={coreSessionKey}
                    isReady={isReady}
                    onShowFailures={() =>
                      setFilters((current) => ({
                        ...current,
                        status: "failed",
                      }))
                    }
                  />
                </div>
                {/* The list's own actions share the tab line, so the filters
                    below keep a full row of equal columns. */}
                <div className="flex min-w-0 shrink-0 items-center gap-2 border-b">
                  <TabsList
                    aria-label={t("records.kind")}
                    className="min-w-0 flex-1 justify-start"
                    variant="line"
                  >
                    <TabsTrigger className="flex-none" value="inference">
                      <MessageSquare aria-hidden="true" />
                      {t("records.inference")}
                    </TabsTrigger>
                    <TabsTrigger className="flex-none" value="discovery">
                      <ListFilter aria-hidden="true" />
                      {t("records.discovery")}
                    </TabsTrigger>
                    <TabsTrigger className="flex-none" value="all">
                      {t("common.all")}
                    </TabsTrigger>
                  </TabsList>
                  {callStatus ? (
                    // Set from a count elsewhere, so it reads as a filter in
                    // force rather than one more control to pick from.
                    <Button
                      aria-label={t("records.clearFilterNamed", {
                        label: callStatusLabel,
                      })}
                      onClick={() => setCallStatus(null)}
                      size="sm"
                      type="button"
                      variant="secondary"
                    >
                      {callStatusLabel}
                      <X aria-hidden="true" />
                    </Button>
                  ) : null}
                  <IconButton
                    variant="outline"
                    disabled={!isReady || listStatus === "loading"}
                    label={t("common.refresh")}
                    onClick={() => manualPollRef.current?.()}
                    type="button"
                  >
                    <RefreshCw aria-hidden="true" />
                  </IconButton>
                </div>
                {error ? (
                  <FormMessage className="mt-2.5 shrink-0" tone="error">
                    {error}
                  </FormMessage>
                ) : null}
                {syncWarning ? (
                  <FormMessage
                    className="mt-2.5 flex shrink-0 items-center gap-2"
                    tone="warning"
                  >
                    <StatusDot tone="pending" />
                    {syncWarning}
                  </FormMessage>
                ) : null}
                <div className="w-full min-w-0 border-b bg-background py-2">
                  <div className="grid min-w-0 grid-cols-6 gap-2 @[640px]:grid-cols-[minmax(0,1.2fr)_minmax(0,1.2fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
                    {/* Keep the common scopes available while secondary filters live in the menu. */}
                    <FilterSelect
                      ariaLabel={t("records.filter", {
                        label: t("records.client"),
                      })}
                      className="col-span-3 w-full @[640px]:col-span-1"
                      label={t("records.client")}
                      onChange={(clientType) =>
                        setFilters((current) => ({
                          ...current,
                          clientType: clientType as ClientType | "",
                        }))
                      }
                      options={[
                        { label: t("common.all"), value: "" },
                        ...clientOptions.map((clientType) => ({
                          label: clientTypeName(clientType),
                          value: clientType,
                          displayLabel: (
                            <span className="inline-flex min-w-0 items-center gap-2">
                              <ClientTypeIcon
                                clientType={clientType}
                                decorative
                                size={16}
                              />
                              <span className="truncate">
                                {clientTypeName(clientType)}
                              </span>
                            </span>
                          ),
                        })),
                      ]}
                      value={filters.clientType}
                      variant="filled"
                    />
                    <MultiFilterSelect
                      allLabel={t("common.all")}
                      ariaLabel={t("records.filter", {
                        label: t("records.model"),
                      })}
                      className="col-span-3 w-full @[640px]:col-span-1"
                      clearLabel={t("records.clearModelFilter")}
                      disabled={!isReady || kind === "discovery"}
                      emptyMessage={t("records.noModelResults")}
                      label={t("records.model")}
                      onChange={setModels}
                      options={modelOptions}
                      searchPlaceholder={t("records.searchModels")}
                      selectedCountLabel={(count) =>
                        t("records.selectedModels", { count })
                      }
                      value={models}
                    />
                    <UsageRangePicker
                      align="start"
                      ariaLabel={t("records.filter", {
                        label: t("records.time"),
                      })}
                      allLabel={t("common.all")}
                      className="col-span-2 w-full @[640px]:col-span-1"
                      label={t("records.time")}
                      onChange={(next) => setDateRange(next)}
                      presets={USAGE_RANGE_PRESETS}
                      value={dateRange}
                    />
                    <FilterSelect
                      ariaLabel={t("records.filter", {
                        label: t("records.status"),
                      })}
                      className="col-span-2 w-full @[640px]:col-span-1"
                      label={t("records.status")}
                      onChange={(status) =>
                        setFilters((current) => ({
                          ...current,
                          status: status as SessionStatus | "",
                        }))
                      }
                      options={[
                        { label: t("common.all"), value: "" },
                        ...STATUSES.map((status) => ({
                          label: statusLabel(status),
                          value: status,
                        })),
                      ]}
                      value={filters.status}
                    />
                    <Popover>
                      <PopoverTrigger asChild>
                        <Button
                          aria-label={t("records.moreFilters")}
                          className="col-span-2 min-w-0 @[640px]:col-span-1"
                          ref={moreFiltersTriggerRef}
                          size="sm"
                          type="button"
                          variant={
                            advancedFilterCount > 0 ? "secondary" : "outline"
                          }
                        >
                          <ListFilter aria-hidden="true" />
                          {t("records.moreFilters")}
                          {advancedFilterCount > 0 ? (
                            <Badge
                              variant="secondary"
                              className="px-1 text-micro tabular-nums"
                            >
                              {advancedFilterCount}
                            </Badge>
                          ) : null}
                        </Button>
                      </PopoverTrigger>
                      <PopoverContent
                        align="end"
                        aria-label={t("records.moreFilters")}
                        className="w-80 p-0"
                      >
                        <FilterMenu
                          activeCount={advancedFilterCount}
                          clearLabel={t("records.clearFilters")}
                          onClear={() => {
                            setFilters((current) => ({
                              ...current,
                              serviceId: "",
                              protocol: "",
                              localAccessTokenIds: [],
                            }));
                          }}
                          selectedCountLabel={(count) =>
                            t("records.filterCount", { count })
                          }
                          title={t("records.moreFilters")}
                          groups={[
                            {
                              id: "service",
                              label: t("records.providerFilter"),
                              count: Number(Boolean(filters.serviceId)),
                              content: (
                                <ServiceSelect
                                  ariaLabel={t("records.filter", {
                                    label: t("records.providerFilter"),
                                  })}
                                  className="w-full"
                                  label=""
                                  onChange={(serviceId) =>
                                    setFilters((current) => ({
                                      ...current,
                                      serviceId,
                                    }))
                                  }
                                  allLabel={t("common.all")}
                                  services={services}
                                  value={filters.serviceId}
                                />
                              ),
                            },
                            {
                              id: "protocol",
                              label: t("records.protocol"),
                              count: Number(Boolean(filters.protocol)),
                              content: (
                                <FilterSelect
                                  ariaLabel={t("records.filter", {
                                    label: t("records.protocol"),
                                  })}
                                  className="w-full"
                                  label=""
                                  onChange={(protocol) =>
                                    setFilters((current) => ({
                                      ...current,
                                      protocol,
                                    }))
                                  }
                                  options={[
                                    { label: t("common.all"), value: "" },
                                    ...protocolOptions.map((protocol) => ({
                                      label: protocolEntryPath(protocol),
                                      value: protocol,
                                    })),
                                  ]}
                                  value={filters.protocol}
                                />
                              ),
                            },
                            {
                              id: "tokens",
                              label: t("records.accessToken"),
                              count: filters.localAccessTokenIds.length,
                              content: (
                                <MultiFilterSelect
                                  allLabel={t("common.all")}
                                  ariaLabel={t("records.filter", {
                                    label: t("records.accessToken"),
                                  })}
                                  className="w-full"
                                  clearLabel={t("records.clearTokenFilter")}
                                  disabled={!isReady || !accessTokensReady}
                                  emptyMessage={t(
                                    "records.noAccessTokenResults",
                                  )}
                                  label=""
                                  onChange={(localAccessTokenIds) =>
                                    setFilters((current) => ({
                                      ...current,
                                      localAccessTokenIds,
                                    }))
                                  }
                                  options={accessTokenOptions}
                                  searchPlaceholder={t(
                                    "records.searchAccessTokens",
                                  )}
                                  selectAllLabel={t("records.selectAllTokens")}
                                  selectedCountLabel={(count) =>
                                    t("records.selectedTokens", { count })
                                  }
                                  value={filters.localAccessTokenIds}
                                />
                              ),
                            },
                          ]}
                        />
                      </PopoverContent>
                    </Popover>
                  </div>
                  <FilterChips
                    chips={activeChips}
                    clearLabel={t("records.clearFilters")}
                    fallbackFocusRef={moreFiltersTriggerRef}
                    label={t("records.activeFilters")}
                    onClear={clearFilters}
                    overflowLabel={(count) =>
                      t("records.moreActiveFilters", { count })
                    }
                  />
                </div>
              </>
            }
          >
            <TabsContent
              value={kind}
              className="@container/request-list min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain"
              data-testid="request-records-scroll"
              onScroll={(event) => {
                atTopRef.current = event.currentTarget.scrollTop <= 8;
              }}
              ref={monitorScrollRef}
            >
              <div className="sticky top-0 z-7">
                {/* Keep queued updates at the top of the list without taking up row space. */}
                {queuedVisibleCount > 0 ? (
                  <div className="relative">
                    <Button
                      className="absolute top-2 left-1/2 -translate-x-1/2 shadow-md"
                      onClick={applyQueue}
                      size="sm"
                      type="button"
                    >
                      {t("records.newRecords", { count: queuedVisibleCount })}
                    </Button>
                  </div>
                ) : null}
              </div>

              {!isReady || listStatus === "blocked" ? (
                <EmptyState
                  className="my-4 min-h-64"
                  title={t("records.waitingReady")}
                  description={t("records.waitingHint")}
                />
              ) : listStatus === "error" && listError ? (
                <EmptyState
                  className="my-4 min-h-64"
                  title={t("records.readFailedTitle")}
                  description={listError}
                />
              ) : (listStatus === "loading" && live.items.length === 0) ||
                // A narrowed list is not empty until earlier pages are read.
                (visibleItems.length === 0 && (filling || loadingMore)) ? (
                <RecordSkeleton />
              ) : visibleItems.length === 0 ? (
                <EmptyState
                  className="my-4 min-h-64"
                  title={t(
                    kind === "discovery"
                      ? "records.emptyDiscovery"
                      : kind === "inference"
                        ? "records.emptyInference"
                        : "records.empty",
                  )}
                  action={
                    callStatus && activeChips.length === 0 ? (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={clearFilters}
                      >
                        {" "}
                        {t("records.clearFilters")}
                      </Button>
                    ) : undefined
                  }
                />
              ) : (
                <SessionStream
                  checkedIds={selecting ? checkedIds : null}
                  coreSessionKey={coreSessionKey}
                  onOpen={selecting ? toggleChecked : openDetail}
                  selectedId={openingId ?? selectedId}
                  services={servicesById}
                  sessions={visibleItems}
                />
              )}

              {live.nextCursor ? (
                <Button
                  className="mx-auto my-3 flex"
                  variant="outline"
                  disabled={loadingMore}
                  onClick={loadEarlier}
                  type="button"
                >
                  {loadingMore ? t("common.loading") : t("records.loadEarlier")}
                </Button>
              ) : null}
            </TabsContent>
          </ScrollWorkspace>
        </Tabs>

        {view === "detail" && selectedSession && selected ? (
          <RecordDetail
            auditContent={auditContent}
            auditError={auditError}
            auditLoading={auditLoading}
            auditSettings={settings}
            conversationCache={conversationCacheRef.current}
            deleting={deleting}
            initialTab={initialDetailTab}
            newerAvailable={
              neighborSession(visibleItems, selectedId, -1) !== null
            }
            olderAvailable={
              neighborSession(visibleItems, selectedId, 1) !== null
            }
            onBack={returnToMonitor}
            onClearDecrypted={clearDecrypted}
            onNavigate={(direction) => {
              const next = neighborSession(
                visibleItems,
                openingId ?? selectedId,
                direction,
              );
              if (next) openDetail(next.id);
            }}
            onDelete={() =>
              setPendingConfirm({ kind: "delete", requestId: selected.id })
            }
            onDeleteSession={() =>
              setPendingConfirm({
                kind: "delete-sessions",
                sessionIds: [selectedSession.id],
                calls: selectedSession.call_count,
              })
            }
            onLockRaw={() => void lockRawContent()}
            onRegisterRecords={(records) =>
              setOverlayRecords((current) => ({ ...current, ...records }))
            }
            onSelectTurn={setSelectedTurnId}
            onUnlockRaw={() => void unlockRawContent()}
            rawSealing={rawSealing}
            record={selected}
            serviceName={serviceLabel(selected.service_id, services)}
            serviceNames={Object.fromEntries(
              services.map((service) => [service.id, service.name]),
            )}
            services={servicesById}
            session={selectedSession}
            turns={selectedTurns}
          />
        ) : null}
      </div>

      {settingsOpen && pendingConfirm === null && rawDialog === null ? (
        <AuditSettingsDialog
          busy={settingsBusy}
          draft={settingsDraft}
          error={settingsError}
          notice={settingsNotice}
          onCancel={() => setSettingsOpen(false)}
          onChange={(key, value) => {
            setSettingsDraft((current) =>
              current ? { ...current, [key]: value } : current,
            );
            setSettingsError(null);
            setSettingsNotice(null);
          }}
          onRawPasswordAction={(action) => {
            setSettingsError(null);
            setSettingsNotice(null);
            setRawDialog({ kind: action });
          }}
          onSave={saveSettings}
          rawSealing={rawSealing}
          rawSealingError={rawSealingError}
        />
      ) : null}

      <RawSealingDialogs
        dialog={rawDialog}
        onClose={closeRawDialog}
        onStatus={(next) => {
          setRawSealing(next);
          setRawSealingError(null);
        }}
        status={rawSealing}
      />

      {purgeOpen && pendingConfirm === null ? (
        <PurgeDialog
          before={purgeBefore}
          busy={purgeBusy}
          onBeforeChange={setPurgeBefore}
          onCancel={() => setPurgeOpen(false)}
          onSubmit={() => {
            if (!purgeBefore) return;
            setPendingConfirm({
              kind: "purge-before",
              before: new Date(purgeBefore).toISOString(),
            });
          }}
        />
      ) : null}

      {pendingConfirm ? (
        <AppConfirmDialog
          confirmLabel={
            pendingConfirm.kind === "audit-risk"
              ? t("records.confirmEnable")
              : pendingConfirm.kind === "delete" ||
                  pendingConfirm.kind === "delete-sessions"
                ? t("records.confirmDelete")
                : t("records.confirmPurge")
          }
          description={<p>{confirmMessage(pendingConfirm)}</p>}
          destructive={pendingConfirm.kind !== "audit-risk"}
          disabled={settingsBusy || deleting || purgeBusy}
          onCancel={() => {
            if (pendingConfirm.kind === "audit-risk") {
              const cancelled = i18n.t("records.captureCancelled");
              if (settingsOpen) {
                setSettingsNotice(cancelled);
              } else {
                notify.success(cancelled);
              }
            }
            setPendingConfirm(null);
          }}
          onConfirm={resolveConfirm}
          open
          title={
            pendingConfirm.kind === "audit-risk"
              ? t("records.enableCaptureTitle")
              : pendingConfirm.kind === "delete"
                ? t("records.deleteTitle")
                : pendingConfirm.kind === "delete-sessions"
                  ? pendingConfirm.sessionIds.length === 1
                    ? t("records.deleteSessionTitle")
                    : t("records.deleteSessionsTitle", {
                        count: pendingConfirm.sessionIds.length,
                      })
                  : t("records.purgeTitle")
          }
        />
      ) : null}
    </>
  );
}

/**
 * The moving durations in the detail view. Each one owns its own clock so the
 * trajectory list and the timeline beside it are never in the repaint path of
 * a label counting up.
 */
function SessionDuration({ session }: { session: RequestSession }) {
  const nowMs = useLiveClock(session.running_since !== null);
  return <>{formatDuration(sessionRuntimeMs(session, nowMs))}</>;
}

function RecordLatency({ record }: { record: RequestRecord }) {
  const nowMs = useLiveClock(
    record.latency_ms === null && !record.completed_at,
  );
  return <>{formatDuration(liveDurationMs(record, nowMs))}</>;
}

// The shortest row, a model discovery, is about 40px tall.
const FIRST_FRAME_ROW_PX = 40;
const ROWS_PER_FRAME = 10;

/**
 * How many of `total` rows to mount. WebKit spends ~2.5 ms styling and laying
 * out one session row, so mounting a full page of 50 held the switch to this
 * page for ~180 ms. The first frame gets what can fit in the window; the rest
 * follow a chunk per frame. Rows already mounted stay mounted.
 */
function useProgressiveRowCount(total: number): number {
  const [mounted, setMounted] = useState(() =>
    Math.ceil(window.innerHeight / FIRST_FRAME_ROW_PX),
  );
  const shown = Math.min(total, mounted);
  useEffect(() => {
    if (shown >= total) return;
    const frame = window.requestAnimationFrame(() =>
      setMounted(shown + ROWS_PER_FRAME),
    );
    return () => window.cancelAnimationFrame(frame);
  }, [shown, total]);
  return shown;
}

/**
 * Width of the widest status badge in the current language. Each row is its
 * own grid, so a status column sized to its content would differ per row.
 */
function useStatusColumnWidth() {
  const ref = useRef<HTMLSpanElement>(null);
  const [width, setWidth] = useState<number | null>(null);
  useLayoutEffect(() => {
    const sizer = ref.current;
    if (!sizer) return;
    const measure = () => {
      const measured = Math.ceil(sizer.getBoundingClientRect().width);
      setWidth(measured > 0 ? measured : null);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(sizer);
    return () => observer.disconnect();
  }, []);
  return { ref, width };
}

const NO_CALLS: readonly RequestActivityCall[] = [];

function SessionStream({
  coreSessionKey,
  sessions,
  services,
  selectedId,
  checkedIds,
  onOpen,
}: {
  coreSessionKey: string | null;
  sessions: RequestSession[];
  services: RequestServiceMap;
  selectedId: string | null;
  /** The checked rows in selection mode, where `onOpen` toggles a row. */
  checkedIds: ReadonlySet<string> | null;
  onOpen: (sessionId: string) => void;
}) {
  const t = i18n.t.bind(i18n);
  // The live board's read of the last few minutes tells each lane its steps.
  const recentCalls = useRequestActivityFeed(coreSessionKey)?.calls ?? NO_CALLS;
  // Conversations in progress lead the list. One waiting between calls moves
  // down into its day once its wait runs out, so the list re-reads the clock
  // every second while any is in progress, even if no poll brings news.
  const { active, rest } = splitActiveSessions(sessions, Date.now());
  useLiveClock(active.length > 0, 1000);
  // Only the date buckets need "now", and those turn over once a day.
  const groups = groupSessionsByDate(rest);
  let remaining = useProgressiveRowCount(sessions.length) - active.length;
  const statusColumn = useStatusColumnWidth();
  return (
    <div
      aria-label={t("records.sessionFlow")}
      className="relative pb-3"
      role="feed"
      style={
        statusColumn.width === null
          ? undefined
          : ({
              "--request-status-column": `${statusColumn.width}px`,
            } as CSSProperties)
      }
    >
      <span
        aria-hidden="true"
        className="invisible absolute top-0 left-0 grid"
        ref={statusColumn.ref}
      >
        {STATUSES.map((status) => (
          <StatusBadge className="col-start-1 row-start-1" key={status}>
            {/* Generated content keeps the labels out of the page text and find-in-page. */}
            <span
              className="before:content-[attr(data-label)]"
              data-label={statusLabel(status)}
            />
          </StatusBadge>
        ))}
      </span>
      {active.length > 0 ? (
        <section
          className="mt-3 first:mt-0"
          data-testid="active-sessions"
          key="active"
        >
          <div className="flex items-center gap-2 px-3 pt-4 pb-2 text-xs font-medium text-muted-foreground">
            <span>{t("records.activityInProgress")}</span>
            <Badge className="font-normal tabular-nums" variant="secondary">
              {t("records.countItems", { count: active.length })}
            </Badge>
          </div>
          {active.map((session) => (
            <SessionLane
              checked={checkedIds?.has(session.id)}
              key={session.id}
              onOpen={onOpen}
              recentCalls={recentCalls}
              selected={session.id === selectedId}
              services={services}
              session={session}
            />
          ))}
        </section>
      ) : null}
      {groups.map((group) => {
        const rows = group.sessions.slice(0, remaining);
        remaining -= rows.length;
        if (rows.length === 0) return null;
        return (
          <section className="mt-3 first:mt-0" key={group.key}>
            <div className="flex items-center gap-2 px-3 pt-4 pb-2 text-xs font-medium text-muted-foreground">
              <span>{group.label}</span>
              <Badge className="font-normal tabular-nums" variant="secondary">
                {t("records.countItems", { count: group.sessions.length })}
              </Badge>
            </div>
            {rows.map((session) =>
              isModelDiscoveryProtocol(session.input_protocol) ? (
                <DiscoveryRow
                  checked={checkedIds?.has(session.id)}
                  key={session.id}
                  onOpen={onOpen}
                  selected={session.id === selectedId}
                  services={services}
                  session={session}
                />
              ) : (
                <SessionRow
                  checked={checkedIds?.has(session.id)}
                  key={session.id}
                  onOpen={onOpen}
                  selected={session.id === selectedId}
                  services={services}
                  session={session}
                />
              ),
            )}
          </section>
        );
      })}
    </div>
  );
}

/**
 * How a list row behaves: it opens its session, or in selection mode
 * (`checked` defined) it is a checkbox. A running session cannot be checked;
 * its records stay until it ends.
 */
function rowBehavior(
  session: RequestSession,
  checked: boolean | undefined,
  selected: boolean,
  onOpen: (sessionId: string) => void,
  t: typeof i18n.t,
) {
  const selecting = checked !== undefined;
  const locked = selecting && sessionRunning(session);
  return {
    locked,
    props: {
      "aria-checked": selecting ? checked : undefined,
      "aria-current": !selecting && selected ? ("true" as const) : undefined,
      "aria-disabled": locked || undefined,
      onClick: () => {
        if (!locked) onOpen(session.id);
      },
      role: selecting ? "checkbox" : undefined,
      title: locked ? t("records.sessionRunningDelete") : undefined,
    },
  };
}

const DiscoveryRow = memo(function DiscoveryRow({
  session,
  services,
  selected,
  checked,
  onOpen,
}: {
  session: RequestSession;
  services: RequestServiceMap;
  selected: boolean;
  /** Set in selection mode, where the row is a checkbox. */
  checked: boolean | undefined;
  onOpen: (sessionId: string) => void;
}) {
  const t = useT();
  const nowMs = useLiveClock(session.running_since !== null);
  const service = requestServiceIdentity(session, services);
  const behavior = rowBehavior(session, checked, selected, onOpen, t);
  return (
    <DataRow
      asChild
      className="grid grid-cols-[max(5rem,var(--request-status-column,0px))_1.25rem_minmax(0,1fr)_auto] gap-x-3 gap-y-1 px-3 py-2.5 @[680px]:grid-cols-[max(5rem,var(--request-status-column,0px))_1.25rem_minmax(0,1fr)_5rem_6rem]"
    >
      <Button
        {...behavior.props}
        className="h-auto w-full rounded-none bg-transparent text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset aria-checked:bg-accent aria-[current=true]:bg-accent"
        data-session-id={session.id}
        data-testid="request-session-row"
        type="button"
        variant="ghost"
      >
        <StatusBadge
          className="col-start-1 row-start-1"
          tone={
            session.status === "cancelled"
              ? "neutral"
              : statusTone(session.status)
          }
        >
          {statusLabel(session.status)}
        </StatusBadge>
        {checked === undefined ? (
          <ClientTypeIcon
            className="col-start-2 row-start-1 mt-0.5"
            clientType={session.client_type}
          />
        ) : (
          <CheckboxMark
            checked={checked}
            className="col-start-2 row-start-1 mt-1 justify-self-center"
            disabled={behavior.locked}
          />
        )}
        <span className="col-start-3 row-start-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <span className="text-xs font-medium">
            {t("records.fetchModels")}
          </span>
          {service.id ? (
            <RequestServiceLabel className="text-xs" service={service} />
          ) : null}
          <code className="truncate text-micro text-muted-foreground">
            GET {protocolEntryPath(session.input_protocol)}
          </code>
        </span>
        <span className="col-start-3 row-start-2 text-micro tabular-nums text-muted-foreground @[680px]:col-start-4 @[680px]:row-start-1 @[680px]:text-right">
          {formatDuration(sessionRuntimeMs(session, nowMs))}
        </span>
        <time
          className="col-start-4 row-start-1 text-right text-micro tabular-nums text-muted-foreground @[680px]:col-start-5"
          dateTime={session.last_started_at}
          title={formatDateTime(session.last_started_at)}
        >
          {formatTime(session.last_started_at)}
        </time>
      </Button>
    </DataRow>
  );
});

const SessionRow = memo(function SessionRow({
  session,
  services,
  selected,
  checked,
  onOpen,
}: {
  session: RequestSession;
  services: RequestServiceMap;
  selected: boolean;
  /** Set in selection mode, where the row is a checkbox. */
  checked: boolean | undefined;
  onOpen: (sessionId: string) => void;
}) {
  // Memoized, so it subscribes to the language itself.
  const t = useT();
  // The elapsed time is interpolated into a translated sentence, so the row is
  // the smallest thing that can repaint it. Only active requests tick.
  const nowMs = useLiveClock(session.running_since !== null);
  const service = requestServiceIdentity(session, services);
  const behavior = rowBehavior(session, checked, selected, onOpen, t);
  const title = compactTitle(session.title) || t("records.untitledSession");
  return (
    <DataRow
      asChild
      className="grid grid-cols-[max(4.5rem,var(--request-status-column,0px))_1.25rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1.5 px-3 py-2.5"
    >
      <Button
        {...behavior.props}
        className="h-auto w-full rounded-none bg-transparent text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset aria-checked:bg-accent aria-[current=true]:bg-accent"
        data-session-id={session.id}
        data-testid="request-session-row"
        type="button"
        variant="ghost"
      >
        <StatusBadge
          className="col-start-1 row-start-1"
          tone={
            session.status === "cancelled"
              ? "neutral"
              : statusTone(session.status)
          }
        >
          {statusLabel(session.status)}
        </StatusBadge>
        {checked === undefined ? (
          <ClientTypeIcon
            className="col-start-2 row-start-1 mt-0.5"
            clientType={session.client_type}
          />
        ) : (
          <CheckboxMark
            checked={checked}
            className="col-start-2 row-start-1 mt-1 justify-self-center"
            disabled={behavior.locked}
          />
        )}
        <span className="col-span-2 col-start-3 row-start-1 flex min-w-0 items-center gap-2 @[560px]/request-list:col-span-1">
          <strong
            className="min-w-0 truncate text-sm font-medium"
            title={session.title || title}
          >
            {title}
          </strong>
          {session.privacy_warned ? (
            <StatusBadge
              className="shrink-0"
              data-testid="privacy-warned-marker"
              title={t("records.privacyWarnedHint")}
              tone="pending"
            >
              {t("records.privacyWarned")}
            </StatusBadge>
          ) : null}
        </span>
        <span className="col-span-full row-start-3 grid min-w-0 grid-cols-[minmax(0,1fr)_8rem] items-center gap-x-3 gap-y-1 text-xs leading-5 @[560px]/request-list:col-span-3 @[560px]/request-list:col-start-2 @[560px]/request-list:row-start-2 @[560px]/request-list:grid-cols-[minmax(0,1fr)_minmax(0,0.75fr)_minmax(0,1.7fr)_4rem]">
          <code
            className="min-w-0 max-w-full truncate font-mono text-muted-foreground"
            title={protocolEntryPath(session.input_protocol)}
          >
            {protocolEntryPath(session.input_protocol)}
          </code>
          <RequestServiceLabel
            className="max-w-40 font-medium text-text-secondary"
            label={
              session.call_count > 1 ? t("records.latestProvider") : undefined
            }
            labelClassName="sr-only"
            service={service}
          />
          <span className="inline-flex min-w-0 flex-wrap gap-x-1 whitespace-nowrap tabular-nums text-muted-foreground">
            {t(
              session.turn_count === session.call_count
                ? "records.sessionMetaTurns"
                : "records.sessionMeta",
              {
                count: session.turn_count,
                turns: session.turn_count,
                calls: session.call_count,
                duration: formatDuration(sessionRuntimeMs(session, nowMs)),
              },
            )}
            {session.output_tokens_per_second != null ? (
              <span title={t("records.outputSpeed")}>
                {` · ${session.output_tokens_per_second.toFixed(1)} tok/s`}
              </span>
            ) : null}
          </span>
          <time
            className="text-right text-xs leading-5 tabular-nums text-muted-foreground"
            dateTime={session.last_started_at}
            title={formatDateTime(session.last_started_at)}
          >
            {formatTime(session.last_started_at)}
          </time>
        </span>
        {/* Capped by the list width, not a fixed size: a redirect shows two names. */}
        <ModelLabel
          className="col-span-2 col-start-3 row-start-2 text-xs text-text-secondary @[560px]/request-list:col-span-1 @[560px]/request-list:col-start-4 @[560px]/request-list:row-start-1 @[560px]/request-list:max-w-[50cqw] @[560px]/request-list:justify-end"
          fallback={t("records.unspecifiedModel")}
          model={session.requested_model}
          reasoningEffort={session.reasoning_effort}
          redirectedTo={session.model_redirect?.to}
        />
      </Button>
    </DataRow>
  );
});

/**
 * A conversation in progress, drawn like a worker on a job board: the model
 * serving it on the left and the conversation in its slot. The slot sweeps
 * while a call runs; between calls it shows how long the conversation has
 * waited and how its last call ended.
 */
const SessionLane = memo(function SessionLane({
  session,
  services,
  selected,
  checked,
  onOpen,
  recentCalls,
}: {
  session: RequestSession;
  services: RequestServiceMap;
  selected: boolean;
  /** Set in selection mode, where the lane is a checkbox. */
  checked: boolean | undefined;
  onOpen: (sessionId: string) => void;
  /** Every conversation's calls of the last few minutes. */
  recentCalls: readonly RequestActivityCall[];
}) {
  const t = useT();
  const trail = useMemo(
    () => laneTrail(recentCalls, session),
    [recentCalls, session],
  );
  const calling = sessionRunning(session);
  // A running call shows tenths of a second; a wait counts whole seconds.
  const nowMs = useLiveClock(true, calling ? LIVE_CLOCK_INTERVAL_MS : 1000);
  const since = conversationActivity(session, nowMs)?.since ?? nowMs;
  const calls = Math.max(1, session.active_request_starts.length);
  const service = requestServiceIdentity(session, services);
  const behavior = rowBehavior(session, checked, selected, onOpen, t);
  const title = compactTitle(session.title) || t("records.untitledSession");
  const selecting = checked !== undefined;
  const outcome =
    !calling && session.status !== "succeeded" ? session.status : null;
  return (
    <DataRow
      asChild
      className={cn(
        "grid items-center gap-x-3 gap-y-2 px-3 py-2.5",
        selecting
          ? "grid-cols-[1.25rem_minmax(0,1fr)] @[560px]/request-list:grid-cols-[1.25rem_minmax(0,11rem)_minmax(0,1fr)]"
          : "grid-cols-1 @[560px]/request-list:grid-cols-[minmax(0,11rem)_minmax(0,1fr)]",
      )}
    >
      <Button
        {...behavior.props}
        className="h-auto w-full rounded-none bg-transparent text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset aria-checked:bg-accent aria-[current=true]:bg-accent"
        data-activity={calling ? "calling" : "idle"}
        data-session-id={session.id}
        data-testid="request-session-row"
        type="button"
        variant="ghost"
      >
        {selecting ? (
          <CheckboxMark
            checked={checked}
            className="row-span-2 justify-self-center @[560px]/request-list:row-span-1"
            disabled={behavior.locked}
          />
        ) : null}
        <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 @[560px]/request-list:flex-col @[560px]/request-list:items-start">
          <ModelLabel
            className="text-xs font-medium text-text-secondary"
            fallback={t("records.unspecifiedModel")}
            model={session.requested_model}
            reasoningEffort={session.reasoning_effort}
            redirectDisplay="target"
            redirectedTo={session.model_redirect?.to}
          />
          <RequestServiceLabel
            className="max-w-full text-micro text-muted-foreground"
            label={
              session.call_count > 1 ? t("records.latestProvider") : undefined
            }
            labelClassName="sr-only"
            service={service}
          />
        </span>
        <ActivitySlot
          className="grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-0.5 px-2.5 py-1.5"
          state={calling ? "running" : "idle"}
        >
          <ClientTypeIcon clientType={session.client_type} />
          <span className="flex min-w-0 items-center gap-2">
            <strong
              className="min-w-0 truncate text-sm font-medium"
              title={session.title || title}
            >
              {title}
            </strong>
            {session.privacy_warned ? (
              <StatusBadge
                className="shrink-0"
                data-testid="privacy-warned-marker"
                title={t("records.privacyWarnedHint")}
                tone="pending"
              >
                {t("records.privacyWarned")}
              </StatusBadge>
            ) : null}
          </span>
          {calling || !outcome ? (
            <LaneActionLabel
              action={laneAction(session)}
              calls={calls}
              elapsedMs={nowMs - since}
            />
          ) : (
            <span
              className="inline-flex items-center gap-1.5 justify-self-end text-xs whitespace-nowrap tabular-nums text-muted-foreground"
              data-testid="lane-state"
              title={t("records.laneIdleHint")}
            >
              <StatusBadge
                tone={outcome === "cancelled" ? "neutral" : statusTone(outcome)}
              >
                {statusLabel(outcome)}
              </StatusBadge>
              {t("records.laneIdle", {
                duration: formatWait(nowMs - since),
              })}
            </span>
          )}
          <span className="col-span-2 col-start-2 flex min-w-0 items-center justify-between gap-3 text-xs leading-5 text-muted-foreground">
            <LaneTrail firstCall={session.call_count === 1} steps={trail} />
            <span className="min-w-0 truncate tabular-nums">
              {t(
                session.turn_count === session.call_count
                  ? "records.sessionMetaTurns"
                  : "records.sessionMeta",
                {
                  count: session.turn_count,
                  turns: session.turn_count,
                  calls: session.call_count,
                  duration: formatDuration(sessionRuntimeMs(session, nowMs)),
                },
              )}
              {session.output_tokens_per_second != null ? (
                <span title={t("records.outputSpeed")}>
                  {` · ${session.output_tokens_per_second.toFixed(1)} tok/s`}
                </span>
              ) : null}
            </span>
          </span>
        </ActivitySlot>
      </Button>
    </DataRow>
  );
});

/**
 * The steps before a lane's current action, one mark each, oldest first.
 * Names and how long the tools ran are on hover and for screen readers.
 */
function LaneTrail({
  firstCall,
  steps,
}: {
  firstCall: boolean;
  steps: readonly LaneStep[];
}) {
  const t = useT();
  if (steps.length === 0) {
    return firstCall ? (
      <span className="shrink-0">{t("records.laneFirstCall")}</span>
    ) : (
      <span />
    );
  }
  return (
    <span
      className="flex min-w-0 shrink-0 items-center gap-1.5"
      data-testid="lane-trail"
    >
      {steps.map((step, index) => {
        const name =
          step.kind === "tool"
            ? step.tool
            : t(
                step.kind === "reasoning"
                  ? "records.laneStepThinking"
                  : "records.laneStepAnswer",
              );
        const counted = step.count > 1 ? `${name} ×${step.count}` : name;
        const label =
          step.ranMs === null
            ? counted
            : t("records.laneStepRan", {
                step: counted,
                duration: formatDuration(step.ranMs),
              });
        return (
          <span className="inline-flex items-center gap-1.5" key={index}>
            {index > 0 ? (
              <span aria-hidden className="size-1 rounded-full bg-border" />
            ) : null}
            <span
              className="inline-flex items-center gap-0.5 text-text-secondary"
              title={label}
            >
              {step.kind === "tool" ? (
                <ToolKindIcon className="size-3.5" name={step.tool} />
              ) : step.kind === "reasoning" ? (
                <Brain
                  animateOnHover={false}
                  aria-hidden="true"
                  className="size-3.5"
                />
              ) : (
                <MessageSquare
                  animateOnHover={false}
                  aria-hidden="true"
                  className="size-3.5"
                />
              )}
              <span className="sr-only">{label}</span>
              {step.count > 1 ? (
                <span
                  aria-hidden="true"
                  className="text-micro tabular-nums text-muted-foreground"
                >
                  ×{step.count}
                </span>
              ) : null}
            </span>
          </span>
        );
      })}
    </span>
  );
}

/** A wait between calls in whole seconds, as minutes once tools run longer. */
function formatWait(elapsedMs: number): string {
  const waited = Math.max(0, elapsedMs);
  return waited < 60_000
    ? `${Math.floor(waited / 1000)} s`
    : formatDuration(waited);
}

/**
 * What a lane says its conversation is doing and for how long: a running
 * call counts tenths of a second, a wait between calls whole seconds.
 */
function LaneActionLabel({
  action,
  calls,
  elapsedMs,
}: {
  action: LaneAction;
  calls: number;
  elapsedMs: number;
}) {
  const t = useT();
  const running =
    action.kind === "waiting" ||
    action.kind === "thinking" ||
    action.kind === "writing" ||
    action.kind === "calling";
  const duration = running
    ? formatDuration(Math.max(0, elapsedMs))
    : formatWait(elapsedMs);
  const iconClass = "size-3.5 shrink-0";
  let icon: ReactNode = null;
  let label: string;
  let hint: string | undefined;
  switch (action.kind) {
    case "waiting":
      icon = (
        <LoaderCircle
          animateOnHover={false}
          aria-hidden="true"
          className={cn(iconClass, "motion-safe:animate-spin")}
        />
      );
      label = t("records.laneWaiting", { duration });
      break;
    case "thinking":
      icon = (
        <Brain
          animateOnHover={false}
          aria-hidden="true"
          className={iconClass}
        />
      );
      label = t("records.laneThinking", { duration });
      break;
    case "writing":
      icon = (
        <SquarePen
          animateOnHover={false}
          aria-hidden="true"
          className={iconClass}
        />
      );
      label = t("records.laneWriting", { duration });
      break;
    case "calling":
      icon = <ToolKindIcon className={iconClass} name={action.tool} />;
      label = action.tool
        ? t("records.laneCallingTool", { tool: action.tool, duration })
        : t("records.laneCallingAnyTool", { duration });
      break;
    case "running": {
      // The conversation view joins tool names the same way.
      const shown = action.tools.slice(0, 2);
      const names = shown.join(" · ");
      icon = <ToolKindIcon className={iconClass} name={action.tools[0]} />;
      label = t("records.laneRunningTools", {
        tools:
          action.tools.length > shown.length
            ? `${names} +${action.tools.length - shown.length}`
            : names,
        duration,
      });
      hint = t("records.laneRunningToolsHint");
      break;
    }
    case "replied":
      icon = (
        <MessageSquare
          animateOnHover={false}
          aria-hidden="true"
          className={iconClass}
        />
      );
      label = t("records.laneReplied", { duration });
      hint = t("records.laneRepliedHint");
      break;
    case "idle":
      label = t("records.laneIdle", { duration });
      hint = t("records.laneIdleHint");
      break;
  }
  if (running && calls > 1) {
    label = t("records.laneCallingMany", { count: calls, duration });
  }
  return (
    <span
      className={cn(
        "inline-flex min-w-0 items-center gap-1.5 justify-self-end text-xs whitespace-nowrap tabular-nums",
        running ? "font-medium text-primary" : "text-muted-foreground",
      )}
      data-action={action.kind}
      data-testid="lane-state"
      title={hint}
    >
      {icon}
      <span
        className={cn(
          "min-w-0 truncate",
          running && "shimmer motion-reduce:shimmer-none",
        )}
      >
        {label}
      </span>
    </span>
  );
}

function RecordDetail({
  serviceNames,
  services,
  record,
  session,
  turns,
  serviceName,
  auditContent,
  auditLoading,
  auditError,
  auditSettings,
  conversationCache,
  deleting,
  rawSealing,
  initialTab,
  newerAvailable,
  olderAvailable,
  onBack,
  onDelete,
  onDeleteSession,
  onNavigate,
  onClearDecrypted,
  onLockRaw,
  onSelectTurn,
  onRegisterRecords,
  onUnlockRaw,
}: {
  record: RequestRecord;
  serviceNames: Record<string, string>;
  services: RequestServiceMap;
  session: RequestSession;
  turns: RequestRecord[];
  serviceName: string | null;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  auditSettings: AuditSettings | null;
  conversationCache: ConversationContentCache;
  deleting: boolean;
  rawSealing: RawSealingState | null;
  initialTab: "conversation" | "trajectory";
  newerAvailable: boolean;
  olderAvailable: boolean;
  onBack: () => void;
  /** Deletes the selected call with its retries. */
  onDelete: () => void;
  /** Deletes every call of the conversation. */
  onDeleteSession: () => void;
  onNavigate: (direction: -1 | 1) => void;
  onClearDecrypted: () => void;
  onLockRaw: () => void;
  onSelectTurn: (requestId: string) => void;
  onRegisterRecords: (records: Record<string, RequestRecord>) => void;
  onUnlockRaw: () => void;
}) {
  const t = useT();
  const copyFeedback = useCopyFeedback();
  const [bundleSize, setBundleSize] = useState<number | null>(null);
  // The tab the operator reads conversations in is a habit, not a property
  // of one session, so it is remembered across sessions and page visits.
  const [rememberedTab, setRememberedTab] = useWorkspaceSnapshot<DetailTab>(
    "request-detail-tab",
    initialTab,
    "desktop",
  );
  const discovery = isModelDiscoveryProtocol(session.input_protocol);
  const defaultTab: DetailTab =
    rememberedTab === "conversation" && discovery
      ? "trajectory"
      : rememberedTab;
  const [detailTab, setDetailTabState] = useState<DetailTab>(defaultTab);
  const setDetailTab = (tab: DetailTab) => {
    setDetailTabState(tab);
    if (tab === "conversation" || tab === "trajectory") setRememberedTab(tab);
  };
  const [outlineOpen, setOutlineOpen] = useWorkspaceSnapshot(
    "request-conversation-outline",
    true,
    "desktop",
  );
  const [childrenByRoot, setChildrenByRoot] = useState<
    Record<string, RequestRecord[]>
  >({});
  const rootId = record.parent_request_id ?? record.id;
  const rootRecord = turns.find((turn) => turn.id === rootId) ?? record;
  // Running calls keep their records, so the whole conversation waits.
  const sessionLive =
    sessionRunning(session) || turns.some((turn) => turn.status === "pending");
  const recoveryRecords = [...(childrenByRoot[rootId] ?? []), rootRecord];
  const isChild = record.parent_request_id !== null;
  const requestPart = auditContent?.request_body ?? null;
  const responsePart = auditContent?.response_content ?? null;
  const upstreamRequestPart = auditContent?.upstream_request_body ?? null;
  const upstreamResponsePart = auditContent?.upstream_response_content ?? null;

  useEffect(() => {
    setDetailTabState(defaultTab);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.id]);

  // One request per root that retried, so this must not re-run on a new `turns`
  // array alone: a conversation with 95 turns would refire a burst of calls
  // every time the detail reloaded. Pin the roots until the set or one of their
  // attempt counts actually changes.
  const rootsWithChildren = useMemo(
    () => turns.filter((turn) => turn.child_count > 0),
    [turns],
  );
  const childRootsKey = rootsWithChildren
    .map((turn) => `${turn.id}:${turn.child_count}`)
    .join(",");
  const childRoots = useMemo(() => rootsWithChildren, [childRootsKey]);

  useEffect(() => {
    if (childRoots.length === 0) {
      setChildrenByRoot((current) =>
        Object.keys(current).length === 0 ? current : {},
      );
      return;
    }
    let cancelled = false;
    void Promise.all(
      childRoots.map(async (turn) => {
        const page = await listRequestRecordChildren(turn.id);
        return [turn.id, page.items] as const;
      }),
    )
      .then((entries) => {
        if (cancelled) return;
        setChildrenByRoot(Object.fromEntries(entries));
        const registered: Record<string, RequestRecord> = {};
        for (const [, items] of entries) {
          for (const child of items) registered[child.id] = child;
        }
        if (Object.keys(registered).length > 0) onRegisterRecords(registered);
      })
      .catch(() => {
        if (!cancelled) setChildrenByRoot({});
      });
    return () => {
      cancelled = true;
    };
  }, [childRoots]);

  // Failures and tokens across the whole conversation, retries included,
  // for the one-line summary under the title.
  const everyRecord = useMemo(
    () => [...turns, ...Object.values(childrenByRoot).flat()],
    [turns, childrenByRoot],
  );
  // A failure means what it means in the usage figures and the tray; a policy
  // block is its own outcome, as on each call's badge. Unlike those figures,
  // retried attempts count here, because they explain the conversation.
  const failedCalls = everyRecord.filter(
    (item) => displayRequestStatus(item.status, item.http_status) === "failed",
  ).length;
  const blockedCalls = everyRecord.filter(
    (item) => item.status === "blocked",
  ).length;
  const sessionTokens = everyRecord.reduce<{
    input: number;
    output: number;
  } | null>((sum, item) => {
    if (!item.usage) return sum;
    return {
      input: (sum?.input ?? 0) + item.usage.input_tokens,
      output: (sum?.output ?? 0) + item.usage.output_tokens,
    };
  }, null);

  const exportEnvironment = useExportEnvironment(auditSettings);
  // The same diagnosis context for copy and export: a record alone cannot
  // show the session failures or settings that explain it.
  const bundleContext = {
    serviceLabel: serviceName,
    session,
    turns,
    childrenByRoot,
    serviceNames,
    environment: exportEnvironment,
  };

  const copyBundle = (includeBodies: boolean) => {
    const bundle = buildRecordBundle(record, auditContent, {
      ...bundleContext,
      includeBodies,
    });
    setBundleSize(includeBodies ? bundle.length : null);
    copyFeedback.copy(includeBodies ? "bundle" : "bundle-meta", bundle);
  };

  const copySkillDiagnostic = () => {
    copyFeedback.copy(
      "skill-diagnostic",
      buildSkillDiagnostic({
        session,
        selectedRequestId: record.id,
        turns,
        childrenByRoot,
        serviceNames,
      }),
    );
  };

  const exportBundle = (format: BundleFormat) => {
    const bundle = buildRecordBundle(record, auditContent, {
      ...bundleContext,
      includeBodies: true,
      format,
    });
    const filename = bundleFilename(record.id, format);
    void saveTextFile(filename, bundle)
      .then((path) => {
        if (path) notify.success(t("records.exported", { path }));
      })
      .catch(() => {
        notify.error(t("records.exportFailed"));
      });
  };

  return (
    <section
      aria-labelledby="request-detail-heading"
      className="@container/detail flex h-full min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
    >
      <PageHeader
        back={{ label: t("records.live"), onClick: onBack }}
        className="mb-0 flex-wrap gap-x-4 gap-y-2"
        titleGroupClassName="flex-1 basis-64"
        title={
          isModelDiscoveryProtocol(session.input_protocol)
            ? t("records.fetchModels")
            : compactTitle(session.title) || t("records.untitledSession")
        }
        titleId="request-detail-heading"
        titlePrefix={<ClientTypeIcon clientType={record.client_type} />}
        overline={
          <StatusBadge
            className="shrink-0"
            data-testid="record-status"
            tone={statusTone(session.status)}
          >
            {statusLabel(session.status)}
            {session.status === "pending" ? (
              <span className="ml-1.5 tabular-nums">
                <SessionDuration session={session} />
              </span>
            ) : null}
          </StatusBadge>
        }
        titleSuffix={
          <>
            <span aria-hidden="true" className="text-border">
              ·
            </span>
            <RequestServiceLabel
              className="max-w-64 text-xs"
              labelClassName="sr-only"
              service={requestServiceIdentity(record, services)}
            />
            {!discovery && session.requested_model ? (
              <>
                <span aria-hidden="true" className="text-border">
                  ·
                </span>
                <ModelLabel
                  className="max-w-56 text-xs"
                  model={session.requested_model}
                  reasoningEffort={session.reasoning_effort}
                  redirectedTo={session.model_redirect?.to}
                />
              </>
            ) : null}
          </>
        }
        actionsClassName="flex-wrap justify-end gap-1.5"
        actions={
          <>
            {copyFeedback.activeKey !== null &&
            MENU_COPY_KEYS.has(copyFeedback.activeKey) ? (
              <span className="text-micro text-muted-foreground" role="status">
                {copyButtonLabel(copyFeedback, copyFeedback.activeKey)}
              </span>
            ) : null}
            <IconButton
              data-testid="newer-session"
              disabled={!newerAvailable}
              label={t("records.newerSession")}
              onClick={() => onNavigate(-1)}
              type="button"
            >
              <ChevronUp aria-hidden="true" />
            </IconButton>
            <IconButton
              data-testid="older-session"
              disabled={!olderAvailable}
              label={t("records.olderSession")}
              onClick={() => onNavigate(1)}
              type="button"
            >
              <ChevronDown aria-hidden="true" />
            </IconButton>
            <div className="inline-flex">
              <Button
                className="rounded-r-none"
                onClick={() => copyBundle(true)}
                size="sm"
                type="button"
                variant="outline"
              >
                <Copy aria-hidden="true" />
                {copyButtonLabel(
                  copyFeedback,
                  "bundle",
                  t("records.copyAll"),
                  bundleSize !== null
                    ? t("records.copiedBytes", {
                        size: formatBytes(bundleSize),
                      })
                    : t("common.copied"),
                )}
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    aria-label={t("records.copyExportOptions")}
                    className="-ml-px rounded-l-none px-1.5"
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    <ChevronDown aria-hidden="true" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={() => copyBundle(false)}>
                    <Copy aria-hidden="true" />
                    {copyButtonLabel(
                      copyFeedback,
                      "bundle-meta",
                      t("records.copyMetaHttp"),
                    )}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onSelect={() => exportBundle("markdown")}>
                    {t("records.exportMarkdown")}
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => exportBundle("txt")}>
                    {t("records.exportTxt")}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  aria-label={t("records.moreActions")}
                  data-testid="record-more-actions"
                  size="icon-sm"
                  type="button"
                  variant="outline"
                >
                  <Ellipsis aria-hidden="true" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  data-testid="copy-skill-diagnostic"
                  onSelect={copySkillDiagnostic}
                  title={t("records.copySkillDiagnosticHint")}
                >
                  <Copy aria-hidden="true" />
                  {copyButtonLabel(
                    copyFeedback,
                    "skill-diagnostic",
                    t("records.copySkillDiagnostic"),
                  )}
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => copyFeedback.copy("session-id", session.id)}
                >
                  <Copy aria-hidden="true" />
                  {copyButtonLabel(
                    copyFeedback,
                    "session-id",
                    t("records.copySessionId"),
                  )}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  disabled={deleting || record.status === "pending"}
                  onSelect={onDelete}
                  variant="destructive"
                >
                  <Trash2 aria-hidden="true" />
                  {record.status === "pending"
                    ? t("records.deletePending")
                    : deleting
                      ? t("records.deleting")
                      : t("records.deleteCall")}
                </DropdownMenuItem>
                <DropdownMenuItem
                  data-testid="delete-session"
                  disabled={deleting || sessionLive}
                  onSelect={onDeleteSession}
                  variant="destructive"
                >
                  <Trash2 aria-hidden="true" />
                  {sessionLive
                    ? t("records.sessionRunningDelete")
                    : t("records.deleteSession")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      />

      <Tabs
        className="flex min-h-0 min-w-0 flex-1 flex-col gap-0"
        onValueChange={(value) => {
          if (DETAIL_TABS.includes(value as DetailTab)) {
            setDetailTab(value as DetailTab);
          }
        }}
        value={detailTab}
      >
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b py-1.5">
          <TabsList aria-label={t("records.sections")} className="h-8">
            {!discovery && (
              <TabsTrigger
                onClick={() => setDetailTab("conversation")}
                value="conversation"
              >
                {t("conversation.tab")}
              </TabsTrigger>
            )}
            <TabsTrigger
              onClick={() => setDetailTab("trajectory")}
              value="trajectory"
            >
              {t("records.tabTrajectory")}
            </TabsTrigger>
            <TabsTrigger
              onClick={() => setDetailTab("content")}
              value="content"
            >
              {t("records.tabContent")}
            </TabsTrigger>
            {!isModelDiscoveryProtocol(session.input_protocol) && (
              <TabsTrigger
                value="binding"
                onClick={() => setDetailTab("binding")}
              >
                {t("binding.tab")}
              </TabsTrigger>
            )}
            <TabsTrigger onClick={() => setDetailTab("audit")} value="audit">
              {t("records.tabAudit")}
            </TabsTrigger>
          </TabsList>
          <dl
            data-testid="session-performance"
            className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-micro text-muted-foreground [&>div+div]:before:mr-2 [&>div+div]:before:text-border [&>div+div]:before:content-['·']"
          >
            {detailTab === "conversation" ? (
              <div className="flex items-center">
                <dt className="sr-only">{t("conversation.outline")}</dt>
                <dd>
                  <Button
                    aria-pressed={outlineOpen}
                    className={cn(
                      "h-6 px-1.5 text-micro text-muted-foreground",
                      outlineOpen && "bg-accent text-accent-foreground",
                    )}
                    data-testid="conversation-outline-toggle"
                    onClick={() => setOutlineOpen((current) => !current)}
                    size="xs"
                    type="button"
                    variant="ghost"
                  >
                    {t("conversation.outline")}
                  </Button>
                </dd>
              </div>
            ) : null}
            {discovery ? (
              <div className="flex items-center gap-1.5">
                <dt>{t("records.duration")}</dt>
                <dd className="font-medium tabular-nums text-foreground">
                  <SessionDuration session={session} />
                </dd>
              </div>
            ) : null}
            <div className="flex items-center">
              <dt className="sr-only">{t("records.turns")}</dt>
              <dd className="tabular-nums text-foreground">
                {t("records.turnsCount", { count: session.turn_count })}
              </dd>
            </div>
            <div className="flex items-center">
              <dt className="sr-only">{t("records.calls")}</dt>
              <dd className="tabular-nums text-foreground">
                {t("conversation.calls", { count: session.call_count })}
              </dd>
            </div>
            {failedCalls > 0 ? (
              <div className="flex items-center">
                <dt className="sr-only">{t("status.failed")}</dt>
                <dd
                  className="tabular-nums text-danger-foreground"
                  data-testid="session-failed-calls"
                >
                  {t("conversation.failures", { count: failedCalls })}
                </dd>
              </div>
            ) : null}
            {blockedCalls > 0 ? (
              <div className="flex items-center">
                <dt className="sr-only">{t("status.blocked")}</dt>
                <dd
                  className="tabular-nums text-blocked-foreground"
                  data-testid="session-blocked-calls"
                >
                  {t("conversation.blocked", { count: blockedCalls })}
                </dd>
              </div>
            ) : null}
            {sessionTokens ? (
              <div className="flex items-center">
                <dt className="sr-only">{t("records.totalTokens")}</dt>
                <dd
                  className="tabular-nums text-foreground"
                  title={`${t("records.inputTokens")} ${sessionTokens.input.toLocaleString()} · ${t("records.outputTokens")} ${sessionTokens.output.toLocaleString()}`}
                >
                  {t("conversation.tokens", {
                    input: formatCompactNumber(sessionTokens.input),
                    output: formatCompactNumber(sessionTokens.output),
                  })}
                </dd>
              </div>
            ) : null}
            {!discovery ? (
              <>
                <div className="flex items-center gap-1.5">
                  <dt>{t("records.modelDuration")}</dt>
                  <dd className="tabular-nums text-foreground">
                    <SessionDuration session={session} />
                  </dd>
                </div>
                <div
                  className="flex items-center gap-1.5"
                  title={t("records.toolDurationHint")}
                >
                  <dt>{t("records.toolDuration")}</dt>
                  <dd className="tabular-nums text-foreground">
                    {session.tool_duration_ms == null
                      ? "\u2014"
                      : `\u2248 ${formatDuration(session.tool_duration_ms)}`}
                  </dd>
                </div>
                <div
                  className="flex items-center gap-1.5"
                  title={t("records.averageTTFT")}
                >
                  <dt>{t("records.ttft")}</dt>
                  <dd className="tabular-nums text-foreground">
                    {session.average_ttft_ms == null
                      ? "\u2014"
                      : formatDuration(session.average_ttft_ms)}
                  </dd>
                </div>
                <div className="flex items-center gap-1">
                  <dt className="sr-only">{t("records.outputSpeed")}</dt>
                  <dd className="tabular-nums text-foreground">
                    {session.output_tokens_per_second == null
                      ? "\u2014 tok/s"
                      : `${session.output_tokens_per_second.toFixed(1)} tok/s`}
                  </dd>
                  <HelpPopover label={t("records.performanceStats")}>
                    <div className="space-y-2">
                      <p>{t("records.modelDurationHint")}</p>
                      <p>{t("records.toolDurationHint")}</p>
                      <p>{t("records.averageTTFTHint")}</p>
                      <p>{t("records.outputSpeedHint")}</p>
                      <p>{t("records.performanceMissingHint")}</p>
                    </div>
                  </HelpPopover>
                </div>
              </>
            ) : null}
          </dl>
        </div>

        <TabsContent
          className="mt-0 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
          value="binding"
        >
          <SessionChannelBindings
            key={session.id}
            sessionId={session.id}
            serviceNames={serviceNames}
            onSelectRequest={(id) => {
              onSelectTurn(id);
              setDetailTab("trajectory");
            }}
          />
        </TabsContent>

        {!discovery && (
          <TabsContent
            className="mt-0 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
            value="conversation"
          >
            <RequestConversation
              auditContent={auditContent}
              auditError={auditError}
              auditLoading={auditLoading}
              childrenByRoot={childrenByRoot}
              contentCache={conversationCache}
              copyFeedback={copyFeedback}
              onSelectRequest={onSelectTurn}
              onUnlockRaw={onUnlockRaw}
              outlineOpen={outlineOpen}
              rawSealing={rawSealing}
              selectedRequestId={record.id}
              services={services}
              session={session}
              turns={turns}
            />
          </TabsContent>
        )}

        <TabsContent
          className="mt-0 flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
          value="trajectory"
        >
          <RequestTrajectory
            auditContent={auditContent}
            auditError={auditError}
            auditLoading={auditLoading}
            childrenByRoot={childrenByRoot}
            copyFeedback={copyFeedback}
            onSelectRequest={onSelectTurn}
            onUnlockRaw={onUnlockRaw}
            selectedRequestId={record.id}
            services={services}
            turns={turns}
          />
        </TabsContent>

        <TabsContent
          className="min-h-0 min-w-0 flex-1 space-y-3 overflow-auto overscroll-contain py-4 [scrollbar-gutter:stable]"
          value="overview"
        >
          <DetailSection title={t("records.identity")}>
            <div className="grid grid-cols-4 gap-3 @max-[720px]:grid-cols-2">
              <DetailField
                label={t("records.model")}
                value={
                  <ModelLabel
                    model={record.requested_model}
                    reasoningEffort={record.reasoning_effort}
                    redirectedTo={record.model_redirect?.to}
                  />
                }
              />
              <DetailField
                label={t("records.entry")}
                value={protocolEntryPath(record.input_protocol, {
                  streaming: record.streaming,
                })}
                code
              />
              <DetailField
                label={t("records.protocol")}
                value={record.input_protocol}
                code
              />
              <DetailField
                label={t("records.transport")}
                value={
                  record.streaming
                    ? t("records.streaming")
                    : t("records.notStreaming")
                }
              />
              <DetailField
                label={t("records.service")}
                value={serviceName ?? record.service_id ?? "—"}
              />
              <DetailField
                label={t("records.started")}
                value={formatDateTime(record.started_at)}
              />
              <DetailField
                label={t("records.finished")}
                value={
                  record.completed_at
                    ? formatDateTime(record.completed_at)
                    : "—"
                }
              />
              <div className="col-span-full min-w-0">
                <dt className="text-xs font-medium text-muted-foreground">
                  ID
                </dt>
                <dd className="mt-1 flex min-w-0 items-center gap-2 text-xs text-text-secondary">
                  <code className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
                    {record.id}
                  </code>
                  <Button
                    className="h-auto px-0 text-xs"
                    onClick={() => copyFeedback.copy("record-id", record.id)}
                    type="button"
                    variant="link"
                  >
                    {copyButtonLabel(copyFeedback, "record-id")}
                  </Button>
                </dd>
              </div>
            </div>
          </DetailSection>

          <DetailSection title={t("records.metrics")}>
            <dl className="grid grid-cols-3 @max-[720px]:grid-cols-2 [&>div]:border-l [&>div]:px-3 [&>div:nth-child(3n+1)]:border-l-0 [&>div:nth-child(3n+1)]:pl-0 @max-[720px]:[&>div:nth-child(3n+1)]:border-l @max-[720px]:[&>div:nth-child(3n+1)]:pl-3 @max-[720px]:[&>div:nth-child(odd)]:border-l-0 @max-[720px]:[&>div:nth-child(odd)]:pl-0">
              <Metric label="HTTP" value={record.http_status ?? "—"} />
              <Metric
                label={t("records.latency")}
                value={<RecordLatency record={record} />}
                live={record.status === "pending"}
              />
              <Metric
                label={t("records.inputTokens")}
                value={record.usage?.input_tokens ?? "—"}
              />
              <Metric
                label={t("records.outputTokens")}
                value={record.usage?.output_tokens ?? "—"}
              />
              <Metric
                label={t("records.totalTokens")}
                value={record.usage?.total_tokens ?? "—"}
              />
              <Metric
                label={t("records.cacheRead")}
                value={record.usage?.cache_read_tokens ?? "—"}
              />
              <Metric
                label={t("records.cacheWrite")}
                value={record.usage?.cache_write_tokens ?? "—"}
              />
            </dl>
          </DetailSection>

          <DetailSection title={t("records.privacyRestore")}>
            {record.privacy_restore ? (
              <dl className="grid grid-cols-4 @max-[720px]:grid-cols-2 [&>div]:border-l [&>div]:px-3 [&>div:first-child]:border-l-0 [&>div:first-child]:pl-0">
                <Metric
                  label={t("records.status")}
                  value={
                    record.privacy_restore.enabled
                      ? t("records.on")
                      : t("records.off")
                  }
                />
                <Metric
                  label={t("records.mappings")}
                  value={record.privacy_restore.mapping_count}
                />
                <Metric
                  label={t("records.restored")}
                  value={record.privacy_restore.restored_count}
                />
                <Metric
                  label={t("records.safeFallback")}
                  value={record.privacy_restore.fallback_count}
                />
              </dl>
            ) : (
              <p className="text-xs text-success-foreground">
                {t("records.noPrivacyRestore")}
              </p>
            )}
          </DetailSection>

          {record.recovery || recoveryRecords.length > 1 ? (
            <DetailSection title={t("failure.details")}>
              <RecoveryChain
                records={recoveryRecords}
                serviceNames={serviceNames}
              />
              <RecoveryDetails
                modelRedirect={record.model_redirect}
                value={record.recovery}
              />
            </DetailSection>
          ) : null}

          {record.error ? (
            <DetailSection tone="error" title={t("records.error")}>
              <dl className="grid grid-cols-3 gap-3 @max-[720px]:grid-cols-2">
                <DetailField
                  label={t("records.category")}
                  value={record.error.category}
                  code
                />
                <DetailField
                  label={t("records.code")}
                  value={record.error.code}
                  code
                />
                <DetailField
                  label={t("records.retryable")}
                  value={
                    record.error.retryable ? t("common.yes") : t("common.no")
                  }
                />
                <DetailField
                  className="col-span-full"
                  label={t("records.message")}
                  value={record.error.message}
                  wrap
                />
                {record.error.upstream ? (
                  <DetailField
                    className="col-span-full"
                    label={`${t("records.upstreamError", {
                      status: record.error.upstream.status,
                    })}${
                      record.error.upstream.content_type
                        ? ` · ${record.error.upstream.content_type}`
                        : ""
                    }`}
                    value={
                      <>
                        <pre
                          className="max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-muted p-2 font-mono text-xs leading-relaxed [overflow-wrap:anywhere]"
                          data-testid="record-upstream-error"
                        >
                          {record.error.upstream.body}
                        </pre>
                        {record.error.upstream.truncated ? (
                          <span className="mt-1 block text-muted-foreground">
                            {t("records.upstreamErrorTruncated")}
                          </span>
                        ) : null}
                      </>
                    }
                    wrap
                  />
                ) : null}
              </dl>
            </DetailSection>
          ) : null}

          <DetailSection title={t("records.related")}>
            <dl className="grid grid-cols-3 gap-3 @max-[720px]:grid-cols-2">
              <DetailField
                label={t("records.attempt")}
                value={
                  record.attempt_index === 0
                    ? t("records.neverReachedUpstream")
                    : String(record.attempt_index)
                }
              />
              <DetailField
                label={t("records.parent")}
                value={record.parent_request_id ?? t("records.rootRecord")}
                code
              />
              <DetailField
                label={t("records.retryChildren")}
                value={String(record.child_count)}
              />
              <DetailField
                label={t("records.route")}
                value={record.route_id ?? "—"}
                code
              />
              <DetailField
                label={t("records.service")}
                value={serviceName ?? record.service_id ?? "—"}
              />
              <DetailField
                label={t("records.token")}
                value={record.local_access_token_id ?? "—"}
                code
              />
            </dl>
          </DetailSection>
        </TabsContent>

        <TabsContent
          className="min-h-0 min-w-0 flex-1 space-y-3 overflow-auto overscroll-contain py-4 [scrollbar-gutter:stable]"
          value="content"
        >
          {auditError ? (
            <p className="text-xs text-danger-foreground" role="alert">
              {auditError}
            </p>
          ) : null}
          {auditLoading ? (
            <p className="text-xs text-muted-foreground" role="status">
              {t("records.decrypting")}
            </p>
          ) : auditContent ? (
            <>
              {rawSealing?.unlocked ? (
                <FormMessage
                  className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5"
                  data-slot="raw-unlocked"
                  tone="notice"
                >
                  <span className="min-w-0">
                    {t("rawSealing.unlockedHint", {
                      minutes: unlockIdleMinutes(rawSealing),
                    })}
                  </span>
                  <Button
                    onClick={onLockRaw}
                    size="xs"
                    type="button"
                    variant="outline"
                  >
                    {t("rawSealing.lock")}
                  </Button>
                </FormMessage>
              ) : null}
              {!isChild ? (
                <>
                  <HTTPMetaSection
                    copyFeedback={copyFeedback}
                    meta={auditContent.http_meta}
                    title={t("records.clientHttp")}
                  />
                  <AuditPartSection
                    copyFeedback={copyFeedback}
                    onUnlock={onUnlockRaw}
                    part={requestPart}
                    protocol={record.input_protocol}
                    sectionKey="request-body"
                    title={t("records.clientBody")}
                    withheld={auditContent.withheld.request_body}
                  />
                  <AuditPartSection
                    copyFeedback={copyFeedback}
                    onUnlock={onUnlockRaw}
                    part={responsePart}
                    protocol={record.input_protocol}
                    sectionKey="response-content"
                    title={t("records.clientResponseContent")}
                    withheld={auditContent.withheld.response_content}
                  />
                </>
              ) : null}
              <HTTPMetaSection
                copyFeedback={copyFeedback}
                copyKey="upstream-http-meta"
                meta={auditContent.upstream_http_meta}
                title={t("records.upstreamHttp")}
              />
              <AuditPartSection
                copyFeedback={copyFeedback}
                onUnlock={onUnlockRaw}
                part={upstreamRequestPart}
                protocol={record.input_protocol}
                sectionKey="upstream-request-body"
                title={t("records.upstreamBody")}
                withheld={auditContent.withheld.upstream_request_body}
              />
              <AuditPartSection
                copyFeedback={copyFeedback}
                onUnlock={onUnlockRaw}
                part={upstreamResponsePart}
                protocol={record.input_protocol}
                sectionKey="upstream-response-content"
                title={t("records.upstreamResponseContent")}
                withheld={auditContent.withheld.upstream_response_content}
              />
            </>
          ) : (
            <p className="text-xs text-muted-foreground">
              {t("records.noDecrypted")}
            </p>
          )}
          <div className="flex items-center justify-between gap-3 rounded-lg bg-muted px-3 py-2 text-xs text-muted-foreground">
            <span>{t("records.decryptMemoryOnly")}</span>
            <Button onClick={onClearDecrypted} type="button" variant="outline">
              {t("records.clearDecrypted")}
            </Button>
          </div>
        </TabsContent>

        <TabsContent
          className="min-h-0 min-w-0 flex-1 overflow-auto overscroll-contain py-4 [scrollbar-gutter:stable]"
          value="audit"
        >
          <RequestPrivacyAudit
            auditContent={auditContent}
            auditError={auditError}
            auditLoading={auditLoading}
            onSelectCall={onSelectTurn}
            selectedRequestId={record.id}
            turns={turns}
          />
        </TabsContent>
      </Tabs>
    </section>
  );
}

function DetailSection({
  title,
  tone,
  children,
}: {
  title: string;
  tone?: "error";
  children: ReactNode;
}) {
  return (
    <section
      className={cn(
        "rounded-md border bg-card p-3.5",
        tone === "error" && "border-destructive/25 bg-danger-wash",
      )}
    >
      <h3 className="mb-2.5 text-sm font-semibold">{title}</h3>
      {children}
    </section>
  );
}

function DetailField({
  label,
  value,
  code = false,
  wrap = false,
  className = "",
}: {
  label: string;
  value: ReactNode;
  code?: boolean;
  /** Show the whole value on as many lines as it needs. */
  wrap?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("min-w-0", className)}>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          "mt-1 text-xs text-text-secondary",
          wrap
            ? "whitespace-pre-wrap [overflow-wrap:anywhere]"
            : "overflow-hidden text-ellipsis whitespace-nowrap",
        )}
      >
        {code && typeof value === "string" && value !== "—" ? (
          <code className="text-xs">{value}</code>
        ) : (
          value
        )}
      </dd>
    </div>
  );
}

function Metric({
  label,
  value,
  live = false,
}: {
  label: string;
  value: ReactNode;
  live?: boolean;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-base font-semibold tabular-nums">
        {value}
        {live ? (
          <small className="ml-1 text-micro font-medium text-warning-foreground">
            {t("records.liveBadge")}
          </small>
        ) : null}
      </dd>
    </div>
  );
}

function RecordSkeleton() {
  const t = i18n.t.bind(i18n);
  return (
    <div aria-label={t("records.loading")} className="grid gap-2 p-3">
      {Array.from({ length: 6 }, (_, index) => (
        <div
          className="grid animate-pulse gap-2 rounded-md border p-3"
          key={index}
        >
          <span className="h-3 w-2/3 rounded bg-muted" />
          <span className="h-2 w-1/2 rounded bg-muted" />
        </div>
      ))}
    </div>
  );
}

function PurgeDialog({
  before,
  busy,
  onBeforeChange,
  onCancel,
  onSubmit,
}: {
  before: string;
  busy: boolean;
  onBeforeChange: (value: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent className="max-w-xl sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("records.purgeBeforeOption")}</DialogTitle>
          <DialogDescription>{t("records.purgeBeforeHint")}</DialogDescription>
        </DialogHeader>
        <Input
          aria-label={t("records.purgeBeforeOption")}
          disabled={busy}
          onChange={(event) => onBeforeChange(event.currentTarget.value)}
          type="datetime-local"
          value={before}
        />
        <DialogFooter>
          <Button
            variant="outline"
            disabled={busy}
            onClick={onCancel}
            type="button"
          >
            {t("common.cancel")}
          </Button>
          <Button disabled={busy || !before} onClick={onSubmit} type="button">
            {busy ? t("records.purging") : t("records.runPurge")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function confirmMessage(pending: PendingConfirm): string {
  if (pending.kind === "audit-risk") {
    return i18n.t("records.enableCaptureBody");
  }
  if (pending.kind === "delete") {
    return i18n.t("records.deleteBody");
  }
  if (pending.kind === "delete-sessions") {
    return i18n.t("records.deleteSessionsBody", {
      calls: i18n.t("records.callCount", { count: pending.calls }),
    });
  }
  if (pending.kind === "purge-all") {
    return i18n.t("records.purgeAllBody");
  }
  return i18n.t("records.purgeBeforeBody", {
    time: formatDateTime(pending.before),
  });
}

function diffSettings(
  baseline: AuditSettings,
  draft: AuditSettings,
): AuditSettingsPatch {
  const patch: AuditSettingsPatch = {};
  (Object.keys(baseline) as Array<keyof AuditSettings>).forEach((key) => {
    if (baseline[key] !== draft[key]) {
      Object.assign(patch, { [key]: draft[key] });
    }
  });
  return patch;
}

function serviceLabel(
  serviceId: string | null,
  services: RoutableService[],
): string | null {
  if (!serviceId) return null;
  return services.find((service) => service.id === serviceId)?.name ?? null;
}

// Same output as toLocaleString / toLocaleTimeString with `hour12: false`,
// which build a new formatter per call; the list formats two times per row.
const dateTimeFormats = new Map<string, Intl.DateTimeFormat>();

function formatDate(value: string, withDate: boolean): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const locale = currentLocale();
  const key = `${locale}:${withDate}`;
  let format = dateTimeFormats.get(key);
  if (!format) {
    format = new Intl.DateTimeFormat(locale, {
      ...(withDate
        ? { year: "numeric", month: "numeric", day: "numeric" }
        : {}),
      hour: "numeric",
      minute: "numeric",
      second: "numeric",
      hour12: false,
    });
    dateTimeFormats.set(key, format);
  }
  return format.format(date);
}

function formatDateTime(value: string): string {
  return formatDate(value, true);
}

function formatTime(value: string): string {
  return formatDate(value, false);
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function isControlTransportError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error ?? "");
  return /error sending request|timed out|timeout|connection reset|connection refused|connection closed/i.test(
    message,
  );
}

function listErrorMessage(error: unknown): string {
  if (isControlTransportError(error)) {
    return i18n.t("records.controlUnavailable");
  }
  return messageOf(error, i18n.t("records.readFailed"));
}
