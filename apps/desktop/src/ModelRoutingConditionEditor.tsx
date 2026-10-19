import { useRef, type ReactNode } from "react";

import { SegmentedControl } from "@/components/SegmentedControl";
import { Brain, ChevronDown, Plus } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { MultiCombobox } from "@/components/ui/combobox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useT } from "@/i18n";
import type { ClassifierDimension } from "@/classifier-settings-model";
import {
  isClassifierField,
  routingFieldGroups,
  routingMoreFields,
  routingPhraseIssue,
  routingTemplateIDs,
  type RoutingPhrase,
  type RoutingTemplateID,
} from "@/model-routing-editor-model";
import {
  maxModelRoutingKeywords,
  maxModelRoutingListItems,
  maxModelRoutingValueRunes,
  modelRoutingTimeStepSeconds,
} from "@/model-routing-group-model";
import {
  formatRoutingCount,
  routingFieldHint,
  routingFieldName,
  routingValueLabel,
} from "@/model-routing-phrase-text";

/** 浮层底部：删除这个条件。改动本身随草稿保存，没有单独的确认。 */
function EditorFooter({
  onRemove,
  disabled,
}: {
  onRemove: () => void;
  disabled: boolean;
}) {
  const t = useT();
  return (
    <div className="-mx-4 -mb-4 flex items-center border-t bg-muted/40 px-2 py-1.5">
      <Button
        type="button"
        size="sm"
        variant="ghost"
        className="text-muted-foreground hover:text-destructive"
        disabled={disabled}
        onClick={onRemove}
        data-routing-phrase-remove
      >
        {t("routingConditions.editor.remove")}
      </Button>
    </div>
  );
}

function EditorHeader({ field, custom }: { field: string; custom: boolean }) {
  const t = useT();
  const classifier = isClassifierField(field);
  return (
    <div className="flex min-w-0 items-center justify-between gap-2">
      <span className="flex min-w-0 items-center gap-1.5 text-sm font-medium">
        {classifier ? (
          <Brain
            aria-hidden="true"
            className="size-3.5 text-violet-foreground"
          />
        ) : null}
        <span className={custom ? "truncate font-mono" : "truncate"}>
          {routingFieldName(field)}
        </span>
      </span>
      {classifier ? (
        <span className="shrink-0 text-micro text-muted-foreground">
          {t(
            custom
              ? "routingConditions.editor.customDimension"
              : "routingConditions.editor.classifierSource",
          )}
        </span>
      ) : null}
    </div>
  );
}

const tokenPresets = [32_000, 64_000, 128_000, 200_000];

type CountMode = "atLeast" | "atMost" | "between";

function countMode(
  phrase: Extract<RoutingPhrase, { kind: "count" }>,
): CountMode {
  if (phrase.min !== undefined && phrase.max !== undefined) return "between";
  if (phrase.max !== undefined) return "atMost";
  return "atLeast";
}

function parseCount(text: string): number | undefined | null {
  if (text.trim() === "") return undefined;
  const value = Number(text);
  return Number.isSafeInteger(value) && value >= 0 ? value : null;
}

/**
 * 一个条件的编辑器，放在条件短语的浮层里。五类条件各有一种写法：
 * 选项勾选、两段切换、数量边界、关键词、时间段。
 */
export function RoutingPhraseEditor({
  phrase,
  options,
  dimensions,
  disabled = false,
  onChange,
  onRemove,
  onManageOptions,
}: {
  phrase: RoutingPhrase;
  /** 选项类字段的取值域：任务类型标签、协议、维度选项等。 */
  options: readonly string[];
  dimensions: readonly ClassifierDimension[];
  disabled?: boolean;
  onChange: (phrase: RoutingPhrase) => void;
  onRemove: () => void;
  /** 跳到维护选项的地方（分类器页）；只对分类器字段出现。 */
  onManageOptions?: () => void;
}) {
  const t = useT();
  const custom = dimensions.some(
    (dimension) => dimension.name === phrase.field,
  );
  const header = <EditorHeader field={phrase.field} custom={custom} />;
  const footer = <EditorFooter onRemove={onRemove} disabled={disabled} />;

  switch (phrase.kind) {
    case "options": {
      const descriptions = new Map(
        (
          dimensions.find((dimension) => dimension.name === phrase.field)
            ?.options ?? []
        ).map((option) => [option.name, option.description ?? ""]),
      );
      // A value the rule already uses stays listed even after it left the
      // domain, so it can be unticked instead of silently kept.
      const listed = [
        ...options,
        ...phrase.values.filter((value) => !options.includes(value)),
      ];
      const toggle = (value: string, checked: boolean) =>
        onChange({
          ...phrase,
          values: checked
            ? [...phrase.values, value]
            : phrase.values.filter((item) => item !== value),
        });
      return (
        <>
          {header}
          <SegmentedControl
            label={t("routingConditions.editor.optionsMode")}
            value={phrase.negate ? "noneOf" : "anyOf"}
            disabled={disabled}
            options={[
              { value: "anyOf", label: t("routingConditions.editor.anyOf") },
              { value: "noneOf", label: t("routingConditions.editor.noneOf") },
            ]}
            onValueChange={(mode) =>
              onChange({ ...phrase, negate: mode === "noneOf" })
            }
          />
          {listed.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              {t("routingConditions.editor.noOptions")}
            </p>
          ) : (
            <div className="-mx-1 grid max-h-56 gap-0.5 overflow-y-auto">
              {listed.map((value) => {
                const label = routingValueLabel(phrase.field, value);
                const description = descriptions.get(value);
                const checked = phrase.values.includes(value);
                return (
                  <Label
                    key={value}
                    className="min-w-0 cursor-pointer rounded-sm px-1 py-1 font-normal hover:bg-muted"
                    data-routing-option={value}
                  >
                    <Checkbox
                      checked={checked}
                      disabled={
                        disabled ||
                        (!checked &&
                          phrase.values.length >= maxModelRoutingListItems)
                      }
                      onCheckedChange={(next) => toggle(value, next === true)}
                    />
                    <span className="shrink-0 text-sm">{label}</span>
                    {label !== value ? (
                      <span className="shrink-0 font-mono text-micro text-muted-foreground">
                        {value}
                      </span>
                    ) : null}
                    {description ? (
                      <span className="min-w-0 truncate text-micro text-muted-foreground">
                        {description}
                      </span>
                    ) : null}
                  </Label>
                );
              })}
            </div>
          )}
          {isClassifierField(phrase.field) ? (
            <p className="flex flex-wrap items-center gap-x-1.5 text-micro text-muted-foreground">
              {t("routingConditions.editor.manageOptions")}
              {onManageOptions ? (
                <Button
                  type="button"
                  variant="link"
                  className="h-auto p-0 text-micro text-primary"
                  onClick={onManageOptions}
                >
                  {t("routingConditions.editor.manageOptionsAction")}
                </Button>
              ) : null}
            </p>
          ) : null}
          {footer}
        </>
      );
    }
    case "boolean":
      return (
        <>
          {header}
          <SegmentedControl
            label={routingFieldName(phrase.field)}
            value={phrase.value ? "true" : "false"}
            disabled={disabled}
            options={(["true", "false"] as const).map((value) => ({
              value,
              label: t(`routingConditions.boolean.${phrase.field}.${value}`),
            }))}
            onValueChange={(value) =>
              onChange({ ...phrase, value: value === "true" })
            }
          />
          <p className="text-micro text-muted-foreground">
            {t("routingConditions.editor.localNote")}
          </p>
          {footer}
        </>
      );
    case "count": {
      const mode = countMode(phrase);
      const tokens = phrase.field === "estimated_input_context_tokens";
      const setMode = (next: CountMode) => {
        const anchor = phrase.min ?? phrase.max ?? 0;
        if (next === "atLeast")
          onChange({ ...phrase, min: anchor, max: undefined });
        else if (next === "atMost")
          onChange({ ...phrase, min: undefined, max: phrase.max ?? anchor });
        else
          onChange({
            ...phrase,
            min: phrase.min ?? 0,
            max: phrase.max ?? Math.max(anchor, phrase.min ?? 0),
          });
      };
      const bound = (side: "min" | "max") => (
        <Input
          type="number"
          min={0}
          step={1}
          inputMode="numeric"
          className="h-8 w-28 tabular-nums"
          aria-label={t(
            side === "min"
              ? "routingConditions.editor.min"
              : "routingConditions.editor.max",
          )}
          disabled={disabled}
          value={phrase[side] === undefined ? "" : String(phrase[side])}
          onChange={(event) => {
            const parsed = parseCount(event.target.value);
            if (parsed === null) return;
            onChange({ ...phrase, [side]: parsed });
          }}
        />
      );
      const single = mode === "atMost" ? "max" : "min";
      const shown = phrase[single];
      return (
        <>
          {header}
          <SegmentedControl
            label={t("routingConditions.editor.countMode")}
            value={mode}
            disabled={disabled}
            options={(["atLeast", "atMost", "between"] as const).map(
              (value) => ({
                value,
                label: t(`routingConditions.editor.${value}`),
              }),
            )}
            onValueChange={setMode}
          />
          {mode === "between" ? (
            <div className="flex items-center gap-2">
              {bound("min")}
              <span className="text-muted-foreground">–</span>
              {bound("max")}
            </div>
          ) : (
            <div className="flex items-center gap-2">
              {bound(single)}
              {tokens && shown !== undefined && shown >= 1_000 ? (
                <span className="text-xs text-muted-foreground">
                  {t("routingConditions.editor.approx", {
                    value: formatRoutingCount(phrase.field, shown),
                  })}
                </span>
              ) : null}
            </div>
          )}
          {tokens && mode !== "between" ? (
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-micro text-muted-foreground">
                {t("routingConditions.editor.common")}
              </span>
              {tokenPresets.map((preset) => (
                <Button
                  key={preset}
                  type="button"
                  size="xs"
                  variant={shown === preset ? "secondary" : "outline"}
                  disabled={disabled}
                  onClick={() => onChange({ ...phrase, [single]: preset })}
                >
                  {formatRoutingCount(phrase.field, preset)}
                </Button>
              ))}
            </div>
          ) : null}
          {phrase.min !== undefined &&
          phrase.max !== undefined &&
          phrase.min > phrase.max ? (
            <p role="alert" className="text-xs text-danger-foreground">
              {t("routingConditions.editor.rangeError")}
            </p>
          ) : null}
          {tokens ? (
            <p className="text-micro text-muted-foreground">
              {t("routingConditions.editor.tokenNote")}
            </p>
          ) : null}
          {footer}
        </>
      );
    }
    case "keywords":
      return (
        <>
          {header}
          <SegmentedControl
            label={t("routingConditions.editor.keywordsMode")}
            value={phrase.negate ? "none" : "any"}
            disabled={disabled}
            options={[
              {
                value: "any",
                label: t("routingConditions.editor.containsAny"),
              },
              {
                value: "none",
                label: t("routingConditions.editor.containsNone"),
              },
            ]}
            onValueChange={(mode) =>
              onChange({ ...phrase, negate: mode === "none" })
            }
          />
          <MultiCombobox
            values={phrase.keywords}
            options={[]}
            max={maxModelRoutingKeywords}
            allowCustomValue
            disabled={disabled}
            placeholder={t("routingConditions.editor.keywordsPlaceholder")}
            emptyMessage={t("routingConditions.editor.keywordsPlaceholder")}
            createLabel={(value) =>
              t("routingConditions.editor.addKeyword", { value })
            }
            removeLabel={(value) =>
              t("routingConditions.editor.removeKeyword", { value })
            }
            aria-label={t("routingConditions.editor.keywords")}
            onChange={(keywords) =>
              onChange({
                ...phrase,
                keywords: keywords
                  .map((keyword) => keyword.trim())
                  .filter(Boolean),
              })
            }
          />
          <p className="text-micro text-muted-foreground">
            {t("routingConditions.editor.keywordsNote", {
              max: maxModelRoutingKeywords,
            })}
          </p>
          {routingPhraseIssue(phrase) === "tooLong" ? (
            <p role="alert" className="text-xs text-danger-foreground">
              {t("routingConditions.editor.keywordTooLong", {
                max: maxModelRoutingValueRunes,
              })}
            </p>
          ) : null}
          {footer}
        </>
      );
    case "time": {
      const complete = phrase.start !== "" && phrase.end !== "";
      return (
        <>
          {header}
          <div className="flex items-center gap-2">
            <Input
              type="time"
              step={modelRoutingTimeStepSeconds}
              className="h-8 w-28 tabular-nums"
              aria-label={t("routingConditions.editor.timeStart")}
              disabled={disabled}
              value={phrase.start}
              onChange={(event) =>
                onChange({ ...phrase, start: event.target.value })
              }
            />
            <span className="text-muted-foreground">–</span>
            <Input
              type="time"
              step={modelRoutingTimeStepSeconds}
              className="h-8 w-28 tabular-nums"
              aria-label={t("routingConditions.editor.timeEnd")}
              disabled={disabled}
              value={phrase.end}
              onChange={(event) =>
                onChange({ ...phrase, end: event.target.value })
              }
            />
          </div>
          {complete && phrase.start > phrase.end ? (
            <p className="text-xs text-text-secondary">
              {t("routingConditions.editor.crossesMidnight", {
                start: phrase.start,
                end: phrase.end,
              })}
            </p>
          ) : null}
          {complete && phrase.start === phrase.end ? (
            <p className="text-xs text-text-secondary">
              {t("routingConditions.editor.singleInstant")}
            </p>
          ) : null}
          <p className="text-micro text-muted-foreground">
            {t("routingConditions.editor.timeNote")}
          </p>
          {footer}
        </>
      );
    }
  }
}

function FieldItem({
  field,
  dimensions,
  onPick,
}: {
  field: string;
  dimensions: readonly ClassifierDimension[];
  onPick: (field: string) => void;
}) {
  const classifier = isClassifierField(field);
  const custom = dimensions.some((dimension) => dimension.name === field);
  const hint = routingFieldHint(field, dimensions);
  return (
    <DropdownMenuItem
      className="items-start py-1"
      onSelect={() => onPick(field)}
      data-routing-field={field}
    >
      {classifier ? (
        <Brain
          aria-hidden="true"
          className="mt-0.5 size-3.5 text-violet-foreground"
        />
      ) : (
        <span aria-hidden="true" className="size-3.5 shrink-0" />
      )}
      <span className="grid min-w-0">
        <span className={custom ? "truncate font-mono" : "truncate"}>
          {routingFieldName(field)}
        </span>
        {hint ? (
          <span className="truncate text-micro text-muted-foreground">
            {hint}
          </span>
        ) : null}
      </span>
    </DropdownMenuItem>
  );
}

/**
 * 「添加条件」：常用字段按用途分组，自定义维度跟在任务类型后面，其余收进
 * 「更多条件」。字段说明在这里出现，替代原来那张只读的字段目录。
 */
export function RoutingFieldPicker({
  dimensions,
  onPick,
  onAfterPick,
  open,
  onOpenChange,
  children,
}: {
  dimensions: readonly ClassifierDimension[];
  onPick: (field: string) => void;
  /** Runs once the menu has closed after a pick: the moment to open an editor. */
  onAfterPick?: () => void;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** The trigger. */
  children: ReactNode;
}) {
  const t = useT();
  // A picked field opens its own editor, but only after the menu is gone: the
  // menu hands focus back to its trigger when its exit animation ends, and
  // that late focus dismisses any popover opened in the meantime.
  const picked = useRef(false);
  const pick = (field: string) => {
    picked.current = true;
    onPick(field);
  };
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>{children}</DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        className="max-h-[min(26rem,var(--radix-dropdown-menu-content-available-height))] w-72"
        aria-label={t("routingConditions.editor.pickField")}
        onCloseAutoFocus={(event) => {
          if (!picked.current) return;
          picked.current = false;
          event.preventDefault();
          onAfterPick?.();
        }}
      >
        {routingFieldGroups.map((group, index) => (
          <div key={group.id}>
            {index > 0 ? <DropdownMenuSeparator /> : null}
            <DropdownMenuLabel className="text-micro font-medium text-muted-foreground">
              {t(`routingConditions.groups.${group.id}`)}
            </DropdownMenuLabel>
            {[
              ...group.fields,
              ...(group.id === "classifier"
                ? dimensions.map((dimension) => dimension.name)
                : []),
            ].map((field) => (
              <FieldItem
                key={field}
                field={field}
                dimensions={dimensions}
                onPick={pick}
              />
            ))}
          </div>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuSub>
          <DropdownMenuSubTrigger className="text-muted-foreground">
            <span className="grid">
              <span>{t("routingConditions.groups.more")}</span>
              <span className="text-micro">
                {t("routingConditions.groups.moreHint", {
                  count: routingMoreFields.length,
                })}
              </span>
            </span>
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="w-64">
            {routingMoreFields.map((field) => (
              <FieldItem
                key={field}
                field={field}
                dimensions={dimensions}
                onPick={pick}
              />
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** 「添加规则」：六个常用场景，条件已经填好；最后是空白规则。 */
export function RoutingRuleTemplates({
  disabled = false,
  onPick,
  onAfterPick,
}: {
  disabled?: boolean;
  onPick: (template: RoutingTemplateID | "blank") => void;
  /** Runs once the menu has closed after a pick. */
  onAfterPick?: () => void;
}) {
  const t = useT();
  // The new row takes focus (its model, its first condition or the field
  // picker) once the menu is gone, so the menu cannot pull focus back to
  // 「添加规则」 after the row has claimed it.
  const picked = useRef(false);
  const pick = (template: RoutingTemplateID | "blank") => {
    picked.current = true;
    onPick(template);
  };
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild disabled={disabled}>
        <Button type="button" size="sm" variant="outline" data-routing-add-rule>
          <Plus aria-hidden="true" />
          {t("routingGroups.editor.addRule")}
          <ChevronDown aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        className="w-76"
        onCloseAutoFocus={(event) => {
          if (!picked.current) return;
          picked.current = false;
          event.preventDefault();
          onAfterPick?.();
        }}
      >
        <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">
          {t("routingGroups.editor.templatesLabel")}
        </DropdownMenuLabel>
        {routingTemplateIDs.map((template) => (
          <DropdownMenuItem
            key={template}
            className="items-start"
            onSelect={() => pick(template)}
            data-routing-template={template}
          >
            <span className="grid min-w-0 flex-1 gap-0.5">
              <span>
                {t(`routingGroups.editor.templates.${template}.title`)}
              </span>
              <span className="text-xs text-muted-foreground">
                {t(`routingGroups.editor.templates.${template}.detail`)}
              </span>
            </span>
            {template === "complexCoding" ? (
              <Brain
                aria-hidden="true"
                className="mt-0.5 size-3.5 text-violet-foreground"
              />
            ) : null}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => pick("blank")}
          data-routing-template="blank"
        >
          {t("routingGroups.editor.blankRule")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
