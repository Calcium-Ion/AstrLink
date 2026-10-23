import type { ComponentProps } from "react";

import {
  CherryStudioColor,
  ClaudeCodeColor,
  ClineMono,
  CodeBuddyColor,
  CursorMono,
  DeepSeekHarnessMono,
  GithubCopilotMono,
  KiloCodeMono,
  OpenClawColor,
  PiColor,
  RooCodeMono,
} from "@/components/brand-icons";
import { Bot } from "@/components/icons";
import { vendorMarks } from "@/components/vendor-marks";
import { cn } from "@/lib/utils";
import { i18n, useT } from "../i18n";
import type { ClientType } from "../request-record-model";

// Clients with their own product artwork use it; the rest draw their vendor's
// mark from `vendorMarks`. Clients without a published mark get a monogram so
// they stay distinct from the unknown-client fallback in icon-only rows.
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
  pi: { name: "Pi", Mark: PiColor },
  deepseek_harness: { name: "DeepSeek Harness", Mark: DeepSeekHarnessMono },
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

/**
 * Clients with how many of something each one has, in the order given. Past
 * `limit`, the remaining clients fold into one "+N" that counts them.
 */
export function ClientTypeCounts({
  className,
  clients,
  label,
  limit = 4,
  size = 14,
  ...props
}: Omit<ComponentProps<"span">, "children"> & {
  clients: readonly { clientType: ClientType; count: number }[];
  /** Names every client and count, for assistive technology and on hover. */
  label: string;
  limit?: number;
  size?: number;
}) {
  const shown = clients.length > limit ? clients.slice(0, limit - 1) : clients;
  const hidden = clients.length - shown.length;
  return (
    <span
      {...props}
      aria-label={label}
      className={cn(
        "inline-flex shrink-0 items-center gap-2 tabular-nums",
        className,
      )}
      role="img"
      title={label}
    >
      {shown.map(({ clientType, count }) => (
        <span className="inline-flex items-center gap-0.5" key={clientType}>
          <ClientTypeIcon clientType={clientType} decorative size={size} />
          {count > 1 ? count : null}
        </span>
      ))}
      {hidden > 0 ? <span>+{hidden}</span> : null}
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
