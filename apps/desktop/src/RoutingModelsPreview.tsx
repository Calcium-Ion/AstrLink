import { PageHeader } from "./PageHeader";

interface PreviewCategory {
  id: string;
  label: string;
  description: string;
  targets: [string, string];
}

const routingSteps = [
  {
    id: "constraints",
    label: "硬约束",
    detail: "先过滤协议、工具、上下文与健康状态",
  },
  {
    id: "classifier",
    label: "mmBERT-small 分类",
    detail: "本地输出唯一 category_id",
  },
  {
    id: "category",
    label: "分类模型池",
    detail: "只读取命中分类拥有的模型",
  },
  {
    id: "priority",
    label: "优先级选择",
    detail: "按桶内稳定顺序尝试与回退",
  },
] as const;

const previewCategories: PreviewCategory[] = [
  {
    id: "general",
    label: "通用问答",
    description: "日常问答、解释与常规助手任务。",
    targets: ["demo/general-primary", "demo/general-backup"],
  },
  {
    id: "code",
    label: "编程开发",
    description: "代码生成、调试、审查与技术实现。",
    targets: ["demo/code-primary", "demo/code-backup"],
  },
  {
    id: "writing",
    label: "内容创作",
    description: "写作、改写、摘要与内容表达。",
    targets: ["demo/writing-primary", "demo/writing-backup"],
  },
  {
    id: "reasoning",
    label: "分析推理",
    description: "需要多步分析、规划与复杂判断的任务。",
    targets: ["demo/reasoning-primary", "demo/reasoning-backup"],
  },
];

export function RoutingModelsPreview() {
  return (
    <section
      aria-labelledby="routing-preview-title"
      className="routing-preview"
    >
      <PageHeader
        actions={<span className="routing-preview__state">尚未接入</span>}
        description={
          <>
            客户端只需使用虚拟模型名，AstrLink
            将请求归入固定任务分类，再从该分类配置的模型池中选择。
          </>
        }
        eyebrow="功能预览"
        title="自动选择合适的模型"
        titleId="routing-preview-title"
      />

      <div className="routing-preview__hero">
        <div className="routing-preview__model-name">
          <span>公开模型名</span>
          <code>astrlink/auto</code>
          <p>真实上游模型由本地分类结果和用户配置共同决定。</p>
        </div>
        <div className="routing-preview__taxonomy">
          <span>任务分类体系</span>
          <div>
            <code>astrlink-text-v1</code>
            <strong>规划中</strong>
          </div>
          <p>分类语义固定版本；分类器实现不会进入公开路由合同。</p>
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
        aria-labelledby="routing-classifier-title"
        className="routing-preview__section"
      >
        <div className="routing-preview__section-heading">
          <div>
            <span>本地分类器</span>
            <h3 id="routing-classifier-title">请求理解</h3>
          </div>
          <small>当前方案仅展示一个分类器</small>
        </div>

        <article
          className="routing-classifier-card"
          data-testid="routing-classifier-card"
        >
          <div className="routing-classifier-card__main">
            <div className="routing-classifier-card__heading">
              <div>
                <span className="routing-classifier-card__badge">唯一分类器</span>
                <h4>mmBERT-small</h4>
                <code>jhu-clsp/mmBERT-small</code>
              </div>
              <span className="routing-classifier-card__status">
                尚未安装
              </span>
            </div>
            <p>
              微调后的本地单标签分类器。它只接收有界请求文本并输出一个
              <code>category_id</code>，不会接收、评价或排序具体模型。
            </p>
            <dl className="routing-classifier-card__facts">
              <div>
                <dt>规模</dt>
                <dd>约 140M 参数</dd>
              </div>
              <div>
                <dt>语言</dt>
                <dd>多语言，包含中文与英文</dd>
              </div>
              <div>
                <dt>输入边界</dt>
                <dd>最多 512 tokens</dd>
              </div>
              <div>
                <dt>计划运行时</dt>
                <dd>本地 CPU · ONNX INT8</dd>
              </div>
            </dl>
          </div>

          <div className="routing-classifier-card__action">
            <strong>按需下载</strong>
            <p>
              分类器不会打包进 AstrLink。正式量化产物发布后再显示准确下载大小。
            </p>
            <button className="btn-primary" disabled type="button">
              下载并启用（开发中）
            </button>
            <small>当前按钮不会发起网络请求或安装操作。</small>
          </div>
        </article>
      </section>

      <section
        aria-labelledby="routing-categories-title"
        className="routing-preview__section"
      >
        <div className="routing-preview__section-heading">
          <div>
            <span>分类模型池</span>
            <h3 id="routing-categories-title">一个分类可以配置多个模型</h3>
          </div>
          <small>以下分类尚未冻结</small>
        </div>

        <p className="routing-preview__notice" role="note">
          以下分类与模型均为界面示意，不会保存、调用 Core 或影响任何请求。
        </p>

        <div className="routing-category-grid">
          {previewCategories.map((category) => (
            <article className="routing-category-card" key={category.id}>
              <header>
                <div>
                  <code>{category.id}</code>
                  <h4>{category.label}</h4>
                </div>
                <span>示意</span>
              </header>
              <p>{category.description}</p>
              <ol aria-label={`${category.label}示意模型`}>
                {category.targets.map((target, index) => (
                  <li key={target}>
                    <span>优先级 {index + 1}</span>
                    <code>{target}</code>
                    <small>{index === 0 ? "首选" : "备用"}</small>
                  </li>
                ))}
              </ol>
            </article>
          ))}
        </div>
      </section>

      <aside className="routing-preview__contract-note">
        <strong>分类与模型保持解耦</strong>
        <p>
          mmBERT-small 只决定任务属于哪个分类；具体模型列表属于对应分类，
          再由桶内优先级提供确定性选择与 fallback。
        </p>
      </aside>
    </section>
  );
}
