import type { ReactNode } from "react";

import { GitHubRepoBadge } from "@/components/GitHubRepoBadge";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { PluginIcon, pluginAvatar } from "@/components/PluginIcon";
import { StatusBadge } from "@/components/StatusBadge";
import { cn } from "@/lib/utils";

import { i18n, useT } from "./i18n";
import { capabilityLabel } from "./plugin-labels";
import { ModelChips, TaskEndpoints } from "./PluginTaskParts";
import { taskAuthLabel } from "./plugin-task-labels";
import {
  HIGH_RISK_CAPABILITIES,
  type PluginInstallFailure,
  type PluginInstallReview,
  type PluginInstallSource,
} from "./plugin-model";

type Translate = ReturnType<typeof useT>;

/** A size in KB or MB. */
export function formatSize(bytes: number): string {
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** A moment as a date in the interface language. */
export function formatDay(iso: string | undefined): string {
  if (!iso) return "";
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  return new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium" }).format(
    at,
  );
}

/**
 * What a plugin installed from source follows, in words: the tag or the
 * branch and when its commit was made. A commit reads by its date alone.
 */
export function sourceRef(source: PluginInstallSource, t: Translate): string {
  const day = formatDay(source.committed_at);
  const ref =
    source.ref_kind === "commit"
      ? t("plugins.install.refCommit")
      : t(`plugins.install.ref.${source.ref_kind}`, { ref: source.ref ?? "" });
  return day ? t("plugins.install.refAt", { ref, day }) : ref;
}

/** Why an install could not go on, in the user's words. */
export function installFailureText(
  failure: PluginInstallFailure,
  t: Translate,
): string {
  return t(`plugins.install.failures.${failure.reason}`, {
    defaultValue: t("plugins.install.failures.unknown", {
      reason: failure.reason,
    }),
    detail: failure.detail ?? "",
  });
}

/** Where the plugin comes from: its repository and directory, or its marketplace. */
function SourceLine({ source }: { source: PluginInstallSource }) {
  const t = useT();
  if (source.kind === "market")
    return (
      <div className="grid gap-0.5" data-testid="plugin-install-source">
        <span className="min-w-0 font-medium break-all">
          {source.repository || t("plugins.source.market")}
          {source.key ? ` / ${source.key}` : ""}
        </span>
        <span className="text-xs text-muted-foreground">
          {t("plugins.install.marketRef", { version: source.ref ?? "" })}
        </span>
      </div>
    );
  const repository = `https://github.com/${source.repository}`;
  return (
    <div className="grid gap-1" data-testid="plugin-install-source">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <GitHubRepoBadge
          href={repository}
          label={t("plugins.install.openRepository", {
            repository: source.repository,
          })}
        />
        <span className="min-w-0 font-medium break-all">
          {source.repository}
          {source.subdir ? ` / ${source.subdir}` : ""}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">{sourceRef(source, t)}</p>
    </div>
  );
}

/**
 * A group of a review: a quiet heading, then its lines; the risks sit on a
 * light red wash. The confirmation window groups what it asks the same way.
 */
export function ReviewGroup({
  title,
  children,
  tone = "default",
  testId,
}: {
  title: string;
  children: ReactNode;
  tone?: "default" | "risk";
  testId?: string;
}) {
  return (
    <section
      className={cn(
        "grid gap-2",
        tone === "risk" &&
          "rounded-md border border-danger/20 bg-danger-wash px-3 py-2.5 text-danger-foreground",
      )}
      data-testid={testId}
    >
      <h3
        className={cn(
          "text-xs font-medium",
          tone === "risk" ? "text-danger-foreground" : "text-muted-foreground",
        )}
      >
        {title}
      </h3>
      {children}
    </section>
  );
}

/** The hosts a task plugin's provider key may go to: its address's, and those it declares. */
function keyHosts(review: PluginInstallReview): string[] {
  const hosts: string[] = [];
  const base = review.task?.base_url;
  if (base) {
    try {
      hosts.push(new URL(base).host);
    } catch {
      // An address that does not parse names no host.
    }
  }
  for (const host of review.task?.allowed_hosts ?? [])
    if (!hosts.includes(host)) hosts.push(host);
  return hosts;
}

/**
 * What the user reviews before code goes in place, in four groups: where it
 * comes from, what it can do, what it is built from, and what is risky.
 * An update or a rollback says what it changes first; a task plugin shows
 * the models and endpoints it serves and the hosts its key goes to.
 */
export function PluginInstallReviewView({
  review,
  rollback = false,
}: {
  review: PluginInstallReview;
  rollback?: boolean;
}) {
  const t = useT();
  const replaces = review.replaces;
  const update =
    replaces?.origin === "source" &&
    replaces.repository === review.source.repository;
  const task = review.task;
  const risks: string[] = [];
  if (replaces && !update && !rollback)
    risks.push(
      t(`plugins.install.replaces.${replaces.origin}`, {
        name: replaces.name,
        repository: replaces.repository ?? "",
      }),
    );
  if (review.client) risks.push(t("plugins.install.client"));
  for (const capability of review.capabilities)
    if (HIGH_RISK_CAPABILITIES.includes(capability))
      risks.push(
        t("plugins.install.highRisk", {
          capability: capabilityLabel(capability, t),
        }),
      );
  const changes = review.changes;
  const changeLines: { text: string; risky?: boolean }[] = [];
  if (changes && (update || rollback)) {
    for (const capability of changes.added_capabilities ?? [])
      changeLines.push({
        text: t("plugins.install.changes.addedCapability", {
          capability: capabilityLabel(capability, t),
        }),
        risky: HIGH_RISK_CAPABILITIES.includes(capability),
      });
    for (const capability of changes.removed_capabilities ?? [])
      changeLines.push({
        text: t("plugins.install.changes.removedCapability", {
          capability: capabilityLabel(capability, t),
        }),
      });
    if (changes.client)
      changeLines.push({
        text: t(`plugins.install.changes.client.${changes.client}`),
        risky: changes.client === "added",
      });
    if (changes.network_purpose_changed)
      changeLines.push({ text: t("plugins.install.changes.network") });
    const dependencies =
      (changes.added_dependencies?.length ?? 0) +
      (changes.changed_dependencies?.length ?? 0) +
      (changes.removed_dependencies?.length ?? 0);
    if (dependencies > 0)
      changeLines.push({
        text: t("plugins.install.changes.dependencies", {
          count: dependencies,
        }),
      });
  }
  const scripts = (review.warnings ?? []).filter(
    (warning) => warning.code === "install_scripts_skipped",
  );
  const otherWarnings = (review.warnings ?? []).filter(
    (warning) => warning.code !== "install_scripts_skipped",
  );
  const hosts = keyHosts(review);
  return (
    <div
      className="grid gap-4 text-sm text-foreground"
      data-testid="plugin-install-review"
    >
      <div className="flex min-w-0 items-start gap-3">
        <PluginIcon
          className="size-9 rounded-md"
          icon={review.icon}
          label={pluginAvatar({ name: review.name, task })}
        />
        <div className="grid min-w-0 gap-0.5">
          <span className="flex min-w-0 flex-wrap items-center gap-x-2">
            <strong className="truncate font-semibold">{review.name}</strong>
            <span className="text-xs text-muted-foreground tabular-nums">
              {review.version}
            </span>
            {task ? (
              <StatusBadge tone="neutral">
                {t("plugins.install.taskKind")}
              </StatusBadge>
            ) : review.kind === "provider" ? (
              <StatusBadge tone="neutral">
                {t("plugins.install.providerKind")}
              </StatusBadge>
            ) : review.kind === "request" ? (
              <StatusBadge tone="neutral">
                {t("plugins.install.requestKind")}
              </StatusBadge>
            ) : null}
          </span>
          {review.description ? (
            <p className="text-xs leading-5 text-text-secondary">
              {review.description}
            </p>
          ) : null}
          {review.author ? (
            <p className="text-micro text-muted-foreground">
              {t("plugins.install.author", { author: review.author })}
            </p>
          ) : null}
        </div>
      </div>
      {update || rollback ? (
        <p className="text-xs">
          {t(
            rollback
              ? "plugins.install.rollbackFrom"
              : "plugins.install.updateFrom",
            {
              from: changes?.from_version || replaces?.version || "",
              to: review.version,
            },
          )}
        </p>
      ) : null}
      {changeLines.length > 0 ? (
        <ReviewGroup title={t("plugins.install.changesTitle")}>
          <ul className="grid gap-0.5 text-xs">
            {changeLines.map((line) => (
              <li
                className={cn(line.risky && "text-danger-foreground")}
                key={line.text}
              >
                {line.text}
              </li>
            ))}
          </ul>
        </ReviewGroup>
      ) : null}
      <ReviewGroup title={t("plugins.install.groups.source")}>
        <SourceLine source={review.source} />
      </ReviewGroup>
      <ReviewGroup title={t("plugins.install.groups.abilities")}>
        {task ? (
          <div className="grid gap-3 text-xs" data-testid="plugin-install-task">
            <TaskEndpoints protocols={task.protocols} routes={task.routes} />
            <div className="grid gap-1.5">
              <span className="text-muted-foreground">
                {t("plugins.market.models", { count: task.models.length })}
              </span>
              <ModelChips models={task.models} />
            </div>
            <dl className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-1">
              <dt className="text-muted-foreground">
                {t("plugins.task.authLabel")}
              </dt>
              <dd>{taskAuthLabel(task.auth, t)}</dd>
              <dt className="text-muted-foreground">
                {t("plugins.task.baseUrl")}
              </dt>
              <dd className="font-mono break-all">
                {task.base_url || t("plugins.task.noBaseUrl")}
              </dd>
              {hosts.length > 0 ? (
                <>
                  <dt className="text-muted-foreground">
                    {t("plugins.install.keyHosts")}
                  </dt>
                  <dd className="font-mono break-all">{hosts.join(", ")}</dd>
                </>
              ) : null}
            </dl>
          </div>
        ) : (
          <>
            {review.capabilities.length === 0 ? (
              <p className="text-xs">{t("plugins.noCapabilities")}</p>
            ) : (
              <div className="flex flex-wrap gap-1.5">
                {review.capabilities.map((capability) => (
                  <StatusBadge
                    key={capability}
                    tone={
                      HIGH_RISK_CAPABILITIES.includes(capability)
                        ? "negative"
                        : "neutral"
                    }
                  >
                    {capabilityLabel(capability, t)}
                  </StatusBadge>
                ))}
              </div>
            )}
            {review.network_purpose ? (
              <dl className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 text-xs">
                <dt className="text-muted-foreground">
                  {t("plugins.about.network")}
                </dt>
                <dd>{review.network_purpose}</dd>
              </dl>
            ) : null}
          </>
        )}
      </ReviewGroup>
      <ReviewGroup title={t("plugins.install.groups.dependencies")}>
        <p className="text-xs">
          {[
            t("plugins.install.code", {
              size: formatSize(
                review.plugin_bytes + (review.client_bytes ?? 0),
              ),
            }),
            review.dependencies.length > 0
              ? t("plugins.install.dependencies", {
                  count: review.dependencies.length,
                  size: formatSize(review.dependency_bytes),
                })
              : t("plugins.install.noDependencies"),
          ].join(" · ")}
        </p>
        {review.dependencies.length > 0 ? (
          <HelpDisclosure title={t("plugins.install.dependencyList")}>
            <ul className="grid gap-0.5 font-mono">
              {review.dependencies.map((dependency) => (
                <li key={`${dependency.name}@${dependency.version}`}>
                  {dependency.name} {dependency.version}
                </li>
              ))}
            </ul>
          </HelpDisclosure>
        ) : null}
        {scripts.length > 0 ? (
          <p className="text-xs text-muted-foreground">
            {t("plugins.install.scripts", {
              packages: scripts.map((warning) => warning.package).join(", "),
            })}
          </p>
        ) : null}
        {otherWarnings.map((warning, index) => (
          <p
            className="text-xs break-words text-warning-foreground"
            key={`${warning.code}-${index}`}
          >
            {t(`plugins.install.warnings.${warning.code}`, {
              defaultValue: warning.detail ?? warning.code,
              detail: warning.detail ?? "",
            })}
          </p>
        ))}
      </ReviewGroup>
      {risks.length > 0 ? (
        <ReviewGroup
          testId="plugin-install-risks"
          title={t("plugins.install.groups.risks")}
          tone="risk"
        >
          <ul className="grid gap-1 text-xs">
            {risks.map((risk) => (
              <li key={risk}>{risk}</li>
            ))}
          </ul>
        </ReviewGroup>
      ) : null}
      {review.changelog ? (
        <HelpDisclosure title={t("plugins.install.changelog")}>
          <pre className="font-sans text-xs whitespace-pre-wrap text-foreground">
            {review.changelog}
          </pre>
        </HelpDisclosure>
      ) : null}
    </div>
  );
}
