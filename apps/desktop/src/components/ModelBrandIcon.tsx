import { ModelIcon } from "@lobehub/icons";

import { cn } from "@/lib/utils";

export function ModelBrandIcon({
  className,
  model,
  size = 14,
}: {
  className?: string;
  model: string | null | undefined;
  size?: number;
}) {
  if (!model?.trim()) return null;

  return (
    <span
      aria-hidden="true"
      className={cn("inline-flex shrink-0 items-center", className)}
    >
      <ModelIcon model={model} size={size} type="color" />
    </span>
  );
}
