import { useRef, useState, type ReactNode } from "react";

import { cn } from "@/lib/utils";

export interface TrendPoint {
  at: number;
  value: number;
}

/** One run of samples taken back to back, holding its last value until `end`. */
export interface TrendSegment<P extends TrendPoint = TrendPoint> {
  points: P[];
  end: number;
}

/**
 * The runs of `points` inside the window ending at `nowMs`, oldest first.
 * Samples more than `gapMs` apart start a new run, so time nobody sampled
 * stays empty. A run holds each value until its next sample; the newest run
 * reaches `nowMs` while its latest sample is fresh. A run that began before
 * the window starts at the window's edge with the value it had there.
 */
export function trendSegments<P extends TrendPoint>(
  points: readonly P[],
  {
    gapMs,
    nowMs,
    windowMs,
  }: { gapMs: number; nowMs: number; windowMs: number },
): TrendSegment<P>[] {
  const start = nowMs - windowMs;
  const runs: TrendSegment<P>[] = [];
  let run: P[] = [];
  points.forEach((point, index) => {
    const previous = points[index - 1];
    if (previous && point.at - previous.at > gapMs) {
      runs.push({ points: run, end: previous.at });
      run = [];
    }
    run.push(point);
  });
  const last = run.at(-1);
  if (last) {
    runs.push({
      points: run,
      end: nowMs - last.at <= gapMs ? Math.max(nowMs, last.at) : last.at,
    });
  }
  return runs.flatMap((segment) => {
    if (segment.end <= start) return [];
    const inside = segment.points.filter((point) => point.at >= start);
    const before = segment.points.filter((point) => point.at < start).at(-1);
    const clipped = before ? [{ ...before, at: start }, ...inside] : inside;
    return clipped.length > 0 ? [{ points: clipped, end: segment.end }] : [];
  });
}

/** The sample the chart shows at `at`, or null where nothing was sampled. */
export function trendPointAt<P extends TrendPoint>(
  segments: readonly TrendSegment<P>[],
  at: number,
): P | null {
  for (const segment of segments) {
    const first = segment.points[0];
    if (!first || at < first.at || at > segment.end) continue;
    let current = first;
    for (const point of segment.points) {
      if (point.at > at) break;
      current = point;
    }
    return current;
  }
  return null;
}

const WIDTH = 1000;
const HEIGHT = 100;
// Room above the highest step for the stroke and the live dot.
const TOP_ROOM = 12;

/**
 * A live step chart of a count over a fixed window ending now. The highest
 * step sets a dashed guide labelled with its value, the newest value carries
 * a dot, and hovering reads out any sampled moment in place of `aside`.
 * `breakdown` shows what makes up the newest value beside the title, and
 * follows the pointer while it hovers.
 */
export function LiveTrendChart<P extends TrendPoint>({
  aside,
  breakdown,
  className,
  formatReadout,
  gapMs,
  label,
  nowMs,
  points,
  title,
  windowMs,
}: {
  /** Shown opposite the title, typically the span the window covers. */
  aside: ReactNode;
  /** What makes up a sample's value, such as who contributed to it. */
  breakdown?: (point: P) => ReactNode;
  className?: string;
  formatReadout: (point: TrendPoint) => ReactNode;
  gapMs: number;
  /** Read by assistive technology in place of the drawing. */
  label: string;
  nowMs: number;
  points: readonly P[];
  title: ReactNode;
  windowMs: number;
}) {
  const plotRef = useRef<HTMLDivElement>(null);
  const [hoverAt, setHoverAt] = useState<number | null>(null);
  const segments = trendSegments(points, { gapMs, nowMs, windowMs });
  const start = nowMs - windowMs;
  const peak = Math.max(
    0,
    ...segments.flatMap((segment) =>
      segment.points.map((point) => point.value),
    ),
  );
  // Small counts keep some headroom, so a single call does not fill the plot.
  const ceiling = Math.max(peak, 2);
  const x = (at: number) =>
    Math.min(WIDTH, Math.max(0, ((at - start) / windowMs) * WIDTH));
  const y = (value: number) => HEIGHT - (value / ceiling) * (HEIGHT - TOP_ROOM);
  const paths = segments.map((segment) => {
    const [first, ...rest] = segment.points;
    let line = `M${x(first.at)},${y(first.value)}`;
    for (const point of rest) line += `H${x(point.at)}V${y(point.value)}`;
    line += `H${x(segment.end)}`;
    return { key: first.at, line, area: `${line}V${HEIGHT}H${x(first.at)}Z` };
  });
  const newest = segments.at(-1);
  const live =
    newest && newest.end >= nowMs ? (newest.points.at(-1) ?? null) : null;
  const hovered = hoverAt === null ? null : trendPointAt(segments, hoverAt);
  const focused = hoverAt === null ? live : hovered;
  const percentY = (value: number) => `${(y(value) / HEIGHT) * 100}%`;

  return (
    <div
      className={cn(
        "grid min-w-0 grid-cols-[1rem_minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)] gap-x-1.5 gap-y-1",
        className,
      )}
      data-slot="live-trend-chart"
    >
      <div className="col-span-2 flex min-w-0 items-center justify-between gap-2 text-xs text-muted-foreground">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{title}</span>
          {focused && breakdown ? breakdown(focused) : null}
        </span>
        <span
          aria-hidden={hovered !== null || undefined}
          className={cn(
            "shrink-0 text-micro tabular-nums",
            hovered !== null && "text-text-secondary",
          )}
        >
          {hoverAt !== null && hovered !== null
            ? formatReadout({ at: hoverAt, value: hovered.value })
            : aside}
        </span>
      </div>
      <div aria-hidden="true" className="relative">
        {peak > 0 ? (
          <span
            className="absolute right-0 -translate-y-1/2 text-micro leading-none tabular-nums text-muted-foreground"
            style={{ top: percentY(peak) }}
          >
            {peak}
          </span>
        ) : null}
      </div>
      <div
        aria-label={label}
        className="relative min-h-0"
        data-slot="live-trend-plot"
        onPointerLeave={() => setHoverAt(null)}
        onPointerMove={(event) => {
          if (event.pointerType === "touch") return;
          const box = plotRef.current?.getBoundingClientRect();
          if (!box || box.width <= 0) return;
          const share = (event.clientX - box.left) / box.width;
          setHoverAt(start + Math.min(1, Math.max(0, share)) * windowMs);
        }}
        ref={plotRef}
        role="img"
      >
        <svg
          aria-hidden="true"
          className="absolute inset-0 size-full overflow-visible"
          preserveAspectRatio="none"
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        >
          <line
            stroke="var(--border)"
            vectorEffect="non-scaling-stroke"
            x1={0}
            x2={WIDTH}
            y1={HEIGHT}
            y2={HEIGHT}
          />
          {peak > 0 ? (
            <line
              stroke="var(--border)"
              strokeDasharray="3 3"
              vectorEffect="non-scaling-stroke"
              x1={0}
              x2={WIDTH}
              y1={y(peak)}
              y2={y(peak)}
            />
          ) : null}
          {paths.map((path) => (
            <g key={path.key}>
              <path d={path.area} fill="var(--primary)" fillOpacity={0.08} />
              <path
                d={path.line}
                fill="none"
                stroke="var(--primary)"
                strokeLinejoin="round"
                strokeWidth={1.5}
                vectorEffect="non-scaling-stroke"
              />
            </g>
          ))}
          {hoverAt !== null ? (
            <line
              stroke="var(--muted-foreground)"
              strokeOpacity={0.5}
              vectorEffect="non-scaling-stroke"
              x1={x(hoverAt)}
              x2={x(hoverAt)}
              y1={0}
              y2={HEIGHT}
            />
          ) : null}
        </svg>
        {live ? (
          <span
            aria-hidden="true"
            className="pointer-events-none absolute right-0 size-2 translate-x-1/2 -translate-y-1/2"
            data-slot="live-trend-dot"
            style={{ top: percentY(live.value) }}
          >
            <span className="absolute inset-0 rounded-full bg-primary opacity-40 motion-safe:animate-ping" />
            <span className="absolute inset-0.5 rounded-full bg-primary" />
          </span>
        ) : null}
      </div>
    </div>
  );
}
