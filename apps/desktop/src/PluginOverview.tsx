import { useState, type CSSProperties } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ExternalLink } from "@/components/ExternalLink";
import { FormMessage } from "@/components/FormMessage";
import { GitHubRepoBadge } from "@/components/GitHubRepoBadge";
import { HelpPopover } from "@/components/HelpPopover";
import { LoadingState } from "@/components/LoadingState";
import { PluginView } from "@/components/PluginView";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatAgo } from "@/lib/format-ago";
import {
  formatCompactNumber,
  formatExactNumber,
} from "./format-compact-number";
import { useT } from "./i18n";
import { EffectCard } from "./PluginEffects";
import { formatSize } from "./PluginInstallReview";
import {
  plaintextOrigin,
  settingValue,
  type PluginBindings,
  type PluginClearTarget,
  type PluginCredit,
  type PluginCreditStars,
  type PluginDetail,
  type PluginHostStorage,
  type PluginStats,
  type PluginStatus,
  type PluginViewBlock,
} from "./plugin-model";

type Translate = ReturnType<typeof useT>;

const testTones: Record<string, StatusTone> = {
  ok: "positive",
  error: "negative",
  failed: "negative",
};

function displayAddress(url: string): string {
  return url.replace(/^https?:\/\//, "").replace(/\/$/, "");
}

function CardTitle({ children }: { children: string }) {
  return (
    <h3 className="text-xs font-medium text-muted-foreground">{children}</h3>
  );
}

/**
 * What the last test and the plugin's own status say, as one view: their
 * rows in order, a row of the status replacing the test's of the same label,
 * since the status is read anew; their other blocks after.
 */
function statusView(...views: Array<PluginViewBlock[] | undefined>) {
  const items: Array<{ label: string; value: string; mono?: boolean }> = [];
  const others: PluginViewBlock[] = [];
  for (const block of views.flatMap((view) => view ?? [])) {
    if (block.type !== "kv") {
      others.push(block);
      continue;
    }
    for (const item of block.items) {
      const at = items.findIndex((kept) => kept.label === item.label);
      if (at >= 0) items[at] = item;
      else items.push(item);
    }
  }
  const merged: PluginViewBlock[] =
    items.length > 0 ? [{ type: "kv", items }] : [];
  return [...merged, ...others];
}

/**
 * The plugin page's overview: the plugin joined to AstrLink and what its
 * server says, how it ran lately, the values sessions are bound to, what is
 * kept for it and the projects it is built on.
 */
export function PluginOverview({
  detail,
  status,
  statusError,
  busy = false,
  onClear,
  stats = null,
  stars = null,
  linkPlay = 0,
  now = new Date(),
}: {
  detail: PluginDetail;
  stats?: PluginStats | null;
  status: PluginStatus | null;
  statusError: string | null;
  busy?: boolean;
  onClear?: (what: PluginClearTarget) => Promise<boolean>;
  /** The GitHub stars of the projects the plugin credits. */
  stars?: PluginCreditStars | null;
  /** Each new value plays the link joining, as when the plugin is enabled. */
  linkPlay?: number;
  now?: Date;
}) {
  return (
    <div
      className="@container/overview grid gap-5"
      data-testid="plugin-overview"
    >
      {statusError ? (
        <FormMessage tone="error">{statusError}</FormMessage>
      ) : null}
      <ConnectionCard
        detail={detail}
        linkPlay={linkPlay}
        now={now}
        status={status}
        statusError={statusError}
      />
      {stats ? <EffectCard detail={detail} stats={stats} /> : null}
      {status?.bindings?.map((bindings) => (
        <BindingsCard bindings={bindings} key={bindings.key} now={now} />
      ))}
      {detail.host_storage.length > 0 ? (
        <StorageCard busy={busy} detail={detail} onClear={onClear} />
      ) : null}
      {detail.credits.length > 0 ? (
        <CreditsLine credits={detail.credits} stars={stars} />
      ) : null}
    </div>
  );
}

/** How the link stands, and its badge. */
function linkState(
  detail: PluginDetail,
  status: PluginStatus | null,
  t: Translate,
): {
  state: "off" | "connected" | "idle" | "failed";
  tone: StatusTone;
  text: string;
} {
  if (!detail.enabled)
    return {
      state: "off",
      tone: "neutral",
      text: t("plugins.state.disabled"),
    };
  if (!detail.hooks.includes("testConnection"))
    return {
      state: "connected",
      tone: "positive",
      text: t("plugins.state.running"),
    };
  const test = status?.last_test;
  if (!test)
    return {
      state: "idle",
      tone: "neutral",
      text: t("plugins.overview.notTested"),
    };
  return {
    state: test.status === "ok" ? "connected" : "failed",
    tone: testTones[test.status] ?? "neutral",
    text: t(`plugins.testStatus.${test.status}`),
  };
}

/**
 * The plugin joined to AstrLink, with its server under it and the last test
 * on the link, then what the test and the plugin's own status say, two
 * columns wide where there is room.
 */
function ConnectionCard({
  detail,
  status,
  statusError,
  now,
  linkPlay,
}: {
  detail: PluginDetail;
  status: PluginStatus | null;
  statusError: string | null;
  now: Date;
  linkPlay: number;
}) {
  const t = useT();
  const server = detail.settings.find((setting) => setting.type === "server");
  const describes = detail.hooks.includes("describeStatus");
  const address = server ? settingValue(detail.config, server.key) : undefined;
  const view = statusView(
    status?.last_test?.view,
    describes ? status?.view : undefined,
  );
  const link = linkState(detail, status, t);
  const test = status?.last_test;
  return (
    <section className="grid min-w-0 gap-3" data-testid="plugin-connection">
      <div
        className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 rounded-md border px-3 py-2.5 text-xs"
        data-state={link.state}
        data-testid="plugin-link"
        key={linkPlay}
      >
        <StatusBadge tone={link.tone}>{link.text}</StatusBadge>
        {server ? (
          typeof address === "string" && address !== "" ? (
            <span className="inline-flex min-w-0 items-center gap-1.5">
              <span className="truncate font-mono">
                {displayAddress(address)}
              </span>
              {plaintextOrigin(address) ? (
                <span className="text-warning-foreground">
                  {t("plugins.network.plaintext")}
                </span>
              ) : null}
            </span>
          ) : (
            <span className="text-muted-foreground">
              {t("plugins.overview.noServer")}
            </span>
          )
        ) : null}
        {test ? (
          <span className="ml-auto min-w-0 truncate text-muted-foreground tabular-nums">
            {`${test.summary} · ${formatAgo(new Date(test.tested_at), now)}`}
          </span>
        ) : null}
      </div>
      {view.length > 0 ||
      (describes && (status === null || status.view_error)) ? (
        <div className="grid gap-2 text-xs">
          {describes && status?.view_error ? (
            <p
              className="flex min-w-0 items-center gap-1 text-warning-foreground"
              data-testid="plugin-status-error"
            >
              {t("plugins.overview.statusUnread")}
              <HelpPopover label={t("plugins.overview.statusErrorLabel")}>
                <p className="break-words">{status.view_error}</p>
              </HelpPopover>
            </p>
          ) : null}
          {view.length > 0 ? (
            <div data-testid="plugin-own-status">
              <PluginView
                blocks={view}
                kvClassName="grid-cols-[7rem_minmax(0,1fr)] gap-x-4 gap-y-2 @[640px]/overview:grid-cols-[7rem_minmax(0,1fr)_7rem_minmax(0,1fr)]"
                truncated={
                  !!status?.truncated || !!status?.last_test?.truncated
                }
                truncatedLabel={t("plugins.truncated")}
              />
            </div>
          ) : null}
          {describes && status === null && !statusError ? (
            <LoadingState label={t("plugins.loading")} />
          ) : null}
        </div>
      ) : null}
      {describes &&
      status !== null &&
      !(status.view && status.view.length > 0) &&
      !status.view_error &&
      !detail.granted_capabilities.includes("ui.views") ? (
        <p className="text-xs text-muted-foreground">
          {t("plugins.overviewNeedsViews")}
        </p>
      ) : null}
    </section>
  );
}

/** What the host keeps for a plugin, and what can be cleared of it. */
const clearable: Partial<Record<PluginHostStorage["kind"], PluginClearTarget>> =
  {
    workspace_bindings: "workspaces",
    plugin_values: "values",
    lookup_credentials: "lookups",
    plugin_files: "files",
  };

/** What holds memory text of what the host keeps. */
const holdsMemory: Partial<Record<PluginHostStorage["kind"], boolean>> = {
  resident_block: true,
};

type Place = "database" | "disk" | "memory" | "records";

/** Where each thing is kept, which the card groups them by. */
const places: Record<PluginHostStorage["kind"], Place> = {
  session_bindings: "database",
  workspace_bindings: "database",
  plugin_sessions: "database",
  plugin_values: "database",
  resident_block: "memory",
  lookup_credentials: "memory",
  request_records: "records",
  plugin_files: "disk",
};

const placeOrder: Place[] = ["database", "disk", "memory", "records"];

/**
 * What is kept for the plugin, a column for each place it is kept in, with
 * how much and clearing what can be cleared. What each one is and how long
 * it stays is in the one ? of the card.
 */
function StorageCard({
  detail,
  busy,
  onClear,
}: {
  detail: PluginDetail;
  busy: boolean;
  onClear?: (what: PluginClearTarget) => Promise<boolean>;
}) {
  const t = useT();
  const [clearing, setClearing] = useState<PluginClearTarget | null>(null);
  // Credentials are voided, not cleared: each session gets a new one.
  const actionLabel = (what: PluginClearTarget) =>
    what === "lookups" ? t("plugins.stored.revoke") : t("plugins.stored.clear");
  const groups = placeOrder
    .map((place) => ({
      place,
      kept: detail.host_storage.filter((kept) => places[kept.kind] === place),
    }))
    .filter((group) => group.kept.length > 0);
  return (
    <section className="grid gap-2" data-testid="plugin-stored">
      <div className="flex min-h-7 items-center gap-1">
        <CardTitle>{t("plugins.stored.title")}</CardTitle>
        <HelpPopover label={t("plugins.stored.helpLabel")} size="wide">
          <StorageHelp detail={detail} />
        </HelpPopover>
      </div>
      <div
        className="grid gap-px overflow-hidden rounded-md border bg-border @[640px]/overview:[grid-template-columns:repeat(var(--places),minmax(0,1fr))]"
        style={{ "--places": groups.length } as CSSProperties}
      >
        {groups.map(({ place, kept: items }) => (
          <section
            className="bg-card p-3"
            data-testid="plugin-stored-place"
            key={place}
          >
            <h4 className="mb-2 text-micro font-medium text-muted-foreground">
              {t(`plugins.stored.places.${place}`)}
            </h4>
            <ul className="grid gap-1.5 text-xs">
              {items.map((kept) => {
                const what = clearable[kept.kind];
                return (
                  <li
                    className="flex min-h-6 min-w-0 items-center justify-between gap-2"
                    data-testid="plugin-stored-item"
                    key={kept.kind}
                  >
                    <span className="flex min-w-0 items-center gap-1.5">
                      <span className="truncate">
                        {t(`plugins.stored.kinds.${kept.kind}.label`)}
                      </span>
                      {holdsMemory[kept.kind] ? (
                        <StatusBadge className="shrink-0" tone="pending">
                          {t("plugins.stored.holds.memory")}
                        </StatusBadge>
                      ) : null}
                    </span>
                    <span className="flex shrink-0 items-center gap-2">
                      {kept.count !== undefined ? (
                        <span className="text-muted-foreground tabular-nums">
                          {kept.bytes !== undefined
                            ? t("plugins.stored.files", {
                                count: kept.count,
                                size: formatSize(kept.bytes),
                              })
                            : t("plugins.stored.count", { count: kept.count })}
                        </span>
                      ) : null}
                      {what && onClear ? (
                        <Button
                          className="h-6 px-2 text-xs"
                          disabled={busy || kept.count === 0}
                          onClick={() => setClearing(what)}
                          size="sm"
                          type="button"
                          variant="outline"
                        >
                          {actionLabel(what)}
                        </Button>
                      ) : null}
                    </span>
                  </li>
                );
              })}
            </ul>
          </section>
        ))}
      </div>
      <ConfirmDialog
        confirmLabel={clearing ? actionLabel(clearing) : ""}
        description={
          <p>
            {clearing ? t(`plugins.stored.clearConfirm.${clearing}`) : null}
          </p>
        }
        destructive
        disabled={busy}
        onCancel={() => setClearing(null)}
        onConfirm={() => {
          const what = clearing;
          setClearing(null);
          if (what && onClear) void onClear(what);
        }}
        open={clearing !== null}
        title={clearing ? t(`plugins.stored.clearTitle.${clearing}`) : ""}
      />
    </section>
  );
}

/** What each kept thing is and how long it stays, behind the card's ?. */
function StorageHelp({ detail }: { detail: PluginDetail }) {
  const t = useT();
  return (
    <div className="grid gap-3" data-testid="plugin-stored-help">
      <p>{t("plugins.stored.help")}</p>
      {placeOrder.map((place) => {
        const items = detail.host_storage.filter(
          (kept) => places[kept.kind] === place,
        );
        if (items.length === 0) return null;
        return (
          <section className="grid gap-1.5" key={place}>
            <h4 className="text-xs font-medium text-muted-foreground">
              {t(`plugins.stored.places.${place}`)}
            </h4>
            <dl className="grid gap-1.5">
              {items.map((kept) => (
                <div key={kept.kind}>
                  <dt className="font-medium">
                    {t(`plugins.stored.kinds.${kept.kind}.label`)}
                  </dt>
                  <dd className="text-muted-foreground">
                    {t(`plugins.stored.kinds.${kept.kind}.what`)}
                    {t("plugins.stored.retention", {
                      retention: t(
                        `plugins.stored.kinds.${kept.kind}.retention`,
                      ),
                    })}
                  </dd>
                  {kept.kind === "plugin_values" &&
                  detail.storage.length > 0 ? (
                    <dd>
                      <ul className="mt-1 grid gap-0.5 text-muted-foreground">
                        {detail.storage.map((item) => (
                          <li key={item.label}>
                            {t("plugins.stored.declared", {
                              label: item.label,
                              retention: item.retention,
                            })}
                            {item.content
                              ? ` · ${t("plugins.stored.holds.conversation")}`
                              : ""}
                          </li>
                        ))}
                      </ul>
                    </dd>
                  ) : null}
                </div>
              ))}
            </dl>
          </section>
        );
      })}
    </div>
  );
}

function BindingsCard({
  bindings,
  now,
}: {
  bindings: PluginBindings;
  now: Date;
}) {
  const t = useT();
  const bound = bindings.values.filter((entry) => entry.value !== "none");
  return (
    <section className="grid gap-2" data-testid="plugin-bindings">
      <div className="flex min-h-7 items-center gap-2">
        <CardTitle>
          {t("plugins.overview.bindings", { label: bindings.label })}
        </CardTitle>
        <Badge className="px-1.5 py-0 tabular-nums" variant="secondary">
          {bound.length}
        </Badge>
      </div>
      {bindings.values.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {t("plugins.overview.noBindings", { label: bindings.label })}
        </p>
      ) : (
        <div className="overflow-hidden rounded-md border">
          <Table className="text-xs">
            <TableHeader>
              <TableRow>
                <TableHead className="h-8 px-3 text-xs">
                  {bindings.label}
                </TableHead>
                <TableHead className="h-8 w-20 px-3 text-right text-xs">
                  {t("plugins.overview.sessions")}
                </TableHead>
                <TableHead className="h-8 w-32 px-3 text-xs">
                  {t("plugins.overview.lastUsed")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {bindings.values.map((entry) => (
                <TableRow key={entry.value}>
                  <TableCell className="px-3 py-1.5">
                    {entry.value === "none" ? (
                      <span className="text-muted-foreground">
                        {t("plugins.overview.none")}
                      </span>
                    ) : (
                      <span className="font-mono">{entry.value}</span>
                    )}
                  </TableCell>
                  <TableCell className="px-3 py-1.5 text-right tabular-nums">
                    {entry.sessions}
                  </TableCell>
                  <TableCell className="px-3 py-1.5 text-muted-foreground">
                    {entry.last_used_at
                      ? formatAgo(new Date(entry.last_used_at), now)
                      : "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  );
}

/** github.com/owner/name of a repository address, null for any other. */
function githubRepository(url: string): string | null {
  const match =
    /^https:\/\/(?:www\.)?github\.com\/([^/]+)\/([^/]+?)(?:\.git)?\/?$/.exec(
      url,
    );
  return match ? `${match[1]}/${match[2]}` : null;
}

/**
 * The projects the plugin is built on, one line under the overview: its name,
 * and for a GitHub repository its stars beside GitHub's mark, which opens it.
 */
function CreditsLine({
  credits,
  stars,
}: {
  credits: PluginCredit[];
  stars: PluginCreditStars | null;
}) {
  const t = useT();
  return (
    <div
      className="grid gap-1.5 border-t pt-3 text-xs text-muted-foreground"
      data-testid="plugin-credits"
    >
      {credits.map((credit) => {
        const repository = githubRepository(credit.url);
        const count = stars?.[credit.url];
        return (
          <div
            className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1"
            data-testid="plugin-credit"
            key={credit.url}
          >
            <span className="min-w-0">
              {t("plugins.overview.uses", { name: credit.name })}
            </span>
            {repository ? (
              <GitHubRepoBadge
                href={credit.url}
                label={
                  count === undefined
                    ? t("plugins.overview.onGitHub", { name: credit.name })
                    : t("plugins.overview.onGitHubStars", {
                        name: credit.name,
                        count,
                        stars: formatExactNumber(count),
                      })
                }
                stars={count}
                starsText={
                  count === undefined ? undefined : formatCompactNumber(count)
                }
                title={[repository, credit.license].filter(Boolean).join(" · ")}
              />
            ) : (
              <ExternalLink className="text-xs" href={credit.url}>
                {displayAddress(credit.url)}
              </ExternalLink>
            )}
          </div>
        );
      })}
    </div>
  );
}
