export type RoutePlanType = "native" | "delegated" | "relaykit";
export type RouteSelectionMode = "priority" | "auto";

export interface RouteMatch {
  protocol: string;
  model?: string;
}

export interface RouteTarget {
  service_id: string;
  plan_type: RoutePlanType;
  upstream_protocol: string;
  priority: number;
  upstream_model?: string;
}

export interface RouteCategory {
  category_id: string;
  targets: RouteTarget[];
}

export interface RouteSelection {
  mode: RouteSelectionMode;
  taxonomy_id?: string;
}

export interface Route {
  id: string;
  name: string;
  enabled: boolean;
  priority: number;
  match: RouteMatch;
  selection?: RouteSelection;
  targets?: RouteTarget[];
  categories?: RouteCategory[];
}

export interface RoutePage {
  items: Route[];
  next_cursor: string | null;
}

export interface RouteRecord {
  route: Route;
  etag: string;
}

export type RouteCreateInput = Omit<Route, "id" | "enabled"> & {
  enabled?: boolean;
};

export type RoutePatchInput = Partial<
  Pick<Route, "name" | "enabled" | "priority" | "match">
> & {
  selection?: RouteSelection | null;
  targets?: RouteTarget[] | null;
  categories?: RouteCategory[] | null;
};

type JsonObject = Record<string, unknown>;

const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const protocolIDPattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const classificationIDPattern = /^[a-z][a-z0-9._-]{0,63}$/;
const etagPattern = /^"sha256:[0-9a-f]{64}"$/;
const planTypes = new Set<RoutePlanType>([
  "native",
  "delegated",
  "relaykit",
]);

function invalid(path: string, message: string): never {
  throw new Error(`Invalid Route IPC response at ${path}: ${message}`);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as JsonObject;
}

function keysAt(
  object: JsonObject,
  required: readonly string[],
  optional: readonly string[],
  path: string,
): void {
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(object)) {
    if (!allowed.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of required) {
    if (!Object.hasOwn(object, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function stringAt(
  value: unknown,
  path: string,
  minimum: number,
  maximum: number,
): string {
  if (typeof value !== "string") {
    return invalid(path, `expected ${minimum} to ${maximum} characters`);
  }
  const length = [...value].length;
  if (length < minimum || length > maximum) {
    return invalid(path, `expected ${minimum} to ${maximum} characters`);
  }
  return value;
}

function priorityAt(value: unknown, path: string): number {
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < 0 ||
    value > 1_000_000
  ) {
    return invalid(path, "expected an integer from 0 through 1000000");
  }
  return value;
}

function protocolAt(value: unknown, path: string): string {
  const protocol = stringAt(value, path, 3, 96);
  if (!protocolIDPattern.test(protocol)) invalid(path, "invalid protocol ID");
  return protocol;
}

function parseMatch(value: unknown, path: string): RouteMatch {
  const match = objectAt(value, path);
  keysAt(match, ["protocol"], ["model"], path);
  const protocol = protocolAt(match.protocol, `${path}.protocol`);
  const model = Object.hasOwn(match, "model")
    ? stringAt(match.model, `${path}.model`, 1, 256)
    : undefined;
  return { protocol, ...(model ? { model } : {}) };
}

function parseSelection(value: unknown, path: string): RouteSelection {
  const selection = objectAt(value, path);
  keysAt(selection, ["mode"], ["taxonomy_id"], path);
  if (selection.mode !== "priority" && selection.mode !== "auto") {
    invalid(`${path}.mode`, "unknown route selection mode");
  }
  if (selection.mode === "auto") {
    const taxonomy = stringAt(
      selection.taxonomy_id,
      `${path}.taxonomy_id`,
      1,
      64,
    );
    if (!classificationIDPattern.test(taxonomy)) {
      invalid(`${path}.taxonomy_id`, "invalid taxonomy ID");
    }
    return { mode: "auto", taxonomy_id: taxonomy };
  }
  if (Object.hasOwn(selection, "taxonomy_id")) {
    invalid(`${path}.taxonomy_id`, "only auto selection may set taxonomy_id");
  }
  return { mode: "priority" };
}

function parseTarget(
  value: unknown,
  path: string,
  match: RouteMatch,
  auto: boolean,
): RouteTarget {
  const target = objectAt(value, path);
  keysAt(
    target,
    ["service_id", "plan_type", "upstream_protocol", "priority"],
    ["upstream_model"],
    path,
  );
  const serviceID = stringAt(
    target.service_id,
    `${path}.service_id`,
    3,
    96,
  );
  if (!resourceIDPattern.test(serviceID)) {
    invalid(`${path}.service_id`, "invalid service ID");
  }
  if (
    typeof target.plan_type !== "string" ||
    !planTypes.has(target.plan_type as RoutePlanType)
  ) {
    invalid(`${path}.plan_type`, "unknown plan type");
  }
  const planType = target.plan_type as RoutePlanType;
  const upstreamProtocol = protocolAt(
    target.upstream_protocol,
    `${path}.upstream_protocol`,
  );
  if (planType !== "relaykit" && upstreamProtocol !== match.protocol) {
    invalid(
      `${path}.upstream_protocol`,
      `${planType} target must preserve the ingress protocol`,
    );
  }
  const upstreamModel = Object.hasOwn(target, "upstream_model")
    ? stringAt(target.upstream_model, `${path}.upstream_model`, 1, 256)
    : undefined;
  if (upstreamModel && !match.model) {
    invalid(`${path}.upstream_model`, "requires an exact public model");
  }
  if (auto && !upstreamModel) {
    invalid(`${path}.upstream_model`, "auto target requires an upstream model");
  }
  return {
    service_id: serviceID,
    plan_type: planType,
    upstream_protocol: upstreamProtocol,
    priority: priorityAt(target.priority, `${path}.priority`),
    ...(upstreamModel ? { upstream_model: upstreamModel } : {}),
  };
}

function parseCategory(
  value: unknown,
  path: string,
  match: RouteMatch,
): RouteCategory {
  const category = objectAt(value, path);
  keysAt(category, ["category_id", "targets"], [], path);
  const categoryID = stringAt(
    category.category_id,
    `${path}.category_id`,
    1,
    64,
  );
  if (!classificationIDPattern.test(categoryID)) {
    invalid(`${path}.category_id`, "invalid category ID");
  }
  if (!Array.isArray(category.targets) || category.targets.length === 0) {
    invalid(`${path}.targets`, "expected at least one target");
  }
  return {
    category_id: categoryID,
    targets: category.targets.map((target, index) =>
      parseTarget(target, `${path}.targets[${index}]`, match, true),
    ),
  };
}

export function parseRoute(value: unknown, path = "$"): Route {
  const route = objectAt(value, path);
  keysAt(
    route,
    ["id", "name", "enabled", "priority", "match"],
    ["selection", "targets", "categories"],
    path,
  );
  const id = stringAt(route.id, `${path}.id`, 3, 96);
  if (!resourceIDPattern.test(id)) invalid(`${path}.id`, "invalid route ID");
  const name = stringAt(route.name, `${path}.name`, 1, 128);
  if (typeof route.enabled !== "boolean") {
    invalid(`${path}.enabled`, "expected a boolean");
  }
  const match = parseMatch(route.match, `${path}.match`);
  const selection = Object.hasOwn(route, "selection")
    ? parseSelection(route.selection, `${path}.selection`)
    : undefined;
  const auto = selection?.mode === "auto";

  if (auto) {
    if (match.model !== "astrlink/auto") {
      invalid(`${path}.match.model`, "auto selection requires astrlink/auto");
    }
    if (Object.hasOwn(route, "targets")) {
      invalid(`${path}.targets`, "auto selection uses category-owned targets");
    }
    if (!Array.isArray(route.categories) || route.categories.length < 2) {
      invalid(`${path}.categories`, "auto selection requires at least two categories");
    }
    const categories = route.categories.map((category, index) =>
      parseCategory(category, `${path}.categories[${index}]`, match),
    );
    if (new Set(categories.map((category) => category.category_id)).size !== categories.length) {
      invalid(`${path}.categories`, "duplicate category ID");
    }
    const models = new Set(
      categories.flatMap((category) =>
        category.targets.map((target) => target.upstream_model),
      ),
    );
    if (models.size < 2) {
      invalid(`${path}.categories`, "auto selection requires two distinct models");
    }
    return {
      id,
      name,
      enabled: route.enabled,
      priority: priorityAt(route.priority, `${path}.priority`),
      match,
      selection,
      categories,
    };
  }

  if (match.model === "astrlink/auto") {
    invalid(`${path}.match.model`, "astrlink/auto is reserved for auto selection");
  }
  if (Object.hasOwn(route, "categories")) {
    invalid(`${path}.categories`, "priority selection cannot contain categories");
  }
  if (!Array.isArray(route.targets) || route.targets.length === 0) {
    invalid(`${path}.targets`, "priority selection requires at least one target");
  }
  return {
    id,
    name,
    enabled: route.enabled,
    priority: priorityAt(route.priority, `${path}.priority`),
    match,
    ...(selection ? { selection } : {}),
    targets: route.targets.map((target, index) =>
      parseTarget(target, `${path}.targets[${index}]`, match, false),
    ),
  };
}

export function parseRoutePage(value: unknown): RoutePage {
  const page = objectAt(value, "$");
  keysAt(page, ["items", "next_cursor"], [], "$");
  if (!Array.isArray(page.items)) invalid("$.items", "expected an array");
  const nextCursor =
    page.next_cursor === null
      ? null
      : stringAt(page.next_cursor, "$.next_cursor", 1, 512);
  return {
    items: page.items.map((route, index) =>
      parseRoute(route, `$.items[${index}]`),
    ),
    next_cursor: nextCursor,
  };
}

export function parseRouteRecord(value: unknown): RouteRecord {
  const record = objectAt(value, "$");
  keysAt(record, ["route", "etag"], [], "$");
  const etag = stringAt(record.etag, "$.etag", 3, 128);
  if (!etagPattern.test(etag)) invalid("$.etag", "invalid strong entity tag");
  return { route: parseRoute(record.route, "$.route"), etag };
}
