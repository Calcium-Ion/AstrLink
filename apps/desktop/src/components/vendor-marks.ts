import {
  AnthropicMono,
  AntigravityColor,
  ByteDanceColor,
  ClaudeColor,
  CodexColor,
  DeepSeekColor,
  DoubaoColor,
  GeminiColor,
  GemmaColor,
  GrokMono,
  HunyuanColor,
  KimiMono,
  MetaColor,
  MinimaxColor,
  MistralColor,
  OpenAIMono,
  OpenCodeMono,
  QwenColor,
  StepfunMono,
  WenxinColor,
  XiaomiMiMoMono,
  ZhipuColor,
} from "@/components/brand-icons";

/**
 * One mark per vendor. Model names, provider kinds and client types all draw
 * their vendor's logo from here, so the same company never shows two logos
 * (for example Z.ai's "Z" beside a model and the 智谱 mark beside its
 * provider). Pick the mark that matches the vendor name used in the UI.
 */
export const vendorMarks = {
  anthropic: AnthropicMono,
  antigravity: AntigravityColor,
  bytedance: ByteDanceColor,
  claude: ClaudeColor,
  codex: CodexColor,
  deepseek: DeepSeekColor,
  doubao: DoubaoColor,
  gemini: GeminiColor,
  gemma: GemmaColor,
  grok: GrokMono,
  hunyuan: HunyuanColor,
  kimi: KimiMono,
  meta: MetaColor,
  minimax: MinimaxColor,
  mistral: MistralColor,
  openai: OpenAIMono,
  opencode: OpenCodeMono,
  qwen: QwenColor,
  stepfun: StepfunMono,
  wenxin: WenxinColor,
  xiaomi: XiaomiMiMoMono,
  zhipu: ZhipuColor,
} satisfies Record<string, typeof OpenAIMono>;

export type Vendor = keyof typeof vendorMarks;
