import { useState, useCallback } from "react";
import { deleteRecoveryPath } from "./bridge";
import { useRecoveryPaths } from "./use-recovery-paths";
import { RecoveryPathEditor } from "./RecoveryPathEditor";
import { pathNodes, type RecoveryPathRecord } from "./recovery-path-model";
import type { RoutableService } from "./service-model";
import { useT } from "./i18n";
import { Button } from "./components/ui/button";
import { Panel } from "./components/Panel";
import { FormMessage } from "./components/FormMessage";
import { ConfirmDialog } from "./components/ConfirmDialog";
export function RecoveryPathManager({
  services,
  ready,
  onDirtyChange,
}: {
  services: RoutableService[];
  ready: boolean;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT(),
    catalog = useRecoveryPaths(ready);
  const [selected, setSelected] = useState<RecoveryPathRecord>(),
    [key, setKey] = useState(0),
    [dirty, setDirty] = useState(false),
    [pending, setPending] = useState<{ record?: RecoveryPathRecord } | null>(
      null,
    ),
    [deleting, setDeleting] = useState(false),
    [error, setError] = useState("");
  const reportDirty = useCallback(
    (value: boolean) => {
      setDirty(value);
      onDirtyChange(value);
    },
    [onDirtyChange],
  );
  const choose = (record?: RecoveryPathRecord) => {
    setSelected(record);
    setKey((value) => value + 1);
    setDirty(false);
    onDirtyChange(false);
  };
  const askChoose = (record?: RecoveryPathRecord) =>
    dirty ? setPending({ record }) : choose(record);
  return (
    <div
      className="grid min-h-0 flex-1 grid-cols-[220px_minmax(0,1fr)] gap-3 overflow-hidden max-[720px]:grid-cols-[150px_minmax(0,1fr)]"
      data-testid="recovery-path-manager"
    >
      <Panel className="flex min-h-0 flex-col">
        <div className="border-b p-3">
          <Button type="button" className="w-full" onClick={() => askChoose()}>
            {t("paths.new")}
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-2">
          {catalog.error ? (
            <FormMessage tone="error">
              {catalog.error}
              <Button type="button" variant="ghost" onClick={catalog.reload}>
                {t("common.retry")}
              </Button>
            </FormMessage>
          ) : null}
          {catalog.records.map((record) => (
            <Button
              key={record.path.id}
              type="button"
              variant={
                selected?.path.id === record.path.id ? "secondary" : "ghost"
              }
              className="mb-1 h-auto w-full justify-start whitespace-normal py-3 text-left"
              onClick={() => askChoose(record)}
            >
              <span className="grid gap-1">
                <strong>{record.path.name}</strong>
                <small className="text-xs font-normal text-muted-foreground">
                  {pathNodes(record.path).length} ·{" "}
                  {t(`paths.${record.path.mode}`)}
                </small>
              </span>
            </Button>
          ))}
        </div>
        {selected ? (
          <div className="border-t p-2">
            <Button
              type="button"
              variant="ghost"
              disabled={selected.references.length > 0}
              onClick={() => setDeleting(true)}
            >
              {t("paths.delete")}
            </Button>
            {selected.references.length > 0 ? (
              <p className="text-xs text-muted-foreground">
                {t("paths.inUse")}
              </p>
            ) : null}
          </div>
        ) : null}
      </Panel>
      <Panel className="flex min-h-0 flex-col">
        {error ? <FormMessage tone="error">{error}</FormMessage> : null}
        <RecoveryPathEditor
          key={key}
          record={selected}
          services={services}
          ready={ready}
          onDirtyChange={reportDirty}
          onSaved={(record) => {
            setSelected(record);
            catalog.reload();
          }}
        />
      </Panel>
      <ConfirmDialog
        open={pending !== null}
        title={t("paths.discard")}
        description={t("paths.discardHint")}
        confirmLabel={t("common.confirm")}
        onCancel={() => setPending(null)}
        onConfirm={() => {
          choose(pending?.record);
          setPending(null);
        }}
      />
      <ConfirmDialog
        open={deleting}
        title={t("paths.delete")}
        description={selected?.path.name ?? ""}
        confirmLabel={t("paths.delete")}
        onCancel={() => setDeleting(false)}
        onConfirm={() => {
          setDeleting(false);
          if (selected)
            void deleteRecoveryPath(selected.path.id, selected.etag)
              .then(() => {
                choose();
                catalog.reload();
              })
              .catch((error) => setError(String(error)));
        }}
      />
    </div>
  );
}
