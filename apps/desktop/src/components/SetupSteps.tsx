import { Check } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** A compact, keyboard-accessible checklist for resumable setup flows. */
export function SetupSteps({
  label,
  steps,
  current,
  onSelect,
  completeLabel,
}: {
  label: string;
  steps: { title: string; description: string; complete: boolean }[];
  current: number;
  onSelect: (index: number) => void;
  completeLabel: string;
}) {
  return (
    <ol
      aria-label={label}
      className="grid grid-cols-3 gap-1 @min-[720px]/workspace-surface:grid-cols-1 @min-[720px]/workspace-surface:gap-2"
    >
      {steps.map((step, index) => (
        <li key={step.title}>
          <Button
            aria-current={current === index ? "step" : undefined}
            className={cn(
              "h-full w-full flex-col justify-start gap-2 px-2 py-2 text-center whitespace-normal @min-[720px]/workspace-surface:flex-row @min-[720px]/workspace-surface:gap-3 @min-[720px]/workspace-surface:px-3 @min-[720px]/workspace-surface:py-3 @min-[720px]/workspace-surface:text-left",
              current === index && "bg-accent text-accent-foreground",
            )}
            onClick={() => onSelect(index)}
            variant="ghost"
          >
            <span
              className={cn(
                "flex size-7 shrink-0 items-center justify-center rounded-full border text-xs tabular-nums",
                step.complete &&
                  "border-success-foreground/30 text-success-foreground",
                current === index &&
                  !step.complete &&
                  "border-primary bg-primary text-primary-foreground",
              )}
            >
              {step.complete ? (
                <Check aria-hidden="true" className="size-4" />
              ) : (
                index + 1
              )}
            </span>
            <span className="min-w-0">
              <span className="block text-xs font-medium @min-[720px]/workspace-surface:text-sm">
                {step.title}
                {step.complete ? (
                  <span className="sr-only"> · {completeLabel}</span>
                ) : null}
              </span>
              <span className="mt-1 hidden text-xs font-normal leading-relaxed text-muted-foreground @min-[720px]/workspace-surface:block">
                {step.description}
              </span>
            </span>
          </Button>
        </li>
      ))}
    </ol>
  );
}
