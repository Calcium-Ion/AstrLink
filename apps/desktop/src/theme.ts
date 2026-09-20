import { useSyncExternalStore } from "react";

import {
  isThemePreference,
  resolveTheme,
  THEME_STORAGE_KEY,
  type ResolvedTheme,
  type ThemePreference,
} from "./theme-model";

let preference: ThemePreference = "system";
let resolved: ResolvedTheme = "light";
let systemTheme: MediaQueryList | null = null;
const listeners = new Set<() => void>();

function updateDocument(): void {
  resolved = resolveTheme(preference, systemTheme?.matches ?? false);
  document.documentElement.dataset.theme = resolved;
  document.documentElement.style.colorScheme = resolved;
  const background = getComputedStyle(document.documentElement).getPropertyValue("--background").trim();
  if (background) document.querySelector('meta[name="theme-color"]')?.setAttribute("content", background);
  listeners.forEach((listener) => listener());
}

export function applyTheme(next: ThemePreference): void {
  preference = next;
  try {
    localStorage.setItem(THEME_STORAGE_KEY, next);
  } catch {
    // Native preferences remain authoritative if browser storage is unavailable.
  }
  updateDocument();
}

export function initializeTheme(): () => void {
  systemTheme = window.matchMedia("(prefers-color-scheme: dark)");
  try {
    const cached = localStorage.getItem(THEME_STORAGE_KEY);
    preference = isThemePreference(cached) ? cached : "system";
  } catch {
    preference = "system";
  }
  updateDocument();
  const media = systemTheme;
  const onSystemChange = () => updateDocument();
  const onStorage = (event: StorageEvent) => {
    if (event.key !== THEME_STORAGE_KEY && event.key !== null) return;
    preference = isThemePreference(event.newValue) ? event.newValue : "system";
    updateDocument();
  };
  media.addEventListener("change", onSystemChange);
  window.addEventListener("storage", onStorage);
  return () => {
    media.removeEventListener("change", onSystemChange);
    window.removeEventListener("storage", onStorage);
  };
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

export function useResolvedTheme(): ResolvedTheme {
  return useSyncExternalStore(subscribe, () => resolved, () => "light");
}
