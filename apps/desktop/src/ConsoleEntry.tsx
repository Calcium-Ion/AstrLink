import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "./components/ui/dialog";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Field } from "./components/Field";
import { Panel } from "./components/Panel";
import { FormMessage } from "./components/FormMessage";
import { applyLocale, i18n } from "./i18n";
import {
  CONSOLE_SIGNED_OUT,
  ConsoleError,
  consoleRequest,
} from "./web-transport";
import { useWebText } from "./web-copy";

export interface ConsoleStatus {
  status: "setup_required" | "setup_expired" | "login_required";
  signed_in: boolean;
  setup_seconds_left: number;
  password_reset: boolean;
  reset_variable: string;
  password_min_length: number;
  password_max_length: number;
}
export function ConsoleLanguage() {
  const t = useWebText();
  return (
    <Button
      size="sm"
      variant="ghost"
      aria-label={t("language")}
      onClick={() => {
        const locale = i18n.language.startsWith("zh") ? "en" : "zh-CN";
        try {
          localStorage.setItem("astrlink:console-locale", locale);
        } catch {
          /* Browsing with storage disabled still supports changing language. */
        }
        void applyLocale(locale);
      }}
    >
      {i18n.language.startsWith("zh") ? "English" : "中文"}
    </Button>
  );
}
export function ConsoleEntry({ children }: { children: ReactNode }) {
  const t = useWebText();
  const [resetNoticeDismissed, setResetNoticeDismissed] = useState(false);
  const [status, setStatus] = useState<ConsoleStatus | null>(null);
  const [error, setError] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [setupUntil, setSetupUntil] = useState(0);
  const [retryUntil, setRetryUntil] = useState(0);
  const [now, setNow] = useState(Date.now());
  const refresh = useCallback(async () => {
    try {
      const { value } = await consoleRequest("/console/v1/status");
      setStatus(value);
      setSetupUntil(Date.now() + value.setup_seconds_left * 1000);
      setNow(Date.now());
    } catch {
      setError(t("failed"));
    }
  }, [t]);
  useEffect(() => {
    void refresh();
    const signedOut = () => {
      setStatus(null);
      setPassword("");
      setConfirmation("");
      void refresh();
    };
    window.addEventListener(CONSOLE_SIGNED_OUT, signedOut);
    return () => window.removeEventListener(CONSOLE_SIGNED_OUT, signedOut);
  }, [refresh]);
  useEffect(() => {
    const timer = window.setInterval(() => {
      setNow(Date.now());
    }, 1000);
    return () => clearInterval(timer);
  }, []);
  const remaining = Math.max(0, Math.ceil((setupUntil - now) / 1000));
  useEffect(() => {
    if (status?.status === "setup_required" && remaining === 0) void refresh();
  }, [remaining, status?.status, refresh]);
  if (status?.signed_in)
    return (
      <>
        {children}
        <Dialog
          open={status.password_reset && !resetNoticeDismissed}
          onOpenChange={(open) => {
            if (!open) setResetNoticeDismissed(true);
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t("resetTitle")}</DialogTitle>
              <DialogDescription>
                {t("reset", { variable: status.reset_variable })}
              </DialogDescription>
            </DialogHeader>
            <Button onClick={() => setResetNoticeDismissed(true)}>
              {t("close")}
            </Button>
          </DialogContent>
        </Dialog>
      </>
    );
  const setup = status?.status === "setup_required";
  const expired = status?.status === "setup_expired";
  const retry = Math.max(0, Math.ceil((retryUntil - now) / 1000));
  const length = [...password].length;
  return (
    <main className="flex min-h-dvh items-center justify-center p-4">
      <Panel className="w-full max-w-md p-6">
        <div className="mb-4 flex items-center justify-between gap-2">
          <h1 className="text-lg font-semibold">{t("title")}</h1>
          <ConsoleLanguage />
        </div>
        {status?.password_reset && (
          <FormMessage tone="warning">
            {t("reset", { variable: status.reset_variable })}
          </FormMessage>
        )}
        {!status ? (
          <>
            <p>{t("loading")}</p>
            <Button
              onClick={() => {
                setError("");
                void refresh();
              }}
            >
              {t("retryAction")}
            </Button>
          </>
        ) : expired ? (
          <p>{t("expired")}</p>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={async (event) => {
              event.preventDefault();
              if (busy || retry) return;
              if (setup && password !== confirmation) {
                setError(t("mismatch"));
                return;
              }
              setBusy(true);
              setError("");
              const secret = password;
              setPassword("");
              setConfirmation("");
              try {
                await consoleRequest(
                  `/console/v1/${setup ? "setup" : "login"}`,
                  "POST",
                  { password: secret },
                );
                await refresh();
              } catch (cause) {
                if (cause instanceof ConsoleError && cause.status === 429) {
                  setNow(Date.now());
                  setRetryUntil(Date.now() + cause.retryAfter * 1000);
                } else if (
                  cause instanceof ConsoleError &&
                  cause.code === "invalid_password"
                )
                  setError(t("invalid_password"));
                else if (
                  cause instanceof ConsoleError &&
                  cause.code === "setup_expired"
                )
                  setError(t("expired"));
                else if (
                  cause instanceof ConsoleError &&
                  cause.code === "already_configured"
                )
                  setError(t("alreadyConfigured"));
                else setError(t("failed"));
                await refresh();
              } finally {
                setBusy(false);
              }
            }}
          >
            {setup && (
              <>
                <p className="text-sm text-muted-foreground">
                  {t("protection")}
                </p>
                <p role="timer" className="text-sm">
                  {t("countdown")}: {Math.floor(remaining / 60)}:
                  {String(remaining % 60).padStart(2, "0")}
                </p>
              </>
            )}
            <Field label={t("password")}>
              <Input
                type="password"
                name="password"
                autoComplete={setup ? "new-password" : "current-password"}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </Field>
            {setup && (
              <>
                <p className="text-xs text-muted-foreground">
                  {t("bounds", {
                    min: status.password_min_length,
                    max: status.password_max_length,
                  })}
                </p>
                <Field label={t("confirmation")}>
                  <Input
                    type="password"
                    name="confirmation"
                    autoComplete="new-password"
                    value={confirmation}
                    onChange={(e) => setConfirmation(e.target.value)}
                    required
                  />
                </Field>
              </>
            )}
            <Button
              type="submit"
              disabled={
                busy ||
                retry > 0 ||
                length === 0 ||
                (setup &&
                  (length < status.password_min_length ||
                    length > status.password_max_length ||
                    password !== confirmation))
              }
            >
              {t(setup ? "setup" : "login")}
            </Button>
            {!setup && (
              <p className="text-xs leading-relaxed text-muted-foreground">
                {t("forgot")}
              </p>
            )}
          </form>
        )}
        {retry > 0 && (
          <FormMessage tone="warning">
            {t("retry", { seconds: retry })}
          </FormMessage>
        )}
        {error && <FormMessage tone="error">{error}</FormMessage>}
      </Panel>
    </main>
  );
}
export function ConsoleLogout() {
  const t = useWebText();
  const [error, setError] = useState("");
  return (
    <>
      <Button
        className="w-full"
        size="sm"
        variant="ghost"
        onClick={async () => {
          try {
            await consoleRequest("/console/v1/logout", "POST");
            window.dispatchEvent(new Event(CONSOLE_SIGNED_OUT));
          } catch {
            setError(t("failed"));
          }
        }}
      >
        {t("logout")}
      </Button>
      {error && (
        <p role="alert" className="text-xs">
          {error}
        </p>
      )}
    </>
  );
}
