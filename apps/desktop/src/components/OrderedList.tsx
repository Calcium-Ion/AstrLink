import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  animate,
  motion,
  useMotionValue,
  useReducedMotion,
} from "motion/react";
import { GripVertical, ArrowUp, ArrowDown } from "@/components/icons";
import { Button } from "./ui/button";
import { Panel } from "./Panel";
import { cn } from "@/lib/utils";
import { useT } from "../i18n";

/** Shared sortable list. The handle supports dragging and ArrowUp/ArrowDown. */
export function OrderedList<T extends { id: string }>({
  items,
  onChange,
  children,
  label,
  disabled = false,
  compact = false,
  positionOf,
}: {
  items: T[];
  onChange: (items: T[]) => void;
  children: (
    item: T,
    index: number,
    controls: ReactNode,
    sorting: boolean,
  ) => ReactNode;
  label: string;
  disabled?: boolean;
  compact?: boolean;
  positionOf?: (item: T) => number;
}) {
  const list = useRef<HTMLOListElement>(null);
  const pointer = useRef<{
    id: number;
    source: string;
    x: number;
    y: number;
    startY: number;
    grabOffset: number;
    handleOffset: number;
    startAnchor: number;
    needsAnchor: boolean;
    needsTarget: boolean;
    moved: boolean;
  } | null>(null);
  const frame = useRef<number | null>(null);
  const keyboardFocus = useRef<string | null>(null);
  const previewRef = useRef<T[] | null>(null);
  const [preview, setPreview] = useState<T[] | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  // Catalog loads and usage updates must not animate as user reorders.
  const [animationEpoch, setAnimationEpoch] = useState(0);
  const [lifted, setLifted] = useState<string | null>(null);
  const [slot, setSlot] = useState<{ top: number; height: number } | null>(
    null,
  );
  const [dragLayout, setDragLayout] = useState<{
    minHeight: number;
    paddingTop: number;
  } | null>(null);
  const landing = useRef<{ id: string; top: number } | null>(null);
  const settling = useRef<ReturnType<typeof animate> | null>(null);
  const dragY = useMotionValue(0);
  const reduceMotion = useReducedMotion();
  const displayed = preview ?? items;
  const orderKey = displayed.map((item) => item.id).join("\0");
  const itemsKey = items.map((item) => item.id).join("\0");
  const transition = reduceMotion
    ? { duration: 0 }
    : { type: "spring" as const, stiffness: 450, damping: 38 };
  const t = useT();
  const rowFor = (id: string) =>
    [...(list.current?.children ?? [])].find(
      (element) => (element as HTMLElement).dataset.orderedItem === id,
    ) as HTMLElement | undefined;
  const stop = (commit = false) => {
    const point = pointer.current;
    if (!point) return;
    const row = rowFor(point.source);
    if (point.moved && row) {
      landing.current = {
        id: point.source,
        top: row.getBoundingClientRect().top,
      };
    }
    if (frame.current !== null) cancelAnimationFrame(frame.current);
    frame.current = null;
    pointer.current = null;
    const next = previewRef.current;
    previewRef.current = null;
    setPreview(null);
    setDragging(null);
    setSlot(null);
    setDragLayout(null);
    if (point.moved) setAnimationEpoch((value) => value + 1);
    if (list.current?.hasPointerCapture(point.id))
      list.current.releasePointerCapture(point.id);
    if (
      commit &&
      next &&
      next.some((item, index) => item.id !== items[index]?.id)
    )
      onChange(next);
  };
  useEffect(
    () => () => {
      if (frame.current !== null) cancelAnimationFrame(frame.current);
      settling.current?.stop();
    },
    [],
  );
  useEffect(() => {
    stop();
  }, [disabled, itemsKey]);
  useEffect(() => {
    if (!dragging) return;
    const cancel = () => stop();
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") cancel();
    };
    window.addEventListener("keydown", escape);
    window.addEventListener("blur", cancel);
    return () => {
      window.removeEventListener("keydown", escape);
      window.removeEventListener("blur", cancel);
    };
  }, [dragging]);
  // Keep the grabbed point under the pointer as the DOM makes room for it.
  useLayoutEffect(() => {
    const point = pointer.current;
    if (point?.moved) {
      const row = rowFor(point.source);
      const handle = row?.querySelector("button");
      if (row && handle) {
        point.grabOffset =
          handle.getBoundingClientRect().top -
          row.getBoundingClientRect().top +
          point.handleOffset;
        if (point.needsAnchor) {
          point.needsAnchor = false;
          // Compact around the grabbed row without collapsing the scroll extent.
          const paddingTop = Math.max(
            0,
            point.startAnchor - row.offsetTop - point.grabOffset,
          );
          if (paddingTop > 0) {
            setDragLayout((current) => current && { ...current, paddingTop });
            return;
          }
        }
      }
      followPointer();
      if (point.needsTarget) {
        point.needsTarget = false;
        locateTarget();
      }
    } else if (landing.current && list.current) {
      const drop = landing.current;
      landing.current = null;
      const row = rowFor(drop.id);
      if (!row) {
        setLifted(null);
        return;
      }
      dragY.set(
        drop.top - list.current.getBoundingClientRect().top - row.offsetTop,
      );
      settling.current = animate(dragY, 0, {
        ...transition,
        onComplete: () => setLifted(null),
      });
    }
  }, [orderKey, dragging, dragLayout?.paddingTop]);
  useEffect(() => {
    if (disabled || !keyboardFocus.current) return;
    const id = keyboardFocus.current;
    keyboardFocus.current = null;
    if (document.activeElement === document.body) {
      const row = [...(list.current?.children ?? [])].find(
        (element) => (element as HTMLElement).dataset.orderedItem === id,
      );
      row
        ?.querySelector<HTMLButtonElement>("button")
        ?.focus({ preventScroll: true });
    }
  }, [disabled, items]);
  const followPointer = () => {
    const point = pointer.current;
    if (!point?.moved || !list.current) return;
    const row = rowFor(point.source);
    if (!row) return;
    dragY.set(
      point.y -
        point.grabOffset -
        list.current.getBoundingClientRect().top -
        row.offsetTop,
    );
    setSlot((current) =>
      current?.top === row.offsetTop && current.height === row.offsetHeight
        ? current
        : { top: row.offsetTop, height: row.offsetHeight },
    );
  };
  const locateTarget = () => {
    const point = pointer.current;
    const current = previewRef.current;
    if (!point?.moved || !list.current || !current) return;
    followPointer();
    const bounds = list.current.getBoundingClientRect();
    if (point.x < bounds.left || point.x > bounds.right) return;
    const source = rowFor(point.source);
    if (!source) return;
    // Read untransformed slots, so animating neighbours cannot move the hit target.
    const center =
      point.y - point.grabOffset + source.offsetHeight / 2 - bounds.top;
    const remaining = current.filter((item) => item.id !== point.source);
    let index = remaining.findIndex((item) => {
      const row = rowFor(item.id);
      return row && center < row.offsetTop + row.offsetHeight / 2;
    });
    if (index < 0) index = remaining.length;
    if (current[index]?.id === point.source) return;
    remaining.splice(
      index,
      0,
      current.find((item) => item.id === point.source)!,
    );
    previewRef.current = remaining;
    setAnimationEpoch((value) => value + 1);
    setPreview(remaining);
  };
  const scrollWhileDragging = () => {
    const point = pointer.current;
    if (!point) return;
    if (point.moved) {
      let scroller = list.current?.parentElement;
      while (
        scroller &&
        !/(auto|scroll)/.test(getComputedStyle(scroller).overflowY)
      )
        scroller = scroller.parentElement;
      if (scroller) {
        const bounds = scroller.getBoundingClientRect();
        if (point.x >= bounds.left && point.x <= bounds.right) {
          const delta =
            point.y < bounds.top + 32
              ? -8
              : point.y > bounds.bottom - 32
                ? 8
                : 0;
          if (delta) {
            scroller.scrollTop += delta;
            locateTarget();
          }
        }
      }
    }
    frame.current = requestAnimationFrame(scrollWhileDragging);
  };
  const move = (from: number, to: number) => {
    if (disabled || from < 0 || to < 0 || to >= items.length || from === to)
      return;
    const next = [...items];
    const [item] = next.splice(from, 1);
    next.splice(to, 0, item);
    setAnimationEpoch((value) => value + 1);
    onChange(next);
  };
  return (
    <ol
      ref={list}
      aria-label={label}
      data-sorting={!!dragging}
      className={cn(
        "relative isolate min-w-0 list-none p-0",
        !compact && "grid gap-2",
        dragging && "select-none cursor-grabbing",
      )}
      style={{ overflowAnchor: "none", ...dragLayout }}
      onPointerMove={(event) => {
        const point = pointer.current;
        if (!point || event.pointerId !== point.id) return;
        point.x = event.clientX;
        point.y = event.clientY;
        if (!point.moved && Math.abs(point.y - point.startY) > 4) {
          point.moved = true;
          previewRef.current = items;
          setPreview(items);
          setDragging(point.source);
          setLifted(point.source);
          setDragLayout({
            minHeight: list.current?.offsetHeight ?? 0,
            paddingTop: 0,
          });
          setAnimationEpoch((value) => value + 1);
          return;
        }
        locateTarget();
      }}
      onPointerUp={(event) => {
        if (event.pointerId === pointer.current?.id) stop(true);
      }}
      onPointerCancel={() => stop()}
      onLostPointerCapture={() => stop()}
    >
      {slot && (
        <li
          aria-hidden="true"
          data-drop-slot=""
          className="pointer-events-none absolute inset-x-0 rounded-md border-2 border-dashed border-primary/60 bg-primary/5 before:absolute before:-left-0.5 before:inset-y-1 before:w-1 before:rounded-full before:bg-primary"
          style={slot}
        />
      )}
      {displayed.map((item, index) => {
        // Preview rows occupy the original slots, including gaps in filtered lists.
        const position = positionOf?.(items[index] ?? item) ?? index + 1;
        const controls = (
          <div className={cn("flex items-center gap-1", !compact && "gap-2")}>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              disabled={disabled}
              className="size-7 shrink-0 touch-none cursor-grab active:cursor-grabbing"
              aria-label={`${t("services.reorder")} ${position}`}
              title={t("services.reorderKeys")}
              onKeyDown={(event) => {
                if (event.key === "Escape") {
                  stop();
                  return;
                }
                if (event.key === "ArrowUp" || event.key === "ArrowDown") {
                  event.preventDefault();
                  if (pointer.current) return;
                  keyboardFocus.current = item.id;
                  move(index, index + (event.key === "ArrowUp" ? -1 : 1));
                }
              }}
              onPointerDown={(event) => {
                if (
                  disabled ||
                  pointer.current ||
                  lifted ||
                  event.button !== 0 ||
                  !list.current
                )
                  return;
                const row = rowFor(item.id);
                if (!row) return;
                event.preventDefault();
                event.currentTarget.focus({ preventScroll: true });
                settling.current?.stop();
                dragY.set(0);
                pointer.current = {
                  id: event.pointerId,
                  source: item.id,
                  x: event.clientX,
                  y: event.clientY,
                  startY: event.clientY,
                  grabOffset: event.clientY - row.getBoundingClientRect().top,
                  handleOffset:
                    event.clientY -
                    event.currentTarget.getBoundingClientRect().top,
                  startAnchor:
                    row.offsetTop +
                    event.clientY -
                    row.getBoundingClientRect().top,
                  needsAnchor: true,
                  needsTarget: true,
                  moved: false,
                };
                list.current.setPointerCapture(event.pointerId);
                frame.current = requestAnimationFrame(scrollWhileDragging);
              }}
            >
              <GripVertical />
            </Button>
            <span className="text-xs tabular-nums text-muted-foreground">
              {position}
            </span>
            {!compact && (
              <div className="ml-auto flex gap-1">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={t("failure.moveUp")}
                  disabled={disabled || index === 0}
                  onClick={() => move(index, index - 1)}
                >
                  <ArrowUp />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={t("failure.moveDown")}
                  disabled={disabled || index === items.length - 1}
                  onClick={() => move(index, index + 1)}
                >
                  <ArrowDown />
                </Button>
              </div>
            )}
          </div>
        );
        return (
          <motion.li
            key={item.id}
            data-ordered-item={item.id}
            data-dragging={dragging === item.id || undefined}
            layout={lifted === item.id ? false : "position"}
            layoutDependency={animationEpoch}
            initial={false}
            transition={{ layout: transition }}
            style={lifted === item.id ? { y: dragY, zIndex: 20 } : undefined}
            className={cn(
              "relative",
              compact && "border-b last:border-b-0",
              lifted === item.id &&
                "rounded-md bg-card shadow-lg ring-1 ring-inset ring-primary/60",
              dragging && "pointer-events-none",
            )}
          >
            {compact ? (
              children(item, index, controls, !!dragging)
            ) : (
              <Panel tone="inset" className="grid gap-3 p-3">
                {controls}
                {children(item, index, controls, !!dragging)}
              </Panel>
            )}
          </motion.li>
        );
      })}
    </ol>
  );
}
