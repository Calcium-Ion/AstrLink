import { Fragment, useEffect, useRef, useState, type RefObject } from "react";

import { ConditionChip } from "@/components/ConditionChip";
import { IconButton } from "@/components/IconButton";
import {
  ArrowRight,
  Brain,
  GripVertical,
  LockKeyhole,
  Plus,
  X,
} from "@/components/icons";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import type { ClassifierDimension } from "@/classifier-settings-model";
import { ModelRoutingTargetSelect } from "@/ModelRoutingTargetSelect";
import {
  RoutingFieldPicker,
  RoutingPhraseEditor,
} from "@/ModelRoutingConditionEditor";
import {
  defaultRoutingPhrase,
  isClassifierField,
  maxRoutingRulePhrases,
  memberTarget,
  phrasesFromWhen,
  type RoutingPhrase,
  type RoutingRuleIssue,
  type RoutingTarget,
} from "@/model-routing-editor-model";
import type {
  ModelRoutingGroup,
  ModelRoutingRule,
} from "@/model-routing-group-model";
import {
  routingPhraseParts,
  routingPhraseText,
} from "@/model-routing-phrase-text";
import { modelRoutingConditionSummary } from "@/request-model-routing-model";
import type { RoutableService } from "@/service-model";
import { cn } from "@/lib/utils";

/** 规则行与「其他情况」行共用的列：句柄、行号、条件、箭头、模型、删除。 */
const rowGrid =
  "grid min-w-0 grid-cols-[1rem_1.25rem_minmax(0,1fr)_1rem_minmax(13rem,20rem)_1.75rem] items-center gap-2 px-3 py-2";

/** 新加的规则一出现就要接着填的那一处。 */
export type RoutingRowFocus = "picker" | "target" | number;

/** A focus request; `seq` makes a repeated request for the same spot count. */
export interface RoutingRowFocusRequest {
  focus: RoutingRowFocus;
  seq: number;
}

/**
 * 规则列表的一行：「当 [条件] 且 [条件] → 模型」。
 *
 * 条件是可点的短语，点开就地编辑；写不成短语的旧规则显示成只读句子，原样
 * 保留，用户可以选择重新设置。行序就是优先级，拖动或方向键调整。
 */
export function ModelRoutingRuleRow({
  rule,
  index,
  count,
  group,
  services,
  used,
  dimensions,
  optionsFor,
  issues,
  disabled,
  focus,
  dragSource,
  onPhrasesChange,
  onTargetChange,
  onRemove,
  onMove,
  onRewrite,
  onManageOptions,
  targetWarning,
}: {
  rule: ModelRoutingRule;
  index: number;
  count: number;
  group: ModelRoutingGroup;
  services: readonly RoutableService[];
  used: readonly RoutingTarget[];
  dimensions: readonly ClassifierDimension[];
  optionsFor: (field: string) => readonly string[];
  issues: readonly RoutingRuleIssue[];
  disabled: boolean;
  focus?: RoutingRowFocusRequest;
  /** 整组共用一个拖拽源：落点在另一行。 */
  dragSource: RefObject<number | null>;
  onPhrasesChange: (phrases: RoutingPhrase[]) => void;
  onTargetChange: (target: RoutingTarget) => void;
  onRemove: () => void;
  onMove: (from: number, to: number) => void;
  onRewrite: () => void;
  onManageOptions?: () => void;
  /** 目标模型不兼容部分请求格式时的说明。 */
  targetWarning?: string;
}) {
  const t = useT();
  const rowRef = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState(false);
  const [openPhrase, setOpenPhrase] = useState<number | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);
  // The phrase a field pick just added; its editor opens once the menu closed.
  const added = useRef<number | null>(null);
  const phrases = phrasesFromWhen(rule.when);
  const number = index + 1;
  const noConditions = issues.some((issue) => issue.problem === "noConditions");
  const conditionsFull =
    phrases !== null && phrases.length >= maxRoutingRulePhrases;

  useEffect(() => {
    const row = rowRef.current;
    if (!row || focus === undefined) return;
    row.scrollIntoView?.({ block: "nearest" });
    if (focus.focus === "picker") setPickerOpen(true);
    else if (focus.focus === "target")
      row
        .querySelector<HTMLInputElement>("[data-routing-target] input")
        ?.focus({ preventScroll: true });
    else setOpenPhrase(focus.focus);
    // Each request applies once; the row's own edits never re-run it.
  }, [focus?.seq]);

  const move = (to: number) => {
    if (to >= 0 && to < count) onMove(index, to);
  };

  const addPhrase = (field: string) => {
    if (!phrases) return;
    const next = [...phrases, defaultRoutingPhrase(field)];
    onPhrasesChange(next);
    added.current = next.length - 1;
  };

  return (
    <div
      ref={rowRef}
      className={cn(
        rowGrid,
        "border-b hover:bg-muted/30",
        dragging && "bg-muted/60",
      )}
      data-routing-rule={index}
      // Only a rule's own handle starts a drop here: text or files dragged
      // over the row are left alone.
      onDragOver={(event) => {
        if (!disabled && dragSource.current !== null) event.preventDefault();
      }}
      onDrop={(event) => {
        const from = dragSource.current;
        if (disabled || from === null) return;
        event.preventDefault();
        dragSource.current = null;
        if (from !== index) onMove(from, index);
      }}
    >
      <span
        role="button"
        tabIndex={disabled ? -1 : 0}
        draggable={!disabled}
        aria-label={t("routingGroups.editor.dragRule", { index: number })}
        aria-disabled={disabled || undefined}
        className={cn(
          "flex size-4 cursor-grab items-center justify-center rounded-sm text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring/30",
          disabled && "cursor-not-allowed opacity-50",
        )}
        data-routing-drag-handle={index}
        onDragStart={(event) => {
          event.dataTransfer?.setData("text/plain", String(index));
          if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
          dragSource.current = index;
          setDragging(true);
        }}
        onDragEnd={() => {
          // A drop outside every row never reaches onDrop.
          dragSource.current = null;
          setDragging(false);
        }}
        onKeyDown={(event) => {
          if (disabled) return;
          if (event.key === "ArrowUp") {
            event.preventDefault();
            move(index - 1);
          } else if (event.key === "ArrowDown") {
            event.preventDefault();
            move(index + 1);
          }
        }}
      >
        <GripVertical aria-hidden="true" className="size-3.5" />
      </span>
      <span className="text-center text-xs text-muted-foreground tabular-nums">
        {number}
      </span>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5">
        <span className="text-xs text-muted-foreground">
          {t("routingGroups.editor.when")}
        </span>
        {phrases === null ? (
          <>
            <span
              className="min-w-0 text-xs text-text-secondary"
              title={t("routingGroups.editor.legacyRule")}
              data-routing-legacy-rule
            >
              {modelRoutingConditionSummary(rule.when)}
            </span>
            <Button
              type="button"
              size="xs"
              variant="outline"
              disabled={disabled}
              onClick={onRewrite}
            >
              {t("routingGroups.editor.legacyRewrite")}
            </Button>
          </>
        ) : (
          <>
            {phrases.map((phrase, phraseIndex) => {
              const parts = routingPhraseParts(phrase);
              const issue = issues.find(
                (item) => item.phraseIndex === phraseIndex,
              );
              return (
                <Fragment key={`${phraseIndex}-${phrase.field}`}>
                  {phraseIndex > 0 ? (
                    <span className="text-xs text-muted-foreground">
                      {t("routingGroups.editor.and")}
                    </span>
                  ) : null}
                  <ConditionChip
                    label={parts.label}
                    value={parts.value}
                    icon={
                      isClassifierField(phrase.field) ? (
                        <Brain
                          aria-hidden="true"
                          className="size-3 text-violet-foreground"
                        />
                      ) : undefined
                    }
                    invalid={issue !== undefined || parts.empty}
                    disabled={disabled}
                    open={openPhrase === phraseIndex}
                    onOpenChange={(open) =>
                      setOpenPhrase(open ? phraseIndex : null)
                    }
                    aria-label={t("routingConditions.editor.chipLabel", {
                      text: routingPhraseText(phrase),
                    })}
                    data-routing-phrase={phraseIndex}
                  >
                    <RoutingPhraseEditor
                      phrase={phrase}
                      options={optionsFor(phrase.field)}
                      dimensions={dimensions}
                      disabled={disabled}
                      onManageOptions={onManageOptions}
                      onChange={(next) =>
                        onPhrasesChange(
                          phrases.map((item, current) =>
                            current === phraseIndex ? next : item,
                          ),
                        )
                      }
                      onRemove={() => {
                        setOpenPhrase(null);
                        onPhrasesChange(
                          phrases.filter(
                            (_, current) => current !== phraseIndex,
                          ),
                        );
                      }}
                    />
                  </ConditionChip>
                </Fragment>
              );
            })}
            <RoutingFieldPicker
              dimensions={dimensions}
              open={pickerOpen}
              onOpenChange={setPickerOpen}
              onPick={addPhrase}
              onAfterPick={() => {
                if (added.current !== null) setOpenPhrase(added.current);
                added.current = null;
              }}
            >
              {noConditions ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-7 border-destructive/60 text-danger-foreground"
                  disabled={disabled}
                  data-routing-add-condition
                >
                  <Plus aria-hidden="true" />
                  {t("routingGroups.editor.noConditions")}
                </Button>
              ) : (
                <Button
                  type="button"
                  size="icon-xs"
                  variant="ghost"
                  className="text-muted-foreground"
                  aria-label={t("routingGroups.editor.addCondition")}
                  title={
                    conditionsFull
                      ? t("routingGroups.editor.conditionLimit", {
                          max: maxRoutingRulePhrases,
                        })
                      : undefined
                  }
                  disabled={disabled || conditionsFull}
                  data-routing-add-condition
                >
                  <Plus aria-hidden="true" />
                </Button>
              )}
            </RoutingFieldPicker>
          </>
        )}
      </div>
      <ArrowRight
        aria-hidden="true"
        className="size-3.5 text-muted-foreground"
      />
      <div className="min-w-0" data-routing-target={index}>
        <ModelRoutingTargetSelect
          services={services}
          used={used}
          value={memberTarget(group, rule.target_member_id)}
          disabled={disabled}
          warning={targetWarning}
          aria-label={t("routingGroups.editor.ruleTarget", { index: number })}
          onChange={onTargetChange}
        />
      </div>
      <IconButton
        type="button"
        className="text-muted-foreground hover:text-destructive"
        label={t("routingGroups.editor.removeRule", { index: number })}
        disabled={disabled}
        onClick={onRemove}
        data-routing-remove-rule={index}
      >
        <X aria-hidden="true" />
      </IconButton>
    </div>
  );
}

/** 最后一行：以上规则都不满足时用的模型。不能删除、不能移动。 */
export function ModelRoutingFallbackRow({
  group,
  services,
  used,
  disabled,
  onTargetChange,
  targetWarning,
}: {
  group: ModelRoutingGroup;
  services: readonly RoutableService[];
  used: readonly RoutingTarget[];
  disabled: boolean;
  onTargetChange: (target: RoutingTarget) => void;
  /** 「其他情况」的模型不兼容部分请求格式时的说明。 */
  targetWarning?: string;
}) {
  const t = useT();
  return (
    <div className={cn(rowGrid, "bg-muted/45")} data-routing-fallback>
      <LockKeyhole
        aria-hidden="true"
        className="size-3.5 text-muted-foreground"
      />
      <span />
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <span className="text-sm font-medium">
          {t("routingGroups.editor.fallback")}
        </span>
        <span className="text-xs text-muted-foreground">
          {t("routingGroups.editor.fallbackHint")}
        </span>
      </div>
      <ArrowRight
        aria-hidden="true"
        className="size-3.5 text-muted-foreground"
      />
      <div className="min-w-0" data-routing-target="fallback">
        <ModelRoutingTargetSelect
          services={services}
          used={used}
          value={memberTarget(group, group.default_member_id)}
          disabled={disabled}
          warning={targetWarning}
          aria-label={t("routingGroups.editor.fallbackTarget")}
          onChange={onTargetChange}
        />
      </div>
      <span />
    </div>
  );
}

/**
 * 只读的条件短语，样式与编辑器里的短语一致但不可点。试运行结果用它复述
 * 每条规则；写不成短语的旧规则退回一句摘要。
 */
export function ModelRoutingPhraseTags({ rule }: { rule: ModelRoutingRule }) {
  const t = useT();
  const phrases = phrasesFromWhen(rule.when);
  if (phrases === null)
    return (
      <span className="text-xs text-text-secondary">
        {modelRoutingConditionSummary(rule.when)}
      </span>
    );
  return (
    <>
      {phrases.map((phrase, phraseIndex) => {
        const parts = routingPhraseParts(phrase);
        return (
          <Fragment key={`${phraseIndex}-${phrase.field}`}>
            {phraseIndex > 0 ? (
              <span className="text-xs text-muted-foreground">
                {t("routingGroups.editor.and")}
              </span>
            ) : null}
            <span
              className="inline-flex h-6 max-w-full min-w-0 items-center gap-1 rounded-md border border-input bg-card px-1.5 text-xs"
              data-routing-phrase-tag
            >
              {isClassifierField(phrase.field) ? (
                <Brain
                  aria-hidden="true"
                  className="size-3 text-violet-foreground"
                />
              ) : null}
              {parts.label ? (
                <span className="shrink-0 text-muted-foreground">
                  {parts.label}
                </span>
              ) : null}
              <span className="min-w-0 truncate font-medium">
                {parts.value}
              </span>
            </span>
          </Fragment>
        );
      })}
    </>
  );
}
