import {
  useLayoutEffect,
  useRef,
  useState,
  type ComponentProps,
  type ReactNode,
} from "react";
import { cn } from "@/lib/utils";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

export interface ActivityCell {
  key: string;
  date: string;
  label: string;
  value: number;
  detail: ReactNode;
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
const MIN_CELL = 8;
const SHORT_RANGE_CELL_SIZE = 14;

/** Consecutive days, Monday-first. Narrow calendars wrap at week boundaries. */
export function ActivityHeatmap({
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
  const [width, setWidth] = useState(768);
  const refs = useRef(new Map<string, HTMLButtonElement>());
  const frame = useRef<HTMLDivElement>(null);
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
  const capacity = Math.max(
    1,
    Math.floor((width - AXIS_WIDTH) / (MIN_CELL + GAP)),
  );
  const bands = Math.ceil(columns / capacity);
  const weeksPerBand = Math.ceil(columns / bands);
  const availableCellSize =
    (width - AXIS_WIDTH - weeksPerBand * GAP) / weeksPerBand;
  const cellSize = Math.max(
    1,
    // A full year fills the panel; short ranges keep compact square cells.
    cells.length > 90
      ? availableCellSize
      : Math.min(SHORT_RANGE_CELL_SIZE, availableCellSize),
  );
  const monthFormat = new Intl.DateTimeFormat(locale, { month: "short" });
  const weekdayFormat = new Intl.DateTimeFormat(locale, { weekday: "short" });

  return (
    <div
      ref={frame}
      className="grid min-w-0 gap-4"
      data-slot="activity-heatmap"
    >
      <div className="grid min-w-0 gap-4 py-1" role="group" aria-label={label}>
        <TooltipProvider delayDuration={150} disableHoverableContent>
          {Array.from({ length: bands }, (_, band) => {
            const firstWeek = band * weeksPerBand;
            const bandColumns = Math.min(weeksPerBand, columns - firstWeek);
            const firstIndex = Math.max(0, firstWeek * 7 - offset);
            const lastIndex = Math.min(
              cells.length,
              (firstWeek + bandColumns) * 7 - offset,
            );
            const bandCells = cells.slice(firstIndex, lastIndex);
            const monthLabels = new Map<number, string>();
            bandCells.forEach((cell, index) => {
              const date = dateOf(cell.date);
              if (index === 0 || date.getDate() === 1) {
                const column =
                  Math.floor((firstIndex + index + offset) / 7) - firstWeek;
                if (column > 0 && column < 3) monthLabels.delete(0);
                if (column === 0 || bandColumns - column >= 3)
                  monthLabels.set(column, monthFormat.format(date));
              }
            });
            return (
              <div
                key={band}
                data-slot="activity-calendar"
                className="grid"
                style={{
                  gap: GAP,
                  gridTemplateColumns: `${AXIS_WIDTH}px repeat(${bandColumns}, ${cellSize}px)`,
                  gridTemplateRows: `20px repeat(7, ${cellSize}px)`,
                }}
              >
                {[...monthLabels].map(([column, month]) => (
                  <span
                    aria-hidden="true"
                    key={`month-${column}`}
                    className="whitespace-nowrap text-micro text-muted-foreground"
                    style={{ gridColumn: column + 2, gridRow: 1 }}
                  >
                    {month}
                  </span>
                ))}
                {Array.from({ length: 7 }, (_, day) => (
                  <span
                    aria-hidden="true"
                    key={`weekday-${day}`}
                    className="flex items-center text-micro leading-none text-muted-foreground"
                    style={{ gridColumn: 1, gridRow: day + 2 }}
                  >
                    {day < 6 && day % 2 === 0
                      ? weekdayFormat.format(new Date(2026, 0, 5 + day))
                      : ""}
                  </span>
                ))}
                {bandCells.map((cell, localIndex) => {
                  const index = firstIndex + localIndex;
                  const level =
                    cell.value === 0
                      ? 0
                      : Math.max(1, Math.ceil((cell.value / max) * 4));
                  return (
                    <ActivityDay
                      key={cell.key}
                      cell={cell}
                      className={LEVELS[level]}
                      data-level={level}
                      onFocus={() => setActiveKey(cell.key)}
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
                        // Explicit keyboard navigation can move to another calendar band.
                        if (
                          Math.floor(
                            (targetIndex + offset) / 7 / weeksPerBand,
                          ) !== band
                        )
                          button?.scrollIntoView({
                            block: "nearest",
                            inline: "nearest",
                          });
                      }}
                      ref={(node) => {
                        if (node) refs.current.set(cell.key, node);
                        else refs.current.delete(cell.key);
                      }}
                      style={{
                        gridColumn:
                          Math.floor((index + offset) / 7) - firstWeek + 2,
                        gridRow: ((index + offset) % 7) + 2,
                      }}
                      tabIndex={index === activeIndex ? 0 : -1}
                    />
                  );
                })}
              </div>
            );
          })}
        </TooltipProvider>
      </div>
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
}

/** Hover state stays local to the day; it never changes the calendar's geometry. */
function ActivityDay({
  cell,
  className,
  ...props
}: ComponentProps<"button"> & { cell: ActivityCell }) {
  const [open, setOpen] = useState(false);
  return (
    <Tooltip open={open} onOpenChange={setOpen}>
      <TooltipTrigger asChild>
        <button
          {...props}
          aria-label={cell.label}
          className={cn(
            "min-w-0 rounded-sm border border-foreground/5 hover:border-success-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring",
            className,
          )}
          data-date={cell.date}
          data-slot="activity-cell"
          onClick={(event) => {
            event.preventDefault();
            setOpen(true);
          }}
          type="button"
        />
      </TooltipTrigger>
      <TooltipContent
        animated={false}
        side="top"
        sideOffset={8}
        className="pointer-events-none max-w-[min(320px,calc(100vw-24px))]"
        aria-live="off"
      >
        {cell.detail}
      </TooltipContent>
    </Tooltip>
  );
}
