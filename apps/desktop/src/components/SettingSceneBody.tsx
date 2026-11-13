import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** The frame of a picture that shows what a setting does. */
export const sceneFrameClass = "rounded-md border bg-muted/70";

/**
 * A panel body with its controls on the left and a picture of their effect
 * on the right: half the width in a medium panel, under the controls in a
 * narrow one.
 */
export function SettingSceneBody({
  children,
  className,
  scene,
}: {
  children: ReactNode;
  className?: string;
  scene: ReactNode;
}) {
  return (
    <div className={cn("@container px-3.5 py-3", className)}>
      <div className="grid gap-3 @[44rem]:grid-cols-2 @[44rem]:gap-4 @[60rem]:grid-cols-[minmax(0,1fr)_minmax(0,30rem)]">
        <div className="min-w-0">{children}</div>
        {scene}
      </div>
    </div>
  );
}
