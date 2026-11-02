import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { FormMessage } from "@/components/FormMessage";
import { Button } from "@/components/ui/button";

import { getClaudeCLI, installClaudeCLI, openExternalURL } from "./bridge";
import type { ClaudeCLIStatus } from "./subscription-model";

export const claudeCodeSetupURL = "https://code.claude.com/docs/en/setup";
const installPollMs = 2000;

function errorText(cause: unknown, fallback: string): string {
  return cause instanceof Error && cause.message ? cause.message : fallback;
}

/**
 * Whether the claude CLI that official Claude accounts run is ready. When it
 * is missing or too old it says what to do: follow the official instructions
 * on a desktop, or install it with one click where Core offers that (the
 * server edition).
 */
export function ClaudeCLINotice() {
  const { t } = useTranslation();
  const [status, setStatus] = useState<ClaudeCLIStatus | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    try {
      const next = await getClaudeCLI();
      if (!mounted.current) return;
      setStatus(next);
      setFailure(null);
    } catch (cause) {
      if (!mounted.current) return;
      setFailure(errorText(cause, t("services.claudeCLI.readFailed")));
    }
  }, [t]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    return () => {
      mounted.current = false;
    };
  }, [refresh]);

  useEffect(() => {
    if (!status?.installing) return;
    const timer = window.setTimeout(() => void refresh(), installPollMs);
    return () => window.clearTimeout(timer);
  }, [status, refresh]);

  const install = async () => {
    setStarting(true);
    setFailure(null);
    try {
      const next = await installClaudeCLI();
      if (mounted.current) setStatus(next);
    } catch (cause) {
      if (mounted.current) {
        setFailure(errorText(cause, t("services.claudeCLI.installFailed")));
      }
    } finally {
      if (mounted.current) setStarting(false);
    }
  };

  const openSetup = async () => {
    setFailure(null);
    try {
      await openExternalURL(claudeCodeSetupURL);
    } catch (cause) {
      setFailure(errorText(cause, t("services.claudeCLI.openSetupFailed")));
    }
  };

  if (!status) {
    return failure ? <FormMessage tone="error">{failure}</FormMessage> : null;
  }
  if (status.installing) {
    return (
      <FormMessage tone="notice">
        {t("services.claudeCLI.installing")}
      </FormMessage>
    );
  }
  if (status.supported) {
    return (
      <p className="text-xs text-muted-foreground">
        {t("services.claudeCLI.found", { version: status.version ?? "" })}
      </p>
    );
  }
  const message = !status.installed
    ? status.install_available
      ? t("services.claudeCLI.missingServer")
      : t("services.claudeCLI.missingDesktop")
    : status.install_available
      ? t("services.claudeCLI.outdatedServer", {
          version: status.version ?? "",
          minimum: status.minimum_version,
        })
      : t("services.claudeCLI.outdatedDesktop", {
          version: status.version ?? "",
          minimum: status.minimum_version,
        });
  return (
    <div className="grid gap-2">
      <FormMessage tone="warning">{message}</FormMessage>
      {status.install_error ? (
        <FormMessage tone="error">
          {t("services.claudeCLI.installError", {
            error: status.install_error,
          })}
        </FormMessage>
      ) : null}
      {failure ? <FormMessage tone="error">{failure}</FormMessage> : null}
      <div className="flex flex-wrap gap-2">
        {status.install_available ? (
          <Button
            disabled={starting}
            onClick={() => void install()}
            size="sm"
            type="button"
          >
            {status.installed
              ? t("services.claudeCLI.update")
              : t("services.claudeCLI.install")}
          </Button>
        ) : (
          <Button
            onClick={() => void openSetup()}
            size="sm"
            type="button"
            variant="outline"
          >
            {t("services.claudeCLI.setup")}
          </Button>
        )}
        <Button
          onClick={() => void refresh()}
          size="sm"
          type="button"
          variant="outline"
        >
          {t("services.claudeCLI.recheck")}
        </Button>
      </div>
    </div>
  );
}
