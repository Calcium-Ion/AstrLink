import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Activity,
  Copy,
  House,
  KeyRound,
  Plus,
  Route,
  Server,
  Settings,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react";

import { AppShell } from "@/components/AppShell";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
  | "copy"
  | "home"
  | "key"
  | "plus"
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
  copy: Copy,
  home: House,
  key: KeyRound,
  plus: Plus,
  route: Route,
  server: Server,
  settings: Settings,
  shield: ShieldCheck,
};

function Icon({ name }: { name: IconName }) {
  const IconComponent = icons[name];
  return (
    <IconComponent
      aria-hidden="true"
      className="size-[18px] shrink-0"
      strokeWidth={1.8}
    />
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
        "min-h-10 w-full justify-start gap-2.5 rounded-[10px] px-2.5 text-[12.5px] font-semibold text-text-secondary hover:bg-accent hover:text-foreground max-[900px]:justify-center max-[900px]:px-0",
        active && "bg-accent text-accent-foreground",
      )}
      disabled={disabled}
      onClick={onClick}
      title={disabled ? `${label}（即将推出）` : label}
      type="button"
      variant="ghost"
    >
      <Icon name={icon} />
      <span className="overflow-hidden text-ellipsis whitespace-nowrap max-[900px]:hidden">{label}</span>
      {disabled ? <Badge className="ml-auto text-[8.5px] max-[900px]:hidden" variant="secondary">即将推出</Badge> : null}
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

  return (
    <div className="grid gap-3.5 [&_[data-slot=page-header]]:mb-0">
      <PageHeader
        description="查看本地网关状态、今日用量与接入配置。"
        eyebrow="工作区"
        title="概览"
      />
      <Card
        className={cn(
          "flex min-h-[62px] flex-row items-center justify-between gap-3.5 rounded-[14px] px-4 py-3 shadow-[0_4px_16px_color-mix(in_srgb,var(--foreground)_3%,transparent)]",
          statusTone === "negative" && "border-destructive/25 bg-danger-wash",
        )}
      >
        <div className="flex min-w-0 items-center gap-[11px]">
          <StatusDot tone={statusTone} />
          <div className="flex min-w-0 flex-col">
            <strong className="text-[12.5px]">{isReady ? "Core 正常运行" : statusLabel}</strong>
            <span className="mt-[3px] overflow-hidden text-[10.5px] leading-[1.45] text-text-secondary text-ellipsis whitespace-nowrap">
              {isReady
                ? `本地网关已在 ${inferenceURL} 监听`
                : snapshot?.last_error ?? "正在建立本地连接…"}
            </span>
          </div>
        </div>
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
      </Card>

      <Card className="min-w-0 gap-0 rounded-2xl px-[18px] pt-[15px] pb-[13px] shadow-[var(--shadow-card)]" aria-labelledby="today-usage-heading">
        <div className="flex items-start justify-between gap-3">
          <div>
            <SectionKicker>本机汇总</SectionKicker>
            <h2 className="mt-1 text-[17px] tracking-[-0.02em]" id="today-usage-heading">今日用量</h2>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {todayUsage.status === "loading" ? (
              <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">统计中…</Badge>
            ) : todayUsage.summary?.capped ? (
              <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">仅统计最近 1000 条</Badge>
            ) : null}
            <Button
              variant="outline"
              disabled={!isReady || todayUsage.status === "loading"}
              onClick={onRefreshTodayUsage}
              type="button"
            >
              刷新
            </Button>
          </div>
        </div>
        <div className="mt-[11px] grid grid-cols-5 max-[720px]:grid-cols-2">
          {(
            [
              ["请求数", todayUsage.summary?.requests],
              ["输入 Token", todayUsage.summary?.input_tokens],
              ["输出 Token", todayUsage.summary?.output_tokens],
              ["总 Token", todayUsage.summary?.total_tokens],
              ["预估费用", null],
            ] as const
          ).map(([label, value]) => (
            <div className="min-w-0 border-l px-3 first:border-l-0 first:pl-0 max-[720px]:border-l-0 max-[720px]:px-0 max-[720px]:py-2" key={label}>
              <span className="block overflow-hidden text-[9px] text-muted-foreground text-ellipsis whitespace-nowrap">{label}</span>
              <strong className="mt-1 block text-xl tracking-[-0.04em]">
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
          <p className="mt-2 text-[9.5px] text-danger-foreground" role="alert">
            {todayUsage.error}
          </p>
        ) : (
          <p className="mt-2 text-[9px] text-muted-foreground">按本机时区统计今天已记录的请求与 Token。</p>
        )}
      </Card>

      <div className="grid grid-cols-2 items-stretch gap-3.5 max-[720px]:grid-cols-1">
        <Card className="min-h-[255px] min-w-0 gap-0 rounded-2xl p-[18px] shadow-[var(--shadow-card)]" aria-labelledby="access-heading">
          <div className="flex min-h-[42px] items-start justify-between gap-3">
            <div>
              <SectionKicker>本地接入</SectionKicker>
              <h2 className="mt-[5px] text-[17px] tracking-[-0.02em]" id="access-heading">连接 AstrLink</h2>
            </div>
            <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-muted px-2 py-[5px] text-[9.5px] font-semibold text-text-secondary">
              <StatusDot tone={statusTone} />
              {isReady ? "可用" : statusLabel}
            </span>
          </div>

          <div className="mt-[18px] flex min-w-0 items-center gap-2.5 rounded-[11px] border bg-muted px-[13px] py-3">
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="text-[9.5px] font-semibold text-muted-foreground">API 地址</span>
              <code className="mt-[5px] overflow-hidden text-[11.5px] text-ellipsis whitespace-nowrap">{inferenceURL || "等待 Core 就绪"}</code>
            </div>
            <Button
              aria-label="复制 API 地址"
              className="size-[31px]"
              disabled={!inferenceURL}
              onClick={() => onCopy(inferenceURL, "API 地址")}
              type="button"
              size="icon-sm"
              variant="outline"
            >
              <Icon name="copy" />
            </Button>
          </div>

          <div className="mt-[13px] flex min-w-0 items-center gap-2.5 rounded-[11px] border bg-card px-[13px] py-3">
            <div className="flex min-w-0 flex-1 items-baseline gap-1.5">
              <span className="text-[9.5px] text-muted-foreground">访问令牌</span>
              <strong className="text-xl tracking-[-0.04em]">{tokensUnknown ? "—" : tokenCatalog.items.length}</strong>
              <small className="text-[9.5px] text-muted-foreground">
                {tokensUnknown
                  ? "Core 就绪后读取"
                  : tokenCatalog.items.length
                    ? "个客户端令牌"
                    : "尚未创建令牌"}
              </small>
            </div>
            {tokenCatalog.stale ? (
              <Badge className="ml-auto bg-warning-wash text-warning-foreground" variant="secondary">等待刷新</Badge>
            ) : null}
            <Button
              variant="outline"
              disabled={!isReady && tokenCatalog.items.length === 0}
              onClick={onManageTokens}
              type="button"
            >
              管理令牌
            </Button>
          </div>
          {copyError ? (
            <p className="mt-2 text-[9.5px] text-danger-foreground" role="alert">
              {copyError}
            </p>
          ) : copyFeedback ? (
            <p className="mt-2 text-[9.5px] text-success-foreground" role="status">
              {copyFeedback}
            </p>
          ) : null}
          <Button className="mt-auto h-auto w-full justify-between rounded-none border-t px-0.5 pt-2.5 text-[10.5px] font-bold no-underline hover:bg-transparent hover:no-underline" onClick={onManageTokens} type="button" variant="link">
            打开访问令牌
            <span aria-hidden="true">→</span>
          </Button>
        </Card>

        <Card className="min-h-[255px] min-w-0 gap-0 rounded-2xl p-[18px] shadow-[var(--shadow-card)]" aria-labelledby="services-heading">
          <div className="flex min-h-[42px] items-start justify-between gap-3">
            <div>
              <SectionKicker>API 服务</SectionKicker>
              <h2 className="mt-[5px] text-[17px] tracking-[-0.02em]" id="services-heading">上游服务</h2>
            </div>
            {catalog.items.length > 0 ? (
              <Button
                disabled={!isReady}
                onClick={onAddService}
                type="button"
                size="sm"
                variant="outline"
              >
                <Icon name="plus" />
                添加
              </Button>
            ) : null}
          </div>

          <div className="mt-[13px] flex items-center gap-6 border-b py-[11px]">
            <div className="flex items-baseline gap-1.5">
              <strong className="text-xl tracking-[-0.04em]">{catalogUnknown ? "—" : catalog.items.length}</strong>
              <span className="text-[9.5px] text-muted-foreground">已配置</span>
            </div>
            <div className="flex items-baseline gap-1.5">
              <strong className="text-xl tracking-[-0.04em]">{catalogUnknown ? "—" : enabledCount}</strong>
              <span className="text-[9.5px] text-muted-foreground">已启用</span>
            </div>
            {catalogUnknown ? (
              <Badge className="ml-auto bg-warning-wash text-warning-foreground" variant="secondary">等待 Core</Badge>
            ) : catalog.stale ? (
              <Badge className="ml-auto bg-warning-wash text-warning-foreground" variant="secondary">等待刷新</Badge>
            ) : null}
          </div>

          {catalogUnknown ? (
            <div className="flex min-h-[142px] flex-1 flex-col items-center justify-center p-[18px] text-center text-muted-foreground">
              <p className="text-xs font-semibold text-text-secondary">Core 就绪后显示已配置服务。</p>
              <span className="mt-[5px] text-[9.5px]">当前没有可用的服务目录数据。</span>
            </div>
          ) : catalog.status === "error" && catalog.items.length === 0 ? (
            <div className="flex min-h-[142px] flex-1 flex-col items-center justify-center p-[18px] text-center text-muted-foreground">
              <p className="text-xs text-danger-foreground">{catalog.error ?? "无法读取 API 服务。"}</p>
              <Button className="mt-3.5" variant="outline" onClick={onRefreshServices} type="button">
                重试
              </Button>
            </div>
          ) : catalog.status === "loading" && catalog.items.length === 0 ? (
            <div className="flex min-h-[142px] flex-1 items-center justify-center p-[18px] text-center">
              <p className="text-xs font-semibold text-text-secondary">正在读取已配置服务…</p>
            </div>
          ) : catalog.items.length === 0 ? (
            <div className="flex min-h-[142px] flex-1 flex-col items-center justify-center p-[18px] text-center text-muted-foreground">
              <p className="text-xs font-semibold text-text-secondary">尚未添加 API 服务。</p>
              <span className="mt-[5px] text-[9.5px]">添加一个 new-api 或 API 订阅即可开始使用。</span>
              <Button className="mt-3.5" disabled={!isReady} onClick={onAddService} type="button">
                添加服务
              </Button>
            </div>
          ) : (
            <div className="mt-2 grid gap-[3px]">
              {catalog.items.slice(0, 3).map((service) => (
                <Button
                  className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-[9px] rounded-lg bg-transparent px-[7px] py-2 text-left text-foreground hover:bg-muted"
                  key={service.id}
                  onClick={onManageServices}
                  type="button"
                  variant="ghost"
                >
                  <StatusDot tone={service.enabled ? "positive" : "neutral"} />
                  <span className="flex min-w-0 flex-col">
                    <strong className="overflow-hidden text-[11px] text-ellipsis whitespace-nowrap">{service.name}</strong>
                    <code className="mt-0.5 overflow-hidden text-[8.5px] text-muted-foreground text-ellipsis whitespace-nowrap">
                      {service.http?.base_url ??
                        (service.subscription?.account_hint
                          ? `OpenAI 账户 ${service.subscription.account_hint}`
                          : "OpenAI Codex OAuth")}
                    </code>
                  </span>
                  <span className="text-[8.5px] whitespace-nowrap text-muted-foreground">{service.capabilities.length} 项能力</span>
                </Button>
              ))}
            </div>
          )}

          <Button className="mt-auto h-auto w-full justify-between rounded-none border-t px-0.5 pt-2.5 text-[10.5px] font-bold no-underline hover:bg-transparent hover:no-underline" onClick={onManageServices} type="button" variant="link">
            管理全部服务
            <span aria-hidden="true">→</span>
          </Button>
        </Card>
      </div>

      <details className="group overflow-hidden rounded-[13px] border bg-card">
        <summary className="flex min-h-[51px] cursor-pointer list-none items-center justify-between px-4 text-text-secondary [&::-webkit-details-marker]:hidden">
          <span className="flex flex-col">
            <strong className="text-[11.5px] text-foreground">系统详情</strong>
            <small className="mt-0.5 text-[9px] text-muted-foreground">版本、监听地址与协议能力</small>
          </span>
          <span aria-hidden="true" className="text-xl transition-transform group-open:rotate-90">›</span>
        </summary>
        <div className="border-t px-4 py-[15px]">
          <dl className="grid grid-cols-3 gap-x-4 gap-y-2.5 max-[720px]:grid-cols-2">
            <div className="min-w-0">
              <dt className="text-[9px] font-semibold text-muted-foreground">桌面版本</dt>
              <dd className="mt-1 overflow-hidden text-[10.5px] text-text-secondary text-ellipsis whitespace-nowrap">{snapshot?.app_version ?? "未知"}</dd>
            </div>
            <div className="min-w-0">
              <dt className="text-[9px] font-semibold text-muted-foreground">Core 版本</dt>
              <dd className="mt-1 overflow-hidden text-[10.5px] text-text-secondary text-ellipsis whitespace-nowrap">{snapshot?.version?.core_version ?? snapshot?.ready?.core_version ?? "待定"}</dd>
            </div>
            <div className="min-w-0">
              <dt className="text-[9px] font-semibold text-muted-foreground">进程</dt>
              <dd className="mt-1 overflow-hidden text-[10.5px] text-text-secondary text-ellipsis whitespace-nowrap">{snapshot?.pid ? `PID ${snapshot.pid}` : "未运行"}</dd>
            </div>
            <div className="min-w-0">
              <dt className="text-[9px] font-semibold text-muted-foreground">控制监听</dt>
              <dd className="mt-1 overflow-hidden text-[10.5px] text-text-secondary text-ellipsis whitespace-nowrap">
                <code>{snapshot?.ready?.control_url ?? "未分配"}</code>
              </dd>
            </div>
            <div className="min-w-0">
              <dt className="text-[9px] font-semibold text-muted-foreground">控制合同</dt>
              <dd className="mt-1 overflow-hidden text-[10.5px] text-text-secondary text-ellipsis whitespace-nowrap">{snapshot?.version?.control_api_version ?? "待握手"}</dd>
            </div>
            <div className="min-w-0">
              <dt className="text-[9px] font-semibold text-muted-foreground">转换引擎</dt>
              <dd className="mt-1 overflow-hidden text-[10.5px] text-text-secondary text-ellipsis whitespace-nowrap">
                {conversionEngine?.available
                  ? `${conversionEngine.name} ${conversionEngine.version ?? ""}`.trim()
                  : "RelayKit 未启用"}
              </dd>
            </div>
          </dl>
          <div className="mt-3.5 grid gap-2 border-t pt-3">
            <span className="text-[9px] font-semibold text-muted-foreground">协议能力</span>
            <div className="flex flex-wrap gap-[5px]">
              {capabilities?.protocols.length ? (
                capabilities.protocols.map((protocol) => (
                  <code className="rounded-[5px] bg-muted px-1.5 py-1 text-[8.5px] text-text-secondary" key={protocol.id}>{protocol.id}</code>
                ))
              ) : (
                <small className="text-[9px] text-muted-foreground">Core 握手完成后显示。</small>
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
      <aside className="sticky top-0 z-20 flex h-screen min-h-[600px] flex-col border-r bg-sidebar px-3.5 pt-[calc(var(--window-chrome-height)+18px)] pb-3.5 backdrop-blur-[18px] max-[900px]:px-2 max-[900px]:pt-[calc(var(--window-chrome-height)+16px)] max-[900px]:pb-3">
        <div className="flex items-center gap-2.5 px-2 pb-[18px] max-[900px]:justify-center max-[900px]:px-0">
          <img
            className="block size-8 shrink-0"
            src={astrlinkLogo}
            alt=""
            width={32}
            height={32}
            aria-hidden="true"
          />
          <span className="overflow-hidden text-base font-[750] tracking-[-0.02em] whitespace-nowrap max-[900px]:hidden">AstrLink</span>
        </div>

        <nav className="flex flex-1 flex-col gap-1" aria-label="主要导航" data-slot="sidebar-navigation">
          <span className="px-[11px] pt-[7px] pb-[5px] text-[10px] font-[750] tracking-[0.11em] text-muted-foreground uppercase max-[900px]:hidden">工作区</span>
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

          <span className="mt-[18px] px-[11px] pt-[7px] pb-[5px] text-[10px] font-[750] tracking-[0.11em] text-muted-foreground uppercase max-[900px]:mx-[9px] max-[900px]:mt-3.5 max-[900px]:mb-2 max-[900px]:h-px max-[900px]:bg-border max-[900px]:p-0 max-[900px]:text-transparent">系统</span>
          <NavButton
            active={page.kind === "settings"}
            icon="settings"
            label="设置"
            onClick={() => navigate({ kind: "settings" })}
          />
        </nav>

        <div
          aria-label={`Core ${statusLabel}`}
          className="mt-3.5 flex items-center gap-[9px] rounded-[11px] border bg-card px-[11px] py-2.5 text-text-secondary max-[900px]:justify-center max-[900px]:px-0 max-[900px]:py-3"
          title={`Core ${statusLabel}`}
        >
          <StatusDot tone={statusTone} />
          <span className="flex min-w-0 flex-col max-[900px]:hidden">
            <strong className="text-[11.5px] text-foreground">Core</strong>
            <small className="mt-px overflow-hidden text-[9.5px] text-ellipsis whitespace-nowrap">{statusLabel}</small>
          </span>
        </div>
      </aside>
      }
    >
        <main
          className={cn(
            "min-w-0 px-[clamp(18px,3vw,30px)] pt-[calc(var(--window-chrome-height)+clamp(18px,3vw,30px))] pb-[clamp(18px,3vw,30px)] [container-type:inline-size] [container-name:workspace-surface] max-h-[680px]:pt-[calc(var(--window-chrome-height)+14px)] max-h-[680px]:pb-3.5",
            page.kind !== "overview" &&
              "flex min-h-screen flex-col",
            ["list", "tokens", "records"].includes(page.kind) &&
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
