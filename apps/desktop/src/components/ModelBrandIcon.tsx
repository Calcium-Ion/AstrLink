import {
  AnthropicMono,
  ByteDanceColor,
  ClaudeColor,
  DeepSeekColor,
  DoubaoColor,
  GeminiColor,
  GemmaColor,
  GrokMono,
  HunyuanColor,
  MetaColor,
  MinimaxColor,
  MistralColor,
  MoonshotMono,
  OpenAIMono,
  QwenColor,
  StepfunMono,
  WenxinColor,
  XiaomiMiMoMono,
  ZAIMono,
} from "@/components/brand-icons";
import { Brain } from "@/components/icons";

import { cn } from "@/lib/utils";

// Keywords follow @lobehub/icons `modelMappings`, in its order, for the brands
// kept here. Other models fall back to the Brain mark.
const brands = [
  {
    Icon: OpenAIMono,
    keywords: [
      "gpt-3",
      "gpt-4",
      "gpt-5",
      "sora",
      "gpt-oss",
      "o1-",
      "^o1",
      "/o1",
      "o3-",
      "^o3",
      "/o3",
      "o4-",
      "^o4",
      "/o4",
      "dalle",
      "dall-e",
      "text-embedding-",
      "tts-",
      "whisper-",
      "codex",
      "davinci",
      "babbage",
      "omni-moderation",
      "text-moderation",
      "text-adb",
      "text-ada",
      "computer-use",
      "^gpt-",
      "/gpt-",
      "openai",
    ],
  },
  {
    Icon: ZAIMono,
    keywords: ["^glm-", "/glm-", "/glm\\d", "-glm-", "chatglm"],
  },
  { Icon: ClaudeColor, keywords: ["claude"] },
  { Icon: AnthropicMono, keywords: ["anthropic"] },
  { Icon: MetaColor, keywords: ["llama", "/l3"] },
  { Icon: GeminiColor, keywords: ["gemini"] },
  { Icon: GemmaColor, keywords: ["gemma"] },
  { Icon: MoonshotMono, keywords: ["kimi", "moonshot"] },
  {
    Icon: QwenColor,
    keywords: [
      "qwen",
      "qwq",
      "qvq",
      "wanx",
      "wan\\d/",
      "wan\\d\\.\\d-",
      "tongyi",
      "gte-rerank",
    ],
  },
  { Icon: MinimaxColor, keywords: ["minimax", "abab", "^image-"] },
  {
    Icon: MistralColor,
    keywords: [
      "mistral",
      "mixtral",
      "codestral",
      "mathstral",
      "/mn-",
      "pixtral",
      "ministral",
      "magistral",
      "devstral",
      "voxtral",
    ],
  },
  { Icon: StepfunMono, keywords: ["step"] },
  { Icon: WenxinColor, keywords: ["ernie", "irag"] },
  { Icon: DoubaoColor, keywords: ["^ep-", "doubao-"] },
  { Icon: HunyuanColor, keywords: ["hunyuan", "hy3"] },
  { Icon: ByteDanceColor, keywords: ["skylark", "seed-", "bytedance"] },
  { Icon: GrokMono, keywords: ["^grok-", "/grok-"] },
  { Icon: DeepSeekColor, keywords: ["deepseek"] },
  { Icon: XiaomiMiMoMono, keywords: ["^mimo-", "/mimo-"] },
].map(({ Icon, keywords }) => ({
  Icon,
  patterns: keywords.map((keyword) => new RegExp(keyword, "i")),
}));

export function ModelBrandIcon({
  className,
  model,
  size = 14,
}: {
  className?: string;
  model: string | null | undefined;
  size?: number;
}) {
  if (!model?.trim()) return null;

  const BrandIcon = brands.find(({ patterns }) =>
    patterns.some((pattern) => pattern.test(model)),
  )?.Icon;

  return (
    <span
      aria-hidden="true"
      className={cn("inline-flex shrink-0 items-center", className)}
    >
      {BrandIcon ? <BrandIcon size={size} /> : <Brain size={size} />}
    </span>
  );
}
