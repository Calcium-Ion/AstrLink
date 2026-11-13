import { useEffect, useMemo } from "react";

import { ClientTypeCounts, clientTypeName } from "@/components/ClientTypeIcon";
import { HelpPopover } from "@/components/HelpPopover";
import { LiveTrendChart } from "@/components/LiveTrendChart";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel } from "@/components/Panel";
import { cn } from "@/lib/utils";

import { getRequestActivity } from "./bridge";
import { currentLocale, useT } from "./i18n";
import {
  ACTIVITY_STEP_MS,
  ACTIVITY_WINDOW_MS,
  activitySince,
  hasRecentActivity,
  summarizeActivity,
} from "./request-activity-model";
import type { RequestActivity } from "./request-record-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

const POLL_INTERVAL_MS = 1000;

const activityKey = (coreSessionKey: string | null) =>
  `request-activity:${coreSessionKey}`;

/**
 * The calls of the last few minutes as the board last read them, for the
 * request list's lanes. Only the board polls; readers share its snapshot.
 */
export function useRequestActivityFeed(
  coreSessionKey: string | null,
): RequestActivity | null {
  return useWorkspaceSnapshot<RequestActivity | null>(
    activityKey(coreSessionKey),
    null,
  )[0];
}

/**
 * Reads the calls of the last few minutes once a second while `enabled`. The
 * gateway keeps them, so the chart is whole however long the page was away;
 * the last read stays in the workspace snapshot so coming back paints it at
 * once.
 */
export function useRequestActivityPoll(
  coreSessionKey: string | null,
  enabled: boolean,
): RequestActivity | null {
  const [activity, setActivity] = useWorkspaceSnapshot<RequestActivity | null>(
    activityKey(coreSessionKey),
    null,
  );
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    let inFlight = false;
    const poll = async () => {
      if (inFlight || document.visibilityState === "hidden") return;
      inFlight = true;
      try {
        const next = await getRequestActivity(activitySince(Date.now()));
        if (!cancelled) setActivity(next);
      } catch {
        // The list below reports sync trouble; the board keeps its last read.
      } finally {
        inFlight = false;
      }
    };
    void poll();
    const timer = window.setInterval(() => void poll(), POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [enabled, setActivity]);
  return activity;
}

function useLiveActivity(coreSessionKey: string | null, isReady: boolean) {
  const activity = useRequestActivityPoll(coreSessionKey, isReady);
  return useMemo(
    () => (activity ? summarizeActivity(activity) : null),
    [activity],
  );
}

function Unit({ children }: { children: string }) {
  return (
    <span className="ml-1 text-xs font-normal tracking-normal text-muted-foreground">
      {children}
    </span>
  );
}

/** A metric's label with the button that explains how it is counted. */
function HelpLabel({
  children,
  help,
  hint,
}: {
  children: string;
  help: string;
  hint: string;
}) {
  return (
    <span className="inline-flex items-center gap-0.5">
      {children}
      {/* The help button is taller than the label line; keep the row even. */}
      <span className="-my-1 inline-flex">
        <HelpPopover label={help}>{hint}</HelpPopover>
      </span>
    </span>
  );
}

/**
 * The live band above the request list: how many tokens the models put out
 * and how many conversations went on at once over the last five minutes. It
 * covers every model call, whatever the list below is filtered to, and stays
 * away while nothing happened in that window; the poll keeps running so it
 * comes back with the next call.
 */
export function RequestActivityBoard({
  coreSessionKey,
  isReady,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
}) {
  const t = useT();
  const live = useLiveActivity(coreSessionKey, isReady);
  if (!live || !hasRecentActivity(live)) return null;
  const snapshot = isReady ? live.snapshot : null;
  const points = live.points;
  const throughput = snapshot?.outputTokensPerSecond ?? null;
  const peak = Math.max(0, ...points.map((sample) => sample.value));
  const timeFormat = new Intl.DateTimeFormat(currentLocale(), {
    hour: "numeric",
    minute: "numeric",
    second: "numeric",
    hour12: false,
  });
  const clientList = new Intl.ListFormat(currentLocale(), { style: "narrow" });
  const throughputFormat = new Intl.NumberFormat(currentLocale(), {
    maximumFractionDigits: throughput !== null && throughput < 10 ? 1 : 0,
  });

  return (
    <div className="py-3 [@media(max-height:680px)]:py-2">
      <Panel
        aria-label={t("records.activityTitle")}
        className="grid shrink-0 grid-cols-[minmax(0,auto)_minmax(10rem,1fr)]"
        data-testid="request-activity"
        role="region"
      >
        <MetricGroup className="grid-cols-[minmax(6.5rem,auto)] border-r @min-[640px]/workspace-surface:grid-cols-[minmax(6.5rem,auto)]">
          <Metric
            label={
              <HelpLabel
                help={t("records.activityThroughputHelp")}
                hint={t("records.activityThroughputHint")}
              >
                {t("records.activityThroughput")}
              </HelpLabel>
            }
            size="sm"
            value={
              // The average slides every second while tokens are in its
              // window; fading each change would flicker.
              <span data-testid="activity-throughput">
                {throughput === null
                  ? "—"
                  : throughputFormat.format(throughput)}
                {throughput === null ? null : <Unit>tok/s</Unit>}
              </span>
            }
          />
        </MetricGroup>
        <LiveTrendChart
          aside={t("records.activityWindow")}
          breakdown={(sample) =>
            sample.clients.length > 0 ? (
              <ClientTypeCounts
                clients={sample.clients}
                data-testid="activity-clients"
                label={clientList.format(
                  sample.clients.map(({ clientType, count }) =>
                    t("records.activityClient", {
                      client: clientTypeName(clientType),
                      count,
                    }),
                  ),
                )}
              />
            ) : null
          }
          className={cn("px-3 pt-2.5 pb-1.5", !isReady && "opacity-60")}
          formatReadout={(point) =>
            t("records.activityReadout", {
              count: point.value,
              time: timeFormat.format(point.at),
            })
          }
          // Points sit one step apart across the window, so the line is whole.
          gapMs={2 * ACTIVITY_STEP_MS}
          label={t("records.activityChart", {
            current: snapshot?.conversations ?? 0,
            peak,
          })}
          nowMs={live.now}
          points={points}
          title={t("records.activityChartTitle")}
          windowMs={ACTIVITY_WINDOW_MS}
        />
      </Panel>
    </div>
  );
}
