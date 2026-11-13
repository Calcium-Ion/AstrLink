import type { GoEvent } from "mygo-runtime";
import { events } from "../mygo";

// The events the Go host emits, under the names the frontend listens to.
export const eventTable = {
  "app-update-status": events.appUpdateStatus,
  "privacy-model-update": events.privacyModelUpdate,
  "provider-import": events.providerImport,
  "tray:navigate": events.trayNavigate,
  "tray:state": events.trayState,
  "trajectory-inspector:select": events.trajectoryInspectorSelect,
  "raw-sealing-changed": events.rawSealingChanged,
  "local-client-status": events.localClientStatus,
  "display-currency-changed": events.displayCurrencyChanged,
  "quota-display-mode-changed": events.quotaDisplayModeChanged,
  "theme-preference-changed": events.themePreferenceChanged,
} satisfies Record<string, GoEvent<unknown>>;

export type EventName = keyof typeof eventTable;
