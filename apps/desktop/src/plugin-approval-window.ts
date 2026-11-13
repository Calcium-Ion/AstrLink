import { getCurrentWindow } from "@tauri-apps/api/window";

/** Must match `LABEL` in `src-tauri/src/plugin_approval.rs`. */
export const PLUGIN_APPROVAL_LABEL = "plugin-approval";

export function isPluginApprovalWindow(): boolean {
  try {
    return getCurrentWindow().label === PLUGIN_APPROVAL_LABEL;
  } catch {
    return false;
  }
}
