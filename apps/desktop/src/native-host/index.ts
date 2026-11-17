// The desktop host's API: its commands (./core), events (./event) and the
// window (./window). Pages import each module on its own, so a test mocks
// each on its own.
export { commandTable, invoke, isDesktopHost } from "./core";
export { listen, type Event, type UnlistenFn } from "./event";
export { hasNativeHost } from "./presence";
export { getCurrentWindow, type CurrentWindow } from "./window";
