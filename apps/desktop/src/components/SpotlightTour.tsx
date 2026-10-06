import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";

import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverAnchor,
  PopoverArrow,
  PopoverContent,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

export interface SpotlightTourStep {
  /** Matches `data-tour-target` on an element inside `root`. */
  id: string;
  title: string;
  body: ReactNode;
}

export interface SpotlightTourLabels {
  next: string;
  back: string;
  skip: string;
  done: string;
  progress: (current: number, total: number) => string;
}

interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

const inset = 4;

function measure(root: HTMLElement, id: string): Box | null {
  const target = root.querySelector<HTMLElement>(`[data-tour-target="${id}"]`);
  if (!target) return null;
  // A horizontally scrolling strip can hide the target. Scroll only the strip:
  // scrollIntoView would also move vertical ancestors.
  const strip = root.getBoundingClientRect();
  let rect = target.getBoundingClientRect();
  if (rect.left < strip.left || rect.right > strip.right) {
    root.scrollLeft +=
      rect.left < strip.left
        ? rect.left - strip.left - inset
        : rect.right - strip.right + inset;
    rect = target.getBoundingClientRect();
  }
  return {
    x: rect.left - inset,
    y: rect.top - inset,
    width: rect.width + inset * 2,
    height: rect.height + inset * 2,
  };
}

function TourLayer({
  run,
  root,
  steps,
  labels,
  onClose,
  returnFocus,
}: {
  run: number;
  root: RefObject<HTMLElement | null>;
  steps: readonly SpotlightTourStep[];
  labels: SpotlightTourLabels;
  onClose: () => void;
  returnFocus?: RefObject<HTMLElement | null>;
}) {
  const reducedMotion = useReducedMotion();
  const titleId = useId();
  const bodyId = useId();
  const next = useRef<HTMLButtonElement>(null);
  const [index, setIndex] = useState(0);
  const [box, setBox] = useState<Box | null>(null);
  const [startedRun, setStartedRun] = useState(run);
  if (run !== startedRun) {
    setStartedRun(run);
    setIndex(0);
  }
  const step = steps[index];
  const last = index === steps.length - 1;

  useLayoutEffect(() => {
    const node = root.current;
    if (!node) return;
    const update = () => setBox(measure(node, step.id));
    update();
    const observer = new ResizeObserver(update);
    observer.observe(node);
    node.addEventListener("scroll", update, { passive: true });
    window.addEventListener("resize", update);
    return () => {
      observer.disconnect();
      node.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
    };
  }, [root, step.id]);

  if (!box) return null;
  const go = (offset: number) =>
    setIndex((current) =>
      Math.min(steps.length - 1, Math.max(0, current + offset)),
    );
  return (
    <Popover
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      {createPortal(
        <PopoverAnchor asChild>
          {/* The spread shadow dims everything except the highlighted target. */}
          <motion.div
            animate={{ opacity: 1, scale: 1, ...box }}
            aria-hidden="true"
            className="pointer-events-none fixed top-0 left-0 z-85 rounded-lg shadow-[0_0_0_9999px_var(--overlay)] ring-2 ring-primary"
            data-tour-spotlight={step.id}
            exit={{ opacity: 0 }}
            initial={{ opacity: 0, scale: reducedMotion ? 1 : 1.12, ...box }}
            transition={
              reducedMotion
                ? { duration: 0 }
                : { type: "spring", stiffness: 380, damping: 34 }
            }
          >
            {reducedMotion ? null : (
              <motion.span
                animate={{ opacity: 0, scale: 1.18 }}
                className="absolute inset-0 rounded-lg ring-2 ring-primary"
                initial={{ opacity: 0.8, scale: 1 }}
                key={step.id}
                transition={{ delay: 0.3, duration: 0.9, ease: "easeOut" }}
              />
            )}
          </motion.div>
        </PopoverAnchor>,
        document.body,
      )}
      <PopoverContent
        align="start"
        aria-describedby={bodyId}
        aria-labelledby={titleId}
        className="z-95 w-80 overflow-visible p-0"
        data-slot="spotlight-tour"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          // Only recover focus lost with the content; never steal it back from
          // whatever the user clicked to dismiss the tour.
          if (document.activeElement === document.body) {
            returnFocus?.current?.focus({ preventScroll: true });
          }
        }}
        onInteractOutside={(event) => {
          // The replay trigger restarts the tour instead of dismissing it.
          if (returnFocus?.current?.contains(event.target as Node)) {
            event.preventDefault();
          }
        }}
        onKeyDown={(event) => {
          if (event.key === "ArrowRight") go(1);
          if (event.key === "ArrowLeft") go(-1);
        }}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          next.current?.focus({ preventScroll: true });
        }}
        side="bottom"
        sideOffset={10}
        updatePositionStrategy="always"
      >
        <PopoverArrow className="-mt-px" height={7} width={14} />
        <motion.div
          animate={{ opacity: 1, y: 0 }}
          className="grid gap-1 px-4 pt-3.5"
          initial={reducedMotion ? false : { opacity: 0, y: 4 }}
          key={step.id}
          transition={{ duration: 0.2 }}
        >
          <p className="text-micro font-medium text-primary tabular-nums">
            {labels.progress(index + 1, steps.length)}
          </p>
          <h3 className="text-sm font-semibold" id={titleId}>
            {step.title}
          </h3>
          <div className="text-xs leading-5 text-muted-foreground" id={bodyId}>
            {step.body}
          </div>
        </motion.div>
        <div className="flex items-center justify-between gap-3 px-4 pt-3 pb-3.5">
          <ol aria-hidden="true" className="m-0 flex list-none gap-1 p-0">
            {steps.map((item, position) => (
              <li
                className={cn(
                  "h-1.5 rounded-full transition-[width,background-color] duration-300 motion-reduce:transition-none",
                  position === index
                    ? "w-4 bg-primary"
                    : "w-1.5 bg-muted-foreground/30",
                )}
                key={item.id}
              />
            ))}
          </ol>
          <div className="flex items-center gap-1.5">
            {index === 0 ? (
              <Button onClick={onClose} size="sm" type="button" variant="ghost">
                {labels.skip}
              </Button>
            ) : (
              <Button
                onClick={() => go(-1)}
                size="sm"
                type="button"
                variant="ghost"
              >
                {labels.back}
              </Button>
            )}
            <Button
              ref={next}
              onClick={() => (last ? onClose() : go(1))}
              size="sm"
              type="button"
            >
              {last ? labels.done : labels.next}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}

/**
 * Walks through targets in place: the page dims around a highlight that glides
 * from one target to the next, with a short explanation anchored below it.
 */
export function SpotlightTour({
  open,
  onOpenChange,
  run = 0,
  ...props
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Changing it restarts an open tour from the first step. */
  run?: number;
  /** Contains the targets; scrolled horizontally when a target is hidden. */
  root: RefObject<HTMLElement | null>;
  steps: readonly SpotlightTourStep[];
  labels: SpotlightTourLabels;
  /** Receives focus when the tour ends, e.g. the trigger that replays it. */
  returnFocus?: RefObject<HTMLElement | null>;
}) {
  return (
    <AnimatePresence>
      {open ? (
        <TourLayer
          key="tour"
          onClose={() => onOpenChange(false)}
          run={run}
          {...props}
        />
      ) : null}
    </AnimatePresence>
  );
}
