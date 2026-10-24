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
  "vertex",
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

/**
 * Where a preset account lives; "official" marks a single-site vendor and
 * "local" a gateway running on this computer.
 */
export type ServiceSiteID = "cn" | "global" | "official" | "local";

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
  {
    id: "openai.embeddings",
    phase: "alpha",
    primary: false,
    streaming: false,
  },
  {
    id: "google.embeddings",
    phase: "alpha",
    primary: false,
    streaming: false,
  },
  { id: "rerank", phase: "alpha", primary: false, streaming: false },
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
  "openai.embeddings": "OpenAI Embeddings",
  "google.embeddings": "Gemini Embeddings",
  rerank: "Rerank",
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
  "openai.embeddings": "/v1/embeddings",
  "google.embeddings": "/v1beta/models/:model:embedContent",
  rerank: "/v1/rerank",
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
    Omit<
      HTTPServicePreset,
      "capabilities" | "sites" | "label" | "description" | "defaultName"
    > & {
      sites?: readonly ServiceSite[];
      capabilityIDs: readonly string[];
      convertTo?: Readonly<Record<string, string>>;
    }
  >
> = {
  opencode_go: {
    id: "opencode_go",
    kind: "opencode_go",
    baseURL: "https://opencode.ai/inference/go/v1",
    baseURLPlaceholder: "https://opencode.ai/inference/go/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "openai.chat",
      "anthropic.messages",
      "google.generate_content",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  opencode_zen: {
    id: "opencode_zen",
    kind: "opencode_zen",
    baseURL: "https://opencode.ai/inference/v1",
    baseURLPlaceholder: "https://opencode.ai/inference/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.responses",
      "openai.chat",
      "anthropic.messages",
      "google.generate_content",
      "openai.models",
    ],
    advancedOnStart: false,
  },
  kimi_coding: {
    id: "kimi_coding",
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
    kind: "newapi",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: allProtocolIDs,
    advancedOnStart: false,
  },
  magpie: {
    id: "magpie",
    kind: "magpie",
    baseURL: "http://127.0.0.1:3425",
    baseURLPlaceholder: "http://127.0.0.1:3425",
    sites: [{ id: "local", baseURL: "http://127.0.0.1:3425" }],
    authScheme: "bearer",
    headerName: "",
    // Magpie documents Chat, Responses, Messages, Gemini and both catalogs;
    // Responses Compact and legacy Completions are not on its surface.
    capabilityIDs: [
      "openai.responses",
      "anthropic.messages",
      "google.generate_content",
      "openai.chat",
      "openai.models",
      "google.models",
    ],
    advancedOnStart: false,
  },
  openai_compatible: {
    id: "openai_compatible",
    kind: "openai_compatible",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com/v1",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: [
      "openai.chat",
      "openai.completions",
      "openai.models",
      "openai.embeddings",
    ],
    advancedOnStart: false,
  },
  openai: {
    id: "openai",
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
      "openai.embeddings",
    ],
    advancedOnStart: false,
  },
  anthropic: {
    id: "anthropic",
    kind: "anthropic",
    baseURL: "https://api.anthropic.com",
    baseURLPlaceholder: "https://api.anthropic.com",
    authScheme: "anthropic_api_key",
    headerName: "",
    capabilityIDs: ["anthropic.messages", "openai.models"],
    advancedOnStart: false,
  },
  vertex: {
    id: "vertex",
    kind: "vertex",
    baseURL: "https://aiplatform.googleapis.com",
    baseURLPlaceholder: "https://aiplatform.googleapis.com",
    authScheme: "google_service_account",
    headerName: "",
    capabilityIDs: [
      "anthropic.messages",
      "google.generate_content",
      "openai.chat",
      "openai.models",
      "google.models",
    ],
    advancedOnStart: false,
  },
  gemini: {
    id: "gemini",
    kind: "gemini",
    baseURL: "https://generativelanguage.googleapis.com",
    baseURLPlaceholder: "https://generativelanguage.googleapis.com",
    authScheme: "google_api_key",
    headerName: "",
    capabilityIDs: [
      "google.generate_content",
      "google.models",
      "google.embeddings",
      "openai.chat",
    ],
    convertTo: { "openai.chat": "google.generate_content" },
    advancedOnStart: false,
  },
  deepseek: {
    id: "deepseek",
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
  if (
    profileID === "vertex" &&
    engine?.available &&
    engine.edges.some(
      (edge) =>
        edge.from === "openai.responses" &&
        edge.to === "google.generate_content" &&
        edge.streaming,
    )
  ) {
    capabilities.unshift({
      protocol: "openai.responses",
      mode: "native",
      streaming: true,
      convert_to: "google.generate_content",
    });
  }
  if (
    !["opencode_go", "opencode_zen", "vertex", "custom"].includes(profileID)
  ) {
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
  return {
    ...preset,
    ...presetText(profileID),
    sites:
      sites ??
      (preset.baseURL ? [{ id: "official", baseURL: preset.baseURL }] : []),
    capabilities,
  };
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

function presetText(
  id: HTTPServicePresetID,
): Pick<HTTPServicePreset, "label" | "description" | "defaultName"> {
  switch (id) {
    case "openai_compatible":
      return {
        label: i18n.t("presets.openaiCompatible"),
        description: i18n.t("presets.openaiCompatibleDescription"),
        defaultName: i18n.t("presets.openaiCompatibleName"),
      };
    case "custom":
      return {
        label: i18n.t("presets.custom"),
        description: i18n.t("presets.customDescription"),
        defaultName: i18n.t("presets.customName"),
      };
    case "openai":
      return {
        label: i18n.t("presets.openaiOfficial"),
        description: i18n.t("presets.openaiOfficialDescription"),
        defaultName: "OpenAI API",
      };
    case "anthropic":
      return {
        label: i18n.t("presets.anthropicOfficial"),
        description: i18n.t("presets.anthropicOfficialDescription"),
        defaultName: "Anthropic API",
      };
    case "gemini":
      return {
        label: i18n.t("presets.geminiOfficial"),
        description: i18n.t("presets.geminiOfficialDescription"),
        defaultName: "Gemini API",
      };
    default: {
      const label = httpServiceKindLabel(id);
      return {
        label,
        description: i18n.t(`presets.${id}Description`),
        defaultName: label,
      };
    }
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
  get google_service_account() {
    return i18n.t("services.vertexCredential");
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
      magpie: "Magpie",
      openai: "OpenAI",
      anthropic: "Anthropic",
      gemini: "Gemini",
      vertex: "Vertex AI",
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
