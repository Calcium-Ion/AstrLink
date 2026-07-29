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
    <div className="routing-showcase" data-testid="auto-routing-showcase">
      <div className="routing-preview__hero">
        <div className="routing-preview__model-name">
          <span>公开模型名</span>
          <code>astrlink/auto</code>
          <p>
            真实上游模型由本地任务分类与你配置的分类模型池决定。分类与运行时通过验收前不可启用，也不会写入
            Core。
          </p>
        </div>
      </div>

      <ol aria-label="自动路由流程" className="routing-flow">
        {routingSteps.map((step, index) => (
          <li key={step.id}>
            <span aria-hidden="true" className="routing-flow__index">
              {String(index + 1).padStart(2, "0")}
            </span>
            <div>
              <strong>{step.label}</strong>
              <small>{step.detail}</small>
            </div>
          </li>
        ))}
      </ol>

      <section
        aria-labelledby="routing-categories-title"
        className="routing-preview__section"
      >
        <div className="routing-preview__section-heading">
          <div>
            <span>任务分类</span>
            <h3 id="routing-categories-title">按分类配置模型池</h3>
          </div>
          <small>示意，不可配置</small>
        </div>

        <div className="routing-category-grid">
          {previewCategories.map((category) => (
            <article className="routing-category-card" key={category.id}>
              <header>
                <div>
                  <code>{category.id}</code>
                  <h4>{category.label}</h4>
                </div>
              </header>
              <p>{category.description}</p>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
