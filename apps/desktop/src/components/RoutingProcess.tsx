import type {
  ClassifierDryRunDimension,
  ClassifierDryRunField,
  ClassifierDryRunRule,
} from "../classifier-settings-model";
import { useT } from "../i18n";
import { cn } from "@/lib/utils";
import { ModelRoutingPhraseTags } from "../ModelRoutingRuleRow";
import type { ModelRoutingGroup } from "../model-routing-group-model";
import { routingFieldName } from "../model-routing-phrase-text";
import { modelRoutingFieldValueLabel } from "../request-model-routing-model";
import {
  memberModel,
  percent,
  routingFactLine,
} from "../routing-process-model";
import { Brain } from "./icons";
import { Badge } from "./ui/badge";

/**
 * 路由组怎么选出模型：分类器的回答，每条规则各自的判定和依据，最后是「其他情况」。
 * 试运行和请求记录共用这一套画法和文字，读者在两处看到的是同一件事。
 */

/** 分类器回答的一行：说了什么、多大把握、规则能不能用它。 */
function AnswerRow({
  name,
  mono = false,
  value,
  code,
  score,
  usable,
  caveat,
  ...props
}: {
  name: string;
  mono?: boolean;
  value: string;
  code?: string;
  score: string | null;
  usable: boolean;
  caveat?: string;
} & Record<`data-${string}`, string | undefined>) {
  const t = useT();
  return (
    <div
      className="grid grid-cols-[minmax(0,6rem)_minmax(0,1fr)_auto] items-center gap-x-3 text-sm"
      {...props}
    >
      <span className="flex min-w-0 items-center gap-1 text-muted-foreground">
        <Brain
          aria-hidden="true"
          className="size-3 shrink-0 text-violet-foreground"
        />
        <span className={cn("truncate", mono && "font-mono")}>{name}</span>
      </span>
      <span className="min-w-0 truncate font-medium">
        {value}
        {code && code !== value ? (
          <span className="ml-1.5 font-mono text-micro font-normal text-muted-foreground">
            {code}
          </span>
        ) : null}
      </span>
      <span className="flex shrink-0 items-center gap-2">
        {score === null ? null : (
          <span className="text-xs text-muted-foreground tabular-nums">
            {score}
          </span>
        )}
        {usable ? (
          <Badge variant="accent">{t("dryRunTab.usable")}</Badge>
        ) : (
          <Badge variant="outline" className="text-warning-foreground">
            {caveat}
          </Badge>
        )}
      </span>
    </div>
  );
}

/**
 * 任务类型和每个已配置的自定义维度。`known` 是阈值判断，可用的维度才可能让规则命中；
 * `produced` 区分「没回答」和「回答了但低于阈值」，两者的修法不同。
 */
export function RoutingAnswerList({
  taskType,
  confidence,
  lowConfidence,
  dimensions,
}: {
  /** 空串表示没有可用的任务类型。 */
  taskType: string;
  confidence: number | null;
  lowConfidence: boolean;
  dimensions: readonly ClassifierDryRunDimension[];
}) {
  const t = useT();
  return (
    <div className="grid gap-2">
      <AnswerRow
        name={t("dryRunTab.taskType")}
        value={
          taskType === ""
            ? t("classifier.dryRun.unknown")
            : modelRoutingFieldValueLabel("task_type", taskType)
        }
        code={taskType === "" ? undefined : taskType}
        score={confidence === null ? null : percent(confidence)}
        usable={taskType !== ""}
        caveat={
          lowConfidence
            ? t("classifier.dryRun.dimensionBelowThreshold")
            : t("dryRunTab.noAnswer")
        }
        data-classifier-dry-run-task-type={taskType || "unknown"}
      />
      {dimensions.length === 0 ? null : (
        <div className="grid gap-2" data-classifier-dry-run-dimensions="true">
          {dimensions.map((dimension) => (
            <AnswerRow
              key={dimension.name}
              name={dimension.name}
              mono
              value={dimension.label === "" ? "—" : dimension.label}
              score={dimension.produced ? percent(dimension.score) : null}
              usable={dimension.known}
              caveat={
                dimension.produced
                  ? t("classifier.dryRun.dimensionBelowThreshold")
                  : t("classifier.dryRun.dimensionNotProduced")
              }
              data-classifier-dry-run-dimension={dimension.name}
              data-dimension-known={dimension.known ? "true" : "false"}
              data-dimension-produced={dimension.produced ? "true" : "false"}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function RuleVerdictRow({
  number,
  rule,
  verdict,
  model,
  classifierValues,
  unknownReason,
  factLines,
}: {
  number: number;
  rule: ModelRoutingGroup["rules"][number] | undefined;
  verdict: ClassifierDryRunRule;
  model: string;
  classifierValues: ReadonlyMap<string, string>;
  unknownReason: (field: ClassifierDryRunField) => string;
  factLines: boolean;
}) {
  const t = useT();
  const hit = verdict.state === "true" && verdict.selected;
  const unknownFields = verdict.fields.filter((field) => !field.known);
  const evaluated = verdict.state === "true" || verdict.state === "false";
  const lines: string[] = [];
  if (verdict.state === "unknown" && unknownFields.length > 0) {
    for (const field of unknownFields)
      lines.push(
        t("dryRunTab.reasonLine", {
          field: routingFieldName(field.field),
          reason: unknownReason(field),
        }),
      );
  } else if (evaluated) {
    const seen = new Set<string>();
    for (const field of verdict.fields) {
      if (seen.has(field.field)) continue;
      seen.add(field.field);
      const classifierValue = classifierValues.get(field.field);
      if (classifierValue !== undefined)
        lines.push(
          t("dryRunTab.classifierValue", {
            field: routingFieldName(field.field),
            value: classifierValue,
          }),
        );
      else if (factLines) {
        const line = routingFactLine(field, rule);
        if (line) lines.push(line);
      }
    }
  }
  const reason =
    lines.length > 0 ? lines.join(t("dryRunTab.reasonSeparator")) : null;
  const badge =
    verdict.state === "true"
      ? verdict.target_missing
        ? {
            variant: "outline" as const,
            tone: "text-warning-foreground",
            text: t("dryRunTab.verdict.targetMissing"),
          }
        : verdict.selected
          ? {
              variant: "default" as const,
              tone: "",
              text: t("dryRunTab.verdict.selected"),
            }
          : {
              variant: "outline" as const,
              tone: "",
              text: t("dryRunTab.verdict.true"),
            }
      : verdict.state === "false"
        ? {
            variant: "outline" as const,
            tone: "text-muted-foreground",
            text: t("dryRunTab.verdict.false"),
          }
        : verdict.state === "unknown"
          ? {
              variant: "outline" as const,
              tone: "text-warning-foreground",
              text: t("dryRunTab.verdict.unknown"),
            }
          : {
              variant: "outline" as const,
              tone: "text-muted-foreground",
              text: t("dryRunTab.verdict.notEvaluated"),
            };
  return (
    <li
      className={cn(
        "relative grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-2 border-b px-3 py-2 last:border-b-0",
        hit &&
          "bg-accent/70 before:absolute before:inset-y-1 before:left-0 before:w-0.5 before:rounded-full before:bg-tide",
      )}
      data-classifier-dry-run-rule={verdict.rule_id}
      data-rule-state={verdict.state}
      data-rule-selected={verdict.selected ? "true" : "false"}
    >
      <span className="text-center text-xs text-muted-foreground tabular-nums">
        {number}
      </span>
      <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs">
        {rule ? (
          <ModelRoutingPhraseTags rule={rule} />
        ) : (
          <span className="font-mono text-text-secondary">
            {verdict.rule_id}
          </span>
        )}
        <span className="text-muted-foreground">→</span>
        <span className={hit ? "font-semibold" : undefined}>
          {model || verdict.target_member_id}
        </span>
        {reason ? (
          <span
            className={cn(
              "basis-full text-micro",
              verdict.state === "unknown"
                ? "text-warning-foreground"
                : "text-muted-foreground",
            )}
            data-rule-reason
          >
            {reason}
          </span>
        ) : null}
      </div>
      <Badge
        variant={badge.variant}
        className={badge.tone}
        data-rule-state-badge={verdict.state}
      >
        {badge.text}
      </Badge>
    </li>
  );
}

/**
 * 组里的每条规则按判定顺序各给一个结论，最后是「其他情况」。只看结果分不清
 * 「没轮到」和「轮到了但不满足」，这张表把两者分开。
 */
export function RoutingRuleList({
  group,
  rules,
  usedDefault,
  fallbackModel,
  classifierValues,
  unknownReason,
  factLines = false,
}: {
  /** 判定时用的组配置，用来把规则写成句子；拿不到时只显示规则 ID。 */
  group: ModelRoutingGroup | null;
  rules: readonly ClassifierDryRunRule[];
  usedDefault: boolean;
  fallbackModel: string;
  classifierValues: ReadonlyMap<string, string>;
  unknownReason: (field: ClassifierDryRunField) => string;
  /** 在规则下面写出请求事实的取值；试运行的条件是用户自己填的，不用重复。 */
  factLines?: boolean;
}) {
  const t = useT();
  const configured = group?.rules ?? [];
  return (
    <ol className="m-0 list-none overflow-hidden rounded-md border p-0">
      {rules.map((verdict, index) => {
        const position = configured.findIndex(
          (rule) => rule.id === verdict.rule_id,
        );
        return (
          <RuleVerdictRow
            key={verdict.rule_id}
            number={position < 0 ? index + 1 : position + 1}
            rule={position < 0 ? undefined : configured[position]}
            verdict={verdict}
            model={
              verdict.target_model ||
              memberModel(group, verdict.target_member_id)
            }
            classifierValues={classifierValues}
            unknownReason={unknownReason}
            factLines={factLines}
          />
        );
      })}
      <li
        className={cn(
          "relative grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-2 bg-muted/45 px-3 py-2",
          usedDefault &&
            "bg-accent/70 before:absolute before:inset-y-1 before:left-0 before:w-0.5 before:rounded-full before:bg-tide",
        )}
        data-classifier-dry-run-fallback-row
        data-rule-selected={usedDefault ? "true" : "false"}
      >
        <span />
        <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs">
          <span className="font-medium">{t("dryRunTab.fallbackRow")}</span>
          <span className="text-muted-foreground">→</span>
          <span className={usedDefault ? "font-semibold" : undefined}>
            {fallbackModel}
          </span>
        </div>
        <Badge
          variant={usedDefault ? "default" : "outline"}
          className={usedDefault ? undefined : "text-muted-foreground"}
        >
          {usedDefault
            ? t("dryRunTab.verdict.fallbackUsed")
            : t("dryRunTab.verdict.fallbackUnused")}
        </Badge>
      </li>
    </ol>
  );
}
