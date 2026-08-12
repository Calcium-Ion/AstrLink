import { useEffect, useId, useMemo, useState, type ReactNode } from "react";

import { FormMessage } from "@/components/FormMessage";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
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

function SettingsIcon({ name }: { name: "window" | "core" | "port" }) {
  const paths: Record<typeof name, ReactNode> = {
    window: (
      <>
        <rect height="14" rx="2" width="18" x="3" y="5" />
        <path d="M3 9h18" />
        <circle cx="7" cy="7" r="0.8" />
        <circle cx="10" cy="7" r="0.8" />
      </>
    ),
    core: (
      <>
        <circle cx="12" cy="12" r="3" />
        <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M5.6 18.4 7 17M17 7l1.4-1.4" />
      </>
    ),
    port: (
      <>
        <path d="M8 7V5a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
        <rect height="12" rx="2" width="14" x="5" y="7" />
        <path d="M12 11v4" />
      </>
    ),
  };

  return (
    <span
      aria-hidden="true"
      className={cn(
        "grid size-10 place-items-center rounded-xl bg-accent text-accent-foreground shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--primary)_12%,transparent)]",
        name === "port" &&
          "bg-success-wash text-success-foreground shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--success)_14%,transparent)]",
      )}
    >
      <svg className="size-[18px]" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.7" viewBox="0 0 24 24">
        {paths[name]}
      </svg>
    </span>
  );
}

function SettingsToggle({
  checked,
  disabled,
  label,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  label: string;
  onChange: (checked: boolean) => void;
}) {
  const id = useId();

  return (
    <div
      className={cn(
        "grid min-h-11 grid-cols-[40px_minmax(0,1fr)] items-center gap-3 rounded-xl border bg-muted px-3 py-2.5 transition-colors",
        checked && "border-primary/30 bg-accent",
        !disabled && "hover:border-primary/30 hover:bg-card",
        disabled && "cursor-not-allowed opacity-60",
      )}
    >
      <Switch
        checked={checked}
        className="h-6 w-10 [&_[data-slot=switch-thumb]]:size-[18px] [&_[data-slot=switch-thumb]]:data-[state=checked]:translate-x-4"
        disabled={disabled}
        id={id}
        onCheckedChange={onChange}
      />
      <Label className="text-[12.5px] leading-[1.4] font-semibold" htmlFor={id}>
        {label}
      </Label>
    </div>
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
        <Card className="gap-2.5 border-destructive/35 bg-danger-wash p-5 text-danger-foreground shadow-[var(--shadow-card)]">
          <strong>无法加载设置</strong>
          <p className="text-xs leading-6">{loadingError}</p>
          <Button variant="outline" onClick={() => window.location.reload()} type="button">
            重新加载
          </Button>
        </Card>
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
        <FormMessage className="px-3.5 py-[11px] text-xs" tone="warning">
          {settings.load_warning}
        </FormMessage>
      ) : null}
      {settings.autostart_error ? (
        <FormMessage className="px-3.5 py-[11px] text-xs" tone="warning">
          {settings.autostart_error}
        </FormMessage>
      ) : settings.autostart_actual !== prefs.autostart ? (
        <FormMessage className="px-3.5 py-[11px] text-xs" tone="warning">
          偏好与系统开机启动状态不一致；可再次切换以重新核对。
        </FormMessage>
      ) : null}
      {actionError ? (
        <FormMessage className="px-3.5 py-[11px] text-xs" tone="error">
          {actionError}
        </FormMessage>
      ) : null}
      {feedback ? (
        <FormMessage className="px-3.5 py-[11px] text-xs" tone="success">
          {feedback}
        </FormMessage>
      ) : null}

      <div className="grid grid-cols-2 gap-3.5 max-[920px]:grid-cols-1">
        <Card className="grid min-w-0 content-start gap-[18px] rounded-[18px] p-5 pb-[18px] shadow-[var(--shadow-card)]">
          <header className="grid grid-cols-[40px_minmax(0,1fr)] items-start gap-3">
            <SettingsIcon name="window" />
            <div>
              <SectionKicker>窗口与启动</SectionKicker>
              <h3 className="mt-[3px] text-base font-[750] tracking-[-0.02em]">桌面行为</h3>
              <p className="mt-1 text-[11px] leading-6 text-text-secondary">更改后立即生效，无需手动保存。</p>
            </div>
          </header>

          <div className="grid gap-2">
            <span className="text-[11px] font-semibold text-text-secondary">关闭主窗口时</span>
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
              <Label
                className={cn(
                  "relative grid min-h-16 cursor-pointer items-stretch gap-[3px] rounded-xl border border-input bg-card px-[13px] py-3 transition-[border-color,background,box-shadow] hover:border-primary/35",
                  prefs.close_behavior === "hide_to_tray" &&
                    "border-primary/50 bg-accent shadow-[0_0_0_3px_color-mix(in_srgb,var(--primary)_10%,transparent)]",
                )}
              >
                <RadioGroupItem className="absolute top-3 right-3" value="hide_to_tray" />
                <strong className="pr-6 text-[12.5px] font-bold">隐藏到托盘</strong>
                <small className="pr-6 text-[10.5px] leading-[1.4] text-muted-foreground">后台继续运行</small>
              </Label>
              <Label
                className={cn(
                  "relative grid min-h-16 cursor-pointer items-stretch gap-[3px] rounded-xl border border-input bg-card px-[13px] py-3 transition-[border-color,background,box-shadow] hover:border-primary/35",
                  prefs.close_behavior === "quit" &&
                    "border-primary/50 bg-accent shadow-[0_0_0_3px_color-mix(in_srgb,var(--primary)_10%,transparent)]",
                )}
              >
                <RadioGroupItem className="absolute top-3 right-3" value="quit" />
                <strong className="pr-6 text-[12.5px] font-bold">退出 AstrLink</strong>
                <small className="pr-6 text-[10.5px] leading-[1.4] text-muted-foreground">结束全部进程</small>
              </Label>
            </RadioGroup>
          </div>

          <div className="grid gap-2">
            <SettingsToggle
              checked={prefs.autostart}
              disabled={prefsBusy}
              label="登录系统后自动启动 AstrLink"
              onChange={(autostart) => void applyInstant({ autostart })}
            />
          </div>

          <p className="text-[11px] leading-6 text-muted-foreground">系统托盘始终提供“显示 AstrLink”和“退出”。</p>
        </Card>

        <Card className="grid min-w-0 content-start gap-[18px] rounded-[18px] p-5 pb-[18px] shadow-[var(--shadow-card)]">
          <header className="grid grid-cols-[40px_minmax(0,1fr)] items-start gap-3">
            <SettingsIcon name="core" />
            <div>
              <SectionKicker>本地运行时</SectionKicker>
              <h3 className="mt-[3px] text-base font-[750] tracking-[-0.02em]">Core 启动与恢复</h3>
              <p className="mt-1 text-[11px] leading-6 text-text-secondary">更改后立即生效，无需手动保存。</p>
            </div>
          </header>

          <div className="grid gap-2">
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
              onChange={(core_auto_recover) => void applyInstant({ core_auto_recover })}
            />
          </div>

          <div
            className={cn(
              "flex min-h-14 items-center gap-[11px] rounded-xl border bg-muted px-3.5 py-3",
              tone === "positive" && "border-success/25 bg-success-wash",
              tone === "pending" && "border-warning/30 bg-warning-wash",
              tone === "negative" && "border-destructive/25 bg-danger-wash",
            )}
          >
            <StatusDot tone={tone} />
            <div className="grid min-w-0 gap-0.5">
              <strong className="text-[12.5px] font-bold">{phaseLabel(phase)}</strong>
              <span className="overflow-hidden text-[11px] leading-[1.45] text-text-secondary text-ellipsis whitespace-nowrap">
                当前状态：{phase}
                {recoveryHint ? ` · ${recoveryHint}` : ""}
              </span>
            </div>
          </div>

          {snapshot?.last_error ? (
            <code className="[overflow-wrap:anywhere] rounded-[10px] bg-muted px-3 py-2.5 text-[11px] leading-[1.45] whitespace-normal text-text-secondary">{snapshot.last_error}</code>
          ) : null}

          <div className="flex flex-wrap items-center gap-2 [&_button]:min-w-[72px]">
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
        </Card>

        <Card
          className={cn(
            "col-span-full grid min-w-0 content-start gap-[18px] rounded-[18px] p-5 pb-[18px] shadow-[var(--shadow-card)] max-[920px]:col-auto",
            portDirty &&
              "border-warning/40 shadow-[var(--shadow-card),0_0_0_3px_var(--warning-wash)]",
          )}
        >
          <header className="grid grid-cols-[40px_minmax(0,1fr)] items-start gap-3">
            <SettingsIcon name="port" />
            <div>
              <SectionKicker>网络入口</SectionKicker>
              <h3 className="mt-[3px] text-base font-[750] tracking-[-0.02em]">本地推理端口</h3>
              <p className="mt-1 text-[11px] leading-6 text-text-secondary">修改后需保存；重启 Core 后才会切换到新端口。</p>
            </div>
          </header>

          <div className="grid grid-cols-[minmax(140px,200px)_minmax(0,1fr)] items-end gap-3.5 max-[920px]:grid-cols-1">
            <Label className="grid items-stretch gap-2">
              <span className="text-[11px] font-semibold text-text-secondary">推理端口</span>
              <Input
                className="min-h-11 rounded-[11px] px-3 text-[15px] font-bold tracking-[0.02em] tabular-nums"
                max={65535}
                min={1024}
                onChange={(event) => setPortDraft(Number(event.target.value))}
                type="number"
                value={portDraft}
              />
            </Label>

            <dl className="grid grid-cols-2 gap-2.5 max-[560px]:grid-cols-1">
              <div className="grid min-h-16 gap-1 rounded-xl border bg-muted px-3.5 py-3">
                <dt className="text-[10.5px] font-semibold text-muted-foreground">正在使用</dt>
                <dd className="text-lg font-[750] tracking-[-0.02em] tabular-nums">{active ?? "Core 未就绪"}</dd>
              </div>
              <div className="grid min-h-16 gap-1 rounded-xl border bg-muted px-3.5 py-3">
                <dt className="text-[10.5px] font-semibold text-muted-foreground">已保存</dt>
                <dd className="text-lg font-[750] tracking-[-0.02em] tabular-nums">{settings.values.inference_port}</dd>
              </div>
            </dl>
          </div>

          <div className="flex items-center justify-between gap-3.5 pt-0.5 max-[560px]:flex-col max-[560px]:items-stretch">
            {portDirty ? (
              <p className="min-w-0 flex-1 text-[11px] leading-6 text-warning-foreground">有未保存的端口修改。</p>
            ) : portNeedsRestart ? (
              <p className="min-w-0 flex-1 text-[11px] leading-6 text-warning-foreground">
                端口修改尚未生效；重启 Core 后切换到已保存端口。
              </p>
            ) : (
              <p className="min-w-0 flex-1 text-[11px] leading-6 text-muted-foreground">
                控制面始终使用仅桌面可知的 127.0.0.1 临时端口。
              </p>
            )}
            <Button
              className="min-w-24 shrink-0 max-[560px]:w-full"
              disabled={!portDirty || busy !== null}
              onClick={() => void savePort()}
              type="button"
            >
              {busy === "port" ? "正在保存…" : "保存端口"}
            </Button>
          </div>
        </Card>
      </div>
    </section>
  );
}
