import { useState } from "react";

import { useCopyFeedback } from "../copy-feedback";
import { i18n, useT } from "../i18n";
import { ModelRoutingPhraseTags } from "../ModelRoutingRuleRow";
import { modelRoutingPublicModelID } from "../model-routing-group-model";
import type { ModelRoutingGroupVersion } from "../model-routing-version-model";
import { memberModel } from "../routing-process-model";
import type { Service } from "../service-model";
import { useModelRoutingGroupVersion } from "./RequestRoutingProcess";
import { SegmentedControl } from "./SegmentedControl";
import { ServiceKindIcon } from "./ServiceKindIcon";
import { Button } from "./ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./ui/dialog";

type VersionView = "rules" | "json";

/** 一版配置写成和编辑页一样的句子：「当 … → 模型」，最后是「其他情况」。 */
function VersionRules({
  version,
  services,
}: {
  version: ModelRoutingGroupVersion;
  services: readonly Pick<Service, "id" | "name" | "kind">[];
}) {
  const t = useT();
  const { group } = version;
  const target = (memberID: string) => {
    const member = group.members.find((item) => item.id === memberID);
    const service = services.find((item) => item.id === member?.service_id);
    return (
      <span className="inline-flex min-w-0 items-center gap-1.5">
        {service ? <ServiceKindIcon kind={service.kind} size={14} /> : null}
        <span className="truncate">
          {member
            ? member.model
            : memberModel(group, memberID) || t("routingProcess.removedMember")}
        </span>
      </span>
    );
  };
  return (
    <ol
      className="m-0 list-none overflow-hidden rounded-md border p-0 text-xs"
      data-routing-version-rules
    >
      {group.rules.map((rule, index) => (
        <li
          key={rule.id}
          className="grid grid-cols-[1.25rem_minmax(0,1fr)] items-center gap-2 border-b px-3 py-2"
        >
          <span className="text-center text-muted-foreground tabular-nums">
            {index + 1}
          </span>
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <span className="text-muted-foreground">
              {t("routingProcess.when")}
            </span>
            <ModelRoutingPhraseTags rule={rule} />
            <span className="text-muted-foreground">→</span>
            {target(rule.target_member_id)}
          </div>
        </li>
      ))}
      <li className="grid grid-cols-[1.25rem_minmax(0,1fr)] items-center gap-2 bg-muted/45 px-3 py-2">
        <span />
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="font-medium">{t("dryRunTab.fallbackRow")}</span>
          <span className="text-muted-foreground">→</span>
          {target(group.default_member_id)}
        </div>
      </li>
    </ol>
  );
}

/**
 * 请求记录里的配置版本点开后看到的那一版配置。默认用句子显示规则；切到 JSON 看原始
 * 配置，方便复制给别人排查。
 */
export function RoutingVersionDialog({
  groupID,
  version,
  open,
  onOpenChange,
  services,
}: {
  groupID: string;
  version: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  services: readonly Pick<Service, "id" | "name" | "kind">[];
}) {
  const t = useT();
  const loaded = useModelRoutingGroupVersion(
    groupID,
    open ? version : undefined,
  );
  const [view, setView] = useState<VersionView>("rules");
  const copy = useCopyFeedback();
  const json = loaded ? JSON.stringify(loaded.document, null, 2) : "";
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl" data-routing-version-dialog>
        <DialogHeader>
          <DialogTitle>
            <span className="font-mono">
              {modelRoutingPublicModelID(groupID)}
            </span>
            {" · "}
            {t("routingProcess.versionLabel", { version })}
          </DialogTitle>
          <DialogDescription className="text-xs">
            {loaded
              ? [
                  t("routingProcess.versionSaved", {
                    time: new Date(loaded.created_at).toLocaleString(
                      i18n.language,
                      {
                        month: "short",
                        day: "numeric",
                        hour: "2-digit",
                        minute: "2-digit",
                      },
                    ),
                  }),
                  loaded.latest_version > loaded.version
                    ? t("routingProcess.versionLatest", {
                        version: loaded.latest_version,
                      })
                    : null,
                ]
                  .filter(Boolean)
                  .join(" · ")
              : t("common.loading")}
          </DialogDescription>
        </DialogHeader>
        <SegmentedControl<VersionView>
          label={t("routingProcess.versionView")}
          options={[
            { value: "rules", label: t("routingProcess.versionRules") },
            { value: "json", label: "JSON" },
          ]}
          value={view}
          onValueChange={setView}
        />
        {loaded ? (
          view === "rules" ? (
            <VersionRules version={loaded} services={services} />
          ) : (
            <pre className="m-0 max-h-80 overflow-auto rounded-md border bg-muted p-3 font-mono text-xs whitespace-pre-wrap text-foreground">
              {json}
            </pre>
          )
        ) : null}
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={!loaded}
            onClick={() => copy.copy("routing-version", json)}
          >
            {copy.activeKey === "routing-version" && copy.state === "copied"
              ? t("common.copied")
              : copy.activeKey === "routing-version" && copy.state === "failed"
                ? t("common.copyFailed")
                : t("routingProcess.copyJson")}
          </Button>
          <DialogClose asChild>
            <Button type="button">{t("common.close")}</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
