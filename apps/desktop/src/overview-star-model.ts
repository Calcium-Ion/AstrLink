import { offersProviders } from "./PluginList";
import type { PluginSummary } from "./plugin-model";
import type { Service } from "./service-model";
import type {
  UsageGroup,
  UsagePluginGroup,
  UsageSummary,
  UsageTotals,
} from "./usage-range";

/** The busiest places an arc names before the rest merge into one. */
export const STAR_ARC_LIMIT = 5;

/** The plugins a side of the gateway lists before the rest merge into one. */
export const STAR_DOCK_LIMIT = 4;

/** What a place on an arc sent or received over the range. */
export interface StarFigures {
  tokens: number;
  requests: number;
  failed: number;
}

/** A client or a model on the star map. */
export interface StarPlace extends StarFigures {
  /** The access token or the model, null for a model Core could not name. */
  id: string | null;
  name: string;
  /**
   * The rest of the range, merged: `count` places, which may be none when
   * the rest came without an access token; `whole` when no place is named,
   * so the rest is all of it.
   */
  rest?: { count: number; whole: boolean };
}

function figures(group: UsageTotals): StarFigures {
  return {
    tokens: group.total_tokens,
    requests: group.requests,
    failed: group.failed_requests,
  };
}

function active(figures: StarFigures): boolean {
  return figures.tokens > 0 || figures.requests + figures.failed > 0;
}

/**
 * The busiest places of a breakdown, then one place for whatever else the
 * totals hold: the other places, and for clients the requests that came
 * without an access token. A single other place keeps its own name.
 */
export function arcPlaces(
  groups: Array<StarFigures & { id: string | null; name: string }>,
  totals: StarFigures,
  limit = STAR_ARC_LIMIT,
): StarPlace[] {
  const busy = groups.filter(active);
  const named = busy.length > limit + 1 ? busy.slice(0, limit) : busy;
  const rest = {
    tokens: totals.tokens,
    requests: totals.requests,
    failed: totals.failed,
  };
  for (const place of named) {
    rest.tokens -= place.tokens;
    rest.requests -= place.requests;
    rest.failed -= place.failed;
  }
  const places: StarPlace[] = named.map((place) => ({ ...place }));
  if (active(rest)) {
    places.push({
      id: null,
      name: "",
      tokens: Math.max(0, rest.tokens),
      requests: Math.max(0, rest.requests),
      failed: Math.max(0, rest.failed),
      rest: { count: busy.length - named.length, whole: named.length === 0 },
    });
  }
  return places;
}

/** The clients of the range: access tokens by name, busiest first. */
export function clientPlaces(
  summary: UsageSummary,
  tokens: ReadonlyArray<{ id: string; name: string }>,
  limit = STAR_ARC_LIMIT,
): StarPlace[] {
  const names = new Map(tokens.map((token) => [token.id, token.name]));
  return arcPlaces(
    summary.by_token
      .filter((group): group is UsageGroup & { id: string } =>
        names.has(group.id ?? ""),
      )
      .map((group) => ({
        id: group.id,
        name: names.get(group.id) ?? group.id,
        ...figures(group),
      })),
    figures(summary.totals),
    limit,
  );
}

/** The models of the range, as sent upstream, busiest first. */
export function modelPlaces(
  summary: UsageSummary,
  unknownName: string,
  limit = STAR_ARC_LIMIT,
): StarPlace[] {
  return arcPlaces(
    summary.by_model.map((group) => ({
      id: group.id,
      name: group.id ?? unknownName,
      ...figures(group),
    })),
    figures(summary.totals),
    limit,
  );
}

/**
 * A line's width for what it carries: Token, or requests when the range
 * used none, against the busiest line. A square root keeps a small client
 * visible beside a large one.
 */
export function flowWidth(value: number, busiest: number): number {
  if (busiest <= 0 || value <= 0) return 3;
  return 3 + 13 * Math.sqrt(Math.min(1, value / busiest));
}

/** What a plugin is doing, as the star map shows it. */
export type StarPluginState = "running" | "failing" | "off" | "failed";

/** A plugin beside the gateway, with what it did over the range. */
export interface StarPlugin {
  plugin: PluginSummary;
  side: "enhancer" | "provider";
  state: StarPluginState;
  /** Requests its hooks acted on, those failed included. */
  calls: number;
  failedCalls: number;
  tasks: number;
  failedTasks: number;
  /** For a provider plugin: the providers that run it, and the requests they served. */
  providers: number;
  served: number;
}

/**
 * The installed plugins with what each did over the range: the requests its
 * hooks acted on, the tasks it took, and for a provider plugin the requests
 * the providers that run it served. A running plugin with a failed call or
 * task over the range is failing.
 */
export function starPlugins(
  plugins: readonly PluginSummary[],
  usage: readonly UsagePluginGroup[],
  services: readonly Service[],
  byService: readonly UsageGroup[],
): StarPlugin[] {
  const used = new Map(usage.map((group) => [group.plugin, group]));
  const served = new Map(byService.map((group) => [group.id, group]));
  return plugins.map((plugin) => {
    const group = used.get(plugin.key);
    const bound = services.filter(
      (service) =>
        service.kind === "plugin" && service.plugin?.key === plugin.key,
    );
    const failedCalls = group?.failed_requests ?? 0;
    const failedTasks = group?.failed_tasks ?? 0;
    const running = plugin.state === "loaded" && plugin.enabled;
    return {
      plugin,
      side: offersProviders(plugin) ? "provider" : "enhancer",
      state:
        plugin.state === "failed"
          ? "failed"
          : !running
            ? "off"
            : failedCalls + failedTasks > 0
              ? "failing"
              : "running",
      calls: (group?.requests ?? 0) + failedCalls,
      failedCalls,
      tasks: group?.tasks ?? 0,
      failedTasks,
      providers: bound.length,
      served: bound.reduce((sum, service) => {
        const usage = served.get(service.id);
        return sum + (usage ? usage.requests + usage.failed_requests : 0);
      }, 0),
    };
  });
}

/** How much a plugin did, to order a side and size its light. */
export function pluginWork(plugin: StarPlugin): number {
  // A generation is a larger piece of work than a request a hook saw.
  return plugin.calls + plugin.served + plugin.tasks * 10;
}

/** A side's plugins, busiest first, those running before those that are not. */
export function sidePlugins(
  plugins: readonly StarPlugin[],
  side: StarPlugin["side"],
): StarPlugin[] {
  const rank = (plugin: StarPlugin) =>
    plugin.state === "off" ? 2 : plugin.state === "failed" ? 1 : 0;
  return plugins
    .filter((plugin) => plugin.side === side)
    .sort(
      (a, b) =>
        rank(a) - rank(b) ||
        pluginWork(b) - pluginWork(a) ||
        a.plugin.name.localeCompare(b.plugin.name),
    );
}

/** The plugins' health, for the line under the map. */
export function pluginHealth(plugins: readonly StarPlugin[]): {
  installed: number;
  running: number;
  problems: number;
} {
  return {
    installed: plugins.length,
    running: plugins.filter(
      (plugin) => plugin.state === "running" || plugin.state === "failing",
    ).length,
    problems: plugins.filter(
      (plugin) => plugin.state === "failed" || plugin.state === "failing",
    ).length,
  };
}
