import { ClientMark } from "@/components/ClientMark";
import { StatusBadge } from "@/components/StatusBadge";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

import {
  configAttention,
  type ClientConfigClient,
  type ClientConfigStatus,
} from "../client-config-model";
import { clientLabel, configBadge, placeLabel } from "../client-setup-clients";
import { currentLocale, useT } from "../i18n";

/** Marks shown before the rest fold into one "+N". */
const markLimit = 6;

/**
 * The clients a token is configured in, shown by their icons; past six, the
 * rest fold into one "+N". A dot marks a client whose config needs the user;
 * hovering lists every client's state. `available` holds the models the
 * gateway serves; null while unknown.
 */
export function TokenClientMarks({
  className,
  tokenId,
  tokenName,
  configs,
  available,
}: {
  className?: string;
  tokenId: string;
  tokenName: string;
  /** This token's configs, one per client and place. */
  configs: readonly ClientConfigStatus[];
  available: ReadonlySet<string> | null;
}) {
  const t = useT();
  const mine = configs.filter(
    (status) =>
      status.token_id === tokenId && status.state !== "not_configured",
  );
  if (mine.length === 0) return null;
  const clients = [...new Set(mine.map((status) => status.client))];
  const shown =
    clients.length > markLimit ? clients.slice(0, markLimit - 1) : clients;
  const hidden = clients.slice(shown.length);
  const attention = (status: ClientConfigStatus) =>
    configAttention(status, tokenId, available);
  const dotOf = (ids: readonly ClientConfigClient[]) => {
    const kinds = mine
      .filter((status) => ids.includes(status.client))
      .map((status) => attention(status)?.kind)
      .filter(Boolean);
    if (kinds.includes("invalid")) return "bg-destructive";
    return kinds.length > 0 ? "bg-warning" : null;
  };
  const dot = (tone: string | null) =>
    tone ? (
      <span
        aria-hidden="true"
        className={cn(
          "absolute -top-0.5 -right-0.5 size-[7px] rounded-full border-[1.5px] border-card",
          tone,
        )}
      />
    ) : null;
  const label = t("clientSetup.configuredIn", {
    clients: clientNames(clients),
  });
  return (
    <TooltipProvider delayDuration={200}>
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            aria-label={label}
            className={cn(
              "inline-flex shrink-0 items-center gap-0.5 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/30",
              className,
            )}
            data-slot="token-client-marks"
            role="img"
            tabIndex={0}
          >
            {shown.map((id) => {
              const tone = dotOf([id]);
              return (
                <span
                  key={id}
                  className="relative inline-flex size-4.5 items-center justify-center rounded-sm border bg-card"
                  data-attention={tone ? "true" : undefined}
                  data-client={id}
                >
                  <ClientMark client={id} size={12} />
                  {dot(tone)}
                </span>
              );
            })}
            {hidden.length > 0 ? (
              <span
                className="relative inline-flex h-4.5 min-w-4.5 items-center justify-center rounded-sm border bg-card px-0.5 text-micro leading-none font-medium text-text-secondary tabular-nums"
                data-attention={dotOf(hidden) ? "true" : undefined}
                data-slot="token-client-more"
              >
                +{hidden.length}
                {dot(dotOf(hidden))}
              </span>
            ) : null}
          </span>
        </TooltipTrigger>
        <TooltipContent
          align="start"
          className="grid w-75 max-w-[calc(100vw-24px)] gap-1.5"
          side="bottom"
          sideOffset={6}
          variant="card"
        >
          <p className="font-semibold">
            {t("clientSetup.marksTitle", {
              name: tokenName,
              count: clients.length,
            })}
          </p>
          {mine.map((status) => {
            const badge = configBadge(
              status,
              tokenId,
              attention(status)?.missing,
            );
            return (
              <div
                key={`${status.client}:${status.wsl ?? ""}:${status.target ?? ""}`}
                className="flex min-w-0 items-center gap-2"
              >
                <ClientMark client={status.client} size={14} />
                <span className="min-w-0 truncate">
                  {placeLabel(status.client, status.wsl, status.target)}
                </span>
                {badge ? (
                  <StatusBadge className="ml-auto" tone={badge.tone}>
                    {badge.label}
                  </StatusBadge>
                ) : null}
              </div>
            );
          })}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function clientNames(clients: readonly ClientConfigClient[]): string {
  return new Intl.ListFormat(currentLocale(), { style: "narrow" }).format(
    clients.map(clientLabel),
  );
}
