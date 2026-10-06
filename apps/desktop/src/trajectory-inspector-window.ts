import { useCallback, useEffect, useMemo, useRef } from "react";
import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import { getCurrentWindow } from "@tauri-apps/api/window";

import { i18n } from "./i18n";
import { notify } from "./notify";
import type { RequestRecord } from "./request-record-model";
import type {
  RequestServiceIdentity,
  RequestServiceMap,
} from "./request-service-model";
import type { TrajectoryRow } from "./request-trajectory-model";
import { getDesktopPlatform } from "./window-chrome";

/**
 * Must match `TRAJECTORY_INSPECTOR_LABEL_PREFIX` in `src-tauri/src/lib.rs`.
 * Labels are numbered because a pinned inspector freezes on its call and the
 * next click has to land in a window of its own.
 */
export const TRAJECTORY_INSPECTOR_LABEL_PREFIX = "trajectory-inspector-";

const SELECT_EVENT = "trajectory-inspector:select";

/**
 * What the main window hands the inspector: the row that was clicked and the
 * record it belongs to, never a captured body. The inspector expands that
 * record into the whole call chain. The inspector decrypts its own audit
 * content, so request and response text is fetched by the window that
 * displays it instead of crossing the event channel.
 */
export interface TrajectoryInspectorSelection {
  row: TrajectoryRow;
  record: RequestRecord;
  service?: RequestServiceIdentity;
  /** Names for the providers the record's route events tried. */
  services?: RequestServiceMap;
}

/** What an inspector window pulls on mount instead of waiting to be told. */
export interface TrajectoryInspectorWindowState {
  selection: TrajectoryInspectorSelection | null;
  pinned: boolean;
  /** Widened by its toggle; absent from a host that predates it. */
  wide?: boolean;
}

function windowLabel(): string | null {
  if (getDesktopPlatform() === "browser") return null;
  try {
    return getCurrentWindow().label;
  } catch {
    return null;
  }
}

export function isTrajectoryInspectorWindow(): boolean {
  return windowLabel()?.startsWith(TRAJECTORY_INSPECTOR_LABEL_PREFIX) ?? false;
}

/**
 * True where the inspector can have an OS window of its own. The browser
 * preview and the unit tests have no window host, so they keep the pane docked
 * over the phase list.
 */
export function detachedInspectorEnabled(): boolean {
  const label = windowLabel();
  return label !== null && !label.startsWith(TRAJECTORY_INSPECTOR_LABEL_PREFIX);
}

/**
 * Routes a selection to the newest unpinned window, opening one when every
 * inspector is pinned or none is left. Resolves to the label it landed in.
 */
export function showTrajectoryInspector(
  selection: TrajectoryInspectorSelection,
): Promise<string> {
  return invoke<string>("show_trajectory_inspector", { selection });
}

/** Refreshes an already open inspector. Never opens one. */
export function updateTrajectoryInspector(
  selection: TrajectoryInspectorSelection,
): Promise<void> {
  return invoke<void>("update_trajectory_inspector", { selection });
}

/** Closes the inspectors that were following the list, keeping pinned ones. */
export function closeTrajectoryInspectors(): Promise<void> {
  return invoke<void>("close_trajectory_inspectors");
}

export function trajectoryInspectorState(): Promise<TrajectoryInspectorWindowState> {
  return invoke<TrajectoryInspectorWindowState>("trajectory_inspector_state");
}

/** Resolves to the pin state the host settled on. */
export function setTrajectoryInspectorPinned(
  pinned: boolean,
): Promise<boolean> {
  return invoke<boolean>("set_trajectory_inspector_pinned", { pinned });
}

/**
 * Widens the window for long bodies, or puts it back where it was. Resolves
 * to the state the host settled on.
 */
export function setTrajectoryInspectorWide(wide: boolean): Promise<boolean> {
  return invoke<boolean>("set_trajectory_inspector_wide", { wide });
}

export function listenInspectorSelection(
  onSelect: (selection: TrajectoryInspectorSelection) => void,
): Promise<UnlistenFn> {
  return listen<TrajectoryInspectorSelection>(SELECT_EVENT, (event) =>
    onSelect(event.payload),
  );
}

export interface DetachedInspector {
  /** The inspector has its own window, so no pane belongs beside the list. */
  enabled: boolean;
  /** Opens or reuses a window for a phase the operator just clicked. */
  show: (selection: TrajectoryInspectorSelection) => void;
}

/**
 * Drives the detached inspectors from the main window.
 *
 * The host owns the routing: which window a selection belongs in, which ones
 * are pinned, and which phase each is frozen on. This side only says whether a
 * selection came from a click (may open a window) or from a poll (may not).
 */
export function useDetachedInspector(
  selection: TrajectoryInspectorSelection | null,
): DetachedInspector {
  const enabled = useMemo(detachedInspectorEnabled, []);
  // The last payload handed to the host, serialized. A new turn replaces every
  // record in the conversation, so the selection is a new object even when the
  // call it points at did not change, and re-sending it made the window redraw.
  const sentRef = useRef<string | null>(null);

  // Follows the selection into whichever window is already open, including a
  // poll that replaces the record while the same phase stays selected. This
  // never opens one, so a background poll cannot resurrect a window the
  // operator closed, and it cannot disturb a pinned one.
  useEffect(() => {
    if (!enabled || !selection) return;
    const payload = JSON.stringify(selection);
    if (payload === sentRef.current) return;
    sentRef.current = payload;
    void updateTrajectoryInspector(selection).catch(reportFailure);
  }, [enabled, selection]);

  // Leaving the conversation closes the inspectors that were following the
  // list: one left behind would keep showing a request the operator can no
  // longer see. Pinned ones were kept on purpose and stay.
  useEffect(() => {
    if (!enabled) return;
    return () => {
      void closeTrajectoryInspectors().catch(reportFailure);
    };
  }, [enabled]);

  const show = useCallback(
    (next: TrajectoryInspectorSelection) => {
      if (!enabled) return;
      // The click already delivered this phase, so the update that follows
      // from the new selection state has nothing to add.
      sentRef.current = JSON.stringify(next);
      void showTrajectoryInspector(next).catch((error: unknown) => {
        notify.error(i18n.t("trajectory.inspectorWindowFailed"));
        reportFailure(error);
      });
    },
    [enabled],
  );

  // Stable, because the list row handler is built from this and the rows are
  // memoized on it. A fresh object every render would repaint all of them.
  return useMemo(() => ({ enabled, show }), [enabled, show]);
}

function reportFailure(error: unknown): void {
  console.error("AstrLink trajectory inspector window failed", error);
}
