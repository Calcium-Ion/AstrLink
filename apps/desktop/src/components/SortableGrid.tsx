import {
  Fragment,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

import { GripVertical } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

const dragThreshold = 4;
const edgeScroll = 48;

export interface SortableGridHandle {
  /** A grip for keyboard moves: Arrow keys move the item one slot. */
  grip: ReactNode;
  dragging: boolean;
}

/**
 * A grid of equal slots reordered by dragging an item or by arrow keys on its
 * grip. While dragging, a copy of the card follows the pointer in a layer on
 * the body and its slot shows as a placeholder; the items reorder live in
 * their slots and nothing inside the scroller is transformed, so scrollers
 * around the grid keep their position in WebKit.
 * `after` renders a full-width row (an expanded tray) below an item's row;
 * during a drag it keeps that row, so the cards under the pointer stay put.
 */
export function SortableGrid<T extends { id: string }>({
  items,
  columns,
  label,
  disabled = false,
  itemLabel,
  onDragStart,
  onChange,
  renderItem,
  after,
  trailing,
  className,
  style,
}: {
  items: T[];
  columns: number;
  label: string;
  disabled?: boolean;
  itemLabel: (item: T, position: number) => string;
  onDragStart?: () => void;
  onChange: (items: T[]) => void;
  renderItem: (item: T, handle: SortableGridHandle) => ReactNode;
  after?: { id: string; content: ReactNode } | null;
  trailing?: ReactNode;
  className?: string;
  style?: CSSProperties;
}) {
  const t = useT();
  const grid = useRef<HTMLDivElement>(null);
  const [preview, setPreview] = useState<string[] | null>(null);
  const [active, setActive] = useState<string | null>(null);
  // Where the pointer-following copy sits, in viewport pixels.
  const [ghost, setGhost] = useState<{
    left: number;
    top: number;
    width: number;
    height: number;
  } | null>(null);
  const drag = useRef<{
    id: string;
    pointer: number;
    x: number;
    y: number;
    moved: boolean;
    order: string[];
    membership: string;
    offsetX: number;
    offsetY: number;
    width: number;
    height: number;
  } | null>(null);
  const focusAfterMove = useRef<string | null>(null);
  const byID = new Map(items.map((item) => [item.id, item]));
  const ids = items.map((item) => item.id);
  // A preview of items that were replaced mid-drag would hide the new ones;
  // until the drag is cancelled below, they show in their own order.
  const order =
    preview?.length === ids.length && preview.every((id) => byID.has(id))
      ? preview
      : ids;
  const shown = order
    .map((id) => byID.get(id))
    .filter((item): item is T => !!item);

  const cancel = () => {
    drag.current = null;
    setPreview(null);
    setActive(null);
    setGhost(null);
  };
  useEffect(() => {
    if (!active) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") cancel();
    };
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("blur", cancel);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("blur", cancel);
    };
  }, [active]);
  useEffect(() => {
    if (disabled) cancel();
  }, [disabled]);
  // Items can be replaced mid-drag, for example when packages are re-derived
  // under new IDs; a preview of the old set would hide the new items.
  const membership = [...ids].sort().join("\0");
  useEffect(() => {
    if (drag.current && drag.current.membership !== membership) cancel();
  }, [membership]);
  // A keyboard move disables the grips while the order saves, which drops
  // focus; it returns to the moved grip once they work again, unless the
  // user has gone elsewhere meanwhile.
  useLayoutEffect(() => {
    const id = focusAfterMove.current;
    if (!id || disabled) return;
    focusAfterMove.current = null;
    const focused = document.activeElement;
    if (
      focused &&
      focused !== document.body &&
      !grid.current?.contains(focused)
    )
      return;
    grid.current
      ?.querySelector<HTMLButtonElement>(
        `[data-grid-item="${CSS.escape(id)}"] [data-grid-grip]`,
      )
      ?.focus({ preventScroll: true });
  });

  const slotAt = (x: number, y: number, from: string[]) => {
    for (const element of grid.current?.querySelectorAll<HTMLElement>(
      "[data-grid-item]",
    ) ?? []) {
      const box = element.getBoundingClientRect();
      if (x >= box.left && x <= box.right && y >= box.top && y <= box.bottom)
        return from.indexOf(element.dataset.gridItem ?? "");
    }
    return -1;
  };
  const scrollNear = (y: number) => {
    let node = grid.current?.parentElement ?? null;
    while (node && !/(auto|scroll)/.test(getComputedStyle(node).overflowY))
      node = node.parentElement;
    if (!node) return;
    const box = node.getBoundingClientRect();
    if (y < box.top + edgeScroll) node.scrollTop -= 12;
    else if (y > box.bottom - edgeScroll) node.scrollTop += 12;
  };
  const start = (event: ReactPointerEvent<HTMLElement>, id: string) => {
    if (disabled || drag.current || event.button !== 0) return;
    const target = event.target as HTMLElement;
    // Controls inside a card keep their own clicks; the grip may drag.
    if (
      !target.closest("[data-grid-grip]") &&
      target.closest(
        "a,button,input,select,textarea,[role=switch],[data-no-drag]",
      )
    )
      return;
    const box = event.currentTarget.getBoundingClientRect();
    drag.current = {
      id,
      pointer: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      moved: false,
      order: ids,
      membership,
      offsetX: event.clientX - box.left,
      offsetY: event.clientY - box.top,
      width: box.width,
      height: box.height,
    };
    const move = (next: PointerEvent) => {
      const state = drag.current;
      if (!state || next.pointerId !== state.pointer) return;
      if (!state.moved) {
        if (
          Math.hypot(next.clientX - state.x, next.clientY - state.y) <
          dragThreshold
        )
          return;
        state.moved = true;
        window.getSelection()?.removeAllRanges();
        onDragStart?.();
        setActive(state.id);
      }
      setGhost({
        left: next.clientX - state.offsetX,
        top: next.clientY - state.offsetY,
        width: state.width,
        height: state.height,
      });
      scrollNear(next.clientY);
      const to = slotAt(next.clientX, next.clientY, state.order);
      const from = state.order.indexOf(state.id);
      if (to < 0 || to === from) return;
      const order = [...state.order];
      order.splice(from, 1);
      order.splice(to, 0, state.id);
      state.order = order;
      setPreview(order);
    };
    const stop = (end: PointerEvent) => {
      const state = drag.current;
      if (state && end.pointerId !== state.pointer) return;
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", stop);
      window.removeEventListener("pointercancel", stop);
      if (!state) return;
      const changed =
        end.type === "pointerup" &&
        state.moved &&
        state.order.some((value, index) => value !== ids[index]);
      if (state.moved) {
        // The release after a drag is not a click on the card below it.
        const swallow = (click: MouseEvent) => click.stopPropagation();
        window.addEventListener("click", swallow, {
          capture: true,
          once: true,
        });
        window.setTimeout(
          () => window.removeEventListener("click", swallow, true),
          0,
        );
      }
      cancel();
      if (changed)
        onChange(
          state.order
            .map((value) => byID.get(value))
            .filter((item): item is T => !!item),
        );
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", stop);
    window.addEventListener("pointercancel", stop);
  };

  const moveBy = (id: string, delta: number) => {
    const from = ids.indexOf(id);
    const to = from + delta;
    if (disabled || from < 0 || to < 0 || to >= ids.length) return;
    const order = [...items];
    const [item] = order.splice(from, 1);
    order.splice(to, 0, item!);
    focusAfterMove.current = id;
    onChange(order);
  };

  const cellCount = shown.length + (trailing ? 1 : 0);
  const afterIndex = (() => {
    if (!after) return -1;
    const index = (active ? ids : shown.map((item) => item.id)).indexOf(
      after.id,
    );
    if (index < 0) return -1;
    return Math.min((Math.floor(index / columns) + 1) * columns, cellCount) - 1;
  })();
  // One flat keyed list: a move reorders the cards' nodes instead of
  // remounting them, and the tray keeps its node while rows shift.
  const cells: ReactNode[] = shown.map((item, index) => {
    const position = index + 1;
    const grip = (
      <Button
        aria-label={itemLabel(item, position)}
        className="cursor-grab touch-none text-muted-foreground active:cursor-grabbing"
        data-grid-grip=""
        disabled={disabled}
        onKeyDown={(event) => {
          const delta = {
            ArrowLeft: -1,
            ArrowUp: -1,
            ArrowRight: 1,
            ArrowDown: 1,
          }[event.key];
          if (!delta) return;
          event.preventDefault();
          if (!drag.current) moveBy(item.id, delta);
        }}
        size="icon-xs"
        title={t("services.reorderCardKeys")}
        type="button"
        variant="ghost"
      >
        <GripVertical />
      </Button>
    );
    return (
      <div
        className="min-w-0"
        data-grid-item={item.id}
        key={`item:${item.id}`}
        onPointerDown={(event) => start(event, item.id)}
      >
        {renderItem(item, {
          grip,
          dragging: active === item.id,
        })}
      </div>
    );
  });
  if (trailing) cells.push(<Fragment key="trailing">{trailing}</Fragment>);
  if (afterIndex >= 0 && after)
    cells.splice(
      afterIndex + 1,
      0,
      <div
        className="col-span-full min-w-0"
        data-grid-tray={after.id}
        key={`tray:${after.id}`}
      >
        {after.content}
      </div>,
    );

  return (
    <div
      aria-label={label}
      className={cn("grid gap-3", active && "select-none", className)}
      data-sorting={active ? "true" : undefined}
      ref={grid}
      role="group"
      style={{
        gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`,
        ...style,
      }}
    >
      {cells}
      {active && ghost && byID.get(active)
        ? createPortal(
            <div
              aria-hidden="true"
              className="pointer-events-none fixed z-50 cursor-grabbing rounded-xl shadow-xl ring-1 ring-primary/40"
              data-grid-ghost=""
              inert
              style={ghost}
            >
              {renderItem(byID.get(active)!, { grip: null, dragging: false })}
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}
