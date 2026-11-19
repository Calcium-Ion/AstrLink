import { useState } from "react";

import { FormMessage } from "@/components/FormMessage";
import { GitHubRepoBadge } from "@/components/GitHubRepoBadge";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { Ellipsis } from "@/components/icons";
import { Panel, PanelHeader } from "@/components/Panel";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { checkPluginUpdate, pluginInstallFailureOf } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import {
  formatDay,
  installFailureText,
  sourceRef,
} from "./PluginInstallReview";
import type {
  PluginDetail,
  PluginInstalledVersion,
  PluginInstallSource,
  PluginUpdateCheck,
} from "./plugin-model";

/**
 * Where a plugin installed from source came from, at the top of its
 * overview: its repository and what it follows, the check for a newer
 * version, and, behind the menu, going back to a version an install
 * replaced or removing the plugin.
 */
export function PluginSourceCard({
  detail,
  source,
  busy,
  onUpdate,
  onRollback,
  onUninstall,
}: {
  detail: PluginDetail;
  source: PluginInstallSource;
  busy: boolean;
  /** Reviews the update the link names. */
  onUpdate: (url: string) => void;
  onRollback: (version: PluginInstalledVersion) => void;
  onUninstall: () => void;
}) {
  const t = useT();
  const [check, setCheck] = useState<PluginUpdateCheck | null>(null);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [choosing, setChoosing] = useState(false);
  const versions = detail.versions ?? [];

  async function runCheck() {
    setChecking(true);
    setError(null);
    try {
      setCheck(await checkPluginUpdate(detail.key));
    } catch (cause) {
      const failure = pluginInstallFailureOf(cause);
      setError(
        failure
          ? installFailureText(failure, t)
          : controlErrorMessage(cause, t("plugins.install.checkFailed")),
      );
    } finally {
      setChecking(false);
    }
  }

  const repository = `https://github.com/${source.repository}`;
  return (
    <Panel data-testid="plugin-source">
      <PanelHeader
        actions={
          <>
            <Button
              disabled={checking || busy}
              onClick={() => void runCheck()}
              size="xs"
              type="button"
              variant="outline"
            >
              {checking
                ? t("plugins.install.checking")
                : t("plugins.install.checkUpdate")}
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  aria-label={t("plugins.install.more")}
                  disabled={busy}
                  size="icon-xs"
                  type="button"
                  variant="ghost"
                >
                  <Ellipsis />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {versions.length > 0 ? (
                  <DropdownMenuItem onSelect={() => setChoosing(true)}>
                    {t("plugins.install.rollback")}
                  </DropdownMenuItem>
                ) : null}
                <DropdownMenuItem onSelect={onUninstall} variant="destructive">
                  {t("plugins.install.uninstall")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
        size="sm"
      >
        <h3 className="text-xs font-semibold">{t("plugins.install.source")}</h3>
      </PanelHeader>
      <div className="grid gap-2 p-3 text-sm">
        {source.kind === "market" ? (
          <div className="grid gap-0.5">
            <span className="min-w-0 break-all">
              {source.repository || t("plugins.source.market")}
              {source.key ? ` / ${source.key}` : ""}
            </span>
            <span className="text-xs text-muted-foreground">
              {t("plugins.install.marketRef", { version: source.ref ?? "" })}
            </span>
          </div>
        ) : (
          <>
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <GitHubRepoBadge
                href={repository}
                label={t("plugins.install.openRepository", {
                  repository: source.repository,
                })}
              />
              <span className="min-w-0 break-all">
                {source.repository}
                {source.subdir ? ` / ${source.subdir}` : ""}
              </span>
            </div>
            <p className="text-xs text-muted-foreground">
              {sourceRef(source, t)}
            </p>
          </>
        )}
        {error ? <FormMessage tone="error">{error}</FormMessage> : null}
        {check ? (
          check.pinned ? (
            <p className="text-xs text-muted-foreground">
              {t("plugins.install.pinned")}
            </p>
          ) : check.available ? (
            <div
              className="grid gap-2 rounded-md border p-2"
              data-testid="plugin-update"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="flex-1 text-xs">
                  {t("plugins.install.available", {
                    version:
                      check.version ??
                      (check.ref_kind === "branch"
                        ? t("plugins.install.newCommit", { ref: check.ref })
                        : (check.ref ?? "")),
                    day: formatDay(check.committed_at),
                  })}
                </span>
                {check.url ? (
                  <Button
                    disabled={busy}
                    onClick={() => onUpdate(check.url ?? "")}
                    size="xs"
                    type="button"
                  >
                    {t("plugins.install.reviewUpdate")}
                  </Button>
                ) : null}
              </div>
              {check.notes ? (
                <HelpDisclosure title={t("plugins.install.notes")}>
                  <p className="whitespace-pre-wrap text-foreground">
                    {check.notes}
                  </p>
                </HelpDisclosure>
              ) : null}
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">
              {t("plugins.install.latest")}
            </p>
          )
        ) : null}
      </div>
      {choosing ? (
        <Dialog open onOpenChange={(open) => !open && setChoosing(false)}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader className="text-left">
              <DialogTitle>
                {t("plugins.install.rollbackTitle", { name: detail.name })}
              </DialogTitle>
            </DialogHeader>
            <ul className="grid gap-1" data-testid="plugin-versions">
              {versions.map((version) => (
                <li
                  className="flex items-center gap-2 border-b py-1.5 last:border-b-0"
                  key={version.id}
                >
                  <span className="grid min-w-0 flex-1 gap-0.5">
                    <span className="text-sm">
                      {version.version || t("plugins.install.unknownVersion")}
                    </span>
                    <span className="text-micro text-muted-foreground">
                      {[
                        version.source ? sourceRef(version.source, t) : "",
                        t("plugins.install.replacedAt", {
                          day: formatDay(version.replaced_at),
                        }),
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                  </span>
                  <Button
                    onClick={() => {
                      setChoosing(false);
                      onRollback(version);
                    }}
                    size="xs"
                    type="button"
                    variant="outline"
                  >
                    {t("plugins.install.rollbackTo")}
                  </Button>
                </li>
              ))}
            </ul>
          </DialogContent>
        </Dialog>
      ) : null}
    </Panel>
  );
}
