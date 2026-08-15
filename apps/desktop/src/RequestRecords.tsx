import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { ConfirmDialog as AppConfirmDialog } from "@/components/ConfirmDialog";
import { FormMessage } from "@/components/FormMessage";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

import { AuditPartSection, HTTPMetaSection } from "./AuditReviewer";
import { buildRecordBundle } from "./audit-bundle";
import type { AuditSettings, AuditSettingsPatch } from "./audit-settings-model";
import {
  deleteRequestRecord,
  getAuditSettings,
  getRequestAuditContent,
  getRequestRecord,
  listRequestRecordChildren,
  listRequestRecords,
  purgeRequestRecords,
  updateAuditSettings,
} from "./bridge";
import { copyButtonLabel, useCopyFeedback, type CopyFeedback } from "./copy-feedback";
import { PageHeader } from "./PageHeader";
import type { RoutableService } from "./service-model";
import {
  applyQueuedRecords,
  formatDuration,
  groupRecordsByDate,
  liveDurationMs,
  mergeLivePage,
  recordMatchesFilters,
  type RecordFilters,
} from "./request-live-model";
import {
  statusLabel,
  statusTone,
  type AuditContent,
  type AuditContentPart,
  type RequestRecord,
  type RequestStatus,
} from "./request-record-model";

const PAGE_LIMIT = 50;
const POLL_INTERVAL_MS = 1000;
const AUDIT_CACHE_MAX_RECORDS = 5;
const AUDIT_CACHE_MAX_BYTES = 32 * 1024 * 1024;
const STATUSES: RequestStatus[] = [
  "pending",
  "succeeded",
  "failed",
  "cancelled",
  "blocked",
];
const EMPTY_FILTERS: RecordFilters = {
  status: "",
  serviceId: "",
  protocol: "",
};

type RecordsView = "monitor" | "detail";
type PendingConfirm =
  | { kind: "audit-risk"; patch: AuditSettingsPatch }
  | { kind: "delete"; requestId: string }
  | { kind: "purge-all" }
  | { kind: "purge-before"; before: string };

interface LiveState {
  items: RequestRecord[];
  queued: RequestRecord[];
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

export function RequestRecords({
  coreSessionKey,
  isReady,
  services,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
  services: RoutableService[];
}) {
  const [view, setView] = useState<RecordsView>("monitor");
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
  const [overlayRecords, setOverlayRecords] = useState<
    Record<string, RequestRecord>
  >({});
  const [navigationIds, setNavigationIds] = useState<string[]>([]);
  const [auditContent, setAuditContent] = useState<AuditContent | null>(null);
  const [auditLoading, setAuditLoading] = useState(false);
  const [auditError, setAuditError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [nowMs, setNowMs] = useState(() => Date.now());

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
    () => live.items.filter((record) => recordMatchesFilters(record, filters)),
    [filters, live.items],
  );
  const queuedVisibleCount = useMemo(
    () =>
      live.queued.filter((record) => recordMatchesFilters(record, filters))
        .length,
    [filters, live.queued],
  );
  const selected = useMemo(() => {
    if (!selectedId) return null;
    return (
      allRecords.find((record) => record.id === selectedId) ??
      overlayRecords[selectedId] ??
      null
    );
  }, [allRecords, overlayRecords, selectedId]);

  useEffect(() => {
    if (!selectedId || selected) return;
    let cancelled = false;
    void getRequestRecord(selectedId)
      .then((record) => {
        if (cancelled) return;
        setOverlayRecords((current) => ({ ...current, [record.id]: record }));
      })
      .catch(() => {
        /* detail view shows missing via selected === null */
      });
    return () => {
      cancelled = true;
    };
  }, [selectedId, selected]);
  const protocolOptions = useMemo(() => {
    const protocols = new Set<string>();
    services.forEach((service) =>
      service.capabilities.forEach((capability) =>
        protocols.add(capability.protocol),
      ),
    );
    allRecords.forEach((record) => protocols.add(record.input_protocol));
    return [...protocols].sort();
  }, [allRecords, services]);

  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

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
    setOverlayRecords({});
    setNavigationIds([]);
    setAuditContent(null);
    setAuditLoading(false);
    setAuditError(null);
    setFilters(EMPTY_FILTERS);
    setSettingsOpen(false);
    setPurgeOpen(false);
    setPendingConfirm(null);
    setNotice(null);
    setError(null);
    setSyncWarning(null);

    if (!isReady) {
      setLive({ items: [], queued: [], nextCursor: null });
      setListStatus("blocked");
      setListError(null);
      return;
    }
    setLive({ items: [], queued: [], nextCursor: null });
    setListStatus("loading");
    setListError(null);
    void listRequestRecords({ limit: PAGE_LIMIT })
      .then((page) => {
        if (generationRef.current !== generation) return;
        setLive({
          items: page.items,
          queued: [],
          nextCursor: page.next_cursor,
        });
        setListStatus("ready");
      })
      .catch((requestError: unknown) => {
        if (generationRef.current !== generation) return;
        setListStatus("error");
        setListError(messageOf(requestError, "无法读取请求记录。"));
      });
  }, [coreSessionKey, isReady]);

  useEffect(() => {
    if (!isReady) return;
    const generation = generationRef.current;
    const poll = async (manual = false) => {
      if (
        pollInFlightRef.current ||
        (!manual && document.visibilityState === "hidden")
      ) {
        return;
      }
      pollInFlightRef.current = true;
      try {
        const page = await listRequestRecords({ limit: PAGE_LIMIT });
        if (generationRef.current !== generation) return;
        setLive((current) => {
          const queueNew =
            current.queued.length > 0 ||
            viewRef.current !== "monitor" ||
            !atTopRef.current;
          const merged = mergeLivePage(
            current.items,
            current.queued,
            page.items,
            queueNew,
          );
          return {
            items: merged.items,
            queued: merged.queued,
            nextCursor:
              current.items.length === 0
                ? page.next_cursor
                : current.nextCursor,
          };
        });
        pollFailureRef.current = 0;
        setSyncWarning(null);
        setListStatus("ready");
        if (manual) setNotice("已同步最新记录。");
      } catch (requestError: unknown) {
        if (generationRef.current !== generation) return;
        pollFailureRef.current += 1;
        if (manual) {
          setError(messageOf(requestError, "无法刷新请求记录。"));
        }
        if (pollFailureRef.current >= 3) {
          setSyncWarning("实时同步暂时中断，正在保留当前记录并继续重试。");
        }
      } finally {
        pollInFlightRef.current = false;
      }
    };
    const timer = window.setInterval(() => void poll(), POLL_INTERVAL_MS);
    manualPollRef.current = () => {
      setError(null);
      setNotice(null);
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
  }, [coreSessionKey, isReady]);

  const selectedIndex = selectedId ? navigationIds.indexOf(selectedId) : -1;
  const previousId =
    selectedIndex > 0 ? navigationIds[selectedIndex - 1] : null;
  const nextId =
    selectedIndex >= 0 && selectedIndex < navigationIds.length - 1
      ? navigationIds[selectedIndex + 1]
      : null;

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
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
        return;
      }
      if (
        viewRef.current !== "detail" ||
        pendingConfirm ||
        settingsOpen ||
        purgeOpen
      ) {
        return;
      }
      const target = event.target;
      if (
        target instanceof HTMLElement &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable)
      ) {
        return;
      }
      if (event.key === "[" && previousId) selectFromSnapshot(previousId);
      if (event.key === "]" && nextId) selectFromSnapshot(nextId);
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
  // counter drops stale responses when the user pages quickly with
  // 上一条/下一条. Pending records are never cached so the content refreshes
  // once the record reaches a terminal status.
  const selectedIsPending = selected?.status === "pending";
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
        const message = messageOf(requestError, "无法读取审计内容。");
        setAuditError(
          message.includes("409")
            ? "审计密钥缺失或损坏，无法解密该记录。"
            : message,
        );
      })
      .finally(() => {
        if (auditGenerationRef.current === generation) setAuditLoading(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, selectedId, selectedIsPending]);

  const openDetail = (requestId: string) => {
    selectedFocusRef.current = requestId;
    setNavigationIds(visibleItems.map((record) => record.id));
    setSelectedId(requestId);
    setViewAndRef("detail");
  };

  const selectFromSnapshot = (requestId: string) => {
    if (requestId === selectedId) return;
    selectedFocusRef.current = requestId;
    setSelectedId(requestId);
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
        `[data-record-id="${escaped}"]`,
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
    setNotice("已清除内存中的解密内容。");
    returnToMonitor();
  };

  const applyQueue = () => {
    setLive((current) => ({
      ...current,
      items: applyQueuedRecords(current.items, current.queued),
      queued: [],
    }));
    atTopRef.current = true;
    monitorScrollRef.current?.scrollTo({ top: 0, behavior: "smooth" });
  };

  const loadMore = async () => {
    if (!live.nextCursor || loadingMore) return;
    const generation = generationRef.current;
    const cursor = live.nextCursor;
    setLoadingMore(true);
    setError(null);
    try {
      const page = await listRequestRecords({
        limit: PAGE_LIMIT,
        cursor,
      });
      if (generationRef.current !== generation) return;
      setLive((current) => {
        const known = new Set(
          [...current.items, ...current.queued].map((record) => record.id),
        );
        return {
          ...current,
          items: [
            ...current.items,
            ...page.items.filter((record) => !known.has(record.id)),
          ],
          nextCursor: page.next_cursor,
        };
      });
    } catch (requestError: unknown) {
      setError(messageOf(requestError, "无法加载更多请求记录。"));
    } finally {
      setLoadingMore(false);
    }
  };

  const commitDelete = async (requestId: string) => {
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
      setLive((current) => ({
        ...current,
        items: current.items.filter((record) => record.id !== requestId),
        queued: current.queued.filter((record) => record.id !== requestId),
      }));
      returnToMonitor();
      setNotice("已删除该记录。");
    } catch (requestError: unknown) {
      setError(messageOf(requestError, "无法删除请求记录。"));
    } finally {
      setDeleting(false);
    }
  };

  const commitPurge = async (
    input: { scope: "all" } | { scope: "before"; before: string },
  ) => {
    setPurgeBusy(true);
    setError(null);
    try {
      const result = await purgeRequestRecords(input);
      auditCacheRef.current.clear();
      setPurgeOpen(false);
      setNotice(
        `已删除 ${result.deleted_records} 条记录、${result.deleted_audit_blobs} 个加密内容块。`,
      );
      const page = await listRequestRecords({ limit: PAGE_LIMIT });
      setLive({
        items: page.items,
        queued: [],
        nextCursor: page.next_cursor,
      });
    } catch (requestError: unknown) {
      setError(messageOf(requestError, "无法清理请求记录。"));
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
      setSettingsError(messageOf(requestError, "无法读取审计设置。"));
    } finally {
      setSettingsBusy(false);
    }
  };

  const commitSettings = async (patch: AuditSettingsPatch) => {
    setSettingsBusy(true);
    setSettingsError(null);
    setSettingsNotice(null);
    try {
      const updated = await updateAuditSettings(patch);
      setSettings({ ...updated });
      setSettingsDraft({ ...updated });
      setSettingsNotice("审计设置已保存。");
    } catch (requestError: unknown) {
      setSettingsError(messageOf(requestError, "无法保存审计设置。"));
    } finally {
      setSettingsBusy(false);
    }
  };

  const saveSettings = () => {
    if (!settings || !settingsDraft) {
      setSettingsError("审计设置尚未加载完成。");
      return;
    }
    const patch = diffSettings(settings, settingsDraft);
    if (Object.keys(patch).length === 0) {
      setSettingsNotice("没有需要保存的更改。");
      return;
    }
    const enabling =
      (patch.request_body_enabled === true &&
        !settings.request_body_enabled) ||
      (patch.response_content_enabled === true &&
        !settings.response_content_enabled);
    if (enabling) {
      setPendingConfirm({
        kind: "audit-risk",
        patch: { ...patch, audit_risk_acknowledged: true },
      });
      return;
    }
    void commitSettings(patch);
  };

  const resolveConfirm = () => {
    const pending = pendingConfirm;
    setPendingConfirm(null);
    if (!pending) return;
    if (pending.kind === "audit-risk") {
      void commitSettings(pending.patch);
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
      {view === "monitor" ? (
        <PageHeader
          actions={
            <>
              <span className="mr-[3px] inline-flex items-center gap-[7px] text-xs font-semibold text-muted-foreground max-[720px]:mr-auto">
                <StatusDot tone="positive" />
                每秒同步
              </span>
              <Button
                variant="outline"
                disabled={!isReady}
                onClick={() => setPurgeOpen(true)}
                type="button"
              >
                清理…
              </Button>
              <Button
                variant="outline"
                disabled={!isReady}
                onClick={() => void openSettings()}
                type="button"
              >
                审计设置
              </Button>
            </>
          }
          description="已认证的推理请求会在开始后立即进入日志流。"
          eyebrow="实时监控"
          title="请求记录"
          titleId="request-records-heading"
        />
      ) : null}
      <div className="flex h-full min-h-0 min-w-0 flex-1">
        <section
          aria-labelledby="request-records-heading"
          className="flex h-full min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-lg border bg-card"
          hidden={view !== "monitor"}
        >
          {notice ? (
            <FormMessage className="mx-[22px] mt-2.5 shrink-0" tone="success">
              {notice}
            </FormMessage>
          ) : null}
          {error ? (
            <FormMessage className="mx-[22px] mt-2.5 shrink-0" tone="error">
              {error}
            </FormMessage>
          ) : null}
          {syncWarning ? (
            <FormMessage className="mx-[22px] mt-2.5 flex shrink-0 items-center gap-2" tone="warning">
              <StatusDot tone="pending" />
              {syncWarning}
            </FormMessage>
          ) : null}

          <div
            className="min-h-0 min-w-0 flex-1 overflow-auto overscroll-contain"
            data-testid="request-records-scroll"
            onScroll={(event) => {
              atTopRef.current = event.currentTarget.scrollTop <= 8;
            }}
            ref={monitorScrollRef}
          >
            <div className="sticky top-0 z-7 flex items-end justify-between border-b bg-card px-4 py-3 @max-[720px]:items-stretch @max-[720px]:flex-col @max-[720px]:gap-2">
              <div className="flex items-end gap-2 @max-[720px]:grid @max-[720px]:grid-cols-2">
                <FilterSelect
                  label="状态"
                  onChange={(status) =>
                    setFilters((current) => ({
                      ...current,
                      status: status as RequestStatus | "",
                    }))
                  }
                  options={[
                    { label: "全部", value: "" },
                    ...STATUSES.map((status) => ({
                      label: statusLabel(status),
                      value: status,
                    })),
                  ]}
                  value={filters.status}
                />
                <FilterSelect
                  label="服务"
                  onChange={(serviceId) =>
                    setFilters((current) => ({ ...current, serviceId }))
                  }
                  options={[
                    { label: "全部", value: "" },
                    ...services.map((service) => ({
                      label: service.name,
                      value: service.id,
                    })),
                  ]}
                  value={filters.serviceId}
                />
                <FilterSelect
                  label="协议"
                  onChange={(protocol) =>
                    setFilters((current) => ({ ...current, protocol }))
                  }
                  options={[
                    { label: "全部", value: "" },
                    ...protocolOptions.map((protocol) => ({
                      label: protocol,
                      value: protocol,
                    })),
                  ]}
                  value={filters.protocol}
                />
              </div>
              <Button
                className="@max-[720px]:self-end"
                variant="outline"
                disabled={!isReady || pollInFlightRef.current}
                onClick={() => manualPollRef.current?.()}
                type="button"
              >
                刷新
              </Button>
            </div>

            {queuedVisibleCount > 0 ? (
              <Button
                className="sticky top-[88px] z-6 mx-auto mt-2 flex shadow-md @max-[720px]:top-[155px]"
                onClick={applyQueue}
                type="button"
              >
                ↑ {queuedVisibleCount} 条新记录
              </Button>
            ) : null}

            {!isReady || listStatus === "blocked" ? (
              <div className="flex min-h-[280px] flex-col items-center justify-center p-8 text-center">
                <strong className="text-xs">等待 Core 就绪</strong>
                <span className="mt-1.5 text-xs text-muted-foreground">连接成功后，请求会自动出现在这里。</span>
              </div>
            ) : listStatus === "error" && listError ? (
              <div className="flex min-h-[280px] flex-col items-center justify-center p-8 text-center">
                <strong className="text-xs">无法读取请求记录</strong>
                <span className="mt-1.5 text-xs text-muted-foreground">{listError}</span>
              </div>
            ) : listStatus === "loading" && live.items.length === 0 ? (
              <RecordSkeleton />
            ) : visibleItems.length === 0 ? (
              <div className="flex min-h-[280px] flex-col items-center justify-center p-8 text-center">
                <strong className="text-xs">没有匹配的请求</strong>
                <span className="mt-1.5 text-xs text-muted-foreground">调整筛选条件，或发起一次新的推理请求。</span>
              </div>
            ) : (
              <RecordStream
                services={services}
                nowMs={nowMs}
                onOpen={openDetail}
                records={visibleItems}
                selectedId={selectedId}
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
                {loadingMore ? "加载中…" : "加载更早记录"}
              </Button>
            ) : null}
          </div>
        </section>

        {view === "detail" && selected ? (
          <RecordDetail
            auditContent={auditContent}
            auditError={auditError}
            auditLoading={auditLoading}
            deleting={deleting}
            serviceName={serviceLabel(selected.service_id, services)}
            index={selectedIndex}
            navigationCount={navigationIds.length}
            nextId={nextId}
            nowMs={nowMs}
            onBack={returnToMonitor}
            onClearDecrypted={clearDecrypted}
            onDelete={() =>
              setPendingConfirm({ kind: "delete", requestId: selected.id })
            }
            onNext={() => nextId && selectFromSnapshot(nextId)}
            onPrevious={() => previousId && selectFromSnapshot(previousId)}
            previousId={previousId}
            record={selected}
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
              setError("请选择清除截止时间。");
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
              ? "确认开启"
              : pendingConfirm.kind === "delete"
                ? "确定删除"
                : "确定清理"
          }
          description={<p>{confirmMessage(pendingConfirm)}</p>}
          destructive={pendingConfirm.kind !== "audit-risk"}
          disabled={settingsBusy || deleting || purgeBusy}
          onCancel={() => {
            if (pendingConfirm.kind === "audit-risk") {
              setSettingsNotice("已取消开启正文捕获。");
            }
            setPendingConfirm(null);
          }}
          onConfirm={resolveConfirm}
          open
          title={
            pendingConfirm.kind === "audit-risk"
              ? "确认开启正文捕获"
              : pendingConfirm.kind === "delete"
                ? "删除请求记录"
                : "清理请求记录"
          }
        />
      ) : null}
    </>
  );
}

function RecordStream({
  records,
  services,
  selectedId,
  nowMs,
  onOpen,
}: {
  records: RequestRecord[];
  services: RoutableService[];
  selectedId: string | null;
  nowMs: number;
  onOpen: (requestId: string) => void;
}) {
  const [expandedRoots, setExpandedRoots] = useState<Set<string>>(() => new Set());
  const [childrenByRoot, setChildrenByRoot] = useState<
    Record<string, RequestRecord[]>
  >({});
  const [childrenLoading, setChildrenLoading] = useState<Set<string>>(
    () => new Set(),
  );
  const fetchedRootsRef = useRef<Set<string>>(new Set());
  const groups = groupRecordsByDate(records, new Date(nowMs));

  const loadChildren = (rootId: string, force = false) => {
    if (!force && fetchedRootsRef.current.has(rootId)) return;
    if (childrenLoading.has(rootId)) return;
    setChildrenLoading((current) => new Set(current).add(rootId));
    void listRequestRecordChildren(rootId)
      .then((page) => {
        fetchedRootsRef.current.add(rootId);
        setChildrenByRoot((current) => ({
          ...current,
          [rootId]: page.items,
        }));
      })
      .catch(() => {
        fetchedRootsRef.current.add(rootId);
        setChildrenByRoot((current) => ({
          ...current,
          [rootId]: current[rootId] ?? [],
        }));
      })
      .finally(() => {
        setChildrenLoading((current) => {
          const next = new Set(current);
          next.delete(rootId);
          return next;
        });
      });
  };

  const toggleRoot = (rootId: string, childCount: number) => {
    setExpandedRoots((current) => {
      const next = new Set(current);
      if (next.has(rootId)) {
        next.delete(rootId);
        return next;
      }
      next.add(rootId);
      return next;
    });
    if (childCount > 0) {
      loadChildren(rootId);
    }
  };

  // Refresh expanded groups when live polling changes child_count.
  useEffect(() => {
    for (const root of records) {
      if (!expandedRoots.has(root.id) || root.child_count <= 0) continue;
      const cached = childrenByRoot[root.id];
      if (cached && cached.length === root.child_count) continue;
      fetchedRootsRef.current.delete(root.id);
      loadChildren(root.id, true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [records, expandedRoots]);

  return (
    <div className="px-3 pb-3" role="feed" aria-label="实时请求流">
      {groups.map((group) => (
        <section className="mt-3 first:mt-0" key={group.key}>
          <div className="flex items-center gap-2.5 py-2 text-xs font-medium text-muted-foreground after:h-px after:flex-1 after:bg-border">
            <span>{group.label}</span>
            <small className="font-medium">{group.records.length} 条</small>
          </div>
          {group.records.map((record) => {
            const expanded = expandedRoots.has(record.id);
            const children = childrenByRoot[record.id] ?? [];
            return (
              <div key={record.id}>
                <RecordRow
                  serviceName={serviceLabel(record.service_id, services)}
                  nowMs={nowMs}
                  onOpen={() => onOpen(record.id)}
                  onToggleRetries={
                    record.child_count > 0
                      ? () => toggleRoot(record.id, record.child_count)
                      : undefined
                  }
                  record={record}
                  retriesExpanded={expanded}
                  selected={record.id === selectedId}
                />
                {expanded ? (
                  <div className="ml-6 border-l pl-2" data-testid="request-record-children">
                    {childrenLoading.has(record.id) && children.length === 0 ? (
                      <p className="px-3 py-2 text-xs text-muted-foreground" role="status">
                        正在加载重试记录…
                      </p>
                    ) : (
                      children.map((child, childIndex) => (
                        <RecordRow
                          child
                          childOrdinal={childIndex + 1}
                          key={child.id}
                          nowMs={nowMs}
                          onOpen={() => onOpen(child.id)}
                          record={child}
                          selected={child.id === selectedId}
                          serviceName={serviceLabel(child.service_id, services)}
                        />
                      ))
                    )}
                  </div>
                ) : null}
              </div>
            );
          })}
        </section>
      ))}
    </div>
  );
}

function RecordRow({
  record,
  serviceName,
  nowMs,
  selected,
  onOpen,
  child = false,
  childOrdinal,
  retriesExpanded = false,
  onToggleRetries,
}: {
  record: RequestRecord;
  serviceName: string | null;
  nowMs: number;
  selected: boolean;
  onOpen: () => void;
  child?: boolean;
  childOrdinal?: number;
  retriesExpanded?: boolean;
  onToggleRetries?: () => void;
}) {
  const captured =
    record.audit.request_body_captured ||
    record.audit.response_content_captured ||
    record.audit.upstream_request_body_captured ||
    record.audit.upstream_response_content_captured;
  const time = new Date(record.started_at);
  return (
    <div className={cn("relative", child && "opacity-95")}>
      <Button
        aria-current={selected ? "true" : undefined}
        className={cn(
          "grid h-auto w-full grid-cols-1 gap-1 rounded-none border-b bg-transparent px-3 py-2.5 text-left text-foreground shadow-none hover:bg-muted focus-visible:bg-accent aria-[current=true]:bg-accent",
          child && "pl-2",
        )}
        data-record-id={record.id}
        data-testid="request-record-row"
        onClick={onOpen}
        type="button"
        variant="ghost"
      >
        <span className="grid min-w-0 grid-cols-[8px_74px_minmax(120px,1fr)_minmax(100px,.7fr)_auto_auto] items-center gap-2 @max-[720px]:grid-cols-[8px_66px_minmax(90px,1fr)_auto_auto]">
          <StatusDot tone={statusTone(record.status)} />
          <time className="text-xs tabular-nums text-muted-foreground" dateTime={record.started_at}>
            {Number.isNaN(time.getTime())
              ? record.started_at
              : time.toLocaleTimeString("zh-CN", { hour12: false })}
          </time>
          <strong className="truncate text-sm font-medium">{record.requested_model ?? "未指定模型"}</strong>
          <span className="overflow-hidden text-xs text-muted-foreground text-ellipsis whitespace-nowrap @max-[720px]:hidden">
            {serviceName ?? record.service_id ?? "正在选择服务"}
          </span>
          {child ? (
            <Badge className="text-micro" variant="secondary">
              子请求 {childOrdinal ?? record.attempt_index}
            </Badge>
          ) : record.child_count > 0 ? (
            <Badge className="text-micro" variant="secondary">最后一次记录</Badge>
          ) : null}
          <span className="text-right text-xs font-semibold tabular-nums">
            {formatDuration(liveDurationMs(record, nowMs))}
          </span>
        </span>
        <span className="flex min-w-0 items-center gap-2 overflow-hidden pl-4 text-micro text-muted-foreground [&>*]:max-w-[190px] [&>*]:overflow-hidden [&>*]:text-ellipsis [&>*]:whitespace-nowrap">
          <StatusText record={record} />
          <span>HTTP {record.http_status ?? "—"}</span>
          <code>{record.input_protocol}</code>
          <span>
            {record.streaming ? "流式" : "非流式"}
          </span>
          <span>
            {record.usage
              ? `${record.usage.input_tokens.toLocaleString()} → ${record.usage.output_tokens.toLocaleString()} Token`
              : "Token —"}
          </span>
          <span>
            {captured ? "已捕获" : "未捕获正文"}
          </span>
        </span>
      </Button>
      {onToggleRetries ? (
        <Button
          aria-expanded={retriesExpanded}
          className="absolute top-1/2 right-2 h-6 -translate-y-1/2 px-2 text-micro"
          onClick={(event) => {
            event.stopPropagation();
            onToggleRetries();
          }}
          type="button"
          variant="outline"
        >
          {retriesExpanded
            ? "收起子请求"
            : `子请求 ${record.child_count} 条`}
        </Button>
      ) : null}
    </div>
  );
}

function StatusText({ record }: { record: RequestRecord }) {
  if (record.error && record.status !== "pending") {
    return (
      <strong className="text-danger-foreground">
        {record.error.category} · {record.error.code}
      </strong>
    );
  }
  return <strong>{statusLabel(record.status)}</strong>;
}

function RecordDetail({
  record,
  serviceName,
  nowMs,
  auditContent,
  auditLoading,
  auditError,
  deleting,
  previousId,
  nextId,
  index,
  navigationCount,
  onBack,
  onPrevious,
  onNext,
  onDelete,
  onClearDecrypted,
}: {
  record: RequestRecord;
  serviceName: string | null;
  nowMs: number;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  deleting: boolean;
  previousId: string | null;
  nextId: string | null;
  index: number;
  navigationCount: number;
  onBack: () => void;
  onPrevious: () => void;
  onNext: () => void;
  onDelete: () => void;
  onClearDecrypted: () => void;
}) {
  const copyFeedback = useCopyFeedback();
  const [bundleSize, setBundleSize] = useState<number | null>(null);
  const isChild = record.parent_request_id !== null;
  const requestPart = auditContent?.request_body ?? null;
  const responsePart = auditContent?.response_content ?? null;
  const upstreamRequestPart = auditContent?.upstream_request_body ?? null;
  const upstreamResponsePart = auditContent?.upstream_response_content ?? null;

  const copyBundle = (includeBodies: boolean) => {
    const bundle = buildRecordBundle(record, auditContent, {
      includeBodies,
      serviceLabel: serviceName,
    });
    setBundleSize(includeBodies ? bundle.length : null);
    copyFeedback.copy(includeBodies ? "bundle" : "bundle-meta", bundle);
  };

  return (
    <section
      aria-labelledby="request-detail-heading"
      className="flex h-full min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-lg border bg-card"
    >
      <PageHeader
        actions={
          <>
            <Button
              variant="outline"
              disabled={!previousId}
              onClick={onPrevious}
              title="快捷键 ["
              type="button"
            >
              上一条
            </Button>
            <span className="text-xs tabular-nums text-muted-foreground">
              {index >= 0 ? index + 1 : "—"} / {navigationCount || "—"}
            </span>
            <Button
              variant="outline"
              disabled={!nextId}
              onClick={onNext}
              title="快捷键 ]"
              type="button"
            >
              下一条
            </Button>
            <Button
              onClick={() => copyBundle(true)}
              type="button"
            >
              {copyButtonLabel(
                copyFeedback,
                "bundle",
                "一键复制全部",
                bundleSize !== null
                  ? `已复制 ${formatBytes(bundleSize)}`
                  : "已复制",
              )}
            </Button>
            <Button
              variant="outline"
              onClick={() => copyBundle(false)}
              type="button"
            >
              {copyButtonLabel(
                copyFeedback,
                "bundle-meta",
                "仅复制元数据 + HTTP",
              )}
            </Button>
            <Button
              className="text-danger-foreground hover:bg-danger-wash hover:text-danger-foreground"
              disabled={deleting || record.status === "pending"}
              onClick={onDelete}
              title={
                record.status === "pending"
                  ? "进行中的记录结束后才能删除"
                  : undefined
              }
              type="button"
              variant="outline"
            >
              {deleting ? "删除中…" : "删除"}
            </Button>
          </>
        }
        back={{ label: "实时监控", onClick: onBack }}
        description="状态与指标会随实时监控中的同一条记录自动更新。"
        eyebrow="请求记录"
        title="记录详情"
        titleId="request-detail-heading"
        variant="card"
      />

      <div className="min-h-0 min-w-0 flex-1 space-y-3 overflow-auto overscroll-contain p-[22px]">
        <DetailSection title="身份">
          <div className="grid grid-cols-4 gap-3 @max-[720px]:grid-cols-2">
            <div className="col-span-full flex items-center gap-2 rounded-lg bg-muted px-3 py-2">
              <StatusDot tone={statusTone(record.status)} />
              <strong className="text-sm">{statusLabel(record.status)}</strong>
              {record.status === "pending" ? (
                <span className="text-xs text-muted-foreground">
                  已运行 {formatDuration(liveDurationMs(record, nowMs))}
                </span>
              ) : null}
            </div>
            <DetailField label="模型" value={record.requested_model ?? "—"} />
            <DetailField label="协议" value={record.input_protocol} code />
            <DetailField
              label="传输"
              value={record.streaming ? "流式" : "非流式"}
            />
            <DetailField
              label="服务"
              value={serviceName ?? record.service_id ?? "—"}
            />
            <DetailField label="开始" value={formatDateTime(record.started_at)} />
            <DetailField
              label="完成"
              value={
                record.completed_at ? formatDateTime(record.completed_at) : "—"
              }
            />
            <div className="col-span-full min-w-0">
              <dt className="text-xs font-medium text-muted-foreground">ID</dt>
              <dd className="mt-1 flex min-w-0 items-center gap-2 text-xs text-text-secondary">
                <code className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{record.id}</code>
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

        <DetailSection title="指标">
          <dl className="grid grid-cols-3 @max-[720px]:grid-cols-2 [&>div]:border-l [&>div]:px-3 [&>div:nth-child(3n+1)]:border-l-0 [&>div:nth-child(3n+1)]:pl-0 @max-[720px]:[&>div:nth-child(3n+1)]:border-l @max-[720px]:[&>div:nth-child(3n+1)]:pl-3 @max-[720px]:[&>div:nth-child(odd)]:border-l-0 @max-[720px]:[&>div:nth-child(odd)]:pl-0">
            <Metric label="HTTP" value={record.http_status ?? "—"} />
            <Metric
              label="延迟"
              value={formatDuration(liveDurationMs(record, nowMs))}
              live={record.status === "pending"}
            />
            <Metric
              label="输入 Token"
              value={record.usage?.input_tokens ?? "—"}
            />
            <Metric
              label="输出 Token"
              value={record.usage?.output_tokens ?? "—"}
            />
            <Metric
              label="总 Token"
              value={record.usage?.total_tokens ?? "—"}
            />
            <Metric
              label="缓存 Token"
              value={record.usage?.cached_input_tokens ?? "—"}
            />
          </dl>
        </DetailSection>

        <DetailSection title="隐私还原">
          {record.privacy_restore ? (
            <dl className="grid grid-cols-4 @max-[720px]:grid-cols-2 [&>div]:border-l [&>div]:px-3 [&>div:first-child]:border-l-0 [&>div:first-child]:pl-0">
              <Metric
                label="状态"
                value={record.privacy_restore.enabled ? "已开启" : "已关闭"}
              />
              <Metric
                label="映射数"
                value={record.privacy_restore.mapping_count}
              />
              <Metric
                label="已还原"
                value={record.privacy_restore.restored_count}
              />
              <Metric
                label="安全降级"
                value={record.privacy_restore.fallback_count}
              />
            </dl>
          ) : (
            <p className="text-xs text-success-foreground">本次未触发请求脱敏，或属于旧版记录。</p>
          )}
        </DetailSection>

        {record.error ? (
          <DetailSection tone="error" title="错误">
            <dl className="grid grid-cols-3 gap-3 @max-[720px]:grid-cols-2">
              <DetailField label="类别" value={record.error.category} code />
              <DetailField label="代码" value={record.error.code} code />
              <DetailField
                label="可重试"
                value={record.error.retryable ? "是" : "否"}
              />
              <DetailField
                className="col-span-full"
                label="信息"
                value={record.error.message}
              />
            </dl>
          </DetailSection>
        ) : null}

        {auditError ? (
          <p className="text-xs text-danger-foreground" role="alert">
            {auditError}
          </p>
        ) : null}
        {auditLoading ? (
          <DetailSection title="内容">
            <p className="text-xs text-muted-foreground" role="status">
              正在解密内容…
            </p>
          </DetailSection>
        ) : auditContent ? (
          <>
            {!isChild ? (
              <>
                <HTTPMetaSection
                  copyFeedback={copyFeedback}
                  meta={auditContent.http_meta}
                  title="客户端 HTTP"
                />
                <AuditPartSection
                  copyFeedback={copyFeedback}
                  part={requestPart}
                  protocol={record.input_protocol}
                  sectionKey="request-body"
                  title="客户端请求体"
                />
                <AuditPartSection
                  copyFeedback={copyFeedback}
                  part={responsePart}
                  protocol={record.input_protocol}
                  sectionKey="response-content"
                  title="客户端响应内容"
                />
              </>
            ) : null}
            <HTTPMetaSection
              copyFeedback={copyFeedback}
              copyKey="upstream-http-meta"
              meta={auditContent.upstream_http_meta}
              title="上游 HTTP"
            />
            <AuditPartSection
              copyFeedback={copyFeedback}
              part={upstreamRequestPart}
              protocol={record.input_protocol}
              sectionKey="upstream-request-body"
              title="上游请求体"
            />
            <AuditPartSection
              copyFeedback={copyFeedback}
              part={upstreamResponsePart}
              protocol={record.input_protocol}
              sectionKey="upstream-response-content"
              title="上游响应内容"
            />
          </>
        ) : null}

        <DetailSection title="关联">
          <dl className="grid grid-cols-3 gap-3 @max-[720px]:grid-cols-2">
            <DetailField
              label="尝试序号"
              value={
                record.attempt_index === 0
                  ? "未到达上游"
                  : String(record.attempt_index)
              }
            />
            <DetailField
              label="父记录"
              value={record.parent_request_id ?? "（根记录）"}
              code
            />
            <DetailField
              label="重试子记录"
              value={String(record.child_count)}
            />
            <DetailField label="路由" value={record.route_id ?? "—"} code />
            <DetailField
              label="服务"
              value={serviceName ?? record.service_id ?? "—"}
            />
            <DetailField
              label="访问令牌"
              value={record.local_access_token_id ?? "—"}
              code
            />
          </dl>
        </DetailSection>

        <DetailSection title="审计">
          <div className="grid grid-cols-2 gap-2.5 @max-[720px]:grid-cols-1">
            {!isChild ? (
              <>
                <AuditSummaryCard
                  captured={record.audit.request_body_captured}
                  label="客户端请求体"
                  part={requestPart}
                  truncated={record.audit.request_body_truncated}
                />
                <AuditSummaryCard
                  captured={record.audit.response_content_captured}
                  label="客户端响应"
                  part={responsePart}
                  truncated={record.audit.response_content_truncated}
                />
              </>
            ) : null}
            <AuditSummaryCard
              captured={record.audit.upstream_request_body_captured}
              label="上游请求体"
              part={upstreamRequestPart}
              truncated={record.audit.upstream_request_body_truncated}
            />
            <AuditSummaryCard
              captured={record.audit.upstream_response_content_captured}
              label="上游响应"
              part={upstreamResponsePart}
              truncated={record.audit.upstream_response_content_truncated}
            />
          </div>
          <div className="mt-3 flex items-center justify-between gap-3 rounded-lg bg-muted px-3 py-2 text-xs text-muted-foreground">
            <span>解密内容仅保存在当前会话内存中。</span>
            <Button
              variant="outline"
              onClick={onClearDecrypted}
              type="button"
            >
              清除已解密内容
            </Button>
          </div>
        </DetailSection>
      </div>
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
      <dd className="mt-1 overflow-hidden text-xs text-text-secondary text-ellipsis whitespace-nowrap">{code && typeof value === "string" && value !== "—" ? <code className="text-xs">{value}</code> : value}</dd>
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
  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-base font-semibold tabular-nums">
        {value}
        {live ? <small className="ml-1 text-micro font-medium text-warning-foreground">实时</small> : null}
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
  return (
    <article className="rounded-md border bg-muted p-3">
      <header className="mb-2 flex items-center justify-between gap-2">
        <strong className="text-sm font-medium">{label}</strong>
        <span className={cn("text-micro text-muted-foreground", captured && "text-success-foreground")}>
          {captured ? "已捕获" : "未捕获"}
        </span>
      </header>
      <dl className="grid grid-cols-3 gap-2">
        <DetailField label="类型" value={part?.media_type ?? "—"} />
        <DetailField
          label="大小"
          value={part ? formatBytes(part.captured_bytes) : "—"}
        />
        <DetailField label="截断" value={truncated ? "是" : "否"} />
      </dl>
    </article>
  );
}

function FilterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: Array<{ label: string; value: string }>;
}) {
  return (
    <Label className="grid items-stretch gap-1.5 text-xs font-semibold text-text-secondary @max-[720px]:last:col-span-full">
      <span>{label}</span>
      <Select
        onValueChange={(next) => onChange(next === "__all__" ? "" : next)}
        value={value || "__all__"}
      >
        <SelectTrigger aria-label={`${label}筛选`} className="h-8 min-w-[120px] @max-[720px]:w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((option) => (
            <SelectItem
              key={option.value || "__all__"}
              value={option.value || "__all__"}
            >
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Label>
  );
}

function RecordSkeleton() {
  return (
    <div aria-label="正在加载请求记录" className="grid gap-2 p-3">
      {Array.from({ length: 6 }, (_, index) => (
        <div className="grid animate-pulse gap-2 rounded-md border p-3" key={index}>
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
  return (
    <ModalDialog onCancel={onCancel} title="审计设置">
      {draft ? (
        <div className="grid grid-cols-2 gap-3 max-[600px]:grid-cols-1">
          <CheckField
            checked={draft.http_meta_enabled}
            label="HTTP 元数据捕获（方法 / URL / 请求头，敏感值已脱敏）"
            onChange={(value) => onChange("http_meta_enabled", value)}
          />
          <CheckField
            checked={draft.request_body_enabled}
            label="请求体捕获"
            onChange={(value) => onChange("request_body_enabled", value)}
          />
          <CheckField
            checked={draft.response_content_enabled}
            label="响应内容捕获"
            onChange={(value) => onChange("response_content_enabled", value)}
          />
          <NumberField
            label="请求体上限（字节）"
            max={16_777_216}
            min={1024}
            onChange={(value) => onChange("request_body_max_bytes", value)}
            value={draft.request_body_max_bytes}
          />
          <NumberField
            label="响应内容上限（字节）"
            max={67_108_864}
            min={1024}
            onChange={(value) => onChange("response_content_max_bytes", value)}
            value={draft.response_content_max_bytes}
          />
          <NumberField
            label="元数据保留（天）"
            max={3650}
            min={1}
            onChange={(value) => onChange("metadata_retention_days", value)}
            value={draft.metadata_retention_days}
          />
          <NumberField
            label="内容保留（天）"
            max={365}
            min={1}
            onChange={(value) => onChange("content_retention_days", value)}
            value={draft.content_retention_days}
          />
          <FormMessage className="col-span-full" tone="notice">
            开启后仅捕获新请求；正文以密文保存在本机。开启正文捕获需进行第二步风险确认；HTTP
            元数据在捕获时即脱敏（Authorization 等敏感值不落盘），无需额外确认。
          </FormMessage>
        </div>
      ) : busy ? (
        <p>加载中…</p>
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
          关闭
        </Button>
        <Button
          disabled={busy || !draft}
          onClick={onSave}
          type="button"
        >
          {busy ? "保存中…" : "保存"}
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
  return (
    <ModalDialog onCancel={onCancel} title="清理请求记录">
      <RadioGroup
        className="grid gap-2"
        disabled={busy}
        onValueChange={(value) => onModeChange(value as "all" | "before")}
        value={mode}
      >
        <Label className="flex items-center gap-2 rounded-lg border bg-muted px-3 py-2 text-sm">
          <RadioGroupItem value="all" />
          <span>清空全部记录及加密内容</span>
        </Label>
        <Label className="flex items-center gap-2 rounded-lg border bg-muted px-3 py-2 text-sm">
          <RadioGroupItem value="before" />
          <span>清除指定时间之前的记录</span>
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
          取消
        </Button>
        <Button
          disabled={busy}
          onClick={onSubmit}
          type="button"
        >
          {busy ? "清理中…" : "执行清理"}
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
  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent className="max-w-xl sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            配置请求记录的捕获范围、保留期限或清理条件。
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
    return "开启正文捕获后，请求/响应原文将以密文形式保存在本机数据库中，密钥也位于本机。在本机被攻破的威胁模型下，这接近明文保存。确认开启？";
  }
  if (pending.kind === "delete") {
    return "删除后该记录及其加密审计内容将不可恢复，确定删除？";
  }
  if (pending.kind === "purge-all") {
    return "确定清空全部请求记录及其加密审计内容？此操作不可恢复。";
  }
  return `确定清除 ${formatDateTime(pending.before)} 之前的全部请求记录及其加密审计内容？此操作不可恢复。`;
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

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", { hour12: false });
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}
