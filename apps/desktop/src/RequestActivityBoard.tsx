import { useEffect, useMemo } from "react";

import { ClientTypeCounts, clientTypeName } from "@/components/ClientTypeIcon";
import { HelpPopover } from "@/components/HelpPopover";
import { LiveTrendChart } from "@/components/LiveTrendChart";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel } from "@/components/Panel";
import { ValueTransition } from "@/components/ValueTransition";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { getRequestActivity } from "./bridge";
import { currentLocale, useT } from "./i18n";
import {
  ACTIVITY_STEP_MS,
  ACTIVITY_WINDOW_MS,
  activitySince,
  summarizeActivity,
} from "./request-activity-model";
import type { RequestActivity } from "./request-record-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

const POLL_INTERVAL_MS = 1000;

/**
 * Reads the calls of the last few minutes once a second. The gateway keeps
 * them, so the chart is whole however long the page was away; the last read
 * stays in the workspace snapshot so coming back paints it at once.
 */
function useLiveActivity(coreSessionKey: string | null, isReady: boolean) {
  const [activity, setActivity] = useWorkspaceSnapshot<RequestActivity | null>(
    `request-activity:${coreSessionKey}`,
    null,
  );
  useEffect(() => {
    if (!isReady) return;
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
  }, [isReady, setActivity]);
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

/**
 * The live band above the request list: how many conversations are going on
 * right now, how many calls they run, recent failures, and how the count
 * moved over the last five minutes. It covers every model call,
 * whatever the list below is filtered to.
 */
export function RequestActivityBoard({
  coreSessionKey,
  isReady,
  onShowFailures,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
  onShowFailures: () => void;
}) {
  const t = useT();
  const live = useLiveActivity(coreSessionKey, isReady);
  const snapshot = isReady ? (live?.snapshot ?? null) : null;
  const points = live?.points ?? [];
  const conversations = snapshot?.conversations ?? null;
  const failures = snapshot?.failures ?? 0;
  const peak = Math.max(0, ...points.map((sample) => sample.value));
  const timeFormat = new Intl.DateTimeFormat(currentLocale(), {
    hour: "numeric",
    minute: "numeric",
    second: "numeric",
    hour12: false,
  });
  const clientList = new Intl.ListFormat(currentLocale(), { style: "narrow" });

  return (
    <Panel
      aria-label={t("records.activityTitle")}
      className="grid shrink-0 grid-cols-[minmax(0,auto)_minmax(10rem,1fr)]"
      data-testid="request-activity"
      role="region"
    >
      <MetricGroup className="grid-cols-[repeat(2,minmax(6.5rem,auto))] border-r @min-[640px]/workspace-surface:grid-cols-[repeat(3,minmax(6.5rem,auto))]">
        <Metric
          label={
            <span className="inline-flex items-center gap-0.5">
              {t("records.activityInProgress")}
              {/* The help button is taller than the label line; keep the row even. */}
              <span className="-my-1 inline-flex">
                <HelpPopover label={t("records.activityInProgressHelp")}>
                  {t("records.activityInProgressHint")}
                </HelpPopover>
              </span>
            </span>
          }
          size="sm"
          value={
            <span data-testid="activity-conversations">
              <ValueTransition valueKey={String(conversations)}>
                {conversations ?? "—"}
              </ValueTransition>
              {conversations === null ? null : (
                <Unit>
                  {t("records.activityConversations", {
                    count: conversations,
                  })}
                </Unit>
              )}
            </span>
          }
        />
        <Metric
          label={t("records.activityCalls")}
          size="sm"
          value={
            <ValueTransition
              data-testid="activity-calls"
              valueKey={String(snapshot?.calls)}
            >
              {snapshot?.calls ?? "—"}
            </ValueTransition>
          }
        />
        <Metric
          className="@max-[639px]/workspace-surface:hidden"
          label={t("records.activityFailures")}
          size="sm"
          value={
            failures > 0 ? (
              <Button
                aria-label={t("records.activityShowFailures", {
                  count: failures,
                })}
                className="h-auto rounded-sm p-0 text-lg leading-6 font-semibold text-destructive"
                data-testid="activity-failures"
                onClick={onShowFailures}
                type="button"
                variant="link"
              >
                {failures}
              </Button>
            ) : (
              <span data-testid="activity-failures">{snapshot ? 0 : "—"}</span>
            )
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
          current: conversations ?? 0,
          peak,
        })}
        nowMs={live?.now ?? Date.now()}
        points={points}
        title={t("records.activityChartTitle")}
        windowMs={ACTIVITY_WINDOW_MS}
      />
    </Panel>
  );
}
