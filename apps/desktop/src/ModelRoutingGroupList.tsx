import { EmptyState } from "@/components/EmptyState";
import { IconButton } from "@/components/IconButton";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { Brain, ChevronRight, Copy, Plus } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { i18n, useT } from "@/i18n";
import { notify } from "@/notify";
import {
  memberTarget,
  phrasesFromWhen,
  routingGroupTargets,
  routingGroupUsesClassifier,
} from "@/model-routing-editor-model";
import {
  modelRoutingPublicModelID,
  type ModelRoutingGroup,
} from "@/model-routing-group-model";
import { routingPhraseText } from "@/model-routing-phrase-text";
import { modelRoutingConditionSummary } from "@/request-model-routing-model";
import type { RoutableService } from "@/service-model";
import { cn } from "@/lib/utils";

const columns =
  "grid min-w-0 grid-cols-[2.25rem_minmax(0,1.6fr)_minmax(0,1fr)_minmax(0,0.9fr)_1rem] items-center gap-3 px-3";

/** 规则摘要：前三条各取第一个条件，配上它选的模型。 */
function ruleSummary(group: ModelRoutingGroup): string {
  if (group.rules.length === 0) return i18n.t("routingGroups.list.noRules");
  const parts = group.rules.slice(0, 3).map((rule) => {
    const phrases = phrasesFromWhen(rule.when);
    const condition =
      phrases && phrases.length > 0
        ? routingPhraseText(phrases[0])
        : modelRoutingConditionSummary(rule.when);
    const model = memberTarget(group, rule.target_member_id)?.model ?? "";
    return `${condition} → ${model}`;
  });
  if (group.rules.length > 3)
    parts.push(
      i18n.t("routingGroups.list.summaryMore", {
        count: group.rules.length - 3,
      }),
    );
  return parts.join(" · ");
}

export function routingGroupMatches(group: ModelRoutingGroup, query: string) {
  const text = query.trim().toLowerCase();
  if (text === "") return true;
  // The list shows and copies `astrlink/<id>`, so that is what gets pasted.
  return (
    modelRoutingPublicModelID(group.id).includes(text) ||
    group.members.some((member) => member.model.toLowerCase().includes(text))
  );
}

/** Copies a routing group's public model name, with the usual clipboard feedback. */
export async function copyRoutingModelName(value: string) {
  const label = i18n.t("routingGroups.list.copyLabel");
  try {
    await navigator.clipboard.writeText(value);
    notify.success(i18n.t("copy.copiedNamed", { label }));
  } catch {
    notify.error(i18n.t("copy.manualSelect"));
  }
}

/**
 * 路由组列表：每行一个对外模型名，带启用开关、规则摘要、用到的模型和默认
 * 模型。点模型名进入编辑页；开关直接保存。
 */
export function ModelRoutingGroupList({
  groups,
  services,
  query,
  disabled,
  pendingToggle,
  onOpen,
  onToggle,
  onCreate,
}: {
  groups: readonly ModelRoutingGroup[];
  services: readonly RoutableService[];
  query: string;
  disabled: boolean;
  /** The group whose enable switch is being saved. */
  pendingToggle: string | null;
  onOpen: (id: string) => void;
  onToggle: (id: string, enabled: boolean) => void;
  onCreate: () => void;
}) {
  const t = useT();
  if (groups.length === 0)
    return (
      <EmptyState
        className="flex-1"
        title={t("routingGroups.list.emptyTitle")}
        description={t("routingGroups.list.emptyDescription")}
        action={
          <Button type="button" disabled={disabled} onClick={onCreate}>
            <Plus aria-hidden="true" />
            {t("routingGroups.list.add")}
          </Button>
        }
      />
    );

  const shown = groups.filter((group) => routingGroupMatches(group, query));
  const serviceOf = (id: string) =>
    services.find((service) => service.id === id);

  return (
    <div
      className="min-h-0 flex-1 overflow-y-auto rounded-md border bg-card"
      data-tab-scroller
      data-testid="routing-group-list"
    >
      <div
        className={cn(
          columns,
          "sticky top-0 z-10 border-b bg-muted py-2 text-micro font-medium text-muted-foreground",
        )}
        aria-hidden="true"
      >
        <span>{t("routingGroups.list.enabledColumn")}</span>
        <span>{t("routingGroups.list.nameColumn")}</span>
        <span>{t("routingGroups.list.modelsColumn")}</span>
        <span>{t("routingGroups.list.fallbackColumn")}</span>
        <span />
      </div>
      {shown.length === 0 ? (
        <p className="px-3 py-6 text-center text-xs text-muted-foreground">
          {t("routingGroups.list.noMatch")}
        </p>
      ) : null}
      {shown.map((group) => {
        const publicID = modelRoutingPublicModelID(group.id);
        const targets = routingGroupTargets(group);
        const kinds = [
          ...new Set(
            targets
              .map((target) => serviceOf(target.serviceId)?.kind)
              .filter((kind) => kind !== undefined),
          ),
        ];
        const fallback = memberTarget(group, group.default_member_id);
        return (
          <div
            key={group.id}
            className={cn(
              columns,
              "cursor-pointer border-b py-3 hover:bg-muted/30",
            )}
            data-routing-group={group.id}
            onClick={() => onOpen(group.id)}
          >
            <span onClick={(event) => event.stopPropagation()}>
              <Switch
                size="sm"
                checked={group.enabled}
                disabled={disabled || pendingToggle === group.id}
                aria-label={t("routingGroups.list.toggle", { name: publicID })}
                onCheckedChange={(enabled) => onToggle(group.id, enabled)}
              />
            </span>
            <div className="min-w-0">
              <div className="flex min-w-0 items-center gap-1.5">
                <Button
                  type="button"
                  variant="link"
                  className={cn(
                    "h-auto min-w-0 justify-start truncate p-0 font-mono text-sm",
                    !group.enabled && "text-muted-foreground",
                  )}
                  aria-label={t("routingGroups.list.open", { name: publicID })}
                  onClick={(event) => {
                    event.stopPropagation();
                    onOpen(group.id);
                  }}
                >
                  {publicID}
                </Button>
                <span onClick={(event) => event.stopPropagation()}>
                  <IconButton
                    type="button"
                    size="icon-xs"
                    className="text-muted-foreground"
                    label={t("routingGroups.list.copy")}
                    onClick={() => void copyRoutingModelName(publicID)}
                  >
                    <Copy aria-hidden="true" />
                  </IconButton>
                </span>
                {group.enabled ? null : (
                  <Badge variant="secondary">
                    {t("routingGroups.list.disabled")}
                  </Badge>
                )}
                {routingGroupUsesClassifier(group) ? (
                  <Badge
                    variant="outline"
                    className="text-muted-foreground"
                    title={t("routingGroups.list.usesClassifierHint")}
                  >
                    <Brain
                      aria-hidden="true"
                      className="text-violet-foreground"
                    />
                    {t("routingGroups.list.usesClassifier")}
                  </Badge>
                ) : null}
              </div>
              <p className="mt-0.5 truncate text-xs text-muted-foreground">
                {ruleSummary(group)}
              </p>
            </div>
            <div className="flex min-w-0 items-center gap-2">
              <span className="flex shrink-0 items-center gap-0.5">
                {kinds.slice(0, 4).map((kind) => (
                  <ServiceKindIcon key={kind} kind={kind} size={16} />
                ))}
              </span>
              <span className="truncate text-xs text-muted-foreground">
                {t("routingGroups.list.modelCount", { count: targets.length })}
              </span>
            </div>
            <span
              className={cn(
                "min-w-0 truncate text-xs",
                !group.enabled && "text-muted-foreground",
              )}
            >
              {fallback?.model ?? ""}
            </span>
            <ChevronRight
              aria-hidden="true"
              className="size-3.5 text-muted-foreground"
            />
          </div>
        );
      })}
    </div>
  );
}
