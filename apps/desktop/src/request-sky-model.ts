import {
  GATEWAY_REWRITES,
  type ClientType,
  type GatewayRewrite,
} from "./request-record-model";
import {
  clientSeriesSlot,
  SERIES_SLOTS,
  serviceSeriesSlot,
} from "./series-colors";
import type { ServiceKind } from "./service-model";
import { parseDayKey, type UsageRoute } from "./usage-range";

/** Rewrites made before routing; the rest depend on the chosen provider. */
const PRE_ROUTING = 5;

export const SKY_REWRITE_COLORS: Record<GatewayRewrite, string> = {
  model_redirect: "var(--sky-redirect)",
  privacy: "var(--sky-privacy)",
  image_compression: "var(--sky-image)",
  global_prompt: "var(--sky-prompt)",
  reasoning_effort: "var(--sky-thinking)",
  conversion: "var(--sky-convert)",
  client_identity: "var(--sky-identity)",
};

export const SKY_MUTED = "var(--sky-muted)";
export const SKY_HEIGHT = 316;
/** Every client and provider is one disc this big. */
export const SKY_NODE_R = 11;

const MAX_PLANETS = 6;
/**
 * Below this width the sky is narrow: four planet spots with names below
 * them, and client names beside their nodes.
 */
const COMPACT_WIDTH = 700;
const COMPACT_PLANETS = 4;
const MAX_CLIENTS = 5;
/** Trails drawn in all, however busy the range. */
export const STRAND_BUDGET = 320;
/** Trails one route draws at most; its volume shows in their opacity. */
export const ROUTE_STRANDS = 28;

function skySeries(slot: number | null): string {
  return slot === null ? SKY_MUTED : `var(--sky-series-${slot + 1})`;
}

export interface SkyService {
  id: string;
  name: string;
  kind: ServiceKind;
  enabled: boolean;
}

export interface SkyClient {
  /** Null for unknown clients and, when folded, for the rest. */
  id: ClientType | null;
  /** Requests in the shown range; 0 leaves the node dim. */
  requests: number;
  color: string;
  /** How many clients this node stands for; above 1 when folded. */
  folded: number;
}

export interface SkyProvider {
  /** Null for providers that were removed since. */
  id: string | null;
  name: string | null;
  kind: ServiceKind | null;
  requests: number;
  color: string;
}

export interface SkyRoute {
  key: string;
  client: number;
  /** Planet index, the cluster at `planets.length`, or null at the sun. */
  target: number | null;
  /** Planet the request failed at before another took it. */
  failedAt: number | null;
  failed: boolean;
  requests: number;
}

export interface SkyPlan {
  clients: SkyClient[];
  planets: SkyProvider[];
  /** Other providers, folded into one node. */
  cluster: SkyProvider[];
  routes: SkyRoute[];
  rewrites: Record<GatewayRewrite, number>;
  requests: number;
}

const REMOVED = "\u0000removed";

/** Planet spots a sky `width` pixels wide has room for. */
export function skyPlanetSlots(width: number): number {
  return width > 0 && width < COMPACT_WIDTH ? COMPACT_PLANETS : MAX_PLANETS;
}

function clientOf(route: UsageRoute): ClientType | null {
  return route.client_type === "unknown" ? null : (route.client_type ?? null);
}

function add<K>(counts: Map<K, number>, key: K, value: number) {
  counts.set(key, (counts.get(key) ?? 0) + value);
}

/**
 * Who sent what where. `layout` places the nodes: clients on the left, at
 * most `slots` planets on the right with the rest folded. Pass the same
 * routes for every range, so switching ranges never moves a node. The counts
 * come from `routes`, the range shown; a node without any stays in place.
 */
export function skyPlan(
  routes: readonly UsageRoute[],
  services: readonly SkyService[],
  slots = MAX_PLANETS,
  layout: readonly UsageRoute[] = routes,
): SkyPlan {
  const known = new Map(services.map((service) => [service.id, service]));
  const providerOf = (id: string | null) =>
    id === null ? null : known.has(id) ? id : REMOVED;
  const tally = (list: readonly UsageRoute[]) => {
    const providers = new Map<string, number>();
    const clients = new Map<ClientType | null, number>();
    for (const route of list) {
      add(clients, clientOf(route), route.requests);
      for (const id of new Set([route.service_id, route.failed_service_id])) {
        const provider = providerOf(id);
        if (provider !== null) add(providers, provider, route.requests);
      }
    }
    return { providers, clients };
  };
  const shown = tally(routes);
  const placed = tally(layout);
  // A layout that misses part of the range still gives it a node.
  for (const [id, count] of shown.providers)
    if (!placed.providers.has(id)) placed.providers.set(id, count);
  for (const [id, count] of shown.clients)
    if (!placed.clients.has(id)) placed.clients.set(id, count);

  const rewrites = Object.fromEntries(
    GATEWAY_REWRITES.map((kind) => [kind, 0]),
  ) as Record<GatewayRewrite, number>;
  let requests = 0;
  for (const route of routes) {
    requests += route.requests;
    for (const kind of route.rewrites) rewrites[kind] += route.requests;
  }

  // Providers in catalog order keep their place as counts change.
  const candidates = [
    ...services
      .filter((service) => service.enabled || placed.providers.has(service.id))
      .map((service) => service.id),
    ...(placed.providers.has(REMOVED) ? [REMOVED] : []),
  ];
  let kept = candidates;
  let folded: string[] = [];
  if (candidates.length > slots) {
    const busy = candidates
      .filter((id) => placed.providers.has(id))
      .sort(
        (left, right) =>
          placed.providers.get(right)! - placed.providers.get(left)!,
      );
    const keep = new Set(
      busy.length <= slots ? busy : busy.slice(0, slots - 1),
    );
    kept = candidates.filter((id) => keep.has(id));
    folded = busy.filter((id) => !keep.has(id));
  }
  const provider = (id: string): SkyProvider => {
    const service = known.get(id);
    return {
      id: service?.id ?? null,
      name: service?.name ?? null,
      kind: service?.kind ?? null,
      requests: shown.providers.get(id) ?? 0,
      color: SKY_MUTED,
    };
  };
  const planets = kept.map(provider);
  // Each planet takes its vendor's color, or the next free one when two
  // planets share a vendor.
  const taken = new Set<number>();
  for (const planet of planets) {
    if (planet.kind === null || planet.id === null) continue;
    let slot = serviceSeriesSlot(planet.kind, planet.id);
    for (let step = 0; step < SERIES_SLOTS && taken.has(slot); step += 1)
      slot = (slot + 1) % SERIES_SLOTS;
    taken.add(slot);
    planet.color = skySeries(slot);
  }
  const cluster = folded
    .map(provider)
    .sort((left, right) => right.requests - left.requests);

  const clientOrder = [...placed.clients.entries()]
    .sort(([, left], [, right]) => right - left)
    .map(([id]) => id);
  const named = clientOrder.filter((id) => id !== null);
  const fold = clientOrder.length > MAX_CLIENTS;
  const own = fold ? named.slice(0, MAX_CLIENTS - 1) : named;
  const clients: SkyClient[] = own.map((id) => ({
    id,
    requests: shown.clients.get(id) ?? 0,
    color: skySeries(clientSeriesSlot(id)),
    folded: 1,
  }));
  const rest = clientOrder.filter((id) => !own.includes(id as ClientType));
  if (rest.length > 0)
    clients.push({
      id: null,
      requests: rest.reduce((sum, id) => sum + (shown.clients.get(id) ?? 0), 0),
      color: SKY_MUTED,
      folded: rest.length,
    });
  // Busiest first reads top to bottom, as on the planet side.
  const clientIndex = (id: ClientType | null) => {
    const at = clients.findIndex((client) => client.id === id);
    return at >= 0 ? at : clients.length - 1;
  };

  const planetIndex = (id: string | null): number | null => {
    const key = providerOf(id);
    if (key === null) return null;
    const at = kept.indexOf(key);
    return at >= 0 ? at : planets.length;
  };
  const planned = routes.map((route): SkyRoute => {
    const target = planetIndex(route.service_id);
    const failedAt = planetIndex(route.failed_service_id);
    return {
      key: [
        route.client_type ?? "",
        route.service_id ?? "",
        route.failed_service_id ?? "",
        route.failed ? 1 : 0,
        ...route.rewrites,
      ].join("|"),
      client: clientIndex(clientOf(route)),
      target,
      failedAt: failedAt === target ? null : failedAt,
      failed: route.failed,
      requests: route.requests,
    };
  });
  return { clients, planets, cluster, routes: planned, rewrites, requests };
}

export type Point = readonly [number, number];
export type Cubic = readonly [Point, Point, Point, Point];

/** A stable 0–1 value for a string. */
export function skyHash(value: string): number {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  // Mix the bits so neighbouring strings land far apart.
  hash ^= hash >>> 16;
  hash = Math.imul(hash, 0x85ebca6b);
  hash ^= hash >>> 13;
  hash = Math.imul(hash, 0xc2b2ae35);
  hash ^= hash >>> 16;
  return (hash >>> 0) / 4294967296;
}

function lerp(a: Point, b: Point, t: number): Point {
  return [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t];
}

/** The point at `t` on a cubic Bézier. */
export function cubicAt(curve: Cubic, t: number): Point {
  const [p0, c0, c1, p1] = curve;
  const a = lerp(p0, c0, t);
  const b = lerp(c0, c1, t);
  const c = lerp(c1, p1, t);
  return lerp(lerp(a, b, t), lerp(b, c, t), t);
}

const fmt = (point: Point) => `${point[0].toFixed(1)},${point[1].toFixed(1)}`;

/** The first `t` of a cubic Bézier as a path, split by de Casteljau. */
export function cubicPath(curve: Cubic, t = 1): string {
  const [p0, c0, c1, p1] = curve;
  const a = lerp(p0, c0, t);
  const b = lerp(c0, c1, t);
  const c = lerp(c1, p1, t);
  const d = lerp(a, b, t);
  const e = lerp(b, c, t);
  return `M${fmt(p0)}C${fmt(a)} ${fmt(d)} ${fmt(lerp(d, e, t))}`;
}

/** A four-point star centred on `x, y`, `size` from centre to tip. */
export function sparklePath(x: number, y: number, size: number): string {
  const s = size;
  const k = s * 0.28;
  const n = (value: number) => Number(value.toFixed(2));
  return `M${n(x)} ${n(y - s)}L${n(x + k)} ${n(y - k)}L${n(x + s)} ${n(y)}L${n(x + k)} ${n(y + k)}L${n(x)} ${n(y + s)}L${n(x - k)} ${n(y + k)}L${n(x - s)} ${n(y)}L${n(x - k)} ${n(y - k)}Z`;
}

// Spots on the 1006-wide reference sky, stretched across the actual width.
const REFERENCE_WIDTH = 1006;
const SUN_Y = 178;
/** Where trails meet the sun, just outside its disc. */
const RIM = 30;
const CLIENT_SPOTS: Point[][] = [
  [[150, 182]],
  [
    [176, 124],
    [150, 240],
  ],
  [
    [178, 98],
    [112, 174],
    [196, 254],
  ],
  [
    [178, 92],
    [110, 162],
    [148, 230],
    [272, 270],
  ],
  [
    [196, 84],
    [116, 140],
    [96, 202],
    [158, 258],
    [288, 280],
  ],
];
const PLANET_SPOTS: Point[][] = [
  [[858, 182]],
  [
    [846, 120],
    [872, 236],
  ],
  [
    [830, 96],
    [904, 176],
    [800, 258],
  ],
  [
    [828, 92],
    [904, 164],
    [852, 230],
    [728, 266],
  ],
];
/** Rewrite flares: before routing on the incoming side, the rest outgoing. */
const FLARE_ANGLES = [312, 286, 260, 234, 208, 70, 110];
const SQUASH = 0.68;
/** Room right of the side planets for their names. */
const SIDE_LABEL_ROOM = 160;
/** Leftmost a client node sits, leaving room for its name. */
const CLIENT_MIN_X = 52;

export interface SkySpot {
  x: number;
  y: number;
}

export interface SkyNodeSpot extends SkySpot {
  /** √(requests / busiest on its side); null when it had none. */
  share: number | null;
}

export interface SkyPlanetSpot extends SkyNodeSpot {
  /** Names sit beside the planets once there are more than four. */
  side: boolean;
}

export interface SkyFlare {
  kind: GatewayRewrite;
  /** Before routing, on the left of the sun. */
  incoming: boolean;
  count: number;
  /** The ray from the sun's corona. */
  path: string;
  /** The rewrite's colored dot at the ray's tip. */
  dot: Point;
  /** Label start, outside the dot. */
  label: Point;
  anchor: "start" | "end";
}

export interface SkyHop {
  from: Point;
  control: Point;
  to: Point;
}

/** One request's way across the sky. */
export interface SkyCourse {
  in: Cubic;
  /** Absent when the request stopped at the gateway. */
  out: Cubic | null;
  /** The hop from the failed planet to the one that took it. */
  hop: SkyHop | null;
}

/** One drawn trail; a busy route draws a sample of its requests. */
export interface SkyStrand {
  in: Cubic;
  out: Cubic | null;
  client: number;
  /** Planet the trail ends at, the cluster at `planets.length`. */
  landing: number | null;
  opacity: number;
  /** Failed and failed-over trails stand out on their way out. */
  outOpacity: number;
}

export interface SkyScene {
  width: number;
  /** Width over the reference sky's. */
  scale: number;
  /** Horizontal squeeze of the corona on a narrow sky. */
  orbitScale: number;
  /** Narrow sky: client names go beside their nodes. */
  compact: boolean;
  sun: SkySpot;
  clients: SkyNodeSpot[];
  planets: SkyPlanetSpot[];
  cluster: SkyNodeSpot | null;
  /** Rewrites used in the range, nothing for the rest. */
  flares: SkyFlare[];
  strands: SkyStrand[];
  /** Ends of failed trails, each marked with a small star. */
  fails: Point[];
  /** One arc per failover between two providers. */
  hops: SkyHop[];
}

export interface SkySceneOptions {
  /** Brightest a single trail gets. */
  hairMax?: number;
  /** Brightness by age for a draw in 0–1; null keeps every trail at full. */
  recency?: ((draw: number) => number) | null;
}

function bezier3(a: Point, b: Point, c: Point, t: number): Point {
  return lerp(lerp(a, b, t), lerp(b, c, t), t);
}

/** A planet's centre, the cluster's at `planets.length`. */
function spotAt(
  scene: Pick<SkyScene, "planets" | "cluster">,
  index: number | null,
): Point | null {
  if (index === null) return null;
  const spot =
    index < scene.planets.length ? scene.planets[index] : scene.cluster;
  return spot ? [spot.x, spot.y] : null;
}

/** A point on the squashed corona around the sun, 0° at the top. */
function around(
  scene: Pick<SkyScene, "sun" | "orbitScale">,
  radius: number,
  degrees: number,
): Point {
  const angle = (degrees * Math.PI) / 180;
  return [
    scene.sun.x + radius * Math.sin(angle) * scene.orbitScale,
    scene.sun.y - radius * Math.cos(angle) * SQUASH,
  ];
}

function rim(sun: SkySpot, degrees: number): Point {
  const angle = (degrees * Math.PI) / 180;
  return [sun.x + RIM * Math.sin(angle), sun.y - RIM * Math.cos(angle)];
}

/**
 * The curves from a client through the sun to a planet. `key` seeds a small
 * jitter, so a route's trails fan out instead of stacking on one line, and
 * they meet the sun at spread-out points on its rim.
 */
export function skyCourse(
  scene: Pick<SkyScene, "scale" | "sun" | "clients" | "planets" | "cluster">,
  key: string,
  client: number,
  target: number | null,
  failedAt: number | null,
  failed = false,
): SkyCourse | null {
  const from = scene.clients[client];
  if (!from) return null;
  const { sun, scale } = scene;
  const end = spotAt(scene, target);
  const landing = spotAt(scene, failedAt ?? target);
  const j = skyHash(key) - 0.5;
  const j2 = skyHash(`${key}~`) - 0.5;
  const A: Point = [from.x, from.y];
  const enter = rim(sun, 270 - (A[1] - sun.y) * 0.25 + j * 40);
  let out: Cubic | null = null;
  if (landing) {
    const C = landing;
    const leave = rim(sun, 90 + (C[1] - sun.y) * 0.25 - j2 * 40);
    const near: Point = [C[0] - 140 * scale, C[1] + j2 * 26];
    // Stop just outside the node; a failed request stops further out, where
    // its star sits.
    const gap = SKY_NODE_R + (failed ? 9 : 1);
    const reach = Math.hypot(C[0] - near[0], C[1] - near[1]) || 1;
    const B: Point = [
      C[0] - ((C[0] - near[0]) / reach) * gap,
      C[1] - ((C[1] - near[1]) / reach) * gap,
    ];
    out = [leave, [leave[0] + 110 * scale, leave[1] + j * 22], near, B];
  }
  return {
    in: [
      [A[0] + SKY_NODE_R + 1, A[1]],
      [A[0] + 140 * scale, A[1] + j * 26],
      [enter[0] - 110 * scale, enter[1] + j2 * 22],
      enter,
    ],
    out,
    hop: failedAt !== null && end && landing ? skyHop(landing, end) : null,
  };
}

/** The gold arc from a provider that failed to the one that took over. */
function skyHop(from: Point, to: Point): SkyHop {
  const gap = SKY_NODE_R + 3;
  return {
    from: [from[0] - gap, from[1]],
    control: [Math.min(from[0], to[0]) - 64, (from[1] + to[1]) / 2],
    to: [to[0] - gap, to[1]],
  };
}

/** A failover hop as a cubic, so a comet can draw part of it. */
export function hopCurve(hop: SkyHop): Cubic {
  return [
    hop.from,
    lerp(hop.from, hop.control, 2 / 3),
    lerp(hop.to, hop.control, 2 / 3),
    hop.to,
  ];
}

export function hopPath(hop: SkyHop): string {
  return `M${fmt(hop.from)}Q${fmt(hop.control)} ${fmt(hop.to)}`;
}

/**
 * A rewrite's flare: a ray off the corona, longer the more requests it
 * changed, and longer still while a comet carrying it passes (`hot`, 0–1).
 */
export function skyFlare(
  scene: Pick<SkyScene, "sun" | "orbitScale">,
  kind: GatewayRewrite,
  count: number,
  hot = 0,
): SkyFlare {
  const index = GATEWAY_REWRITES.indexOf(kind);
  const angle = FLARE_ANGLES[index];
  const length = 24 + Math.min(34, Math.sqrt(count) * 8) + hot * 10;
  const dot = around(scene, 36 + length, angle);
  const left = angle > 180;
  return {
    kind,
    incoming: index < PRE_ROUTING,
    count,
    path: `M${fmt(around(scene, 24, angle - 12))}L${fmt(around(scene, 28 + length, angle))}L${fmt(around(scene, 24, angle + 12))}Z`,
    dot,
    label: [dot[0] + (left ? -8 : 8), dot[1] + 4],
    anchor: left ? "end" : "start",
  };
}

interface StrandGroup {
  key: string;
  client: number;
  target: number | null;
  failedAt: number | null;
  failed: boolean;
  requests: number;
}

/**
 * How many trails each group draws: up to `ROUTE_STRANDS`, lowered evenly
 * until all fit in `STRAND_BUDGET`.
 */
function strandCap(groups: readonly StrandGroup[]): number {
  const drawn = (cap: number) =>
    groups.reduce((sum, group) => sum + Math.min(group.requests, cap), 0);
  let cap = ROUTE_STRANDS;
  while (cap > 1 && drawn(cap) > STRAND_BUDGET) cap -= 1;
  return cap;
}

/** Where everything sits on a sky `width` pixels wide. */
export function skyScene(
  plan: SkyPlan,
  width: number,
  { hairMax = 0.3, recency = null }: SkySceneOptions = {},
): SkyScene {
  const scale = width / REFERENCE_WIDTH;
  const x = (reference: number) => reference * scale;
  const sun: SkySpot = { x: width / 2, y: SUN_Y };
  const orbitScale = Math.min(1, scale);
  const share = (requests: number, busiest: number) =>
    requests > 0 ? Math.sqrt(requests / Math.max(1, busiest)) : null;

  const clientSpots =
    CLIENT_SPOTS[Math.min(plan.clients.length, CLIENT_SPOTS.length) - 1] ?? [];
  const busiestClient = Math.max(
    0,
    ...plan.clients.map((client) => client.requests),
  );
  const clients = plan.clients.map((client, index): SkyNodeSpot => {
    const [cx, cy] = clientSpots[index];
    return {
      x: Math.max(CLIENT_MIN_X, x(cx)),
      y: cy,
      share: share(client.requests, busiestClient),
    };
  });

  const slots = plan.planets.length + (plan.cluster.length > 0 ? 1 : 0);
  const side = slots > 4;
  const spots: Point[] = side
    ? Array.from({ length: slots }, (_, index) =>
        bezier3([780, 78], [944, 178], [700, 284], index / (slots - 1)),
      )
    : (PLANET_SPOTS[slots - 1] ?? []);
  // A narrow sky moves the arc left so the names beside it still fit.
  const shift = side
    ? Math.max(
        0,
        Math.max(...spots.map(([sx]) => sx)) * scale -
          (width - SIDE_LABEL_ROOM),
      )
    : 0;
  const clusterRequests = plan.cluster.reduce(
    (sum, provider) => sum + provider.requests,
    0,
  );
  const busiestPlanet = Math.max(
    clusterRequests,
    ...plan.planets.map((planet) => planet.requests),
  );
  const planets = plan.planets.map(
    (planet, index): SkyPlanetSpot => ({
      x: x(spots[index][0]) - shift,
      y: spots[index][1],
      share: share(planet.requests, busiestPlanet),
      side,
    }),
  );
  const clusterSpot = plan.cluster.length > 0 ? spots[slots - 1] : null;
  const cluster = clusterSpot
    ? {
        x: x(clusterSpot[0]) - shift,
        y: clusterSpot[1],
        share: share(clusterRequests, busiestPlanet),
      }
    : null;

  const placed = { scale, sun, orbitScale, clients, planets, cluster };
  const flares = GATEWAY_REWRITES.filter((kind) => plan.rewrites[kind] > 0).map(
    (kind) => skyFlare(placed, kind, plan.rewrites[kind]),
  );

  // Routes that differ only in their rewrites take the same way.
  const groups = new Map<string, StrandGroup>();
  const pairs = new Map<string, { requests: number; drawn: number }>();
  const nodeKey = (index: number | null) =>
    index === null
      ? "sun"
      : (plan.planets[index]?.id ??
        (index < plan.planets.length ? "gone" : "cluster"));
  for (const route of plan.routes) {
    const clientKey = plan.clients[route.client]?.id ?? "rest";
    const key = `${clientKey}>${nodeKey(route.target)}|${route.failedAt === null ? "" : nodeKey(route.failedAt)}|${route.failed ? 1 : 0}`;
    const group = groups.get(key);
    if (group) group.requests += route.requests;
    else
      groups.set(key, {
        key,
        client: route.client,
        target: route.target,
        failedAt: route.failedAt,
        failed: route.failed,
        requests: route.requests,
      });
  }
  const cap = strandCap([...groups.values()]);
  const pairOf = (group: StrandGroup) => `${group.client}>${group.target}`;
  for (const group of groups.values()) {
    const pair = pairs.get(pairOf(group)) ?? { requests: 0, drawn: 0 };
    pair.requests += group.requests;
    pair.drawn += Math.min(group.requests, cap);
    pairs.set(pairOf(group), pair);
  }

  const strands: SkyStrand[] = [];
  const fails: Point[] = [];
  const hops = new Map<string, SkyHop>();
  for (const group of groups.values()) {
    const pair = pairs.get(pairOf(group))!;
    // A route's trails weigh about log(n) together, so a busy route reads as
    // a denser fan of hairlines, never a solid ribbon.
    const weight = Math.min(
      hairMax,
      (0.9 + 0.75 * Math.log1p(pair.requests)) / pair.drawn,
    );
    const special = group.failed || group.failedAt !== null;
    const drawn = Math.min(group.requests, cap);
    for (let index = 0; index < drawn; index += 1) {
      const key = `${group.key}#${index}`;
      const course = skyCourse(
        placed,
        key,
        group.client,
        group.target,
        group.failedAt,
        group.failed,
      );
      if (!course) break;
      const age = recency ? recency(skyHash(`${key}@`)) : 1;
      strands.push({
        in: course.in,
        out: course.out,
        client: group.client,
        landing: group.failedAt ?? group.target,
        opacity: weight * age,
        outOpacity: special ? 0.5 : weight * age,
      });
      if (group.failed && course.out) fails.push(course.out[3]);
      if (course.hop) hops.set(`${group.failedAt}>${group.target}`, course.hop);
    }
  }

  return {
    width,
    scale,
    orbitScale,
    compact: width < COMPACT_WIDTH,
    sun,
    clients,
    planets,
    cluster,
    flares,
    strands,
    fails,
    hops: [...hops.values()],
  };
}

const HOUR_MS = 3_600_000;
const MONTH_HOURS = 30 * 24;

/**
 * How bright a trail of the last 30 days is by its age: 1 now, down to 0.45
 * a month ago. Routes carry no times, so a trail draws its age from the
 * days' request counts; `draw` in 0–1 picks one request among them.
 */
export function skyRecency(
  days: readonly { date: string; requests: number }[],
  now: number,
): (draw: number) => number {
  const lit = days.flatMap((day) => {
    const start = parseDayKey(day.date)?.getTime();
    return day.requests > 0 && start !== undefined
      ? [{ start, requests: day.requests }]
      : [];
  });
  const total = lit.reduce((sum, day) => sum + day.requests, 0);
  return (draw) => {
    let left = draw * total;
    for (const day of lit) {
      if (left < day.requests) {
        const at = Math.min(
          now,
          day.start + (left / day.requests) * 24 * HOUR_MS,
        );
        const hours = (now - at) / HOUR_MS;
        return 0.45 + 0.55 * Math.min(1, Math.max(0, 1 - hours / MONTH_HOURS));
      }
      left -= day.requests;
    }
    return 1;
  };
}

export interface SkyStar {
  x: number;
  y: number;
  opacity: number;
  /** Dot radius, or the tip length of a four-point star. */
  size: number;
  sparkle: boolean;
}

/** The same seeded stars on every render, about 150 on the reference sky. */
export function skyStars(width: number, sparkles: boolean): SkyStar[] {
  let seed = 91;
  const next = () => (seed = (seed * 16807) % 2147483647) / 2147483647;
  const count = Math.round((150 * width) / REFERENCE_WIDTH);
  return Array.from({ length: count }, () => {
    const x = next() * width;
    const y = next() * SKY_HEIGHT;
    const k = next();
    const opacity = 0.2 + next() * 0.5;
    const sparkle = sparkles && k > 0.86;
    return {
      x,
      y,
      opacity,
      size: sparkle ? 2 + (k - 0.86) * 32 : 0.5 + k * 0.9,
      sparkle,
    };
  });
}

/** Star-chart rings around the sun and the meridians through them. */
export function skyGrid(scene: Pick<SkyScene, "sun" | "orbitScale">) {
  return {
    rings: [70, 140, 210, 290, 380].map((radius) => ({
      rx: radius * scene.orbitScale,
      ry: radius * SQUASH,
      dash: radius > 200 ? [1, 5] : [2, 4],
    })),
    meridians: Array.from({ length: 12 }, (_, index) => [
      around(scene, 46, index * 30),
      around(scene, 420, index * 30),
    ]),
  };
}

/** Where a just-finished request flies: its client node and planet. */
export function skyCometRoute(
  plan: SkyPlan,
  request: {
    client_type: ClientType | null;
    service_id: string | null;
    failed_service_id: string | null;
  },
): { client: number; target: number | null; failedAt: number | null } | null {
  const id = request.client_type === "unknown" ? null : request.client_type;
  let client = plan.clients.findIndex((node) => node.id === id);
  // A client folded into the rest flies from the rest's node.
  if (client < 0 && plan.clients.at(-1)?.id === null)
    client = plan.clients.length - 1;
  if (client < 0) return null;
  const planetOf = (service: string | null): number | null | undefined => {
    if (service === null) return null;
    const at = plan.planets.findIndex((planet) => planet.id === service);
    if (at >= 0) return at;
    if (plan.cluster.some((provider) => provider.id === service))
      return plan.planets.length;
    return undefined;
  };
  const target = planetOf(request.service_id);
  const failedAt = planetOf(request.failed_service_id);
  // A provider the sky does not show yet waits for the next exposure.
  if (target === undefined || failedAt === undefined) return null;
  return { client, target, failedAt: failedAt === target ? null : failedAt };
}
