import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

// Self-hosted: the desktop app has no guaranteed network at launch.
// Latin and digits render in Plex; CJK falls back to the system face.
import "@fontsource-variable/ibm-plex-sans/wght.css";
import "@fontsource/ibm-plex-mono/latin-400.css";
import "@fontsource/ibm-plex-mono/latin-500.css";

import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";

import App from "./App";
import { AppErrorBoundary } from "./AppErrorBoundary";
import { getPreferences } from "./bridge";
import { applyLocale, i18n, useT } from "./i18n";
import { TrajectoryInspectorWindow } from "./TrajectoryInspectorWindow";
import { isTrajectoryInspectorWindow } from "./trajectory-inspector-window";
import { TrayPopoverWindow } from "./TrayPopover";
import { isTrayPopoverWindow } from "./tray-popover-window";
import { WindowChrome } from "./WindowChrome";
import { getDesktopPlatform } from "./window-chrome";
import { applyTheme, initializeTheme } from "./theme";
import { isThemePreference } from "./theme-model";
import "./styles/globals.css";

const root = document.getElementById("root");

if (!root) {
  throw new Error("AstrLink root element is missing");
}

const desktopPlatform = getDesktopPlatform();
document.documentElement.dataset.desktopPlatform = desktopPlatform;
// The tray popover paints its own panel on a transparent window: no title
// bar, no page background.
const trayPopover = isTrayPopoverWindow();
if (trayPopover) document.documentElement.dataset.surface = "tray-popover";

initializeTheme();

async function loadPreferences(): Promise<void> {
  let themeUpdated = false;
  if (isTauri()) {
    // Register before reading preferences so an inspector cannot miss a change.
    await listen("theme-preference-changed", ({ payload }) => {
      if (isThemePreference(payload)) {
        themeUpdated = true;
        applyTheme(payload);
      }
    }).catch((error) => console.error("Unable to observe AstrLink theme", error));
  }
  const settings = await getPreferences();
  if (!themeUpdated) applyTheme(settings.values.theme);
  await applyLocale(settings.values.locale);
}

void loadPreferences()
  .catch(() => {
    // Browser preview has no preferences IPC.
  });

function LocaleGate({ children }: { children: ReactNode }) {
  useT();
  return children;
}

// Every window loads this bundle; the label decides which app it becomes.
const surface = trayPopover ? (
  <TrayPopoverWindow />
) : isTrajectoryInspectorWindow() ? (
  <TrajectoryInspectorWindow />
) : (
  <App />
);

createRoot(root).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <LocaleGate>
        <TooltipProvider>
          {trayPopover ? null : <WindowChrome platform={desktopPlatform} />}
          <AppErrorBoundary>{surface}</AppErrorBoundary>
          {trayPopover ? null : <Toaster position="bottom-right" />}
        </TooltipProvider>
      </LocaleGate>
    </I18nextProvider>
  </StrictMode>,
);
