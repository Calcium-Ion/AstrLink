import { describe, expect, it } from "vitest";

import type { ClientType } from "./request-record-model";
import {
  ROUTE_STRANDS,
  skyCometRoute,
  skyPlan,
  skyRecency,
  skyScene,
  STRAND_BUDGET,
  type SkyService,
} from "./request-sky-model";
import type { UsageRoute } from "./usage-range";

function services(count: number, overrides: Partial<SkyService>[] = []) {
  return Array.from(
    { length: count },
    (_, index): SkyService => ({
      id: `service_${index + 1}`,
      name: `Provider ${index + 1}`,
      kind: "newapi",
      enabled: true,
      ...overrides[index],
    }),
  );
}

function route(
  client: ClientType | null,
  service: string | null,
  requests: number,
  extra: Partial<UsageRoute> = {},
): UsageRoute {
  return {
    client_type: client,
    service_id: service,
    failed_service_id: null,
    failed: false,
    rewrites: [],
    requests,
    ...extra,
  };
}

describe("request sky plan", () => {
  it("shows four providers in catalog order with a wash by requests", () => {
    const catalog = services(4, [
      { kind: "codex_subscription" },
      { kind: "claude_subscription" },
    ]);
    const plan = skyPlan(
      [
        route("codex", "service_1", 40),
        route("claude_code", "service_2", 10),
        route("claude_code", "service_4", 2),
      ],
      catalog,
    );
    expect(plan.planets.map((planet) => planet.id)).toEqual([
      "service_1",
      "service_2",
      "service_3",
      "service_4",
    ]);
    expect(plan.planets.map((planet) => planet.requests)).toEqual([
      40, 10, 0, 2,
    ]);
    expect(plan.cluster).toEqual([]);
    const scene = skyScene(plan, 1006);
    expect(scene.planets.every((planet) => !planet.side)).toBe(true);
    expect(scene.planets[0].share).toBe(1);
    expect(scene.planets[1].share).toBeCloseTo(Math.sqrt(10 / 40));
    // An idle provider keeps its place without a wash.
    expect(scene.planets[2].share).toBeNull();
    expect(scene.cluster).toBeNull();
  });

  it("gives each planet its own color when two share a vendor", () => {
    const plan = skyPlan(
      [],
      services(3, [
        { kind: "codex_subscription" },
        { kind: "codex_subscription" },
        { kind: "claude_subscription" },
      ]),
    );
    const colors = plan.planets.map((planet) => planet.color);
    expect(new Set(colors).size).toBe(3);
    expect(colors.every((color) => color.startsWith("var(--sky-series-"))).toBe(
      true,
    );
  });

  it("shows all of six providers beside the arc", () => {
    const plan = skyPlan(
      [route("codex", "service_6", 3)],
      services(6, [{}, {}, {}, {}, { enabled: false }]),
    );
    // A disabled provider with no requests is not drawn.
    expect(plan.planets.map((planet) => planet.id)).toEqual([
      "service_1",
      "service_2",
      "service_3",
      "service_4",
      "service_6",
    ]);
    expect(skyScene(plan, 1006).planets.every((planet) => planet.side)).toBe(
      true,
    );
    const six = skyPlan([], services(6));
    expect(six.planets).toHaveLength(6);
    expect(six.cluster).toEqual([]);
  });

  it("folds providers beyond the five busiest into a cluster", () => {
    const catalog = services(15);
    const counts = [3, 0, 50, 7, 0, 1, 30, 0, 9, 2, 0, 12, 0, 0, 4];
    const plan = skyPlan(
      counts.flatMap((count, index) =>
        count > 0 ? [route("codex", `service_${index + 1}`, count)] : [],
      ),
      catalog,
    );
    // The five busiest keep catalog order; the rest go busiest first.
    expect(plan.planets.map((planet) => planet.id)).toEqual([
      "service_3",
      "service_4",
      "service_7",
      "service_9",
      "service_12",
    ]);
    expect(
      plan.cluster.map((provider) => [provider.id, provider.requests]),
    ).toEqual([
      ["service_15", 4],
      ["service_1", 3],
      ["service_10", 2],
      ["service_6", 1],
    ]);
    const folded = plan.routes.find((item) => item.key.includes("service_15"));
    expect(folded?.target).toBe(plan.planets.length);
    const scene = skyScene(plan, 1006);
    expect(scene.planets).toHaveLength(5);
    expect(scene.cluster).not.toBeNull();
  });

  it("shows six of fifteen providers when only six had requests", () => {
    const plan = skyPlan(
      [1, 4, 6, 9, 12, 15].map((index) =>
        route("codex", `service_${index}`, index),
      ),
      services(15),
    );
    expect(plan.planets).toHaveLength(6);
    expect(plan.cluster).toEqual([]);
  });

  it("draws a removed provider as one planet without a name", () => {
    const plan = skyPlan(
      [route("codex", "service_gone", 2), route("codex", "service_old", 1)],
      services(1),
    );
    expect(plan.planets.at(-1)).toMatchObject({
      id: null,
      name: null,
      requests: 3,
    });
  });

  it("keeps four clients and folds the rest into one node", () => {
    const plan = skyPlan(
      [
        route("codex", "service_1", 9),
        route("claude_code", "service_1", 8),
        route("cursor", "service_1", 7),
        route("opencode", "service_1", 6),
        route("cline", "service_1", 5),
        route("unknown", "service_1", 4),
        route(null, "service_1", 3),
      ],
      services(1),
    );
    expect(plan.clients.map((client) => client.id)).toEqual([
      "codex",
      "claude_code",
      "cursor",
      "opencode",
      null,
    ]);
    expect(plan.clients[4]).toMatchObject({ folded: 2, requests: 12 });
  });

  it("places nodes by the month so switching to today moves nothing", () => {
    const catalog = services(3);
    const month = [
      route("claude_code", "service_1", 90),
      route("codex", "service_2", 40),
      route("cursor", "service_3", 5),
    ];
    const today = [route("cursor", "service_3", 2)];
    const layout = [...month, ...today];
    const monthPlan = skyPlan(month, catalog, 6, layout);
    const todayPlan = skyPlan(today, catalog, 6, layout);
    expect(todayPlan.clients.map((client) => client.id)).toEqual(
      monthPlan.clients.map((client) => client.id),
    );
    const monthScene = skyScene(monthPlan, 1006);
    const todayScene = skyScene(todayPlan, 1006);
    const spots = (scene: typeof monthScene) =>
      [...scene.clients, ...scene.planets].map(({ x, y }) => [x, y]);
    expect(spots(todayScene)).toEqual(spots(monthScene));
    // Today only Cursor sent anything; the rest stay dim in their places.
    expect(todayScene.clients.map((client) => client.share)).toEqual([
      null,
      null,
      1,
    ]);
    expect(todayScene.planets.map((planet) => planet.share)).toEqual([
      null,
      null,
      1,
    ]);
  });

  it("draws flares only for rewrites used in the range", () => {
    const plan = skyPlan(
      [
        route("codex", "service_1", 9, { rewrites: ["privacy"] }),
        route("codex", "service_1", 4, {
          rewrites: ["privacy", "conversion"],
        }),
      ],
      services(1),
    );
    expect(plan.rewrites).toMatchObject({
      model_redirect: 0,
      privacy: 13,
      conversion: 4,
    });
    const scene = skyScene(plan, 1006);
    expect(scene.flares.map((flare) => flare.kind)).toEqual([
      "privacy",
      "conversion",
    ]);
    const [privacy, conversion] = scene.flares;
    expect(privacy.incoming).toBe(true);
    expect(privacy.dot[0]).toBeLessThan(scene.sun.x);
    expect(privacy.anchor).toBe("end");
    expect(conversion.dot[0]).toBeGreaterThan(scene.sun.x);
    expect(conversion.anchor).toBe("start");
  });

  it("marks failures with a star and failovers with one arc", () => {
    const plan = skyPlan(
      [
        route("codex", "service_2", 3, { failed_service_id: "service_1" }),
        route("codex", "service_2", 2, { failed: true }),
        route("codex", null, 1, { failed: true }),
      ],
      services(2),
    );
    expect(plan.routes[0]).toMatchObject({ target: 1, failedAt: 0 });
    const scene = skyScene(plan, 1006);
    expect(scene.hops).toHaveLength(1);
    const [planet1, planet2] = scene.planets;
    expect(scene.hops[0].from).toEqual([planet1.x - 14, planet1.y]);
    expect(scene.hops[0].to).toEqual([planet2.x - 14, planet2.y]);
    expect(scene.hops[0].control[0]).toBe(Math.min(planet1.x, planet2.x) - 64);
    // Two failed trails end short of the provider, each with its star.
    expect(scene.fails).toHaveLength(2);
    for (const [x, y] of scene.fails)
      expect(Math.hypot(x - planet2.x, y - planet2.y)).toBeCloseTo(20, 0);
    const special = scene.strands.filter(
      (strand) => strand.out && strand.outOpacity === 0.5,
    );
    expect(special).toHaveLength(5);
    // A request stopped at the gateway has no way out.
    expect(scene.strands.filter((strand) => strand.out === null)).toHaveLength(
      1,
    );
  });

  it("samples a busy route and weighs it by the log of its requests", () => {
    const plan = skyPlan(
      [route("codex", "service_1", 1000), route("claude_code", "service_1", 1)],
      services(1),
    );
    const scene = skyScene(plan, 1006, { hairMax: 0.3 });
    const busy = scene.strands.filter((strand) => strand.client === 0);
    expect(busy).toHaveLength(ROUTE_STRANDS);
    expect(busy[0].opacity).toBeCloseTo(
      (0.9 + 0.75 * Math.log1p(1000)) / ROUTE_STRANDS,
    );
    // One request draws one hairline, capped at the theme's brightest.
    const single = scene.strands.filter((strand) => strand.client === 1);
    expect(single).toHaveLength(1);
    expect(single[0].opacity).toBe(0.3);
    expect(
      skyScene(plan, 1006, { hairMax: 0.42 }).strands.at(-1)?.opacity,
    ).toBe(0.42);
  });

  it("draws at most the strand budget however busy the range", () => {
    const clients: ClientType[] = [
      "codex",
      "claude_code",
      "cursor",
      "opencode",
    ];
    const plan = skyPlan(
      clients.flatMap((client) =>
        [1, 2, 3, 4, 5, 6].map((index) =>
          route(client, `service_${index}`, 5000),
        ),
      ),
      services(6),
    );
    const scene = skyScene(plan, 1006);
    expect(scene.strands.length).toBeLessThanOrEqual(STRAND_BUDGET);
    expect(scene.strands.length).toBeGreaterThan(STRAND_BUDGET - 24);
    const perRoute = new Map<string, number>();
    for (const strand of scene.strands) {
      const key = `${strand.client}>${strand.landing}`;
      perRoute.set(key, (perRoute.get(key) ?? 0) + 1);
    }
    expect(perRoute.size).toBe(24);
    expect(new Set(perRoute.values()).size).toBe(1);
  });

  it("dims 30-day trails by age", () => {
    const now = new Date(2026, 10, 2, 12).getTime();
    const recency = skyRecency(
      [
        { date: "2026-10-04", requests: 1 },
        { date: "2026-10-20", requests: 0 },
        { date: "2026-11-02", requests: 1 },
      ],
      now,
    );
    expect(recency(0.25)).toBeCloseTo(0.45 + 0.55 * (1 - 696 / 720));
    expect(recency(0.75)).toBeGreaterThan(0.99);
    expect(skyRecency([], now)(0.5)).toBe(1);
  });
});

describe("comet routes", () => {
  const plan = skyPlan(
    [
      route("codex", "service_1", 9),
      route("claude_code", "service_1", 8),
      route("cursor", "service_2", 7),
      route("opencode", "service_3", 6),
      route("cline", "service_4", 5),
      route("pi", "service_5", 4),
      route("kimi_code", "service_6", 3),
      route("droid", "service_7", 2),
    ],
    services(9),
  );
  const request = (
    client: ClientType | null,
    service: string | null,
    failed: string | null = null,
  ) => ({
    client_type: client,
    service_id: service,
    failed_service_id: failed,
  });

  it("flies from the client's node to the provider's planet or the cluster", () => {
    expect(skyCometRoute(plan, request("codex", "service_2"))).toEqual({
      client: 0,
      target: 1,
      failedAt: null,
    });
    expect(
      skyCometRoute(plan, request("droid", "service_7", "service_1")),
    ).toEqual({ client: plan.clients.length - 1, target: 5, failedAt: 0 });
  });

  it("waits for the next read when the sky has no place for it", () => {
    expect(skyCometRoute(plan, request("codex", "service_9"))).toBeNull();
    expect(
      skyCometRoute(skyPlan([], services(1)), request("codex", "service_1")),
    ).toBeNull();
  });
});
