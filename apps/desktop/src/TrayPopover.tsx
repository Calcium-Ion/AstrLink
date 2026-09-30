import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { listen } from "@tauri-apps/api/event";

import {
  ArrowUpRight,
  Ban,
  Copy,
  Eye,
  RefreshCw,
  RotateCcw,
  Settings,
  X,
} from "@/components/icons";
import { IconButton } from "@/components/IconButton";
import { Metric, MetricGroup } from "@/components/Metric";
import { SectionKicker } from "@/components/SectionKicker";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { SubscriptionQuotaMeter } from "@/components/SubscriptionQuotaMeter";
import { UsageMeterGrid } from "@/components/UsageMeter";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { ActiveRawGrants } from "./ActiveRawGrants";
import {
  getTrayState,
  listRawAccess,
  trayAction,
  trayPopoverHide,
  trayPopoverResize,
} from "./bridge";
import type { CorePhase } from "./core-model";
import { i18n, useT } from "./i18n";
import type { TrayPreferences } from "./preferences-model";
import { useQuotaDisplayMode } from "./quota-display";
import type { RawAccessGrant } from "./raw-access-model";
import { formatResetCountdown, windowLabel } from "./subscription-usage-model";
import {
  cacheHitPercent,
  formatCompactTokens,
  formatUsd,
  parseTrayState,
  percentChange,
  type TrayAction,
  type TrayRawKeyEventKind,
  type TrayState,
  type TraySubscription,
} from "./tray-model";
import { TRAY_POPOVER_WIDTH, TRAY_STATE_EVENT } from "./tray-popover-window";

const COPY_FEEDBACK_MS = 1_500;
const CLOCK_TICK_MS = 30_000;
/** Subscription window rows shown before the list folds behind a toggle. */
export const SUBSCRIPTION_FOLD_LIMIT = 10;

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
  const seconds = Math.max(
    0,
    Math.floor((now.getTime() - from.getTime()) / 1000),
  );
  if (seconds < 60) return i18n.t("tray.justNow");
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return i18n.t("tray.minutesAgo", { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return i18n.t("tray.hoursAgo", { count: hours });
  return i18n.t("tray.daysAgo", { count: Math.floor(hours / 24) });
}

function rawKeyEventKey(kind: TrayRawKeyEventKind): string {
  switch (kind) {
    case "raw_password_set":
      return "tray.rawKeyEvent.passwordSet";
    case "raw_password_changed":
      return "tray.rawKeyEvent.passwordChanged";
    case "raw_key_reset":
      return "tray.rawKeyEvent.keyReset";
  }
}

function formatLatency(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms} ms`;
}

interface SubscriptionGroup {
  key: string;
  name: string;
  kind: TraySubscription["kind"];
  rows: Array<{
    key: string;
    label: string;
    usedPercent: number;
    reset: string | null;
    resetDetail: string | null;
  }>;
}

/** Plans with their windows, cut off after `limit` windows in total. */
function subscriptionGroups(
  subscriptions: TraySubscription[],
  limit: number,
  now: Date,
): SubscriptionGroup[] {
  let budget = limit;
  const groups: SubscriptionGroup[] = [];
  subscriptions.forEach((subscription, index) => {
    if (budget <= 0) return;
    const windows = subscription.windows.slice(0, budget);
    budget -= windows.length;
    groups.push({
      key: `${index}`,
      name: subscription.name,
      kind: subscription.kind,
      rows: windows.map((window, windowIndex) => {
        const countdown = {
          used_percent: window.used_percent,
          reset_at: window.reset_at ?? undefined,
        };
        return {
          key: `${windowIndex}`,
          label:
            window.label ??
            windowLabel(
              window.limit_window_seconds ?? undefined,
              window.secondary,
            ),
          usedPercent: window.used_percent,
          reset: formatResetCountdown(countdown, now, { short: true }),
          resetDetail: formatResetCountdown(countdown, now),
        };
      }),
    });
  });
  return groups;
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
      title={t("tray.hourlyPeak", {
        hour: `${peakHour}`.padStart(2, "0"),
        tokens: formatCompactTokens(max),
      })}
    >
      {tokens.map((value, hour) => {
        const height =
          value <= 0 ? 2 : Math.max(3, Math.round((value / max) * 36));
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

function Chip({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: "up" | "down" | "muted";
}) {
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
  onClose,
  onRawGrantRevoked,
  preview = false,
  rawGrants = [],
  className,
}: {
  state: TrayState | null;
  tray: TrayPreferences;
  now: Date;
  copyFeedback?: string | null;
  onAction: (action: TrayAction) => void;
  onClose?: () => void;
  /** A timed grant was revoked here; reread `rawGrants`. */
  onRawGrantRevoked?: () => void;
  preview?: boolean;
  /** Timed agent grants to raw content still running. */
  rawGrants?: RawAccessGrant[];
  className?: string;
}) {
  const t = useT();
  const quotaMode = useQuotaDisplayMode();
  const [subscriptionsExpanded, setSubscriptionsExpanded] = useState(false);
  const view = state?.view ?? null;
  const phase: CorePhase = view?.phase ?? "unavailable";
  const ready = phase === "ready";
  const digest = state?.digest ?? null;
  const usage = tray.usage;
  const wantsUsage = Object.values(usage).some(Boolean);
  const address = view?.inference_url
    ? displayAddress(view.inference_url)
    : null;
  const canStart =
    phase === "stopped" || phase === "exited" || phase === "error";
  const busy =
    phase === "spawning" ||
    phase === "waiting_for_ready" ||
    phase === "handshaking" ||
    phase === "stopping";

  const chips: Array<{
    key: string;
    label: string;
    value: string;
    tone?: "up" | "down" | "muted";
  }> = [];
  if (digest) {
    if (
      usage.compare_yesterday &&
      digest.today &&
      digest.yesterday_tokens !== null
    ) {
      const change = percentChange(
        digest.today.total_tokens,
        digest.yesterday_tokens,
      );
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
        tone:
          change === null || change === 0
            ? "muted"
            : change > 0
              ? "up"
              : "down",
      });
    }
    if (usage.cache_hit && digest.today) {
      const percent = cacheHitPercent(digest.today);
      if (percent !== null) {
        chips.push({
          key: "cache",
          label: t("tray.cacheHit"),
          value: `${percent}%`,
        });
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
      chips.push({
        key: "last",
        label: t("tray.lastRequest"),
        value: parts.join(" · "),
      });
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
  const subscriptions =
    usage.subscription_windows && digest ? digest.subscriptions : [];
  const subscriptionWindowCount = subscriptions.reduce(
    (count, subscription) => count + subscription.windows.length,
    0,
  );
  const subscriptionsFoldable =
    subscriptionWindowCount > SUBSCRIPTION_FOLD_LIMIT;
  const visibleSubscriptions = subscriptionGroups(
    subscriptions,
    subscriptionsFoldable && !subscriptionsExpanded
      ? SUBSCRIPTION_FOLD_LIMIT
      : subscriptionWindowCount,
    now,
  );
  const showResetColumn = visibleSubscriptions.some((group) =>
    group.rows.some((row) => row.reset),
  );
  const digestAt =
    state?.digest_age_ms != null
      ? new Date(now.getTime() - state.digest_age_ms)
      : null;

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
      <header className="flex items-start gap-2.5 px-4 py-3.5">
        <StatusDot className="mt-[7px] size-2" tone={phaseTone(phase)} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <strong className="truncate text-sm font-semibold">
              {t(phaseKey(phase))}
            </strong>
            {view?.recovery_scheduled ? (
              <span className="truncate text-micro text-warning-foreground">
                {t("tray.status.recovery", { attempt: view.recovery_attempt })}
              </span>
            ) : null}
            {view?.observer_active ? (
              <Badge
                className="shrink-0 gap-1"
                data-slot="tray-observed"
                variant="accent"
              >
                <Eye aria-hidden="true" className="size-3" />
                {t(
                  view.observer_read_level === "raw"
                    ? "tray.observedRaw"
                    : "tray.observed",
                )}
              </Badge>
            ) : null}
          </div>
          {address ? (
            <div className="mt-0.5 flex min-w-0 items-center gap-1.5">
              <code className="truncate font-mono text-xs text-text-secondary">
                {address}
              </code>
              {copyFeedback ? (
                <span className="shrink-0 text-micro text-success-foreground">
                  {copyFeedback}
                </span>
              ) : null}
            </div>
          ) : null}
          {view?.pending_raw_access ? (
            // Requests are decided only in the approval window.
            <Button
              className="mt-0.5 h-auto p-0 text-micro text-warning-foreground"
              data-slot="tray-raw-pending"
              onClick={() => onAction({ kind: "raw_access" })}
              type="button"
              variant="link"
            >
              {t("tray.rawAccessPending", { count: view.pending_raw_access })}
            </Button>
          ) : null}
          {view?.raw_password_required ? (
            // Only the main window sets up the raw password; take the user
            // there instead of leaving the tray silent about it.
            <Button
              className="mt-0.5 h-auto p-0 text-micro text-warning-foreground"
              data-slot="tray-raw-password-required"
              onClick={() => onAction({ kind: "open" })}
              type="button"
              variant="link"
            >
              {t("tray.rawPasswordRequired")}
            </Button>
          ) : null}
          {view?.raw_key_replaced ? (
            // The warning and its resolution live in the main window, too.
            <Button
              className="mt-0.5 h-auto p-0 text-micro text-warning-foreground"
              data-slot="tray-raw-key-replaced"
              onClick={() => onAction({ kind: "open" })}
              type="button"
              variant="link"
            >
              {t("tray.rawKeyReplaced")}
            </Button>
          ) : null}
          {view?.raw_key_event ? (
            <p
              className="mt-0.5 text-micro text-text-secondary"
              data-slot="tray-raw-key-event"
            >
              {t(rawKeyEventKey(view.raw_key_event.kind), {
                ago: formatAgo(new Date(view.raw_key_event.at), now),
              })}
            </p>
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
        <div className="flex shrink-0 items-center gap-0.5 text-muted-foreground">
          {tray.copy_address ? (
            <IconButton
              disabled={!address}
              label={t("tray.copyAddress")}
              onClick={() => onAction({ kind: "copy_address" })}
            >
              <Copy aria-hidden="true" />
            </IconButton>
          ) : null}
          <IconButton
            label={t("tray.settings")}
            onClick={() => onAction({ kind: "navigate", page: "settings" })}
          >
            <Settings aria-hidden="true" />
          </IconButton>
          <IconButton label={t("common.close")} onClick={onClose}>
            <X aria-hidden="true" />
          </IconButton>
        </div>
      </header>

      <ActiveRawGrants
        className="border-t px-4 py-3"
        grants={rawGrants}
        onRevoked={() => onRawGrantRevoked?.()}
        title={t("tray.rawGrantsTitle")}
      />

      {/* Usage: the reason to open the panel. */}
      {ready && wantsUsage ? (
        <div
          className="grid max-h-96 content-start gap-4 overflow-y-auto overscroll-contain border-t px-4 pt-4 pb-5"
          data-slot="tray-usage"
        >
          {showToday ? (
            <div className="grid gap-3">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <SectionKicker>{t("tray.today")}</SectionKicker>
                  {usage.today && digest?.today && digest.today.failed > 0 ? (
                    <Badge
                      className="border-destructive/30 text-destructive"
                      variant="outline"
                    >
                      {t("tray.failedCount", { count: digest.today.failed })}
                    </Badge>
                  ) : null}
                </div>
                <div className="flex items-center gap-1 text-micro text-muted-foreground">
                  {digestAt ? (
                    <span>
                      {t("tray.updatedAgo", { ago: formatAgo(digestAt, now) })}
                    </span>
                  ) : null}
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
                <p className="text-xs text-muted-foreground">
                  {t("tray.loading")}
                </p>
              ) : digest.today === null && digest.cost_today === null ? (
                <p className="text-xs text-muted-foreground">
                  {t("tray.noCallsToday")}
                </p>
              ) : (
                <MetricGroup className="auto-cols-fr grid-flow-col grid-cols-none rounded-md [overflow-wrap:anywhere] @min-[640px]/workspace-surface:grid-cols-none">
                  {usage.today && digest.today ? (
                    <>
                      <Metric
                        label={t("tray.requests")}
                        size="sm"
                        title={digest.today.requests.toLocaleString()}
                        value={digest.today.requests.toLocaleString()}
                      />
                      <Metric
                        label={t("tray.tokens")}
                        size="sm"
                        title={digest.today.total_tokens.toLocaleString()}
                        value={formatCompactTokens(digest.today.total_tokens)}
                      />
                    </>
                  ) : null}
                  {usage.cost && digest.cost_today ? (
                    <Metric
                      label={t("tray.cost")}
                      size="sm"
                      title={`$${formatUsd(digest.cost_today.amount_usd)}`}
                      value={`$${formatUsd(digest.cost_today.amount_usd)}`}
                    />
                  ) : null}
                </MetricGroup>
              )}
              {usage.today && digest ? (
                <HourlySparkline now={now} tokens={digest.hourly_tokens} />
              ) : null}
            </div>
          ) : null}

          {chips.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {chips.map((chip) => (
                <Chip
                  key={chip.key}
                  label={chip.label}
                  tone={chip.tone}
                  value={chip.value}
                />
              ))}
            </div>
          ) : null}

          {subscriptions.length > 0 ? (
            <div
              className={cn(
                "grid gap-1.5",
                (showToday || chips.length > 0) && "border-t pt-4",
              )}
            >
              {/* One grid, so the bars, percents and resets line up across plans. */}
              <UsageMeterGrid
                captions={showResetColumn}
                className="gap-y-0.5"
                data-slot="tray-subscriptions"
              >
                <div className="col-span-full mb-1.5 grid grid-cols-subgrid items-center">
                  <SectionKicker className="col-span-2">
                    {t("tray.subscriptions")}
                  </SectionKicker>
                  <SectionKicker className="text-right">
                    {t(
                      quotaMode === "remaining"
                        ? "tray.quotaRemainingColumn"
                        : "tray.quotaUsedColumn",
                    )}
                  </SectionKicker>
                  {showResetColumn ? (
                    <SectionKicker className="text-right">
                      {t("tray.quotaResetColumn")}
                    </SectionKicker>
                  ) : null}
                </div>
                {visibleSubscriptions.map((group, index) => (
                  <Fragment key={group.key}>
                    <div
                      className={cn(
                        "col-span-full flex min-w-0 items-center gap-1.5 text-xs font-medium",
                        index > 0 && "mt-2",
                      )}
                    >
                      <ServiceKindIcon kind={group.kind} size={14} />
                      <span className="min-w-0 truncate" title={group.name}>
                        {group.name}
                      </span>
                    </div>
                    {group.rows.map((row) => (
                      <SubscriptionQuotaMeter
                        key={row.key}
                        accessibleLabel={`${group.name} · ${row.label}`}
                        // A dash keeps the column filled where no reset is known.
                        caption={showResetColumn ? (row.reset ?? "—") : null}
                        captionDetail={row.resetDetail}
                        className="pl-5"
                        label={row.label}
                        layout="row"
                        usedPercent={row.usedPercent}
                      />
                    ))}
                  </Fragment>
                ))}
              </UsageMeterGrid>
              {subscriptionsFoldable ? (
                <Button
                  aria-expanded={subscriptionsExpanded}
                  className="h-6 justify-self-start px-1.5 text-micro text-muted-foreground"
                  onClick={() =>
                    setSubscriptionsExpanded((expanded) => !expanded)
                  }
                  size="xs"
                  type="button"
                  variant="ghost"
                >
                  {subscriptionsExpanded
                    ? t("tray.showLessSubscriptions")
                    : t("tray.showMoreSubscriptions", {
                        count:
                          subscriptionWindowCount - SUBSCRIPTION_FOLD_LIMIT,
                      })}
                </Button>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : !ready ? (
        <div className="flex items-center justify-between gap-3 border-t bg-muted/40 px-4 py-3">
          <p className="min-w-0 text-xs text-muted-foreground">
            {t("tray.notReadyHint")}
          </p>
          {tray.gateway_controls ? (
            <Button
              className="shrink-0"
              disabled={busy}
              onClick={() =>
                onAction({
                  kind: "core",
                  op:
                    phase === "stopped"
                      ? "start"
                      : canStart
                        ? "start"
                        : "restart",
                })
              }
              size="sm"
              type="button"
            >
              {t(
                phase === "stopped"
                  ? "tray.core.start"
                  : canStart
                    ? "tray.core.start"
                    : "tray.core.restart",
              )}
            </Button>
          ) : null}
        </div>
      ) : null}

      {/* Footer: gateway controls and the always-available actions. */}
      <footer className="flex items-center justify-between gap-2 border-t bg-muted/40 px-4 py-3">
        <div className="flex min-w-0 items-center gap-0.5">
          {tray.gateway_controls && ready ? (
            // Bare icon buttons, the same weight as the header's copy and
            // settings controls; the destructive one only turns red on intent.
            <div
              aria-label={t("tray.gatewayControls")}
              className="inline-flex items-center gap-1 text-muted-foreground"
              role="group"
            >
              <IconButton
                label={t("tray.core.restart")}
                onClick={() => onAction({ kind: "core", op: "restart" })}
                size="icon"
              >
                <RotateCcw aria-hidden="true" />
              </IconButton>
              <IconButton
                className="hover:bg-danger-wash hover:text-destructive"
                label={t("tray.core.stop")}
                onClick={() => onAction({ kind: "core", op: "stop" })}
                size="icon"
              >
                <Ban aria-hidden="true" />
              </IconButton>
            </div>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button
            className="px-2 text-muted-foreground"
            onClick={() => onAction({ kind: "quit" })}
            type="button"
            variant="ghost"
          >
            {t("tray.quit")}
          </Button>
          <Button onClick={() => onAction({ kind: "open" })} type="button">
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
  const [rawGrants, setRawGrants] = useState<RawAccessGrant[]>([]);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const feedbackTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const close = useCallback(() => {
    void trayPopoverHide().catch((error) =>
      console.error("Unable to hide the AstrLink tray popover", error),
    );
  }, []);

  useEffect(() => {
    let cancelled = false;
    void getTrayState()
      .then((next) => {
        if (!cancelled) setState(next);
      })
      .catch((error) =>
        console.error("Unable to read the AstrLink tray state", error),
      );
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

  // The host counts the running grants; who holds them is read on demand.
  const activeRawGrants = state?.view?.active_raw_grants ?? 0;
  const [rawGrantsGeneration, setRawGrantsGeneration] = useState(0);
  useEffect(() => {
    if (activeRawGrants === 0) {
      setRawGrants([]);
      return;
    }
    let cancelled = false;
    void listRawAccess().then(
      (list) => {
        if (!cancelled) setRawGrants(list.active);
      },
      (error) =>
        console.error("Unable to read AstrLink raw access grants", error),
    );
    return () => {
      cancelled = true;
    };
  }, [activeRawGrants, rawGrantsGeneration]);

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
      if (event.key === "Escape") close();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [close]);

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
          if (feedbackTimer.current !== null)
            clearTimeout(feedbackTimer.current);
          feedbackTimer.current = setTimeout(
            () => setCopyFeedback(null),
            COPY_FEEDBACK_MS,
          );
        }
      })
      .catch((error) => {
        if (action.kind === "copy_address")
          setCopyFeedback(i18n.t("tray.copyFailed"));
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
      onPointerDown={(event) => {
        if (event.target === event.currentTarget) close();
      }}
      style={{ width: TRAY_POPOVER_WIDTH }}
    >
      {tray ? (
        <TrayPopoverPanel
          copyFeedback={copyFeedback}
          now={now}
          onAction={handleAction}
          onClose={close}
          onRawGrantRevoked={() =>
            setRawGrantsGeneration((generation) => generation + 1)
          }
          rawGrants={rawGrants}
          state={state}
          tray={tray}
        />
      ) : null}
    </div>
  );
}
