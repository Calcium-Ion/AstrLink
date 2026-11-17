import { getCurrentWindow } from "./native-host/window";

/** Must match `PluginApprovalLabel` in `internal/control/plugins.go`. */
export const PLUGIN_APPROVAL_LABEL = "plugin-approval";

export function isPluginApprovalWindow(): boolean {
  try {
    return getCurrentWindow().label === PLUGIN_APPROVAL_LABEL;
  } catch {
    return false;
  }
}
