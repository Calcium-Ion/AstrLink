// The desktop host's API in the shape the frontend used under Tauri. Pages
// import ./core, ./event and ./window as they imported @tauri-apps/api/core,
// /event and /window, so a test mocks each on its own.
export { commandTable, invoke, isTauri } from "./core";
export { listen, type Event, type UnlistenFn } from "./event";
export { hasNativeHost } from "./presence";
export { getCurrentWindow, type CurrentWindow } from "./window";
