import {
  ClaudeCodeColor,
  CodexColor,
  CursorMono,
  DeepSeekHarnessMono,
  GrokMono,
  PiColor,
} from "@/components/brand-icons";
import { cn } from "@/lib/utils";

import type { AgentToolId } from "../agent-install-model";

const marks = {
  cursor: CursorMono,
  claude: ClaudeCodeColor,
  codex: CodexColor,
  grok: GrokMono,
  pi: PiColor,
  dsh: DeepSeekHarnessMono,
} satisfies Record<AgentToolId | "pi", typeof CursorMono>;
export type AgentToolIconId = keyof typeof marks;

/** Decorative brand mark; pair it with the tool's visible name. */
export function AgentToolIcon({
  className,
  id,
  size = 20,
}: {
  className?: string;
  id: AgentToolIconId;
  size?: number;
}) {
  const Mark = marks[id];
  // Pi's filled mark reaches the viewBox edges; inset it to match the others.
  const markSize = id === "pi" ? size * 0.8 : size;
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-flex shrink-0 items-center justify-center",
        className,
      )}
      style={{ height: size, width: size }}
    >
      <Mark size={markSize} />
    </span>
  );
}
