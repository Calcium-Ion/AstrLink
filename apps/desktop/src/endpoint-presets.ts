import type {
  AuthScheme,
  EndpointCapability,
  EndpointKind,
} from "./endpoint-model";

export type EndpointProfileID =
  | "newapi"
  | "subscription_openai"
  | "subscription_anthropic"
  | "subscription_gemini"
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

export interface EndpointPreset {
  id: EndpointProfileID;
  label: string;
  description: string;
  defaultName: string;
  kind: EndpointKind;
  baseURL: string;
  baseURLPlaceholder: string;
  authScheme: AuthScheme;
  headerName: string;
  capabilities: EndpointCapability[];
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
    EndpointProfileID,
    Omit<EndpointPreset, "capabilities"> & {
      capabilityIDs: readonly string[];
      capabilityMode: EndpointCapability["mode"];
    }
  >
> = {
  newapi: {
    id: "newapi",
    label: "new-api",
    description: "推荐。适用于 new-api 生态面板，自动启用全部兼容协议。",
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
  subscription_openai: {
    id: "subscription_openai",
    label: "OpenAI / Codex 订阅",
    description:
      "适用于 Sub2API 等平台提供的 OpenAI 或 Codex 订阅，自动启用 Responses、Chat 与模型列表。",
    defaultName: "Codex 订阅",
    kind: "custom",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: ["openai.responses", "openai.chat", "openai.models"],
    capabilityMode: "delegated",
    advancedOnStart: false,
  },
  subscription_anthropic: {
    id: "subscription_anthropic",
    label: "Claude 订阅",
    description:
      "适用于 Claude Code 或 Anthropic 订阅，自动启用 Messages 与模型列表。",
    defaultName: "Claude 订阅",
    kind: "custom",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: ["anthropic.messages", "openai.models"],
    capabilityMode: "delegated",
    advancedOnStart: false,
  },
  subscription_gemini: {
    id: "subscription_gemini",
    label: "Gemini 订阅",
    description:
      "适用于 Gemini CLI 或 Gemini API 订阅，自动启用 Gemini 原生生成与模型列表。",
    defaultName: "Gemini 订阅",
    kind: "custom",
    baseURL: "",
    baseURLPlaceholder: "https://api.example.com",
    authScheme: "bearer",
    headerName: "",
    capabilityIDs: ["google.generate_content", "google.models"],
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
    label: "OpenAI 官方",
    description: "直连 OpenAI 官方 API。",
    defaultName: "OpenAI",
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
    label: "Anthropic 官方",
    description: "直连 Anthropic Messages API。",
    defaultName: "Anthropic",
    kind: "anthropic",
    baseURL: "https://api.anthropic.com",
    baseURLPlaceholder: "https://api.anthropic.com",
    authScheme: "anthropic_api_key",
    headerName: "",
    capabilityIDs: ["anthropic.messages"],
    capabilityMode: "native",
    advancedOnStart: false,
  },
  gemini: {
    id: "gemini",
    label: "Google Gemini 官方",
    description: "直连 Gemini Generate Content 与 Models API。",
    defaultName: "Gemini",
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

export const endpointProfileIDs = Object.keys(
  profileDefinitions,
) as EndpointProfileID[];

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

export function endpointPreset(
  profileID: EndpointProfileID,
  discovered: readonly ProtocolDescriptor[] = [],
): EndpointPreset {
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

export function endpointProfileLabel(profileID: EndpointProfileID): string {
  return profileDefinitions[profileID].label;
}

export function endpointKindLabel(kind: EndpointKind): string {
  return (
    {
      newapi: "new-api",
      openai: "OpenAI",
      anthropic: "Anthropic",
      gemini: "Gemini",
      openai_compatible: "OpenAI 兼容",
      custom: "自定义 / 订阅",
    } satisfies Record<EndpointKind, string>
  )[kind];
}
