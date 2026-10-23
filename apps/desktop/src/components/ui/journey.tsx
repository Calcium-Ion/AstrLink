/**
 * A request's route through the client, gateway and upstream lanes.
 *
 * Motion adapted from affectUI's free components, MIT licensed.
 * Copyright (c) 2026 Florian Höller (adfects). See LICENSES/affectui-MIT.txt.
 * The blur-up reveals, the 0fr to 1fr disclosure, the drawn check and the
 * moving shine follow its Progress Tracker and Tool Call Card; the route
 * itself is AstrLink's, drawn on the same three lanes as the timeline strip.
 */
import {
  useEffect,
  useRef,
  type ComponentProps,
  type CSSProperties,
  type ReactNode,
} from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";

import {
  Ban,
  CircleHelp,
  LoaderCircle,
  ShieldCheck,
  X,
} from "@/components/icons";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import "./journey.css";

export type JourneyLane = 0 | 1 | 2;
export type JourneyStatus =
  | "pending"
  | "ok"
  | "failed"
  | "blocked"
  | "cancelled"
  | "unknown";

/**
 * Lane centres in the rail, in px: client, gateway, upstream. The client
 * lane runs under the call's status disc, so the route starts there.
 */
const RAIL_X = [20, 34, 48] as const;
const RAIL_WIDTH = 60;
/** Lane centres in the sparkline, in px from its top. */
const SPARK_Y = [2.5, 7, 11.5] as const;
/** Where a step's node sits: the middle of its first line. */
const NODE_Y = 14;

type CustomProperties = CSSProperties & Record<`--${string}`, string | number>;

/**
 * A call in the list: one quiet line until it is focused, then a card. The
 * entrance and the jump glow replay only when the caller asks for them.
 */
export function JourneyCall({
  open,
  running = false,
  entering = false,
  enterDelayMs = 0,
  flashKey,
  className,
  style,
  children,
  ...props
}: ComponentProps<"div"> & {
  open: boolean;
  running?: boolean;
  /** Play the blur-up entrance, for calls that just appeared. */
  entering?: boolean;
  enterDelayMs?: number;
  /** A new key replays the glow that marks where a jump landed. */
  flashKey?: string | number | null;
}) {
  return (
    <div
      {...props}
      className={cn("journey-call", className)}
      data-slot="journey-call"
      data-state={open ? "open" : "closed"}
      data-running={running || undefined}
      data-entering={entering || undefined}
      style={
        entering
          ? ({
              ...style,
              "--journey-delay": `${enterDelayMs}ms`,
            } as CustomProperties)
          : style
      }
    >
      {children}
      {flashKey != null ? (
        <span aria-hidden="true" className="journey-flash" key={flashKey} />
      ) : null}
    </div>
  );
}

const emblemIcons = {
  pending: LoaderCircle,
  failed: X,
  blocked: ShieldCheck,
  cancelled: Ban,
  unknown: CircleHelp,
};

/** A status disc whose mark swaps with a spring when the outcome changes. */
export function JourneyEmblem({
  status,
  className,
}: {
  status: JourneyStatus;
  className?: string;
}) {
  const reducedMotion = useReducedMotion();
  const quiet = reducedMotion || status === "unknown";
  return (
    <span
      aria-hidden="true"
      className={cn("journey-emblem", className)}
      data-status={status}
    >
      <AnimatePresence initial={false} mode="popLayout">
        <motion.span
          key={status}
          className="journey-emblem-mark"
          initial={quiet ? false : { opacity: 0, scale: 0.4, rotate: -40 }}
          animate={{ opacity: 1, scale: 1, rotate: 0 }}
          exit={{ opacity: 0, scale: quiet ? 1 : 0.6 }}
          transition={
            quiet
              ? { duration: reducedMotion ? 0 : 0.16 }
              : { type: "spring", stiffness: 420, damping: 26, mass: 0.7 }
          }
        >
          <EmblemMark status={status} />
        </motion.span>
      </AnimatePresence>
    </span>
  );
}

function EmblemMark({ status }: { status: JourneyStatus }) {
  if (status === "ok") {
    return (
      <svg viewBox="0 0 16 16" className="journey-check">
        <path d="M3.5 8.4 6.6 11.3 12.5 4.9" pathLength={1} />
      </svg>
    );
  }
  const Icon = emblemIcons[status];
  return <Icon animateOnHover={false} />;
}

const STACK_LIMIT = 6;

/** Outcomes of a folded group's calls, overlapped like a stack of chips. */
export function JourneyStack({
  marks,
  className,
}: {
  marks: readonly JourneyStatus[];
  className?: string;
}) {
  const shown = marks.slice(0, STACK_LIMIT);
  return (
    <span aria-hidden="true" className={cn("journey-stack", className)}>
      {shown.map((status, index) => (
        <span
          className="journey-stack-mark"
          data-status={status}
          key={index}
          style={{ "--journey-index": index } as CustomProperties}
        />
      ))}
      {marks.length > shown.length ? (
        <span className="journey-stack-more">
          +{marks.length - shown.length}
        </span>
      ) : null}
    </span>
  );
}

/**
 * Text whose changed characters roll in from below, for a clock that is
 * still counting. The first render and unchanged characters stay still.
 */
export function RollingText({
  text,
  className,
}: {
  text: string;
  className?: string;
}) {
  const chars = Array.from(text);
  // Keyed from the right, so units keep their place as digits grow.
  const keys = chars.map((char, index) => `${chars.length - index}:${char}`);
  const committed = useRef<{ text: string; rolled: Set<string> } | null>(null);
  const last = committed.current;
  const rolled =
    last === null
      ? new Set<string>()
      : last.text === text
        ? last.rolled
        : new Set(keys.filter((key) => !textKeys(last.text).has(key)));
  useEffect(() => {
    committed.current = { text, rolled };
  });
  return (
    <span className={cn("journey-rolling", className)}>
      <span className="sr-only">{text}</span>
      {chars.map((char, index) => (
        <span
          aria-hidden="true"
          className="journey-rolling-char"
          data-roll={rolled.has(keys[index]!) ? "" : undefined}
          key={keys[index]}
        >
          {char}
        </span>
      ))}
    </span>
  );
}

function textKeys(text: string): Set<string> {
  const chars = Array.from(text);
  return new Set(chars.map((char, index) => `${chars.length - index}:${char}`));
}

/**
 * A compact route: one step per phase, each as wide as its share of the
 * call, the whole line as wide as the call's share of the longest one.
 */
export function JourneySparkline({
  share,
  className,
  style,
  children,
  ...props
}: ComponentProps<"div"> & {
  /** 0 to 1, the call's duration against the session's longest call. */
  share: number;
}) {
  return (
    <div
      {...props}
      className={cn("journey-spark", className)}
      data-slot="journey-sparkline"
      style={{ ...style, "--journey-share": share } as CustomProperties}
    >
      {children}
    </div>
  );
}

export function JourneySparkStop({
  lane,
  fromLane,
  grow,
  pending = false,
  unknown = false,
  mark = false,
  markClassName,
  className,
  style,
  ...props
}: ComponentProps<"button"> & {
  lane: JourneyLane;
  fromLane: JourneyLane | null;
  /** The phase's duration in ms; widths divide what the minimums leave. */
  grow: number;
  pending?: boolean;
  unknown?: boolean;
  /** Show the phase's dot: outcomes and failures, not every hop. */
  mark?: boolean;
  markClassName?: string;
}) {
  const y = SPARK_Y[lane];
  const from = fromLane === null ? y : SPARK_Y[fromLane];
  return (
    <button
      type="button"
      {...props}
      className={cn("journey-spark-stop", className)}
      data-pending={pending || undefined}
      data-timing={unknown ? "unknown" : undefined}
      style={
        {
          ...style,
          "--journey-grow": grow,
          "--journey-y": `${y}px`,
        } as CustomProperties
      }
    >
      {from !== y ? (
        <span
          aria-hidden="true"
          className="journey-spark-rise"
          style={{
            top: Math.min(from, y) - 0.75,
            height: Math.abs(from - y) + 1.5,
          }}
        />
      ) : null}
      <span aria-hidden="true" className="journey-spark-run" />
      {mark ? (
        <span
          aria-hidden="true"
          className={cn("journey-spark-dot", markClassName)}
        />
      ) : null}
      {pending ? (
        <span aria-hidden="true" className="journey-spark-head" />
      ) : null}
    </button>
  );
}

/**
 * Steps on a three-lane rail. `animate` replays the cascade for a call that
 * was just opened; a call scrolled back into view stays still.
 */
export function JourneyRail({
  animate = false,
  className,
  children,
  ...props
}: ComponentProps<"ol"> & { animate?: boolean }) {
  return (
    <div
      className="journey-body"
      data-animate={animate || undefined}
      data-slot="journey-body"
    >
      <ol {...props} className={cn("journey-rail", className)}>
        {children}
      </ol>
    </div>
  );
}

export function JourneyStep({
  lane,
  fromLane,
  toLane,
  index,
  pending = false,
  unknown = false,
  dwell = 0,
  wait,
  marker,
  markerKind = "dot",
  markerClassName,
  selected = false,
  selectionId,
  retryStart = false,
  detail,
  className,
  children,
  ...props
}: ComponentProps<typeof Button> & {
  lane: JourneyLane;
  fromLane: JourneyLane | null;
  toLane: JourneyLane | null;
  index: number;
  pending?: boolean;
  unknown?: boolean;
  /** 0 to 1: how far the rail lingers below the step, by its duration. */
  dwell?: number;
  /** Colours the lingering rail: time spent upstream, or an attempt lost there. */
  wait?: "upstream" | "failed";
  marker?: ReactNode;
  markerKind?: "dot" | "end" | "live";
  markerClassName?: string;
  selected?: boolean;
  /** Steps sharing an id pass one selection surface between them. */
  selectionId: string;
  retryStart?: boolean;
  /** Shown under the selected step, outside its button. */
  detail?: ReactNode;
}) {
  const reducedMotion = useReducedMotion();
  const x = RAIL_X[lane];
  const from = fromLane === null ? null : RAIL_X[fromLane];
  return (
    <li
      className="journey-step"
      data-marker={markerKind}
      data-pending={pending || undefined}
      data-selected={selected || undefined}
      data-retry-start={retryStart || undefined}
      data-timing={unknown ? "unknown" : undefined}
      data-wait={wait}
      style={
        {
          "--journey-x": `${x}px`,
          "--journey-index": index,
          "--journey-dwell": dwell,
        } as CustomProperties
      }
    >
      {selected ? (
        <motion.span
          aria-hidden="true"
          className="journey-step-surface"
          layoutId={reducedMotion ? undefined : selectionId}
          transition={
            reducedMotion
              ? { duration: 0 }
              : { type: "spring", stiffness: 480, damping: 38 }
          }
        />
      ) : null}
      <span aria-hidden="true" className="journey-step-graph">
        {from === null ? <span className="journey-step-lead" /> : null}
        {toLane !== null || pending ? (
          <span className="journey-step-out" />
        ) : null}
        {pending ? <span className="journey-step-head" /> : null}
      </span>
      <Button
        type="button"
        variant="ghost"
        {...props}
        className={cn("journey-step-main", className)}
      >
        {/* The node rides in the step's own button, so the step carries its
            outcome mark wherever it is read from. */}
        <span aria-hidden="true" className="journey-step-port">
          {from !== null ? (
            <svg
              className="journey-step-in"
              viewBox={`0 0 ${RAIL_WIDTH} ${NODE_Y}`}
              preserveAspectRatio="none"
            >
              <path
                d={
                  from === x
                    ? `M${x} 0V${NODE_Y}`
                    : `M${from} 0C${from} ${NODE_Y / 2} ${x} ${NODE_Y / 2} ${x} ${NODE_Y}`
                }
              />
            </svg>
          ) : null}
          <span className={cn("journey-step-node", markerClassName)}>
            {marker}
          </span>
        </span>
        {children}
      </Button>
      {detail ? <div className="journey-step-detail">{detail}</div> : null}
    </li>
  );
}
