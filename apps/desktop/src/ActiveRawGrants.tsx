import { useEffect, useState, type ReactNode } from "react";

import { DataRow } from "@/components/DataRow";
import { FormMessage } from "@/components/FormMessage";
import { SectionKicker } from "@/components/SectionKicker";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { revokeRawGrant } from "./bridge";
import { useT } from "./i18n";
import { formatRemaining, type RawAccessGrant } from "./raw-access-model";

const TICK_MS = 1_000;

/**
 * The timed grants still running, each with the time it has left and a way
 * to end it now. Shared by the approval window and the tray popover; the
 * caller refreshes the list once `onRevoked` reports a grant gone.
 */
export function ActiveRawGrants({
  className,
  grants,
  note,
  onRevoked,
  title,
}: {
  className?: string;
  grants: RawAccessGrant[];
  /** Shown under the list, and gone with it. */
  note?: ReactNode;
  onRevoked: (grant: RawAccessGrant) => void;
  /** Shown above the list, and gone with it once nothing is running. */
  title: ReactNode;
}) {
  const t = useT();
  const [now, setNow] = useState(() => Date.now());
  const [revoking, setRevoking] = useState<ReadonlySet<string>>(new Set());
  const [error, setError] = useState<string | null>(null);

  const running = grants.filter((grant) => Date.parse(grant.expires_at) > now);
  const hasRunning = running.length > 0;
  useEffect(() => {
    if (!hasRunning) return;
    const timer = setInterval(() => setNow(Date.now()), TICK_MS);
    return () => clearInterval(timer);
  }, [hasRunning]);

  const revoke = (grant: RawAccessGrant) => {
    setError(null);
    setRevoking((previous) => new Set(previous).add(grant.grant_id));
    void revokeRawGrant(grant.grant_id)
      .then(
        () => onRevoked(grant),
        (reason: unknown) =>
          setError(
            t("rawAccess.active.revokeFailed", {
              message:
                reason instanceof Error ? reason.message : String(reason),
            }),
          ),
      )
      .finally(() =>
        setRevoking((previous) => {
          const next = new Set(previous);
          next.delete(grant.grant_id);
          return next;
        }),
      );
  };

  if (!hasRunning) return null;
  return (
    <div className={cn("grid gap-2", className)} data-slot="active-raw-grants">
      <SectionKicker>{title}</SectionKicker>
      <div className="overflow-hidden rounded-md border">
        {running.map((grant) => (
          <DataRow
            className="px-3 py-2"
            data-grant-id={grant.grant_id}
            key={grant.grant_id}
          >
            <div className="min-w-0 flex-1">
              <strong className="block truncate text-xs font-medium">
                {grant.client_name || t("rawAccess.unnamedClient")}
              </strong>
              <span className="block truncate text-micro text-text-secondary tabular-nums">
                {t(
                  grant.scope === "all_requests"
                    ? "rawAccess.active.scopeAll"
                    : "rawAccess.active.scopeRequest",
                )}
                {" · "}
                {t("rawAccess.active.remaining", {
                  time: formatRemaining(grant.expires_at, now),
                })}
              </span>
            </div>
            <Button
              disabled={revoking.has(grant.grant_id)}
              onClick={() => revoke(grant)}
              size="sm"
              type="button"
              variant="outline"
            >
              {t("rawAccess.active.revoke")}
            </Button>
          </DataRow>
        ))}
      </div>
      {note ? <p className="text-micro text-muted-foreground">{note}</p> : null}
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
    </div>
  );
}
