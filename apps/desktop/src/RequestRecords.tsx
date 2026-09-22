import { SessionChannelBindings } from "./SessionChannelBindings";
import { RecoveryChain, RecoveryDetails } from "./components/RecoveryDetails";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  ChevronDown,
  Copy,
  SlidersHorizontal as ListFilter,
  MessageSquare,
  RefreshCw,
  SlidersHorizontal as Settings2,
  Shredder as Trash2,
} from "@/components/icons";

import { ConfirmDialog as AppConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { ActionGroup } from "@/components/ActionGroup";
import { ScrollWorkspace } from "@/components/ScrollWorkspace";
import { FilterSelect } from "@/components/FilterSelect";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { Metric as SummaryMetric, MetricGroup } from "@/components/Metric";
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
import { Checkbox } from "@/components/ui/checkbox";
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
import {
  buildRecordBundle,
  bundleFilename,
  type BundleFormat,
} from "./audit-bundle";
import { buildSkillDiagnostic } from "./skill-diagnostic";
import type { AuditSettings, AuditSettingsPatch } from "./audit-settings-model";
import {
  deleteRequestRecord,
  getAuditSettings,
  getRequestAuditContent,
  getRequestSession,
  listRequestRecordChildren,
  listRequestSessions,
  purgeRequestRecords,
  saveTextFile,
  updateAuditSettings,
} from "./bridge";
import { copyButtonLabel, useCopyFeedback } from "./copy-feedback";
import { i18n, useT } from "./i18n";
import { useLiveClock } from "./live-clock";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";
import type { RoutableService } from "./service-model";
import {
  requestServiceIdentity,
  type RequestService,
  type RequestServiceIdentity,
  type RequestServiceMap,
} from "./request-service-model";
import {
  formatDuration,
  liveDurationMs,
  sessionRuntimeMs,
  type RecordFilters,
} from "./request-live-model";
import {
  isModelDiscoveryProtocol,
  statusLabel,
  statusTone,
  type AuditContent,
  type AuditContentPart,
  type RequestRecord,
  type RequestSession,
  type RequestSessionDetail,
  type RequestSessionKind,
  type SessionStatus,
} from "./request-record-model";
import { RequestTrajectory } from "./RequestTrajectory";
import {
  applyQueuedSessions,
  groupSessionsByDate,
  mergeLiveSessions,
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
};

type RecordsView = "monitor" | "detail";
type RecordsKind = RequestSessionKind | "all";
type DetailTab = "trajectory" | "content" | "audit" | "binding";
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
function sessionSummaryKey(session: RequestSession): string {
  return [
    session.id,
    session.status,
    session.requested_model ?? "",
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
      turn.requested_model ?? "",
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
  coreSessionKey,
  isReady,
  services,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
  services: (RoutableService & RequestService)[];
}) {
  const t = i18n.t.bind(i18n);
  const servicesById = useMemo(
    () => Object.fromEntries(services.map((service) => [service.id, service])),
    [services],
  );
  const [view, setView] = useState<RecordsView>("monitor");
  const [kind, setKind] = useState<RecordsKind>("inference");
  const [live, setLive] = useState<LiveState>({
    items: [],
    queued: [],
    nextCursor: null,
  });
  const [filters, setFilters] = useState<RecordFilters>(EMPTY_FILTERS);
  const [listStatus, setListStatus] = useState<
    "blocked" | "loading" | "ready" | "error"
  >("blocked");
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
    setFilters(EMPTY_FILTERS);
    setSettingsOpen(false);
    setSettings(null);
    setSettingsDraft(null);
    setSettingsError(null);
    setSettingsNotice(null);
    setPurgeOpen(false);
    setPendingConfirm(null);
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
  }, [coreSessionKey, isReady]);

  useEffect(() => {
    const generation = ++listGenerationRef.current;
    pollInFlightRef.current = false;
    pollFailureRef.current = 0;
    atTopRef.current = true;
    if (monitorScrollRef.current) monitorScrollRef.current.scrollTop = 0;
    setLive({ items: [], queued: [], nextCursor: null });
    setLoadingMore(false);
    setListError(null);
    setSyncWarning(null);
    setListStatus(isReady ? "loading" : "blocked");
    if (!isReady) return;
    pollInFlightRef.current = true;
    void listRequestSessions({
      limit: PAGE_LIMIT,
      kind: kind === "all" ? undefined : kind,
    })
      .then((page) => {
        if (listGenerationRef.current !== generation) return;
        setLive({
          items: page.items,
          queued: [],
          nextCursor: page.next_cursor,
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
  }, [coreSessionKey, isReady, kind]);

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
  }, [coreSessionKey, isReady, kind]);

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
  // record reaches a terminal status.
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
        if (cacheable) cacheInsert(selected.id, content);
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
  }, [view, selectedId, selected?.id, selectedIsPending, selectedAuditKey]);

  const openDetail = (sessionId: string) => {
    selectedFocusRef.current = sessionId;
    setSelectedId(sessionId);
    setSelectedTurnId(null);
    setViewAndRef("detail");
  };

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

  const toggleBodyCapture = (enabled: boolean) => {
    if (!settings || settingsBusy) return;
    if (enabled) {
      if (settings.request_body_enabled && settings.response_content_enabled) {
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
                          type="button"
                        >
                          <Settings2 aria-hidden="true" />
                          {t("records.auditSettings")}
                        </Button>
                      </>
                    }
                    className="mb-0 flex-wrap"
                    description={t("records.description")}
                    title={t("records.title")}
                    titleId="request-records-heading"
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
                <ActionGroup className="w-full border-b bg-background py-2">
                  <span className="inline-flex items-center gap-1.5 text-micro text-muted-foreground">
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
                  <div className="grid min-w-0 flex-1 basis-72 grid-cols-[minmax(0,0.8fr)_minmax(0,1fr)_minmax(0,1.2fr)] gap-2 @[760px]:max-w-xl">
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
                    <FilterSelect
                      ariaLabel={t("records.filter", {
                        label: t("records.provider"),
                      })}
                      className="w-full"
                      label={t("records.provider")}
                      onChange={(serviceId) =>
                        setFilters((current) => ({ ...current, serviceId }))
                      }
                      options={[
                        { label: t("common.all"), value: "" },
                        ...services.map((service) => ({
                          label: service.name,
                          value: service.id,
                        })),
                      ]}
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
                  </div>
                  <Button
                    variant="outline"
                    disabled={!isReady || listStatus === "loading"}
                    onClick={() => manualPollRef.current?.()}
                    size="sm"
                    type="button"
                  >
                    <RefreshCw aria-hidden="true" />
                    {t("common.refresh")}
                  </Button>
                </ActionGroup>
              </>
            }
          >
            <TabsContent
              value={kind}
              className="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain"
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
                    filters.status || filters.serviceId || filters.protocol ? (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setFilters(EMPTY_FILTERS)}
                      >
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
            deleting={deleting}
            onBack={returnToMonitor}
            onClearDecrypted={clearDecrypted}
            onDelete={() =>
              setPendingConfirm({ kind: "delete", requestId: selected.id })
            }
            onRegisterRecords={(records) =>
              setOverlayRecords((current) => ({ ...current, ...records }))
            }
            onSelectTurn={setSelectedTurnId}
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

      {settingsOpen && pendingConfirm === null ? (
        <SettingsDialog
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
          onSave={saveSettings}
        />
      ) : null}

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

function SessionPerformance({ session }: { session: RequestSession }) {
  const t = useT();
  return (
    <MetricGroup
      aria-label={t("records.performanceStats")}
      className="shrink-0 border-b"
      data-testid="session-performance"
    >
      <SummaryMetric
        size="compact"
        label={
          <span className="flex items-center gap-1">
            {t("records.modelDuration")}
            <HelpPopover label={t("records.performanceStats")}>
              <div className="space-y-2">
                <p>{t("records.modelDurationHint")}</p>
                <p>{t("records.toolDurationHint")}</p>
                <p>{t("records.averageTTFTHint")}</p>
                <p>{t("records.outputSpeedHint")}</p>
                <p>{t("records.performanceMissingHint")}</p>
              </div>
            </HelpPopover>
          </span>
        }
        value={<SessionDuration session={session} />}
      />
      <SummaryMetric
        size="compact"
        label={t("records.toolDuration")}
        title={t("records.toolDurationHint")}
        value={
          session.tool_duration_ms == null
            ? "—"
            : `≈ ${formatDuration(session.tool_duration_ms)}`
        }
      />
      <SummaryMetric
        size="compact"
        label={t("records.averageTTFT")}
        value={
          session.average_ttft_ms == null
            ? "—"
            : formatDuration(session.average_ttft_ms)
        }
      />
      <SummaryMetric
        size="compact"
        label={t("records.outputSpeed")}
        value={
          session.output_tokens_per_second == null
            ? "—"
            : `${session.output_tokens_per_second.toFixed(1)} tok/s`
        }
      />
    </MetricGroup>
  );
}

function RecordLatency({ record }: { record: RequestRecord }) {
  const nowMs = useLiveClock(
    record.latency_ms === null && !record.completed_at,
  );
  return <>{formatDuration(liveDurationMs(record, nowMs))}</>;
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
  return (
    <div className="pb-3" role="feed" aria-label={t("records.sessionFlow")}>
      {groups.map((group) => (
        <section className="mt-3 first:mt-0" key={group.key}>
          <div className="flex items-center gap-2 px-3 pt-4 pb-2 text-xs font-medium text-muted-foreground">
            <span>{group.label}</span>
            <Badge className="font-normal tabular-nums" variant="secondary">
              {t("records.countItems", { count: group.sessions.length })}
            </Badge>
          </div>
          {group.sessions.map((session) =>
            isModelDiscoveryProtocol(session.input_protocol) ? (
              <DiscoveryRow
                key={session.id}
                onOpen={() => onOpen(session.id)}
                selected={session.id === selectedId}
                session={session}
                service={requestServiceIdentity(session, services)}
              />
            ) : (
              <SessionRow
                key={session.id}
                onOpen={() => onOpen(session.id)}
                selected={session.id === selectedId}
                service={requestServiceIdentity(session, services)}
                session={session}
              />
            ),
          )}
        </section>
      ))}
    </div>
  );
}

function DiscoveryRow({
  session,
  service,
  selected,
  onOpen,
}: {
  session: RequestSession;
  service: RequestServiceIdentity;
  selected: boolean;
  onOpen: () => void;
}) {
  const t = useT();
  const nowMs = useLiveClock(session.active_request_starts.length > 0);
  const last = new Date(session.last_started_at);
  return (
    <DataRow
      asChild
      className="grid grid-cols-[5rem_minmax(0,1fr)_auto] gap-x-3 gap-y-1 px-3 py-2.5 @[680px]:grid-cols-[5rem_minmax(0,1fr)_5rem_6rem]"
    >
      <Button
        aria-current={selected ? "true" : undefined}
        className="h-auto w-full rounded-none bg-transparent text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset aria-[current=true]:bg-accent"
        data-session-id={session.id}
        data-testid="request-session-row"
        onClick={onOpen}
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
        <span className="col-start-2 row-start-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
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
        <span className="col-start-2 row-start-2 text-micro tabular-nums text-muted-foreground @[680px]:col-start-3 @[680px]:row-start-1 @[680px]:text-right">
          {formatDuration(sessionRuntimeMs(session, nowMs))}
        </span>
        <time
          className="col-start-3 row-start-1 text-right text-micro tabular-nums text-muted-foreground @[680px]:col-start-4"
          dateTime={session.last_started_at}
          title={formatDateTime(session.last_started_at)}
        >
          {Number.isNaN(last.getTime())
            ? session.last_started_at
            : last.toLocaleTimeString(dateTimeLocale(), { hour12: false })}
        </time>
      </Button>
    </DataRow>
  );
}

function SessionRow({
  session,
  service,
  selected,
  onOpen,
}: {
  session: RequestSession;
  service: RequestServiceIdentity;
  selected: boolean;
  onOpen: () => void;
}) {
  const t = i18n.t.bind(i18n);
  // The elapsed time is interpolated into a translated sentence, so the row is
  // the smallest thing that can repaint it. Only active requests tick.
  const nowMs = useLiveClock(session.active_request_starts.length > 0);
  const last = new Date(session.last_started_at);
  return (
    <DataRow
      asChild
      className="grid grid-cols-[5rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1 px-3 py-3 @[680px]:grid-cols-[5rem_minmax(0,1fr)_15rem]"
    >
      <Button
        aria-current={selected ? "true" : undefined}
        className="h-auto w-full rounded-none bg-transparent text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset aria-[current=true]:bg-accent"
        data-session-id={session.id}
        data-testid="request-session-row"
        onClick={onOpen}
        type="button"
        variant="ghost"
      >
        <StatusBadge
          className="col-start-1 row-start-1 @[680px]:row-span-2 @[680px]:row-start-1"
          tone={
            session.status === "cancelled"
              ? "neutral"
              : statusTone(session.status)
          }
        >
          {statusLabel(session.status)}
        </StatusBadge>
        <strong
          className="col-span-2 col-start-2 row-start-1 min-w-0 truncate text-sm font-medium @[680px]:col-span-1"
          title={session.title}
        >
          {session.title}
        </strong>
        <ModelLabel
          className="col-span-2 col-start-2 row-start-2 text-xs text-text-secondary @[680px]:col-span-1 @[680px]:col-start-3 @[680px]:row-start-1 @[680px]:justify-end"
          fallback={t("records.unspecifiedModel")}
          model={session.requested_model}
          reasoningEffort={session.reasoning_effort}
        />
        <span className="col-span-2 col-start-2 row-start-3 grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 text-xs leading-5 text-muted-foreground @[680px]:row-start-2 @[680px]:grid-cols-[7rem_minmax(0,1fr)_12rem_4.5rem]">
          <code
            className="col-start-1 row-start-2 min-w-0 truncate font-mono text-xs @[680px]:row-start-1"
            title={protocolEntryPath(session.input_protocol)}
          >
            {protocolEntryPath(session.input_protocol)}
          </code>
          <RequestServiceLabel
            className="col-start-1 row-start-1 font-medium text-foreground @[680px]:col-start-2"
            label={
              session.call_count > 1 ? t("records.latestProvider") : undefined
            }
            labelClassName="sr-only @[880px]:not-sr-only @[880px]:w-24"
            service={service}
          />
          <span className="col-start-2 row-start-1 whitespace-nowrap tabular-nums @[680px]:col-start-3">
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
          </span>
          <time
            className="col-start-2 row-start-2 text-right tabular-nums @[680px]:col-start-4 @[680px]:row-start-1"
            dateTime={session.last_started_at}
            title={formatDateTime(session.last_started_at)}
          >
            {Number.isNaN(last.getTime())
              ? session.last_started_at
              : last.toLocaleTimeString(dateTimeLocale(), { hour12: false })}
          </time>
        </span>
      </Button>
    </DataRow>
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
  deleting,
  onBack,
  onDelete,
  onClearDecrypted,
  onSelectTurn,
  onRegisterRecords,
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
  deleting: boolean;
  onBack: () => void;
  onDelete: () => void;
  onClearDecrypted: () => void;
  onSelectTurn: (requestId: string) => void;
  onRegisterRecords: (records: Record<string, RequestRecord>) => void;
}) {
  const t = useT();
  const copyFeedback = useCopyFeedback();
  const [bundleSize, setBundleSize] = useState<number | null>(null);
  const [detailTab, setDetailTab] = useState<DetailTab>("trajectory");
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
    setDetailTab("trajectory");
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

  const copyBundle = (includeBodies: boolean) => {
    const bundle = buildRecordBundle(record, auditContent, {
      includeBodies,
      serviceLabel: serviceName,
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
      includeBodies: true,
      serviceLabel: serviceName,
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
        titleSuffix={
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
        actionsClassName="flex-wrap justify-end gap-1.5"
        actions={
          <>
            {copyFeedback.activeKey === "bundle-meta" ? (
              <span className="text-micro text-muted-foreground" role="status">
                {copyButtonLabel(copyFeedback, "bundle-meta")}
              </span>
            ) : null}
            <IconButton
              className="text-danger-foreground hover:bg-danger-wash hover:text-danger-foreground"
              disabled={deleting || record.status === "pending"}
              label={
                record.status === "pending"
                  ? t("records.deletePending")
                  : deleting
                    ? t("records.deleting")
                    : t("common.delete")
              }
              onClick={onDelete}
              type="button"
            >
              <Trash2 aria-hidden="true" />
            </IconButton>
            <Button
              data-testid="copy-skill-diagnostic"
              onClick={copySkillDiagnostic}
              size="sm"
              title={t("records.copySkillDiagnosticHint")}
              type="button"
              variant="outline"
            >
              <Copy aria-hidden="true" />
              {copyButtonLabel(
                copyFeedback,
                "skill-diagnostic",
                t("records.copySkillDiagnostic"),
              )}
            </Button>
            <div className="inline-flex">
              <Button
                className="rounded-r-none"
                onClick={() => copyBundle(true)}
                size="sm"
                type="button"
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
                    className="rounded-l-none border-l border-primary-foreground/20 px-1.5"
                    size="sm"
                    type="button"
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
          </>
        }
      />

      <Tabs
        className="flex min-h-0 min-w-0 flex-1 flex-col gap-0"
        onValueChange={(value) => setDetailTab(value as DetailTab)}
        value={detailTab}
      >
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b py-1.5">
          <TabsList aria-label={t("records.sections")} className="h-8">
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
          <dl className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-micro text-muted-foreground">
            <div className="flex min-w-0 max-w-64 items-center gap-1.5">
              <dt className="sr-only">{t("records.provider")}</dt>
              <dd className="min-w-0 text-foreground">
                <RequestServiceLabel
                  service={requestServiceIdentity(record, services)}
                />
              </dd>
            </div>
            {isModelDiscoveryProtocol(session.input_protocol) && (
              <div className="flex items-center gap-1.5">
                <dt>{t("records.duration")}</dt>
                <dd className="font-medium tabular-nums text-foreground">
                  <SessionDuration session={session} />
                </dd>
              </div>
            )}
            <div className="flex items-center gap-1.5">
              <dt>{t("records.turns")}</dt>
              <dd className="font-medium tabular-nums text-foreground">
                {session.turn_count}
              </dd>
            </div>
            <div className="flex items-center gap-1.5">
              <dt>{t("records.calls")}</dt>
              <dd className="font-medium tabular-nums text-foreground">
                {session.call_count}
              </dd>
            </div>
          </dl>
        </div>

        {!isModelDiscoveryProtocol(session.input_protocol) && (
          <SessionPerformance session={session} />
        )}

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
            selectedRequestId={record.id}
            services={services}
            turns={turns}
          />
        </TabsContent>

        <TabsContent
          className="min-h-0 min-w-0 flex-1 space-y-3 overflow-auto overscroll-contain py-4"
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
              <RecoveryDetails value={record.recovery} />
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
                />
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
          className="min-h-0 min-w-0 flex-1 space-y-3 overflow-auto overscroll-contain py-4"
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
              {!isChild ? (
                <>
                  <HTTPMetaSection
                    copyFeedback={copyFeedback}
                    meta={auditContent.http_meta}
                    title={t("records.clientHttp")}
                  />
                  <AuditPartSection
                    copyFeedback={copyFeedback}
                    part={requestPart}
                    protocol={record.input_protocol}
                    sectionKey="request-body"
                    title={t("records.clientBody")}
                  />
                  <AuditPartSection
                    copyFeedback={copyFeedback}
                    part={responsePart}
                    protocol={record.input_protocol}
                    sectionKey="response-content"
                    title={t("records.clientResponseContent")}
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
                part={upstreamRequestPart}
                protocol={record.input_protocol}
                sectionKey="upstream-request-body"
                title={t("records.upstreamBody")}
              />
              <AuditPartSection
                copyFeedback={copyFeedback}
                part={upstreamResponsePart}
                protocol={record.input_protocol}
                sectionKey="upstream-response-content"
                title={t("records.upstreamResponseContent")}
              />
            </>
          ) : (
            <p className="text-xs text-muted-foreground">
              {t("records.noDecrypted")}
            </p>
          )}
        </TabsContent>

        <TabsContent
          className="min-h-0 min-w-0 flex-1 space-y-3 overflow-auto overscroll-contain py-4"
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
  className = "",
}: {
  label: string;
  value: ReactNode;
  code?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("min-w-0", className)}>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 overflow-hidden text-xs text-text-secondary text-ellipsis whitespace-nowrap">
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

function SettingsDialog({
  draft,
  busy,
  error,
  notice,
  onChange,
  onCancel,
  onSave,
}: {
  draft: AuditSettings | null;
  busy: boolean;
  error: string | null;
  notice: string | null;
  onChange: <K extends keyof AuditSettings>(
    key: K,
    value: AuditSettings[K],
  ) => void;
  onCancel: () => void;
  onSave: () => void;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <ModalDialog onCancel={onCancel} title={t("records.auditSettings")}>
      {draft ? (
        <div className="grid grid-cols-2 gap-3 max-[600px]:grid-cols-1">
          <CheckField
            checked={draft.http_meta_enabled}
            label={t("records.httpMeta")}
            onChange={(value) => onChange("http_meta_enabled", value)}
          />
          <NumberField
            label={t("records.requestLimit")}
            max={16_777_216}
            min={1024}
            onChange={(value) => onChange("request_body_max_bytes", value)}
            value={draft.request_body_max_bytes}
          />
          <NumberField
            label={t("records.responseLimit")}
            max={67_108_864}
            min={1024}
            onChange={(value) => onChange("response_content_max_bytes", value)}
            value={draft.response_content_max_bytes}
          />
          <NumberField
            label={t("records.metaRetention")}
            max={3650}
            min={1}
            onChange={(value) => onChange("metadata_retention_days", value)}
            value={draft.metadata_retention_days}
          />
          <NumberField
            label={t("records.contentRetention")}
            max={365}
            min={1}
            onChange={(value) => onChange("content_retention_days", value)}
            value={draft.content_retention_days}
          />
          <FormMessage className="col-span-full" tone="notice">
            {t("records.settingsHint")}
          </FormMessage>
        </div>
      ) : busy ? (
        <p>{t("common.loading")}</p>
      ) : null}
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {notice ? <FormMessage tone="success">{notice}</FormMessage> : null}
      <DialogFooter>
        <Button
          variant="outline"
          disabled={busy}
          onClick={onCancel}
          type="button"
        >
          {t("common.close")}
        </Button>
        <Button disabled={busy || !draft} onClick={onSave} type="button">
          {busy ? t("common.saving") : t("common.save")}
        </Button>
      </DialogFooter>
    </ModalDialog>
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

function CheckField({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <Label className="flex items-start gap-2 rounded-lg border bg-muted px-3 py-2 text-xs leading-5">
      <Checkbox
        aria-label={label}
        checked={checked}
        onCheckedChange={(value) => onChange(value === true)}
      />
      <span>{label}</span>
    </Label>
  );
}

function NumberField({
  label,
  value,
  min,
  max,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  onChange: (value: number) => void;
}) {
  return (
    <Label className="grid items-stretch gap-1.5 text-xs font-semibold text-text-secondary">
      <span>{label}</span>
      <Input
        max={max}
        min={min}
        onChange={(event) => onChange(Number(event.currentTarget.value))}
        type="number"
        value={value}
      />
    </Label>
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

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(dateTimeLocale(), { hour12: false });
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
