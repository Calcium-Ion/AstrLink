import { useEffect, useMemo, useRef, useState } from "react";

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
      className="service-models-editor"
    >
      <div className="service-models-editor__heading">
        <div className="service-models-editor__title">
          <strong id="service-models-editor-heading">支持模型</strong>
          <p className="service-models-editor__help">
            精确匹配白名单；空清单不参与推理路由。
          </p>
        </div>
        <span className="service-models-editor__count" aria-live="polite">
          {hasQuery && models.length > 0
            ? `${filtered.length} / ${models.length}`
            : `${models.length} / 2,000`}
        </span>
      </div>

      <div className="service-models-editor__toolbar">
        <input
          aria-label="搜索已配置模型"
          className="service-models-editor__search"
          placeholder="搜索模型…"
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <button
          className="btn-secondary service-models-editor__fetch"
          disabled={probingModels}
          onClick={onDiscoverModels}
          type="button"
        >
          {probingModels ? "获取中…" : "获取模型列表"}
        </button>
        <button
          aria-expanded={adding}
          aria-label="添加模型"
          className="btn-secondary service-models-editor__add-toggle"
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
        >
          {adding ? "−" : "+"}
        </button>
      </div>

      {adding ? (
        <div className="service-models-editor__add">
          {bulkPaste ? (
            <textarea
              aria-label="待添加模型 ID"
              placeholder={"每行一个模型 ID，例如：\ngpt-5\nclaude-sonnet-4-5"}
              rows={3}
              value={modelEditor}
              onChange={(event) => onModelEditorChange(event.target.value)}
            />
          ) : (
            <input
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
          <div className="service-models-editor__add-actions">
            <button
              className="service-models-editor__text-action"
              onClick={() => setBulkPaste((value) => !value)}
              type="button"
            >
              {bulkPaste ? "单行输入" : "批量粘贴"}
            </button>
            <button className="btn-secondary" onClick={submitAdd} type="button">
              添加
            </button>
          </div>
        </div>
      ) : null}

      {models.length === 0 ? (
        <p className="service-models-editor__empty" role="status">
          还没有模型 · 服务不会参与路由
        </p>
      ) : (
        <>
          <div className="service-models-editor__meta">
            <span className="service-models-editor__stats">
              {hasQuery
                ? `匹配 ${filtered.length} / ${models.length}`
                : `${groups.length} 组 · ${models.length} 个模型`}
            </span>
            <div className="service-models-editor__meta-actions">
              {!hasQuery ? (
                <button
                  className="service-models-editor__text-action"
                  onClick={allCollapsed ? expandAll : collapseAll}
                  type="button"
                >
                  {allCollapsed ? "展开全部" : "折叠全部"}
                </button>
              ) : (
                <button
                  className="service-models-editor__text-action service-models-editor__text-action--danger"
                  disabled={filtered.length === 0}
                  onClick={() =>
                    setConfirm({
                      kind: "remove_filtered",
                      models: filtered,
                    })
                  }
                  type="button"
                >
                  删除匹配（{filtered.length}）
                </button>
              )}
              <button
                className="service-models-editor__text-action service-models-editor__text-action--danger"
                onClick={() => setConfirm({ kind: "clear" })}
                type="button"
              >
                清空
              </button>
            </div>
          </div>

          {filtered.length === 0 ? (
            <p className="service-models-editor__filter-empty" role="status">
              没有匹配“{query.trim()}”的模型。
            </p>
          ) : (
            <div className="service-model-list" aria-label="已配置模型">
              {groups.map((group) => {
                const collapsedGroup = isGroupCollapsed(group.key);
                return (
                  <section className="service-model-group" key={group.key}>
                    <div className="service-model-group__header">
                      <button
                        aria-expanded={!collapsedGroup}
                        className="service-model-group__toggle"
                        onClick={() => toggleGroup(group.key)}
                        type="button"
                      >
                        <span
                          aria-hidden="true"
                          className="service-model-group__chevron"
                        >
                          {collapsedGroup ? "▸" : "▾"}
                        </span>
                        <strong>{group.key}</strong>
                        <small className="service-model-group__count">
                          {group.models.length}
                        </small>
                      </button>
                      <button
                        aria-label={`删除分组 ${group.key}`}
                        className="service-model-group__remove"
                        onClick={() =>
                          setConfirm({
                            kind: "remove_group",
                            group: group.key,
                            models: group.models,
                          })
                        }
                        type="button"
                      >
                        −
                      </button>
                    </div>
                    {collapsedGroup ? null : (
                      <div className="service-model-group__body">
                        {group.models.map((model) => {
                          const label = encodeModelEditorValue(model);
                          return (
                            <div className="service-model-chip" key={model}>
                              <code title={label}>{label}</code>
                              <button
                                aria-label={`删除 ${label}`}
                                className="service-model-chip__remove"
                                onClick={() => onRemoveModels([model])}
                                type="button"
                              >
                                ×
                              </button>
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

      {confirm ? (
        <div className="token-dialog-backdrop" role="presentation">
          <section
            aria-labelledby="service-models-confirm-title"
            aria-modal="true"
            className="token-dialog"
            role="dialog"
          >
            <h3 id="service-models-confirm-title">
              {confirm.kind === "clear"
                ? "清空支持模型？"
                : confirm.kind === "remove_group"
                  ? `删除分组“${confirm.group}”？`
                  : "删除匹配的模型？"}
            </h3>
            <p>
              {confirm.kind === "clear"
                ? `将移除全部 ${models.length} 个模型；空清单时该服务不会参与推理路由。`
                : `将从白名单移除 ${confirm.models.length} 个模型。保存前可继续编辑。`}
            </p>
            <div className="token-dialog__actions">
              <button
                className="btn-secondary"
                onClick={() => setConfirm(null)}
                type="button"
              >
                取消
              </button>
              <button
                className="btn-primary"
                onClick={applyConfirm}
                type="button"
              >
                确认删除
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </fieldset>
  );
}
