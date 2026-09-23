import {
  AnthropicMono,
  ClaudeColor,
  CodexColor,
  DeepSeekColor,
  DoubaoColor,
  GeminiColor,
  GrokMono,
  KimiMono,
  MinimaxColor,
  OpenAIMono,
  OpenCodeMono,
  QwenColor,
  ZhipuColor,
} from "@/components/brand-icons";
import { Connect as Cable } from "@/components/icons";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

import newapiLogo from "../assets/newapi-logo.svg";
import { serviceKindLabel, type ServiceKind } from "../service-model";

function kindMark(kind: ServiceKind, size: number): ReactNode {
  switch (kind) {
    case "newapi":
      return (
        <img
          src={newapiLogo}
          alt=""
          aria-hidden="true"
          width={size}
          height={size}
        />
      );
    case "codex_subscription":
      return <CodexColor size={size} />;
    case "claude_subscription":
      return <ClaudeColor size={size} />;
    case "grok_subscription":
      return <GrokMono size={size} />;
    case "opencode_go":
    case "opencode_zen":
      return <OpenCodeMono size={size} />;
    case "moonshot":
    case "kimi_coding":
      return <KimiMono size={size} />;
    case "glm":
    case "glm_coding":
      return <ZhipuColor size={size} />;
    case "minimax":
    case "minimax_coding":
      return <MinimaxColor size={size} />;
    case "openai":
    case "openai_compatible":
      return <OpenAIMono size={size} />;
    case "anthropic":
      return <AnthropicMono size={size} />;
    case "gemini":
      return <GeminiColor size={size} />;
    case "deepseek":
      return <DeepSeekColor size={size} />;
    case "qwen":
      return <QwenColor size={size} />;
    case "doubao":
      return <DoubaoColor size={size} />;
    case "xai":
      return <GrokMono size={size} />;
    case "custom":
      return (
        <Cable
          aria-hidden="true"
          className="text-muted-foreground"
          size={size}
        />
      );
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
      className={cn(
        "inline-flex shrink-0 items-center justify-center",
        className,
      )}
      role="img"
      style={{ height: size, width: size }}
    >
      {kindMark(kind, size)}
    </span>
  );
}
