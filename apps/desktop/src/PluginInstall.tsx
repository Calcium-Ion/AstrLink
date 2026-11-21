import { useEffect, useRef, useState } from "react";

import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { HelpPopover } from "@/components/HelpPopover";
import { LoadingState } from "@/components/LoadingState";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input, verbatimTextInput } from "@/components/ui/input";

import {
  installPlugin,
  listenPluginApprovalFinished,
  openPluginApproval,
  pickPluginFolder,
  pluginApprovalOf,
  pluginInstallFailureOf,
  previewPluginInstall,
} from "./bridge";
import { controlErrorCode, controlErrorMessage } from "./control-error";
import { isWebEdition } from "./edition";
import { useT } from "./i18n";
import {
  installFailureText,
  PluginInstallReviewView,
} from "./PluginInstallReview";
import type {
  PluginHostSettings,
  PluginHostSettingsUpdate,
  PluginInstallFailure,
  PluginInstallReview,
} from "./plugin-model";

/** Where an install from a GitHub link or a local folder is. */
type Phase =
  | { kind: "idle" }
  | { kind: "previewing" }
  | { kind: "failed"; message: string; failure: PluginInstallFailure | null }
  | { kind: "review"; review: PluginInstallReview }
  | { kind: "installing"; review: PluginInstallReview }
  /** The desktop's confirmation window has it. */
  | { kind: "waiting"; review: PluginInstallReview }
  /** The web console's own dialog asks once more. */
  | { kind: "confirming"; review: PluginInstallReview };

/**
 * Installs a plugin from a GitHub link or, on the desktop, a folder on this
 * computer: Core fetches the commit the link names, or reads the folder, and
 * the packages its lock file pins, builds it without running any script, and
 * checks it; the review shows what it is before anything is installed, and
 * the install itself waits for the confirmation window. `link` opens the
 * dialog on a link or a folder already, as an update does.
 */
export function PluginInstallDialog({
  link,
  host,
  onHostChange,
  onClose,
  onInstalled,
}: {
  link?: string;
  host: PluginHostSettings | null;
  onHostChange: (update: PluginHostSettingsUpdate) => void;
  onClose: () => void;
  onInstalled: (key: string) => void;
}) {
  const t = useT();
  const [url, setUrl] = useState(link ?? "");
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  // A marketplace link (…/index.json#key@version) comes from the market page
  // and is reviewed as it is; a GitHub link or a folder can be edited.
  const fromMarket = Boolean(
    link && /^https?:\/\//i.test(link) && !/github\.com/i.test(link),
  );
  const [registry, setRegistry] = useState(host?.npm_registry ?? "");
  // The newest preview wins; an older one's answer is dropped.
  const previewRun = useRef(0);

  async function preview(target: string) {
    const run = ++previewRun.current;
    setPhase({ kind: "previewing" });
    try {
      const review = await previewPluginInstall(target.trim());
      if (run === previewRun.current) setPhase({ kind: "review", review });
    } catch (error) {
      if (run !== previewRun.current) return;
      const failure = pluginInstallFailureOf(error);
      setPhase({
        kind: "failed",
        failure,
        message: failure
          ? installFailureText(failure, t)
          : controlErrorMessage(error, t("plugins.install.failed")),
      });
    }
  }

  // A development build runs effects twice; the link is reviewed once.
  const reviewed = useRef(false);
  useEffect(() => {
    if (!link || reviewed.current) return;
    reviewed.current = true;
    void preview(link);
    // The link opens the dialog once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function install(review: PluginInstallReview, approved = false) {
    setPhase({ kind: "installing", review });
    try {
      const installed = await installPlugin(review.token, approved);
      onInstalled(installed.key);
    } catch (error) {
      if (controlErrorCode(error) === "approval_pending") {
        setPhase({ kind: "waiting", review });
        return;
      }
      if (pluginApprovalOf(error)) {
        setPhase({ kind: "confirming", review });
        return;
      }
      const failure = pluginInstallFailureOf(error);
      setPhase({
        kind: "failed",
        failure,
        message: failure
          ? installFailureText(failure, t)
          : controlErrorMessage(error, t("plugins.install.failed")),
      });
    }
  }

  // The confirmation window decides the install.
  const waitingKey = phase.kind === "waiting" ? phase.review.key : null;
  useEffect(() => {
    if (!waitingKey) return;
    let stop: (() => void) | undefined;
    let stopped = false;
    void listenPluginApprovalFinished((finished) => {
      if (finished.key !== waitingKey) return;
      if (finished.approved && !finished.error) {
        onInstalled(finished.key);
        return;
      }
      setPhase((current) => {
        if (current.kind !== "waiting") return current;
        if (!finished.error) return { kind: "review", review: current.review };
        const failure = pluginInstallFailureOf(finished.error);
        return {
          kind: "failed",
          failure,
          message: failure ? installFailureText(failure, t) : finished.error,
        };
      });
    }).then((unlisten) => {
      if (stopped) unlisten();
      else stop = unlisten;
    });
    return () => {
      stopped = true;
      stop?.();
    };
  }, [waitingKey, onInstalled, t]);

  const review =
    phase.kind === "review" ||
    phase.kind === "installing" ||
    phase.kind === "waiting" ||
    phase.kind === "confirming"
      ? phase.review
      : null;
  const busy = phase.kind === "previewing" || phase.kind === "installing";
  const [picking, setPicking] = useState(false);
  async function pickFolder() {
    setPicking(true);
    try {
      const start = url.trim();
      const folder = await pickPluginFolder(
        start && !/^https?:\/\//i.test(start) ? start : null,
      );
      if (folder) {
        setUrl(folder);
        void preview(folder);
      }
    } catch (error) {
      setPhase({
        kind: "failed",
        failure: null,
        message: controlErrorMessage(error, t("plugins.install.failed")),
      });
    } finally {
      setPicking(false);
    }
  }
  const saveRegistry = () => {
    const next = registry.trim().replace(/\/+$/, "");
    if (next !== (host?.npm_registry ?? ""))
      onHostChange({ npm_registry: next });
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="flex flex-col overflow-hidden sm:max-w-xl"
        data-testid="plugin-install-dialog"
      >
        <DialogHeader className="text-left">
          <DialogTitle className="flex items-center gap-1">
            {fromMarket
              ? t("plugins.install.marketTitle")
              : t("plugins.install.title")}
            {fromMarket ? null : (
              <HelpPopover
                inDialog
                label={t("plugins.install.helpLabel")}
                size="wide"
              >
                <div className="grid gap-2">
                  <p>{t("plugins.install.helpLinks")}</p>
                  {isWebEdition ? null : (
                    <p>{t("plugins.install.helpFolder")}</p>
                  )}
                  <p>{t("plugins.install.helpBuild")}</p>
                  <p>{t("plugins.install.helpConfirm")}</p>
                </div>
              </HelpPopover>
            )}
          </DialogTitle>
        </DialogHeader>
        {fromMarket ? null : (
          <form
            className="flex items-end gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (url.trim() && !busy) void preview(url);
            }}
          >
            <Field
              className="min-w-0 flex-1"
              htmlFor="plugin-install-url"
              label={
                isWebEdition
                  ? t("plugins.install.link")
                  : t("plugins.install.linkOrFolder")
              }
            >
              <Input
                {...verbatimTextInput}
                autoFocus={!link}
                disabled={busy || phase.kind === "waiting"}
                id="plugin-install-url"
                onChange={(event) => setUrl(event.target.value)}
                placeholder="https://github.com/owner/repo"
                value={url}
              />
            </Field>
            <Button
              disabled={!url.trim() || busy || phase.kind === "waiting"}
              size="sm"
              type="submit"
              variant="outline"
            >
              {t("plugins.install.check")}
            </Button>
            {isWebEdition ? null : (
              <Button
                disabled={busy || picking || phase.kind === "waiting"}
                onClick={() => void pickFolder()}
                size="sm"
                type="button"
                variant="outline"
              >
                {t("plugins.install.chooseFolder")}
              </Button>
            )}
          </form>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
          {phase.kind === "previewing" ? (
            <LoadingState
              className="w-full py-6"
              label={t("plugins.install.previewing")}
            />
          ) : phase.kind === "failed" ? (
            <FormMessage asChild tone="error">
              <div className="grid gap-1.5">
                <span>{phase.message}</span>
                {phase.failure?.messages?.length ? (
                  <pre className="max-h-48 overflow-auto font-mono text-micro whitespace-pre-wrap">
                    {phase.failure.messages.join("\n")}
                  </pre>
                ) : null}
              </div>
            </FormMessage>
          ) : review ? (
            <PluginInstallReviewView review={review} />
          ) : null}
        </div>
        {phase.kind === "waiting" ? (
          <FormMessage
            className="flex flex-wrap items-center gap-2"
            data-testid="plugin-install-waiting"
            tone="notice"
          >
            <span className="flex-1">{t("plugins.install.waiting")}</span>
            <Button
              onClick={() => void openPluginApproval()}
              size="xs"
              type="button"
              variant="outline"
            >
              {t("plugins.approval.open")}
            </Button>
          </FormMessage>
        ) : null}
        {phase.kind === "confirming" ? (
          <FormMessage
            className="flex flex-wrap items-center gap-2"
            data-testid="plugin-install-confirming"
            tone="warning"
          >
            <span className="flex-1">
              {t("plugins.install.confirmWeb", { name: phase.review.name })}
            </span>
            <Button
              onClick={() => void install(phase.review, true)}
              size="xs"
              type="button"
            >
              {t("plugins.install.confirm")}
            </Button>
          </FormMessage>
        ) : null}
        {fromMarket ? null : (
          <HelpDisclosure title={t("plugins.install.registry")}>
            <Field
              htmlFor="plugin-install-registry"
              inDialog
              label={t("plugins.install.registryLabel")}
              tooltip={t("plugins.install.registryHelp")}
            >
              <Input
                {...verbatimTextInput}
                id="plugin-install-registry"
                onBlur={saveRegistry}
                onChange={(event) => setRegistry(event.target.value)}
                placeholder="https://registry.npmjs.org"
                value={registry}
              />
            </Field>
          </HelpDisclosure>
        )}
        <DialogFooter>
          <Button onClick={onClose} size="sm" type="button" variant="outline">
            {t("common.cancel")}
          </Button>
          <Button
            disabled={phase.kind !== "review"}
            onClick={() => review && void install(review)}
            size="sm"
            type="button"
          >
            {review?.replaces?.origin === "source" &&
            review.replaces.repository === review.source.repository
              ? t("plugins.install.update")
              : t("plugins.install.install")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
