import type { ReactNode } from "react";

import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { ModelChecklist } from "@/components/ModelChecklist";
import { ModelSelect } from "@/components/ModelSelect";
import { ChevronDown, SquarePen } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  selectTriggerClassName,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

import { configModels, type ClientCatalogEntry } from "./client-config-model";
import {
  autoFillClaude,
  modelService,
  piLimit,
  requiredEntry,
  type ModelCatalog,
  type ModelDraft,
} from "./client-delivery-model";
import type { ConversionEngineCapability } from "./core-model";
import { i18n } from "./i18n";
import { protocolLabel } from "./service-presets";

/** What Pi assumes for a model whose entry names no limit. */
const piDefaults = { contextWindow: 128000, maxTokens: 16384 } as const;

/** The Select value standing for a blank effort; Radix reserves "". */
const defaultEffort = "default";

/**
 * One client's model settings. Every edit is committed as it happens, so a
 * field removed by switching tabs loses nothing.
 */
export function ClientSettingsForm({
  entry,
  protocolOf,
  draft,
  list,
  candidates,
  catalog,
  catalogStatus,
  engine,
  efforts,
  disabled,
  onEdit,
}: {
  entry: ClientCatalogEntry;
  /** The protocol each model is written with. */
  protocolOf: (model: string) => string;
  draft: ModelDraft;
  /** The switchable models of a list client. */
  list: readonly string[];
  candidates: readonly string[];
  catalog: ModelCatalog;
  catalogStatus: "loading" | "ready" | "error";
  engine: ConversionEngineCapability | null | undefined;
  /** Codex's reasoning efforts for the chosen model. */
  efforts: readonly string[];
  disabled: boolean;
  onEdit: (patch: Partial<ModelDraft>) => void;
}) {
  const t = i18n.t.bind(i18n);
  const client = entry.id;
  const label = entry.name;
  const entryOf = (model: string) =>
    model
      ? requiredEntry(catalog, model, protocolOf(model), engine)
      : undefined;
  const entryBadge = (model: string) =>
    entryOf(model) ? (
      <Badge
        variant="secondary"
        className="shrink-0 bg-warning-wash text-warning-foreground"
      >
        {t("clientSetup.entryRequired")}
      </Badge>
    ) : null;
  const optionAdornment = (model: string) => {
    const badge = entryBadge(model);
    return badge ? <span className="ml-auto">{badge}</span> : null;
  };
  const catalogHint =
    catalogStatus === "loading"
      ? t("clientSetup.modelsLoading")
      : catalogStatus === "error"
        ? t("clientSetup.modelsFailed")
        : t("clientSetup.modelsListed", {
            protocol: entry.protocols
              .map(protocolLabel)
              .join(t("agentDebug.partsSeparator")),
            count: candidates.length,
          });

  const modelField = (
    field:
      | "model"
      | "haikuModel"
      | "sonnetModel"
      | "opusModel"
      | "fableModel"
      | "subagentModel",
    fieldLabel: string,
    placeholder: string,
    className?: string,
  ) => (
    <Field
      className={className}
      htmlFor={`client-setup-${field}`}
      label={fieldLabel}
    >
      <ModelSelect
        aria-label={fieldLabel}
        disabled={disabled}
        id={`client-setup-${field}`}
        maxLength={256}
        onValueChange={(value) => onEdit({ [field]: value })}
        optionAdornment={optionAdornment}
        options={candidates}
        placeholder={placeholder}
        value={draft[field]}
      />
    </Field>
  );

  // Each provider needing an entry is named once, for every model written.
  const written =
    client === "claude"
      ? configModels({
          model: draft.model,
          haikuModel: draft.haikuModel,
          sonnetModel: draft.sonnetModel,
          opusModel: draft.opusModel,
          fableModel: draft.fableModel,
          subagentModel: draft.subagentModel,
        })
      : [
          ...new Set(
            [draft.model, ...(entry.list ? list : [])].filter(Boolean),
          ),
        ];
  const entries = new Map(
    written.flatMap((model) => {
      const protocol = protocolOf(model.trim());
      const needed = entryOf(model.trim());
      return needed
        ? [
            [
              `${needed.service.id}\n${protocol}`,
              { ...needed, protocol },
            ] as const,
          ]
        : [];
    }),
  );
  const entryNotes = [...entries].map(([key, needed]) => (
    <FormMessage key={key} tone="notice">
      {t("clientSetup.entryWillEnable", {
        service: needed.service.name,
        protocol: protocolLabel(needed.protocol),
        target: protocolLabel(needed.target),
      })}
    </FormMessage>
  ));

  const checklist = (
    <ModelChecklist
      disabled={disabled}
      items={candidates.map((model) => ({
        id: model,
        detail: modelService(catalog, model, protocolOf(model), engine)?.name,
        locked: model === draft.model.trim(),
        adornment: (
          <>
            {model === draft.model.trim() ? (
              <Badge variant="accent" className="shrink-0">
                {t("clientSetup.defaultTag")}
              </Badge>
            ) : null}
            {entryBadge(model)}
          </>
        ),
      }))}
      label={t("clientSetup.switchable", { client: label })}
      onSelectedChange={(models) => onEdit({ models })}
      selected={list}
    />
  );

  let body: ReactNode;
  if (client === "claude") {
    body = (
      <>
        <div className="grid gap-3">
          <div className="flex items-center gap-2">
            <span className="flex-1 text-xs font-medium text-text-secondary">
              {t("clientSetup.models")}
            </span>
            <Button
              disabled={disabled || candidates.length === 0}
              onClick={() => onEdit(autoFillClaude(candidates))}
              size="xs"
              type="button"
              variant="ghost"
            >
              <SquarePen aria-hidden="true" />
              {t("clientSetup.autoFill")}
            </Button>
          </div>
          <div className="grid gap-3 @[480px]/pane:grid-cols-2">
            {modelField(
              "model",
              t("clientSetup.defaultModel"),
              t("clientSetup.blankSkipped"),
            )}
            {modelField(
              "haikuModel",
              t("clientSetup.haikuModel"),
              t("clientSetup.blankSkipped"),
            )}
            {modelField(
              "sonnetModel",
              t("clientSetup.sonnetModel"),
              t("clientSetup.blankSkipped"),
            )}
            {modelField(
              "opusModel",
              t("clientSetup.opusModel"),
              t("clientSetup.blankSkipped"),
            )}
            {modelField(
              "fableModel",
              t("clientSetup.fableModel"),
              t("clientSetup.blankSkipped"),
            )}
            {modelField(
              "subagentModel",
              t("clientSetup.subagentModel"),
              t("clientSetup.blankFollowsDefault"),
            )}
          </div>
          <p className="text-xs text-muted-foreground">{catalogHint}</p>
        </div>
      </>
    );
  } else if (client === "codex") {
    body = (
      <div className="grid gap-1.5">
        <div className="grid gap-3 @[480px]/pane:grid-cols-2">
          {modelField(
            "model",
            t("clientSetup.defaultModel"),
            t("clientSetup.modelPlaceholder"),
          )}
          <Field
            htmlFor="client-setup-reasoningEffort"
            label={t("clientSetup.reasoningEffort")}
          >
            <Select
              disabled={disabled}
              onValueChange={(value) => {
                // Radix reports a blank while its options settle.
                if (!value) return;
                onEdit({
                  reasoningEffort: value === defaultEffort ? "" : value,
                });
              }}
              value={draft.reasoningEffort || defaultEffort}
            >
              <SelectTrigger
                className="w-full"
                id="client-setup-reasoningEffort"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={defaultEffort}>
                  {t("clientSetup.effortDefault")}
                </SelectItem>
                {efforts.map((effort) => (
                  <SelectItem key={effort} value={effort}>
                    {effort}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </div>
        <p className="text-xs text-muted-foreground">{catalogHint}</p>
      </div>
    );
  } else if (!entry.list) {
    body = (
      <div className="grid gap-1.5">
        {modelField(
          "model",
          t("clientSetup.defaultModel"),
          t("clientSetup.modelPlaceholder"),
          "max-w-[340px]",
        )}
        <p className="text-xs text-muted-foreground">{catalogHint}</p>
      </div>
    );
  } else if (client === "pi") {
    const limitsInvalid =
      piLimit(draft.contextWindow) === null ||
      piLimit(draft.maxTokens) === null;
    body = (
      <div className="grid gap-1.5">
        <div className="grid gap-3 @[480px]/pane:grid-cols-2">
          {modelField(
            "model",
            t("clientSetup.defaultModel"),
            t("clientSetup.modelPlaceholder"),
          )}
          <Field group label={t("clientSetup.switchable", { client: label })}>
            <Popover>
              <PopoverTrigger asChild>
                <Button
                  aria-label={t("clientSetup.switchable", { client: label })}
                  className={cn(
                    selectTriggerClassName,
                    "h-8 w-full font-normal hover:bg-card hover:text-inherit",
                  )}
                  disabled={disabled}
                  type="button"
                  variant="outline"
                >
                  <span className="min-w-0 truncate">
                    {t("clientSetup.modelCount", {
                      count: new Set(
                        [...list, draft.model.trim()].filter(Boolean),
                      ).size,
                    })}
                  </span>
                  <ChevronDown
                    aria-hidden="true"
                    className="size-4 opacity-50"
                  />
                </Button>
              </PopoverTrigger>
              <PopoverContent
                align="start"
                className="z-110 w-[min(560px,calc(100vw-48px))] p-2"
              >
                {checklist}
              </PopoverContent>
            </Popover>
          </Field>
          {(["contextWindow", "maxTokens"] as const).map((key) => (
            <Field
              key={key}
              htmlFor={`client-setup-${key}`}
              label={t(`clientSetup.${key}`)}
            >
              <Input
                aria-invalid={piLimit(draft[key]) === null}
                autoComplete="off"
                disabled={disabled}
                id={`client-setup-${key}`}
                inputMode="numeric"
                maxLength={9}
                onChange={(event) => onEdit({ [key]: event.target.value })}
                placeholder={t("clientSetup.piDefault", {
                  tokens: piDefaults[key],
                })}
                value={draft[key]}
              />
            </Field>
          ))}
        </div>
        {limitsInvalid ? (
          <FormMessage tone="error">
            {t("clientSetup.limitInvalid")}
          </FormMessage>
        ) : (
          <p className="text-xs text-muted-foreground">{catalogHint}</p>
        )}
      </div>
    );
  } else {
    body = (
      <>
        {modelField(
          "model",
          t("clientSetup.defaultModel"),
          t("clientSetup.modelPlaceholder"),
          "max-w-[340px]",
        )}
        <div className="grid gap-1.5">
          <div className="flex items-center gap-2">
            <span className="flex-1 text-xs font-medium text-text-secondary">
              {t("clientSetup.switchable", { client: label })}
            </span>
            {i18n.exists(`clientSetup.switchHint.${client}`) ? (
              <span className="text-micro text-muted-foreground">
                {t(`clientSetup.switchHint.${client}`)}
              </span>
            ) : null}
          </div>
          {checklist}
          {catalogStatus !== "ready" ? (
            <p className="text-xs text-muted-foreground">{catalogHint}</p>
          ) : null}
        </div>
      </>
    );
  }
  return (
    <>
      {body}
      {entryNotes}
    </>
  );
}
