import { useCallback, useEffect, useId, useState } from "react";

import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { StatusBadge } from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";

import { openAuthorizationURL, pluginLogin } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import type { PluginCredentialView, PluginLoginView } from "./plugin-model";

/**
 * The account a plugin logs a provider in with, in place of a key: who is
 * logged in, and a button that logs in, or in again. A provider not saved
 * yet logs in once it is.
 */
export function PluginLoginField({
  pluginKey,
  serviceId,
}: {
  pluginKey: string;
  serviceId?: string;
}) {
  const t = useT();
  const [credential, setCredential] = useState<PluginCredentialView | null>(
    null,
  );
  const [open, setOpen] = useState(false);
  const refresh = useCallback(async () => {
    if (!serviceId) return;
    try {
      const answer = await pluginLogin(pluginKey, {
        service_id: serviceId,
        action: "status",
      });
      setCredential(answer.credential);
    } catch {
      setCredential(null);
    }
  }, [pluginKey, serviceId]);
  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <Field
      group
      label={t("services.pluginLogin.account")}
      tooltip={serviceId ? undefined : t("services.pluginLogin.saveFirst")}
    >
      <div
        className="flex min-w-0 flex-wrap items-center gap-2"
        data-testid="plugin-login-field"
      >
        {credential?.logged_in ? (
          <span className="min-w-0 truncate text-sm">
            {credential.label ||
              credential.account_id ||
              t("services.pluginLogin.loggedIn")}
          </span>
        ) : (
          <span className="text-sm text-muted-foreground">
            {t("services.pluginLogin.notLoggedIn")}
          </span>
        )}
        {credential?.needs_login ? (
          <StatusBadge tone="negative">
            {t("services.pluginLogin.needsLogin")}
          </StatusBadge>
        ) : null}
        <Button
          disabled={!serviceId}
          onClick={() => setOpen(true)}
          size="sm"
          type="button"
          variant="outline"
        >
          {credential?.logged_in
            ? t("services.pluginLogin.loginAgain")
            : t("services.pluginLogin.login")}
        </Button>
      </div>
      {open && serviceId ? (
        <PluginLoginDialog
          onClose={() => {
            setOpen(false);
            void refresh();
          }}
          pluginKey={pluginKey}
          serviceId={serviceId}
        />
      ) : null}
    </Field>
  );
}

/**
 * A login in progress: the vendor's page opened in the browser, an
 * address pasted back, a device code, or a form, as the plugin's steps ask.
 */
function PluginLoginDialog({
  onClose,
  pluginKey,
  serviceId,
}: {
  onClose: () => void;
  pluginKey: string;
  serviceId: string;
}) {
  const t = useT();
  const id = useId();
  const [view, setView] = useState<PluginLoginView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pasted, setPasted] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  const call = useCallback(
    async (
      action: "start" | "state" | "submit",
      extra: { pasted?: string; fields?: Record<string, string> } = {},
    ) => {
      try {
        const answer = await pluginLogin(pluginKey, {
          service_id: serviceId,
          action,
          ...extra,
        });
        if (answer.login) setView(answer.login);
        setError(null);
        return answer.login ?? null;
      } catch (cause) {
        setError(controlErrorMessage(cause, t("services.pluginLogin.failed")));
        return null;
      }
    },
    [pluginKey, serviceId, t],
  );

  useEffect(() => {
    let current = true;
    void call("start").then((started) => {
      if (current && started?.step === "redirect" && started.url)
        void openAuthorizationURL(started.url).catch(() => undefined);
    });
    return () => {
      current = false;
    };
  }, [call]);

  // A redirect or device step finishes outside the dialog: it asks how
  // the login goes until it is done.
  const waiting = view?.step === "redirect" || view?.step === "device";
  useEffect(() => {
    if (!waiting) return;
    const every = Math.max(2, view?.interval ?? 2) * 1000;
    const timer = window.setInterval(() => void call("state"), every);
    return () => window.clearInterval(timer);
  }, [call, view?.interval, waiting]);

  useEffect(() => {
    if (view?.step === "done") onClose();
  }, [onClose, view?.step]);

  const cancel = () => {
    void pluginLogin(pluginKey, {
      service_id: serviceId,
      action: "cancel",
    }).catch(() => undefined);
    onClose();
  };

  const submit = async (extra: {
    pasted?: string;
    fields?: Record<string, string>;
  }) => {
    setBusy(true);
    await call("submit", extra);
    setBusy(false);
  };

  return (
    <Dialog open onOpenChange={(next) => !next && cancel()}>
      <DialogContent className="sm:max-w-md" data-testid="plugin-login-dialog">
        <DialogHeader className="text-left">
          <DialogTitle>{t("services.pluginLogin.title")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3 text-sm">
          {view === null && !error ? (
            <p className="text-muted-foreground">
              {t("services.pluginLogin.starting")}
            </p>
          ) : null}
          {view?.step === "redirect" ? (
            <>
              <p>{view.hint || t("services.pluginLogin.inBrowser")}</p>
              <div>
                <Button
                  onClick={() =>
                    view.url && void openAuthorizationURL(view.url)
                  }
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {t("services.pluginLogin.openPage")}
                </Button>
              </div>
            </>
          ) : null}
          {view?.step === "paste" ? (
            <form
              className="grid gap-3"
              onSubmit={(event) => {
                event.preventDefault();
                void submit({ pasted });
              }}
            >
              <p>{view.hint || t("services.pluginLogin.pasteHint")}</p>
              <div>
                <Button
                  onClick={() =>
                    view.url && void openAuthorizationURL(view.url)
                  }
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {t("services.pluginLogin.openPage")}
                </Button>
              </div>
              <Field
                htmlFor={`${id}-pasted`}
                inDialog
                label={t("services.pluginLogin.pasted")}
              >
                <Input
                  className="font-mono text-xs"
                  id={`${id}-pasted`}
                  onChange={(event) => setPasted(event.target.value)}
                  value={pasted}
                />
              </Field>
              <div>
                <Button
                  disabled={busy || !pasted.trim()}
                  size="sm"
                  type="submit"
                >
                  {t("services.pluginLogin.continue")}
                </Button>
              </div>
            </form>
          ) : null}
          {view?.step === "device" ? (
            <>
              <p>{t("services.pluginLogin.deviceHint")}</p>
              <p
                className="rounded-md border bg-muted/30 px-3 py-2 text-center font-mono text-lg tracking-widest"
                data-testid="plugin-login-code"
              >
                {view.user_code}
              </p>
              <div>
                <Button
                  onClick={() => {
                    const url =
                      view.verification_uri_complete ?? view.verification_uri;
                    if (url) void openAuthorizationURL(url);
                  }}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {t("services.pluginLogin.openPage")}
                </Button>
              </div>
            </>
          ) : null}
          {view?.step === "fields" ? (
            <form
              className="grid gap-3"
              onSubmit={(event) => {
                event.preventDefault();
                void submit({ fields: values });
              }}
            >
              {(view.fields ?? []).map((field) => (
                <Field
                  htmlFor={`${id}-${field.name}`}
                  inDialog
                  key={field.name}
                  label={field.label}
                  tooltip={field.description || undefined}
                >
                  {field.type === "textarea" ? (
                    <Textarea
                      id={`${id}-${field.name}`}
                      onChange={(event) =>
                        setValues((current) => ({
                          ...current,
                          [field.name]: event.target.value,
                        }))
                      }
                      placeholder={field.placeholder}
                      value={values[field.name] ?? ""}
                    />
                  ) : (
                    <Input
                      autoComplete="off"
                      id={`${id}-${field.name}`}
                      onChange={(event) =>
                        setValues((current) => ({
                          ...current,
                          [field.name]: event.target.value,
                        }))
                      }
                      placeholder={field.placeholder}
                      type={field.type === "password" ? "password" : "text"}
                      value={values[field.name] ?? ""}
                    />
                  )}
                </Field>
              ))}
              <div>
                <Button disabled={busy} size="sm" type="submit">
                  {t("services.pluginLogin.continue")}
                </Button>
              </div>
            </form>
          ) : null}
          {view?.step === "failed" || view?.error || error ? (
            <FormMessage tone="error">
              {error ?? view?.error ?? t("services.pluginLogin.failed")}
            </FormMessage>
          ) : null}
        </div>
        <DialogFooter>
          {view?.step === "failed" ? (
            <Button
              onClick={() => void call("start")}
              size="sm"
              type="button"
              variant="outline"
            >
              {t("services.pluginLogin.retry")}
            </Button>
          ) : null}
          <Button onClick={cancel} size="sm" type="button" variant="outline">
            {t("common.cancel")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
