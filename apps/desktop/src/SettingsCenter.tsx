import { useEffect, useMemo, useState, type ReactNode } from "react";

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
    <span aria-hidden="true" className="settings-card__icon">
      <svg fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.7" viewBox="0 0 24 24">
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
  return (
    <label className={`settings-toggle${disabled ? " is-disabled" : ""}`}>
      <input
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        type="checkbox"
      />
      <span className="settings-toggle__track" aria-hidden="true">
        <span className="settings-toggle__thumb" />
      </span>
      <span className="settings-toggle__label">{label}</span>
    </label>
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
      <section className="settings-center">
        <PageHeader eyebrow="桌面偏好" title="设置" />
        <div className="settings-card settings-card--error">
          <strong>无法加载设置</strong>
          <p>{loadingError}</p>
          <button className="btn-secondary" onClick={() => window.location.reload()} type="button">
            重新加载
          </button>
        </div>
      </section>
    );
  }
  if (!settings || portDraft === null) {
    return (
      <section className="settings-center">
        <PageHeader eyebrow="桌面偏好" title="设置" />
        <p className="settings-loading">正在读取桌面与系统设置…</p>
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
    <section className="settings-center">
      <PageHeader
        eyebrow="桌面偏好"
        title="设置"
        description="窗口与 Core 偏好会立即生效；推理端口需单独保存。"
      />

      {settings.load_warning ? (
        <div className="settings-notice settings-notice--warning" role="status">
          {settings.load_warning}
        </div>
      ) : null}
      {settings.autostart_error ? (
        <div className="settings-notice settings-notice--warning" role="status">
          {settings.autostart_error}
        </div>
      ) : settings.autostart_actual !== prefs.autostart ? (
        <div className="settings-notice settings-notice--warning" role="status">
          偏好与系统开机启动状态不一致；可再次切换以重新核对。
        </div>
      ) : null}
      {actionError ? (
        <div className="settings-notice settings-notice--error" role="alert">
          {actionError}
        </div>
      ) : null}
      {feedback ? (
        <div className="settings-notice settings-notice--success" role="status">
          {feedback}
        </div>
      ) : null}

      <div className="settings-stack">
        <article className="settings-card">
          <header className="settings-card__header">
            <SettingsIcon name="window" />
            <div>
              <span className="section-kicker">窗口与启动</span>
              <h3>桌面行为</h3>
              <p>更改后立即生效，无需手动保存。</p>
            </div>
          </header>

          <div className="settings-field">
            <span className="settings-field__label">关闭主窗口时</span>
            <div
              className={`settings-segmented${prefsBusy ? " is-disabled" : ""}`}
              role="radiogroup"
              aria-label="关闭主窗口时"
            >
              <label
                className={
                  prefs.close_behavior === "hide_to_tray"
                    ? "settings-segmented__option is-active"
                    : "settings-segmented__option"
                }
              >
                <input
                  checked={prefs.close_behavior === "hide_to_tray"}
                  disabled={prefsBusy}
                  name="close_behavior"
                  onChange={() => void applyInstant({ close_behavior: "hide_to_tray" })}
                  type="radio"
                  value="hide_to_tray"
                />
                <strong>隐藏到托盘</strong>
                <small>后台继续运行</small>
              </label>
              <label
                className={
                  prefs.close_behavior === "quit"
                    ? "settings-segmented__option is-active"
                    : "settings-segmented__option"
                }
              >
                <input
                  checked={prefs.close_behavior === "quit"}
                  disabled={prefsBusy}
                  name="close_behavior"
                  onChange={() => void applyInstant({ close_behavior: "quit" })}
                  type="radio"
                  value="quit"
                />
                <strong>退出 AstrLink</strong>
                <small>结束全部进程</small>
              </label>
            </div>
          </div>

          <div className="settings-toggle-list">
            <SettingsToggle
              checked={prefs.autostart}
              disabled={prefsBusy}
              label="登录系统后自动启动 AstrLink"
              onChange={(autostart) => void applyInstant({ autostart })}
            />
          </div>

          <p className="settings-hint">系统托盘始终提供“显示 AstrLink”和“退出”。</p>
        </article>

        <article className="settings-card">
          <header className="settings-card__header">
            <SettingsIcon name="core" />
            <div>
              <span className="section-kicker">本地运行时</span>
              <h3>Core 启动与恢复</h3>
              <p>更改后立即生效，无需手动保存。</p>
            </div>
          </header>

          <div className="settings-toggle-list">
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

          <div className={`settings-core-status settings-core-status--${tone}`}>
            <span className={tone === "neutral" ? "dot" : `dot dot--${tone}`} />
            <div>
              <strong>{phaseLabel(phase)}</strong>
              <span>
                当前状态：{phase}
                {recoveryHint ? ` · ${recoveryHint}` : ""}
              </span>
            </div>
          </div>

          {snapshot?.last_error ? (
            <code className="settings-error-detail">{snapshot.last_error}</code>
          ) : null}

          <div className="settings-actions">
            <button
              className="btn-primary"
              disabled={!canStart || busy !== null}
              onClick={() => void runCoreAction("start")}
              type="button"
            >
              {busy === "start" ? "启动中…" : "启动"}
            </button>
            <button
              className="btn-secondary"
              disabled={!canStop || busy !== null}
              onClick={() => void runCoreAction("stop")}
              type="button"
            >
              {busy === "stop" ? "停止中…" : "停止"}
            </button>
            <button
              className="btn-secondary"
              disabled={phase === "unavailable" || busy !== null}
              onClick={() => void runCoreAction("restart")}
              type="button"
            >
              {busy === "restart" ? "重启中…" : "重启"}
            </button>
          </div>
        </article>

        <article className={`settings-card settings-card--port${portDirty ? " is-dirty" : ""}`}>
          <header className="settings-card__header">
            <SettingsIcon name="port" />
            <div>
              <span className="section-kicker">网络入口</span>
              <h3>本地推理端口</h3>
              <p>修改后需保存；重启 Core 后才会切换到新端口。</p>
            </div>
          </header>

          <div className="settings-port-row">
            <label className="settings-port-field">
              <span className="settings-field__label">推理端口</span>
              <input
                max={65535}
                min={1024}
                onChange={(event) => setPortDraft(Number(event.target.value))}
                type="number"
                value={portDraft}
              />
            </label>

            <dl className="settings-port-metrics">
              <div>
                <dt>正在使用</dt>
                <dd>{active ?? "Core 未就绪"}</dd>
              </div>
              <div>
                <dt>已保存</dt>
                <dd>{settings.values.inference_port}</dd>
              </div>
            </dl>
          </div>

          <div className="settings-port-footer">
            {portDirty ? (
              <p className="settings-hint settings-port-pending">有未保存的端口修改。</p>
            ) : portNeedsRestart ? (
              <p className="settings-hint settings-port-pending">
                端口修改尚未生效；重启 Core 后切换到已保存端口。
              </p>
            ) : (
              <p className="settings-hint">
                控制面始终使用仅桌面可知的 127.0.0.1 临时端口。
              </p>
            )}
            <button
              className="btn-primary"
              disabled={!portDirty || busy !== null}
              onClick={() => void savePort()}
              type="button"
            >
              {busy === "port" ? "正在保存…" : "保存端口"}
            </button>
          </div>
        </article>
      </div>
    </section>
  );
}
