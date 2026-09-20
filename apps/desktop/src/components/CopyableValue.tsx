import { useEffect, useRef, useState } from "react";

import { Check, Copy } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { i18n } from "@/i18n";
import { notify } from "@/notify";

/** A selectable value with a compact copy action and clipboard feedback. */
export function CopyableValue({
  label,
  value,
  placeholder,
  copyLabel,
  variant = "inline",
}: {
  label: string;
  value: string;
  placeholder: string;
  copyLabel: string;
  variant?: "inline" | "block";
}) {
  const [copiedValue, setCopiedValue] = useState<string | null>(null);
  const feedbackTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const copied = Boolean(value) && copiedValue === value;

  useEffect(() => {
    setCopiedValue(null);
    return () => {
      if (feedbackTimer.current !== null) clearTimeout(feedbackTimer.current);
    };
  }, [value]);

  const copy = async () => {
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      setCopiedValue(value);
      notify.success(i18n.t("copy.copiedNamed", { label }));
      if (feedbackTimer.current !== null) clearTimeout(feedbackTimer.current);
      feedbackTimer.current = setTimeout(() => setCopiedValue(null), 1_800);
    } catch {
      setCopiedValue(null);
      notify.error(i18n.t("copy.manualSelect"));
    }
  };

  const copyButton = (
    <Button
      aria-label={copyLabel}
      className={copied ? "shrink-0 text-success-foreground" : "shrink-0"}
      disabled={!value}
      onClick={() => void copy()}
      size={variant === "block" ? "sm" : "icon-sm"}
      title={copied ? i18n.t("common.copied") : copyLabel}
      type="button"
      variant="ghost"
    >
      {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
      {variant === "block" ? (copied ? i18n.t("common.copied") : copyLabel) : null}
    </Button>
  );

  if (variant === "block") {
    return (
      <div className="grid min-w-0 gap-2">
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs font-medium">{label}</span>
          {copyButton}
        </div>
        <p className="select-text whitespace-pre-wrap text-sm leading-relaxed [overflow-wrap:anywhere]">
          {value || placeholder}
        </p>
      </div>
    );
  }

  return (
    <div className="flex min-w-0 items-center gap-2">
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="shrink-0 text-xs text-muted-foreground">{label}</span>
        <code className="min-w-0 select-text break-all font-mono text-xs font-medium tracking-tight" title={value || undefined}>
          {value || placeholder}
        </code>
      </div>
      {copyButton}
    </div>
  );
}
