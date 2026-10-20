import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { ArrowUpRight, BadgeAlert, Ban, CircleCheck } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import type {
  RoutingFormatGap,
  RoutingFormatReport,
} from "@/model-routing-format-model";
import { protocolLabel } from "@/service-presets";

/** 一个不兼容的模型写成「模型 · 提供商 · 第 1、3 条」。 */
function gapText(gap: RoutingFormatGap, t: ReturnType<typeof useT>): string {
  const places = [
    ...(gap.rules.length > 0
      ? [
          t("routingFormats.rules", {
            numbers: gap.rules.join(t("routingFormats.listSeparator")),
          }),
        ]
      : []),
    ...(gap.fallback ? [t("dryRunTab.fallbackRow")] : []),
  ];
  return [
    gap.member.model,
    gap.service?.name ?? t("binding.removedService"),
    ...places,
  ].join(" · ");
}

/**
 * 路由组里有模型不兼容部分请求格式时，编辑页顶部的汇总：按格式列出不兼容的模型和
 * 它所在的规则；提供商能转换的，一键开启。组里的模型在所有格式上一致时不出现。
 */
export function ModelRoutingFormatNotice({
  report,
  busy,
  error,
  onEnable,
  onOpenService,
}: {
  report: RoutingFormatReport;
  busy: boolean;
  error: string | null;
  onEnable?: () => void;
  onOpenService?: (serviceId: string) => void;
}) {
  const t = useT();
  if (!report.mixed) return null;
  const fixable = [...report.fixable.values()];
  const firstGap = report.rows.flatMap((row) => row.gaps)[0];
  return (
    <div className="border-b px-3 py-3" data-routing-format-notice>
      <FormMessage tone="warning" asChild>
        <div className="grid gap-2">
          <div className="flex items-center gap-1.5">
            <BadgeAlert aria-hidden="true" className="size-4 shrink-0" />
            <span className="font-semibold">{t("routingFormats.title")}</span>
            <HelpPopover label={t("routingFormats.helpLabel")}>
              {t("routingFormats.help")}
            </HelpPopover>
          </div>
          <div
            className="grid grid-cols-[minmax(10rem,auto)_minmax(0,1fr)] items-center gap-x-4 gap-y-1.5 text-foreground"
            role="table"
            aria-label={t("routingFormats.title")}
          >
            <span
              role="columnheader"
              className="text-micro text-muted-foreground"
            >
              {t("routingFormats.format")}
            </span>
            <span
              role="columnheader"
              className="text-micro text-muted-foreground"
            >
              {t("routingFormats.incompatible")}
            </span>
            {report.rows.map((row) => (
              <div
                key={row.protocol}
                role="row"
                className="contents"
                data-routing-format-row={row.protocol}
              >
                <span role="cell">{protocolLabel(row.protocol)}</span>
                {row.gaps.length === 0 ? (
                  <span
                    role="cell"
                    className="inline-flex items-center gap-1 text-success-foreground"
                  >
                    <CircleCheck aria-hidden="true" className="size-3.5" />
                    {t("routingFormats.allCompatible")}
                  </span>
                ) : (
                  <span
                    role="cell"
                    className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 text-danger-foreground"
                  >
                    {row.gaps.map((gap) => (
                      <span
                        key={gap.member.id}
                        className="inline-flex min-w-0 items-center gap-1"
                      >
                        <Ban aria-hidden="true" className="size-3.5 shrink-0" />
                        <span className="truncate">{gapText(gap, t)}</span>
                      </span>
                    ))}
                  </span>
                )}
              </div>
            ))}
          </div>
          {error ? (
            <p className="text-danger-foreground" role="alert">
              {error}
            </p>
          ) : null}
          {(fixable.length > 0 && onEnable) || (firstGap && onOpenService) ? (
            <div className="flex flex-wrap items-center justify-end gap-2 border-t border-warning/30 pt-2">
              {firstGap && onOpenService ? (
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  className="h-auto gap-1 px-0 py-0 text-xs text-warning-foreground"
                  onClick={() => onOpenService(firstGap.member.service_id)}
                >
                  {t("routingFormats.openService")}
                  <ArrowUpRight aria-hidden="true" className="size-3" />
                </Button>
              ) : null}
              {fixable.length > 0 && onEnable ? (
                <Button
                  type="button"
                  size="sm"
                  disabled={busy}
                  aria-busy={busy}
                  onClick={onEnable}
                  data-routing-format-enable
                >
                  {fixable.length === 1
                    ? t("routingFormats.enableOne", {
                        service: fixable[0].service.name,
                      })
                    : t("routingFormats.enableMany", {
                        count: fixable.length,
                      })}
                </Button>
              ) : null}
            </div>
          ) : null}
        </div>
      </FormMessage>
    </div>
  );
}
