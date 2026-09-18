import { useAnimation, type AnimationDefinition } from "motion/react";
import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  type ComponentPropsWithoutRef,
  type ReactNode,
} from "react";

import { cn } from "@/lib/utils";

export interface AnimatedIconProps extends ComponentPropsWithoutRef<"svg"> {
  size?: number | string;
  animateOnHover?: boolean;
}

interface AnimationOptions {
  animate?: AnimationDefinition[];
  normal?: string;
}

const interactiveSelector =
  'button, a[href], summary, label, [role="button"], [role="tab"], [role="menuitem"], [role="menuitemcheckbox"], [role="menuitemradio"], [role="option"], [role="checkbox"], [role="radio"], [role="switch"]';

// Keep upstream artwork/variants while exposing a single SVG to Radix and the
// shared controls. Parent listeners also cover buttons whose SVG ignores hits.
export function createAnimatedIcon(
  name: string,
  artwork: (controls: ReturnType<typeof useAnimation>) => ReactNode,
  { animate = ["animate"], normal = "normal" }: AnimationOptions = {},
) {
  const Icon = forwardRef<SVGSVGElement, AnimatedIconProps>(
    function AnimatedIcon(
      {
        size = 24,
        className,
        strokeWidth = 2,
        animateOnHover = true,
        children,
        ...props
      },
      forwardedRef,
    ) {
      const controls = useAnimation();
      const svgRef = useRef<SVGSVGElement>(null);
      useImperativeHandle(forwardedRef, () => svgRef.current!, []);

      useEffect(() => {
        const svg = svgRef.current;
        if (!svg || !animateOnHover) return;
        const trigger = svg.closest(interactiveSelector) ?? svg;
        const reducedMotion = window.matchMedia(
          "(prefers-reduced-motion: reduce)",
        );
        let hovered = false;
        let focused = false;
        let active = false;
        let generation = 0;

        const update = () => {
          const disabled = trigger.matches(':disabled, [aria-disabled="true"]');
          const next =
            !reducedMotion.matches && !disabled && (hovered || focused);
          if (next === active) return;
          active = next;
          const current = ++generation;
          controls.stop();
          if (reducedMotion.matches) {
            controls.set(normal);
          } else if (next) {
            void (async () => {
              for (const target of animate) {
                if (current !== generation) return;
                await controls.start(target);
              }
            })();
          } else {
            void controls.start(normal);
          }
        };
        const enter = () => {
          hovered = true;
          update();
        };
        const leave = () => {
          hovered = false;
          update();
        };
        const focus = () => {
          focused = true;
          update();
        };
        const blur = (event: Event) => {
          if (
            trigger.contains((event as FocusEvent).relatedTarget as Node | null)
          )
            return;
          focused = false;
          update();
        };

        trigger.addEventListener("mouseenter", enter);
        trigger.addEventListener("mouseleave", leave);
        trigger.addEventListener("focusin", focus);
        trigger.addEventListener("focusout", blur);
        reducedMotion.addEventListener("change", update);
        return () => {
          ++generation;
          controls.stop();
          trigger.removeEventListener("mouseenter", enter);
          trigger.removeEventListener("mouseleave", leave);
          trigger.removeEventListener("focusin", focus);
          trigger.removeEventListener("focusout", blur);
          reducedMotion.removeEventListener("change", update);
        };
      }, [animateOnHover, controls]);

      return (
        <svg
          ref={svgRef}
          xmlns="http://www.w3.org/2000/svg"
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth={strokeWidth}
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden={
            props["aria-label"] || props["aria-labelledby"] ? undefined : true
          }
          focusable="false"
          data-animated-icon={name}
          className={cn("shrink-0", className)}
          {...props}
        >
          {artwork(controls)}
          {children}
        </svg>
      );
    },
  );
  Icon.displayName = `${name}Icon`;
  return Icon;
}

export type AnimatedIcon = ReturnType<typeof createAnimatedIcon>;
