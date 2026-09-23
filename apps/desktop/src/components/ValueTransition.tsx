import { useLayoutEffect, useRef, type HTMLAttributes } from "react";
import { Slot } from "radix-ui";

/** Gently reveal changed values without animating layout or replaying on return. */
export function ValueTransition({
  valueKey,
  asChild = false,
  initialOpacity = 0.45,
  duration = 180,
  offsetY = 0,
  children,
  ...props
}: HTMLAttributes<HTMLSpanElement> & {
  valueKey: string;
  asChild?: boolean;
  initialOpacity?: number;
  duration?: number;
  offsetY?: number;
}) {
  const element = useRef<HTMLSpanElement>(null);
  const previous = useRef(valueKey);
  useLayoutEffect(() => {
    if (previous.current === valueKey) return;
    previous.current = valueKey;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    // Navigation can use a small compositor-only slide. Value refreshes retain
    // their opacity-only reveal; neither variant remounts or measures content.
    const keyframes = offsetY
      ? [
          { opacity: initialOpacity, transform: `translateY(${offsetY}px)` },
          { opacity: 1, transform: "none" },
        ]
      : [{ opacity: initialOpacity }, { opacity: 1 }];
    const animation = element.current?.animate?.(keyframes, {
      duration,
      easing: offsetY ? "cubic-bezier(0.22, 1, 0.36, 1)" : "ease-out",
    });
    return () => animation?.cancel();
  }, [valueKey, initialOpacity, duration, offsetY]);

  const Component = asChild ? Slot.Root : "span";
  return (
    <Component {...props} ref={element} data-slot="value-transition">
      {children}
    </Component>
  );
}
