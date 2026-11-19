import { useState } from "react";

import { CopyableValue } from "@/components/CopyableValue";
import { HelpPopover } from "@/components/HelpPopover";
import { ChevronRight, RefreshCw } from "@/components/icons";
import { Panel } from "@/components/Panel";
import { PluginIcon, pluginAvatar } from "@/components/PluginIcon";
import { SettingGroup, SettingRow } from "@/components/SettingRows";
import { Sparkline } from "@/components/Sparkline";
import type { StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Input, verbatimTextInput } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

import { useT } from "./i18n";
import { isMarketAddress } from "./PluginMarket";
import { taskSurfaceLabels } from "./plugin-task-labels";
import type {
  PluginHostSettings,
  PluginHostSettingsUpdate,
  PluginLoadError,
  PluginSummary,
} from "./plugin-model";
import { dayPoints, dayTrouble, dayWork, pluginDays } from "./plugin-usage";
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

/** The purpose a plugin serves, which groups the list. */
export type PluginGroup = "providers" | "requests" | "extensions";

export function pluginGroup(plugin: PluginSummary): PluginGroup {
  if (offersProviders(plugin)) return "providers";
  if (plugin.kind === "request") return "requests";
  return "extensions";
}

const GROUPS: PluginGroup[] = ["providers", "requests", "extensions"];

/** What a plugin does, in a few words, for its row. */
function pluginPurpose(plugin: PluginSummary, t: Translate): string {
  if (plugin.task?.description) return plugin.task.description;
  if (plugin.request?.description) return plugin.request.description;
  if (plugin.kind === "provider" || plugin.kind === "task") {
    const count = plugin.providers?.length ?? 0;
    return count > 0 ? "" : t("plugins.row.noProviders");
  }
  return "";
}

/**
 * The installed plugins by what they are for: those that connect providers,
 * those that check requests, and those that add features. Each group is a
 * panel of rows under a quiet heading.
 */
export function PluginRows({
  plugins,
  recent,
  rules,
  busyKey,
  disabled,
  onOpen,
  onEnabled,
  onReload,
}: {
  plugins: PluginSummary[];
  recent?: UsageSummary | null;
  /** How many rules each request plugin has, by key; absent until read. */
  rules?: Record<string, number> | null;
  /** The plugin whose switch is being saved. */
  busyKey?: string | null;
  disabled?: boolean;
  onOpen: (key: string) => void;
  onEnabled: (plugin: PluginSummary, enabled: boolean) => void;
  onReload: () => void;
}) {
  const t = useT();
  return (
    <div aria-label={t("plugins.listLabel")} className="grid gap-4" role="list">
      {GROUPS.map((group) => {
        const members = plugins.filter(
          (plugin) => pluginGroup(plugin) === group,
        );
        if (members.length === 0) return null;
        return (
          <section
            className="grid gap-1"
            data-group={group}
            data-testid="plugin-group"
            key={group}
            role="listitem"
          >
            <h2 className="flex items-center gap-1.5 px-1 text-xs font-medium text-muted-foreground">
              {t(`plugins.groups.${group}`)}
              <span className="tabular-nums">{members.length}</span>
              <HelpPopover
                label={t("plugins.groups.helpLabel", {
                  group: t(`plugins.groups.${group}`),
                })}
              >
                {t(`plugins.groups.${group}Help`)}
              </HelpPopover>
            </h2>
            <Panel asChild>
              <ul>
                {members.map((plugin) => (
                  <PluginRow
                    busy={busyKey === plugin.key}
                    disabled={disabled}
                    key={plugin.key}
                    onEnabled={(enabled) => onEnabled(plugin, enabled)}
                    onOpen={() => onOpen(plugin.key)}
                    onReload={onReload}
                    plugin={plugin}
                    recent={recent}
                    rules={rules ? (rules[plugin.key] ?? 0) : undefined}
                  />
                ))}
              </ul>
            </Panel>
          </section>
        );
      })}
    </div>
  );
}

function PluginRow({
  plugin,
  recent,
  rules,
  busy,
  disabled,
  onOpen,
  onEnabled,
  onReload,
}: {
  plugin: PluginSummary;
  recent?: UsageSummary | null;
  rules?: number;
  busy: boolean;
  disabled?: boolean;
  onOpen: () => void;
  onEnabled: (enabled: boolean) => void;
  onReload: () => void;
}) {
  const t = useT();
  const surfaces = plugin.task
    ? taskSurfaceLabels(plugin.task.protocols, plugin.task.routes, t)
    : [];
  const days = recent ? pluginDays(recent, plugin.key) : null;
  const work = days?.reduce((sum, day) => sum + dayWork(day), 0) ?? 0;
  const trouble = days?.reduce((sum, day) => sum + dayTrouble(day), 0) ?? 0;
  const providers =
    plugin.kind === "provider" || plugin.kind === "task"
      ? (plugin.providers?.length ?? 0)
      : 0;
  // A request plugin does nothing until it has a rule.
  const idle =
    plugin.kind === "request" && plugin.state === "loaded" && rules === 0;
  const subtitle = plugin.error
    ? t("plugins.row.failed", { reason: loadErrorText(plugin.error, t) })
    : idle
      ? t("plugins.row.noRules")
      : [
          pluginPurpose(plugin, t),
          providers > 0 ? t("plugins.row.providers", { count: providers }) : "",
          plugin.kind === "request" && rules
            ? t("plugins.row.rules", { count: rules })
            : "",
          pluginOrigin(plugin, t),
        ]
          .filter(Boolean)
          .join(" · ");
  const meta = plugin.running_tasks
    ? t("plugins.task.runningCount", { count: plugin.running_tasks })
    : days
      ? work > 0
        ? t("plugins.row.recent", { count: work })
        : ""
      : plugin.calls
        ? t("plugins.row.times", { count: plugin.calls })
        : "";
  return (
    <li className="flex min-w-0 items-center gap-3 border-b pr-3 last:border-b-0">
      <Button
        variant="ghost"
        className="grid h-15 min-w-0 flex-1 shrink grid-cols-[1.75rem_minmax(0,1fr)_auto_auto] items-center justify-normal gap-x-3 rounded-none px-0 pl-3 text-left text-sm font-normal whitespace-normal hover:bg-muted/40 focus-visible:bg-accent focus-visible:ring-0"
        data-plugin-key={plugin.key}
        data-testid="plugin-row"
        onClick={onOpen}
        type="button"
      >
        <PluginIcon
          className="size-7 rounded-md"
          icon={plugin.icon}
          label={pluginAvatar(plugin)}
        />
        <span className="grid min-w-0 gap-0.5">
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
                className="hidden shrink-0 font-normal @[720px]:inline-flex"
                key={surface}
                variant="outline"
              >
                {surface}
              </Badge>
            ))}
            {plugin.state === "loaded" && !plugin.enabled ? (
              <span className="shrink-0 text-micro text-muted-foreground">
                {t("plugins.state.disabled")}
              </span>
            ) : null}
          </span>
          <span
            className={cn(
              "truncate text-xs",
              plugin.error
                ? "text-danger-foreground"
                : idle
                  ? "text-warning-foreground"
                  : "text-text-secondary",
            )}
            title={plugin.error?.message ?? subtitle}
          >
            {subtitle}
          </span>
        </span>
        <span className="hidden w-28 justify-items-end gap-0.5 text-micro text-muted-foreground tabular-nums @[560px]:grid">
          {days && work > 0 ? (
            <Sparkline
              className="h-4 w-20"
              format={(value) => t("plugins.row.times", { count: value })}
              label={t("plugins.row.trend")}
              points={dayPoints(days, dayWork)}
            />
          ) : null}
          {meta ? <span>{meta}</span> : null}
          {trouble > 0 ? (
            <span className="text-warning-foreground">
              {t("plugins.row.failures", { count: trouble })}
            </span>
          ) : null}
        </span>
        <ChevronRight
          aria-hidden="true"
          className="size-3.5 text-muted-foreground"
        />
      </Button>
      {plugin.state === "failed" ? (
        <Button
          disabled={disabled}
          onClick={onReload}
          size="sm"
          type="button"
          variant="outline"
        >
          {t("plugins.row.reload")}
        </Button>
      ) : (
        <Switch
          aria-label={t("plugins.row.enable", { name: plugin.name })}
          checked={plugin.enabled}
          disabled={disabled || busy}
          onCheckedChange={onEnabled}
        />
      )}
    </li>
  );
}

const INJECTION_BUDGETS_KIB = [4, 8, 16, 32, 64];

/** The commands a plugin project is built and tried with. */
/**
 * The address of a marketplace the user adds, whose tab then joins New
 * API's two: saved as typed, removed when cleared.
 */
function CustomMarketField({
  value,
  onSave,
}: {
  value: string;
  onSave: (index: string) => void;
}) {
  const t = useT();
  const [draft, setDraft] = useState(value);
  const next = draft.trim();
  return (
    <form
      className="flex min-w-0 flex-1 items-center gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (next === value || (next && !isMarketAddress(next))) return;
        onSave(next);
      }}
    >
      <Input
        {...verbatimTextInput}
        className="h-8 min-w-0 flex-1 font-mono text-xs"
        id="plugin-custom-market"
        onChange={(event) => setDraft(event.target.value)}
        placeholder="https://…/index.json"
        value={draft}
      />
      <Button
        disabled={next === value || (next !== "" && !isMarketAddress(next))}
        size="sm"
        type="submit"
        variant="outline"
      >
        {!value
          ? t("plugins.market.customAdd")
          : next
            ? t("common.save")
            : t("plugins.market.customRemove")}
      </Button>
    </form>
  );
}

/**
 * The settings of every plugin at once, behind the header's 插件设置: the
 * pause of them all, their interfaces, how much they may add to one request
 * together and the order they run in; the marketplace the user added; then
 * what building plugins needs: the plugin directory and its rescan.
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
  customMarket,
  onCustomMarket,
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
  /** The marketplace the user added, "" for none. */
  customMarket: string;
  onCustomMarket: (index: string) => void;
}) {
  const t = useT();
  const budgetKib = host ? Math.round(host.injection_budget_bytes / 1024) : 16;
  const budgets = INJECTION_BUDGETS_KIB.includes(budgetKib)
    ? INJECTION_BUDGETS_KIB
    : [...INJECTION_BUDGETS_KIB, budgetKib].sort((a, b) => a - b);
  return (
    <Dialog onOpenChange={onOpenChange} open={open}>
      <DialogContent
        className="flex max-h-[85vh] flex-col overflow-hidden sm:max-w-2xl"
        data-testid="plugin-host-dialog"
      >
        <DialogHeader className="text-left">
          <DialogTitle>{t("plugins.host.title")}</DialogTitle>
        </DialogHeader>
        <div
          className="@container grid min-h-0 gap-5 overflow-y-auto overscroll-contain"
          data-testid="plugin-host-controls"
        >
          {host ? (
            <SettingGroup>
              <SettingRow
                inDialog
                helpLabel={t("plugins.host.helpLabel", {
                  label: t("plugins.host.pause"),
                })}
                help={t("plugins.host.pauseHelp")}
                htmlFor="plugin-host-pause"
                label={t("plugins.host.pause")}
              >
                <Switch
                  checked={host.paused}
                  disabled={disabled}
                  id="plugin-host-pause"
                  onCheckedChange={(paused) => onChange({ paused })}
                />
              </SettingRow>
              <SettingRow
                inDialog
                helpLabel={t("plugins.host.helpLabel", {
                  label: t("plugins.ui.switch"),
                })}
                help={uiHelp}
                htmlFor="plugin-host-ui"
                label={t("plugins.ui.switch")}
              >
                <Switch
                  checked={ui}
                  disabled={disabled}
                  id="plugin-host-ui"
                  onCheckedChange={onUi}
                />
              </SettingRow>
              <SettingRow
                inDialog
                helpLabel={t("plugins.host.helpLabel", {
                  label: t("plugins.host.budget"),
                })}
                help={t("plugins.host.budgetHelp")}
                htmlFor="plugin-host-budget"
                label={t("plugins.host.budget")}
              >
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
                </Select>
              </SettingRow>
              {canOrder ? (
                <SettingRow
                  inDialog
                  helpLabel={t("plugins.host.helpLabel", {
                    label: t("plugins.order.title"),
                  })}
                  help={t("plugins.order.description")}
                  htmlFor="plugin-host-order"
                  label={t("plugins.order.title")}
                >
                  <Button
                    disabled={disabled}
                    id="plugin-host-order"
                    onClick={onOrder}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    {t("plugins.order.open")}
                  </Button>
                </SettingRow>
              ) : null}
            </SettingGroup>
          ) : null}
          <SettingGroup title={t("plugins.list.market")}>
            <SettingRow
              inDialog
              helpLabel={t("plugins.host.helpLabel", {
                label: t("plugins.market.customTitle"),
              })}
              help={t("plugins.market.customHelp")}
              htmlFor="plugin-custom-market"
              label={t("plugins.market.customTitle")}
            >
              <CustomMarketField onSave={onCustomMarket} value={customMarket} />
            </SettingRow>
          </SettingGroup>
          <SettingGroup title={t("plugins.developHome.title")}>
            {directory !== null ? (
              <SettingRow
                inDialog
                helpLabel={t("plugins.host.helpLabel", {
                  label: t("plugins.directoryHelpLabel"),
                })}
                help={`${t("plugins.directoryHelp")} ${t("plugins.rescanHelp")}`}
                label={t("plugins.directoryHelpLabel")}
              >
                <div className="grid min-w-0 flex-1 gap-2">
                  <CopyableValue
                    copyLabel={t("common.copy")}
                    label={t("plugins.directoryPath")}
                    placeholder=""
                    value={directory}
                  />
                  <Button
                    className="justify-self-start"
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
              </SettingRow>
            ) : null}
          </SettingGroup>
        </div>
      </DialogContent>
    </Dialog>
  );
}
