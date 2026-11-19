import { useCallback, useEffect, useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { ExternalLink } from "@/components/ExternalLink";
import { FormMessage } from "@/components/FormMessage";
import { IconButton } from "@/components/IconButton";
import { Plus, RefreshCw, SquareStack } from "@/components/icons";
import { LoadingState } from "@/components/LoadingState";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel } from "@/components/Panel";
import { StatusBadge } from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";

import { clearPluginTasks, listPluginTasks } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { formatDateTime, formatMoment } from "./format-time";
import { useT } from "./i18n";
import { ModelChips, TaskEndpoints } from "./PluginTaskParts";
import {
  taskAuthLabel,
  taskStatusLabel,
  taskStatusTone,
  taskSurfaceLabel,
} from "./plugin-task-labels";
import type {
  PluginDetail,
  PluginTask,
  PluginTaskRecord,
} from "./plugin-model";
import type { Service } from "./service-model";

/** A section of a detail page: a quiet heading, then its content. */
function Section({
  title,
  actions,
  children,
  testId,
}: {
  title: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
  testId?: string;
}) {
  return (
    <section className="grid min-w-0 gap-2" data-testid={testId}>
      <div className="flex min-h-7 items-center justify-between gap-2">
        <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
        {actions}
      </div>
      {children}
    </section>
  );
}

/** The providers bound to a task plugin, from the providers page. */
function boundProviders(services: Service[], key: string): Service[] {
  return services.filter(
    (service) =>
      service.kind === "plugin" &&
      service.plugin?.key === key &&
      service.plugin.provider === "task",
  );
}

/**
 * A task plugin's overview: its numbers, what it is, the endpoints clients
 * reach it on, its models and the providers that run it.
 */
export function TaskPluginOverview({
  detail,
  task,
  services,
  onAddProvider,
  onOpenProvider,
}: {
  detail: PluginDetail;
  task: PluginTask;
  services: Service[];
  /** Opens the editor of a new provider of this plugin; absent where there is none. */
  onAddProvider?: () => void;
  onOpenProvider?: (id: string) => void;
}) {
  const t = useT();
  const providers = boundProviders(services, detail.key);
  const about: Array<{ label: string; value: React.ReactNode }> = [];
  if (task.author)
    about.push({
      label: t("plugins.task.author"),
      value: task.author_url ? (
        <ExternalLink className="text-xs" href={task.author_url}>
          {task.author}
        </ExternalLink>
      ) : (
        task.author
      ),
    });
  if (task.website)
    about.push({
      label: t("plugins.task.website"),
      value: (
        <ExternalLink className="text-xs break-all" href={task.website}>
          {task.website.replace(/^https?:\/\//, "")}
        </ExternalLink>
      ),
    });
  about.push({
    label: t("plugins.task.baseUrl"),
    value: task.base_url ? (
      <span className="font-mono">{task.base_url}</span>
    ) : (
      <span className="text-muted-foreground">
        {t("plugins.task.noBaseUrl")}
      </span>
    ),
  });
  about.push({
    label: t("plugins.task.authLabel"),
    value: taskAuthLabel(task.auth, t),
  });
  about.push({
    label: t("plugins.task.fetchModeLabel"),
    value: t(`plugins.task.fetchMode.${task.fetch_mode}`, {
      defaultValue: task.fetch_mode,
    }),
  });
  if (task.allowed_hosts?.length)
    about.push({
      label: t("plugins.task.allowedHosts"),
      value: <span className="font-mono">{task.allowed_hosts.join(", ")}</span>,
    });
  return (
    <div
      className="@container/overview grid min-w-0 gap-5"
      data-testid="plugin-task-overview"
    >
      <Panel>
        <MetricGroup className="grid-cols-2 @[560px]/overview:grid-cols-4">
          <Metric
            label={t("plugins.task.metrics.models")}
            size="sm"
            value={task.models.length}
          />
          <Metric
            label={t("plugins.task.metrics.running")}
            size="sm"
            value={detail.running_tasks ?? 0}
          />
          <Metric
            label={t("plugins.task.metrics.tasks")}
            size="sm"
            value={detail.tasks ?? 0}
          />
          <Metric
            label={t("plugins.task.metrics.providers")}
            size="sm"
            value={providers.length}
          />
        </MetricGroup>
      </Panel>
      {task.description ? (
        <p className="text-sm leading-6 text-text-secondary">
          {task.description}
        </p>
      ) : null}
      <Section
        title={t("plugins.task.providers")}
        testId="plugin-task-providers"
        actions={
          onAddProvider && detail.enabled ? (
            <Button
              onClick={onAddProvider}
              size="xs"
              type="button"
              variant="outline"
            >
              <Plus />
              {t("plugins.task.addProvider")}
            </Button>
          ) : null
        }
      >
        {providers.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {detail.enabled
              ? t("plugins.task.noProviders")
              : t("plugins.task.enableFirst")}
          </p>
        ) : (
          <ul className="grid divide-y overflow-hidden rounded-md border text-xs">
            {providers.map((service) => (
              <li key={service.id}>
                <Button
                  className="grid h-auto w-full min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-none px-3 py-2 text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset"
                  disabled={!onOpenProvider}
                  onClick={() => onOpenProvider?.(service.id)}
                  type="button"
                  variant="ghost"
                >
                  <span className="grid min-w-0 gap-0.5">
                    <span className="truncate font-medium">{service.name}</span>
                    <span className="truncate font-mono text-micro text-muted-foreground">
                      {service.http?.base_url || t("plugins.task.noBaseUrl")}
                    </span>
                  </span>
                  <span className="flex items-center gap-2 text-micro text-muted-foreground">
                    {t("plugins.market.models", {
                      count: service.models.length,
                    })}
                    <StatusBadge
                      tone={service.enabled ? "positive" : "neutral"}
                    >
                      {service.enabled
                        ? t("plugins.task.providerOn")
                        : t("plugins.task.providerOff")}
                    </StatusBadge>
                  </span>
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Section>
      <Section title={t("plugins.task.endpoints")}>
        <TaskEndpoints protocols={task.protocols} routes={task.routes} />
      </Section>
      <Section
        title={t("plugins.market.models", { count: task.models.length })}
      >
        <ModelChips models={task.models} />
      </Section>
      <Section title={t("plugins.task.about")}>
        <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-4 gap-y-2 text-xs">
          {about.map((item) => (
            <div className="contents" key={item.label}>
              <dt className="text-muted-foreground">{item.label}</dt>
              <dd className="min-w-0 break-words">{item.value}</dd>
            </div>
          ))}
        </dl>
      </Section>
    </div>
  );
}

/** How long a task ran, or has run. */
function taskDuration(task: PluginTaskRecord, now: number): string {
  const start = new Date(task.created_at).getTime();
  const end = task.finished_at ? new Date(task.finished_at).getTime() : now;
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start)
    return "";
  const seconds = Math.round((end - start) / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

/** The usage facts a task reported, as a short line. */
function factsText(usage: PluginTaskRecord["usage"]): string {
  if (!usage) return "";
  return Object.entries(usage)
    .slice(0, 4)
    .map(([name, value]) => `${name} ${String(value)}`)
    .join(" · ");
}

/**
 * The tasks a task plugin ran, the newest first: status, model, how it came
 * in, how long it took and what it reported, a page at a time. Finished
 * tasks can be cleared; their results go with them.
 */
export function TaskPluginTasks({ pluginKey }: { pluginKey: string }) {
  const t = useT();
  const [items, setItems] = useState<PluginTaskRecord[] | null>(null);
  const [next, setNext] = useState<string | undefined>(undefined);
  const [running, setRunning] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [clearing, setClearing] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(
    async (cursor?: string) => {
      setLoading(true);
      setError(null);
      try {
        const page = await listPluginTasks(pluginKey, cursor);
        setItems((current) =>
          cursor && current ? [...current, ...page.items] : page.items,
        );
        setNext(page.next_cursor);
        setRunning(page.running);
      } catch (cause) {
        setError(controlErrorMessage(cause, t("plugins.task.tasksFailed")));
      } finally {
        setLoading(false);
      }
    },
    [pluginKey, t],
  );

  useEffect(() => {
    setItems(null);
    void load();
  }, [load]);

  async function clear() {
    setBusy(true);
    try {
      await clearPluginTasks(pluginKey);
      await load();
    } catch (cause) {
      setError(controlErrorMessage(cause, t("plugins.task.clearFailed")));
    } finally {
      setBusy(false);
    }
  }

  const now = Date.now();
  const finished = (items ?? []).filter(
    (task) => task.status === "SUCCESS" || task.status === "FAILURE",
  ).length;
  return (
    <div className="grid min-w-0 gap-3" data-testid="plugin-tasks">
      <div className="flex min-h-7 items-center justify-between gap-2">
        <h3 className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          {t("plugins.task.tasksTitle")}
          {running > 0 ? (
            <StatusBadge tone="pending">
              {t("plugins.task.runningCount", { count: running })}
            </StatusBadge>
          ) : null}
        </h3>
        <span className="flex items-center gap-1.5">
          <Button
            disabled={busy || finished === 0}
            onClick={() => setClearing(true)}
            size="xs"
            type="button"
            variant="outline"
          >
            {t("plugins.task.clear")}
          </Button>
          <IconButton
            disabled={loading}
            label={t("common.refresh")}
            onClick={() => void load()}
            size="icon-sm"
            type="button"
            variant="ghost"
          >
            <RefreshCw />
          </IconButton>
        </span>
      </div>
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {items === null ? (
        loading ? (
          <LoadingState label={t("plugins.loading")} />
        ) : null
      ) : items.length === 0 ? (
        <EmptyState
          illustration={
            <SquareStack className="size-6 text-muted-foreground" />
          }
          title={t("plugins.task.noTasks")}
        />
      ) : (
        <ul className="grid divide-y overflow-hidden rounded-md border">
          {items.map((task) => (
            <li
              className="grid min-w-0 grid-cols-[5.5rem_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1 px-3 py-2.5 text-xs"
              data-testid="plugin-task-row"
              key={task.id}
            >
              <StatusBadge
                className="justify-self-start"
                tone={taskStatusTone(task.status)}
              >
                {taskStatusLabel(task.status, t)}
                {task.status === "IN_PROGRESS" && task.progress
                  ? ` ${task.progress}`
                  : ""}
              </StatusBadge>
              <span className="grid min-w-0 gap-0.5">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="truncate font-medium">{task.model}</span>
                  <span className="shrink-0 text-micro text-muted-foreground">
                    {taskSurfaceLabel(task.surface, t)}
                    {task.action ? ` · ${task.action}` : ""}
                  </span>
                </span>
                {task.fail_reason ? (
                  <span className="break-words text-micro text-danger-foreground">
                    {task.fail_reason}
                  </span>
                ) : factsText(task.usage) ? (
                  <span className="truncate text-micro text-muted-foreground">
                    {factsText(task.usage)}
                  </span>
                ) : null}
              </span>
              <span className="grid justify-items-end gap-0.5 text-micro text-muted-foreground tabular-nums">
                <time
                  dateTime={task.created_at}
                  title={formatDateTime(task.created_at)}
                >
                  {formatMoment(task.created_at)}
                </time>
                <span>{taskDuration(task, now)}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
      {next ? (
        <Button
          className="justify-self-center"
          disabled={loading}
          onClick={() => void load(next)}
          size="sm"
          type="button"
          variant="ghost"
        >
          {t("plugins.task.loadMore")}
        </Button>
      ) : null}
      <ConfirmDialog
        confirmLabel={t("plugins.task.clear")}
        description={<p>{t("plugins.task.clearConfirm")}</p>}
        destructive
        onCancel={() => setClearing(false)}
        onConfirm={() => {
          setClearing(false);
          void clear();
        }}
        open={clearing}
        title={t("plugins.task.clearTitle")}
      />
    </div>
  );
}
