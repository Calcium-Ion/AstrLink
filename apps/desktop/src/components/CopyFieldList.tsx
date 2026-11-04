import { useEffect, useRef, useState } from "react";

import { Check, Copy } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { i18n } from "@/i18n";
import { cn } from "@/lib/utils";
import { notify } from "@/notify";

export interface CopyField {
  key: string;
  label: string;
  /** What the row shows; for a secret, only its hint. */
  value: string;
  /**
   * Copies something other than `value`, such as a token the host puts on
   * the clipboard. Resolves false when the clipboard refused it.
   */
  copy?: () => Promise<boolean>;
  /** The message when `copy` fails. */
  copyFailed?: string;
  /** A choice made from a list, shown as text with nothing to copy. */
  plain?: boolean;
}

/**
 * Values to fill in elsewhere, one row each, with a copy action and
 * clipboard feedback. Rows sit in one bordered list.
 */
export function CopyFieldList({
  fields,
  className,
}: {
  fields: readonly CopyField[];
  className?: string;
}) {
  const [copied, setCopied] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timer.current !== null) clearTimeout(timer.current);
    },
    [],
  );

  const copy = async (field: CopyField) => {
    let done = false;
    try {
      if (field.copy) {
        done = await field.copy();
      } else {
        await navigator.clipboard.writeText(field.value);
        done = true;
      }
    } catch {
      done = false;
    }
    if (!done) {
      setCopied(null);
      notify.error(field.copyFailed ?? i18n.t("copy.manualSelect"));
      return;
    }
    setCopied(field.key);
    notify.success(i18n.t("copy.copiedNamed", { label: field.label }));
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(null), 1_800);
  };

  return (
    <dl
      className={cn("grid min-w-0 rounded-md border", className)}
      data-slot="copy-field-list"
    >
      {fields.map((field) => {
        const isCopied = copied === field.key;
        return (
          <div
            key={field.key}
            className="grid min-h-9 grid-cols-[6rem_minmax(0,1fr)_auto] items-center gap-2.5 border-b px-2.5 py-1 text-xs last:border-b-0"
            data-field={field.key}
          >
            <dt className="text-muted-foreground">{field.label}</dt>
            <dd className="min-w-0">
              {field.plain ? (
                <span className="block truncate">{field.value}</span>
              ) : (
                <code
                  className="block truncate font-mono select-text"
                  title={field.value}
                >
                  {field.value}
                </code>
              )}
            </dd>
            {field.plain ? (
              <span />
            ) : (
              <Button
                aria-label={i18n.t("copy.copyNamed", {
                  label: field.label,
                })}
                className={cn(isCopied && "text-success-foreground")}
                onClick={() => void copy(field)}
                size="xs"
                type="button"
                variant="ghost"
              >
                {isCopied ? (
                  <Check aria-hidden="true" />
                ) : (
                  <Copy aria-hidden="true" />
                )}
                {isCopied ? i18n.t("common.copied") : i18n.t("common.copy")}
              </Button>
            )}
          </div>
        );
      })}
    </dl>
  );
}
