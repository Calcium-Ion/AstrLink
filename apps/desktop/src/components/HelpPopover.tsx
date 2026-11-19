import type { MouseEventHandler, ReactNode, Ref } from "react";
import { CircleHelp } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

/**
 * Supporting explanations stay available without consuming working height.
 * The wide size holds a small illustration beside its text.
 */
export function HelpPopover({
  label,
  children,
  inDialog = false,
  open,
  onOpenChange,
  onTriggerClick,
  triggerClassName,
  size = "default",
  triggerRef,
}: {
  label: string;
  children: ReactNode;
  inDialog?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onTriggerClick?: MouseEventHandler<HTMLButtonElement>;
  /** Extra classes for the trigger, e.g. to align it with a control's baseline. */
  triggerClassName?: string;
  size?: "default" | "wide";
  triggerRef?: Ref<HTMLButtonElement>;
}) {
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <Button
          ref={triggerRef}
          aria-label={label}
          onClick={onTriggerClick}
          size="icon-xs"
          type="button"
          variant="ghost"
          className={triggerClassName}
        >
          <CircleHelp aria-hidden="true" className="text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        animated
        align="start"
        className={cn(
          "text-xs leading-relaxed",
          inDialog && "z-110",
          size === "wide" && "w-[26rem] p-3",
        )}
      >
        {children}
      </PopoverContent>
    </Popover>
  );
}
