import { useRef, type ReactNode } from "react";

import { i18n } from "@/i18n";
import { useExitSnapshot } from "@/lib/exit-snapshot";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

interface ConfirmDialogProps {
  cancelLabel?: string;
  confirmLabel: string;
  confirmDisabled?: boolean;
  description: ReactNode;
  destructive?: boolean;
  disabled?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  open: boolean;
  title: string;
}

export function ConfirmDialog(props: ConfirmDialogProps) {
  const { onCancel, onConfirm, open } = props;
  // Callers close the dialog by clearing the state that picks its copy, so the
  // exit animation keeps what was being confirmed instead of another prompt.
  const {
    cancelLabel = i18n.t("common.cancel"),
    confirmLabel,
    confirmDisabled = false,
    description,
    destructive = false,
    disabled = false,
    title,
  } = useExitSnapshot(props, open);
  const actionPendingRef = useRef(false);
  return (
    <AlertDialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) return;
        if (actionPendingRef.current) {
          actionPendingRef.current = false;
          return;
        }
        onCancel();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="space-y-2">{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={disabled}>
            {cancelLabel}
          </AlertDialogCancel>
          <AlertDialogAction
            disabled={disabled || confirmDisabled}
            onClick={() => {
              // onConfirm reads the caller's already-cleared state while closing.
              if (!open) return;
              actionPendingRef.current = true;
              onConfirm();
            }}
            variant={destructive ? "destructive" : "default"}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
