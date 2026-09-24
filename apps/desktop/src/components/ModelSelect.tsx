import { useMemo, type ComponentProps } from "react";

import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { ModelLabel } from "@/components/ModelLabel";
import { Search } from "@/components/icons";
import { Combobox } from "@/components/ui/combobox";
import { useT } from "@/i18n";

/** Shared searchable model selection; also accepts partial queries and custom IDs. */
export function ModelSelect({
  options,
  value,
  emptyMessage,
  placeholder,
  clearLabel,
  ...props
}: Omit<
  ComponentProps<typeof Combobox>,
  "emptyMessage" | "leadingIcon" | "renderOption"
> & {
  emptyMessage?: string;
}) {
  const t = useT();
  const models = useMemo(
    () => [...new Set(options)].filter(Boolean),
    [options],
  );
  return (
    <Combobox
      {...props}
      value={value}
      options={models}
      maxLength={props.maxLength ?? 256}
      placeholder={placeholder ?? t("serviceTest.modelPlaceholder")}
      emptyMessage={emptyMessage ?? t("serviceTest.noMatchingModels")}
      clearLabel={clearLabel ?? t("common.clearSearch")}
      leadingIcon={
        value ? (
          <ModelBrandIcon model={value} size={16} />
        ) : (
          <Search className="size-3.5" />
        )
      }
      renderOption={(model) => <ModelLabel model={model} />}
    />
  );
}
