import { useState, type ReactNode } from "react";

import { SearchInput } from "@/components/SearchInput";
import { Checkbox } from "@/components/ui/checkbox";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

export interface ModelChecklistItem {
  id: string;
  /** Where the model comes from, such as its provider; also searched. */
  detail?: string;
  /** Inline marks after the model, such as a small badge. */
  adornment?: ReactNode;
  /** Always included, such as the default model; shown checked. */
  locked?: boolean;
}

/**
 * A searchable two-column checklist of models with a select-all box that
 * covers the models the search shows. It grows with its content; the panel
 * around it scrolls.
 */
export function ModelChecklist({
  items,
  selected,
  onSelectedChange,
  label,
  disabled = false,
  className,
}: {
  items: readonly ModelChecklistItem[];
  selected: readonly string[];
  onSelectedChange: (selected: string[]) => void;
  label: string;
  disabled?: boolean;
  className?: string;
}) {
  const t = useT();
  const [query, setQuery] = useState("");
  const search = query.trim().toLocaleLowerCase();
  const visible = items.filter((item) =>
    `${item.id} ${item.detail ?? ""}`.toLocaleLowerCase().includes(search),
  );
  const chosen = new Set([
    ...selected,
    ...items.filter((item) => item.locked).map((item) => item.id),
  ]);
  const togglable = visible.filter((item) => !item.locked);
  const checkedCount = togglable.filter((item) => chosen.has(item.id)).length;
  const allState =
    togglable.length > 0 && checkedCount === togglable.length
      ? true
      : checkedCount > 0
        ? "indeterminate"
        : false;
  // Keep the items' order, which the caller chose.
  const commit = (next: Set<string>) =>
    onSelectedChange(items.map((item) => item.id).filter((id) => next.has(id)));
  const selectedCount = items.filter((item) => chosen.has(item.id)).length;

  return (
    <div
      aria-label={label}
      className={cn(
        "@container/checklist overflow-hidden rounded-md border",
        className,
      )}
      data-slot="model-checklist"
      role="group"
    >
      <div className="flex items-center gap-2 border-b bg-muted px-2 py-1.5">
        <Checkbox
          aria-label={t("modelChecklist.selectAll")}
          checked={allState}
          disabled={disabled || togglable.length === 0}
          onCheckedChange={() => {
            const next = new Set(chosen);
            for (const item of togglable) {
              if (allState === true) next.delete(item.id);
              else next.add(item.id);
            }
            commit(next);
          }}
        />
        <SearchInput
          className="flex-1"
          clearLabel={t("common.clearSearch")}
          label={t("modelChecklist.search")}
          onValueChange={setQuery}
          placeholder={t("modelChecklist.search")}
          value={query}
        />
        <span className="shrink-0 text-xs text-text-secondary tabular-nums">
          {t("modelChecklist.count", {
            selected: selectedCount,
            total: items.length,
          })}
        </span>
      </div>
      {visible.length === 0 ? (
        <p className="px-3 py-3 text-xs text-muted-foreground">
          {t(
            items.length === 0
              ? "modelChecklist.empty"
              : "modelChecklist.noMatch",
          )}
        </p>
      ) : (
        <ul className="grid grid-cols-1 gap-x-3 px-2.5 py-1.5 @[520px]/checklist:grid-cols-2">
          {visible.map((item) => (
            <li key={item.id} className="min-w-0">
              <label
                className="flex h-7 min-w-0 items-center gap-2 text-xs"
                title={item.id}
              >
                <Checkbox
                  aria-label={item.id}
                  checked={chosen.has(item.id)}
                  disabled={disabled || item.locked}
                  onCheckedChange={(checked) => {
                    const next = new Set(chosen);
                    if (checked === true) next.add(item.id);
                    else next.delete(item.id);
                    commit(next);
                  }}
                />
                <code className="min-w-0 truncate font-mono">{item.id}</code>
                {item.adornment}
                {item.detail ? (
                  <span className="ml-auto shrink-0 pl-2 text-micro whitespace-nowrap text-muted-foreground">
                    {item.detail}
                  </span>
                ) : null}
              </label>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
