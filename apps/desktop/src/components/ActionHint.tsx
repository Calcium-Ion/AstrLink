import type { ReactNode } from "react";
import { motion, useReducedMotion } from "motion/react";

import { ArrowRight, X } from "@/components/icons";
import { IconButton } from "@/components/IconButton";
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
} from "@/components/ui/popover";

/** Point out an existing action without moving focus or changing its behavior. */
export function ActionHint({
  children,
  open,
  onDismiss,
  message,
  dismissLabel,
}: {
  children: ReactNode;
  open: boolean;
  onDismiss: () => void;
  message: string;
  dismissLabel: string;
}) {
  const reducedMotion = useReducedMotion();
  return (
    <Popover open={open} onOpenChange={(next) => !next && onDismiss()}>
      <PopoverAnchor asChild>
        <motion.div
          className="shrink-0 rounded-md"
          initial={false}
          animate={{ y: open && !reducedMotion ? [0, -4, 0] : 0 }}
          transition={{ duration: 0.6, repeat: open ? 2 : 0, delay: 0.15 }}
        >
          {children}
        </motion.div>
      </PopoverAnchor>
      <PopoverContent
        side="top"
        align="end"
        hideWhenDetached
        role="status"
        aria-live="polite"
        className="flex w-64 items-start gap-2 p-3 text-xs leading-relaxed motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-2 motion-safe:duration-300"
        onOpenAutoFocus={(event) => event.preventDefault()}
        onCloseAutoFocus={(event) => event.preventDefault()}
      >
        <ArrowRight
          aria-hidden="true"
          className="mt-0.5 size-4 shrink-0 rotate-45 text-primary"
        />
        <p className="flex-1">{message}</p>
        <IconButton label={dismissLabel} size="icon-xs" onClick={onDismiss}>
          <X aria-hidden="true" />
        </IconButton>
      </PopoverContent>
    </Popover>
  );
}
