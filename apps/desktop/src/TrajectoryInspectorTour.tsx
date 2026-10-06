import { useEffect, useLayoutEffect, useRef, useState } from "react";

import { useFirstVisitGuide } from "./components/GuideDialog";
import { IconButton } from "./components/IconButton";
import {
  SpotlightTour,
  type SpotlightTourStep,
} from "./components/SpotlightTour";
import { CircleHelp } from "./components/icons";
import { findShortcutLabel } from "./find-model";
import { useT } from "./i18n";

export const TRAJECTORY_INSPECTOR_TOUR_KEY = "astrlink.inspector-tour.v1";

/** Let the call load and the pane settle before the first visit measures. */
const firstVisitDelay = 600;

/** Every target, in tour order; see `data-tour-target` on each control. */
const TOUR_TARGETS = ["inspector-widen", "inspector-find", "inspector-pin"];

/**
 * Tours the inspector window's own controls the first time it shows a call:
 * widening the window, finding in the body, and pinning. The targets span
 * the title bar and the pane, so the tour measures from the document. A tab
 * without a captured body has no search box, so that step is left out
 * rather than pointing at nothing.
 */
export function TrajectoryInspectorTour({ ready }: { ready: boolean }) {
  const t = useT();
  const [settled, setSettled] = useState(false);
  useEffect(() => {
    if (!ready) return;
    const timer = window.setTimeout(() => setSettled(true), firstVisitDelay);
    return () => window.clearTimeout(timer);
  }, [ready]);
  const [open, setOpen] = useFirstVisitGuide(
    TRAJECTORY_INSPECTOR_TOUR_KEY,
    settled,
  );
  // A replay restarts from the first step, even while the tour is open.
  const [run, setRun] = useState(0);
  const [present, setPresent] = useState<readonly string[]>([]);
  const trigger = useRef<HTMLButtonElement>(null);
  const root = useRef<HTMLElement>(document.body);

  // Which targets are on screen is read once per run, before the first
  // paint, so the step count does not change under the reader.
  useLayoutEffect(() => {
    if (!open) return;
    setPresent(
      TOUR_TARGETS.filter(
        (id) => document.querySelector(`[data-tour-target="${id}"]`) !== null,
      ),
    );
  }, [open, run]);

  const steps: SpotlightTourStep[] = [
    {
      id: "inspector-widen",
      title: t("trajectory.tour.widenTitle"),
      body: t("trajectory.tour.widen"),
    },
    {
      id: "inspector-find",
      title: t("trajectory.tour.findTitle"),
      body: t("trajectory.tour.find", { shortcut: findShortcutLabel() }),
    },
    {
      id: "inspector-pin",
      title: t("trajectory.tour.pinTitle"),
      body: t("trajectory.tour.pin"),
    },
  ].filter((step) => present.includes(step.id));

  return (
    <>
      <IconButton
        className="text-muted-foreground hover:bg-foreground/8 hover:text-foreground"
        data-testid="trajectory-inspector-tour"
        label={t("trajectory.tour.help")}
        onClick={() => {
          setRun((value) => value + 1);
          setOpen(true);
        }}
        ref={trigger}
        size="icon-xs"
        type="button"
      >
        <CircleHelp aria-hidden="true" className="size-3.5" />
      </IconButton>
      <SpotlightTour
        labels={{
          next: t("trajectory.tour.next"),
          back: t("trajectory.tour.back"),
          skip: t("trajectory.tour.skip"),
          done: t("trajectory.tour.done"),
          progress: (current, total) =>
            t("trajectory.tour.progress", { current, total }),
        }}
        onOpenChange={setOpen}
        open={open && steps.length > 0}
        returnFocus={trigger}
        root={root}
        run={run}
        steps={steps}
      />
    </>
  );
}
