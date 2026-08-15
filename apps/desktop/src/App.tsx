import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Activity,
  Check,
  ChevronRight,
  Copy,
  House,
  KeyRound,
  Plus,
  RefreshCw,
  Route,
  Server,
  Settings,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react";

import { AppShell } from "@/components/AppShell";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
import { Panel, PanelHeader } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import {
  getCoreStatus,
  listAccessTokens,
  listRequestRecords,
  listServices,
  restartCore,
} from "./bridge";
import {
  AccessTokenManager,
  type AccessTokenCatalog,
} from "./AccessTokenManager";
import type { AccessTokenSummary } from "./access-token-model";
import astrlinkLogo from "./assets/astrlink-logo.svg";
import {
  failedSnapshot,
  phaseLabel,
  phaseTone,
  type AppSnapshot,
} from "./core-model";
import { PageHeader } from "./PageHeader";
import { RequestGate } from "./request-gate";
import { RequestRecords } from "./RequestRecords";
import { RouteManager } from "./RouteManager";
import { SafetyPolicy } from "./SafetyPolicy";
import { SettingsCenter } from "./SettingsCenter";
import {
  ServiceManager,
  type ServiceCatalogStatus,
  type ServiceManagerView,
} from "./ServiceManager";
import type { Service } from "./service-model";
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
  | { kind: "routing" }
  | { kind: "settings" }
  | ServiceManagerView;

type TodayUsageState = {
  status: "blocked" | "loading" | "ready" | "error";
  summary: TodayUsageSummary | null;
  error: string | null;
};
type IconName =
  | "activity"
  | "check"
  | "copy"
  | "home"
  | "key"
  | "plus"
  | "refresh"
  | "route"
  | "server"
  | "settings"
  | "shield";

interface ServiceCatalog {
  status: ServiceCatalogStatus;
  items: Service[];
  error: string | null;
  stale: boolean;
}

const emptyCatalog: ServiceCatalog = {
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

const icons: Record<IconName, LucideIcon> = {
  activity: Activity,
  check: Check,
  copy: Copy,
  home: House,
  key: KeyRound,
  plus: Plus,
  refresh: RefreshCw,
  route: Route,
  server: Server,
  settings: Settings,
  shield: ShieldCheck,
};

function formatOverviewMetric(
  value: number | undefined,
  status: TodayUsageState["status"],
): string {
  if (value === undefined || status === "blocked" || status === "error") {
    return "—";
  }
  return value.toLocaleString();
}

function OverviewStatusChip({
  isReady,
  statusLabel,
  statusTone,
}: {
  isReady: boolean;
  statusLabel: string;
  statusTone: StatusTone;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-micro font-medium",
        statusTone === "negative" &&
          "border-destructive/25 bg-danger-wash text-danger-foreground",
        statusTone === "positive" &&
          "border-success/20 bg-success-wash text-success-foreground",
        statusTone === "pending" &&
          "border-warning/25 bg-warning-wash text-warning-foreground",
        (statusTone === "neutral") &&
          "border-border bg-card text-text-secondary",
      )}
    >
      <StatusDot tone={statusTone} />
      {isReady ? "Core 正常运行" : statusLabel}
    </span>
  );
}

function Icon({ name }: { name: IconName }) {
  const IconComponent = icons[name];
  return (
    <IconComponent aria-hidden="true" className="size-4 shrink-0" strokeWidth={1.6} />
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
    <Button
      aria-label={disabled ? `${label}，即将推出` : label}
      aria-current={active ? "page" : undefined}
      className={cn(
        "relative h-7.5 w-full justify-start gap-2 rounded-md px-2 text-xs font-medium text-text-secondary hover:bg-accent hover:text-accent-foreground max-[900px]:justify-center max-[900px]:px-0",
        // Active state is a tide-coloured left rule plus a flat wash. This bar
        // is the only place the logo's tide colour appears in the UI.
        active &&
          "bg-accent font-semibold text-accent-foreground before:absolute before:inset-y-1 before:-left-2 before:w-0.5 before:rounded-full before:bg-tide max-[900px]:before:hidden",
      )}
      disabled={disabled}
      onClick={onClick}
      title={disabled ? `${label}（即将推出）` : label}
      type="button"
      variant="ghost"
    >
      <Icon name={icon} />
      <span className="overflow-hidden text-ellipsis whitespace-nowrap max-[900px]:hidden">{label}</span>
      {disabled ? <Badge className="ml-auto max-[900px]:hidden" variant="secondary">即将推出</Badge> : null}
    </Button>
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
  onRefreshServices,
  onRefreshTodayUsage,
  onRestart,
  snapshot,
  todayUsage,
  tokenCatalog,
}: {
  catalog: ServiceCatalog;
  copyError: string | null;
  copyFeedback: string | null;
  isNativeApp: boolean;
  isReady: boolean;
  isRestarting: boolean;
  onAddService: () => void;
  onCopy: (value: string, label: string) => void;
  onManageServices: () => void;
  onManageTokens: () => void;
  onRefreshServices: () => void;
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
  const enabledCount = catalog.items.filter((service) => service.enabled).length;
  const catalogUnknown =
    catalog.status === "blocked" && catalog.items.length === 0;
  const tokensUnknown =
    tokenCatalog.status === "blocked" && tokenCatalog.items.length === 0;
  const inferenceURL = snapshot?.ready?.inference_url ?? "";
  const apiCopied = copyFeedback === "API 地址已复制";
  const usageMetrics = [
    ["请求数", todayUsage.summary?.requests],
    ["输入 Token", todayUsage.summary?.input_tokens],
    ["输出 Token", todayUsage.summary?.output_tokens],
    ["总 Token", todayUsage.summary?.total_tokens],
    ["预估费用", null],
  ] as const;
  const systemDetails: Array<[string, string]> = [
    ["桌面版本", snapshot?.app_version ?? "未知"],
    [
      "Core 版本",
      snapshot?.version?.core_version ?? snapshot?.ready?.core_version ?? "待定",
    ],
    ["进程", snapshot?.pid ? `PID ${snapshot.pid}` : "未运行"],
    ["控制监听", snapshot?.ready?.control_url ?? "未分配"],
    ["控制合同", snapshot?.version?.control_api_version ?? "待握手"],
    [
      "转换引擎",
      conversionEngine?.available
        ? `${conversionEngine.name} ${conversionEngine.version ?? ""}`.trim()
        : "RelayKit 未启用",
    ],
  ];

  return (
    <div className="grid gap-4 [&_[data-slot=page-header]]:mb-4">
      <PageHeader
        actions={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <OverviewStatusChip
              isReady={isReady}
              statusLabel={statusLabel}
              statusTone={statusTone}
            />
            {isNativeApp && !isReady ? (
              <Button
                variant="outline"
                disabled={isRestarting || snapshot?.phase === "stopping"}
                onClick={onRestart}
                type="button"
              >
                {isRestarting ? "重启中…" : "重启 Core"}
              </Button>
            ) : null}
          </div>
        }
        description="查看本地网关状态、今日用量与接入配置。"
        eyebrow="工作区"
        title="概览"
      />

      <div className="grid grid-cols-[minmax(0,1.4fr)_minmax(240px,0.72fr)] items-stretch gap-4 max-[860px]:grid-cols-1">
        <Panel
          aria-labelledby="access-heading"
          className={cn(
            "flex flex-col",
            !isReady && statusTone === "negative" && "border-destructive/25",
          )}
        >
          <PanelHeader
            actions={
              <span className="inline-flex items-center gap-1.5 text-micro text-text-secondary">
                <StatusDot tone={statusTone} />
                {isReady ? "可用" : statusLabel}
              </span>
            }
          >
            <SectionKicker>本地接入</SectionKicker>
            <h2 className="mt-1 text-base font-semibold tracking-tight" id="access-heading">
              连接 AstrLink
            </h2>
          </PanelHeader>

          <div
            className={cn(
              "flex min-w-0 flex-1 items-center gap-3 border-b px-4 py-3.5",
              apiCopied && "bg-success-wash",
            )}
          >
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
                API 地址
              </span>
              <code className="mt-1 overflow-hidden font-mono text-base font-medium tracking-tight text-ellipsis whitespace-nowrap">
                {inferenceURL || "等待 Core 就绪"}
              </code>
              <span className="mt-1 overflow-hidden text-xs text-text-secondary text-ellipsis whitespace-nowrap">
                {isReady
                  ? `本地网关已在 ${inferenceURL} 监听`
                  : snapshot?.last_error ?? "正在建立本地连接…"}
              </span>
            </div>
            <Button
              aria-label="复制 API 地址"
              disabled={!inferenceURL}
              onClick={() => onCopy(inferenceURL, "API 地址")}
              size="icon"
              type="button"
              variant="outline"
            >
              <Icon name={apiCopied ? "check" : "copy"} />
            </Button>
          </div>

          <div className="flex min-w-0 items-center gap-2.5 px-4 py-3">
            <div className="flex min-w-0 flex-1 items-baseline gap-1.5">
              <strong className="text-lg tracking-tight tabular-nums">
                {tokensUnknown ? "—" : tokenCatalog.items.length}
              </strong>
              <span className="text-xs text-text-secondary">
                {tokensUnknown
                  ? "个访问令牌，Core 就绪后读取"
                  : tokenCatalog.items.length
                    ? "个客户端访问令牌"
                    : "尚未创建访问令牌"}
              </span>
            </div>
            {tokenCatalog.stale ? (
              <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
                等待刷新
              </Badge>
            ) : null}
            <Button
              disabled={!isReady && tokenCatalog.items.length === 0}
              onClick={onManageTokens}
              size="sm"
              type="button"
              variant={
                !tokensUnknown && tokenCatalog.items.length === 0
                  ? "default"
                  : "outline"
              }
            >
              管理令牌
            </Button>
          </div>
          {copyError ? (
            <p className="border-t px-4 py-2 text-xs text-danger-foreground" role="alert">
              {copyError}
            </p>
          ) : copyFeedback ? (
            <p className="border-t px-4 py-2 text-xs text-success-foreground" role="status">
              {copyFeedback}
            </p>
          ) : null}
        </Panel>

        <Panel aria-labelledby="today-usage-heading" className="flex flex-col">
          <PanelHeader
            actions={
              <Button
                disabled={!isReady || todayUsage.status === "loading"}
                onClick={onRefreshTodayUsage}
                size="icon-sm"
                type="button"
                variant="ghost"
                aria-label="刷新"
              >
                <RefreshCw
                  aria-hidden="true"
                  className={cn(
                    "motion-reduce:animate-none",
                    todayUsage.status === "loading" && "animate-spin",
                  )}
                />
                <span className="sr-only">刷新</span>
              </Button>
            }
          >
            <SectionKicker>本机汇总</SectionKicker>
            <h2 className="mt-1 text-base font-semibold tracking-tight" id="today-usage-heading">
              今日用量
            </h2>
          </PanelHeader>

          {todayUsage.status === "loading" || todayUsage.summary?.capped ? (
            <div className="flex flex-wrap items-center gap-1.5 border-b px-4 py-2">
              <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
                {todayUsage.status === "loading" ? "统计中…" : "仅统计最近 1000 条"}
              </Badge>
            </div>
          ) : null}

          <div className="flex-1">
            {usageMetrics.map(([label, value]) => (
              <DataRow className="justify-between py-2" key={label}>
                <span className="overflow-hidden text-xs text-text-secondary text-ellipsis whitespace-nowrap">
                  {label}
                </span>
                <strong className="text-base tracking-tight tabular-nums">
                  {label === "预估费用"
                    ? "—"
                    : formatOverviewMetric(value ?? undefined, todayUsage.status)}
                </strong>
              </DataRow>
            ))}
          </div>

          {todayUsage.status === "error" && todayUsage.error ? (
            <p className="border-t px-4 py-2.5 text-xs text-danger-foreground" role="alert">
              {todayUsage.error}
            </p>
          ) : (
            <p className="border-t px-4 py-2.5 text-micro text-muted-foreground">
              按本机时区统计今天已记录的请求与 Token。
            </p>
          )}
        </Panel>
      </div>

      <Panel aria-labelledby="services-heading">
        <PanelHeader
          actions={
            <>
              <div className="flex items-baseline gap-4 pr-1 max-[560px]:hidden">
                <div className="flex items-baseline gap-1.5">
                  <strong className="text-base tracking-tight tabular-nums">
                    {catalogUnknown ? "—" : catalog.items.length}
                  </strong>
                  <span className="text-micro text-muted-foreground">已配置</span>
                </div>
                <div className="flex items-baseline gap-1.5">
                  <strong className="text-base tracking-tight tabular-nums">
                    {catalogUnknown ? "—" : enabledCount}
                  </strong>
                  <span className="text-micro text-muted-foreground">已启用</span>
                </div>
              </div>
              {catalogUnknown ? (
                <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
                  等待 Core
                </Badge>
              ) : catalog.stale ? (
                <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
                  等待刷新
                </Badge>
              ) : catalog.items.length > 0 ? (
                <Button
                  disabled={!isReady}
                  onClick={onAddService}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  <Icon name="plus" />
                  添加
                </Button>
              ) : null}
            </>
          }
        >
          <SectionKicker>API 服务</SectionKicker>
          <h2 className="mt-1 text-base font-semibold tracking-tight" id="services-heading">
            上游服务
          </h2>
        </PanelHeader>

        {catalogUnknown ? (
          <div className="flex flex-col items-center justify-center gap-1 border-b px-4 py-8 text-center">
            <p className="text-sm text-text-secondary">Core 就绪后显示已配置服务。</p>
            <span className="text-xs text-muted-foreground">
              当前没有可用的服务目录数据。
            </span>
          </div>
        ) : catalog.status === "error" && catalog.items.length === 0 ? (
          <div className="flex flex-col items-center justify-center gap-3 border-b px-4 py-8 text-center">
            <p className="text-sm text-danger-foreground">
              {catalog.error ?? "无法读取 API 服务。"}
            </p>
            <Button size="sm" variant="outline" onClick={onRefreshServices} type="button">
              重试
            </Button>
          </div>
        ) : catalog.status === "loading" && catalog.items.length === 0 ? (
          <div className="flex items-center justify-center border-b px-4 py-8 text-center">
            <p className="text-sm text-text-secondary">正在读取已配置服务…</p>
          </div>
        ) : catalog.items.length === 0 ? (
          <div className="flex flex-col items-center justify-center gap-3 border-b px-4 py-8 text-center">
            <div className="grid gap-1">
              <p className="text-sm text-text-secondary">尚未添加 API 服务。</p>
              <span className="text-xs text-muted-foreground">
                添加一个 new-api 或 API 订阅即可开始使用。
              </span>
            </div>
            <Button disabled={!isReady} onClick={onAddService} size="sm" type="button">
              添加服务
            </Button>
          </div>
        ) : (
          <div>
            {catalog.items.slice(0, 3).map((service) => (
              <Button
                className="grid h-auto min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-none border-b bg-transparent px-4 py-2.5 text-left font-normal text-foreground hover:bg-muted"
                key={service.id}
                onClick={onManageServices}
                type="button"
                variant="ghost"
              >
                <StatusDot tone={service.enabled ? "positive" : "neutral"} />
                <span className="flex min-w-0 flex-col">
                  <strong className="overflow-hidden text-sm font-medium text-ellipsis whitespace-nowrap">
                    {service.name}
                  </strong>
                  <code className="mt-0.5 overflow-hidden font-mono text-micro text-muted-foreground text-ellipsis whitespace-nowrap">
                    {service.http?.base_url ??
                      (service.subscription?.account_hint
                        ? `OpenAI 账户 ${service.subscription.account_hint}`
                        : "OpenAI Codex OAuth")}
                  </code>
                </span>
                <span className="text-micro whitespace-nowrap text-muted-foreground">
                  {service.capabilities.length} 项能力
                </span>
              </Button>
            ))}
            {catalog.items.length > 3 ? (
              <p className="border-b px-4 py-2 text-xs text-muted-foreground">
                还有 {catalog.items.length - 3} 个服务
              </p>
            ) : null}
          </div>
        )}

        <Button
          className="h-auto w-full justify-between rounded-none px-4 py-2.5 text-xs font-medium text-text-secondary no-underline hover:bg-muted hover:text-foreground hover:no-underline"
          onClick={onManageServices}
          type="button"
          variant="link"
        >
          管理全部服务
          <span aria-hidden="true">→</span>
        </Button>
      </Panel>

      <Panel asChild>
        <details className="group">
          <summary className="flex cursor-pointer list-none items-center justify-between px-4 py-3 text-text-secondary [&::-webkit-details-marker]:hidden">
            <span className="flex min-w-0 flex-col">
              <strong className="text-sm font-medium text-foreground">系统详情</strong>
              <small className="mt-0.5 text-xs text-muted-foreground">
                版本、监听地址与协议能力
              </small>
            </span>
            <ChevronRight
              aria-hidden="true"
              className="size-4 shrink-0 transition-transform group-open:rotate-90"
            />
          </summary>
          <div className="border-t px-4 py-4">
            <dl className="grid grid-cols-3 gap-x-6 gap-y-4 max-[720px]:grid-cols-2">
              {systemDetails.map(([term, detail]) => (
                <div className="min-w-0" key={term}>
                  <dt className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
                    {term}
                  </dt>
                  <dd className="mt-1 overflow-hidden text-xs text-text-secondary text-ellipsis whitespace-nowrap">
                    {detail}
                  </dd>
                </div>
              ))}
            </dl>
            <div className="mt-4 grid gap-2 border-t pt-4">
              <span className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
                协议能力
              </span>
              <div className="flex flex-wrap gap-1.5">
                {capabilities?.protocols.length ? (
                  capabilities.protocols.map((protocol) => (
                    <code
                      className="rounded-sm border px-1.5 py-0.5 font-mono text-micro text-text-secondary"
                      key={protocol.id}
                    >
                      {protocol.id}
                    </code>
                  ))
                ) : (
                  <small className="text-xs text-muted-foreground">
                    Core 握手完成后显示。
                  </small>
                )}
              </div>
            </div>
          </div>
        </details>
      </Panel>
    </div>
  );
}

export default function App() {
  const [snapshot, setSnapshot] = useState<AppSnapshot | null>(null);
  const [isRestarting, setIsRestarting] = useState(false);
  const [catalog, setCatalog] = useState<ServiceCatalog>(emptyCatalog);
  const [tokenCatalog, setTokenCatalog] =
    useState<AccessTokenCatalog>(emptyTokenCatalog);
  const [todayUsage, setTodayUsage] = useState<TodayUsageState>(emptyTodayUsage);
  const [page, setPage] = useState<WorkspacePage>({ kind: "overview" });
  const [pendingPage, setPendingPage] = useState<WorkspacePage | null>(null);
  const [copyFeedback, setCopyFeedback] = useState<string | null>(null);
  const [copyError, setCopyError] = useState<string | null>(null);
  const editorDirtyRef = useRef(false);
  const requestGateRef = useRef<RequestGate | null>(null);
  const catalogGeneration = useRef(0);
  const tokenCatalogGeneration = useRef(0);
  const todayUsageGeneration = useRef(0);
  const copyFeedbackTimer = useRef<number | null>(null);
  requestGateRef.current ??= new RequestGate();
  const requestGate = requestGateRef.current;
  const handleEditorDirtyChange = useCallback((dirty: boolean) => {
    editorDirtyRef.current = dirty;
  }, []);

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

  const refreshServices = useCallback(async () => {
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
      const result = await listServices();
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
    void refreshServices();
  }, [coreSessionKey, isReady, refreshServices]);

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
      const leavingServiceEditor =
        (page.kind === "create" || page.kind === "edit") &&
        (next.kind !== page.kind ||
          (page.kind === "edit" &&
            next.kind === "edit" &&
            next.serviceId !== page.serviceId));
      const leavingRouteEditor =
        page.kind === "routing" && next.kind !== "routing";
      const leavingSettings = page.kind === "settings" && next.kind !== "settings";
      const leavingEditor = leavingServiceEditor || leavingRouteEditor || leavingSettings;
      if (leavingEditor && editorDirtyRef.current) {
        setPendingPage(next);
        return;
      }
      if (leavingEditor) handleEditorDirtyChange(false);
      setPendingPage(null);
      setPage(next);
    },
    [handleEditorDirtyChange, page],
  );

  const confirmPendingNavigation = () => {
    if (pendingPage === null) return;
    setPage(pendingPage);
    setPendingPage(null);
    handleEditorDirtyChange(false);
  };

  const handleServiceSaved = (service: Service) => {
    setCatalog((current) => {
      const existingIndex = current.items.findIndex((item) => item.id === service.id);
      const items =
        existingIndex === -1
          ? [...current.items, service]
          : current.items.map((item) => (item.id === service.id ? service : item));
      return { status: "ready", items, error: null, stale: false };
    });
    handleEditorDirtyChange(false);
    setPage({ kind: "list" });
  };

  const handleServiceRemoved = (serviceId: string) => {
    setCatalog((current) => ({
      status: "ready",
      items: current.items.filter((service) => service.id !== serviceId),
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

  const serviceSectionActive =
    page.kind === "list" || page.kind === "create" || page.kind === "edit";
  const statusTone = snapshot ? phaseTone(snapshot.phase) : "neutral";
  const statusLabel = snapshot ? phaseLabel(snapshot.phase) : "连接中";
  const protocols = useMemo(
    () => snapshot?.capabilities?.protocols ?? [],
    [snapshot?.capabilities?.protocols],
  );

  return (
    <AppShell
      sidebar={
      <aside className="sticky top-0 z-20 flex h-screen min-h-[600px] flex-col border-r bg-sidebar px-3 pt-[calc(var(--window-chrome-height)+16px)] pb-3 max-[900px]:px-2 max-[900px]:pb-2.5">
        <div className="flex items-center gap-2 px-2 pb-5 max-[900px]:justify-center max-[900px]:px-0">
          <img
            className="block size-5 shrink-0"
            src={astrlinkLogo}
            alt=""
            width={20}
            height={20}
            aria-hidden="true"
          />
          <span className="overflow-hidden text-sm font-semibold tracking-tight whitespace-nowrap max-[900px]:hidden">AstrLink</span>
        </div>

        <nav className="flex flex-1 flex-col gap-0.5" aria-label="主要导航" data-slot="sidebar-navigation">
          <span className="px-2 pb-1.5 text-micro font-medium tracking-[0.08em] text-muted-foreground uppercase max-[900px]:hidden">工作区</span>
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
          <NavButton
            active={page.kind === "routing"}
            icon="route"
            label="路由与模型"
            onClick={() => navigate({ kind: "routing" })}
          />

          <span className="mt-4 px-2 pb-1.5 text-micro font-medium tracking-[0.08em] text-muted-foreground uppercase max-[900px]:mx-2 max-[900px]:mt-3 max-[900px]:mb-2 max-[900px]:h-px max-[900px]:bg-border max-[900px]:p-0 max-[900px]:text-transparent">系统</span>
          <NavButton
            active={page.kind === "settings"}
            icon="settings"
            label="设置"
            onClick={() => navigate({ kind: "settings" })}
          />
        </nav>

        <div
          aria-label={`Core ${statusLabel}`}
          className="mt-3 flex items-center gap-2 border-t px-2 pt-3 text-text-secondary max-[900px]:justify-center max-[900px]:px-0"
          title={`Core ${statusLabel}`}
        >
          <StatusDot tone={statusTone} />
          <span className="flex min-w-0 items-baseline gap-1.5 max-[900px]:hidden">
            <strong className="text-xs font-medium text-foreground">Core</strong>
            <small className="overflow-hidden text-micro text-ellipsis whitespace-nowrap">{statusLabel}</small>
          </span>
        </div>
      </aside>
      }
    >
        <main
          className={cn(
            // One measure for every page: content stops at 1080px and stays
            // centred, so a single row of data never spans the whole window.
            "@container/workspace-surface mx-auto w-full max-w-[1080px] min-w-0 px-8 pt-[calc(var(--window-chrome-height)+28px)] pb-8 max-[900px]:px-5 max-h-[680px]:pt-[calc(var(--window-chrome-height)+18px)] max-h-[680px]:pb-5",
            page.kind !== "overview" &&
              "flex min-h-screen flex-col",
            ["list", "tokens", "records", "safety"].includes(page.kind) &&
              "h-screen overflow-hidden",
          )}
          data-page={page.kind}
          data-slot="workspace"
        >
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
              onRefreshServices={() => void refreshServices()}
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
              services={catalog.items}
              isReady={isReady}
            />
          ) : page.kind === "routing" ? (
            <RouteManager
              coreSessionKey={coreSessionKey}
              services={catalog.items}
              isReady={isReady}
              onDirtyChange={handleEditorDirtyChange}
              onManageServices={() => navigate({ kind: "list" })}
              protocols={protocols}
            />
          ) : page.kind === "settings" ? (
            <SettingsCenter
              onCoreSnapshot={setSnapshot}
              onDirtyChange={handleEditorDirtyChange}
              snapshot={snapshot}
            />
          ) : (
            <ServiceManager
              catalogError={catalog.error}
              catalogStatus={catalog.status}
              isReady={isReady}
              onDirtyChange={handleEditorDirtyChange}
              onRefresh={() => void refreshServices()}
              onServiceRemoved={handleServiceRemoved}
              onServiceSaved={handleServiceSaved}
              onViewChange={(next) => navigate(next)}
              protocols={protocols}
              services={catalog.items}
              view={page}
            />
          )}
        </main>
      <ConfirmDialog
        cancelLabel="继续编辑"
        confirmLabel="放弃修改并离开"
        description={
          <p>
              当前配置尚未保存。离开此页面后，本次修改将会丢失。
          </p>
        }
        onCancel={() => setPendingPage(null)}
        onConfirm={confirmPendingNavigation}
        open={pendingPage !== null}
        title="放弃未保存的修改？"
      />
    </AppShell>
  );
}
