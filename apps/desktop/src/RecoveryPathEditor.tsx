import { ModelSelect } from "./components/ModelSelect";
import { useEffect, useMemo, useState } from "react";
import {
  createRecoveryPath,
  updateRecoveryPath,
  previewRecoveryPath,
} from "./bridge";
import { useRoutingDefaults } from "./use-routing-defaults";
import {
  pathNodes,
  pathNodeKey,
  parseRecoveryPath,
  type RecoveryPath,
  type RecoveryPathNode,
  type RecoveryPathRecord,
  type RecoveryPreview,
} from "./recovery-path-model";
import type { RoutableService } from "./service-model";
import { protocolLabel } from "./service-presets";
import { useT } from "./i18n";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { Switch } from "./components/ui/switch";
import { Field } from "./components/Field";
import { FilterSelect } from "./components/FilterSelect";
import { Panel, PanelHeader } from "./components/Panel";
import { OrderedList } from "./components/OrderedList";
import { FormMessage } from "./components/FormMessage";
import { ConfirmDialog } from "./components/ConfirmDialog";
import { FailurePolicyEditor } from "./components/FailurePolicyEditor";
import { RecoveryPathPreview } from "./components/RecoveryPathPreview";
export const newPathNodeID = () =>
  `node_${crypto.randomUUID().replaceAll("-", "")}`;
export function RecoveryPathEditor({
  record,
  services,
  ready,
  initialProtocol,
  onSaved,
  onDirtyChange,
}: {
  record?: RecoveryPathRecord;
  services: RoutableService[];
  ready: boolean;
  initialProtocol?: string;
  onSaved: (record: RecoveryPathRecord) => void;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const t = useT(),
    defaults = useRoutingDefaults(ready);
  const [draft, setDraft] = useState<RecoveryPath>(
    () =>
      record?.path ?? {
        id: "path_draft",
        name: "",
        protocol: initialProtocol ?? "openai.responses",
        mode: "automatic",
        targets: [],
      },
  );
  const [baseline, setBaseline] = useState(() => JSON.stringify(draft)),
    [error, setError] = useState(""),
    [saving, setSaving] = useState(false),
    [preview, setPreview] = useState<RecoveryPreview | null>(null),
    [previewError, setPreviewError] = useState(""),
    [previewBusy, setPreviewBusy] = useState(false);
  const [scenario, setScenario] = useState("network_error"),
    [successAt, setSuccessAt] = useState(0),
    [requestModel, setRequestModel] = useState(""),
    [streaming, setStreaming] = useState(false),
    [retryAfter, setRetryAfter] = useState("");
  const [conversion, setConversion] = useState<RecoveryPath | null>(null);
  const dirty = JSON.stringify(draft) !== baseline;
  useEffect(() => {
    onDirtyChange?.(dirty);
    return () => onDirtyChange?.(false);
  }, [dirty, onDirtyChange]);
  const protocols = useMemo(
    () => [
      ...new Set([
        draft.protocol,
        ...services
          .flatMap((service) =>
            service.capabilities.map((capability) => capability.protocol),
          )
          .filter((protocol) => !protocol.includes("models")),
      ]),
    ],
    [draft.protocol, services],
  );
  const nodes = pathNodes(draft);
  const updateNodes = (items: RecoveryPathNode[]) =>
    setDraft((current) =>
      current.mode === "steps"
        ? { ...current, steps: items }
        : { ...current, targets: items },
    );
  const updateNode = (id: string, patch: Partial<RecoveryPathNode>) =>
    updateNodes(
      nodes.map((node) => (node.id === id ? { ...node, ...patch } : node)),
    );
  let valid = true;
  try {
    parseRecoveryPath(draft);
  } catch {
    valid = false;
  }
  const signature = JSON.stringify(draft);
  useEffect(() => {
    setPreview(null);
    if (!ready || !valid) {
      setPreviewBusy(false);
      return;
    }
    let active = true;
    setPreviewBusy(true);
    const timeout = setTimeout(() => {
      void previewRecoveryPath({
        path: draft,
        model: requestModel,
        streaming,
        error: scenario,
        success_at: successAt,
        retry_after: retryAfter,
      })
        .then((value) => {
          if (active) {
            setPreview(value);
            setPreviewError("");
          }
        })
        .catch((error) => {
          if (active) setPreviewError(String(error));
        })
        .finally(() => {
          if (active) setPreviewBusy(false);
        });
    }, 200);
    return () => {
      active = false;
      clearTimeout(timeout);
    };
  }, [
    ready,
    valid,
    signature,
    scenario,
    successAt,
    requestModel,
    streaming,
    retryAfter,
  ]);
  const save = async () => {
    try {
      parseRecoveryPath(draft);
      setSaving(true);
      setError("");
      const { id: _, ...input } = draft;
      const saved = record
        ? await updateRecoveryPath(record.path.id, record.etag, input)
        : await createRecoveryPath(input);
      setDraft(saved.path);
      setBaseline(JSON.stringify(saved.path));
      onSaved(saved);
    } catch (error) {
      setError(String(error));
    } finally {
      setSaving(false);
    }
  };
  const add = () => {
    const service = services.find((service) =>
      service.capabilities.some(
        (capability) => capability.protocol === draft.protocol,
      ),
    );
    if (!service) return;
    const capability = service.capabilities.find(
      (item) => item.protocol === draft.protocol,
    )!;
    updateNodes([
      ...nodes,
      {
        id: newPathNodeID(),
        service_id: service.id,
        upstream_protocol: draft.protocol,
        plan_type: capability.mode,
      },
    ]);
  };
  const convert = () => {
    if (draft.mode === "automatic") {
      if (!preview || previewBusy) return;
      const steps = preview.steps
        .filter((step) => step.status !== "skipped")
        .map((step) => {
          const source = nodes.find((node) => node.id === step.step_id)!;
          const { max_retries: _, ...node } = source;
          return { ...node, id: newPathNodeID() };
        });
      if (!steps.length) {
        setError(t("paths.noConversion"));
        return;
      }
      const { targets: _, strategy: __, ...rest } = draft;
      setConversion({ ...rest, mode: "steps", steps });
    } else {
      const counts = new Map<string, number>();
      for (const node of nodes)
        counts.set(pathNodeKey(node), (counts.get(pathNodeKey(node)) ?? 0) + 1);
      const seen = new Set<string>(),
        targets: RecoveryPathNode[] = [];
      for (const node of nodes) {
        const key = pathNodeKey(node);
        if (seen.has(key)) continue;
        seen.add(key);
        targets.push({
          ...node,
          id: newPathNodeID(),
          max_retries: counts.get(key)! - 1,
        });
      }
      const { steps: _, ...rest } = draft;
      setConversion({
        ...rest,
        mode: "automatic",
        strategy: "retry_first",
        targets,
      });
    }
  };
  const conversionOrder = conversion
    ? (conversion.mode === "steps"
        ? pathNodes(conversion)
        : pathNodes(conversion).flatMap((node) =>
            Array.from({ length: (node.max_retries ?? 1) + 1 }, () => node),
          )
      )
        .map(
          (node) =>
            `${services.find((service) => service.id === node.service_id)?.name ?? node.service_id}${node.upstream_model ? ` / ${node.upstream_model}` : ""}`,
        )
        .join(" → ")
    : "";
  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
      data-testid="recovery-path-editor"
    >
      <div className="flex shrink-0 items-center justify-between gap-3 border-b p-3">
        <strong className="text-sm">
          {record ? t("paths.edit") : t("paths.new")}
        </strong>
        <Button
          type="button"
          disabled={!dirty || !valid || !ready || !defaults.loaded || saving}
          onClick={() => void save()}
        >
          {saving ? t("common.saving") : t("paths.save")}
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        <fieldset disabled={!ready || saving} className="grid min-w-0 gap-4">
          {error ? <FormMessage tone="error">{error}</FormMessage> : null}
          {!defaults.loaded ? (
            <FormMessage>
              {t("failure.defaultsUnavailable")}
              <Button
                type="button"
                variant="ghost"
                disabled={!ready}
                onClick={defaults.reload}
              >
                {t("common.retry")}
              </Button>
            </FormMessage>
          ) : null}
          <div className="grid grid-cols-2 gap-3 max-[640px]:grid-cols-1">
            <Field label={t("paths.name")}>
              <Input
                value={draft.name}
                maxLength={128}
                placeholder={t("paths.namePlaceholder")}
                onChange={(event) =>
                  setDraft({ ...draft, name: event.target.value })
                }
              />
            </Field>
            <Field label={t("paths.protocol")}>
              <FilterSelect
                label=""
                ariaLabel={t("paths.protocol")}
                value={draft.protocol}
                onChange={(protocol) => setDraft({ ...draft, protocol })}
                options={protocols.map((protocol) => ({
                  value: protocol,
                  label: protocolLabel(protocol),
                }))}
              />
            </Field>
          </div>
          <Panel>
            <PanelHeader
              actions={
                <Button
                  type="button"
                  variant="outline"
                  disabled={
                    !valid ||
                    (draft.mode === "automatic" && (!preview || previewBusy))
                  }
                  onClick={convert}
                >
                  {draft.mode === "automatic"
                    ? t("paths.toSteps")
                    : t("paths.toAutomatic")}
                </Button>
              }
            >
              <h3 className="text-sm font-medium">
                {t(`paths.${draft.mode}`)}
              </h3>
            </PanelHeader>
            <div className="grid gap-3 p-3">
              {draft.mode === "automatic" ? (
                <FilterSelect
                  label={t("failure.order")}
                  ariaLabel={t("failure.order")}
                  value={draft.strategy ?? ""}
                  onChange={(strategy) => {
                    const { strategy: _, ...rest } = draft;
                    setDraft(
                      strategy
                        ? {
                            ...rest,
                            strategy: strategy as RecoveryPath["strategy"],
                          }
                        : rest,
                    );
                  }}
                  options={[
                    { value: "", label: t("paths.inherit") },
                    { value: "retry_first", label: t("failure.retryFirst") },
                    {
                      value: "failover_first",
                      label: t("failure.failoverFirst"),
                    },
                  ]}
                />
              ) : (
                <p className="text-xs text-muted-foreground">
                  {t("paths.stepsHint")}
                </p>
              )}
              <OrderedList
                items={nodes}
                onChange={updateNodes}
                label={t("paths.order")}
              >
                {(node, index) => {
                  const service = services.find(
                      (service) => service.id === node.service_id,
                    ),
                    capabilities = service?.capabilities ?? [];
                  const modes = [
                    ...new Set([
                      ...capabilities
                        .filter((cap) => cap.protocol === draft.protocol)
                        .map((cap) => cap.mode),
                      ...(capabilities.some((cap) => cap.mode === "native")
                        ? ["relaykit"]
                        : []),
                    ]),
                  ];
                  if (!modes.includes(node.plan_type))
                    modes.push(node.plan_type);
                  return (
                    <>
                      <div className="grid grid-cols-2 gap-3 max-[640px]:grid-cols-1">
                        <Field label={t("paths.provider")}>
                          <FilterSelect
                            label=""
                            ariaLabel={`${t("paths.provider")} ${index + 1}`}
                            value={node.service_id}
                            onChange={(service_id) => {
                              const next = services.find(
                                  (service) => service.id === service_id,
                                )!,
                                cap = next.capabilities.find(
                                  (cap) => cap.protocol === draft.protocol,
                                );
                              updateNode(node.id, {
                                service_id,
                                plan_type: cap?.mode ?? "relaykit",
                                upstream_protocol: cap
                                  ? draft.protocol
                                  : (next.capabilities[0]?.protocol ??
                                    draft.protocol),
                                upstream_model: undefined,
                              });
                            }}
                            options={services.map((service) => ({
                              value: service.id,
                              label:
                                service.name +
                                (service.enabled
                                  ? ""
                                  : ` · ${t("common.disabled")}`),
                            }))}
                          />
                        </Field>
                        <Field label={t("paths.model")}>
                          <ModelSelect
                            aria-label={`${t("paths.model")} ${index + 1}`}
                            options={service?.models ?? []}
                            value={node.upstream_model ?? ""}
                            maxLength={256}
                            placeholder={t("paths.sameModel")}
                            onValueChange={(model) =>
                              updateNode(node.id, {
                                upstream_model: model || undefined,
                              })
                            }
                          />
                        </Field>
                      </div>
                      <div className="flex flex-wrap gap-2">
                        {draft.mode === "automatic" ? (
                          <FilterSelect
                            className="w-auto"
                            label={t("paths.retryCount")}
                            ariaLabel={`${t("paths.retryCount")} ${index + 1}`}
                            value={
                              node.max_retries === undefined
                                ? ""
                                : String(node.max_retries)
                            }
                            onChange={(value) =>
                              updateNode(node.id, {
                                max_retries:
                                  value === "" ? undefined : Number(value),
                              })
                            }
                            options={[
                              {
                                value: "",
                                label: t("paths.inheritCount", {
                                  count: (
                                    draft.failure_policy ??
                                    service?.failure_policy ??
                                    defaults.default_failure_policy
                                  ).max_retries,
                                }),
                              },
                              ...[0, 1, 2, 3, 4, 5].map((count) => ({
                                value: String(count),
                                label: t("paths.times", { count }),
                              })),
                            ]}
                          />
                        ) : (
                          <Button
                            type="button"
                            variant="ghost"
                            disabled={nodes.length >= 20}
                            onClick={() =>
                              updateNodes([
                                ...nodes.slice(0, index + 1),
                                { ...node, id: newPathNodeID() },
                                ...nodes.slice(index + 1),
                              ])
                            }
                          >
                            {t("paths.duplicate")}
                          </Button>
                        )}
                        <Button
                          type="button"
                          variant="ghost"
                          onClick={() =>
                            updateNodes(
                              nodes.filter((item) => item.id !== node.id),
                            )
                          }
                        >
                          {t("routes.remove")}
                        </Button>
                      </div>
                      <details>
                        <summary className="cursor-pointer text-xs text-muted-foreground">
                          {t("paths.execution")}
                        </summary>
                        <div className="mt-2 grid grid-cols-2 gap-2">
                          <FilterSelect
                            label=""
                            ariaLabel={t("paths.execution")}
                            value={node.plan_type}
                            onChange={(value) =>
                              updateNode(node.id, {
                                plan_type:
                                  value as RecoveryPathNode["plan_type"],
                                upstream_protocol:
                                  value === "relaykit"
                                    ? node.upstream_protocol
                                    : draft.protocol,
                              })
                            }
                            options={modes.map((value) => ({
                              value,
                              label:
                                value === "relaykit"
                                  ? t("failure.convertedTarget")
                                  : value === "native"
                                    ? t("failure.nativeTarget")
                                    : t("failure.delegatedTarget"),
                            }))}
                          />
                          {node.plan_type === "relaykit" ? (
                            <FilterSelect
                              label=""
                              ariaLabel={t("paths.upstreamProtocol")}
                              value={node.upstream_protocol}
                              onChange={(upstream_protocol) =>
                                updateNode(node.id, { upstream_protocol })
                              }
                              options={[
                                ...new Set([
                                  node.upstream_protocol,
                                  ...capabilities
                                    .filter((cap) => cap.mode === "native")
                                    .map((cap) => cap.protocol),
                                ]),
                              ].map((value) => ({
                                value,
                                label: protocolLabel(value),
                              }))}
                            />
                          ) : null}
                        </div>
                      </details>
                    </>
                  );
                }}
              </OrderedList>
              <Button
                type="button"
                variant="outline"
                disabled={
                  !services.length ||
                  nodes.length >= (draft.mode === "steps" ? 20 : 200)
                }
                onClick={add}
              >
                {draft.mode === "steps"
                  ? t("paths.addStep")
                  : t("paths.addProvider")}
              </Button>
            </div>
          </Panel>
          <Field label={t("failure.maxAttempts")} hint={t("paths.limitHint")}>
            <FilterSelect
              label=""
              ariaLabel={t("failure.maxAttempts")}
              value={
                draft.max_attempts === undefined
                  ? ""
                  : String(draft.max_attempts)
              }
              onChange={(value) => {
                const { max_attempts: _, ...rest } = draft;
                setDraft(
                  value ? { ...rest, max_attempts: Number(value) } : rest,
                );
              }}
              options={[
                {
                  value: "",
                  label: t("paths.inheritCount", {
                    count: defaults.max_attempts,
                  }),
                },
                ...Array.from({ length: 20 }, (_, index) => ({
                  value: String(index + 1),
                  label: t("paths.times", { count: index + 1 }),
                })),
              ]}
            />
          </Field>
          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-medium">{t("paths.preview")}</h3>
              <p className="mt-1 text-xs text-muted-foreground">
                {t("paths.previewHint")}
              </p>
            </PanelHeader>
            <div className="grid gap-3 p-3">
              <div className="grid grid-cols-2 gap-3 max-[640px]:grid-cols-1">
                <FilterSelect
                  label={t("paths.simulate")}
                  ariaLabel={t("paths.simulate")}
                  value={scenario}
                  onChange={setScenario}
                  options={[
                    {
                      value: "network_error",
                      label: t("failure.networkError"),
                    },
                    {
                      value: "response_timeout",
                      label: t("failure.responseTimeout"),
                    },
                    ...Object.keys(
                      defaults.default_failure_policy.http_status,
                    ).map((code) => ({ value: code, label: `HTTP ${code}` })),
                    { value: "418", label: "HTTP 418" },
                  ]}
                />
                <Field label={t("paths.successAt")}>
                  <Input
                    type="number"
                    min={0}
                    max={20}
                    value={successAt}
                    onChange={(event) =>
                      setSuccessAt(Number(event.target.value))
                    }
                  />
                </Field>
                <Field label={t("paths.requestModel")}>
                  <ModelSelect
                    aria-label={t("paths.requestModel")}
                    options={services.flatMap((service) => service.models)}
                    value={requestModel}
                    onValueChange={setRequestModel}
                  />
                </Field>
                <Field label="Retry-After">
                  <Input
                    value={retryAfter}
                    placeholder={t("paths.retryAfter")}
                    onChange={(event) => setRetryAfter(event.target.value)}
                  />
                </Field>
              </div>
              <Label className="flex items-center gap-2">
                <Switch checked={streaming} onCheckedChange={setStreaming} />
                {t("paths.streaming")}
              </Label>
              {previewBusy ? (
                <p className="text-xs">{t("common.loading")}</p>
              ) : preview ? (
                <RecoveryPathPreview preview={preview} services={services} />
              ) : (
                <p className="text-xs text-muted-foreground">
                  {valid ? previewError : t("paths.completeFirst")}
                </p>
              )}
            </div>
          </Panel>
          <details>
            <summary className="cursor-pointer text-sm">
              {t("paths.advanced")}
            </summary>
            <div className="mt-3 grid gap-3">
              <Label className="flex items-center gap-2">
                <Switch
                  checked={!!draft.failure_policy}
                  onCheckedChange={(checked) => {
                    const { failure_policy: _, ...rest } = draft;
                    setDraft(
                      checked
                        ? {
                            ...rest,
                            failure_policy: structuredClone(
                              defaults.default_failure_policy,
                            ),
                          }
                        : rest,
                    );
                  }}
                />
                {t("paths.override")}
              </Label>
              {draft.failure_policy ? (
                <FailurePolicyEditor
                  value={draft.failure_policy}
                  onChange={(failure_policy) =>
                    setDraft({ ...draft, failure_policy })
                  }
                />
              ) : (
                <p className="text-xs text-muted-foreground">
                  {t("failure.inheritHint")}
                </p>
              )}
            </div>
          </details>
          <Panel>
            <PanelHeader>
              <h3 className="text-sm font-medium">{t("paths.references")}</h3>
            </PanelHeader>
            <div className="grid gap-2 p-3 text-xs">
              {record?.references.length ? (
                record.references.map((ref, index) => (
                  <p key={index}>
                    {ref.name}
                    {ref.category_id ? ` / ${ref.category_id}` : ""} ·{" "}
                    {protocolLabel(ref.protocol)}
                    {ref.override ? ` · ${t("paths.hasOverride")}` : ""}
                  </p>
                ))
              ) : (
                <p>{t("paths.noReferences")}</p>
              )}
              <p className="text-muted-foreground">{t("paths.sharedHint")}</p>
            </div>
          </Panel>
        </fieldset>
      </div>
      <ConfirmDialog
        open={!!conversion}
        title={t("paths.convertTitle")}
        description={`${t(draft.mode === "automatic" ? "paths.convertStepsHint" : "paths.convertAutomaticHint")}\n${conversionOrder}`}
        confirmLabel={t("paths.applyOrder")}
        onCancel={() => setConversion(null)}
        onConfirm={() => {
          if (conversion) setDraft(conversion);
          setConversion(null);
        }}
      />
    </div>
  );
}
