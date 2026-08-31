import { toast } from "sonner";

export const notify = {
  success(message: string) {
    toast.success(message);
  },
  error(message: string) {
    toast.error(message);
  },
  warning(message: string) {
    toast.warning(message);
  },
};
