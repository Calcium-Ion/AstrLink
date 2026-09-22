import { DeepSeek, Doubao, Grok, Qwen, Anthropic, Claude, Codex, Gemini, Kimi, Minimax, NewAPI, OpenAI, OpenCode, Zhipu } from "@lobehub/icons";
import { Connect as Cable } from "@/components/icons";
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
    case "claude_subscription":
      return <Claude.Color size={size} />;
    case "opencode_go":
    case "opencode_zen":
      return <OpenCode size={size} />;
    case "moonshot":
    case "kimi_coding":
      return <Kimi size={size} />;
    case "glm":
    case "glm_coding":
      return <Zhipu.Color size={size} />;
    case "minimax":
    case "minimax_coding":
      return <Minimax.Color size={size} />;
    case "openai":
    case "openai_compatible":
      return <OpenAI size={size} />;
    case "anthropic":
      return <Anthropic size={size} />;
    case "gemini":
      return <Gemini.Color size={size} />;
    case "deepseek":
      return <DeepSeek.Color size={size} />;
    case "qwen":
      return <Qwen.Color size={size} />;
    case "doubao":
      return <Doubao.Color size={size} />;
    case "xai":
      return <Grok size={size} />;
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
