import {
  AnthropicMono,
  AntigravityColor,
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

type Mark =
  | "newapi"
  | "codex"
  | "claude"
  | "grok"
  | "antigravity"
  | "opencode"
  | "kimi"
  | "zhipu"
  | "minimax"
  | "openai"
  | "anthropic"
  | "gemini"
  | "deepseek"
  | "qwen"
  | "doubao"
  | "custom";

const kindMarks: Record<ServiceKind, Mark> = {
  newapi: "newapi",
  codex_subscription: "codex",
  claude_subscription: "claude",
  grok_subscription: "grok",
  antigravity_subscription: "antigravity",
  opencode_go: "opencode",
  opencode_zen: "opencode",
  moonshot: "kimi",
  kimi_coding: "kimi",
  glm: "zhipu",
  glm_coding: "zhipu",
  minimax: "minimax",
  minimax_coding: "minimax",
  openai: "openai",
  openai_compatible: "openai",
  anthropic: "anthropic",
  gemini: "gemini",
  deepseek: "deepseek",
  qwen: "qwen",
  doubao: "doubao",
  xai: "grok",
  custom: "custom",
};

function renderMark(mark: Mark, size: number): ReactNode {
  switch (mark) {
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
    case "codex":
      return <CodexColor size={size} />;
    case "claude":
      return <ClaudeColor size={size} />;
    case "grok":
      return <GrokMono size={size} />;
    case "opencode":
      return <OpenCodeMono size={size} />;
    case "kimi":
      return <KimiMono size={size} />;
    case "zhipu":
      return <ZhipuColor size={size} />;
    case "minimax":
      return <MinimaxColor size={size} />;
    case "openai":
      return <OpenAIMono size={size} />;
    case "anthropic":
      return <AnthropicMono size={size} />;
    case "antigravity":
      return <AntigravityColor size={size} />;
    case "gemini":
      return <GeminiColor size={size} />;
    case "deepseek":
      return <DeepSeekColor size={size} />;
    case "qwen":
      return <QwenColor size={size} />;
    case "doubao":
      return <DoubaoColor size={size} />;
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

/** Whether another kind draws the same logo, so the logo alone is ambiguous. */
export function kindMarkIsShared(kind: ServiceKind): boolean {
  const mark = kindMarks[kind];
  return (
    Object.values(kindMarks).filter((candidate) => candidate === mark).length >
    1
  );
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
      {renderMark(kindMarks[kind], size)}
    </span>
  );
}
