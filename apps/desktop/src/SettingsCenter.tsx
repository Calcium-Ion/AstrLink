import { useWorkspaceSnapshot } from "./workspace-snapshots";
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { ChoiceCard } from "@/components/ChoiceCard";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { CopyableValue } from "@/components/CopyableValue";
import { DataRow } from "@/components/DataRow";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { InferencePortNotice } from "@/components/InferencePortNotice";
import { NumberInput } from "@/components/NumberField";
import { Panel, PanelFooter, PanelHeader } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";
import { SegmentedControl } from "@/components/SegmentedControl";
import { SettingRow, SettingSwitchRow } from "@/components/SettingRow";
import { StatusBadge } from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Menu, RefreshCw, Server, SlidersHorizontal } from "@/components/icons";
import { cn } from "@/lib/utils";
import { SyncSettings } from "./SyncSettings";

import {
  getCoreStatus,
  getPreferences,
  getTrayState,
  listNetworkAddresses,
  restartCore,
  startCore,
  stopCore,
  trayAction,
  updatePreferences,
} from "./bridge";
import {
  phaseLabel,
  phaseTone,
  type AppSnapshot,
  type NetworkAddress,
} from "./core-model";
import {
  applyLocale,
  currentLocale,
  i18n,
  LOCALE_NAMES,
  LOCALES,
  useT,
} from "./i18n";
import {
  INFERENCE_LISTENS,
  MAX_MAX_CONCURRENT_INSPECTIONS,
  MAX_REQUEST_BODY_MIB,
  MAX_RESPONSE_START_TIMEOUT_SECONDS,
  MIN_MAX_CONCURRENT_INSPECTIONS,
  TRAY_MENUBAR_TEXTS,
  TRAY_USAGE_KEYS,
  type DataBackupFile,
  type InferenceListen,
  type Preferences,
  type SettingsSnapshot,
  type TrayMenubarText,
  type TrayPreferences,
} from "./preferences-model";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";
import { applyQuotaDisplayMode, QUOTA_DISPLAY_MODES } from "./quota-display";
import {
  currencyName,
  DISPLAY_CURRENCIES,
  formatCurrency,
  formatRateDate,
  isDisplayCurrencyChoice,
  useDisplayCurrency,
  type DisplayCurrency,
} from "./currency-display";
import { applyTheme } from "./theme";
import { THEME_PREFERENCES } from "./theme-model";
import type { TrayState } from "./tray-model";
import { TrayPopoverPanel } from "./TrayPopover";

const TRAY_PREVIEW_REFRESH_MS = 30_000;
/** Time for a requested usage collection to land before the preview re-reads. */
const TRAY_PREVIEW_SETTLE_MS = 2_500;

type SettingsTab = "general" | "gateway" | "tray" | "sync";

type InstantPatch = Omit<
  Preferences,
  | "inference_port"
  | "inference_listen"
  | "use_system_proxy"
  | "max_concurrent_inspections"
  | "response_start_timeout_seconds"
  | "max_request_body_mib"
>;

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : i18n.t("settings.failed");
}

/** The URL a client on another device types for one of this machine's addresses. */
function networkClientURL(ip: string, port: number): string {
  return ip.includes(":") ? `http://[${ip}]:${port}` : `http://${ip}:${port}`;
}

function activePort(snapshot: AppSnapshot | null): number | null {
  if (!snapshot?.ready) return null;
  try {
    const port = Number(new URL(snapshot.ready.inference_url).port);
    return Number.isInteger(port) ? port : null;
  } catch {
    return null;
  }
}

function SettingsToggle({
  checked,
  disabled,
  hint,
  label,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  hint?: string;
  label: string;
  onChange: (checked: boolean) => void;
}) {
  const id = useId();

  return (
    <DataRow className={cn(disabled && "opacity-60")}>
      <Label
        className="block min-w-0 flex-1 cursor-pointer text-sm font-normal"
        htmlFor={id}
      >
        {label}
        {hint ? (
          <span className="mt-0.5 block text-xs text-muted-foreground">
            {hint}
          </span>
        ) : null}
      </Label>
      <Switch
        checked={checked}
        className="shrink-0"
        disabled={disabled}
        id={id}
        onCheckedChange={onChange}
      />
    </DataRow>
  );
}

function formatBackupSize(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return unit === 0 ? `${value} B` : `${value.toFixed(1)} ${units[unit]}`;
}

/** What the currency setting does, then the rate in use or why there is none. */
function currencyHint(
  t: ReturnType<typeof useT>,
  currency: DisplayCurrency,
): ReactNode {
  const hint = t("settings.currencyHint");
  if (currency.wanted === "USD") return hint;
  const status =
    currency.code === currency.wanted && currency.rate_date
      ? t("settings.currencyRate", {
          date: formatRateDate(currency.rate_date, currentLocale()),
          amount: formatCurrency(currency.per_usd, currency.digits, currency),
        })
      : t("settings.currencyPending");
  return (
    <>
      {hint}
      <span className="block">{status}</span>
    </>
  );
}

// Hand-made database copies are not encrypted with the live data; point them
// out but never offer to delete them from here.
function DataBackupNotice({ files }: { files: DataBackupFile[] }) {
  const t = useT();
  if (files.length === 0) return null;
  const total = files.reduce((sum, file) => sum + file.size_bytes, 0);
  const newest = files.reduce<number | null>(
    (latest, file) =>
      file.modified_unix !== null &&
      (latest === null || file.modified_unix > latest)
        ? file.modified_unix
        : latest,
    null,
  );
  const date =
    newest === null
      ? "—"
      : new Intl.DateTimeFormat(currentLocale(), {
          dateStyle: "medium",
        }).format(new Date(newest * 1000));
  return (
    <FormMessage className="flex items-center gap-1 py-1.5" tone="warning">
      <span className="min-w-0 flex-1">
        {t("settings.dataBackups", {
          count: files.length,
          size: formatBackupSize(total),
          date,
        })}
      </span>
      <HelpPopover label={t("settings.dataBackupsTitle")}>
        <span className="grid gap-1.5">
          <span>{t("settings.dataBackupsBody")}</span>
          <span className="grid gap-0.5 font-mono text-xs [overflow-wrap:anywhere]">
            {files.map((file) => (
              <span key={file.name}>{file.name}</span>
            ))}
          </span>
        </span>
      </HelpPopover>
    </FormMessage>
  );
}

function SettingsPanelHeader({
  hint,
  kicker,
  title,
}: {
  hint?: string;
  kicker: string;
  title: string;
}) {
  return (
    <PanelHeader>
      <SectionKicker>{kicker}</SectionKicker>
      <strong className="mt-1 block text-sm font-semibold tracking-tight">
        {title}
      </strong>
      {hint ? <p className="mt-1 text-xs text-text-secondary">{hint}</p> : null}
    </PanelHeader>
  );
}

export function SettingsCenter({
  snapshot,
  localDataNotice,
  onCoreSnapshot,
  onDirtyChange,
}: {
  snapshot: AppSnapshot | null;
  /** Shown above the other notices when saved data no longer decrypts. */
  localDataNotice?: ReactNode;
  onCoreSnapshot: (snapshot: AppSnapshot) => void;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const [savedSettings, cacheSettings] =
    useWorkspaceSnapshot<SettingsSnapshot | null>(
      "preferences",
      null,
      "desktop",
    );
  const [settings, setSettings] = useState(savedSettings);
  const mutationVersion = useRef(0);
  const [portDraft, setPortDraft] = useState<number | null>(
    settings?.values.inference_port ?? null,
  );
  const [listenDraft, setListenDraft] = useState<InferenceListen | null>(
    settings?.values.inference_listen ?? null,
  );
  const [listenConfirmOpen, setListenConfirmOpen] = useState(false);
  const [proxyDraft, setProxyDraft] = useState<boolean | null>(
    settings?.values.use_system_proxy ?? null,
  );
  const [addresses, setAddresses] = useState<NetworkAddress[] | null>(null);
  const [addressesError, setAddressesError] = useState<string | null>(null);
  const [concurrencyDraft, setConcurrencyDraft] = useState<number | null>(
    settings?.values.max_concurrent_inspections ?? null,
  );
  const [bodyLimitDraft, setBodyLimitDraft] = useState<number | null>(
    settings?.values.max_request_body_mib ?? null,
  );
  const [timeoutDraft, setTimeoutDraft] = useState<number | null>(
    settings?.values.response_start_timeout_seconds ?? null,
  );
  const [loadingError, setLoadingError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<
    "prefs" | "port" | "listen" | "start" | "stop" | "restart" | null
  >(null);
  const [tab, setTab] = useState<SettingsTab>("general");
  const [syncDirty, setSyncDirty] = useState(false);
  const [trayState, setTrayState] = useState<TrayState | null>(null);
  const [trayStateError, setTrayStateError] = useState<string | null>(null);
  const [now, setNow] = useState(() => new Date());

  const entryDirtyRef = useRef(false);
  const fieldId = useId();
  const currency = useDisplayCurrency();

  // The preview is the real panel over real data; refresh it on a slow tick.
  useEffect(() => {
    let cancelled = false;
    const load = () => {
      Promise.resolve()
        .then(() => getTrayState())
        .then((next) => {
          if (cancelled) return;
          setTrayState(next);
          setTrayStateError(null);
          setNow(new Date());
        })
        .catch((error) => {
          if (!cancelled) setTrayStateError(messageOf(error));
        });
    };
    load();
    const timer = window.setInterval(load, TRAY_PREVIEW_REFRESH_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, []);

  // The host only collects usage while someone is looking at it. Opening
  // the tray tab counts, so ask once and re-read after the numbers land.
  useEffect(() => {
    if (tab !== "tray") return;
    let cancelled = false;
    let timer: number | null = null;
    Promise.resolve()
      .then(() => trayAction({ kind: "refresh" }))
      .then(() => {
        timer = window.setTimeout(() => {
          timer = null;
          Promise.resolve()
            .then(() => getTrayState())
            .then((next) => {
              if (cancelled) return;
              setTrayState(next);
              setTrayStateError(null);
              setNow(new Date());
            })
            .catch(() => {});
        }, TRAY_PREVIEW_SETTLE_MS);
      })
      .catch(() => {
        // Browser preview has no host; the periodic read still runs.
      });
    return () => {
      cancelled = true;
      if (timer !== null) window.clearTimeout(timer);
    };
  }, [tab]);

  useEffect(() => {
    let cancelled = false;
    const version = mutationVersion.current;
    void getPreferences()
      .then((next) => {
        if (!cancelled && mutationVersion.current === version) {
          cacheSettings(next);
          setSettings(next);
          if (!entryDirtyRef.current) {
            setPortDraft(next.values.inference_port);
            setListenDraft(next.values.inference_listen);
            setProxyDraft(next.values.use_system_proxy);
            setConcurrencyDraft(next.values.max_concurrent_inspections);
            setTimeoutDraft(next.values.response_start_timeout_seconds);
            setBodyLimitDraft(next.values.max_request_body_mib);
          }
          setLoadingError(null);
        }
      })
      .catch((error) => {
        if (!cancelled && mutationVersion.current === version)
          setLoadingError(messageOf(error));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // The running Core answers other devices only when it was started that
  // way; a saved preference waits for the next restart.
  const exposedActive =
    snapshot?.phase === "ready" &&
    snapshot.inference_listen_active === "all_interfaces";
  useEffect(() => {
    if (!exposedActive) {
      setAddresses(null);
      setAddressesError(null);
      return;
    }
    let cancelled = false;
    listNetworkAddresses()
      .then((response) => {
        if (cancelled) return;
        setAddresses(response.addresses);
        setAddressesError(null);
      })
      .catch((error) => {
        if (!cancelled) setAddressesError(messageOf(error));
      });
    return () => {
      cancelled = true;
    };
  }, [exposedActive]);

  const active = activePort(snapshot);
  const listenDirty =
    settings !== null &&
    listenDraft !== null &&
    listenDraft !== settings.values.inference_listen;
  const proxyDirty =
    settings !== null &&
    proxyDraft !== null &&
    proxyDraft !== settings.values.use_system_proxy;
  const portDirty = useMemo(
    () =>
      settings !== null &&
      portDraft !== null &&
      portDraft !== settings.values.inference_port,
    [portDraft, settings],
  );
  const concurrencyDirty = useMemo(
    () =>
      settings !== null &&
      concurrencyDraft !== null &&
      concurrencyDraft !== settings.values.max_concurrent_inspections,
    [concurrencyDraft, settings],
  );
  const timeoutDirty = useMemo(
    () =>
      settings !== null &&
      timeoutDraft !== null &&
      timeoutDraft !== settings.values.response_start_timeout_seconds,
    [timeoutDraft, settings],
  );
  const bodyLimitDirty =
    settings !== null &&
    bodyLimitDraft !== null &&
    bodyLimitDraft !== settings.values.max_request_body_mib;
  const bodyLimitValid =
    bodyLimitDraft !== null &&
    Number.isInteger(bodyLimitDraft) &&
    bodyLimitDraft >= 0 &&
    bodyLimitDraft <= MAX_REQUEST_BODY_MIB;
  const entryDirty =
    portDirty ||
    listenDirty ||
    proxyDirty ||
    concurrencyDirty ||
    timeoutDirty ||
    bodyLimitDirty;
  entryDirtyRef.current = entryDirty;
  const editorDirty = entryDirty || syncDirty;
  useEffect(() => {
    onDirtyChange(editorDirty);
    return () => onDirtyChange(false);
  }, [editorDirty, onDirtyChange]);

  const gatewayRunning =
    snapshot != null &&
    !["stopped", "exited", "error", "unavailable"].includes(snapshot.phase);

  /** Says when a saved launch setting applies, and offers the restart it needs. */
  const notifyLaunchSaved = (): void => {
    if (!gatewayRunning) {
      notify.success(i18n.t("settings.notifySavedNextStart"));
      return;
    }
    notify.success(i18n.t("settings.notifySavedRestart"), {
      label: i18n.t("settings.restartNow"),
      onClick: () => void runCoreAction("restart"),
    });
    // Show the pending restart now instead of at the next status poll.
    getCoreStatus().then(onCoreSnapshot, () => undefined);
  };

  const applyInstant = async (patch: Partial<InstantPatch>): Promise<void> => {
    if (!settings || busy !== null) return;
    const previous = settings;
    const values: Preferences = {
      ...settings.values,
      ...patch,
      inference_port: settings.values.inference_port,
      inference_listen: settings.values.inference_listen,
      use_system_proxy: settings.values.use_system_proxy,
      max_concurrent_inspections: settings.values.max_concurrent_inspections,
      response_start_timeout_seconds:
        settings.values.response_start_timeout_seconds,
      max_request_body_mib: settings.values.max_request_body_mib,
    };
    mutationVersion.current += 1;
    setBusy("prefs");
    setActionError(null);
    setSettings({ ...settings, values });
    try {
      const next = await updatePreferences(values);
      cacheSettings(next);
      setSettings(next);
      if (patch.quota_display_mode !== undefined)
        applyQuotaDisplayMode(next.values.quota_display_mode);
      if (patch.theme !== undefined) applyTheme(next.values.theme);
      if (patch.locale && patch.locale !== previous.values.locale) {
        await applyLocale(patch.locale);
      }
    } catch (error) {
      setSettings(previous);
      setActionError(messageOf(error));
    } finally {
      setBusy(null);
    }
  };

  const runCoreAction = async (
    action: "start" | "stop" | "restart",
  ): Promise<void> => {
    setBusy(action);
    setActionError(null);
    try {
      const next =
        action === "start"
          ? await startCore()
          : action === "stop"
            ? await stopCore()
            : await restartCore();
      onCoreSnapshot(next);
      notify.success(
        action === "start"
          ? i18n.t("settings.notifyStart")
          : action === "stop"
            ? i18n.t("settings.notifyStop")
            : i18n.t("settings.notifyRestart"),
      );
    } catch (error) {
      setActionError(messageOf(error));
    } finally {
      setBusy(null);
    }
  };

  /** Opening to every interface is confirmed first; everything else applies to the draft. */
  const selectListen = (value: InferenceListen): void => {
    const current = listenDraft ?? settings?.values.inference_listen;
    if (value === "all_interfaces" && current !== "all_interfaces") {
      setListenConfirmOpen(true);
      return;
    }
    setListenDraft(value);
  };

  /** Saves the entry drafts; `revert` saves loopback right away, dirty or not. */
  const saveEntry = async (
    revert?: Pick<Preferences, "inference_listen">,
  ): Promise<void> => {
    if (
      !settings ||
      portDraft === null ||
      listenDraft === null ||
      proxyDraft === null ||
      concurrencyDraft === null ||
      timeoutDraft === null ||
      bodyLimitDraft === null ||
      !bodyLimitValid ||
      (!entryDirty && !revert)
    ) {
      return;
    }
    mutationVersion.current += 1;
    setBusy(revert ? "listen" : "port");
    setActionError(null);
    try {
      const next = await updatePreferences({
        ...settings.values,
        inference_port: portDraft,
        inference_listen: revert?.inference_listen ?? listenDraft,
        use_system_proxy: proxyDraft,
        max_concurrent_inspections: concurrencyDraft,
        response_start_timeout_seconds: timeoutDraft,
        max_request_body_mib: bodyLimitDraft,
      });
      cacheSettings(next);
      setSettings(next);
      setPortDraft(next.values.inference_port);
      setListenDraft(next.values.inference_listen);
      setProxyDraft(next.values.use_system_proxy);
      setConcurrencyDraft(next.values.max_concurrent_inspections);
      setTimeoutDraft(next.values.response_start_timeout_seconds);
      setBodyLimitDraft(next.values.max_request_body_mib);
      notifyLaunchSaved();
    } catch (error) {
      setActionError(messageOf(error));
    } finally {
      setBusy(null);
    }
  };

  if (loadingError && !settings) {
    return (
      <section className="gutter-frame grid gap-4 pb-2">
        <PageHeader title={t("settings.title")} />
        <Panel className="grid gap-2.5 border-destructive/35 bg-danger-wash p-4 text-danger-foreground">
          <strong className="text-sm font-semibold">
            {t("settings.loadFailed")}
          </strong>
          <p className="text-xs">{loadingError}</p>
          <Button
            className="justify-self-start"
            variant="outline"
            onClick={() => window.location.reload()}
            type="button"
          >
            {t("settings.reload")}
          </Button>
        </Panel>
      </section>
    );
  }
  if (
    !settings ||
    portDraft === null ||
    proxyDraft === null ||
    concurrencyDraft === null ||
    timeoutDraft === null ||
    bodyLimitDraft === null
  ) {
    return (
      <section className="gutter-frame grid gap-4 pb-2">
        <PageHeader title={t("settings.title")} />
        <p className="text-xs text-text-secondary">{t("settings.loading")}</p>
      </section>
    );
  }

  const prefs = settings.values;
  const prefsBusy = busy === "prefs";
  const applyTray = (patch: Partial<TrayPreferences>) =>
    void applyInstant({ tray: { ...prefs.tray, ...patch } });
  const phase = snapshot?.phase ?? "unavailable";
  const canStart = ["stopped", "exited", "error"].includes(phase);
  const canStop = gatewayRunning;
  const recoveryHint =
    snapshot?.recovery_scheduled_in_ms !== null &&
    snapshot?.recovery_scheduled_in_ms !== undefined
      ? t("settings.recoveryScheduled", {
          attempt: snapshot.recovery_attempt,
          seconds: Math.ceil(snapshot.recovery_scheduled_in_ms / 1000),
        })
      : snapshot?.recovery_attempt
        ? t("settings.recoveryAttempted", {
            attempt: snapshot.recovery_attempt,
          })
        : null;
  const restartPending = snapshot?.restart_pending === true;
  const listenValue = listenDraft ?? settings.values.inference_listen;
  const exposedPort = active ?? settings.values.inference_port;
  // A port waiting for a restart differs from the one in use; a fallback
  // port with nothing pending is what clients use, so show its address.
  const portChanging =
    active !== null && portDraft !== active && (portDirty || restartPending);
  const clientURL = (
    snapshot?.ready?.client_inference_url ??
    `http://localhost:${settings.values.inference_port}`
  ).replace(/\/+$/, "");
  const [addressBefore, addressAfter = ""] = t("settings.portAddressHint", {
    url: "\u0000",
  }).split("\u0000");
  const portHint = portChanging ? (
    t("settings.portChanging", { active, draft: portDraft })
  ) : (
    <>
      {addressBefore}
      <span className="font-mono">{clientURL}</span>
      {addressAfter}
    </>
  );

  return (
    <section className="gutter-frame flex h-full min-h-0 min-w-0 flex-col overflow-hidden">
      <PageHeader title={t("settings.title")} />

      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3">
        {localDataNotice}
        <InferencePortNotice snapshot={snapshot} />
        {loadingError ? (
          <FormMessage tone="error">{loadingError}</FormMessage>
        ) : null}

        {settings.load_warning ? (
          <FormMessage tone="warning">{settings.load_warning}</FormMessage>
        ) : null}
        {settings.autostart_error ? (
          <FormMessage tone="warning">{settings.autostart_error}</FormMessage>
        ) : settings.autostart_actual !== prefs.autostart ? (
          <FormMessage tone="warning">
            {t("settings.autostartMismatch")}
          </FormMessage>
        ) : null}
        <DataBackupNotice files={settings.data_backups} />
        {settings.local_key_storage === "keychain_unavailable" ? (
          <FormMessage tone="warning">
            {t("settings.localKeyKeychainUnavailable")}
          </FormMessage>
        ) : null}
        {actionError ? (
          <FormMessage tone="error">{actionError}</FormMessage>
        ) : null}

        <Tabs
          className="min-h-0 min-w-0 flex-1 gap-3 overflow-y-clip"
          onValueChange={(value) => setTab(value as SettingsTab)}
          value={tab}
        >
          <TabsList
            aria-label={t("settings.tabsLabel")}
            className="shrink-0"
            scrollable
          >
            <TabsTrigger value="general">
              <SlidersHorizontal aria-hidden="true" />
              {t("settings.tabs.general")}
            </TabsTrigger>
            <TabsTrigger value="gateway">
              <Server aria-hidden="true" />
              {t("settings.tabs.gateway")}
            </TabsTrigger>
            <TabsTrigger value="tray">
              <Menu aria-hidden="true" />
              {t("settings.tabs.tray")}
            </TabsTrigger>
            <TabsTrigger value="sync">
              <RefreshCw aria-hidden="true" />
              {t("settings.tabs.sync")}
            </TabsTrigger>
          </TabsList>

          <TabsContent
            className="gutter-scroller min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
            data-tab-scroller
            value="general"
          >
            <div className="grid min-w-0 gap-3 pr-1 pb-2">
              <Panel>
                <PanelHeader>
                  <h2 className="text-sm font-semibold">
                    {t("settings.appearance")}
                  </h2>
                </PanelHeader>
                <SettingRow
                  hint={t("settings.themeHint")}
                  label={t("settings.theme")}
                >
                  <SegmentedControl
                    disabled={prefsBusy}
                    label={t("settings.theme")}
                    onValueChange={(theme) => void applyInstant({ theme })}
                    options={THEME_PREFERENCES.map((theme) => ({
                      value: theme,
                      label: t(`settings.themeOptions.${theme}`),
                    }))}
                    value={prefs.theme}
                  />
                </SettingRow>
                <SettingRow
                  hint={t("settings.languageHint")}
                  label={t("settings.language")}
                >
                  <SegmentedControl
                    disabled={prefsBusy}
                    label={t("settings.language")}
                    onValueChange={(locale) => void applyInstant({ locale })}
                    options={LOCALES.map((locale) => ({
                      value: locale,
                      label: LOCALE_NAMES[locale],
                    }))}
                    value={prefs.locale}
                  />
                </SettingRow>
                <SettingRow
                  hint={t("settings.quotaDisplayHint")}
                  label={t("settings.quotaDisplayMode")}
                >
                  <SegmentedControl
                    disabled={prefsBusy}
                    label={t("settings.quotaDisplayMode")}
                    onValueChange={(quota_display_mode) =>
                      void applyInstant({ quota_display_mode })
                    }
                    options={QUOTA_DISPLAY_MODES.map((mode) => ({
                      value: mode,
                      label: t(`settings.quotaDisplayOptions.${mode}`),
                    }))}
                    value={prefs.quota_display_mode}
                  />
                </SettingRow>
                <SettingRow
                  hint={currencyHint(t, currency)}
                  hintId={`${fieldId}-currency-hint`}
                  label={t("settings.currency")}
                >
                  <Select
                    disabled={prefsBusy}
                    onValueChange={(display_currency) => {
                      // Radix reports "" while it remounts; not a choice.
                      if (isDisplayCurrencyChoice(display_currency))
                        void applyInstant({ display_currency });
                    }}
                    value={prefs.display_currency}
                  >
                    <SelectTrigger
                      aria-describedby={`${fieldId}-currency-hint`}
                      aria-label={t("settings.currency")}
                      className="w-56"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="auto">
                        {t("settings.currencyAuto", {
                          name: currencyName(currency.system, currentLocale()),
                        })}
                      </SelectItem>
                      {DISPLAY_CURRENCIES.map((code) => (
                        <SelectItem key={code} value={code}>
                          {currencyName(code, currentLocale())} {code}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </SettingRow>
              </Panel>

              <Panel>
                <PanelHeader>
                  <h2 className="text-sm font-semibold">
                    {t("settings.windowTitle")}
                  </h2>
                </PanelHeader>
                <SettingRow
                  hint={t("settings.onCloseHint")}
                  label={t("settings.onClose")}
                >
                  <SegmentedControl
                    disabled={prefsBusy}
                    label={t("settings.onClose")}
                    onValueChange={(close_behavior) =>
                      void applyInstant({ close_behavior })
                    }
                    options={[
                      {
                        value: "hide_to_tray",
                        label: t("settings.hideToTray"),
                      },
                      { value: "quit", label: t("settings.quit") },
                    ]}
                    value={prefs.close_behavior}
                  />
                </SettingRow>
                <SettingSwitchRow
                  checked={prefs.autostart}
                  disabled={prefsBusy}
                  hint={t("settings.autostartHint")}
                  label={t("settings.autostart")}
                  onCheckedChange={(autostart) =>
                    void applyInstant({ autostart })
                  }
                />
                <SettingSwitchRow
                  checked={prefs.autostart_hidden}
                  disabled={prefsBusy || !prefs.autostart}
                  hint={t("settings.autostartHiddenHint")}
                  inset
                  label={t("settings.autostartHidden")}
                  onCheckedChange={(autostart_hidden) =>
                    void applyInstant({ autostart_hidden })
                  }
                />
              </Panel>
            </div>
          </TabsContent>

          <TabsContent
            className="gutter-scroller min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
            data-tab-scroller
            value="gateway"
          >
            <div className="grid min-w-0 gap-3 pr-1 pb-2">
              <Panel>
                <PanelHeader
                  actions={
                    <>
                      {canStart ? (
                        <Button
                          disabled={busy !== null}
                          onClick={() => void runCoreAction("start")}
                          size="sm"
                          type="button"
                          variant="outline"
                        >
                          {busy === "start"
                            ? t("settings.starting")
                            : t("settings.start")}
                        </Button>
                      ) : (
                        <Button
                          disabled={!canStop || busy !== null}
                          onClick={() => void runCoreAction("stop")}
                          size="sm"
                          type="button"
                          variant="outline"
                        >
                          {busy === "stop"
                            ? t("settings.stopping")
                            : t("settings.stop")}
                        </Button>
                      )}
                      <Button
                        disabled={phase === "unavailable" || busy !== null}
                        onClick={() => void runCoreAction("restart")}
                        size="sm"
                        type="button"
                        variant="outline"
                      >
                        {busy === "restart"
                          ? t("settings.restarting")
                          : t("settings.restart")}
                      </Button>
                    </>
                  }
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-sm font-semibold">
                      {t("settings.gatewayTitle")}
                    </h2>
                    <StatusBadge tone={phaseTone(phase)}>
                      {phaseLabel(phase)}
                    </StatusBadge>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {t("settings.gatewayHint")}
                  </p>
                  {recoveryHint ? (
                    <p className="mt-1 text-xs text-warning-foreground">
                      {recoveryHint}
                    </p>
                  ) : null}
                </PanelHeader>
                {snapshot?.last_error ? (
                  <div className="border-b px-4 py-3">
                    <code className="block rounded-sm border bg-card px-2 py-1.5 font-mono text-xs whitespace-normal text-text-secondary [overflow-wrap:anywhere]">
                      {snapshot.last_error}
                    </code>
                  </div>
                ) : null}
                <SettingSwitchRow
                  checked={prefs.core_auto_start}
                  disabled={prefsBusy}
                  hint={t("settings.coreAutoStartHint")}
                  label={t("settings.coreAutoStart")}
                  onCheckedChange={(core_auto_start) =>
                    void applyInstant({ core_auto_start })
                  }
                />
                <SettingSwitchRow
                  checked={prefs.core_auto_recover}
                  disabled={prefsBusy}
                  hint={t("settings.coreAutoRecoverHint")}
                  label={t("settings.coreAutoRecover")}
                  onCheckedChange={(core_auto_recover) =>
                    void applyInstant({ core_auto_recover })
                  }
                />
              </Panel>

              <Panel
                className={cn(
                  "flex flex-col",
                  entryDirty && "border-warning/50",
                )}
              >
                <PanelHeader>
                  <h2 className="text-sm font-semibold">
                    {t("settings.connectionTitle")}
                  </h2>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {t("settings.connectionHint")}
                  </p>
                </PanelHeader>
                <SettingRow
                  hint={portHint}
                  hintId={`${fieldId}-port-hint`}
                  htmlFor={`${fieldId}-port`}
                  label={t("settings.portField")}
                >
                  <Input
                    aria-describedby={`${fieldId}-port-hint`}
                    className="h-8 w-24 font-mono tabular-nums"
                    id={`${fieldId}-port`}
                    max={65535}
                    min={1024}
                    onChange={(event) =>
                      setPortDraft(Number(event.target.value))
                    }
                    type="number"
                    value={portDraft}
                  />
                </SettingRow>
                <SettingRow
                  hint={t("settings.listenHint")}
                  label={t("settings.listenField")}
                >
                  <SegmentedControl
                    disabled={busy !== null}
                    label={t("settings.listenField")}
                    onValueChange={selectListen}
                    options={INFERENCE_LISTENS.map((listen) => ({
                      value: listen,
                      label: t(`settings.listenOptions.${listen}`),
                    }))}
                    value={listenValue}
                  />
                </SettingRow>
                {exposedActive ? (
                  <div className="border-b px-4 py-3">
                    <FormMessage asChild tone="warning">
                      <div className="grid gap-2">
                        <p>
                          {t("settings.listenExposedNotice", {
                            port: exposedPort,
                          })}
                        </p>
                        {addressesError ? (
                          <p>
                            {t("settings.listenExposedAddressesFailed", {
                              error: addressesError,
                            })}
                          </p>
                        ) : addresses === null ? null : addresses.length ===
                          0 ? (
                          <p>{t("settings.listenExposedNoAddresses")}</p>
                        ) : (
                          <ul className="grid gap-1">
                            {addresses.map((address) => (
                              <li key={`${address.interface}-${address.ip}`}>
                                <CopyableValue
                                  copyLabel={t("settings.listenAddressCopy")}
                                  label={networkClientURL(
                                    address.ip,
                                    exposedPort,
                                  )}
                                  placeholder=""
                                  value={networkClientURL(
                                    address.ip,
                                    exposedPort,
                                  )}
                                />
                              </li>
                            ))}
                          </ul>
                        )}
                        <div>
                          <Button
                            disabled={
                              busy !== null ||
                              settings.values.inference_listen === "loopback"
                            }
                            onClick={() =>
                              void saveEntry({ inference_listen: "loopback" })
                            }
                            size="sm"
                            type="button"
                            variant="outline"
                          >
                            {busy === "listen"
                              ? t("settings.listenReverting")
                              : t("settings.listenRevert")}
                          </Button>
                        </div>
                      </div>
                    </FormMessage>
                  </div>
                ) : null}
                <SettingSwitchRow
                  checked={proxyDraft}
                  disabled={busy !== null}
                  hint={t("settings.useSystemProxyHint")}
                  label={t("settings.useSystemProxy")}
                  onCheckedChange={setProxyDraft}
                />
                <div className="border-b bg-muted/40 px-4 py-2">
                  <SectionKicker>{t("settings.requestHandling")}</SectionKicker>
                </div>
                <SettingRow
                  hint={t("settings.concurrencyHint")}
                  hintId={`${fieldId}-concurrency-hint`}
                  htmlFor={`${fieldId}-concurrency`}
                  label={t("settings.concurrencyField")}
                >
                  <NumberInput
                    className="h-8 w-32"
                    describedBy={`${fieldId}-concurrency-hint`}
                    id={`${fieldId}-concurrency`}
                    label={t("settings.concurrencyField")}
                    max={MAX_MAX_CONCURRENT_INSPECTIONS}
                    min={MIN_MAX_CONCURRENT_INSPECTIONS}
                    onChange={setConcurrencyDraft}
                    unit={t("settings.concurrencyUnit")}
                    value={concurrencyDraft}
                  />
                </SettingRow>
                <SettingRow
                  hint={t("settings.responseStartTimeoutHint")}
                  hintId={`${fieldId}-timeout-hint`}
                  htmlFor={`${fieldId}-timeout`}
                  label={t("settings.responseStartTimeoutField")}
                >
                  <NumberInput
                    className="h-8 w-32"
                    describedBy={`${fieldId}-timeout-hint`}
                    id={`${fieldId}-timeout`}
                    label={t("settings.responseStartTimeoutField")}
                    max={MAX_RESPONSE_START_TIMEOUT_SECONDS}
                    min={0}
                    onChange={setTimeoutDraft}
                    unit={t("settings.secondsUnit")}
                    value={timeoutDraft}
                  />
                </SettingRow>
                <SettingRow
                  hint={t("settings.requestBodyLimitHint")}
                  hintId={`${fieldId}-body-hint`}
                  htmlFor={`${fieldId}-body`}
                  label={t("settings.requestBodyLimitField")}
                >
                  <NumberInput
                    className="h-8 w-32"
                    describedBy={`${fieldId}-body-hint`}
                    id={`${fieldId}-body`}
                    invalid={!bodyLimitValid}
                    label={t("settings.requestBodyLimitField")}
                    max={MAX_REQUEST_BODY_MIB}
                    min={0}
                    onChange={setBodyLimitDraft}
                    unit="MiB"
                    value={bodyLimitDraft}
                  />
                </SettingRow>
                <PanelFooter
                  actions={
                    <>
                      {restartPending && !entryDirty ? (
                        <Button
                          disabled={busy !== null}
                          onClick={() => void runCoreAction("restart")}
                          type="button"
                          variant="outline"
                        >
                          {busy === "restart"
                            ? t("settings.restarting")
                            : t("settings.restartNow")}
                        </Button>
                      ) : null}
                      <Button
                        disabled={
                          !entryDirty || !bodyLimitValid || busy !== null
                        }
                        onClick={() => void saveEntry()}
                        type="button"
                      >
                        {busy === "port"
                          ? t("settings.savingPort")
                          : t("settings.savePort")}
                      </Button>
                    </>
                  }
                  className="py-2.5"
                >
                  <p
                    className={cn(
                      "text-xs",
                      entryDirty || restartPending
                        ? "text-warning-foreground"
                        : "text-muted-foreground",
                    )}
                  >
                    {entryDirty
                      ? t("settings.entryDirty")
                      : restartPending
                        ? t("settings.restartPending")
                        : t("settings.entryHint")}
                  </p>
                </PanelFooter>
              </Panel>
              <ConfirmDialog
                cancelLabel={t("settings.listenKeepLoopback")}
                confirmLabel={t("settings.listenConfirm")}
                description={
                  <p>
                    {t("settings.listenConfirmBody", {
                      port: portDraft ?? settings.values.inference_port,
                    })}
                  </p>
                }
                onCancel={() => setListenConfirmOpen(false)}
                onConfirm={() => {
                  setListenDraft("all_interfaces");
                  setListenConfirmOpen(false);
                }}
                open={listenConfirmOpen}
                title={t("settings.listenConfirmTitle")}
              />
            </div>
          </TabsContent>

          <TabsContent
            className="gutter-scroller min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
            data-tab-scroller
            value="tray"
          >
            <div className="grid grid-cols-[minmax(0,1fr)_396px] items-start gap-3 pb-2 pr-1 max-[920px]:grid-cols-1">
              <div className="grid min-w-0 gap-3">
                <Panel className="min-w-0">
                  <SettingsPanelHeader
                    hint={t("settings.trayMenubarTextHint")}
                    kicker={t("settings.trayKicker")}
                    title={t("settings.trayMenubarText")}
                  />
                  <div className="px-4 py-3">
                    <RadioGroup
                      aria-label={t("settings.trayMenubarText")}
                      className={cn(
                        "grid grid-cols-3 gap-2 max-[560px]:grid-cols-2",
                        prefsBusy && "pointer-events-none opacity-60",
                      )}
                      disabled={prefsBusy}
                      onValueChange={(value) =>
                        applyTray({ menubar_text: value as TrayMenubarText })
                      }
                      value={prefs.tray.menubar_text}
                    >
                      {TRAY_MENUBAR_TEXTS.map((option) => (
                        <ChoiceCard
                          key={option}
                          disabled={prefsBusy}
                          label={t(`settings.trayMenubarOptions.${option}`)}
                          selected={prefs.tray.menubar_text === option}
                          value={option}
                        />
                      ))}
                    </RadioGroup>
                  </div>
                </Panel>

                <Panel className="min-w-0">
                  <SettingsPanelHeader
                    kicker={t("settings.trayKicker")}
                    title={t("settings.trayUsageSection")}
                  />
                  {TRAY_USAGE_KEYS.map((key) => (
                    <SettingsToggle
                      key={key}
                      checked={prefs.tray.usage[key]}
                      disabled={prefsBusy}
                      hint={t(`settings.trayUsage.${key}Hint`)}
                      label={t(`settings.trayUsage.${key}`)}
                      onChange={(checked) =>
                        applyTray({
                          usage: { ...prefs.tray.usage, [key]: checked },
                        })
                      }
                    />
                  ))}
                </Panel>

                <Panel className="min-w-0">
                  <SettingsPanelHeader
                    kicker={t("settings.trayKicker")}
                    title={t("settings.trayActionsSection")}
                  />
                  <SettingsToggle
                    checked={prefs.tray.copy_address}
                    disabled={prefsBusy}
                    label={t("settings.trayCopyAddress")}
                    onChange={(copy_address) => applyTray({ copy_address })}
                  />
                  <SettingsToggle
                    checked={prefs.tray.gateway_controls}
                    disabled={prefsBusy}
                    label={t("settings.trayGatewayControls")}
                    onChange={(gateway_controls) =>
                      applyTray({ gateway_controls })
                    }
                  />
                </Panel>
              </div>

              <Panel
                className="sticky top-0 min-w-0 max-[920px]:static max-[920px]:order-first"
                tone="inset"
              >
                <SettingsPanelHeader
                  hint={t("settings.trayPanelHint")}
                  kicker={t("settings.trayKicker")}
                  title={t("settings.trayPreview")}
                />
                <div className="flex flex-col gap-2 p-4">
                  <div
                    className="mx-auto w-full max-w-[340px]"
                    data-slot="tray-preview"
                  >
                    <TrayPopoverPanel
                      now={now}
                      onAction={() => {}}
                      preview
                      state={trayState}
                      tray={prefs.tray}
                    />
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {trayStateError
                      ? t("settings.trayPreviewFailed")
                      : t("settings.trayPreviewHint")}
                  </p>
                </div>
              </Panel>
            </div>
          </TabsContent>

          <TabsContent
            className="gutter-scroller min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
            data-tab-scroller
            value="sync"
          >
            <SyncSettings onDirtyChange={setSyncDirty} />
          </TabsContent>
        </Tabs>
      </div>
    </section>
  );
}
