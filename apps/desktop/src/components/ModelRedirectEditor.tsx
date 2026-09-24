import { Fragment, useEffect, useId, useMemo, useRef } from "react";

import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { ModelRedirectHelp } from "@/components/ModelRedirectHelp";
import { ModelSelect } from "@/components/ModelSelect";
import { ArrowRight, Plus, X } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  astrlinkAutoModelId,
  builtinModelRedirects,
  maxModelRedirects,
  maxRedirectModelLength,
  modelRedirectIssues,
  type BuiltinModelRedirect,
  type ModelRedirect,
  type ModelRedirectIssue,
} from "@/failure-policy-model";
import { useT } from "@/i18n";

// Blank fields are incomplete rather than wrong; report them after editing is committed.
const incompleteIssues: ReadonlySet<ModelRedirectIssue> = new Set([
  "empty_from",
  "empty_to",
]);

type PendingFocus =
  | { kind: "from" | "to" | "remove"; index: number }
  | { kind: "add" };

/** Edits the exact-match, single-hop model redirect table in routing settings. */
export function ModelRedirectEditor({
  value,
  onChange,
  modelOptions,
  disabled = false,
  showAllIssues = false,
  onEditingChange,
}: {
  value: readonly ModelRedirect[];
  onChange: (value: ModelRedirect[]) => void;
  /** Models listed by enabled API providers. */
  modelOptions: readonly string[];
  disabled?: boolean;
  /** Also report blank fields, when automatic saving validates a committed edit. */
  showAllIssues?: boolean;
  /** Suspend automatic persistence until a model name is committed. */
  onEditingChange?: (editing: boolean) => void;
}) {
  const t = useT();
  const id = useId();
  const sectionRef = useRef<HTMLElement>(null);
  const pendingFocus = useRef<PendingFocus | null>(null);
  const issues = modelRedirectIssues(value);
  const listed = useMemo(() => new Set(modelOptions), [modelOptions]);
  const targetOptions = useMemo(
    () => modelOptions.filter((model) => model !== astrlinkAutoModelId),
    [modelOptions],
  );
  // Clients still configured with the retired auto model can be redirected.
  const sourceOptions = useMemo(
    () => [...targetOptions, astrlinkAutoModelId],
    [targetOptions],
  );
  const full = value.length >= maxModelRedirects;
  const builtinRows = builtinModelRedirects.map((builtin, builtinIndex) => {
    const index = value.findIndex((redirect) => redirect.from === builtin.from);
    return {
      builtin,
      index: index < 0 ? -builtinIndex - 1 : index,
      redirect: value[index] ?? {
        from: builtin.from,
        to: builtin.defaultTo,
        enabled: false,
      },
    };
  });
  const builtinIndices = new Set(builtinRows.map((row) => row.index));
  const rows = [
    ...builtinRows,
    ...value.flatMap((redirect, index) =>
      builtinIndices.has(index)
        ? []
        : [{ redirect, index, builtin: undefined }],
    ),
  ];

  const reveal = (focus: PendingFocus) => {
    const section = sectionRef.current;
    if (!section) return;
    if (focus.kind === "add") {
      section
        .querySelector<HTMLButtonElement>("[data-redirect-add]")
        ?.focus({ preventScroll: true });
      return;
    }
    const row = section.querySelector<HTMLElement>(
      `[data-redirect-row="${focus.index}"]`,
    );
    const target = row?.querySelector<HTMLElement>(
      focus.kind === "remove"
        ? "[data-redirect-remove]"
        : `[data-redirect-field="${focus.kind}"] [role="combobox"]`,
    );
    if (!row || !target) return;
    target.focus({ preventScroll: true });
    // Reveal the row inside the list only; the workspace must not scroll.
    const list = section.querySelector<HTMLElement>(
      '[data-slot="table-container"]',
    );
    if (!list) return;
    const bounds = list.getBoundingClientRect();
    const header =
      list.querySelector("thead")?.getBoundingClientRect().height ?? 0;
    const box = row.getBoundingClientRect();
    // Reveal the rule's help and validation messages with its controls.
    let bottom = box.bottom;
    for (
      let detail = row.nextElementSibling;
      detail && !detail.hasAttribute("data-redirect-row");
      detail = detail.nextElementSibling
    ) {
      bottom = detail.getBoundingClientRect().bottom;
    }
    if (bottom > bounds.bottom) list.scrollTop += bottom - bounds.bottom;
    else if (box.top < bounds.top + header)
      list.scrollTop -= bounds.top + header - box.top;
  };

  useEffect(() => {
    const focus = pendingFocus.current;
    if (!focus) return;
    pendingFocus.current = null;
    reveal(focus);
  });

  const add = () => {
    if (disabled || full) return;
    pendingFocus.current = { kind: "from", index: value.length };
    onChange([...value, { from: "", to: "", enabled: true }]);
  };
  const update = (
    index: number,
    redirect: ModelRedirect,
    patch: Partial<ModelRedirect>,
  ) => {
    if (disabled || (index < 0 && full)) return;
    if (index < 0) {
      onChange([...value, { ...redirect, ...patch }]);
      return;
    }
    onChange(
      value.map((item, current) =>
        current === index ? { ...item, ...patch } : item,
      ),
    );
  };
  const remove = (index: number) => {
    const next = value.filter((_, current) => current !== index);
    const removable = next.flatMap((redirect, current) =>
      builtinModelRedirects.some((builtin) => builtin.from === redirect.from)
        ? []
        : [current],
    );
    const focusIndex =
      removable.find((current) => current >= index) ?? removable.at(-1);
    pendingFocus.current =
      focusIndex === undefined
        ? { kind: "add" }
        : { kind: "remove", index: focusIndex };
    onChange(next);
  };
  const issueMessage = (issue: ModelRedirectIssue) =>
    t(`modelRedirect.issues.${issue}`, {
      max: maxRedirectModelLength,
      model: astrlinkAutoModelId,
    });

  const addButton = (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="shrink-0"
      disabled={disabled || full}
      data-redirect-add
      onClick={add}
    >
      <Plus aria-hidden="true" />
      {t("modelRedirect.add")}
    </Button>
  );

  return (
    <section
      ref={sectionRef}
      aria-labelledby={`${id}-title`}
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-2"
    >
      <div className="flex min-w-0 shrink-0 flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1.5">
          <h2 id={`${id}-title`} className="text-sm font-semibold">
            {t("modelRedirect.title")}
          </h2>
          <Badge className="tabular-nums" variant="secondary">
            {full
              ? t("modelRedirect.limit", { max: maxModelRedirects })
              : t("modelRedirect.count", { count: rows.length })}
          </Badge>
          <ModelRedirectHelp />
        </div>
        {addButton}
      </div>
      <Table
        aria-labelledby={`${id}-title`}
        className="table-fixed"
        containerClassName="min-h-0 flex-1 overflow-y-auto"
      >
        <TableHeader className="sticky top-0 z-20 bg-background">
          <TableRow>
            <TableHead className="w-14 text-center">
              {t("modelRedirect.enabled")}
            </TableHead>
            <TableHead>{t("modelRedirect.from")}</TableHead>
            <TableHead className="w-8">
              <span className="sr-only">{t("modelRedirect.arrow")}</span>
            </TableHead>
            <TableHead>{t("modelRedirect.to")}</TableHead>
            <TableHead className="w-10">
              <span className="sr-only">{t("modelRedirect.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map(({ redirect, index, builtin }) => {
            const number = index + 1;
            const rowDisabled = disabled || (index < 0 && full);
            const name =
              redirect.from || t("modelRedirect.rowName", { index: number });
            const issue = index < 0 ? undefined : issues[index];
            const shownIssue =
              issue && (showAllIssues || !incompleteIssues.has(issue))
                ? issue
                : undefined;
            const unlisted =
              !issue &&
              (!builtin || redirect.enabled) &&
              redirect.to !== "" &&
              !listed.has(redirect.to);
            return (
              <Fragment key={builtin?.id ?? index}>
                <TableRow
                  data-redirect-row={index}
                  className={shownIssue || unlisted ? "border-b-0" : undefined}
                >
                  <TableCell className="h-10 py-1 text-center">
                    <Switch
                      size="sm"
                      aria-label={t("modelRedirect.enableRule", { name })}
                      checked={redirect.enabled}
                      disabled={rowDisabled}
                      onCheckedChange={(enabled) =>
                        update(index, redirect, { enabled })
                      }
                    />
                  </TableCell>
                  <TableCell className="py-1" data-redirect-field="from">
                    {builtin ? (
                      <div className="grid gap-0.5 py-1">
                        <div className="flex flex-wrap items-center gap-x-1.5">
                          <ModelBrandIcon model={builtin.from} size={16} />
                          <span className="text-sm font-medium">
                            {t(`modelRedirect.builtin.${builtin.id}.name`)}
                          </span>
                          <span className="text-xs text-muted-foreground">
                            {t("modelRedirect.builtin.label")}
                          </span>
                          <BuiltinRedirectHelp builtin={builtin} />
                        </div>
                        <code
                          className="truncate text-xs text-muted-foreground"
                          title={builtin.from}
                        >
                          {builtin.from}
                        </code>
                      </div>
                    ) : (
                      <ModelSelect
                        aria-label={t("modelRedirect.fromLabel", {
                          index: number,
                        })}
                        options={sourceOptions}
                        value={redirect.from}
                        placeholder={t("modelRedirect.fromPlaceholder")}
                        maxLength={maxRedirectModelLength}
                        disabled={disabled}
                        onValueChange={(from) => {
                          // A recognized built-in replaces this input with its
                          // fixed label, so there will be no later blur event.
                          onEditingChange?.(
                            !builtinModelRedirects.some(
                              (builtin) => builtin.from === from,
                            ),
                          );
                          update(index, redirect, { from });
                        }}
                        onValueCommit={() => onEditingChange?.(false)}
                      />
                    )}
                  </TableCell>
                  <TableCell className="px-0 py-1 text-center">
                    <ArrowRight
                      aria-hidden="true"
                      animateOnHover={false}
                      className="inline-block size-4 text-muted-foreground"
                    />
                  </TableCell>
                  <TableCell className="py-1" data-redirect-field="to">
                    <ModelSelect
                      aria-label={
                        builtin
                          ? t("modelRedirect.builtin.toLabel", {
                              name: t(
                                `modelRedirect.builtin.${builtin.id}.name`,
                              ),
                            })
                          : t("modelRedirect.toLabel", { index: number })
                      }
                      options={targetOptions}
                      value={redirect.to}
                      placeholder={t("modelRedirect.toPlaceholder")}
                      maxLength={maxRedirectModelLength}
                      disabled={rowDisabled}
                      onValueChange={(to) => {
                        onEditingChange?.(true);
                        update(index, redirect, { to });
                      }}
                      onValueCommit={() => onEditingChange?.(false)}
                    />
                  </TableCell>
                  <TableCell className="py-1">
                    {builtin ? null : (
                      <IconButton
                        type="button"
                        className="text-muted-foreground hover:text-destructive"
                        label={t("modelRedirect.removeRule", { name })}
                        disabled={disabled}
                        data-redirect-remove
                        onClick={() => remove(index)}
                      >
                        <X aria-hidden="true" />
                      </IconButton>
                    )}
                  </TableCell>
                </TableRow>
                {shownIssue || unlisted ? (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={5} className="pt-0 pb-2">
                      <FormMessage
                        tone={shownIssue ? "error" : "warning"}
                        className="py-1 whitespace-normal"
                      >
                        {shownIssue
                          ? issueMessage(shownIssue)
                          : t("modelRedirect.targetUnlisted", {
                              model: redirect.to,
                            })}
                      </FormMessage>
                    </TableCell>
                  </TableRow>
                ) : null}
              </Fragment>
            );
          })}
        </TableBody>
      </Table>
    </section>
  );
}

function BuiltinRedirectHelp({ builtin }: { builtin: BuiltinModelRedirect }) {
  const t = useT();
  const help = (key: string) =>
    t(`modelRedirect.builtin.${builtin.id}.${key}`, {
      from: builtin.from,
      model: builtin.defaultTo,
    });
  return (
    <HelpPopover label={help("help")}>
      <div className="grid gap-2">
        <p className="font-medium text-foreground">{help("name")}</p>
        <p>{help("helpWhat")}</p>
        <p>{help("helpTarget")}</p>
        <p className="text-muted-foreground">{help("helpScope")}</p>
      </div>
    </HelpPopover>
  );
}
