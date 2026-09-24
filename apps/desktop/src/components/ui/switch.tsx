"use client";

import * as React from "react";
import { Switch as SwitchPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

function Switch({
  className,
  size = "default",
  onCheckedChange,
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root> & {
  size?: "sm" | "default";
}) {
  const [armed, setArmed] = React.useState(false);

  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      data-size={size}
      className={cn(
        "peer group/switch inline-flex shrink-0 cursor-pointer items-center rounded-full border border-transparent p-0.5 outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-45 data-[size=default]:h-6 data-[size=default]:w-11 data-[size=sm]:h-5 data-[size=sm]:w-9 data-[state=checked]:bg-primary-fill data-[state=unchecked]:bg-input",
        armed
          ? "transition-colors motion-reduce:transition-none"
          : "transition-none",
        className,
      )}
      {...props}
      onCheckedChange={(checked) => {
        // Data arriving after mount is initialization, not a user toggle.
        setArmed(true);
        onCheckedChange?.(checked);
      }}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className={cn(
          "pointer-events-none block rounded-full bg-primary-fill-foreground shadow-sm ring-0 group-data-[size=default]/switch:size-5 group-data-[size=sm]/switch:size-4 group-data-[size=default]/switch:data-[state=checked]:translate-x-5 group-data-[size=sm]/switch:data-[state=checked]:translate-x-4 data-[state=unchecked]:translate-x-0",
          armed
            ? "transition-transform motion-reduce:transition-none"
            : "transition-none",
        )}
      />
    </SwitchPrimitive.Root>
  );
}

export { Switch };
