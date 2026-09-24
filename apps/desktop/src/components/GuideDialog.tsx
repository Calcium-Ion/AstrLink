import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";

import { IconButton } from "@/components/IconButton";
import { CircleHelp, RotateCcw } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

/** Opens a guide once per storage key, the first time `ready` is true. */
export function useFirstVisitGuide(storageKey: string, ready = true) {
  const [open, setOpen] = useState(false);
  const checked = useRef(false);
  useEffect(() => {
    if (!ready || checked.current) return;
    checked.current = true;
    try {
      if (localStorage.getItem(storageKey) === "seen") return;
      localStorage.setItem(storageKey, "seen");
    } catch {
      // Storage can be unavailable in a WebView; the guide must still work.
    }
    setOpen(true);
  }, [ready, storageKey]);
  return [open, setOpen] as const;
}

/** A help trigger that opens a short, replayable animated walkthrough. */
export function GuideDialog({
  triggerLabel,
  triggerVariant = "icon",
  title,
  description,
  note,
  replayLabel,
  dismissLabel,
  open: controlledOpen,
  onOpenChange,
  className,
  children,
}: {
  triggerLabel: string;
  /** `button` shows the label next to the icon for a more prominent entry. */
  triggerVariant?: "icon" | "button";
  title: string;
  description: string;
  note?: string;
  replayLabel: string;
  dismissLabel: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** Extra dialog classes, e.g. a wider max width for side-by-side demos. */
  className?: string;
  /** Mounted only while open and remounted on replay to restart the demo. */
  children: ReactNode;
}) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
  const [playback, setPlayback] = useState(0);
  const trigger = useRef<HTMLButtonElement>(null);
  const dismiss = useRef<HTMLButtonElement>(null);
  const open = controlledOpen ?? uncontrolledOpen;
  const setOpen = (next: boolean) => {
    setUncontrolledOpen(next);
    onOpenChange?.(next);
  };

  return (
    <>
      {triggerVariant === "button" ? (
        <Button
          ref={trigger}
          aria-haspopup="dialog"
          onClick={() => setOpen(true)}
          size="sm"
          type="button"
          variant="outline"
        >
          <CircleHelp aria-hidden="true" className="text-primary" />
          {triggerLabel}
        </Button>
      ) : (
        <Button
          ref={trigger}
          aria-label={triggerLabel}
          aria-haspopup="dialog"
          onClick={() => setOpen(true)}
          size="icon-xs"
          type="button"
          variant="ghost"
        >
          <CircleHelp aria-hidden="true" className="text-muted-foreground" />
        </Button>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          className={cn("gap-3 sm:max-w-md", className)}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            dismiss.current?.focus({ preventScroll: true });
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            trigger.current?.focus({ preventScroll: true });
          }}
        >
          <DialogHeader className="pr-6 text-left">
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          {open ? <Fragment key={playback}>{children}</Fragment> : null}
          {note ? (
            <p className="text-xs leading-5 text-muted-foreground">{note}</p>
          ) : null}
          <DialogFooter className="flex-row items-center justify-between sm:justify-between">
            <IconButton
              label={replayLabel}
              onClick={() => setPlayback((value) => value + 1)}
              variant="ghost"
            >
              <RotateCcw aria-hidden="true" />
            </IconButton>
            <Button ref={dismiss} onClick={() => setOpen(false)}>
              {dismissLabel}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
