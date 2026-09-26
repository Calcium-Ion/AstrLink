import { useCallback, useEffect, useId, useRef, useState } from "react";

import { FormMessage } from "@/components/FormMessage";
import { LoadingState } from "@/components/LoadingState";
import { SectionKicker } from "@/components/SectionKicker";
import { StatusBadge } from "@/components/StatusBadge";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

import { listServiceRiskEvents } from "./bridge";
import { i18n, useT } from "./i18n";
import {
  subscriptionRiskLabel,
  type SubscriptionRisk,
  type SubscriptionRiskEvent,
  type SubscriptionRiskEventKind,
} from "./service-model";
import { formatResetCountdown } from "./subscription-usage-model";

/** Events shown in the popover; Core keeps at most 50 per service. */
const RISK_EVENT_LIMIT = 20;

type RiskEventsState =
  | { status: "loading" }
  | { status: "ready"; items: SubscriptionRiskEvent[] }
  | { status: "error" };

const riskEventTones: Record<SubscriptionRiskEventKind, StatusTone> = {
  suspended: "negative",
  cooling: "pending",
  cleared: "positive",
};

const riskEventKindKeys: Record<SubscriptionRiskEventKind, string> = {
  suspended: "services.riskEventSuspended",
  cooling: "services.riskEventCooling",
  cleared: "services.riskEventCleared",
};

export function riskTone(risk: Pick<SubscriptionRisk, "state">): StatusTone {
  return risk.state === "suspended" ? "negative" : "pending";
}

/** Short badge text: a suspended account waits for the user, cooling counts down. */
export function riskBadgeLabel(risk: SubscriptionRisk, now: Date): string {
  if (risk.state === "suspended") return i18n.t("services.riskStateSuspended");
  const countdown = risk.paused_until
    ? formatResetCountdown(
        { used_percent: 0, reset_at: risk.paused_until },
        now,
      )
    : null;
  return countdown
    ? i18n.t("services.riskCoolingBadge", { countdown })
    : i18n.t("services.riskStateCooling");
}

function formatRiskTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(i18n.language === "zh-CN" ? "zh-CN" : "en", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

/**
 * Row badge for a subscription paused by an upstream risk signal. The badge
 * opens a popover with the risk details; the event history is only fetched
 * while the popover is open.
 */
export function ServiceRiskBadge({
  disabled = false,
  now,
  onRestore,
  risk,
  serviceId,
  serviceName,
}: {
  disabled?: boolean;
  now: Date;
  onRestore: () => void;
  risk: SubscriptionRisk;
  serviceId: string;
  serviceName: string;
}) {
  const t = useT();
  const eventsHeadingId = useId();
  const [open, setOpen] = useState(false);
  const [events, setEvents] = useState<RiskEventsState>({ status: "loading" });
  const generation = useRef(0);
  const label = riskBadgeLabel(risk, now);
  const tone = riskTone(risk);

  const loadEvents = useCallback(() => {
    const current = generation.current + 1;
    generation.current = current;
    setEvents({ status: "loading" });
    listServiceRiskEvents(serviceId, RISK_EVENT_LIMIT).then(
      (items) => {
        if (generation.current === current) {
          setEvents({ status: "ready", items });
        }
      },
      (cause: unknown) => {
        console.error(
          "AstrLink failed to load subscription risk events",
          serviceId,
          cause,
        );
        if (generation.current === current) setEvents({ status: "error" });
      },
    );
  }, [serviceId]);

  useEffect(() => {
    if (!open) return;
    loadEvents();
    return () => {
      // Drop replies that land after the popover closed or the service changed.
      generation.current += 1;
    };
  }, [loadEvents, open]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          aria-label={t("services.riskDetailsNamed", {
            state: label,
            name: serviceName,
          })}
          className="h-auto rounded-sm p-0 hover:bg-transparent"
          data-risk-state={risk.state}
          data-testid="service-risk-badge"
          type="button"
          variant="ghost"
        >
          <StatusBadge tone={tone}>{label}</StatusBadge>
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="grid w-80 gap-3 text-xs"
        data-testid="service-risk-popover"
      >
        <div className="grid gap-1">
          <div className="flex min-w-0 items-center gap-2">
            <StatusDot tone={tone} />
            <strong className="min-w-0 text-sm font-semibold">
              {subscriptionRiskLabel(risk.code, risk.state)}
            </strong>
          </div>
          <p className="text-text-secondary">
            {risk.state === "suspended"
              ? t("services.riskSuspendedHint")
              : t("services.riskCoolingHint")}
          </p>
        </div>
        {risk.message ? (
          <div className="grid gap-1">
            <SectionKicker>{t("services.riskUpstreamMessage")}</SectionKicker>
            <p
              className="rounded-sm border bg-muted px-2 py-1.5 font-mono break-words whitespace-pre-wrap"
              data-testid="service-risk-message"
            >
              {risk.message}
            </p>
          </div>
        ) : null}
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
          {risk.http_status !== undefined ? (
            <>
              <dt className="text-muted-foreground">
                {t("services.riskHttpStatus")}
              </dt>
              <dd className="tabular-nums">{risk.http_status}</dd>
            </>
          ) : null}
          <dt className="text-muted-foreground">
            {t("services.riskObservedAt")}
          </dt>
          <dd className="tabular-nums">
            <time dateTime={risk.observed_at}>
              {formatRiskTime(risk.observed_at)}
            </time>
          </dd>
          {risk.occurrences !== undefined ? (
            <>
              <dt className="text-muted-foreground">
                {t("services.riskOccurrences")}
              </dt>
              <dd className="tabular-nums">{risk.occurrences}</dd>
            </>
          ) : null}
          {risk.state === "cooling" && risk.paused_until ? (
            <>
              <dt className="text-muted-foreground">
                {t("services.riskPausedUntil")}
              </dt>
              <dd className="tabular-nums">
                <time dateTime={risk.paused_until}>
                  {formatRiskTime(risk.paused_until)}
                </time>
              </dd>
            </>
          ) : null}
        </dl>
        <section aria-labelledby={eventsHeadingId} className="grid gap-1.5">
          <SectionKicker id={eventsHeadingId}>
            {t("services.riskEvents")}
          </SectionKicker>
          {events.status === "loading" ? (
            <LoadingState
              className="justify-start text-xs"
              label={t("common.loading")}
            />
          ) : events.status === "error" ? (
            <FormMessage
              className="flex items-center justify-between gap-2 py-1"
              tone="error"
            >
              {t("services.riskEventsFailed")}
              <Button
                onClick={loadEvents}
                size="xs"
                type="button"
                variant="ghost"
              >
                {t("common.retry")}
              </Button>
            </FormMessage>
          ) : events.items.length === 0 ? (
            <p className="text-muted-foreground">
              {t("services.riskEventsEmpty")}
            </p>
          ) : (
            <ol
              className="grid max-h-40 gap-1.5 overflow-y-auto overscroll-contain"
              data-testid="service-risk-events"
            >
              {events.items.map((event) => (
                <li
                  className="grid min-w-0 gap-0.5 border-b pb-1.5 last:border-b-0 last:pb-0"
                  key={event.id}
                >
                  <div className="flex min-w-0 items-center gap-1.5">
                    <StatusDot tone={riskEventTones[event.kind]} />
                    <span className="shrink-0 font-medium">
                      {t(riskEventKindKeys[event.kind])}
                    </span>
                    {event.kind !== "cleared" && event.code ? (
                      <span className="min-w-0 truncate text-muted-foreground">
                        {subscriptionRiskLabel(event.code, event.kind)}
                        {event.http_status !== undefined
                          ? ` · HTTP ${event.http_status}`
                          : ""}
                      </span>
                    ) : null}
                    <time
                      className="ml-auto shrink-0 text-muted-foreground tabular-nums"
                      dateTime={event.observed_at}
                    >
                      {formatRiskTime(event.observed_at)}
                    </time>
                  </div>
                  {event.message ? (
                    <p className="break-words text-muted-foreground">
                      {event.message}
                    </p>
                  ) : null}
                </li>
              ))}
            </ol>
          )}
        </section>
        <Button
          className="justify-self-end"
          disabled={disabled}
          onClick={() => {
            setOpen(false);
            onRestore();
          }}
          size="sm"
          type="button"
          variant={risk.state === "suspended" ? "default" : "outline"}
        >
          {t("services.riskRestore")}
        </Button>
      </PopoverContent>
    </Popover>
  );
}
