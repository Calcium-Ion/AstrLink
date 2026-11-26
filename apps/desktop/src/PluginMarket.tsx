import { useCallback, useEffect, useMemo, useState } from "react";

import { EmptyState } from "@/components/EmptyState";
import { ExternalLink, openInSystemBrowser } from "@/components/ExternalLink";
import { FormMessage } from "@/components/FormMessage";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import {
  ArrowUpRight,
  Boxes,
  Check,
  ChevronRight,
  Copy,
  RefreshCw,
} from "@/components/icons";
import { LoadingState } from "@/components/LoadingState";
import { MarkdownContent } from "@/components/MarkdownContent";
import { Panel } from "@/components/Panel";
import { PluginIcon, pluginAvatar } from "@/components/PluginIcon";
import { SegmentedControl } from "@/components/SegmentedControl";
import { StatusBadge } from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import {
  findPluginByCode,
  getPluginMarket,
  getPluginMarketDoc,
  pluginInstallFailureOf,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useCopyFeedback } from "./copy-feedback";
import { useLocale, useT } from "./i18n";
import { installFailureText } from "./PluginInstallReview";
import { ModelChips, TaskEndpoints } from "./PluginTaskParts";
import { taskAuthLabel } from "./plugin-task-labels";
import type {
  PluginMarket,
  PluginMarketDoc,
  PluginMarketEntry,
} from "./plugin-model";

/** The marketplace the page last opened, kept for this computer. */
const MARKET_INDEX_KEY = "astrlink.plugins.marketIndex";

/** New API's community marketplace. */
export const COMMUNITY_MARKET_INDEX =
  "https://www.newapi.ai/api/v1/community/index.json";

/** A plugin's code on the New API site, which finds it in any marketplace. */
const PLUGIN_CODE = /^\d{8}$/;

/** The address of a marketplace the user added, kept for this computer. */
const CUSTOM_MARKET_KEY = "astrlink.plugins.customMarket";

type MarketSource = "official" | "community" | "custom";

function rememberedIndex(): string {
  try {
    return window.localStorage.getItem(MARKET_INDEX_KEY) ?? "";
  } catch {
    return "";
  }
}

/**
 * The marketplace the user added in 插件设置, "" for none. One opened by
 * its address before it had its own setting becomes it.
 */
export function readCustomMarket(): string {
  try {
    const stored = window.localStorage.getItem(CUSTOM_MARKET_KEY);
    if (stored !== null) return stored;
    const last = rememberedIndex();
    if (!last || last === COMMUNITY_MARKET_INDEX) return "";
    window.localStorage.setItem(CUSTOM_MARKET_KEY, last);
    return last;
  } catch {
    return "";
  }
}

/** Keeps the marketplace the user added, or forgets it for "". */
export function saveCustomMarket(index: string) {
  try {
    window.localStorage.setItem(CUSTOM_MARKET_KEY, index);
    const last = rememberedIndex();
    if (!index && last && last !== COMMUNITY_MARKET_INDEX)
      window.localStorage.removeItem(MARKET_INDEX_KEY);
  } catch {
    // Without storage the marketplace lasts until the window closes.
  }
}

/** Whether text is the address of a marketplace's index: http(s). */
export function isMarketAddress(text: string): boolean {
  try {
    const parsed = new URL(text);
    return parsed.protocol === "https:" || parsed.protocol === "http:";
  } catch {
    return false;
  }
}

function hostOf(index: string): string {
  try {
    return new URL(index).host;
  } catch {
    return index;
  }
}

/** New API's plugin pages, in Chinese or English. */
function newAPIPage(locale: string, page: "plugins" | "community"): string {
  return `https://www.newapi.ai/${locale.startsWith("zh") ? "zh" : "en"}/${page}`;
}

/** New API's plugin API, which the marketplaces' plugins are written for. */
const PLUGIN_API_DOCS =
  "https://github.com/QuantumNous/new-api/blob/main/docs/plugin-api/v1.md";

/** The raw contract files an AI reads to write a New API plugin. */
const NEW_API_PLUGIN_DOCS = [
  "https://raw.githubusercontent.com/QuantumNous/new-api/main/docs/plugin-api/v1.md",
  "https://raw.githubusercontent.com/QuantumNous/new-api/main/docs/plugin-api/v1.d.ts",
  "https://raw.githubusercontent.com/QuantumNous/new-api/main/docs/plugin-api/v1.schema.json",
].join("\n");

/** The templates astrlink plugin new offers, named in the prompt for an AI. */
const PLUGIN_TEMPLATES =
  "basic, route, stream, response, schedule, http, ui, files";

/** The commands that start, run and check a plugin, in the publish dialog. */
const PUBLISH_COMMANDS = [
  { command: "astrlink plugin new <key>", key: "new" },
  { command: "astrlink plugin dev", key: "dev" },
  { command: "astrlink plugin check", key: "check" },
] as const;

function rememberIndex(index: string) {
  try {
    if (index) window.localStorage.setItem(MARKET_INDEX_KEY, index);
    else window.localStorage.removeItem(MARKET_INDEX_KEY);
  } catch {
    // The choice is a convenience: without storage the official one opens.
  }
}

/** Whether a version is newer than another, by their numbers. */
function newer(candidate: string, current: string): boolean {
  const parts = (version: string) =>
    version
      .split(/[-+]/)[0]
      .split(".")
      .map((part) => Number.parseInt(part, 10) || 0);
  const a = parts(candidate);
  const b = parts(current);
  for (let index = 0; index < Math.max(a.length, b.length); index++) {
    const left = a[index] ?? 0;
    const right = b[index] ?? 0;
    if (left !== right) return left > right;
  }
  return false;
}

/** What the card's button does for an entry: install, update, or nothing. */
function entryState(
  entry: PluginMarketEntry,
): "install" | "update" | "installed" | "revoked" | "incompatible" {
  if (entry.unsupported?.length) return "incompatible";
  if (!entry.installed) return "install";
  if (newer(entry.latest, entry.installed)) return "update";
  return entry.revoked ? "revoked" : "installed";
}

/** Why the marketplace took down the version installed now. */
function revokedReason(
  entry: PluginMarketEntry,
  t: ReturnType<typeof useT>,
): string {
  return entry.revoked?.reason
    ? t("plugins.market.revokedReason", { reason: entry.revoked.reason })
    : t("plugins.market.revokedNoReason");
}

/** Why AstrLink cannot install an entry: the new-api parts it needs. */
function incompatibleReason(
  entry: PluginMarketEntry,
  t: ReturnType<typeof useT>,
): string {
  return t("plugins.market.incompatibleReason", {
    parts: (entry.unsupported ?? [])
      .map((part) =>
        t(
          part === "provider"
            ? "plugins.market.parts.provider"
            : part === "transform"
              ? "plugins.market.parts.transform"
              : "plugins.market.parts.other",
          { part },
        ),
      )
      .join(t("plugins.market.partSeparator")),
  });
}

/**
 * A plugin marketplace in new-api's index format: New API's official or
 * community one, or one the user added in 插件设置, its plugins as rows, a row opening what the plugin serves,
 * its usage guide and its versions. Installing hands the link of a version
 * to the install dialog, which reviews it first. The page header's search
 * filters the rows, and an 8-digit code finds the plugin of that code in any
 * New API marketplace.
 */
export function PluginMarketView({
  isReady,
  query,
  customIndex,
  onInstall,
  onOpenInstalled,
}: {
  isReady: boolean;
  /** The marketplace the user added in 插件设置, "" for none. */
  customIndex: string;
  /** The page header's search. */
  query: string;
  /** Reviews the install of the version a link names. */
  onInstall: (link: string) => void;
  /** Opens an installed plugin's own page. */
  onOpenInstalled?: (key: string) => void;
}) {
  const t = useT();
  const locale = useLocale();
  const [source, setSource] = useState<MarketSource>(() => {
    const last = rememberedIndex();
    if (last === COMMUNITY_MARKET_INDEX) return "community";
    return last && last === customIndex ? "custom" : "official";
  });
  // The tab of a marketplace the user removed goes with it.
  const shownSource = source === "custom" && !customIndex ? "official" : source;
  const index =
    shownSource === "official"
      ? ""
      : shownSource === "community"
        ? COMMUNITY_MARKET_INDEX
        : customIndex;
  // What the added marketplace calls itself, once it was read.
  const [customName, setCustomName] = useState<{
    index: string;
    name: string;
  } | null>(null);
  const [publishing, setPublishing] = useState(false);
  const [market, setMarket] = useState<PluginMarket | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<{
    entry: PluginMarketEntry;
    index: string;
  } | null>(null);
  // The plugin an 8-digit code found outside the listing shown.
  const [found, setFound] = useState<PluginMarket | null>(null);
  const [finding, setFinding] = useState(false);
  const [findError, setFindError] = useState<string | null>(null);

  const load = useCallback(
    async (target: string) => {
      setLoading(true);
      setError(null);
      try {
        const next = await getPluginMarket(target || undefined);
        setMarket(next);
        if (target && target !== COMMUNITY_MARKET_INDEX && next.name)
          setCustomName({ index: target, name: next.name });
      } catch (cause) {
        const failure = pluginInstallFailureOf(cause);
        setMarket(null);
        setError(
          failure
            ? installFailureText(failure, t)
            : controlErrorMessage(cause, t("plugins.market.failed")),
        );
      } finally {
        setLoading(false);
      }
    },
    [t],
  );

  useEffect(() => {
    rememberIndex(index);
    if (isReady) void load(index);
  }, [index, isReady, load]);

  const [group, setGroup] = useState<EntryGroup | "all">("all");
  const needle = query.trim().toLowerCase();
  const groupCounts = useMemo(() => {
    const counts: Record<EntryGroup, number> = {
      providers: 0,
      requests: 0,
      other: 0,
    };
    for (const entry of market?.plugins ?? []) counts[entryGroup(entry)] += 1;
    return counts;
  }, [market]);
  const groups = (["providers", "requests", "other"] as const).filter(
    (next) => groupCounts[next] > 0,
  );
  // A group the listing shown does not have filters nothing.
  const shownGroup = group !== "all" && groupCounts[group] > 0 ? group : "all";
  const listed = useMemo(() => {
    const plugins = (market?.plugins ?? []).filter(
      (entry) => shownGroup === "all" || entryGroup(entry) === shownGroup,
    );
    if (!needle) return plugins;
    return plugins.filter((entry) =>
      [
        entry.name,
        entry.key,
        entry.description ?? "",
        entry.code ?? "",
        entry.author?.login ?? "",
        ...entry.models,
      ].some((text) => text.toLowerCase().includes(needle)),
    );
  }, [market, needle, shownGroup]);
  // A code the listing does not have is looked up on the New API site.
  const code =
    PLUGIN_CODE.test(needle) && listed.length === 0 && market ? needle : "";

  useEffect(() => {
    setFound(null);
    setFindError(null);
    setFinding(false);
    if (!code || !isReady) return;
    let current = true;
    setFinding(true);
    findPluginByCode(code)
      .then((result) => {
        if (current) setFound(result);
      })
      .catch((cause: unknown) => {
        if (!current) return;
        const failure = pluginInstallFailureOf(cause);
        if (failure?.reason === "no_plugin") return;
        setFindError(
          failure
            ? installFailureText(failure, t)
            : controlErrorMessage(cause, t("plugins.market.failed")),
        );
      })
      .finally(() => {
        if (current) setFinding(false);
      });
    return () => {
      current = false;
    };
  }, [code, isReady, t]);

  const shown = found && code ? found.plugins : listed;
  const shownIndex = found && code ? found.index : (market?.index ?? index);

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3"
      data-testid="plugin-market"
    >
      <div className="flex min-h-8 min-w-0 shrink-0 flex-wrap items-center gap-x-3 gap-y-2">
        <SegmentedControl
          disabled={!isReady}
          label={t("plugins.market.source")}
          onValueChange={setSource}
          options={[
            { value: "official", label: t("plugins.market.official") },
            { value: "community", label: t("plugins.market.community") },
            ...(customIndex
              ? [
                  {
                    value: "custom" as const,
                    label:
                      customName?.index === customIndex
                        ? customName.name
                        : hostOf(customIndex),
                  },
                ]
              : []),
          ]}
          value={shownSource}
          variant="line"
        />
        <span className="flex-1" />
        {found && code ? (
          <span className="text-micro text-muted-foreground">
            {t("plugins.market.foundByCode", { code })}
          </span>
        ) : shownSource !== "custom" ? (
          <span className="inline-flex items-center gap-1 text-micro">
            <ExternalLink
              className="text-micro"
              href={newAPIPage(locale, "plugins")}
            >
              {t("plugins.market.provenance")}
            </ExternalLink>
            <HelpPopover label={t("plugins.market.provenanceHelpLabel")}>
              <div className="grid gap-2">
                <p>{t("plugins.market.provenanceHelp")}</p>
                <p>
                  {shownSource === "official"
                    ? t("plugins.market.officialHelp")
                    : t("plugins.market.communityHelp")}
                </p>
              </div>
            </HelpPopover>
          </span>
        ) : null}
        <Button
          onClick={() => setPublishing(true)}
          size="sm"
          type="button"
          variant="outline"
        >
          {t("plugins.publish.open")}
        </Button>
        <IconButton
          disabled={!isReady || loading}
          label={t("common.refresh")}
          onClick={() => void load(index)}
          size="icon-sm"
          type="button"
          variant="ghost"
        >
          <RefreshCw
            className={
              loading ? "animate-spin motion-reduce:animate-none" : undefined
            }
          />
        </IconButton>
      </div>
      {market && groups.length > 1 && !(found && code) ? (
        <div className="shrink-0 self-start">
          <SegmentedControl
            label={t("plugins.market.kind")}
            onValueChange={setGroup}
            options={[
              {
                value: "all",
                label: t("plugins.market.allKinds"),
                count: market.plugins.length,
              },
              ...groups.map((next) => ({
                value: next,
                label:
                  next === "other"
                    ? t("plugins.market.otherKind")
                    : t(`plugins.groups.${next}`),
                count: groupCounts[next],
              })),
            ]}
            value={shownGroup}
          />
        </div>
      ) : null}
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {findError ? <FormMessage tone="error">{findError}</FormMessage> : null}
      {(loading && !market) || finding ? (
        <LoadingState
          className="flex-1"
          label={
            finding
              ? t("plugins.market.finding", { code })
              : t("plugins.market.loading")
          }
        />
      ) : market && shown.length === 0 ? (
        <EmptyState
          className="flex-1"
          illustration={<Boxes className="size-6 text-muted-foreground" />}
          title={
            code
              ? t("plugins.market.noCode", { code })
              : query.trim()
                ? t("plugins.market.noMatch")
                : t("plugins.market.empty")
          }
        />
      ) : market ? (
        <div
          className="min-h-0 flex-1 overflow-y-auto overscroll-contain pb-1"
          data-tab-scroller
        >
          <Panel asChild>
            <ul aria-label={t("plugins.market.listLabel")}>
              {shown.map((entry) => (
                <li className="border-b last:border-b-0" key={entry.key}>
                  <MarketRow
                    entry={entry}
                    onInstall={onInstall}
                    onOpen={() => setOpen({ entry, index: shownIndex })}
                    onOpenInstalled={onOpenInstalled}
                  />
                </li>
              ))}
            </ul>
          </Panel>
        </div>
      ) : null}
      {publishing ? (
        <PublishDialog locale={locale} onClose={() => setPublishing(false)} />
      ) : null}
      {open ? (
        <MarketEntryDialog
          entry={open.entry}
          index={open.index}
          onClose={() => setOpen(null)}
          onOpenInstalled={
            onOpenInstalled
              ? (key) => {
                  setOpen(null);
                  onOpenInstalled(key);
                }
              : undefined
          }
          onInstall={(link) => {
            setOpen(null);
            onInstall(link);
          }}
        />
      ) : null}
    </div>
  );
}

function EntryAction({
  entry,
  onInstall,
  onOpenInstalled,
  size = "xs",
}: {
  entry: PluginMarketEntry;
  onInstall: (link: string) => void;
  onOpenInstalled?: (key: string) => void;
  size?: "xs" | "sm";
}) {
  const t = useT();
  const state = entryState(entry);
  if (state === "installed")
    return (
      <Button
        className="shrink-0"
        onClick={(event) => {
          event.stopPropagation();
          onOpenInstalled?.(entry.key);
        }}
        size={size}
        type="button"
        variant="ghost"
      >
        {t("plugins.market.open")}
        <ChevronRight />
      </Button>
    );
  if (state === "revoked")
    return (
      <StatusBadge
        className="shrink-0"
        title={revokedReason(entry, t)}
        tone="negative"
      >
        {t("plugins.market.revoked")}
      </StatusBadge>
    );
  if (state === "incompatible")
    return (
      <span
        className="shrink-0 text-xs text-muted-foreground"
        title={incompatibleReason(entry, t)}
      >
        {t("plugins.market.incompatible")}
      </span>
    );
  return (
    <Button
      className="shrink-0"
      onClick={(event) => {
        event.stopPropagation();
        onInstall(entry.link);
      }}
      size={size}
      type="button"
      variant={state === "update" ? "default" : "outline"}
    >
      {state === "update"
        ? t("plugins.market.update", { version: entry.latest })
        : t("plugins.market.install")}
    </Button>
  );
}

type EntryGroup = "providers" | "requests" | "other";

/**
 * Which group of the installed list an entry would join: a plugin that
 * serves models joins the providers, one that only processes requests the
 * request checks.
 */
function entryGroup(entry: PluginMarketEntry): EntryGroup {
  const parts = entry.parts ?? [];
  if (
    parts.includes("task") ||
    parts.includes("provider") ||
    entry.protocols.length > 0 ||
    entry.routes.length > 0
  )
    return "providers";
  if (parts.includes("transform")) return "requests";
  return "other";
}

/** What kind of plugin an entry is, from what it declares. */
function entryKind(entry: PluginMarketEntry, t: ReturnType<typeof useT>) {
  const group = entryGroup(entry);
  return group === "other" ? "" : t(`plugins.groups.${group}`);
}

function MarketRow({
  entry,
  onInstall,
  onOpen,
  onOpenInstalled,
}: {
  entry: PluginMarketEntry;
  onInstall: (link: string) => void;
  onOpen: () => void;
  onOpenInstalled?: (key: string) => void;
}) {
  const t = useT();
  const kind = entryKind(entry, t);
  const state = entryState(entry);
  const subtitle =
    state === "incompatible"
      ? incompatibleReason(entry, t)
      : [
          entry.description || t("plugins.market.noDescription"),
          entry.models.length > 0
            ? t("plugins.market.models", { count: entry.models.length })
            : "",
          entry.author
            ? t("plugins.market.by", { author: entry.author.login })
            : "",
        ]
          .filter(Boolean)
          .join(" · ");
  return (
    <div
      className="flex h-15 min-w-0 cursor-pointer items-center gap-3 px-3 transition-colors hover:bg-muted/40"
      data-testid="plugin-market-row"
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen();
        }
      }}
      role="button"
      tabIndex={0}
    >
      <PluginIcon
        className="size-7 rounded-md"
        icon={entry.icon_url}
        label={pluginAvatar({ name: entry.name, task: { icon: entry.icon } })}
      />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 items-center gap-2">
          <strong className="truncate text-sm font-medium">{entry.name}</strong>
          <span className="shrink-0 text-micro text-muted-foreground tabular-nums">
            {entry.latest}
          </span>
          {kind ? (
            <Badge className="shrink-0 font-normal" variant="secondary">
              {kind}
            </Badge>
          ) : null}
          {entry.recommended ? (
            <Badge className="shrink-0 font-normal" variant="accent">
              {t("plugins.market.recommended")}
            </Badge>
          ) : null}
          {entry.installed ? (
            <span className="inline-flex shrink-0 items-center gap-1 text-micro text-success-foreground">
              <Check className="size-3" />
              {state === "update"
                ? t("plugins.market.installedVersion", {
                    version: entry.installed,
                  })
                : t("plugins.market.installed")}
            </span>
          ) : null}
        </span>
        <span className="truncate text-xs text-text-secondary" title={subtitle}>
          {subtitle}
        </span>
      </span>
      <EntryAction
        entry={entry}
        onInstall={onInstall}
        onOpenInstalled={onOpenInstalled}
        size="sm"
      />
    </div>
  );
}

/**
 * A marketplace version's usage guide or release notes, read when the
 * dialog opens; null while it loads or when the version has none.
 */
function useMarketDoc(
  index: string,
  key: string,
  version: string | undefined,
  doc: "readme" | "changelog",
  listed: boolean,
): { doc: PluginMarketDoc | null; failed: boolean } {
  const [state, setState] = useState<{
    doc: PluginMarketDoc | null;
    failed: boolean;
  }>({ doc: null, failed: false });
  useEffect(() => {
    setState({ doc: null, failed: false });
    if (!listed || !version) return;
    let current = true;
    getPluginMarketDoc({ index, key, version, doc })
      .then((found) => {
        if (current) setState({ doc: found, failed: false });
      })
      .catch(() => {
        if (current) setState({ doc: null, failed: true });
      });
    return () => {
      current = false;
    };
  }, [doc, index, key, listed, version]);
  return state;
}

/** What a marketplace plugin serves, its usage guide, models and versions. */
function MarketEntryDialog({
  entry,
  index,
  onClose,
  onInstall,
  onOpenInstalled,
}: {
  entry: PluginMarketEntry;
  /** The marketplace the entry came from, which its documents are read from. */
  index: string;
  onClose: () => void;
  onInstall: (link: string) => void;
  onOpenInstalled?: (key: string) => void;
}) {
  const t = useT();
  const latest = entry.versions.find(
    (version) => version.version === entry.latest,
  );
  const state = entryState(entry);
  const guide = useMarketDoc(
    index,
    entry.key,
    latest?.version,
    "readme",
    latest?.readme === true,
  );
  const notes = useMarketDoc(
    index,
    entry.key,
    latest?.version,
    "changelog",
    latest?.changelog === true,
  );
  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="sm:max-w-xl" data-testid="plugin-market-entry">
        <DialogHeader className="text-left">
          <div className="flex min-w-0 items-center gap-3">
            <PluginIcon
              className="size-10 rounded-md"
              icon={entry.icon_url}
              label={pluginAvatar({
                name: entry.name,
                task: { icon: entry.icon },
              })}
            />
            <div className="grid min-w-0 gap-0.5">
              <DialogTitle className="flex min-w-0 items-center gap-2">
                <span className="truncate">{entry.name}</span>
                <span className="text-xs font-normal text-muted-foreground tabular-nums">
                  {entry.latest}
                </span>
              </DialogTitle>
              <DialogDescription className="line-clamp-3 text-xs leading-5">
                {entry.description || t("plugins.market.noDescription")}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>
        <div className="grid max-h-[60vh] min-h-0 gap-4 overflow-y-auto overscroll-contain text-sm">
          {entry.revoked && entry.installed ? (
            <FormMessage tone="error">{revokedReason(entry, t)}</FormMessage>
          ) : null}
          {entry.author || entry.code || entry.installs_30d !== undefined ? (
            <dl
              className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs"
              data-testid="plugin-market-facts"
            >
              {entry.author ? (
                <>
                  <dt className="text-muted-foreground">
                    {t("plugins.market.author")}
                  </dt>
                  <dd className="flex min-w-0 flex-wrap gap-x-3">
                    {entry.author.url ? (
                      <ExternalLink href={entry.author.url}>
                        {entry.author.login}
                      </ExternalLink>
                    ) : (
                      <span>{entry.author.login}</span>
                    )}
                    {entry.repository ? (
                      <ExternalLink href={entry.repository}>
                        {t("plugins.market.repository")}
                      </ExternalLink>
                    ) : null}
                  </dd>
                </>
              ) : null}
              {entry.code ? (
                <>
                  <dt className="text-muted-foreground">
                    {t("plugins.market.code")}
                  </dt>
                  <dd className="font-mono tabular-nums">{entry.code}</dd>
                </>
              ) : null}
              {entry.installs_30d !== undefined ? (
                <>
                  <dt className="text-muted-foreground">
                    {t("plugins.market.installs")}
                  </dt>
                  <dd className="tabular-nums">
                    {t("plugins.market.installsCount", {
                      count: entry.installs_30d,
                    })}
                  </dd>
                </>
              ) : null}
            </dl>
          ) : null}
          {latest?.readme ? (
            <section className="grid gap-1.5" data-testid="plugin-market-guide">
              <h3 className="flex items-center justify-between gap-2 text-xs font-medium text-muted-foreground">
                {t("plugins.market.guide")}
                {guide.doc?.source_url ? (
                  <ExternalLink
                    className="font-normal"
                    href={guide.doc.source_url}
                  >
                    {t("plugins.market.guideSource")}
                  </ExternalLink>
                ) : null}
              </h3>
              {guide.doc ? (
                <MarkdownContent
                  className="rounded-md border bg-muted/20 px-3 py-2 text-xs"
                  content={guide.doc.markdown}
                />
              ) : guide.failed ? (
                <FormMessage>{t("plugins.market.guideFailed")}</FormMessage>
              ) : (
                <p className="text-xs text-muted-foreground">
                  {t("plugins.market.guideLoading")}
                </p>
              )}
            </section>
          ) : null}
          {entryGroup(entry) === "providers" ? (
            <section className="grid gap-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">
                {t("plugins.task.endpoints")}
              </h3>
              <TaskEndpoints
                protocols={entry.protocols}
                routes={entry.routes}
              />
            </section>
          ) : null}
          {latest?.base_url || latest?.auth ? (
            <section className="grid gap-1 text-xs">
              <h3 className="text-xs font-medium text-muted-foreground">
                {t("plugins.task.connection")}
              </h3>
              <dl className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-1">
                {latest.base_url ? (
                  <>
                    <dt className="text-muted-foreground">
                      {t("plugins.task.baseUrl")}
                    </dt>
                    <dd className="truncate font-mono">{latest.base_url}</dd>
                  </>
                ) : null}
                {latest.auth ? (
                  <>
                    <dt className="text-muted-foreground">
                      {t("plugins.task.authLabel")}
                    </dt>
                    <dd>{taskAuthLabel(latest.auth, t)}</dd>
                  </>
                ) : null}
                {latest.allowed_hosts?.length ? (
                  <>
                    <dt className="text-muted-foreground">
                      {t("plugins.task.allowedHosts")}
                    </dt>
                    <dd className="font-mono">
                      {latest.allowed_hosts.join(", ")}
                    </dd>
                  </>
                ) : null}
              </dl>
            </section>
          ) : null}
          {entry.models.length > 0 ? (
            <section className="grid gap-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">
                {t("plugins.market.models", { count: entry.models.length })}
              </h3>
              <ModelChips models={entry.models} />
            </section>
          ) : null}
          {notes.doc ? (
            <HelpDisclosure title={t("plugins.market.notes")}>
              <MarkdownContent
                className="text-xs text-foreground"
                content={notes.doc.markdown}
              />
            </HelpDisclosure>
          ) : null}
          {entry.versions.length > 1 ? (
            <HelpDisclosure title={t("plugins.market.versions")}>
              <ul className="grid gap-0.5" data-testid="plugin-market-versions">
                {entry.versions.map((version) => (
                  <li
                    className="flex items-center justify-between gap-2 py-0.5"
                    key={version.version}
                  >
                    <span className="tabular-nums text-foreground">
                      {version.version}
                      {version.version === entry.installed ? (
                        <span className="ml-2 text-muted-foreground">
                          {t("plugins.market.installed")}
                        </span>
                      ) : null}
                    </span>
                    {version.version !== entry.installed &&
                    state !== "incompatible" ? (
                      <Button
                        onClick={() => onInstall(version.link)}
                        size="xs"
                        type="button"
                        variant="ghost"
                      >
                        {t("plugins.market.installVersion")}
                      </Button>
                    ) : null}
                  </li>
                ))}
              </ul>
            </HelpDisclosure>
          ) : null}
        </div>
        <DialogFooter>
          {entry.website ? (
            <ExternalLink
              className="mr-auto self-center text-xs"
              href={entry.website}
            >
              {t("plugins.market.website")}
            </ExternalLink>
          ) : null}
          <Button onClick={onClose} size="sm" type="button" variant="outline">
            {t("common.close")}
          </Button>
          {state === "incompatible" ? (
            <StatusBadge
              className="self-center"
              title={incompatibleReason(entry, t)}
              tone="neutral"
            >
              {t("plugins.market.incompatible")}
            </StatusBadge>
          ) : state === "install" || state === "update" ? (
            <Button
              onClick={() => onInstall(entry.link)}
              size="sm"
              type="button"
            >
              {state === "update"
                ? t("plugins.market.update", { version: entry.latest })
                : t("plugins.market.install")}
            </Button>
          ) : entry.installed && onOpenInstalled ? (
            <Button
              onClick={() => onOpenInstalled(entry.key)}
              size="sm"
              type="button"
            >
              {t("plugins.market.openPlugin")}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * How a plugin reaches other people. Each way takes its own format: New
 * API's community marketplace takes New API plugins, which an upload there
 * turns into a plugin code; a GitHub link carries a plugin written with
 * astrlink plugin, which only AstrLink runs. Each row hands an AI the
 * prompt for its format.
 */
function PublishDialog({
  locale,
  onClose,
}: {
  locale: string;
  onClose: () => void;
}) {
  const t = useT();
  const publish = newAPIPage(locale, "community");
  const feedback = useCopyFeedback();
  const failed = feedback.state === "failed";
  const copyButton = (text: string) => {
    const copied =
      feedback.activeKey === "prompt" && feedback.state === "copied";
    return (
      <Button
        onClick={() => feedback.copy("prompt", text)}
        size="sm"
        type="button"
        variant="outline"
      >
        {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
        {t("plugins.publish.copyPrompt")}
      </Button>
    );
  };
  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent
        className="flex max-h-[85vh] flex-col overflow-hidden sm:max-w-xl"
        data-testid="plugin-publish-dialog"
      >
        <DialogHeader className="text-left">
          <DialogTitle>{t("plugins.publish.title")}</DialogTitle>
        </DialogHeader>
        <div className="grid min-h-0 gap-3 overflow-y-auto overscroll-contain">
          <div className="flex">
            {copyButton(
              t("plugins.publish.prompt", {
                astrlinkTemplates: PLUGIN_TEMPLATES,
                docs: NEW_API_PLUGIN_DOCS,
              }),
            )}
          </div>
          <Panel asChild>
            <ul>
              <li
                className="grid gap-2 border-b px-3 py-2.5"
                data-testid="plugin-publish-community"
              >
                <div className="grid gap-0.5">
                  <span className="text-sm font-medium">
                    {t("plugins.publish.community.title")}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {t("plugins.publish.community.body")}
                  </span>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Button asChild size="sm" variant="outline">
                    <a
                      href={publish}
                      onClick={(event) => openInSystemBrowser(event, publish)}
                      rel="noopener noreferrer"
                      target="_blank"
                    >
                      {t("plugins.publish.community.action")}
                      <ArrowUpRight />
                    </a>
                  </Button>
                  <ExternalLink className="text-xs" href={PLUGIN_API_DOCS}>
                    {t("plugins.publish.docs")}
                  </ExternalLink>
                </div>
              </li>
              <li
                className="grid gap-2 px-3 py-2.5"
                data-testid="plugin-publish-github"
              >
                <div className="grid gap-0.5">
                  <span className="text-sm font-medium">
                    {t("plugins.publish.github.title")}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {t("plugins.publish.github.body")}
                  </span>
                </div>
                <dl className="grid gap-1.5 text-xs">
                  {PUBLISH_COMMANDS.map((entry) => (
                    <div
                      className="grid gap-x-3 gap-y-0.5 sm:grid-cols-[13rem_1fr] sm:items-baseline"
                      key={entry.key}
                    >
                      <dt>
                        <code className="font-mono">{entry.command}</code>
                      </dt>
                      <dd className="text-muted-foreground">
                        {t(`plugins.developHome.command.${entry.key}`)}
                      </dd>
                    </div>
                  ))}
                </dl>
              </li>
            </ul>
          </Panel>
          <p aria-live="polite" className="text-xs empty:hidden">
            {feedback.activeKey === null
              ? ""
              : failed
                ? t("copy.manualSelect")
                : t("plugins.publish.copied")}
          </p>
        </div>
      </DialogContent>
    </Dialog>
  );
}
