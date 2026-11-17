import { getCurrentWindow } from "./native-host/window";

/** Must match `ApprovalLabel` in `internal/ui/approval.go`. */
export const RAW_ACCESS_APPROVAL_LABEL = "raw-access-approval";

export function isRawAccessApprovalWindow(): boolean {
  try {
    return getCurrentWindow().label === RAW_ACCESS_APPROVAL_LABEL;
  } catch {
    return false;
  }
}
