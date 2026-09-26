import { useEffect, useId, useState } from "react";
import { builtinToolAction } from "../bridge";
import {
  builtinImagesServiceKinds,
  defaultBuiltinTools,
  parseBuiltinTools,
  type BuiltinTool,
  type BuiltinToolKind,
  type BuiltinTools,
} from "@/builtin-tools-model";
import type { RoutableService } from "@/service-model";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { Field } from "./Field";
import { FilterSelect } from "./FilterSelect";
import { FormMessage } from "./FormMessage";
import { HelpPopover } from "./HelpPopover";
import { ModelSelect } from "./ModelSelect";
import { Panel, PanelHeader } from "./Panel";
import { ServiceSelect } from "./ServiceSelect";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Switch } from "./ui/switch";

function ToolEditor({
  kind,
  value,
  services,
  disabled,
  testHintId,
  onChange,
}: {
  kind: BuiltinToolKind;
  value: BuiltinTool;
  services: readonly RoutableService[];
  disabled: boolean;
  testHintId: string;
  onChange: (value: BuiltinTool) => void;
}) {
  const t = useT();
  const [secret, setSecret] = useState("");
  const [configured, setConfigured] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const title = t(`builtinTools.${kind}`);
  const sourceTypes =
    kind === "web_search"
      ? [
          "web_search",
          "web_search_preview",
          "web_search_preview_2025_03_11",
          "web_search_2025_08_26",
        ]
      : [kind];
  const backend = value.backend || "upstream";
  const direct = backend === "service_images";
  const providersFor = (backend: BuiltinTool["backend"]) =>
    services.filter(
      (service) =>
        service.enabled &&
        (backend === "service_images"
          ? builtinImagesServiceKinds.includes(service.kind)
          : service.capabilities.some(
              (capability) =>
                capability.protocol === "openai.responses" &&
                !capability.convert_to,
            )),
    );
  const providers = providersFor(backend);
  const selected = providers.find((service) => service.id === value.service_id);
  useEffect(() => {
    if (disabled) return;
    let active = true;
    void builtinToolAction(kind, "status")
      .then((result) => {
        if (active) setConfigured(result.configured === true);
      })
      .catch(() => {
        if (active) setError(t("builtinTools.keyStatusFailed"));
      });
    return () => {
      active = false;
    };
  }, [kind, disabled, t]);
  const change = (patch: Partial<BuiltinTool>) => {
    setMessage("");
    setError("");
    onChange({ ...value, ...patch });
  };
  // For image generation the model is a chat model when one executes the
  // tool and an image model otherwise, so it only carries over between the
  // Images API backends.
  const changeBackend = (next: BuiltinTool["backend"]) => {
    const service_id = providersFor(next).some(
      (service) => service.id === value.service_id,
    )
      ? value.service_id
      : "";
    change(
      kind === "image_generation" &&
        (backend === "upstream") !== (next === "upstream")
        ? { backend: next, service_id, model: "" }
        : { backend: next, service_id },
    );
  };
  const act = async (action: "save_key" | "delete_key" | "test") => {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      if (action === "test")
        parseBuiltinTools({
          ...defaultBuiltinTools(),
          [kind]: { ...value, enabled: true },
        });
      const result = await builtinToolAction(
        kind,
        action,
        action === "save_key"
          ? { secret }
          : action === "test"
            ? { ...value, enabled: true }
            : undefined,
      );
      if (action !== "test") {
        setConfigured(result.configured === true);
        setSecret("");
      }
      setMessage(
        action === "test"
          ? t("builtinTools.testPassed", { duration: result.duration_ms })
          : t("builtinTools.keySaved"),
      );
    } catch (error) {
      setError(
        error instanceof Error ? error.message : t("builtinTools.failed"),
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel>
      <PanelHeader
        size="sm"
        className="px-4 py-2"
        actions={
          <>
            <Button
              type="button"
              size="sm"
              variant="outline"
              aria-describedby={testHintId}
              disabled={busy || disabled || Boolean(secret)}
              onClick={() => void act("test")}
            >
              {busy ? t("builtinTools.testing") : t("builtinTools.test")}
            </Button>
            <Switch
              aria-label={t("builtinTools.enable", { tool: title })}
              checked={value.enabled}
              disabled={disabled || busy}
              onCheckedChange={(enabled) => change({ enabled })}
            />
          </>
        }
      >
        <div className="flex items-center gap-1.5">
          <h3 className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-sm font-semibold">
            <code>{kind}</code>
            <span className="text-xs font-normal text-muted-foreground">
              {title}
            </span>
          </h3>
          <HelpPopover label={t("builtinTools.toolHelp", { tool: kind })}>
            <div className="grid gap-2">
              <div className="grid gap-1">
                <p className="font-medium">{t("builtinTools.sourceTypes")}</p>
                {sourceTypes.map((type) => (
                  <code key={type} className="break-all">
                    {type}
                  </code>
                ))}
              </div>
              <div className="grid gap-1">
                <p className="font-medium">{t("builtinTools.executionHelp")}</p>
                <p>
                  {t(
                    backend === "external"
                      ? kind === "web_search"
                        ? "builtinTools.externalSearchHint"
                        : "builtinTools.externalImagesHint"
                      : direct
                        ? "builtinTools.serviceImagesHint"
                        : "builtinTools.upstreamHint",
                  )}
                </p>
              </div>
              {backend !== "external" || kind === "image_generation" ? (
                <div className="grid gap-1">
                  <p className="font-medium">{t("builtinTools.modelHelp")}</p>
                  <p>
                    {t(
                      direct || backend === "external"
                        ? "builtinTools.serviceImageModelHint"
                        : kind === "web_search"
                          ? "builtinTools.searchModelHint"
                          : "builtinTools.imageExecutorHint",
                    )}
                  </p>
                </div>
              ) : null}
            </div>
          </HelpPopover>
        </div>
      </PanelHeader>
      <fieldset disabled={disabled || busy} className="grid gap-3 px-4 py-3">
        <div
          className={cn(
            "grid min-w-0 items-end gap-3 @sm/tools:grid-cols-2",
            backend === "external"
              ? kind === "image_generation"
                ? "@2xl/tools:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,0.8fr)]"
                : "@2xl/tools:grid-cols-2"
              : "@2xl/tools:grid-cols-[minmax(0,1fr)_minmax(0,0.6fr)_minmax(0,1fr)]",
          )}
        >
          <Field
            label={t("builtinTools.executor")}
            className="@sm/tools:col-span-2 @2xl/tools:col-span-1"
          >
            <FilterSelect
              ariaLabel={t("builtinTools.backend", { tool: title })}
              label=""
              className="w-full data-[size=sm]:h-8"
              value={backend}
              onChange={(next) => changeBackend(next as BuiltinTool["backend"])}
              options={
                kind === "web_search"
                  ? [
                      {
                        value: "upstream",
                        label: t("builtinTools.upstream", { tool: kind }),
                      },
                      { value: "external", label: t("builtinTools.tavily") },
                    ]
                  : [
                      {
                        value: "service_images",
                        label: t("builtinTools.serviceImages"),
                      },
                      {
                        value: "upstream",
                        label: t("builtinTools.upstream", { tool: kind }),
                      },
                      { value: "external", label: t("builtinTools.imagesApi") },
                    ]
              }
            />
          </Field>
          {backend !== "external" ? (
            <>
              <Field label={t("builtinTools.provider")}>
                <ServiceSelect
                  ariaLabel={t("builtinTools.providerFor", { tool: title })}
                  label=""
                  className="w-full data-[size=sm]:h-8"
                  value={value.service_id ?? ""}
                  onChange={(service_id) => change({ service_id, model: "" })}
                  services={providers}
                />
              </Field>
              <Field
                label={t(
                  direct ? "builtinTools.imageModel" : "builtinTools.model",
                )}
              >
                <ModelSelect
                  aria-label={t(
                    direct
                      ? "builtinTools.imageModelFor"
                      : "builtinTools.modelFor",
                    { tool: title },
                  )}
                  options={selected?.models ?? []}
                  value={value.model ?? ""}
                  onValueChange={(model) => change({ model })}
                />
              </Field>
            </>
          ) : (
            <>
              <Field
                label={t("builtinTools.apiUrl")}
                className={
                  kind === "web_search"
                    ? "@sm/tools:col-span-2 @2xl/tools:col-span-1"
                    : undefined
                }
              >
                <Input
                  aria-label={t("builtinTools.urlFor", { tool: title })}
                  value={value.base_url ?? ""}
                  onChange={(event) => change({ base_url: event.target.value })}
                  placeholder={
                    kind === "web_search"
                      ? "https://api.tavily.com"
                      : "https://api.openai.com/v1"
                  }
                />
              </Field>
              {kind === "image_generation" ? (
                <Field label={t("builtinTools.imageModel")}>
                  <Input
                    aria-label={t("builtinTools.imageModelFor", {
                      tool: title,
                    })}
                    value={value.model ?? ""}
                    onChange={(event) => change({ model: event.target.value })}
                  />
                </Field>
              ) : null}
            </>
          )}
        </div>
        {backend !== "external" && providers.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {t(
              direct
                ? "builtinTools.noImageProviders"
                : "builtinTools.noProviders",
            )}
          </p>
        ) : null}
        {backend === "external" ? (
          <Field
            label={
              configured
                ? t("builtinTools.keyConfigured")
                : t("builtinTools.apiKey")
            }
          >
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <div className="min-w-32 flex-1">
                <Input
                  type="password"
                  autoComplete="off"
                  aria-label={t("builtinTools.keyFor", { tool: title })}
                  value={secret}
                  onChange={(event) => setSecret(event.target.value)}
                />
              </div>
              <Button
                size="sm"
                type="button"
                disabled={!secret}
                onClick={() => void act("save_key")}
              >
                {t("builtinTools.saveKey")}
              </Button>
              {configured ? (
                <Button
                  size="sm"
                  variant="ghost"
                  type="button"
                  onClick={() => void act("delete_key")}
                >
                  {t("builtinTools.clearKey")}
                </Button>
              ) : null}
            </div>
          </Field>
        ) : null}
        {message ? (
          <p role="status" className="text-xs text-text-secondary">
            {message}
          </p>
        ) : null}
        {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      </fieldset>
    </Panel>
  );
}

export function BuiltinToolsEditor({
  value,
  services,
  disabled,
  onChange,
}: {
  value?: BuiltinTools;
  services: readonly RoutableService[];
  disabled: boolean;
  onChange: (value: BuiltinTools) => void;
}) {
  const t = useT();
  const id = useId();
  const testHintId = `${id}-test-hint`;
  const tools = value ?? defaultBuiltinTools();
  return (
    <section
      aria-labelledby={id}
      className="@container/tools grid min-w-0 shrink-0 gap-2"
    >
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
        <div className="flex items-center gap-1.5">
          <h2 id={id} className="text-sm font-semibold">
            {t("builtinTools.title")}
          </h2>
          <HelpPopover label={t("builtinTools.help")}>
            <div className="grid gap-2">
              <p>{t("builtinTools.description")}</p>
              <pre className="whitespace-pre-wrap break-all font-mono">
                {
                  '{"tools":[{"type":"web_search"},{"type":"image_generation"}]}'
                }
              </pre>
            </div>
          </HelpPopover>
          <code className="text-xs text-muted-foreground">tools[].type</code>
        </div>
        <p id={testHintId} className="text-xs text-muted-foreground">
          {t("builtinTools.testHint")}
        </p>
      </div>
      <div className="space-y-3" data-testid="builtin-tools-region">
        {(["web_search", "image_generation"] as const).map((kind) => (
          <ToolEditor
            key={kind}
            kind={kind}
            value={tools[kind]}
            services={services}
            disabled={disabled}
            testHintId={testHintId}
            onChange={(next) => onChange({ ...tools, [kind]: next })}
          />
        ))}
      </div>
    </section>
  );
}
