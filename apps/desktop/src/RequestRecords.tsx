import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { AuditReviewer } from "./AuditReviewer";
import type { AuditSettings, AuditSettingsPatch } from "./audit-settings-model";
import {
  deleteRequestRecord,
  getAuditSettings,
  getRequestAuditContent,
  listRequestRecords,
  purgeRequestRecords,
  updateAuditSettings,
} from "./bridge";
import type { Endpoint } from "./endpoint-model";
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
const STATUSES: RequestStatus[] = [
  "pending",
  "succeeded",
  "failed",
  "cancelled",
  "blocked",
];
const EMPTY_FILTERS: RecordFilters = {
  status: "",
  endpointId: "",
  protocol: "",
};

type RecordsView = "monitor" | "detail" | "audit";
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

export function RequestRecords({
  coreSessionKey,
  isReady,
  endpoints,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
  endpoints: Endpoint[];
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
  const selected = useMemo(
    () => allRecords.find((record) => record.id === selectedId) ?? null,
    [allRecords, selectedId],
  );
  const protocolOptions = useMemo(() => {
    const protocols = new Set<string>();
    endpoints.forEach((endpoint) =>
      endpoint.capabilities.forEach((capability) =>
        protocols.add(capability.protocol),
      ),
    );
    allRecords.forEach((record) => protocols.add(record.input_protocol));
    return [...protocols].sort();
  }, [allRecords, endpoints]);

  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    auditGenerationRef.current += 1;
    pollFailureRef.current = 0;
    pollInFlightRef.current = false;
    atTopRef.current = true;
    viewRef.current = "monitor";
    setView("monitor");
    setSelectedId(null);
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

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
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
      if (viewRef.current === "audit") {
        setViewAndRef("detail");
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
    },
    [],
  );

  const setViewAndRef = (next: RecordsView) => {
    viewRef.current = next;
    setView(next);
  };

  const clearAudit = () => {
    auditGenerationRef.current += 1;
    setAuditContent(null);
    setAuditError(null);
    setAuditLoading(false);
  };

  const openDetail = (requestId: string) => {
    selectedFocusRef.current = requestId;
    setNavigationIds(visibleItems.map((record) => record.id));
    setSelectedId(requestId);
    clearAudit();
    setViewAndRef("detail");
  };

  const selectFromSnapshot = (requestId: string) => {
    if (requestId === selectedId) return;
    selectedFocusRef.current = requestId;
    setSelectedId(requestId);
    clearAudit();
  };

  const returnToMonitor = () => {
    clearAudit();
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

  const decryptAndReview = async () => {
    if (!selected) return;
    const generation = auditGenerationRef.current + 1;
    auditGenerationRef.current = generation;
    setAuditLoading(true);
    setAuditError(null);
    try {
      const content = await getRequestAuditContent(selected.id);
      if (auditGenerationRef.current !== generation) return;
      setAuditContent(content);
      setViewAndRef("audit");
    } catch (requestError: unknown) {
      if (auditGenerationRef.current !== generation) return;
      const message = messageOf(requestError, "无法读取审计内容。");
      setAuditError(
        message.includes("409")
          ? "审计密钥缺失或损坏，无法解密该记录。"
          : message,
      );
    } finally {
      if (auditGenerationRef.current === generation) setAuditLoading(false);
    }
  };

  const commitDelete = async (requestId: string) => {
    setDeleting(true);
    setError(null);
    try {
      await deleteRequestRecord(requestId);
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

  const selectedIndex = selectedId ? navigationIds.indexOf(selectedId) : -1;
  const previousId =
    selectedIndex > 0 ? navigationIds[selectedIndex - 1] : null;
  const nextId =
    selectedIndex >= 0 && selectedIndex < navigationIds.length - 1
      ? navigationIds[selectedIndex + 1]
      : null;

  return (
    <>
      <div className="records-stack">
        <section
          aria-labelledby="request-records-heading"
          className="workspace-card records-surface records-monitor"
          hidden={view !== "monitor"}
        >
          <header className="records-page-header">
            <div>
              <span className="section-kicker">实时监控</span>
              <h2 id="request-records-heading">请求记录</h2>
              <p>已认证的推理请求会在开始后立即进入日志流。</p>
            </div>
            <div className="records-page-actions">
              <span className="records-live-indicator">
                <span className="dot dot--positive" />
                每秒同步
              </span>
              <button
                className="btn-secondary"
                disabled={!isReady}
                onClick={() => setPurgeOpen(true)}
                type="button"
              >
                清理…
              </button>
              <button
                className="btn-secondary"
                disabled={!isReady}
                onClick={() => void openSettings()}
                type="button"
              >
                审计设置
              </button>
            </div>
          </header>

          {notice ? (
            <p className="inline-notice records-banner" role="status">
              {notice}
            </p>
          ) : null}
          {error ? (
            <p className="inline-error records-banner" role="alert">
              {error}
            </p>
          ) : null}
          {syncWarning ? (
            <p className="records-sync-warning" role="status">
              <span className="dot dot--pending" />
              {syncWarning}
            </p>
          ) : null}

          <div
            className="records-monitor__scroll"
            onScroll={(event) => {
              atTopRef.current = event.currentTarget.scrollTop <= 8;
            }}
            ref={monitorScrollRef}
          >
            <div className="records-toolbar">
              <div className="records-filters">
                <FilterSelect
                  label="状态"
                  onChange={(status) =>
                    setFilters((current) => ({
                      ...current,
                      status: status as RequestStatus | "",
                    }))
                  }
                  value={filters.status}
                >
                  <option value="">全部</option>
                  {STATUSES.map((status) => (
                    <option key={status} value={status}>
                      {statusLabel(status)}
                    </option>
                  ))}
                </FilterSelect>
                <FilterSelect
                  label="服务"
                  onChange={(endpointId) =>
                    setFilters((current) => ({ ...current, endpointId }))
                  }
                  value={filters.endpointId}
                >
                  <option value="">全部</option>
                  {endpoints.map((endpoint) => (
                    <option key={endpoint.id} value={endpoint.id}>
                      {endpoint.name}
                    </option>
                  ))}
                </FilterSelect>
                <FilterSelect
                  label="协议"
                  onChange={(protocol) =>
                    setFilters((current) => ({ ...current, protocol }))
                  }
                  value={filters.protocol}
                >
                  <option value="">全部</option>
                  {protocolOptions.map((protocol) => (
                    <option key={protocol} value={protocol}>
                      {protocol}
                    </option>
                  ))}
                </FilterSelect>
              </div>
              <button
                className="btn-secondary records-refresh"
                disabled={!isReady || pollInFlightRef.current}
                onClick={() => manualPollRef.current?.()}
                type="button"
              >
                刷新
              </button>
            </div>

            {queuedVisibleCount > 0 ? (
              <button
                className="records-new-button"
                onClick={applyQueue}
                type="button"
              >
                ↑ {queuedVisibleCount} 条新记录
              </button>
            ) : null}

            {!isReady || listStatus === "blocked" ? (
              <div className="records-empty">
                <strong>等待 Core 就绪</strong>
                <span>连接成功后，请求会自动出现在这里。</span>
              </div>
            ) : listStatus === "error" && listError ? (
              <div className="records-empty">
                <strong>无法读取请求记录</strong>
                <span>{listError}</span>
              </div>
            ) : listStatus === "loading" && live.items.length === 0 ? (
              <RecordSkeleton />
            ) : visibleItems.length === 0 ? (
              <div className="records-empty">
                <strong>没有匹配的请求</strong>
                <span>调整筛选条件，或发起一次新的推理请求。</span>
              </div>
            ) : (
              <RecordStream
                endpoints={endpoints}
                nowMs={nowMs}
                onOpen={openDetail}
                records={visibleItems}
                selectedId={selectedId}
              />
            )}

            {live.nextCursor ? (
              <button
                className="btn-secondary records-load-more"
                disabled={loadingMore}
                onClick={() => void loadMore()}
                type="button"
              >
                {loadingMore ? "加载中…" : "加载更早记录"}
              </button>
            ) : null}
          </div>
        </section>

        {view === "detail" && selected ? (
          <RecordDetail
            auditContent={auditContent}
            auditError={auditError}
            auditLoading={auditLoading}
            deleting={deleting}
            endpointName={endpointLabel(selected.endpoint_id, endpoints)}
            index={selectedIndex}
            navigationCount={navigationIds.length}
            nextId={nextId}
            nowMs={nowMs}
            onBack={returnToMonitor}
            onDelete={() =>
              setPendingConfirm({ kind: "delete", requestId: selected.id })
            }
            onNext={() => nextId && selectFromSnapshot(nextId)}
            onPrevious={() => previousId && selectFromSnapshot(previousId)}
            onReview={() => void decryptAndReview()}
            previousId={previousId}
            record={selected}
          />
        ) : null}

        {view === "audit" && selected && auditContent ? (
          <AuditReviewer
            content={auditContent}
            onBack={() => setViewAndRef("detail")}
            onClear={() => {
              clearAudit();
              setViewAndRef("detail");
            }}
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
        <ConfirmDialog
          busy={settingsBusy || deleting || purgeBusy}
          confirmLabel={
            pendingConfirm.kind === "audit-risk"
              ? "确认开启"
              : pendingConfirm.kind === "delete"
                ? "确定删除"
                : "确定清理"
          }
          message={confirmMessage(pendingConfirm)}
          onCancel={() => {
            if (pendingConfirm.kind === "audit-risk") {
              setSettingsNotice("已取消开启正文捕获。");
            }
            setPendingConfirm(null);
          }}
          onConfirm={resolveConfirm}
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
  endpoints,
  selectedId,
  nowMs,
  onOpen,
}: {
  records: RequestRecord[];
  endpoints: Endpoint[];
  selectedId: string | null;
  nowMs: number;
  onOpen: (requestId: string) => void;
}) {
  const groups = groupRecordsByDate(records, new Date(nowMs));
  return (
    <div className="record-stream" role="feed" aria-label="实时请求流">
      {groups.map((group) => (
        <section className="record-day" key={group.key}>
          <div className="record-day__divider">
            <span>{group.label}</span>
            <small>{group.records.length} 条</small>
          </div>
          {group.records.map((record) => (
            <RecordRow
              endpointName={endpointLabel(record.endpoint_id, endpoints)}
              key={record.id}
              nowMs={nowMs}
              onOpen={() => onOpen(record.id)}
              record={record}
              selected={record.id === selectedId}
            />
          ))}
        </section>
      ))}
    </div>
  );
}

function RecordRow({
  record,
  endpointName,
  nowMs,
  selected,
  onOpen,
}: {
  record: RequestRecord;
  endpointName: string | null;
  nowMs: number;
  selected: boolean;
  onOpen: () => void;
}) {
  const captured =
    record.audit.request_body_captured ||
    record.audit.response_content_captured;
  const time = new Date(record.started_at);
  return (
    <button
      aria-current={selected ? "true" : undefined}
      className={`record-row${record.status === "pending" ? " is-pending" : ""}`}
      data-record-id={record.id}
      onClick={onOpen}
      type="button"
    >
      <span className="record-row__primary">
        <span
          className={`dot dot--${statusTone(record.status)}`}
          aria-hidden="true"
        />
        <time dateTime={record.started_at}>
          {Number.isNaN(time.getTime())
            ? record.started_at
            : time.toLocaleTimeString("zh-CN", { hour12: false })}
        </time>
        <strong>{record.requested_model ?? "未指定模型"}</strong>
        <span className="record-row__service">
          {endpointName ?? record.endpoint_id ?? "正在选择服务"}
        </span>
        <span className="record-row__duration">
          {formatDuration(liveDurationMs(record, nowMs))}
        </span>
      </span>
      <span className="record-row__secondary">
        <StatusText record={record} />
        <span>HTTP {record.http_status ?? "—"}</span>
        <code>{record.input_protocol}</code>
        <span className="record-row__minor">
          {record.streaming ? "流式" : "非流式"}
        </span>
        <span className="record-row__tokens">
          {record.usage
            ? `${record.usage.input_tokens.toLocaleString()} → ${record.usage.output_tokens.toLocaleString()} Token`
            : "Token —"}
        </span>
        <span className="record-row__audit">
          {captured ? "已捕获" : "未捕获正文"}
        </span>
      </span>
    </button>
  );
}

function StatusText({ record }: { record: RequestRecord }) {
  if (record.error && record.status !== "pending") {
    return (
      <strong className="record-row__error">
        {record.error.category} · {record.error.code}
      </strong>
    );
  }
  return <strong>{statusLabel(record.status)}</strong>;
}

function RecordDetail({
  record,
  endpointName,
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
  onReview,
}: {
  record: RequestRecord;
  endpointName: string | null;
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
  onReview: () => void;
}) {
  const captured =
    record.audit.request_body_captured ||
    record.audit.response_content_captured;
  const requestPart = auditContent?.request_body ?? null;
  const responsePart = auditContent?.response_content ?? null;
  return (
    <section
      aria-labelledby="request-detail-heading"
      className="workspace-card records-surface record-detail"
    >
      <header className="records-page-header">
        <div>
          <button className="records-back" onClick={onBack} type="button">
            <span aria-hidden="true">←</span>
            实时监控
          </button>
          <span className="section-kicker">请求记录</span>
          <h2 id="request-detail-heading">记录详情</h2>
          <p>状态与指标会随实时监控中的同一条记录自动更新。</p>
        </div>
        <div className="records-page-actions">
          <button
            className="btn-secondary"
            disabled={!previousId}
            onClick={onPrevious}
            type="button"
          >
            上一条
          </button>
          <span className="record-detail__position">
            {index >= 0 ? index + 1 : "—"} / {navigationCount || "—"}
          </span>
          <button
            className="btn-secondary"
            disabled={!nextId}
            onClick={onNext}
            type="button"
          >
            下一条
          </button>
          <button
            className="btn-secondary is-danger"
            disabled={deleting || record.status === "pending"}
            onClick={onDelete}
            title={
              record.status === "pending" ? "进行中的记录结束后才能删除" : undefined
            }
            type="button"
          >
            {deleting ? "删除中…" : "删除"}
          </button>
        </div>
      </header>

      <div className="record-detail__scroll">
        <DetailSection title="身份">
          <div className="record-identity">
            <div className="record-identity__status">
              <span className={`dot dot--${statusTone(record.status)}`} />
              <strong>{statusLabel(record.status)}</strong>
              {record.status === "pending" ? (
                <span>
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
              value={endpointName ?? record.endpoint_id ?? "—"}
            />
            <DetailField label="开始" value={formatDateTime(record.started_at)} />
            <DetailField
              label="完成"
              value={
                record.completed_at ? formatDateTime(record.completed_at) : "—"
              }
            />
            <div className="detail-field record-id-field">
              <dt>ID</dt>
              <dd>
                <code>{record.id}</code>
                <button
                  className="text-button"
                  onClick={() => void copyText(record.id)}
                  type="button"
                >
                  复制
                </button>
              </dd>
            </div>
          </div>
        </DetailSection>

        <DetailSection title="指标">
          <dl className="record-metrics">
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

        {record.error ? (
          <DetailSection tone="error" title="错误">
            <dl className="record-detail-grid">
              <DetailField label="类别" value={record.error.category} code />
              <DetailField label="代码" value={record.error.code} code />
              <DetailField
                label="可重试"
                value={record.error.retryable ? "是" : "否"}
              />
              <DetailField
                className="is-wide"
                label="信息"
                value={record.error.message}
              />
            </dl>
          </DetailSection>
        ) : null}

        <DetailSection title="关联">
          <dl className="record-detail-grid">
            <DetailField label="路由" value={record.route_id ?? "—"} code />
            <DetailField
              label="服务"
              value={endpointName ?? record.endpoint_id ?? "—"}
            />
            <DetailField
              label="访问令牌"
              value={record.local_access_token_id ?? "—"}
              code
            />
          </dl>
        </DetailSection>

        <DetailSection title="审计">
          <div className="record-audit-summary">
            <AuditSummaryCard
              captured={record.audit.request_body_captured}
              label="请求体"
              part={requestPart}
              truncated={record.audit.request_body_truncated}
            />
            <AuditSummaryCard
              captured={record.audit.response_content_captured}
              label="响应内容"
              part={responsePart}
              truncated={record.audit.response_content_truncated}
            />
          </div>
          <div className="record-audit-action">
            <div>
              <strong>内容不会自动解密</strong>
              <span>点击后在独立审查页中解密，离开监控会清除内存。</span>
            </div>
            <button
              className="btn-primary"
              disabled={!captured || auditLoading}
              onClick={onReview}
              type="button"
            >
              {auditLoading ? "正在解密…" : "解密并审查内容"}
            </button>
          </div>
          {auditError ? (
            <p className="inline-error" role="alert">
              {auditError}
            </p>
          ) : null}
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
      className={`record-detail-section${tone ? ` is-${tone}` : ""}`}
    >
      <h3>{title}</h3>
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
    <div className={`detail-field ${className}`}>
      <dt>{label}</dt>
      <dd>{code && typeof value === "string" && value !== "—" ? <code>{value}</code> : value}</dd>
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
    <div>
      <dt>{label}</dt>
      <dd>
        {value}
        {live ? <small>实时</small> : null}
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
    <article>
      <header>
        <strong>{label}</strong>
        <span className={captured ? "is-captured" : ""}>
          {captured ? "已捕获" : "未捕获"}
        </span>
      </header>
      <dl>
        <DetailField
          label="类型"
          value={part?.media_type ?? (captured ? "解密后可见" : "—")}
        />
        <DetailField
          label="大小"
          value={part ? formatBytes(part.captured_bytes) : captured ? "解密后可见" : "—"}
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
  children,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  children: ReactNode;
}) {
  return (
    <label>
      <span>{label}</span>
      <select
        onChange={(event) => onChange(event.currentTarget.value)}
        value={value}
      >
        {children}
      </select>
    </label>
  );
}

function RecordSkeleton() {
  return (
    <div aria-label="正在加载请求记录" className="record-skeleton">
      {Array.from({ length: 6 }, (_, index) => (
        <div key={index}>
          <span />
          <span />
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
        <div className="audit-settings-form">
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
          <p className="inline-notice audit-settings-form__hint">
            开启后仅捕获新请求；正文以密文保存在本机。开启捕获需进行第二步风险确认。
          </p>
        </div>
      ) : busy ? (
        <p>加载中…</p>
      ) : null}
      {error ? <p className="inline-error">{error}</p> : null}
      {notice ? <p className="inline-notice">{notice}</p> : null}
      <div className="token-dialog__actions">
        <button
          className="btn-secondary"
          disabled={busy}
          onClick={onCancel}
          type="button"
        >
          关闭
        </button>
        <button
          className="btn-primary"
          disabled={busy || !draft}
          onClick={onSave}
          type="button"
        >
          {busy ? "保存中…" : "保存"}
        </button>
      </div>
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
      <div className="purge-options">
        <label>
          <input
            checked={mode === "all"}
            name="purge-mode"
            onChange={() => onModeChange("all")}
            type="radio"
          />
          <span>清空全部记录及加密内容</span>
        </label>
        <label>
          <input
            checked={mode === "before"}
            name="purge-mode"
            onChange={() => onModeChange("before")}
            type="radio"
          />
          <span>清除指定时间之前的记录</span>
        </label>
        {mode === "before" ? (
          <input
            onChange={(event) => onBeforeChange(event.currentTarget.value)}
            type="datetime-local"
            value={before}
          />
        ) : null}
      </div>
      <div className="token-dialog__actions">
        <button
          className="btn-secondary"
          disabled={busy}
          onClick={onCancel}
          type="button"
        >
          取消
        </button>
        <button
          className="btn-primary"
          disabled={busy}
          onClick={onSubmit}
          type="button"
        >
          {busy ? "清理中…" : "执行清理"}
        </button>
      </div>
    </ModalDialog>
  );
}

function ConfirmDialog({
  title,
  message,
  confirmLabel,
  busy,
  onCancel,
  onConfirm,
}: {
  title: string;
  message: string;
  confirmLabel: string;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <ModalDialog onCancel={onCancel} title={title}>
      <p>{message}</p>
      <div className="token-dialog__actions">
        <button
          className="btn-secondary"
          disabled={busy}
          onClick={onCancel}
          type="button"
        >
          取消
        </button>
        <button
          className="btn-primary"
          disabled={busy}
          onClick={onConfirm}
          type="button"
        >
          {confirmLabel}
        </button>
      </div>
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
    <div
      className="token-dialog-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onCancel();
      }}
      role="presentation"
    >
      <section
        aria-label={title}
        aria-modal="true"
        className="token-dialog records-dialog"
        role="dialog"
      >
        <h3>{title}</h3>
        {children}
      </section>
    </div>
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
    <label className="audit-settings-form__check">
      <input
        checked={checked}
        onChange={(event) => onChange(event.currentTarget.checked)}
        type="checkbox"
      />
      <span>{label}</span>
    </label>
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
    <label>
      <span>{label}</span>
      <input
        max={max}
        min={min}
        onChange={(event) => onChange(Number(event.currentTarget.value))}
        type="number"
        value={value}
      />
    </label>
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

function endpointLabel(
  endpointId: string | null,
  endpoints: Endpoint[],
): string | null {
  if (!endpointId) return null;
  return endpoints.find((endpoint) => endpoint.id === endpointId)?.name ?? null;
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

async function copyText(value: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(value);
  } catch {
    // The code remains selectable when clipboard permission is unavailable.
  }
}
