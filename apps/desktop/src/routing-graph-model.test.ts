import { describe, expect, it } from "vitest";
import {
  appendCall,
  canConnect,
  disabledBypasses,
  isolateNode,
  linearCalls,
  parseGraphDocument,
  reachable,
  removeGraphNode,
  reorderCalls,
  type RoutingGraph,
} from "./routing-graph-model";

function fixture(): RoutingGraph {
  return {
    nodes: [
      { id: "entry", kind: "entry", enabled: true, model: "public" },
      ...["call_a", "call_b", "call_c"].map((id) => ({
        id,
        kind: "call" as const,
        enabled: id !== "call_b",
        service_id: "service_" + id,
        upstream_model: "actual",
      })),
    ],
    edges: [
      { id: "first", source: "entry", port: "next", target: "call_a" },
      { id: "second", source: "call_a", port: "failure", target: "call_b" },
      { id: "third", source: "call_b", port: "failure", target: "call_c" },
    ],
  };
}

describe("routing graph editing invariants", () => {
  it("retains disabled nodes and renders a bypass without mutating declared edges", () => {
    const graph = fixture(),
      original = structuredClone(graph);
    expect(disabledBypasses(graph)).toEqual([
      {
        source: "call_a",
        target: "call_c",
        port: "failure",
        skipped: ["call_b"],
      },
    ]);
    expect(linearCalls(graph, "entry")?.map((node) => node.id)).toEqual([
      "call_a",
      "call_b",
      "call_c",
    ]);
    expect(graph).toEqual(original);
    graph.nodes[2].enabled = true;
    expect(disabledBypasses(graph)).toEqual([]);
  });
  it("reorders the edges, not node positions, and preserves disabled state", () => {
    const graph = reorderCalls(fixture(), "entry", [
      "call_c",
      "call_b",
      "call_a",
    ]);
    expect(linearCalls(graph, "entry")?.map((node) => node.id)).toEqual([
      "call_c",
      "call_b",
      "call_a",
    ]);
    expect(graph.nodes.find((node) => node.id === "call_b")?.enabled).toBe(
      false,
    );
    expect(canConnect(graph, "call_a", "call_c")).toBe(false);
    expect(canConnect(graph, "call_a", "entry")).toBe(false);
  });
  it("inserts and removes an exclusive fallback without losing its tail", () => {
    const graph = appendCall(fixture(), "call_a", {
      id: "inserted",
      kind: "call",
      enabled: true,
      service_id: "new_service",
      upstream_model: "different",
    });
    expect(linearCalls(graph, "entry")?.map((node) => node.id)).toEqual([
      "call_a",
      "inserted",
      "call_b",
      "call_c",
    ]);
    expect(
      linearCalls(removeGraphNode(graph, "inserted"), "entry")?.map(
        (node) => node.id,
      ),
    ).toEqual(["call_a", "call_b", "call_c"]);
  });
  it("isolates a shared ancestor and edited node while preserving the shared tail", () => {
    const graph = fixture();
    graph.nodes.push({
      id: "other_entry",
      kind: "entry",
      enabled: true,
      model: "other",
    });
    graph.edges.push({
      id: "other_first",
      source: "other_entry",
      port: "next",
      target: "call_a",
    });
    expect(linearCalls(graph, "entry")).toBeNull();
    const before = reachable(graph, "other_entry");
    const isolated = isolateNode(graph, "entry", "call_b");
    expect([...isolated.copied.keys()]).toEqual(["call_a", "call_b"]);
    expect(reachable(isolated.graph, "other_entry")).toEqual(before);
    const mine = reachable(isolated.graph, "entry");
    expect(mine.has("call_a")).toBe(false);
    expect(mine.has("call_b")).toBe(false);
    expect(mine.has("call_c")).toBe(true);
    expect(mine.has(isolated.copied.get("call_b")!)).toBe(true);
  });
  it("rejects malformed persisted data at the bridge boundary", () => {
    expect(() =>
      parseGraphDocument({
        draft: fixture(),
        active: fixture(),
        layout: {},
        revision: -1,
        etag: "tag",
        history: [],
      }),
    ).toThrow();
    expect(() =>
      parseGraphDocument({
        draft: fixture(),
        active: fixture(),
        layout: { entry: { x: NaN, y: 1 } },
        revision: 1,
        etag: "tag",
        history: [],
      }),
    ).toThrow();
  });
});
