import { ModelSelect } from "./components/ModelSelect";
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { ChevronDown, Plus, X } from "@/components/icons";
import { useT } from "./i18n";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataRow } from "@/components/DataRow";
import { FormMessage } from "@/components/FormMessage";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import { Panel } from "@/components/Panel";
import { StatusBadge } from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

import { encodeModelEditorValue } from "./model-editor";
import {
  filterModels,
  groupModelsByCategory,
  type ModelCategory,
  type ModelGroup,
} from "./model-groups";
import {
  modelFormatLabels,
  type ModelFormat,
  type ModelProtocol,
} from "./service-model";

/** Model IDs returned by the latest successful upstream fetch. */
export type UpstreamModelSnapshot = {
  models: ReadonlySet<string>;
  /** Some discovery protocols failed, so an absent model may still exist. */
  partial: boolean;
};

export type ServiceModelsEditorProps = {
  /** Use one toolbar when the surrounding tab already names the editor. */
  compact?: boolean;
  models: string[];
  modelEditor: string;
  probingModels: boolean;
  upstream?: UpstreamModelSnapshot | null;
  /** Formats this provider can pin a model to. Fewer than two hide the choice. */
  formats: readonly ModelFormat[];
  /** Models pinned to a format. Every other model is automatic. */
  modelProtocols: readonly ModelProtocol[];
  /** The format an automatic model uses, for kinds that have a table. */
  builtinProtocol: (model: string) => ModelFormat | undefined;
  onModelEditorChange: (value: string) => void;
  onAddModels: () => void;
  onDiscoverModels?: () => void;
  onRemoveModels: (models: string[]) => void;
  onClearModels: () => void;
  /** Pins the models to protocol, or makes them automatic with null. */
  onModelProtocolsChange: (
    models: string[],
    protocol: ModelFormat | null,
  ) => void;
};

type ModelsConfirm =
  | { kind: "clear" }
  | { kind: "remove_filtered"; models: string[] }
  | { kind: "remove_missing"; models: string[] }
  | { kind: "remove_category"; category: string; models: string[] }
  | { kind: "remove_group"; group: string; models: string[] }
  | { kind: "remove_selected"; models: string[] }
  | null;

/** What every model row shares: its format choice and selection state. */
type ModelRowControls = {
  builtinProtocol: (model: string) => ModelFormat | undefined;
  /** The checked models in selection mode, or null outside it. */
  checked: ReadonlySet<string> | null;
  formats: readonly ModelFormat[];
  missing: ReadonlySet<string>;
  onCheckedChange: (model: string, checked: boolean) => void;
  onProtocolChange: (model: string, protocol: ModelFormat | null) => void;
  onRemove: (model: string) => void;
  protocols: ReadonlyMap<string, ModelFormat>;
};

/** The menu value of a model without a format of its own, apart from every protocol ID. */
const DEFAULT_FORMAT = "default";

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

function countMissing(
  models: readonly string[],
  missing: ReadonlySet<string>,
): number {
  return models.reduce(
    (count, model) => count + (missing.has(model) ? 1 : 0),
    0,
  );
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
  missingCount,
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
  missingCount: number;
  onRemove: () => void;
  onToggle: () => void;
  removeLabel: string;
  testId: "service-model-category-toggle" | "service-model-group-toggle";
  title: string;
  tone: "card" | "section";
}) {
  const t = useT();
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
        {missingCount > 0 ? (
          <StatusBadge className="py-0" tone="pending">
            {t("models.missingCount", { count: missingCount })}
          </StatusBadge>
        ) : null}
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

/**
 * The format choices for one row, or for the checked models when value is
 * absent. The default choice names what the model falls back to.
 */
function ModelFormatMenuItems({
  defaultLabel,
  formats,
  onChange,
  value,
}: {
  defaultLabel: string;
  formats: readonly ModelFormat[];
  onChange: (protocol: ModelFormat | null) => void;
  value?: ModelFormat | typeof DEFAULT_FORMAT;
}) {
  const t = useT();
  const choose = (next: string) =>
    onChange(next === DEFAULT_FORMAT ? null : (next as ModelFormat));
  const choices = [
    { value: DEFAULT_FORMAT, label: defaultLabel },
    ...formats.map((format) => ({
      value: format,
      label: modelFormatLabels[format],
    })),
  ];
  return (
    <>
      <DropdownMenuLabel className="grid max-w-64 gap-0.5">
        <span>{t("models.format")}</span>
        <span className="text-xs font-normal text-muted-foreground">
          {t("models.formatHint")}
        </span>
      </DropdownMenuLabel>
      <DropdownMenuSeparator />
      {value === undefined ? (
        choices.map((choice) => (
          <DropdownMenuItem
            key={choice.value}
            onSelect={() => choose(choice.value)}
          >
            {choice.label}
          </DropdownMenuItem>
        ))
      ) : (
        <DropdownMenuRadioGroup onValueChange={choose} value={value}>
          {choices.map((choice) => (
            <DropdownMenuRadioItem key={choice.value} value={choice.value}>
              {choice.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      )}
    </>
  );
}

/** The row action that pins one model to a format, shown beside delete. */
function ModelFormatMenu({
  builtin,
  formats,
  label,
  onChange,
  value,
}: {
  builtin: ModelFormat | undefined;
  formats: readonly ModelFormat[];
  label: string;
  onChange: (protocol: ModelFormat | null) => void;
  value: ModelFormat | undefined;
}) {
  const t = useT();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          aria-label={t("models.formatFor", { label })}
          className="h-6 shrink-0 px-1.5 text-xs text-muted-foreground opacity-0 group-focus-within/row:opacity-100 group-hover/row:opacity-100 data-[state=open]:opacity-100"
          size="xs"
          type="button"
          variant="ghost"
        >
          {t("models.setFormat")}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <ModelFormatMenuItems
          defaultLabel={
            builtin
              ? t("models.formatDefaultBuiltin", {
                  format: modelFormatLabels[builtin],
                })
              : t("models.formatFollowEntry")
          }
          formats={formats}
          onChange={onChange}
          value={value ?? DEFAULT_FORMAT}
        />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ModelRow({
  controls,
  model,
}: {
  controls: ModelRowControls;
  model: string;
}) {
  const t = useT();
  const checkboxID = useId();
  const label = encodeModelEditorValue(model);
  const format = controls.protocols.get(model);
  const name = (
    <span className="flex min-w-0 flex-1 items-center gap-2">
      <code
        className="min-w-0 overflow-hidden text-sm text-ellipsis whitespace-nowrap"
        title={label}
      >
        {label}
      </code>
      {format ? (
        <Badge
          className="shrink-0 px-1.5 py-0 text-micro font-normal"
          data-testid="service-model-format"
          title={t("models.formatTagTitle", {
            format: modelFormatLabels[format],
          })}
          variant="secondary"
        >
          {t("models.formatTag", { format: modelFormatLabels[format] })}
        </Badge>
      ) : null}
    </span>
  );
  return (
    <DataRow
      className={cn("group/row py-2", LIST_GAP, LIST_INSET)}
      data-testid="service-model-row"
    >
      {/* In selection mode the brand mark's cell holds the checkbox, so nothing moves. */}
      <ModelListLead>
        {controls.checked ? (
          <Checkbox
            aria-label={t("models.selectModel", { label })}
            checked={controls.checked.has(model)}
            id={checkboxID}
            onCheckedChange={(checked) =>
              controls.onCheckedChange(model, checked === true)
            }
          />
        ) : (
          <ModelBrandIcon model={model} size={16} />
        )}
      </ModelListLead>
      {controls.checked ? (
        <Label
          className="min-w-0 flex-1 cursor-pointer font-normal"
          htmlFor={checkboxID}
        >
          {name}
        </Label>
      ) : (
        name
      )}
      {controls.missing.has(model) ? (
        <StatusBadge tone="pending">{t("models.missingUpstream")}</StatusBadge>
      ) : null}
      {controls.formats.length > 1 ? (
        <ModelFormatMenu
          builtin={controls.builtinProtocol(model)}
          formats={controls.formats}
          label={label}
          onChange={(protocol) => controls.onProtocolChange(model, protocol)}
          value={format}
        />
      ) : null}
      <Button
        aria-label={t("models.deleteNamed", { label })}
        className="size-6 shrink-0 rounded text-sm text-danger-foreground opacity-0 hover:bg-danger-wash group-hover/row:opacity-100 group-focus-within/row:opacity-100"
        onClick={() => controls.onRemove(model)}
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
  controls,
  group,
  nested = false,
  onRemove,
  onToggle,
}: {
  collapsed: boolean;
  controls: ModelRowControls;
  group: ModelGroup;
  nested?: boolean;
  onRemove: () => void;
  onToggle: () => void;
}) {
  const t = useT();
  const body = (
    <>
      <ModelListHeader
        brandModel={nested ? undefined : group.models[0]}
        collapsed={collapsed}
        countLabel={`${group.models.length}`}
        missingCount={countMissing(group.models, controls.missing)}
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
            <ModelRow controls={controls} key={model} model={model} />
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
  compact = false,
  models,
  modelEditor,
  probingModels,
  upstream,
  formats,
  modelProtocols,
  builtinProtocol,
  onModelEditorChange,
  onAddModels,
  onDiscoverModels,
  onRemoveModels,
  onClearModels,
  onModelProtocolsChange,
}: ServiceModelsEditorProps) {
  const t = useT();
  const [query, setQuery] = useState("");
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [confirm, setConfirm] = useState<ModelsConfirm>(null);
  const [adding, setAdding] = useState(false);
  const [bulkPaste, setBulkPaste] = useState(false);
  // Selection mode, started from the select button, makes each row's mark a checkbox.
  const [selecting, setSelecting] = useState(false);
  const [checked, setChecked] = useState<ReadonlySet<string>>(() => new Set());
  const addInputRef = useRef<HTMLInputElement | null>(null);
  const seededCollapse = useRef(false);
  const selectButtonRef = useRef<HTMLButtonElement | null>(null);
  const moreButtonRef = useRef<HTMLButtonElement | null>(null);
  const selectAllRef = useRef<HTMLButtonElement | null>(null);
  const cancelSelectionRef = useRef<HTMLButtonElement | null>(null);
  // Set when the user enters or leaves selection mode, whose controls
  // replace the focused one.
  const moveFocusRef = useRef(false);

  const catalog = useMemo(() => [...models].sort(), [models]);
  const missing = useMemo(
    () =>
      upstream ? catalog.filter((model) => !upstream.models.has(model)) : [],
    [catalog, upstream],
  );
  const missingSet = useMemo(() => new Set(missing), [missing]);
  const protocolByModel = useMemo(
    () => new Map(modelProtocols.map((entry) => [entry.model, entry.protocol])),
    [modelProtocols],
  );

  // An emptied list has no row to hold the selection controls.
  if (selecting && catalog.length === 0) {
    setSelecting(false);
    setChecked(new Set());
  }

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

  useEffect(() => {
    if (!moveFocusRef.current) return;
    moveFocusRef.current = false;
    const target = (
      selecting
        ? [selectAllRef.current, cancelSelectionRef.current]
        : [selectButtonRef.current, moreButtonRef.current]
    ).find((element) => element?.isConnected && !element.disabled);
    target?.focus({ preventScroll: true });
  }, [selecting]);

  const filtered = useMemo(
    () => filterModels(catalog, query),
    [catalog, query],
  );
  // A bulk action takes the checked models the search shows, so it acts on
  // what the user sees. Models in collapsed groups count too.
  const checkedModels = useMemo(
    () => (selecting ? filtered.filter((model) => checked.has(model)) : []),
    [checked, filtered, selecting],
  );
  const allChecked =
    filtered.length > 0 && checkedModels.length === filtered.length;
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

  const startSelection = () => {
    setChecked(new Set());
    setSelecting(true);
    moveFocusRef.current = true;
  };

  const exitSelection = () => {
    setSelecting(false);
    setChecked(new Set());
    moveFocusRef.current = true;
  };

  const toggleChecked = (model: string, next: boolean) => {
    setChecked((current) => {
      const updated = new Set(current);
      if (next) updated.add(model);
      else updated.delete(model);
      return updated;
    });
  };

  const toggleAllChecked = () => {
    setChecked(allChecked ? new Set() : new Set(filtered));
  };

  const applyFormat = (protocol: ModelFormat | null) => {
    if (checkedModels.length === 0) return;
    onModelProtocolsChange(checkedModels, protocol);
    exitSelection();
  };

  const rowControls: ModelRowControls = {
    builtinProtocol,
    checked: selecting ? checked : null,
    formats,
    missing: missingSet,
    onCheckedChange: toggleChecked,
    onProtocolChange: (model, protocol) =>
      onModelProtocolsChange([model], protocol),
    onRemove: (model) => onRemoveModels([model]),
    protocols: protocolByModel,
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
      if (confirm.kind === "remove_selected") exitSelection();
    }
    setConfirm(null);
  };

  const submitAdd = () => {
    onAddModels();
  };

  return (
    <fieldset
      aria-labelledby="service-models-editor-heading"
      className={cn(
        "min-w-0 border-0 bg-transparent px-0 pb-0.5",
        compact
          ? "grid grid-cols-[minmax(0,1fr)_auto] items-start gap-2"
          : "border-t pt-3",
      )}
    >
      <div
        className={
          compact ? "sr-only" : "flex items-start justify-between gap-3"
        }
      >
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

      <div
        className={cn(
          "flex min-w-0 flex-wrap items-center gap-2",
          compact ? "col-start-1 row-start-1" : "mt-2.5",
          compact && (selecting || catalog.length === 0) && "col-span-2",
          compact && selecting && "invisible",
        )}
      >
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
        <div className="col-span-full mt-2 grid gap-2 rounded-md border bg-muted p-2.5">
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
          className="col-span-full mt-2.5 rounded-md border border-dashed bg-muted/70 p-3 text-center text-xs text-muted-foreground"
          role="status"
        >
          {t("models.empty")}
        </p>
      ) : (
        <>
          {missing.length > 0 ? (
            <FormMessage
              className="col-span-full mt-2 flex flex-wrap items-center gap-x-3 gap-y-1"
              data-testid="service-models-missing"
              tone="warning"
            >
              <span className="min-w-0 flex-[1_1_240px]">
                {t(
                  upstream?.partial
                    ? "models.missingNoticePartial"
                    : "models.missingNotice",
                  { count: missing.length },
                )}
              </span>
              <Button
                className="h-auto shrink-0 px-0 text-xs text-danger-foreground"
                onClick={() =>
                  setConfirm({ kind: "remove_missing", models: missing })
                }
                type="button"
                variant="link"
              >
                {t("models.deleteMissing", { count: missing.length })}
              </Button>
            </FormMessage>
          ) : null}
          {/* Selection covers this row without changing its size, so the list below stays put. */}
          <div
            className={cn(
              "relative flex items-center justify-between gap-2.5",
              compact ? "row-start-1 min-h-8" : "my-1.5 mt-2 min-h-4",
              compact && (selecting ? "col-span-2 col-start-1" : "col-start-2"),
            )}
          >
            <span
              className={cn(
                "text-xs font-semibold text-muted-foreground tabular-nums",
                selecting && "invisible",
                compact && "sr-only",
              )}
            >
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
            <div
              className={cn(
                "flex flex-wrap items-center justify-end gap-2.5",
                selecting && "invisible",
              )}
              data-testid="service-models-actions"
            >
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
              <span className="inline-flex items-center gap-0.5">
                <Button
                  ref={selectButtonRef}
                  className="h-auto px-0 text-xs"
                  disabled={filtered.length === 0}
                  onClick={startSelection}
                  type="button"
                  variant="link"
                >
                  {t("models.select")}
                </Button>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      ref={moreButtonRef}
                      aria-label={t("models.moreActions")}
                      className="h-auto px-0 text-muted-foreground hover:text-foreground has-[>svg]:px-0"
                      type="button"
                      variant="link"
                    >
                      <ChevronDown aria-hidden="true" className="size-3.5" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem
                      onSelect={() => setConfirm({ kind: "clear" })}
                      variant="destructive"
                    >
                      {t("models.clear")}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </span>
            </div>
            {selecting ? (
              <div
                className="absolute inset-0 flex items-center justify-between gap-2.5 whitespace-nowrap"
                data-testid="service-models-selection"
              >
                <span
                  className="min-w-0 truncate text-xs font-semibold text-muted-foreground tabular-nums"
                  role="status"
                >
                  {t("models.selectedCount", { count: checkedModels.length })}
                </span>
                <div className="flex shrink-0 items-center gap-2.5">
                  <Label className="inline-flex cursor-pointer gap-1.5 font-normal text-text-secondary">
                    <Checkbox
                      ref={selectAllRef}
                      checked={allChecked}
                      disabled={filtered.length === 0}
                      onCheckedChange={toggleAllChecked}
                    />
                    {t("services.selectAll")}
                  </Label>
                  <Button
                    ref={cancelSelectionRef}
                    className="h-auto px-0 text-xs"
                    onClick={exitSelection}
                    type="button"
                    variant="link"
                  >
                    {t("common.cancel")}
                  </Button>
                  {formats.length > 1 ? (
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          className="h-auto gap-0.5 px-0 text-xs has-[>svg]:px-0"
                          disabled={checkedModels.length === 0}
                          type="button"
                          variant="link"
                        >
                          {t("models.setFormat")}
                          <ChevronDown
                            aria-hidden="true"
                            className="size-3.5"
                          />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <ModelFormatMenuItems
                          defaultLabel={
                            checkedModels.some((model) =>
                              builtinProtocol(model),
                            )
                              ? t("models.formatResetBuiltin")
                              : t("models.formatFollowEntry")
                          }
                          formats={formats}
                          onChange={applyFormat}
                        />
                      </DropdownMenuContent>
                    </DropdownMenu>
                  ) : null}
                  <Button
                    className="h-auto px-0 text-xs text-danger-foreground"
                    disabled={checkedModels.length === 0}
                    onClick={() =>
                      setConfirm({
                        kind: "remove_selected",
                        models: checkedModels,
                      })
                    }
                    type="button"
                    variant="link"
                  >
                    {t("common.delete")}
                  </Button>
                </div>
              </div>
            ) : null}
          </div>

          {filtered.length === 0 ? (
            <p
              className="col-span-full mt-1.5 rounded-md border border-dashed p-2.5 text-xs text-muted-foreground"
              role="status"
            >
              {t("models.noMatch", { query: query.trim() })}
            </p>
          ) : (
            <div
              className="col-span-full grid gap-2"
              aria-label={t("models.configured")}
            >
              {categories.map((category) => {
                if (category.groups.length === 1) {
                  const group = category.groups[0]!;
                  return (
                    <ModelGroupSection
                      key={group.key}
                      collapsed={isGroupCollapsed(groupCollapseKey(group.key))}
                      controls={rowControls}
                      group={group}
                      onRemove={() =>
                        setConfirm({
                          kind: "remove_group",
                          group: group.key,
                          models: group.models,
                        })
                      }
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
                        missingCount={countMissing(models, missingSet)}
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
                              controls={rowControls}
                              group={group}
                              nested
                              onRemove={() =>
                                setConfirm({
                                  kind: "remove_group",
                                  group: group.key,
                                  models: group.models,
                                })
                              }
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
                : confirm?.kind === "remove_missing"
                  ? t("models.deleteMissingTitle")
                  : confirm?.kind === "remove_selected"
                    ? t("models.deleteSelectedTitle")
                    : t("models.deleteMatchesTitle")
        }
      />
    </fieldset>
  );
}
