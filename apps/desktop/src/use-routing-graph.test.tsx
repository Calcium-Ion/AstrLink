// @vitest-environment happy-dom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { GraphDocument } from "./routing-graph-model";

const bridge = vi.hoisted(() => ({
  getRoutingGraph: vi.fn(),
  saveRoutingGraph: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
import { useRoutingGraph } from "./use-routing-graph";

const documentFor = (
  model = "public",
  etag = "version-one",
): GraphDocument => ({
  draft: {
    nodes: [{ id: "entry_public", kind: "entry", enabled: true, model }],
    edges: [],
  },
  active: { nodes: [], edges: [] },
  layout: {},
  revision: 0,
  etag,
  history: [],
});
let current: ReturnType<typeof useRoutingGraph>;
const dirty = () => {};
function Harness() {
  current = useRoutingGraph(true, dirty);
  return <span>{current.dirty ? "dirty" : "saved"}</span>;
}

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

it("accepts canonical saved fields without an endless autosave loop", async () => {
  vi.useFakeTimers();
  bridge.getRoutingGraph.mockResolvedValue(documentFor());
  bridge.saveRoutingGraph.mockResolvedValue(documentFor());
  const container = document.createElement("div"),
    root = createRoot(container);
  try {
    await act(async () => root.render(<Harness />));
    await act(async () =>
      current.edit((draft) => ({
        ...draft,
        graph: {
          ...draft.graph,
          nodes: draft.graph.nodes.map((node) => ({
            ...node,
            max_attempts: 0,
          })),
        },
      })),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(bridge.saveRoutingGraph).toHaveBeenCalledTimes(1);
    expect(current.dirty).toBe(false);
    expect(current.draft.graph.nodes[0].max_attempts).toBeUndefined();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2400);
    });
    expect(bridge.saveRoutingGraph).toHaveBeenCalledTimes(1);
  } finally {
    await act(async () => root.unmount());
  }
});

it("preserves edits made while saving and uses the acknowledged version next", async () => {
  vi.useFakeTimers();
  bridge.getRoutingGraph.mockResolvedValue(documentFor());
  let acknowledge!: (value: GraphDocument) => void;
  const pending = new Promise<GraphDocument>((resolve) => {
    acknowledge = resolve;
  });
  bridge.saveRoutingGraph
    .mockImplementationOnce(() => pending)
    .mockResolvedValue(documentFor("newer", "version-three"));
  const container = document.createElement("div"),
    root = createRoot(container);
  const editModel = (model: string) =>
    current.edit((draft) => ({
      ...draft,
      graph: {
        ...draft.graph,
        nodes: draft.graph.nodes.map((node) => ({ ...node, model })),
      },
    }));
  try {
    await act(async () => root.render(<Harness />));
    await act(async () => editModel("first"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    await act(async () => editModel("newer"));
    await act(async () => acknowledge(documentFor("first", "version-two")));
    expect(current.draft.graph.nodes[0].model).toBe("newer");
    expect(current.dirty).toBe(true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(bridge.saveRoutingGraph).toHaveBeenCalledTimes(2);
    expect(bridge.saveRoutingGraph.mock.calls[1][2]).toBe("version-two");
    expect(current.draft.graph.nodes[0].model).toBe("newer");
    expect(current.dirty).toBe(false);
  } finally {
    await act(async () => root.unmount());
  }
});
