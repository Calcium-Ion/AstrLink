import { Brain } from "@/components/icons";
import { vendorMarks, type Vendor } from "@/components/vendor-marks";

import { cn } from "@/lib/utils";

// Keywords follow @lobehub/icons `modelMappings`, in its order, for the
// vendors kept here. The mark itself comes from `vendorMarks` so a model shows
// the same logo as its provider kind. Other models fall back to the Brain mark.
const brands: { vendor: Vendor; keywords: string[] }[] = [
  {
    vendor: "openai",
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
    vendor: "zhipu",
    keywords: ["^glm-", "/glm-", "/glm\\d", "-glm-", "chatglm"],
  },
  { vendor: "claude", keywords: ["claude"] },
  { vendor: "anthropic", keywords: ["anthropic"] },
  { vendor: "meta", keywords: ["llama", "/l3"] },
  { vendor: "gemini", keywords: ["gemini"] },
  { vendor: "gemma", keywords: ["gemma"] },
  { vendor: "kimi", keywords: ["kimi", "moonshot"] },
  {
    vendor: "qwen",
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
  { vendor: "minimax", keywords: ["minimax", "abab", "^image-"] },
  {
    vendor: "mistral",
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
  { vendor: "stepfun", keywords: ["step"] },
  { vendor: "wenxin", keywords: ["ernie", "irag"] },
  { vendor: "doubao", keywords: ["^ep-", "doubao-"] },
  { vendor: "hunyuan", keywords: ["hunyuan", "hy3"] },
  { vendor: "bytedance", keywords: ["skylark", "seed-", "bytedance"] },
  { vendor: "grok", keywords: ["^grok-", "/grok-"] },
  { vendor: "deepseek", keywords: ["deepseek"] },
  { vendor: "xiaomi", keywords: ["^mimo-", "/mimo-"] },
];

const matchers = brands.map(({ vendor, keywords }) => ({
  vendor,
  patterns: keywords.map((keyword) => new RegExp(keyword, "i")),
}));

/** The vendor a model id belongs to, or null when no brand keyword matches. */
export function modelVendor(model: string | null | undefined): Vendor | null {
  if (!model?.trim()) return null;
  return (
    matchers.find(({ patterns }) =>
      patterns.some((pattern) => pattern.test(model)),
    )?.vendor ?? null
  );
}

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

  const vendor = modelVendor(model);
  const BrandIcon = vendor ? vendorMarks[vendor] : null;

  return (
    <span
      aria-hidden="true"
      className={cn("inline-flex shrink-0 items-center", className)}
    >
      {BrandIcon ? <BrandIcon size={size} /> : <Brain size={size} />}
    </span>
  );
}
