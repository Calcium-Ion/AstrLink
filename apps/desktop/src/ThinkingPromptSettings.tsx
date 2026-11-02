import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";

import { FilterSelect } from "./components/FilterSelect";
import { OptionRow, optionRowLabel } from "./components/OptionRow";
import { StrengthMeter } from "./components/StrengthMeter";
import {
  Activity,
  ArrowUp,
  ArrowDown,
  Brain,
  MessageSquare,
  Pin,
  SlidersHorizontal,
  type AnimatedIcon,
} from "./components/icons";
import { Field } from "./components/Field";
import { HelpPopover } from "./components/HelpPopover";
import { Panel, PanelHeader } from "./components/Panel";
import { SettingSceneBody } from "./components/SettingSceneBody";
import { Switch } from "./components/ui/switch";
import { cn } from "./lib/utils";
import { Textarea } from "./components/ui/textarea";
import {
  adaptivePresets,
  adaptiveRange,
  maxSystemPromptLength,
  reasoningLevels,
  reasoningModes,
  type ReasoningLevel,
  type ReasoningMode,
  type RoutingSettings,
} from "./failure-policy-model";
import { useT } from "./i18n";
import {
  PromptScene,
  ThinkingScene,
  thinkingSceneText,
} from "./ThinkingPromptDemos";

const modeIcons = {
  passthrough: SlidersHorizontal,
  fixed: Pin,
  adaptive: Activity,
} as const satisfies Record<ReasoningMode, AnimatedIcon>;

const promptFields = [
  {
    key: "system_prompt_prefix",
    Icon: ArrowUp,
    label: "routing.promptPrefix",
    placeholder: "routing.promptPrefixPlaceholder",
  },
  {
    key: "system_prompt_suffix",
    Icon: ArrowDown,
    label: "routing.promptSuffix",
    placeholder: "routing.promptSuffixPlaceholder",
  },
] as const;

const rank = (level: ReasoningLevel) => reasoningLevels.indexOf(level);

/** Whitespace-only text adds nothing to a prompt, so it is saved as empty. */
function committedPrompt(text: string) {
  return text.trim() === "" ? "" : text;
}

/**
 * Prompt text edited locally and committed on blur, so partly typed text is
 * never autosaved. Enter inserts a newline. Text is saved as typed.
 */
function SystemPromptField({
  label,
  placeholder,
  value,
  onCommit,
  Icon,
}: {
  Icon: typeof ArrowUp;
  label: string;
  placeholder: string;
  value: string;
  onCommit: (value: string) => void;
}) {
  const id = useId();
  const counterId = `${id}-count`;
  const [text, setText] = useState(value);
  useEffect(() => setText(value), [value]);
  const latest = useRef({ text, value, onCommit });
  useLayoutEffect(() => {
    latest.current = { text, value, onCommit };
  });
  // Switching tabs removes a focused field without a blur in WebKit, so an
  // edit still pending at unmount is committed then.
  useEffect(
    () => () => {
      const { text, value, onCommit } = latest.current;
      const next = committedPrompt(text);
      if (text !== value && next !== value) onCommit(next);
    },
    [],
  );
  const commit = () => {
    if (text === value) return;
    const next = committedPrompt(text);
    setText(next);
    if (next !== value) onCommit(next);
  };
  return (
    <Field
      className="w-full"
      htmlFor={id}
      label={
        <span className="flex items-center justify-between gap-2 text-foreground">
          <span id={`${id}-label`} className="flex items-center gap-2">
            <Icon aria-hidden="true" size={16} className="text-primary" />
            {label}
          </span>
          <span
            id={counterId}
            aria-live="polite"
            aria-atomic="true"
            className={cn(
              "text-right text-xs font-normal tabular-nums text-muted-foreground",
              text.length >= maxSystemPromptLength * 0.9 &&
                "text-warning-foreground",
              text.length >= maxSystemPromptLength && "text-destructive",
            )}
          >
            {text.length} / {maxSystemPromptLength}
          </span>
        </span>
      }
    >
      <Textarea
        id={id}
        className="h-11 min-h-11 max-h-48 resize-y overflow-y-auto overscroll-contain field-sizing-fixed"
        aria-labelledby={`${id}-label`}
        aria-describedby={counterId}
        value={text}
        maxLength={maxSystemPromptLength}
        placeholder={placeholder}
        onChange={(event) => setText(event.target.value)}
        onBlur={commit}
      />
    </Field>
  );
}

export function ThinkingPromptSettings({
  value,
  onChange,
}: {
  value: RoutingSettings;
  onChange: (value: RoutingSettings) => void;
}) {
  const t = useT();
  const id = useId();
  const mode = value.reasoning_mode ?? "passthrough";
  const level = value.reasoning_level ?? "medium";
  const preset = value.adaptive_preset ?? "everyday";
  const min = value.adaptive_min ?? "low";
  const max = value.adaptive_max ?? "max";
  const enabled = value.system_prompt_enabled ?? false;
  const levelOptions = reasoningLevels.map((option) => ({
    value: option,
    label: t(`routing.thinkingLevels.${option}`),
  }));
  return (
    <>
      <Panel data-testid="thinking-settings">
        <PanelHeader icon={<Brain size={16} />} iconTone="warm" size="sm">
          <div className="flex items-center gap-1">
            <h2 className="text-sm font-semibold">
              {t("routing.thinkingTitle")}
            </h2>
            <HelpPopover label={t("routing.thinkingHelp")}>
              <p className="m-0 text-xs leading-relaxed text-muted-foreground">
                {t("routing.thinkingNotes")}
              </p>
            </HelpPopover>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("routing.thinkingHint")}
          </p>
        </PanelHeader>
        <SettingSceneBody scene={<ThinkingScene settings={value} />}>
          <div className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-2">
            <span id={`${id}-mode`} className={optionRowLabel}>
              {t("routing.thinkingModeLabel")}
            </span>
            <OptionRow
              labelId={`${id}-mode`}
              value={mode}
              onValueChange={(reasoning_mode) =>
                onChange({ ...value, reasoning_mode })
              }
              options={reasoningModes.map((option) => {
                const Icon = modeIcons[option];
                return {
                  value: option,
                  id: `${id}-mode-${option}`,
                  label: t(`routing.thinkingModes.${option}`),
                  icon: (selected: boolean) => (
                    <Icon
                      size={14}
                      className={
                        selected ? "text-primary" : "text-muted-foreground"
                      }
                    />
                  ),
                };
              })}
            />
            {mode === "fixed" ? (
              <>
                <span id={`${id}-level`} className={optionRowLabel}>
                  {t("routing.thinkingLevelLabel")}
                </span>
                <OptionRow
                  labelId={`${id}-level`}
                  value={level}
                  onValueChange={(reasoning_level) =>
                    onChange({ ...value, reasoning_level })
                  }
                  options={reasoningLevels.map((option, index) => ({
                    value: option,
                    id: `${id}-level-${option}`,
                    label: t(`routing.thinkingLevels.${option}`),
                    icon: (selected: boolean) => (
                      <StrengthMeter
                        active={selected}
                        level={index}
                        size="sm"
                      />
                    ),
                  }))}
                />
              </>
            ) : null}
            {mode === "adaptive" ? (
              <>
                <span id={`${id}-range`} className={optionRowLabel}>
                  {t("routing.thinkingRangeLabel")}
                </span>
                <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <OptionRow
                    labelId={`${id}-range`}
                    value={preset}
                    onValueChange={(adaptive_preset) =>
                      onChange({ ...value, adaptive_preset })
                    }
                    options={adaptivePresets.map((option) => {
                      const [low, high] = adaptiveRange({
                        ...value,
                        adaptive_preset: option,
                      });
                      return {
                        value: option,
                        id: `${id}-range-${option}`,
                        label: t(`routing.thinkingPresets.${option}`),
                        icon: (selected: boolean) => (
                          <StrengthMeter
                            active={selected}
                            from={Math.max(1, rank(low))}
                            level={rank(high)}
                            size="sm"
                          />
                        ),
                      };
                    })}
                  />
                  {preset === "custom" ? (
                    <span className="flex items-center gap-1.5">
                      {/* Moving one end past the other carries it along, so the saved range stays in order. */}
                      <FilterSelect
                        ariaLabel={t("routing.thinkingRangeMin")}
                        label={t("routing.thinkingRangeFrom")}
                        options={levelOptions}
                        value={min}
                        onChange={(next) => {
                          const adaptive_min = reasoningLevels.find(
                            (option) => option === next,
                          );
                          if (!adaptive_min) return;
                          onChange({
                            ...value,
                            adaptive_min,
                            ...(rank(adaptive_min) > rank(max) && {
                              adaptive_max: adaptive_min,
                            }),
                          });
                        }}
                      />
                      <FilterSelect
                        ariaLabel={t("routing.thinkingRangeMax")}
                        label={t("routing.thinkingRangeTo")}
                        options={levelOptions}
                        value={max}
                        onChange={(next) => {
                          const adaptive_max = reasoningLevels.find(
                            (option) => option === next,
                          );
                          if (!adaptive_max) return;
                          onChange({
                            ...value,
                            adaptive_max,
                            ...(rank(adaptive_max) < rank(min) && {
                              adaptive_min: adaptive_max,
                            }),
                          });
                        }}
                      />
                    </span>
                  ) : null}
                </div>
              </>
            ) : null}
            <p className="col-start-2 text-xs leading-relaxed text-muted-foreground">
              {thinkingSceneText(value, t)}
            </p>
          </div>
        </SettingSceneBody>
      </Panel>
      <Panel data-testid="global-prompt-settings">
        <PanelHeader
          icon={<MessageSquare size={16} />}
          size="sm"
          actions={
            <Switch
              size="default"
              checked={enabled}
              aria-label={t("routing.promptEnabled")}
              onCheckedChange={(system_prompt_enabled) =>
                onChange({ ...value, system_prompt_enabled })
              }
            />
          }
        >
          <div className="flex items-center gap-1">
            <h2 className="text-sm font-semibold">
              {t("routing.promptTitle")}
            </h2>
            <HelpPopover label={t("routing.promptHelp")}>
              <p className="m-0 text-xs leading-relaxed text-muted-foreground">
                {t("routing.promptNotes")}
              </p>
            </HelpPopover>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("routing.promptHint")}
          </p>
        </PanelHeader>
        <SettingSceneBody
          scene={
            <PromptScene
              enabled={enabled}
              prefix={value.system_prompt_prefix ?? ""}
              suffix={value.system_prompt_suffix ?? ""}
            />
          }
        >
          <div className="grid gap-2">
            {promptFields.map(({ key, label, placeholder, Icon }) => (
              <SystemPromptField
                key={key}
                Icon={Icon}
                label={t(label)}
                placeholder={t(placeholder)}
                value={value[key] ?? ""}
                onCommit={(next) => onChange({ ...value, [key]: next })}
              />
            ))}
          </div>
        </SettingSceneBody>
      </Panel>
    </>
  );
}
