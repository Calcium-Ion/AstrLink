import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Route } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { useT } from "@/i18n";

/**
 * Model identity and its optional requested reasoning level share one line.
 * `redirectedTo` adds the model a routing redirect sent the call to, drawn as
 * "model → redirectedTo" so both names keep their brand marks.
 * Narrow columns can show the target with the full redirect in its hint.
 * `reasoningUpdate` is the level a configuration_update item set mid
 * conversation, shown next to the request's own level when they differ.
 */
export function ModelLabel({
  className,
  fallback = "—",
  model,
  reasoningEffort,
  reasoningUpdate,
  redirectDisplay = "inline",
  redirectedTo,
}: {
  className?: string;
  fallback?: string;
  model: string | null;
  reasoningEffort?: string | null;
  reasoningUpdate?: string | null;
  redirectDisplay?: "inline" | "target";
  redirectedTo?: string;
}) {
  const t = useT();
  const targetOnly = redirectDisplay === "target" && Boolean(redirectedTo);
  const displayModel = targetOnly ? redirectedTo : model;
  const label = displayModel ?? fallback;
  const redirectHint = redirectedTo
    ? t("records.modelRedirectNote", {
        from: model ?? fallback,
        to: redirectedTo,
      })
    : undefined;
  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-full items-center gap-1.5",
        className,
      )}
      data-redirected-to={redirectedTo || undefined}
    >
      <ModelBrandIcon model={displayModel} />
      <span
        className="min-w-0 truncate"
        title={targetOnly ? redirectHint : label}
      >
        {label}
      </span>
      {targetOnly ? (
        <span
          className="inline-flex shrink-0 cursor-help text-muted-foreground"
          title={redirectHint}
        >
          <Route animateOnHover={false} aria-hidden="true" className="size-3" />
          <span className="sr-only">{redirectHint}</span>
        </span>
      ) : redirectedTo ? (
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
      {reasoningUpdate && reasoningUpdate !== reasoningEffort ? (
        <Badge
          aria-label={`${t("records.reasoningEffortUpdate")}: ${reasoningUpdate}`}
          title={`${t("records.reasoningEffortUpdate")}: ${reasoningUpdate}`}
          variant="secondary"
        >
          {reasoningEffort ? (
            <span aria-hidden="true" className="mr-1 text-muted-foreground">
              →
            </span>
          ) : null}
          {reasoningUpdate}
        </Badge>
      ) : null}
    </span>
  );
}
