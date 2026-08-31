import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";

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
import { WindowChrome } from "./WindowChrome";
import { getDesktopPlatform } from "./window-chrome";
import "./styles/globals.css";

const root = document.getElementById("root");

if (!root) {
  throw new Error("AstrLink root element is missing");
}

const desktopPlatform = getDesktopPlatform();
document.documentElement.dataset.desktopPlatform = desktopPlatform;

void getPreferences()
  .then((settings) => applyLocale(settings.values.locale))
  .catch(() => {
    // Browser preview has no preferences IPC.
  });

function LocaleGate({ children }: { children: ReactNode }) {
  useT();
  return children;
}

createRoot(root).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <LocaleGate>
        <TooltipProvider>
          <WindowChrome platform={desktopPlatform} />
          <AppErrorBoundary>
            <App />
          </AppErrorBoundary>
          <Toaster position="bottom-right" />
        </TooltipProvider>
      </LocaleGate>
    </I18nextProvider>
  </StrictMode>,
);
