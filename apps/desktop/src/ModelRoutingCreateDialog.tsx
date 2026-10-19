import { useEffect, useState } from "react";

import { Field } from "@/components/Field";
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
import { useT } from "@/i18n";
import { ModelRoutingTargetSelect } from "@/ModelRoutingTargetSelect";
import {
  routingGroupNameIssue,
  suggestRoutingGroupName,
  type RoutingTarget,
} from "@/model-routing-editor-model";
import { modelRoutingPublicModelID } from "@/model-routing-group-model";
import type { RoutableService } from "@/service-model";

/**
 * 新建路由组：一个模型名加一个默认模型就是一个能用的组，规则之后在编辑页里加。
 * 这样新组从一开始就合法，不会出现「至少需要一个成员」那种半成品。
 */
export function ModelRoutingCreateDialog({
  open,
  existing,
  services,
  creating,
  error,
  onCancel,
  onCreate,
}: {
  open: boolean;
  existing: readonly string[];
  services: readonly RoutableService[];
  creating: boolean;
  error: string | null;
  onCancel: () => void;
  onCreate: (name: string, target: RoutingTarget) => void;
}) {
  const t = useT();
  const [name, setName] = useState("");
  const [target, setTarget] = useState<RoutingTarget | undefined>();
  const [touched, setTouched] = useState(false);

  // Each opening starts from a fresh suggestion; the existing names change.
  useEffect(() => {
    if (!open) return;
    setName(suggestRoutingGroupName(existing));
    setTarget(undefined);
    setTouched(false);
  }, [open]);

  const issue = routingGroupNameIssue(name, existing);
  const submit = () => {
    setTouched(true);
    if (issue !== null || target === undefined || creating) return;
    onCreate(name, target);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !creating) onCancel();
      }}
    >
      <DialogContent
        className="sm:max-w-md"
        data-testid="routing-create-dialog"
      >
        <DialogHeader>
          <DialogTitle>{t("routingGroups.create.title")}</DialogTitle>
          <DialogDescription>
            {t("routingGroups.create.description")}
          </DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <Field
            label={t("routingGroups.create.name")}
            hint={
              touched && issue
                ? undefined
                : t("routingGroups.create.nameHint", {
                    model: modelRoutingPublicModelID(name || "…"),
                  })
            }
          >
            <Input
              className="font-mono"
              value={name}
              maxLength={64}
              aria-invalid={touched && issue !== null}
              onChange={(event) => setName(event.target.value.toLowerCase())}
            />
          </Field>
          {touched && issue ? (
            <p role="alert" className="-mt-2 text-xs text-danger-foreground">
              {t(`routingGroups.nameIssues.${issue}`)}
            </p>
          ) : null}
          <Field
            group
            label={t("routingGroups.create.defaultModel")}
            hint={t("routingGroups.create.defaultModelHint")}
          >
            <ModelRoutingTargetSelect
              services={services}
              value={target}
              onChange={setTarget}
              aria-label={t("routingGroups.create.defaultModel")}
            />
          </Field>
          {error ? <FormMessage tone="error">{error}</FormMessage> : null}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={creating}
              onClick={onCancel}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              disabled={creating || target === undefined}
              data-routing-create
            >
              {creating
                ? t("routingGroups.create.creating")
                : t("routingGroups.create.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
