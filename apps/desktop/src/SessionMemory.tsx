import { useCallback, useEffect, useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { HelpPopover } from "@/components/HelpPopover";
import { LoadingState } from "@/components/LoadingState";
import { Panel, PanelHeader } from "@/components/Panel";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import {
  getSessionMetadata,
  getSessionPlugins,
  listMetadataAttributes,
  pauseSessionPlugin,
  refreshSessionPlugin,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { formatClockTime, formatDateTime, formatMoment } from "./format-time";
import { useT } from "./i18n";
import { shownStatus } from "./PluginRecordDetails";
import type {
  MetadataAttribute,
  RequestPluginRecord,
  SessionMetadata,
  SessionPlugin,
} from "./plugin-model";
import type { RequestRecord } from "./request-record-model";
import { SessionMetadataDialog, sessionBindingSource } from "./SessionMetadata";
import { PluginSlot } from "./plugin-ui";

const statusTones: Record<RequestPluginRecord["status"], StatusTone> = {
  ok: "positive",
  skipped: "neutral",
  failed: "negative",
};

const triggers = new Set([
  "start",
  "bound",
  "config",
  "compact",
  "manual",
  "auto",
]);

type Pending = { kind: "refresh" | "pause" | "resume"; part: SessionPlugin };

/**
 * A turn the plugin added nothing to, at most keeping earlier blocks: the
 * request's own record keeps it, the session's list leaves it out.
 */
function idle(entry: RequestPluginRecord): boolean {
  return (
    entry.hook === "prepareTurn" &&
    entry.status === "ok" &&
    !entry.summary &&
    !entry.skipped_reason &&
    (!entry.injection || entry.injection.injected_bytes === 0)
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KB`;
}

/**
 * The session's memory: the project it is bound to and how, the block each
 * plugin keeps for it in Core's memory, what the plugins did in its
 * requests, and the refresh and pause of each plugin there.
 */
export function SessionMemory({
  sessionId,
  refreshKey,
  records,
  onSelectRequest,
}: {
  sessionId: string;
  refreshKey: string;
  records: RequestRecord[];
  onSelectRequest: (id: string) => void;
}) {
  const t = useT();
  const [parts, setParts] = useState<SessionPlugin[] | null>(null);
  const [bindings, setBindings] = useState<SessionMetadata | null>(null);
  const [attributes, setAttributes] = useState<MetadataAttribute[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);

  const load = useCallback(async () => {
    try {
      const [plugins, values, list] = await Promise.all([
        getSessionPlugins(sessionId),
        getSessionMetadata(sessionId),
        listMetadataAttributes(),
      ]);
      setParts(plugins);
      setBindings(values);
      setAttributes(list);
      setError(null);
    } catch (reason) {
      setError(controlErrorMessage(reason, t("sessionMemory.loadFailed")));
    }
  }, [sessionId, t]);

  useEffect(() => {
    void load();
  }, [load, refreshKey]);

  async function act(next: Pending) {
    setBusy(true);
    try {
      const plugins =
        next.kind === "refresh"
          ? await refreshSessionPlugin(sessionId, next.part.plugin)
          : await pauseSessionPlugin(
              sessionId,
              next.part.plugin,
              next.kind === "pause",
            );
      setParts(plugins);
      setError(null);
    } catch (reason) {
      setError(controlErrorMessage(reason, t("sessionMemory.actionFailed")));
    } finally {
      setBusy(false);
    }
  }

  if (parts === null && error === null)
    return <LoadingState className="flex-1" label={t("common.loading")} />;
  const shown = attributes.filter(
    (attribute) => attribute.marker || bindings?.values[attribute.key],
  );
  const keys = new Set((parts ?? []).map((part) => part.plugin));
  const history = records
    .flatMap((record) =>
      (record.plugin_records ?? [])
        .filter((entry) => keys.has(entry.plugin) && !idle(entry))
        .map((entry) => ({ record, entry })),
    )
    .reverse();

  return (
    <div
      className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain py-3"
      data-tab-scroller
      data-testid="session-memory"
    >
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {/* What plugins' interfaces show of the session. */}
      <PluginSlot
        className="space-y-3"
        context={{ session: sessionId }}
        name="session.section"
      />
      {bindings && shown.length > 0 ? (
        <Panel>
          <PanelHeader
            actions={
              <Button
                onClick={() => setEditing(true)}
                size="sm"
                type="button"
                variant="outline"
              >
                {t("sessionMemory.changeProject")}
              </Button>
            }
            size="sm"
          >
            <h3 className="text-xs font-semibold">
              {t("sessionMemory.project")}
            </h3>
          </PanelHeader>
          <dl className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-4 gap-y-2 p-3 text-xs">
            {shown.map((attribute) => {
              const bound = bindings.values[attribute.key];
              return (
                <div className="contents" key={attribute.key}>
                  <dt className="text-muted-foreground">{attribute.label}</dt>
                  <dd className="min-w-0">
                    {bound ? (
                      <>
                        <span className="font-mono">
                          {bound.value === "none"
                            ? t("sessionMetadata.none")
                            : bound.value}
                        </span>
                        <span className="text-muted-foreground">
                          {" · "}
                          {sessionBindingSource(bound, t)}
                        </span>
                      </>
                    ) : (
                      <span className="text-muted-foreground">
                        {t("sessionMetadata.notBound")}
                      </span>
                    )}
                  </dd>
                </div>
              );
            })}
            {bindings.workspace ? (
              <>
                <dt className="text-muted-foreground">
                  {t("sessionMemory.workspace")}
                </dt>
                <dd className="min-w-0 font-mono">{bindings.workspace.name}</dd>
              </>
            ) : null}
          </dl>
        </Panel>
      ) : null}
      {(parts ?? []).length === 0 ? (
        <EmptyState title={t("sessionMemory.noPlugins")} />
      ) : null}
      {(parts ?? []).map((part) => (
        <Panel data-testid="session-memory-plugin" key={part.plugin}>
          <PanelHeader
            actions={
              <>
                <Button
                  disabled={busy || part.paused}
                  onClick={() => setPending({ kind: "refresh", part })}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {t("sessionMemory.refresh")}
                </Button>
                <Button
                  disabled={busy}
                  onClick={() =>
                    setPending({
                      kind: part.paused ? "resume" : "pause",
                      part,
                    })
                  }
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {part.paused
                    ? t("sessionMemory.resume")
                    : t("sessionMemory.pause")}
                </Button>
              </>
            }
            size="sm"
          >
            <div className="flex min-w-0 items-center gap-2">
              <h3 className="truncate text-xs font-semibold">{part.name}</h3>
              {part.paused ? (
                <StatusBadge tone="neutral">
                  {t("sessionMemory.paused")}
                </StatusBadge>
              ) : null}
              {part.refresh_pending ? (
                <StatusBadge tone="neutral">
                  {t("sessionMemory.refreshPending")}
                </StatusBadge>
              ) : null}
              <HelpPopover label={t("sessionMemory.helpLabel")}>
                <p>{t("sessionMemory.help")}</p>
              </HelpPopover>
            </div>
          </PanelHeader>
          <div className="grid gap-2 p-3 text-xs">
            {part.first_turn ? (
              <p className="text-muted-foreground">
                {t("sessionMemory.firstTurn", { turn: part.first_turn })}
              </p>
            ) : null}
            {part.block ? (
              <>
                <p className="text-muted-foreground">
                  {[
                    triggers.has(part.block.trigger)
                      ? t(`pluginRecords.triggers.${part.block.trigger}`)
                      : part.block.trigger,
                    formatMoment(part.block.computed_at),
                    formatBytes(part.block.bytes),
                  ].join(" · ")}
                </p>
                <pre
                  className="max-h-80 overflow-auto rounded-md border bg-muted/40 p-2 font-mono text-micro leading-5 whitespace-pre-wrap"
                  data-testid="session-memory-block"
                >
                  {part.block.text}
                </pre>
              </>
            ) : (
              <p className="text-muted-foreground">
                {t("sessionMemory.noBlock")}
              </p>
            )}
            {part.lookup ? (
              // How the model may look things up matters while debugging, so
              // it waits behind a fold.
              <HelpDisclosure title={t("sessionMemory.lookup")}>
                <pre
                  className="max-h-60 overflow-auto rounded-md border bg-muted/40 p-2 font-mono text-micro leading-5 whitespace-pre-wrap text-foreground"
                  data-testid="session-memory-lookup"
                >
                  {part.lookup}
                </pre>
              </HelpDisclosure>
            ) : null}
          </div>
        </Panel>
      ))}
      {history.length > 0 ? (
        <Panel>
          <PanelHeader size="sm">
            <h3 className="text-xs font-semibold">
              {t("sessionMemory.records")}
            </h3>
          </PanelHeader>
          <ul className="divide-y text-xs">
            {history.map(({ record, entry }, index) => (
              <li key={`${record.id}:${entry.hook}:${index}`}>
                <Button
                  className="grid h-auto w-full min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 rounded-none px-3 py-2 text-left text-xs font-normal"
                  onClick={() => onSelectRequest(record.id)}
                  type="button"
                  variant="ghost"
                >
                  <time
                    className="text-muted-foreground tabular-nums"
                    dateTime={record.started_at}
                    title={formatDateTime(record.started_at)}
                  >
                    {formatClockTime(record.started_at)}
                  </time>
                  <span className="min-w-0 truncate">
                    <span className="font-medium">{entry.title}</span>
                    {entry.summary ? ` · ${entry.summary}` : ""}
                    {entry.trigger && triggers.has(entry.trigger)
                      ? ` · ${t(`pluginRecords.triggers.${entry.trigger}`)}`
                      : ""}
                    {entry.skipped_reason
                      ? ` · ${t(`pluginRecords.skipReasons.${entry.skipped_reason}`, { defaultValue: entry.skipped_reason })}`
                      : ""}
                  </span>
                  {/* As in request records, only what went wrong is marked. */}
                  {shownStatus(entry) === "ok" ? null : (
                    <StatusBadge tone={statusTones[shownStatus(entry)]}>
                      {t(`pluginRecords.status.${shownStatus(entry)}`)}
                    </StatusBadge>
                  )}
                </Button>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}
      {bindings ? (
        <SessionMetadataDialog
          attributes={shown}
          bindings={bindings}
          onChanged={(next) => {
            setBindings(next);
            void load();
          }}
          onClose={() => setEditing(false)}
          open={editing}
          sessionId={sessionId}
        />
      ) : null}
      <ConfirmDialog
        confirmLabel={
          pending ? t(`sessionMemory.confirm.${pending.kind}.action`) : ""
        }
        description={
          <p>
            {pending ? t(`sessionMemory.confirm.${pending.kind}.body`) : null}
          </p>
        }
        disabled={busy}
        onCancel={() => setPending(null)}
        onConfirm={() => {
          const next = pending;
          setPending(null);
          if (next) void act(next);
        }}
        open={pending !== null}
        title={
          pending
            ? t(`sessionMemory.confirm.${pending.kind}.title`, {
                name: pending.part.name,
              })
            : ""
        }
      />
    </div>
  );
}
