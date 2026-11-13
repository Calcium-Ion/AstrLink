import {
  Currency,
  Files,
  MainWindow,
  TrajectoryInspector,
  Tray,
  Window,
  type TrayAction,
  type TrayPreferences,
} from "../mygo";
import type { CommandTable } from "./types";

// Commands bound in commands_window.go: window chrome, the main window's
// placement, the tray and its popover, trajectory inspectors, files and
// the display currency. Optional arguments the frontend leaves out arrive
// as null.
export const windowCommands = {
  window_chrome_preferences: () => Window.windowChromePreferences(),

  fit_main_window_to_content: (a: {
    height: number;
    devicePixelRatio: number;
  }) => MainWindow.fitMainWindowToContent(a.height, a.devicePixelRatio),
  set_main_window_unsaved: (a: { unsaved: boolean }) =>
    MainWindow.setMainWindowUnsaved(a.unsaved),

  save_text_file: (a: { defaultFilename: string; contents: string }) =>
    Files.saveTextFile(a.defaultFilename, a.contents),
  display_currency: () => Currency.displayCurrency(),

  tray_state: (a: { tray?: TrayPreferences | null }) =>
    Tray.trayState(a.tray ?? null),
  tray_action: (a: { action: TrayAction }) => Tray.trayAction(a.action),
  tray_popover_resize: (a: { height: number; devicePixelRatio: number }) =>
    Tray.trayPopoverResize(a.height, a.devicePixelRatio),
  tray_popover_hide: () => Tray.trayPopoverHide(),
  take_tray_navigation: () => Tray.takeTrayNavigation(),

  show_trajectory_inspector: (a: { selection: unknown }) =>
    TrajectoryInspector.showTrajectoryInspector(a.selection),
  update_trajectory_inspector: (a: { selection: unknown }) =>
    TrajectoryInspector.updateTrajectoryInspector(a.selection),
  close_trajectory_inspectors: () =>
    TrajectoryInspector.closeTrajectoryInspectors(),
  trajectory_inspector_state: () =>
    TrajectoryInspector.trajectoryInspectorState(),
  set_trajectory_inspector_pinned: (a: { pinned: boolean }) =>
    TrajectoryInspector.setTrajectoryInspectorPinned(a.pinned),
  set_trajectory_inspector_wide: (a: { wide: boolean }) =>
    TrajectoryInspector.setTrajectoryInspectorWide(a.wide),
} satisfies CommandTable;
