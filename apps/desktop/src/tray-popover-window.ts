import { getCurrentWindow } from "./native-host/window";

/** Must match `PopoverLabel` in `internal/ui/tray.go`. */
export const TRAY_POPOVER_LABEL = "tray-popover";

/** Emitted by the host with a fresh `TrayState` whenever it changes. */
export const TRAY_STATE_EVENT = "tray:state";

/**
 * Tells the main window the tray left a page for it to take with
 * `takeTrayNavigation`: a `WorkspacePage.kind`, or a named view of one such as
 * {@link TRAY_FAILED_TODAY_PAGE}.
 */
export const TRAY_NAVIGATE_EVENT = "tray:navigate";

/**
 * Request records narrowed to the calls behind today's failure count. Must
 * match `navigationKinds` in `internal/ui/tray_model.go`.
 */
export const TRAY_FAILED_TODAY_PAGE = "failedToday";

/**
 * The safety page opened on its privacy statistics. Must match
 * `navigationKinds` in `internal/ui/tray_model.go`.
 */
export const TRAY_PRIVACY_OVERVIEW_PAGE = "privacyOverview";

/**
 * Width of the popover page in CSS pixels. The host sizes the window to fit
 * it at the pixel ratio the page reports.
 */
export const TRAY_POPOVER_WIDTH = 360;

export function isTrayPopoverWindow(): boolean {
  try {
    return getCurrentWindow().label === TRAY_POPOVER_LABEL;
  } catch {
    return false;
  }
}
