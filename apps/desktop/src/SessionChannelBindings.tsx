import { useEffect, useRef, useState } from "react";
import {
  getSessionChannelBindings,
  releaseSessionChannelBindings,
} from "./bridge";
import type {
  ChannelBindingAudit,
  ChannelBindingEvent,
} from "./channel-binding-model";
import { useT } from "./i18n";
import { notify } from "./notify";
import { protocolEntryPath } from "./service-presets";
import { Panel, PanelHeader } from "./components/Panel";
import { EmptyState } from "./components/EmptyState";
import { FormMessage } from "./components/FormMessage";
import { StatusBadge } from "./components/StatusBadge";
import { Button } from "./components/ui/button";

/** A session owns this view: no extra navigation or manual session identifiers. */
export function SessionChannelBindings({
  sessionId,
  serviceNames,
  onSelectRequest,
}: {
  sessionId: string;
  serviceNames: Record<string, string>;
  onSelectRequest: (requestId: string) => void;
}) {
  const t = useT();
  const [data, setData] = useState<ChannelBindingAudit | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  const [older, setOlder] = useState<ChannelBindingEvent[]>([]);
  const [olderHasMore, setOlderHasMore] = useState<boolean | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const browsingHistory = useRef(false);
  const generation = useRef(0);
  const mutation = useRef(false);
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    setData(null);
    setError(null);
    setOlder([]);
    setOlderHasMore(null);
    browsingHistory.current = false;
    const load = async () => {
      const version = generation.current;
      try {
        if (!mutation.current && !browsingHistory.current) {
          const result = await getSessionChannelBindings(sessionId);
          if (active && version === generation.current) {
            setData(result);
            setError(null);
          }
        }
      } catch (error) {
        if (active && version === generation.current)
          setError(
            error instanceof Error ? error.message : t("binding.loadFailed"),
          );
      } finally {
        if (active) timer = setTimeout(() => void load(), 3000);
      }
    };
    void load();
    return () => {
      active = false;
      clearTimeout(timer);
      generation.current++;
    };
  }, [sessionId, reload, t]);

  const release = async () => {
    if (mutation.current) return;
    mutation.current = true;
    const version = ++generation.current;
    setBusy(true);
    try {
      const result = await releaseSessionChannelBindings(sessionId);
      if (version !== generation.current) return;
      setData(result);
      setOlder([]);
      setOlderHasMore(null);
      browsingHistory.current = false;
      setError(null);
      notify.success(t("binding.releasedNotice"));
    } catch (error) {
      if (version === generation.current)
        setError(
          error instanceof Error ? error.message : t("binding.releaseFailed"),
        );
    } finally {
      mutation.current = false;
      setBusy(false);
    }
  };
  const loadOlder = async () => {
    const before = (older.at(-1) ?? data?.events.at(-1))?.id;
    if (!before || loadingOlder || mutation.current) return;
    // Invalidate a latest-page poll already in flight before freezing history.
    const version = ++generation.current;
    browsingHistory.current = true;
    setLoadingOlder(true);
    try {
      const result = await getSessionChannelBindings(sessionId, before);
      if (version !== generation.current) return;
      setOlder((current) => [...current, ...result.events]);
      setOlderHasMore(result.has_more);
      setError(null);
    } catch (error) {
      if (version === generation.current)
        setError(
          error instanceof Error ? error.message : t("binding.loadFailed"),
        );
    } finally {
      setLoadingOlder(false);
    }
  };
  const service = (id: string) =>
    serviceNames[id] ?? (id ? t("binding.removedService") : "—");
  const time = (value: string) => new Date(value).toLocaleString();
  const activeBindings =
    data?.bindings.filter(
      (binding) => Date.parse(binding.expires_at) > Date.now(),
    ) ?? [];
  const events = [...(data?.events ?? []), ...older];

  return (
    <div
      className="min-h-0 flex-1 overflow-y-auto overscroll-contain py-3"
      data-testid="channel-binding-scroll"
    >
      <div className="grid gap-3">
        {error && (
          <FormMessage tone="error">
            {error}
            <Button
              variant="ghost"
              size="sm"
              disabled={busy || loadingOlder}
              onClick={() => setReload((value) => value + 1)}
            >
              {t("common.retry")}
            </Button>
          </FormMessage>
        )}
        {!data && !error && (
          <p className="text-xs text-muted-foreground" role="status">
            {t("common.loading")}
          </p>
        )}
        {data && (
          <>
            <Panel>
              <PanelHeader
                actions={
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={busy}
                    onClick={() => void release()}
                  >
                    {busy ? t("binding.releasing") : t("binding.release")}
                  </Button>
                }
              >
                <h2 className="text-sm font-semibold">
                  {t("binding.current")}
                </h2>
                <p className="mt-1 text-xs text-muted-foreground">
                  {t("binding.releaseHint")}
                </p>
              </PanelHeader>
              {!data.enabled && (
                <p className="border-b px-4 py-3 text-xs text-muted-foreground">
                  {t("binding.disabled")}
                </p>
              )}
              {activeBindings.length === 0 ? (
                <EmptyState
                  className="min-h-28 border-0"
                  title={t("binding.empty")}
                  description={t(
                    data.enabled
                      ? "binding.emptyHint"
                      : "binding.emptyDisabled",
                  )}
                />
              ) : (
                <ul className="divide-y">
                  {activeBindings.map((binding) => (
                    <li
                      key={`${binding.local_access_token_id}:${binding.protocol}:${binding.model}`}
                      className="flex flex-wrap items-center justify-between gap-2 px-4 py-3"
                    >
                      <div className="min-w-0 flex-1 basis-56">
                        <p
                          className="truncate text-sm font-medium"
                          title={service(binding.service_id)}
                        >
                          {service(binding.service_id)}
                        </p>
                        <p className="mt-1 break-all text-xs text-muted-foreground">
                          {binding.model} ·{" "}
                          {protocolEntryPath(binding.protocol)}
                          {activeBindings.length > 1 &&
                          binding.local_access_token_id
                            ? ` · ${t("binding.token", { id: binding.local_access_token_id.slice(-8) })}`
                            : ""}
                        </p>
                      </div>
                      <p className="text-xs text-muted-foreground">
                        {t("binding.expires", {
                          time: time(binding.expires_at),
                        })}
                      </p>
                    </li>
                  ))}
                </ul>
              )}
              <p className="border-t px-4 py-2 text-xs text-muted-foreground">
                {t("binding.strictHint")}
              </p>
            </Panel>
            <Panel>
              <PanelHeader
                actions={
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={busy || loadingOlder}
                    onClick={() => setReload((value) => value + 1)}
                  >
                    {t("common.refresh")}
                  </Button>
                }
              >
                <h2 className="text-sm font-semibold">
                  {t("binding.history")}
                </h2>
                <p className="mt-1 text-xs text-muted-foreground">
                  {t("binding.historyHint")}
                </p>
              </PanelHeader>
              {data.events.length === 0 ? (
                <EmptyState
                  className="min-h-28 border-0"
                  title={t("binding.noEvents")}
                />
              ) : (
                <ol className="divide-y">
                  {events.map((event) => (
                    <li key={event.id} className="grid gap-1.5 px-4 py-3">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <div className="flex min-w-0 flex-wrap items-center gap-2">
                          <StatusBadge
                            tone={
                              event.action === "hit" || event.action === "bound"
                                ? "positive"
                                : "neutral"
                            }
                          >
                            {t(`binding.actions.${event.action}`)}
                          </StatusBadge>
                          {event.service_id && (
                            <span className="break-all text-xs font-medium">
                              {event.action === "switched" &&
                              event.previous_service_id
                                ? `${service(event.previous_service_id)} → `
                                : ""}
                              {service(event.service_id)}
                            </span>
                          )}
                        </div>
                        <time
                          dateTime={event.at}
                          className="text-micro tabular-nums text-muted-foreground"
                        >
                          {time(event.at)}
                        </time>
                      </div>
                      <p className="break-words text-xs text-muted-foreground">
                        {t(`binding.reasons.${event.reason}`, {
                          defaultValue: event.reason,
                        })}
                        {event.source
                          ? ` · ${t(`binding.sources.${event.source}`, { defaultValue: event.source })}`
                          : ""}
                        {event.model ? ` · ${event.model}` : ""}
                        {event.previous_service_id && event.action === "miss"
                          ? ` · ${service(event.previous_service_id)}`
                          : ""}
                      </p>
                      {event.request_id && (
                        <Button
                          className="h-auto justify-self-start px-0 py-0.5"
                          variant="link"
                          size="sm"
                          onClick={() => onSelectRequest(event.request_id)}
                        >
                          {t("binding.viewRequest")}
                        </Button>
                      )}
                    </li>
                  ))}
                </ol>
              )}
              <div className="flex flex-wrap items-center justify-between gap-2 border-t px-4 py-2">
                <p className="text-xs text-muted-foreground">
                  {t("binding.retention")}
                </p>
                {(olderHasMore ?? data.has_more) && (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={loadingOlder || busy}
                    onClick={() => void loadOlder()}
                  >
                    {t(loadingOlder ? "common.loading" : "records.loadEarlier")}
                  </Button>
                )}
              </div>
            </Panel>
          </>
        )}
      </div>
    </div>
  );
}
