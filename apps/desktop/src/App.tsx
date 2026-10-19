import { isWebEdition } from "./edition";
import { ConsoleLogout } from "./ConsoleEntry";
import { WebSettings } from "./WebSettings";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { isTauri } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import {
  Activity,
  Brain,
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
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";

import {
  getCoreStatus,
  listAccessTokens,
  getUsageSummary,
  listServices,
  restartCore,
  setMainWindowUnsaved,
  takeTrayNavigation,
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
import {
  IntelligentRouting,
  type IntelligentRoutingView,
} from "./IntelligentRouting";
import { useOnboarding } from "./use-onboarding";
import type { CopyState } from "./copy-feedback";
import { currentLocale, i18n, useT } from "./i18n";
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
import {
  TRAY_FAILED_TODAY_PAGE,
  TRAY_NAVIGATE_EVENT,
  TRAY_PRIVACY_OVERVIEW_PAGE,
} from "./tray-popover-window";
import type { RecordsTarget } from "./request-live-model";
import {
  DEFAULT_USAGE_RANGE_PRESET,
  localDayKey,
  resolveUsageWindow,
  type UsageRange,
  type UsageState,
} from "./usage-range";

type WorkspacePage =
  | { kind: "overview" }
  | { kind: "tokens" }
  | { kind: "safety"; view?: "models" | "overview" }
  | { kind: "records"; target?: RecordsTarget }
  | { kind: "routing" }
  | {
      kind: "intelligentRouting";
      view?: IntelligentRoutingView;
      /** Opens this group's rule editor, e.g. from a request's routing. */
      editGroup?: string;
    }
  | { kind: "agentTools"; preselectSkill?: AgentSkillId }
  | { kind: "settings" }
  | { kind: "about" }
  | ServiceManagerView;

type IconName =
  | "about"
  | "activity"
  | "bot"
  | "brain"
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
/**
 * Opens the pages the tray leaves for this window. The host keeps a page until
 * the window takes it, so one asked for while the window was being built again
 * is taken once the window listens.
 */
export function followTrayNavigation(
  open: (target: WorkspacePage) => void,
): () => void {
  let cancelled = false;
  let stop: UnlistenFn | null = null;
  const follow = () => {
    takeTrayNavigation()
      .then((kind) => {
        const target = trayNavigationTarget(kind);
        if (!cancelled && target) open(target);
      })
      .catch((error) =>
        console.error("Unable to read AstrLink tray navigation", error),
      );
  };
  listen(TRAY_NAVIGATE_EVENT, follow)
    .then((unlisten) => {
      if (cancelled) {
        unlisten();
        return;
      }
      stop = unlisten;
      follow();
    })
    .catch((error) =>
      console.error("Unable to observe AstrLink tray navigation", error),
    );
  return () => {
    cancelled = true;
    stop?.();
  };
}

export function trayNavigationTarget(
  kind: unknown,
  now = new Date(),
): WorkspacePage | null {
  switch (kind) {
    case TRAY_FAILED_TODAY_PAGE: {
      // The same local day the tray counted its failures over.
      const today = localDayKey(now);
      return {
        kind: "records",
        target: { status: "failed", range: { from: today, to: today } },
      };
    }
    case TRAY_PRIVACY_OVERVIEW_PAGE:
      return { kind: "safety", view: "overview" };
    case "overview":
    case "tokens":
    case "safety":
    case "records":
    case "routing":
    case "intelligentRouting":
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
  brain: Brain,
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
        "relative h-9 w-full justify-start gap-2.5 rounded-lg px-3 text-base font-normal text-text-secondary hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground max-[960px]:justify-center max-[960px]:px-0",
        // The active page sits on a neutral pill one step darker than the
        // window ground, with heavier ink.
        active &&
          "bg-sidebar-accent font-semibold text-sidebar-accent-foreground hover:bg-sidebar-accent",
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
          className="absolute top-1/2 right-3 size-2 -translate-y-1/2 rounded-full bg-primary max-[960px]:top-1 max-[960px]:right-1 max-[960px]:translate-y-0"
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
  const [usageRange, setUsageRange] = useState<UsageRange>(
    DEFAULT_USAGE_RANGE_PRESET,
  );
  const [page, setPage] = useState<WorkspacePage>({ kind: "overview" });
  const [pendingPage, setPendingPage] = useState<WorkspacePage | null>(null);
  // Overview and the guide add providers through the same type picker as the list.
  const [serviceKindPickerOpen, setServiceKindPickerOpen] = useState(false);
  const [copyState, setCopyState] = useState<CopyState>("idle");
  const editorDirtyRef = useRef(false);
  const requestGateRef = useRef<RequestGate | null>(null);
  const catalogGeneration = useRef(0);
  const tokenCatalogGeneration = useRef(0);
  const usageGeneration = useRef(0);
  const copyFeedbackTimer = useRef<number | null>(null);
  requestGateRef.current ??= new RequestGate();
  const requestGate = requestGateRef.current;
  const handleEditorDirtyChange = useCallback((dirty: boolean) => {
    if (editorDirtyRef.current !== dirty) {
      setMainWindowUnsaved(dirty).catch((error) =>
        console.error("Unable to keep unsaved AstrLink edits", error),
      );
    }
    editorDirtyRef.current = dirty;
  }, []);
  // A reload drops the edits an earlier page asked the host to keep.
  useEffect(() => {
    setMainWindowUnsaved(false).catch((error) =>
      console.error("Unable to keep unsaved AstrLink edits", error),
    );
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
        const usageWindow = resolveUsageWindow(usageRange, new Date());
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
    [isReady, usageRange],
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

  const copyValue = async (value: string) => {
    if (!value) return;
    if (copyFeedbackTimer.current !== null) {
      window.clearTimeout(copyFeedbackTimer.current);
      copyFeedbackTimer.current = null;
    }
    try {
      await navigator.clipboard.writeText(value);
      setCopyState("copied");
      copyFeedbackTimer.current = window.setTimeout(
        () => setCopyState("idle"),
        1_800,
      );
    } catch {
      setCopyState("failed");
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
      // The routing groups editor now lives under intelligent routing.
      const leavingIntelligentRouting =
        page.kind === "intelligentRouting" &&
        next.kind !== "intelligentRouting";
      const leavingSettings =
        page.kind === "settings" && next.kind !== "settings";
      const leavingEditor =
        leavingServiceEditor ||
        leavingRouteEditor ||
        leavingIntelligentRouting ||
        leavingSettings;
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
    return followTrayNavigation((target) => navigateRef.current(target));
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
  // ゲートウェイ plus a phase does not fit one sidebar line.
  const stackedGatewayStatus = currentLocale() === "ja";
  const protocols = useMemo(
    () => snapshot?.capabilities?.protocols ?? [],
    [snapshot?.capabilities?.protocols],
  );
  // The routing rule editor offers the protocols core says it serves. Only the
  // alpha phase is included: a post-alpha ID is a reserved identifier the
  // gateway cannot execute today, and offering it would let the user write a
  // rule that can never match. For the same reason Google formats are left
  // out, since their path carries the model and cannot name astrlink/<group>,
  // and so is model discovery, which no routing group serves.
  const protocolIDs = useMemo(
    () =>
      protocols
        .filter(
          (entry) =>
            entry.phase === "alpha" &&
            !entry.id.startsWith("google.") &&
            !entry.id.endsWith(".models"),
        )
        .map((entry) => entry.id),
    [protocols],
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
        <aside className="flex h-full min-h-0 flex-col px-4 pt-[var(--sidebar-top)] pb-4 max-[960px]:px-2 max-[960px]:pb-2.5">
          <div className="flex items-center gap-3 px-3 pb-6 max-[960px]:justify-center max-[960px]:px-0">
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
            <div
              className="flex flex-col gap-1"
              role="group"
              aria-label={t("nav.workspace")}
            >
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
                label={t("nav.providerRouting")}
                onClick={() => navigate({ kind: "routing" })}
              />
              <NavButton
                active={page.kind === "intelligentRouting"}
                icon="brain"
                label={t("nav.intelligentRouting")}
                onClick={() => navigate({ kind: "intelligentRouting" })}
              />
            </div>

            <Separator className="mx-2 my-2 bg-sidebar-border data-[orientation=horizontal]:w-auto" />

            <div
              className="flex flex-col gap-1"
              role="group"
              aria-label={t("nav.system")}
            >
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
            </div>
          </nav>

          <div
            aria-label={t("nav.gatewayStatus", { status: statusLabel })}
            className={cn(
              "mx-2 mt-3 flex gap-2.5 border-t border-sidebar-border px-1 text-text-secondary max-[960px]:h-11 max-[960px]:items-center max-[960px]:justify-center max-[960px]:px-0 max-[960px]:pt-2",
              stackedGatewayStatus
                ? "items-start py-2"
                : "h-11 items-center pt-2",
            )}
            title={t("nav.gatewayStatus", { status: statusLabel })}
          >
            <span className="grid size-5 shrink-0 place-items-center">
              <StatusDot tone={statusTone} />
            </span>
            <span
              className={cn(
                "min-w-0 max-[960px]:hidden",
                stackedGatewayStatus ? "grid" : "flex items-baseline gap-1.5",
              )}
            >
              <strong
                className={cn(
                  "text-sm font-medium text-foreground",
                  stackedGatewayStatus
                    ? "truncate"
                    : "shrink-0 whitespace-nowrap",
                )}
              >
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
        >
          <main
            className={cn(
              "@container/workspace-surface h-full min-h-0 w-full min-w-0 px-8 pt-[var(--workspace-top)] pb-6 max-[960px]:px-5 [@media(max-height:680px)]:pb-3",
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
                "intelligentRouting",
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
                copyState={copyState}
                isNativeApp={isNativeApp}
                isReady={isReady}
                isRestarting={isRestarting}
                onAddService={() => setServiceKindPickerOpen(true)}
                onCopy={(value) => void copyValue(value)}
                onManageServices={() => navigate({ kind: "list" })}
                onManageTokens={() => navigate({ kind: "tokens" })}
                onOpenOnboarding={isWebEdition ? undefined : onboarding.open}
                showOnboardingHint={!isWebEdition && onboarding.showResumeHint}
                onDismissOnboardingHint={onboarding.dismissResumeHint}
                onOpenRecords={(target) =>
                  // The list covers the days the overview counted, unless
                  // the target names a day of its own.
                  navigate({
                    kind: "records",
                    target: { range: usageRange, ...target },
                  })
                }
                onOpenService={(serviceId) =>
                  navigate({ kind: "edit", serviceId })
                }
                onRefreshServices={() => void refreshServices()}
                onRefresh={() => {
                  void refreshUsage();
                  void refreshServices({ silent: true });
                  void refreshAccessTokens({ silent: true });
                }}
                onRestart={() => void handleRestart()}
                onUsageRangeChange={setUsageRange}
                snapshot={snapshot}
                tokenCatalog={tokenCatalog}
                usage={usage}
                usageRange={usageRange}
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
                services={catalog.items}
                isReady={isReady}
                conversionEngine={snapshot?.capabilities?.conversion_engine}
                onEditRoutingGroup={(groupID) =>
                  navigate({
                    kind: "intelligentRouting",
                    view: "groups",
                    editGroup: groupID,
                  })
                }
                onOpenService={(serviceId) =>
                  navigate({ kind: "edit", serviceId, tab: "protocols" })
                }
                onServicesChanged={() => void refreshServices({ silent: true })}
                target={page.target}
              />
            ) : page.kind === "routing" ? (
              <RouteManager
                services={catalog.items}
                isReady={isReady}
                onDirtyChange={handleEditorDirtyChange}
              />
            ) : page.kind === "intelligentRouting" ? (
              <IntelligentRouting
                key={page.editGroup ?? ""}
                initialView={page.view}
                initialEditGroup={page.editGroup}
                ready={isReady}
                services={catalog.items}
                protocols={protocolIDs}
                onDirtyChange={handleEditorDirtyChange}
                conversionEngine={snapshot?.capabilities?.conversion_engine}
                onOpenService={(serviceId) =>
                  navigate({ kind: "edit", serviceId, tab: "protocols" })
                }
                onServicesChanged={() => void refreshServices({ silent: true })}
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
                inferenceURL={snapshot?.ready?.client_inference_url ?? ""}
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
