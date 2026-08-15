import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Self-hosted: the desktop app has no guaranteed network at launch.
// Latin and digits render in Plex; CJK falls back to the system face.
import "@fontsource-variable/ibm-plex-sans/wght.css";
import "@fontsource/ibm-plex-mono/latin-400.css";
import "@fontsource/ibm-plex-mono/latin-500.css";

import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";

import App from "./App";
import { AppErrorBoundary } from "./AppErrorBoundary";
import { WindowChrome } from "./WindowChrome";
import { getDesktopPlatform } from "./window-chrome";
import "./styles/globals.css";

const root = document.getElementById("root");

if (!root) {
  throw new Error("AstrLink root element is missing");
}

const desktopPlatform = getDesktopPlatform();
document.documentElement.dataset.desktopPlatform = desktopPlatform;

if (import.meta.env.DEV) {
  void import("./dev-webview-reload").then(
    ({ isTauriRuntime, startDevWebviewReload }) => {
      startDevWebviewReload({ enabled: isTauriRuntime() });
    },
  );
}

createRoot(root).render(
  <StrictMode>
    <TooltipProvider>
      <WindowChrome platform={desktopPlatform} />
      <AppErrorBoundary>
        <App />
      </AppErrorBoundary>
      <Toaster position="bottom-right" />
    </TooltipProvider>
  </StrictMode>,
);
