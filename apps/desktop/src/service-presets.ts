import { i18n } from "./i18n";
import type {
  HTTPServiceKind,
  ServiceAuthScheme,
  ServiceCapability,
} from "./service-model";

export type HTTPServicePresetID =
  | "newapi"
  | "openai_compatible"
  | "openai"
  | "anthropic"
  | "gemini"
  | "custom";

export interface ProtocolDescriptor {
  id: string;
  phase: "alpha" | "post_alpha";
  primary: boolean;
  streaming: boolean;
}

export interface HTTPServicePreset {
  id: HTTPServicePresetID;
  label: string;
  description: string;
  defaultName: string;
  kind: HTTPServiceKind;
  baseURL: string;
  baseURLPlaceholder: string;
  authScheme: ServiceAuthScheme;
  headerName: string;
  capabilities: ServiceCapability[];
  advancedOnStart: boolean;
}

export const localConversionPassthrough = "none";

export type ConversionQuality = "good" | "fair" | "discouraged";

export interface ConversionTarget {
  id: string;
  enabled: boolean;
  quality: ConversionQuality | null;
  streaming: boolean;
}

interface ConversionEngineSnapshot {
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
    Omit<HTTPServicePreset, "capabilities"> & {
      capabilityIDs: readonly string[];
    }
  >
> = {
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
    description: "适用于提供标准 OpenAI Chat、Completions 与 Models 接口的服务。",
    defaultName: "OpenAI 兼容服务",
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
    capabilityIDs: ["google.generate_content", "google.models"],
    advancedOnStart: false,
  },
  custom: {
    id: "custom",
    label: "自定义",
    description: "仅在服务不符合上述类型时使用；需要在高级配置中声明能力。",
    defaultName: "自定义服务",
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
): HTTPServicePreset {
  const definition = profileDefinitions[profileID];
  const descriptors = new Map(
    protocolDescriptors(discovered).map((protocol) => [protocol.id, protocol]),
  );
  const capabilities = definition.capabilityIDs.map((protocol) => ({
    protocol,
    mode: "native" as const,
    streaming: descriptors.get(protocol)?.streaming ?? false,
  }));
  const { capabilityIDs: _ids, ...preset } = definition;
  return localizeHttpPreset({
    ...preset,
    capabilities,
  });
}

function localizeHttpPreset(preset: HTTPServicePreset): HTTPServicePreset {
  switch (preset.id) {
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

export function httpServicePresetLabel(
  profileID: HTTPServicePresetID,
): string {
  return httpServicePreset(profileID).label;
}

export function httpServiceKindLabel(kind: HTTPServiceKind): string {
  return (
    {
      newapi: "New API",
      openai: "OpenAI",
      anthropic: "Anthropic",
      gemini: "Gemini",
      openai_compatible: i18n.t("kind.openai_compatible"),
      custom: i18n.t("kind.custom"),
    } satisfies Record<HTTPServiceKind, string>
  )[kind];
}
