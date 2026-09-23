import {
  ClaudeCodeColor,
  CodexColor,
  CursorMono,
  GrokMono,
} from "@/components/brand-icons";
import { cn } from "@/lib/utils";

import type { AgentToolId } from "../agent-install-model";

const marks = {
  cursor: CursorMono,
  claude: ClaudeCodeColor,
  codex: CodexColor,
  grok: GrokMono,
} satisfies Record<AgentToolId, typeof CursorMono>;

/** Decorative brand mark; pair it with the tool's visible name. */
export function AgentToolIcon({
  className,
  id,
  size = 20,
}: {
  className?: string;
  id: AgentToolId;
  size?: number;
}) {
  const Mark = marks[id];
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-flex shrink-0 items-center justify-center",
        className,
      )}
      style={{ height: size, width: size }}
    >
      <Mark size={size} />
    </span>
  );
}
