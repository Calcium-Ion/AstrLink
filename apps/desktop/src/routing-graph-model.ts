import { parseFailurePolicy, type FailurePolicy } from "./failure-policy-model";

export type GraphKind = "entry" | "call" | "condition" | "stop";
export type GraphField =
  | "request.model"
  | "request.protocol"
  | "request.streaming"
  | "request.has_tools"
  | "request.has_images"
  | "last.status"
  | "last.error"
  | "attempts"
  | "quota.exhausted"
  | "quota.used_percent";
export interface GraphPredicate {
  all?: GraphPredicate[];
  any?: GraphPredicate[];
  field?: GraphField;
  operator?: "eq" | "ne" | "gt" | "gte" | "lt" | "lte";
  value?: string | boolean | number;
  service_id?: string;
  window?: "primary" | "secondary";
}
export interface GraphRule {
  id: string;
  label?: string;
  predicate: GraphPredicate;
}
export interface GraphNode {
  id: string;
  kind: GraphKind;
  enabled: boolean;
  name?: string;
  model?: string;
  service_id?: string;
  upstream_model?: string;
  max_attempts?: number;
  failure_policy?: FailurePolicy;
  rules?: GraphRule[];
  unknown_port?: string;
}
export interface GraphEdge {
  id: string;
  source: string;
  port: string;
  target: string;
}
export interface RoutingGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}
export type GraphLayout = Record<string, { x: number; y: number }>;
export interface GraphDocument {
  draft: RoutingGraph;
  layout: GraphLayout;
  active: RoutingGraph;
  revision: number;
  etag: string;
  history: { revision: number; created_at: string }[];
}
export interface GraphStep {
  node_id: string;
  kind: string;
  status: string;
  reason?: string;
  port?: string;
  service_id?: string;
  model?: string;
}
export interface GraphPreview {
  steps: GraphStep[];
  stop_reason: string;
  attempts: number;
}
export interface GraphPreviewInput {
  graph: RoutingGraph;
  entry_id: string;
  facts: {
    protocol: string;
    streaming: boolean;
    has_tools?: boolean;
    has_images?: boolean;
  };
  outcomes: Record<string, string>;
}

const kinds: GraphKind[] = ["entry", "call", "condition", "stop"];
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid routing graph document");
  return value as Record<string, unknown>;
}
function text(value: unknown): string {
  if (typeof value !== "string") throw new Error("Invalid graph text");
  return value;
}
function integer(value: unknown): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
    throw new Error("Invalid graph version");
  return value;
}

export function parseGraph(value: unknown): RoutingGraph {
  const graph = object(value);
  if (
    !Array.isArray(graph.nodes) ||
    !Array.isArray(graph.edges) ||
    graph.nodes.length > 512 ||
    graph.edges.length > 1024
  )
    throw new Error("Invalid routing graph size");
  const nodes = graph.nodes.map((value): GraphNode => {
    const node = object(value);
    if (
      !kinds.includes(node.kind as GraphKind) ||
      typeof node.enabled !== "boolean"
    )
      throw new Error("Invalid routing node");
    const result: GraphNode = {
      id: text(node.id),
      kind: node.kind as GraphKind,
      enabled: node.enabled,
    };
    for (const key of [
      "name",
      "model",
      "service_id",
      "upstream_model",
      "unknown_port",
    ] as const)
      if (node[key] !== undefined) result[key] = text(node[key]);
    if (node.max_attempts !== undefined)
      result.max_attempts = integer(node.max_attempts);
    if (node.failure_policy !== undefined)
      result.failure_policy = parseFailurePolicy(node.failure_policy);
    if (node.rules !== undefined) {
      if (!Array.isArray(node.rules))
        throw new Error("Invalid condition rules");
      result.rules = node.rules.map((value) => {
        const rule = object(value);
        return {
          id: text(rule.id),
          label: rule.label === undefined ? undefined : text(rule.label),
          predicate: parsePredicate(rule.predicate),
        };
      });
    }
    return result;
  });
  return {
    nodes,
    edges: graph.edges.map((value) => {
      const edge = object(value);
      return {
        id: text(edge.id),
        source: text(edge.source),
        port: text(edge.port),
        target: text(edge.target),
      };
    }),
  };
}

export function parsePredicate(value: unknown, depth = 0): GraphPredicate {
  if (depth > 4) throw new Error("Condition nesting exceeds limit");
  const p = object(value);
  for (const key of ["all", "any"] as const)
    if (p[key] !== undefined) {
      if (!Array.isArray(p[key]) || p[key].length > 16)
        throw new Error("Invalid condition group");
      return { [key]: p[key].map((child) => parsePredicate(child, depth + 1)) };
    }
  if (!["string", "boolean", "number"].includes(typeof p.value))
    throw new Error("Invalid condition value");
  return {
    field: text(p.field) as GraphField,
    operator: text(p.operator) as GraphPredicate["operator"],
    value: p.value as string | number | boolean,
    ...(p.service_id ? { service_id: text(p.service_id) } : {}),
    ...(p.window ? { window: text(p.window) as "primary" | "secondary" } : {}),
  };
}

export function parseGraphDocument(value: unknown): GraphDocument {
  const doc = object(value);
  const positions = object(doc.layout),
    layout: GraphLayout = {};
  for (const [id, value] of Object.entries(positions)) {
    const p = object(value);
    if (
      typeof p.x !== "number" ||
      !Number.isFinite(p.x) ||
      typeof p.y !== "number" ||
      !Number.isFinite(p.y)
    )
      throw new Error("Invalid graph layout");
    layout[id] = { x: p.x, y: p.y };
  }
  if (!Array.isArray(doc.history)) throw new Error("Invalid graph history");
  return {
    draft: parseGraph(doc.draft),
    active: parseGraph(doc.active),
    layout,
    revision: integer(doc.revision),
    etag: text(doc.etag),
    history: doc.history.map((value) => {
      const item = object(value);
      return {
        revision: integer(item.revision),
        created_at: text(item.created_at),
      };
    }),
  };
}

export function parseGraphStep(value: unknown): GraphStep {
  const step = object(value);
  return {
    node_id: text(step.node_id),
    kind: text(step.kind),
    status: text(step.status),
    ...Object.fromEntries(
      ["reason", "port", "service_id", "model"].flatMap((key) =>
        step[key] === undefined ? [] : [[key, text(step[key])]],
      ),
    ),
  };
}
export function parseGraphPreview(value: unknown): GraphPreview {
  const result = object(value);
  if (!Array.isArray(result.steps) || result.steps.length > 2048)
    throw new Error("Invalid routing trace");
  return {
    steps: result.steps.map(parseGraphStep),
    stop_reason: text(result.stop_reason),
    attempts: integer(result.attempts),
  };
}

export const graphID = () => `node_${crypto.randomUUID().replaceAll("-", "")}`;
export const graphEdge = (
  source: string,
  port: string,
  target: string,
): GraphEdge => ({ id: graphID(), source, port, target });
export const emptyGraph = (): RoutingGraph => ({ nodes: [], edges: [] });
export const graphPort = (node: GraphNode) =>
  node.kind === "entry" ? "next" : "failure";

export function reachable(graph: RoutingGraph, start: string): Set<string> {
  const seen = new Set<string>(),
    pending = [start],
    outgoing = new Map<string, string[]>();
  for (const edge of graph.edges) {
    const targets = outgoing.get(edge.source) ?? [];
    targets.push(edge.target);
    outgoing.set(edge.source, targets);
  }
  while (pending.length) {
    const id = pending.pop()!;
    if (seen.has(id)) continue;
    seen.add(id);
    pending.push(...(outgoing.get(id) ?? []));
  }
  return seen;
}
export function affectedEntries(graph: RoutingGraph, id: string): GraphNode[] {
  return graph.nodes.filter(
    (node) => node.kind === "entry" && reachable(graph, node.id).has(id),
  );
}
export function canConnect(
  graph: RoutingGraph,
  source: string,
  target: string,
): boolean {
  return (
    source !== target &&
    graph.nodes.find((node) => node.id === target)?.kind !== "entry" &&
    !reachable(graph, target).has(source)
  );
}
export function connectGraph(
  graph: RoutingGraph,
  source: string,
  port: string,
  target: string,
): RoutingGraph {
  if (!canConnect(graph, source, target)) return graph;
  return {
    ...graph,
    edges: [
      ...graph.edges.filter(
        (edge) => edge.source !== source || edge.port !== port,
      ),
      graphEdge(source, port, target),
    ],
  };
}

export function appendCall(
  graph: RoutingGraph,
  after: string,
  call: GraphNode,
): RoutingGraph {
  const node = graph.nodes.find((item) => item.id === after);
  if (!node || (node.kind !== "entry" && node.kind !== "call")) return graph;
  const port = graphPort(node),
    previous = graph.edges.find(
      (edge) => edge.source === after && edge.port === port,
    );
  return {
    nodes: [...graph.nodes, call],
    edges: [
      ...graph.edges.filter((edge) => edge !== previous),
      graphEdge(after, port, call.id),
      ...(previous ? [graphEdge(call.id, "failure", previous.target)] : []),
    ],
  };
}

// A compact editor may reorder only an exclusive, uninterrupted linear chain.
export function linearCalls(
  graph: RoutingGraph,
  entry: string,
): GraphNode[] | null {
  const result: GraphNode[] = [],
    seen = new Set<string>();
  let id = graph.edges.find(
    (edge) => edge.source === entry && edge.port === "next",
  )?.target;
  while (id) {
    if (seen.has(id)) return null;
    seen.add(id);
    const node = graph.nodes.find((item) => item.id === id);
    if (
      !node ||
      node.kind !== "call" ||
      graph.edges.filter((edge) => edge.target === id).length !== 1
    )
      return null;
    const outgoing = graph.edges.filter((edge) => edge.source === id);
    if (outgoing.length > 1 || outgoing.some((edge) => edge.port !== "failure"))
      return null;
    result.push(node);
    id = outgoing[0]?.target;
  }
  return result;
}
export function reorderCalls(
  graph: RoutingGraph,
  entry: string,
  order: string[],
): RoutingGraph {
  const calls = linearCalls(graph, entry);
  if (
    !calls ||
    calls.length !== order.length ||
    new Set(order).size !== order.length ||
    order.some((id) => !calls.some((node) => node.id === id))
  )
    return graph;
  const sources = new Set([entry, ...order]);
  return {
    ...graph,
    edges: [
      ...graph.edges.filter((edge) => !sources.has(edge.source)),
      ...order.map((id, i) =>
        graphEdge(i ? order[i - 1] : entry, i ? "failure" : "next", id),
      ),
    ],
  };
}

export function removeGraphNode(graph: RoutingGraph, id: string): RoutingGraph {
  const node = graph.nodes.find((item) => item.id === id),
    incoming = graph.edges.filter((edge) => edge.target === id),
    outgoing = graph.edges.filter((edge) => edge.source === id);
  const reconnect =
    node?.kind === "call" &&
    incoming.length === 1 &&
    outgoing.length === 1 &&
    outgoing[0].port === "failure";
  return {
    nodes: graph.nodes.filter((node) => node.id !== id),
    edges: [
      ...graph.edges.filter((edge) => edge.source !== id && edge.target !== id),
      ...(reconnect
        ? [graphEdge(incoming[0].source, incoming[0].port, outgoing[0].target)]
        : []),
    ],
  };
}

// Clone only the shared prefix that can reach the edited node. Outgoing paths
// outside this prefix remain shared, so changing one call never forks a tail.
export function isolateNode(
  graph: RoutingGraph,
  entryID: string,
  nodeID: string,
): { graph: RoutingGraph; copied: Map<string, string> } {
  const mine = reachable(graph, entryID),
    other = new Set<string>();
  for (const node of graph.nodes)
    if (node.kind === "entry" && node.id !== entryID)
      for (const id of reachable(graph, node.id)) other.add(id);
  const copied = new Map<string, string>();
  for (const node of graph.nodes)
    if (
      mine.has(node.id) &&
      other.has(node.id) &&
      reachable(graph, node.id).has(nodeID)
    )
      copied.set(node.id, graphID());
  if (!copied.size) return { graph, copied };
  const nodes = [
    ...graph.nodes,
    ...graph.nodes
      .filter((node) => copied.has(node.id))
      .map((node) => ({ ...structuredClone(node), id: copied.get(node.id)! })),
  ];
  const edges = graph.edges.map((edge) =>
    mine.has(edge.source) && !other.has(edge.source) && copied.has(edge.target)
      ? { ...edge, target: copied.get(edge.target)! }
      : edge,
  );
  for (const edge of graph.edges)
    if (copied.has(edge.source))
      edges.push(
        graphEdge(
          copied.get(edge.source)!,
          edge.port,
          copied.get(edge.target) ?? edge.target,
        ),
      );
  return { graph: { nodes, edges }, copied };
}

export function disabledBypasses(
  graph: RoutingGraph,
): { source: string; target: string; port: string; skipped: string[] }[] {
  const result: {
    source: string;
    target: string;
    port: string;
    skipped: string[];
  }[] = [];
  for (const edge of graph.edges) {
    const source = graph.nodes.find((node) => node.id === edge.source);
    if (!source?.enabled || (source.kind !== "call" && source.kind !== "entry"))
      continue;
    let target = graph.nodes.find((node) => node.id === edge.target);
    const skipped: string[] = [];
    while (
      target?.kind === "call" &&
      !target.enabled &&
      !skipped.includes(target.id)
    ) {
      skipped.push(target.id);
      const next = graph.edges.find(
        (edge) => edge.source === target?.id && edge.port === "failure",
      );
      target = graph.nodes.find((node) => node.id === next?.target);
    }
    if (target && skipped.length && !skipped.includes(target.id))
      result.push({
        source: edge.source,
        target: target.id,
        port: edge.port,
        skipped,
      });
  }
  return result;
}
