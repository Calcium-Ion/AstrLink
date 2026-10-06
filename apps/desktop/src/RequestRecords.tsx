import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { SessionChannelBindings } from "./SessionChannelBindings";
import { RecoveryChain, RecoveryDetails } from "./components/RecoveryDetails";
import {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  ChevronDown,
  ChevronUp,
  Copy,
  SlidersHorizontal as ListFilter,
  Menu as Ellipsis,
  MessageSquare,
  RefreshCw,
  SlidersHorizontal as Settings2,
  Shredder as Trash2,
} from "@/components/icons";

import { ConfirmDialog as AppConfirmDialog } from "@/components/ConfirmDialog";
import { ClientTypeIcon, clientTypeName } from "@/components/ClientTypeIcon";
import { DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { ScrollWorkspace } from "@/components/ScrollWorkspace";
import { FilterSelect } from "@/components/FilterSelect";
import { ServiceSelect } from "@/components/ServiceSelect";
import { FormMessage } from "@/components/FormMessage";
import { MultiFilterSelect } from "@/components/MultiFilterSelect";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { ModelLabel } from "@/components/ModelLabel";
import { RequestServiceLabel } from "@/components/RequestServiceLabel";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import { formatCompactNumber } from "./format-compact-number";
import { i18n, useT } from "./i18n";
import { useLiveClock } from "./live-clock";
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
} from "./request-live-model";
import {
  CLIENT_TYPES,
  displayRequestStatus,
  holdsRawPart,
  isModelDiscoveryProtocol,
  statusLabel,
  statusTone,
  type AuditContent,
  type AuditContentPart,
  type ClientType,
  type RequestRecord,
  type RequestSession,
  type RequestSessionDetail,
  type RequestSessionKind,
  type SessionStatus,
} from "./request-record-model";
import { RequestConversation } from "./RequestConversation";
import { RequestTrajectory } from "./RequestTrajectory";
import {
  applyQueuedSessions,
  groupSessionsByDate,
  mergeLiveSessions,
  reuseUnchangedSessions,
  sessionMatchesFilters,
} from "./session-live-model";
import { protocolEntryPath } from "./service-presets";

const PAGE_LIMIT = 50;
const POLL_INTERVAL_MS = 1000;
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
    session.tool_duration_ms ?? "",
    session.average_ttft_ms ?? "",
    session.output_tokens_per_second ?? "",
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
  initialLocalAccessTokenId,
  isReady,
  services,
}: {
  accessTokens: AccessTokenSummary[];
  accessTokensReady: boolean;
  coreSessionKey: string | null;
  /** The reading tab a session opens on until the operator picks one. */
  initialDetailTab?: "conversation" | "trajectory";
  initialLocalAccessTokenId?: string;
  isReady: boolean;
  services: (RoutableService & RequestService)[];
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
  const [kind, setKind] = useState<RecordsKind>("inference");
  const [filters, setFilters] = useState<RecordFilters>(() => ({
    ...EMPTY_FILTERS,
    localAccessTokenIds: initialLocalAccessTokenId
      ? [initialLocalAccessTokenId]
      : [],
  }));
  const tokenFilter = () =>
    filters.localAccessTokenIds.length
      ? filters.localAccessTokenIds
      : undefined;
  const localAccessTokenFilterKey = filters.localAccessTokenIds.join("\u0000");
  // Core applies the token filter, so each selection retains its own list.
  const [live, setLive] = useWorkspaceSnapshot<LiveState>(
    `request-list:${coreSessionKey}:${kind}:${localAccessTokenFilterKey}`,
    {
      items: [],
      queued: [],
      nextCursor: null,
    },
  );
  const [listStatus, setListStatus] = useWorkspaceSnapshot<
    "blocked" | "loading" | "ready" | "error"
  >(
    `request-list-status:${coreSessionKey}:${kind}:${localAccessTokenFilterKey}`,
    "blocked",
  );
  const [listError, setListError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [syncWarning, setSyncWarning] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
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
  const [purgeMode, setPurgeMode] = useState<"all" | "before">("all");
  const [purgeBefore, setPurgeBefore] = useState("");
  const [purgeBusy, setPurgeBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);
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
  const pollInFlightRef = useRef(false);
  const pollFailureRef = useRef(0);
  const manualPollRef = useRef<(() => void) | null>(null);
  const atTopRef = useRef(true);
  const viewRef = useRef<RecordsView>("monitor");
  const monitorScrollRef = useRef<HTMLDivElement | null>(null);
  const selectedFocusRef = useRef<string | null>(null);

  viewRef.current = view;

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
    const load = async () => {
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
      }
    };
    void load();
    if (!detailIsLive) {
      return () => {
        cancelled = true;
      };
    }
    const timer = window.setInterval(() => void load(), POLL_INTERVAL_MS);
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

  useEffect(() => {
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    auditGenerationRef.current += 1;
    auditCacheRef.current.clear();
    pollFailureRef.current = 0;
    pollInFlightRef.current = false;
    atTopRef.current = true;
    viewRef.current = "monitor";
    setView("monitor");
    setSelectedId(null);
    setSelectedTurnId(null);
    setOverlaySessions({});
    setOverlayRecords({});
    setAuditContent(null);
    setAuditLoading(false);
    setAuditError(null);
    const initialTokenIds = initialLocalAccessTokenId
      ? [initialLocalAccessTokenId]
      : [];
    setFilters((current) => {
      const sameTokenSelection =
        current.localAccessTokenIds.length === initialTokenIds.length &&
        current.localAccessTokenIds.every(
          (id, index) => id === initialTokenIds[index],
        );
      if (
        sameTokenSelection &&
        current.status === EMPTY_FILTERS.status &&
        current.serviceId === EMPTY_FILTERS.serviceId &&
        current.protocol === EMPTY_FILTERS.protocol &&
        current.clientType === EMPTY_FILTERS.clientType
      ) {
        return current;
      }
      return { ...EMPTY_FILTERS, localAccessTokenIds: initialTokenIds };
    });
    setSettingsOpen(false);
    setSettings(null);
    setSettingsDraft(null);
    setSettingsError(null);
    setSettingsNotice(null);
    setPurgeOpen(false);
    setPendingConfirm(null);
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
  }, [coreSessionKey, initialLocalAccessTokenId, isReady]);

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
    void listRequestSessions({
      limit: PAGE_LIMIT,
      kind: kind === "all" ? undefined : kind,
      local_access_token_ids: tokenFilter(),
    })
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
  }, [coreSessionKey, isReady, kind, localAccessTokenFilterKey]);

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
        const page = await listRequestSessions({
          limit: PAGE_LIMIT,
          kind: kind === "all" ? undefined : kind,
          local_access_token_ids: tokenFilter(),
        });
        if (listGenerationRef.current !== generation) return;
        setLive((current) => {
          const queueNew =
            current.queued.length > 0 ||
            viewRef.current !== "monitor" ||
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
  }, [coreSessionKey, isReady, kind, localAccessTokenFilterKey]);

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
      if (viewRef.current === "detail") returnToMonitor();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  });

  useEffect(
    () => () => {
      auditGenerationRef.current += 1;
      auditCacheRef.current.clear();
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

  // Stable, so the memoized list rows skip the page's re-renders.
  const openDetail = useCallback((sessionId: string) => {
    selectedFocusRef.current = sessionId;
    setSelectedId(sessionId);
    setSelectedTurnId(null);
    viewRef.current = "detail";
    setView("detail");
  }, []);

  const returnToMonitor = () => {
    setViewAndRef("monitor");
    window.requestAnimationFrame(() => {
      const requestId = selectedFocusRef.current;
      if (!requestId) return;
      const escaped =
        typeof CSS !== "undefined" && typeof CSS.escape === "function"
          ? CSS.escape(requestId)
          : requestId.replace(/["\\]/g, "\\$&");
      const target = monitorScrollRef.current?.querySelector(
        `[data-session-id="${escaped}"]`,
      );
      if (target instanceof HTMLButtonElement) {
        target.focus({ preventScroll: true });
      }
    });
  };

  const clearDecrypted = () => {
    auditCacheRef.current.clear();
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
    setError(null);
    try {
      const page = await listRequestSessions({
        limit: PAGE_LIMIT,
        kind: kind === "all" ? undefined : kind,
        local_access_token_ids: tokenFilter(),
        cursor,
      });
      if (listGenerationRef.current !== generation) return;
      setLive((current) => {
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

  const commitDelete = async (requestId: string) => {
    const generation = listGenerationRef.current;
    setDeleting(true);
    setError(null);
    try {
      await deleteRequestRecord(requestId);
      auditCacheRef.current.delete(requestId);
      setOverlayRecords((current) =>
        Object.fromEntries(
          Object.entries(current).filter(
            ([id, record]) =>
              id !== requestId && record.parent_request_id !== requestId,
          ),
        ),
      );
      const page = await listRequestSessions({
        limit: PAGE_LIMIT,
        kind: kind === "all" ? undefined : kind,
        local_access_token_ids: tokenFilter(),
      });
      if (listGenerationRef.current !== generation) return;
      setLive({
        items: page.items,
        queued: [],
        nextCursor: page.next_cursor,
      });
      if (selectedId) {
        setOverlaySessions((current) => {
          const next = { ...current };
          delete next[selectedId];
          return next;
        });
      }
      returnToMonitor();
      notify.success(i18n.t("records.deleted"));
    } catch (requestError: unknown) {
      setError(messageOf(requestError, i18n.t("records.deleteFailed")));
    } finally {
      setDeleting(false);
    }
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
      setPurgeOpen(false);
      notify.success(
        i18n.t("records.purged", {
          records: result.deleted_records,
          blobs: result.deleted_audit_blobs,
        }),
      );
      const page = await listRequestSessions({
        limit: PAGE_LIMIT,
        kind: kind === "all" ? undefined : kind,
        local_access_token_ids: tokenFilter(),
      });
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
    } else if (pending.kind === "purge-all") {
      void commitPurge({ scope: "all" });
    } else {
      void commitPurge({ scope: "before", before: pending.before });
    }
  };

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
                      <>
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
                        <Button
                          variant="ghost"
                          disabled={!isReady}
                          onClick={() => setPurgeOpen(true)}
                          size="sm"
                          type="button"
                        >
                          <Trash2 aria-hidden="true" />
                          {t("records.purgeEllipsis")}
                        </Button>
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
                      </>
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
                <TabsList
                  aria-label={t("records.kind")}
                  className="w-full shrink-0 justify-start border-b"
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
                <div className="flex w-full min-w-0 flex-wrap items-center gap-2 border-b bg-background py-2">
                  <div className="grid min-w-0 flex-1 basis-72 grid-cols-3 gap-2 @[680px]:max-w-4xl @[680px]:grid-cols-5">
                    <FilterSelect
                      ariaLabel={t("records.filter", {
                        label: t("records.status"),
                      })}
                      className="w-full"
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
                    <ServiceSelect
                      ariaLabel={t("records.filter", {
                        label: t("records.providerFilter"),
                      })}
                      className="w-full"
                      label={t("records.providerFilter")}
                      onChange={(serviceId) =>
                        setFilters((current) => ({ ...current, serviceId }))
                      }
                      allLabel={t("common.all")}
                      services={services}
                      value={filters.serviceId}
                    />
                    <FilterSelect
                      ariaLabel={t("records.filter", {
                        label: t("records.protocol"),
                      })}
                      className="w-full"
                      label={t("records.protocol")}
                      onChange={(protocol) =>
                        setFilters((current) => ({ ...current, protocol }))
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
                    <FilterSelect
                      ariaLabel={t("records.filter", {
                        label: t("records.client"),
                      })}
                      className="w-full"
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
                    />
                    <MultiFilterSelect
                      allLabel={t("common.all")}
                      ariaLabel={t("records.filter", {
                        label: t("records.accessToken"),
                      })}
                      className="w-full"
                      clearLabel={t("records.clearTokenFilter")}
                      disabled={!isReady || !accessTokensReady}
                      emptyMessage={t("records.noAccessTokenResults")}
                      label={t("records.accessToken")}
                      onChange={(localAccessTokenIds) =>
                        setFilters((current) => ({
                          ...current,
                          localAccessTokenIds,
                        }))
                      }
                      options={accessTokenOptions}
                      searchPlaceholder={t("records.searchAccessTokens")}
                      selectAllLabel={t("records.selectAllTokens")}
                      selectedCountLabel={(count) =>
                        t("records.selectedTokens", { count })
                      }
                      value={filters.localAccessTokenIds}
                    />
                  </div>
                  <IconButton
                    className="ml-auto"
                    variant="outline"
                    disabled={!isReady || listStatus === "loading"}
                    label={t("common.refresh")}
                    onClick={() => manualPollRef.current?.()}
                    type="button"
                  >
                    <RefreshCw aria-hidden="true" />
                  </IconButton>
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
              ) : listStatus === "loading" && live.items.length === 0 ? (
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
                    filters.status ||
                    filters.serviceId ||
                    filters.protocol ||
                    filters.clientType ||
                    filters.localAccessTokenIds.length ? (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setFilters(EMPTY_FILTERS)}
                      >
                        {" "}
                        {t("records.clearFilters")}
                      </Button>
                    ) : undefined
                  }
                />
              ) : (
                <SessionStream
                  onOpen={openDetail}
                  selectedId={selectedId}
                  services={servicesById}
                  sessions={visibleItems}
                />
              )}

              {live.nextCursor ? (
                <Button
                  className="mx-auto my-3 flex"
                  variant="outline"
                  disabled={loadingMore}
                  onClick={() => void loadMore()}
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
              const next = neighborSession(visibleItems, selectedId, direction);
              if (next) openDetail(next.id);
            }}
            onDelete={() =>
              setPendingConfirm({ kind: "delete", requestId: selected.id })
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
          mode={purgeMode}
          onBeforeChange={setPurgeBefore}
          onCancel={() => setPurgeOpen(false)}
          onModeChange={setPurgeMode}
          onSubmit={() => {
            if (purgeMode === "all") {
              setPendingConfirm({ kind: "purge-all" });
              return;
            }
            if (!purgeBefore) {
              setError(i18n.t("records.needCutoff"));
              return;
            }
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
              : pendingConfirm.kind === "delete"
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
  const nowMs = useLiveClock(session.active_request_starts.length > 0);
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

function SessionStream({
  sessions,
  services,
  selectedId,
  onOpen,
}: {
  sessions: RequestSession[];
  services: RequestServiceMap;
  selectedId: string | null;
  onOpen: (sessionId: string) => void;
}) {
  const t = i18n.t.bind(i18n);
  // Only the date buckets need "now", and those turn over once a day.
  const groups = groupSessionsByDate(sessions);
  let remaining = useProgressiveRowCount(sessions.length);
  return (
    <div className="pb-3" role="feed" aria-label={t("records.sessionFlow")}>
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
                  key={session.id}
                  onOpen={onOpen}
                  selected={session.id === selectedId}
                  services={services}
                  session={session}
                />
              ) : (
                <SessionRow
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

const DiscoveryRow = memo(function DiscoveryRow({
  session,
  services,
  selected,
  onOpen,
}: {
  session: RequestSession;
  services: RequestServiceMap;
  selected: boolean;
  onOpen: (sessionId: string) => void;
}) {
  const t = useT();
  const nowMs = useLiveClock(session.active_request_starts.length > 0);
  const service = requestServiceIdentity(session, services);
  return (
    <DataRow
      asChild
      className="grid grid-cols-[5rem_1.25rem_minmax(0,1fr)_auto] gap-x-3 gap-y-1 px-3 py-2.5 @[680px]:grid-cols-[5rem_1.25rem_minmax(0,1fr)_5rem_6rem]"
    >
      <Button
        aria-current={selected ? "true" : undefined}
        className="h-auto w-full rounded-none bg-transparent text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset aria-[current=true]:bg-accent"
        data-session-id={session.id}
        data-testid="request-session-row"
        onClick={() => onOpen(session.id)}
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
        <ClientTypeIcon
          className="col-start-2 row-start-1 mt-0.5"
          clientType={session.client_type}
        />
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
  onOpen,
}: {
  session: RequestSession;
  services: RequestServiceMap;
  selected: boolean;
  onOpen: (sessionId: string) => void;
}) {
  // Memoized, so it subscribes to the language itself.
  const t = useT();
  // The elapsed time is interpolated into a translated sentence, so the row is
  // the smallest thing that can repaint it. Only active requests tick.
  const nowMs = useLiveClock(session.active_request_starts.length > 0);
  const service = requestServiceIdentity(session, services);
  return (
    <DataRow
      asChild
      className="grid grid-cols-[4.5rem_1.25rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1.5 px-3 py-2.5"
    >
      <Button
        aria-current={selected ? "true" : undefined}
        className="h-auto w-full rounded-none bg-transparent text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset aria-[current=true]:bg-accent"
        data-session-id={session.id}
        data-testid="request-session-row"
        onClick={() => onOpen(session.id)}
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
        <ClientTypeIcon
          className="col-start-2 row-start-1 mt-0.5"
          clientType={session.client_type}
        />
        <strong
          className="col-span-2 col-start-3 row-start-1 min-w-0 truncate text-sm font-medium @[560px]/request-list:col-span-1"
          title={session.title}
        >
          {session.title}
        </strong>
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
        <ModelLabel
          className="col-span-2 col-start-3 row-start-2 text-xs text-text-secondary @[560px]/request-list:col-span-1 @[560px]/request-list:col-start-4 @[560px]/request-list:row-start-1 @[560px]/request-list:max-w-60 @[560px]/request-list:justify-end"
          fallback={t("records.unspecifiedModel")}
          model={session.requested_model}
          reasoningEffort={session.reasoning_effort}
          redirectedTo={session.model_redirect?.to}
        />
      </Button>
    </DataRow>
  );
});

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
  deleting,
  rawSealing,
  initialTab,
  newerAvailable,
  olderAvailable,
  onBack,
  onDelete,
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
  deleting: boolean;
  rawSealing: RawSealingState | null;
  initialTab: "conversation" | "trajectory";
  newerAvailable: boolean;
  olderAvailable: boolean;
  onBack: () => void;
  onDelete: () => void;
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
  const failedCalls = everyRecord.filter((item) => {
    const status = displayRequestStatus(item.status, item.http_status);
    return status === "failed" || status === "blocked";
  }).length;
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
            : session.title
        }
        titleId="request-detail-heading"
        titlePrefix={<ClientTypeIcon clientType={record.client_type} />}
        titleSuffix={
          <>
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
                      : t("common.delete")}
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
        </TabsContent>

        <TabsContent
          className="min-h-0 min-w-0 flex-1 space-y-3 overflow-auto overscroll-contain py-4 [scrollbar-gutter:stable]"
          value="audit"
        >
          <div className="grid grid-cols-2 gap-2.5 @max-[720px]:grid-cols-1">
            {!isChild ? (
              <>
                <AuditSummaryCard
                  captured={record.audit.request_body_captured}
                  label={t("records.clientBody")}
                  part={requestPart}
                  truncated={record.audit.request_body_truncated}
                />
                <AuditSummaryCard
                  captured={record.audit.response_content_captured}
                  label={t("records.clientResponse")}
                  part={responsePart}
                  truncated={record.audit.response_content_truncated}
                />
              </>
            ) : null}
            <AuditSummaryCard
              captured={record.audit.upstream_request_body_captured}
              label={t("records.upstreamBody")}
              part={upstreamRequestPart}
              truncated={record.audit.upstream_request_body_truncated}
            />
            <AuditSummaryCard
              captured={record.audit.upstream_response_content_captured}
              label={t("records.upstreamResponse")}
              part={upstreamResponsePart}
              truncated={record.audit.upstream_response_content_truncated}
            />
          </div>
          <div className="flex items-center justify-between gap-3 rounded-lg bg-muted px-3 py-2 text-xs text-muted-foreground">
            <span>{t("records.decryptMemoryOnly")}</span>
            <Button onClick={onClearDecrypted} type="button" variant="outline">
              {t("records.clearDecrypted")}
            </Button>
          </div>
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

function AuditSummaryCard({
  label,
  captured,
  truncated,
  part,
}: {
  label: string;
  captured: boolean;
  truncated: boolean;
  part: AuditContentPart | null;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <article className="rounded-md border bg-muted p-3">
      <header className="mb-2 flex items-center justify-between gap-2">
        <strong className="text-sm font-medium">{label}</strong>
        <span
          className={cn(
            "text-micro text-muted-foreground",
            captured && "text-success-foreground",
          )}
        >
          {captured ? t("records.captured") : t("records.notCaptured")}
        </span>
      </header>
      <dl className="grid grid-cols-3 gap-2">
        <DetailField
          label={t("records.type")}
          value={part?.media_type ?? "—"}
        />
        <DetailField
          label={t("records.size")}
          value={part ? formatBytes(part.captured_bytes) : "—"}
        />
        <DetailField
          label={t("records.truncated")}
          value={truncated ? t("common.yes") : t("common.no")}
        />
      </dl>
    </article>
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
  mode,
  before,
  busy,
  onModeChange,
  onBeforeChange,
  onCancel,
  onSubmit,
}: {
  mode: "all" | "before";
  before: string;
  busy: boolean;
  onModeChange: (mode: "all" | "before") => void;
  onBeforeChange: (value: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <ModalDialog onCancel={onCancel} title={t("records.purgeTitle")}>
      <RadioGroup
        className="grid gap-2"
        disabled={busy}
        onValueChange={(value) => onModeChange(value as "all" | "before")}
        value={mode}
      >
        <Label className="flex items-center gap-2 rounded-lg border bg-muted px-3 py-2 text-sm">
          <RadioGroupItem value="all" />
          <span>{t("records.purgeAllOption")}</span>
        </Label>
        <Label className="flex items-center gap-2 rounded-lg border bg-muted px-3 py-2 text-sm">
          <RadioGroupItem value="before" />
          <span>{t("records.purgeBeforeOption")}</span>
        </Label>
        {mode === "before" ? (
          <Input
            onChange={(event) => onBeforeChange(event.currentTarget.value)}
            type="datetime-local"
            value={before}
          />
        ) : null}
      </RadioGroup>
      <DialogFooter>
        <Button
          variant="outline"
          disabled={busy}
          onClick={onCancel}
          type="button"
        >
          {t("common.cancel")}
        </Button>
        <Button disabled={busy} onClick={onSubmit} type="button">
          {busy ? t("records.purging") : t("records.runPurge")}
        </Button>
      </DialogFooter>
    </ModalDialog>
  );
}

function ModalDialog({
  title,
  children,
  onCancel,
}: {
  title: string;
  children: ReactNode;
  onCancel: () => void;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent className="max-w-xl sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {t("records.dialogDescription")}
          </DialogDescription>
        </DialogHeader>
        {children}
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

function dateTimeLocale(): string {
  return i18n.language === "zh-CN" ? "zh-CN" : "en";
}

// Same output as toLocaleString / toLocaleTimeString with `hour12: false`,
// which build a new formatter per call; the list formats two times per row.
const dateTimeFormats = new Map<string, Intl.DateTimeFormat>();

function formatDate(value: string, withDate: boolean): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const locale = dateTimeLocale();
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
