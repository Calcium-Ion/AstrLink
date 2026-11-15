// The window hosting the page.
// The label is what the host's preload script set for the window; the
// controls are MyGo's, and the state the Window service reads.
import {
  currentWindow,
  isMyGo,
  onWindowActive,
  windowActive,
} from "mygo-runtime";
import { Window, events } from "../mygo";
import type { Event, UnlistenFn } from "./event";

declare global {
  interface Window {
    __ASTRLINK_WINDOW_LABEL__?: string;
  }
}

export interface CurrentWindow {
  /** The window's role: main, tray-popover, trajectory-inspector-N… */
  readonly label: string;
  close(): Promise<void>;
  minimize(): Promise<void>;
  toggleMaximize(): Promise<void>;
  isFocused(): Promise<boolean>;
  isFullscreen(): Promise<boolean>;
  isMaximized(): Promise<boolean>;
  /** Calls handler after the window was resized or changed state. */
  onResized(handler: () => void): Promise<UnlistenFn>;
  onFocusChanged(handler: (event: Event<boolean>) => void): Promise<UnlistenFn>;
}

const appWindow: CurrentWindow = {
  get label() {
    return window.__ASTRLINK_WINDOW_LABEL__ ?? "";
  },
  close: () => currentWindow.close(),
  minimize: () => currentWindow.minimize(),
  toggleMaximize: () => currentWindow.toggleMaximize(),
  isFocused: async () => windowActive(),
  isFullscreen: async () => (await Window.windowState()).full_screen,
  isMaximized: () => currentWindow.isMaximized(),
  onResized: async (handler) => events.windowResized.on(() => handler()),
  onFocusChanged: async (handler) =>
    onWindowActive((focused) => handler({ payload: focused })),
};

/** Returns the window hosting the page; throws outside the desktop app. */
export function getCurrentWindow(): CurrentWindow {
  if (!isMyGo()) throw new Error("not running in the AstrLink desktop app");
  return appWindow;
}
