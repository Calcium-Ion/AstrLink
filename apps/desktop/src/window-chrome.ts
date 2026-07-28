import { invoke } from "@tauri-apps/api/core";

export type DesktopPlatform = "browser" | "linux" | "macos" | "windows";
export type WindowControl = "close" | "maximize" | "minimize";

export interface WindowControlLayout {
  start: WindowControl[];
  end: WindowControl[];
}

interface WindowChromePreferences {
  decoration_layout: string | null;
}

const fallbackLinuxLayout = (): WindowControlLayout => ({
  start: [],
  end: ["minimize", "maximize", "close"],
});

export function getDesktopPlatform(): DesktopPlatform {
  if (
    typeof window === "undefined" ||
    !("__TAURI_INTERNALS__" in window)
  ) {
    return "browser";
  }

  switch (window.__ASTRLINK_DESKTOP_PLATFORM__) {
    case "linux":
    case "macos":
    case "windows":
      return window.__ASTRLINK_DESKTOP_PLATFORM__;
    default:
      return "browser";
  }
}

function parseControlList(value: string): WindowControl[] {
  const controls: WindowControl[] = [];
  for (const token of value.split(",")) {
    const control = token.trim();
    if (
      (control === "close" ||
        control === "maximize" ||
        control === "minimize") &&
      !controls.includes(control)
    ) {
      controls.push(control);
    }
  }
  return controls;
}

export function parseLinuxDecorationLayout(
  value: string | null | undefined,
): WindowControlLayout {
  if (!value) return fallbackLinuxLayout();

  const separator = value.indexOf(":");
  if (separator === -1) return fallbackLinuxLayout();

  const start = parseControlList(value.slice(0, separator));
  const end = parseControlList(value.slice(separator + 1)).filter(
    (control) => !start.includes(control),
  );

  return start.length === 0 && end.length === 0
    ? fallbackLinuxLayout()
    : { start, end };
}

export async function loadLinuxWindowControlLayout(): Promise<WindowControlLayout> {
  const value = await invoke<unknown>("window_chrome_preferences");
  if (
    typeof value !== "object" ||
    value === null ||
    !("decoration_layout" in value)
  ) {
    return fallbackLinuxLayout();
  }

  const decorationLayout = (value as WindowChromePreferences).decoration_layout;
  return parseLinuxDecorationLayout(
    typeof decorationLayout === "string" ? decorationLayout : null,
  );
}
