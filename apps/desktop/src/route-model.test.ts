import { describe, expect, it } from "vitest";

import { parseRoute, parseRoutePage, parseRouteRecord } from "./route-model";

const priorityRoute = {
  id: "route_code",
  name: "Code alias",
  enabled: true,
  priority: 10,
  match: { protocol: "openai.responses", model: "team/code" },
  selection: { mode: "priority" },
  targets: [
    {
      service_id: "service_primary",
      plan_type: "native",
      upstream_protocol: "openai.responses",
      priority: 0,
      upstream_model: "gpt-5.2",
    },
  ],
};

describe("route IPC model", () => {
  it("parses priority route pages and records", () => {
    expect(
      parseRoutePage({ items: [priorityRoute], next_cursor: null }).items[0],
    ).toEqual(priorityRoute);
    expect(
      parseRouteRecord({
        route: priorityRoute,
        etag: `"sha256:${"a".repeat(64)}"`,
      }).route.id,
    ).toBe("route_code");
  });

  it("parses the stable category-owned auto shape", () => {
    const route = parseRoute({
      id: "route_auto",
      name: "Automatic",
      enabled: false,
      priority: 0,
      match: { protocol: "openai.responses", model: "astrlink/auto" },
      selection: { mode: "auto", taxonomy_id: "astrlink-text-v1" },
      categories: [
        {
          category_id: "coding",
          targets: [
            {
              service_id: "service_primary",
              plan_type: "native",
              upstream_protocol: "openai.responses",
              priority: 0,
              upstream_model: "model-code",
            },
          ],
        },
        {
          category_id: "general",
          targets: [
            {
              service_id: "service_primary",
              plan_type: "native",
              upstream_protocol: "openai.responses",
              priority: 0,
              upstream_model: "model-general",
            },
          ],
        },
      ],
    });
    expect(route.selection?.mode).toBe("auto");
    expect(route.categories?.map((category) => category.category_id)).toEqual([
      "coding",
      "general",
    ]);
  });

  it("rejects route boundary drift and unsafe cross-field combinations", () => {
    expect(() => parseRoute({ ...priorityRoute, unexpected: true })).toThrow(
      "unexpected field",
    );
    expect(() =>
      parseRoute({
        ...priorityRoute,
        match: { protocol: "openai.responses" },
      }),
    ).toThrow("requires an exact public model");
    expect(() =>
      parseRoute({
        ...priorityRoute,
        targets: [
          {
            ...priorityRoute.targets[0],
            upstream_protocol: "openai.chat",
          },
        ],
      }),
    ).toThrow("must preserve the ingress protocol");
    expect(() =>
      parseRoute({
        ...priorityRoute,
        match: { protocol: "openai.responses", model: "astrlink/auto" },
      }),
    ).toThrow("reserved");
  });
});
