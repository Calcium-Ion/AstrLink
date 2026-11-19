import type { ReactNode } from "react";

import { CopyableValue } from "@/components/CopyableValue";
import { DataRow } from "@/components/DataRow";
import { HelpPopover } from "@/components/HelpPopover";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel } from "@/components/Panel";
import { PluginIcon, pluginAvatar } from "@/components/PluginIcon";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { RefreshCw } from "@/components/icons";

import { formatExactNumber } from "./format-compact-number";
import { useT } from "./i18n";
import { taskSurfaceLabels } from "./plugin-task-labels";
import type {
  PluginHostSettings,
  PluginHostSettingsUpdate,
  PluginLoadError,
  PluginSummary,
} from "./plugin-model";
import type { UsageSummary } from "./usage-range";

type Translate = ReturnType<typeof useT>;

/** What a plugin that did not load says, in the user's words: where, and why. */
export function loadErrorText(error: PluginLoadError, t: Translate): string {
  const reason = t(`plugins.loadErrors.${error.code}`, {
    defaultValue: error.code,
  });
  const where =
    error.file && error.line
      ? t("plugins.errorAt", { file: error.file, line: error.line })
      : (error.file ?? "");
  return [where, reason].filter(Boolean).join(" · ");
}

export function pluginTone(plugin: PluginSummary): StatusTone {
  if (plugin.state === "failed") return "negative";
  return plugin.enabled ? "positive" : "neutral";
}

export function pluginState(plugin: PluginSummary, t: Translate): string {
  if (plugin.state === "failed") return t("plugins.state.failed");
  return plugin.enabled
    ? t("plugins.state.running")
    : t("plugins.state.disabled");
}

/** Where a plugin comes from, as its row and its page say it. */
export function pluginOrigin(plugin: PluginSummary, t: Translate): string {
  if (plugin.development) return t("plugins.develop.build");
  const installed = plugin.installed;
  if (installed?.kind === "market")
    return installed.repository || t("plugins.source.market");
  if (installed) return installed.repository;
  return t(`plugins.source.${plugin.source}`);
}

/**
 * Whether a plugin answers a search: by its name or key, where it comes
 * from, what it says it does, or a model it generates with.
 */
export function pluginMatches(
  plugin: PluginSummary,
  query: string,
  t: Translate,
): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return [
    plugin.name,
    plugin.key,
    pluginOrigin(plugin, t),
    plugin.task?.description ?? "",
    ...(plugin.task?.models ?? []),
  ].some((text) => text.toLowerCase().includes(needle));
}

/** Whether a plugin offers providers: a new-api task or provider plugin, or a provider adapter. */
export function offersProviders(plugin: PluginSummary): boolean {
  return (
    plugin.kind === "task" ||
    plugin.kind === "provider" ||
    plugin.requested_capabilities.includes("provider")
  );
}

/**
 * The band over the list: how many plugins are installed and running, the
 * requests they handled over the last 30 days, the tasks under way, and the
 * plugins in trouble: one that did not load, or a running one that failed
 * over the same days. Should the days not be read, the calls and failures
 * since the gateway started stand in.
 */
export function PluginMetrics({
  plugins,
  recent,
}: {
  plugins: PluginSummary[];
  /** The last 30 days: undefined while they are read, null if they could not be. */
  recent?: Pick<UsageSummary, "by_plugin"> | null;
}) {
  const t = useT();
  const running = plugins.filter(
    (plugin) => plugin.state === "loaded" && plugin.enabled,
  ).length;
  const failedRecently = new Set(
    (recent?.by_plugin ?? [])
      .filter((group) => group.failed_requests + group.failed_tasks > 0)
      .map((group) => group.plugin),
  );
  const calls =
    recent === undefined
      ? "—"
      : formatExactNumber(
          recent
            ? recent.by_plugin.reduce(
                (sum, group) => sum + group.requests + group.failed_requests,
                0,
              )
            : plugins.reduce((sum, plugin) => sum + (plugin.calls ?? 0), 0),
        );
  const tasks = plugins.reduce(
    (sum, plugin) => sum + (plugin.running_tasks ?? 0),
    0,
  );
  const problems = plugins.filter(
    (plugin) =>
      plugin.state === "failed" ||
      (plugin.enabled &&
        (recent ? failedRecently.has(plugin.key) : (plugin.failures ?? 0) > 0)),
  ).length;
  return (
    <Panel
      aria-label={t("plugins.metrics.label")}
      className="shrink-0"
      data-testid="plugin-metrics"
      role="region"
    >
      {/* Narrow, the band keeps what needs a look: installed, running and problems. */}
      <MetricGroup className="grid-cols-3 @min-[640px]/workspace-surface:grid-cols-5">
        <Metric
          label={t("plugins.metrics.installed")}
          size="sm"
          value={plugins.length}
        />
        <Metric
          label={t("plugins.metrics.running")}
          size="sm"
          value={running}
        />
        <Metric
          className="@max-[639px]/workspace-surface:hidden"
          label={t(
            recent === null
              ? "plugins.metrics.calls"
              : "plugins.metrics.recentCalls",
          )}
          size="sm"
          value={calls}
        />
        <Metric
          className="@max-[639px]/workspace-surface:hidden"
          label={t("plugins.metrics.tasks")}
          size="sm"
          value={tasks}
        />
        <Metric
          label={t("plugins.metrics.problems")}
          size="sm"
          value={
            <span className={problems > 0 ? "text-destructive" : undefined}>
              {problems}
            </span>
          }
        />
      </MetricGroup>
    </Panel>
  );
}

/** A group of the list: a quiet heading with its count, then its rows. */
function ListGroup({
  title,
  count,
  children,
}: {
  title: string;
  count: number;
  children: ReactNode;
}) {
  return (
    <section className="grid gap-1" data-testid="plugin-group">
      <h2 className="flex items-center gap-2 px-3 pt-3 pb-1 text-xs font-medium text-muted-foreground">
        {title}
        <Badge className="px-1.5 py-0 tabular-nums" variant="secondary">
          {count}
        </Badge>
      </h2>
      <ul className="border-y">{children}</ul>
    </section>
  );
}

/**
 * The installed plugins, those that enhance requests apart from those that
 * offer providers, each a row: its state, icon, name and version, where it
 * comes from and what it serves, and on the right what it did.
 */
export function PluginRows({
  plugins,
  onOpen,
}: {
  plugins: PluginSummary[];
  onOpen: (key: string) => void;
}) {
  const t = useT();
  const providers = plugins.filter(offersProviders);
  const extensions = plugins.filter((plugin) => !offersProviders(plugin));
  // In the order a request meets them, as the star map draws them.
  const groups = [
    { key: "extensions", plugins: extensions },
    { key: "providers", plugins: providers },
  ].filter((group) => group.plugins.length > 0);
  return (
    <div aria-label={t("plugins.listLabel")} className="grid gap-2" role="list">
      {groups.map((group) => (
        <ListGroup
          count={group.plugins.length}
          key={group.key}
          title={t(`plugins.groups.${group.key}`)}
        >
          {group.plugins.map((plugin) => (
            <li className="border-b last:border-b-0" key={plugin.key}>
              <PluginRow onOpen={() => onOpen(plugin.key)} plugin={plugin} />
            </li>
          ))}
        </ListGroup>
      ))}
    </div>
  );
}

function PluginRow({
  plugin,
  onOpen,
}: {
  plugin: PluginSummary;
  onOpen: () => void;
}) {
  const t = useT();
  const surfaces = plugin.task
    ? taskSurfaceLabels(plugin.task.protocols, plugin.task.routes, t)
    : [];
  const description =
    plugin.task?.description ??
    (plugin.error ? loadErrorText(plugin.error, t) : "");
  return (
    <DataRow
      asChild
      className="grid grid-cols-[5rem_1.75rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-3 py-2.5"
    >
      <Button
        className="h-auto w-full rounded-none bg-transparent text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset"
        data-plugin-key={plugin.key}
        data-testid="plugin-row"
        onClick={onOpen}
        type="button"
        variant="ghost"
      >
        <StatusBadge
          className="row-span-2 self-start justify-self-start"
          tone={pluginTone(plugin)}
        >
          {pluginState(plugin, t)}
        </StatusBadge>
        <PluginIcon
          className="row-span-2 size-7 self-start rounded-md"
          icon={plugin.icon}
          label={pluginAvatar(plugin)}
        />
        <span className="flex min-w-0 items-center gap-2">
          <strong className="truncate text-sm font-medium">
            {plugin.name}
          </strong>
          {plugin.version ? (
            <span className="shrink-0 text-micro text-muted-foreground tabular-nums">
              {plugin.version}
            </span>
          ) : null}
          {surfaces.map((surface) => (
            <Badge
              className="hidden shrink-0 px-1.5 py-0 font-normal @[560px]:inline-flex"
              key={surface}
              variant="outline"
            >
              {surface}
            </Badge>
          ))}
        </span>
        <span className="row-span-2 grid justify-items-end gap-0.5 text-micro text-muted-foreground tabular-nums">
          {plugin.task ? (
            <span>
              {t("plugins.market.models", { count: plugin.task.models.length })}
            </span>
          ) : null}
          {plugin.running_tasks ? (
            <span className="text-warning-foreground">
              {t("plugins.task.runningCount", { count: plugin.running_tasks })}
            </span>
          ) : plugin.calls ? (
            <span>
              {t("plugins.metrics.callCount", { count: plugin.calls })}
            </span>
          ) : null}
        </span>
        <span
          className={
            plugin.error
              ? "col-start-3 truncate text-xs text-danger-foreground"
              : "col-start-3 truncate text-xs text-muted-foreground"
          }
          title={plugin.error?.message ?? description}
        >
          {[pluginOrigin(plugin, t), description].filter(Boolean).join(" · ")}
        </span>
      </Button>
    </DataRow>
  );
}

const INJECTION_BUDGETS_KIB = [4, 8, 16, 32, 64];

/**
 * The settings of every plugin at once, behind the header's 插件设置: the
 * pause of them all, their interfaces, what they may inject into one
 * request together, the order they run in, and the plugin directory.
 */
export function PluginHostDialog({
  open,
  host,
  directory,
  disabled,
  ui,
  rescanning,
  canOrder,
  onOpenChange,
  onChange,
  onUi,
  onOrder,
  onRescan,
  uiHelp,
}: {
  open: boolean;
  host: PluginHostSettings | null;
  directory: string | null;
  disabled: boolean;
  /** Whether plugins' interfaces load in this window. */
  ui: boolean;
  rescanning: boolean;
  canOrder: boolean;
  onOpenChange: (open: boolean) => void;
  onChange: (update: PluginHostSettingsUpdate) => void;
  onUi: (on: boolean) => void;
  onOrder: () => void;
  onRescan: () => void;
  uiHelp: string;
}) {
  const t = useT();
  const budgetKib = host ? Math.round(host.injection_budget_bytes / 1024) : 16;
  const budgets = INJECTION_BUDGETS_KIB.includes(budgetKib)
    ? INJECTION_BUDGETS_KIB
    : [...INJECTION_BUDGETS_KIB, budgetKib].sort((a, b) => a - b);
  const row = (id: string, label: string, help: string, control: ReactNode) => (
    <div className="flex min-h-12 items-center justify-between gap-6 py-2">
      <span className="flex min-w-0 items-center gap-1">
        <Label className="text-sm font-medium" htmlFor={id}>
          {label}
        </Label>
        <HelpPopover inDialog label={t("plugins.host.helpLabel", { label })}>
          {help}
        </HelpPopover>
      </span>
      {control}
    </div>
  );
  return (
    <Dialog onOpenChange={onOpenChange} open={open}>
      <DialogContent className="sm:max-w-lg" data-testid="plugin-host-dialog">
        <DialogHeader className="text-left">
          <DialogTitle>{t("plugins.host.title")}</DialogTitle>
        </DialogHeader>
        <div className="grid divide-y" data-testid="plugin-host-controls">
          {host ? (
            <>
              {row(
                "plugin-host-pause",
                t("plugins.host.pause"),
                t("plugins.host.pauseHelp"),
                <Switch
                  aria-label={t("plugins.host.pause")}
                  checked={host.paused}
                  disabled={disabled}
                  id="plugin-host-pause"
                  onCheckedChange={(paused) => onChange({ paused })}
                />,
              )}
              {row(
                "plugin-host-ui",
                t("plugins.ui.switch"),
                uiHelp,
                <Switch
                  aria-label={t("plugins.ui.switch")}
                  checked={ui}
                  disabled={disabled}
                  id="plugin-host-ui"
                  onCheckedChange={onUi}
                />,
              )}
              {row(
                "plugin-host-budget",
                t("plugins.host.budget"),
                t("plugins.host.budgetHelp"),
                <Select
                  disabled={disabled}
                  onValueChange={(value) =>
                    onChange({ injection_budget_bytes: Number(value) * 1024 })
                  }
                  value={String(budgetKib)}
                >
                  <SelectTrigger
                    className="w-28"
                    id="plugin-host-budget"
                    size="sm"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {budgets.map((kib) => (
                      <SelectItem key={kib} value={String(kib)}>
                        {t("plugins.host.budgetValue", { value: kib })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>,
              )}
              {canOrder
                ? row(
                    "plugin-host-order",
                    t("plugins.order.title"),
                    t("plugins.order.description"),
                    <Button
                      disabled={disabled}
                      id="plugin-host-order"
                      onClick={onOrder}
                      size="sm"
                      type="button"
                      variant="outline"
                    >
                      {t("plugins.order.open")}
                    </Button>,
                  )
                : null}
            </>
          ) : null}
          {directory !== null ? (
            <div className="grid gap-2 py-3">
              <div className="flex items-center justify-between gap-3">
                <span className="flex min-w-0 items-center gap-1 text-sm font-medium">
                  {t("plugins.directoryHelpLabel")}
                  <HelpPopover inDialog label={t("plugins.directoryHelpLabel")}>
                    {t("plugins.directoryHelp")} {t("plugins.rescanHelp")}
                  </HelpPopover>
                </span>
                <Button
                  disabled={disabled || rescanning}
                  onClick={onRescan}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  <RefreshCw
                    className={
                      rescanning
                        ? "animate-spin motion-reduce:animate-none"
                        : undefined
                    }
                  />
                  {rescanning ? t("plugins.rescanning") : t("plugins.rescan")}
                </Button>
              </div>
              <CopyableValue
                copyLabel={t("common.copy")}
                label={t("plugins.directoryPath")}
                placeholder=""
                value={directory}
              />
            </div>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** The commands a plugin project is built and tried with. */
const COMMANDS = [
  { command: "astrlink plugin new <key>", key: "new" },
  { command: "astrlink plugin dev", key: "dev" },
  { command: "astrlink plugin check", key: "check" },
  { command: "astrlink plugin test", key: "test" },
  { command: "astrlink plugin build", key: "build" },
];

/**
 * The develop tab of the list: where the plugins live, the commands a
 * project is built with, and the plugins built from projects on this
 * computer.
 */
export function PluginDevelopHome({
  directory,
  plugins,
  disabled,
  rescanning,
  onRescan,
  onOpen,
}: {
  directory: string;
  plugins: PluginSummary[];
  disabled: boolean;
  rescanning: boolean;
  onRescan: () => void;
  onOpen: (key: string) => void;
}) {
  const t = useT();
  const built = plugins.filter((plugin) => plugin.development);
  return (
    <div className="grid min-w-0 gap-5 py-3" data-testid="plugin-develop-home">
      <section className="grid gap-2">
        <div className="flex items-center justify-between gap-3">
          <h3 className="flex items-center gap-1 text-xs font-medium text-muted-foreground">
            {t("plugins.directoryHelpLabel")}
            <HelpPopover label={t("plugins.directoryHelpLabel")}>
              {t("plugins.directoryHelp")} {t("plugins.rescanHelp")}
            </HelpPopover>
          </h3>
          <Button
            disabled={disabled || rescanning}
            onClick={onRescan}
            size="xs"
            type="button"
            variant="outline"
          >
            <RefreshCw
              className={
                rescanning
                  ? "animate-spin motion-reduce:animate-none"
                  : undefined
              }
            />
            {rescanning ? t("plugins.rescanning") : t("plugins.rescan")}
          </Button>
        </div>
        <CopyableValue
          copyLabel={t("common.copy")}
          label={t("plugins.directoryPath")}
          placeholder=""
          value={directory}
        />
      </section>
      <section className="grid gap-2">
        <h3 className="text-xs font-medium text-muted-foreground">
          {t("plugins.developHome.commands")}
        </h3>
        <dl className="grid divide-y overflow-hidden rounded-md border text-xs">
          {COMMANDS.map((entry) => (
            <div
              className="grid gap-1 px-3 py-2 @[560px]:grid-cols-[14rem_minmax(0,1fr)] @[560px]:gap-4"
              key={entry.key}
            >
              <dt>
                <code className="font-mono">{entry.command}</code>
              </dt>
              <dd className="text-text-secondary">
                {t(`plugins.developHome.command.${entry.key}`)}
              </dd>
            </div>
          ))}
        </dl>
      </section>
      <section className="grid gap-2">
        <h3 className="text-xs font-medium text-muted-foreground">
          {t("plugins.developHome.builds")}
        </h3>
        {built.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {t("plugins.developHome.noBuilds")}
          </p>
        ) : (
          <ul className="grid divide-y overflow-hidden rounded-md border text-xs">
            {built.map((plugin) => (
              <li key={plugin.key}>
                <Button
                  className="grid h-auto w-full min-w-0 grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-3 rounded-none px-3 py-2 text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset"
                  onClick={() => onOpen(plugin.key)}
                  type="button"
                  variant="ghost"
                >
                  <PluginIcon icon={plugin.icon} label={pluginAvatar(plugin)} />
                  <span className="grid min-w-0 gap-0.5">
                    <span className="truncate font-medium">{plugin.name}</span>
                    <span className="truncate font-mono text-micro text-muted-foreground">
                      {plugin.development?.project}
                    </span>
                  </span>
                  <StatusBadge tone={pluginTone(plugin)}>
                    {pluginState(plugin, t)}
                  </StatusBadge>
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
