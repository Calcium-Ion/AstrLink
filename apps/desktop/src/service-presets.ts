import { i18n } from "./i18n";
import type { ClientType } from "./request-record-model";
import {
  bestConversionTarget,
  type HTTPServiceKind,
  type ServiceAuthScheme,
  type ServiceCapability,
} from "./service-model";

export type HTTPServicePresetID = HTTPServiceKind;

export const codingPlanPresetIDs: HTTPServicePresetID[] = [
  "opencode_go",
  "kimi_coding",
  "glm_coding",
  "minimax_coding",
];

export const payAsYouGoPresetIDs: HTTPServicePresetID[] = [
  "opencode_zen",
  "openai",
  "anthropic",
  "gemini",
  "deepseek",
  "qwen",
  "moonshot",
  "glm",
  "minimax",
  "doubao",
  "xai",
];

export interface ProtocolDescriptor {
  id: string;
  phase: "alpha" | "post_alpha";
  primary: boolean;
  streaming: boolean;
}

/** Where a preset account lives; "official" marks a single-site vendor. */
export type ServiceSiteID = "cn" | "global" | "official";

export interface ServiceSite {
  id: ServiceSiteID;
  baseURL: string;
  /** Other hosts of the same site that saved addresses may still use. */
  aliases?: readonly string[];
}

export interface HTTPServicePreset {
  id: HTTPServicePresetID;
  label: string;
  description: string;
  defaultName: string;
  kind: HTTPServiceKind;
  baseURL: string;
  baseURLPlaceholder: string;
  /** Selectable vendor addresses; empty when the user must supply one. */
  sites: readonly ServiceSite[];
  authScheme: ServiceAuthScheme;
  headerName: string;
  capabilities: ServiceCapability[];
  advancedOnStart: boolean;
  models?: string[];
}

export const localConversionPassthrough = "none";

export type ConversionQuality = "good" | "fair" | "discouraged";

export interface ConversionTarget {
  id: string;
  enabled: boolean;
  quality: ConversionQuality | null;
  streaming: boolean;
}

export interface ConversionEngineSnapshot {
  available: boolean;
  edges: Array<{
    from: string;
    to: string;
    quality?: ConversionQuality;
    streaming: boolean;
  }>;
}

/**
 * Inference protocols the conversion engine can actually bridge. Discovery
 * protocols and the Responses Compact / legacy Completions variants have no
 * advertised edges, so offering them would only ever render dead options.
 */
const convertibleProtocolIDs = [
  "openai.responses",
  "anthropic.messages",
  "google.generate_content",
  "openai.chat",
] as const;

export function conversionQualityLabel(quality: ConversionQuality): string {
  if (quality === "good") return i18n.t("presets.qualityGood");
  if (quality === "fair") return i18n.t("presets.qualityFair");
  return i18n.t("presets.qualityDiscouraged");
}

export const conversionQualityLabels: Record<ConversionQuality, string> = {
  get good() {
    return conversionQualityLabel("good");
  },
  get fair() {
    return conversionQualityLabel("fair");
  },
  get discouraged() {
    return conversionQualityLabel("discouraged");
  },
};

export function supportsLocalConversion(protocolID: string): boolean {
  return (convertibleProtocolIDs as readonly string[]).includes(protocolID);
}

export function localConversionTargets(
  protocolID: string,
  engine?: ConversionEngineSnapshot | null,
): ConversionTarget[] {
  const advertised = new Map(
    (engine?.available === true ? engine.edges : [])
      .filter((edge) => edge.from === protocolID)
      .map((edge) => [edge.to, edge] as const),
  );
  return convertibleProtocolIDs
    .filter((id) => id !== protocolID)
    .map((id) => {
      const edge = advertised.get(id);
      return {
        id,
        enabled: edge !== undefined,
        quality: edge?.quality ?? null,
        streaming: edge?.streaming ?? false,
      };
    });
}

export const alphaProtocolDescriptors: readonly ProtocolDescriptor[] = [
  { id: "openai.responses", phase: "alpha", primary: true, streaming: true },
  {
    id: "openai.responses.compact",
    phase: "alpha",
    primary: false,
    streaming: false,
  },
  {
    id: "anthropic.messages",
    phase: "alpha",
    primary: false,
    streaming: true,
  },
  {
    id: "google.generate_content",
    phase: "alpha",
    primary: false,
    streaming: true,
  },
  { id: "openai.chat", phase: "alpha", primary: false, streaming: true },
  {
    id: "openai.completions",
    phase: "alpha",
    primary: false,
    streaming: true,
  },
  { id: "openai.models", phase: "alpha", primary: false, streaming: false },
  { id: "google.models", phase: "alpha", primary: false, streaming: false },
];

export const protocolLabels: Readonly<Record<string, string>> = {
  "openai.responses": "OpenAI Responses",
  "openai.responses.compact": "Responses Compact",
  "anthropic.messages": "Anthropic Messages",
  "google.generate_content": "Gemini Generate Content",
  "openai.chat": "OpenAI Chat Completions",
  "openai.completions": "OpenAI Legacy Completions",
  "openai.models": "OpenAI Models",
  "google.models": "Gemini Models",
};

export const protocolClients: Readonly<Record<string, string>> = {
  "openai.responses": "services.protocolClients.responses",
  "openai.chat": "services.protocolClients.chat",
  "anthropic.messages": "services.protocolClients.messages",
  "google.generate_content": "services.protocolClients.gemini",
};

/** Marks for the clients named in `protocolClients`, in the same order. */
export const protocolClientTypes: Readonly<
  Record<string, readonly ClientType[]>
> = {
  "openai.responses": ["codex"],
  "openai.chat": ["opencode", "openclaw"],
  "anthropic.messages": ["claude_code"],
  "google.generate_content": ["gemini_cli"],
};

/** Client entry path on the local inference plane. Gemini keeps the action suffix. */
export const protocolEntryPaths: Readonly<Record<string, string>> = {
  "openai.responses": "/v1/responses",
  "openai.responses.compact": "/v1/responses/compact",
  "anthropic.messages": "/v1/messages",
  "google.generate_content": "/v1beta/models/:model:generateContent",
  "openai.chat": "/v1/chat/completions",
  "openai.completions": "/v1/completions",
  "openai.models": "/v1/models",
  "google.models": "/v1beta/models",
};

export function protocolEntryPath(
  protocolID: string,
  options: { streaming?: boolean } = {},
): string {
  if (protocolID === "google.generate_content" && options.streaming) {
    return "/v1beta/models/:model:streamGenerateContent";
  }
  return protocolEntryPaths[protocolID] ?? protocolID;
}

const allProtocolIDs = alphaProtocolDescriptors.map(({ id }) => id);

const profileDefinitions: Readonly<
  Record<
    HTTPServicePresetID,
    Omit<HTTPServicePreset, "capabilities" | "sites"> & {
      sites?: readonly ServiceSite[];
      capabilityIDs: readonly string[];
      convertTo?: Readonly<Record<string, string>>;
    }
  >
> = {
  opencode_go: {
    id: "opencode_go",
    label: "OpenCode Go",
    description: "",
    defaultName: "OpenCode Go",
    kind: "opencode_go",
    baseURL: "https://opencode.ai/zen/go/v1",
    baseURLPlaceholder: "https://opencode.ai/zen/go/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "openai.chat",
      "anthropic.messages",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  opencode_zen: {
    id: "opencode_zen",
    label: "OpenCode Zen",
    description: "",
    defaultName: "OpenCode Zen",
    kind: "opencode_zen",
    baseURL: "https://opencode.ai/zen/v1",
    baseURLPlaceholder: "https://opencode.ai/zen/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "openai.chat",
      "anthropic.messages",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  kimi_coding: {
    id: "kimi_coding",
    label: "Kimi Coding",
    description: "",
    defaultName: "Kimi Coding",
    kind: "kimi_coding",
    baseURL: "https://api.kimi.ai/coding",
    baseURLPlaceholder: "https://api.kimi.ai/coding",
    // api.kimi.com is the same service under its previous domain.
    sites: [
      {
        id: "official",
        baseURL: "https://api.kimi.ai/coding",
        aliases: ["api.kimi.com"],
      },
    ],
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "anthropic.messages",
      "openai.chat",
      "openai.models",
    ],
    advancedOnStart: false,
    models: ["kimi-for-coding"],
  },
  glm_coding: {
    id: "glm_coding",
    label: "GLM Coding Plan",
    description: "",
    defaultName: "GLM Coding Plan",
    kind: "glm_coding",
    baseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
    baseURLPlaceholder: "https://open.bigmodel.cn/api/coding/paas/v4",
    sites: [
      { id: "cn", baseURL: "https://open.bigmodel.cn/api/coding/paas/v4" },
      { id: "global", baseURL: "https://api.z.ai/api/coding/paas/v4" },
    ],
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: ["openai.responses", "anthropic.messages", "openai.chat"],
    advancedOnStart: false,
    models: ["glm-5.3", "glm-5.3-flash"],
  },
  minimax_coding: {
    id: "minimax_coding",
    label: "MiniMax Coding Plan",
    description: "",
    defaultName: "MiniMax Coding Plan",
    kind: "minimax_coding",
    baseURL: "https://api.minimax.cn/v1",
    baseURLPlaceholder: "https://api.minimax.cn/v1",
    sites: [
      {
        id: "cn",
        baseURL: "https://api.minimax.cn/v1",
        aliases: ["api.minimaxi.com"],
      },
      { id: "global", baseURL: "https://api.minimax.io/v1" },
    ],
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "anthropic.messages",
      "openai.chat",
      "openai.models",
    ],
    advancedOnStart: false,
    models: ["MiniMax-M3"],
  },
  newapi: {
    id: "newapi",
    label: "New API",
    description:
      "外部网关首选。适用于 New API 生态面板，自动启用全部兼容协议。",
    defaultName: "New API",
    kind: "newapi",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: allProtocolIDs,
    advancedOnStart: false,
  },
  openai_compatible: {
    id: "openai_compatible",
    label: "OpenAI 兼容（Chat / Completions）",
    description:
      "适用于提供标准 OpenAI Chat、Completions 与 Models 接口的 API 提供商。",
    defaultName: "OpenAI 兼容 API 提供商",
    kind: "openai_compatible",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: ["openai.chat", "openai.completions", "openai.models"],
    advancedOnStart: false,
  },
  openai: {
    id: "openai",
    label: "OpenAI 官方 API",
    description: "直连 OpenAI 官方 API。",
    defaultName: "OpenAI API",
    kind: "openai",
    baseURL: "https://api.openai.com/v1",
    baseURLPlaceholder: "https://api.openai.com/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "openai.responses.compact",
      "openai.chat",
      "openai.completions",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  anthropic: {
    id: "anthropic",
    label: "Anthropic 官方 API",
    description: "直连 Anthropic Messages API。",
    defaultName: "Anthropic API",
    kind: "anthropic",
    baseURL: "https://api.anthropic.com",
    baseURLPlaceholder: "https://api.anthropic.com",
    authScheme: "anthropic_api_key",
    headerName: "",
    capabilityIDs: ["anthropic.messages", "openai.models"],
    advancedOnStart: false,
  },
  gemini: {
    id: "gemini",
    label: "Google Gemini 官方 API",
    description: "直连 Gemini Generate Content 与 Models API。",
    defaultName: "Gemini API",
    kind: "gemini",
    baseURL: "https://generativelanguage.googleapis.com",
    baseURLPlaceholder: "https://generativelanguage.googleapis.com",
    authScheme: "google_api_key",
    headerName: "",
    capabilityIDs: ["google.generate_content", "google.models", "openai.chat"],
    convertTo: { "openai.chat": "google.generate_content" },
    advancedOnStart: false,
  },
  deepseek: {
    id: "deepseek",
    label: "DeepSeek",
    description: "",
    defaultName: "DeepSeek API",
    kind: "deepseek",
    baseURL: "https://api.deepseek.com/v1",
    baseURLPlaceholder: "https://api.deepseek.com/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "anthropic.messages",
      "openai.chat",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  qwen: {
    id: "qwen",
    label: "通义千问（百炼）",
    description: "",
    defaultName: "通义千问（百炼） API",
    kind: "qwen",
    baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    baseURLPlaceholder: "https://dashscope.aliyuncs.com/compatible-mode/v1",
    sites: [
      {
        id: "cn",
        baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
      },
      {
        id: "global",
        baseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
      },
    ],
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: ["openai.responses", "anthropic.messages", "openai.chat"],
    advancedOnStart: false,
  },
  moonshot: {
    id: "moonshot",
    label: "Kimi（Moonshot）",
    description: "",
    defaultName: "Kimi（Moonshot） API",
    kind: "moonshot",
    baseURL: "https://api.moonshot.cn/v1",
    baseURLPlaceholder: "https://api.moonshot.cn/v1",
    sites: [
      { id: "cn", baseURL: "https://api.moonshot.cn/v1" },
      { id: "global", baseURL: "https://api.moonshot.ai/v1" },
    ],
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "anthropic.messages",
      "openai.chat",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  glm: {
    id: "glm",
    label: "智谱 GLM",
    description: "",
    defaultName: "智谱 GLM API",
    kind: "glm",
    baseURL: "https://open.bigmodel.cn/api/paas/v4",
    baseURLPlaceholder: "https://open.bigmodel.cn/api/paas/v4",
    sites: [
      { id: "cn", baseURL: "https://open.bigmodel.cn/api/paas/v4" },
      { id: "global", baseURL: "https://api.z.ai/api/paas/v4" },
    ],
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: ["openai.responses", "anthropic.messages", "openai.chat"],
    advancedOnStart: false,
  },
  minimax: {
    id: "minimax",
    label: "MiniMax",
    description: "",
    defaultName: "MiniMax API",
    kind: "minimax",
    baseURL: "https://api.minimax.cn/v1",
    baseURLPlaceholder: "https://api.minimax.cn/v1",
    sites: [
      {
        id: "cn",
        baseURL: "https://api.minimax.cn/v1",
        aliases: ["api.minimaxi.com"],
      },
      { id: "global", baseURL: "https://api.minimax.io/v1" },
    ],
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "anthropic.messages",
      "openai.chat",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  doubao: {
    id: "doubao",
    label: "豆包（火山方舟）",
    description: "",
    defaultName: "豆包（火山方舟） API",
    kind: "doubao",
    baseURL: "https://ark.cn-beijing.volces.com/api/v3",
    baseURLPlaceholder: "https://ark.cn-beijing.volces.com/api/v3",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: ["openai.responses", "anthropic.messages", "openai.chat"],
    advancedOnStart: false,
  },
  xai: {
    id: "xai",
    label: "xAI（Grok）",
    description: "",
    defaultName: "xAI（Grok） API",
    kind: "xai",
    baseURL: "https://api.x.ai/v1",
    baseURLPlaceholder: "https://api.x.ai/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "openai.responses.compact",
      "anthropic.messages",
      "openai.chat",
      "openai.completions",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  custom: {
    id: "custom",
    label: "自定义",
    description:
      "仅在 API 提供商不符合上述类型时使用；需要在高级配置中声明能力。",
    defaultName: "自定义 API 提供商",
    kind: "custom",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [],
    advancedOnStart: true,
  },
};

export const httpServicePresetIDs = Object.keys(
  profileDefinitions,
) as HTTPServicePresetID[];

export function protocolDescriptors(
  discovered: readonly ProtocolDescriptor[],
): ProtocolDescriptor[] {
  const byID = new Map(
    alphaProtocolDescriptors.map((protocol) => [protocol.id, protocol]),
  );
  for (const protocol of discovered) byID.set(protocol.id, protocol);
  return [...byID.values()];
}

export function protocolLabel(protocolID: string): string {
  return protocolLabels[protocolID] ?? protocolID;
}

export function httpServicePreset(
  profileID: HTTPServicePresetID,
  discovered: readonly ProtocolDescriptor[] = [],
  engine?: ConversionEngineSnapshot | null,
): HTTPServicePreset {
  const definition = profileDefinitions[profileID];
  const descriptors = new Map(
    protocolDescriptors(discovered).map((protocol) => [protocol.id, protocol]),
  );
  const capabilities = definition.capabilityIDs.map((protocol) => ({
    protocol,
    mode: "native" as const,
    streaming: descriptors.get(protocol)?.streaming ?? false,
    ...(definition.convertTo?.[protocol]
      ? { convert_to: definition.convertTo[protocol] }
      : {}),
  }));
  if (!["opencode_go", "opencode_zen", "custom"].includes(profileID)) {
    // Existing conversion entries (e.g. Gemini's Chat entry) are not upstreams.
    const upstream = definition.capabilityIDs.filter(
      (protocol) => !definition.convertTo?.[protocol],
    );
    for (const protocol of [
      "openai.responses",
      "openai.chat",
      "anthropic.messages",
      "google.generate_content",
    ]) {
      if (definition.capabilityIDs.includes(protocol)) continue;
      const target = bestConversionTarget(protocol, upstream, engine);
      if (target)
        capabilities.push({
          protocol,
          mode: "native",
          streaming: true,
          convert_to: target,
        });
    }
  }
  const {
    capabilityIDs: _ids,
    convertTo: _conversions,
    sites,
    ...preset
  } = definition;
  return localizeHttpPreset({
    ...preset,
    sites:
      sites ??
      (preset.baseURL ? [{ id: "official", baseURL: preset.baseURL }] : []),
    capabilities,
  });
}

/** The preset site an address belongs to, or null for a custom address. */
export function serviceSiteForBaseURL(
  preset: Pick<HTTPServicePreset, "sites">,
  baseURL: string,
): ServiceSiteID | null {
  let host: string;
  try {
    host = new URL(baseURL.trim()).host;
  } catch {
    return null;
  }
  return (
    preset.sites.find(
      (site) =>
        new URL(site.baseURL).host === host || site.aliases?.includes(host),
    )?.id ?? null
  );
}

function localizeHttpPreset(preset: HTTPServicePreset): HTTPServicePreset {
  switch (preset.id) {
    case "deepseek":
    case "qwen":
    case "moonshot":
    case "glm":
    case "minimax":
    case "doubao":
    case "xai":
      return {
        ...preset,
        label: i18n.t(`kind.${preset.id}`),
        defaultName: i18n.t(`kind.${preset.id}`),
        description: i18n.t(`presets.${preset.id}Description`),
      };
    case "opencode_go":
    case "opencode_zen":
    case "kimi_coding":
    case "glm_coding":
    case "minimax_coding":
      return {
        ...preset,
        description: i18n.t(`presets.${preset.id}Description`),
      };
    case "newapi":
      return {
        ...preset,
        description: i18n.t("presets.newapiDescription"),
      };
    case "openai_compatible":
      return {
        ...preset,
        label: i18n.t("presets.openaiCompatible"),
        description: i18n.t("presets.openaiCompatibleDescription"),
        defaultName: i18n.t("presets.openaiCompatibleName"),
      };
    case "openai":
      return {
        ...preset,
        label: i18n.t("presets.openaiOfficial"),
        description: i18n.t("presets.openaiOfficialDescription"),
      };
    case "anthropic":
      return {
        ...preset,
        label: i18n.t("presets.anthropicOfficial"),
        description: i18n.t("presets.anthropicOfficialDescription"),
      };
    case "gemini":
      return {
        ...preset,
        label: i18n.t("presets.geminiOfficial"),
        description: i18n.t("presets.geminiOfficialDescription"),
      };
    case "custom":
      return {
        ...preset,
        label: i18n.t("presets.custom"),
        description: i18n.t("presets.customDescription"),
        defaultName: i18n.t("presets.customName"),
      };
  }
}

export const serviceAuthLabels: Record<ServiceAuthScheme, string> = {
  get none() {
    return i18n.t("services.authNone");
  },
  get bearer() {
    return i18n.t("services.authBearer");
  },
  get anthropic_api_key() {
    return i18n.t("services.authAnthropic");
  },
  get google_api_key() {
    return i18n.t("services.authGoogle");
  },
  get custom_header() {
    return i18n.t("services.authCustomHeader");
  },
};

export function httpServicePresetLabel(profileID: HTTPServicePresetID): string {
  return httpServicePreset(profileID).label;
}

export function httpServiceKindLabel(kind: HTTPServiceKind): string {
  return (
    {
      newapi: "New API",
      openai: "OpenAI",
      anthropic: "Anthropic",
      gemini: "Gemini",
      deepseek: i18n.t("kind.deepseek"),
      qwen: i18n.t("kind.qwen"),
      moonshot: i18n.t("kind.moonshot"),
      glm: i18n.t("kind.glm"),
      minimax: i18n.t("kind.minimax"),
      doubao: i18n.t("kind.doubao"),
      xai: i18n.t("kind.xai"),
      openai_compatible: i18n.t("kind.openai_compatible"),
      custom: i18n.t("kind.custom"),
      opencode_go: "OpenCode Go",
      opencode_zen: "OpenCode Zen",
      kimi_coding: "Kimi Coding",
      glm_coding: "GLM Coding Plan",
      minimax_coding: "MiniMax Coding Plan",
    } satisfies Record<HTTPServiceKind, string>
  )[kind];
}
