import { ModelSelect } from "./components/ModelSelect";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ChevronDown, Plus, X } from "@/components/icons";
import { useT } from "./i18n";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Panel } from "@/components/Panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

import { encodeModelEditorValue } from "./model-editor";
import {
  filterModels,
  groupModelsByCategory,
  type ModelCategory,
  type ModelGroup,
} from "./model-groups";

export type ServiceModelsEditorProps = {
  models: string[];
  modelEditor: string;
  probingModels: boolean;
  onModelEditorChange: (value: string) => void;
  onAddModels: () => void;
  onDiscoverModels?: () => void;
  onRemoveModels: (models: string[]) => void;
  onClearModels: () => void;
};

type ModelsConfirm =
  | { kind: "clear" }
  | { kind: "remove_filtered"; models: string[] }
  | { kind: "remove_category"; category: string; models: string[] }
  | { kind: "remove_group"; group: string; models: string[] }
  | null;

const COLLAPSE_THRESHOLD = 12;

function categoryCollapseKey(key: string): string {
  return `cat:${key}`;
}

function groupCollapseKey(key: string): string {
  return `grp:${key}`;
}

function collapseKeysFor(models: readonly string[]): string[] {
  return groupModelsByCategory(models).flatMap((category) => [
    ...(category.groups.length > 1 ? [categoryCollapseKey(category.key)] : []),
    ...category.groups.map((group) => groupCollapseKey(group.key)),
  ]);
}

function initialCollapsed(models: readonly string[]): Set<string> {
  if (models.length < COLLAPSE_THRESHOLD) return new Set();
  return new Set(collapseKeysFor(models));
}

function categoryModels(category: ModelCategory): string[] {
  return category.groups.flatMap((group) => group.models);
}

const LIST_INSET = "px-3";
const LIST_GAP = "gap-2";

function CollapseChevron({ collapsed }: { collapsed: boolean }) {
  return (
    <ChevronDown
      aria-hidden="true"
      className={cn(
        "size-4 shrink-0 text-muted-foreground transition-transform",
        collapsed && "-rotate-90",
      )}
    />
  );
}

function ModelListLead({ children }: { children: ReactNode }) {
  return (
    <span className="flex size-4 shrink-0 items-center justify-center">
      {children}
    </span>
  );
}

function ModelListHeader({
  brandModel,
  collapsed,
  countLabel,
  onRemove,
  onToggle,
  removeLabel,
  testId,
  title,
  tone,
}: {
  brandModel?: string;
  collapsed: boolean;
  countLabel: string;
  onRemove: () => void;
  onToggle: () => void;
  removeLabel: string;
  testId: "service-model-category-toggle" | "service-model-group-toggle";
  title: string;
  tone: "card" | "section";
}) {
  return (
    <div
      className={cn(
        "group flex items-center",
        LIST_GAP,
        LIST_INSET,
        "py-1.5",
        tone === "card" && "bg-muted/70",
        tone === "card" && !collapsed && "border-b",
      )}
    >
      <Button
        aria-expanded={!collapsed}
        className="h-auto min-w-0 flex-1 justify-start gap-2 rounded-sm p-0 text-left text-sm text-text-secondary"
        data-testid={testId}
        onClick={onToggle}
        type="button"
        variant="ghost"
      >
        <ModelListLead>
          <CollapseChevron collapsed={collapsed} />
        </ModelListLead>
        {brandModel ? <ModelBrandIcon model={brandModel} size={16} /> : null}
        <strong
          className={cn(
            "min-w-0 overflow-hidden font-medium text-foreground text-ellipsis whitespace-nowrap",
            tone === "section" ? "text-xs" : "text-sm",
          )}
        >
          {title}
        </strong>
        <Badge
          className="px-1.5 py-0 text-micro tabular-nums"
          variant="secondary"
        >
          {countLabel}
        </Badge>
      </Button>
      <Button
        aria-label={removeLabel}
        className="size-6 shrink-0 text-sm text-danger-foreground opacity-0 hover:bg-danger-wash group-hover:opacity-100 group-focus-within:opacity-100"
        onClick={onRemove}
        type="button"
        size="icon-xs"
        variant="ghost"
      >
        <X className="size-3" />
      </Button>
    </div>
  );
}

function ModelRow({
  model,
  onRemove,
}: {
  model: string;
  onRemove: () => void;
}) {
  const t = useT();
  const label = encodeModelEditorValue(model);
  return (
    <DataRow
      className={cn("group/row py-2", LIST_GAP, LIST_INSET)}
      data-testid="service-model-row"
    >
      <ModelListLead>
        <ModelBrandIcon model={model} size={16} />
      </ModelListLead>
      <code
        className="min-w-0 flex-1 overflow-hidden text-sm text-ellipsis whitespace-nowrap"
        title={label}
      >
        {label}
      </code>
      <Button
        aria-label={t("models.deleteNamed", { label })}
        className="size-6 shrink-0 rounded text-sm text-danger-foreground opacity-0 hover:bg-danger-wash group-hover/row:opacity-100 group-focus-within/row:opacity-100"
        onClick={onRemove}
        type="button"
        size="icon-xs"
        variant="ghost"
      >
        <X className="size-3" />
      </Button>
    </DataRow>
  );
}

function ModelGroupSection({
  collapsed,
  group,
  nested = false,
  onRemove,
  onRemoveModel,
  onToggle,
}: {
  collapsed: boolean;
  group: ModelGroup;
  nested?: boolean;
  onRemove: () => void;
  onRemoveModel: (model: string) => void;
  onToggle: () => void;
}) {
  const t = useT();
  const body = (
    <>
      <ModelListHeader
        brandModel={nested ? undefined : group.models[0]}
        collapsed={collapsed}
        countLabel={`${group.models.length}`}
        onRemove={onRemove}
        onToggle={onToggle}
        removeLabel={t("models.deleteGroup", { group: group.key })}
        testId="service-model-group-toggle"
        title={group.key}
        tone={nested ? "section" : "card"}
      />
      {collapsed
        ? null
        : group.models.map((model) => (
            <ModelRow
              key={model}
              model={model}
              onRemove={() => onRemoveModel(model)}
            />
          ))}
    </>
  );

  if (nested) {
    return <section className="min-w-0">{body}</section>;
  }

  return (
    <Panel asChild>
      <section>{body}</section>
    </Panel>
  );
}

export function ServiceModelsEditor({
  models,
  modelEditor,
  probingModels,
  onModelEditorChange,
  onAddModels,
  onDiscoverModels,
  onRemoveModels,
  onClearModels,
}: ServiceModelsEditorProps) {
  const t = useT();
  const [query, setQuery] = useState("");
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [confirm, setConfirm] = useState<ModelsConfirm>(null);
  const [adding, setAdding] = useState(false);
  const [bulkPaste, setBulkPaste] = useState(false);
  const addInputRef = useRef<HTMLInputElement | null>(null);
  const seededCollapse = useRef(false);

  const catalog = useMemo(() => [...models].sort(), [models]);

  useEffect(() => {
    if (seededCollapse.current) return;
    if (catalog.length === 0) return;
    seededCollapse.current = true;
    setCollapsed(initialCollapsed(catalog));
  }, [catalog]);

  useEffect(() => {
    if (!adding) return;
    addInputRef.current?.focus();
  }, [adding, bulkPaste]);

  const filtered = useMemo(
    () => filterModels(catalog, query),
    [catalog, query],
  );
  const categories = useMemo(() => groupModelsByCategory(filtered), [filtered]);
  const groupCount = useMemo(
    () =>
      categories.reduce((count, category) => count + category.groups.length, 0),
    [categories],
  );
  const nestedCategoryCount = useMemo(
    () => categories.filter((category) => category.groups.length > 1).length,
    [categories],
  );
  const allCollapseKeys = useMemo(() => collapseKeysFor(catalog), [catalog]);
  const hasQuery = query.trim().length > 0;
  const allCollapsed =
    !hasQuery &&
    allCollapseKeys.length > 0 &&
    allCollapseKeys.every((key) => collapsed.has(key));

  const isGroupCollapsed = (key: string) => {
    if (hasQuery) return false;
    return collapsed.has(key);
  };

  const toggleGroup = (key: string) => {
    if (hasQuery) return;
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const expandAll = () => setCollapsed(new Set());
  const collapseAll = () => setCollapsed(new Set(allCollapseKeys));

  const resetBrowsing = () => {
    setQuery("");
  };

  const applyConfirm = () => {
    if (!confirm) return;
    if (confirm.kind === "clear") {
      onClearModels();
      resetBrowsing();
      setCollapsed(new Set());
      seededCollapse.current = false;
    } else {
      onRemoveModels(confirm.models);
      if (confirm.kind === "remove_filtered") resetBrowsing();
    }
    setConfirm(null);
  };

  const submitAdd = () => {
    onAddModels();
  };

  return (
    <fieldset
      aria-labelledby="service-models-editor-heading"
      className="min-w-0 border-0 border-t bg-transparent px-0 pt-3 pb-0.5"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="grid min-w-0 gap-0.5">
          <strong
            className="text-sm font-semibold"
            id="service-models-editor-heading"
          >
            {t("models.title")}
          </strong>
          <p className="text-xs text-muted-foreground">{t("models.hint")}</p>
        </div>
        <Badge
          className="mt-px shrink-0 tabular-nums"
          aria-live="polite"
          variant="secondary"
        >
          {t("models.count", { count: catalog.length })}
        </Badge>
      </div>

      <div className="mt-2.5 flex min-w-0 flex-wrap items-center gap-2">
        <ModelSelect
          options={catalog}
          aria-label={t("models.searchConfigured")}
          className="min-w-0 flex-[1_1_160px]"
          placeholder={t("models.searchPlaceholder")}
          value={query}
          onValueChange={setQuery}
        />
        {onDiscoverModels ? (
          <Button
            className="h-8 shrink-0"
            disabled={probingModels}
            onClick={onDiscoverModels}
            type="button"
            variant="outline"
          >
            {probingModels ? t("models.fetching") : t("models.fetchList")}
          </Button>
        ) : null}
        <Button
          aria-expanded={adding}
          aria-label={t("models.addModel")}
          className="size-8 shrink-0 p-0 text-base font-semibold"
          onClick={() => {
            setAdding((open) => {
              const next = !open;
              if (!next) {
                setBulkPaste(false);
                onModelEditorChange("");
              }
              return next;
            });
          }}
          type="button"
          size="icon-sm"
          variant="outline"
        >
          {adding ? <X className="size-4" /> : <Plus className="size-4" />}
        </Button>
      </div>

      {adding ? (
        <div className="mt-2 grid gap-2 rounded-md border bg-muted p-2.5">
          {bulkPaste ? (
            <Textarea
              aria-label={t("models.pendingIds")}
              placeholder={t("models.bulkPlaceholder")}
              className="min-h-[72px] resize-y"
              rows={3}
              value={modelEditor}
              onChange={(event) => onModelEditorChange(event.target.value)}
            />
          ) : (
            <Input
              ref={addInputRef}
              aria-label={t("models.pendingIds")}
              placeholder={t("models.singlePlaceholder")}
              type="text"
              value={modelEditor}
              onChange={(event) => onModelEditorChange(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  submitAdd();
                }
              }}
            />
          )}
          <div className="flex items-center justify-end gap-2.5">
            <Button
              className="h-auto px-0 text-xs"
              onClick={() => setBulkPaste((value) => !value)}
              type="button"
              variant="link"
            >
              {bulkPaste ? t("models.singleMode") : t("models.bulkMode")}
            </Button>
            <Button variant="outline" onClick={submitAdd} type="button">
              {t("models.add")}
            </Button>
          </div>
        </div>
      ) : null}

      {catalog.length === 0 ? (
        <p
          className="mt-2.5 rounded-md border border-dashed bg-muted/70 p-3 text-center text-xs text-muted-foreground"
          role="status"
        >
          {t("models.empty")}
        </p>
      ) : (
        <>
          <div className="my-1.5 mt-2 flex min-h-4 items-center justify-between gap-2.5">
            <span className="text-xs font-semibold text-muted-foreground tabular-nums">
              {hasQuery
                ? t("models.matchCount", {
                    shown: filtered.length,
                    total: catalog.length,
                  })
                : nestedCategoryCount > 0
                  ? t("models.nestedSummary", {
                      categories: nestedCategoryCount,
                      groups: groupCount,
                      count: catalog.length,
                    })
                  : t("models.groupSummary", {
                      groups: groupCount,
                      count: catalog.length,
                    })}
            </span>
            <div className="flex flex-wrap items-center justify-end gap-2.5">
              {hasQuery ? (
                <Button
                  className="h-auto px-0 text-xs text-danger-foreground"
                  disabled={filtered.length === 0}
                  onClick={() =>
                    setConfirm({
                      kind: "remove_filtered",
                      models: filtered,
                    })
                  }
                  type="button"
                  variant="link"
                >
                  {t("models.deleteMatches", { count: filtered.length })}
                </Button>
              ) : (
                <Button
                  className="h-auto px-0 text-xs"
                  onClick={allCollapsed ? expandAll : collapseAll}
                  type="button"
                  variant="link"
                >
                  {allCollapsed
                    ? t("models.expandAll")
                    : t("models.collapseAll")}
                </Button>
              )}
              <Button
                className="h-auto px-0 text-xs text-danger-foreground"
                onClick={() => setConfirm({ kind: "clear" })}
                type="button"
                variant="link"
              >
                {t("models.clear")}
              </Button>
            </div>
          </div>

          {filtered.length === 0 ? (
            <p
              className="mt-1.5 rounded-md border border-dashed p-2.5 text-xs text-muted-foreground"
              role="status"
            >
              {t("models.noMatch", { query: query.trim() })}
            </p>
          ) : (
            <div className="grid gap-2" aria-label={t("models.configured")}>
              {categories.map((category) => {
                if (category.groups.length === 1) {
                  const group = category.groups[0]!;
                  return (
                    <ModelGroupSection
                      key={group.key}
                      collapsed={isGroupCollapsed(groupCollapseKey(group.key))}
                      group={group}
                      onRemove={() =>
                        setConfirm({
                          kind: "remove_group",
                          group: group.key,
                          models: group.models,
                        })
                      }
                      onRemoveModel={(model) => onRemoveModels([model])}
                      onToggle={() => toggleGroup(groupCollapseKey(group.key))}
                    />
                  );
                }

                const models = categoryModels(category);
                const collapsedCategory = isGroupCollapsed(
                  categoryCollapseKey(category.key),
                );
                return (
                  <Panel asChild key={category.key}>
                    <section>
                      <ModelListHeader
                        brandModel={models[0]}
                        collapsed={collapsedCategory}
                        countLabel={`${models.length}`}
                        onRemove={() =>
                          setConfirm({
                            kind: "remove_category",
                            category: category.key,
                            models,
                          })
                        }
                        onToggle={() =>
                          toggleGroup(categoryCollapseKey(category.key))
                        }
                        removeLabel={t("models.deleteCategory", {
                          category: category.key,
                        })}
                        testId="service-model-category-toggle"
                        title={category.key}
                        tone="card"
                      />
                      {collapsedCategory ? null : (
                        <div className="[&>section+section]:border-t">
                          {category.groups.map((group) => (
                            <ModelGroupSection
                              key={group.key}
                              collapsed={isGroupCollapsed(
                                groupCollapseKey(group.key),
                              )}
                              group={group}
                              nested
                              onRemove={() =>
                                setConfirm({
                                  kind: "remove_group",
                                  group: group.key,
                                  models: group.models,
                                })
                              }
                              onRemoveModel={(model) => onRemoveModels([model])}
                              onToggle={() =>
                                toggleGroup(groupCollapseKey(group.key))
                              }
                            />
                          ))}
                        </div>
                      )}
                    </section>
                  </Panel>
                );
              })}
            </div>
          )}
        </>
      )}

      <ConfirmDialog
        confirmLabel={t("models.confirmDelete")}
        description={
          <p>
            {confirm?.kind === "clear"
              ? t("models.clearAllBody", { count: catalog.length })
              : confirm
                ? t("models.deleteSomeBody", { count: confirm.models.length })
                : ""}
          </p>
        }
        destructive
        onCancel={() => setConfirm(null)}
        onConfirm={applyConfirm}
        open={confirm !== null}
        title={
          confirm?.kind === "clear"
            ? t("models.clearTitle")
            : confirm?.kind === "remove_category"
              ? t("models.deleteCategoryTitle", { category: confirm.category })
              : confirm?.kind === "remove_group"
                ? t("models.deleteGroupTitle", { group: confirm.group })
                : t("models.deleteMatchesTitle")
        }
      />
    </fieldset>
  );
}
