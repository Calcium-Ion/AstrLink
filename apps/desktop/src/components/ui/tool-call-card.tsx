/**
 * Adapted from affectUI's free Tool Call Card (Clean), MIT licensed.
 * https://www.affectui.com/r/tool-call-card-clean.json
 * Copyright (c) 2026 Florian Höller (adfects).
 * See LICENSES/affectui-MIT.txt.
 *
 * Uses AstrLink's primitives, tokens and code sections. Tool execution happens
 * in the client, so this observer variant has no stopwatch or retry action.
 */
import { useState, type ComponentProps, type ReactNode } from "react";

import {
  BadgeAlert,
  ChevronRight,
  CircleHelp,
  Boxes,
  MessageSquare,
} from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";

import "./tool-call-card.css";

export type ToolCallStatus = "received" | "error" | "unavailable";

/**
 * The free card's compact header, also usable as contiguous virtualized rows.
 * Edges belong to each row so a long trace need not mount an entire card.
 */
export function CallCardRow({
  edge,
  className,
  ...props
}: ComponentProps<typeof Button> & {
  edge?: "first" | "middle" | "last" | "only";
}) {
  return (
    <Button
      variant="ghost"
      className={cn(
        "min-w-0 justify-start gap-2 text-left text-xs font-normal hover:bg-muted/60",
        edge && "rounded-none border-x border-border/70 bg-card shadow-none",
        (edge === "first" || edge === "only") && "rounded-t-md border-t",
        (edge === "last" || edge === "only") && "rounded-b-md border-b",
        className,
      )}
      {...props}
    />
  );
}

/** An arriving error opens untouched disclosures; a reader's choice wins. */
function useToolDisclosure(failed: boolean) {
  const [override, setOverride] = useState<boolean | null>(null);
  return { open: override ?? failed, onOpenChange: setOverride };
}

export function ToolCallCard({
  name,
  summary,
  icon,
  status,
  statusLabel,
  children,
  className,
  ...props
}: Omit<ComponentProps<"div">, "children"> & {
  name: string;
  summary: string;
  icon?: ReactNode;
  status: ToolCallStatus;
  statusLabel: string;
  children: ReactNode;
}) {
  const disclosure = useToolDisclosure(status === "error");
  const StatusIcon =
    status === "error"
      ? BadgeAlert
      : status === "received"
        ? MessageSquare
        : CircleHelp;

  return (
    <Collapsible
      {...props}
      {...disclosure}
      className={cn(
        "tool-call-card group/tool-call min-w-0 overflow-hidden rounded-md border bg-card text-xs",
        status === "error" && "border-destructive/30",
        className,
      )}
      data-slot="tool-call-card"
      data-status={status}
    >
      <CollapsibleTrigger asChild>
        <CallCardRow className="tool-call-trigger h-auto min-h-8 w-full min-w-0 justify-start gap-2 rounded-none px-2.5 py-1.5 text-left text-xs font-normal hover:bg-muted/60">
          <span
            aria-hidden="true"
            className="tool-call-icon shrink-0 text-muted-foreground [&_svg]:size-3.5"
          >
            {icon ?? <Boxes />}
          </span>
          <span className="flex min-w-0 flex-1 items-baseline gap-2">
            <code
              className="max-w-[45%] shrink-0 truncate font-semibold"
              title={name}
            >
              {name}
            </code>
            <span
              className="min-w-0 truncate text-muted-foreground"
              title={summary}
            >
              {summary}
            </span>
          </span>
          <span
            key={status}
            className={cn(
              "tool-call-status flex shrink-0 items-center gap-1 text-micro text-muted-foreground",
              status === "error" && "text-danger-foreground",
            )}
            title={statusLabel}
          >
            <StatusIcon aria-hidden="true" className="size-3.5" />
            <span className={status === "error" ? "" : "sr-only"}>
              {statusLabel}
            </span>
          </span>
          <ChevronRight
            aria-hidden="true"
            className="size-3 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/tool-call:rotate-90 motion-reduce:transition-none"
          />
        </CallCardRow>
      </CollapsibleTrigger>
      <CollapsibleContent
        className="tool-call-content"
        inert={!disclosure.open || undefined}
        aria-hidden={!disclosure.open || undefined}
      >
        <div className="space-y-2 border-t p-2.5">{children}</div>
      </CollapsibleContent>
    </Collapsible>
  );
}

/** A compact summary for calls from one model response, with failures exposed. */
export function ToolCallGroup({
  label,
  summary,
  errorLabel,
  icons,
  children,
}: {
  label: string;
  summary: string;
  errorLabel?: string;
  icons: ReactNode[];
  children: ReactNode;
}) {
  const disclosure = useToolDisclosure(Boolean(errorLabel));
  return (
    <Collapsible
      {...disclosure}
      className="tool-call-group group/tool-group min-w-0"
      data-slot="tool-call-group"
    >
      <CollapsibleTrigger asChild>
        <Button
          className="tool-call-group-trigger h-8 w-full min-w-0 justify-start gap-2 px-1 text-left text-xs font-normal"
          variant="ghost"
        >
          <span
            aria-hidden="true"
            className="tool-call-group-icons flex shrink-0 py-1"
          >
            {icons.slice(0, 3).map((icon, index) => (
              <span
                key={index}
                className="tool-call-group-icon relative flex size-6 items-center justify-center rounded-md border bg-card text-muted-foreground shadow-xs [&_svg]:size-3"
                style={{ animationDelay: `${index * 45}ms`, zIndex: 3 - index }}
              >
                {icon}
              </span>
            ))}
          </span>
          <span className="shrink-0 font-medium">{label}</span>
          <span
            className="min-w-0 flex-1 truncate text-muted-foreground"
            title={summary}
          >
            {summary}
          </span>
          {errorLabel ? (
            <span className="shrink-0 text-micro text-danger-foreground">
              {errorLabel}
            </span>
          ) : null}
          <ChevronRight
            aria-hidden="true"
            className="size-3 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/tool-group:rotate-90 motion-reduce:transition-none"
          />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent
        className="tool-call-content"
        inert={!disclosure.open || undefined}
        aria-hidden={!disclosure.open || undefined}
      >
        <div className="space-y-1.5 pb-1">{children}</div>
      </CollapsibleContent>
    </Collapsible>
  );
}
