import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Activity,
  Bot,
  Home as House,
  Key as KeyRound,
  Route,
  Server,
  Settings,
  ShieldCheck,
  type AnimatedIcon,
} from "@/components/icons";

import { AppShell } from "@/components/AppShell";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { StatusDot } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import {
  getCoreStatus,
  listAccessTokens,
  getUsageSummary,
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
import { Overview, type ServiceCatalog } from "./Overview";
import { i18n, useT } from "./i18n";
import { RequestGate } from "./request-gate";
import { RequestRecords } from "./RequestRecords";
import { RouteManager } from "./RouteManager";
import { SafetyPolicy } from "./SafetyPolicy";
import { AgentDebugSettings } from "./AgentDebugSettings";
import { SettingsCenter } from "./SettingsCenter";
import { ServiceManager, type ServiceManagerView } from "./ServiceManager";
import type { Service } from "./service-model";
import {
  DEFAULT_USAGE_RANGE_PRESET,
  resolveUsageWindow,
  type UsageRangePreset,
  type UsageState,
} from "./usage-range";

type WorkspacePage =
  | { kind: "overview" }
  | { kind: "tokens" }
  | { kind: "safety" }
  | { kind: "records" }
  | { kind: "routing" }
  | { kind: "agentTools" }
  | { kind: "settings" }
  | ServiceManagerView;

type IconName =
  | "activity"
  | "bot"
  | "home"
  | "key"
  | "route"
  | "server"
  | "settings"
  | "shield";

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

const blockedUsage: UsageState = {
  status: "blocked",
  summary: null,
  error: null,
};

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

const icons: Record<IconName, AnimatedIcon> = {
  activity: Activity,
  bot: Bot,
  home: House,
  key: KeyRound,
  route: Route,
  server: Server,
  settings: Settings,
  shield: ShieldCheck,
};

function Icon({ className, name }: { className?: string; name: IconName }) {
  const IconComponent = icons[name];
  return (
    <IconComponent aria-hidden="true" className={cn("size-4 shrink-0", className)} strokeWidth={1.6} />
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
      aria-label={disabled ? i18n.t("common.comingSoon", { label }) : label}
      aria-current={active ? "page" : undefined}
      className={cn(
        "relative h-9 w-full justify-start gap-2.5 rounded-md px-2 text-sm font-medium text-text-secondary hover:bg-accent hover:text-accent-foreground max-[900px]:justify-center max-[900px]:px-0",
        // Active state is a tide-coloured left rule plus a flat wash. This bar
        // is the only place the logo's tide colour appears in the UI.
        active &&
          "bg-accent font-semibold text-accent-foreground before:absolute before:inset-y-1 before:-left-2 before:w-0.5 before:rounded-full before:bg-tide max-[900px]:before:hidden",
      )}
      disabled={disabled}
      onClick={onClick}
      title={disabled ? i18n.t("common.comingSoonTitle", { label }) : label}
      type="button"
      variant="ghost"
    >
      <Icon className="size-5" name={icon} />
      <span className="overflow-hidden text-ellipsis whitespace-nowrap max-[900px]:hidden">{label}</span>
      {disabled ? <Badge className="ml-auto max-[900px]:hidden" variant="secondary">{i18n.t("common.comingSoonBadge")}</Badge> : null}
    </Button>
  );
}

export default function App() {
  const t = useT();
  const [snapshot, setSnapshot] = useState<AppSnapshot | null>(null);
  const [isRestarting, setIsRestarting] = useState(false);
  const [catalog, setCatalog] = useState<ServiceCatalog>(emptyCatalog);
  const [tokenCatalog, setTokenCatalog] =
    useState<AccessTokenCatalog>(emptyTokenCatalog);
  const [usage, setUsage] = useState<UsageState>(blockedUsage);
  const [usagePreset, setUsagePreset] = useState<UsageRangePreset>(
    DEFAULT_USAGE_RANGE_PRESET,
  );
  const [page, setPage] = useState<WorkspacePage>({ kind: "overview" });
  const [pendingPage, setPendingPage] = useState<WorkspacePage | null>(null);
  const [copyFeedback, setCopyFeedback] = useState<string | null>(null);
  const [copyError, setCopyError] = useState<string | null>(null);
  const editorDirtyRef = useRef(false);
  const requestGateRef = useRef<RequestGate | null>(null);
  const catalogGeneration = useRef(0);
  const tokenCatalogGeneration = useRef(0);
  const usageGeneration = useRef(0);
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
          failedSnapshot(current, messageOf(error, i18n.t("app.queryFailed"))),
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
          failedSnapshot(current, messageOf(error, i18n.t("app.restartFailed"))),
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
          error: messageOf(error, i18n.t("overview.readServicesFailed")),
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
          error: messageOf(error, i18n.t("app.readTokensFailed")),
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

  const refreshUsage = useCallback(async () => {
    const generation = usageGeneration.current + 1;
    usageGeneration.current = generation;
    if (!isReady) {
      setUsage(blockedUsage);
      return;
    }

    // Keep the previous summary visible while a wider range loads, so
    // switching presets never blanks the panel.
    setUsage((current) => ({
      status: "loading",
      summary: current.summary,
      error: null,
    }));
    try {
      const usageWindow = resolveUsageWindow(usagePreset, new Date());
      const summary = await getUsageSummary(usageWindow);
      if (usageGeneration.current !== generation) return;
      setUsage({
        status: "ready",
        summary,
        error: null,
      });
    } catch (error) {
      if (usageGeneration.current === generation) {
        setUsage((current) => ({
          status: "error",
          summary: current.summary,
          error: messageOf(error, i18n.t("app.usageFailed")),
        }));
      }
    }
  }, [isReady, usagePreset]);

  useEffect(() => {
    if (!isReady) {
      usageGeneration.current += 1;
      setUsage(blockedUsage);
      return;
    }
    void refreshUsage();
  }, [coreSessionKey, isReady, refreshUsage]);

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
      setCopyFeedback(i18n.t("copy.copiedNamed", { label }));
      if (copyFeedbackTimer.current !== null) {
        window.clearTimeout(copyFeedbackTimer.current);
      }
      copyFeedbackTimer.current = window.setTimeout(
        () => setCopyFeedback(null),
        1_800,
      );
    } catch {
      setCopyFeedback(null);
      setCopyError(i18n.t("copy.manualSelect"));
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
  const statusLabel = snapshot ? phaseLabel(snapshot.phase) : t("core.phase.connecting");
  const protocols = useMemo(
    () => snapshot?.capabilities?.protocols ?? [],
    [snapshot?.capabilities?.protocols],
  );

  return (
    <AppShell
      sidebar={
      <aside className="flex h-full min-h-0 flex-col border-r bg-sidebar px-3 pt-[calc(var(--window-chrome-height)+16px)] pb-3 max-[900px]:px-2 max-[900px]:pb-2.5">
        <div className="flex items-center gap-3 px-2 pb-5 max-[900px]:justify-center max-[900px]:px-0">
          <img
            className="block size-8 shrink-0"
            src={astrlinkLogo}
            alt=""
            width={32}
            height={32}
            aria-hidden="true"
          />
          <span className="overflow-hidden text-xl font-semibold tracking-tight whitespace-nowrap max-[900px]:hidden">AstrLink</span>
        </div>

        <nav className="flex flex-1 flex-col gap-1" aria-label={t("nav.main")} data-slot="sidebar-navigation">
          <span className="px-2 pb-1.5 text-xs font-medium tracking-[0.08em] text-muted-foreground uppercase max-[900px]:hidden">{t("nav.workspace")}</span>
          <NavButton
            active={page.kind === "overview"}
            icon="home"
            label={t("nav.overview")}
            onClick={() => navigate({ kind: "overview" })}
          />
          <NavButton
            active={serviceSectionActive}
            icon="server"
            label={t("nav.services")}
            onClick={() => navigate({ kind: "list" })}
          />
          <NavButton
            active={page.kind === "tokens"}
            icon="key"
            label={t("nav.tokens")}
            onClick={() => navigate({ kind: "tokens" })}
          />
          <NavButton
            active={page.kind === "safety"}
            icon="shield"
            label={t("nav.safety")}
            onClick={() => navigate({ kind: "safety" })}
          />
          <NavButton
            active={page.kind === "records"}
            icon="activity"
            label={t("nav.records")}
            onClick={() => navigate({ kind: "records" })}
          />
          <NavButton
            active={page.kind === "routing"}
            icon="route"
            label={t("nav.routing")}
            onClick={() => navigate({ kind: "routing" })}
          />

          <span className="mt-4 px-2 pb-1.5 text-xs font-medium tracking-[0.08em] text-muted-foreground uppercase max-[900px]:mx-2 max-[900px]:mt-3 max-[900px]:mb-2 max-[900px]:h-px max-[900px]:bg-border max-[900px]:p-0 max-[900px]:text-transparent">{t("nav.system")}</span>
          <NavButton
            active={page.kind === "agentTools"}
            icon="bot"
            label={t("nav.agentTools")}
            onClick={() => navigate({ kind: "agentTools" })}
          />
          <NavButton
            active={page.kind === "settings"}
            icon="settings"
            label={t("nav.settings")}
            onClick={() => navigate({ kind: "settings" })}
          />
        </nav>

        <div
          aria-label={t("nav.gatewayStatus", { status: statusLabel })}
          className="mt-3 flex items-center gap-2 border-t px-2 pt-3 text-text-secondary max-[900px]:justify-center max-[900px]:px-0"
          title={t("nav.gatewayStatus", { status: statusLabel })}
        >
          <StatusDot tone={statusTone} />
          <span className="flex min-w-0 items-baseline gap-1.5 max-[900px]:hidden">
            <strong className="text-sm font-medium text-foreground">{t("nav.gateway")}</strong>
            <small className="overflow-hidden text-xs text-ellipsis whitespace-nowrap">{statusLabel}</small>
          </span>
        </div>
      </aside>
      }
    >
        <main
          className={cn(
            // One measure for every page: content stops at 1080px and stays
            // centred, so a single row of data never spans the whole window.
            "@container/workspace-surface mx-auto h-full min-h-0 w-full max-w-[1080px] min-w-0 px-8 pt-[calc(var(--window-chrome-height)+28px)] pb-8 max-[900px]:px-5 max-h-[680px]:pt-[calc(var(--window-chrome-height)+18px)] max-h-[680px]:pb-5",
            page.kind !== "overview" && "flex flex-col",
            ["list", "create", "edit", "tokens", "records", "safety", "routing", "agentTools"].includes(
              page.kind,
            )
              ? "overflow-hidden"
              : "overflow-y-auto overscroll-none",
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
              onOpenService={(serviceId) => navigate({ kind: "edit", serviceId })}
              onRefreshServices={() => void refreshServices()}
              onRefreshUsage={() => void refreshUsage()}
              onRestart={() => void handleRestart()}
              onUsagePresetChange={setUsagePreset}
              snapshot={snapshot}
              tokenCatalog={tokenCatalog}
              usage={usage}
              usagePreset={usagePreset}
            />
          ) : page.kind === "tokens" ? (
            <AccessTokenManager
              catalog={tokenCatalog}
              coreSessionKey={coreSessionKey}
              inferenceURL={snapshot?.ready?.inference_url ?? ""}
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
          ) : page.kind === "agentTools" ? (
            <AgentDebugSettings />
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
              conversionEngine={snapshot?.capabilities?.conversion_engine}
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
        cancelLabel={t("common.continueEditing")}
        confirmLabel={t("common.discardAndLeave")}
        description={
          <p>
              {t("app.unsavedBody")}
          </p>
        }
        onCancel={() => setPendingPage(null)}
        onConfirm={confirmPendingNavigation}
        open={pendingPage !== null}
        title={t("common.discardUnsaved")}
      />
    </AppShell>
  );
}
