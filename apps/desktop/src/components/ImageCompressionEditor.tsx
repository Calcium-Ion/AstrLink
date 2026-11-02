import { useId } from "react";
import type {
  ImageCompression,
  ImageCompressionMode,
} from "@/failure-policy-model";
import { imageCompressionModes } from "@/failure-policy-model";
import { useT } from "@/i18n";
import { HelpPopover } from "./HelpPopover";
import { ImageCompressionScene } from "./ImageCompressionScene";
import {
  FoldHorizontal,
  ImageDown,
  ShieldCheck,
  type AnimatedIcon,
} from "./icons";
import { NumberInput } from "./NumberField";
import { OptionRow, optionRowLabel } from "./OptionRow";
import { Panel, PanelHeader } from "./Panel";
import { SettingSceneBody } from "./SettingSceneBody";
import { StrengthMeter } from "./StrengthMeter";
import { Switch } from "./ui/switch";

const modeIcons = {
  lossless: ShieldCheck,
  lossy: FoldHorizontal,
} as const satisfies Record<ImageCompressionMode, AnimatedIcon>;

// Any quality from 1 through 100 can be typed; one that matches no preset
// leaves every preset unselected.
const qualityPresets = [
  { value: "small", quality: 60 },
  { value: "balanced", quality: 75 },
  { value: "sharp", quality: 85 },
  { value: "fine", quality: 95 },
] as const;
type QualityPreset = (typeof qualityPresets)[number]["value"];

export function ImageCompressionEditor({
  value,
  disabled,
  onChange,
}: {
  value: ImageCompression;
  disabled: boolean;
  onChange: (value: ImageCompression) => void;
}) {
  const t = useT();
  const id = useId();
  const change = (patch: Partial<ImageCompression>) =>
    onChange({ ...value, ...patch });
  const preset =
    qualityPresets.find((option) => option.quality === value.quality)?.value ??
    "";
  return (
    <Panel className="shrink-0" data-testid="image-compression-settings">
      <PanelHeader
        icon={<ImageDown size={16} />}
        iconTone="success"
        size="sm"
        actions={
          <Switch
            size="default"
            aria-label={t("imageCompression.enable")}
            checked={value.enabled}
            disabled={disabled}
            onCheckedChange={(enabled) => change({ enabled })}
          />
        }
      >
        <div className="flex items-center gap-1">
          <h2 className="text-sm font-semibold">
            {t("imageCompression.title")}
          </h2>
          <HelpPopover label={t("imageCompression.help")}>
            <p className="m-0 text-xs leading-relaxed text-muted-foreground">
              {t("imageCompression.demo.notes")}
            </p>
          </HelpPopover>
        </div>
        <p className="mt-1 text-xs text-muted-foreground">
          {t("imageCompression.hint")}
        </p>
      </PanelHeader>
      <SettingSceneBody scene={<ImageCompressionScene value={value} />}>
        <div className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-2">
          <span id={`${id}-mode`} className={optionRowLabel}>
            {t("imageCompression.mode")}
          </span>
          <OptionRow
            labelId={`${id}-mode`}
            value={value.mode}
            disabled={disabled}
            onValueChange={(mode) => change({ mode })}
            options={imageCompressionModes.map((option) => {
              const Icon = modeIcons[option];
              return {
                value: option,
                id: `${id}-mode-${option}`,
                label: t(`imageCompression.modes.${option}`),
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
          {value.mode === "lossy" ? (
            <>
              <span id={`${id}-quality`} className={optionRowLabel}>
                {t("imageCompression.quality")}
              </span>
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <OptionRow<QualityPreset | "">
                  labelId={`${id}-quality`}
                  value={preset}
                  disabled={disabled}
                  onValueChange={(next) => {
                    const option = qualityPresets.find(
                      (item) => item.value === next,
                    );
                    if (option) change({ quality: option.quality });
                  }}
                  options={qualityPresets.map((option, index) => ({
                    value: option.value,
                    id: `${id}-quality-${option.value}`,
                    label: t(`imageCompression.qualities.${option.value}`),
                    icon: (selected: boolean) => (
                      <StrengthMeter
                        active={selected}
                        level={index + 1}
                        size="sm"
                      />
                    ),
                  }))}
                />
                <div className="w-24">
                  <NumberInput
                    className="h-8"
                    label={t("imageCompression.qualityValue")}
                    unit="/ 100"
                    min={1}
                    max={100}
                    disabled={disabled}
                    value={value.quality}
                    onChange={(quality) =>
                      change({
                        quality: Math.min(
                          100,
                          Math.max(1, Math.round(quality)),
                        ),
                      })
                    }
                  />
                </div>
              </div>
            </>
          ) : null}
          <p className="col-start-2 text-xs text-muted-foreground">
            {t(`imageCompression.modeHints.${value.mode}`)}
          </p>
        </div>
      </SettingSceneBody>
    </Panel>
  );
}
