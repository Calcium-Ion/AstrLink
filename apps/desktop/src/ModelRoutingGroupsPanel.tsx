import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { ConfirmDialog } from "./components/ConfirmDialog";
import { FormMessage } from "./components/FormMessage";
import { Plus, Search, X } from "./components/icons";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  getClassifierSettings,
  getRoutingSettings,
  updateRoutingSettings,
} from "./bridge";
import type {
  ClassifierDimension,
  ClassifierSettings,
} from "./classifier-settings-model";
import type { RoutingSettings } from "./failure-policy-model";
import { useT } from "./i18n";
import { intelligentRoutingErrorText } from "./intelligent-routing-errors";
import { ModelRoutingCreateDialog } from "./ModelRoutingCreateDialog";
import { ModelRoutingGroupEditor } from "./ModelRoutingGroupEditor";
import { ModelRoutingGroupList } from "./ModelRoutingGroupList";
import {
  newRoutingGroup,
  pruneRoutingMembers,
  type RoutingTarget,
} from "./model-routing-editor-model";
import {
  modelRoutingPublicModelID,
  type ModelRoutingGroup,
} from "./model-routing-group-model";
import type { RoutableService } from "./service-model";
import type { ConversionEngineCapability } from "./core-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

interface EditingGroup {
  /** The name the group is saved under; the draft may rename it. */
  savedID: string;
  draft: ModelRoutingGroup;
  baseline: string;
}

/** The label list the classifier tab writes to every group, if one is set. */
function sharedTaskTypeLabels(
  groups: readonly ModelRoutingGroup[],
): readonly string[] | undefined {
  return groups.find((group) => (group.task_type_labels?.length ?? 0) > 0)
    ?.task_type_labels;
}

/**
 * 「路由组」页签：列表和单个组的编辑页。
 *
 * 每次写入都先读最新的路由设置，只替换这一次改动的组：分类器页会改写各组的
 * 任务类型标签，拿着打开页面时的旧副本整份写回，会把那边刚保存的标签冲掉。
 */
export function ModelRoutingGroupsPanel({
  ready,
  services,
  protocols = [],
  onDirtyChange,
  active = true,
  actionsSlot = null,
  editRequest = null,
  onEditingChange,
  onUnsavedChange,
  onGroupIDChange,
  onDryRun,
  onOpenClassifier,
  conversionEngine,
  onOpenService,
  onServicesChanged,
}: {
  ready: boolean;
  services: readonly RoutableService[];
  /** core capabilities' protocol IDs: the value domain of the protocol field. */
  protocols?: readonly string[];
  onDirtyChange: (dirty: boolean) => void;
  /** Whether this tab is showing; only then do its actions fill the slot. */
  active?: boolean;
  /** The tabs row's action area. Without it the toolbar renders inline. */
  actionsSlot?: HTMLElement | null;
  /** Another tab asked to edit this group; `seq` makes repeats distinct. */
  editRequest?: { id: string; seq: number } | null;
  onEditingChange?: (editing: boolean) => void;
  /** The group with unsaved edits, so a dry run can warn it uses the saved one. */
  onUnsavedChange?: (groupID: string | null) => void;
  /** A saved group was renamed (`to` is its new ID) or deleted (`to` is null). */
  onGroupIDChange?: (from: string, to: string | null) => void;
  onDryRun?: (groupID: string) => void;
  onOpenClassifier?: () => void;
  conversionEngine?: ConversionEngineCapability | null;
  onOpenService?: (serviceId: string) => void;
  onServicesChanged?: () => void;
}) {
  const t = useT();
  const [settings, setSettings] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  const [classifier, setClassifier] =
    useWorkspaceSnapshot<ClassifierSettings | null>(
      "classifier-settings",
      null,
    );
  const [dimensions, setDimensions] = useWorkspaceSnapshot<
    ClassifierDimension[]
  >("classifier-dimensions", []);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const [query, setQuery] = useState("");
  const [editing, setEditing] = useState<EditingGroup | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [pendingToggle, setPendingToggle] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmLeave, setConfirmLeave] = useState(false);
  const editingRef = useRef(editing);
  editingRef.current = editing;

  const groups = settings?.model_routing_groups ?? [];
  const dirty =
    editing !== null && JSON.stringify(editing.draft) !== editing.baseline;

  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);

  const isEditing = editing !== null;
  useEffect(() => {
    onEditingChange?.(isEditing);
  }, [isEditing, onEditingChange]);

  const unsavedID = dirty ? (editing?.savedID ?? null) : null;
  useEffect(() => {
    onUnsavedChange?.(unsavedID);
  }, [unsavedID, onUnsavedChange]);

  useEffect(() => {
    if (!ready) return;
    let live = true;
    void Promise.all([getRoutingSettings(), getClassifierSettings()])
      .then(([routing, loadedClassifier]) => {
        if (!live) return;
        setSettings(routing);
        setClassifier(loadedClassifier);
        setDimensions(loadedClassifier.dimensions ?? []);
        setLoadError(null);
      })
      .catch((caught: unknown) => {
        if (live)
          setLoadError(
            intelligentRoutingErrorText(caught, t("failure.loadFailed")),
          );
      });
    return () => {
      live = false;
    };
  }, [ready, reload, t, setSettings, setClassifier, setDimensions]);

  const open = (id: string) => {
    const group = groups.find((item) => item.id === id);
    if (!group) return;
    setError(null);
    setEditing({ savedID: id, draft: group, baseline: JSON.stringify(group) });
  };

  // A dry run's 「编辑规则」 lands here. An editor with unsaved work stays put.
  // A request that arrives before the groups have loaded (a link from another
  // page) waits for them instead of being dropped.
  const pendingEdit = useRef<string | null>(null);
  useEffect(() => {
    if (!editRequest) return;
    const current = editingRef.current;
    if (
      current &&
      JSON.stringify(current.draft) !== current.baseline &&
      current.savedID !== editRequest.id
    )
      return;
    if (current?.savedID === editRequest.id) return;
    if (!groups.some((item) => item.id === editRequest.id)) {
      pendingEdit.current = editRequest.id;
      return;
    }
    pendingEdit.current = null;
    open(editRequest.id);
    // Only a new request opens an editor, not a settings reload.
  }, [editRequest]);
  useEffect(() => {
    const id = pendingEdit.current;
    if (id === null || !groups.some((item) => item.id === id)) return;
    pendingEdit.current = null;
    if (!editingRef.current) open(id);
  }, [groups]);

  /** Read-modify-write of the group list; returns what Core saved. */
  const writeGroups = async (
    change: (latest: ModelRoutingGroup[]) => ModelRoutingGroup[],
  ): Promise<ModelRoutingGroup[]> => {
    const latest = await getRoutingSettings();
    const result = await updateRoutingSettings({
      model_routing_groups: change(latest.model_routing_groups ?? []),
    });
    setSettings(result);
    return result.model_routing_groups ?? [];
  };

  const save = () => {
    if (editing === null || !ready || saving || !dirty) return;
    const submitted = editing.draft;
    const savedID = editing.savedID;
    setSaving(true);
    setError(null);
    void writeGroups((latest) => {
      const index = latest.findIndex((group) => group.id === savedID);
      const current = index >= 0 ? latest[index] : undefined;
      // Labels belong to the classifier tab, which may have saved newer ones.
      const { task_type_labels: _drafted, ...rest } = submitted;
      const labels = current ? current.task_type_labels : _drafted;
      const group = pruneRoutingMembers({
        ...rest,
        ...(labels && labels.length > 0 ? { task_type_labels: labels } : {}),
      });
      return index >= 0
        ? latest.map((item, position) => (position === index ? group : item))
        : [...latest, group];
    })
      .then((saved) => {
        const group = saved.find((item) => item.id === submitted.id);
        if (!group) return;
        if (group.id !== savedID) onGroupIDChange?.(savedID, group.id);
        setEditing((current) => {
          if (current === null) return current;
          const baseline = JSON.stringify(group);
          return current.draft === submitted
            ? { savedID: group.id, draft: group, baseline }
            : { ...current, savedID: group.id, baseline };
        });
      })
      .catch((caught: unknown) =>
        setError(
          intelligentRoutingErrorText(
            caught,
            t("routingGroups.editor.saveFailed"),
          ),
        ),
      )
      .finally(() => setSaving(false));
  };

  const toggle = (id: string, enabled: boolean) => {
    if (!ready || pendingToggle !== null) return;
    setPendingToggle(id);
    setListError(null);
    void writeGroups((latest) =>
      latest.map((group) => (group.id === id ? { ...group, enabled } : group)),
    )
      .catch((caught: unknown) =>
        setListError(
          intelligentRoutingErrorText(
            caught,
            t("routingGroups.list.toggleFailed", {
              name: modelRoutingPublicModelID(id),
            }),
          ),
        ),
      )
      .finally(() => setPendingToggle(null));
  };

  const create = (name: string, target: RoutingTarget) => {
    if (!ready || creating) return;
    setCreating(true);
    setCreateError(null);
    void writeGroups((latest) => [
      ...latest,
      newRoutingGroup(name, target, sharedTaskTypeLabels(latest)),
    ])
      .then((saved) => {
        const group = saved.find((item) => item.id === name);
        setCreateOpen(false);
        if (group)
          setEditing({
            savedID: group.id,
            draft: group,
            baseline: JSON.stringify(group),
          });
      })
      .catch((caught: unknown) =>
        setCreateError(
          intelligentRoutingErrorText(caught, t("routingGroups.create.failed")),
        ),
      )
      .finally(() => setCreating(false));
  };

  const remove = () => {
    if (editing === null || !ready) return;
    const savedID = editing.savedID;
    setSaving(true);
    setError(null);
    void writeGroups((latest) => latest.filter((group) => group.id !== savedID))
      .then(() => {
        setConfirmDelete(false);
        setEditing(null);
        onGroupIDChange?.(savedID, null);
      })
      .catch((caught: unknown) => {
        setConfirmDelete(false);
        setError(
          intelligentRoutingErrorText(
            caught,
            t("routingGroups.editor.deleteFailed"),
          ),
        );
      })
      .finally(() => setSaving(false));
  };

  if (editing !== null)
    return (
      <>
        <ModelRoutingGroupEditor
          group={editing.draft}
          savedID={editing.savedID}
          otherIDs={groups
            .map((group) => group.id)
            .filter((id) => id !== editing.savedID)}
          services={services}
          dimensions={dimensions}
          protocols={protocols}
          conversionEngine={conversionEngine}
          onOpenService={onOpenService}
          onServicesChanged={onServicesChanged}
          classifierEnabled={classifier ? classifier.enabled : null}
          ready={ready}
          dirty={dirty}
          saving={saving}
          error={error}
          onChange={(draft) => {
            setError(null);
            setEditing((current) =>
              current ? { ...current, draft } : current,
            );
          }}
          onSave={save}
          onDiscard={() =>
            setEditing((current) =>
              current
                ? { ...current, draft: JSON.parse(current.baseline) }
                : current,
            )
          }
          onBack={() => {
            if (dirty) setConfirmLeave(true);
            else setEditing(null);
          }}
          onDelete={() => setConfirmDelete(true)}
          onDryRun={() => onDryRun?.(editing.savedID)}
          onOpenClassifier={() => onOpenClassifier?.()}
        />
        <ConfirmDialog
          open={confirmDelete}
          destructive
          disabled={saving}
          title={t("routingGroups.editor.deleteTitle")}
          description={t("routingGroups.editor.deleteBody", {
            model: modelRoutingPublicModelID(editing.savedID),
          })}
          confirmLabel={t("common.delete")}
          onCancel={() => setConfirmDelete(false)}
          onConfirm={remove}
        />
        <ConfirmDialog
          open={confirmLeave}
          destructive
          title={t("routingGroups.editor.leaveTitle")}
          description={t("routingGroups.editor.leaveBody")}
          confirmLabel={t("routingGroups.editor.leaveConfirm")}
          onCancel={() => setConfirmLeave(false)}
          onConfirm={() => {
            setConfirmLeave(false);
            setEditing(null);
          }}
        />
      </>
    );

  const toolbar = (
    <>
      <div className="relative w-52 min-w-0">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          type="search"
          className="h-8 pr-8 pl-8 [&::-webkit-search-cancel-button]:appearance-none"
          aria-label={t("routingGroups.list.searchLabel")}
          placeholder={t("routingGroups.list.searchPlaceholder")}
          value={query}
          onChange={(event) => setQuery(event.currentTarget.value)}
        />
        {query ? (
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            className="absolute top-1/2 right-1 -translate-y-1/2 text-muted-foreground"
            aria-label={t("routingGroups.list.clearSearch")}
            onClick={() => setQuery("")}
          >
            <X aria-hidden="true" />
          </Button>
        ) : null}
      </div>
      <Button
        type="button"
        disabled={!ready || settings === null}
        onClick={() => {
          setCreateError(null);
          setCreateOpen(true);
        }}
        data-routing-new-group
      >
        <Plus aria-hidden="true" />
        {t("routingGroups.list.add")}
      </Button>
    </>
  );

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3"
      data-testid="model-routing-groups-panel"
    >
      {actionsSlot ? (
        active ? (
          createPortal(toolbar, actionsSlot)
        ) : null
      ) : (
        <div className="flex min-w-0 shrink-0 items-center justify-end gap-2">
          {toolbar}
        </div>
      )}
      {loadError ? (
        <FormMessage tone="error" className="flex items-center gap-2">
          {loadError}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setReload((value) => value + 1)}
          >
            {t("common.retry")}
          </Button>
        </FormMessage>
      ) : null}
      {listError ? <FormMessage tone="error">{listError}</FormMessage> : null}
      {settings === null ? (
        <p className="text-xs text-muted-foreground">
          {ready ? t("common.loading") : t("services.gatewayNotReady")}
        </p>
      ) : (
        <ModelRoutingGroupList
          groups={groups}
          services={services}
          query={query}
          disabled={!ready}
          pendingToggle={pendingToggle}
          onOpen={open}
          onToggle={toggle}
          onCreate={() => {
            setCreateError(null);
            setCreateOpen(true);
          }}
        />
      )}
      <ModelRoutingCreateDialog
        open={createOpen}
        existing={groups.map((group) => group.id)}
        services={services}
        creating={creating}
        error={createError}
        onCancel={() => setCreateOpen(false)}
        onCreate={create}
      />
    </div>
  );
}
