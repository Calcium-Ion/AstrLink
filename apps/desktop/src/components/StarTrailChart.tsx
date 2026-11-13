import {
  memo,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent,
  type ReactNode,
} from "react";

import { AnchoredTooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

export interface StarTrailChartArc {
  /** Unique in the chart. */
  key: string;
  /** 0 is the innermost ring. */
  ring: number;
  /** Local hour, 0–23; 0 sits at the top and hours run clockwise. */
  hour: number;
  color: string;
  /** 0–1; brighter arcs had more requests. */
  brightness: number;
  /** Names the hour for assistive technology. */
  label: string;
  detail: ReactNode | (() => ReactNode);
}

const SIZE = 264;
const CENTER = SIZE / 2;
const OUTER = 107;
const INNER = 22;
const MAX_STEP = 8.5;
const EDGE = OUTER + 6;
const HOUR_DEGREES = 15;

/** Ring radius and stroke widths for `rings` rings. */
export function starTrailGeometry(rings: number) {
  const step =
    rings > 1 ? Math.min(MAX_STEP, (OUTER - INNER) / (rings - 1)) : MAX_STEP;
  const width = Math.min(5, Math.max(1.6, step * 0.58));
  const todayWidth = width + Math.min(0.9, step * 0.25);
  const radius = (ring: number) => OUTER - (rings - 1 - ring) * step;
  return { step, width, todayWidth, radius };
}

function point(radius: number, degrees: number): [number, number] {
  const angle = (degrees * Math.PI) / 180;
  return [CENTER + radius * Math.sin(angle), CENTER - radius * Math.cos(angle)];
}

function arcPath(radius: number, from: number, to: number): string {
  const [x0, y0] = point(radius, from);
  const [x1, y1] = point(radius, to);
  const large = to - from > 180 ? 1 : 0;
  return `M${x0.toFixed(2)},${y0.toFixed(2)}A${radius},${radius} 0 ${large} 1 ${x1.toFixed(2)},${y1.toFixed(2)}`;
}

/** One hour's arc, leaving a gap the round caps do not close. */
function hourArc(radius: number, hour: number, width: number) {
  const pad = Math.min(
    HOUR_DEGREES / 2 - 0.75,
    (((width / 2 + 0.8) / radius) * 180) / Math.PI,
  );
  const from = hour * HOUR_DEGREES + pad;
  const to = (hour + 1) * HOUR_DEGREES - pad;
  return { from, to, mid: (from + to) / 2 };
}

/** Re-renders each minute so the progress arc keeps up with the clock. */
function useMinuteClock(fixed: Date | undefined): Date {
  const [now, setNow] = useState(() => fixed ?? new Date());
  useEffect(() => {
    if (fixed) {
      setNow(fixed);
      return;
    }
    let timer: ReturnType<typeof setTimeout>;
    const tick = () => {
      const current = new Date();
      setNow(current);
      timer = setTimeout(tick, 60_000 - (current.getTime() % 60_000) + 50);
    };
    tick();
    return () => clearTimeout(timer);
  }, [fixed]);
  return now;
}

interface ArcShape {
  arc: StarTrailChartArc;
  path: string;
  width: number;
  left: number;
  top: number;
}

/**
 * The lit hours; hover, focus and the clock leave them alone. Hours that share
 * a color, width and brightness step draw as one path, so a busy month stays a
 * few dozen elements. Lit hours never overlap, so merging them changes nothing
 * on screen; `data-keys` names the hours in each path.
 */
const ArcLayer = memo(function ArcLayer({
  shapes,
}: {
  shapes: readonly ArcShape[];
}) {
  const looks = new Map<
    string,
    { color: string; width: number; step: number; keys: string[]; d: string[] }
  >();
  for (const { arc, path, width } of shapes) {
    // Steps of 0.05 opacity look the same.
    const step = Math.round(Math.min(1, Math.max(0, arc.brightness)) * 14);
    const id = `${arc.color}\u0000${width}\u0000${step}`;
    let look = looks.get(id);
    if (!look) {
      look = { color: arc.color, width, step, keys: [], d: [] };
      looks.set(id, look);
    }
    look.keys.push(arc.key);
    look.d.push(path);
  }
  return (
    <g>
      {[...looks].map(([id, { color, width, step, keys, d }]) => (
        <path
          d={d.join("")}
          data-keys={keys.join(" ")}
          data-slot="star-trail-arc"
          fill="none"
          key={id}
          stroke={color}
          strokeLinecap="round"
          strokeOpacity={(6 + step) / 20}
          strokeWidth={width}
        />
      ))}
    </g>
  );
});

/** Hour ticks and the 0, 6, 12 and 18 o'clock labels. */
const Dial = memo(function Dial({
  hourLabels,
}: {
  hourLabels: readonly [string, string, string, string];
}) {
  return (
    <>
      {Array.from({ length: 24 }, (_, hour) => {
        const major = hour % 6 === 0;
        const [x0, y0] = point(EDGE + 3, hour * HOUR_DEGREES);
        const [x1, y1] = point(EDGE + (major ? 8 : 5.5), hour * HOUR_DEGREES);
        return (
          <line
            key={hour}
            stroke="var(--muted-foreground)"
            strokeOpacity={major ? 0.6 : 0.3}
            strokeWidth={major ? 1.3 : 1}
            x1={x0}
            x2={x1}
            y1={y0}
            y2={y1}
          />
        );
      })}
      {hourLabels.map((text, index) => {
        const [x, y] = point(EDGE + 15, index * 90);
        return (
          <text
            dominantBaseline="central"
            fill="var(--muted-foreground)"
            fontSize={10}
            key={index}
            textAnchor="middle"
            x={x}
            y={y}
          >
            {text}
          </text>
        );
      })}
    </>
  );
});

/**
 * One invisible button per lit hour for keyboard and assistive technology.
 * The chart handles their focus and keys by delegation and moves the one tab
 * stop itself, so hovering and focusing never render them again.
 */
const HourButtons = memo(function HourButtons({
  buttons,
  shapes,
}: {
  buttons: Map<string, HTMLButtonElement>;
  shapes: readonly ArcShape[];
}) {
  return shapes.map(({ arc, left, top }) => (
    <button
      aria-label={arc.label}
      className="pointer-events-none absolute size-1 -translate-x-1/2 -translate-y-1/2 opacity-0"
      data-key={arc.key}
      data-slot="star-trail-hour"
      key={arc.key}
      ref={(node) => {
        if (!node) return;
        buttons.set(arc.key, node);
        return () => {
          buttons.delete(arc.key);
        };
      }}
      style={{ left: `${left}%`, top: `${top}%` }}
      tabIndex={-1}
      type="button"
    />
  ));
});

/**
 * Rings, such as days with today outermost or models with the busiest
 * outermost, and hours around the dial from 0 at the top. A lit arc marks an
 * hour with requests. The outside arc is the part of today that has passed.
 * Arcs take one tab stop; arrow keys move between hours and rings, and
 * hovering or focusing an arc shows its detail.
 */
export const StarTrailChart = memo(function StarTrailChart({
  arcs,
  className,
  label,
  now: fixedNow,
  rings,
  summary,
  today,
  hourLabels,
}: {
  arcs: StarTrailChartArc[];
  className?: string;
  label: string;
  /** Fixed clock for tests; the chart follows the system clock otherwise. */
  now?: Date;
  rings: number;
  /** A sentence that sums the chart up for assistive technology. */
  summary: string;
  /**
   * Whether the chart reaches today, as `(now) => fraction` of it that has
   * passed; null hides the progress arc and the now dot.
   */
  today: (now: Date) => number | null;
  /** Labels for 0, 6, 12 and 18 o'clock. */
  hourLabels: readonly [string, string, string, string];
}) {
  const now = useMinuteClock(fixedNow);
  const summaryId = useId();
  const tooltipId = useId();
  const svgRef = useRef<SVGSVGElement>(null);
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [tooltipKey, setTooltipKey] = useState<string | null>(null);
  const [focusedKey, setFocusedKey] = useState<string | null>(null);
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const tooltipShown = useRef(false);

  const geometry = useMemo(() => starTrailGeometry(rings), [rings]);
  const shapes = useMemo(
    () =>
      arcs.map((arc): ArcShape => {
        const radius = geometry.radius(arc.ring);
        const width =
          arc.ring === rings - 1 ? geometry.todayWidth : geometry.width;
        const { from, to, mid } = hourArc(radius, arc.hour, width);
        const [x, y] = point(radius, mid);
        return {
          arc,
          path: arcPath(radius, from, to),
          width,
          left: (x / SIZE) * 100,
          top: (y / SIZE) * 100,
        };
      }),
    [arcs, geometry, rings],
  );
  const byPlace = useMemo(
    () => new Map(arcs.map((arc) => [`${arc.ring}:${arc.hour}`, arc])),
    [arcs],
  );
  const index = useMemo(
    () => new Map(arcs.map((arc, at) => [arc.key, at])),
    [arcs],
  );
  const activeIndex = activeKey !== null ? (index.get(activeKey) ?? -1) : -1;
  const tabIndexAt = activeIndex >= 0 ? activeIndex : arcs.length - 1;

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
    tooltipShown.current = false;
    setTooltipKey(null);
  }, [cancelHover]);
  useEffect(() => cancelHover, [cancelHover]);

  /** The lit arc under the pointer, by ring radius and clock angle. */
  const arcAt = (event: PointerEvent): StarTrailChartArc | null => {
    const svg = svgRef.current;
    if (!svg || rings === 0) return null;
    const box = svg.getBoundingClientRect();
    if (box.width === 0) return null;
    const scale = SIZE / box.width;
    const x = (event.clientX - box.left) * scale - CENTER;
    const y = (event.clientY - box.top) * scale - CENTER;
    const distance = Math.hypot(x, y);
    const ring = rings - 1 - Math.round((OUTER - distance) / geometry.step);
    if (ring < 0 || ring >= rings) return null;
    const reach = Math.max(geometry.step / 2, geometry.todayWidth / 2 + 2);
    if (Math.abs(distance - geometry.radius(ring)) > reach) return null;
    const degrees = ((Math.atan2(x, -y) * 180) / Math.PI + 360) % 360;
    const hour = Math.min(23, Math.floor(degrees / HOUR_DEGREES));
    return byPlace.get(`${ring}:${hour}`) ?? null;
  };

  const progress = today(now);
  const nowDegrees = progress === null ? 0 : progress * 360;
  const [nowX, nowY] = point(EDGE, nowDegrees);
  const tooltipArc = arcs.find((arc) => arc.key === tooltipKey);
  const tooltipAnchor = tooltipKey ? buttons.current.get(tooltipKey) : null;
  const highlighted = tooltipKey ?? focusedKey;

  // Arcs take one tab stop: the hour last focused, or the latest one.
  const tabKey = arcs[tabIndexAt]?.key;
  useLayoutEffect(() => {
    const button = tabKey ? buttons.current.get(tabKey) : undefined;
    if (button) button.tabIndex = 0;
    return () => {
      if (button) button.tabIndex = -1;
    };
  }, [shapes, tabKey]);
  // Only the hour with the tooltip points at it.
  useLayoutEffect(() => {
    const button = tooltipKey ? buttons.current.get(tooltipKey) : undefined;
    button?.setAttribute("aria-describedby", tooltipId);
    return () => button?.removeAttribute("aria-describedby");
  }, [tooltipId, tooltipKey]);
  const hourAt = (target: EventTarget) =>
    target instanceof HTMLButtonElement &&
    target.dataset.slot === "star-trail-hour"
      ? (index.get(target.dataset.key ?? "") ?? null)
      : null;

  return (
    <div
      aria-describedby={summaryId}
      aria-label={label}
      className={cn("relative aspect-square w-full max-w-66", className)}
      data-slot="star-trail-chart"
      onPointerLeave={dismissTooltip}
      onPointerMove={(event) => {
        if (event.pointerType === "touch") return;
        const arc = arcAt(event);
        if (!arc) {
          if (tooltipShown.current || hoverTimer.current) dismissTooltip();
          return;
        }
        if (arc.key === tooltipKey) return;
        cancelHover();
        if (tooltipShown.current) showTooltip(arc.key);
        else hoverTimer.current = setTimeout(() => showTooltip(arc.key), 120);
      }}
      onPointerDown={(event) => {
        const arc = arcAt(event);
        if (arc) showTooltip(arc.key);
      }}
      onFocus={(event) => {
        const at = hourAt(event.target);
        if (at === null) return;
        setActiveKey(arcs[at].key);
        setFocusedKey(arcs[at].key);
        showTooltip(arcs[at].key);
      }}
      onBlur={(event) => {
        if (hourAt(event.target) === null) return;
        setFocusedKey(null);
        dismissTooltip();
      }}
      onKeyDown={(event) => {
        const at = hourAt(event.target);
        if (at === null) return;
        const next = neighbor(arcs, at, event.key);
        if (next === null) return;
        event.preventDefault();
        buttons.current.get(arcs[next].key)?.focus({ preventScroll: true });
      }}
      role="group"
    >
      <p className="sr-only" id={summaryId}>
        {summary}
      </p>
      <svg
        aria-hidden="true"
        className="block size-full overflow-visible"
        ref={svgRef}
        viewBox={`0 0 ${SIZE} ${SIZE}`}
      >
        <defs>
          <linearGradient id={`${summaryId}-star`} x1="0" x2="1" y1="1" y2="0">
            <stop offset="0" stopColor="var(--series-3)" />
            <stop offset="0.55" stopColor="var(--series-1)" />
            <stop offset="1" stopColor="var(--series-5)" />
          </linearGradient>
        </defs>
        <circle
          cx={CENTER}
          cy={CENTER}
          fill="var(--muted)"
          fillOpacity={0.55}
          r={EDGE}
          stroke="var(--border)"
        />
        {progress !== null && nowDegrees > 0.5 ? (
          <path
            d={arcPath(EDGE, 0, Math.min(359.9, nowDegrees))}
            data-slot="star-trail-progress"
            fill="none"
            stroke="var(--tide)"
            strokeLinecap="round"
            strokeWidth={2.2}
          />
        ) : null}
        <Dial hourLabels={hourLabels} />
        <ArcLayer shapes={shapes} />
        {shapes
          .filter(({ arc }) => arc.key === highlighted)
          .map(({ arc, path, width }) => (
            <g key={arc.key}>
              {focusedKey === arc.key ? (
                <path
                  d={path}
                  fill="none"
                  stroke="var(--ring)"
                  strokeLinecap="round"
                  strokeWidth={width + 4}
                />
              ) : null}
              <path
                d={path}
                fill="none"
                stroke="var(--card)"
                strokeLinecap="round"
                strokeWidth={width + 2}
              />
              <path
                d={path}
                fill="none"
                stroke={arc.color}
                strokeLinecap="round"
                strokeWidth={width + 0.6}
              />
            </g>
          ))}
        <path
          d="M0-9l2.6 6.4L9 0 2.6 2.6 0 9l-2.6-6.4L-9 0l6.4-2.6z"
          fill={`url(#${summaryId}-star)`}
          transform={`translate(${CENTER} ${CENTER})`}
        />
        {progress !== null ? (
          <g data-slot="star-trail-now">
            <circle
              cx={nowX}
              cy={nowY}
              fill="var(--tide)"
              fillOpacity={0.22}
              r={6}
            />
            <circle
              cx={nowX}
              cy={nowY}
              fill="var(--tide)"
              r={3}
              stroke="var(--card)"
              strokeWidth={1.3}
            />
          </g>
        ) : null}
      </svg>
      <HourButtons buttons={buttons.current} shapes={shapes} />
      {tooltipArc && tooltipAnchor ? (
        <AnchoredTooltip
          anchor={tooltipAnchor}
          id={tooltipId}
          onDismiss={dismissTooltip}
        >
          {typeof tooltipArc.detail === "function"
            ? tooltipArc.detail()
            : tooltipArc.detail}
        </AnchoredTooltip>
      ) : null}
    </div>
  );
});

/**
 * The arc a key moves to: left and right step through hours in time order,
 * up and down move a ring out or in to the nearest lit hour.
 */
function neighbor(
  arcs: readonly StarTrailChartArc[],
  at: number,
  key: string,
): number | null {
  const last = arcs.length - 1;
  switch (key) {
    case "ArrowLeft":
      return Math.max(0, at - 1);
    case "ArrowRight":
      return Math.min(last, at + 1);
    case "Home":
      return 0;
    case "End":
      return last;
    case "ArrowUp":
    case "ArrowDown": {
      const from = arcs[at];
      const step = key === "ArrowUp" ? 1 : -1;
      const rings = arcs.map((arc) => arc.ring);
      for (
        let ring = from.ring + step;
        ring >= Math.min(...rings) && ring <= Math.max(...rings);
        ring += step
      ) {
        let best: number | null = null;
        arcs.forEach((arc, index) => {
          if (arc.ring !== ring) return;
          if (
            best === null ||
            clockDistance(arc.hour, from.hour) <
              clockDistance(arcs[best].hour, from.hour)
          )
            best = index;
        });
        if (best !== null) return best;
      }
      return at;
    }
    default:
      return null;
  }
}

/** Hours apart on the dial, so 23 and 1 sit two hours apart. */
function clockDistance(left: number, right: number): number {
  const apart = Math.abs(left - right);
  return Math.min(apart, 24 - apart);
}
