import { useCallback, useEffect, useRef, useState } from "react";

import { Blocks, Plus, RefreshCw, SlidersHorizontal } from "@/components/icons";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { LoadingState } from "@/components/LoadingState";
import { PluginIcon, pluginAvatar } from "@/components/PluginIcon";
import { SearchInput } from "@/components/SearchInput";
import { SegmentedControl } from "@/components/SegmentedControl";
import { StatusBadge } from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import { PluginRunLog } from "./PluginDevelop";
import { PluginHealth } from "./PluginHealth";
import { PluginInstallDialog } from "./PluginInstall";
import {
  loadErrorText,
  PluginHostDialog,
  pluginMatches,
  pluginOrigin,
  PluginRows,
} from "./PluginList";
import { useRecentPluginUsage } from "./plugin-usage";
import {
  PluginMarketView,
  readCustomMarket,
  saveCustomMarket,
} from "./PluginMarket";
import { ProviderPluginOverview, TaskPluginTasks } from "./PluginTaskDetail";
import {
  installFailureText,
  PluginInstallReviewView,
} from "./PluginInstallReview";
import { orderedPlugins, PluginOrderDialog } from "./PluginOrder";
import { PluginMenu, sourceLine } from "./PluginSource";
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
  /** A task plugin's tasks. */
  | "tasks"
  /** What the plugin did: its calls, its days and its log. */
  | "log"
  /** A tab the plugin's interface registered. */
  | `plugin:${string}`;

/** The two halves of the list page: the installed plugins and the marketplace. */
type ListTab = "installed" | "market";

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
  const [query, setQuery] = useState("");
  const [marketQuery, setMarketQuery] = useState("");
  // The plugin whose switch on the list is being saved.
  const [switching, setSwitching] = useState<string | null>(null);
  const [switchError, setSwitchError] = useState<string | null>(null);
  const [hostOpen, setHostOpen] = useState(false);
  // The marketplace the user added in 插件设置, beside New API's two.
  const [customMarket, setCustomMarket] = useState(readCustomMarket);
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

  /**
   * Turns a plugin on or off from its row. Turning one on grants what it
   * asks for, so it may wait for the confirmation as on its own page.
   */
  async function switchPlugin(plugin: PluginSummary, enabled: boolean) {
    setSwitching(plugin.key);
    setSwitchError(null);
    try {
      const next = await save(plugin.key, { enabled });
      if (next !== "waiting") show(next);
    } catch (error) {
      setSwitchError(controlErrorMessage(error, t("plugins.saveFailed")));
    } finally {
      setSwitching(null);
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
  // How many rules each request plugin has, once the host settings are read.
  const rules = host
    ? (host.request_plugins ?? []).reduce<Record<string, number>>(
        (counts, rule) => {
          counts[rule.plugin] = (counts[rule.plugin] ?? 0) + 1;
          return counts;
        },
        {},
      )
    : null;
  const listed = plugins.find((plugin) => plugin.key === selectedKey) ?? null;
  // The page of a plugin shows it as its detail has it, once read.
  const selected =
    listed && detail?.key === listed.key ? { ...listed, ...detail } : listed;
  const uiOn = isWebEdition ? host?.web_ui === true : ui.safeMode === "off";

  const notices = (
    <>
      {listError || hostError || switchError ? (
        <FormMessage className="mb-3" tone="error">
          {listError ?? hostError ?? switchError}
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
              <span className="truncate text-xs text-muted-foreground @max-[560px]:hidden">
                {[
                  selected.version,
                  detail?.key === selected.key && detail.installed
                    ? sourceLine(detail.installed, t)
                    : pluginOrigin(selected, t),
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
            }
            actions={
              detail && detail.key === selected.key ? (
                <div className="flex items-center gap-2">
                  <PluginMenu
                    busy={busy}
                    detail={detail}
                    onRollback={(version) => void rollback(detail.key, version)}
                    onTest={() => void runTest(detail.key)}
                    onUiEnabled={(on) =>
                      void apply(() => save(detail.key, { ui_enabled: on }))
                    }
                    onUninstall={() => setUninstalling(detail)}
                    onUpdate={(link) => setInstalling({ link })}
                    testing={testing}
                  />
                  {detail.state === "loaded" ? (
                    <Switch
                      aria-label={t("plugins.row.enable", {
                        name: detail.name,
                      })}
                      checked={detail.enabled}
                      data-testid="plugin-enabled"
                      disabled={busy}
                      onCheckedChange={(on) => {
                        if (!on) {
                          void apply(() =>
                            save(detail.key, { enabled: false }),
                          );
                          return;
                        }
                        // A plugin is trusted code: what it declares the
                        // install review listed, and enabling it grants all
                        // of that.
                        void apply(() =>
                          save(detail.key, { enabled: true }),
                        ).then((enabled) => {
                          if (!enabled) return;
                          setTab("overview");
                          setLinkPlay((count) => count + 1);
                          if (detail.hooks.includes("describeStatus"))
                            void loadStatus(detail.key);
                        });
                      }}
                    />
                  ) : (
                    <Button
                      disabled={rescanning}
                      onClick={() => void rescan()}
                      size="sm"
                      type="button"
                      variant="outline"
                    >
                      {t("plugins.row.reload")}
                    </Button>
                  )}
                </div>
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
                {list && listTab === "installed" && plugins.length > 0 ? (
                  <SearchInput
                    className="hidden w-48 @[720px]:block @[960px]:w-56"
                    clearLabel={t("common.clearSearch")}
                    label={t("plugins.search")}
                    onValueChange={setQuery}
                    placeholder={t("plugins.search")}
                    value={query}
                  />
                ) : null}
                {list && listTab === "market" ? (
                  <SearchInput
                    className="hidden w-48 @[720px]:block @[960px]:w-56"
                    clearLabel={t("common.clearSearch")}
                    label={t("plugins.market.search")}
                    onValueChange={setMarketQuery}
                    placeholder={t("plugins.market.search")}
                    value={marketQuery}
                  />
                ) : null}
                {list || host ? (
                  <IconButton
                    disabled={!isReady}
                    label={t("plugins.host.open")}
                    onClick={() => setHostOpen(true)}
                    type="button"
                    variant="outline"
                  >
                    <SlidersHorizontal />
                  </IconButton>
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
                <SegmentedControl
                  label={t("plugins.list.tabs")}
                  onValueChange={setListTab}
                  options={[
                    {
                      value: "installed",
                      label: t("plugins.list.installed"),
                      count: plugins.length,
                    },
                    { value: "market", label: t("plugins.list.market") },
                  ]}
                  value={listTab}
                />
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
          ) : list !== null && listTab === "market" ? (
            <PluginMarketView
              customIndex={customMarket}
              isReady={isReady}
              onInstall={(link) => setInstalling({ link })}
              onOpenInstalled={(key) => {
                setSelectedKey(key);
                // The list read before the plugin was installed lacks it.
                if (!plugins.some((plugin) => plugin.key === key))
                  void loadList();
              }}
              query={marketQuery}
            />
          ) : list !== null && plugins.length === 0 ? (
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
              className="flex-1"
              description={t("plugins.emptyHint")}
              illustration={<Blocks className="size-6" />}
              title={t("plugins.empty")}
              variant="page"
            />
          ) : list !== null ? (
            <div
              className="flex min-h-0 min-w-0 flex-1 flex-col gap-3"
              data-testid="plugin-installed"
            >
              <div
                className="min-h-0 flex-1 overflow-y-auto overscroll-contain pb-3"
                data-tab-scroller
              >
                {shown.length === 0 ? (
                  <EmptyState
                    className="mt-3"
                    title={t("plugins.market.noMatch")}
                  />
                ) : (
                  <PluginRows
                    busyKey={switching}
                    disabled={!isReady}
                    onEnabled={(plugin, enabled) =>
                      void switchPlugin(plugin, enabled)
                    }
                    onOpen={setSelectedKey}
                    onReload={() => void rescan()}
                    plugins={shown}
                    recent={recent}
                    rules={rules}
                  />
                )}
              </div>
            </div>
          ) : null}
        </>
      )}
      <PluginHostDialog
        canOrder={plugins.length > 1 && host !== null}
        customMarket={customMarket}
        directory={list?.directory ?? null}
        disabled={!isReady}
        host={host}
        onChange={(update) => void changeHost(update)}
        onCustomMarket={(index) => {
          saveCustomMarket(index);
          setCustomMarket(index);
        }}
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
  onAddProvider?: () => void;
  onOpenService?: (id: string) => void;
}) {
  const t = useT();
  const loaded = detail.state === "loaded";
  const task = detail.task;
  const pluginTabs = usePluginSlot("plugin.tab", detail.key);
  const servesProviders = Boolean(task) || detail.kind === "provider";
  const tabs: DetailTab[] = !loaded
    ? []
    : task
      ? ["overview", "tasks", "log"]
      : servesProviders || detail.kind === "request"
        ? ["overview", "log"]
        : [
            "overview",
            "settings",
            "log",
            ...pluginTabs.map((entry) => `plugin:${entry.id}` as const),
          ];
  const current = tabs.includes(tab) ? tab : "overview";
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
                  {item.startsWith("plugin:")
                    ? (pluginTabs.find((entry) => `plugin:${entry.id}` === item)
                        ?.title ?? t("plugins.ui.tab"))
                    : t(`plugins.tabs.${item}`)}
                </TabsTrigger>
              ))}
            </TabsList>
            {current === "overview" || current === "log" ? (
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
            {servesProviders ? (
              <ProviderPluginOverview
                detail={detail}
                health={<PluginHealth detail={detail} revision={revision} />}
                onAddProvider={onAddProvider}
                onAllTasks={task ? () => onTab("tasks") : undefined}
                onOpenProvider={onOpenService}
                services={services}
              />
            ) : detail.request ? (
              <div className="grid min-w-0 gap-4">
                {detail.request.description ? (
                  <p className="text-sm leading-6 text-text-secondary">
                    {detail.request.description}
                  </p>
                ) : null}
                <RequestPluginInstances detail={detail} />
                <PluginHealth detail={detail} revision={revision} />
              </div>
            ) : (
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
            value="log"
          >
            {current === "log" ? (
              <PluginRunLog
                days={
                  stats && !servesProviders && !detail.request ? (
                    <DailyEffects detail={detail} stats={stats} />
                  ) : undefined
                }
                detail={detail}
                status={status}
                statusError={statusError}
              />
            ) : null}
          </TabsContent>
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
