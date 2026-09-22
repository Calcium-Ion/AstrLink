import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { useT } from "@/i18n";

/** Model identity and its optional requested reasoning level share one line. */
export function ModelLabel({
  className,
  fallback = "—",
  model,
  reasoningEffort,
}: {
  className?: string;
  fallback?: string;
  model: string | null;
  reasoningEffort?: string | null;
}) {
  const t = useT();
  const label = model ?? fallback;
  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-full items-center gap-1.5",
        className,
      )}
    >
      <ModelBrandIcon model={model} />
      <span className="min-w-0 truncate" title={label}>
        {label}
      </span>
      {reasoningEffort ? (
        <Badge
          aria-label={`${t("records.reasoningEffort")}: ${reasoningEffort}`}
          title={`${t("records.reasoningEffort")}: ${reasoningEffort}`}
          variant="secondary"
        >
          {reasoningEffort}
        </Badge>
      ) : null}
    </span>
  );
}
