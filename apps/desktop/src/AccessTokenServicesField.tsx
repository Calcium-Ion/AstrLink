import { useId, useState } from "react";

import type {
  AccessTokenServiceScope,
  AccessTokenSummary,
} from "./access-token-model";
import { currentLocale, i18n } from "./i18n";
import { serviceKindLabel, type Service } from "./service-model";
import { ChoiceCard } from "@/components/ChoiceCard";
import { FormMessage } from "@/components/FormMessage";
import { SearchInput } from "@/components/SearchInput";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import { BadgeAlert, Server } from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { RadioGroup } from "@/components/ui/radio-group";
import { cn } from "@/lib/utils";

export interface AccessTokenServicesDraft {
  scope: AccessTokenServiceScope;
  ids: string[];
}

/**
 * Shows which providers a limited token may use. Tokens that may use every
 * provider stay unmarked, as before the limit existed.
 */
export function AccessTokenScopeBadge({
  token,
  services,
  servicesReady,
}: {
  token: Pick<AccessTokenSummary, "service_scope" | "service_ids">;
  services: Service[];
  servicesReady: boolean;
}) {
  const t = i18n.t.bind(i18n);
  if (token.service_scope !== "selected") return null;
  const byID = new Map(services.map((service) => [service.id, service]));
  const live = token.service_ids.flatMap((id) => byID.get(id) ?? []);
  if (servicesReady && live.length === 0) {
    return (
      <Badge
        variant="outline"
        className="min-w-8 shrink-[8] border-danger-foreground/30 bg-danger-wash text-danger-foreground"
        title={t("tokens.scopeEmptyHint")}
      >
        <BadgeAlert aria-hidden="true" />
        <span className="truncate">{t("tokens.scopeEmpty")}</span>
      </Badge>
    );
  }
  const names = live.map((service) => service.name);
  const label =
    live.length === 1
      ? live[0].name
      : t("tokens.scopeCount", { count: token.service_ids.length });
  return (
    <Badge
      variant="accent"
      className="max-w-48 min-w-8 shrink-[8]"
      title={t("tokens.scopeTitle", {
        names: new Intl.ListFormat(currentLocale()).format(names),
      })}
    >
      <Server aria-hidden="true" />
      <span className="truncate">{label}</span>
    </Badge>
  );
}

export function AccessTokenServicesField({
  disabled,
  error,
  services,
  value,
  onChange,
}: {
  disabled?: boolean;
  error?: string | null;
  services: Service[];
  value: AccessTokenServicesDraft;
  onChange: (value: AccessTokenServicesDraft) => void;
}) {
  const t = i18n.t.bind(i18n);
  const id = useId();
  const [query, setQuery] = useState("");
  const search = query.trim().toLocaleLowerCase();
  const visible = services.filter((service) =>
    `${service.name} ${serviceKindLabel(service.kind)}`
      .toLocaleLowerCase()
      .includes(search),
  );
  const selected = new Set(value.ids);
  const toggle = (serviceID: string, checked: boolean) => {
    const ids = checked
      ? [...value.ids.filter((item) => item !== serviceID), serviceID]
      : value.ids.filter((item) => item !== serviceID);
    onChange({ scope: "selected", ids });
  };

  return (
    <div className="grid gap-2">
      <span className="text-sm font-medium" id={`${id}-label`}>
        {t("tokens.scopeLabel")}
      </span>
      <RadioGroup
        aria-labelledby={`${id}-label`}
        className="grid grid-cols-2 gap-2 max-[560px]:grid-cols-1"
        disabled={disabled}
        onValueChange={(scope) =>
          onChange({ ...value, scope: scope as AccessTokenServiceScope })
        }
        value={value.scope}
      >
        <ChoiceCard
          description={t("tokens.scopeAllHint")}
          disabled={disabled}
          id={`${id}-all`}
          label={t("tokens.scopeAll")}
          selected={value.scope === "all"}
          value="all"
        />
        <ChoiceCard
          description={t("tokens.scopeSelectedHint")}
          disabled={disabled}
          id={`${id}-selected`}
          label={t("tokens.scopeSelected")}
          selected={value.scope === "selected"}
          value="selected"
        />
      </RadioGroup>
      {value.scope === "selected" ? (
        <div
          className={cn(
            "grid overflow-hidden rounded-md border",
            error && "border-danger-foreground/50",
          )}
        >
          <div className="flex items-center gap-2 border-b bg-muted/40 p-1.5">
            <SearchInput
              className="min-w-0 flex-1"
              clearLabel={t("common.clearSearch")}
              label={t("tokens.scopeSearch")}
              onValueChange={setQuery}
              placeholder={t("tokens.scopeSearch")}
              size="sm"
              value={query}
            />
            <span className="shrink-0 px-1 text-xs text-muted-foreground tabular-nums">
              {t("tokens.scopePicked", {
                picked: value.ids.length,
                total: services.length,
              })}
            </span>
          </div>
          <ul
            aria-label={t("tokens.scopeLabel")}
            className="max-h-[clamp(5rem,calc(100dvh-30rem),11rem)] overflow-y-auto"
          >
            {services.length === 0 ? (
              <li className="px-3 py-4 text-center text-xs text-muted-foreground">
                {t("tokens.scopeNoServices")}
              </li>
            ) : visible.length === 0 ? (
              <li className="px-3 py-4 text-center text-xs text-muted-foreground">
                {t("common.noSearchResults")}
              </li>
            ) : (
              visible.map((service) => {
                const checkboxID = `${id}-${service.id}`;
                return (
                  <li className="border-b last:border-b-0" key={service.id}>
                    <Label
                      className={cn(
                        "cursor-pointer gap-2.5 px-2.5 py-2 text-sm leading-normal font-normal select-auto hover:bg-muted/50",
                        disabled && "cursor-not-allowed opacity-60",
                      )}
                      htmlFor={checkboxID}
                    >
                      <Checkbox
                        checked={selected.has(service.id)}
                        disabled={disabled}
                        id={checkboxID}
                        onCheckedChange={(checked) =>
                          toggle(service.id, checked === true)
                        }
                      />
                      <ServiceKindIcon kind={service.kind} size={18} />
                      <span className="grid min-w-0 flex-1">
                        <span className="truncate text-sm font-medium">
                          {service.name}
                        </span>
                        <span className="truncate text-micro text-muted-foreground">
                          {t("tokens.scopeServiceMeta", {
                            kind: serviceKindLabel(service.kind),
                            count: service.models.length,
                          })}
                          {service.enabled
                            ? ""
                            : ` · ${t("tokens.scopeServiceOff")}`}
                        </span>
                      </span>
                    </Label>
                  </li>
                );
              })
            )}
          </ul>
        </div>
      ) : null}
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {value.scope === "selected" && !error ? (
        <p className="text-xs text-muted-foreground">
          {t("tokens.scopeModelsHint")}
        </p>
      ) : null}
    </div>
  );
}
