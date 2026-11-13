import { useEffect, useRef, useState } from "react";

import { Check, Copy } from "@/components/icons";
import { i18n } from "@/i18n";
import { cn } from "@/lib/utils";
import { notify } from "@/notify";

/** Short values, such as model ids, each copied by a click on it. */
export function CopyChips({
  label,
  values,
  className,
}: {
  /** What the values are, for the list and the copy feedback. */
  label: string;
  values: readonly string[];
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

  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      setCopied(null);
      notify.error(i18n.t("copy.manualSelect"));
      return;
    }
    setCopied(value);
    notify.success(i18n.t("copy.copiedNamed", { label: value }));
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(null), 1_800);
  };

  return (
    <ul
      aria-label={label}
      className={cn(
        "m-0 flex min-w-0 list-none flex-wrap gap-1.5 p-0",
        className,
      )}
    >
      {values.map((value) => {
        const isCopied = copied === value;
        return (
          <li key={value} className="min-w-0">
            <button
              aria-label={i18n.t("copy.copyNamed", { label: value })}
              className={cn(
                "inline-flex h-7 max-w-full items-center gap-1.5 rounded-md border px-2 font-mono text-xs outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring/30",
                isCopied && "text-success-foreground",
              )}
              onClick={() => void copy(value)}
              title={value}
              type="button"
            >
              <span className="min-w-0 truncate">{value}</span>
              {isCopied ? (
                <Check aria-hidden="true" className="size-3.5 shrink-0" />
              ) : (
                <Copy
                  aria-hidden="true"
                  className="size-3.5 shrink-0 text-muted-foreground"
                />
              )}
            </button>
          </li>
        );
      })}
    </ul>
  );
}
