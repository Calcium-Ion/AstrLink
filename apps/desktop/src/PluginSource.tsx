import { useState } from "react";

import { Ellipsis } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

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
 * What the header of a plugin's page offers besides its switch: the update
 * a check found, and behind ⋯ the connection test, its interface, the check
 * for a newer version (for a local folder, installing from it again), going
 * back to a version an install replaced, and removing it.
 */
export function PluginMenu({
  detail,
  busy,
  testing,
  onTest,
  onUiEnabled,
  onUpdate,
  onRollback,
  onUninstall,
}: {
  detail: PluginDetail;
  busy: boolean;
  testing: boolean;
  onTest: () => void;
  onUiEnabled: (on: boolean) => void;
  /** Reviews the update the link or the local folder names. */
  onUpdate: (url: string) => void;
  onRollback: (version: PluginInstalledVersion) => void;
  onUninstall: () => void;
}) {
  const t = useT();
  const source = detail.installed;
  const [check, setCheck] = useState<PluginUpdateCheck | null>(null);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [choosing, setChoosing] = useState(false);
  const versions = detail.versions ?? [];
  const loaded = detail.state === "loaded";
  const testable = loaded && detail.hooks.includes("testConnection");
  // A local folder has no newer version to look for: installing from it
  // again builds what it holds now.
  const local = source?.kind === "local";

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

  const found = check && !check.pinned && check.available ? check : null;
  const checked = check
    ? check.pinned
      ? t("plugins.install.pinned")
      : check.available
        ? ""
        : t("plugins.install.latest")
    : "";
  const hasMenu = testable || (loaded && detail.client) || source !== undefined;
  return (
    <div className="flex items-center gap-2" data-testid="plugin-source">
      {error || checked ? (
        <span
          className={cn(
            "hidden max-w-64 truncate text-xs @[720px]:inline",
            error ? "text-danger-foreground" : "text-muted-foreground",
          )}
          title={error ?? checked}
        >
          {error ?? checked}
        </span>
      ) : null}
      {found?.url ? (
        <Button
          data-testid="plugin-update"
          disabled={busy}
          onClick={() => onUpdate(found.url ?? "")}
          size="sm"
          title={found.notes || undefined}
          type="button"
          variant="outline"
        >
          {t("plugins.install.updateTo", {
            version:
              found.version ??
              (found.ref_kind === "branch"
                ? t("plugins.install.newCommit", { ref: found.ref })
                : (found.ref ?? "")),
          })}
        </Button>
      ) : null}
      {hasMenu ? (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              aria-label={t("plugins.install.more")}
              disabled={busy}
              size="icon-sm"
              type="button"
              variant="ghost"
            >
              <Ellipsis />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-44">
            {testable ? (
              <DropdownMenuItem disabled={testing} onSelect={onTest}>
                {testing ? t("plugins.testing") : t("plugins.test")}
              </DropdownMenuItem>
            ) : null}
            {loaded && detail.client ? (
              <DropdownMenuCheckboxItem
                checked={detail.ui_enabled}
                onCheckedChange={(on) => onUiEnabled(on === true)}
              >
                {t("plugins.ui.pluginSwitch")}
              </DropdownMenuCheckboxItem>
            ) : null}
            {source ? (
              <>
                {testable || (loaded && detail.client) ? (
                  <DropdownMenuSeparator />
                ) : null}
                {local ? (
                  <DropdownMenuItem onSelect={() => onUpdate(source.url)}>
                    {t("plugins.install.reinstallFolder")}
                  </DropdownMenuItem>
                ) : (
                  <DropdownMenuItem
                    disabled={checking}
                    onSelect={() => void runCheck()}
                  >
                    {checking
                      ? t("plugins.install.checking")
                      : t("plugins.install.checkUpdate")}
                  </DropdownMenuItem>
                )}
                {versions.length > 0 ? (
                  <DropdownMenuItem onSelect={() => setChoosing(true)}>
                    {t("plugins.install.rollback")}
                  </DropdownMenuItem>
                ) : null}
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={onUninstall} variant="destructive">
                  {t("plugins.install.uninstall")}
                </DropdownMenuItem>
              </>
            ) : null}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
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
    </div>
  );
}

/** Where a plugin installed from source comes from, in one short line. */
export function sourceLine(
  source: PluginInstallSource,
  t: ReturnType<typeof useT>,
): string {
  if (source.kind === "market")
    return source.repository || t("plugins.source.market");
  if (source.kind === "local")
    return `${t("plugins.source.local")} ${source.repository}`;
  return [
    `GitHub ${source.repository}${source.subdir ? `/${source.subdir}` : ""}`,
    sourceRef(source, t),
  ]
    .filter(Boolean)
    .join(" · ");
}
