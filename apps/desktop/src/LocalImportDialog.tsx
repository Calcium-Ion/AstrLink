import { useEffect, useMemo, useRef, useState } from "react";

import { AgentToolIcon } from "@/components/AgentToolIcon";
import type { DialogPickerGroup } from "@/components/DialogPicker";
import { FormMessage } from "@/components/FormMessage";
import { PackageLogo } from "@/components/PackageCard";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { StatusBadge } from "@/components/StatusBadge";
import { ScanText } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
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
import { cn } from "@/lib/utils";

import {
  confirmLocalImport,
  dismissLocalImport,
  getService,
  openLocalImport,
  probeLocalImportModels,
  probeServiceModels,
  readLocalImport,
  updateService,
} from "./bridge";
import { useT } from "./i18n";
import {
  closedImport,
  detectKinds,
  discoveryProtocol,
  existingMatch,
  importModels,
  insecureAddress,
  localImportToolNames,
  planCandidate,
  probeFailure,
  servicesSharingAddresses,
  type ExistingMatch,
  type LocalImportCandidate,
  type LocalImportOpened,
  type LocalImportPlace,
  type LocalImportRead,
  type LocalImportSkipped,
  type LocalImportTool,
  type ModelOrigin,
  type ProbeFailure,
} from "./local-import-model";
import {
  providerImportInput,
  type ProviderImportPlan,
} from "./provider-import-model";
import {
  serviceKindLabel,
  type HTTPServiceKind,
  type ModelDiscoveryProtocol,
  type Service,
} from "./service-model";
import type { ProtocolDescriptor } from "./service-presets";

type Translate = ReturnType<typeof useT>;

const allTools: LocalImportTool[] = ["claude", "codex", "pi"];

export const localImportPickerValue = "local-import";

function toolList(t: Translate, tools: readonly LocalImportTool[]): string {
  return tools
    .map((tool) => localImportToolNames[tool])
    .join(t("localImport.toolSeparator"));
}

/** The kind picker's entry for providers the agent tools already use. */
export function localImportPickerGroup(
  t: Translate,
): DialogPickerGroup<typeof localImportPickerValue> {
  return {
    label: t("localImport.pickerGroup"),
    options: [
      {
        value: localImportPickerValue,
        label: t("localImport.pickerOption"),
        description: toolList(t, allTools),
        icon: <ScanText className="size-5 text-foreground" />,
      },
    ],
  };
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/** The address without `https://`; plain http stays visible. */
function shownAddress(address: string): string {
  return address.replace(/^https:\/\//, "");
}

function placeLabel(place: LocalImportPlace): string {
  const name = localImportToolNames[place.tool];
  return place.wsl ? `${name} · ${place.wsl}` : name;
}

function Dot() {
  return <span aria-hidden="true">·</span>;
}

function Sources({ places }: { places: readonly LocalImportPlace[] }) {
  return (
    <span className="ml-auto flex shrink-0 items-center gap-2.5 text-xs text-muted-foreground">
      {places.map((place) => (
        <span
          className="flex items-center gap-1"
          key={`${place.tool}:${place.wsl ?? ""}`}
        >
          <AgentToolIcon id={place.tool} size={14} />
          {placeLabel(place)}
        </span>
      ))}
    </span>
  );
}

/** A translation with `value` in a code span, wherever the locale puts it. */
function WithCode({ text, value }: { text: string; value: string }) {
  const [before, after = ""] = text.split("\u0000");
  return (
    <span>
      {before}
      <code className="font-mono">{value}</code>
      {after}
    </span>
  );
}

interface Row {
  candidate: LocalImportCandidate;
  kinds: HTTPServiceKind[];
  kind: HTTPServiceKind;
  plan: ProviderImportPlan | null;
  name: string;
  match: ExistingMatch | null;
  needsKey: boolean;
  secret: string;
  /** No list to fetch and none configured: nothing the provider could serve. */
  noModels: boolean;
  importable: boolean;
}

interface RowContext {
  services: readonly Service[];
  hints: ReadonlyMap<string, string | null>;
  protocols: readonly ProtocolDescriptor[];
}

function buildRow(
  candidate: LocalImportCandidate,
  choice: HTTPServiceKind | undefined,
  secret: string,
  context: RowContext,
): Row {
  const kinds = detectKinds(candidate.address);
  const kind = choice && kinds.includes(choice) ? choice : kinds[0];
  const plan = planCandidate(candidate, kind, context.protocols);
  const match = existingMatch(candidate, context.services, context.hints);
  const needsKey = candidate.key.status !== "found";
  const noModels =
    plan !== null &&
    discoveryProtocol(plan.capabilities) === null &&
    plan.models.length === 0;
  return {
    candidate,
    kinds,
    kind,
    plan,
    name:
      plan?.defaultName ?? candidate.name ?? shownAddress(candidate.address),
    match,
    needsKey,
    secret,
    noModels,
    importable:
      plan !== null &&
      !noModels &&
      !match?.sameKey &&
      (!needsKey || secret.trim() !== ""),
  };
}

interface Outcome {
  id: string;
  name: string;
  kind: HTTPServiceKind;
  address: string;
  status: "imported" | "fallback" | "failed";
  origin: ModelOrigin | null;
  /** Models fetched, or saved when they came from the config or preset. */
  count: number;
  reason: ProbeFailure | null;
  message: string | null;
  service: { id: string; etag: string; models: string[] } | null;
  discovery: ModelDiscoveryProtocol | null;
  refetching: boolean;
}

class EmptyModelList extends Error {}

/**
 * Fetches the provider's model list once with the key the host holds, then
 * creates it. Without a list it keeps the configured or preset models, and
 * without any models it is not created.
 */
async function importRow(
  t: Translate,
  session: string,
  row: Row & { plan: ProviderImportPlan },
  onImported: (service: Service) => void,
): Promise<Outcome> {
  const { plan } = row;
  const discovery = discoveryProtocol(plan.capabilities);
  const outcome: Outcome = {
    id: row.candidate.id,
    name: row.name,
    kind: row.kind,
    // The address the list showed, so each outcome reads as the same row.
    address: row.candidate.address,
    status: "failed",
    origin: null,
    count: 0,
    reason: null,
    message: null,
    service: null,
    discovery,
    refetching: false,
  };
  const input = providerImportInput(plan, row.name, row.secret.trim());
  let fetched: string[] | null = null;
  if (discovery) {
    try {
      const probe = await probeLocalImportModels(session, row.candidate.id, {
        kind: plan.kind,
        http: input.http,
        protocol: discovery,
      });
      if (probe.model_ids.length > 0) fetched = probe.model_ids;
      else outcome.reason = "empty";
    } catch (cause) {
      if (closedImport(cause)) {
        return { ...outcome, message: t("localImport.closed") };
      }
      outcome.reason = probeFailure(cause);
    }
  }
  const models = importModels(fetched, row.candidate, plan);
  if (!models) return outcome;
  try {
    const record = await confirmLocalImport(session, row.candidate.id, {
      ...input,
      models: models.models,
    });
    onImported(record.service);
    return {
      ...outcome,
      status: discovery && !fetched ? "fallback" : "imported",
      origin: models.origin,
      count: fetched ? fetched.length : models.models.length,
      service: {
        id: record.service.id,
        etag: record.etag,
        models: record.service.models,
      },
    };
  } catch (cause) {
    return {
      ...outcome,
      reason: null,
      message: closedImport(cause)
        ? t("localImport.closed")
        : t("localImport.failed", { message: messageOf(cause) }),
    };
  }
}

function probeReason(t: Translate, reason: ProbeFailure): string {
  switch (reason) {
    case "timeout":
      return t("localImport.probeReasons.timeout");
    case "auth":
      return t("localImport.probeReasons.auth");
    case "notFound":
      return t("localImport.probeReasons.notFound");
    case "empty":
      return t("localImport.probeReasons.empty");
    case "other":
      return t("localImport.probeReasons.other");
  }
}

function unreadableReason(t: Translate, code: string | null): string {
  switch (code) {
    case "permission":
      return t("localImport.unreadableReasons.permission");
    case "too_large":
      return t("localImport.unreadableReasons.tooLarge");
    case "not_text":
      return t("localImport.unreadableReasons.notText");
    default:
      return t("localImport.unreadableReasons.io");
  }
}

function skippedLine(t: Translate, item: LocalImportSkipped): string {
  const tool = placeLabel(item);
  const name = item.name ?? "";
  const path =
    item.path && item.wsl
      ? t("localImport.skipped.inWsl", { path: item.path, name: item.wsl })
      : (item.path ?? "");
  switch (item.reason) {
    case "chatgpt_login":
      return t("localImport.skipped.chatgptLogin", { tool });
    case "astrlink":
      return item.name
        ? t("localImport.skipped.astrlink", { tool, name })
        : t("localImport.skipped.astrlinkUnnamed", { tool });
    case "missing":
      return t("localImport.skipped.missing", { path });
    case "unreadable":
      return t("localImport.skipped.unreadable", {
        path,
        reason: unreadableReason(t, item.detail),
      });
    case "invalid":
      return t("localImport.skipped.invalid", {
        path,
        detail: item.detail ?? "",
      });
    case "invalid_address":
      return item.name
        ? t("localImport.skipped.invalidAddress", { tool, name })
        : t("localImport.skipped.invalidAddressUnnamed", { tool });
    case "unsupported":
      return item.detail
        ? t("localImport.skipped.unsupportedFormat", {
            tool,
            name,
            format: item.detail,
          })
        : t("localImport.skipped.unsupported", { tool, name });
    case "sign_in":
      return t("localImport.skipped.signIn", { tool, name });
  }
}

function KeyLine({ t, row }: { t: Translate; row: Row }) {
  const { key } = row.candidate;
  switch (key.status) {
    case "found":
      return key.hint ? (
        <span>
          {t("localImport.key")} <span className="font-mono">{key.hint}</span>
        </span>
      ) : (
        <span>{t("localImport.keyRead")}</span>
      );
    case "env":
      return (
        <WithCode
          text={t("localImport.keyInEnv", { name: "\u0000" })}
          value={key.name}
        />
      );
    case "command":
      return <span>{t("localImport.keyByCommand")}</span>;
    case "missing":
      return <span>{t("localImport.keyMissing")}</span>;
  }
}

function modelsLine(t: Translate, row: Row): string {
  if (row.match) {
    return row.match.sameKey
      ? t("localImport.sameAs", { name: row.match.service.name })
      : t("localImport.sameAddress", { name: row.match.service.name });
  }
  const { plan } = row;
  if (!plan || row.noModels) return t("localImport.noModels");
  if (
    row.candidate.models.length > 0 ||
    !discoveryProtocol(plan.capabilities)
  ) {
    return t("localImport.modelCount", { count: plan.models.length });
  }
  return t("localImport.modelsOnImport");
}

function FoundRow({
  disabled,
  onKey,
  onKind,
  onSelect,
  row,
  selected,
  t,
}: {
  disabled: boolean;
  onKey: (value: string) => void;
  onKind: (kind: HTTPServiceKind) => void;
  onSelect: (selected: boolean) => void;
  row: Row;
  selected: boolean;
  t: Translate;
}) {
  const added = Boolean(row.match?.sameKey);
  const choosing = row.kinds.length > 1 && !added;
  // A preset is named after its type; say it once.
  const named = row.name !== serviceKindLabel(row.kind);
  return (
    <div
      className={cn(
        "flex min-w-0 items-start gap-3 rounded-lg border px-3 py-2.5",
        added && "bg-muted/30",
      )}
    >
      <Checkbox
        aria-label={row.name}
        checked={selected && row.importable}
        className="mt-2.5"
        disabled={disabled || !row.importable}
        onCheckedChange={(value) => onSelect(value === true)}
      />
      <span className={cn(added && "opacity-50")}>
        <PackageLogo>
          <ServiceKindIcon kind={row.kind} size={20} />
        </PackageLogo>
      </span>
      <div className="grid min-w-0 flex-1 gap-1">
        <div className="flex min-h-7 min-w-0 items-center gap-2">
          {named || !choosing ? (
            <span
              className={cn(
                "truncate text-sm font-medium",
                added && "text-muted-foreground",
              )}
            >
              {row.name}
            </span>
          ) : null}
          {choosing ? (
            <Select
              disabled={disabled}
              onValueChange={(next) => {
                // Radix reports a blank value while options mount.
                if (next) onKind(next as HTTPServiceKind);
              }}
              value={row.kind}
            >
              <SelectTrigger
                aria-label={t("localImport.kindLabel")}
                className="h-6 gap-1 px-2 text-xs"
                size="sm"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {row.kinds.map((choice) => (
                  <SelectItem key={choice} value={choice}>
                    {serviceKindLabel(choice)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : named ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              {serviceKindLabel(row.kind)}
            </span>
          ) : null}
          {added ? (
            <StatusBadge tone="neutral">{t("localImport.added")}</StatusBadge>
          ) : null}
          {row.needsKey && !added ? (
            <StatusBadge tone="pending">
              {t("localImport.needsKey")}
            </StatusBadge>
          ) : null}
          <Sources places={row.candidate.sources} />
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
          <code className="truncate font-mono">
            {shownAddress(row.candidate.address)}
          </code>
          <Dot />
          <KeyLine row={row} t={t} />
          <Dot />
          <span>{modelsLine(t, row)}</span>
        </div>
        {choosing ? (
          <p className="text-xs text-text-secondary">
            {t("localImport.ambiguous")}
          </p>
        ) : null}
        {row.noModels && !added ? (
          <p className="text-xs text-text-secondary">
            {t("localImport.noModelsHint")}
          </p>
        ) : null}
        {insecureAddress(row.candidate.address) && !added ? (
          <FormMessage className="mt-1" tone="warning">
            {t("localImport.insecure")}
          </FormMessage>
        ) : null}
        {row.needsKey && !added ? (
          <div className="mt-1 max-w-80">
            <Input
              aria-label={t("localImport.keyLabel", { name: row.name })}
              autoComplete="new-password"
              className="h-8 text-xs"
              disabled={disabled}
              maxLength={16_384}
              onChange={(event) => onKey(event.currentTarget.value)}
              placeholder={t("localImport.keyPlaceholder")}
              type="password"
              value={row.secret}
            />
          </div>
        ) : null}
      </div>
    </div>
  );
}

function OutcomeDetail({
  onRefetch,
  outcome,
  t,
}: {
  onRefetch: () => void;
  outcome: Outcome;
  t: Translate;
}) {
  const preset = serviceKindLabel(outcome.kind);
  if (outcome.status === "failed") {
    return (
      <span>
        {outcome.message ??
          t("localImport.noModelsFailed", {
            reason: probeReason(t, outcome.reason ?? "other"),
          })}
      </span>
    );
  }
  if (outcome.status === "imported") {
    return (
      <span>
        {outcome.origin === "fetched"
          ? t("localImport.fetched", { count: outcome.count })
          : outcome.origin === "preset"
            ? t("localImport.fromPreset", { count: outcome.count, preset })
            : t("localImport.fromConfig", { count: outcome.count })}
      </span>
    );
  }
  const reason = probeReason(t, outcome.reason ?? "other");
  return (
    <span>
      {outcome.origin === "preset"
        ? t("localImport.fallbackPreset", {
            count: outcome.count,
            preset,
            reason,
          })
        : t("localImport.fallbackConfig", { count: outcome.count, reason })}
      <Button
        className="ml-1 h-auto p-0 align-baseline text-xs"
        disabled={outcome.refetching}
        onClick={onRefetch}
        type="button"
        variant="link"
      >
        {outcome.refetching
          ? t("localImport.refetching")
          : t("localImport.refetch")}
      </Button>
    </span>
  );
}

function OutcomeRow({
  onRefetch,
  outcome,
  t,
}: {
  onRefetch: () => void;
  outcome: Outcome;
  t: Translate;
}) {
  return (
    <div className="flex min-w-0 items-start gap-3 rounded-lg border px-3 py-2.5">
      <PackageLogo>
        <ServiceKindIcon kind={outcome.kind} size={20} />
      </PackageLogo>
      <div className="grid min-w-0 flex-1 gap-1">
        <div className="flex min-h-7 min-w-0 items-center gap-2">
          <span className="truncate text-sm font-medium">{outcome.name}</span>
          {outcome.name !== serviceKindLabel(outcome.kind) ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              {serviceKindLabel(outcome.kind)}
            </span>
          ) : null}
          <StatusBadge
            className="ml-auto"
            tone={
              outcome.status === "imported"
                ? "positive"
                : outcome.status === "fallback"
                  ? "pending"
                  : "negative"
            }
          >
            {outcome.status === "failed"
              ? t("localImport.notImported")
              : t("localImport.imported")}
          </StatusBadge>
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
          <code className="truncate font-mono">
            {shownAddress(outcome.address)}
          </code>
          <Dot />
          <OutcomeDetail onRefetch={onRefetch} outcome={outcome} t={t} />
        </div>
      </div>
    </div>
  );
}

type Stage =
  | "locating"
  | "consent"
  | "reading"
  | "results"
  | "importing"
  | "done"
  | "error";

/**
 * Imports providers from Claude Code, Codex and Pi. Nothing is read until
 * the user picks tools and asks for it; found providers start unchecked and
 * keys stay in the host, which drops them when the dialog closes.
 */
export function LocalImportDialog({
  onClose,
  onImported,
  protocols,
  services,
}: {
  onClose: () => void;
  onImported: (service: Service) => void;
  protocols: readonly ProtocolDescriptor[];
  services: readonly Service[];
}) {
  const t = useT();
  const [stage, setStage] = useState<Stage>("locating");
  const [error, setError] = useState<string | null>(null);
  const [opened, setOpened] = useState<LocalImportOpened | null>(null);
  const [checked, setChecked] = useState<ReadonlySet<string>>(new Set());
  const [read, setRead] = useState<LocalImportRead | null>(null);
  const [readTools, setReadTools] = useState<LocalImportTool[]>([]);
  const [hints, setHints] = useState<ReadonlyMap<string, string | null>>(
    new Map(),
  );
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [choices, setChoices] = useState<
    Readonly<Record<string, HTTPServiceKind>>
  >({});
  const [secrets, setSecrets] = useState<Readonly<Record<string, string>>>({});
  const [outcomes, setOutcomes] = useState<Outcome[]>([]);
  const [attempt, setAttempt] = useState(0);
  const mounted = useRef(true);
  const session = useRef<string | null>(null);
  const started = useRef(-1);

  const endSession = () => {
    const id = session.current;
    session.current = null;
    if (id) {
      dismissLocalImport(id).catch((cause: unknown) =>
        console.error("Unable to end the local import", cause),
      );
    }
  };

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      const id = session.current;
      session.current = null;
      if (id) {
        dismissLocalImport(id).catch((cause: unknown) =>
          console.error("Unable to end the local import", cause),
        );
      }
    };
  }, []);

  // Finds the files to list; none is opened until the user asks to read.
  useEffect(() => {
    if (started.current === attempt) return;
    started.current = attempt;
    openLocalImport().then(
      (result) => {
        if (!mounted.current) {
          void dismissLocalImport(result.session).catch(() => undefined);
          return;
        }
        session.current = result.session;
        setOpened(result);
        setChecked(new Set(result.sources.map((source) => source.id)));
        setStage("consent");
      },
      (cause: unknown) => {
        if (!mounted.current) return;
        setError(t("localImport.locateFailed", { message: messageOf(cause) }));
        setStage("error");
      },
    );
  }, [attempt, t]);

  const context = useMemo<RowContext>(
    () => ({ services, hints, protocols }),
    [services, hints, protocols],
  );
  const rows = useMemo(
    () =>
      read?.candidates.map((candidate) =>
        buildRow(
          candidate,
          choices[candidate.id],
          secrets[candidate.id] ?? "",
          context,
        ),
      ) ?? [],
    [read, choices, secrets, context],
  );
  const importable = rows.filter((row) => row.importable);
  const chosen = importable.filter((row) => selected.has(row.candidate.id));
  const busy = stage === "reading" || stage === "importing";

  const close = () => {
    if (busy) return;
    endSession();
    onClose();
  };

  const restart = () => {
    endSession();
    setOpened(null);
    setRead(null);
    setError(null);
    setSelected(new Set());
    setChoices({});
    setSecrets({});
    setOutcomes([]);
    setStage("locating");
    setAttempt((current) => current + 1);
  };

  const startRead = async () => {
    if (!opened || stage !== "consent") return;
    const sources = opened.sources.filter((source) => checked.has(source.id));
    if (sources.length === 0) return;
    setStage("reading");
    try {
      const result = await readLocalImport(
        opened.session,
        sources.map((source) => source.id),
      );
      // Single reads carry the saved-key hint that tells a repeat apart.
      const known = await Promise.allSettled(
        servicesSharingAddresses(result.candidates, services).map(
          async (service) =>
            [
              service.id,
              service.http?.credential_hint ??
                (await getService(service.id)).service.http?.credential_hint ??
                null,
            ] as const,
        ),
      );
      if (!mounted.current) return;
      setHints(
        new Map(
          known.flatMap((item) =>
            item.status === "fulfilled" ? [item.value] : [],
          ),
        ),
      );
      setReadTools(
        allTools.filter((tool) =>
          sources.some((source) => source.tool === tool),
        ),
      );
      setRead(result);
      setStage("results");
    } catch (cause) {
      if (!mounted.current) return;
      setError(
        closedImport(cause)
          ? t("localImport.closed")
          : t("localImport.readFailed", { message: messageOf(cause) }),
      );
      setStage("error");
    }
  };

  const startImport = async () => {
    if (!opened || stage !== "results" || chosen.length === 0) return;
    setStage("importing");
    const results: Outcome[] = [];
    for (const row of chosen) {
      if (!row.plan) continue;
      results.push(
        await importRow(
          t,
          opened.session,
          { ...row, plan: row.plan },
          onImported,
        ),
      );
    }
    endSession();
    if (!mounted.current) return;
    setSecrets({});
    setOutcomes(results);
    setStage("done");
  };

  const updateOutcome = (id: string, patch: Partial<Outcome>) => {
    if (!mounted.current) return;
    setOutcomes((current) =>
      current.map((item) => (item.id === id ? { ...item, ...patch } : item)),
    );
  };

  // A second model-list request, only when the user asks for it.
  const refetch = async (outcome: Outcome) => {
    const { service, discovery } = outcome;
    if (!service || !discovery || outcome.refetching) return;
    updateOutcome(outcome.id, { refetching: true });
    try {
      const probe = await probeServiceModels(service.id, discovery);
      if (probe.model_ids.length === 0) throw new EmptyModelList();
      const models = [...new Set([...probe.model_ids, ...service.models])];
      const record = await updateService(service.id, service.etag, { models });
      onImported(record.service);
      updateOutcome(outcome.id, {
        status: "imported",
        origin: "fetched",
        count: probe.model_ids.length,
        reason: null,
        refetching: false,
        service: {
          id: record.service.id,
          etag: record.etag,
          models: record.service.models,
        },
      });
    } catch (cause) {
      updateOutcome(outcome.id, {
        refetching: false,
        reason: cause instanceof EmptyModelList ? "empty" : probeFailure(cause),
      });
    }
  };

  const selectRow = (id: string, value: boolean) =>
    setSelected((current) => {
      const next = new Set(current);
      if (value) next.add(id);
      else next.delete(id);
      return next;
    });

  // A row that stops being importable also stops being chosen.
  const keepChoice = (candidate: LocalImportCandidate, row: Row) => {
    if (!row.importable) selectRow(candidate.id, false);
  };

  const imported = outcomes.filter((item) => item.status !== "failed").length;
  const failed = outcomes.length - imported;
  const description =
    stage === "results" || stage === "importing"
      ? rows.length > 0
        ? t("localImport.resultsDescription", {
            tools: toolList(t, readTools),
            count: rows.length,
          })
        : t("localImport.nothingFound", { tools: toolList(t, readTools) })
      : stage === "done"
        ? imported === 0
          ? t("localImport.doneNone")
          : failed > 0
            ? t("localImport.donePartial", { count: imported, failed })
            : t("localImport.doneDescription", { count: imported })
        : t("localImport.consentDescription");
  const allChosen =
    importable.length > 0 && chosen.length === importable.length;

  return (
    <Dialog open onOpenChange={(open) => !open && close()}>
      <DialogContent
        className="top-[calc(50%+var(--window-chrome-height)/2)] flex max-h-[min(calc(100dvh-var(--window-chrome-height)-2rem),44rem)] w-[calc(100vw_-_2rem)] max-w-none flex-col gap-4 sm:max-w-2xl"
        showCloseButton={!busy}
      >
        <DialogHeader className="shrink-0 text-left">
          <DialogTitle className="flex items-center gap-2.5">
            <ScanText
              aria-hidden="true"
              className="size-5 text-muted-foreground"
            />
            {t("localImport.title")}
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        {stage === "locating" ? (
          <p className="text-xs text-muted-foreground">
            {t("localImport.locating")}
          </p>
        ) : null}

        {(stage === "consent" || stage === "reading") && opened ? (
          <fieldset
            className="grid min-h-0 min-w-0 gap-2 overflow-y-auto"
            disabled={stage === "reading"}
          >
            <legend className="sr-only">
              {t("localImport.consentLegend")}
            </legend>
            {opened.sources.map((source) => (
              <Label
                className="flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border px-3 py-2.5 font-normal leading-normal transition-colors hover:border-primary/40"
                htmlFor={`local-import-${source.id}`}
                key={source.id}
              >
                <Checkbox
                  checked={checked.has(source.id)}
                  className="mt-0.5"
                  id={`local-import-${source.id}`}
                  onCheckedChange={(value) =>
                    setChecked((current) => {
                      const next = new Set(current);
                      if (value === true) next.add(source.id);
                      else next.delete(source.id);
                      return next;
                    })
                  }
                />
                <AgentToolIcon className="mt-px" id={source.tool} size={18} />
                <span className="grid min-w-0 gap-1">
                  <span className="text-sm leading-5 font-medium">
                    {localImportToolNames[source.tool]}
                    {source.wsl ? (
                      <span className="ml-1.5 text-xs font-normal text-muted-foreground">
                        WSL · {source.wsl}
                      </span>
                    ) : null}
                  </span>
                  <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-muted-foreground">
                    {source.paths.map((file) => (
                      <code className="font-mono break-all" key={file}>
                        {file}
                      </code>
                    ))}
                  </span>
                  {source.tool === "codex" ? (
                    <span className="text-xs text-muted-foreground">
                      {t("localImport.codexNote")}
                    </span>
                  ) : null}
                </span>
              </Label>
            ))}
          </fieldset>
        ) : null}

        {(stage === "results" || stage === "importing") && read ? (
          <div className="flex min-h-0 flex-1 flex-col gap-2">
            {rows.length > 0 ? (
              <Label
                className="flex shrink-0 items-center gap-3 px-3 text-xs leading-normal font-normal text-text-secondary"
                htmlFor="local-import-all"
              >
                <Checkbox
                  checked={allChosen}
                  disabled={stage === "importing" || importable.length === 0}
                  id="local-import-all"
                  onCheckedChange={(value) =>
                    setSelected(
                      value === true
                        ? new Set(importable.map((row) => row.candidate.id))
                        : new Set(),
                    )
                  }
                />
                <span className="font-medium text-foreground">
                  {t("localImport.selectAll")}
                </span>
                <span>
                  {t("localImport.selection", {
                    available: importable.length,
                    selected: chosen.length,
                  })}
                </span>
              </Label>
            ) : null}
            <div
              className="grid min-h-0 flex-1 content-start gap-2 overflow-y-auto pr-0.5"
              data-testid="local-import-list"
            >
              {rows.map((row) => (
                <FoundRow
                  disabled={stage === "importing"}
                  key={row.candidate.id}
                  onKey={(value) => {
                    setSecrets((current) => ({
                      ...current,
                      [row.candidate.id]: value,
                    }));
                    keepChoice(
                      row.candidate,
                      buildRow(row.candidate, row.kind, value, context),
                    );
                  }}
                  onKind={(kind) => {
                    setChoices((current) => ({
                      ...current,
                      [row.candidate.id]: kind,
                    }));
                    keepChoice(
                      row.candidate,
                      buildRow(row.candidate, kind, row.secret, context),
                    );
                  }}
                  onSelect={(value) => selectRow(row.candidate.id, value)}
                  row={row}
                  selected={selected.has(row.candidate.id)}
                  t={t}
                />
              ))}
              {read.skipped.length > 0 ? (
                <div className="grid gap-1 px-1 pt-1 text-xs text-muted-foreground">
                  <span className="font-medium text-text-secondary">
                    {t("localImport.skippedTitle", {
                      count: read.skipped.length,
                    })}
                  </span>
                  {read.skipped.map((item, index) => (
                    <span key={index}>{skippedLine(t, item)}</span>
                  ))}
                </div>
              ) : null}
            </div>
          </div>
        ) : null}

        {stage === "done" ? (
          <div
            className="grid min-h-0 flex-1 content-start gap-2 overflow-y-auto"
            data-testid="local-import-list"
          >
            {outcomes.map((outcome) => (
              <OutcomeRow
                key={outcome.id}
                onRefetch={() => void refetch(outcome)}
                outcome={outcome}
                t={t}
              />
            ))}
          </div>
        ) : null}

        {stage === "error" && error ? (
          <FormMessage tone="error">{error}</FormMessage>
        ) : null}

        <DialogFooter className="shrink-0 border-t pt-4 min-[540px]:flex-row min-[540px]:items-center min-[540px]:justify-end">
          {(stage === "results" || stage === "importing") && rows.length > 0 ? (
            <p className="text-xs text-text-secondary min-[540px]:mr-auto">
              {t("localImport.footerNote")}
            </p>
          ) : null}
          {stage === "done" ? (
            <Button onClick={close} type="button">
              {t("localImport.done")}
            </Button>
          ) : (stage === "results" || stage === "importing") &&
            rows.length === 0 ? (
            <Button onClick={close} type="button">
              {t("common.close")}
            </Button>
          ) : (
            <>
              <Button
                disabled={busy}
                onClick={close}
                type="button"
                variant="outline"
              >
                {t("common.cancel")}
              </Button>
              {stage === "error" ? (
                <Button onClick={restart} type="button">
                  {t("localImport.restart")}
                </Button>
              ) : stage === "results" || stage === "importing" ? (
                <Button
                  disabled={stage === "importing" || chosen.length === 0}
                  onClick={() => void startImport()}
                  type="button"
                >
                  {stage === "importing"
                    ? t("localImport.importing")
                    : chosen.length === 0
                      ? t("localImport.import")
                      : t("localImport.importCount", { count: chosen.length })}
                </Button>
              ) : (
                <Button
                  disabled={stage !== "consent" || checked.size === 0}
                  onClick={() => void startRead()}
                  type="button"
                >
                  <ScanText aria-hidden="true" />
                  {stage === "reading"
                    ? t("localImport.reading")
                    : t("localImport.read")}
                </Button>
              )}
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
