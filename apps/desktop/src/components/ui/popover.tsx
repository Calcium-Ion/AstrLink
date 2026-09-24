import type { ComponentProps } from "react";
import { Popover as PopoverPrimitive } from "radix-ui";

import { ignoreImeEscape } from "@/lib/ime";
import { cn } from "@/lib/utils";

export function Popover(props: ComponentProps<typeof PopoverPrimitive.Root>) {
  return <PopoverPrimitive.Root {...props} />;
}

export function PopoverTrigger(
  props: ComponentProps<typeof PopoverPrimitive.Trigger>,
) {
  return <PopoverPrimitive.Trigger data-slot="popover-trigger" {...props} />;
}

export function PopoverAnchor(
  props: ComponentProps<typeof PopoverPrimitive.Anchor>,
) {
  return <PopoverPrimitive.Anchor {...props} />;
}

export function PopoverContent({
  className,
  align = "end",
  sideOffset = 8,
  collisionPadding = 12,
  onWheel,
  onTouchMove,
  onEscapeKeyDown,
  ...props
}: ComponentProps<typeof PopoverPrimitive.Content>) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        align={align}
        className={cn(
          "z-50 max-h-(--radix-popover-content-available-height) w-72 max-w-[calc(100vw-24px)] overflow-y-auto overscroll-contain rounded-md border bg-popover p-4 text-popover-foreground shadow-md outline-none",
          className,
        )}
        collisionPadding={collisionPadding}
        data-slot="popover-content"
        sideOffset={sideOffset}
        {...props}
        // Keep native scrolling in this portal out of an ancestor dialog's
        // document-level scroll lock; overscroll containment prevents chaining.
        onWheel={(event) => {
          event.stopPropagation();
          onWheel?.(event);
        }}
        onTouchMove={(event) => {
          event.stopPropagation();
          onTouchMove?.(event);
        }}
        onEscapeKeyDown={ignoreImeEscape(onEscapeKeyDown)}
      />
    </PopoverPrimitive.Portal>
  );
}
