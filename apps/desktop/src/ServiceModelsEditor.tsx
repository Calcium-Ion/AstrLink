import { useEffect, useMemo, useRef, useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";

import { encodeModelEditorValue } from "./model-editor";
import { filterModels, groupModels } from "./model-groups";

export type ServiceModelsEditorProps = {
  models: string[];
  modelEditor: string;
  probingModels: boolean;
  onModelEditorChange: (value: string) => void;
  onAddModels: () => void;
  onDiscoverModels: () => void;
  onRemoveModels: (models: string[]) => void;
  onReplaceModels: (models: string[]) => void;
};

type ModelsConfirm =
  | { kind: "clear" }
  | { kind: "remove_filtered"; models: string[] }
  | { kind: "remove_group"; group: string; models: string[] }
  | null;

const COLLAPSE_THRESHOLD = 12;

function initialCollapsed(models: readonly string[]): Set<string> {
  if (models.length < COLLAPSE_THRESHOLD) return new Set();
  return new Set(groupModels(models).map((group) => group.key));
}

export function ServiceModelsEditor({
  models,
  modelEditor,
  probingModels,
  onModelEditorChange,
  onAddModels,
  onDiscoverModels,
  onRemoveModels,
  onReplaceModels,
}: ServiceModelsEditorProps) {
  const [query, setQuery] = useState("");
  const [collapsed, setCollapsed] = useState<Set<string>>(() =>
    initialCollapsed(models),
  );
  const [confirm, setConfirm] = useState<ModelsConfirm>(null);
  const [adding, setAdding] = useState(false);
  const [bulkPaste, setBulkPaste] = useState(false);
  const addInputRef = useRef<HTMLInputElement | null>(null);
  const seededCollapse = useRef(false);

  useEffect(() => {
    if (seededCollapse.current) return;
    if (models.length === 0) return;
    seededCollapse.current = true;
    setCollapsed(initialCollapsed(models));
  }, [models]);

  useEffect(() => {
    if (!adding) return;
    addInputRef.current?.focus();
  }, [adding, bulkPaste]);

  const filtered = useMemo(() => filterModels(models, query), [models, query]);
  const groups = useMemo(() => groupModels(filtered), [filtered]);
  const allGroupKeys = useMemo(
    () => groupModels(models).map((group) => group.key),
    [models],
  );
  const hasQuery = query.trim().length > 0;
  const allCollapsed =
    !hasQuery &&
    allGroupKeys.length > 0 &&
    allGroupKeys.every((key) => collapsed.has(key));

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
  const collapseAll = () => setCollapsed(new Set(allGroupKeys));

  const applyConfirm = () => {
    if (!confirm) return;
    if (confirm.kind === "clear") {
      onReplaceModels([]);
      setQuery("");
      setCollapsed(new Set());
      seededCollapse.current = false;
    } else {
      onRemoveModels(confirm.models);
      if (confirm.kind === "remove_filtered") setQuery("");
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
          <strong className="text-[12.5px] font-bold" id="service-models-editor-heading">支持模型</strong>
          <p className="text-[9px] leading-[1.4] text-muted-foreground">
            精确匹配白名单；空清单不参与推理路由。
          </p>
        </div>
        <Badge className="mt-px shrink-0 tabular-nums" aria-live="polite" variant="secondary">
          {hasQuery && models.length > 0
            ? `${filtered.length} / ${models.length}`
            : `${models.length} / 2,000`}
        </Badge>
      </div>

      <div className="mt-2.5 flex min-w-0 flex-wrap items-center gap-2">
        <Input
          aria-label="搜索已配置模型"
          className="h-8 min-w-0 flex-[1_1_160px]"
          placeholder="搜索模型…"
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Button
          className="h-8 shrink-0"
          disabled={probingModels}
          onClick={onDiscoverModels}
          type="button"
          variant="outline"
        >
          {probingModels ? "获取中…" : "获取模型列表"}
        </Button>
        <Button
          aria-expanded={adding}
          aria-label="添加模型"
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
          {adding ? "−" : "+"}
        </Button>
      </div>

      {adding ? (
        <div className="mt-2 grid gap-2 rounded-[9px] border bg-muted p-[9px]">
          {bulkPaste ? (
            <Textarea
              aria-label="待添加模型 ID"
              placeholder={"每行一个模型 ID，例如：\ngpt-5\nclaude-sonnet-4-5"}
              className="min-h-[72px] resize-y"
              rows={3}
              value={modelEditor}
              onChange={(event) => onModelEditorChange(event.target.value)}
            />
          ) : (
            <Input
              ref={addInputRef}
              aria-label="待添加模型 ID"
              placeholder="输入模型 ID，回车添加"
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
              className="h-auto px-0 text-[9.5px]"
              onClick={() => setBulkPaste((value) => !value)}
              type="button"
              variant="link"
            >
              {bulkPaste ? "单行输入" : "批量粘贴"}
            </Button>
            <Button variant="outline" onClick={submitAdd} type="button">
              添加
            </Button>
          </div>
        </div>
      ) : null}

      {models.length === 0 ? (
        <p className="mt-2.5 rounded-[9px] border border-dashed bg-muted/70 p-3 text-center text-[10.5px] text-muted-foreground" role="status">
          还没有模型 · 服务不会参与路由
        </p>
      ) : (
        <>
          <div className="my-1.5 mt-2 flex min-h-4 items-center justify-between gap-2.5">
            <span className="text-[9.5px] font-semibold text-muted-foreground tabular-nums">
              {hasQuery
                ? `匹配 ${filtered.length} / ${models.length}`
                : `${groups.length} 组 · ${models.length} 个模型`}
            </span>
            <div className="flex flex-wrap items-center justify-end gap-2.5">
              {!hasQuery ? (
                <Button
                  className="h-auto px-0 text-[9.5px]"
                  onClick={allCollapsed ? expandAll : collapseAll}
                  type="button"
                  variant="link"
                >
                  {allCollapsed ? "展开全部" : "折叠全部"}
                </Button>
              ) : (
                <Button
                  className="h-auto px-0 text-[9.5px] text-danger-foreground"
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
                  删除匹配（{filtered.length}）
                </Button>
              )}
              <Button
                className="h-auto px-0 text-[9.5px] text-danger-foreground"
                onClick={() => setConfirm({ kind: "clear" })}
                type="button"
                variant="link"
              >
                清空
              </Button>
            </div>
          </div>

          {filtered.length === 0 ? (
            <p className="mt-1.5 rounded-[9px] border border-dashed p-2.5 text-[10.5px] text-muted-foreground" role="status">
              没有匹配“{query.trim()}”的模型。
            </p>
          ) : (
            <div className="grid gap-2" aria-label="已配置模型">
              {groups.map((group) => {
                const collapsedGroup = isGroupCollapsed(group.key);
                return (
                  <section className="min-w-0" key={group.key}>
                    <div className="group flex items-center gap-1.5 border-b px-px py-[5px]">
                      <Button
                        aria-expanded={!collapsedGroup}
                        className="h-auto min-w-0 flex-1 justify-start gap-1.5 px-0 text-left text-[11px] text-text-secondary hover:bg-transparent"
                        data-testid="service-model-group-toggle"
                        onClick={() => toggleGroup(group.key)}
                        type="button"
                        variant="ghost"
                      >
                        <span
                          aria-hidden="true"
                          className="shrink-0 text-[8px] leading-none text-muted-foreground"
                        >
                          {collapsedGroup ? "▸" : "▾"}
                        </span>
                        <strong className="min-w-0 overflow-hidden text-[11px] font-bold text-foreground text-ellipsis whitespace-nowrap">{group.key}</strong>
                        <Badge className="px-1.5 py-0 text-[8.5px] tabular-nums" variant="secondary">
                          {group.models.length}
                        </Badge>
                      </Button>
                      <Button
                        aria-label={`删除分组 ${group.key}`}
                        className="size-6 shrink-0 text-sm text-danger-foreground opacity-0 hover:bg-danger-wash group-hover:opacity-100 group-focus-within:opacity-100"
                        onClick={() =>
                          setConfirm({
                            kind: "remove_group",
                            group: group.key,
                            models: group.models,
                          })
                        }
                        type="button"
                        size="icon-xs"
                        variant="ghost"
                      >
                        −
                      </Button>
                    </div>
                    {collapsedGroup ? null : (
                      <div className="mt-[7px] flex flex-wrap gap-[5px]">
                        {group.models.map((model) => {
                          const label = encodeModelEditorValue(model);
                          return (
                            <div className="group/chip inline-flex min-w-0 max-w-[260px] items-center gap-1 overflow-hidden rounded-[7px] border bg-muted py-1 pr-1.5 pl-2 text-text-secondary hover:border-primary/25" data-testid="service-model-chip" key={model}>
                              <code className="min-w-0 overflow-hidden text-[9.5px] text-ellipsis whitespace-nowrap" title={label}>{label}</code>
                              <Button
                                aria-label={`删除 ${label}`}
                                className="size-6 shrink-0 rounded text-[11px] text-danger-foreground opacity-0 hover:bg-danger-wash group-hover/chip:opacity-100 group-focus-within/chip:opacity-100"
                                onClick={() => onRemoveModels([model])}
                                type="button"
                                size="icon-xs"
                                variant="ghost"
                              >
                                ×
                              </Button>
                            </div>
                          );
                        })}
                      </div>
                    )}
                  </section>
                );
              })}
            </div>
          )}
        </>
      )}

      <ConfirmDialog
        confirmLabel="确认删除"
        description={
          <p>
              {confirm?.kind === "clear"
                ? `将移除全部 ${models.length} 个模型；空清单时该服务不会参与推理路由。`
                : confirm
                  ? `将从白名单移除 ${confirm.models.length} 个模型。保存前可继续编辑。`
                  : ""}
          </p>
        }
        destructive
        onCancel={() => setConfirm(null)}
        onConfirm={applyConfirm}
        open={confirm !== null}
        title={
          confirm?.kind === "clear"
            ? "清空支持模型？"
            : confirm?.kind === "remove_group"
              ? `删除分组“${confirm.group}”？`
              : "删除匹配的模型？"
        }
      />
    </fieldset>
  );
}
