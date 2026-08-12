import { Badge } from "@/components/ui/badge";

import { AutoRoutingShowcase } from "./AutoRoutingShowcase";
import { PageHeader } from "./PageHeader";

/** Standalone preview page wrapper; the live routing page embeds AutoRoutingShowcase. */
export function RoutingModelsPreview() {
  return (
    <section
      aria-labelledby="routing-preview-title"
      className="mx-auto w-full max-w-[1120px]"
    >
      <PageHeader
        actions={
          <Badge className="bg-warning-wash text-warning-foreground" variant="secondary">
            尚未接入
          </Badge>
        }
        description="客户端使用 astrlink/auto，AstrLink 按任务分类从对应模型池中选择。"
        eyebrow="功能预览"
        title="自动选择合适的模型"
        titleId="routing-preview-title"
      />
      <AutoRoutingShowcase />
    </section>
  );
}
