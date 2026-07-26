import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  getCoreStatus,
  listAccessTokens,
  listEndpoints,
  listRequestRecords,
  restartCore,
} from "./bridge";
import {
  AccessTokenManager,
  type AccessTokenCatalog,
} from "./AccessTokenManager";
import type { AccessTokenSummary } from "./access-token-model";
import {
  EndpointManager,
  type EndpointCatalogStatus,
  type EndpointManagerView,
} from "./EndpointManager";
import type { Endpoint } from "./endpoint-model";
import astrlinkLogo from "./assets/astrlink-logo.svg";
import {
  failedSnapshot,
  phaseLabel,
  phaseTone,
  type AppSnapshot,
} from "./core-model";
import { RequestGate } from "./request-gate";
import { RequestRecords } from "./RequestRecords";
import { SafetyPolicy } from "./SafetyPolicy";
import type { RequestRecord } from "./request-record-model";
import {
  aggregateTodayUsage,
  startOfTodayIso,
  type TodayUsageSummary,
} from "./today-usage";

type WorkspacePage =
  | { kind: "overview" }
  | { kind: "tokens" }
  | { kind: "safety" }
  | { kind: "records" }
  | EndpointManagerView;

type TodayUsageState = {
  status: "blocked" | "loading" | "ready" | "error";
  summary: TodayUsageSummary | null;
  error: string | null;
};
type IconName =
  | "activity"
  | "chevron-left"
  | "copy"
  | "home"
  | "key"
  | "plus"
  | "route"
  | "server"
  | "settings"
  | "shield";

interface EndpointCatalog {
  status: EndpointCatalogStatus;
  items: Endpoint[];
  error: string | null;
  stale: boolean;
}

const emptyCatalog: EndpointCatalog = {
  status: "blocked",
  items: [],
  error: null,
  stale: false,
};

const emptyTokenCatalog: AccessTokenCatalog = {
  status: "blocked",
  items: [],
  error: null,
  stale: false,
};

const emptyTodayUsage: TodayUsageState = {
  status: "blocked",
  summary: null,
  error: null,
};

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function Icon({ name }: { name: IconName }) {
  const paths: Record<IconName, ReactNode> = {
    activity: (
      <path d="M3 12h4l2.2-5 4.1 10 2.2-5H21" />
    ),
    "chevron-left": <path d="m15 18-6-6 6-6" />,
    copy: (
      <>
        <rect width="13" height="13" x="9" y="9" rx="2" />
        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
      </>
    ),
    home: (
      <>
        <path d="m3 11 9-8 9 8" />
        <path d="M5 10v10h14V10M9 20v-6h6v6" />
      </>
    ),
    key: (
      <>
        <circle cx="8" cy="15" r="4" />
        <path d="m11 12 8-8M17 6l2 2M14 9l2 2" />
      </>
    ),
    plus: <path d="M12 5v14M5 12h14" />,
    route: (
      <>
        <circle cx="6" cy="18" r="2" />
        <circle cx="18" cy="6" r="2" />
        <path d="M8 18h2a8 8 0 0 0 8-8V8M14 6h2" />
      </>
    ),
    server: (
      <>
        <rect width="18" height="7" x="3" y="3" rx="2" />
        <rect width="18" height="7" x="3" y="14" rx="2" />
        <path d="M7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6" />
      </>
    ),
    settings: (
      <>
        <circle cx="12" cy="12" r="3" />
        <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.83 2.83-.06-.06a1.7 1.7 0 0 0-1.88-.34 1.7 1.7 0 0 0-1.03 1.56V21h-4v-.09A1.7 1.7 0 0 0 9 19.35a1.7 1.7 0 0 0-1.88.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-1.56-1.03H3v-4h.09A1.7 1.7 0 0 0 4.65 9a1.7 1.7 0 0 0-.34-1.88l-.06-.06 2.83-2.83.06.06A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1.03-1.56V3h4v.09A1.7 1.7 0 0 0 15 4.65a1.7 1.7 0 0 0 1.88-.34l.06-.06 2.83 2.83-.06.06A1.7 1.7 0 0 0 19.4 9c.12.37.2.77.2 1.18v3.64c0 .41-.08.81-.2 1.18Z" />
      </>
    ),
    shield: (
      <>
        <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z" />
        <path d="m9 12 2 2 4-4" />
      </>
    ),
  };

  return (
    <svg
      aria-hidden="true"
      className="app-icon"
      fill="none"
      viewBox="0 0 24 24"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="1.8"
    >
      {paths[name]}
    </svg>
  );
}

function NavButton({
  active = false,
  disabled = false,
  icon,
  label,
  onClick,
}: {
  active?: boolean;
  disabled?: boolean;
  icon: IconName;
  label: string;
  onClick?: () => void;
}) {
  return (
    <button
      aria-label={disabled ? `${label}，即将推出` : label}
      aria-current={active ? "page" : undefined}
      className={`nav-item${active ? " nav-item--active" : ""}`}
      disabled={disabled}
      onClick={onClick}
      title={disabled ? `${label}（即将推出）` : label}
      type="button"
    >
      <Icon name={icon} />
      <span className="nav-item__label">{label}</span>
      {disabled ? <span className="nav-item__soon">即将推出</span> : null}
    </button>
  );
}

function Overview({
  catalog,
  copyError,
  copyFeedback,
  isNativeApp,
  isReady,
  isRestarting,
  onAddService,
  onCopy,
  onManageServices,
  onManageTokens,
  onRefreshEndpoints,
  onRefreshTodayUsage,
  onRestart,
  snapshot,
  todayUsage,
  tokenCatalog,
}: {
  catalog: EndpointCatalog;
  copyError: string | null;
  copyFeedback: string | null;
  isNativeApp: boolean;
  isReady: boolean;
  isRestarting: boolean;
  onAddService: () => void;
  onCopy: (value: string, label: string) => void;
  onManageServices: () => void;
  onManageTokens: () => void;
  onRefreshEndpoints: () => void;
  onRefreshTodayUsage: () => void;
  onRestart: () => void;
  snapshot: AppSnapshot | null;
  todayUsage: TodayUsageState;
  tokenCatalog: AccessTokenCatalog;
}) {
  const capabilities = snapshot?.capabilities ?? null;
  const conversionEngine = capabilities?.conversion_engine;
  const statusTone = snapshot ? phaseTone(snapshot.phase) : "neutral";
  const statusLabel = snapshot ? phaseLabel(snapshot.phase) : "连接中";
  const enabledCount = catalog.items.filter((endpoint) => endpoint.enabled).length;
  const catalogUnknown =
    catalog.status === "blocked" && catalog.items.length === 0;
  const tokensUnknown =
    tokenCatalog.status === "blocked" && tokenCatalog.items.length === 0;
  const inferenceURL = snapshot?.ready?.inference_url ?? "";

  return (
    <div className="overview-page">
      <section className={`core-strip core-strip--${statusTone}`}>
        <div className="core-strip__status">
          <span className={`dot dot--${statusTone}`} aria-hidden="true" />
          <div>
            <strong>{isReady ? "Core 正常运行" : statusLabel}</strong>
            <span>
              {isReady
                ? `本地网关已在 ${inferenceURL} 监听`
                : snapshot?.last_error ?? "正在建立本地连接…"}
            </span>
          </div>
        </div>
        {isNativeApp && !isReady ? (
          <button
            className="btn-secondary"
            disabled={isRestarting || snapshot?.phase === "stopping"}
            onClick={onRestart}
            type="button"
          >
            {isRestarting ? "重启中…" : "重启 Core"}
          </button>
        ) : null}
      </section>

      <section className="usage-card" aria-labelledby="today-usage-heading">
        <div className="usage-card__header">
          <div>
            <span className="section-kicker">本机汇总</span>
            <h2 id="today-usage-heading">今日用量</h2>
          </div>
          <div className="usage-card__actions">
            {todayUsage.status === "loading" ? (
              <span className="usage-pending-badge">统计中…</span>
            ) : todayUsage.summary?.capped ? (
              <span className="usage-pending-badge">仅统计最近 1000 条</span>
            ) : null}
            <button
              className="btn-secondary"
              disabled={!isReady || todayUsage.status === "loading"}
              onClick={onRefreshTodayUsage}
              type="button"
            >
              刷新
            </button>
          </div>
        </div>
        <div className="usage-card__metrics">
          {(
            [
              ["请求数", todayUsage.summary?.requests],
              ["输入 Token", todayUsage.summary?.input_tokens],
              ["输出 Token", todayUsage.summary?.output_tokens],
              ["总 Token", todayUsage.summary?.total_tokens],
              ["预估费用", null],
            ] as const
          ).map(([label, value]) => (
            <div key={label}>
              <span>{label}</span>
              <strong>
                {label === "预估费用"
                  ? "—"
                  : value === undefined || todayUsage.status === "blocked"
                    ? "—"
                    : todayUsage.status === "error"
                      ? "—"
                      : value.toLocaleString()}
              </strong>
            </div>
          ))}
        </div>
        {todayUsage.status === "error" && todayUsage.error ? (
          <p className="inline-error" role="alert">
            {todayUsage.error}
          </p>
        ) : (
          <p>按本机时区统计今天已记录的请求与 Token。</p>
        )}
      </section>

      <div className="overview-grid">
        <section className="workspace-card access-card" aria-labelledby="access-heading">
          <div className="workspace-card__header">
            <div>
              <span className="section-kicker">本地接入</span>
              <h2 id="access-heading">连接 AstrLink</h2>
            </div>
            <span className={`compact-status compact-status--${statusTone}`}>
              <span className={`dot dot--${statusTone}`} aria-hidden="true" />
              {isReady ? "可用" : statusLabel}
            </span>
          </div>

          <div className="copy-field">
            <div>
              <span>API 地址</span>
              <code>{inferenceURL || "等待 Core 就绪"}</code>
            </div>
            <button
              aria-label="复制 API 地址"
              disabled={!inferenceURL}
              onClick={() => onCopy(inferenceURL, "API 地址")}
              type="button"
            >
              <Icon name="copy" />
            </button>
          </div>

          <div className="access-summary">
            <div>
              <span>访问令牌</span>
              <strong>{tokensUnknown ? "—" : tokenCatalog.items.length}</strong>
              <small>
                {tokensUnknown
                  ? "Core 就绪后读取"
                  : tokenCatalog.items.length
                    ? "个客户端令牌"
                    : "尚未创建令牌"}
              </small>
            </div>
            {tokenCatalog.stale ? (
              <span className="stale-badge">等待刷新</span>
            ) : null}
            <button
              className="btn-secondary"
              disabled={!isReady && tokenCatalog.items.length === 0}
              onClick={onManageTokens}
              type="button"
            >
              管理令牌
            </button>
          </div>
          {copyError ? (
            <p className="inline-error" role="alert">
              {copyError}
            </p>
          ) : copyFeedback ? (
            <p className="inline-notice" role="status">
              {copyFeedback}
            </p>
          ) : null}
          <button className="card-footer-action" onClick={onManageTokens} type="button">
            打开访问令牌
            <span aria-hidden="true">→</span>
          </button>
        </section>

        <section className="workspace-card services-summary" aria-labelledby="services-heading">
          <div className="workspace-card__header">
            <div>
              <span className="section-kicker">API 服务</span>
              <h2 id="services-heading">上游服务</h2>
            </div>
            {catalog.items.length > 0 ? (
              <button
                className="icon-text-button"
                disabled={!isReady}
                onClick={onAddService}
                type="button"
              >
                <Icon name="plus" />
                添加
              </button>
            ) : null}
          </div>

          <div className="service-counts">
            <div>
              <strong>{catalogUnknown ? "—" : catalog.items.length}</strong>
              <span>已配置</span>
            </div>
            <div>
              <strong>{catalogUnknown ? "—" : enabledCount}</strong>
              <span>已启用</span>
            </div>
            {catalogUnknown ? (
              <span className="stale-badge">等待 Core</span>
            ) : catalog.stale ? (
              <span className="stale-badge">等待刷新</span>
            ) : null}
          </div>

          {catalogUnknown ? (
            <div className="compact-empty">
              <p>Core 就绪后显示已配置服务。</p>
              <span>当前没有可用的服务目录数据。</span>
            </div>
          ) : catalog.status === "error" && catalog.items.length === 0 ? (
            <div className="compact-empty compact-empty--error">
              <p>{catalog.error ?? "无法读取 API 服务。"}</p>
              <button className="btn-secondary" onClick={onRefreshEndpoints} type="button">
                重试
              </button>
            </div>
          ) : catalog.status === "loading" && catalog.items.length === 0 ? (
            <div className="compact-empty">
              <p>正在读取已配置服务…</p>
            </div>
          ) : catalog.items.length === 0 ? (
            <div className="compact-empty">
              <p>尚未添加 API 服务。</p>
              <span>添加一个 new-api 或 API 订阅即可开始使用。</span>
              <button className="btn-primary" disabled={!isReady} onClick={onAddService} type="button">
                添加服务
              </button>
            </div>
          ) : (
            <div className="service-preview-list">
              {catalog.items.slice(0, 3).map((endpoint) => (
                <button
                  className="service-preview"
                  key={endpoint.id}
                  onClick={onManageServices}
                  type="button"
                >
                  <span className={`dot dot--${endpoint.enabled ? "positive" : "neutral"}`} />
                  <span>
                    <strong>{endpoint.name}</strong>
                    <code>{endpoint.base_url}</code>
                  </span>
                  <span>{endpoint.capabilities.length} 项能力</span>
                </button>
              ))}
            </div>
          )}

          <button className="card-footer-action" onClick={onManageServices} type="button">
            管理全部服务
            <span aria-hidden="true">→</span>
          </button>
        </section>
      </div>

      <details className="system-details">
        <summary>
          <span>
            <strong>系统详情</strong>
            <small>版本、监听地址与协议能力</small>
          </span>
          <span aria-hidden="true">›</span>
        </summary>
        <div className="system-details__body">
          <dl className="detail-grid">
            <div>
              <dt>桌面版本</dt>
              <dd>{snapshot?.app_version ?? "未知"}</dd>
            </div>
            <div>
              <dt>Core 版本</dt>
              <dd>{snapshot?.version?.core_version ?? snapshot?.ready?.core_version ?? "待定"}</dd>
            </div>
            <div>
              <dt>进程</dt>
              <dd>{snapshot?.pid ? `PID ${snapshot.pid}` : "未运行"}</dd>
            </div>
            <div>
              <dt>控制监听</dt>
              <dd>
                <code>{snapshot?.ready?.control_url ?? "未分配"}</code>
              </dd>
            </div>
            <div>
              <dt>控制合同</dt>
              <dd>{snapshot?.version?.control_api_version ?? "待握手"}</dd>
            </div>
            <div>
              <dt>转换引擎</dt>
              <dd>
                {conversionEngine?.available
                  ? `${conversionEngine.name} ${conversionEngine.version ?? ""}`.trim()
                  : "RelayKit 未启用"}
              </dd>
            </div>
          </dl>
          <div className="protocol-detail">
            <span>协议能力</span>
            <div>
              {capabilities?.protocols.length ? (
                capabilities.protocols.map((protocol) => (
                  <code key={protocol.id}>{protocol.id}</code>
                ))
              ) : (
                <small>Core 握手完成后显示。</small>
              )}
            </div>
          </div>
        </div>
      </details>
    </div>
  );
}

export default function App() {
  const [snapshot, setSnapshot] = useState<AppSnapshot | null>(null);
  const [isRestarting, setIsRestarting] = useState(false);
  const [catalog, setCatalog] = useState<EndpointCatalog>(emptyCatalog);
  const [tokenCatalog, setTokenCatalog] =
    useState<AccessTokenCatalog>(emptyTokenCatalog);
  const [todayUsage, setTodayUsage] = useState<TodayUsageState>(emptyTodayUsage);
  const [page, setPage] = useState<WorkspacePage>({ kind: "overview" });
  const [editorDirty, setEditorDirty] = useState(false);
  const [copyFeedback, setCopyFeedback] = useState<string | null>(null);
  const [copyError, setCopyError] = useState<string | null>(null);
  const requestGateRef = useRef<RequestGate | null>(null);
  const catalogGeneration = useRef(0);
  const tokenCatalogGeneration = useRef(0);
  const todayUsageGeneration = useRef(0);
  const copyFeedbackTimer = useRef<number | null>(null);
  requestGateRef.current ??= new RequestGate();
  const requestGate = requestGateRef.current;

  const refreshCore = useCallback(async () => {
    const generation = requestGate.begin();
    if (generation === null) return;
    try {
      const next = await getCoreStatus();
      if (requestGate.isCurrent(generation)) setSnapshot(next);
    } catch (error) {
      if (requestGate.isCurrent(generation)) {
        setSnapshot((current) =>
          failedSnapshot(current, messageOf(error, "无法查询 Core 状态。")),
        );
      }
    }
  }, [requestGate]);

  useEffect(() => {
    let cancelled = false;
    let timer: number | null = null;

    const poll = async () => {
      await refreshCore();
      if (!cancelled) timer = window.setTimeout(() => void poll(), 1_500);
    };

    void poll();
    return () => {
      cancelled = true;
      requestGate.invalidate();
      if (timer !== null) window.clearTimeout(timer);
    };
  }, [refreshCore, requestGate]);

  const handleRestart = async () => {
    const generation = requestGate.beginExclusive();
    if (generation === null) return;
    setIsRestarting(true);
    try {
      const next = await restartCore();
      if (requestGate.isCurrent(generation)) setSnapshot(next);
    } catch (error) {
      if (requestGate.isCurrent(generation)) {
        setSnapshot((current) =>
          failedSnapshot(current, messageOf(error, "无法重启 Core。")),
        );
      }
    } finally {
      if (requestGate.endExclusive(generation)) setIsRestarting(false);
    }
  };

  const isReady = snapshot?.phase === "ready";
  const isNativeApp = snapshot !== null && snapshot.phase !== "unavailable";
  const coreSessionKey =
    isReady && snapshot?.ready
      ? `${snapshot.pid ?? "none"}|${snapshot.ready.control_url}|${snapshot.ready.inference_url}`
      : null;

  const refreshEndpoints = useCallback(async () => {
    const generation = catalogGeneration.current + 1;
    catalogGeneration.current = generation;
    if (!isReady) {
      setCatalog((current) => ({
        ...current,
        status: "blocked",
        error: null,
        stale: current.items.length > 0,
      }));
      return;
    }

    setCatalog((current) => ({
      ...current,
      status: "loading",
      error: null,
      stale: current.items.length > 0,
    }));
    try {
      const result = await listEndpoints();
      if (catalogGeneration.current === generation) {
        setCatalog({
          status: "ready",
          items: result.items,
          error: null,
          stale: false,
        });
      }
    } catch (error) {
      if (catalogGeneration.current === generation) {
        setCatalog((current) => ({
          ...current,
          status: "error",
          error: messageOf(error, "无法读取 API 服务。"),
          stale: current.items.length > 0,
        }));
      }
    }
  }, [isReady]);

  useEffect(() => {
    if (!isReady) {
      catalogGeneration.current += 1;
      setCatalog((current) => ({
        ...current,
        status: "blocked",
        error: null,
        stale: current.items.length > 0,
      }));
      return;
    }
    void refreshEndpoints();
  }, [coreSessionKey, isReady, refreshEndpoints]);

  const refreshAccessTokens = useCallback(async () => {
    const generation = tokenCatalogGeneration.current + 1;
    tokenCatalogGeneration.current = generation;
    if (!isReady) {
      setTokenCatalog((current) => ({
        ...current,
        status: "blocked",
        error: null,
        stale: current.items.length > 0,
      }));
      return;
    }

    setTokenCatalog((current) => ({
      ...current,
      status: "loading",
      error: null,
      stale: current.items.length > 0,
    }));
    try {
      const result = await listAccessTokens();
      if (tokenCatalogGeneration.current === generation) {
        setTokenCatalog({
          status: "ready",
          items: result.items,
          error: null,
          stale: false,
        });
      }
    } catch (error) {
      if (tokenCatalogGeneration.current === generation) {
        setTokenCatalog((current) => ({
          ...current,
          status: "error",
          error: messageOf(error, "无法读取访问令牌。"),
          stale: current.items.length > 0,
        }));
      }
    }
  }, [isReady]);

  useEffect(() => {
    if (!isReady) {
      tokenCatalogGeneration.current += 1;
      setTokenCatalog((current) => ({
        ...current,
        status: "blocked",
        error: null,
        stale: current.items.length > 0,
      }));
      return;
    }
    void refreshAccessTokens();
  }, [coreSessionKey, isReady, refreshAccessTokens]);

  const refreshTodayUsage = useCallback(async () => {
    const generation = todayUsageGeneration.current + 1;
    todayUsageGeneration.current = generation;
    if (!isReady) {
      setTodayUsage({ status: "blocked", summary: null, error: null });
      return;
    }

    setTodayUsage((current) => ({
      status: "loading",
      summary: current.summary,
      error: null,
    }));
    try {
      const accumulated: RequestRecord[] = [];
      let cursor: string | undefined;
      let nextCursor: string | null = null;
      for (let pageIndex = 0; pageIndex < 5; pageIndex += 1) {
        const page = await listRequestRecords({
          from: startOfTodayIso(new Date()),
          limit: 200,
          ...(cursor ? { cursor } : {}),
        });
        if (todayUsageGeneration.current !== generation) return;
        accumulated.push(...page.items);
        nextCursor = page.next_cursor;
        if (!nextCursor) break;
        cursor = nextCursor;
      }
      if (todayUsageGeneration.current !== generation) return;
      setTodayUsage({
        status: "ready",
        summary: aggregateTodayUsage(accumulated, nextCursor !== null),
        error: null,
      });
    } catch (error) {
      if (todayUsageGeneration.current === generation) {
        setTodayUsage((current) => ({
          status: "error",
          summary: current.summary,
          error: messageOf(error, "无法统计今日用量。"),
        }));
      }
    }
  }, [isReady]);

  useEffect(() => {
    if (!isReady) {
      todayUsageGeneration.current += 1;
      setTodayUsage({ status: "blocked", summary: null, error: null });
      return;
    }
    void refreshTodayUsage();
  }, [coreSessionKey, isReady, refreshTodayUsage]);

  useEffect(
    () => () => {
      if (copyFeedbackTimer.current !== null) {
        window.clearTimeout(copyFeedbackTimer.current);
      }
    },
    [],
  );

  const copyValue = async (value: string, label: string) => {
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      setCopyError(null);
      setCopyFeedback(`${label}已复制`);
      if (copyFeedbackTimer.current !== null) {
        window.clearTimeout(copyFeedbackTimer.current);
      }
      copyFeedbackTimer.current = window.setTimeout(
        () => setCopyFeedback(null),
        1_800,
      );
    } catch {
      setCopyFeedback(null);
      setCopyError("无法自动复制，请手动选择文本。");
    }
  };

  const navigate = useCallback(
    (next: WorkspacePage) => {
      const leavingEditor =
        (page.kind === "create" || page.kind === "edit") &&
        (next.kind !== page.kind ||
          (page.kind === "edit" &&
            next.kind === "edit" &&
            next.endpointId !== page.endpointId));
      if (
        leavingEditor &&
        editorDirty &&
        !window.confirm("当前修改尚未保存，确定要离开吗？")
      ) {
        return;
      }
      if (leavingEditor) setEditorDirty(false);
      setPage(next);
    },
    [editorDirty, page],
  );

  const handleEndpointSaved = (endpoint: Endpoint) => {
    setCatalog((current) => {
      const existingIndex = current.items.findIndex((item) => item.id === endpoint.id);
      const items =
        existingIndex === -1
          ? [...current.items, endpoint]
          : current.items.map((item) => (item.id === endpoint.id ? endpoint : item));
      return { status: "ready", items, error: null, stale: false };
    });
    setEditorDirty(false);
    setPage({ kind: "list" });
  };

  const handleEndpointRemoved = (endpointId: string) => {
    setCatalog((current) => ({
      status: "ready",
      items: current.items.filter((endpoint) => endpoint.id !== endpointId),
      error: null,
      stale: false,
    }));
  };

  const handleTokenCreated = (token: AccessTokenSummary) => {
    setTokenCatalog((current) => ({
      status: "ready",
      items: [
        token,
        ...current.items.filter((item) => item.id !== token.id),
      ],
      error: null,
      stale: false,
    }));
  };

  const handleTokenDeleted = (tokenId: string) => {
    setTokenCatalog((current) => ({
      status: "ready",
      items: current.items.filter((token) => token.id !== tokenId),
      error: null,
      stale: false,
    }));
  };

  const pageTitle =
    page.kind === "overview"
      ? "概览"
      : page.kind === "tokens"
        ? "访问令牌"
        : page.kind === "safety"
          ? "安全策略"
          : page.kind === "records"
            ? "请求记录"
            : page.kind === "list"
              ? "API 服务"
              : page.kind === "create"
                ? "添加服务"
                : "编辑服务";
  const serviceSectionActive =
    page.kind === "list" || page.kind === "create" || page.kind === "edit";
  const statusTone = snapshot ? phaseTone(snapshot.phase) : "neutral";
  const statusLabel = snapshot ? phaseLabel(snapshot.phase) : "连接中";
  const protocols = useMemo(
    () => snapshot?.capabilities?.protocols ?? [],
    [snapshot?.capabilities?.protocols],
  );

  return (
    <div className="desktop-shell">
      <aside className="sidebar">
        <div className="brand">
          <img
            className="brand__mark"
            src={astrlinkLogo}
            alt=""
            width={32}
            height={32}
            aria-hidden="true"
          />
          <span className="brand__name">AstrLink</span>
        </div>

        <nav className="sidebar__nav" aria-label="主要导航">
          <span className="nav-group-label">工作区</span>
          <NavButton
            active={page.kind === "overview"}
            icon="home"
            label="概览"
            onClick={() => navigate({ kind: "overview" })}
          />
          <NavButton
            active={serviceSectionActive}
            icon="server"
            label="API 服务"
            onClick={() => navigate({ kind: "list" })}
          />
          <NavButton
            active={page.kind === "tokens"}
            icon="key"
            label="访问令牌"
            onClick={() => navigate({ kind: "tokens" })}
          />
          <NavButton
            active={page.kind === "safety"}
            icon="shield"
            label="安全策略"
            onClick={() => navigate({ kind: "safety" })}
          />
          <NavButton
            active={page.kind === "records"}
            icon="activity"
            label="请求记录"
            onClick={() => navigate({ kind: "records" })}
          />

          <span className="nav-group-label nav-group-label--secondary">即将提供</span>
          <NavButton disabled icon="route" label="路由与模型" />
          <NavButton disabled icon="settings" label="设置" />
        </nav>

        <div className={`sidebar-status sidebar-status--${statusTone}`}>
          <span className={`dot dot--${statusTone}`} aria-hidden="true" />
          <span>
            <strong>Core</strong>
            <small>{statusLabel}</small>
          </span>
        </div>
      </aside>

      <div className="app-surface">
        <header className="workspace-header">
          <div>
            {page.kind === "create" || page.kind === "edit" ? (
              <button
                aria-label="返回服务列表"
                className="workspace-header__back"
                onClick={() => navigate({ kind: "list" })}
                type="button"
              >
                <Icon name="chevron-left" />
              </button>
            ) : null}
            <h1>{pageTitle}</h1>
          </div>
          <div className={`status-pill status-pill--${statusTone}`}>
            <span className={`dot dot--${statusTone}`} aria-hidden="true" />
            {statusLabel}
          </div>
        </header>

        <main className={`workspace workspace--${page.kind}`}>
          {page.kind === "overview" ? (
            <Overview
              catalog={catalog}
              copyError={copyError}
              copyFeedback={copyFeedback}
              isNativeApp={isNativeApp}
              isReady={isReady}
              isRestarting={isRestarting}
              onAddService={() => navigate({ kind: "create" })}
              onCopy={(value, label) => void copyValue(value, label)}
              onManageServices={() => navigate({ kind: "list" })}
              onManageTokens={() => navigate({ kind: "tokens" })}
              onRefreshEndpoints={() => void refreshEndpoints()}
              onRefreshTodayUsage={() => void refreshTodayUsage()}
              onRestart={() => void handleRestart()}
              snapshot={snapshot}
              todayUsage={todayUsage}
              tokenCatalog={tokenCatalog}
            />
          ) : page.kind === "tokens" ? (
            <AccessTokenManager
              catalog={tokenCatalog}
              coreSessionKey={coreSessionKey}
              isReady={isReady}
              onRefresh={() => void refreshAccessTokens()}
              onTokenCreated={handleTokenCreated}
              onTokenDeleted={handleTokenDeleted}
            />
          ) : page.kind === "safety" ? (
            <SafetyPolicy
              coreSessionKey={coreSessionKey}
              isReady={isReady}
            />
          ) : page.kind === "records" ? (
            <RequestRecords
              coreSessionKey={coreSessionKey}
              endpoints={catalog.items}
              isReady={isReady}
            />
          ) : (
            <EndpointManager
              catalogError={catalog.error}
              catalogStatus={catalog.status}
              endpoints={catalog.items}
              isReady={isReady}
              onDirtyChange={setEditorDirty}
              onEndpointRemoved={handleEndpointRemoved}
              onEndpointSaved={handleEndpointSaved}
              onRefresh={() => void refreshEndpoints()}
              onViewChange={(next) => navigate(next)}
              protocols={protocols}
              view={page}
            />
          )}
        </main>
      </div>
    </div>
  );
}
