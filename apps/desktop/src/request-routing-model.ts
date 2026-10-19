import { i18n } from "./i18n";
import type {
  ModelRoutingDecision,
  RequestRecord,
  RequestRoutingDecision,
  RequestStatus,
} from "./request-record-model";
import type { RequestServiceMap } from "./request-service-model";
import type { TrajectoryRow } from "./request-trajectory-model";

/**
 * What routing did with one provider: excluded it while planning, picked it
 * but could not send (a missing credential, an open circuit), or used it.
 */
export type RoutingStepOutcome = "skipped" | "rejected" | "selected";

export interface RoutingStep {
  serviceId: string;
  outcome: RoutingStepOutcome;
  /** The skip or rejection reason, or the selected plan type, as Core wrote it. */
  code: string;
}

/** The raw text and status of a route event, before any display mapping. */
export interface RouteEvent {
  summary: string;
  status: RequestStatus;
}

const SEPARATOR = " · ";
const planTypes = new Set(["native", "delegated", "relaykit"]);
/** A provider without the model could not have served the call in any state. */
const MODEL_NOT_LISTED = "model_not_listed";

/**
 * Core writes a used provider as "plan · id" and a rejected one as
 * "id · reason". Records written before events existed carry only the id.
 * A row standing in for a routing decision without route events is empty.
 */
export function routeEventStep(event: RouteEvent): RoutingStep | null {
  const { summary } = event;
  if (!summary) return null;
  const at = event.status === "failed" ? summary.lastIndexOf(SEPARATOR) : -1;
  if (at > 0) {
    return {
      serviceId: summary.slice(0, at),
      outcome: "rejected",
      code: summary.slice(at + SEPARATOR.length),
    };
  }
  const first = summary.indexOf(SEPARATOR);
  const plan = first > 0 ? summary.slice(0, first) : "";
  if (planTypes.has(plan)) {
    return {
      serviceId: summary.slice(first + SEPARATOR.length),
      outcome: "selected",
      code: plan,
    };
  }
  return { serviceId: summary, outcome: "selected", code: "" };
}

/**
 * The route events that name API providers. A routing group records the model
 * it chose as a route event too ("model · rule"); read as a provider step it
 * would put a model name among the providers. That event comes first and
 * names the member model, which no provider event does.
 */
export function withoutModelRoutingEvent<T extends RouteEvent>(
  routes: readonly T[],
  decision: ModelRoutingDecision | undefined,
): T[] {
  if (!decision) return [...routes];
  const model = decision.member_model;
  let removed = false;
  return routes.filter((route) => {
    if (removed || route.status === "failed") return true;
    if (
      route.summary === model ||
      route.summary.startsWith(`${model}${SEPARATOR}`)
    ) {
      removed = true;
      return false;
    }
    return true;
  });
}

/**
 * Every provider routing considered for one call, in the order it decided:
 * the providers excluded ahead of the one used, in priority order, then the
 * route events as they happened. A provider both skipped and rejected (an
 * open circuit is both) appears once, as rejected, at its priority position.
 * Providers that do not list the model were never candidates and are left out.
 */
export function routingSteps(
  routes: readonly RouteEvent[],
  decision: RequestRoutingDecision | undefined,
): RoutingStep[] {
  const steps: RoutingStep[] = (decision?.skipped ?? [])
    .filter((skip) => skip.reason !== MODEL_NOT_LISTED)
    .map((skip) => ({
      serviceId: skip.service_id,
      outcome: "skipped",
      code: skip.reason,
    }));
  for (const route of routes) {
    const step = routeEventStep(route);
    if (!step) continue;
    const known =
      step.outcome === "rejected"
        ? steps.find(
            (item) =>
              item.serviceId === step.serviceId && item.outcome !== "selected",
          )
        : undefined;
    if (known) {
      known.outcome = "rejected";
      known.code = step.code;
    } else {
      steps.push(step);
    }
  }
  return steps;
}

/** A skip or rejection reason in words; unknown codes stay as written. */
export function routingReasonLabel(code: string): string {
  for (const group of ["skips", "rejections"]) {
    const key = `routingDecision.${group}.${code}`;
    if (i18n.exists(key)) return i18n.t(key);
  }
  return code;
}

/** How the selected provider was called, or null for an unknown plan. */
export function routingPlanLabel(code: string): string | null {
  return planTypes.has(code) ? i18n.t(`routingDecision.plans.${code}`) : null;
}

function serviceName(id: string, services: RequestServiceMap): string {
  return services[id]?.name ?? id;
}

/** Skipped providers grouped by reason, e.g. "已停用 ×3、未列出该模型". */
function skippedDigest(steps: readonly RoutingStep[]): string {
  const counts = new Map<string, number>();
  for (const step of steps) {
    counts.set(step.code, (counts.get(step.code) ?? 0) + 1);
  }
  return [...counts]
    .map(([code, count]) => {
      const label = routingReasonLabel(code);
      return count > 1
        ? i18n.t("routingDecision.reasonCount", { reason: label, count })
        : label;
    })
    .join(i18n.t("routingDecision.reasonSeparator"));
}

/**
 * The one-line text of a call's ROUTE row: the provider and why it was used
 * or refused, and on the call's last ROUTE row the providers routing skipped,
 * grouped by reason. Without a step the row stands for a call no provider
 * could take.
 */
export function routeRowSummary(
  route: RouteEvent,
  record:
    | Pick<RequestRecord, "events" | "routing_decision" | "service_id">
    | undefined,
  services: RequestServiceMap,
  last: boolean,
): string {
  const step = routeEventStep(route);
  const decision = record?.routing_decision;
  const parts: string[] = [];
  if (!step) {
    const unlisted =
      decision !== undefined &&
      decision.skipped.length > 0 &&
      decision.skipped.every((skip) => skip.reason === MODEL_NOT_LISTED);
    parts.push(
      i18n.t(
        unlisted
          ? "routingDecision.noProviderListsModel"
          : "routingDecision.noProvider",
      ),
    );
  } else {
    parts.push(serviceName(step.serviceId, services));
    if (step.outcome === "rejected") {
      parts.push(routingReasonLabel(step.code));
    } else {
      // The decision explains the call's current provider only.
      if (decision?.selected && step.serviceId === record?.service_id) {
        parts.push(
          i18n.t(`routingDecision.selectionsShort.${decision.selected}`),
        );
      }
      // Forwarding as is needs no mention; a conversion does.
      if (step.code && step.code !== "native") {
        const plan = routingPlanLabel(step.code);
        if (plan) parts.push(plan);
      }
    }
  }
  if (last && decision && record) {
    const named = new Set(
      record.events
        .filter((event) => event.kind === "routed")
        .map((event) => routeEventStep(event)?.serviceId),
    );
    const skipped = routingSteps([], decision).filter(
      (item) => !named.has(item.serviceId),
    );
    if (skipped.length > 0) {
      parts.push(
        i18n.t(
          step?.outcome === "rejected"
            ? "routingDecision.skippedMore"
            : "routingDecision.skippedCount",
          { count: skipped.length, reasons: skippedDigest(skipped) },
        ),
      );
    }
  }
  return parts.join(SEPARATOR);
}

/** ROUTE rows with their display text; every other row as it was. */
export function readableRouteRows(
  rows: readonly TrajectoryRow[],
  records: ReadonlyMap<string, RequestRecord>,
  services: RequestServiceMap = {},
): TrajectoryRow[] {
  const lastRoute = new Map<string, string>();
  for (const row of rows) {
    if (row.chip === "ROUTE") lastRoute.set(row.requestId, row.id);
  }
  return rows.map((row) =>
    row.chip === "ROUTE"
      ? {
          ...row,
          summary: routeRowSummary(
            row,
            records.get(row.requestId),
            services,
            lastRoute.get(row.requestId) === row.id,
          ),
        }
      : row,
  );
}
