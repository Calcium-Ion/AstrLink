"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import { Tooltip as TooltipPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

const tooltipSurface =
  "z-50 w-fit rounded-sm bg-foreground px-2 py-1 text-micro text-balance text-background";

function TooltipProvider({
  delayDuration = 0,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return (
    <TooltipPrimitive.Provider
      data-slot="tooltip-provider"
      delayDuration={delayDuration}
      {...props}
    />
  );
}

function Tooltip({
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Root>) {
  return <TooltipPrimitive.Root data-slot="tooltip" {...props} />;
}

function TooltipTrigger({
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Trigger>) {
  return <TooltipPrimitive.Trigger data-slot="tooltip-trigger" {...props} />;
}

function TooltipContent({
  className,
  sideOffset = 0,
  children,
  animated = true,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Content> & {
  animated?: boolean;
}) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        sideOffset={sideOffset}
        className={cn(
          tooltipSurface,
          "origin-(--radix-tooltip-content-transform-origin)",
          animated &&
            "animate-in fade-in-0 zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
          className,
        )}
        {...props}
      >
        {children}
        <TooltipPrimitive.Arrow className="z-50 size-2.5 translate-y-[calc(-50%_-_2px)] rotate-45 rounded-xs bg-foreground fill-foreground" />
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  );
}

/** One on-demand tooltip for dense grids with many lightweight anchors. */
function AnchoredTooltip({
  anchor,
  children,
  className,
  id,
  onDismiss,
}: {
  anchor: HTMLElement;
  children: React.ReactNode;
  className?: string;
  id: string;
  onDismiss: () => void;
}) {
  const [rect, setRect] = React.useState<DOMRect | null>(null);
  React.useLayoutEffect(() => {
    const measure = () => setRect(anchor.getBoundingClientRect());
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(anchor);
    window.addEventListener("resize", measure);
    window.addEventListener("scroll", onDismiss, true);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measure);
      window.removeEventListener("scroll", onDismiss, true);
    };
  }, [anchor, onDismiss]);

  // Keep the existing Radix surface, arrow, collision handling and dismissal.
  // Only its invisible positioning anchor is shared; real grid buttons never
  // move or remount when the tooltip switches to another cell.
  if (!rect) return null;
  return createPortal(
    <TooltipProvider disableHoverableContent>
      <Tooltip
        open
        onOpenChange={(open) => {
          if (!open) onDismiss();
        }}
      >
        <TooltipTrigger asChild>
          <span
            aria-hidden="true"
            style={{
              pointerEvents: "none",
              position: "fixed",
              left: rect.left,
              top: rect.top,
              width: rect.width,
              height: rect.height,
            }}
          />
        </TooltipTrigger>
        <TooltipContent
          id={id}
          animated={false}
          side="top"
          sideOffset={8}
          className={cn(
            "pointer-events-none max-w-[min(320px,calc(100vw-24px))]",
            className,
          )}
          aria-live="off"
          onPointerDownOutside={(event) => {
            if (
              anchor.contains(event.detail.originalEvent.target as Node | null)
            )
              event.preventDefault();
          }}
        >
          {children}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>,
    document.body,
  );
}

export {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
  TooltipProvider,
  AnchoredTooltip,
};
