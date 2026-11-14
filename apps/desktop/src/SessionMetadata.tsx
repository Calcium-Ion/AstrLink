import { type FormEvent, useEffect, useState } from "react";

import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
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
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatAgo } from "@/lib/format-ago";
import {
  getSessionMetadata,
  listMetadataAttributes,
  listMetadataValues,
  setSessionMetadata,
  unbindSessionMetadata,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import { notify } from "./notify";
import type {
  MetadataAttribute,
  MetadataValueUsage,
  SessionMetadata,
  SessionMetadataValue,
} from "./plugin-model";

/** The picker's choice of a value nobody used yet, typed in beside it. */
const NEW_VALUE = "\u0000new";
import { metadataValueFits } from "./TokenMetadataDialog";

/**
 * A session's bindings in the session detail's header: one chip per
 * attribute a session marker can bind, opening a dialog to change or unbind
 * it. Nothing shows while no attribute has a marker and nothing is bound.
 * refreshKey changes whenever the session does, since a reply may bind it.
 */
export function SessionMetadataChips({
  sessionId,
  refreshKey,
}: {
  sessionId: string;
  refreshKey: string;
}) {
  const t = useT();
  const [attributes, setAttributes] = useState<MetadataAttribute[] | null>(
    null,
  );
  const [bindings, setBindings] = useState<SessionMetadata | null>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    let current = true;
    const load = async () => {
      const [list, values] = await Promise.all([
        listMetadataAttributes(),
        getSessionMetadata(sessionId),
      ]);
      if (!current || !Array.isArray(list) || !values) return;
      setAttributes(list);
      setBindings(values);
    };
    load().catch(() => {
      // The header leaves out what it could not read.
      if (current) setBindings(null);
    });
    return () => {
      current = false;
    };
  }, [sessionId, refreshKey]);

  const shown = (attributes ?? []).filter(
    (attribute) => attribute.marker || bindings?.values[attribute.key],
  );
  if (shown.length === 0 || bindings === null) return null;
  return (
    <>
      {shown.map((attribute) => {
        const bound = bindings.values[attribute.key];
        return (
          <Button
            className="h-6 max-w-44 shrink-0 gap-1 px-1.5 text-xs font-normal"
            data-testid="session-metadata-chip"
            key={attribute.key}
            onClick={() => setOpen(true)}
            size="sm"
            title={bound ? sourceText(bound, t) : undefined}
            type="button"
            variant="ghost"
          >
            <span className="text-muted-foreground">{attribute.label}</span>
            <span className="truncate font-mono">
              {bound
                ? bound.value === "none"
                  ? t("sessionMetadata.none")
                  : bound.value
                : t("sessionMetadata.notBound")}
            </span>
          </Button>
        );
      })}
      <SessionMetadataDialog
        attributes={shown}
        bindings={bindings}
        onChanged={setBindings}
        onClose={() => setOpen(false)}
        open={open}
        sessionId={sessionId}
      />
    </>
  );
}

/** How a session binding was set, and in which turn. */
export function sessionBindingSource(
  value: SessionMetadataValue,
  t: ReturnType<typeof useT>,
): string {
  return sourceText(value, t);
}

function sourceText(
  value: SessionMetadataValue,
  t: ReturnType<typeof useT>,
): string {
  const source = t(`sessionMetadata.setBy.${value.set_by}`, {
    defaultValue: value.set_by,
  });
  return value.turn
    ? t("sessionMetadata.turn", { source, turn: value.turn })
    : source;
}

/** The values a session may take, the ones used most recently first. */
function pickable(
  usage: MetadataValueUsage[],
  offered: string[],
  current: string | undefined,
): Array<{ value: string; usage?: MetadataValueUsage }> {
  const options: Array<{ value: string; usage?: MetadataValueUsage }> = [];
  const add = (value: string, entry?: MetadataValueUsage) => {
    if (value === "none" || options.some((option) => option.value === value))
      return;
    options.push({ value, usage: entry });
  };
  for (const entry of usage) add(entry.value, entry);
  for (const value of offered) add(value);
  if (current !== undefined) add(current);
  return options;
}

export function SessionMetadataDialog({
  attributes,
  bindings,
  onChanged,
  onClose,
  open,
  sessionId,
  now = new Date(),
}: {
  attributes: MetadataAttribute[];
  bindings: SessionMetadata;
  onChanged: (bindings: SessionMetadata) => void;
  onClose: () => void;
  open: boolean;
  sessionId: string;
  now?: Date;
}) {
  const t = useT();
  const [values, setValues] = useState<Record<string, string>>({});
  const [typed, setTyped] = useState<Record<string, string>>({});
  const [follow, setFollow] = useState<Record<string, boolean>>({});
  const [usage, setUsage] = useState<Record<string, MetadataValueUsage[]>>({});
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setTyped({});
    setFollow({});
    setValues(
      Object.fromEntries(
        attributes.map((attribute) => {
          const bound = bindings.values[attribute.key]?.value;
          return [attribute.key, bound && bound !== "none" ? bound : ""];
        }),
      ),
    );
    let current = true;
    Promise.all(
      attributes.map(
        async (attribute) =>
          [
            attribute.key,
            await listMetadataValues(attribute.key).catch(() => []),
          ] as const,
      ),
    )
      .then((entries) => {
        if (current) setUsage(Object.fromEntries(entries));
      })
      .catch(() => {
        // The picker offers what it has without the usage counts.
      });
    return () => {
      current = false;
    };
    // The form starts from the bindings the dialog opened with.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  /** The value the form gives an attribute: picked, or typed as a new one. */
  function chosen(key: string): string {
    const picked = values[key] ?? "";
    return picked === NEW_VALUE ? (typed[key] ?? "").trim() : picked;
  }

  /** The workspace's value the session's new one would differ from. */
  function workspaceValue(key: string): string | undefined {
    const bound = bindings.workspace?.values[key];
    const value = chosen(key);
    return bound !== undefined && value !== "" && value !== bound
      ? bound
      : undefined;
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    const changes: Array<[string, string, boolean]> = [];
    for (const attribute of attributes) {
      const value = chosen(attribute.key);
      if (value === "" || value === bindings.values[attribute.key]?.value)
        continue;
      if (!metadataValueFits(attribute, value)) {
        setError(t("sessionMetadata.invalidValue", { label: attribute.label }));
        return;
      }
      changes.push([
        attribute.key,
        value,
        workspaceValue(attribute.key) !== undefined &&
          follow[attribute.key] === true,
      ]);
    }
    setSaving(true);
    try {
      let latest = bindings;
      for (const [key, value, workspace] of changes) {
        latest = await setSessionMetadata(sessionId, key, value, workspace);
      }
      onChanged(latest);
      onClose();
    } catch (reason) {
      setError(controlErrorMessage(reason, t("sessionMetadata.saveFailed")));
    } finally {
      setSaving(false);
    }
  }

  async function unbind(attribute: MetadataAttribute) {
    setSaving(true);
    try {
      const latest = await unbindSessionMetadata(sessionId, attribute.key);
      onChanged(latest);
      setValues((current) => ({ ...current, [attribute.key]: "" }));
      setError(null);
      notify.success(
        t("sessionMetadata.unboundNotice", { label: attribute.label }),
      );
    } catch (reason) {
      setError(controlErrorMessage(reason, t("sessionMetadata.saveFailed")));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog
      onOpenChange={(next) => {
        if (!next && !saving) onClose();
      }}
      open={open}
    >
      <DialogContent>
        <form className="grid gap-4" onSubmit={save}>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {t("sessionMetadata.title")}
              <HelpPopover inDialog label={t("sessionMetadata.helpLabel")}>
                <div className="grid gap-2">
                  <p>{t("sessionMetadata.help")}</p>
                  <p>{t("sessionMetadata.unbindHelp")}</p>
                  {bindings.workspace ? (
                    <p>
                      {t("sessionMetadata.workspaceHelp", {
                        name: bindings.workspace.name,
                      })}
                    </p>
                  ) : null}
                </div>
              </HelpPopover>
            </DialogTitle>
          </DialogHeader>
          {attributes.length === 0 ? (
            <LoadingState label={t("common.loading")} />
          ) : (
            <div className="grid gap-3">
              {attributes.map((attribute) => {
                const bound = bindings.values[attribute.key];
                const options = pickable(
                  usage[attribute.key] ?? [],
                  bindings.choices?.[attribute.key] ?? [],
                  bound?.value,
                );
                const previous = workspaceValue(attribute.key);
                const fieldId = `session-metadata-${attribute.key}`;
                return (
                  <Field
                    htmlFor={fieldId}
                    key={attribute.key}
                    label={
                      bound
                        ? `${attribute.label} · ${sourceText(bound, t)}`
                        : attribute.label
                    }
                  >
                    <div className="grid gap-2">
                      <div className="flex items-center gap-2">
                        <Select
                          onValueChange={(value) =>
                            setValues((current) => ({
                              ...current,
                              [attribute.key]: value,
                            }))
                          }
                          value={values[attribute.key] || undefined}
                        >
                          <SelectTrigger
                            className="min-w-0 flex-1 font-mono"
                            id={fieldId}
                          >
                            <SelectValue
                              placeholder={t("sessionMetadata.unboundValue")}
                            />
                          </SelectTrigger>
                          <SelectContent>
                            {options.map((option) => (
                              <SelectItem
                                className="font-mono"
                                description={
                                  option.usage
                                    ? t("sessionMetadata.usage", {
                                        count: option.usage.sessions,
                                        ago: option.usage.last_used_at
                                          ? formatAgo(
                                              new Date(
                                                option.usage.last_used_at,
                                              ),
                                              now,
                                            )
                                          : t("sessionMetadata.neverUsed"),
                                      })
                                    : t("sessionMetadata.offered")
                                }
                                key={option.value}
                                value={option.value}
                              >
                                {option.value}
                              </SelectItem>
                            ))}
                            {options.length > 0 ? <SelectSeparator /> : null}
                            <SelectItem value={NEW_VALUE}>
                              {t("sessionMetadata.newValue")}
                            </SelectItem>
                          </SelectContent>
                        </Select>
                        <Button
                          disabled={saving || !bound || bound.value === "none"}
                          onClick={() => void unbind(attribute)}
                          type="button"
                          variant="outline"
                        >
                          {t("sessionMetadata.unbind")}
                        </Button>
                      </div>
                      {values[attribute.key] === NEW_VALUE ? (
                        <Input
                          aria-label={t("sessionMetadata.newValueLabel", {
                            label: attribute.label,
                          })}
                          autoComplete="off"
                          className="font-mono"
                          onChange={(event) =>
                            setTyped((current) => ({
                              ...current,
                              [attribute.key]: event.target.value,
                            }))
                          }
                          placeholder={t("sessionMetadata.newValuePlaceholder")}
                          value={typed[attribute.key] ?? ""}
                        />
                      ) : null}
                      {previous !== undefined && bindings.workspace ? (
                        <div className="flex items-center gap-2">
                          <Checkbox
                            checked={follow[attribute.key] === true}
                            id={`${fieldId}-workspace`}
                            onCheckedChange={(checked) =>
                              setFollow((current) => ({
                                ...current,
                                [attribute.key]: checked === true,
                              }))
                            }
                          />
                          <Label
                            className="text-xs font-normal"
                            htmlFor={`${fieldId}-workspace`}
                          >
                            {t("sessionMetadata.workspaceFollow", {
                              name: bindings.workspace.name,
                              previous,
                            })}
                          </Label>
                        </div>
                      ) : null}
                    </div>
                  </Field>
                );
              })}
            </div>
          )}
          {error ? <FormMessage tone="error">{error}</FormMessage> : null}
          <DialogFooter>
            <Button
              disabled={saving}
              onClick={onClose}
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
  );
}
