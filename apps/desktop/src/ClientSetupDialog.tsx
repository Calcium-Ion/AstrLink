import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";

import { Field } from "@/components/Field";
import { ChoiceCard } from "@/components/ChoiceCard";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import {
  ClaudeCodeColor,
  CodexColor,
  GeminiColor,
  OpenCodeMono,
  OpenClawColor,
  PiMono,
} from "@/components/brand-icons";
import { CCSwitchIcon } from "@/components/CCSwitchIcon";
import { ArrowUpRight, Key, Server, Settings } from "@/components/icons";
import { RadioGroup } from "@/components/ui/radio-group";
import { ModelSelect } from "@/components/ModelSelect";
import { FormMessage } from "@/components/FormMessage";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import type { AccessTokenSummary } from "./access-token-model";
import {
  applyClientConfig,
  checkClientProxy,
  getClientConfigStatus,
  getRoutingSettings,
  getService,
  updateService,
  isCCSwitchInstalled,
  listServices,
  openCCSwitchImport,
  removeClientConfig,
  type ClientConfigTarget,
} from "./bridge";
import {
  ccSwitchImports,
  isDirectClient,
  type ClientConfigClient,
  type ClientConfigModels,
  type ClientConfigState,
  type ClientConfigStatus,
  type ClientProxyCheck,
  type DirectClient,
} from "./client-config-model";
import {
  astrlinkAutoModelId,
  type ModelRedirect,
} from "./failure-policy-model";
import { bestConversionTarget, type Service } from "./service-model";
import type { ConversionEngineCapability } from "./core-model";
import { protocolLabel } from "./service-presets";
import { i18n } from "./i18n";
import { notify } from "./notify";

export const clientSetupClients = [
  { id: "claude", label: "Claude Code", Icon: ClaudeCodeColor },
  { id: "codex", label: "Codex", Icon: CodexColor },
  { id: "gemini", label: "Gemini CLI", Icon: GeminiColor },
  { id: "opencode", label: "OpenCode", Icon: OpenCodeMono },
  { id: "openclaw", label: "OpenClaw", Icon: OpenClawColor },
  { id: "pi", label: "Pi", Icon: PiMono },
] as const satisfies readonly {
  id: ClientConfigClient;
  label: string;
  Icon: unknown;
}[];

export function clientLabel(client: ClientConfigClient): string {
  return clientSetupClients.find(({ id }) => id === client)?.label ?? client;
}

const protocols: Record<ClientConfigClient, string> = {
  claude: "anthropic.messages",
  codex: "openai.responses",
  gemini: "google.generate_content",
  opencode: "openai.chat",
  openclaw: "openai.chat",
  pi: "openai.responses",
};

const claudeTiers = [
  "haikuModel",
  "sonnetModel",
  "opusModel",
  "fableModel",
] as const;

const stateTones: Record<
  Exclude<ClientConfigState, "not_configured">,
  StatusTone
> = {
  configured: "positive",
  outdated: "pending",
  modified: "pending",
  invalid: "negative",
};

/** How a client card can be used right now. */
type Availability = "loading" | "direct" | "cc-switch" | "missing";

interface Failure {
  message: string;
  detail?: string;
}

/**
 * Explains a system proxy that gets between Codex and the gateway. Only the
 * user changes proxy settings; AstrLink just points at what to change.
 */
function proxyHint(
  check: ClientProxyCheck,
  baseURL: string,
): { tone: "warning" | "notice"; message: string } | null {
  const t = i18n.t.bind(i18n);
  const { client, numeric } = check;
  if (client.route === "blocked") {
    return {
      tone: "warning",
      message: t("clientSetup.proxy.blocked", { proxy: client.proxy }),
    };
  }
  if (client.route === "proxied") {
    return {
      tone: "notice",
      message: t("clientSetup.proxy.proxied", { proxy: client.proxy }),
    };
  }
  if (numeric?.route === "blocked") {
    return {
      tone: "notice",
      message: t("clientSetup.proxy.numericBlocked", {
        proxy: numeric.proxy,
        url: baseURL,
      }),
    };
  }
  return null;
}

function failure(message: string, cause: unknown): Failure {
  return {
    message,
    detail: cause instanceof Error ? cause.message : undefined,
  };
}

export function ClientSetupDialog({
  token,
  tokens = [],
  inferenceURL,
  conversionEngine,
  onClose,
  onChanged,
}: {
  token: AccessTokenSummary;
  /** Names the token another client is configured with. */
  tokens?: readonly AccessTokenSummary[];
  inferenceURL: string;
  conversionEngine?: ConversionEngineCapability | null;
  onClose: () => void;
  /** Called after a client config was written or removed. */
  onChanged?: () => void;
}) {
  const t = i18n.t.bind(i18n);
  const [statuses, setStatuses] = useState<ClientConfigStatus[] | null>(null);
  const [statusFailed, setStatusFailed] = useState(false);
  const [ccSwitch, setCCSwitch] = useState(false);
  const [choice, setChoice] = useState<ClientConfigClient | null>(null);
  const [model, setModel] = useState("");
  const [claudeModels, setClaudeModels] = useState({
    model: "",
    haikuModel: "",
    sonnetModel: "",
    opusModel: "",
    fableModel: "",
  });
  const [busy, setBusy] = useState<"write" | "remove" | "cc-switch" | null>(
    null,
  );
  const [error, setError] = useState<Failure | null>(null);
  const [conflict, setConflict] = useState<{
    target: ClientConfigTarget;
    keys: string[];
  } | null>(null);
  const [removal, setRemoval] = useState<DirectClient | null>(null);
  const [catalog, setCatalog] = useState<{
    services: Service[];
    redirects: ModelRedirect[];
  }>({ services: [], redirects: [] });
  const [catalogStatus, setCatalogStatus] = useState<
    "loading" | "ready" | "error"
  >("loading");
  const [proxyCheck, setProxyCheck] = useState<{
    url: string;
    result: ClientProxyCheck;
  } | null>(null);
  const active = useRef(true);
  const pending = useRef(false);

  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);

  const refresh = useCallback(async () => {
    try {
      const next = await getClientConfigStatus(inferenceURL || null);
      if (!active.current) return;
      setStatuses(next);
      setStatusFailed(false);
    } catch {
      if (!active.current) return;
      setStatuses((current) => current ?? []);
      setStatusFailed(true);
    }
  }, [inferenceURL]);

  useEffect(() => {
    void refresh();
    // CC Switch is optional; a failed check only hides its import.
    void isCCSwitchInstalled().then(
      (installed) => active.current && setCCSwitch(installed),
      () => undefined,
    );
  }, [refresh]);

  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([
      listServices(),
      // Redirect sources are optional suggestions; any failure only drops them.
      Promise.resolve().then(() => getRoutingSettings()),
    ]).then(([services, routing]) => {
      if (cancelled) return;
      setCatalog({
        services: services.status === "fulfilled" ? services.value.items : [],
        redirects:
          routing.status === "fulfilled"
            ? (routing.value.model_redirects ?? [])
            : [],
      });
      setCatalogStatus(services.status === "fulfilled" ? "ready" : "error");
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const statusOf = (id: ClientConfigClient) =>
    statuses?.find((status) => status.client === id);
  const availability = (id: ClientConfigClient): Availability => {
    if (!isDirectClient(id)) return ccSwitch ? "cc-switch" : "missing";
    const card = statusOf(id);
    // A failed status read says nothing about whether the client exists.
    if (!card) return "loading";
    return card.detected ? "direct" : "missing";
  };
  const usable = (id: ClientConfigClient) =>
    ["direct", "cc-switch"].includes(availability(id));
  // Wait for the status before picking a card, so the choice does not jump.
  const client =
    (choice && usable(choice) ? choice : null) ??
    (statuses === null
      ? null
      : (clientSetupClients.find(({ id }) => usable(id))?.id ?? null));
  const mode = client ? availability(client) : null;
  const status = client ? statusOf(client) : undefined;
  const label = client ? clientLabel(client) : "";
  const isClaude = client === "claude";
  const modelRequired = !isClaude && !model.trim();
  const configured = status?.token_id != null;
  const offersCCSwitch = ccSwitch && client !== null && ccSwitchImports(client);
  const checksProxy = client === "codex" && inferenceURL !== "";

  useEffect(() => {
    if (!checksProxy) return;
    let cancelled = false;
    // Optional and read-only: a failed check only hides its hint.
    void Promise.resolve()
      .then(() => checkClientProxy(inferenceURL))
      .then(
        (result) =>
          !cancelled && result && setProxyCheck({ url: inferenceURL, result }),
        () => undefined,
      );
    return () => {
      cancelled = true;
    };
  }, [checksProxy, inferenceURL]);

  // Removing needs a readable file; an unreadable one is left for the user.
  const removable =
    mode === "direct" && configured && status?.state !== "invalid"
      ? (status?.client ?? null)
      : null;
  const otherToken =
    status?.token_id != null && status.token_id !== token.id
      ? tokens.find((item) => item.id === status.token_id)
      : undefined;

  const hasEntry = (service: Service, protocol: string) =>
    service.capabilities.some((row) => row.protocol === protocol);
  const entryTarget = (service: Service, protocol: string) =>
    bestConversionTarget(
      protocol,
      service.capabilities
        .filter((row) => !row.convert_to)
        .map((row) => row.protocol),
      conversionEngine,
    );
  const supportsEntry = (service: Service, protocol: string) =>
    service.enabled &&
    (hasEntry(service, protocol) || Boolean(entryTarget(service, protocol)));

  // Preserve the existing route when any provider already accepts this entry.
  const modelService = (modelID: string, protocol: string) => {
    const upstreamModel =
      catalog.redirects.find(
        (redirect) => redirect.enabled && redirect.from === modelID,
      )?.to ?? modelID;
    const candidates = catalog.services.filter(
      (service) =>
        service.models.includes(upstreamModel) &&
        supportsEntry(service, protocol),
    );
    return (
      candidates.find((service) => hasEntry(service, protocol)) ?? candidates[0]
    );
  };
  const requiredEntry = (modelID: string, protocol: string) => {
    const service = modelService(modelID.trim(), protocol);
    if (!service || hasEntry(service, protocol)) return undefined;
    const target = entryTarget(service, protocol);
    return target ? { service, target } : undefined;
  };
  const modelOptions = client
    ? [
        ...catalog.services
          .filter((service) => supportsEntry(service, protocols[client]))
          .flatMap((service) => service.models),
        ...catalog.redirects
          .filter(
            (redirect) =>
              redirect.enabled &&
              redirect.from !== astrlinkAutoModelId &&
              // Gemini paths cannot carry "/" in the model segment.
              !(client === "gemini" && redirect.from.includes("/")) &&
              catalog.services.some(
                (service) =>
                  service.models.includes(redirect.to) &&
                  supportsEntry(service, protocols[client]),
              ),
          )
          .map((redirect) => redirect.from),
      ].sort()
    : [];
  const optionAdornment = (modelID: string) =>
    client && requiredEntry(modelID, protocols[client]) ? (
      <Badge variant="secondary" className="ml-auto shrink-0 text-micro">
        {t("clientSetup.entryRequired")}
      </Badge>
    ) : null;
  const entryNote = (modelID: string) => {
    if (!client) return null;
    const entry = requiredEntry(modelID, protocols[client]);
    return entry ? (
      <FormMessage tone="notice">
        {t("clientSetup.entryWillEnable", {
          service: entry.service.name,
          protocol: protocolLabel(protocols[client]),
          target: protocolLabel(entry.target),
        })}
      </FormMessage>
    ) : null;
  };

  const models = (): ClientConfigModels =>
    isClaude
      ? Object.fromEntries(
          Object.entries(claudeModels)
            .map(([key, value]) => [key, value.trim()])
            .filter(([, value]) => value),
        )
      : { model: model.trim() };

  const begin = (next: NonNullable<typeof busy>) => {
    if (pending.current) return false;
    pending.current = true;
    setBusy(next);
    setError(null);
    return true;
  };
  const finish = () => {
    pending.current = false;
    if (active.current) setBusy(null);
  };

  // Read a fresh record and patch against its ETag so concurrent edits survive.
  const enableEntries = async (target: {
    client: ClientConfigClient;
    models: ClientConfigModels;
  }): Promise<boolean> => {
    const protocol = protocols[target.client];
    const entries = new Map(
      Object.values(target.models).flatMap((modelID) => {
        const entry = requiredEntry(modelID ?? "", protocol);
        return entry ? [[entry.service.id, entry.service] as const] : [];
      }),
    );
    for (const service of entries.values()) {
      try {
        const record = await getService(service.id);
        if (!record.service.enabled)
          throw new Error(t("clientSetup.entryRequired"));
        if (!hasEntry(record.service, protocol)) {
          const convertTo = entryTarget(record.service, protocol);
          if (!convertTo) throw new Error(t("clientSetup.entryRequired"));
          const updated = await updateService(service.id, record.etag, {
            capabilities: [
              ...record.service.capabilities,
              {
                protocol,
                mode: "native",
                streaming: true,
                convert_to: convertTo,
              },
            ],
          });
          record.service = updated.service;
        }
        if (active.current)
          setCatalog((current) => ({
            ...current,
            services: current.services.map((item) =>
              item.id === service.id ? record.service : item,
            ),
          }));
      } catch (cause) {
        if (active.current)
          setError(
            failure(
              t("clientSetup.entryEnableFailed", {
                service: service.name,
                protocol: protocolLabel(protocol),
              }),
              cause,
            ),
          );
        return false;
      }
    }
    if (entries.size > 0) {
      // The fresh patched records above also keep the marker correct if refresh fails.
      try {
        const next = await listServices();
        if (active.current)
          setCatalog((current) => ({ ...current, services: next.items }));
      } catch {
        /* Keep the freshly read records. */
      }
    }
    return active.current;
  };

  const write = async (target: ClientConfigTarget, replace: boolean) => {
    if (!begin("write")) return;
    try {
      if (!(await enableEntries(target))) return;
      const outcome = await applyClientConfig({ ...target, replace });
      if (!active.current) return;
      if (outcome.status === "needs_confirmation") {
        setConflict({ target, keys: outcome.keys });
        return;
      }
      notify.success(
        t("clientSetup.written", { client: clientLabel(target.client) }),
      );
      onChanged?.();
      await refresh();
    } catch (cause) {
      if (active.current)
        setError(failure(t("clientSetup.writeFailed"), cause));
    } finally {
      finish();
    }
  };

  const remove = async (target: DirectClient) => {
    if (!begin("remove")) return;
    try {
      await removeClientConfig(target);
      if (!active.current) return;
      notify.success(t("clientSetup.removed", { client: clientLabel(target) }));
      onChanged?.();
      await refresh();
    } catch (cause) {
      if (active.current)
        setError(failure(t("clientSetup.removeFailed"), cause));
    } finally {
      finish();
    }
  };

  const importWithCCSwitch = async () => {
    if (!client || modelRequired || !begin("cc-switch")) return;
    try {
      const target = {
        tokenId: token.id,
        client,
        models: models(),
        inferenceUrl: inferenceURL,
      };
      if (!(await enableEntries(target))) return;
      await openCCSwitchImport(target);
      if (!active.current) return;
      notify.success(t("clientSetup.ccSwitchOpened"));
      onClose();
    } catch {
      if (active.current)
        setError({ message: t("clientSetup.ccSwitchFailed") });
    } finally {
      finish();
    }
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!client || modelRequired) return;
    if (!isDirectClient(client)) {
      void importWithCCSwitch();
      return;
    }
    if (mode !== "direct" || status?.state === "invalid") return;
    void write(
      {
        tokenId: token.id,
        client,
        models: models(),
        inferenceUrl: inferenceURL,
      },
      false,
    );
  };

  const cardNote = (id: ClientConfigClient) => {
    const card = statusOf(id);
    switch (availability(id)) {
      case "loading":
        return null;
      case "cc-switch":
        return t("clientSetup.ccSwitchOnly");
      case "missing":
        return t(
          isDirectClient(id)
            ? "clientSetup.notInstalled"
            : "clientSetup.unsupported",
        );
      case "direct":
        return card && card.state !== "not_configured" ? (
          <StatusBadge tone={stateTones[card.state]}>
            {t(`clientSetup.state.${card.state}`)}
          </StatusBadge>
        ) : null;
    }
  };

  const working = busy !== null;
  const baseURL = `${inferenceURL}${
    ["codex", "opencode", "openclaw", "pi"].includes(client ?? "") ? "/v1" : ""
  }`;
  const proxy =
    checksProxy && proxyCheck?.url === inferenceURL
      ? proxyHint(proxyCheck.result, baseURL)
      : null;

  return (
    <Dialog
      open
      onOpenChange={(open) => !open && !pending.current && onClose()}
    >
      <DialogContent
        showCloseButton={!working}
        className="w-[calc(100vw_-_2rem)] max-w-none sm:max-w-3xl"
      >
        <DialogHeader className="text-left">
          <DialogTitle>{t("clientSetup.title")}</DialogTitle>
          <DialogDescription>{t("clientSetup.description")}</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={submit}>
          <fieldset className="grid min-w-0 gap-2">
            <legend className="mb-2 text-xs font-medium text-text-secondary">
              {t("clientSetup.client")}
            </legend>
            <RadioGroup
              aria-label={t("clientSetup.client")}
              className="grid grid-cols-2 gap-2 min-[480px]:grid-cols-3 min-[720px]:grid-cols-6"
              value={client ?? ""}
              onValueChange={(value) => {
                setChoice(value as ClientConfigClient);
                setError(null);
              }}
              disabled={working}
            >
              {clientSetupClients.map(({ id, label: name, Icon }) => (
                <ChoiceCard
                  key={id}
                  id={`client-setup-${id}`}
                  value={id}
                  label={name}
                  description={cardNote(id)}
                  selected={client === id}
                  disabled={working || !usable(id)}
                  layout="tile"
                  // Pi's filled mark reaches the edges; inset it to match.
                  icon={<Icon size={id === "pi" ? 21 : 26} />}
                />
              ))}
            </RadioGroup>
            {statusFailed ? (
              <FormMessage tone="error">
                {t("clientSetup.statusFailed")}
              </FormMessage>
            ) : null}
            {!ccSwitch ? (
              <p className="text-xs text-muted-foreground">
                {t("clientSetup.unsupportedHint")}
              </p>
            ) : null}
          </fieldset>
          {client ? (
            <>
              <div className="grid gap-x-3 gap-y-2 rounded-md border bg-muted/40 px-3 py-2.5 text-xs min-[540px]:grid-cols-2">
                <div className="flex min-w-0 items-start gap-2">
                  <Server
                    aria-hidden="true"
                    className="size-4 shrink-0 text-muted-foreground"
                  />
                  <span className="shrink-0 text-muted-foreground">
                    {t("overview.apiAddress")}
                  </span>
                  <code className="min-w-0 break-all">{baseURL}</code>
                </div>
                <div className="flex min-w-0 items-start gap-2">
                  <Key
                    aria-hidden="true"
                    className="size-4 shrink-0 text-muted-foreground"
                  />
                  <span className="shrink-0 text-muted-foreground">
                    {t("clientSetup.token")}
                  </span>
                  <span className="min-w-0 truncate" title={token.name}>
                    {token.name}{" "}
                    <span className="font-mono text-muted-foreground">
                      {token.hint}
                    </span>
                  </span>
                </div>
                {mode === "direct" && status ? (
                  <div className="flex min-w-0 items-start gap-2 min-[540px]:col-span-2">
                    <Settings
                      aria-hidden="true"
                      className="size-4 shrink-0 text-muted-foreground"
                    />
                    <span className="shrink-0 text-muted-foreground">
                      {t("clientSetup.configFile")}
                    </span>
                    <span className="grid min-w-0 gap-1">
                      {status.paths.map((path) => (
                        <code key={path} className="break-all">
                          {path}
                        </code>
                      ))}
                    </span>
                  </div>
                ) : null}
              </div>
              {mode === "cc-switch" ? (
                <FormMessage tone="notice">
                  {t("clientSetup.ccSwitchOnlyHint", { client: label })}
                </FormMessage>
              ) : status?.state === "invalid" ? (
                <FormMessage tone="error">
                  {t("clientSetup.invalidHint")}
                </FormMessage>
              ) : status?.state === "modified" ? (
                <FormMessage tone="warning">
                  {t("clientSetup.modifiedHint")}
                </FormMessage>
              ) : status?.token_id != null && status.token_id !== token.id ? (
                <FormMessage tone="notice">
                  {otherToken
                    ? t("clientSetup.usesToken", {
                        client: label,
                        name: otherToken.name,
                        current: token.name,
                      })
                    : t("clientSetup.usesOtherToken", {
                        client: label,
                        current: token.name,
                      })}
                </FormMessage>
              ) : null}
              {proxy ? (
                <FormMessage tone={proxy.tone}>{proxy.message}</FormMessage>
              ) : null}
              <Field
                htmlFor="client-setup-model"
                label={t(
                  isClaude ? "clientSetup.defaultModel" : "clientSetup.model",
                )}
                hint={t(
                  catalogStatus === "loading"
                    ? "clientSetup.modelsLoading"
                    : catalogStatus === "error"
                      ? "clientSetup.modelsFailed"
                      : isClaude
                        ? "clientSetup.claudeModelHint"
                        : "clientSetup.modelHint",
                )}
              >
                <ModelSelect
                  id="client-setup-model"
                  aria-label={t(
                    isClaude ? "clientSetup.defaultModel" : "clientSetup.model",
                  )}
                  options={modelOptions}
                  optionAdornment={optionAdornment}
                  value={isClaude ? claudeModels.model : model}
                  placeholder={t("clientSetup.modelPlaceholder")}
                  onValueChange={(value) =>
                    isClaude
                      ? setClaudeModels((current) => ({
                          ...current,
                          model: value,
                        }))
                      : setModel(value)
                  }
                  maxLength={256}
                  disabled={working}
                />
              </Field>
              {entryNote(isClaude ? claudeModels.model : model)}
              {isClaude && (
                <div className="grid gap-3 min-[540px]:grid-cols-2">
                  {claudeTiers.map((key) => (
                    <Field
                      key={key}
                      htmlFor={`client-setup-${key}`}
                      label={t(`clientSetup.${key}`)}
                      hint={
                        key === "fableModel" && ccSwitch
                          ? t("clientSetup.fableNotImported")
                          : undefined
                      }
                    >
                      <ModelSelect
                        id={`client-setup-${key}`}
                        aria-label={t(`clientSetup.${key}`)}
                        options={modelOptions}
                        optionAdornment={optionAdornment}
                        value={claudeModels[key]}
                        onValueChange={(value) =>
                          setClaudeModels((current) => ({
                            ...current,
                            [key]: value,
                          }))
                        }
                        placeholder={t("clientSetup.modelPlaceholder")}
                        maxLength={256}
                        disabled={working}
                      />
                      {entryNote(claudeModels[key])}
                    </Field>
                  ))}
                </div>
              )}
              {mode === "direct" && isDirectClient(client) ? (
                <HelpDisclosure title={t("clientSetup.limitsTitle")}>
                  <p>{t(`clientSetup.limits.${client}`)}</p>
                  {offersCCSwitch ? (
                    <p>{t("clientSetup.ccSwitchOverwrites")}</p>
                  ) : null}
                </HelpDisclosure>
              ) : null}
            </>
          ) : null}
          {error && (
            <FormMessage tone="error">
              {error.message}
              {error.detail ? (
                <span className="mt-1 block break-all">{error.detail}</span>
              ) : null}
            </FormMessage>
          )}
          <DialogFooter className="border-t pt-4 min-[540px]:flex-row min-[540px]:justify-end">
            {removable ? (
              <Button
                type="button"
                variant="ghost"
                className="min-[540px]:mr-auto"
                disabled={working}
                onClick={() => setRemoval(removable)}
              >
                {t(
                  busy === "remove"
                    ? "clientSetup.removing"
                    : "clientSetup.remove",
                )}
              </Button>
            ) : null}
            <Button
              type="button"
              variant="outline"
              disabled={working}
              onClick={onClose}
            >
              {t("common.close")}
            </Button>
            {mode === "direct" && offersCCSwitch ? (
              <Button
                type="button"
                variant="outline"
                disabled={working || modelRequired}
                onClick={() => void importWithCCSwitch()}
              >
                <CCSwitchIcon size={16} />
                {t(
                  busy === "cc-switch"
                    ? "clientSetup.ccSwitchOpening"
                    : "clientSetup.ccSwitchImport",
                )}
                <ArrowUpRight />
              </Button>
            ) : null}
            {mode === "cc-switch" ? (
              <Button type="submit" disabled={working || modelRequired}>
                <CCSwitchIcon size={16} />
                {t(
                  busy === "cc-switch"
                    ? "clientSetup.ccSwitchOpening"
                    : "clientSetup.ccSwitchImport",
                )}
                <ArrowUpRight />
              </Button>
            ) : (
              <Button
                type="submit"
                disabled={
                  working ||
                  mode !== "direct" ||
                  modelRequired ||
                  status?.state === "invalid"
                }
              >
                {t(
                  busy === "write"
                    ? "clientSetup.writing"
                    : configured
                      ? "clientSetup.update"
                      : "clientSetup.write",
                )}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
      <ConfirmDialog
        open={conflict !== null}
        title={t("clientSetup.replaceTitle")}
        description={
          conflict ? (
            <>
              <p>
                {t("clientSetup.replaceBody", {
                  client: clientLabel(conflict.target.client),
                })}
              </p>
              <ul className="grid gap-1">
                {conflict.keys.map((key) => (
                  <li key={key}>
                    <code className="break-all text-xs">{key}</code>
                  </li>
                ))}
              </ul>
            </>
          ) : null
        }
        confirmLabel={t("clientSetup.replaceConfirm")}
        onCancel={() => setConflict(null)}
        onConfirm={() => {
          if (!conflict) return;
          setConflict(null);
          void write(conflict.target, true);
        }}
      />
      <ConfirmDialog
        open={removal !== null}
        destructive
        title={t("clientSetup.removeTitle", {
          client: removal ? clientLabel(removal) : "",
        })}
        description={
          <p>
            {t("clientSetup.removeBody", {
              client: removal ? clientLabel(removal) : "",
            })}
          </p>
        }
        confirmLabel={t("clientSetup.remove")}
        onCancel={() => setRemoval(null)}
        onConfirm={() => {
          if (!removal) return;
          setRemoval(null);
          void remove(removal);
        }}
      />
    </Dialog>
  );
}
