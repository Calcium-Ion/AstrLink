import { useId, useState } from "react";
import type { FailureAction, FailurePolicy } from "../failure-policy-model";
import { useT } from "../i18n";
import { cn } from "../lib/utils";
import { HelpPopover } from "./HelpPopover";
import { IconButton } from "./IconButton";
import { Plus, RotateCcw, X } from "./icons";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Switch } from "./ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";

export function FailureRulesEditor({
  value,
  onChange,
  title,
  hint,
  headingLevel = 3,
  onReset,
  className,
}: {
  value: FailurePolicy;
  onChange: (value: FailurePolicy) => void;
  title: string;
  hint?: string;
  headingLevel?: 2 | 3 | 4;
  onReset?: () => void;
  className?: string;
}) {
  const t = useT();
  const id = useId();
  const Heading = `h${headingLevel}` as const;
  const [newStatus, setNewStatus] = useState("");
  const canAdd = /^[45]\d{2}$/.test(newStatus) && !Object.hasOwn(value.http_status, newStatus);
  const update = (patch: Partial<FailurePolicy>) => onChange({ ...value, ...patch });
  const rules: { key: string; label: string; action: FailureAction; code?: string }[] = [
    { key: "network_error", label: t("failure.networkError"), action: value.network_error },
    { key: "response_timeout", label: t("failure.responseTimeout"), action: value.response_timeout },
    ...Object.entries(value.http_status)
      .sort(([a], [b]) => Number(a) - Number(b))
      .map(([code, action]) => ({ key: code, label: t(`failure.statuses.${code}`, { defaultValue: "" }), action, code })),
  ];
  const addRule = () => {
    if (!canAdd) return;
    update({ http_status: { ...value.http_status, [newStatus]: "retry_and_failover" } });
    setNewStatus("");
  };

  return (
    <section aria-labelledby={`${id}-title`} className={cn("flex min-h-0 min-w-0 flex-col gap-2", className)}>
      <div className="flex min-w-0 shrink-0 items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1">
          <Heading id={`${id}-title`} className="text-sm font-semibold">{title}</Heading>
          <HelpPopover label={t("failure.rulesHelp")}>
            <div className="grid gap-2">
              {hint ? <p>{hint}</p> : null}
              <p>{t("failure.retryRulesHint")}</p>
              <p>{t("failure.otherErrors")}</p>
            </div>
          </HelpPopover>
        </div>
        {onReset ? (
          <Button type="button" variant="ghost" size="sm" onClick={onReset}>
            <RotateCcw aria-hidden="true" />
            {t("failure.resetSection")}
          </Button>
        ) : null}
      </div>
      <Table aria-label={title} className="table-fixed" containerClassName="min-h-0 flex-1 overflow-auto">
        <TableHeader className="sticky top-0 z-10 bg-background">
          <TableRow>
            <TableHead>{t("failure.errorType")}</TableHead>
            <TableHead className="w-24 text-center whitespace-normal">{t("failure.allowRetry")}</TableHead>
            <TableHead className="w-10"><span className="sr-only">{t("failure.ruleActions")}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rules.map(({ key, label, action, code }) => {
            const errorLabel = code ? `${t("failure.httpCode", { code })}${label ? ` · ${label}` : ""}` : label;
            return (
              <TableRow key={key}>
                <TableCell className="py-1 text-xs whitespace-normal">
                  {code ? (
                    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
                      <span className="font-mono tabular-nums">{t("failure.httpCode", { code })}</span>
                      {label ? <span className="text-muted-foreground">{label}</span> : null}
                    </div>
                  ) : label}
                </TableCell>
                <TableCell className="h-9 py-1 text-center">
                  <Switch
                    size="sm"
                    aria-label={`${errorLabel} · ${t("failure.allowRetry")}`}
                    checked={action !== "stop"}
                    onCheckedChange={(checked) => {
                      const next = checked ? "retry_and_failover" : "stop";
                      if (code) update({ http_status: { ...value.http_status, [code]: next } });
                      else update({ [key]: next });
                    }}
                  />
                </TableCell>
                <TableCell className="py-1">
                  {code ? (
                    <IconButton
                      type="button"
                      className="text-muted-foreground hover:text-destructive"
                      label={t("failure.removeCode", { code })}
                      onClick={() => {
                        const next = { ...value.http_status };
                        delete next[code];
                        update({ http_status: next });
                      }}
                    >
                      <X aria-hidden="true" />
                    </IconButton>
                  ) : null}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-t pt-2">
        <Input
          aria-label={t("failure.addStatus")}
          className="w-32 font-mono"
          inputMode="numeric"
          maxLength={3}
          placeholder="400-599"
          value={newStatus}
          onChange={(event) => setNewStatus(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              addRule();
            }
          }}
        />
        <Button type="button" variant="outline" size="sm" disabled={!canAdd} onClick={addRule}>
          <Plus aria-hidden="true" />
          {t("failure.addRule")}
        </Button>
      </div>
    </section>
  );
}
