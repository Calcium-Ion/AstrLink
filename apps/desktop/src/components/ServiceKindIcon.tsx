import { Anthropic, Codex, Gemini, NewAPI, OpenAI } from "@lobehub/icons";
import { Cable } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

import { serviceKindLabel, type ServiceKind } from "../service-model";

/** NewAPI.Color fills the viewBox edge-to-edge; other brand marks have padding. */
const OPTICAL_SCALE: Partial<Record<ServiceKind, number>> = {
  newapi: 0.75,
};

function markSize(kind: ServiceKind, size: number): number {
  return Math.round(size * (OPTICAL_SCALE[kind] ?? 1));
}

function kindMark(kind: ServiceKind, size: number): ReactNode {
  switch (kind) {
    case "newapi":
      return <NewAPI.Color size={size} />;
    case "codex_subscription":
      return <Codex.Color size={size} />;
    case "openai":
    case "openai_compatible":
      return <OpenAI size={size} />;
    case "anthropic":
      return <Anthropic size={size} />;
    case "gemini":
      return <Gemini.Color size={size} />;
    case "custom":
      return <Cable aria-hidden="true" className="text-muted-foreground" size={size} />;
  }
}

export function ServiceKindIcon({
  className,
  kind,
  size = 20,
}: {
  className?: string;
  kind: ServiceKind;
  size?: number;
}) {
  return (
    <span
      aria-label={serviceKindLabel(kind)}
      className={cn("inline-flex shrink-0 items-center justify-center", className)}
      role="img"
      style={{ height: size, width: size }}
    >
      {kindMark(kind, markSize(kind, size))}
    </span>
  );
}
