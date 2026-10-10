import { toast } from "sonner";

export interface NotifyAction {
  label: string;
  onClick: () => void;
}

// A toast with a button stays long enough to reach it.
const ACTION_DURATION_MS = 8_000;

export const notify = {
  success(message: string, action?: NotifyAction) {
    toast.success(
      message,
      action
        ? {
            action: { label: action.label, onClick: action.onClick },
            duration: ACTION_DURATION_MS,
          }
        : undefined,
    );
  },
  error(message: string) {
    toast.error(message);
  },
  warning(message: string) {
    toast.warning(message);
  },
  info(message: string) {
    toast.info(message);
  },
};
