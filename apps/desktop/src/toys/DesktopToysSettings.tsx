// The desktop pets page body: how they move, then the big pet's card with
// its switch, its own settings and a picture of her playing them.
import { useEffect, useId } from "react";

import { ClientTypeIcon } from "@/components/ClientTypeIcon";
import { Activity, RefreshCw } from "@/components/icons";
import { OptionRow, optionRowLabel } from "@/components/OptionRow";
import { Panel, PanelHeader } from "@/components/Panel";
import { SettingSceneBody } from "@/components/SettingSceneBody";
import { StrengthMeter } from "@/components/StrengthMeter";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

import { useT } from "../i18n";
import {
  PET_CHARACTERS,
  TOY_LEVELS,
  TOY_SIZES,
  type DesktopToysPreferences,
  type PetCharacter,
  type ToySize,
} from "../preferences-model";
import { PetScene } from "./DesktopToyScenes";
import { cfg } from "./toy-config";

const LEVEL_BARS = { calm: 1, normal: 2, lively: 4 } as const;
const SIZE_DOTS = { small: 6, normal: 9, large: 12 } as const;
const SHOWN = ["orb", "status", "milestone"] as const;

/** The pet's size, as dots that grow. */
function SizeRow({
  disabled,
  id,
  onChange,
  value,
}: {
  disabled: boolean;
  id: string;
  onChange: (size: ToySize) => void;
  value: ToySize;
}) {
  const t = useT();
  return (
    <>
      <span className={optionRowLabel} id={id}>
        {t("desktopToys.size.label")}
      </span>
      <OptionRow
        disabled={disabled}
        labelId={id}
        onValueChange={onChange}
        options={TOY_SIZES.map((size) => ({
          value: size,
          id: `${id}-${size}`,
          label: t(`desktopToys.size.${size}`),
          icon: (selected: boolean) => (
            <span
              aria-hidden="true"
              className="grid size-3.5 place-items-center"
            >
              <span
                className={cn(
                  "rounded-[2px]",
                  selected ? "bg-primary" : "bg-primary/40",
                )}
                style={{ width: SIZE_DOTS[size], height: SIZE_DOTS[size] }}
              />
            </span>
          ),
        }))}
        value={value}
      />
    </>
  );
}

/** Who the big pet is: a girl of your choice, or whoever is busiest. */
function CharacterRow({
  disabled,
  id,
  onChange,
  value,
}: {
  disabled: boolean;
  id: string;
  onChange: (character: PetCharacter) => void;
  value: PetCharacter;
}) {
  const t = useT();
  return (
    <>
      <span className={optionRowLabel} id={id}>
        {t("desktopToys.character.label")}
      </span>
      <OptionRow
        disabled={disabled}
        labelId={id}
        onValueChange={onChange}
        options={PET_CHARACTERS.map((character) => ({
          value: character,
          id: `${id}-${character}`,
          label: t(`desktopToys.character.${character}`),
          icon: () =>
            character === "auto" ? (
              <RefreshCw aria-hidden="true" size={13} />
            ) : (
              <ClientTypeIcon clientType={character} decorative size={14} />
            ),
        }))}
        value={value}
      />
    </>
  );
}

/** A labelled row of checkboxes. */
function CheckRow({
  disabled,
  id,
  items,
  label,
}: {
  disabled: boolean;
  id: string;
  items: Array<{
    key: string;
    label: string;
    checked: boolean;
    onChange: (on: boolean) => void;
  }>;
  label: string;
}) {
  return (
    <>
      <span className={optionRowLabel} id={id}>
        {label}
      </span>
      <div
        aria-labelledby={id}
        className="flex min-h-8 flex-wrap items-center gap-x-4 gap-y-1"
        role="group"
      >
        {items.map((item) => (
          <Label className="text-xs" key={item.key}>
            <Checkbox
              checked={item.checked}
              disabled={disabled}
              onCheckedChange={(on) => item.onChange(on === true)}
            />
            {item.label}
          </Label>
        ))}
      </div>
    </>
  );
}

export function DesktopToysSettings({
  value,
  disabled,
  onChange,
}: {
  value: DesktopToysPreferences;
  disabled: boolean;
  onChange: (next: DesktopToysPreferences) => void;
}) {
  const t = useT();
  const id = useId();
  // The picture reads the settings as the pet does.
  useEffect(() => {
    cfg.prefs = value;
  }, [value]);
  const setPet = (patch: Partial<DesktopToysPreferences["pet"]>) =>
    onChange({ ...value, pet: { ...value.pet, ...patch } });
  const name = t("desktopToys.toys.pet.name");
  return (
    <div className="grid gap-3 pb-2 pr-1">
      <Panel className="min-w-0">
        <PanelHeader icon={<Activity size={16} />} iconTone="warm" size="sm">
          <h2 className="text-sm font-semibold">
            {t("desktopToys.motion.title")}
          </h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {t("desktopToys.motion.hint")}
          </p>
        </PanelHeader>
        <div className="@container px-3.5 py-3">
          <div className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-2 @[52rem]:grid-cols-[auto_minmax(0,1fr)_auto_minmax(0,1fr)]">
            <span className={optionRowLabel} id={`${id}-level`}>
              {t("desktopToys.motion.label")}
            </span>
            <OptionRow
              disabled={disabled}
              labelId={`${id}-level`}
              onValueChange={(level) => onChange({ ...value, level })}
              options={TOY_LEVELS.map((level) => ({
                value: level,
                id: `${id}-level-${level}`,
                label: t(`desktopToys.levels.${level}`),
                icon: (selected: boolean) => (
                  <StrengthMeter
                    active={selected}
                    level={LEVEL_BARS[level]}
                    size="sm"
                  />
                ),
              }))}
              value={value.level}
            />
            <CheckRow
              disabled={disabled}
              id={`${id}-show`}
              items={SHOWN.map((key) => ({
                key,
                label: t(`desktopToys.show.${key}`),
                checked: value[key],
                onChange: (on) => onChange({ ...value, [key]: on }),
              }))}
              label={t("desktopToys.show.label")}
            />
            <p className="col-start-2 text-xs leading-relaxed text-muted-foreground @[52rem]:col-span-3">
              {t(`desktopToys.levelText.${value.level}`)}
            </p>
          </div>
        </div>
      </Panel>
      <Panel className="min-w-0" data-toy="pet">
        <PanelHeader
          actions={
            <Switch
              aria-label={name}
              checked={value.on.pet}
              disabled={disabled}
              onCheckedChange={(pet) =>
                onChange({ ...value, on: { ...value.on, pet } })
              }
            />
          }
          size="sm"
        >
          <h2 className="text-sm font-semibold">{name}</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {t("desktopToys.toys.pet.hint")}
          </p>
        </PanelHeader>
        <SettingSceneBody scene={<PetScene />}>
          <div className="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-2">
            <SizeRow
              disabled={disabled}
              id={`${id}-pet-size`}
              onChange={(size) => setPet({ size })}
              value={value.pet.size}
            />
            <CharacterRow
              disabled={disabled}
              id={`${id}-pet-character`}
              onChange={(character) => setPet({ character })}
              value={value.pet.character}
            />
            <CheckRow
              disabled={disabled}
              id={`${id}-pet-play`}
              items={[
                {
                  key: "grab",
                  label: t("desktopToys.opts.grab"),
                  checked: value.pet.grab,
                  onChange: (grab) => setPet({ grab }),
                },
              ]}
              label={t("desktopToys.opts.play")}
            />
            <CheckRow
              disabled={disabled}
              id={`${id}-pet-bubble`}
              items={(["reply", "failover"] as const).map((event) => ({
                key: event,
                label: t(`desktopToys.opts.${event}`),
                checked: value.pet[event],
                onChange: (on) => setPet({ [event]: on }),
              }))}
              label={t("desktopToys.opts.bubble")}
            />
          </div>
        </SettingSceneBody>
      </Panel>
    </div>
  );
}
