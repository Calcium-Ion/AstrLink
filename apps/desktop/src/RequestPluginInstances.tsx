import { useCallback, useEffect, useId, useMemo, useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { IconButton } from "@/components/IconButton";
import { Plus, SquarePen, X } from "@/components/icons";
import { Panel } from "@/components/Panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";

import {
  dryRunRequestPlugin,
  getPluginHostSettings,
  updatePluginHostSettings,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useLocale, useT } from "./i18n";
import type {
  PluginDetail,
  PluginHostSettings,
  RequestPluginDryRun,
  RequestPluginInstance,
  RequestPluginSample,
} from "./plugin-model";

/** A field of a request plugin's configSchema, as far as the form reads it. */
interface SchemaField {
  type?: string;
  title?: unknown;
  description?: unknown;
  default?: unknown;
  examples?: unknown[];
  enum?: (string | number)[];
  enumTitles?: unknown[];
  format?: string;
  items?: SchemaField;
  properties?: Record<string, SchemaField>;
  visibleWhen?: Record<string, unknown>;
  minimum?: number;
  maximum?: number;
}

/** A text of the plugin's in the page's language, else English. */
export function localized(text: unknown, locale: string): string {
  if (typeof text === "string") return text;
  if (!text || typeof text !== "object") return "";
  const texts = text as Record<string, string>;
  const primary = locale.split("-")[0];
  return (
    texts[locale] ??
    texts[primary] ??
    (primary === "zh" ? texts["zh-CN"] : undefined) ??
    texts.en ??
    Object.values(texts)[0] ??
    ""
  );
}

export function schemaProperties(schema: unknown): [string, SchemaField][] {
  if (!schema || typeof schema !== "object") return [];
  const properties = (schema as { properties?: Record<string, SchemaField> })
    .properties;
  return properties ? Object.entries(properties) : [];
}

/** The config an editor starts from: each default, then what is saved. */
export function startingConfig(
  schema: unknown,
  saved: Record<string, unknown> | undefined,
): Record<string, unknown> {
  const config: Record<string, unknown> = {};
  for (const [name, field] of schemaProperties(schema)) {
    if (field.default !== undefined) config[name] = field.default;
  }
  return { ...config, ...saved };
}

/**
 * The fields of a plugin's config schema, those visibleWhen shows, each
 * by its type and format.
 */
export function ConfigSchemaFields({
  idPrefix,
  onChange,
  onInvalid,
  schema,
  value,
}: {
  idPrefix: string;
  onChange: (key: string, value: unknown) => void;
  onInvalid?: (key: string, invalid: boolean) => void;
  schema: unknown;
  value: Record<string, unknown>;
}) {
  return (
    <>
      {schemaProperties(schema)
        .filter(([, field]) => visible(field, value))
        .map(([key, field]) => (
          <ConfigField
            field={field}
            id={`${idPrefix}-${key}`}
            key={key}
            onChange={(next) => onChange(key, next)}
            onInvalid={(bad) => onInvalid?.(key, bad)}
            value={value[key]}
          />
        ))}
    </>
  );
}

/** Whether a field shows: every sibling it names holds one of its values. */
function visible(field: SchemaField, config: Record<string, unknown>): boolean {
  for (const [sibling, values] of Object.entries(field.visibleWhen ?? {})) {
    const allowed = Array.isArray(values) ? values : [values];
    if (!allowed.includes(config[sibling] as never)) return false;
  }
  return true;
}

/**
 * The request processing instances of one plugin: each a configured use
 * that checks and changes requests, its answers or hears of them, for every
 * model or the models it lists. They run in the order they were added.
 */
export function RequestPluginInstances({
  detail,
  disabled,
}: {
  detail: PluginDetail;
  disabled?: boolean;
}) {
  const t = useT();
  const [host, setHost] = useState<PluginHostSettings | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<{
    index: number | null;
    draft: RequestPluginInstance;
  } | null>(null);
  const [removing, setRemoving] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const schema = detail.request?.config_schema;

  useEffect(() => {
    let current = true;
    getPluginHostSettings()
      .then((settings) => {
        if (current) setHost(settings);
      })
      .catch((cause: unknown) => {
        if (current)
          setError(
            controlErrorMessage(cause, t("plugins.requestPlugins.loadFailed")),
          );
      });
    return () => {
      current = false;
    };
  }, [t]);

  const save = useCallback(
    async (list: RequestPluginInstance[]): Promise<string | null> => {
      setSaving(true);
      try {
        setHost(await updatePluginHostSettings({ request_plugins: list }));
        setError(null);
        return null;
      } catch (cause) {
        return controlErrorMessage(
          cause,
          t("plugins.requestPlugins.saveFailed"),
        );
      } finally {
        setSaving(false);
      }
    },
    [t],
  );

  const all = host?.request_plugins ?? [];
  const mine = all
    .map((instance, index) => ({ instance, index }))
    .filter(({ instance }) => instance.plugin === detail.key);

  const toggle = async (index: number, enabled: boolean) => {
    const list = all.map((instance, at) =>
      at === index ? { ...instance, enabled } : instance,
    );
    const failed = await save(list);
    if (failed) setError(failed);
  };

  return (
    <Panel className="grid gap-3 p-4" data-testid="request-plugin-instances">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
        <div className="grid min-w-0 gap-1">
          <h3 className="text-sm font-semibold">
            {t("plugins.requestPlugins.title")}
          </h3>
          <span className="flex flex-wrap gap-1">
            {(detail.request?.stages ?? []).map((stage) => (
              <Badge
                className="px-1.5 py-0 font-normal"
                key={stage}
                variant="outline"
              >
                {t(`plugins.requestPlugins.stages.${stage}`, {
                  defaultValue: stage,
                })}
              </Badge>
            ))}
          </span>
        </div>
        <Button
          disabled={disabled || host === null || saving}
          onClick={() =>
            setEditing({
              index: null,
              draft: {
                name: detail.name,
                plugin: detail.key,
                enabled: true,
                models: [],
                config: startingConfig(schema, undefined),
              },
            })
          }
          size="sm"
          type="button"
          variant="outline"
        >
          <Plus />
          {t("plugins.requestPlugins.add")}
        </Button>
      </div>
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {host !== null && mine.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {t("plugins.requestPlugins.empty")}
        </p>
      ) : null}
      <ul className="grid gap-2">
        {mine.map(({ instance, index }) => (
          <li
            className="flex min-w-0 items-center gap-3 rounded-md border px-3 py-2"
            data-testid="request-plugin-instance"
            key={instance.id ?? index}
          >
            <div className="grid min-w-0 flex-1 gap-0.5">
              <span className="truncate text-sm font-medium">
                {instance.name}
              </span>
              <span className="truncate text-micro text-muted-foreground">
                {instance.models.length === 0
                  ? t("plugins.requestPlugins.allModels")
                  : instance.models.join(", ")}
              </span>
            </div>
            <Switch
              aria-label={t("plugins.requestPlugins.enabled")}
              checked={instance.enabled}
              disabled={disabled || saving}
              onCheckedChange={(checked) => void toggle(index, checked)}
            />
            <IconButton
              disabled={disabled || saving}
              label={t("plugins.requestPlugins.edit")}
              onClick={() =>
                setEditing({
                  index,
                  draft: {
                    ...instance,
                    config: startingConfig(schema, instance.config),
                  },
                })
              }
              type="button"
              variant="ghost"
            >
              <SquarePen />
            </IconButton>
            <IconButton
              disabled={disabled || saving}
              label={t("plugins.requestPlugins.remove")}
              onClick={() => setRemoving(index)}
              type="button"
              variant="ghost"
            >
              <X />
            </IconButton>
          </li>
        ))}
      </ul>
      {editing ? (
        <InstanceEditor
          initial={editing.draft}
          isNew={editing.index === null}
          onClose={() => setEditing(null)}
          pluginKey={detail.key}
          samples={detail.request?.samples ?? []}
          onSave={async (draft) => {
            const list =
              editing.index === null
                ? [...all, draft]
                : all.map((instance, at) =>
                    at === editing.index ? draft : instance,
                  );
            const failed = await save(list);
            if (!failed) setEditing(null);
            return failed;
          }}
          saving={saving}
          schema={schema}
        />
      ) : null}
      <ConfirmDialog
        confirmLabel={t("plugins.requestPlugins.remove")}
        description={t("plugins.requestPlugins.removeConfirm", {
          name: removing === null ? "" : (all[removing]?.name ?? ""),
        })}
        destructive
        onCancel={() => setRemoving(null)}
        onConfirm={() => {
          const index = removing;
          setRemoving(null);
          if (index === null) return;
          void save(all.filter((_, at) => at !== index)).then((failed) => {
            if (failed) setError(failed);
          });
        }}
        open={removing !== null}
        title={t("plugins.requestPlugins.removeTitle")}
      />
    </Panel>
  );
}

function InstanceEditor({
  initial,
  isNew,
  onClose,
  onSave,
  pluginKey,
  samples,
  saving,
  schema,
}: {
  initial: RequestPluginInstance;
  isNew: boolean;
  onClose: () => void;
  onSave: (draft: RequestPluginInstance) => Promise<string | null>;
  pluginKey: string;
  samples: RequestPluginSample[];
  saving: boolean;
  schema: unknown;
}) {
  const t = useT();
  const id = useId();
  const [name, setName] = useState(initial.name);
  const [models, setModels] = useState(initial.models.join("\n"));
  const [config, setConfig] = useState<Record<string, unknown>>(
    initial.config ?? {},
  );
  const [invalid, setInvalid] = useState<Record<string, boolean>>({});
  const [error, setError] = useState<string | null>(null);
  const fields = useMemo(() => schemaProperties(schema), [schema]);

  const submit = async () => {
    if (Object.values(invalid).some(Boolean)) {
      setError(t("plugins.requestPlugins.invalidJSON"));
      return;
    }
    const draft: RequestPluginInstance = {
      ...initial,
      name: name.trim(),
      models: models
        .split(/[\n,]/)
        .map((model) => model.trim())
        .filter((model, index, list) => model && list.indexOf(model) === index),
      config,
    };
    setError(await onSave(draft));
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="sm:max-w-xl"
        data-testid="request-plugin-editor"
      >
        <DialogHeader className="text-left">
          <DialogTitle>
            {isNew
              ? t("plugins.requestPlugins.addTitle")
              : t("plugins.requestPlugins.editTitle")}
          </DialogTitle>
        </DialogHeader>
        <form
          className="grid max-h-[65vh] min-h-0 gap-4 overflow-y-auto overscroll-contain pr-1"
          id={id}
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <Field
            htmlFor={`${id}-name`}
            inDialog
            label={t("plugins.requestPlugins.name")}
          >
            <Input
              id={`${id}-name`}
              maxLength={64}
              onChange={(event) => setName(event.target.value)}
              value={name}
            />
          </Field>
          <Field
            htmlFor={`${id}-models`}
            inDialog
            label={t("plugins.requestPlugins.models")}
            tooltip={t("plugins.requestPlugins.modelsHelp")}
          >
            <Textarea
              className="min-h-16 font-mono text-xs"
              id={`${id}-models`}
              onChange={(event) => setModels(event.target.value)}
              placeholder={t("plugins.requestPlugins.allModels")}
              value={models}
            />
          </Field>
          {fields.length > 0 ? (
            <section className="grid gap-4 border-t pt-4">
              <h4 className="text-xs font-medium text-muted-foreground">
                {t("plugins.requestPlugins.config")}
              </h4>
              <ConfigSchemaFields
                idPrefix={id}
                onChange={(key, value) =>
                  setConfig((current) => ({ ...current, [key]: value }))
                }
                onInvalid={(key, bad) =>
                  setInvalid((current) => ({ ...current, [key]: bad }))
                }
                schema={schema}
                value={config}
              />
            </section>
          ) : null}
          <DryRun config={config} pluginKey={pluginKey} samples={samples} />
          {error ? <FormMessage tone="error">{error}</FormMessage> : null}
        </form>
        <DialogFooter>
          <Button onClick={onClose} size="sm" type="button" variant="outline">
            {t("common.cancel")}
          </Button>
          <Button
            disabled={saving || name.trim() === ""}
            form={id}
            size="sm"
            type="submit"
          >
            {t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** One field of an instance's config, by its type and format. */
function ConfigField({
  field,
  id,
  onChange,
  onInvalid,
  value,
}: {
  field: SchemaField;
  id: string;
  onChange: (value: unknown) => void;
  onInvalid: (invalid: boolean) => void;
  value: unknown;
}) {
  const t = useT();
  const locale = useLocale();
  const label = localized(field.title, locale) || id;
  const tooltip = localized(field.description, locale) || undefined;
  const example = Array.isArray(field.examples)
    ? String(field.examples[0] ?? "")
    : undefined;
  const titleOf = (index: number, option: string | number) =>
    localized(field.enumTitles?.[index], locale) || String(option);

  if (field.type === "boolean") {
    return (
      <Field htmlFor={id} inDialog label={label} tooltip={tooltip}>
        <Switch
          checked={value === true}
          id={id}
          onCheckedChange={(checked) => onChange(checked)}
        />
      </Field>
    );
  }
  if (
    (field.type === "string" ||
      field.type === "number" ||
      field.type === "integer") &&
    field.enum?.length
  ) {
    const options = field.enum;
    return (
      <Field htmlFor={id} inDialog label={label} tooltip={tooltip}>
        <Select
          onValueChange={(next) => {
            const index = options.findIndex(
              (option) => String(option) === next,
            );
            onChange(options[index]);
          }}
          value={value === undefined ? undefined : String(value)}
        >
          <SelectTrigger className="w-full" id={id} size="sm">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map((option, index) => (
              <SelectItem key={String(option)} value={String(option)}>
                {titleOf(index, option)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
    );
  }
  if (field.type === "array" && field.items?.enum?.length) {
    const items = field.items;
    const chosen = Array.isArray(value) ? value : [];
    return (
      <Field group inDialog label={label} tooltip={tooltip}>
        <div className="grid gap-1.5 sm:grid-cols-2">
          {items.enum!.map((option, index) => (
            <Label className="text-sm font-normal" key={String(option)}>
              <Checkbox
                checked={chosen.includes(option)}
                onCheckedChange={(checked) =>
                  onChange(
                    checked === true
                      ? [...chosen, option]
                      : chosen.filter((item) => item !== option),
                  )
                }
              />
              {localized(items.enumTitles?.[index], locale) || String(option)}
            </Label>
          ))}
        </div>
      </Field>
    );
  }
  if (field.type === "number" || field.type === "integer") {
    return (
      <Field htmlFor={id} inDialog label={label} tooltip={tooltip}>
        <Input
          id={id}
          max={field.maximum}
          min={field.minimum}
          onChange={(event) => {
            const text = event.target.value;
            onChange(text === "" ? undefined : Number(text));
          }}
          step={field.type === "integer" ? 1 : "any"}
          type="number"
          value={typeof value === "number" ? value : ""}
        />
      </Field>
    );
  }
  if (field.type === "string") {
    const text = typeof value === "string" ? value : "";
    if (field.format === "textarea" || field.format === "keywords") {
      return (
        <Field htmlFor={id} inDialog label={label} tooltip={tooltip}>
          <Textarea
            className="min-h-24 text-xs"
            id={id}
            onChange={(event) => onChange(event.target.value)}
            placeholder={
              field.format === "keywords"
                ? t("plugins.requestPlugins.keywords")
                : example
            }
            value={text}
          />
        </Field>
      );
    }
    return (
      <Field htmlFor={id} inDialog label={label} tooltip={tooltip}>
        <Input
          autoComplete="off"
          className={
            field.format === "regex" || field.format === "url"
              ? "font-mono text-xs"
              : undefined
          }
          id={id}
          onChange={(event) => onChange(event.target.value)}
          placeholder={example}
          type={field.format === "password" ? "password" : "text"}
          value={text}
        />
      </Field>
    );
  }
  return (
    <JSONField
      id={id}
      label={label}
      onChange={onChange}
      onInvalid={onInvalid}
      tooltip={tooltip}
      value={value}
    />
  );
}

/** A list or object field the form edits as JSON. */
function JSONField({
  id,
  label,
  onChange,
  onInvalid,
  tooltip,
  value,
}: {
  id: string;
  label: string;
  onChange: (value: unknown) => void;
  onInvalid: (invalid: boolean) => void;
  tooltip?: string;
  value: unknown;
}) {
  const t = useT();
  const [text, setText] = useState(() =>
    value === undefined ? "" : JSON.stringify(value, null, 2),
  );
  const [bad, setBad] = useState(false);
  return (
    <Field htmlFor={id} inDialog label={label} tooltip={tooltip}>
      <Textarea
        aria-invalid={bad}
        className="min-h-24 font-mono text-xs"
        id={id}
        onChange={(event) => {
          const next = event.target.value;
          setText(next);
          try {
            onChange(next.trim() === "" ? undefined : JSON.parse(next));
            setBad(false);
            onInvalid(false);
          } catch {
            setBad(true);
            onInvalid(true);
          }
        }}
        value={text}
      />
      {bad ? (
        <FormMessage tone="error">
          {t("plugins.requestPlugins.invalidJSON")}
        </FormMessage>
      ) : null}
    </Field>
  );
}

/** The formats a dry run's request may be in. */
const dryRunFormats = ["openai", "openai_responses", "claude", "gemini"];

/**
 * A dry run of the instance being edited: a request, one of the plugin's
 * samples or written by hand, and a reply text, run through the instance
 * without being sent. It shows the request as ingress left it, what
 * stopped it, the reply the check left, and each step.
 */
function DryRun({
  config,
  pluginKey,
  samples,
}: {
  config: Record<string, unknown>;
  pluginKey: string;
  samples: RequestPluginSample[];
}) {
  const t = useT();
  const locale = useLocale();
  const id = useId();
  const first = samples[0];
  const [format, setFormat] = useState(first?.format ?? "openai");
  const [body, setBody] = useState(() =>
    JSON.stringify(
      first?.body ?? {
        model: "gpt-4o",
        messages: [{ role: "user", content: "" }],
      },
      null,
      2,
    ),
  );
  const [reply, setReply] = useState(first?.reply ?? "");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<RequestPluginDryRun | null>(null);
  const [error, setError] = useState<string | null>(null);

  const run = async () => {
    let parsed: unknown;
    try {
      parsed = JSON.parse(body);
    } catch {
      setError(t("plugins.requestPlugins.invalidJSON"));
      return;
    }
    setRunning(true);
    setError(null);
    try {
      setResult(
        await dryRunRequestPlugin(pluginKey, {
          config,
          format,
          body: parsed,
          ...(reply.trim() ? { reply } : {}),
        }),
      );
    } catch (cause) {
      setResult(null);
      setError(
        controlErrorMessage(cause, t("plugins.requestPlugins.dryRunFailed")),
      );
    } finally {
      setRunning(false);
    }
  };

  return (
    <section
      className="grid gap-3 border-t pt-4"
      data-testid="request-plugin-dry-run"
    >
      <h4 className="text-xs font-medium text-muted-foreground">
        {t("plugins.requestPlugins.dryRun")}
      </h4>
      {samples.length > 0 ? (
        <Field
          htmlFor={`${id}-sample`}
          inDialog
          label={t("plugins.requestPlugins.sample")}
        >
          <Select
            onValueChange={(value) => {
              const sample = samples[Number(value)];
              if (!sample) return;
              setFormat(sample.format);
              setBody(JSON.stringify(sample.body, null, 2));
              setReply(sample.reply ?? "");
              setResult(null);
            }}
          >
            <SelectTrigger className="w-full" id={`${id}-sample`} size="sm">
              <SelectValue
                placeholder={t("plugins.requestPlugins.sampleChoose")}
              />
            </SelectTrigger>
            <SelectContent>
              {samples.map((sample, index) => (
                <SelectItem key={index} value={String(index)}>
                  {localized(sample.name, locale) ||
                    `${sample.format} ${index + 1}`}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      ) : null}
      <Field
        htmlFor={`${id}-format`}
        inDialog
        label={t("plugins.requestPlugins.format")}
      >
        <Select onValueChange={setFormat} value={format}>
          <SelectTrigger className="w-full" id={`${id}-format`} size="sm">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {dryRunFormats.map((name) => (
              <SelectItem key={name} value={name}>
                {t(`plugins.requestPlugins.formats.${name}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field
        htmlFor={`${id}-body`}
        inDialog
        label={t("plugins.requestPlugins.body")}
      >
        <Textarea
          className="min-h-32 font-mono text-xs"
          id={`${id}-body`}
          onChange={(event) => setBody(event.target.value)}
          value={body}
        />
      </Field>
      <Field
        htmlFor={`${id}-reply`}
        inDialog
        label={t("plugins.requestPlugins.reply")}
        tooltip={t("plugins.requestPlugins.replyHelp")}
      >
        <Textarea
          className="min-h-16 text-xs"
          id={`${id}-reply`}
          onChange={(event) => setReply(event.target.value)}
          value={reply}
        />
      </Field>
      <div>
        <Button
          disabled={running}
          onClick={() => void run()}
          size="sm"
          type="button"
          variant="outline"
        >
          {t("plugins.requestPlugins.runDryRun")}
        </Button>
      </div>
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {result ? (
        <div
          className="grid gap-2 text-xs"
          data-testid="request-plugin-dry-run-result"
        >
          {result.stopped ? (
            <FormMessage tone="error">{result.stopped.message}</FormMessage>
          ) : (
            <FormMessage tone="success">
              {t("plugins.requestPlugins.dryRunPassed")}
            </FormMessage>
          )}
          {result.failures.map((failure, index) => (
            <FormMessage key={index} tone="warning">
              {failure}
            </FormMessage>
          ))}
          {result.records.map((record, index) => (
            <p className="text-muted-foreground" key={index}>
              {t(`plugins.requestPlugins.stages.${record.hook}`, {
                defaultValue: record.hook,
              })}
              {record.summary ? ` · ${record.summary}` : ""}
            </p>
          ))}
          {result.reply !== undefined ? (
            <div className="grid gap-1">
              <span className="font-medium text-muted-foreground">
                {t("plugins.requestPlugins.replyAfter")}
              </span>
              <p className="whitespace-pre-wrap rounded-md border bg-muted/20 p-2">
                {result.reply}
              </p>
            </div>
          ) : null}
          {result.body !== undefined && !result.stopped ? (
            <div className="grid gap-1">
              <span className="font-medium text-muted-foreground">
                {t("plugins.requestPlugins.bodyAfter")}
              </span>
              <pre className="max-h-48 overflow-auto rounded-md border bg-muted/20 p-2 font-mono text-micro whitespace-pre-wrap">
                {JSON.stringify(result.body, null, 2)}
              </pre>
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
