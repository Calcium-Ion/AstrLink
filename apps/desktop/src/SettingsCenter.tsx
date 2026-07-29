import { useEffect, useMemo, useState } from "react";

import {
  getPreferences,
  restartCore,
  startCore,
  stopCore,
  updatePreferences,
} from "./bridge";
import type { AppSnapshot } from "./core-model";
import type { Preferences, SettingsSnapshot } from "./preferences-model";
import { PageHeader } from "./PageHeader";

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
  const [draft, setDraft] = useState<Preferences | null>(null);
  const [loadingError, setLoadingError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "start" | "stop" | "restart" | null>(
    null,
  );

  useEffect(() => {
    let cancelled = false;
    void getPreferences()
      .then((next) => {
        if (!cancelled) {
          setSettings(next);
          setDraft(next.values);
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
  const dirty = useMemo(
    () => draft !== null && settings !== null && JSON.stringify(draft) !== JSON.stringify(settings.values),
    [draft, settings],
  );
  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);

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

  const save = async (): Promise<void> => {
    if (!draft) return;
    setBusy("save");
    setActionError(null);
    setFeedback(null);
    try {
      const next = await updatePreferences(draft);
      setSettings(next);
      setDraft(next.values);
      setFeedback(
        active !== null && active !== next.values.inference_port
          ? "设置已保存。新推理端口将在重启 Core 后生效。"
          : "设置已保存并与系统状态核对。",
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
  if (!draft || !settings) {
    return (
      <section className="settings-center">
        <PageHeader eyebrow="桌面偏好" title="设置" />
        <p className="settings-loading">正在读取桌面与系统设置…</p>
      </section>
    );
  }

  const phase = snapshot?.phase ?? "unavailable";
  const canStart = ["stopped", "exited", "error"].includes(phase);
  const canStop = !["stopped", "exited", "error", "unavailable"].includes(phase);

  return (
    <section className="settings-center">
      <PageHeader
        eyebrow="桌面偏好"
        title="设置"
        description="管理窗口行为、系统启动与本地 Core 生命周期。"
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
      ) : settings.autostart_actual !== draft.autostart ? (
        <div className="settings-notice settings-notice--warning" role="status">
          保存值与系统开机启动状态不一致；保存后将尝试重新核对。
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

      <div className="settings-grid">
        <article className="settings-card">
          <h3>桌面行为</h3>
          <label>
            <span>关闭主窗口时</span>
            <select
              value={draft.close_behavior}
              onChange={(event) =>
                setDraft({ ...draft, close_behavior: event.target.value as Preferences["close_behavior"] })
              }
            >
              <option value="hide_to_tray">隐藏到系统托盘</option>
              <option value="quit">退出 AstrLink</option>
            </select>
          </label>
          <label className="settings-check">
            <input
              checked={draft.autostart}
              onChange={(event) => setDraft({ ...draft, autostart: event.target.checked })}
              type="checkbox"
            />
            <span>登录系统后自动启动 AstrLink</span>
          </label>
          <small>系统托盘始终提供“显示 AstrLink”和“退出”。</small>
        </article>

        <article className="settings-card">
          <h3>Core 启动与恢复</h3>
          <label className="settings-check">
            <input
              checked={draft.core_auto_start}
              onChange={(event) => setDraft({ ...draft, core_auto_start: event.target.checked })}
              type="checkbox"
            />
            <span>AstrLink 启动时自动启动 Core</span>
          </label>
          <label className="settings-check">
            <input
              checked={draft.core_auto_recover}
              onChange={(event) => setDraft({ ...draft, core_auto_recover: event.target.checked })}
              type="checkbox"
            />
            <span>Core 异常退出后自动恢复</span>
          </label>
          <p>
            当前状态：<strong>{phase}</strong>
            {snapshot?.recovery_scheduled_in_ms !== null &&
            snapshot?.recovery_scheduled_in_ms !== undefined
              ? ` · 第 ${snapshot.recovery_attempt}/5 次恢复将在约 ${Math.ceil(snapshot.recovery_scheduled_in_ms / 1000)} 秒后进行`
              : snapshot?.recovery_attempt
                ? ` · 已尝试恢复 ${snapshot.recovery_attempt}/5 次`
                : ""}
          </p>
          {snapshot?.last_error ? <code className="settings-error-detail">{snapshot.last_error}</code> : null}
          <div className="settings-actions">
            <button disabled={!canStart || busy !== null} onClick={() => void runCoreAction("start")} type="button">
              启动
            </button>
            <button className="btn-secondary" disabled={!canStop || busy !== null} onClick={() => void runCoreAction("stop")} type="button">
              停止
            </button>
            <button className="btn-secondary" disabled={phase === "unavailable" || busy !== null} onClick={() => void runCoreAction("restart")} type="button">
              重启
            </button>
          </div>
        </article>

        <article className="settings-card settings-card--wide">
          <h3>本地推理端口</h3>
          <label>
            <span>已保存端口</span>
            <input
              max={65535}
              min={1024}
              onChange={(event) =>
                setDraft({ ...draft, inference_port: Number(event.target.value) })
              }
              type="number"
              value={draft.inference_port}
            />
          </label>
          <p>
            正在使用：<strong>{active ?? "Core 未就绪"}</strong>
            {" · "}
            已保存：<strong>{draft.inference_port}</strong>
          </p>
          {active !== null && active !== draft.inference_port ? (
            <small className="settings-port-pending">端口修改尚未生效；保存后重启 Core。</small>
          ) : (
            <small>控制面始终使用仅桌面可知的 127.0.0.1 临时端口。</small>
          )}
        </article>
      </div>

      <div className="settings-savebar">
        <span>{dirty ? "有未保存的修改" : "所有设置均已保存"}</span>
        <button disabled={!dirty || busy !== null} onClick={() => void save()} type="button">
          {busy === "save" ? "正在保存…" : "保存设置"}
        </button>
      </div>
    </section>
  );
}
