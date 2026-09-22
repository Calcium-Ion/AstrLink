import { useLayoutEffect, useRef, useState, type HTMLAttributes, type ReactNode } from "react";
import { Slot } from "radix-ui";

import { cn } from "@/lib/utils";

/** A fixed header aligned with the content inside a separate native scrollport. */
export function ScrollWorkspace({
  children,
  className,
  contentAsChild = false,
  contentClassName,
  contentSlot = "scroll-workspace-content",
  header,
  headerClassName,
  ...props
}: HTMLAttributes<HTMLDivElement> & {
  header: ReactNode;
  headerClassName?: string;
  contentAsChild?: boolean;
  contentClassName?: string;
  contentSlot?: string;
}) {
  const contentRef = useRef<HTMLDivElement>(null);
  const [gutter, setGutter] = useState(0);
  const Content = contentAsChild ? Slot.Root : "div";

  useLayoutEffect(() => {
    const content = contentRef.current;
    if (!content) return;
    // Measure native/overlay scrollbars instead of assuming a platform width.
    const syncGutter = () => setGutter(content.offsetWidth - content.clientWidth);
    syncGutter();
    const observer = new ResizeObserver(syncGutter);
    observer.observe(content);
    return () => observer.disconnect();
  }, []);

  return (
    <div
      className={cn("flex h-full min-h-0 min-w-0 flex-col gap-3 overflow-hidden", className)}
      data-slot="scroll-workspace"
      {...props}
    >
      <div
        className={cn("flex shrink-0 flex-col gap-3", headerClassName)}
        data-slot="scroll-workspace-header"
        style={{ paddingInlineEnd: gutter }}
      >
        {header}
      </div>
      <Content
        className={cn("min-h-0 flex-1 overflow-y-auto overscroll-contain [scrollbar-gutter:stable]", contentClassName)}
        data-slot={contentSlot}
        ref={contentRef}
      >
        {children}
      </Content>
    </div>
  );
}
