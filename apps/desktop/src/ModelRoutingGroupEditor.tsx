import { useMemo, useRef, useState } from "react";

import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { Panel, PanelHeader } from "@/components/Panel";
import { Brain, Copy, Flask, Plus, SquarePen } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import type { ClassifierDimension } from "@/classifier-settings-model";
import { useT } from "@/i18n";
import { PageHeader } from "@/PageHeader";
import { RoutingRuleTemplates } from "@/ModelRoutingConditionEditor";
import { copyRoutingModelName } from "@/ModelRoutingGroupList";
import {
  ModelRoutingFallbackRow,
  ModelRoutingRuleRow,
  type RoutingRowFocus,
} from "@/ModelRoutingRuleRow";
import {
  addRoutingRule,
  memberTarget,
  moveRoutingRule,
  removeRoutingRule,
  routingGroupIssues,
  routingGroupNameIssue,
  routingGroupTargets,
  routingGroupUsesClassifier,
  routingPhraseIssue,
  routingRuleIssues,
  routingTemplateIDs,
  routingTemplatePhrases,
  setDefaultRoutingTarget,
  setRuleRoutingPhrases,
  setRuleRoutingTarget,
  type RoutingTemplateID,
} from "@/model-routing-editor-model";
import {
  maxModelRoutingMembersPerGroup,
  maxModelRoutingRulesPerGroup,
  modelRoutingFieldValues,
  modelRoutingMediaKinds,
  modelRoutingPublicModelID,
  modelRoutingTaskTypes,
  type ModelRoutingGroup,
} from "@/model-routing-group-model";
import type { RoutableService } from "@/service-model";
import type { ConversionEngineCapability } from "@/core-model";
import {
  memberAcceptedFormats,
  routingFormatReport,
} from "@/model-routing-format-model";
import { ModelRoutingFormatNotice } from "@/ModelRoutingFormatNotice";
import { enableServiceEntry } from "@/service-entry-model";
import { protocolLabel } from "@/service-presets";

/** 改名浮层：组名就是客户端请求的模型名，改了要提醒客户端跟着改。 */
function RenamePopover({
  name,
  issue,
  disabled,
  onChange,
}: {
  name: string;
  issue: string | null;
  disabled: boolean;
  onChange: (name: string) => void;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <IconButton
          type="button"
          size="icon-xs"
          className="text-muted-foreground"
          label={t("routingGroups.editor.rename")}
          disabled={disabled}
          data-routing-rename
        >
          <SquarePen aria-hidden="true" />
        </IconButton>
      </PopoverTrigger>
      <PopoverContent align="start" className="grid w-80 gap-2">
        <Field
          label={t("routingGroups.create.name")}
          tooltip={t("routingGroups.editor.renameHint")}
        >
          <span className="flex min-w-0 items-center gap-1">
            <span className="shrink-0 font-mono text-sm text-muted-foreground">
              astrlink/
            </span>
            <Input
              className="h-8 font-mono"
              value={name}
              maxLength={64}
              aria-invalid={issue !== null}
              onChange={(event) => onChange(event.target.value.toLowerCase())}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.nativeEvent.isComposing)
                  setOpen(false);
              }}
            />
          </span>
        </Field>
        {issue ? (
          <p role="alert" className="text-xs text-danger-foreground">
            {issue}
          </p>
        ) : null}
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="justify-self-end"
          onClick={() => setOpen(false)}
        >
          {t("routingGroups.editor.renameDone")}
        </Button>
      </PopoverContent>
    </Popover>
  );
}

/**
 * 一个路由组的编辑页：标题行（返回、模型名、试运行、启用、删除）、规则列表、
 * 底部保存栏。规则一行一句，最后一行是「其他情况」；改动都是草稿，点保存才写回。
 */
export function ModelRoutingGroupEditor({
  group,
  savedID,
  otherIDs,
  services,
  dimensions,
  protocols,
  classifierEnabled,
  ready,
  dirty,
  saving,
  error,
  onChange,
  onSave,
  onDiscard,
  onBack,
  onDelete,
  onDryRun,
  onOpenClassifier,
  conversionEngine,
  onOpenService,
  onServicesChanged,
}: {
  group: ModelRoutingGroup;
  /** The saved name; renaming compares against it. */
  savedID: string;
  /** Names of the other groups, for the rename check. */
  otherIDs: readonly string[];
  services: readonly RoutableService[];
  dimensions: readonly ClassifierDimension[];
  protocols: readonly string[];
  /** `null` while the classifier settings are unknown. */
  classifierEnabled: boolean | null;
  ready: boolean;
  dirty: boolean;
  saving: boolean;
  error: string | null;
  onChange: (group: ModelRoutingGroup) => void;
  onSave: () => void;
  onDiscard: () => void;
  onBack: () => void;
  onDelete: () => void;
  onDryRun: () => void;
  onOpenClassifier: () => void;
  /** Decides which missing entries a provider could open by converting. */
  conversionEngine?: ConversionEngineCapability | null;
  /** Opens a provider's entry protocols. */
  onOpenService?: (serviceId: string) => void;
  /** A provider changed here (an entry was enabled); reload the list. */
  onServicesChanged?: () => void;
}) {
  const t = useT();
  const disabled = !ready || saving;
  const dragSource = useRef<number | null>(null);
  const [focus, setFocus] = useState<{
    ruleID: string;
    focus: RoutingRowFocus;
    seq: number;
  } | null>(null);
  // Set when a menu pick adds a rule; applied once that menu has closed, so
  // the menu's own focus hand-back cannot undo it.
  const pendingFocus = useRef<{
    ruleID: string;
    focus: RoutingRowFocus;
  } | null>(null);

  const issues = useMemo(() => routingRuleIssues(group), [group]);
  const groupIssues = useMemo(() => routingGroupIssues(group), [group]);
  const formats = useMemo(
    () => routingFormatReport(group, services, conversionEngine),
    [group, services, conversionEngine],
  );
  const [formatBusy, setFormatBusy] = useState(false);
  const [formatError, setFormatError] = useState<string | null>(null);
  const enableConversions = async () => {
    setFormatBusy(true);
    setFormatError(null);
    try {
      for (const { service, protocols } of formats.fixable.values())
        for (const protocol of protocols)
          await enableServiceEntry(service.id, protocol, conversionEngine);
    } catch {
      setFormatError(t("routingFormats.enableFailed"));
    } finally {
      setFormatBusy(false);
      onServicesChanged?.();
    }
  };
  /** The words on a target that some request formats cannot reach. */
  const targetWarning = (memberID: string): string | undefined => {
    const lacking = formats.memberGaps.get(memberID);
    const member = group.members.find((item) => item.id === memberID);
    if (!lacking || !member) return undefined;
    const list = (protocols: readonly string[]) =>
      protocols
        .map((protocol) => protocolLabel(protocol))
        .join(t("routingFormats.listSeparator"));
    const accepted = memberAcceptedFormats(member, services);
    const provider =
      services.find((service) => service.id === member.service_id)?.name ??
      t("binding.removedService");
    return [
      accepted.length > 0
        ? t("routingFormats.memberAccepts", {
            provider,
            formats: list(accepted),
          })
        : null,
      t("routingFormats.memberFails", { formats: list(lacking) }),
    ]
      .filter(Boolean)
      .join("");
  };
  const rulesFull = group.rules.length >= maxModelRoutingRulesPerGroup;
  const nameProblem = routingGroupNameIssue(group.id, otherIDs, savedID);
  const nameIssue = nameProblem
    ? t(`routingGroups.nameIssues.${nameProblem}`)
    : null;
  const used = useMemo(() => routingGroupTargets(group), [group]);
  const labels =
    group.task_type_labels && group.task_type_labels.length > 0
      ? group.task_type_labels
      : modelRoutingTaskTypes;
  const optionsFor = (field: string): readonly string[] =>
    field === "task_type"
      ? labels
      : field === "media_kind"
        ? // Core reads the body of every request that reaches a group, so
          // a live media_kind is never "unknown" and a rule on it would
          // never match. A rule that already names it still lists it.
          modelRoutingMediaKinds.filter((kind) => kind !== "unknown")
        : (modelRoutingFieldValues(field, dimensions, protocols) ?? []);
  const publicID = modelRoutingPublicModelID(group.id);
  const defaultModel =
    memberTarget(group, group.default_member_id)?.model ?? "";

  /** Adds a rule; from a menu, its focus waits until the menu has closed. */
  const addTemplate = (
    template: RoutingTemplateID | "blank",
    fromMenu = false,
  ) => {
    const phrases =
      template === "blank" ? [] : routingTemplatePhrases(template, labels);
    const next = addRoutingRule(group, phrases);
    const rule = next.rules[next.rules.length - 1];
    const incomplete = phrases.findIndex(
      (phrase) => routingPhraseIssue(phrase) !== null,
    );
    const request = {
      ruleID: rule.id,
      focus: (template === "blank"
        ? "picker"
        : incomplete >= 0
          ? incomplete
          : "target") as RoutingRowFocus,
    };
    if (fromMenu) pendingFocus.current = request;
    else setFocus({ ...request, seq: Date.now() });
    onChange(next);
  };

  const status = error
    ? error
    : !ready && dirty
      ? t("routing.waitingConnection")
      : saving
        ? t("common.saving")
        : groupIssues.length > 0
          ? t(`routingGroups.editor.groupIssues.${groupIssues[0]}`, {
              max:
                groupIssues[0] === "tooManyRules"
                  ? maxModelRoutingRulesPerGroup
                  : maxModelRoutingMembersPerGroup,
            })
          : issues.length > 0
            ? t("routingGroups.editor.fixIssues", { count: issues.length })
            : dirty
              ? t("routingGroups.editor.unsaved")
              : t("routingGroups.editor.saved");

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3"
      data-testid="routing-group-editor"
    >
      <PageHeader
        className="mb-0"
        back={{ label: t("routingGroups.editor.back"), onClick: onBack }}
        title={publicID}
        titleGroupClassName="[&_h1]:font-mono"
        titleSuffix={
          <span className="flex shrink-0 items-center gap-0.5">
            <IconButton
              type="button"
              size="icon-xs"
              className="text-muted-foreground"
              label={t("routingGroups.list.copy")}
              onClick={() => void copyRoutingModelName(publicID)}
            >
              <Copy aria-hidden="true" />
            </IconButton>
            <RenamePopover
              name={group.id}
              issue={nameIssue}
              disabled={disabled}
              onChange={(id) => onChange({ ...group, id })}
            />
          </span>
        }
        actions={
          <>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={onDryRun}
              data-routing-dry-run
            >
              <Flask aria-hidden="true" />
              {t("routingGroups.editor.dryRun")}
            </Button>
            <Label className="font-normal text-text-secondary">
              {t("routingGroups.editor.enabled")}
              <Switch
                size="sm"
                checked={group.enabled}
                disabled={disabled}
                aria-label={t("routingGroups.editor.enabled")}
                onCheckedChange={(enabled) => onChange({ ...group, enabled })}
              />
            </Label>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="text-muted-foreground hover:text-destructive"
              disabled={disabled}
              onClick={onDelete}
            >
              {t("routingGroups.editor.delete")}
            </Button>
          </>
        }
      />
      {nameIssue ? <FormMessage tone="error">{nameIssue}</FormMessage> : null}
      {classifierEnabled === false && routingGroupUsesClassifier(group) ? (
        <FormMessage
          tone="warning"
          className="flex flex-wrap items-center gap-x-2 gap-y-1"
        >
          <Brain aria-hidden="true" className="size-3.5 shrink-0" />
          {t("routingGroups.editor.classifierOff")}
          <Button
            type="button"
            variant="link"
            className="h-auto p-0 text-xs text-warning-foreground underline"
            onClick={onOpenClassifier}
          >
            {t("routingGroups.editor.classifierOffAction")}
          </Button>
        </FormMessage>
      ) : null}
      <Panel
        className="flex min-h-0 flex-1 flex-col"
        data-testid="routing-rules"
      >
        <PanelHeader
          size="sm"
          actions={
            <RoutingRuleTemplates
              disabled={disabled || rulesFull}
              onPick={(template) => addTemplate(template, true)}
              onAfterPick={() => {
                const request = pendingFocus.current;
                pendingFocus.current = null;
                if (request) setFocus({ ...request, seq: Date.now() });
              }}
            />
          }
        >
          <div className="flex min-w-0 items-center gap-2">
            <h2 className="shrink-0 text-sm font-semibold">
              {t("routingGroups.editor.rulesTitle")}
            </h2>
            <span className="truncate text-xs text-muted-foreground">
              {t("routingGroups.editor.rulesHint")}
            </span>
            <HelpPopover label={t("routingGroups.editor.rulesHelpLabel")}>
              {t("routingGroups.editor.rulesHelp")}
            </HelpPopover>
          </div>
        </PanelHeader>
        <div className="min-h-0 flex-1 overflow-y-auto" data-tab-scroller>
          <ModelRoutingFormatNotice
            report={formats}
            busy={formatBusy}
            error={formatError}
            onEnable={disabled ? undefined : () => void enableConversions()}
            onOpenService={onOpenService}
          />
          {group.rules.length === 0 ? (
            <div
              className="grid gap-3 border-b px-4 py-5"
              data-routing-no-rules
            >
              <div>
                <p className="text-sm font-medium">
                  {t("routingGroups.editor.emptyTitle")}
                </p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {t("routingGroups.editor.emptyDescription", {
                    model: defaultModel,
                  })}
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                {routingTemplateIDs.map((template) => (
                  <Button
                    key={template}
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={disabled}
                    onClick={() => addTemplate(template)}
                    data-routing-template-button={template}
                  >
                    {template === "complexCoding" ? (
                      <Brain
                        aria-hidden="true"
                        className="text-violet-foreground"
                      />
                    ) : null}
                    {t(`routingGroups.editor.templates.${template}.title`)}
                  </Button>
                ))}
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="text-muted-foreground"
                  disabled={disabled}
                  onClick={() => addTemplate("blank")}
                >
                  <Plus aria-hidden="true" />
                  {t("routingGroups.editor.blankRule")}
                </Button>
              </div>
            </div>
          ) : (
            group.rules.map((rule, index) => (
              <ModelRoutingRuleRow
                key={rule.id}
                rule={rule}
                index={index}
                count={group.rules.length}
                group={group}
                services={services}
                used={used}
                dimensions={dimensions}
                optionsFor={optionsFor}
                issues={issues.filter((issue) => issue.ruleIndex === index)}
                disabled={disabled}
                focus={
                  focus?.ruleID === rule.id
                    ? { focus: focus.focus, seq: focus.seq }
                    : undefined
                }
                dragSource={dragSource}
                onPhrasesChange={(phrases) =>
                  onChange(setRuleRoutingPhrases(group, index, phrases))
                }
                onTargetChange={(target) =>
                  onChange(setRuleRoutingTarget(group, index, target))
                }
                onRemove={() => onChange(removeRoutingRule(group, index))}
                onMove={(from, to) =>
                  onChange(moveRoutingRule(group, from, to))
                }
                onRewrite={() => {
                  setFocus({
                    ruleID: rule.id,
                    focus: "picker",
                    seq: Date.now(),
                  });
                  onChange(setRuleRoutingPhrases(group, index, []));
                }}
                onManageOptions={onOpenClassifier}
                targetWarning={targetWarning(rule.target_member_id)}
              />
            ))
          )}
          <ModelRoutingFallbackRow
            group={group}
            services={services}
            used={used}
            disabled={disabled}
            onTargetChange={(target) =>
              onChange(setDefaultRoutingTarget(group, target))
            }
            targetWarning={targetWarning(group.default_member_id)}
          />
        </div>
      </Panel>
      <footer className="flex min-w-0 shrink-0 items-center justify-between gap-3 border-t pt-3 pb-1">
        <span
          role="status"
          aria-live="polite"
          className={
            error || issues.length > 0 || groupIssues.length > 0
              ? "min-w-0 text-xs text-danger-foreground"
              : "min-w-0 text-xs text-muted-foreground"
          }
        >
          {status}
        </span>
        <div className="flex shrink-0 items-center gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={!dirty || saving}
            onClick={onDiscard}
          >
            {t("routingGroups.editor.discard")}
          </Button>
          <Button
            type="button"
            className="min-w-28"
            disabled={
              !ready ||
              !dirty ||
              saving ||
              issues.length > 0 ||
              groupIssues.length > 0 ||
              nameIssue !== null
            }
            onClick={onSave}
            data-routing-save
          >
            {saving ? t("common.saving") : t("routing.save")}
          </Button>
        </div>
      </footer>
    </div>
  );
}
