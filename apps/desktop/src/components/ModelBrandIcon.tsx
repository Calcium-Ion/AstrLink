import { modelMappings } from "@lobehub/icons";

import { Brain } from "@/components/icons";

import { cn } from "@/lib/utils";

const brands = modelMappings.map((mapping) => ({
  ...mapping,
  patterns: mapping.keywords.map((keyword) => new RegExp(keyword, "i")),
}));

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

  const brand = brands.find(({ patterns }) =>
    patterns.some((pattern) => pattern.test(model)),
  );
  const BrandIcon = brand?.Icon.Color ?? brand?.Icon;

  return (
    <span
      aria-hidden="true"
      className={cn("inline-flex shrink-0 items-center", className)}
    >
      {BrandIcon ? (
        <BrandIcon size={size} {...brand?.props} />
      ) : (
        <Brain size={size} />
      )}
    </span>
  );
}
