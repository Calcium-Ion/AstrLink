import { useEffect, useMemo, useRef, useState } from "react";

import { Brain, Boxes, Flask } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ClassifierDryRunPanel } from "./ClassifierDryRunPanel";
import { ClassifierSettingsPanel } from "./ClassifierSettingsPanel";
import type { RoutingSettings } from "./failure-policy-model";
import { useT } from "./i18n";
import { ModelRoutingGroupsPanel } from "./ModelRoutingGroupsPanel";
import { PageHeader } from "./PageHeader";
import type { RoutableService } from "./service-model";
import type { ConversionEngineCapability } from "./core-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

export type IntelligentRoutingView = "groups" | "classifier" | "dryRun";

/** Tabs that hold a draft; the dry run keeps no unsaved state. */
type DraftTab = "groups" | "classifier";

export function IntelligentRouting({
  initialView = "groups",
  initialEditGroup,
  services,
  protocols = [],
  ready,
  onDirtyChange,
  conversionEngine,
  onOpenService,
  onServicesChanged,
}: {
  initialView?: IntelligentRoutingView;
  /** A group whose editor opens right away. */
  initialEditGroup?: string;
  services: readonly RoutableService[];
  /** core capabilities' protocol IDs — the value domain of the `protocol` rule field. */
  protocols?: readonly string[];
  ready: boolean;
  onDirtyChange: (dirty: boolean) => void;
  conversionEngine?: ConversionEngineCapability | null;
  /** Opens a provider's entry protocols. */
  onOpenService?: (serviceId: string) => void;
  /** A provider changed from here; reload the list. */
  onServicesChanged?: () => void;
}) {
  const t = useT();
  const [view, setView] = useState<IntelligentRoutingView>(initialView);
  // The tabs row's right side; the active tab renders its own actions there so
  // navigation and the tab's toolbar share one row.
  const [actionsSlot, setActionsSlot] = useState<HTMLDivElement | null>(null);
  const [editing, setEditing] = useState(false);
  const [dryRunGroup, setDryRunGroup] = useState("");
  const [unsavedGroup, setUnsavedGroup] = useState<string | null>(null);
  const [editRequest, setEditRequest] = useState<{
    id: string;
    seq: number;
  } | null>(() =>
    initialEditGroup ? { id: initialEditGroup, seq: Date.now() } : null,
  );
  const [settings] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  const groupCount = settings?.model_routing_groups?.length;

  // One dirty slot per draft-holding tab, so one tab unmounting or saving can
  // never clear another tab's unsaved edit.
  const [tabDirty, setTabDirty] = useState<Record<DraftTab, boolean>>({
    groups: false,
    classifier: false,
  });
  const dirty = tabDirty.groups || tabDirty.classifier;
  const setTabDirtyChange = useMemo(
    () =>
      Object.fromEntries(
        (["groups", "classifier"] as const).map((key) => [
          key,
          (next: boolean) =>
            setTabDirty((current) =>
              current[key] === next ? current : { ...current, [key]: next },
            ),
        ]),
      ) as Record<DraftTab, (next: boolean) => void>,
    [],
  );

  // Only the aggregate leaves this component, and only when it changes.
  const reportedDirty = useRef(false);
  useEffect(() => {
    if (reportedDirty.current === dirty) return;
    reportedDirty.current = dirty;
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);

  // The group editor is a page of its own: it brings its own header with a
  // way back, so the tab row steps aside while it is showing.
  const showEditor = editing && view === "groups";

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-clip"
      data-testid="intelligent-routing-panel"
    >
      {showEditor ? null : (
        <PageHeader className="mb-0" title={t("intelligentRouting.title")} />
      )}
      <Tabs
        className="min-h-0 min-w-0 flex-1 gap-3 overflow-y-clip"
        onValueChange={(value) => setView(value as IntelligentRoutingView)}
        value={view}
      >
        <div
          className="flex min-w-0 shrink-0 items-center justify-between gap-3"
          hidden={showEditor}
        >
          <TabsList
            scrollable
            aria-label={t("intelligentRouting.workspace")}
            className="min-w-0 shrink-0"
          >
            <TabsTrigger value="groups">
              <Boxes aria-hidden="true" />
              {t("intelligentRouting.tabs.groups")}
              {groupCount ? (
                <Badge className="tabular-nums" variant="secondary">
                  {groupCount}
                </Badge>
              ) : null}
            </TabsTrigger>
            <TabsTrigger value="classifier">
              <Brain aria-hidden="true" />
              {t("intelligentRouting.tabs.classifier")}
            </TabsTrigger>
            <TabsTrigger value="dryRun">
              <Flask aria-hidden="true" />
              {t("intelligentRouting.tabs.dryRun")}
            </TabsTrigger>
          </TabsList>
          <div
            ref={setActionsSlot}
            className="flex min-w-0 items-center justify-end gap-2"
            data-intelligent-routing-actions
          />
        </div>
        {/* Every tab outlives a switch: the editor and the classifier hold
            drafts, and the dry run keeps its last result. Each one scrolls
            inside itself, so the tab bodies do not. */}
        <TabsContent
          forceMount
          hidden={view !== "groups"}
          value="groups"
          className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-clip"
        >
          <ModelRoutingGroupsPanel
            ready={ready}
            services={services}
            protocols={protocols}
            onDirtyChange={setTabDirtyChange.groups}
            active={view === "groups"}
            actionsSlot={actionsSlot}
            editRequest={editRequest}
            onEditingChange={setEditing}
            onUnsavedChange={setUnsavedGroup}
            onGroupIDChange={(from, to) =>
              // The dry run keeps naming the group it was opened on.
              setDryRunGroup((current) =>
                current === from ? (to ?? "") : current,
              )
            }
            onDryRun={(id) => {
              setDryRunGroup(id);
              setView("dryRun");
            }}
            onOpenClassifier={() => setView("classifier")}
            conversionEngine={conversionEngine}
            onOpenService={onOpenService}
            onServicesChanged={onServicesChanged}
          />
        </TabsContent>
        <TabsContent
          forceMount
          hidden={view !== "classifier"}
          value="classifier"
          className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-clip"
        >
          <ClassifierSettingsPanel
            ready={ready}
            services={services}
            onDirtyChange={setTabDirtyChange.classifier}
            active={view === "classifier"}
            actionsSlot={actionsSlot}
          />
        </TabsContent>
        <TabsContent
          forceMount
          hidden={view !== "dryRun"}
          value="dryRun"
          className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-clip"
        >
          <ClassifierDryRunPanel
            ready={ready}
            protocols={protocols}
            services={services}
            group={dryRunGroup}
            onGroupChange={setDryRunGroup}
            onEditGroup={(id) => {
              setEditRequest({ id, seq: Date.now() });
              setView("groups");
            }}
            unsavedGroups={unsavedGroup ? [unsavedGroup] : []}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}
