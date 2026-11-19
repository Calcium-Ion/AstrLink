import { useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DisclosureRow } from "@/components/DisclosureRow";
import { PluginView } from "@/components/PluginView";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { runPluginAction } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import { notify } from "./notify";
import { hookLabel } from "./plugin-labels";
import type {
  MetadataEvent,
  MetadataValue,
  PluginRecordAction,
  RequestMetadata,
  RequestPluginRecord,
} from "./plugin-model";
import type { RequestRecord } from "./request-record-model";

const statusTones: Record<RequestPluginRecord["status"], StatusTone> = {
  ok: "positive",
  skipped: "neutral",
  failed: "negative",
};

const knownSkipReasons = new Set([
  "no_project",
  "short_query",
  "no_hits",
  "search_failed",
  "server_unavailable",
  "timeout",
  "plugin_error",
  "privacy_blocked",
  "privacy_unavailable",
  "paused_all",
  "client_disabled",
  "session_paused",
  "over_budget",
  "invalid_result",
  "no_system_channel",
  "no_opening_message",
  "injection_failed",
  "rate_limited",
  "turn_limit",
  "unbound",
  "invalid_input",
  "circuit_open",
  "no_request_gate",
  "no_request_write",
  "no_request_write_raw",
  "would_exclude_all",
  "invalid_body",
  "header_denied",
  "stream_stopped",
  "memory_limit",
  "canceled",
  "unavailable",
]);

/** The hooks of the extension points, whose records name the point. */
/** The stages of new-api's request processing plugins. */
const requestStages = new Set([
  "ingress",
  "request",
  "outbound",
  "egress",
  "reply",
  "report",
]);

const extensionHooks = new Set([
  "onRequest",
  "route",
  "beforeForward",
  "beforeUpstream",
  "rewriteResponse",
  "onStreamEvent",
  "onResponse",
]);

const knownEffects = new Set([
  "deny",
  "tag",
  "order",
  "exclude",
  "model",
  "system",
  "turn",
  "params",
  "tools",
  "body",
  "headers",
  "rewrite",
  "stream",
  "observed",
]);

/** What an extension point's call did, in words. */
function effectsText(entry: RequestPluginRecord, t: Translate): string {
  const effects = (entry.effects ?? []).map((effect) =>
    effect === "tag" && entry.tags && entry.tags.length > 0
      ? t("pluginRecords.effects.tagged", {
          tags: entry.tags.join(t("pluginRecords.effects.separator")),
        })
      : knownEffects.has(effect)
        ? t(`pluginRecords.effects.${effect}`)
        : effect,
  );
  return effects.join(t("pluginRecords.effects.separator"));
}

const knownTriggers = new Set([
  "start",
  "bound",
  "config",
  "compact",
  "manual",
  "auto",
]);

/** The limits a report has apart from the queries'. */
const reportLimits = new Set(["rate_limited", "turn_limit"]);

const knownObserve = new Set([
  "queued",
  "awaiting_reply",
  "held_unbound",
  "skipped_unbound",
  "skipped_privacy",
  "skipped_binding_turn",
  "skipped_continuation",
  "queue_full",
  "disabled",
]);

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KB`;
}

type Translate = ReturnType<typeof useT>;

/** Why the hook gave the request nothing, in words. */
function skipReason(entry: RequestPluginRecord, t: Translate): string {
  const reason = entry.skipped_reason;
  if (!reason) return "";
  if (!knownSkipReasons.has(reason)) return reason;
  return entry.lookup?.writes && reportLimits.has(reason)
    ? t(`pluginRecords.reportSkipReasons.${reason}`)
    : t(`pluginRecords.skipReasons.${reason}`);
}

function observeState(observe: string, t: Translate): string {
  return knownObserve.has(observe)
    ? t(`pluginRecords.observeStates.${observe}`)
    : observe;
}

function lookupText(entry: RequestPluginRecord, t: Translate): string {
  const lookup = entry.lookup;
  if (!lookup) return "";
  return [
    lookup.tool,
    t("pluginRecords.lookupInput", { count: lookup.input_runes }),
    entry.status === "ok"
      ? t("pluginRecords.lookupResult", {
          size: formatBytes(lookup.result_bytes),
        })
      : "",
    lookup.hits !== undefined
      ? t("pluginRecords.lookupHits", { count: lookup.hits })
      : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

function injectionText(entry: RequestPluginRecord, t: Translate): string {
  const injection = entry.injection;
  if (!injection) return "";
  return [
    t(`pluginRecords.placement.${injection.placement}`),
    formatBytes(injection.injected_bytes),
    injection.replayed_blocks > 0
      ? t("pluginRecords.replayed", { count: injection.replayed_blocks })
      : "",
    injection.dropped_items > 0
      ? t("pluginRecords.dropped", { count: injection.dropped_items })
      : "",
    injection.usage_bytes
      ? t("pluginRecords.usagePart", {
          size: formatBytes(injection.usage_bytes),
        })
      : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

function networkText(entry: RequestPluginRecord, t: Translate): string {
  const network = entry.network;
  return [
    t("pluginRecords.networkShort", {
      count: network.requests,
      wait: network.wait_ms,
    }),
    network.denied > 0
      ? t("pluginRecords.networkDenied", { count: network.denied })
      : "",
    network.errors > 0
      ? t("pluginRecords.networkErrors", { count: network.errors })
      : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

/**
 * The status a record shows: a block the plugin skipped while the request
 * still carried the usage of its lookups was no skip as far as the request
 * goes.
 */
export function shownStatus(
  entry: RequestPluginRecord,
): RequestPluginRecord["status"] {
  return entry.status === "skipped" && (entry.injection?.usage_bytes ?? 0) > 0
    ? "ok"
    : entry.status;
}

/**
 * What a record says at a glance, and what waits behind it. The headline is
 * what the plugin did; the points under it are why, what it added to the
 * request and what became of the turn's capture; the rest, down to the
 * hook's name, opens on demand.
 */
function describe(entry: RequestPluginRecord, t: Translate) {
  const reason = skipReason(entry, t);
  const usageOnly = shownStatus(entry) !== entry.status;
  const observe =
    entry.observe && entry.observe !== "queued"
      ? observeState(entry.observe, t)
      : "";
  const effects = effectsText(entry, t);
  const stream =
    entry.events !== undefined && entry.events > 0
      ? entry.failures
        ? t("pluginRecords.streamEventsFailed", {
            count: entry.events,
            failures: entry.failures,
          })
        : t("pluginRecords.streamEvents", { count: entry.events })
      : "";
  const headline =
    entry.summary ||
    (entry.status !== "ok" ? reason : "") ||
    effects ||
    stream ||
    lookupText(entry, t) ||
    (observe ? t("pluginRecords.observeLine", { state: observe }) : "");
  const points = [
    entry.trigger
      ? knownTriggers.has(entry.trigger)
        ? t(`pluginRecords.triggers.${entry.trigger}`)
        : entry.trigger
      : "",
    entry.injection && entry.injection.injected_bytes > 0
      ? t(usageOnly ? "pluginRecords.usageOnly" : "pluginRecords.injected", {
          placement: t(`pluginRecords.placement.${entry.injection.placement}`),
          size: formatBytes(entry.injection.injected_bytes),
        })
      : "",
    entry.summary && reason ? reason : "",
    entry.summary && effects ? effects : "",
    stream && headline !== stream ? stream : "",
    entry.status === "ok" && reason && !entry.summary ? reason : "",
    observe && headline !== t("pluginRecords.observeLine", { state: observe })
      ? t("pluginRecords.observeLine", { state: observe })
      : "",
  ].filter(Boolean);
  const details: Array<[string, string, string?]> = [];
  if (entry.injection)
    details.push([t("pluginRecords.injection"), injectionText(entry, t)]);
  if (entry.lookup)
    details.push([
      entry.lookup.writes
        ? t("pluginRecords.report")
        : t("pluginRecords.lookup"),
      lookupText(entry, t),
      "plugin-record-lookup",
    ]);
  if (entry.commands && entry.commands.length > 0)
    details.push([t("pluginRecords.commands"), entry.commands.join(", ")]);
  if (entry.network.requests > 0)
    details.push([t("pluginRecords.network"), networkText(entry, t)]);
  // Quiet: a turn that went as usual, with nothing of its own to say.
  const quiet =
    entry.status === "ok" &&
    !entry.summary &&
    !entry.lookup &&
    points.length === 0 &&
    !(entry.view && entry.view.length > 0) &&
    !(entry.actions && entry.actions.length > 0);
  return {
    headline: headline || (quiet ? t("pluginRecords.quiet") : ""),
    points,
    details,
    quiet,
    usageOnly,
  };
}

/**
 * The plugins' parts in one request, one line each: what the plugin did,
 * why and at what cost, in that order of weight. The sizes, the network and
 * the plugin's own view open from the line; a failure opens by itself.
 */
export function PluginRecordDetails({ record }: { record: RequestRecord }) {
  const t = useT();
  const records = record.plugin_records ?? [];
  const [confirming, setConfirming] = useState<{
    plugin: string;
    action: PluginRecordAction;
  } | null>(null);
  const [running, setRunning] = useState(false);

  async function run(plugin: string, action: PluginRecordAction) {
    if (!record.session_id) return;
    setRunning(true);
    try {
      const result = await runPluginAction(
        plugin,
        action.id,
        record.session_id,
      );
      const why = result.message || result.reason;
      if (result.status === "ok")
        notify.success(result.message || t("pluginRecords.actionDone"));
      else
        notify.error(
          why
            ? t("pluginRecords.actionFailed", { reason: why })
            : t("pluginRecords.actionFailedPlain"),
        );
    } catch (reason) {
      notify.error(
        controlErrorMessage(reason, t("pluginRecords.actionFailedPlain")),
      );
    } finally {
      setRunning(false);
    }
  }

  if (records.length === 0) {
    return (
      <p className="text-xs leading-6 text-muted-foreground">
        {t("pluginRecords.none")}
      </p>
    );
  }
  return (
    <div className="grid min-w-0" data-testid="plugin-records">
      <ul className="min-w-0 divide-y rounded-md border">
        {records.map((entry, index) => (
          <PluginRecordRow
            actions={record.session_id ? entry.actions : undefined}
            entry={entry}
            key={`${entry.plugin}:${entry.hook}:${index}`}
            onAction={(action) => {
              if (action.confirm)
                setConfirming({ plugin: entry.plugin, action });
              else void run(entry.plugin, action);
            }}
            running={running}
          />
        ))}
      </ul>
      <ConfirmDialog
        confirmLabel={confirming?.action.label ?? ""}
        description={<p>{confirming?.action.confirm}</p>}
        disabled={running}
        onCancel={() => setConfirming(null)}
        onConfirm={() => {
          const target = confirming;
          setConfirming(null);
          if (target) void run(target.plugin, target.action);
        }}
        open={confirming !== null}
        title={confirming?.action.label ?? ""}
      />
    </div>
  );
}

/** The label column the details and the plugin's own view share. */
const detailColumns = "grid-cols-[5rem_minmax(0,1fr)]";

function PluginRecordRow({
  entry,
  actions,
  running,
  onAction,
}: {
  entry: RequestPluginRecord;
  actions?: PluginRecordAction[];
  running: boolean;
  onAction: (action: PluginRecordAction) => void;
}) {
  const t = useT();
  const { headline, points, details, quiet, usageOnly } = describe(entry, t);
  const point = extensionHooks.has(entry.hook)
    ? hookLabel(entry.hook, t)
    : requestStages.has(entry.hook)
      ? t(`plugins.requestPlugins.stages.${entry.hook}`)
      : "";
  const view = entry.view && entry.view.length > 0 ? entry.view : null;
  const buttons = actions && actions.length > 0 ? actions : null;
  // As in request records, only a record that went wrong carries a pill.
  const line = (
    <div className="grid min-w-0 flex-1 gap-0.5">
      <div className="flex min-w-0 items-center gap-2">
        <span
          className={cn(
            "min-w-0 truncate text-xs font-medium",
            quiet && "font-normal text-muted-foreground",
          )}
        >
          {entry.title}
        </span>
        {point ? (
          <span
            className="shrink-0 text-micro text-muted-foreground"
            data-testid="plugin-record-point"
          >
            {point}
          </span>
        ) : null}
        {entry.status !== "ok" && !usageOnly ? (
          <StatusBadge
            className="shrink-0"
            data-testid="plugin-record-status"
            tone={statusTones[entry.status]}
          >
            {t(`pluginRecords.status.${entry.status}`)}
          </StatusBadge>
        ) : null}
        <span className="flex-1" />
        {entry.elapsed_ms > 0 ? (
          <span className="shrink-0 text-micro text-muted-foreground tabular-nums">
            {entry.elapsed_ms} ms
          </span>
        ) : null}
      </div>
      {headline ? (
        <p
          className={cn(
            "min-w-0 text-xs break-words",
            quiet && "text-muted-foreground",
            entry.status === "failed" && "text-danger-foreground",
          )}
          data-testid="plugin-record-headline"
        >
          {headline}
        </p>
      ) : null}
      {points.length > 0 ? (
        <p className="min-w-0 text-micro break-words text-muted-foreground">
          {points.join(" · ")}
        </p>
      ) : null}
    </div>
  );
  return (
    <li data-testid="plugin-record">
      <DisclosureRow defaultOpen={entry.status === "failed"} summary={line}>
        <div
          className="grid min-w-0 gap-2.5 px-3 pt-0.5 pb-3"
          data-testid="plugin-record-details"
        >
          {details.length > 0 ? (
            <dl
              className={cn(
                "grid min-w-0 gap-x-4 gap-y-1 text-xs",
                detailColumns,
              )}
            >
              {details.map(([label, value, testID]) => (
                <div className="contents" key={label}>
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd
                    className="min-w-0 break-words tabular-nums"
                    data-testid={testID}
                  >
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
          ) : null}
          {view ? (
            <PluginView
              blocks={view}
              kvClassName={detailColumns}
              truncated={entry.truncated}
              truncatedLabel={t("pluginRecords.truncated")}
            />
          ) : null}
          {buttons ? (
            <div className="flex flex-wrap gap-2">
              {buttons.map((action) => (
                <Button
                  className="h-7 text-xs"
                  data-testid="plugin-record-action"
                  disabled={running}
                  key={action.id}
                  onClick={() => onAction(action)}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {action.label}
                </Button>
              ))}
            </div>
          ) : null}
          <p className="truncate font-mono text-micro text-muted-foreground">
            {entry.plugin} {entry.plugin_version} · {entry.hook}
          </p>
        </div>
      </DisclosureRow>
    </li>
  );
}

function valueSource(value: MetadataValue, t: ReturnType<typeof useT>): string {
  const source =
    value.source === "session" && value.set_by
      ? t(`pluginRecords.setBy.${value.set_by}`, {
          defaultValue: t("pluginRecords.metadataSources.session"),
        })
      : t(`pluginRecords.metadataSources.${value.source}`);
  return value.turn
    ? t("pluginRecords.metadataTurn", { source, turn: value.turn })
    : source;
}

function eventText(event: MetadataEvent, t: ReturnType<typeof useT>): string {
  const value = event.value ? ` ${event.value}` : "";
  if (event.event === "ignored") {
    return t("pluginRecords.metadataIgnored", {
      key: event.key,
      value: event.value ?? "",
      reason: event.reason
        ? t(`pluginRecords.ignoredReasons.${event.reason}`, {
            defaultValue: event.reason,
          })
        : "",
    });
  }
  const notes = [
    event.set_by
      ? t(`pluginRecords.setBy.${event.set_by}`, { defaultValue: event.set_by })
      : "",
    event.new ? t("pluginRecords.metadataNew") : "",
    event.normalized
      ? t("pluginRecords.metadataNormalized", { value: event.normalized })
      : "",
  ].filter(Boolean);
  const suffix = notes.length > 0 ? ` (${notes.join(" · ")})` : "";
  return `${event.key}: ${t(`pluginRecords.metadataEvents.${event.event}`)}${value}${suffix}`;
}

/**
 * A request's metadata in one line: each value with where it came from, then
 * what the request changed or tried to.
 */
export function RequestMetadataLine({
  metadata,
}: {
  metadata: RequestMetadata | undefined;
}) {
  const t = useT();
  if (!metadata) return null;
  const values = Object.entries(metadata.values);
  if (values.length === 0 && metadata.events.length === 0) return null;
  return (
    <div
      className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-micro text-muted-foreground"
      data-testid="request-metadata"
    >
      <span className="font-medium">{t("pluginRecords.metadata")}</span>
      {values.map(([key, value]) => (
        <span className="min-w-0" key={key}>
          <span className="font-mono text-foreground">
            {key}={value.value}
          </span>{" "}
          ({valueSource(value, t)})
        </span>
      ))}
      {metadata.events.map((event, index) => (
        <span className="min-w-0 text-warning-foreground" key={index}>
          {eventText(event, t)}
        </span>
      ))}
    </div>
  );
}
