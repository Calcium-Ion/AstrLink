import { Badge } from "@/components/ui/badge";

import { AutoRoutingShowcase } from "./AutoRoutingShowcase";
import { i18n } from "./i18n";
import { PageHeader } from "./PageHeader";

/** Standalone preview page wrapper; the live routing page embeds AutoRoutingShowcase. */
export function RoutingModelsPreview() {
  const t = i18n.t.bind(i18n);
  return (
    <section
      aria-labelledby="routing-preview-title"
      className="mx-auto w-full max-w-[1120px]"
    >
      <PageHeader
        actions={<Badge variant="secondary">{t("routes.unconfigured")}</Badge>}
        description={t("routes.autoHint")}
        title={t("routes.autoTitle")}
        titleId="routing-preview-title"
      />
      <AutoRoutingShowcase />
    </section>
  );
}
