import { type FormEvent, useEffect, useState } from "react";

import { EmptyState } from "@/components/EmptyState";
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
import { Input } from "@/components/ui/input";
import type { AccessTokenSummary } from "./access-token-model";
import {
  getAccessTokenMetadata,
  listMetadataAttributes,
  saveAccessTokenMetadata,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import type { MetadataAttribute } from "./plugin-model";

/** Whether value suits attribute: the reserved none, or what its pattern accepts. */
export function metadataValueFits(
  attribute: MetadataAttribute,
  value: string,
): boolean {
  if (value === "none") return true;
  try {
    return new RegExp(attribute.pattern).test(value);
  } catch {
    return false;
  }
}

/**
 * An access token's default metadata: the values its requests get when their
 * headers and session set none.
 */
export function TokenMetadataDialog({
  token,
  onClose,
}: {
  token: AccessTokenSummary | null;
  onClose: () => void;
}) {
  const t = useT();
  const [attributes, setAttributes] = useState<MetadataAttribute[] | null>(
    null,
  );
  const [values, setValues] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const tokenId = token?.id ?? null;

  useEffect(() => {
    if (tokenId === null) return;
    let current = true;
    setAttributes(null);
    setError(null);
    void Promise.all([
      listMetadataAttributes(),
      getAccessTokenMetadata(tokenId),
    ])
      .then(([list, defaults]) => {
        if (!current) return;
        setAttributes(list);
        setValues(defaults);
      })
      .catch((reason) => {
        if (current)
          setError(controlErrorMessage(reason, t("tokenMetadata.loadFailed")));
      });
    return () => {
      current = false;
    };
  }, [tokenId, t]);

  async function save(event: FormEvent) {
    event.preventDefault();
    if (tokenId === null || attributes === null) return;
    const next: Record<string, string> = {};
    for (const attribute of attributes) {
      const value = (values[attribute.key] ?? "").trim();
      if (value === "") continue;
      if (!metadataValueFits(attribute, value)) {
        setError(t("tokenMetadata.invalidValue", { label: attribute.label }));
        return;
      }
      next[attribute.key] = value;
    }
    setSaving(true);
    try {
      await saveAccessTokenMetadata(tokenId, next);
      onClose();
    } catch (reason) {
      setError(controlErrorMessage(reason, t("tokenMetadata.saveFailed")));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog
      onOpenChange={(open) => {
        if (!open && !saving) onClose();
      }}
      open={token !== null}
    >
      <DialogContent>
        <form className="grid gap-4" onSubmit={save}>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {t("tokenMetadata.title", { name: token?.name ?? "" })}
              <HelpPopover inDialog label={t("tokenMetadata.helpLabel")}>
                <p>{t("tokenMetadata.help")}</p>
              </HelpPopover>
            </DialogTitle>
          </DialogHeader>
          {attributes === null && !error ? (
            <LoadingState label={t("tokenMetadata.loading")} />
          ) : attributes !== null && attributes.length === 0 ? (
            <EmptyState
              description={t("tokenMetadata.emptyHint")}
              title={t("tokenMetadata.empty")}
            />
          ) : (
            <div className="grid gap-3">
              {(attributes ?? []).map((attribute) => (
                <Field
                  htmlFor={`token-metadata-${attribute.key}`}
                  key={attribute.key}
                  label={attribute.label}
                >
                  <Input
                    autoComplete="off"
                    className="font-mono"
                    id={`token-metadata-${attribute.key}`}
                    onChange={(event) =>
                      setValues((current) => ({
                        ...current,
                        [attribute.key]: event.target.value,
                      }))
                    }
                    placeholder={t("tokenMetadata.unset")}
                    value={values[attribute.key] ?? ""}
                  />
                </Field>
              ))}
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
            <Button
              disabled={
                saving || attributes === null || attributes.length === 0
              }
              type="submit"
            >
              {t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
