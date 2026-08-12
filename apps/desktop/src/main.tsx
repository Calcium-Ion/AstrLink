import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

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
