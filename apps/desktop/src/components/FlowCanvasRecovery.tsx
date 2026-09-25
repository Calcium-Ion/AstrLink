import {
  Panel as FlowPanel,
  useStore,
  type ReactFlowState,
} from "@xyflow/react";
import { MapPin } from "./icons";
import { Button } from "./ui/button";

function contentIsOffscreen({
  width,
  height,
  transform,
  nodeLookup,
}: ReactFlowState) {
  if (width <= 0 || height <= 0) return false;
  const [offsetX, offsetY, zoom] = transform;
  let hasContent = false;
  // Check each node: the viewport can sit in empty space between distant nodes.
  for (const node of nodeLookup.values()) {
    if (node.hidden) continue;
    const nodeWidth = node.measured.width ?? node.width;
    const nodeHeight = node.measured.height ?? node.height;
    if (!nodeWidth || !nodeHeight) return false;
    hasContent = true;
    const left = node.internals.positionAbsolute.x * zoom + offsetX;
    const top = node.internals.positionAbsolute.y * zoom + offsetY;
    if (
      left < width &&
      left + nodeWidth * zoom > 0 &&
      top < height &&
      top + nodeHeight * zoom > 0
    )
      return false;
  }
  return hasContent;
}

export function FlowCanvasRecovery({
  label,
  onReturn,
}: {
  label: string;
  onReturn: () => void;
}) {
  // Only rerender when visibility changes, not on every frame of a pan or zoom.
  const offscreen = useStore(contentIsOffscreen);
  if (!offscreen) return null;
  return (
    <FlowPanel position="bottom-center" className="mb-10!">
      <Button type="button" className="shadow-md" onClick={onReturn}>
        <MapPin aria-hidden="true" className="size-3.5" />
        {label}
      </Button>
    </FlowPanel>
  );
}
