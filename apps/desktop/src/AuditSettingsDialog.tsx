import { CapabilityToggle } from "@/components/CapabilityToggle";
import { FormMessage } from "@/components/FormMessage";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { NumberField } from "@/components/NumberField";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import type { AuditSettings } from "./audit-settings-model";
import { useT } from "./i18n";
import type { RawSealingState } from "./raw-sealing-model";
import type { RawPanelAction } from "./RawSealingControls";
import { RawPasswordPanel } from "./RawSealingControls";

const MIB = 1024 * 1024;

export function AuditSettingsDialog({
  draft,
  busy,
  error,
  notice,
  rawSealing,
  rawSealingError,
  onChange,
  onCancel,
  onRawPasswordAction,
  onSave,
}: {
  draft: AuditSettings | null;
  busy: boolean;
  error: string | null;
  notice: string | null;
  rawSealing: RawSealingState | null;
  rawSealingError: string | null;
  onChange: <K extends keyof AuditSettings>(
    key: K,
    value: AuditSettings[K],
  ) => void;
  onCancel: () => void;
  onRawPasswordAction: (action: RawPanelAction) => void;
  onSave: () => void;
}) {
  const t = useT();

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onCancel()}>
      <DialogContent
        className="top-[calc(50%+var(--window-chrome-height)/2)] flex max-h-[calc(100dvh-var(--window-chrome-height)-2rem)] max-w-[calc(100%-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-xl"
        showCloseButton={!busy}
      >
        <DialogHeader className="shrink-0 gap-1.5 border-b px-5 py-3 pr-12 text-left">
          <DialogTitle>{t("records.auditSettings")}</DialogTitle>
          <DialogDescription>
            {t("records.auditSettingsDescription")}
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex min-h-0 flex-col"
          onSubmit={(event) => {
            event.preventDefault();
            if (!busy && draft) onSave();
          }}
        >
          <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 py-3">
            {draft ? (
              <div className="space-y-3">
                <section aria-label={t("records.captureScope")}>
                  <CapabilityToggle
                    size="default"
                    label={t("records.httpMeta")}
                    description={t("records.httpMetaDescription")}
                    checked={draft.http_meta_enabled}
                    disabled={busy}
                    onCheckedChange={(value) =>
                      onChange("http_meta_enabled", value)
                    }
                  />
                </section>
                <RawPasswordPanel
                  agentAccess={draft.agent_raw_access_enabled}
                  busy={busy}
                  error={rawSealingError}
                  onAction={onRawPasswordAction}
                  onAgentAccessChange={(value) =>
                    onChange("agent_raw_access_enabled", value)
                  }
                  status={rawSealing}
                />
                <section
                  className="border-t pt-3"
                  aria-label={t("records.captureLimits")}
                >
                  <h3 className="text-sm font-semibold">
                    {t("records.captureLimits")}
                  </h3>
                  <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                    {t("records.captureLimitsHint")}
                  </p>
                  <div className="mt-2 grid grid-cols-2 gap-4">
                    <NumberField
                      label={t("records.requestLimit")}
                      hint={t("records.limitMaximum", { count: 64 })}
                      unit="MiB"
                      min={1 / 1024}
                      max={64}
                      step="any"
                      value={draft.request_body_max_bytes / MIB}
                      disabled={busy}
                      onChange={(value) =>
                        onChange(
                          "request_body_max_bytes",
                          Math.round(value * MIB),
                        )
                      }
                    />
                    <NumberField
                      label={t("records.responseLimit")}
                      hint={t("records.limitMaximum", { count: 64 })}
                      unit="MiB"
                      min={1 / 1024}
                      max={64}
                      step="any"
                      value={draft.response_content_max_bytes / MIB}
                      disabled={busy}
                      onChange={(value) =>
                        onChange(
                          "response_content_max_bytes",
                          Math.round(value * MIB),
                        )
                      }
                    />
                  </div>
                </section>
                <section
                  className="border-t pt-3"
                  aria-label={t("records.retentionPeriod")}
                >
                  <h3 className="text-sm font-semibold">
                    {t("records.retentionPeriod")}
                  </h3>
                  <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                    {t("records.retentionHint")}
                  </p>
                  <div className="mt-2 grid grid-cols-2 gap-4">
                    <NumberField
                      label={t("records.metaRetention")}
                      unit={t("records.daysUnit")}
                      min={1}
                      max={3650}
                      value={draft.metadata_retention_days}
                      disabled={busy}
                      onChange={(value) =>
                        onChange("metadata_retention_days", value)
                      }
                    />
                    <NumberField
                      label={t("records.contentRetention")}
                      unit={t("records.daysUnit")}
                      min={1}
                      max={365}
                      value={draft.content_retention_days}
                      disabled={busy}
                      onChange={(value) =>
                        onChange("content_retention_days", value)
                      }
                    />
                  </div>
                </section>
                <div className="border-t pt-3">
                  <HelpDisclosure title={t("records.captureStorageHelp")}>
                    <p>{t("records.settingsHint")}</p>
                    <p>{t("records.limitUnitHint")}</p>
                  </HelpDisclosure>
                </div>
              </div>
            ) : busy ? (
              <p className="text-sm text-muted-foreground" role="status">
                {t("common.loading")}
              </p>
            ) : null}
          </div>
          <div className="shrink-0 space-y-3 border-t bg-muted/30 px-5 py-3">
            {error ? <FormMessage tone="error">{error}</FormMessage> : null}
            {notice ? <FormMessage tone="success">{notice}</FormMessage> : null}
            <DialogFooter className="flex-row justify-end">
              <Button
                variant="outline"
                disabled={busy}
                onClick={onCancel}
                type="button"
              >
                {t("common.close")}
              </Button>
              <Button disabled={busy || !draft} type="submit">
                {busy ? t("common.saving") : t("common.save")}
              </Button>
            </DialogFooter>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
