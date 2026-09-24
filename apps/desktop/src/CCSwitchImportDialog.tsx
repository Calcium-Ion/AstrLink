import { useEffect, useRef, useState, type FormEvent } from "react";

import { Field } from "@/components/Field";
import { ChoiceCard } from "@/components/ChoiceCard";
import { CCSwitchIcon } from "@/components/CCSwitchIcon";
import {
  ClaudeCodeColor,
  CodexColor,
  GeminiColor,
  OpenCodeMono,
  OpenClawColor,
} from "@/components/brand-icons";
import { ArrowUpRight, Key, Server } from "@/components/icons";
import { RadioGroup } from "@/components/ui/radio-group";
import { ModelSelect } from "@/components/ModelSelect";
import { FormMessage } from "@/components/FormMessage";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

import type { AccessTokenSummary } from "./access-token-model";
import {
  listRoutes,
  listServices,
  openCCSwitchImport,
  type CCSwitchClient,
  type CCSwitchModels,
} from "./bridge";
import type { Route } from "./route-model";
import type { Service } from "./service-model";
import { i18n } from "./i18n";
import { notify } from "./notify";

const clients = [
  { id: "claude", label: "Claude Code", Icon: ClaudeCodeColor },
  { id: "codex", label: "Codex", Icon: CodexColor },
  { id: "gemini", label: "Gemini CLI", Icon: GeminiColor },
  { id: "opencode", label: "OpenCode", Icon: OpenCodeMono },
  { id: "openclaw", label: "OpenClaw", Icon: OpenClawColor },
] as const;

const protocols: Record<CCSwitchClient, string> = {
  claude: "anthropic.messages",
  codex: "openai.responses",
  gemini: "google.generate_content",
  opencode: "openai.chat",
  openclaw: "openai.chat",
};

export function CCSwitchImportDialog({
  token,
  inferenceURL,
  onClose,
}: {
  token: AccessTokenSummary;
  inferenceURL: string;
  onClose: () => void;
}) {
  const t = i18n.t.bind(i18n);
  const [client, setClient] = useState<CCSwitchClient>("claude");
  const [name, setName] = useState(`AstrLink · ${token.name}`);
  const [model, setModel] = useState("");
  const [claudeModels, setClaudeModels] = useState({
    model: "",
    haikuModel: "",
    sonnetModel: "",
    opusModel: "",
  });
  const isClaude = client === "claude";
  const modelRequired = !isClaude && !model.trim();
  const [opening, setOpening] = useState(false);
  const [error, setError] = useState(false);
  const [catalog, setCatalog] = useState<{
    services: Service[];
    routes: Route[];
  }>({ services: [], routes: [] });
  const [catalogStatus, setCatalogStatus] = useState<
    "loading" | "ready" | "error"
  >("loading");
  const active = useRef(true);
  const pending = useRef(false);

  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([listServices(), listRoutes()]).then(
      ([services, routes]) => {
        if (cancelled) return;
        setCatalog({
          services: services.status === "fulfilled" ? services.value.items : [],
          routes: routes.status === "fulfilled" ? routes.value.items : [],
        });
        setCatalogStatus(
          services.status === "fulfilled" && routes.status === "fulfilled"
            ? "ready"
            : "error",
        );
      },
    );
    return () => {
      cancelled = true;
    };
  }, []);

  const modelOptions = [
    ...catalog.services
      .filter(
        (service) =>
          service.enabled &&
          service.capabilities.some(
            (capability) => capability.protocol === protocols[client],
          ),
      )
      .flatMap((service) => service.models),
    ...catalog.routes
      .filter(
        (route) =>
          route.enabled &&
          route.match.protocol === protocols[client] &&
          route.match.model &&
          !route.match.model.includes("*"),
      )
      .map((route) => route.match.model!),
  ].sort();

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (pending.current || !name.trim() || modelRequired) return;
    pending.current = true;
    setOpening(true);
    setError(false);
    const models: CCSwitchModels = isClaude
      ? Object.fromEntries(
          Object.entries(claudeModels)
            .map(([key, value]) => [key, value.trim()])
            .filter(([, value]) => value),
        )
      : { model: model.trim() };
    try {
      await openCCSwitchImport({
        tokenId: token.id,
        client,
        name: name.trim(),
        models,
        inferenceUrl: inferenceURL,
      });
      if (!active.current) return;
      notify.success(t("ccSwitch.opened"));
      onClose();
    } catch {
      if (active.current) setError(true);
    } finally {
      pending.current = false;
      if (active.current) setOpening(false);
    }
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => !open && !pending.current && onClose()}
    >
      <DialogContent
        showCloseButton={!opening}
        className="w-[calc(100vw_-_2rem)] max-w-none sm:max-w-2xl"
      >
        <DialogHeader className="text-left">
          <DialogTitle className="flex items-center gap-2.5">
            <CCSwitchIcon size={28} />
            {t("ccSwitch.title")}
          </DialogTitle>
          <DialogDescription>{t("ccSwitch.description")}</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(event) => void submit(event)}>
          <fieldset className="min-w-0">
            <legend className="mb-2 text-xs font-medium text-text-secondary">
              {t("ccSwitch.client")}
            </legend>
            <RadioGroup
              aria-label={t("ccSwitch.client")}
              className="grid grid-cols-2 gap-2 min-[540px]:grid-cols-5"
              value={client}
              onValueChange={(value) => setClient(value as CCSwitchClient)}
              disabled={opening}
            >
              {clients.map(({ id, label, Icon }) => (
                <ChoiceCard
                  key={id}
                  id={`cc-switch-${id}`}
                  value={id}
                  label={label}
                  selected={client === id}
                  disabled={opening}
                  layout="tile"
                  icon={<Icon size={26} />}
                />
              ))}
            </RadioGroup>
          </fieldset>
          <div className="grid gap-3 rounded-md border bg-muted/40 p-3 min-[540px]:grid-cols-2">
            <div className="flex min-w-0 items-start gap-2.5">
              <Server
                aria-hidden="true"
                className="mt-0.5 size-4 shrink-0 text-muted-foreground"
              />
              <div className="grid min-w-0 gap-1">
                <span className="text-xs text-muted-foreground">
                  {t("overview.apiAddress")}
                </span>
                <code className="break-all text-xs">
                  {inferenceURL}
                  {["codex", "opencode", "openclaw"].includes(client)
                    ? "/v1"
                    : ""}
                </code>
              </div>
            </div>
            <div className="flex min-w-0 items-start gap-2.5">
              <Key
                aria-hidden="true"
                className="mt-0.5 size-4 shrink-0 text-muted-foreground"
              />
              <div className="grid min-w-0 gap-1">
                <span className="text-xs text-muted-foreground">
                  {t("ccSwitch.token")}
                </span>
                <span className="truncate text-xs" title={token.name}>
                  {token.name}{" "}
                  <span className="font-mono text-muted-foreground">
                    {token.hint}
                  </span>
                </span>
              </div>
            </div>
          </div>
          <div className="grid gap-4 min-[540px]:grid-cols-2">
            <Field htmlFor="cc-switch-name" label={t("ccSwitch.name")}>
              <Input
                id="cc-switch-name"
                value={name}
                onChange={(event) => setName(event.currentTarget.value)}
                maxLength={128}
                disabled={opening}
                required
              />
            </Field>
            <Field
              htmlFor="cc-switch-model"
              label={t(isClaude ? "ccSwitch.defaultModel" : "ccSwitch.model")}
              hint={t(
                catalogStatus === "loading"
                  ? "ccSwitch.modelsLoading"
                  : catalogStatus === "error"
                    ? "ccSwitch.modelsFailed"
                    : isClaude
                      ? "ccSwitch.claudeModelHint"
                      : "ccSwitch.modelHint",
              )}
            >
              <ModelSelect
                id="cc-switch-model"
                aria-label={t(
                  isClaude ? "ccSwitch.defaultModel" : "ccSwitch.model",
                )}
                options={modelOptions}
                value={isClaude ? claudeModels.model : model}
                placeholder={t("ccSwitch.modelPlaceholder")}
                onValueChange={(value) =>
                  isClaude
                    ? setClaudeModels((current) => ({
                        ...current,
                        model: value,
                      }))
                    : setModel(value)
                }
                maxLength={256}
                disabled={opening}
              />
            </Field>
          </div>
          {isClaude && (
            <div className="grid gap-3 min-[540px]:grid-cols-3">
              {(["haikuModel", "sonnetModel", "opusModel"] as const).map(
                (key) => (
                  <Field
                    key={key}
                    htmlFor={`cc-switch-${key}`}
                    label={t(`ccSwitch.${key}`)}
                  >
                    <ModelSelect
                      id={`cc-switch-${key}`}
                      aria-label={t(`ccSwitch.${key}`)}
                      options={modelOptions}
                      value={claudeModels[key]}
                      onValueChange={(value) =>
                        setClaudeModels((current) => ({
                          ...current,
                          [key]: value,
                        }))
                      }
                      placeholder={t("ccSwitch.modelPlaceholder")}
                      disabled={opening}
                    />
                  </Field>
                ),
              )}
            </div>
          )}
          {error && (
            <FormMessage tone="error">{t("ccSwitch.failed")}</FormMessage>
          )}
          <DialogFooter className="border-t pt-4 min-[540px]:flex-row min-[540px]:justify-end">
            <Button
              type="button"
              variant="outline"
              disabled={opening}
              onClick={onClose}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              disabled={opening || !name.trim() || modelRequired}
            >
              {opening ? t("ccSwitch.opening") : t("ccSwitch.open")}
              <ArrowUpRight />
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
