import { Panel } from "@/components/Panel";
import { SectionKicker } from "@/components/SectionKicker";

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
      <Panel className="p-4">
        <SectionKicker>公开模型名</SectionKicker>
        <code className="mt-1.5 block truncate font-mono text-xl font-semibold tracking-tight text-primary">
          astrlink/auto
        </code>
        <p className="mt-2 max-w-[68ch] text-xs text-text-secondary">
          真实上游模型由本地任务分类与你配置的分类模型池决定。分类与运行时通过验收前不可启用，也不会写入
          Core。
        </p>
      </Panel>

      <ol aria-label="自动路由流程" className="mt-3 grid list-none grid-cols-3 gap-2 p-0 max-[720px]:grid-cols-1">
        {routingSteps.map((step, index) => (
          <li className="flex min-w-0 items-center gap-2.5 rounded-md border bg-card p-3" key={step.id}>
            <span aria-hidden="true" className="grid size-6 shrink-0 place-items-center rounded-sm border bg-muted text-micro font-medium tabular-nums">
              {String(index + 1).padStart(2, "0")}
            </span>
            <div className="flex min-w-0 flex-col gap-0.5">
              <strong className="text-sm font-medium">{step.label}</strong>
              <small className="truncate text-xs text-muted-foreground">{step.detail}</small>
            </div>
          </li>
        ))}
      </ol>

      <section aria-labelledby="routing-categories-title" className="mt-5">
        <div className="mb-2 flex items-end justify-between gap-3">
          <div>
            <SectionKicker>任务分类</SectionKicker>
            <h3
              className="mt-1 text-base font-semibold tracking-tight"
              id="routing-categories-title"
            >
              按分类配置模型池
            </h3>
          </div>
          <small className="text-xs text-muted-foreground">示意，不可配置</small>
        </div>

        <div className="grid grid-cols-2 gap-2 max-[720px]:grid-cols-1">
          {previewCategories.map((category) => (
            <Panel className="min-w-0 p-3" data-testid="routing-category" key={category.id}>
              <code className="block truncate font-mono text-micro text-accent-foreground">
                {category.id}
              </code>
              <h4 className="mt-1 text-sm font-medium">{category.label}</h4>
              <p className="mt-1 text-xs text-text-secondary">
                {category.description}
              </p>
            </Panel>
          ))}
        </div>
      </section>
    </div>
  );
}
