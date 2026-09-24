import {
  memo,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { cn } from "@/lib/utils";
import { AnchoredTooltip } from "@/components/ui/tooltip";
import { ScrollArea } from "@/components/ui/scroll-area";

export interface ActivityCell {
  key: string;
  date: string;
  label: string;
  value: number;
  detail: ReactNode | (() => ReactNode);
}

const LEVELS = [
  "bg-muted",
  "bg-success/20",
  "bg-success/45",
  "bg-success/70",
  "bg-success",
] as const;
const AXIS_WIDTH = 28;
const GAP = 3;
const MIN_CELL = 14;
const MAX_CELL = 18;
const SHORT_RANGE_CELL_SIZE = 14;

/** Consecutive days, Monday-first. Narrow calendars scroll to recent weeks. */
export const ActivityHeatmap = memo(function ActivityHeatmap({
  cells,
  label,
  lessLabel,
  moreLabel,
  emptyLabel,
  caption,
  locale,
}: {
  cells: ActivityCell[];
  label: string;
  lessLabel: string;
  moreLabel: string;
  emptyLabel: string;
  caption: string;
  locale: string;
}) {
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [tooltipKey, setTooltipKey] = useState<string | null>(null);
  const tooltipId = useId();
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const tooltipShown = useRef(false);
  const skipDelayUntil = useRef(0);
  const cancelHover = useCallback(() => {
    if (hoverTimer.current !== null) clearTimeout(hoverTimer.current);
    hoverTimer.current = null;
  }, []);
  const showTooltip = useCallback(
    (key: string) => {
      cancelHover();
      tooltipShown.current = true;
      setTooltipKey(key);
    },
    [cancelHover],
  );
  const dismissTooltip = useCallback(() => {
    cancelHover();
    // Match the existing provider's 300ms grace period between nearby cells.
    if (tooltipShown.current) skipDelayUntil.current = Date.now() + 300;
    tooltipShown.current = false;
    setTooltipKey(null);
  }, [cancelHover]);
  useEffect(() => cancelHover, [cancelHover]);
  const [width, setWidth] = useState(768);
  const refs = useRef(new Map<string, HTMLButtonElement>());
  const frame = useRef<HTMLDivElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const calendar = useRef<HTMLDivElement>(null);
  const selectedIndex = cells.findIndex((cell) => cell.key === activeKey);
  const activeIndex = selectedIndex < 0 ? cells.length - 1 : selectedIndex;
  useLayoutEffect(() => {
    const node = frame.current;
    if (!node) return;
    const measureWidth = () => {
      const next = Math.floor(node.getBoundingClientRect().width);
      // Height changes and tooltips must never feed back into calendar layout.
      if (next > 0) setWidth((current) => (current === next ? current : next));
    };
    measureWidth();
    const observer = new ResizeObserver(measureWidth);
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  const max = Math.max(0, ...cells.map((cell) => cell.value));
  const dateOf = (date: string) => new Date(`${date}T12:00:00`);
  const offset = cells.length ? (dateOf(cells[0].date).getDay() + 6) % 7 : 0;
  const columns = Math.max(1, Math.ceil((cells.length + offset) / 7));
  const availableCellSize = (width - AXIS_WIDTH - columns * GAP) / columns;
  const cellSize = Math.max(
    MIN_CELL,
    // Keep a single calendar with readable cells; overflow scrolls locally.
    Math.min(
      cells.length > 90 ? MAX_CELL : SHORT_RANGE_CELL_SIZE,
      availableCellSize,
    ),
  );
  const monthFormat = useMemo(
    () => new Intl.DateTimeFormat(locale, { month: "short" }),
    [locale],
  );
  const weekdayFormat = useMemo(
    () => new Intl.DateTimeFormat(locale, { weekday: "short" }),
    [locale],
  );
  const tooltipCell = cells.find((cell) => cell.key === tooltipKey);
  const tooltipAnchor = tooltipKey ? refs.current.get(tooltipKey) : undefined;
  const firstKey = cells[0]?.key;
  const lastKey = cells[cells.length - 1]?.key;
  useLayoutEffect(() => {
    const node = viewport.current;
    const content = calendar.current;
    if (!node || !content) return;
    const showLatest = () => {
      node.scrollLeft = Math.max(0, node.scrollWidth - node.clientWidth);
    };
    showLatest();
    // Observe the actual scrollport and content after layout: shrinking a
    // window must hide older weeks on the left, never the newest on the right.
    const observer = new ResizeObserver(showLatest);
    observer.observe(node);
    observer.observe(content);
    return () => observer.disconnect();
  }, [firstKey, lastKey]);

  const revealCell = (button: HTMLButtonElement) => {
    const node = viewport.current;
    if (!node) return;
    const view = node.getBoundingClientRect();
    const cell = button.getBoundingClientRect();
    // Move only the calendar, preserving the overview's vertical position.
    if (cell.left < view.left) node.scrollLeft += cell.left - view.left;
    else if (cell.right > view.right)
      node.scrollLeft += cell.right - view.right;
  };
  const monthLabels = new Map<number, string>();
  cells.forEach((cell, index) => {
    const date = dateOf(cell.date);
    if (index === 0 || date.getDate() === 1) {
      const column = Math.floor((index + offset) / 7);
      if (column > 0 && column < 3) monthLabels.delete(0);
      if (column === 0 || columns - column >= 3)
        monthLabels.set(column, monthFormat.format(date));
    }
  });

  return (
    <div
      ref={frame}
      className="grid min-w-0 gap-4"
      data-slot="activity-heatmap"
    >
      <div
        className="grid min-w-0 py-1"
        style={{
          gridTemplateColumns: `${AXIS_WIDTH}px minmax(0, 1fr)`,
          gap: GAP,
        }}
        role="group"
        aria-label={label}
      >
        <div
          aria-hidden="true"
          className="grid"
          style={{
            gap: GAP,
            gridTemplateRows: `20px repeat(7, ${cellSize}px)`,
          }}
        >
          {Array.from({ length: 7 }, (_, day) => (
            <span
              key={`weekday-${day}`}
              className="flex items-center text-micro leading-none text-muted-foreground"
              style={{ gridRow: day + 2 }}
            >
              {day < 6 && day % 2 === 0
                ? weekdayFormat.format(new Date(2026, 0, 5 + day))
                : ""}
            </span>
          ))}
        </div>
        <ScrollArea
          className="min-w-0"
          orientation="horizontal"
          viewportProps={{
            ref: viewport,
            onScroll: cancelHover,
          }}
        >
          <div
            ref={calendar}
            data-slot="activity-calendar"
            className="grid w-max pb-3"
            style={{
              gap: GAP,
              gridTemplateColumns: `repeat(${columns}, ${cellSize}px)`,
              gridTemplateRows: `20px repeat(7, ${cellSize}px)`,
            }}
          >
            {[...monthLabels].map(([column, month]) => (
              <span
                aria-hidden="true"
                key={`month-${column}`}
                className="whitespace-nowrap text-micro text-muted-foreground"
                style={{ gridColumn: column + 1, gridRow: 1 }}
              >
                {month}
              </span>
            ))}
            {cells.map((cell, index) => {
              const level =
                cell.value === 0
                  ? 0
                  : Math.max(1, Math.ceil((cell.value / max) * 4));
              return (
                <button
                  key={cell.key}
                  type="button"
                  aria-label={cell.label}
                  aria-describedby={
                    tooltipKey === cell.key ? tooltipId : undefined
                  }
                  data-date={cell.date}
                  data-slot="activity-cell"
                  className={cn(
                    "min-w-0 rounded-sm border border-foreground/5 hover:border-success-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring",
                    LEVELS[level],
                  )}
                  data-level={level}
                  onPointerEnter={(event) => {
                    if (event.pointerType === "touch") return;
                    cancelHover();
                    if (
                      tooltipShown.current ||
                      Date.now() < skipDelayUntil.current
                    ) {
                      showTooltip(cell.key);
                    } else {
                      hoverTimer.current = setTimeout(
                        () => showTooltip(cell.key),
                        150,
                      );
                    }
                  }}
                  onPointerLeave={dismissTooltip}
                  onFocus={(event) => {
                    revealCell(event.currentTarget);
                    setActiveKey(cell.key);
                    showTooltip(cell.key);
                  }}
                  onBlur={dismissTooltip}
                  onClick={() => showTooltip(cell.key)}
                  onKeyDown={(event) => {
                    const steps: Record<string, number> = {
                      ArrowLeft: -7,
                      ArrowRight: 7,
                      ArrowUp: -1,
                      ArrowDown: 1,
                    };
                    let next = index;
                    if (event.key === "Home") next = 0;
                    else if (event.key === "End") next = cells.length - 1;
                    else if (event.key in steps) next += steps[event.key];
                    else return;
                    event.preventDefault();
                    const targetIndex = Math.max(
                      0,
                      Math.min(cells.length - 1, next),
                    );
                    const button = refs.current.get(cells[targetIndex].key);
                    button?.focus({ preventScroll: true });
                  }}
                  ref={(node) => {
                    if (node) refs.current.set(cell.key, node);
                    else refs.current.delete(cell.key);
                  }}
                  style={{
                    gridColumn: Math.floor((index + offset) / 7) + 1,
                    gridRow: ((index + offset) % 7) + 2,
                  }}
                  tabIndex={index === activeIndex ? 0 : -1}
                />
              );
            })}
          </div>
        </ScrollArea>
      </div>
      {tooltipCell && tooltipAnchor ? (
        <AnchoredTooltip
          anchor={tooltipAnchor}
          id={tooltipId}
          onDismiss={dismissTooltip}
          scrollContainer={viewport.current}
        >
          {typeof tooltipCell.detail === "function"
            ? tooltipCell.detail()
            : tooltipCell.detail}
        </AnchoredTooltip>
      ) : null}
      <div
        className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 border-t pt-3 text-xs text-muted-foreground"
        data-slot="activity-footer"
      >
        <span data-slot="activity-detail">
          {max === 0 ? emptyLabel : caption}
        </span>
        <div
          className="ml-auto flex shrink-0 items-center gap-1.5"
          aria-label={`${lessLabel} → ${moreLabel}`}
        >
          <span className="mr-1 text-micro">{lessLabel}</span>
          {LEVELS.map((level) => (
            <span
              aria-hidden="true"
              className={cn(
                "size-3 rounded-sm border border-foreground/5",
                level,
              )}
              key={level}
            />
          ))}
          <span className="ml-1 text-micro">{moreLabel}</span>
        </div>
      </div>
    </div>
  );
});
