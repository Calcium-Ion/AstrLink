import { isWebEdition } from "./edition";
import { ConsoleLogout } from "./ConsoleEntry";
import { WebSettings } from "./WebSettings";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { isTauri } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import {
  Activity,
  CircleHelp,
  Bot,
  Home as House,
  Key as KeyRound,
  Route,
  Server,
  Settings,
  ShieldCheck,
  type AnimatedIcon,
} from "@/components/icons";

import { WorkspaceSnapshotProvider } from "./workspace-snapshots";
import { ValueTransition } from "./components/ValueTransition";
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
import { GettingStarted } from "./GettingStarted";
import { useOnboarding } from "./use-onboarding";
import { i18n, useT } from "./i18n";
import { RequestGate } from "./request-gate";
import { RequestRecords } from "./RequestRecords";
import { RouteManager } from "./RouteManager";
import { SafetyPolicy } from "./SafetyPolicy";
import { AgentDebugSettings } from "./AgentDebugSettings";
import type { AgentSkillId } from "./agent-install-model";
import { RawPasswordGate, useRawSetupNeeded } from "./RawSealingControls";
import { useRawSealingStatus } from "./use-raw-sealing-status";
import { LocalDataNotice } from "./LocalDataNotice";
import { SettingsCenter } from "./SettingsCenter";
import { About } from "./About";
import { useAppUpdates } from "./use-app-updates";
import { usePrivacyModelUpdate } from "./use-privacy-model-update";
import { updateNotice } from "./update-model";
import { toast } from "sonner";
import {
  ServiceKindPickerDialog,
  ServiceManager,
  type ServiceManagerView,
} from "./ServiceManager";
import { ProviderImportDialog } from "./ProviderImportDialog";
import { useProviderImport } from "./use-provider-import";
import { notify } from "./notify";
import type { Service } from "./service-model";
import { TRAY_NAVIGATE_EVENT } from "./tray-popover-window";
import {
  DEFAULT_USAGE_RANGE_PRESET,
  resolveUsageWindow,
  type UsageRangePreset,
  type UsageState,
} from "./usage-range";

type WorkspacePage =
  | { kind: "overview" }
  | { kind: "tokens" }
  | { kind: "safety"; view?: "models" }
  | { kind: "records"; tokenId?: string }
  | { kind: "routing" }
  | { kind: "agentTools"; preselectSkill?: AgentSkillId }
  | { kind: "settings" }
  | { kind: "about" }
  | ServiceManagerView;

type IconName =
  | "about"
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

/** How often the overview picks up new traffic while it is on screen. */
const OVERVIEW_REVALIDATE_MS = 60_000;

/**
 * A silent refresh shows no loading state and keeps the last good data when it
 * fails, so the panels never flash. It only runs over settled data: a visible
 * load or a starting gateway owns the panel until then.
 */
type RefreshOptions = { silent?: boolean };

function revalidatable(status: UsageState["status"]): boolean {
  return status === "ready" || status === "error";
}

/** Unchanged results keep their old object, so nothing re-renders. */
function sameData(current: unknown, next: unknown): boolean {
  return JSON.stringify(current) === JSON.stringify(next);
}

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

/** The pages the tray popover may open; anything else is ignored. */
export function trayNavigationTarget(kind: unknown): WorkspacePage | null {
  switch (kind) {
    case "overview":
    case "tokens":
    case "safety":
    case "records":
    case "routing":
    case "agentTools":
    case "settings":
    case "about":
    case "list":
      return { kind };
    default:
      return null;
  }
}

const icons: Record<IconName, AnimatedIcon> = {
  about: CircleHelp,
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
    <IconComponent
      aria-hidden="true"
      className={cn("size-4 shrink-0", className)}
      strokeWidth={1.6}
    />
  );
}

function NavButton({
  active = false,
  disabled = false,
  badge,
  icon,
  label,
  onClick,
}: {
  active?: boolean;
  disabled?: boolean;
  badge?: string;
  icon: IconName;
  label: string;
  onClick?: () => void;
}) {
  return (
    <Button
      aria-label={disabled ? i18n.t("common.comingSoon", { label }) : label}
      aria-current={active ? "page" : undefined}
      className={cn(
        "relative h-9 w-full justify-start gap-2.5 rounded-md px-2 text-sm font-medium text-text-secondary hover:bg-accent hover:text-accent-foreground max-[960px]:justify-center max-[960px]:px-0",
        // Active state is a tide-coloured left rule plus a flat wash. This bar
        // is the only place the logo's tide colour appears in the UI.
        active &&
          "bg-accent font-semibold text-accent-foreground before:absolute before:inset-y-1 before:-left-2 before:w-0.5 before:rounded-full before:bg-tide max-[960px]:before:hidden",
      )}
      disabled={disabled}
      onClick={onClick}
      title={disabled ? i18n.t("common.comingSoonTitle", { label }) : label}
      type="button"
      variant="ghost"
    >
      <Icon className="size-5" name={icon} />
      {badge ? (
        <span
          className="absolute right-1 top-1 size-2 rounded-full bg-primary"
          role="status"
          aria-label={badge}
          title={badge}
        />
      ) : null}
      <span className="overflow-hidden text-ellipsis whitespace-nowrap max-[960px]:hidden">
        {label}
      </span>
      {disabled ? (
        <Badge className="ml-auto max-[960px]:hidden" variant="secondary">
          {i18n.t("common.comingSoonBadge")}
        </Badge>
      ) : null}
    </Button>
  );
}

export default function App() {
  const t = useT();
  const updates = useAppUpdates();
  const notifiedUpdate = useRef<string | null>(null);
  const modelUpdate = usePrivacyModelUpdate();
  const notifiedModelUpdate = useRef<string | null>(null);
  const [snapshot, setSnapshot] = useState<AppSnapshot | null>(null);
  const [isRestarting, setIsRestarting] = useState(false);
  // Hiding the unreadable-credentials notice lasts until the app restarts.
  const [localDataDismissed, setLocalDataDismissed] = useState(false);
  const [catalog, setCatalog] = useState<ServiceCatalog>(emptyCatalog);
  const [tokenCatalog, setTokenCatalog] =
    useState<AccessTokenCatalog>(emptyTokenCatalog);
  const [usage, setUsage] = useState<UsageState>(blockedUsage);
  const [usagePreset, setUsagePreset] = useState<UsageRangePreset>(
    DEFAULT_USAGE_RANGE_PRESET,
  );
  const [page, setPage] = useState<WorkspacePage>({ kind: "overview" });
  const [pendingPage, setPendingPage] = useState<WorkspacePage | null>(null);
  // Overview and the guide add providers through the same type picker as the list.
  const [serviceKindPickerOpen, setServiceKindPickerOpen] = useState(false);
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
          failedSnapshot(
            current,
            messageOf(error, i18n.t("app.restartFailed")),
          ),
        );
      }
    } finally {
      if (requestGate.endExclusive(generation)) setIsRestarting(false);
    }
  };

  const isReady = snapshot?.phase === "ready";
  const isNativeApp =
    !isWebEdition && snapshot !== null && snapshot.phase !== "unavailable";
  const coreSessionKey =
    isReady && snapshot?.ready
      ? `${snapshot.pid ?? "none"}|${snapshot.ready.control_url}|${snapshot.ready.inference_url}`
      : null;
  const rawSealing = useRawSealingStatus(coreSessionKey, isReady);
  const rawSetupNeeded = useRawSetupNeeded(rawSealing.status);
  const onboarding = useOnboarding({
    isReady,
    catalog,
    tokenCatalog,
    usage,
    passwordReady: rawSealing.status !== null && !rawSetupNeeded,
  });

  // Silent refreshes read the rendered state to stay out of a visible load.
  const catalogRef = useRef(catalog);
  catalogRef.current = catalog;
  const tokenCatalogRef = useRef(tokenCatalog);
  tokenCatalogRef.current = tokenCatalog;
  const usageRef = useRef(usage);
  usageRef.current = usage;

  const refreshServices = useCallback(
    async ({ silent = false }: RefreshOptions = {}) => {
      if (silent && (!isReady || !revalidatable(catalogRef.current.status)))
        return;
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

      if (!silent) {
        setCatalog((current) => ({
          ...current,
          status: "loading",
          error: null,
          stale: current.items.length > 0,
        }));
      }
      try {
        const result = await listServices();
        if (catalogGeneration.current === generation) {
          setCatalog((current) =>
            current.status === "ready" &&
            !current.stale &&
            sameData(current.items, result.items)
              ? current
              : {
                  status: "ready",
                  items: result.items,
                  error: null,
                  stale: false,
                },
          );
        }
      } catch (error) {
        if (catalogGeneration.current === generation) {
          setCatalog((current) =>
            silent && current.status !== "loading"
              ? current
              : {
                  ...current,
                  status: "error",
                  error: messageOf(
                    error,
                    i18n.t("overview.readServicesFailed"),
                  ),
                  stale: current.items.length > 0,
                },
          );
        }
      }
    },
    [isReady],
  );

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

  const refreshAccessTokens = useCallback(
    async ({ silent = false }: RefreshOptions = {}) => {
      if (
        silent &&
        (!isReady || !revalidatable(tokenCatalogRef.current.status))
      )
        return;
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

      if (!silent) {
        setTokenCatalog((current) => ({
          ...current,
          status: "loading",
          error: null,
          stale: current.items.length > 0,
        }));
      }
      try {
        const result = await listAccessTokens();
        if (tokenCatalogGeneration.current === generation) {
          setTokenCatalog((current) =>
            current.status === "ready" &&
            !current.stale &&
            sameData(current.items, result.items)
              ? current
              : {
                  status: "ready",
                  items: result.items,
                  error: null,
                  stale: false,
                },
          );
        }
      } catch (error) {
        if (tokenCatalogGeneration.current === generation) {
          setTokenCatalog((current) =>
            silent && current.status !== "loading"
              ? current
              : {
                  ...current,
                  status: "error",
                  error: messageOf(error, i18n.t("app.readTokensFailed")),
                  stale: current.items.length > 0,
                },
          );
        }
      }
    },
    [isReady],
  );

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

  const refreshUsage = useCallback(
    async ({ silent = false }: RefreshOptions = {}) => {
      if (silent && (!isReady || !revalidatable(usageRef.current.status)))
        return;
      const generation = usageGeneration.current + 1;
      usageGeneration.current = generation;
      if (!isReady) {
        setUsage(blockedUsage);
        return;
      }

      // Keep the previous summary visible while a wider range loads, so
      // switching presets never blanks the panel.
      if (!silent) {
        setUsage((current) => ({
          status: "loading",
          summary: current.summary,
          error: null,
        }));
      }
      try {
        const usageWindow = resolveUsageWindow(usagePreset, new Date());
        const summary = await getUsageSummary(usageWindow);
        if (usageGeneration.current !== generation) return;
        setUsage((current) =>
          current.status === "ready" && sameData(current.summary, summary)
            ? current
            : { status: "ready", summary, error: null },
        );
      } catch (error) {
        if (usageGeneration.current === generation) {
          setUsage((current) =>
            silent && current.status !== "loading"
              ? current
              : {
                  status: "error",
                  summary: current.summary,
                  error: messageOf(error, i18n.t("app.usageFailed")),
                },
          );
        }
      }
    },
    [isReady, usagePreset],
  );

  useEffect(() => {
    if (!isReady) {
      usageGeneration.current += 1;
      setUsage(blockedUsage);
      return;
    }
    void refreshUsage();
  }, [coreSessionKey, isReady, refreshUsage]);

  // Returning to the overview, a periodic tick, and the window coming back to
  // the foreground pick up new traffic without a manual refresh.
  const revalidateOverviewRef = useRef(() => {});
  revalidateOverviewRef.current = () => {
    void refreshServices({ silent: true });
    void refreshAccessTokens({ silent: true });
    void refreshUsage({ silent: true });
  };
  const onOverview = page.kind === "overview";
  useEffect(() => {
    if (!onOverview || !isReady) return;
    const revalidate = () => {
      if (!document.hidden) revalidateOverviewRef.current();
    };
    revalidate();
    const timer = window.setInterval(revalidate, OVERVIEW_REVALIDATE_MS);
    document.addEventListener("visibilitychange", revalidate);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", revalidate);
    };
  }, [isReady, onOverview]);

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
      const leavingSettings =
        page.kind === "settings" && next.kind !== "settings";
      const leavingEditor =
        leavingServiceEditor || leavingRouteEditor || leavingSettings;
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

  // The tray popover routes through the same guard as the sidebar, so a dirty
  // editor still gets its confirmation.
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;
  useEffect(() => {
    if (!isTauri()) return;
    let cancelled = false;
    let stop: UnlistenFn | null = null;
    listen<unknown>(TRAY_NAVIGATE_EVENT, ({ payload }) => {
      const target = trayNavigationTarget(payload);
      if (target) navigateRef.current(target);
    })
      .then((unlisten) => {
        if (cancelled) unlisten();
        else stop = unlisten;
      })
      .catch((error) =>
        console.error("Unable to observe AstrLink tray navigation", error),
      );
    return () => {
      cancelled = true;
      stop?.();
    };
  }, []);

  const updateNoticeKind = updateNotice(updates.snapshot);
  useEffect(() => {
    const version = updates.snapshot.release?.version;
    if (!updateNoticeKind || !version) return;
    const key = `${updateNoticeKind}:${version}`;
    if (notifiedUpdate.current === key) return;
    notifiedUpdate.current = key;
    // About already shows this state; a toast linking back to it adds nothing.
    if (page.kind === "about") return;
    toast.info(t(`about.${updateNoticeKind}Notification`, { version }), {
      action: {
        label: t("about.title"),
        onClick: () => navigateRef.current({ kind: "about" }),
      },
    });
  }, [updateNoticeKind, updates.snapshot.release?.version, page.kind, t]);

  const modelUpdateNotice =
    modelUpdate === null
      ? undefined
      : t(`safety.modelUpdate.${modelUpdate.phase}.notification`, {
          name: modelUpdate.name,
          version: modelUpdate.version,
        });
  useEffect(() => {
    if (modelUpdate === null || modelUpdateNotice === undefined) return;
    // A finished download announces again: the next step is the switch.
    const key = `${modelUpdate.catalog_id}:${modelUpdate.version}:${modelUpdate.phase}`;
    if (notifiedModelUpdate.current === key) return;
    notifiedModelUpdate.current = key;
    // The privacy page already shows the update on the model card.
    if (page.kind === "safety") return;
    toast.info(modelUpdateNotice, {
      action: {
        label: t(`safety.modelUpdate.${modelUpdate.phase}.action`),
        onClick: () => navigateRef.current({ kind: "safety", view: "models" }),
      },
    });
  }, [modelUpdate, modelUpdateNotice, page.kind, t]);

  const confirmPendingNavigation = () => {
    if (pendingPage === null) return;
    setPage(pendingPage);
    setPendingPage(null);
    handleEditorDirtyChange(false);
  };

  // A local change supersedes any catalog read that started before it.
  const rememberService = (service: Service) => {
    catalogGeneration.current += 1;
    setCatalog((current) => {
      const existingIndex = current.items.findIndex(
        (item) => item.id === service.id,
      );
      const items =
        existingIndex === -1
          ? [...current.items, service]
          : current.items.map((item) =>
              item.id === service.id ? service : item,
            );
      return { status: "ready", items, error: null, stale: false };
    });
  };

  const handleServiceSaved = (service: Service) => {
    rememberService(service);
    handleEditorDirtyChange(false);
    setPage({ kind: "list" });
  };

  const handleServiceRemoved = (serviceId: string) => {
    catalogGeneration.current += 1;
    setCatalog((current) => ({
      status: "ready",
      items: current.items.filter((service) => service.id !== serviceId),
      error: null,
      stale: false,
    }));
  };

  const handleTokenCreated = (token: AccessTokenSummary) => {
    tokenCatalogGeneration.current += 1;
    setTokenCatalog((current) => ({
      status: "ready",
      items: [token, ...current.items.filter((item) => item.id !== token.id)],
      error: null,
      stale: false,
    }));
    if (!isWebEdition && onboarding.active) setPage({ kind: "overview" });
  };

  const handleTokenDeleted = (tokenId: string) => {
    tokenCatalogGeneration.current += 1;
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
  const statusLabel = snapshot
    ? phaseLabel(snapshot.phase)
    : t("core.phase.connecting");
  const protocols = useMemo(
    () => snapshot?.capabilities?.protocols ?? [],
    [snapshot?.capabilities?.protocols],
  );
  const providerImport = useProviderImport({
    enabled: isReady && rawSealing.status !== null && !rawSetupNeeded,
    protocols,
  });

  const handleProviderImported = (service: Service) => {
    providerImport.close();
    rememberService(service);
    notify.success(t("providerImport.added", { name: service.name }));
    // Without models the provider cannot route yet; land where they are fetched.
    navigate(
      service.models.length > 0
        ? { kind: "list" }
        : { kind: "edit", serviceId: service.id, tab: "models" },
    );
  };

  return (
    <AppShell
      sidebar={
        <aside className="flex h-full min-h-0 flex-col border-r bg-sidebar px-3 pt-[var(--sidebar-top)] pb-3 max-[960px]:px-2 max-[960px]:pb-2.5">
          <div className="flex items-center gap-3 px-2 pb-5 max-[960px]:justify-center max-[960px]:px-0">
            <img
              className="block size-8 shrink-0"
              src={astrlinkLogo}
              alt=""
              width={32}
              height={32}
              aria-hidden="true"
            />
            <span className="overflow-hidden text-lg font-semibold tracking-tight whitespace-nowrap max-[960px]:hidden">
              AstrLink
            </span>
          </div>

          <nav
            className="flex flex-1 flex-col gap-1"
            aria-label={t("nav.main")}
            data-slot="sidebar-navigation"
          >
            <span className="px-2 pb-1.5 text-xs font-medium tracking-[0.08em] text-muted-foreground uppercase max-[960px]:hidden">
              {t("nav.workspace")}
            </span>
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
              badge={modelUpdateNotice}
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

            <span className="mt-4 px-2 pb-1.5 text-xs font-medium tracking-[0.08em] text-muted-foreground uppercase max-[960px]:mx-2 max-[960px]:mt-3 max-[960px]:mb-2 max-[960px]:h-px max-[960px]:bg-border max-[960px]:p-0 max-[960px]:text-transparent">
              {t("nav.system")}
            </span>
            {!isWebEdition && (
              <NavButton
                active={page.kind === "agentTools"}
                icon="bot"
                label={t("nav.agentTools")}
                onClick={() => navigate({ kind: "agentTools" })}
              />
            )}
            <NavButton
              active={page.kind === "settings"}
              icon="settings"
              label={t("nav.settings")}
              onClick={() => navigate({ kind: "settings" })}
            />
            {!isWebEdition && (
              <NavButton
                active={page.kind === "about"}
                icon="about"
                label={t("nav.about")}
                badge={
                  updateNoticeKind
                    ? t(`about.phase.${updateNoticeKind}`)
                    : undefined
                }
                onClick={() => navigate({ kind: "about" })}
              />
            )}
            {isWebEdition && <ConsoleLogout />}
          </nav>

          <div
            aria-label={t("nav.gatewayStatus", { status: statusLabel })}
            className="mt-3 flex items-center gap-2 border-t px-2 pt-3 text-text-secondary max-[960px]:justify-center max-[960px]:px-0"
            title={t("nav.gatewayStatus", { status: statusLabel })}
          >
            <StatusDot tone={statusTone} />
            <span className="flex min-w-0 items-baseline gap-1.5 max-[960px]:hidden">
              <strong className="text-sm font-medium text-foreground">
                {t("nav.gateway")}
              </strong>
              <small className="overflow-hidden text-xs text-ellipsis whitespace-nowrap">
                {statusLabel}
              </small>
            </span>
          </div>
        </aside>
      }
    >
      <WorkspaceSnapshotProvider sessionKey={coreSessionKey}>
        <ValueTransition
          asChild
          valueKey={page.kind === "edit" ? `edit:${page.serviceId}` : page.kind}
          initialOpacity={0}
          duration={280}
          offsetY={8}
        >
          <main
            className={cn(
              "@container/workspace-surface h-full min-h-0 w-full min-w-0 px-8 pt-[var(--workspace-top)] pb-8 max-[960px]:px-5 [@media(max-height:680px)]:pb-5",
              "flex flex-col",
              [
                "about",
                "overview",
                "list",
                "create",
                "edit",
                "tokens",
                "records",
                "safety",
                "routing",
                "agentTools",
                "settings",
              ].includes(page.kind)
                ? "overflow-hidden"
                : "overflow-y-auto overscroll-none",
            )}
            data-page={page.kind}
            data-slot="workspace"
          >
            {!isWebEdition && onboarding.active && page.kind !== "overview" ? (
              <div
                className="mb-2 flex shrink-0 flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-muted-foreground"
                data-slot="onboarding-return"
              >
                <span>
                  {t("onboarding.inProgress")} ·{" "}
                  {t(
                    [
                      "onboarding.passwordTitle",
                      "onboarding.serviceTitle",
                      "onboarding.tokenTitle",
                      "onboarding.clientTitle",
                    ][onboarding.step],
                  )}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => navigate({ kind: "overview" })}
                >
                  {t("onboarding.return")}
                </Button>
              </div>
            ) : null}
            {!isWebEdition && page.kind === "overview" && onboarding.active ? (
              <GettingStarted
                conversionEngine={snapshot?.capabilities?.conversion_engine}
                onboarding={onboarding}
                catalog={catalog}
                tokenCatalog={tokenCatalog}
                snapshot={snapshot}
                usage={usage}
                isReady={isReady}
                isRestarting={isRestarting}
                onAddService={() => setServiceKindPickerOpen(true)}
                onManageServices={() => navigate({ kind: "list" })}
                onManageTokens={() => navigate({ kind: "tokens" })}
                onOpenRecords={() => navigate({ kind: "records" })}
                onRefresh={() => {
                  void refreshServices();
                  void refreshAccessTokens();
                  void refreshUsage();
                }}
                onRestart={() => void handleRestart()}
                onRawSealingStatus={rawSealing.setStatus}
                rawSealing={rawSealing}
              />
            ) : page.kind === "overview" ? (
              <Overview
                catalog={catalog}
                copyError={copyError}
                copyFeedback={copyFeedback}
                isNativeApp={isNativeApp}
                isReady={isReady}
                isRestarting={isRestarting}
                onAddService={() => setServiceKindPickerOpen(true)}
                onCopy={(value, label) => void copyValue(value, label)}
                onManageServices={() => navigate({ kind: "list" })}
                onManageTokens={() => navigate({ kind: "tokens" })}
                onOpenOnboarding={isWebEdition ? undefined : onboarding.open}
                showOnboardingHint={!isWebEdition && onboarding.showResumeHint}
                onDismissOnboardingHint={onboarding.dismissResumeHint}
                onOpenService={(serviceId) =>
                  navigate({ kind: "edit", serviceId })
                }
                onOpenTokenRecords={(tokenId) =>
                  navigate({ kind: "records", tokenId })
                }
                onRefreshServices={() => void refreshServices()}
                onRefresh={() => {
                  void refreshUsage();
                  void refreshServices({ silent: true });
                  void refreshAccessTokens({ silent: true });
                }}
                onRestart={() => void handleRestart()}
                onUsagePresetChange={setUsagePreset}
                snapshot={snapshot}
                tokenCatalog={tokenCatalog}
                usage={usage}
                usagePreset={usagePreset}
              />
            ) : page.kind === "tokens" ? (
              <AccessTokenManager
                conversionEngine={snapshot?.capabilities?.conversion_engine}
                catalog={tokenCatalog}
                coreSessionKey={coreSessionKey}
                inferenceURL={snapshot?.ready?.client_inference_url ?? ""}
                isReady={isReady}
                onRefresh={() => void refreshAccessTokens()}
                onTokenCreated={handleTokenCreated}
                onTokenDeleted={handleTokenDeleted}
              />
            ) : page.kind === "safety" ? (
              <SafetyPolicy
                coreSessionKey={coreSessionKey}
                initialView={page.view}
                isReady={isReady}
                onInstallPlaceholderSkill={
                  isWebEdition
                    ? undefined
                    : () =>
                        navigate({
                          kind: "agentTools",
                          preselectSkill: "redaction-placeholders",
                        })
                }
              />
            ) : page.kind === "records" ? (
              <RequestRecords
                accessTokens={tokenCatalog.items}
                accessTokensReady={tokenCatalog.status === "ready"}
                coreSessionKey={coreSessionKey}
                initialLocalAccessTokenId={page.tokenId}
                services={catalog.items}
                isReady={isReady}
              />
            ) : page.kind === "routing" ? (
              <RouteManager
                services={catalog.items}
                isReady={isReady}
                onDirtyChange={handleEditorDirtyChange}
              />
            ) : page.kind === "agentTools" ? (
              <AgentDebugSettings preselectSkill={page.preselectSkill} />
            ) : page.kind === "about" ? (
              <About
                snapshot={updates.snapshot}
                onSnapshot={updates.accept}
                loadError={updates.error}
                hasUnsavedChanges={() => editorDirtyRef.current}
              />
            ) : page.kind === "settings" && isWebEdition ? (
              <WebSettings coreSessionKey={coreSessionKey} isReady={isReady} />
            ) : page.kind === "settings" ? (
              <SettingsCenter
                localDataNotice={
                  <LocalDataNotice
                    coreSessionKey={coreSessionKey}
                    dismissed={localDataDismissed}
                    onDismiss={() => setLocalDataDismissed(true)}
                    onOpenServices={() => navigate({ kind: "list" })}
                  />
                }
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
        </ValueTransition>
      </WorkspaceSnapshotProvider>
      <ServiceKindPickerDialog
        onOpenChange={setServiceKindPickerOpen}
        onSelect={(serviceKind) => navigate({ kind: "create", serviceKind })}
        open={serviceKindPickerOpen}
      />
      <ConfirmDialog
        cancelLabel={t("common.continueEditing")}
        confirmLabel={t("common.discardAndLeave")}
        description={<p>{t("app.unsavedBody")}</p>}
        onCancel={() => setPendingPage(null)}
        onConfirm={confirmPendingNavigation}
        open={pendingPage !== null}
        title={t("common.discardUnsaved")}
      />
      <RawPasswordGate
        onStatus={rawSealing.setStatus}
        status={rawSealing.status}
        // The guide asks for the password in its own first step.
        suspended={
          !onboarding.settled || (onboarding.active && page.kind === "overview")
        }
      />
      {providerImport.active && (
        <ProviderImportDialog
          key={providerImport.active.id}
          id={providerImport.active.id}
          plan={providerImport.active.plan}
          onAdded={handleProviderImported}
          onClose={providerImport.close}
        />
      )}
    </AppShell>
  );
}
