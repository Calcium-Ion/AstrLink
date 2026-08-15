import { useEffect, useId, useMemo, useState } from "react";

import { ChoiceCard } from "@/components/ChoiceCard";
import { DataRow } from "@/components/DataRow";
import { FormMessage } from "@/components/FormMessage";
import { Panel, PanelHeader } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

import {
  getPreferences,
  restartCore,
  startCore,
  stopCore,
  updatePreferences,
} from "./bridge";
import { phaseLabel, phaseTone, type AppSnapshot } from "./core-model";
import type { Preferences, SettingsSnapshot } from "./preferences-model";
import { PageHeader } from "./PageHeader";

type InstantPatch = Omit<Preferences, "inference_port">;

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : "设置操作失败。";
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
        className="min-w-0 flex-1 cursor-pointer text-sm font-normal"
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
  hint: string;
  kicker: string;
  title: string;
}) {
  return (
    <PanelHeader>
      <SectionKicker>{kicker}</SectionKicker>
      <strong className="mt-1 block text-sm font-semibold tracking-tight">
        {title}
      </strong>
      <p className="mt-1 text-xs text-text-secondary">{hint}</p>
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
  const [settings, setSettings] = useState<SettingsSnapshot | null>(null);
  const [portDraft, setPortDraft] = useState<number | null>(null);
  const [loadingError, setLoadingError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<string | null>(null);
  const [busy, setBusy] = useState<
    "prefs" | "port" | "start" | "stop" | "restart" | null
  >(null);

  useEffect(() => {
    let cancelled = false;
    void getPreferences()
      .then((next) => {
        if (!cancelled) {
          setSettings(next);
          setPortDraft(next.values.inference_port);
          setLoadingError(null);
        }
      })
      .catch((error) => {
        if (!cancelled) setLoadingError(messageOf(error));
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
  useEffect(() => {
    onDirtyChange(portDirty);
    return () => onDirtyChange(false);
  }, [portDirty, onDirtyChange]);

  const applyInstant = async (patch: Partial<InstantPatch>): Promise<void> => {
    if (!settings || busy !== null) return;
    const previous = settings;
    const values: Preferences = {
      ...settings.values,
      ...patch,
      inference_port: settings.values.inference_port,
    };
    setBusy("prefs");
    setActionError(null);
    setFeedback(null);
    setSettings({ ...settings, values });
    try {
      const next = await updatePreferences(values);
      setSettings(next);
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
    setFeedback(null);
    try {
      const next =
        action === "start"
          ? await startCore()
          : action === "stop"
            ? await stopCore()
            : await restartCore();
      onCoreSnapshot(next);
      setFeedback(
        action === "start"
          ? "Core 正在启动。"
          : action === "stop"
            ? "Core 已停止。"
            : "Core 正在使用已保存设置重启。",
      );
    } catch (error) {
      setActionError(messageOf(error));
    } finally {
      setBusy(null);
    }
  };

  const savePort = async (): Promise<void> => {
    if (!settings || portDraft === null || !portDirty) return;
    setBusy("port");
    setActionError(null);
    setFeedback(null);
    try {
      const next = await updatePreferences({
        ...settings.values,
        inference_port: portDraft,
      });
      setSettings(next);
      setPortDraft(next.values.inference_port);
      setFeedback(
        active !== null && active !== next.values.inference_port
          ? "端口已保存。重启 Core 后生效。"
          : "端口已保存并与系统状态核对。",
      );
    } catch (error) {
      setActionError(messageOf(error));
    } finally {
      setBusy(null);
    }
  };

  if (loadingError) {
    return (
      <section className="grid gap-4 pb-2">
        <PageHeader eyebrow="桌面偏好" title="设置" />
        <Panel className="grid gap-2.5 border-destructive/35 bg-danger-wash p-4 text-danger-foreground">
          <strong className="text-sm font-semibold">无法加载设置</strong>
          <p className="text-xs">{loadingError}</p>
          <Button
            className="justify-self-start"
            variant="outline"
            onClick={() => window.location.reload()}
            type="button"
          >
            重新加载
          </Button>
        </Panel>
      </section>
    );
  }
  if (!settings || portDraft === null) {
    return (
      <section className="grid gap-4 pb-2">
        <PageHeader eyebrow="桌面偏好" title="设置" />
        <p className="text-xs text-text-secondary">正在读取桌面与系统设置…</p>
      </section>
    );
  }

  const prefs = settings.values;
  const prefsBusy = busy === "prefs";
  const phase = snapshot?.phase ?? "unavailable";
  const tone = phaseTone(phase);
  const canStart = ["stopped", "exited", "error"].includes(phase);
  const canStop = !["stopped", "exited", "error", "unavailable"].includes(phase);
  const recoveryHint =
    snapshot?.recovery_scheduled_in_ms !== null &&
    snapshot?.recovery_scheduled_in_ms !== undefined
      ? `第 ${snapshot.recovery_attempt}/5 次恢复将在约 ${Math.ceil(snapshot.recovery_scheduled_in_ms / 1000)} 秒后进行`
      : snapshot?.recovery_attempt
        ? `已尝试恢复 ${snapshot.recovery_attempt}/5 次`
        : null;
  const portNeedsRestart =
    active !== null && active !== settings.values.inference_port;

  return (
    <section className="grid gap-4 pb-2">
      <PageHeader
        eyebrow="桌面偏好"
        title="设置"
        description="窗口与 Core 偏好会立即生效；推理端口需单独保存。"
      />

      {settings.load_warning ? (
        <FormMessage tone="warning">{settings.load_warning}</FormMessage>
      ) : null}
      {settings.autostart_error ? (
        <FormMessage tone="warning">{settings.autostart_error}</FormMessage>
      ) : settings.autostart_actual !== prefs.autostart ? (
        <FormMessage tone="warning">
          偏好与系统开机启动状态不一致；可再次切换以重新核对。
        </FormMessage>
      ) : null}
      {actionError ? (
        <FormMessage tone="error">{actionError}</FormMessage>
      ) : null}
      {feedback ? <FormMessage tone="success">{feedback}</FormMessage> : null}

      <div className="grid grid-cols-2 gap-3 max-[920px]:grid-cols-1">
        <Panel className="min-w-0">
          <SettingsPanelHeader
            hint="更改后立即生效，无需手动保存。"
            kicker="窗口与启动"
            title="桌面行为"
          />

          <div className="grid gap-2 border-b px-4 py-3">
            <span className="text-xs font-medium text-text-secondary">
              关闭主窗口时
            </span>
            <RadioGroup
              className={cn(
                "grid grid-cols-2 gap-2 max-[560px]:grid-cols-1",
                prefsBusy && "pointer-events-none opacity-60",
              )}
              aria-label="关闭主窗口时"
              disabled={prefsBusy}
              onValueChange={(value) =>
                void applyInstant({
                  close_behavior: value as Preferences["close_behavior"],
                })
              }
              value={prefs.close_behavior}
            >
              <ChoiceCard
                description="后台继续运行"
                label="隐藏到托盘"
                selected={prefs.close_behavior === "hide_to_tray"}
                value="hide_to_tray"
              />
              <ChoiceCard
                description="结束全部进程"
                label="退出 AstrLink"
                selected={prefs.close_behavior === "quit"}
                value="quit"
              />
            </RadioGroup>
          </div>

          <SettingsToggle
            checked={prefs.autostart}
            disabled={prefsBusy}
            label="登录系统后自动启动 AstrLink"
            onChange={(autostart) => void applyInstant({ autostart })}
          />

          <p className="px-4 py-2.5 text-xs text-muted-foreground">
            系统托盘始终提供“显示 AstrLink”和“退出”。
          </p>
        </Panel>

        <Panel className="min-w-0">
          <SettingsPanelHeader
            hint="更改后立即生效，无需手动保存。"
            kicker="本地运行时"
            title="Core 启动与恢复"
          />

          <SettingsToggle
            checked={prefs.core_auto_start}
            disabled={prefsBusy}
            label="AstrLink 启动时自动启动 Core"
            onChange={(core_auto_start) => void applyInstant({ core_auto_start })}
          />
          <SettingsToggle
            checked={prefs.core_auto_recover}
            disabled={prefsBusy}
            label="Core 异常退出后自动恢复"
            onChange={(core_auto_recover) =>
              void applyInstant({ core_auto_recover })
            }
          />

          <div className="border-b px-4 py-3">
            <div
              className={cn(
                "flex items-center gap-2.5 rounded-md border bg-muted px-3 py-2.5",
                tone === "positive" && "border-success/25 bg-success-wash",
                tone === "pending" && "border-warning/30 bg-warning-wash",
                tone === "negative" && "border-destructive/25 bg-danger-wash",
              )}
            >
              <StatusDot tone={tone} />
              <div className="grid min-w-0 gap-0.5">
                <strong className="text-sm font-medium">
                  {phaseLabel(phase)}
                </strong>
                <span className="truncate text-xs text-text-secondary">
                  当前状态：{phase}
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
              {busy === "start" ? "启动中…" : "启动"}
            </Button>
            <Button
              variant="outline"
              disabled={!canStop || busy !== null}
              onClick={() => void runCoreAction("stop")}
              type="button"
            >
              {busy === "stop" ? "停止中…" : "停止"}
            </Button>
            <Button
              variant="outline"
              disabled={phase === "unavailable" || busy !== null}
              onClick={() => void runCoreAction("restart")}
              type="button"
            >
              {busy === "restart" ? "重启中…" : "重启"}
            </Button>
          </div>
        </Panel>

        <Panel
          className={cn(
            "col-span-full min-w-0 max-[920px]:col-auto",
            portDirty && "border-warning/50",
          )}
        >
          <SettingsPanelHeader
            hint="修改后需保存；重启 Core 后才会切换到新端口。"
            kicker="网络入口"
            title="本地推理端口"
          />

          <div className="grid grid-cols-[minmax(140px,200px)_minmax(0,1fr)] items-end gap-3 border-b px-4 py-3 max-[920px]:grid-cols-1">
            <Label className="grid gap-1.5">
              <span className="text-xs font-medium text-text-secondary">
                推理端口
              </span>
              <Input
                className="font-mono tabular-nums"
                max={65535}
                min={1024}
                onChange={(event) => setPortDraft(Number(event.target.value))}
                type="number"
                value={portDraft}
              />
            </Label>

            <dl className="grid grid-cols-2 gap-2 max-[560px]:grid-cols-1">
              <div className="grid gap-1 rounded-md border bg-muted px-3 py-2">
                <dt className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
                  正在使用
                </dt>
                <dd className="text-sm font-medium tabular-nums">
                  {active ?? "Core 未就绪"}
                </dd>
              </div>
              <div className="grid gap-1 rounded-md border bg-muted px-3 py-2">
                <dt className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
                  已保存
                </dt>
                <dd className="text-sm font-medium tabular-nums">
                  {settings.values.inference_port}
                </dd>
              </div>
            </dl>
          </div>

          <div className="flex items-center justify-between gap-3 px-4 py-3 max-[560px]:flex-col max-[560px]:items-stretch">
            {portDirty ? (
              <p className="min-w-0 flex-1 text-xs text-warning-foreground">
                有未保存的端口修改。
              </p>
            ) : portNeedsRestart ? (
              <p className="min-w-0 flex-1 text-xs text-warning-foreground">
                端口修改尚未生效；重启 Core 后切换到已保存端口。
              </p>
            ) : (
              <p className="min-w-0 flex-1 text-xs text-muted-foreground">
                控制面始终使用仅桌面可知的 127.0.0.1 临时端口。
              </p>
            )}
            <Button
              className="shrink-0 max-[560px]:w-full"
              disabled={!portDirty || busy !== null}
              onClick={() => void savePort()}
              type="button"
            >
              {busy === "port" ? "正在保存…" : "保存端口"}
            </Button>
          </div>
        </Panel>
      </div>
    </section>
  );
}
