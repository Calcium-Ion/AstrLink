import { useEffect, useState } from "react";
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
import { Field } from "./Field";
import { FilterSelect } from "./FilterSelect";
import { FormMessage } from "./FormMessage";
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
  onChange,
}: {
  kind: BuiltinToolKind;
  value: BuiltinTool;
  services: readonly RoutableService[];
  disabled: boolean;
  onChange: (value: BuiltinTool) => void;
}) {
  const t = useT();
  const [secret, setSecret] = useState("");
  const [configured, setConfigured] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const title = t(`builtinTools.${kind}`);
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
        actions={
          <Switch
            aria-label={t("builtinTools.enable", { tool: title })}
            checked={value.enabled}
            disabled={disabled || busy}
            onCheckedChange={(enabled) => change({ enabled })}
          />
        }
      >
        <h2 className="text-sm font-semibold">{title}</h2>
      </PanelHeader>
      <fieldset disabled={disabled || busy} className="grid gap-3 p-3">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <FilterSelect
            ariaLabel={t("builtinTools.backend", { tool: title })}
            label={t("builtinTools.executor")}
            value={backend}
            onChange={(next) => changeBackend(next as BuiltinTool["backend"])}
            options={
              kind === "web_search"
                ? [
                    { value: "upstream", label: t("builtinTools.upstream") },
                    { value: "external", label: t("builtinTools.tavily") },
                  ]
                : [
                    {
                      value: "service_images",
                      label: t("builtinTools.serviceImages"),
                    },
                    { value: "upstream", label: t("builtinTools.upstream") },
                    { value: "external", label: t("builtinTools.imagesApi") },
                  ]
            }
          />
          <span className="text-xs text-muted-foreground">
            {t(
              backend === "external"
                ? "builtinTools.externalHint"
                : direct
                  ? "builtinTools.serviceImagesHint"
                  : "builtinTools.upstreamHint",
            )}
          </span>
        </div>
        {backend !== "external" ? (
          <div className="grid items-start gap-3 md:grid-cols-[minmax(0,12rem)_minmax(0,1fr)]">
            {providers.length === 0 ? (
              <p className="text-xs text-muted-foreground md:col-span-2">
                {t(
                  direct
                    ? "builtinTools.noImageProviders"
                    : "builtinTools.noProviders",
                )}
              </p>
            ) : null}
            <Field label={t("builtinTools.provider")}>
              <ServiceSelect
                ariaLabel={t("builtinTools.providerFor", { tool: title })}
                label=""
                value={value.service_id ?? ""}
                onChange={(service_id) => change({ service_id, model: "" })}
                services={providers}
              />
            </Field>
            <Field
              label={t(
                direct ? "builtinTools.imageModel" : "builtinTools.model",
              )}
              hint={t(
                direct
                  ? "builtinTools.serviceImageModelHint"
                  : kind === "web_search"
                    ? "builtinTools.searchModelHint"
                    : "builtinTools.imageExecutorHint",
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
          </div>
        ) : (
          <>
            <div className="grid gap-3 md:grid-cols-2">
              <Field label={t("builtinTools.apiUrl")}>
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
                    value={value.model ?? ""}
                    onChange={(event) => change({ model: event.target.value })}
                  />
                </Field>
              ) : null}
            </div>
            <Field
              label={
                configured
                  ? t("builtinTools.keyConfigured")
                  : t("builtinTools.apiKey")
              }
            >
              <div className="flex min-w-0 gap-2">
                <Input
                  type="password"
                  autoComplete="off"
                  aria-label={t("builtinTools.keyFor", { tool: title })}
                  value={secret}
                  onChange={(event) => setSecret(event.target.value)}
                />
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
          </>
        )}
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={busy || disabled || Boolean(secret)}
            onClick={() => void act("test")}
          >
            {busy ? t("builtinTools.testing") : t("builtinTools.test")}
          </Button>
          <span className="text-xs text-muted-foreground">
            {t("builtinTools.testHint")}
          </span>
        </div>
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
  const tools = value ?? defaultBuiltinTools();
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden">
      <p className="shrink-0 text-xs text-muted-foreground">
        {t("builtinTools.description")}
      </p>
      <div
        className="min-h-0 flex-1 space-y-3 overflow-y-auto"
        data-testid="builtin-tools-region"
      >
        {(["web_search", "image_generation"] as const).map((kind) => (
          <ToolEditor
            key={kind}
            kind={kind}
            value={tools[kind]}
            services={services}
            disabled={disabled}
            onChange={(next) => onChange({ ...tools, [kind]: next })}
          />
        ))}
      </div>
    </div>
  );
}
