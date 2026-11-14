import { useEffect, useRef, type HTMLAttributes, type ReactNode } from "react";
import {
  useAnimate,
  useReducedMotion,
  type AnimationSequence,
} from "motion/react";

import { cn } from "@/lib/utils";

/** How the link stands: joined, broken, not checked yet, or switched off. */
export type ConnectionLinkState = "connected" | "failed" | "idle" | "off";

export interface ConnectionLinkEnd {
  /** About 28px: an image or an icon. */
  mark: ReactNode;
  name: ReactNode;
  caption?: ReactNode;
}

const lineClasses: Record<ConnectionLinkState, string> = {
  connected: "bg-success",
  failed: "bg-destructive/70",
  idle: "bg-tide/60",
  off: "hidden",
};

/** Where the sparks fly when the link lands, as unit vectors. */
const BURST = Array.from({ length: 8 }, (_, index) => {
  const angle = ((index + 0.5) / 8) * Math.PI * 2;
  return { x: Math.cos(angle), y: Math.sin(angle) };
});

/** Where the sparks start and stop, from the centre of the tile, in px. */
const BURST_FROM = 26;
const BURST_TO = 62;

/** The joining waits this long, in s, so a dialog that started it closes first. */
const LEAD = 0.25;

/**
 * Two ends and the link between them, the way the plugin page draws a plugin
 * joined to AstrLink. The link's colour and the badge on it tell the state.
 * Each new playKey plays the joining once: a spark runs from the first end to
 * the second, which lights up and throws sparks, and the badge lands. With
 * reduced motion the link simply shows its state.
 */
export function ConnectionLink({
  from,
  to,
  state,
  badge,
  detail,
  playKey = 0,
  className,
  ...props
}: Omit<HTMLAttributes<HTMLDivElement>, "children"> & {
  from: ConnectionLinkEnd;
  to: ConnectionLinkEnd;
  state: ConnectionLinkState;
  /** The state in words, on the middle of the link. */
  badge: ReactNode;
  /** A short line under the badge, such as the last test. */
  detail?: ReactNode;
  /** Each new value plays the joining once; the first one does not. */
  playKey?: number;
}) {
  const reducedMotion = useReducedMotion();
  const [scope, animate] = useAnimate<HTMLDivElement>();
  const played = useRef(playKey);
  useEffect(() => {
    if (playKey === played.current) return;
    played.current = playKey;
    if (reducedMotion || !scope.current) return;
    const particles = Array.from(
      scope.current.querySelectorAll<HTMLElement>('[data-part="particle"]'),
    );
    const ease = [0.55, 0, 0.2, 1] as const;
    const at = (time: number) => LEAD + time;
    const sequence: AnimationSequence = [
      // Everything that arrives later starts out of sight.
      ['[data-part="fill"]', { scaleX: 0 }, { duration: 0 }],
      [
        '[data-part="badge"]',
        { opacity: 0, scale: 0.6 },
        { duration: 0, at: 0 },
      ],
      ['[data-part="to"]', { filter: "grayscale(1)" }, { duration: 0, at: 0 }],
      [
        '[data-part="from"]',
        { scale: [1, 1.1, 1] },
        { duration: 0.32, at: at(0) },
      ],
      [
        '[data-part="spark"]',
        { left: ["0%", "100%"], opacity: [1, 1] },
        { duration: 0.42, ease, at: at(0.08) },
      ],
      [
        '[data-part="fill"]',
        { scaleX: [0, 1] },
        { duration: 0.42, ease, at: at(0.08) },
      ],
      ['[data-part="spark"]', { opacity: 0 }, { duration: 0.1, at: at(0.5) }],
      [
        '[data-part="to"]',
        { scale: [1, 1.18, 1], filter: ["grayscale(1)", "grayscale(0)"] },
        { duration: 0.5, ease: "easeOut", at: at(0.48) },
      ],
      [
        '[data-part="ripple"]',
        { scale: [1, 2], opacity: [0.8, 0] },
        { duration: 0.75, ease: "easeOut", at: at(0.48) },
      ],
      // Sparks leave the tile's edge, bright for most of the way. A value
      // holds its first keyframe until its segment starts, so they start
      // unseen.
      ...particles.map((particle, index): AnimationSequence[number] => {
        const { x, y } = BURST[index % BURST.length];
        return [
          particle,
          {
            x: [x * BURST_FROM, x * BURST_TO],
            y: [y * BURST_FROM, y * BURST_TO],
            opacity: [0, 1, 1, 1, 0],
            scale: [1, 0.4],
          },
          { duration: 0.65, ease: "easeOut", at: at(0.5) },
        ];
      }),
      [
        '[data-part="badge"]',
        { opacity: [0, 1], scale: [0.6, 1] },
        { duration: 0.32, ease: "backOut", at: at(0.74) },
      ],
    ];
    const controls = animate(sequence);
    return () => controls.stop();
  }, [animate, playKey, reducedMotion, scope]);
  return (
    <div
      className={cn(
        "grid grid-cols-[9rem_minmax(5rem,1fr)_9rem] items-start gap-x-2",
        className,
      )}
      data-slot="connection-link"
      data-state={state}
      ref={scope}
      {...props}
    >
      <LinkEnd end={from} part="from" />
      {/* The link reaches over the ends' columns to their tiles. */}
      <div className="relative -mx-[3.5rem] h-12">
        <span
          aria-hidden="true"
          className={cn(
            "absolute inset-x-1 top-1/2 border-t",
            state === "off" && "border-dashed",
          )}
        />
        <span
          aria-hidden="true"
          className={cn(
            "absolute inset-x-1 top-1/2 h-0.5 origin-left -translate-y-1/2 rounded-full",
            lineClasses[state],
          )}
          data-part="fill"
        />
        <span
          aria-hidden="true"
          className="absolute top-1/2 left-0 size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-tide opacity-0 shadow-[0_0_0_3px_var(--tide-wash),0_0_16px_4px_var(--tide)]"
          data-part="spark"
        />
        <div className="absolute inset-x-0 top-1/2 flex -translate-y-1/2 justify-center">
          <span className="bg-card px-1.5" data-part="badge">
            {badge}
          </span>
        </div>
        {detail ? (
          <p className="absolute inset-x-[3.5rem] top-[calc(50%+0.9rem)] line-clamp-2 text-center text-micro break-words text-muted-foreground">
            {detail}
          </p>
        ) : null}
      </div>
      <LinkEnd burst dim={state === "off"} end={to} part="to" />
    </div>
  );
}

function LinkEnd({
  end,
  part,
  burst = false,
  dim = false,
}: {
  end: ConnectionLinkEnd;
  part: "from" | "to";
  burst?: boolean;
  dim?: boolean;
}) {
  return (
    <div className="flex min-w-0 flex-col items-center gap-1 text-center">
      <div className="relative mb-0.5">
        <span
          className={cn(
            "relative z-10 flex size-12 items-center justify-center rounded-lg border bg-card shadow-xs",
            dim && "opacity-50 grayscale",
          )}
          data-part={part}
        >
          {end.mark}
        </span>
        {burst ? (
          <>
            <span
              aria-hidden="true"
              className="pointer-events-none absolute inset-0 rounded-lg border-2 border-tide opacity-0"
              data-part="ripple"
            />
            {BURST.map((_, index) => (
              <span
                aria-hidden="true"
                className="pointer-events-none absolute top-1/2 left-1/2 z-20 size-2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-tide opacity-0 shadow-[0_0_6px_1px_var(--tide)]"
                data-part="particle"
                key={index}
              />
            ))}
          </>
        ) : null}
      </div>
      <span className="max-w-full truncate text-xs font-medium">
        {end.name}
      </span>
      {end.caption ? (
        <span className="max-w-full truncate text-micro text-muted-foreground">
          {end.caption}
        </span>
      ) : null}
    </div>
  );
}
