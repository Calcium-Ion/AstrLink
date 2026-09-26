import { useSyncExternalStore } from "react";

const key = "astrlink.intelligence.enabled.v1";
const event = "astrlink-intelligence-preference";
function enabled() {
  try {
    return localStorage.getItem(key) !== "false";
  } catch {
    return true;
  }
}
function subscribe(listener: () => void) {
  window.addEventListener(event, listener);
  window.addEventListener("storage", listener);
  return () => {
    window.removeEventListener(event, listener);
    window.removeEventListener("storage", listener);
  };
}
export function useIntelligenceEnabled() {
  return useSyncExternalStore(subscribe, enabled, () => true);
}
export function setIntelligenceEnabled(value: boolean) {
  localStorage.setItem(key, String(value));
  window.dispatchEvent(new Event(event));
}
