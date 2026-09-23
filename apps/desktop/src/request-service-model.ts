import { i18n } from "./i18n";
import type { RequestRecord, RequestSession } from "./request-record-model";
import type { Service } from "./service-model";

export type RequestService = Pick<Service, "id" | "name"> &
  Partial<Pick<Service, "kind">>;
export type RequestServiceMap = Readonly<Record<string, RequestService>>;

export interface RequestServiceIdentity {
  id: string | null;
  name: string;
  kind?: Service["kind"];
}

/** Resolve the recorded service, never infer the provider from a model name. */
export function requestServiceIdentity(
  record: Pick<RequestRecord | RequestSession, "service_id" | "status">,
  services: RequestServiceMap = {},
): RequestServiceIdentity {
  const id = record.service_id;
  if (!id) {
    return { id: null, name: i18n.t(missingServiceLabel(record.status)) };
  }
  const service = services[id];
  return { id, name: service?.name ?? id, kind: service?.kind };
}

/**
 * Route summaries name services by ID, one " · " segment each: the chosen one
 * ("native · id") and any rejected on the way ("id · reason").
 */
export function namedRouteSummary(
  summary: string,
  services: RequestServiceMap = {},
): string {
  return summary
    .split(" · ")
    .map((part) => services[part]?.name ?? part)
    .join(" · ");
}

/** The services a record's route events name, for a window without the list. */
export function routeServices(
  record: Pick<RequestRecord, "events">,
  services: RequestServiceMap = {},
): RequestServiceMap {
  const named: Record<string, RequestService> = {};
  for (const event of record.events) {
    if (event.kind !== "routed") continue;
    for (const part of event.summary.split(" · ")) {
      const service = services[part];
      if (service) named[part] = service;
    }
  }
  return named;
}

// Failures tied to one provider carry its id; a failure without one means no
// provider could serve the request.
export function missingServiceLabel(
  status: RequestRecord["status"] | RequestSession["status"],
) {
  switch (status) {
    case "pending":
      return "records.selectingService";
    case "failed":
      return "records.allServicesFailed";
    default:
      return "records.noService";
  }
}
