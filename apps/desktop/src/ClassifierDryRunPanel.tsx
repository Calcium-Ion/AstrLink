import { useEffect, useRef, useState, type ReactNode } from "react";

import { EmptyState } from "./components/EmptyState";
import { Field } from "./components/Field";
import { FormMessage } from "./components/FormMessage";
import { HelpDisclosure } from "./components/HelpDisclosure";
import { HelpPopover } from "./components/HelpPopover";
import { Panel, PanelFooter, PanelHeader } from "./components/Panel";
import { SegmentedControl } from "./components/SegmentedControl";
import {
  RoutingAnswerList,
  RoutingRuleList,
} from "./components/RoutingProcess";
import { SplitWorkspace } from "./components/SplitWorkspace";
import {
  ArrowUpRight,
  CircleCheck,
  Flask,
  SlidersHorizontal,
} from "./components/icons";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "./components/ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./components/ui/select";
import { Textarea } from "./components/ui/textarea";
import { classifierDryRun, getRoutingSettings } from "./bridge";
import type {
  ClassifierDryRunField,
  ClassifierDryRunResult,
} from "./classifier-settings-model";
import { i18n, useT } from "./i18n";
import { intelligentRoutingErrorText } from "./intelligent-routing-errors";
import { isClassifierField } from "./model-routing-editor-model";
import {
  modelRoutingMediaKinds,
  modelRoutingPublicModelID,
  modelRoutingTimeStepSeconds,
  type ModelRoutingGroup,
} from "./model-routing-group-model";
import {
  modelRoutingCodeLabel,
  modelRoutingFieldValueLabel,
} from "./request-model-routing-model";
import type { RoutingSettings } from "./failure-policy-model";
import type { RoutableService } from "./service-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { classifierValueTexts, memberModel } from "./routing-process-model";

/** What the run was asked to judge, kept with its result: the form may change after. */
interface CompletedRun {
  result: ClassifierDryRunResult;
  /** The group the server evaluated, as fetched right before the run. */
  group: ModelRoutingGroup | null;
  groupId: string;
}

type RunState =
  | { status: "idle" }
  | { status: "running" }
  | { status: "done"; run: CompletedRun }
  | { status: "failed"; message: string };

/** Radix Select cannot hold an empty value, so "nothing chosen" needs a token. */
const noGroup = "__none__";
const unknownFact = "__unknown__";
const sampleIDs = ["coding", "billing", "research", "chat"] as const;
type SampleID = (typeof sampleIDs)[number];

/**
 * The facts a dry run can carry. Fields read from real request bodies (images,
 * tools, token counts…) are not among them; that extension is deferred.
 */
const dryRunFactFields = new Set([
  "protocol",
  "media_kind",
  "streaming",
  "continuation",
  "system_time",
]);

/**
 * The classifier's full ranked output for one field. `rank` is 0-based on the
 * wire and is shown 1-based. `chosen` marks the value that was actually
 * substituted; at most one per field.
 */
function CandidateTable({ field }: { field: ClassifierDryRunField }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-1 text-xs font-medium text-text-secondary">
        <span className="font-mono">{field.field}</span>
        <HelpPopover label={t("classifier.dryRun.candidatesHintLabel")}>
          {t("classifier.dryRun.candidatesHint")}
        </HelpPopover>
      </div>
      <ol
        className="m-0 grid list-none gap-1 p-0"
        data-classifier-dry-run-candidates={field.field}
      >
        {field.candidates.map((candidate) => (
          <li
            key={`${candidate.rank}-${candidate.value}`}
            className="flex items-center gap-2 text-xs"
            data-classifier-dry-run-candidate={candidate.value}
            data-candidate-rank={candidate.rank}
            data-candidate-chosen={candidate.chosen ? "true" : "false"}
          >
            <span className="w-6 shrink-0 text-muted-foreground tabular-nums">
              {candidate.rank + 1}
            </span>
            <span className="font-mono">{candidate.value}</span>
            <span className="text-muted-foreground tabular-nums">
              {candidate.score.toFixed(2)}
            </span>
            <Badge variant={candidate.chosen ? "accent" : "outline"}>
              {candidate.chosen
                ? t("classifier.dryRun.candidateChosen")
                : t("classifier.dryRun.candidateEliminated")}
            </Badge>
          </li>
        ))}
      </ol>
    </div>
  );
}

function ClassifierAnswers({ result }: { result: ClassifierDryRunResult }) {
  const t = useT();
  const lowConfidence = result.fallback_reason === "classifier_low_confidence";
  const withCandidates = result.fields.filter(
    (field) => field.candidates.length > 0,
  );
  return (
    <section className="grid gap-2.5" data-classifier-dry-run-answers>
      <h3 className="text-xs font-medium text-text-secondary">
        {t("dryRunTab.classifierTitle")}
      </h3>
      {!result.configured ? (
        // Without a classifier set up nothing was asked; one line says so
        // instead of a list of answers that are all unknown.
        <FormMessage tone="notice" data-classifier-dry-run-not-called>
          {t("dryRunTab.notConfigured")}
        </FormMessage>
      ) : (
        <>
          <RoutingAnswerList
            taskType={result.category}
            confidence={result.confidence}
            lowConfidence={lowConfidence}
            dimensions={result.dimensions}
          />
          {result.fallback_reason === "classifier_timeout" ? (
            // A timeout names the limit the classifier settings set, so the
            // preview reads as the error it is rather than a generic fallback.
            <FormMessage tone="error" data-classifier-dry-run-timeout>
              {t("dryRunTab.classifierTimeout", {
                ms: result.timeout_ms ?? result.duration_ms,
              })}
            </FormMessage>
          ) : result.fallback_reason === null ? null : (
            <p
              className="text-xs text-warning-foreground"
              data-classifier-dry-run-fallback
            >
              {t("dryRunTab.fallbackLine", {
                reason: modelRoutingCodeLabel(result.fallback_reason),
              })}
              <span className="ml-1.5 font-mono text-micro text-muted-foreground">
                {result.fallback_reason}
              </span>
            </p>
          )}
        </>
      )}
      {withCandidates.length === 0 ? null : (
        <HelpDisclosure title={t("dryRunTab.candidates")}>
          {withCandidates.map((field) => (
            <CandidateTable key={field.field} field={field} />
          ))}
        </HelpDisclosure>
      )}
      {result.instruction === null ? null : (
        // What the classifier was told, placeholders rendered, so what the
        // prompt says can be checked against what was sent.
        <HelpDisclosure title={t("dryRunTab.instruction")}>
          <pre
            className="m-0 max-h-60 overflow-auto rounded-sm border border-border bg-muted p-2 text-xs break-words whitespace-pre-wrap text-foreground"
            data-classifier-dry-run-instruction
          >
            {result.instruction}
          </pre>
        </HelpDisclosure>
      )}
      {result.sanitized_preview === "" ? null : (
        <HelpDisclosure title={t("dryRunTab.sanitized")}>
          <pre className="m-0 max-h-40 overflow-auto rounded-sm border border-border bg-muted p-2 text-xs whitespace-pre-wrap text-foreground">
            {result.sanitized_preview}
          </pre>
        </HelpDisclosure>
      )}
    </section>
  );
}

/** Why a rule's field had no fact in this run, in the reader's terms. */
function unknownReason(field: ClassifierDryRunField): string {
  if (isClassifierField(field.field))
    return i18n.t(
      field.skip_reason === "dimension_not_produced"
        ? "dryRunTab.reasons.notProduced"
        : "dryRunTab.reasons.classifier",
    );
  return i18n.t(
    dryRunFactFields.has(field.field)
      ? "dryRunTab.reasons.factUnset"
      : "dryRunTab.reasons.notAvailable",
  );
}

/**
 * Every rule of the group in evaluation order, each with its own verdict, then
 * the Otherwise row. A rule that never ran and one that ran and failed look
 * identical from the outcome alone; this list tells them apart.
 */
function RuleVerdicts({
  run,
  onEditGroup,
}: {
  run: CompletedRun;
  onEditGroup?: (group: string) => void;
}) {
  const t = useT();
  const { result, group, groupId } = run;
  const decision = result.decision;
  if (decision === null) return null;
  const classifierValues = classifierValueTexts(
    result.category,
    result.confidence,
    result.dimensions,
  );
  const fallbackModel = group
    ? memberModel(group, group.default_member_id)
    : decision.used_default
      ? decision.model
      : "";
  return (
    <section className="grid gap-2.5" data-classifier-dry-run-rules="true">
      <div className="flex min-w-0 items-center justify-between gap-2">
        <h3 className="min-w-0 truncate text-xs font-medium text-text-secondary">
          {t("dryRunTab.rulesTitle")} ·{" "}
          <span className="font-mono">
            {modelRoutingPublicModelID(groupId)}
          </span>
        </h3>
        {onEditGroup ? (
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto shrink-0 gap-1 px-0 py-0 text-xs text-primary"
            onClick={() => onEditGroup(groupId)}
          >
            {t("dryRunTab.editRules")}
            <ArrowUpRight aria-hidden="true" className="size-3" />
          </Button>
        ) : null}
      </div>
      {result.rules.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {t("dryRunTab.noRules")}
        </p>
      ) : null}
      <RoutingRuleList
        group={group}
        rules={result.rules}
        usedDefault={decision.used_default}
        fallbackModel={fallbackModel}
        classifierValues={classifierValues}
        unknownReason={unknownReason}
      />
    </section>
  );
}

/**
 * The conclusion of a run that named a group: which model the request would
 * use and why, or why no rule was judged at all.
 */
function outcomeText(
  run: CompletedRun,
  services: readonly RoutableService[],
): { judged: boolean; text: string } {
  const { result, group, groupId } = run;
  const decision = result.decision;
  if (decision === null) {
    // The handler returns before judging any rule when the privacy check stops
    // the text, which is not "group missing". A missing classifier does not:
    // the rules are judged without it.
    return {
      judged: false,
      text: result.fallback_reason?.startsWith("privacy_")
        ? i18n.t("dryRunTab.privacyStopped", {
            reason: modelRoutingCodeLabel(result.fallback_reason),
          })
        : i18n.t("dryRunTab.groupMissing", {
            group: modelRoutingPublicModelID(groupId),
          }),
    };
  }
  const provider =
    services.find((service) => service.id === decision.service_id)?.name ??
    i18n.t("binding.removedService");
  const ruleIndex =
    decision.rule_id === null
      ? -1
      : (group?.rules ?? []).findIndex((rule) => rule.id === decision.rule_id);
  return {
    judged: true,
    text: decision.used_default
      ? i18n.t("dryRunTab.outcomeDefault", { model: decision.model, provider })
      : ruleIndex >= 0
        ? i18n.t("dryRunTab.outcomeRule", {
            model: decision.model,
            provider,
            index: ruleIndex + 1,
          })
        : i18n.t("dryRunTab.outcomeUnlistedRule", {
            model: decision.model,
            provider,
          }),
  };
}

/** The conclusion first: which model this request would use, and why. */
function Outcome({
  run,
  services,
}: {
  run: CompletedRun;
  services: readonly RoutableService[];
}) {
  const t = useT();
  const { result, group } = run;
  const outcome = outcomeText(run, services);
  if (!outcome.judged)
    return (
      <FormMessage tone="warning" data-classifier-dry-run-outcome="none">
        {outcome.text}
      </FormMessage>
    );
  const missing = result.rules.find(
    (rule) => rule.state === "true" && rule.target_missing,
  );
  const missingIndex = missing
    ? (group?.rules ?? []).findIndex((rule) => rule.id === missing.rule_id)
    : -1;
  return (
    <div className="grid gap-2">
      <FormMessage
        tone="success"
        className="flex items-start gap-2"
        data-classifier-dry-run-outcome={
          result.decision?.used_default ? "default" : "rule"
        }
      >
        <CircleCheck aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
        <span>{outcome.text}</span>
      </FormMessage>
      {missing ? (
        <FormMessage tone="warning" data-classifier-dry-run-target-missing>
          {t("dryRunTab.outcomeTargetMissing", {
            index: missingIndex >= 0 ? missingIndex + 1 : missing.rule_id,
          })}
        </FormMessage>
      ) : null}
    </div>
  );
}

/** A request fact that may be left unknown: unknown is not the same as "no". */
function TriState({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (next: string) => void;
}) {
  const t = useT();
  return (
    <Field group label={label}>
      <SegmentedControl
        label={label}
        value={value === "" ? "unknown" : value}
        options={[
          { value: "unknown", label: t("dryRunTab.unknown") },
          { value: "true", label: t("classifier.dryRun.booleanTrue") },
          { value: "false", label: t("classifier.dryRun.booleanFalse") },
        ]}
        onValueChange={(next) => onChange(next === "unknown" ? "" : next)}
      />
    </Field>
  );
}

/**
 * Runs one classification through the local Core and, when a routing group is
 * chosen, judges that group's whole rule table with the production evaluator.
 * Classifier testing and routing testing live here together: the classifier's
 * answers are what the rules read, so one run shows both.
 *
 * The text never goes on to a routed model. It does call the classifier for
 * real, once per run — the cloud service, or the existing model the classifier
 * settings name; without one the rules are judged with every classifier field
 * unknown.
 */
export function ClassifierDryRunPanel({
  ready,
  protocols = [],
  services = [],
  group = "",
  onGroupChange,
  onEditGroup,
  unsavedGroups = [],
}: {
  ready: boolean;
  /** Protocol IDs from core capabilities; the fact domain, same as the rule editor's. */
  protocols?: readonly string[];
  /** Names the provider of the model that would be used. */
  services?: readonly RoutableService[];
  /** The routing group to judge; "" judges none. The editor's 试运行 button sets it. */
  group?: string;
  onGroupChange?: (group: string) => void;
  /** Opens that group's rule editor. */
  onEditGroup?: (group: string) => void;
  /** Groups with unsaved edits in the editor; the run uses their saved version. */
  unsavedGroups?: readonly string[];
}) {
  const t = useT();
  const sampleText = (id: SampleID) => t(`dryRunTab.samples.${id}.text`);
  const [text, setText] = useState(() => sampleText("coding"));
  const [selected, setSelected] = useState(group);
  const [groups, setGroups] = useState<readonly ModelRoutingGroup[]>([]);
  // The routing groups tab publishes every write here, so a group created or
  // renamed there is offered at once, without waiting for the next run.
  const [routingSnapshot] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  const [state, setState] = useState<RunState>({ status: "idle" });
  // Request-side facts. Empty string is "unspecified", which the Go side turns
  // into an unknown fact rather than a default.
  const [protocol, setProtocol] = useState("");
  const [mediaKind, setMediaKind] = useState("");
  const [streaming, setStreaming] = useState("");
  const [continuation, setContinuation] = useState("");
  const [systemTime, setSystemTime] = useState("");
  // The first load picks a group only if nobody chose one yet.
  const touched = useRef(group !== "");
  const runID = useRef(0);

  useEffect(() => {
    setSelected(group);
    if (group !== "") touched.current = true;
  }, [group]);

  useEffect(() => {
    if (!ready) return;
    let active = true;
    void getRoutingSettings()
      .then((settings) => {
        if (!active) return;
        const loaded = settings.model_routing_groups ?? [];
        setGroups(loaded);
        // Most runs are about a routing group, so open on the first enabled
        // one. The classifier answers show either way.
        if (!touched.current) {
          touched.current = true;
          const first = loaded.find((item) => item.enabled);
          if (first) {
            setSelected(first.id);
            onGroupChange?.(first.id);
          }
        }
      })
      // The group list only tailors the run; a failure here must not stop the
      // user from classifying text.
      .catch(() => {
        if (active) setGroups([]);
      });
    return () => {
      active = false;
    };
    // `onGroupChange` is a callback prop; reloading on its identity would
    // refetch on every parent render.
  }, [ready]);

  useEffect(() => {
    if (routingSnapshot) setGroups(routingSnapshot.model_routing_groups ?? []);
  }, [routingSnapshot]);

  const choose = (next: string) => {
    touched.current = true;
    setSelected(next);
    onGroupChange?.(next);
  };

  const factCount = [
    protocol,
    mediaKind,
    streaming,
    continuation,
    systemTime,
  ].filter((value) => value !== "").length;
  const preset = sampleIDs.find((id) => sampleText(id) === text);
  const canRun = ready && state.status !== "running" && text.trim() !== "";

  const run = (): void => {
    if (!canRun) return;
    const chosen = selected;
    const id = ++runID.current;
    setState({ status: "running" });
    void (async () => {
      // The sentences in the verdict list must describe the rules the server
      // evaluated, so the group is read fresh for every run.
      const settings =
        chosen === "" ? null : await getRoutingSettings().catch(() => null);
      const latest = settings?.model_routing_groups ?? groups;
      if (settings) setGroups(latest);
      const result = await classifierDryRun({
        text,
        ...(chosen === "" ? {} : { group_id: chosen }),
        // Only what the user filled in travels; an omitted key stays unknown.
        ...(protocol === "" ? {} : { protocol }),
        ...(mediaKind === "" ? {} : { media_kind: mediaKind }),
        ...(streaming === "" ? {} : { streaming: streaming === "true" }),
        ...(continuation === ""
          ? {}
          : { continuation: continuation === "true" }),
        ...(systemTime === "" ? {} : { system_time: systemTime }),
      });
      if (id !== runID.current) return;
      setState({
        status: "done",
        run: {
          result,
          group: latest.find((item) => item.id === chosen) ?? null,
          groupId: chosen,
        },
      });
    })().catch((caught: unknown) => {
      if (id !== runID.current) return;
      setState({
        status: "failed",
        message: intelligentRoutingErrorText(
          caught,
          t("classifier.dryRun.failed"),
        ),
      });
    });
  };

  const listedGroups =
    selected === "" || groups.some((item) => item.id === selected)
      ? groups
      : [...groups, { id: selected, enabled: true } as ModelRoutingGroup];

  const liveSummary =
    state.status === "running"
      ? t("classifier.dryRun.running")
      : state.status === "failed"
        ? state.message
        : state.status === "done"
          ? state.run.groupId === ""
            ? `${t("dryRunTab.taskType")} ${
                state.run.result.category === ""
                  ? t("classifier.dryRun.unknown")
                  : modelRoutingFieldValueLabel(
                      "task_type",
                      state.run.result.category,
                    )
              }`
            : outcomeText(state.run, services).text
          : "";

  let body: ReactNode;
  if (state.status === "idle")
    body = (
      <EmptyState
        className="flex-1 border-0"
        title={t("dryRunTab.idleTitle")}
        description={t("dryRunTab.idleHint")}
      />
    );
  else if (state.status === "running")
    body = (
      <EmptyState
        className="flex-1 border-0"
        title={t("classifier.dryRun.running")}
        description={t("dryRunTab.runningHint")}
      />
    );
  else if (state.status === "failed")
    body = <FormMessage tone="error">{state.message}</FormMessage>;
  else {
    const { run: completed } = state;
    body = (
      <>
        {completed.groupId === "" ? null : (
          <Outcome run={completed} services={services} />
        )}
        <ClassifierAnswers result={completed.result} />
        {completed.groupId === "" ? (
          <div
            className="grid justify-items-center gap-1 border-t px-4 pt-6 pb-2 text-center"
            data-classifier-dry-run-no-group
          >
            <p className="text-sm font-medium">{t("dryRunTab.noGroupTitle")}</p>
            <p className="max-w-[40ch] text-xs text-muted-foreground">
              {t("dryRunTab.noGroupHint")}
            </p>
          </div>
        ) : (
          <RuleVerdicts run={completed} onEditGroup={onEditGroup} />
        )}
      </>
    );
  }

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col"
      data-testid="classifier-dry-run-panel"
      onKeyDown={(event) => {
        if (
          (event.metaKey || event.ctrlKey) &&
          event.key === "Enter" &&
          !event.nativeEvent.isComposing
        ) {
          event.preventDefault();
          run();
        }
      }}
    >
      <SplitWorkspace className="gutter-scroller min-h-0 flex-1">
        <Panel className="flex min-h-0 flex-col" data-testid="dry-run-input">
          <PanelHeader
            className="shrink-0 flex-wrap items-center gap-2 px-3 py-2"
            actions={
              <>
                <Popover>
                  <PopoverTrigger asChild>
                    <Button
                      type="button"
                      size="xs"
                      variant="ghost"
                      data-dry-run-facts-trigger
                    >
                      <SlidersHorizontal aria-hidden="true" />
                      {t("dryRunTab.facts")}
                      {factCount > 0 ? (
                        <Badge variant="secondary" className="tabular-nums">
                          {factCount}
                        </Badge>
                      ) : null}
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent className="grid w-80 gap-3 text-xs">
                    <p className="flex items-center gap-1 text-sm font-medium">
                      {t("dryRunTab.factsTitle")}
                      <HelpPopover label={t("dryRunTab.factsTitle")}>
                        <span className="grid gap-1.5">
                          <span>{t("dryRunTab.factsHint")}</span>
                          <span>{t("dryRunTab.factsUnavailable")}</span>
                        </span>
                      </HelpPopover>
                    </p>
                    <Field label={t("dryRunTab.protocol")}>
                      <Select
                        value={protocol === "" ? unknownFact : protocol}
                        onValueChange={(next) =>
                          setProtocol(next === unknownFact ? "" : next)
                        }
                      >
                        <SelectTrigger
                          size="sm"
                          className="w-full text-xs"
                          aria-label={t("dryRunTab.protocol")}
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value={unknownFact}>
                            {t("classifier.dryRun.protocolUnknown")}
                          </SelectItem>
                          {protocols.map((value) => (
                            <SelectItem key={value} value={value}>
                              {modelRoutingFieldValueLabel("protocol", value)}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </Field>
                    <Field label={t("classifier.dryRun.mediaKind")}>
                      <Select
                        value={mediaKind === "" ? unknownFact : mediaKind}
                        onValueChange={(next) =>
                          setMediaKind(next === unknownFact ? "" : next)
                        }
                      >
                        <SelectTrigger
                          size="sm"
                          className="w-full text-xs"
                          aria-label={t("classifier.dryRun.mediaKind")}
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value={unknownFact}>
                            {t("classifier.dryRun.mediaKindUnknown")}
                          </SelectItem>
                          {modelRoutingMediaKinds
                            .filter((value) => value !== "unknown")
                            .map((value) => (
                              <SelectItem key={value} value={value}>
                                {modelRoutingFieldValueLabel(
                                  "media_kind",
                                  value,
                                )}
                              </SelectItem>
                            ))}
                        </SelectContent>
                      </Select>
                    </Field>
                    <TriState
                      label={t("classifier.dryRun.streaming")}
                      value={streaming}
                      onChange={setStreaming}
                    />
                    <TriState
                      label={t("classifier.dryRun.continuation")}
                      value={continuation}
                      onChange={setContinuation}
                    />
                    <Field
                      label={t("classifier.dryRun.systemTime")}
                      tooltip={t("classifier.dryRun.systemTimeHint")}
                    >
                      <span className="flex items-center gap-2">
                        <Input
                          type="time"
                          step={modelRoutingTimeStepSeconds}
                          className="h-7 w-28 text-xs tabular-nums"
                          value={systemTime}
                          aria-label={t("classifier.dryRun.systemTime")}
                          onChange={(event) =>
                            setSystemTime(event.target.value)
                          }
                        />
                        {systemTime === "" ? null : (
                          <Button
                            type="button"
                            size="xs"
                            variant="ghost"
                            onClick={() => setSystemTime("")}
                          >
                            {t("dryRunTab.clearTime")}
                          </Button>
                        )}
                      </span>
                    </Field>
                  </PopoverContent>
                </Popover>
                <Select
                  value={preset ?? "custom"}
                  onValueChange={(next) => {
                    if ((sampleIDs as readonly string[]).includes(next))
                      setText(sampleText(next as SampleID));
                  }}
                >
                  <SelectTrigger
                    size="sm"
                    className="w-auto min-w-28 text-xs"
                    aria-label={t("dryRunTab.samples.label")}
                    data-dry-run-samples
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem disabled value="custom">
                      {t("dryRunTab.samples.custom")}
                    </SelectItem>
                    {sampleIDs.map((id) => (
                      <SelectItem key={id} value={id}>
                        {t(`dryRunTab.samples.${id}.label`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </>
            }
          >
            <div className="flex items-center gap-1 whitespace-nowrap">
              <h2 className="text-sm font-semibold">
                {t("dryRunTab.inputTitle")}
              </h2>
              <HelpPopover label={t("classifier.dryRun.runHintLabel")}>
                <div className="grid gap-2">
                  <p>{t("classifier.dryRun.runHint")}</p>
                  <p>{t("classifier.dryRun.textHint")}</p>
                  <p>{t("dryRunTab.shortcut")}</p>
                </div>
              </HelpPopover>
            </div>
          </PanelHeader>
          <Textarea
            aria-label={t("dryRunTab.inputTitle")}
            className="h-0 min-h-0 flex-1 resize-none field-sizing-fixed rounded-none border-0 p-3 text-sm leading-relaxed focus-visible:ring-inset"
            placeholder={t("dryRunTab.inputPlaceholder")}
            value={text}
            onChange={(event) => setText(event.target.value)}
          />
          <PanelFooter
            className="gap-2 px-3 py-2"
            actions={
              <Button
                type="button"
                size="sm"
                aria-keyshortcuts="Meta+Enter Control+Enter"
                title={t("dryRunTab.shortcut")}
                aria-busy={state.status === "running"}
                disabled={!canRun}
                onClick={run}
                data-dry-run-start
              >
                <Flask aria-hidden="true" />
                {state.status === "running"
                  ? t("classifier.dryRun.running")
                  : t("dryRunTab.run")}
              </Button>
            }
          >
            <Select
              value={selected === "" ? noGroup : selected}
              onValueChange={(next) => choose(next === noGroup ? "" : next)}
            >
              <SelectTrigger
                size="sm"
                className="w-auto max-w-60 min-w-0 text-xs"
                aria-label={t("dryRunTab.group")}
                data-dry-run-group
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={noGroup}>
                  {t("dryRunTab.noGroup")}
                </SelectItem>
                {listedGroups.map((item) => (
                  <SelectItem key={item.id} value={item.id}>
                    <span className="font-mono">
                      {modelRoutingPublicModelID(item.id)}
                    </span>
                    {item.enabled ? null : (
                      <span className="text-xs text-muted-foreground">
                        {t("dryRunTab.disabled")}
                      </span>
                    )}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </PanelFooter>
        </Panel>

        <Panel className="flex min-h-0 flex-col" data-testid="dry-run-result">
          <PanelHeader
            className="shrink-0 items-center px-3 py-2"
            actions={
              // A run that called no classifier has no duration to show, and
              // the badge would claim a call that did not happen.
              state.status === "done" &&
              state.run.result.timeout_ms === null ? null : (
                <>
                  {state.status === "done" ? (
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {t("dryRunTab.durationMs", {
                        ms: state.run.result.duration_ms,
                      })}
                    </span>
                  ) : null}
                  <Badge variant="secondary">
                    {t("dryRunTab.callsClassifier")}
                  </Badge>
                </>
              )
            }
          >
            <h2 className="text-sm font-semibold">
              {t("dryRunTab.resultTitle")}
            </h2>
          </PanelHeader>
          <p aria-live="polite" className="sr-only">
            {liveSummary}
          </p>
          <div
            className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto overscroll-contain p-3"
            data-tab-scroller
          >
            {selected !== "" && unsavedGroups.includes(selected) ? (
              <FormMessage tone="notice" data-classifier-dry-run-unsaved>
                {t("dryRunTab.unsaved", {
                  group: modelRoutingPublicModelID(selected),
                })}
              </FormMessage>
            ) : null}
            {body}
          </div>
        </Panel>
      </SplitWorkspace>
    </div>
  );
}
