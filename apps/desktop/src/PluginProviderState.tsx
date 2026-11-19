import { useCallback, useEffect, useState } from "react";

import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { IconButton } from "@/components/IconButton";
import { RefreshCw } from "@/components/icons";
import { StatusBadge } from "@/components/StatusBadge";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";

import { getPluginProviderState } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { formatMoment } from "./format-time";
import { useLocale, useT } from "./i18n";
import type {
  PluginBalance,
  PluginBalanceWindow,
  PluginProviderState,
  PluginText,
} from "./plugin-model";
import { localized } from "./RequestPluginInstances";

/** How often an open provider page reads the balance again. */
const refreshEvery = 60 * 60 * 1000;

/**
 * The balance a plugin reads of a provider's account, and whether the
 * plugin rests the provider, on the provider page. Core reads the balance
 * again when the page opens and the one it keeps is an hour old; the page
 * asks again every hour while it is open, and the button reads it now.
 */
export function PluginProviderStateField({
  pluginKey,
  readsBalance,
  serviceId,
}: {
  pluginKey: string;
  readsBalance: boolean;
  serviceId: string;
}) {
  const t = useT();
  const [state, setState] = useState<PluginProviderState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const read = useCallback(
    async (refresh: boolean) => {
      setBusy(true);
      try {
        setState(
          await getPluginProviderState(pluginKey, {
            service_id: serviceId,
            refresh,
          }),
        );
        setError(null);
      } catch (cause) {
        setError(
          controlErrorMessage(cause, t("services.pluginBalance.failed")),
        );
      } finally {
        setBusy(false);
      }
    },
    [pluginKey, serviceId, t],
  );

  useEffect(() => {
    void read(false);
    const timer = window.setInterval(() => void read(false), refreshEvery);
    return () => window.clearInterval(timer);
  }, [read]);

  if (!readsBalance && !state?.resting) return null;
  const failure = error ?? state?.error;
  return (
    <Field
      group
      label={
        readsBalance
          ? t("services.pluginBalance.label")
          : t("services.pluginBalance.status")
      }
    >
      <div className="grid min-w-0 gap-2" data-testid="plugin-provider-state">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {state?.balance ? (
            <BalanceAmount balance={state.balance} />
          ) : readsBalance && !failure ? (
            <span className="text-sm text-muted-foreground">
              {busy
                ? t("services.pluginBalance.reading")
                : t("services.pluginBalance.unread")}
            </span>
          ) : null}
          {state?.resting ? (
            <StatusBadge tone="pending">
              {t("services.pluginBalance.resting")}
            </StatusBadge>
          ) : null}
          {state?.updated_at ? (
            <span className="text-xs text-muted-foreground tabular-nums">
              {t("services.pluginBalance.updated", {
                time: formatMoment(state.updated_at),
              })}
            </span>
          ) : null}
          {readsBalance ? (
            <IconButton
              disabled={busy}
              label={
                busy ? t("common.refreshing") : t("services.pluginBalance.read")
              }
              onClick={() => void read(true)}
              type="button"
            >
              <RefreshCw
                aria-hidden="true"
                className={cn(
                  busy && "animate-spin motion-reduce:animate-none",
                )}
              />
            </IconButton>
          ) : null}
        </div>
        {state?.balance?.windows?.length ? (
          <ul className="grid gap-2">
            {state.balance.windows.map((window, index) => (
              <BalanceWindowRow key={index} window={window} />
            ))}
          </ul>
        ) : null}
        {state?.resting && state.notice ? (
          <FormMessage tone="warning">{state.notice}</FormMessage>
        ) : null}
        {failure ? <FormMessage tone="error">{failure}</FormMessage> : null}
      </div>
    </Field>
  );
}

function BalanceAmount({ balance }: { balance: PluginBalance }) {
  const t = useT();
  const locale = useLocale();
  if (balance.amount !== undefined && balance.currency) {
    let text: string;
    try {
      text = new Intl.NumberFormat(locale, {
        style: "currency",
        currency: balance.currency,
      }).format(balance.amount);
    } catch {
      text = `${balance.amount} ${balance.currency}`;
    }
    return <span className="text-sm font-medium tabular-nums">{text}</span>;
  }
  if (balance.unlimited) {
    return (
      <span className="text-sm font-medium">
        {t("services.pluginBalance.unlimited")}
      </span>
    );
  }
  return null;
}

function BalanceWindowRow({ window }: { window: PluginBalanceWindow }) {
  const t = useT();
  const locale = useLocale();
  const number = new Intl.NumberFormat(locale, { maximumFractionDigits: 2 });
  const share =
    window.limit > 0
      ? Math.min(100, Math.max(0, (window.used / window.limit) * 100))
      : 0;
  return (
    <li className="grid gap-1">
      <div className="flex min-w-0 items-baseline justify-between gap-2 text-xs">
        <span className="min-w-0 truncate">
          {text(window.name, locale) || t("services.pluginBalance.window")}
        </span>
        <span className="shrink-0 text-muted-foreground tabular-nums">
          {number.format(window.used)} / {number.format(window.limit)}
          {window.unit ? ` ${window.unit}` : ""}
          {window.reset_at
            ? ` · ${t("services.pluginBalance.resets", {
                time: formatMoment(window.reset_at * 1000),
              })}`
            : ""}
        </span>
      </div>
      <Progress
        aria-label={text(window.name, locale)}
        tone={
          share >= 100 ? "destructive" : share >= 80 ? "warning" : "default"
        }
        value={share}
      />
    </li>
  );
}

function text(value: PluginText, locale: string): string {
  return localized(value, locale);
}
