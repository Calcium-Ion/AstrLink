import { Button } from "./ui/button";
import { Check, Ban, ArrowRight, CircleHelp } from "./icons";
import type { GraphStep, RoutingGraph } from "../routing-graph-model";
import { useT } from "../i18n";

export function RoutingGraphTrace({
  steps,
  graph,
  onSelect,
}: {
  steps: GraphStep[];
  graph?: RoutingGraph;
  onSelect?: (id: string) => void;
}) {
  const t = useT();
  return (
    <ol className="grid gap-1" aria-label={t("graph.trace")}>
      {steps.map((step, index) => {
        const node = graph?.nodes.find((item) => item.id === step.node_id);
        const reason = step.reason?.startsWith("matched:")
          ? t("graph.ruleMatched")
          : step.reason?.startsWith("unknown:")
            ? t("graph.unknownRule")
            : step.reason?.startsWith("http_")
              ? step.reason.replace("http_", "HTTP ")
              : t(`graph.reasons.${step.reason ?? ""}`, {
                  defaultValue: step.reason ?? "",
                });
        return (
          <li key={`${index}-${step.node_id}`}>
            <Button
              variant="ghost"
              size="sm"
              type="button"
              className="flex h-auto w-full justify-start items-start gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted disabled:opacity-100"
              disabled={!onSelect}
              onClick={() => onSelect?.(step.node_id)}
            >
              <span className="mt-px text-muted-foreground">
                {step.status === "succeeded" ? (
                  <Check className="size-3.5 text-success-foreground" />
                ) : step.status === "skipped" ? (
                  <Ban className="size-3.5" />
                ) : step.reason?.startsWith("unknown:") ? (
                  <CircleHelp className="size-3.5" />
                ) : (
                  <ArrowRight className="size-3.5" />
                )}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">
                  {node?.model ||
                    node?.upstream_model ||
                    node?.name ||
                    step.model ||
                    step.node_id}
                </span>
                <span className="text-muted-foreground">
                  {t(`graph.status.${step.status}`, {
                    defaultValue: step.status,
                  })}
                  {reason ? ` · ${reason}` : ""}
                </span>
              </span>
            </Button>
          </li>
        );
      })}
    </ol>
  );
}
