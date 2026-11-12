import { useId } from "react";
import type {
  ImageCompression,
  ImageCompressionMode,
} from "@/failure-policy-model";
import { imageCompressionModes } from "@/failure-policy-model";
import { currentLocale, useT } from "@/i18n";
import {
  imageCompressionSavedBytes,
  imageCompressionSavedShare,
  type ImageCompressionTotals,
} from "@/image-compression-totals-model";
import { formatImageBytes } from "@/request-trajectory-model";
import { HelpPopover } from "./HelpPopover";
import { ImageCompressionScene } from "./ImageCompressionScene";
import { Metric, MetricGroup } from "./Metric";
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
  totals,
}: {
  value: ImageCompression;
  disabled: boolean;
  onChange: (value: ImageCompression) => void;
  /** What compression has saved so far; null until Core answers. */
  totals?: ImageCompressionTotals | null;
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
            <p className="m-0 mt-2 text-xs leading-relaxed text-muted-foreground">
              {t("imageCompression.totals.helpTotals")}
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
      {totals && (totals.requests > 0 || value.enabled) ? (
        <ImageCompressionTotalsBand totals={totals} />
      ) : null}
    </Panel>
  );
}

/** What compression has saved since counting began, under the settings. */
function ImageCompressionTotalsBand({
  totals,
}: {
  totals: ImageCompressionTotals;
}) {
  const t = useT();
  const date = new Intl.DateTimeFormat(currentLocale(), {
    year: "numeric",
    month: "long",
    day: "numeric",
  }).format(new Date(totals.since));
  if (totals.requests === 0) {
    return (
      <p
        className="border-t px-3.5 py-2.5 text-xs text-muted-foreground"
        data-testid="image-compression-totals"
      >
        {t("imageCompression.totals.sinceEmpty", { date })}
      </p>
    );
  }
  const share = imageCompressionSavedShare(totals);
  return (
    <div
      className="grid gap-2 border-t px-3.5 py-3"
      data-testid="image-compression-totals"
    >
      <MetricGroup className="overflow-hidden rounded-md border">
        <Metric
          detail={
            share
              ? t("imageCompression.totals.savedShare", { percent: share })
              : null
          }
          emphasis
          label={
            <span title={t("imageCompression.totals.savedHint")}>
              {t("imageCompression.totals.saved")}
            </span>
          }
          size="sm"
          value={formatImageBytes(imageCompressionSavedBytes(totals))}
        />
        <Metric
          detail={t("imageCompression.totals.imagesOf", {
            count: totals.images,
          })}
          label={t("imageCompression.totals.compressed")}
          size="sm"
          value={t("imageCompression.totals.compressedValue", {
            count: totals.compressed,
          })}
        />
        <Metric
          detail={t("imageCompression.totals.requests", {
            count: totals.requests,
          })}
          label={t("imageCompression.totals.original")}
          size="sm"
          value={formatImageBytes(totals.original_bytes)}
        />
        <Metric
          label={t("imageCompression.totals.sent")}
          size="sm"
          value={formatImageBytes(totals.sent_bytes)}
        />
      </MetricGroup>
      <p className="text-xs text-muted-foreground">
        {t("imageCompression.totals.since", { date })}
      </p>
    </div>
  );
}
