import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal, flushSync } from "react-dom";

import { ChoiceCard } from "./components/ChoiceCard";
import { ConditionChip } from "./components/ConditionChip";
import { Field } from "./components/Field";
import { FormMessage } from "./components/FormMessage";
import { HelpDisclosure } from "./components/HelpDisclosure";
import { HelpPopover } from "./components/HelpPopover";
import { Panel, PanelHeader } from "./components/Panel";
import { SegmentedControl } from "./components/SegmentedControl";
import {
  ChevronDown,
  MessageSquare,
  Plus,
  SquarePen,
} from "./components/icons";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "./components/ui/dropdown-menu";
import { Input } from "./components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "./components/ui/popover";
import { RadioGroup } from "./components/ui/radio-group";
import { Switch } from "./components/ui/switch";
import { Textarea } from "./components/ui/textarea";
import {
  classifierCredentialAction,
  getClassifierSettings,
  getRoutingSettings,
  updateClassifierSettings,
  updateRoutingSettings,
} from "./bridge";
import {
  classifierDimensionNameMaxRunes,
  classifierExistingModelTimeoutMaxMS,
  classifierExistingModelTimeoutMinMS,
  classifierMaxCustomDimensions,
  classifierPlaceholder,
  classifierPlaceholderParts,
  classifierProtocols,
  classifierTimeoutMaxMS,
  classifierTimeoutMinMS,
  defaultClassifierPromptFor,
  legacyDefaultClassifierPrompts,
  linkClassifierProtocol,
  type ClassifierDimension,
  type ClassifierProtocol,
  type ClassifierSettings,
} from "./classifier-settings-model";
import {
  calculateClassifierDimensionIssues,
  calculateClassifierSettingsIssues,
  type ClassifierDimensionIssue,
  type ClassifierSettingsIssue,
} from "./classifier-settings-validation";
import type { RoutingSettings } from "./failure-policy-model";
import { useT } from "./i18n";
import { intelligentRoutingErrorText } from "./intelligent-routing-errors";
import {
  calculateDimensionUseIssues,
  calculateTaskTypeLabelIssues,
  maxModelRoutingTaskTypeLabelRunes,
  modelRoutingCatalogLabelDomain,
  modelRoutingTaskTypeLabelsDiverge,
  type TaskTypeLabelIssue,
} from "./model-routing-catalog-model";
import {
  maxModelRoutingListItems,
  maxModelRoutingTaskTypeLabels,
} from "./model-routing-group-model";
import { ModelRoutingTargetSelect } from "./ModelRoutingTargetSelect";
import { modelRoutingFieldValueLabel } from "./request-model-routing-model";
import type { RoutableService } from "./service-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

function issueFor(
  issues: readonly ClassifierSettingsIssue[],
  field: ClassifierSettingsIssue["field"],
): ClassifierSettingsIssue | undefined {
  return issues.find((issue) => issue.field === field);
}

function FieldIssue({
  issue,
  namespace = "classifier.fieldErrors",
}: {
  issue: ClassifierSettingsIssue | undefined;
  namespace?: string;
}) {
  const t = useT();
  if (issue === undefined) return null;
  return (
    <span role="alert" className="text-xs text-danger-foreground">
      {t(`${namespace}.${issue.problem}`)}
    </span>
  );
}

/** Locates one problem inside the dimension list, for the inline marker. */
function dimensionIssue(
  issues: readonly ClassifierDimensionIssue[],
  index: number,
  target: ClassifierDimensionIssue["target"],
  optionIndex?: number,
): ClassifierDimensionIssue | undefined {
  return issues.find(
    (issue) =>
      issue.index === index &&
      issue.target === target &&
      issue.optionIndex === optionIndex,
  );
}

function DimensionIssue({
  issue,
}: {
  issue: ClassifierDimensionIssue | undefined;
}) {
  const t = useT();
  if (issue === undefined) return null;
  return (
    <span role="alert" className="text-xs text-danger-foreground">
      {t(`classifierTab.dimensionErrors.${issue.problem}`)}
    </span>
  );
}

/**
 * Inserts a placeholder for one part of a dimension into the prompt. task_type
 * and every custom dimension offer the same parts, so the prompt can embed any
 * of them without the user knowing the token syntax.
 */
function PromptPlaceholderMenu({
  dimensions,
  onInsert,
  onInserted,
}: {
  dimensions: readonly { name: string; title: string }[];
  onInsert: (token: string) => void;
  /** Runs once the menu has closed after an insert, to return to the prompt. */
  onInserted: () => void;
}) {
  const t = useT();
  // The menu hands focus back to its trigger as it closes; after an insert the
  // caret belongs in the prompt instead.
  const inserted = useRef(false);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          data-classifier-insert-placeholder
        >
          {t("classifierTab.insertPlaceholder")}
          <ChevronDown aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        className="w-52"
        onCloseAutoFocus={(event) => {
          if (!inserted.current) return;
          inserted.current = false;
          event.preventDefault();
          onInserted();
        }}
      >
        {dimensions.map((dimension) => (
          <DropdownMenuSub key={dimension.name}>
            <DropdownMenuSubTrigger
              data-classifier-placeholder-dimension={dimension.name}
            >
              <span className="truncate">{dimension.title}</span>
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="w-72">
              {classifierPlaceholderParts.map((part) => {
                const token = classifierPlaceholder(dimension.name, part);
                return (
                  <DropdownMenuItem
                    key={part}
                    data-classifier-placeholder={token}
                    onSelect={() => {
                      inserted.current = true;
                      onInsert(token);
                    }}
                  >
                    <span className="shrink-0">
                      {t(
                        `classifierTab.placeholderParts.${part === "" ? "all" : part}`,
                      )}
                    </span>
                    <code className="ml-auto min-w-0 truncate font-mono text-micro text-muted-foreground">
                      {token}
                    </code>
                  </DropdownMenuItem>
                );
              })}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * An empty stored prompt means "use the built-in one". Show that text instead
 * of a blank box so the user edits a working instruction that writes out every
 * dimension. Both draft and baseline get it, so loading alone never marks the
 * tab dirty; saving it unchanged stores the empty value again. A stored copy
 * of an earlier default also means the default, as in Core.
 */
function withDefaultPrompt(settings: ClassifierSettings): ClassifierSettings {
  const dimensions = settings.dimensions ?? [];
  const withDimensions = { ...settings, dimensions };
  if (
    settings.inline.prompt.trim() !== "" &&
    !legacyDefaultClassifierPrompts.includes(settings.inline.prompt)
  )
    return withDimensions;
  return {
    ...withDimensions,
    inline: {
      ...settings.inline,
      prompt: defaultClassifierPromptFor(dimensions),
    },
  };
}

function groupsOf(settings: RoutingSettings | null) {
  return settings?.model_routing_groups ?? [];
}

/** The label list the tab edits: one shared list, read from the groups. */
function labelDomainOf(settings: RoutingSettings): string[] {
  return [
    ...modelRoutingCatalogLabelDomain(
      groupsOf(settings).map((group) => group.task_type_labels),
    ),
  ];
}

/** Popover footer holding the one destructive action of the popover. */
function PopoverFooter({
  label,
  disabled,
  onClick,
}: {
  label: string;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <div className="-mx-4 -mb-4 flex items-center border-t bg-muted/40 px-2 py-1.5">
      <Button
        type="button"
        size="sm"
        variant="ghost"
        className="text-muted-foreground hover:text-destructive"
        disabled={disabled}
        onClick={onClick}
      >
        {label}
      </Button>
    </div>
  );
}

/**
 * One option of a classification dimension. The chip reads the option name; its
 * popover renames it, edits the description that guides the classifier (custom
 * dimensions only — `task_type_labels` is a bare list) and removes it.
 */
function OptionChip({
  name,
  display,
  description,
  describable,
  invalid,
  problem,
  disabled,
  open,
  onOpenChange,
  onRename,
  onDescribe,
  onRemove,
  "data-option": dataOption,
}: {
  name: string;
  /** What the chip shows; built-in task types read as 编码 rather than coding. */
  display: string;
  description: string;
  describable: boolean;
  invalid: boolean;
  problem?: string;
  disabled: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRename: (name: string) => void;
  onDescribe: (description: string) => void;
  onRemove: () => void;
  "data-option": string;
}) {
  const t = useT();
  const shown = display === "" ? t("classifierTab.unnamedOption") : display;
  return (
    <ConditionChip
      value={shown}
      className={describable ? "font-mono" : undefined}
      icon={
        describable && description.trim() !== "" ? (
          <MessageSquare
            aria-hidden="true"
            className="order-last size-3 text-muted-foreground"
          />
        ) : undefined
      }
      invalid={invalid}
      disabled={disabled}
      open={open}
      onOpenChange={onOpenChange}
      aria-label={t("classifierTab.editOption", { name: shown })}
      data-classifier-option={dataOption}
    >
      <Field
        label={t("classifierTab.option")}
        tooltip={
          !describable && display !== name
            ? t("classifierTab.optionShownAs", { display })
            : undefined
        }
      >
        <Input
          className="h-8 font-mono"
          value={name}
          maxLength={
            describable
              ? classifierDimensionNameMaxRunes
              : maxModelRoutingTaskTypeLabelRunes
          }
          disabled={disabled}
          placeholder={t("classifierTab.optionNamePlaceholder")}
          aria-label={t("classifierTab.option")}
          aria-invalid={invalid || undefined}
          onChange={(event) => onRename(event.target.value)}
        />
      </Field>
      {describable ? (
        <Field
          label={t("classifierTab.optionDescription")}
          tooltip={t("classifierTab.optionDescriptionHint")}
        >
          <Input
            className="h-8"
            value={description}
            disabled={disabled}
            aria-label={t("classifierTab.optionDescription")}
            onChange={(event) => onDescribe(event.target.value)}
          />
        </Field>
      ) : null}
      {problem ? (
        <p role="alert" className="text-xs text-danger-foreground">
          {problem}
        </p>
      ) : null}
      <PopoverFooter
        label={t("classifierTab.removeOption")}
        disabled={disabled}
        onClick={onRemove}
      />
    </ConditionChip>
  );
}

/** The ghost 「+ 选项」 button after a row's chips. */
function AddOptionButton({
  name,
  disabled,
  onClick,
}: {
  name: string;
  disabled: boolean;
  onClick: () => void;
}) {
  const t = useT();
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      className="text-muted-foreground"
      disabled={disabled}
      aria-label={t("classifierTab.addOptionLabel", { name })}
      onClick={onClick}
    >
      <Plus aria-hidden="true" />
      {t("classifierTab.addOption")}
    </Button>
  );
}

const rowGrid =
  "grid min-w-0 grid-cols-[minmax(0,10rem)_minmax(0,1fr)_1.75rem] items-start gap-3 px-4 py-3";

/**
 * The classifier is a singleton resource of its own — not a RoutingSettings
 * field and not a Service. This tab edits all of it in one draft: where the
 * classifier runs, the dimensions it answers, and the advanced knobs. The one
 * exception is the task type option list, which the wire stores on every
 * routing group (`task_type_labels`); the tab edits it as one shared list and
 * writes it to each group on save, so the storage layout never leaks into a
 * second Save button.
 */
export function ClassifierSettingsPanel({
  ready,
  services,
  onDirtyChange,
  active = true,
  actionsSlot = null,
}: {
  ready: boolean;
  services: readonly RoutableService[];
  onDirtyChange: (dirty: boolean) => void;
  /** Whether this tab is the visible one; only then does it fill the slot. */
  active?: boolean;
  /**
   * A node in the page's tab row that hosts this tab's save status and button.
   * Without one the actions render at the top of the panel.
   */
  actionsSlot?: HTMLElement | null;
}) {
  const t = useT();
  const [, setSavedSettings] = useWorkspaceSnapshot<ClassifierSettings | null>(
    "classifier-settings",
    null,
  );
  const [savedDimensions, setSavedDimensions] = useWorkspaceSnapshot<
    ClassifierDimension[]
  >("classifier-dimensions", []);
  const [routing, setRouting] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  const [draft, setDraft] = useState<ClassifierSettings | null>(null);
  const [baseline, setBaseline] = useState("");
  const [labels, setLabels] = useState<string[] | null>(null);
  const [labelBaseline, setLabelBaseline] = useState("");
  const [routingError, setRoutingError] = useState<string | null>(null);
  const [credentialConfigured, setCredentialConfigured] = useState(false);
  const [credentialDraft, setCredentialDraft] = useState("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [showIssues, setShowIssues] = useState(false);
  const [reload, setReload] = useState(0);
  // One popover at a time: `dim:<i>`, `opt:<i>:<j>` or `label:<j>`.
  const [openPopover, setOpenPopover] = useState<string | null>(null);
  const credentialDraftRef = useRef(credentialDraft);
  credentialDraftRef.current = credentialDraft;
  const mutationVersion = useRef(0);
  const promptField = useRef<HTMLTextAreaElement>(null);
  // Where the caret last was in the prompt, so a placeholder picked from the
  // menu lands there; null appends it.
  const promptCaret = useRef<{ start: number; end: number } | null>(null);
  const settingsDirty = draft !== null && JSON.stringify(draft) !== baseline;
  const labelsDirty =
    labels !== null && JSON.stringify(labels) !== labelBaseline;
  const dirty = settingsDirty || labelsDirty || credentialDraft.trim() !== "";
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;

  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);

  useEffect(() => {
    if (!ready) return;
    let active = true;
    const version = mutationVersion.current;
    void Promise.all([
      getClassifierSettings(),
      classifierCredentialAction("status"),
    ])
      .then(([loaded, credential]) => {
        if (!active || mutationVersion.current !== version) return;
        setCredentialConfigured(credential.configured);
        setSavedSettings(loaded);
        setSavedDimensions(loaded.dimensions ?? []);
        // A reconnection must never replace an unsaved draft.
        if (!dirtyRef.current) {
          const shown = withDefaultPrompt(loaded);
          setDraft(shown);
          setBaseline(JSON.stringify(shown));
        }
        setLoadError(null);
      })
      .catch((caught) => {
        if (active && mutationVersion.current === version)
          setLoadError(
            intelligentRoutingErrorText(caught, t("classifier.loadFailed")),
          );
      });
    // The task type options live on the routing groups. A failure there only
    // locks that one row: the rest of the classifier stays editable.
    void getRoutingSettings()
      .then((loaded) => {
        if (!active || mutationVersion.current !== version) return;
        setRouting(loaded);
        if (!dirtyRef.current) {
          const domain = labelDomainOf(loaded);
          setLabels(domain);
          setLabelBaseline(JSON.stringify(domain));
        }
        setRoutingError(null);
      })
      .catch((caught) => {
        if (active && mutationVersion.current === version)
          setRoutingError(
            intelligentRoutingErrorText(
              caught,
              t("classifierTab.routingLoadFailed"),
            ),
          );
      });
    return () => {
      active = false;
    };
  }, [ready, reload, t, setSavedSettings, setSavedDimensions, setRouting]);

  const groups = groupsOf(routing);
  const settingsIssues =
    draft === null
      ? []
      : calculateClassifierSettingsIssues(draft, {
          credentialConfigured,
          credentialDraft,
        });
  const dimensions = draft?.dimensions ?? [];
  const dimensionIssues = calculateClassifierDimensionIssues(dimensions);
  // A dimension a rule still names cannot go: Core refuses the save, and the
  // message names the group so the rule can be found.
  const dimensionUseIssues =
    draft === null
      ? []
      : calculateDimensionUseIssues(savedDimensions, dimensions, groups);
  const labelIssues =
    labels === null ? [] : calculateTaskTypeLabelIssues(labels, groups);
  const blocking = showIssues ? settingsIssues : [];
  const blockingDimensions = showIssues ? dimensionIssues : [];
  const shownLabelIssues = labelsDirty || showIssues ? labelIssues : [];
  const shownDimensionUseIssues =
    settingsDirty || showIssues ? dimensionUseIssues : [];
  const usesExistingModel = draft?.classifier_source === "existing_model";

  const patch = (next: Partial<ClassifierSettings>): void => {
    setShowIssues(false);
    setError(null);
    setDraft((current) =>
      current === null ? current : { ...current, ...next },
    );
  };

  const patchDimensions = (
    update: (dimensions: ClassifierDimension[]) => ClassifierDimension[],
  ): void => {
    setShowIssues(false);
    setError(null);
    setDraft((current) => {
      if (current === null) return current;
      const dimensions = update(current.dimensions ?? []);
      // The default prompt writes out every dimension, so it follows them; a
      // prompt the user wrote stays as written.
      const prompt =
        current.inline.prompt ===
        defaultClassifierPromptFor(current.dimensions ?? [])
          ? defaultClassifierPromptFor(dimensions)
          : current.inline.prompt;
      return { ...current, dimensions, inline: { ...current.inline, prompt } };
    });
  };

  const patchLabels = (next: string[]): void => {
    setShowIssues(false);
    setError(null);
    setLabels(next);
  };

  const save = (): void => {
    if (draft === null || !ready || saving || !dirty) return;
    const pendingLabelIssues = labelsDirty ? labelIssues : [];
    if (
      settingsIssues.length > 0 ||
      dimensionIssues.length > 0 ||
      dimensionUseIssues.length > 0 ||
      pendingLabelIssues.length > 0
    ) {
      setShowIssues(true);
      return;
    }
    const submitted = draft;
    const submittedKey = credentialDraft;
    const submittedLabels = labels ?? [];
    const writeSettings = settingsDirty;
    const writeLabels = labelsDirty;
    setError(null);
    mutationVersion.current += 1;
    setSaving(true);
    void (async () => {
      // The key goes only to the credential sub-resource; the settings
      // document must never carry it.
      if (submittedKey.trim() !== "") {
        const credential = await classifierCredentialAction(
          "save_key",
          submittedKey,
        );
        // The key is stored from here on, so the panel must say so even if a
        // later write is rejected — otherwise it would report "not
        // configured" while the backend holds a working key.
        setCredentialConfigured(credential.configured);
        if (credentialDraftRef.current === submittedKey) setCredentialDraft("");
      }
      if (writeSettings) {
        // The default prompt is stored empty, so it keeps following the
        // built-in one instead of freezing today's wording.
        const result = await updateClassifierSettings(
          submitted.inline.prompt ===
            defaultClassifierPromptFor(submitted.dimensions ?? [])
            ? { ...submitted, inline: { ...submitted.inline, prompt: "" } }
            : submitted,
        );
        const shown = withDefaultPrompt(result);
        setSavedSettings(result);
        setSavedDimensions(result.dimensions ?? []);
        setBaseline(JSON.stringify(shown));
        setDraft((current) => (current === submitted ? shown : current));
      }
      if (writeLabels) {
        // Read-modify-write: only `task_type_labels` is this tab's to set on
        // each group; everything else goes back as the server has it now, so
        // a group saved in the routing editor meanwhile is not reverted.
        const latest = await getRoutingSettings();
        const result = await updateRoutingSettings({
          model_routing_groups: groupsOf(latest).map((group) => ({
            ...group,
            task_type_labels: [...submittedLabels],
          })),
        });
        setRouting(result);
        setLabelBaseline(JSON.stringify(submittedLabels));
        setLabels((current) =>
          JSON.stringify(current) === JSON.stringify(submittedLabels)
            ? [...submittedLabels]
            : current,
        );
      }
      const status = await classifierCredentialAction("status");
      setCredentialConfigured(status.configured);
    })()
      // Reported even after further edits: a write that failed half way left
      // part of the draft unsaved, and only this message says which part.
      .catch((caught: unknown) =>
        setError(
          intelligentRoutingErrorText(caught, t("classifier.saveFailed")),
        ),
      )
      .finally(() => setSaving(false));
  };

  const deleteKey = (): void => {
    // Deleting also drops the local draft: leaving it in place kept the panel
    // dirty, so the next save would quietly write the just-deleted key back.
    setError(null);
    void classifierCredentialAction("delete_key")
      .then((status) => {
        setCredentialConfigured(status.configured);
        setCredentialDraft("");
      })
      .catch((caught: unknown) =>
        setError(
          intelligentRoutingErrorText(caught, t("classifier.saveFailed")),
        ),
      );
  };

  const statusText =
    !ready && dirty
      ? t("routing.waitingConnection")
      : saving
        ? t("common.saving")
        : dirty
          ? t("classifierTab.unsaved")
          : t("classifierTab.saved");
  const actions = (
    <div
      className="flex min-w-0 shrink-0 items-center justify-end gap-2"
      data-classifier-actions
    >
      <span
        role="status"
        aria-live="polite"
        className="truncate text-xs text-muted-foreground"
      >
        {draft === null ? "" : statusText}
      </span>
      <Button
        type="button"
        size="sm"
        disabled={!ready || !dirty || saving}
        onClick={save}
        data-classifier-save
      >
        {t("routing.save")}
      </Button>
    </div>
  );

  /**
   * Opens the editor of something just added. Radix hands focus back to a
   * closing popover's trigger one timer tick later, and a popover opened in
   * between reads that as focus leaving it and closes again, so the open
   * waits until the previous one has settled.
   */
  const openAfterClose = (key: string): void => {
    flushSync(() => setOpenPopover(null));
    window.setTimeout(() => setOpenPopover(key), 0);
  };

  const popoverProps = (key: string) => ({
    open: openPopover === key,
    onOpenChange: (open: boolean) =>
      setOpenPopover((current) =>
        open ? key : current === key ? null : current,
      ),
  });

  const labelsEditable =
    ready && !saving && labels !== null && groups.length > 0;
  const labelsDiverge =
    !labelsDirty &&
    modelRoutingTaskTypeLabelsDiverge(
      groups.map((group) => group.task_type_labels),
    );
  const atDimensionCapacity =
    dimensions.length >= classifierMaxCustomDimensions;

  // Why the task type options read or lock the way they do, behind the row's ?.
  const labelNotes = [
    ...(labels !== null && groups.length === 0
      ? [t("classifierTab.labelsNoGroups")]
      : []),
    ...(labelsDiverge ? [t("classifierTab.labelsDiverge")] : []),
    ...(labels !== null && labels.length >= maxModelRoutingTaskTypeLabels
      ? [
          t("classifierTab.labelLimit", {
            max: maxModelRoutingTaskTypeLabels,
          }),
        ]
      : []),
  ];

  const labelIssueText = (issue: TaskTypeLabelIssue) =>
    t(`classifierTab.labelErrors.${issue.problem}`, {
      label: issue.label ?? "",
      group: issue.group ?? "",
      max: maxModelRoutingTaskTypeLabels,
    });

  const taskTypeRow = (
    <div className={rowGrid} data-classifier-dimension="task_type">
      <div className="grid min-w-0 gap-0.5">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-sm font-medium">
            {t("classifierTab.taskType")}
          </span>
          <Badge variant="secondary">{t("classifierTab.builtIn")}</Badge>
          {labelNotes.length > 0 ? (
            <HelpPopover label={t("classifierTab.taskTypeHelpLabel")}>
              <span className="grid gap-1.5">
                {labelNotes.map((note) => (
                  <span key={note}>{note}</span>
                ))}
              </span>
            </HelpPopover>
          ) : null}
        </span>
        <code className="font-mono text-micro text-muted-foreground">
          task_type
        </code>
      </div>
      <div className="grid min-w-0 gap-1.5">
        {labels === null ? (
          <span className="text-xs text-muted-foreground">
            {routingError ?? t("common.loading")}
          </span>
        ) : (
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            {labels.map((label, index) => {
              const problem = shownLabelIssues.find(
                (issue) =>
                  (issue.label !== undefined && issue.label === label) ||
                  (issue.problem === "empty" && label.trim() === ""),
              );
              return (
                <OptionChip
                  key={`label-${index}`}
                  name={label}
                  display={
                    label === ""
                      ? ""
                      : modelRoutingFieldValueLabel("task_type", label)
                  }
                  description=""
                  describable={false}
                  invalid={label.trim() === "" || problem !== undefined}
                  problem={problem ? labelIssueText(problem) : undefined}
                  disabled={!labelsEditable}
                  {...popoverProps(`label:${index}`)}
                  onRename={(name) =>
                    patchLabels(
                      labels.map((item, current) =>
                        current === index ? name : item,
                      ),
                    )
                  }
                  onDescribe={() => {}}
                  onRemove={() => {
                    setOpenPopover(null);
                    patchLabels(
                      labels.filter((_, current) => current !== index),
                    );
                  }}
                  data-option={label}
                />
              );
            })}
            <AddOptionButton
              name={t("classifierTab.taskType")}
              disabled={
                !labelsEditable ||
                labels.length >= maxModelRoutingTaskTypeLabels
              }
              onClick={() => {
                patchLabels([...labels, ""]);
                openAfterClose(`label:${labels.length}`);
              }}
            />
          </div>
        )}
        {shownLabelIssues
          // An empty option is marked on its own chip, which is open for typing.
          .filter((issue) => issue.problem !== "empty")
          .map((issue, index) => (
            <span
              key={`label-issue-${index}`}
              role="alert"
              className="text-xs text-danger-foreground"
            >
              {labelIssueText(issue)}
            </span>
          ))}
      </div>
      <span />
    </div>
  );

  const dimensionRow = (dimension: ClassifierDimension, index: number) => {
    const nameIssue = dimensionIssue(blockingDimensions, index, "name");
    const optionsIssue = dimensionIssue(blockingDimensions, index, "options");
    const title =
      dimension.name.trim() === ""
        ? t("classifierTab.unnamedDimension")
        : dimension.name;
    const update = (next: ClassifierDimension) =>
      patchDimensions((current) =>
        current.map((item, position) => (position === index ? next : item)),
      );
    return (
      <div
        key={`dimension-${index}`}
        className={rowGrid}
        data-classifier-dimension={index}
      >
        <div className="grid min-w-0 gap-0.5">
          <span
            className={
              dimension.name.trim() === ""
                ? "truncate text-sm text-muted-foreground"
                : "truncate font-mono text-sm font-medium"
            }
          >
            {title}
          </span>
          {dimension.description ? (
            <span className="text-xs text-muted-foreground">
              {dimension.description}
            </span>
          ) : null}
          <DimensionIssue issue={nameIssue} />
        </div>
        <div className="grid min-w-0 gap-1.5">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            {dimension.options.map((option, optionIndex) => {
              const issue = dimensionIssue(
                blockingDimensions,
                index,
                "optionName",
                optionIndex,
              );
              return (
                <OptionChip
                  key={`option-${optionIndex}`}
                  name={option.name}
                  display={option.name}
                  description={option.description ?? ""}
                  describable
                  invalid={option.name.trim() === "" || issue !== undefined}
                  problem={
                    issue
                      ? t(`classifierTab.dimensionErrors.${issue.problem}`)
                      : undefined
                  }
                  disabled={!ready || saving}
                  {...popoverProps(`opt:${index}:${optionIndex}`)}
                  onRename={(name) =>
                    update({
                      ...dimension,
                      options: dimension.options.map((item, position) =>
                        position === optionIndex ? { ...item, name } : item,
                      ),
                    })
                  }
                  onDescribe={(description) =>
                    update({
                      ...dimension,
                      options: dimension.options.map((item, position) =>
                        position === optionIndex
                          ? { ...item, description }
                          : item,
                      ),
                    })
                  }
                  onRemove={() => {
                    setOpenPopover(null);
                    update({
                      ...dimension,
                      options: dimension.options.filter(
                        (_, position) => position !== optionIndex,
                      ),
                    });
                  }}
                  data-option={option.name}
                />
              );
            })}
            <AddOptionButton
              name={title}
              disabled={
                !ready ||
                saving ||
                dimension.options.length >= maxModelRoutingListItems
              }
              onClick={() => {
                update({
                  ...dimension,
                  options: [
                    ...dimension.options,
                    { name: "", description: "" },
                  ],
                });
                openAfterClose(`opt:${index}:${dimension.options.length}`);
              }}
            />
          </div>
          <DimensionIssue issue={optionsIssue} />
        </div>
        <Popover {...popoverProps(`dim:${index}`)}>
          <PopoverTrigger asChild>
            <Button
              type="button"
              size="icon-sm"
              variant="ghost"
              className="text-muted-foreground"
              disabled={!ready || saving}
              aria-label={t("classifierTab.editDimension", { name: title })}
              data-classifier-edit-dimension={index}
            >
              <SquarePen aria-hidden="true" />
            </Button>
          </PopoverTrigger>
          <PopoverContent
            align="end"
            sideOffset={4}
            className="grid w-80 gap-3 text-xs"
          >
            <Field
              label={t("classifierTab.dimensionName")}
              tooltip={
                // Built outside i18n: the braces of {{dim.x}} would read as
                // an interpolation.
                <span className="flex min-w-0 flex-wrap items-center gap-1">
                  {t("classifierTab.dimensionPlaceholderHint")}
                  {classifierPlaceholderParts
                    .filter((part) => part !== "")
                    .map((part) => (
                      <code
                        key={part}
                        className="rounded-sm bg-muted px-1 py-0.5 font-mono text-text-secondary"
                      >
                        {classifierPlaceholder(
                          dimension.name === "" ? "…" : dimension.name,
                          part,
                        )}
                      </code>
                    ))}
                </span>
              }
            >
              <Input
                className="h-8 font-mono"
                value={dimension.name}
                maxLength={classifierDimensionNameMaxRunes}
                placeholder={t("classifierTab.dimensionNamePlaceholder")}
                aria-label={t("classifierTab.dimensionName")}
                aria-invalid={nameIssue !== undefined || undefined}
                onChange={(event) =>
                  update({ ...dimension, name: event.target.value })
                }
              />
            </Field>
            <Field label={t("classifierTab.dimensionDescription")}>
              <Input
                className="h-8"
                value={dimension.description ?? ""}
                placeholder={t("classifierTab.dimensionDescriptionPlaceholder")}
                aria-label={t("classifierTab.dimensionDescription")}
                onChange={(event) =>
                  update({ ...dimension, description: event.target.value })
                }
              />
            </Field>
            <DimensionIssue issue={nameIssue} />
            <PopoverFooter
              label={t("classifierTab.removeDimension")}
              disabled={!ready || saving}
              onClick={() => {
                setOpenPopover(null);
                patchDimensions((current) =>
                  current.filter((_, position) => position !== index),
                );
              }}
            />
          </PopoverContent>
        </Popover>
      </div>
    );
  };

  const promptIsDefault =
    draft !== null &&
    draft.inline.prompt === defaultClassifierPromptFor(draft.dimensions ?? []);
  // Every dimension a placeholder can name: the built-in task_type, then the
  // custom ones that already have a name.
  const placeholderDimensions = [
    { name: "task_type", title: t("classifierTab.taskType") },
    ...dimensions
      .filter((dimension) => dimension.name.trim() !== "")
      .map((dimension) => ({ name: dimension.name, title: dimension.name })),
  ];
  const insertIntoPrompt = (token: string) => {
    if (draft === null) return;
    const prompt = draft.inline.prompt;
    const caret = promptCaret.current;
    const start = Math.min(caret?.start ?? prompt.length, prompt.length);
    const end = Math.min(caret?.end ?? prompt.length, prompt.length);
    // Appended without a caret, the token keeps a space from the text before.
    const text =
      caret === null && /\S$/.test(prompt.slice(0, start))
        ? ` ${token}`
        : token;
    const after = start + text.length;
    promptCaret.current = { start: after, end: after };
    patch({
      inline: {
        ...draft.inline,
        prompt: prompt.slice(0, start) + text + prompt.slice(end),
      },
    });
  };
  const returnToPrompt = () => {
    const field = promptField.current;
    const caret = promptCaret.current;
    if (field === null) return;
    field.focus({ preventScroll: true });
    if (caret !== null) field.setSelectionRange(caret.start, caret.end);
  };
  const timeoutValue =
    draft === null
      ? 0
      : usesExistingModel
        ? draft.existing_model.timeout_ms
        : draft.inline.timeout_ms;

  let content: ReactNode = null;
  if (draft !== null) {
    content = (
      <fieldset
        disabled={!ready}
        className="gutter-scroller flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-auto border-0 p-0 pb-1"
        data-tab-scroller
      >
        <Panel className="shrink-0">
          <PanelHeader
            className="items-center"
            actions={
              <span className="inline-flex items-center gap-2 text-xs text-text-secondary">
                {t(draft.enabled ? "classifierTab.on" : "classifierTab.off")}
                <Switch
                  checked={draft.enabled}
                  aria-label={t("classifier.enabled")}
                  onCheckedChange={(checked) => patch({ enabled: checked })}
                />
              </span>
            }
          >
            <h3 className="text-sm font-semibold">
              {t("classifierTab.title")}
            </h3>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {t("classifierTab.description")}
            </p>
          </PanelHeader>
          <div className="grid min-w-0 gap-4 p-4">
            <RadioGroup
              className="grid min-w-0 grid-cols-2 gap-2"
              aria-label={t("classifierTab.source")}
              value={draft.classifier_source}
              onValueChange={(value) =>
                // Switching source keeps both branches' drafts intact, so
                // toggling back and forth never discards typed input.
                patch({
                  classifier_source:
                    value === "inline" ? "inline" : "existing_model",
                })
              }
            >
              <ChoiceCard
                id="classifier-source-existing-model"
                value="existing_model"
                label={t("classifierTab.sourceExisting")}
                description={t("classifierTab.sourceExistingHint")}
                selected={draft.classifier_source === "existing_model"}
              />
              <ChoiceCard
                id="classifier-source-inline"
                value="inline"
                label={t("classifierTab.sourceInline")}
                description={t("classifierTab.sourceInlineHint")}
                selected={draft.classifier_source === "inline"}
              />
            </RadioGroup>
            {draft.classifier_source === "provider_ref" ? (
              <FieldIssue issue={issueFor(settingsIssues, "protocol")} />
            ) : null}
            {usesExistingModel ? (
              <div
                className="grid min-w-0 max-w-md gap-1.5"
                data-classifier-source-fields="existing_model"
              >
                <Field
                  group
                  label={t("classifierTab.existingModel")}
                  tooltip={t("classifierTab.existingModelHint")}
                >
                  <ModelRoutingTargetSelect
                    services={services}
                    value={
                      draft.existing_model.service_id === "" &&
                      draft.existing_model.model === ""
                        ? undefined
                        : {
                            serviceId: draft.existing_model.service_id,
                            model: draft.existing_model.model,
                          }
                    }
                    disabled={!ready}
                    aria-label={t("classifierTab.existingModel")}
                    onChange={(target) =>
                      patch({
                        existing_model: {
                          ...draft.existing_model,
                          service_id: target.serviceId,
                          model: target.model,
                        },
                      })
                    }
                  />
                </Field>
                <FieldIssue issue={issueFor(blocking, "model")} />
              </div>
            ) : null}
            {draft.classifier_source === "inline" ? (
              <div
                className="grid min-w-0 gap-3"
                data-classifier-source-fields="inline"
              >
                <Field
                  group
                  label={t("classifierTab.inlineService")}
                  tooltip={t("classifier.protocolHint")}
                >
                  <SegmentedControl
                    label={t("classifierTab.inlineService")}
                    value={draft.inline.protocol}
                    options={classifierProtocols.map((value) => ({
                      value,
                      label: t(`classifier.protocols.${value}`),
                    }))}
                    onValueChange={(value) =>
                      setDraft((current) =>
                        current === null
                          ? current
                          : {
                              ...current,
                              inline: linkClassifierProtocol(
                                current.inline,
                                value as ClassifierProtocol,
                              ),
                            },
                      )
                    }
                  />
                </Field>
                <div className="grid min-w-0 gap-3 sm:grid-cols-2">
                  <Field
                    label={t("classifier.endpoint")}
                    tooltip={t("classifier.endpointHint")}
                  >
                    <Input
                      value={draft.inline.endpoint}
                      aria-label={t("classifier.endpoint")}
                      aria-invalid={
                        issueFor(blocking, "endpoint") !== undefined
                      }
                      onChange={(event) =>
                        patch({
                          inline: {
                            ...draft.inline,
                            endpoint: event.target.value,
                          },
                        })
                      }
                    />
                    <FieldIssue
                      issue={issueFor(blocking, "endpoint")}
                      namespace="classifier.endpointErrors"
                    />
                  </Field>
                  <Field
                    label={t("classifier.model")}
                    tooltip={t("classifier.modelHint")}
                  >
                    <Input
                      value={draft.inline.model}
                      aria-label={t("classifier.model")}
                      aria-invalid={issueFor(blocking, "model") !== undefined}
                      onChange={(event) =>
                        patch({
                          inline: {
                            ...draft.inline,
                            model: event.target.value,
                          },
                        })
                      }
                    />
                    <FieldIssue issue={issueFor(blocking, "model")} />
                  </Field>
                </div>
                <div className="grid min-w-0 max-w-md gap-1.5">
                  <Field
                    label={t("classifier.apiKey")}
                    tooltip={t("classifier.apiKeyHint")}
                  >
                    <Input
                      type="password"
                      autoComplete="off"
                      value={credentialDraft}
                      aria-label={t("classifier.apiKey")}
                      aria-invalid={issueFor(blocking, "apiKey") !== undefined}
                      placeholder={
                        credentialConfigured
                          ? t("classifier.apiKeyStored")
                          : t("classifier.apiKeyPlaceholder")
                      }
                      onChange={(event) => {
                        setShowIssues(false);
                        setError(null);
                        setCredentialDraft(event.target.value);
                      }}
                    />
                  </Field>
                  <span className="flex items-center gap-2">
                    <span className="text-xs text-muted-foreground">
                      {credentialConfigured
                        ? t("classifier.credentialConfigured")
                        : t("classifier.credentialMissing")}
                    </span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={!ready || !credentialConfigured}
                      onClick={deleteKey}
                    >
                      {t("classifier.deleteKey")}
                    </Button>
                  </span>
                  <FieldIssue issue={issueFor(blocking, "apiKey")} />
                </div>
              </div>
            ) : null}
          </div>
        </Panel>

        <Panel className="shrink-0">
          <PanelHeader
            className="items-center"
            actions={
              <>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {t("classifierTab.dimensionCount", {
                    count: dimensions.length,
                    max: classifierMaxCustomDimensions,
                  })}
                </span>
                {atDimensionCapacity ? (
                  <HelpPopover label={t("classifierTab.dimensionLimitLabel")}>
                    {t("classifierTab.dimensionLimit", {
                      max: classifierMaxCustomDimensions,
                    })}
                  </HelpPopover>
                ) : null}
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={!ready || saving || atDimensionCapacity}
                  title={
                    atDimensionCapacity
                      ? t("classifierTab.dimensionLimit", {
                          max: classifierMaxCustomDimensions,
                        })
                      : undefined
                  }
                  onClick={() => {
                    patchDimensions((current) => [
                      ...current,
                      { name: "", description: "", options: [] },
                    ]);
                    openAfterClose(`dim:${dimensions.length}`);
                  }}
                  data-classifier-add-dimension
                >
                  <Plus aria-hidden="true" />
                  {t("classifierTab.addDimension")}
                </Button>
              </>
            }
          >
            <h3 className="text-sm font-semibold">
              {t("classifierTab.dimensionsTitle")}
            </h3>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {t("classifierTab.dimensionsDescription")}
            </p>
          </PanelHeader>
          <div className="divide-y">
            {taskTypeRow}
            {dimensions.map(dimensionRow)}
          </div>
          {shownDimensionUseIssues.map((issue) => (
            <p
              key={`dimension-use-${issue.group}-${issue.dimension}-${issue.option ?? ""}`}
              role="alert"
              className="border-t px-4 py-2 text-xs text-danger-foreground"
            >
              {t(
                issue.option === undefined
                  ? "classifierTab.dimensionErrors.inUse"
                  : "classifierTab.dimensionErrors.optionInUse",
                {
                  dimension: issue.dimension,
                  option: issue.option,
                  group: issue.group,
                },
              )}
            </p>
          ))}
          <DimensionIssue
            issue={dimensionIssue(
              blockingDimensions,
              classifierMaxCustomDimensions,
              "name",
            )}
          />
        </Panel>

        <Panel className="shrink-0 px-4 py-3" data-classifier-advanced>
          <HelpDisclosure
            title={
              <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                <span className="font-medium text-text-secondary">
                  {t("classifier.advanced")}
                </span>
                <span>
                  {t("classifierTab.advancedSummary", {
                    timeout: timeoutValue,
                    threshold: draft.inline.low_confidence_threshold,
                    prompt: t(
                      promptIsDefault
                        ? "classifierTab.promptDefault"
                        : "classifierTab.promptCustom",
                    ),
                  })}
                </span>
              </span>
            }
            open={
              showIssues &&
              ["timeout_ms", "low_confidence_threshold", "prompt"].some(
                (field) =>
                  issueFor(
                    settingsIssues,
                    field as ClassifierSettingsIssue["field"],
                  ) !== undefined,
              )
                ? true
                : undefined
            }
          >
            <div className="grid min-w-0 gap-3 text-sm text-foreground">
              <div className="flex min-w-0 flex-wrap gap-4">
                {/*
                  The budget belongs to the source that spends it: `inline`
                  reads its own 1–5000 envelope and `existing_model` its
                  independent 1–60000 budget, because a real model call takes
                  far longer than the inline one.
                */}
                <Field
                  className="w-36"
                  label={t("classifier.timeout")}
                  tooltip={
                    usesExistingModel
                      ? t("classifier.timeoutHintExistingModel", {
                          max: classifierExistingModelTimeoutMaxMS,
                        })
                      : t("classifier.timeoutHint", {
                          max: classifierTimeoutMaxMS,
                        })
                  }
                >
                  <Input
                    type="number"
                    className="tabular-nums"
                    min={
                      usesExistingModel
                        ? classifierExistingModelTimeoutMinMS
                        : classifierTimeoutMinMS
                    }
                    max={
                      usesExistingModel
                        ? classifierExistingModelTimeoutMaxMS
                        : classifierTimeoutMaxMS
                    }
                    value={String(timeoutValue)}
                    aria-label={t("classifier.timeout")}
                    aria-invalid={
                      issueFor(blocking, "timeout_ms") !== undefined
                    }
                    onChange={(event) =>
                      patch(
                        usesExistingModel
                          ? {
                              existing_model: {
                                ...draft.existing_model,
                                timeout_ms: event.target.valueAsNumber,
                              },
                            }
                          : {
                              inline: {
                                ...draft.inline,
                                timeout_ms: event.target.valueAsNumber,
                              },
                            },
                      )
                    }
                  />
                  <FieldIssue issue={issueFor(blocking, "timeout_ms")} />
                </Field>
                <Field
                  className="w-36"
                  label={t("classifier.threshold")}
                  tooltip={t("classifier.thresholdHint")}
                >
                  <Input
                    type="number"
                    className="tabular-nums"
                    min={0}
                    max={1}
                    step={0.05}
                    value={String(draft.inline.low_confidence_threshold)}
                    aria-label={t("classifier.threshold")}
                    aria-invalid={
                      issueFor(blocking, "low_confidence_threshold") !==
                      undefined
                    }
                    onChange={(event) =>
                      patch({
                        inline: {
                          ...draft.inline,
                          low_confidence_threshold: event.target.valueAsNumber,
                        },
                      })
                    }
                  />
                  <FieldIssue
                    issue={issueFor(blocking, "low_confidence_threshold")}
                  />
                </Field>
              </div>
              <Field
                label={t("classifier.prompt")}
                tooltip={t("classifier.promptHint")}
              >
                <Textarea
                  ref={promptField}
                  rows={3}
                  value={draft.inline.prompt}
                  aria-label={t("classifier.prompt")}
                  aria-invalid={issueFor(blocking, "prompt") !== undefined}
                  onSelect={(event) => {
                    promptCaret.current = {
                      start: event.currentTarget.selectionStart,
                      end: event.currentTarget.selectionEnd,
                    };
                  }}
                  onChange={(event) => {
                    promptCaret.current = {
                      start: event.target.selectionStart,
                      end: event.target.selectionEnd,
                    };
                    patch({
                      inline: { ...draft.inline, prompt: event.target.value },
                    });
                  }}
                />
                <FieldIssue issue={issueFor(blocking, "prompt")} />
              </Field>
              <div className="flex flex-wrap items-center gap-1">
                <PromptPlaceholderMenu
                  dimensions={placeholderDimensions}
                  onInsert={insertIntoPrompt}
                  onInserted={returnToPrompt}
                />
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  disabled={promptIsDefault}
                  onClick={() => {
                    promptCaret.current = null;
                    patch({
                      inline: {
                        ...draft.inline,
                        prompt: defaultClassifierPromptFor(
                          draft.dimensions ?? [],
                        ),
                      },
                    });
                  }}
                >
                  {t("classifierTab.resetPrompt")}
                </Button>
              </div>
            </div>
          </HelpDisclosure>
        </Panel>
      </fieldset>
    );
  }

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-clip"
      data-testid="classifier-settings-panel"
    >
      {actionsSlot === null
        ? actions
        : active
          ? createPortal(actions, actionsSlot)
          : null}
      {loadError ? (
        <FormMessage tone="error">
          {loadError}
          <Button
            type="button"
            variant="ghost"
            onClick={() => setReload((value) => value + 1)}
          >
            {t("common.retry")}
          </Button>
        </FormMessage>
      ) : null}
      {error ||
      (showIssues &&
        (settingsIssues.length > 0 ||
          dimensionIssues.length > 0 ||
          dimensionUseIssues.length > 0 ||
          (labelsDirty && labelIssues.length > 0))) ? (
        <FormMessage tone="error">
          {error ? t("classifier.saveFailedNotice") : t("classifier.fixErrors")}
          {error ? (
            <span className="text-danger-foreground"> {error}</span>
          ) : null}
        </FormMessage>
      ) : null}
      {draft === null ? (
        <p className="text-xs text-muted-foreground">
          {ready ? t("common.loading") : t("services.gatewayNotReady")}
        </p>
      ) : (
        content
      )}
    </div>
  );
}
