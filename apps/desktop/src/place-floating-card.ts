const DEFAULT_PAD = 8;
const DEFAULT_GAP = 8;

/**
 * Place a fixed card next to an anchor so it stays inside the viewport.
 * Prefers centered-above; flips left/right or below when a side would clip.
 * `left` is set in CSS pixels so the box is not shrink-wrapped against the
 * remaining viewport (which is what turns an edge tooltip into a strip).
 */
export function placeFloatingCard({
  anchorX,
  anchorY,
  gap = DEFAULT_GAP,
  height,
  padding = DEFAULT_PAD,
  viewportHeight,
  viewportWidth,
  width,
}: {
  anchorX: number;
  anchorY: number;
  gap?: number;
  height: number;
  padding?: number;
  viewportHeight: number;
  viewportWidth: number;
  width: number;
}): { left: number; top: number } {
  const maxLeft = Math.max(padding, viewportWidth - width - padding);
  let left = anchorX - width / 2;
  if (left + width > viewportWidth - padding) {
    left = anchorX - width - gap;
  }
  if (left < padding) {
    left = anchorX + gap;
  }
  left = Math.min(Math.max(left, padding), maxLeft);

  const maxTop = Math.max(padding, viewportHeight - height - padding);
  let top = anchorY - height - gap;
  if (top < padding) {
    top = anchorY + gap;
  }
  top = Math.min(Math.max(top, padding), maxTop);

  return { left, top };
}
