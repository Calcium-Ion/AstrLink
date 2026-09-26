import {
  ClaudeCodeColor,
  ClineMono,
  CodexColor,
  CursorMono,
  GeminiColor,
  GrokMono,
  OpenClawColor,
  OpenCodeMono,
  PiMono,
} from "@/components/brand-icons";
import { Bot } from "@/components/icons";
import { cn } from "@/lib/utils";
import { useT } from "../i18n";
import type { ClientType } from "../request-record-model";

const clients = {
  codex: { name: "Codex", Mark: CodexColor },
  claude_code: { name: "Claude Code", Mark: ClaudeCodeColor },
  cursor: { name: "Cursor", Mark: CursorMono },
  grok_cli: { name: "Grok CLI", Mark: GrokMono },
  gemini_cli: { name: "Gemini CLI", Mark: GeminiColor },
  opencode: { name: "OpenCode", Mark: OpenCodeMono },
  openclaw: { name: "OpenClaw", Mark: OpenClawColor },
  cline: { name: "Cline", Mark: ClineMono },
  pi: { name: "Pi", Mark: PiMono },
} satisfies Record<
  Exclude<ClientType, "unknown">,
  { name: string; Mark: typeof CodexColor }
>;

/** Named, non-interactive mark suitable for use inside a clickable record row. */
export function ClientTypeIcon({
  clientType,
  className,
  size = 20,
}: {
  clientType?: ClientType | null;
  className?: string;
  size?: number;
}) {
  const t = useT();
  const client =
    clientType && clientType !== "unknown" ? clients[clientType] : undefined;
  const label = t("records.clientType", {
    client: client?.name ?? t("records.unknownClient"),
  });
  const Mark = client?.Mark;
  // Pi's filled mark reaches the viewBox edges and looks heavier than the
  // outline marks. Inset the artwork while keeping every client's slot equal.
  const markSize = clientType === "pi" ? size * 0.8 : size;
  return (
    <span
      aria-label={label}
      className={cn(
        "inline-flex shrink-0 items-center justify-center text-muted-foreground",
        className,
      )}
      role="img"
      style={{ width: size, height: size }}
      title={label}
    >
      <span
        aria-hidden="true"
        className="inline-flex"
        style={{ width: markSize, height: markSize }}
      >
        {Mark ? (
          <Mark className="size-full" size={markSize} />
        ) : (
          <Bot className="size-full" size={markSize} />
        )}
      </span>
    </span>
  );
}
