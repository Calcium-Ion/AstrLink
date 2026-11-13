import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import {
  Activity,
  AlignLeft,
  Blocks,
  Boxes,
  Flask,
  Home,
  Plus,
  RefreshCw,
  SlidersHorizontal,
  SquareStack,
  Waypoints,
} from "@/components/icons";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { LoadingState } from "@/components/LoadingState";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel } from "@/components/Panel";
import { PluginIcon, pluginAvatar } from "@/components/PluginIcon";
import { SearchInput } from "@/components/SearchInput";
import { SegmentedControl } from "@/components/SegmentedControl";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";
import {
  clearPluginData,
  deletePluginCredential,
  getPlugin,
  listenPluginApprovalFinished,
  openPluginApproval,
  pluginApprovalOf,
  getPluginCreditStars,
  getPluginHostSettings,
  getPluginStats,
  getPluginStatus,
  listPlugins,
  pluginInstallFailureOf,
  reloadPlugins,
  rollbackPlugin,
  savePluginCredential,
  testPlugin,
  uninstallPlugin,
  updatePlugin,
  updatePluginHostSettings,
} from "./bridge";
import { controlErrorCode, controlErrorMessage } from "./control-error";
import { isWebEdition } from "./edition";
import { PluginApprovalSummary } from "./PluginApprovalSummary";
import { useT } from "./i18n";
import { PageHeader } from "./PageHeader";
import { PluginDevelop } from "./PluginDevelop";
import { PluginInstallDialog } from "./PluginInstall";
import {
  loadErrorText,
  PluginDevelopHome,
  PluginHostDialog,
  pluginMatches,
  PluginMetrics,
  pluginOrigin,
  PluginRows,
  pluginState,
  pluginTone,
} from "./PluginList";
import { PluginStarMap, useRecentPluginUsage } from "./PluginStarMap";
import { PluginMarketView } from "./PluginMarket";
import { TaskPluginOverview, TaskPluginTasks } from "./PluginTaskDetail";
import {
  installFailureText,
  PluginInstallReviewView,
} from "./PluginInstallReview";
import { orderedPlugins, PluginOrderDialog } from "./PluginOrder";
import { PluginSourceCard } from "./PluginSource";
import {
  PluginSlotView,
  refreshPluginUi,
  setPluginUiSafeMode,
  usePluginSlot,
  usePluginUi,
} from "./plugin-ui";
import { capabilityLabel } from "./plugin-labels";
import { PluginOverview } from "./PluginOverview";
import { RequestPluginInstances } from "./RequestPluginInstances";
import { PluginSettingsForm, type CredentialInput } from "./PluginSettingsForm";
import type { PluginProviderChoice } from "./ServiceManager";
import type { Service } from "./service-model";
import {
  type PluginApproval,
  type PluginClearTarget,
  type PluginCreditStars,
  type PluginDetail,
  type PluginHostSettings,
  type PluginHostSettingsUpdate,
  type PluginInstalledVersion,
  type PluginInstallReview,
  type PluginList,
  type PluginSettingsUpdate,
  type PluginStats,
  type PluginStatus,
  type PluginSummary,
} from "./plugin-model";
import { DailyEffects } from "./PluginEffects";

type DetailTab =
  | "overview"
  | "settings"
  | "status"
  | "develop"
  /** A task plugin's tasks. */
  | "tasks"
  /** A tab the plugin's interface registered. */
  | `plugin:${string}`;

/** The tabs of the list page: the installed plugins, the marketplace, development. */
type ListTab = "installed" | "market" | "develop";

/** How the installed plugins show: a star map, or a list. */
type PluginView = "map" | "list";

/** Where the page keeps the view a viewer chose last. */
const VIEW_KEY = "astrlink.plugins.view";

function rememberedView(): PluginView {
  try {
    return localStorage.getItem(VIEW_KEY) === "list" ? "list" : "map";
  } catch {
    return "map";
  }
}

function rememberView(view: PluginView) {
  try {
    localStorage.setItem(VIEW_KEY, view);
  } catch {
    // The page works without storage; the view just resets.
  }
}

/** The plugin open after the list changes: the same one while it is listed, else the list. */
function keptSelection(current: string | null, list: PluginList) {
  return current && list.plugins.some((plugin) => plugin.key === current)
    ? current
    : null;
}

/** The origin of the server that sends credential, once Core allows it. */
function serverOrigin(detail: PluginDetail, credential: string) {
  const server = detail.settings.find(
    (setting) => setting.type === "server" && setting.credential === credential,
  );
  return server
    ? detail.network_origins.find((origin) => origin.setting === server.key)
        ?.origin
    : undefined;
}

export function PluginManager({
  coreSessionKey,
  isReady,
  services = [],
  onAddProvider,
  onOpenService,
  open,
  visit,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
  /** The plugin this visit opens, once the list has it. */
  open?: string;
  /** The providers, for a task plugin's page to list those that run it. */
  services?: Service[];
  /** Opens the editor of a new provider of a plugin. */
  onAddProvider?: (choice: PluginProviderChoice) => void;
  /** Opens a provider's editor. */
  onOpenService?: (id: string) => void;
  /** A new value each time the sidebar opens the page, which shows the list. */
  visit?: object;
}) {
  const t = useT();
  const [list, setList] = useState<PluginList | null>(null);
  const [listTab, setListTab] = useState<ListTab>("installed");
  const [view, setView] = useState<PluginView>(rememberedView);
  const [query, setQuery] = useState("");
  const [hostOpen, setHostOpen] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [rescanning, setRescanning] = useState(false);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  // The plugin a visit opens waits for the list that holds it.
  const opening = useRef<string | null>(null);
  // The sidebar's 插件 leads to the list, from a plugin's page too.
  useEffect(() => {
    opening.current = open ?? null;
    setSelectedKey(open ?? null);
    // A visit is a new page value; `open` comes with it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visit]);
  const [detail, setDetail] = useState<PluginDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [status, setStatus] = useState<PluginStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [stats, setStats] = useState<PluginStats | null>(null);
  const [stars, setStars] = useState<PluginCreditStars | null>(null);
  // Counts the times the plugin was joined anew: enabled, or tested fine.
  const [linkPlay, setLinkPlay] = useState(0);
  const [tab, setTab] = useState<DetailTab>("overview");
  const [testing, setTesting] = useState(false);
  // Every detail Core sends starts the settings form afresh.
  const [revision, setRevision] = useState(0);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  // A change that grants a plugin more waits for the user: in the
  // desktop's confirmation window (awaiting), or in the web console's own
  // dialog (confirming). afterApproval finishes what the change began, such
  // as the credentials of a server it added.
  const [awaiting, setAwaiting] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<{
    key: string;
    update: PluginSettingsUpdate;
    approval: PluginApproval;
    kept: string[];
  } | null>(null);
  const [ordering, setOrdering] = useState(false);
  const [savingOrder, setSavingOrder] = useState(false);
  // The install dialog, on the link of an update already.
  const [installing, setInstalling] = useState<{ link?: string } | null>(null);
  // A rollback the web console's own dialog confirms.
  const [confirmingRollback, setConfirmingRollback] = useState<{
    key: string;
    version: PluginInstalledVersion;
    review: PluginInstallReview;
  } | null>(null);
  const [uninstalling, setUninstalling] = useState<PluginDetail | null>(null);
  // Loading plugins' interface code in the web console takes the console's
  // own confirmation.
  const [confirmingWebUi, setConfirmingWebUi] = useState(false);
  const ui = usePluginUi();
  const afterApproval = useRef<{
    key: string;
    run: (detail: PluginDetail | null) => Promise<void>;
  } | null>(null);
  const selectedRef = useRef<string | null>(null);
  const [host, setHost] = useState<PluginHostSettings | null>(null);
  const [hostError, setHostError] = useState<string | null>(null);

  const loadList = useCallback(async () => {
    try {
      const next = await listPlugins();
      setList(next);
      setListError(null);
      setSelectedKey((current) => keptSelection(current, next));
    } catch (error) {
      setListError(controlErrorMessage(error, t("plugins.loadFailed")));
    }
    try {
      setHost(await getPluginHostSettings());
    } catch {
      // The header leaves out what it could not read.
      setHost(null);
    }
  }, [t]);

  async function changeHost(
    update: PluginHostSettingsUpdate,
    approved = false,
  ) {
    setHostError(null);
    try {
      setHost(
        approved
          ? await updatePluginHostSettings(update, true)
          : await updatePluginHostSettings(update),
      );
      if (update.web_ui !== undefined) refreshPluginUi();
    } catch (error) {
      if (update.web_ui && controlErrorCode(error) === "approval_required") {
        setConfirmingWebUi(true);
        return;
      }
      setHostError(controlErrorMessage(error, t("plugins.host.saveFailed")));
    }
  }

  // What the plugins' interfaces load follows the list: a plugin enabled,
  // turned off, updated or removed.
  useEffect(() => {
    if (list) refreshPluginUi();
  }, [list]);

  /** Clears what the host keeps for the plugin, then shows the new counts. */
  async function clearData(key: string, what: PluginClearTarget) {
    setBusy(true);
    setActionError(null);
    try {
      await clearPluginData(key, what);
      await loadDetail(key);
      return true;
    } catch (error) {
      setActionError(
        controlErrorMessage(error, t("plugins.stored.clearFailed")),
      );
      return false;
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    if (!isReady) return;
    setSelectedKey(opening.current);
    opening.current = null;
    void loadList();
  }, [coreSessionKey, isReady, loadList]);

  const loadDetail = useCallback(
    async (key: string) => {
      try {
        const next = await getPlugin(key);
        setDetail(next);
        setRevision((current) => current + 1);
        setDetailError(null);
        return next;
      } catch (error) {
        setDetailError(controlErrorMessage(error, t("plugins.detailFailed")));
        return null;
      }
    },
    [t],
  );

  const loadStatus = useCallback(
    async (key: string) => {
      // The account is numbers from the request records: without it the
      // overview and the status page just leave it out.
      void getPluginStats(key)
        .then(setStats)
        .catch(() => setStats(null));
      try {
        setStatus(await getPluginStatus(key));
        setStatusError(null);
      } catch (error) {
        setStatusError(controlErrorMessage(error, t("plugins.statusFailed")));
      }
    },
    [t],
  );

  useEffect(() => {
    setDetail(null);
    setStatus(null);
    setStats(null);
    setStars(null);
    setActionError(null);
    setTab("overview");
    if (!selectedKey) return;
    void loadDetail(selectedKey).then((next) => {
      if (next?.state !== "loaded") return;
      void loadStatus(selectedKey);
      // GitHub's answer comes apart from everything else, if at all.
      if (next.credits.length > 0)
        void getPluginCreditStars(selectedKey)
          .then(setStars)
          .catch(() => setStars(null));
    });
  }, [selectedKey, loadDetail, loadStatus]);

  async function rescan() {
    setRescanning(true);
    try {
      const next = await reloadPlugins();
      setList(next);
      setListError(null);
      // A new selection loads through the effect; the kept one reloads here.
      const kept = keptSelection(selectedKey, next);
      setSelectedKey(kept);
      if (kept && kept === selectedKey) {
        const reloaded = await loadDetail(kept);
        if (reloaded?.state === "loaded") void loadStatus(kept);
      }
    } catch (error) {
      setListError(controlErrorMessage(error, t("plugins.loadFailed")));
    } finally {
      setRescanning(false);
    }
  }

  /** Shows a detail Core sent and the list row of its state. */
  function show(next: PluginDetail) {
    setDetail(next);
    setRevision((current) => current + 1);
    setList((current) =>
      current
        ? {
            ...current,
            plugins: current.plugins.map((plugin) =>
              plugin.key === next.key
                ? { ...plugin, ...summaryOf(next) }
                : plugin,
            ),
          }
        : current,
    );
  }

  /**
   * Saves a change of a plugin's settings. One that grants the plugin more
   * waits for the user, and comes back as "waiting": in the desktop's
   * confirmation window, or in the web console's own dialog.
   */
  async function save(
    key: string,
    update: PluginSettingsUpdate,
    approved = false,
  ): Promise<PluginDetail | "waiting"> {
    try {
      return approved
        ? await updatePlugin(key, update, true)
        : await updatePlugin(key, update);
    } catch (error) {
      if (controlErrorCode(error) === "approval_pending") {
        setAwaiting(key);
        return "waiting";
      }
      const approval = pluginApprovalOf(error);
      if (approval) {
        setConfirming({
          key,
          update,
          approval,
          kept: approval.added_capabilities ?? [],
        });
        return "waiting";
      }
      throw error;
    }
  }

  /** Shows a plugin again once a change of it was decided elsewhere. */
  const reloadDecided = useCallback(
    async (key: string, approved: boolean) => {
      void loadList();
      const pending = afterApproval.current;
      afterApproval.current = null;
      if (selectedRef.current !== key) return;
      const next = await loadDetail(key);
      if (approved && pending?.key === key) await pending.run(next);
      if (approved && next?.enabled) {
        setTab("overview");
        setLinkPlay((count) => count + 1);
        if (next.hooks.includes("describeStatus")) void loadStatus(key);
      }
    },
    [loadDetail, loadList, loadStatus],
  );

  useEffect(() => {
    selectedRef.current = selectedKey;
  }, [selectedKey]);

  useEffect(() => {
    let stop: (() => void) | undefined;
    let stopped = false;
    void listenPluginApprovalFinished((finished) => {
      setAwaiting((current) => (current === finished.key ? null : current));
      if (finished.error) setActionError(finished.error);
      void reloadDecided(finished.key, finished.approved && !finished.error);
    }).then((unlisten) => {
      if (stopped) unlisten();
      else stop = unlisten;
    });
    return () => {
      stopped = true;
      stop?.();
    };
  }, [reloadDecided]);

  /** Shows the plugin an install put in place. */
  const showInstalled = useCallback(
    async (key: string) => {
      setInstalling(null);
      void loadList();
      if (selectedRef.current !== key) {
        setSelectedKey(key);
        return;
      }
      const next = await loadDetail(key);
      if (next?.state === "loaded") void loadStatus(key);
    },
    [loadDetail, loadList, loadStatus],
  );

  /**
   * Puts back a version an install replaced. It waits for the user as an
   * install does: in the confirmation window, or the web console's dialog.
   */
  async function rollback(
    key: string,
    version: PluginInstalledVersion,
    approved = false,
  ) {
    setBusy(true);
    setActionError(null);
    try {
      show(await rollbackPlugin(key, version.id, approved));
      void loadList();
    } catch (error) {
      if (controlErrorCode(error) === "approval_pending") {
        setAwaiting(key);
        return;
      }
      const approval = pluginApprovalOf(error);
      if (approval?.install) {
        setConfirmingRollback({ key, version, review: approval.install });
        return;
      }
      const failure = pluginInstallFailureOf(error);
      setActionError(
        failure
          ? installFailureText(failure, t)
          : controlErrorMessage(error, t("plugins.install.rollbackFailed")),
      );
    } finally {
      setBusy(false);
    }
  }

  /** Removes a plugin installed from source; a builtin of its key comes back. */
  async function uninstall(key: string) {
    setBusy(true);
    setActionError(null);
    try {
      const next = await uninstallPlugin(key);
      setList(next);
      setSelectedKey((current) => keptSelection(current, next));
      if (next.plugins.some((plugin) => plugin.key === key)) {
        const reloaded = await loadDetail(key);
        if (reloaded?.state === "loaded") void loadStatus(key);
      }
    } catch (error) {
      setActionError(
        controlErrorMessage(error, t("plugins.install.uninstallFailed")),
      );
    } finally {
      setBusy(false);
    }
  }

  /** Applies a change and refreshes the list row that shows its state. */
  async function apply(change: () => Promise<PluginDetail | "waiting">) {
    setBusy(true);
    setActionError(null);
    try {
      const next = await change();
      if (next === "waiting") return false;
      setDetail(next);
      setRevision((current) => current + 1);
      setList((current) =>
        current
          ? {
              ...current,
              plugins: current.plugins.map((plugin) =>
                plugin.key === next.key
                  ? { ...plugin, ...summaryOf(next) }
                  : plugin,
              ),
            }
          : current,
      );
      return true;
    } catch (error) {
      setActionError(controlErrorMessage(error, t("plugins.saveFailed")));
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function runTest(key: string) {
    setTesting(true);
    setActionError(null);
    try {
      const result = await testPlugin(key);
      setTab("overview");
      // The overview shows the test, with what it changed in the counts.
      await loadStatus(key);
      if (result.status === "ok") setLinkPlay((count) => count + 1);
    } catch (error) {
      setActionError(controlErrorMessage(error, t("plugins.testFailed")));
    } finally {
      setTesting(false);
    }
  }

  /**
   * Saves the settings form: the settings first, so the servers' origins
   * exist, then each new credential, to its server's origin.
   */
  async function saveSettings(
    key: string,
    update: PluginSettingsUpdate,
    credentials: CredentialInput[],
  ) {
    setBusy(true);
    setActionError(null);
    const saveCredentials = async (start: PluginDetail | null) => {
      let next = start;
      for (const credential of credentials) {
        const current = next ?? detail;
        const origin =
          credential.origin ??
          (current ? serverOrigin(current, credential.name) : undefined);
        if (!origin) throw new Error(t("plugins.settings.noOrigin"));
        next = await savePluginCredential(key, credential.name, {
          secret: credential.secret,
          origin,
        });
      }
      return next;
    };
    try {
      let next: PluginDetail | null = null;
      if (Object.keys(update).length > 0) {
        const saved = await save(key, update);
        if (saved === "waiting") {
          // The credentials follow the server the change adds, once the
          // user confirmed it.
          afterApproval.current = {
            key,
            run: async (confirmed) => {
              try {
                const after = await saveCredentials(confirmed);
                if (after) show(after);
              } catch (error) {
                setActionError(
                  controlErrorMessage(error, t("plugins.saveFailed")),
                );
              }
            },
          };
          return false;
        }
        next = saved;
      }
      next = await saveCredentials(next);
      if (next) show(next);
      void loadStatus(key);
      return true;
    } catch (error) {
      setActionError(controlErrorMessage(error, t("plugins.saveFailed")));
      return false;
    } finally {
      setBusy(false);
    }
  }

  // The list shows the plugins in the order their extension points run.
  const plugins = orderedPlugins(list?.plugins ?? [], host?.order);
  const recent = useRecentPluginUsage(isReady && list !== null, plugins);
  const shown = plugins.filter((plugin) => pluginMatches(plugin, query, t));
  const listed = plugins.find((plugin) => plugin.key === selectedKey) ?? null;
  // The page of a plugin shows it as its detail has it, once read.
  const selected =
    listed && detail?.key === listed.key ? { ...listed, ...detail } : listed;
  const uiOn = isWebEdition ? host?.web_ui === true : ui.safeMode === "off";

  const notices = (
    <>
      {listError || hostError ? (
        <FormMessage className="mb-3" tone="error">
          {listError ?? hostError}
        </FormMessage>
      ) : null}
      {host?.paused ? (
        <FormMessage className="mb-3" tone="notice">
          {t("plugins.host.pausedNotice")}
        </FormMessage>
      ) : null}
      {ui.safeMode === "crashed" ? (
        <FormMessage
          className="mb-3 flex flex-wrap items-center gap-2"
          data-testid="plugin-ui-crashed"
          tone="warning"
        >
          <span className="flex-1">{t("plugins.ui.crashed")}</span>
          <Button
            onClick={() => setPluginUiSafeMode(false)}
            size="xs"
            type="button"
            variant="outline"
          >
            {t("plugins.ui.load")}
          </Button>
        </FormMessage>
      ) : null}
      {!isReady && list !== null ? (
        <FormMessage className="mb-3" tone="notice">
          {t("plugins.stale")}
        </FormMessage>
      ) : null}
    </>
  );

  return (
    <section
      aria-labelledby="plugin-manager-heading"
      className="@container gutter-frame flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
    >
      {selected ? (
        <>
          <PageHeader
            back={{
              label: t("plugins.title"),
              onClick: () => setSelectedKey(null),
            }}
            title={selected.name}
            titleId="plugin-manager-heading"
            titlePrefix={
              <PluginIcon
                className="size-6 rounded-md"
                icon={selected.icon}
                label={pluginAvatar(selected)}
              />
            }
            titleSuffix={
              <span className="flex min-w-0 items-center gap-2">
                <StatusBadge tone={pluginTone(selected)}>
                  {pluginState(selected, t)}
                </StatusBadge>
                {/* Narrow, the name keeps the room; the page says the rest. */}
                <span className="truncate text-xs text-muted-foreground @max-[560px]:hidden">
                  {[pluginOrigin(selected, t), selected.version]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              </span>
            }
            actions={
              detail && detail.key === selected.key ? (
                <PluginActions
                  busy={busy}
                  detail={detail}
                  onDisable={() =>
                    void apply(() => save(detail.key, { enabled: false }))
                  }
                  onEnable={() => {
                    // A plugin is trusted code: what it declares the install
                    // review listed, and enabling it grants all of that.
                    void apply(() => save(detail.key, { enabled: true })).then(
                      (enabled) => {
                        if (!enabled) return;
                        setTab("overview");
                        setLinkPlay((count) => count + 1);
                        if (detail.hooks.includes("describeStatus"))
                          void loadStatus(detail.key);
                      },
                    );
                  }}
                  onTest={() => void runTest(detail.key)}
                  onUiEnabled={(on) =>
                    void apply(() => save(detail.key, { ui_enabled: on }))
                  }
                  testing={testing}
                />
              ) : null
            }
          />
          {notices}
          {detailError ? (
            <FormMessage tone="error">{detailError}</FormMessage>
          ) : detail === null || detail.key !== selected.key ? (
            <LoadingState className="flex-1" label={t("plugins.loading")} />
          ) : (
            <PluginDetailPane
              actionError={actionError}
              advancedOpen={advancedOpen}
              busy={busy}
              detail={detail}
              onAdvancedOpen={setAdvancedOpen}
              onClearData={(what) => clearData(detail.key, what)}
              onCredentialClear={(name) =>
                apply(() => deletePluginCredential(detail.key, name))
              }
              onSaveSettings={(update, credentials) =>
                saveSettings(detail.key, update, credentials)
              }
              awaiting={awaiting === detail.key}
              onOpenApproval={() => void openPluginApproval()}
              onUpdate={(link) => setInstalling({ link })}
              onRollback={(version) => void rollback(detail.key, version)}
              onUninstall={() => setUninstalling(detail)}
              onRefreshStatus={() =>
                // Refresh reads the plugin anew, then what it did.
                void loadDetail(detail.key).then((next) => {
                  if (next?.state === "loaded") void loadStatus(detail.key);
                })
              }
              onTab={setTab}
              linkPlay={linkPlay}
              revision={revision}
              services={services}
              onAddProvider={
                onAddProvider && detail.providers?.length
                  ? () => {
                      const provider = detail.providers?.[0];
                      if (provider)
                        onAddProvider({
                          plugin: detail.key,
                          pluginName: detail.name,
                          provider,
                          ...(detail.icon ? { icon: detail.icon } : {}),
                          ...(detail.task ? { task: detail.task } : {}),
                        });
                    }
                  : undefined
              }
              onOpenService={onOpenService}
              stars={stars}
              stats={stats}
              status={status}
              statusError={statusError}
              tab={tab}
            />
          )}
        </>
      ) : (
        <>
          <PageHeader
            actions={
              <>
                {list || host ? (
                  <Button
                    disabled={!isReady}
                    onClick={() => setHostOpen(true)}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    <SlidersHorizontal aria-hidden="true" />
                    {t("plugins.host.open")}
                  </Button>
                ) : null}
                <Button
                  disabled={!isReady}
                  onClick={() => setInstalling({})}
                  size="sm"
                  type="button"
                >
                  <Plus aria-hidden="true" />
                  {t("plugins.install.open")}
                </Button>
              </>
            }
            className="mb-0"
            title={t("plugins.title")}
            titleId="plugin-manager-heading"
            titleSuffix={
              list ? (
                <span className="inline-flex shrink-0 items-center gap-1.5 text-micro whitespace-nowrap text-muted-foreground">
                  <StatusDot
                    tone={
                      !isReady
                        ? "neutral"
                        : host?.paused
                          ? "pending"
                          : "positive"
                    }
                  />
                  {host?.paused
                    ? t("plugins.host.pausedShort")
                    : t("plugins.countLabel", { count: plugins.length })}
                </span>
              ) : null
            }
            variant="compact"
          />
          <div className="pt-3">{notices}</div>
          {!isReady && list === null ? (
            <EmptyState
              className="flex-1"
              description={t("plugins.blockedHint")}
              illustration={<Blocks className="size-6" />}
              title={t("plugins.blocked")}
              variant="page"
            />
          ) : list === null && !listError ? (
            <LoadingState className="flex-1" label={t("plugins.loading")} />
          ) : list !== null ? (
            <Tabs
              className="min-h-0 min-w-0 flex-1 gap-0"
              onValueChange={(value) => setListTab(value as ListTab)}
              value={listTab}
            >
              <div className="pb-3">
                <PluginMetrics plugins={plugins} recent={recent} />
              </div>
              <div className="flex min-w-0 shrink-0 items-center gap-2 border-b">
                <TabsList
                  aria-label={t("plugins.list.tabs")}
                  className="min-w-0 flex-1 justify-start"
                  variant="line"
                >
                  <TabsTrigger className="flex-none" value="installed">
                    <Blocks aria-hidden="true" />
                    {t("plugins.list.installed")}
                  </TabsTrigger>
                  <TabsTrigger className="flex-none" value="market">
                    <Boxes aria-hidden="true" />
                    {t("plugins.list.market")}
                  </TabsTrigger>
                  <TabsTrigger className="flex-none" value="develop">
                    <Flask aria-hidden="true" />
                    {t("plugins.list.develop")}
                  </TabsTrigger>
                </TabsList>
                {listTab === "installed" ? (
                  <>
                    {plugins.length > 0 ? (
                      <>
                        <SearchInput
                          className="hidden w-48 @[720px]:block"
                          clearLabel={t("common.clearSearch")}
                          label={t("plugins.search")}
                          onValueChange={setQuery}
                          placeholder={t("plugins.search")}
                          value={query}
                        />
                        {/* Narrow, the map has no room: the list stands. */}
                        <span className="hidden @[640px]:inline-flex">
                          <SegmentedControl
                            label={t("plugins.view.label")}
                            onValueChange={(next) => {
                              setView(next);
                              rememberView(next);
                            }}
                            options={[
                              {
                                value: "map",
                                label: t("plugins.view.map"),
                                icon: <Waypoints />,
                              },
                              {
                                value: "list",
                                label: t("plugins.view.list"),
                                icon: <AlignLeft />,
                              },
                            ]}
                            value={view}
                          />
                        </span>
                      </>
                    ) : null}
                    <IconButton
                      disabled={!isReady || rescanning}
                      label={t("plugins.rescan")}
                      onClick={() => void rescan()}
                      type="button"
                      variant="outline"
                    >
                      <RefreshCw
                        className={
                          rescanning
                            ? "animate-spin motion-reduce:animate-none"
                            : undefined
                        }
                      />
                    </IconButton>
                  </>
                ) : null}
              </div>
              <TabsContent
                className="flex min-h-0 min-w-0 flex-1 flex-col"
                value="installed"
              >
                {plugins.length === 0 ? (
                  <EmptyState
                    action={
                      <Button
                        onClick={() => setListTab("market")}
                        size="sm"
                        type="button"
                        variant="outline"
                      >
                        {t("plugins.list.browseMarket")}
                      </Button>
                    }
                    className="h-full"
                    description={t("plugins.emptyHint")}
                    illustration={<Blocks className="size-6" />}
                    title={t("plugins.empty")}
                    variant="page"
                  />
                ) : (
                  <>
                    {view === "map" ? (
                      <PluginStarMap
                        className="my-3 hidden @[640px]:block"
                        matches={(plugin) => pluginMatches(plugin, query, t)}
                        onAddEnhancer={
                          isReady ? () => setInstalling({}) : undefined
                        }
                        onAddProvider={() => setListTab("market")}
                        onHub={() => setHostOpen(true)}
                        onOpen={setSelectedKey}
                        onShowList={() => setView("list")}
                        paused={host?.paused === true}
                        usage={recent}
                        plugins={plugins}
                        services={services}
                      />
                    ) : null}
                    <div
                      className={cn(
                        "min-h-0 flex-1 overflow-y-auto overscroll-contain pb-3",
                        view === "map" && "@[640px]:hidden",
                      )}
                      data-tab-scroller
                    >
                      {shown.length === 0 ? (
                        <EmptyState
                          className="mt-3"
                          title={t("plugins.market.noMatch")}
                        />
                      ) : (
                        <PluginRows onOpen={setSelectedKey} plugins={shown} />
                      )}
                    </div>
                  </>
                )}
              </TabsContent>
              <TabsContent
                className="flex min-h-0 min-w-0 flex-1 flex-col pt-3"
                value="market"
              >
                {listTab === "market" ? (
                  <PluginMarketView
                    isReady={isReady}
                    onInstall={(link) => setInstalling({ link })}
                  />
                ) : null}
              </TabsContent>
              <TabsContent
                className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
                data-tab-scroller
                value="develop"
              >
                <PluginDevelopHome
                  directory={list.directory}
                  disabled={!isReady}
                  onOpen={setSelectedKey}
                  onRescan={() => void rescan()}
                  plugins={plugins}
                  rescanning={rescanning}
                />
              </TabsContent>
            </Tabs>
          ) : null}
        </>
      )}
      <PluginHostDialog
        canOrder={plugins.length > 1 && host !== null}
        directory={list?.directory ?? null}
        disabled={!isReady}
        host={host}
        onChange={(update) => void changeHost(update)}
        onOpenChange={setHostOpen}
        onOrder={() => {
          setHostOpen(false);
          setOrdering(true);
        }}
        onRescan={() => void rescan()}
        onUi={(on) =>
          isWebEdition
            ? void changeHost({ web_ui: on })
            : setPluginUiSafeMode(!on)
        }
        open={hostOpen}
        rescanning={rescanning}
        ui={uiOn}
        uiHelp={
          isWebEdition ? t("plugins.ui.webHelp") : t("plugins.ui.switchHelp")
        }
      />
      {installing ? (
        <PluginInstallDialog
          host={host}
          link={installing.link}
          onClose={() => setInstalling(null)}
          onHostChange={(update) => void changeHost(update)}
          onInstalled={(key) => void showInstalled(key)}
        />
      ) : null}
      <ConfirmDialog
        confirmDisabled={busy}
        confirmLabel={t("plugins.install.rollbackTo")}
        description={
          confirmingRollback ? (
            <PluginInstallReviewView
              review={confirmingRollback.review}
              rollback
            />
          ) : null
        }
        onCancel={() => setConfirmingRollback(null)}
        onConfirm={() => {
          const target = confirmingRollback;
          setConfirmingRollback(null);
          if (target) void rollback(target.key, target.version, true);
        }}
        open={confirmingRollback !== null}
        title={t("plugins.install.rollbackTitle", {
          name: confirmingRollback?.review.name ?? "",
        })}
      />
      <ConfirmDialog
        confirmLabel={t("plugins.ui.webConfirm")}
        description={<p>{t("plugins.ui.webConfirmDescription")}</p>}
        onCancel={() => setConfirmingWebUi(false)}
        onConfirm={() => {
          setConfirmingWebUi(false);
          void changeHost({ web_ui: true }, true);
        }}
        open={confirmingWebUi}
        title={t("plugins.ui.webConfirmTitle")}
        tone="warning"
      />
      <ConfirmDialog
        confirmDisabled={busy}
        confirmLabel={t("plugins.install.uninstall")}
        description={
          uninstalling ? (
            <p>
              {uninstalling.source === "override"
                ? t("plugins.install.uninstallBuiltin")
                : t("plugins.install.uninstallDescription")}
            </p>
          ) : null
        }
        destructive
        onCancel={() => setUninstalling(null)}
        onConfirm={() => {
          const target = uninstalling;
          setUninstalling(null);
          if (target) void uninstall(target.key);
        }}
        open={uninstalling !== null}
        title={t("plugins.install.uninstallTitle", {
          name: uninstalling?.name ?? "",
        })}
      />
      {ordering ? (
        <PluginOrderDialog
          onCancel={() => setOrdering(false)}
          onSave={(order) => {
            setSavingOrder(true);
            void updatePluginHostSettings({ order })
              .then((next) => {
                setHost(next);
                setOrdering(false);
              })
              .catch((error) =>
                setHostError(
                  controlErrorMessage(error, t("plugins.host.saveFailed")),
                ),
              )
              .finally(() => setSavingOrder(false));
          }}
          order={host?.order}
          plugins={plugins}
          saving={savingOrder}
        />
      ) : null}
      <ConfirmDialog
        confirmDisabled={busy}
        confirmLabel={t("plugins.approval.confirm")}
        description={
          confirming ? (
            <PluginApprovalSummary
              approval={confirming.approval}
              detail={detail?.key === confirming.key ? detail : null}
              kept={confirming.kept}
              onKept={(kept) =>
                setConfirming((current) =>
                  current ? { ...current, kept } : current,
                )
              }
            />
          ) : null
        }
        onCancel={() => {
          setConfirming(null);
          afterApproval.current = null;
        }}
        onConfirm={() => {
          const target = confirming;
          setConfirming(null);
          if (!target) return;
          const added = target.approval.added_capabilities ?? [];
          const update = target.update.granted_capabilities
            ? {
                ...target.update,
                granted_capabilities: target.update.granted_capabilities.filter(
                  (grant) =>
                    !added.includes(grant) || target.kept.includes(grant),
                ),
              }
            : target.update;
          void apply(() => save(target.key, update, true)).then((saved) =>
            reloadDecided(target.key, saved),
          );
        }}
        open={confirming !== null}
        title={
          confirming?.approval.enable
            ? t("plugins.approval.enableTitle", {
                name: confirming.approval.name,
              })
            : t("plugins.approval.grantTitle", {
                name: confirming?.approval.name ?? "",
              })
        }
      />
    </section>
  );
}

function summaryOf(detail: PluginDetail): Partial<PluginSummary> {
  return {
    enabled: detail.enabled,
    granted_capabilities: detail.granted_capabilities,
    state: detail.state,
  };
}

/**
 * What a plugin's page offers in its header: its interface switch, the
 * connection test, and enabling or turning it off.
 */
function PluginActions({
  detail,
  busy,
  testing,
  onEnable,
  onDisable,
  onTest,
  onUiEnabled,
}: {
  detail: PluginDetail;
  busy: boolean;
  testing: boolean;
  onEnable: () => void;
  onDisable: () => void;
  onTest: () => void;
  onUiEnabled: (on: boolean) => void;
}) {
  const t = useT();
  if (detail.state !== "loaded") return null;
  return (
    <div className="flex flex-wrap items-center justify-end gap-2">
      {detail.client ? (
        <Label className="mr-1 inline-flex cursor-pointer items-center gap-2 text-xs font-normal text-text-secondary">
          <span>{t("plugins.ui.pluginSwitchLabel")}</span>
          <Switch
            aria-label={t("plugins.ui.pluginSwitch")}
            checked={detail.ui_enabled}
            disabled={busy}
            id="plugin-ui-enabled"
            onCheckedChange={onUiEnabled}
            size="sm"
          />
        </Label>
      ) : null}
      {detail.hooks.includes("testConnection") ? (
        <Button
          disabled={testing || busy}
          onClick={onTest}
          size="sm"
          type="button"
          variant="outline"
        >
          {testing ? t("plugins.testing") : t("plugins.test")}
        </Button>
      ) : null}
      {detail.enabled ? (
        <Button
          disabled={busy}
          onClick={onDisable}
          size="sm"
          type="button"
          variant="outline"
        >
          {t("plugins.disable")}
        </Button>
      ) : (
        <Button disabled={busy} onClick={onEnable} size="sm" type="button">
          {t("plugins.enable")}
        </Button>
      )}
    </div>
  );
}

/** Each tab of a plugin's page with its icon. */
const tabIcons: Partial<Record<DetailTab, ReactNode>> = {
  overview: <Home aria-hidden="true" />,
  settings: <SlidersHorizontal aria-hidden="true" />,
  status: <Activity aria-hidden="true" />,
  develop: <Flask aria-hidden="true" />,
  tasks: <SquareStack aria-hidden="true" />,
};

function PluginDetailPane({
  detail,
  stats,
  stars,
  linkPlay,
  status,
  statusError,
  busy,
  actionError,
  tab,
  revision,
  advancedOpen,
  services,
  onTab,
  onRefreshStatus,
  onAdvancedOpen,
  onSaveSettings,
  onCredentialClear,
  onClearData,
  awaiting,
  onOpenApproval,
  onUpdate,
  onRollback,
  onUninstall,
  onAddProvider,
  onOpenService,
}: {
  detail: PluginDetail;
  stats: PluginStats | null;
  stars: PluginCreditStars | null;
  linkPlay: number;
  status: PluginStatus | null;
  statusError: string | null;
  busy: boolean;
  actionError: string | null;
  tab: DetailTab;
  revision: number;
  advancedOpen: boolean;
  services: Service[];
  onTab: (tab: DetailTab) => void;
  onRefreshStatus: () => void;
  onAdvancedOpen: (open: boolean) => void;
  onSaveSettings: (
    update: PluginSettingsUpdate,
    credentials: CredentialInput[],
  ) => Promise<boolean>;
  onCredentialClear: (name: string) => Promise<boolean>;
  onClearData: (what: PluginClearTarget) => Promise<boolean>;
  awaiting: boolean;
  onOpenApproval: () => void;
  onUpdate: (link: string) => void;
  onRollback: (version: PluginInstalledVersion) => void;
  onUninstall: () => void;
  onAddProvider?: () => void;
  onOpenService?: (id: string) => void;
}) {
  const t = useT();
  const loaded = detail.state === "loaded";
  const task = detail.task;
  const pluginTabs = usePluginSlot("plugin.tab", detail.key);
  const tabs: DetailTab[] = !loaded
    ? []
    : task
      ? ["overview", "tasks", "status"]
      : detail.kind === "provider" || detail.kind === "request"
        ? ["overview", "status"]
        : [
            "overview",
            "settings",
            "status",
            "develop",
            ...pluginTabs.map((entry) => `plugin:${entry.id}` as const),
          ];
  const current = tabs.includes(tab) ? tab : "overview";
  const source = detail.installed ? (
    <PluginSourceCard
      busy={busy}
      detail={detail}
      onRollback={onRollback}
      onUninstall={onUninstall}
      onUpdate={onUpdate}
      source={detail.installed}
    />
  ) : null;
  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col"
      data-testid="plugin-detail"
    >
      {actionError ? (
        <FormMessage className="mb-3" tone="error">
          {actionError}
        </FormMessage>
      ) : null}
      {awaiting ? (
        <FormMessage
          className="mb-3 flex flex-wrap items-center gap-2"
          data-testid="plugin-awaiting-approval"
          tone="notice"
        >
          <span className="flex-1">{t("plugins.approval.waiting")}</span>
          <Button
            onClick={onOpenApproval}
            size="xs"
            type="button"
            variant="outline"
          >
            {t("plugins.approval.open")}
          </Button>
        </FormMessage>
      ) : null}
      {!loaded ? (
        <div className="grid gap-3">
          {detail.error ? (
            <FormMessage tone="error">
              <span className="block font-medium">
                {loadErrorText(detail.error, t)}
              </span>
              <span className="mt-1 block break-words">
                {detail.error.message}
              </span>
            </FormMessage>
          ) : null}
          {source}
        </div>
      ) : (
        <Tabs
          className="min-h-0 min-w-0 flex-1 gap-0"
          onValueChange={(value) => onTab(value as DetailTab)}
          value={current}
        >
          <div className="flex min-w-0 shrink-0 items-center gap-2 border-b">
            <TabsList
              aria-label={t("plugins.tabsLabel")}
              className="min-w-0 flex-1 justify-start"
              scrollable
              variant="line"
            >
              {tabs.map((item) => (
                <TabsTrigger className="flex-none" key={item} value={item}>
                  {tabIcons[item] ?? null}
                  {item.startsWith("plugin:")
                    ? (pluginTabs.find((entry) => `plugin:${entry.id}` === item)
                        ?.title ?? t("plugins.ui.tab"))
                    : t(`plugins.tabs.${item}`)}
                </TabsTrigger>
              ))}
            </TabsList>
            {current === "overview" || current === "status" ? (
              <IconButton
                label={t("common.refresh")}
                onClick={onRefreshStatus}
                size="icon-sm"
                type="button"
                variant="ghost"
              >
                <RefreshCw />
              </IconButton>
            ) : null}
            {!loaded || task ? null : (
              <HelpPopover label={t("plugins.about.label")} size="wide">
                <AboutPlugin detail={detail} />
              </HelpPopover>
            )}
          </div>
          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain py-4"
            data-tab-scroller
            value="overview"
          >
            {task ? (
              <div className="grid min-w-0 gap-5">
                {source}
                <TaskPluginOverview
                  detail={detail}
                  onAddProvider={onAddProvider}
                  onOpenProvider={onOpenService}
                  services={services}
                  task={task}
                />
              </div>
            ) : (
              <>
                {source ? <div className="mb-4">{source}</div> : null}
                {detail.request ? (
                  <div className="mb-4">
                    <RequestPluginInstances
                      detail={detail}
                      disabled={!detail.enabled}
                    />
                  </div>
                ) : null}
                <PluginOverview
                  busy={busy}
                  detail={detail}
                  linkPlay={linkPlay}
                  onClear={onClearData}
                  stars={stars}
                  stats={stats}
                  status={status}
                  statusError={statusError}
                />
              </>
            )}
          </TabsContent>
          {task ? (
            <TabsContent
              className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain py-4"
              data-tab-scroller
              value="tasks"
            >
              {current === "tasks" ? (
                <TaskPluginTasks pluginKey={detail.key} />
              ) : null}
            </TabsContent>
          ) : (
            <TabsContent
              className="flex min-h-0 min-w-0 flex-1 flex-col"
              value="settings"
            >
              <PluginSettingsForm
                advancedOpen={advancedOpen}
                busy={busy}
                detail={detail}
                key={`${detail.key}:${revision}`}
                onAdvancedOpen={onAdvancedOpen}
                onClearCredential={onCredentialClear}
                onSave={onSaveSettings}
              />
            </TabsContent>
          )}
          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain py-4"
            data-tab-scroller
            value="status"
          >
            <StatusTab
              detail={detail}
              stats={stats}
              status={status}
              statusError={statusError}
            />
          </TabsContent>
          {task ? null : (
            <TabsContent
              className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain py-4"
              data-tab-scroller
              value="develop"
            >
              {current === "develop" ? <PluginDevelop detail={detail} /> : null}
            </TabsContent>
          )}
          {pluginTabs.map((entry) => (
            <TabsContent
              className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain py-4"
              data-tab-scroller
              key={entry.id}
              value={`plugin:${entry.id}`}
            >
              {current === `plugin:${entry.id}` ? (
                <PluginSlotView
                  context={{ plugin: detail.key }}
                  entry={entry}
                />
              ) : null}
            </TabsContent>
          ))}
        </Tabs>
      )}
    </div>
  );
}

/** What the plugin does and the capabilities it uses, behind the ? by its name. */
function AboutPlugin({ detail }: { detail: PluginDetail }) {
  const t = useT();
  return (
    <div className="grid gap-3" data-testid="plugin-about">
      {detail.notice ? (
        <section className="grid gap-1">
          <h3 className="text-xs font-medium text-muted-foreground">
            {t("plugins.about.does")}
          </h3>
          <p className="leading-6 whitespace-pre-wrap">{detail.notice}</p>
        </section>
      ) : null}
      {detail.network_purpose ? (
        <section className="grid gap-1">
          <h3 className="text-xs font-medium text-muted-foreground">
            {t("plugins.about.network")}
          </h3>
          <p>{detail.network_purpose}</p>
        </section>
      ) : null}
      <section className="grid gap-1.5">
        <h3 className="text-xs font-medium text-muted-foreground">
          {t("plugins.about.capabilities")}
        </h3>
        {detail.requested_capabilities.length === 0 ? (
          <p className="text-muted-foreground">{t("plugins.noCapabilities")}</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {detail.requested_capabilities.map((capability) => (
              <StatusBadge
                key={capability}
                tone={
                  detail.granted_capabilities.includes(capability)
                    ? "positive"
                    : "neutral"
                }
              >
                {capabilityLabel(capability, t)}
              </StatusBadge>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function StatusTab({
  detail,
  stats,
  status,
  statusError,
}: {
  detail: PluginDetail;
  stats: PluginStats | null;
  status: PluginStatus | null;
  statusError: string | null;
}) {
  const t = useT();
  if (statusError) return <FormMessage tone="error">{statusError}</FormMessage>;
  if (status === null) return <LoadingState label={t("plugins.loading")} />;
  const failures = Object.entries(status.failures);
  const warn = (count: number) => (
    <span className={count > 0 ? "text-warning-foreground" : undefined}>
      {count}
    </span>
  );
  return (
    <div
      className="@container/overview grid min-w-0 gap-5"
      data-testid="plugin-status"
    >
      <Panel>
        <MetricGroup className="grid-cols-2 @[560px]/overview:grid-cols-4">
          <Metric
            label={t("plugins.status.calls")}
            size="sm"
            value={status.calls}
          />
          <Metric
            label={t("plugins.status.timeouts")}
            size="sm"
            value={warn(status.timeouts)}
          />
          <Metric
            label={t("plugins.status.memoryLimits")}
            size="sm"
            value={warn(status.memory_limits)}
          />
          <Metric
            detail={t("plugins.status.networkDetail", {
              denied: status.network.denied,
              errors: status.network.errors,
            })}
            label={t("plugins.status.networkRequests")}
            size="sm"
            value={status.network.requests}
          />
        </MetricGroup>
      </Panel>
      {failures.length > 0 ? (
        <section className="grid gap-1">
          <h3 className="text-xs font-medium text-muted-foreground">
            {t("plugins.status.failures")}
          </h3>
          <ul className="divide-y border-y text-xs">
            {failures.map(([reason, count]) => (
              <li className="flex justify-between gap-3 py-2" key={reason}>
                <span>
                  {t(`plugins.status.reasons.${reason}`, {
                    defaultValue: reason,
                  })}
                </span>
                <span className="tabular-nums">{count}</span>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {/* The days count what an extension did; a task plugin's are its tasks. */}
      {stats && !detail.task ? (
        <DailyEffects detail={detail} stats={stats} />
      ) : null}
    </div>
  );
}
