import {
  CherryStudioColor,
  ClaudeCodeColor,
  ClineMono,
  CodeBuddyColor,
  CursorMono,
  GithubCopilotMono,
  KiloCodeMono,
  OpenClawColor,
  PiMono,
  RooCodeMono,
} from "@/components/brand-icons";
import { Bot } from "@/components/icons";
import { vendorMarks } from "@/components/vendor-marks";
import { cn } from "@/lib/utils";
import { i18n, useT } from "../i18n";
import type { ClientType } from "../request-record-model";

// Clients named after a vendor draw that vendor's mark from `vendorMarks` so
// they match the vendor's models and provider kinds. Clients without a
// published mark get a monogram so they stay distinct from the unknown-client
// fallback in icon-only rows.
const clients: Record<
  Exclude<ClientType, "unknown">,
  { name: string; Mark?: typeof CursorMono }
> = {
  codex: { name: "Codex", Mark: vendorMarks.codex },
  claude_code: { name: "Claude Code", Mark: ClaudeCodeColor },
  cursor: { name: "Cursor", Mark: CursorMono },
  grok_cli: { name: "Grok CLI", Mark: vendorMarks.grok },
  gemini_cli: { name: "Gemini CLI", Mark: vendorMarks.gemini },
  opencode: { name: "OpenCode", Mark: vendorMarks.opencode },
  openclaw: { name: "OpenClaw", Mark: OpenClawColor },
  cline: { name: "Cline", Mark: ClineMono },
  pi: { name: "Pi", Mark: PiMono },
  deepseek_harness: { name: "DeepSeek Harness", Mark: vendorMarks.deepseek },
  codewhale: { name: "Codewhale" },
  reasonix: { name: "Reasonix" },
  qwen_code: { name: "Qwen Code", Mark: vendorMarks.qwen },
  kimi_code: { name: "Kimi Code", Mark: vendorMarks.kimi },
  codebuddy: { name: "CodeBuddy", Mark: CodeBuddyColor },
  copilot: { name: "GitHub Copilot", Mark: GithubCopilotMono },
  droid: { name: "Droid" },
  crush: { name: "Crush" },
  kilo_code: { name: "Kilo Code", Mark: KiloCodeMono },
  roo_code: { name: "Roo Code", Mark: RooCodeMono },
  mistral_vibe: { name: "Mistral Vibe", Mark: vendorMarks.mistral },
  zed: { name: "Zed" },
  cherry_studio: { name: "Cherry Studio", Mark: CherryStudioColor },
};

/** Product name of a detected client, or the unknown-client label. */
export function clientTypeName(clientType?: ClientType | null): string {
  return clientType && clientType !== "unknown"
    ? clients[clientType].name
    : i18n.t("records.unknownClient");
}

/** Decorative marks for a group of clients whose names are shown beside them. */
export function ClientTypeIcons({
  clientTypes,
  className,
  size = 16,
}: {
  clientTypes: readonly ClientType[];
  className?: string;
  size?: number;
}) {
  return (
    <span className={cn("flex shrink-0 items-center gap-1", className)}>
      {clientTypes.map((clientType) => (
        <ClientTypeIcon
          clientType={clientType}
          decorative
          key={clientType}
          size={size}
        />
      ))}
    </span>
  );
}

/** Named, non-interactive mark suitable for use inside a clickable record row. */
export function ClientTypeIcon({
  clientType,
  className,
  decorative = false,
  size = 20,
}: {
  clientType?: ClientType | null;
  className?: string;
  /** Hide the mark from assistive technology when its name is shown beside it. */
  decorative?: boolean;
  size?: number;
}) {
  const t = useT();
  const client =
    clientType && clientType !== "unknown" ? clients[clientType] : undefined;
  const label = t("records.clientType", {
    client: client?.name ?? t("records.unknownClient"),
  });
  const Mark = client?.Mark;
  // Pi's filled mark and the monogram tile reach the viewBox edges and look
  // heavier than the outline marks. Inset them while keeping every slot equal.
  const markSize = clientType === "pi" || (client && !Mark) ? size * 0.8 : size;
  return (
    <span
      {...(decorative
        ? { "aria-hidden": true }
        : { "aria-label": label, role: "img", title: label })}
      className={cn(
        // Mono brand marks paint with currentColor and their brand colour is
        // black; muting them would gray out the logo. Only the fallback is muted.
        "inline-flex shrink-0 items-center justify-center",
        client ? "text-foreground" : "text-muted-foreground",
        className,
      )}
      style={{ width: size, height: size }}
    >
      <span
        aria-hidden="true"
        className="inline-flex"
        style={{ width: markSize, height: markSize }}
      >
        {Mark ? (
          <Mark className="size-full" size={markSize} />
        ) : client ? (
          <span className="flex size-full items-center justify-center rounded-sm bg-foreground text-micro leading-none font-semibold text-background">
            {client.name[0]}
          </span>
        ) : (
          <Bot className="size-full" size={markSize} />
        )}
      </span>
    </span>
  );
}
