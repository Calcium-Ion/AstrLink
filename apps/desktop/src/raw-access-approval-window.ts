import { getCurrentWindow } from "./native-host/window";

/** Must match `LABEL` in `src-tauri/src/raw_approval.rs`. */
export const RAW_ACCESS_APPROVAL_LABEL = "raw-access-approval";

export function isRawAccessApprovalWindow(): boolean {
  try {
    return getCurrentWindow().label === RAW_ACCESS_APPROVAL_LABEL;
  } catch {
    return false;
  }
}
