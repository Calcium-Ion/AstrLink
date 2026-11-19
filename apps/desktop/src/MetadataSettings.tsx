import { type FormEvent, useCallback, useEffect, useState } from "react";

import { Plus, SquarePen, Shredder as Trash2 } from "@/components/icons";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/EmptyState";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { LoadingState } from "@/components/LoadingState";
import { Panel, PanelHeader } from "@/components/Panel";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  createMetadataAttribute,
  deleteMetadataAttribute,
  listMetadataAttributes,
  updateMetadataAttribute,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import type { MetadataAttribute, MetadataAttributeInput } from "./plugin-model";

const emptyInput: MetadataAttributeInput = {
  key: "",
  label: "",
  description: "",
  pattern: "",
  marker: "",
};

/** The first problem with an attribute, worded for the form; null when it is fine. */
export function metadataInputProblem(
  input: MetadataAttributeInput,
  t: ReturnType<typeof useT>,
): string | null {
  if (!/^[a-z][a-z0-9_]{0,31}$/.test(input.key))
    return t("metadata.problems.key");
  const label = input.label.trim();
  if (label === "" || [...label].length > 32)
    return t("metadata.problems.label");
  if (!input.pattern.startsWith("^") || !input.pattern.endsWith("$"))
    return t("metadata.problems.anchored");
  try {
    new RegExp(input.pattern);
  } catch {
    return t("metadata.problems.pattern");
  }
  if (
    input.marker !== "" &&
    (!/^[a-z][a-z0-9-]{0,31}$/.test(input.marker) ||
      input.marker.includes("astrlink"))
  )
    return t("metadata.problems.marker");
  return null;
}

function ownerLabel(
  attribute: MetadataAttribute,
  t: ReturnType<typeof useT>,
): string {
  if (attribute.owner === "user") return t("metadata.ownerUser");
  if (attribute.owner_missing)
    return t("metadata.ownerMissing", { plugin: attribute.owner });
  return t("metadata.ownerPlugin", { plugin: attribute.owner });
}

/**
 * Request attributes: the user's own, editable, and the ones plugins
 * registered, which only take values.
 */
export function MetadataSettings() {
  const t = useT();
  const [attributes, setAttributes] = useState<MetadataAttribute[] | null>(
    null,
  );
  const [loadError, setLoadError] = useState<string | null>(null);
  const [editing, setEditing] = useState<{
    original: MetadataAttribute | null;
    input: MetadataAttributeInput;
  } | null>(null);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<MetadataAttribute | null>(
    null,
  );
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setAttributes(await listMetadataAttributes());
      setLoadError(null);
    } catch (error) {
      setLoadError(controlErrorMessage(error, t("metadata.loadFailed")));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  async function save(event: FormEvent) {
    event.preventDefault();
    if (!editing) return;
    const input = { ...editing.input, label: editing.input.label.trim() };
    const problem = metadataInputProblem(input, t);
    if (problem) {
      setFormError(problem);
      return;
    }
    setSaving(true);
    try {
      if (editing.original) {
        await updateMetadataAttribute(editing.original.key, input);
      } else {
        await createMetadataAttribute(input);
      }
      setEditing(null);
      setFormError(null);
      await load();
    } catch (error) {
      setFormError(controlErrorMessage(error, t("metadata.saveFailed")));
    } finally {
      setSaving(false);
    }
  }

  async function confirmDelete() {
    const target = pendingDelete;
    setPendingDelete(null);
    if (!target) return;
    try {
      await deleteMetadataAttribute(target.key);
      setDeleteError(null);
      await load();
    } catch (error) {
      setDeleteError(controlErrorMessage(error, t("metadata.deleteFailed")));
    }
  }

  const edit = (original: MetadataAttribute | null) => {
    setFormError(null);
    setEditing({
      original,
      input: original
        ? {
            key: original.key,
            label: original.label,
            description: original.description,
            pattern: original.pattern,
            marker: original.marker ?? "",
          }
        : emptyInput,
    });
  };
  const change = (field: keyof MetadataAttributeInput, value: string) =>
    setEditing((current) =>
      current
        ? { ...current, input: { ...current.input, [field]: value } }
        : current,
    );

  return (
    <Panel className="min-w-0" data-testid="metadata-settings">
      <PanelHeader
        actions={
          <>
            <HelpPopover label={t("metadata.helpLabel")}>
              <p>{t("metadata.help.what")}</p>
              <p>{t("metadata.help.order")}</p>
              <p>{t("metadata.help.none")}</p>
              <p>{t("metadata.help.headers")}</p>
            </HelpPopover>
            <Button onClick={() => edit(null)} size="sm" type="button">
              <Plus />
              {t("metadata.add")}
            </Button>
          </>
        }
        size="sm"
      >
        <h2 className="text-sm font-semibold">{t("metadata.title")}</h2>
      </PanelHeader>
      {deleteError ? (
        <FormMessage className="m-3" tone="error">
          {deleteError}
        </FormMessage>
      ) : null}
      {loadError ? (
        <FormMessage className="m-3" tone="error">
          {loadError}
        </FormMessage>
      ) : attributes === null ? (
        <LoadingState className="p-6" label={t("metadata.loading")} />
      ) : attributes.length === 0 ? (
        <EmptyState
          className="py-8"
          description={t("metadata.emptyHint")}
          title={t("metadata.empty")}
        />
      ) : (
        <div className="overflow-x-auto">
          <Table className="text-xs">
            <TableHeader>
              <TableRow>
                <TableHead>{t("metadata.columns.label")}</TableHead>
                <TableHead>{t("metadata.columns.key")}</TableHead>
                <TableHead>{t("metadata.columns.pattern")}</TableHead>
                <TableHead>{t("metadata.columns.marker")}</TableHead>
                <TableHead>{t("metadata.columns.owner")}</TableHead>
                <TableHead className="w-0" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {attributes.map((attribute) => {
                const own = attribute.owner === "user";
                return (
                  <TableRow
                    data-testid="metadata-attribute"
                    key={attribute.key}
                  >
                    <TableCell
                      className="font-medium"
                      title={attribute.description}
                    >
                      {attribute.label}
                    </TableCell>
                    <TableCell className="font-mono" title={attribute.header}>
                      {attribute.key}
                    </TableCell>
                    <TableCell
                      className="max-w-[14rem] truncate font-mono"
                      title={attribute.pattern}
                    >
                      {attribute.pattern}
                    </TableCell>
                    <TableCell className="font-mono">
                      {attribute.marker ?? "—"}
                    </TableCell>
                    <TableCell>
                      {own ? (
                        ownerLabel(attribute, t)
                      ) : (
                        <StatusBadge
                          tone={attribute.owner_missing ? "pending" : "neutral"}
                        >
                          {ownerLabel(attribute, t)}
                        </StatusBadge>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-1">
                        {own ? (
                          <IconButton
                            label={t("metadata.edit", {
                              label: attribute.label,
                            })}
                            onClick={() => edit(attribute)}
                            size="icon-sm"
                            type="button"
                            variant="ghost"
                          >
                            <SquarePen />
                          </IconButton>
                        ) : null}
                        {own || attribute.owner_missing ? (
                          <IconButton
                            label={t("metadata.delete", {
                              label: attribute.label,
                            })}
                            onClick={() => setPendingDelete(attribute)}
                            size="icon-sm"
                            type="button"
                            variant="ghost"
                          >
                            <Trash2 />
                          </IconButton>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}

      <Dialog
        onOpenChange={(open) => {
          if (!open && !saving) setEditing(null);
        }}
        open={editing !== null}
      >
        <DialogContent>
          <form className="grid gap-4" onSubmit={save}>
            <DialogHeader>
              <DialogTitle>
                {editing?.original
                  ? t("metadata.editTitle")
                  : t("metadata.addTitle")}
              </DialogTitle>
            </DialogHeader>
            <div className="grid gap-3">
              <Field htmlFor="metadata-key" label={t("metadata.columns.key")}>
                <Input
                  autoComplete="off"
                  disabled={editing?.original !== null}
                  id="metadata-key"
                  onChange={(event) => change("key", event.target.value)}
                  placeholder="env"
                  value={editing?.input.key ?? ""}
                />
              </Field>
              <Field
                htmlFor="metadata-label"
                label={t("metadata.columns.label")}
              >
                <Input
                  id="metadata-label"
                  onChange={(event) => change("label", event.target.value)}
                  placeholder={t("metadata.labelPlaceholder")}
                  value={editing?.input.label ?? ""}
                />
              </Field>
              <Field
                htmlFor="metadata-pattern"
                label={t("metadata.columns.pattern")}
              >
                <Input
                  className="font-mono"
                  id="metadata-pattern"
                  onChange={(event) => change("pattern", event.target.value)}
                  placeholder="^(dev|staging|prod)$"
                  value={editing?.input.pattern ?? ""}
                />
              </Field>
              <Field
                htmlFor="metadata-marker"
                label={t("metadata.columns.marker")}
              >
                <Input
                  className="font-mono"
                  id="metadata-marker"
                  onChange={(event) => change("marker", event.target.value)}
                  placeholder={t("metadata.optional")}
                  value={editing?.input.marker ?? ""}
                />
              </Field>
              <Field
                htmlFor="metadata-description"
                label={t("metadata.description")}
              >
                <Input
                  id="metadata-description"
                  onChange={(event) =>
                    change("description", event.target.value)
                  }
                  placeholder={t("metadata.optional")}
                  value={editing?.input.description ?? ""}
                />
              </Field>
            </div>
            {formError ? (
              <FormMessage tone="error">{formError}</FormMessage>
            ) : null}
            <DialogFooter>
              <Button
                disabled={saving}
                onClick={() => setEditing(null)}
                type="button"
                variant="outline"
              >
                {t("common.cancel")}
              </Button>
              <Button disabled={saving} type="submit">
                {t("common.save")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        confirmLabel={t("metadata.deleteConfirm")}
        description={t("metadata.deleteDescription", {
          label: pendingDelete?.label ?? "",
        })}
        destructive
        onCancel={() => setPendingDelete(null)}
        onConfirm={() => void confirmDelete()}
        open={pendingDelete !== null}
        title={t("metadata.deleteTitle")}
      />
    </Panel>
  );
}
