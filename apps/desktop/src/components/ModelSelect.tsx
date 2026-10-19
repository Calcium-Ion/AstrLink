import { useMemo, type ComponentProps, type ReactNode } from "react";

import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { ModelLabel } from "@/components/ModelLabel";
import { Search } from "@/components/icons";
import { Combobox } from "@/components/ui/combobox";
import { useLocale, useT } from "@/i18n";

/** Shared searchable model selection; also accepts partial queries and custom IDs. */
export function ModelSelect({
  options,
  value,
  emptyMessage,
  placeholder,
  clearLabel,
  optionAdornment,
  optionLabel,
  optionGroup,
  leadingIcon,
  onValueChange,
  ...props
}: Omit<
  ComponentProps<typeof Combobox>,
  "emptyMessage" | "leadingIcon" | "options" | "renderOption"
> & {
  /** Wire values; a `string[]` because callers pass ids, not display text. */
  options: readonly string[];
  emptyMessage?: string;
  optionAdornment?: (model: string) => ReactNode;
  /**
   * Display name of an option when it is not a model. The control shows the
   * label and the caller keeps the wire value: `onValueChange` still receives
   * the value, and the label is what the list filters on.
   */
  optionLabel?: (value: string) => string;
  /**
   * Heading an option sits under. Options arrive in group order; consecutive
   * options with the same heading share one.
   */
  optionGroup?: (value: string) => string | undefined;
  /** Overrides the model brand mark; `null` drops the leading icon entirely. */
  leadingIcon?: ReactNode;
}) {
  const t = useT();
  // `optionLabel` reads the language imperatively, so a locale change must
  // invalidate the labels even though the callback itself never changes.
  const locale = useLocale();
  const models = useMemo(
    () => [...new Set(options)].filter(Boolean),
    [options],
  );
  const labelled = useMemo(
    () =>
      models.map((model) => ({
        value: model,
        label: optionLabel?.(model) ?? model,
        group: optionGroup?.(model),
      })),
    [models, optionLabel, optionGroup, locale],
  );
  // A labelled option set is closed: the control renders the label and each row
  // carries its own value, so a duplicate label can never redirect the write.
  const shown =
    labelled.find((option) => option.value === value)?.label ?? value;
  return (
    <Combobox
      {...props}
      value={value}
      options={labelled}
      displayValue={shown}
      onValueChange={onValueChange}
      maxLength={props.maxLength ?? 256}
      placeholder={placeholder ?? t("serviceTest.modelPlaceholder")}
      emptyMessage={emptyMessage ?? t("serviceTest.noMatchingModels")}
      // A closed option set has no empty value to clear, so the × would write
      // `""` — a key the caller can never read back. Suppress it there only.
      clearLabel={
        props.allowCustomValue === false
          ? undefined
          : (clearLabel ?? t("common.clearSearch"))
      }
      leadingIcon={
        leadingIcon === undefined ? (
          value ? (
            <ModelBrandIcon model={value} size={16} />
          ) : (
            <Search className="size-3.5" />
          )
        ) : (
          leadingIcon
        )
      }
      renderOption={(label, option) => {
        if (!optionLabel) {
          return (
            <span className="flex min-w-0 items-center gap-2">
              <ModelLabel model={label} className="flex-1" />
              {option ? optionAdornment?.(option.value) : null}
            </span>
          );
        }
        return (
          <span className="flex min-w-0 items-center gap-2">
            <span className="min-w-0 flex-1 break-all">{label}</span>
            {option ? optionAdornment?.(option.value) : null}
          </span>
        );
      }}
    />
  );
}
