import { describe, expect, it } from "vitest";

import type { RequestRecord } from "./request-record-model";
import { routeServices } from "./request-service-model";

describe("routeServices", () => {
  const served = { id: "service_served", name: "New API" };
  const skipped = { id: "service_skipped", name: "mly" };
  const unrelated = { id: "service_unrelated", name: "Unrelated" };
  const services = {
    [served.id]: served,
    [skipped.id]: skipped,
    [unrelated.id]: unrelated,
  };
  const events: RequestRecord["events"] = [
    {
      kind: "routed",
      started_at: "2026-07-25T10:00:00Z",
      ended_at: "2026-07-25T10:00:00Z",
      status: "succeeded",
      summary: `native · ${served.id}`,
      attempt_index: 1,
    },
  ];

  it("names the providers the route and the routing decision mention, and no others", () => {
    expect(
      routeServices(
        {
          events,
          routing_decision: {
            selected: "priority",
            skipped: [
              { service_id: skipped.id, reason: "disabled" },
              // Deleted since: the window shows the ID instead.
              { service_id: "service_deleted", reason: "model_not_listed" },
            ],
          },
        },
        services,
      ),
    ).toStrictEqual({ [served.id]: served, [skipped.id]: skipped });
    expect(routeServices({ events }, services)).toStrictEqual({
      [served.id]: served,
    });
  });
});
