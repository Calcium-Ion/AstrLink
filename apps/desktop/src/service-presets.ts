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

const allProtocolIDs = alphaProtocolDescriptors.map(({ id }) => id);

const profileDefinitions: Readonly<
  Record<
    HTTPServicePresetID,
    Omit<HTTPServicePreset, "capabilities"> & {
      capabilityIDs: readonly string[];
      capabilityMode: ServiceCapability["mode"];
    }
  >
> = {
  newapi: {
    id: "newapi",
    label: "new-api",
    description:
      "外部网关首选。适用于 new-api 生态面板，自动启用全部兼容协议。",
    defaultName: "new-api",
    kind: "newapi",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: allProtocolIDs,
    capabilityMode: "delegated",
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
    capabilityMode: "native",
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
    capabilityMode: "native",
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
    capabilityMode: "native",
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
    capabilityMode: "native",
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
    capabilityMode: "native",
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
    mode: definition.capabilityMode,
    streaming: descriptors.get(protocol)?.streaming ?? false,
  }));
  const { capabilityIDs: _ids, capabilityMode: _mode, ...preset } = definition;
  return {
    ...preset,
    capabilities,
  };
}

export function httpServicePresetLabel(
  profileID: HTTPServicePresetID,
): string {
  return profileDefinitions[profileID].label;
}

export function httpServiceKindLabel(kind: HTTPServiceKind): string {
  return (
    {
      newapi: "new-api",
      openai: "OpenAI",
      anthropic: "Anthropic",
      gemini: "Gemini",
      openai_compatible: "OpenAI 兼容",
      custom: "自定义 API",
    } satisfies Record<HTTPServiceKind, string>
  )[kind];
}
