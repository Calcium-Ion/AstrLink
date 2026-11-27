import { useEffect, useId, useState } from "react";
import { ModelChecklist } from "@/components/ModelChecklist";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { getCodexModelReasoning } from "./bridge";
import { maxListModels } from "./client-config-model";
import type { CodexModelReasoning } from "./mygo";
import { useT } from "./i18n";

export function useCodexModelReasoning(
  tokenId: string,
  inferenceURL: string,
  models: readonly string[],
) {
  const ids = [...new Set(models.filter(Boolean))].join("\n");
  const scope = JSON.stringify([tokenId, inferenceURL]);
  // Results stay for the token and address, so typing a default model only
  // looks up the names not seen yet instead of reloading the whole list.
  const [result, setResult] = useState<{
    scope: string;
    items: Map<string, CodexModelReasoning | null>;
  } | null>(null);
  const known = result?.scope === scope ? result.items : undefined;
  const missing = ids ? ids.split("\n").filter((id) => !known?.has(id)) : [];
  const wanted = missing.join("\n");
  useEffect(() => {
    if (!wanted) return;
    let current = true;
    const names = wanted.split("\n");
    const merge = (entries: [string, CodexModelReasoning | null][]) =>
      setResult((previous) => {
        const items = new Map(
          previous?.scope === scope ? previous.items : undefined,
        );
        for (const [id, value] of entries) items.set(id, value);
        return { scope, items };
      });
    async function load() {
      for (let i = 0; i < names.length && current; i += maxListModels) {
        const batch = names.slice(i, i + maxListModels);
        let entries: [string, CodexModelReasoning | null][];
        try {
          const values = await getCodexModelReasoning(
            tokenId,
            inferenceURL,
            batch,
          );
          entries = batch.map((id) => [
            id,
            Object.hasOwn(values, id) ? values[id] : null,
          ]);
        } catch {
          entries = batch.map((id) => [id, null]);
        }
        if (current) merge(entries);
      }
    }
    // A known list loads at once; names typed afterwards wait for a pause.
    const timer = setTimeout(() => void load(), known ? 300 : 0);
    return () => {
      current = false;
      clearTimeout(timer);
    };
    // `known` only picks the delay; the effect reruns when names change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wanted, inferenceURL, scope, tokenId]);
  return known;
}

export function CodexModelMenu({
  model,
  candidates,
  list,
  disabled,
  reasoning,
  onChange,
}: {
  model: string;
  candidates: readonly string[];
  list: readonly string[];
  disabled: boolean;
  reasoning: ReadonlyMap<string, CodexModelReasoning | null> | undefined;
  onChange: (models: string[]) => void;
}) {
  const t = useT();
  const title = useId();
  const selected = [...new Set([model, ...list].filter(Boolean))];
  const options = [...new Set([...candidates, ...selected])];
  return (
    <section className="mt-3 border-t pt-3" aria-labelledby={title}>
      <Collapsible>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="grid gap-1">
            <h4 className="text-xs font-medium text-text-secondary" id={title}>
              {t("clientSetup.codexMenu.title")}
            </h4>
            <p className="text-xs text-muted-foreground" aria-live="polite">
              {t("modelChecklist.count", {
                selected: selected.length,
                total: options.length,
              })}
            </p>
          </div>
          <CollapsibleTrigger asChild>
            <Button
              size="sm"
              type="button"
              variant="outline"
              disabled={disabled || options.length === 0}
            >
              {t("clientSetup.codexMenu.choose")}
            </Button>
          </CollapsibleTrigger>
        </div>
        {selected.length > maxListModels && (
          <p role="alert" className="pt-1 text-xs text-destructive">
            {t("clientSetup.codexMenu.limit", { count: maxListModels })}
          </p>
        )}
        <CollapsibleContent className="pt-2">
          <p className="pb-1.5 text-xs text-muted-foreground">
            {t("clientSetup.codexMenu.levels")}
          </p>
          <ModelChecklist
            label={t("clientSetup.switchable", { client: "Codex" })}
            className="max-h-64 overflow-y-auto"
            stackedDetails
            disabled={disabled}
            items={options.map((id) => {
              const info = reasoning?.get(id);
              const detail =
                info === undefined
                  ? t("clientSetup.modelsLoading")
                  : info?.levels.length
                    ? `${info.levels.join(" / ")} · ${info.default ? t("clientSetup.codexMenu.defaultLevel", { level: info.default }) : t("clientSetup.codexMenu.noDefault")}`
                    : t(
                        info?.reasoning === false
                          ? "clientSetup.codexMenu.notAdjustable"
                          : "clientSetup.codexMenu.unknown",
                      );
              return {
                id,
                detail,
                locked: id === model,
                adornment:
                  id === model ? (
                    <Badge variant="accent">
                      {t("clientSetup.defaultModel")}
                    </Badge>
                  ) : undefined,
              };
            })}
            selected={selected}
            onSelectedChange={onChange}
          />
        </CollapsibleContent>
      </Collapsible>
    </section>
  );
}
