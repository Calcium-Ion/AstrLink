import { Card } from "@/components/ui/card";

interface PreviewCategory {
  id: string;
  label: string;
  description: string;
}

const routingSteps = [
  {
    id: "understand",
    label: "理解任务",
    detail: "根据当前请求判断任务类型",
  },
  {
    id: "category",
    label: "匹配分类模型池",
    detail: "只使用该分类下你配置的模型",
  },
  {
    id: "priority",
    label: "按优先级尝试",
    detail: "首选失败时自动回退到备用",
  },
] as const;

const previewCategories: PreviewCategory[] = [
  {
    id: "general",
    label: "通用问答",
    description: "日常问答、解释与常规助手任务。",
  },
  {
    id: "code",
    label: "编程开发",
    description: "代码生成、调试、审查与技术实现。",
  },
  {
    id: "writing",
    label: "内容创作",
    description: "写作、改写、摘要与内容表达。",
  },
  {
    id: "reasoning",
    label: "分析推理",
    description: "需要多步分析、规划与复杂判断的任务。",
  },
];

export function AutoRoutingShowcase() {
  return (
    <div data-testid="auto-routing-showcase">
      <Card className="grid grid-cols-1 gap-0 overflow-hidden rounded-2xl border-primary/20 shadow-[0_10px_32px_color-mix(in_srgb,var(--foreground)_5%,transparent)]">
        <div className="min-w-0 p-5">
          <span className="block text-[9px] font-bold text-muted-foreground">公开模型名</span>
          <code className="mt-[9px] block overflow-hidden text-[clamp(22px,3vw,32px)] font-[750] tracking-[-0.04em] text-accent-foreground text-ellipsis whitespace-nowrap">astrlink/auto</code>
          <p className="mt-2 text-[10px] leading-6 text-text-secondary">
            真实上游模型由本地任务分类与你配置的分类模型池决定。分类与运行时通过验收前不可启用，也不会写入
            Core。
          </p>
        </div>
      </Card>

      <ol aria-label="自动路由流程" className="mt-3.5 grid list-none grid-cols-3 gap-2 p-0 max-[720px]:grid-cols-1">
        {routingSteps.map((step, index) => (
          <li className="flex min-w-0 items-center gap-[9px] rounded-[11px] border bg-card p-[11px]" key={step.id}>
            <span aria-hidden="true" className="grid size-7 shrink-0 place-items-center rounded-[9px] bg-accent text-[9px] font-extrabold text-accent-foreground">
              {String(index + 1).padStart(2, "0")}
            </span>
            <div className="flex min-w-0 flex-col gap-[3px]">
              <strong className="text-[10.5px]">{step.label}</strong>
              <small className="overflow-hidden text-[8.5px] leading-[1.4] text-muted-foreground">{step.detail}</small>
            </div>
          </li>
        ))}
      </ol>

      <section
        aria-labelledby="routing-categories-title"
        className="mt-[22px]"
      >
        <div className="mb-[9px] flex items-end justify-between gap-3.5">
          <div>
            <span className="block text-[8.5px] font-[750] tracking-[0.08em] text-muted-foreground uppercase">任务分类</span>
            <h3 className="mt-1 text-sm tracking-[-0.015em]" id="routing-categories-title">按分类配置模型池</h3>
          </div>
          <small className="text-[9px] text-muted-foreground">示意，不可配置</small>
        </div>

        <div className="grid grid-cols-2 gap-2.5 max-[720px]:grid-cols-1">
          {previewCategories.map((category) => (
            <Card className="min-w-0 gap-0 rounded-[13px] p-3.5 shadow-none" data-testid="routing-category" key={category.id}>
              <header className="flex min-w-0 items-start justify-between gap-2.5">
                <div className="min-w-0">
                  <code className="block overflow-hidden text-[8.5px] font-bold text-accent-foreground text-ellipsis whitespace-nowrap">{category.id}</code>
                  <h4 className="mt-1 text-xs">{category.label}</h4>
                </div>
              </header>
              <p className="mt-2 text-[9px] leading-6 text-text-secondary">{category.description}</p>
            </Card>
          ))}
        </div>
      </section>
    </div>
  );
}
