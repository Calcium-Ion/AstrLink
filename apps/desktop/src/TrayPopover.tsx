import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { listen } from "@tauri-apps/api/event";

import {
  Activity,
  ArrowUpRight,
  Ban,
  Bot,
  Copy,
  Eye,
  Key as KeyRound,
  RefreshCw,
  RotateCcw,
  Route,
  Server,
  Settings,
  ShieldCheck,
  type AnimatedIcon,
} from "@/components/icons";
import { IconButton } from "@/components/IconButton";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { UsageMeter } from "@/components/UsageMeter";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { getTrayState, trayAction, trayPopoverHide, trayPopoverResize } from "./bridge";
import type { CorePhase } from "./core-model";
import { i18n, useT } from "./i18n";
import { TRAY_PAGES, type TrayPage, type TrayPreferences } from "./preferences-model";
import {
  formatResetCountdown,
  usageWindowTone,
  windowLabel,
} from "./subscription-usage-model";
import {
  cacheHitPercent,
  formatCompactTokens,
  formatUsd,
  parseTrayState,
  percentChange,
  type TrayAction,
  type TrayState,
} from "./tray-model";
import { TRAY_POPOVER_WIDTH, TRAY_STATE_EVENT } from "./tray-popover-window";

const COPY_FEEDBACK_MS = 1_500;
const CLOCK_TICK_MS = 30_000;
/** Subscription window rows shown before the list folds behind a toggle. */
export const SUBSCRIPTION_FOLD_LIMIT = 10;

/** Maps a quick page to the `WorkspacePage.kind` the main window routes on. */
const pageKinds: Record<TrayPage, string> = {
  records: "records",
  services: "list",
  tokens: "tokens",
  safety: "safety",
  routing: "routing",
  agent_tools: "agentTools",
};

const pageIcons: Record<TrayPage, AnimatedIcon> = {
  records: Activity,
  services: Server,
  tokens: KeyRound,
  safety: ShieldCheck,
  routing: Route,
  agent_tools: Bot,
};

const pageLabelKeys: Record<TrayPage, string> = {
  records: "nav.records",
  services: "nav.services",
  tokens: "nav.tokens",
  safety: "nav.safety",
  routing: "nav.routing",
  agent_tools: "nav.agentTools",
};

function phaseTone(phase: CorePhase): StatusTone {
  if (phase === "ready") return "positive";
  if (phase === "error" || phase === "exited") return "negative";
  if (phase === "stopped" || phase === "unavailable") return "neutral";
  return "pending";
}

function phaseKey(phase: CorePhase): string {
  switch (phase) {
    case "ready":
      return "tray.status.ready";
    case "stopped":
      return "tray.status.stopped";
    case "stopping":
      return "tray.status.stopping";
    case "error":
    case "exited":
      return "tray.status.failed";
    case "unavailable":
      return "tray.status.unavailable";
    default:
      return "tray.status.starting";
  }
}

function displayAddress(url: string): string {
  return url.replace(/^https?:\/\//, "").replace(/\/$/, "");
}

export function formatAgo(from: Date, now: Date): string {
  const seconds = Math.max(0, Math.floor((now.getTime() - from.getTime()) / 1000));
  if (seconds < 60) return i18n.t("tray.justNow");
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return i18n.t("tray.minutesAgo", { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return i18n.t("tray.hoursAgo", { count: hours });
  return i18n.t("tray.daysAgo", { count: Math.floor(hours / 24) });
}

function formatLatency(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms} ms`;
}

/** 24 bars, one per local hour; the current hour is emphasised. */
function HourlySparkline({ tokens, now }: { tokens: number[]; now: Date }) {
  const t = useT();
  const max = Math.max(...tokens, 0);
  if (tokens.length !== 24 || max <= 0) return null;
  const peakHour = tokens.indexOf(max);
  const currentHour = now.getHours();
  return (
    <div
      aria-label={t("tray.hourlyChart")}
      className="flex h-9 items-end gap-px"
      role="img"
      title={t("tray.hourlyPeak", { hour: `${peakHour}`.padStart(2, "0"), tokens: formatCompactTokens(max) })}
    >
      {tokens.map((value, hour) => {
        const height = value <= 0 ? 2 : Math.max(3, Math.round((value / max) * 36));
        return (
          <span
            key={hour}
            aria-hidden="true"
            className={cn(
              "min-w-0 flex-1 rounded-[1px] transition-[height] duration-300 motion-reduce:transition-none",
              hour === currentHour
                ? "bg-primary"
                : hour > currentHour
                  ? "bg-border"
                  : value > 0
                    ? "bg-primary/35"
                    : "bg-border",
            )}
            style={{ height }}
          />
        );
      })}
    </div>
  );
}

function Stat({ label, value, badge }: { label: string; value: string; badge?: string }) {
  return (
    <div className="min-w-0">
      <div className="truncate text-2xl leading-8 font-semibold tracking-tight tabular-nums">
        {value}
      </div>
      <div className="flex min-w-0 items-center gap-1.5 text-micro text-muted-foreground">
        <span className="truncate">{label}</span>
        {badge ? (
          <Badge className="border-destructive/30 text-destructive" variant="outline">
            {badge}
          </Badge>
        ) : null}
      </div>
    </div>
  );
}

function Chip({ label, value, tone }: { label: string; value: string; tone?: "up" | "down" | "muted" }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1 rounded-sm border bg-muted/60 px-1.5 py-0.5 text-micro">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span
        className={cn(
          "truncate font-medium tabular-nums",
          tone === "up" && "text-warning-foreground",
          tone === "down" && "text-success-foreground",
          tone === "muted" && "text-muted-foreground",
        )}
      >
        {value}
      </span>
    </span>
  );
}

/**
 * The panel itself, pure over its inputs so the settings page can preview it
 * with a draft of the tray preferences and the same live state.
 */
export function TrayPopoverPanel({
  state,
  tray,
  now,
  copyFeedback,
  onAction,
  preview = false,
  className,
}: {
  state: TrayState | null;
  tray: TrayPreferences;
  now: Date;
  copyFeedback?: string | null;
  onAction: (action: TrayAction) => void;
  preview?: boolean;
  className?: string;
}) {
  const t = useT();
  const [subscriptionsExpanded, setSubscriptionsExpanded] = useState(false);
  const view = state?.view ?? null;
  const phase: CorePhase = view?.phase ?? "unavailable";
  const ready = phase === "ready";
  const digest = state?.digest ?? null;
  const usage = tray.usage;
  const wantsUsage = Object.values(usage).some(Boolean);
  const pages = TRAY_PAGES.filter((page) => tray.pages.includes(page));
  const address = view?.inference_url ? displayAddress(view.inference_url) : null;
  const canStart = phase === "stopped" || phase === "exited" || phase === "error";
  const busy = phase === "spawning" || phase === "waiting_for_ready" || phase === "handshaking" || phase === "stopping";

  const chips: Array<{ key: string; label: string; value: string; tone?: "up" | "down" | "muted" }> = [];
  if (digest) {
    if (usage.compare_yesterday && digest.today && digest.yesterday_tokens !== null) {
      const change = percentChange(digest.today.total_tokens, digest.yesterday_tokens);
      chips.push({
        key: "compare",
        label: t("tray.vsYesterday"),
        value:
          change === null
            ? t("tray.noYesterday")
            : change === 0
              ? t("tray.flat")
              : change > 0
                ? t("tray.up", { percent: change })
                : t("tray.down", { percent: Math.abs(change) }),
        tone: change === null || change === 0 ? "muted" : change > 0 ? "up" : "down",
      });
    }
    if (usage.cache_hit && digest.today) {
      const percent = cacheHitPercent(digest.today);
      if (percent !== null) {
        chips.push({ key: "cache", label: t("tray.cacheHit"), value: `${percent}%` });
      }
    }
    if (usage.top_model && digest.top_model) {
      chips.push({
        key: "model",
        label: t("tray.topModel"),
        value: `${digest.top_model.name} · ${digest.top_model.percent}%`,
      });
    }
    if (usage.top_client && digest.top_client) {
      chips.push({
        key: "client",
        label: t("tray.topClient"),
        value: `${digest.top_client.name} · ${digest.top_client.percent}%`,
      });
    }
    if (usage.last_request && digest.last_request) {
      const last = digest.last_request;
      const parts = [formatAgo(new Date(last.started_at), now)];
      if (last.model) parts.push(last.model);
      if (last.latency_ms !== null) parts.push(formatLatency(last.latency_ms));
      if (last.failed) parts.push(t("tray.lastFailed"));
      chips.push({ key: "last", label: t("tray.lastRequest"), value: parts.join(" · ") });
    }
    if (usage.month_total && digest.month_tokens !== null) {
      chips.push({
        key: "month",
        label: t("tray.month"),
        value: `${formatCompactTokens(digest.month_tokens)} tokens`,
      });
    }
  }

  const showToday = usage.today || usage.cost;
  const subscriptionRows =
    usage.subscription_windows && digest
      ? digest.subscriptions.flatMap((subscription) =>
          subscription.windows.map((window, index) => ({
            key: `${subscription.name}-${index}`,
            name: subscription.name,
            window,
          })),
        )
      : [];
  const subscriptionsFoldable = subscriptionRows.length > SUBSCRIPTION_FOLD_LIMIT;
  const visibleSubscriptionRows =
    subscriptionsFoldable && !subscriptionsExpanded
      ? subscriptionRows.slice(0, SUBSCRIPTION_FOLD_LIMIT)
      : subscriptionRows;
  const digestAt = state?.digest_age_ms != null ? new Date(now.getTime() - state.digest_age_ms) : null;

  return (
    <section
      aria-label={t("tray.panelLabel")}
      className={cn(
        "flex w-full flex-col overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-2xl",
        className,
      )}
      data-slot="tray-panel"
      inert={preview}
    >
      {/* Header: state, address, quick actions. */}
      <header className="flex items-start gap-2.5 px-4 pt-3.5 pb-3">
        <StatusDot className="mt-[7px] size-2" tone={phaseTone(phase)} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <strong className="truncate text-sm font-semibold">{t(phaseKey(phase))}</strong>
            {view?.recovery_scheduled ? (
              <span className="truncate text-micro text-warning-foreground">
                {t("tray.status.recovery", { attempt: view.recovery_attempt })}
              </span>
            ) : null}
            {view?.observer_active ? (
              <Badge
                className="shrink-0 gap-1 border-accent-foreground/30 bg-accent text-accent-foreground"
                data-slot="tray-observed"
                variant="outline"
              >
                <Eye aria-hidden="true" className="size-3" />
                {t("tray.observed")}
              </Badge>
            ) : null}
          </div>
          {address ? (
            <div className="mt-0.5 flex min-w-0 items-center gap-1.5">
              <code className="truncate font-mono text-xs text-text-secondary">{address}</code>
              {copyFeedback ? (
                <span className="shrink-0 text-micro text-success-foreground">{copyFeedback}</span>
              ) : null}
            </div>
          ) : null}
          {view?.inference_port_fallback ? (
            <p className="mt-0.5 text-micro text-warning-foreground">
              {t("tray.status.fallback", {
                requested: view.inference_port_fallback.requested_port,
                active: view.inference_port_fallback.active_port,
              })}
            </p>
          ) : null}
          {(phase === "error" || phase === "exited") && view?.last_error ? (
            <p className="mt-1 line-clamp-2 text-micro text-text-secondary [overflow-wrap:anywhere]">
              {view.last_error}
            </p>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          {tray.copy_address ? (
            <IconButton
              disabled={!address}
              label={t("tray.copyAddress")}
              onClick={() => onAction({ kind: "copy_address" })}
            >
              <Copy aria-hidden="true" />
            </IconButton>
          ) : null}
          <IconButton label={t("tray.settings")} onClick={() => onAction({ kind: "navigate", page: "settings" })}>
            <Settings aria-hidden="true" />
          </IconButton>
        </div>
      </header>

      {/* Usage: the reason to open the panel. */}
      {ready && wantsUsage ? (
        <div className="grid gap-3 border-t px-4 py-3">
          {showToday ? (
            <div className="grid gap-2.5">
              <div className="flex items-center justify-between gap-2">
                <SectionKicker>{t("tray.today")}</SectionKicker>
                <div className="flex items-center gap-1 text-micro text-muted-foreground">
                  {digestAt ? <span>{t("tray.updatedAgo", { ago: formatAgo(digestAt, now) })}</span> : null}
                  <IconButton
                    label={t("tray.refresh")}
                    onClick={() => onAction({ kind: "refresh" })}
                    size="icon-xs"
                  >
                    <RefreshCw aria-hidden="true" />
                  </IconButton>
                </div>
              </div>
              {digest === null ? (
                <p className="text-xs text-muted-foreground">{t("tray.loading")}</p>
              ) : digest.today === null && digest.cost_today === null ? (
                <p className="text-xs text-muted-foreground">{t("tray.noCallsToday")}</p>
              ) : (
                <div
                  className={cn(
                    "grid gap-3",
                    usage.today && usage.cost && digest.cost_today ? "grid-cols-3" : "grid-cols-2",
                  )}
                >
                  {usage.today && digest.today ? (
                    <>
                      <Stat
                        badge={digest.today.failed > 0 ? t("tray.failedCount", { count: digest.today.failed }) : undefined}
                        label={t("tray.requests")}
                        value={digest.today.requests.toLocaleString()}
                      />
                      <Stat label={t("tray.tokens")} value={formatCompactTokens(digest.today.total_tokens)} />
                    </>
                  ) : null}
                  {usage.cost && digest.cost_today ? (
                    <Stat label={t("tray.cost")} value={`$${formatUsd(digest.cost_today.amount_usd)}`} />
                  ) : null}
                </div>
              )}
              {usage.today && digest ? <HourlySparkline now={now} tokens={digest.hourly_tokens} /> : null}
            </div>
          ) : null}

          {chips.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {chips.map((chip) => (
                <Chip key={chip.key} label={chip.label} tone={chip.tone} value={chip.value} />
              ))}
            </div>
          ) : null}

          {subscriptionRows.length > 0 ? (
            <div className="grid gap-2.5">
              <div className="flex items-center justify-between gap-2">
                <SectionKicker>{t("tray.subscriptions")}</SectionKicker>
                {subscriptionsFoldable ? (
                  <span className="text-micro text-muted-foreground tabular-nums">
                    {visibleSubscriptionRows.length}/{subscriptionRows.length}
                  </span>
                ) : null}
              </div>
              <div
                className={cn(
                  "grid gap-2.5",
                  // Expanded lists scroll inside the panel so the popover never
                  // outgrows the screen it is anchored to.
                  subscriptionsFoldable && subscriptionsExpanded && "max-h-72 overflow-y-auto overscroll-contain pr-1",
                )}
                data-slot="tray-subscriptions"
              >
                {visibleSubscriptionRows.map(({ key, name, window }) => (
                  <UsageMeter
                    key={key}
                    caption={formatResetCountdown(
                      {
                        used_percent: window.used_percent,
                        reset_at: window.reset_at ?? undefined,
                      },
                      now,
                    )}
                    label={`${name} · ${windowLabel(window.limit_window_seconds ?? undefined, window.secondary)}`}
                    tone={
                      usageWindowTone(window.used_percent) === "critical"
                        ? "destructive"
                        : usageWindowTone(window.used_percent) === "warning"
                          ? "warning"
                          : "success"
                    }
                    value={window.used_percent}
                    valueLabel={`${Math.round(window.used_percent)}%`}
                  />
                ))}
              </div>
              {subscriptionsFoldable ? (
                <Button
                  aria-expanded={subscriptionsExpanded}
                  className="h-6 justify-self-start px-1.5 text-micro text-muted-foreground"
                  onClick={() => setSubscriptionsExpanded((expanded) => !expanded)}
                  size="xs"
                  type="button"
                  variant="ghost"
                >
                  {subscriptionsExpanded
                    ? t("tray.showLessSubscriptions")
                    : t("tray.showMoreSubscriptions", {
                        count: subscriptionRows.length - SUBSCRIPTION_FOLD_LIMIT,
                      })}
                </Button>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : !ready ? (
        <div className="flex items-center justify-between gap-3 border-t bg-muted/40 px-4 py-3">
          <p className="min-w-0 text-xs text-muted-foreground">{t("tray.notReadyHint")}</p>
          {tray.gateway_controls ? (
            <Button
              className="shrink-0"
              disabled={busy}
              onClick={() => onAction({ kind: "core", op: phase === "stopped" ? "start" : canStart ? "start" : "restart" })}
              size="sm"
              type="button"
            >
              {t(phase === "stopped" ? "tray.core.start" : canStart ? "tray.core.start" : "tray.core.restart")}
            </Button>
          ) : null}
        </div>
      ) : null}

      {/* Quick pages. */}
      {pages.length > 0 ? (
        <div
          className={cn(
            "grid gap-1 border-t px-2 py-2",
            pages.length === 1 ? "grid-cols-1" : pages.length === 2 || pages.length === 4 ? "grid-cols-2" : "grid-cols-3",
          )}
        >
          {pages.map((page) => {
            const Icon = pageIcons[page];
            return (
              <Button
                key={page}
                className="h-8 justify-start px-2 text-xs font-normal"
                onClick={() => onAction({ kind: "navigate", page: pageKinds[page] })}
                size="sm"
                type="button"
                variant="ghost"
              >
                <Icon aria-hidden="true" className="size-3.5 text-muted-foreground" strokeWidth={1.6} />
                <span className="truncate">{t(pageLabelKeys[page])}</span>
              </Button>
            );
          })}
        </div>
      ) : null}

      {/* Footer: gateway controls and the always-available actions. */}
      <footer className="flex items-center justify-between gap-2 border-t bg-muted/40 px-2 py-2">
        <div className="flex min-w-0 items-center gap-0.5">
          {tray.gateway_controls && ready ? (
            // Bare icon buttons, the same weight as the header's copy and
            // settings controls; the destructive one only turns red on intent.
            <div
              aria-label={t("tray.gatewayControls")}
              className="inline-flex items-center gap-0.5"
              role="group"
            >
              <IconButton
                label={t("tray.core.restart")}
                onClick={() => onAction({ kind: "core", op: "restart" })}
              >
                <RotateCcw aria-hidden="true" />
              </IconButton>
              <IconButton
                className="hover:bg-danger-wash hover:text-destructive"
                label={t("tray.core.stop")}
                onClick={() => onAction({ kind: "core", op: "stop" })}
              >
                <Ban aria-hidden="true" />
              </IconButton>
            </div>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button
            className="h-7 px-2 text-xs text-muted-foreground"
            onClick={() => onAction({ kind: "quit" })}
            size="sm"
            type="button"
            variant="ghost"
          >
            {t("tray.quit")}
          </Button>
          <Button className="h-7" onClick={() => onAction({ kind: "open" })} size="sm" type="button">
            {t("tray.open")}
            <ArrowUpRight aria-hidden="true" />
          </Button>
        </div>
      </footer>
    </section>
  );
}

/**
 * The popover window surface: pulls the state, follows host pushes, reports
 * its height so the host can size the transparent window, and closes on
 * Escape.
 */
export function TrayPopoverWindow() {
  const [state, setState] = useState<TrayState | null>(null);
  const [now, setNow] = useState(() => new Date());
  const [copyFeedback, setCopyFeedback] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const feedbackTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let cancelled = false;
    void getTrayState()
      .then((next) => {
        if (!cancelled) setState(next);
      })
      .catch((error) => console.error("Unable to read the AstrLink tray state", error));
    const unlisten = listen<unknown>(TRAY_STATE_EVENT, ({ payload }) => {
      try {
        setState(parseTrayState(payload));
        setNow(new Date());
      } catch (error) {
        console.error("Ignoring an invalid AstrLink tray state", error);
      }
    }).catch((error) => {
      console.error("Unable to observe the AstrLink tray state", error);
      return () => {};
    });
    return () => {
      cancelled = true;
      void unlisten.then((stop) => stop());
    };
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), CLOCK_TICK_MS);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const root = rootRef.current;
    if (!root || typeof ResizeObserver === "undefined") return;
    let frame = 0;
    const report = () => {
      frame = 0;
      const height = Math.ceil(root.getBoundingClientRect().height);
      if (height > 0) void trayPopoverResize(height).catch(() => {});
    };
    const observer = new ResizeObserver(() => {
      if (frame === 0) frame = window.requestAnimationFrame(report);
    });
    observer.observe(root);
    report();
    return () => {
      observer.disconnect();
      if (frame !== 0) window.cancelAnimationFrame(frame);
    };
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") void trayPopoverHide().catch(() => {});
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  useEffect(
    () => () => {
      if (feedbackTimer.current !== null) clearTimeout(feedbackTimer.current);
    },
    [],
  );

  const handleAction = useCallback((action: TrayAction) => {
    void trayAction(action)
      .then(() => {
        if (action.kind === "copy_address") {
          setCopyFeedback(i18n.t("tray.copied"));
          if (feedbackTimer.current !== null) clearTimeout(feedbackTimer.current);
          feedbackTimer.current = setTimeout(() => setCopyFeedback(null), COPY_FEEDBACK_MS);
        }
      })
      .catch((error) => {
        if (action.kind === "copy_address") setCopyFeedback(i18n.t("tray.copyFailed"));
        console.error("AstrLink tray action failed", error);
      });
  }, []);

  const tray = useMemo(() => state?.tray, [state]);
  // The window hugs the tray icon; the shadow gets its room on the far side.
  const below = state?.popover_below ?? true;

  return (
    <div
      ref={rootRef}
      className={cn("px-3", below ? "pt-1 pb-5" : "pt-5 pb-1")}
      style={{ width: TRAY_POPOVER_WIDTH }}
    >
      {tray ? (
        <TrayPopoverPanel
          copyFeedback={copyFeedback}
          now={now}
          onAction={handleAction}
          state={state}
          tray={tray}
        />
      ) : null}
    </div>
  );
}
