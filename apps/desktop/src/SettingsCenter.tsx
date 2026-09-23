import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { useEffect, useId, useMemo, useRef, useState } from "react";

import { ChoiceCard } from "@/components/ChoiceCard";
import { DataRow } from "@/components/DataRow";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { InferencePortNotice } from "@/components/InferencePortNotice";
import { Panel, PanelHeader } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Menu, SlidersHorizontal } from "@/components/icons";
import { cn } from "@/lib/utils";

import {
  getPreferences,
  getTrayState,
  restartCore,
  startCore,
  stopCore,
  trayAction,
  updatePreferences,
} from "./bridge";
import { phaseLabel, phaseTone, type AppSnapshot } from "./core-model";
import { applyLocale, i18n, useT, type Locale } from "./i18n";
import {
  MAX_MAX_CONCURRENT_INSPECTIONS,
  MAX_REQUEST_BODY_MIB,
  MAX_RESPONSE_START_TIMEOUT_SECONDS,
  MIN_MAX_CONCURRENT_INSPECTIONS,
  TRAY_MENUBAR_TEXTS,
  TRAY_PAGES,
  TRAY_USAGE_KEYS,
  type Preferences,
  type SettingsSnapshot,
  type TrayMenubarText,
  type TrayPage,
  type TrayPreferences,
} from "./preferences-model";
import { notify } from "./notify";
import { PageHeader } from "./PageHeader";
import {
  applyQuotaDisplayMode,
  QUOTA_DISPLAY_MODES,
  type QuotaDisplayMode,
} from "./quota-display";
import { applyTheme } from "./theme";
import { THEME_PREFERENCES, type ThemePreference } from "./theme-model";
import type { TrayState } from "./tray-model";
import { TrayPopoverPanel } from "./TrayPopover";

const TRAY_PREVIEW_REFRESH_MS = 30_000;
/** Time for a requested usage collection to land before the preview re-reads. */
const TRAY_PREVIEW_SETTLE_MS = 2_500;

type SettingsTab = "general" | "tray";

const pageLabelKeys: Record<TrayPage, string> = {
  records: "nav.records",
  services: "nav.services",
  tokens: "nav.tokens",
  safety: "nav.safety",
  routing: "nav.routing",
  agent_tools: "nav.agentTools",
};

type InstantPatch = Omit<
  Preferences,
  | "inference_port"
  | "max_concurrent_inspections"
  | "response_start_timeout_seconds"
  | "max_request_body_mib"
>;

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : i18n.t("settings.failed");
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
  onCoreSnapshot,
  onDirtyChange,
}: {
  snapshot: AppSnapshot | null;
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
    "prefs" | "port" | "start" | "stop" | "restart" | null
  >(null);
  const [tab, setTab] = useState<SettingsTab>("general");
  const [trayState, setTrayState] = useState<TrayState | null>(null);
  const [trayStateError, setTrayStateError] = useState<string | null>(null);
  const [now, setNow] = useState(() => new Date());

  const entryDirtyRef = useRef(false);

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

  const active = activePort(snapshot);
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
    portDirty || concurrencyDirty || timeoutDirty || bodyLimitDirty;
  entryDirtyRef.current = entryDirty;
  useEffect(() => {
    onDirtyChange(entryDirty);
    return () => onDirtyChange(false);
  }, [entryDirty, onDirtyChange]);

  const applyInstant = async (patch: Partial<InstantPatch>): Promise<void> => {
    if (!settings || busy !== null) return;
    const previous = settings;
    const values: Preferences = {
      ...settings.values,
      ...patch,
      inference_port: settings.values.inference_port,
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
      if (patch.use_system_proxy !== undefined) {
        notify.success(i18n.t("settings.notifyProxySaved"));
      }
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

  const savePort = async (): Promise<void> => {
    if (
      !settings ||
      portDraft === null ||
      concurrencyDraft === null ||
      timeoutDraft === null ||
      bodyLimitDraft === null ||
      !bodyLimitValid ||
      !entryDirty
    ) {
      return;
    }
    mutationVersion.current += 1;
    setBusy("port");
    setActionError(null);
    try {
      const next = await updatePreferences({
        ...settings.values,
        inference_port: portDraft,
        max_concurrent_inspections: concurrencyDraft,
        response_start_timeout_seconds: timeoutDraft,
        max_request_body_mib: bodyLimitDraft,
      });
      cacheSettings(next);
      setSettings(next);
      setPortDraft(next.values.inference_port);
      setConcurrencyDraft(next.values.max_concurrent_inspections);
      setTimeoutDraft(next.values.response_start_timeout_seconds);
      setBodyLimitDraft(next.values.max_request_body_mib);
      const gatewayRunning =
        snapshot != null &&
        !["stopped", "exited", "error", "unavailable"].includes(snapshot.phase);
      notify.success(
        gatewayRunning
          ? i18n.t("settings.notifyPortSavedRestart")
          : i18n.t("settings.notifyPortSaved"),
      );
    } catch (error) {
      setActionError(messageOf(error));
    } finally {
      setBusy(null);
    }
  };

  if (loadingError && !settings) {
    return (
      <section className="grid gap-4 pb-2">
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
    concurrencyDraft === null ||
    timeoutDraft === null ||
    bodyLimitDraft === null
  ) {
    return (
      <section className="grid gap-4 pb-2">
        <PageHeader title={t("settings.title")} />
        <p className="text-xs text-text-secondary">{t("settings.loading")}</p>
      </section>
    );
  }

  const prefs = settings.values;
  const prefsBusy = busy === "prefs";
  const applyTray = (patch: Partial<TrayPreferences>) =>
    void applyInstant({ tray: { ...prefs.tray, ...patch } });
  const togglePage = (page: TrayPage, enabled: boolean) => {
    const selected = new Set(prefs.tray.pages);
    if (enabled) selected.add(page);
    else selected.delete(page);
    // Keep navigation order regardless of the order pages were toggled in.
    applyTray({
      pages: TRAY_PAGES.filter((candidate) => selected.has(candidate)),
    });
  };
  const phase = snapshot?.phase ?? "unavailable";
  const tone = phaseTone(phase);
  const canStart = ["stopped", "exited", "error"].includes(phase);
  const canStop = !["stopped", "exited", "error", "unavailable"].includes(
    phase,
  );
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
  const portNeedsRestart =
    active !== null &&
    active !== settings.values.inference_port &&
    snapshot?.inference_port_fallback?.requested_port !==
      settings.values.inference_port;

  return (
    <section className="flex h-full min-h-0 min-w-0 flex-col overflow-hidden">
      <PageHeader title={t("settings.title")} />

      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3">
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
        {actionError ? (
          <FormMessage tone="error">{actionError}</FormMessage>
        ) : null}

        <Tabs
          className="min-h-0 min-w-0 flex-1 gap-3 overflow-hidden"
          onValueChange={(value) => setTab(value as SettingsTab)}
          value={tab}
        >
          <TabsList aria-label={t("settings.tabsLabel")} className="shrink-0">
            <TabsTrigger value="general">
              <SlidersHorizontal aria-hidden="true" />
              {t("settings.tabs.general")}
            </TabsTrigger>
            <TabsTrigger value="tray">
              <Menu aria-hidden="true" />
              {t("settings.tabs.tray")}
            </TabsTrigger>
          </TabsList>

          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
            data-tab-scroller
            value="general"
          >
            <div className="grid grid-cols-2 gap-3 pb-2 pr-1 max-[920px]:grid-cols-1">
              <Panel className="min-w-0">
                <SettingsPanelHeader
                  hint={t("settings.instantHint")}
                  kicker={t("settings.windowKicker")}
                  title={t("settings.desktopBehavior")}
                />

                <div className="grid gap-2 border-b px-4 py-3">
                  <span className="text-xs font-medium text-text-secondary">
                    {t("settings.theme")}
                  </span>
                  <RadioGroup
                    className="grid grid-cols-3 gap-2 max-[560px]:grid-cols-1"
                    aria-label={t("settings.theme")}
                    disabled={prefsBusy}
                    onValueChange={(value) =>
                      void applyInstant({ theme: value as ThemePreference })
                    }
                    value={prefs.theme}
                  >
                    {THEME_PREFERENCES.map((theme) => (
                      <ChoiceCard
                        key={theme}
                        disabled={prefsBusy}
                        label={t(`settings.themeOptions.${theme}`)}
                        selected={prefs.theme === theme}
                        value={theme}
                      />
                    ))}
                  </RadioGroup>
                </div>

                <div className="grid gap-2 border-b px-4 py-3">
                  <span className="text-xs font-medium text-text-secondary">
                    {t("settings.quotaDisplayMode")}
                  </span>
                  <RadioGroup
                    className="grid grid-cols-2 gap-2 max-[560px]:grid-cols-1"
                    aria-label={t("settings.quotaDisplayMode")}
                    disabled={prefsBusy}
                    onValueChange={(value) =>
                      void applyInstant({
                        quota_display_mode: value as QuotaDisplayMode,
                      })
                    }
                    value={prefs.quota_display_mode}
                  >
                    {QUOTA_DISPLAY_MODES.map((mode) => (
                      <ChoiceCard
                        key={mode}
                        disabled={prefsBusy}
                        label={t(`settings.quotaDisplayOptions.${mode}`)}
                        selected={prefs.quota_display_mode === mode}
                        value={mode}
                      />
                    ))}
                  </RadioGroup>
                  <p className="text-xs text-muted-foreground">
                    {t("settings.quotaDisplayHint")}
                  </p>
                </div>

                <div className="grid gap-2 border-b px-4 py-3">
                  <span className="text-xs font-medium text-text-secondary">
                    {t("settings.language")}
                  </span>
                  <RadioGroup
                    className={cn(
                      "grid grid-cols-2 gap-2 max-[560px]:grid-cols-1",
                      prefsBusy && "pointer-events-none opacity-60",
                    )}
                    aria-label={t("settings.language")}
                    disabled={prefsBusy}
                    onValueChange={(value) =>
                      void applyInstant({ locale: value as Locale })
                    }
                    value={prefs.locale}
                  >
                    <ChoiceCard
                      description={t("settings.languageHint")}
                      label="English"
                      selected={prefs.locale === "en"}
                      value="en"
                    />
                    <ChoiceCard
                      description={t("settings.languageHint")}
                      label="简体中文"
                      selected={prefs.locale === "zh-CN"}
                      value="zh-CN"
                    />
                  </RadioGroup>
                </div>

                <div className="grid gap-2 border-b px-4 py-3">
                  <span className="text-xs font-medium text-text-secondary">
                    {t("settings.onClose")}
                  </span>
                  <RadioGroup
                    className={cn(
                      "grid grid-cols-2 gap-2 max-[560px]:grid-cols-1",
                      prefsBusy && "pointer-events-none opacity-60",
                    )}
                    aria-label={t("settings.onClose")}
                    disabled={prefsBusy}
                    onValueChange={(value) =>
                      void applyInstant({
                        close_behavior: value as Preferences["close_behavior"],
                      })
                    }
                    value={prefs.close_behavior}
                  >
                    <ChoiceCard
                      description={t("settings.hideToTrayHint")}
                      label={t("settings.hideToTray")}
                      selected={prefs.close_behavior === "hide_to_tray"}
                      value="hide_to_tray"
                    />
                    <ChoiceCard
                      description={t("settings.quitHint")}
                      label={t("settings.quit")}
                      selected={prefs.close_behavior === "quit"}
                      value="quit"
                    />
                  </RadioGroup>
                </div>

                <SettingsToggle
                  checked={prefs.autostart}
                  disabled={prefsBusy}
                  label={t("settings.autostart")}
                  onChange={(autostart) => void applyInstant({ autostart })}
                />

                <p className="px-4 py-2.5 text-xs text-muted-foreground">
                  {t("settings.trayHint")}
                </p>
              </Panel>

              <Panel className="min-w-0">
                <SettingsPanelHeader
                  kicker={t("settings.runtimeKicker")}
                  title={t("settings.gatewayTitle")}
                />

                <SettingsToggle
                  checked={prefs.core_auto_start}
                  disabled={prefsBusy}
                  label={t("settings.coreAutoStart")}
                  onChange={(core_auto_start) =>
                    void applyInstant({ core_auto_start })
                  }
                />
                <SettingsToggle
                  checked={prefs.core_auto_recover}
                  disabled={prefsBusy}
                  label={t("settings.coreAutoRecover")}
                  onChange={(core_auto_recover) =>
                    void applyInstant({ core_auto_recover })
                  }
                />

                <SettingsToggle
                  checked={prefs.use_system_proxy}
                  disabled={busy !== null}
                  label={t("settings.useSystemProxy")}
                  hint={t("settings.systemProxyHint")}
                  onChange={(use_system_proxy) =>
                    void applyInstant({ use_system_proxy })
                  }
                />

                <div className="border-b px-4 py-3">
                  <div
                    className={cn(
                      "flex items-center gap-2.5 rounded-md border bg-muted px-3 py-2.5",
                      tone === "positive" &&
                        "border-success/25 bg-success-wash",
                      tone === "pending" && "border-warning/30 bg-warning-wash",
                      tone === "negative" &&
                        "border-destructive/25 bg-danger-wash",
                    )}
                  >
                    <StatusDot tone={tone} />
                    <div className="grid min-w-0 gap-0.5">
                      <strong className="text-sm font-medium">
                        {phaseLabel(phase)}
                      </strong>
                      <span className="truncate text-xs text-text-secondary">
                        {t("settings.currentStatus", { phase })}
                        {recoveryHint ? ` · ${recoveryHint}` : ""}
                      </span>
                    </div>
                  </div>

                  {snapshot?.last_error ? (
                    <code className="mt-2 block rounded-sm border bg-card px-2 py-1.5 font-mono text-xs whitespace-normal text-text-secondary [overflow-wrap:anywhere]">
                      {snapshot.last_error}
                    </code>
                  ) : null}
                </div>

                <div className="flex flex-wrap items-center gap-2 px-4 py-3">
                  <Button
                    disabled={!canStart || busy !== null}
                    onClick={() => void runCoreAction("start")}
                    type="button"
                  >
                    {busy === "start"
                      ? t("settings.starting")
                      : t("settings.start")}
                  </Button>
                  <Button
                    variant="outline"
                    disabled={!canStop || busy !== null}
                    onClick={() => void runCoreAction("stop")}
                    type="button"
                  >
                    {busy === "stop"
                      ? t("settings.stopping")
                      : t("settings.stop")}
                  </Button>
                  <Button
                    variant="outline"
                    disabled={phase === "unavailable" || busy !== null}
                    onClick={() => void runCoreAction("restart")}
                    type="button"
                  >
                    {busy === "restart"
                      ? t("settings.restarting")
                      : t("settings.restart")}
                  </Button>
                </div>
              </Panel>

              <Panel
                className={cn(
                  "col-span-full min-w-0 max-[920px]:col-auto",
                  entryDirty && "border-warning/50",
                )}
              >
                <SettingsPanelHeader
                  hint={t("settings.portHint")}
                  kicker={t("settings.portKicker")}
                  title={t("settings.portTitle")}
                />

                <div className="grid grid-cols-3 gap-3 border-b px-4 py-3 max-[560px]:grid-cols-1">
                  <Field label={t("settings.portField")}>
                    <Input
                      className="font-mono tabular-nums"
                      max={65535}
                      min={1024}
                      onChange={(event) =>
                        setPortDraft(Number(event.target.value))
                      }
                      type="number"
                      value={portDraft}
                    />
                  </Field>
                  <Field label={t("settings.portActive")}>
                    <span className="flex h-8 items-center rounded-md border bg-muted px-2.5 font-mono text-sm tabular-nums">
                      {active ?? t("settings.portNotReady")}
                    </span>
                  </Field>
                  <Field label={t("settings.portSaved")}>
                    <span className="flex h-8 items-center rounded-md border bg-muted px-2.5 font-mono text-sm tabular-nums">
                      {settings.values.inference_port}
                    </span>
                  </Field>
                </div>

                <div className="grid grid-cols-3 gap-3 border-b px-4 py-3 max-[560px]:grid-cols-1">
                  <Field label={t("settings.concurrencyField")}>
                    <Input
                      aria-label={t("settings.concurrencyField")}
                      className="font-mono tabular-nums"
                      max={MAX_MAX_CONCURRENT_INSPECTIONS}
                      min={MIN_MAX_CONCURRENT_INSPECTIONS}
                      onChange={(event) =>
                        setConcurrencyDraft(Number(event.target.value))
                      }
                      type="number"
                      value={concurrencyDraft}
                    />
                  </Field>
                  <Field label={t("settings.concurrencySaved")}>
                    <span className="flex h-8 items-center rounded-md border bg-muted px-2.5 font-mono text-sm tabular-nums">
                      {settings.values.max_concurrent_inspections}
                    </span>
                  </Field>
                  <p className="col-span-1 flex items-end text-xs text-muted-foreground max-[560px]:items-start">
                    {t("settings.concurrencyHint")}
                  </p>
                </div>

                <div className="grid grid-cols-3 gap-3 border-b px-4 py-3 max-[560px]:grid-cols-1">
                  <Field label={t("settings.responseStartTimeoutField")}>
                    <Input
                      aria-label={t("settings.responseStartTimeoutField")}
                      className="font-mono tabular-nums"
                      max={MAX_RESPONSE_START_TIMEOUT_SECONDS}
                      min={0}
                      onChange={(event) =>
                        setTimeoutDraft(Number(event.target.value))
                      }
                      type="number"
                      value={timeoutDraft}
                    />
                  </Field>
                  <Field label={t("settings.responseStartTimeoutSaved")}>
                    <span className="flex h-8 items-center rounded-md border bg-muted px-2.5 font-mono text-sm tabular-nums">
                      {settings.values.response_start_timeout_seconds}
                    </span>
                  </Field>
                  <p className="col-span-1 flex items-end text-xs text-muted-foreground max-[560px]:items-start">
                    {t("settings.responseStartTimeoutHint")}
                  </p>
                </div>

                <div className="grid grid-cols-3 gap-3 border-b px-4 py-3 max-[560px]:grid-cols-1">
                  <Field label={t("settings.requestBodyLimitField")}>
                    <Input
                      aria-label={t("settings.requestBodyLimitField")}
                      aria-invalid={!bodyLimitValid}
                      className="font-mono tabular-nums"
                      max={MAX_REQUEST_BODY_MIB}
                      min={0}
                      step={1}
                      onChange={(event) =>
                        setBodyLimitDraft(Number(event.target.value))
                      }
                      type="number"
                      value={bodyLimitDraft}
                    />
                  </Field>
                  <Field label={t("settings.concurrencySaved")}>
                    <span className="flex h-8 items-center rounded-md border bg-muted px-2.5 font-mono text-sm tabular-nums">
                      {settings.values.max_request_body_mib === 0
                        ? t("settings.requestBodyUnlimited")
                        : `${settings.values.max_request_body_mib} MiB`}
                    </span>
                  </Field>
                  <p className="col-span-1 flex items-end text-xs text-muted-foreground max-[560px]:items-start">
                    {t("settings.requestBodyLimitHint")}
                  </p>
                </div>

                <div className="flex items-center justify-between gap-3 px-4 py-3 max-[560px]:flex-col max-[560px]:items-stretch">
                  {entryDirty ? (
                    <p className="min-w-0 flex-1 text-xs text-warning-foreground">
                      {t("settings.portDirty")}
                    </p>
                  ) : portNeedsRestart ? (
                    <p className="min-w-0 flex-1 text-xs text-warning-foreground">
                      {t("settings.portNeedsRestart")}
                    </p>
                  ) : (
                    <p className="min-w-0 flex-1 text-xs text-muted-foreground">
                      {t("settings.portControlHint")}
                    </p>
                  )}
                  <Button
                    className="shrink-0 max-[560px]:w-full"
                    disabled={!entryDirty || !bodyLimitValid || busy !== null}
                    onClick={() => void savePort()}
                    type="button"
                  >
                    {busy === "port"
                      ? t("settings.savingPort")
                      : t("settings.savePort")}
                  </Button>
                </div>
              </Panel>
            </div>
          </TabsContent>

          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain"
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
                  <div className="grid gap-2 px-4 py-3">
                    <span className="text-xs font-medium text-text-secondary">
                      {t("settings.trayPagesSection")}
                    </span>
                    <div
                      aria-label={t("settings.trayPagesSection")}
                      className="flex flex-wrap gap-1.5"
                      role="group"
                    >
                      {TRAY_PAGES.map((page) => {
                        const selected = prefs.tray.pages.includes(page);
                        return (
                          <Button
                            key={page}
                            aria-pressed={selected}
                            disabled={prefsBusy}
                            onClick={() => togglePage(page, !selected)}
                            size="sm"
                            type="button"
                            variant={selected ? "secondary" : "outline"}
                          >
                            {t(pageLabelKeys[page])}
                          </Button>
                        );
                      })}
                    </div>
                    <p className="text-xs text-muted-foreground">
                      {t("settings.trayPagesHint")}
                    </p>
                  </div>
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
        </Tabs>
      </div>
    </section>
  );
}
