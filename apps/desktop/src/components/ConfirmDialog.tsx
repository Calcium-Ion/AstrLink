import { useRef, type ReactNode } from "react";

import { i18n } from "@/i18n";
import { useExitSnapshot } from "@/lib/exit-snapshot";

import { BadgeAlert } from "@/components/icons";
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

/** `warning` puts a warning icon on the title, for a risk the user accepts. */
type ConfirmDialogTone = "default" | "warning";

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
  tone?: ConfirmDialogTone;
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
    tone = "default",
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
          <AlertDialogTitle
            className={
              tone === "warning" ? "flex items-center gap-2" : undefined
            }
          >
            {tone === "warning" ? (
              <BadgeAlert aria-hidden="true" className="size-5 text-warning" />
            ) : null}
            {title}
          </AlertDialogTitle>
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
