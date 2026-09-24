import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { useT } from "@/i18n";

/**
 * Model identity and its optional requested reasoning level share one line.
 * `redirectedTo` adds the model a routing redirect sent the call to, drawn as
 * "model → redirectedTo" so both names keep their brand marks.
 */
export function ModelLabel({
  className,
  fallback = "—",
  model,
  reasoningEffort,
  redirectedTo,
}: {
  className?: string;
  fallback?: string;
  model: string | null;
  reasoningEffort?: string | null;
  redirectedTo?: string;
}) {
  const t = useT();
  const label = model ?? fallback;
  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-full items-center gap-1.5",
        className,
      )}
      data-redirected-to={redirectedTo || undefined}
    >
      <ModelBrandIcon model={model} />
      <span className="min-w-0 truncate" title={label}>
        {label}
      </span>
      {redirectedTo ? (
        <>
          <span aria-hidden="true" className="shrink-0 text-muted-foreground">
            →
          </span>
          <span className="sr-only">{t("records.redirectedTo")}</span>
          <ModelBrandIcon model={redirectedTo} />
          <span className="min-w-0 truncate" title={redirectedTo}>
            {redirectedTo}
          </span>
        </>
      ) : null}
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
