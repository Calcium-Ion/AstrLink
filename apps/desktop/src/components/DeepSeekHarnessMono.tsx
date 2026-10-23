import DeepSeekMono from "@lobehub/icons/es/DeepSeek/components/Mono";
import { forwardRef, type ComponentProps } from "react";

import { cn } from "@/lib/utils";

export const DeepSeekHarnessMono = forwardRef<
  SVGSVGElement,
  ComponentProps<typeof DeepSeekMono>
>(function DeepSeekHarnessMono({ className, ...props }, ref) {
  return (
    <DeepSeekMono
      {...props}
      className={cn("text-black dark:text-foreground", className)}
      ref={ref}
    />
  );
});
