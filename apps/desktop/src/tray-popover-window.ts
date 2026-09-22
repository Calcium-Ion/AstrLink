import { getCurrentWindow } from "@tauri-apps/api/window";

/** Must match `POPOVER_LABEL` in `src-tauri/src/tray.rs`. */
export const TRAY_POPOVER_LABEL = "tray-popover";

/** Emitted by the host with a fresh `TrayState` whenever it changes. */
export const TRAY_STATE_EVENT = "tray:state";

/** Emitted to the main window with the `WorkspacePage.kind` to open. */
export const TRAY_NAVIGATE_EVENT = "tray:navigate";

/** Logical width of the popover window, fixed by the host. */
export const TRAY_POPOVER_WIDTH = 360;

export function isTrayPopoverWindow(): boolean {
  try {
    return getCurrentWindow().label === TRAY_POPOVER_LABEL;
  } catch {
    return false;
  }
}
