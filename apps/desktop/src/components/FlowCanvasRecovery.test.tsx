// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ReactFlowProvider, useStoreApi, type Node } from "@xyflow/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { FlowCanvasRecovery } from "./FlowCanvasRecovery";

let root: Root;
let container: HTMLDivElement;
let store: ReturnType<typeof useStoreApi>;
const onReturn = vi.fn();
const node = (id: string, x = 100): Node => ({
  id,
  position: { x, y: 100 },
  measured: { width: 250, height: 150 },
  data: {},
});

function Harness() {
  store = useStoreApi();
  return <FlowCanvasRecovery label="回到内容" onReturn={onReturn} />;
}

beforeEach(() => {
  onReturn.mockReset();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});
async function render(nodes: Node[]) {
  await act(async () =>
    root.render(
      <ReactFlowProvider
        initialNodes={nodes}
        initialWidth={800}
        initialHeight={500}
      >
        <Harness />
      </ReactFlowProvider>,
    ),
  );
}
const button = () => container.querySelector("button");

it("offers a return only when content leaves the viewport and hides it after returning", async () => {
  await render([node("entry")]);
  expect(button()).toBeNull();
  const originalNodes = store.getState().nodes;
  await act(async () => store.setState({ transform: [-2000, -1200, 1] }));
  expect(button()?.textContent).toBe("回到内容");
  onReturn.mockImplementation(() => store.setState({ transform: [0, 0, 1] }));
  await act(async () => button()!.click());
  expect(onReturn).toHaveBeenCalledOnce();
  expect(button()).toBeNull();
  expect(store.getState().nodes).toBe(originalNodes);
});

it("uses individual nodes, zoom, partial visibility and viewport size", async () => {
  await render([node("a"), node("b", 4000)]);
  // Being inside the combined bounds does not mean a node is visible.
  await act(async () => store.setState({ transform: [-2000, 0, 1] }));
  expect(button()).not.toBeNull();
  await act(async () => store.getState().setNodes([node("a")]));
  for (const [x, zoom, outside] of [
    [-340, 1, false],
    [-350, 1, true],
    [-100, 0.25, true],
    [-75, 0.25, false],
  ] as const) {
    await act(async () => store.setState({ transform: [x, 0, zoom] }));
    expect(button() !== null).toBe(outside);
  }
  await act(async () => store.setState({ transform: [0, 0, 1], height: 100 }));
  expect(button()).not.toBeNull();
  await act(async () => store.setState({ height: 500 }));
  expect(button()).toBeNull();
  await act(async () => store.setState({ width: 0 }));
  expect(button()).toBeNull();
});

it("does not offer a return for empty, hidden or unmeasured content", async () => {
  await render([]);
  await act(async () => store.setState({ transform: [-2000, 0, 1] }));
  expect(button()).toBeNull();
  await act(async () =>
    store.getState().setNodes([{ ...node("hidden"), hidden: true }]),
  );
  expect(button()).toBeNull();
  await act(async () =>
    store.getState().setNodes([{ ...node("loading"), measured: {} }]),
  );
  expect(button()).toBeNull();
  await act(async () => store.getState().setNodes([node("ready")]));
  expect(button()).not.toBeNull();
  await act(async () => store.getState().setNodes([]));
  expect(button()).toBeNull();
});
