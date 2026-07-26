import type {
  AuthScheme,
  EndpointAuth,
  EndpointCapability,
  EndpointCreateInput,
  EndpointPatchInput,
  EndpointRecord,
} from "./endpoint-model";
import {
  endpointPreset,
  type EndpointProfileID,
  type ProtocolDescriptor,
} from "./endpoint-presets";

export type DraftProfileID = EndpointProfileID | "current";

export interface EndpointDraft {
  name: string;
  kind: EndpointCreateInput["kind"];
  baseURL: string;
  authScheme: AuthScheme;
  headerName: string;
  secret: string;
  secretIdentity: string | null;
  removeCredential: boolean;
  enabled: boolean;
  capabilities: EndpointCapability[];
  advancedOpen: boolean;
}

export interface EndpointDraftSession {
  activeProfile: DraftProfileID;
  drafts: Partial<Record<DraftProfileID, EndpointDraft>>;
  original: EndpointRecord | null;
}

export interface DraftValidationIssue {
  message: string;
  targetID: string;
  advanced: boolean;
}

function cloneCapabilities(
  capabilities: readonly EndpointCapability[],
): EndpointCapability[] {
  return capabilities.map((capability) => ({
    ...capability,
    ...(capability.models ? { models: [...capability.models] } : {}),
  }));
}

function capabilitiesForPayload(
  capabilities: readonly EndpointCapability[],
): EndpointCapability[] {
  return capabilities.map((capability) => {
    const models = capability.models?.filter((model) => model !== "");
    return {
      protocol: capability.protocol,
      mode: capability.mode,
      streaming: capability.streaming,
      ...(models && models.length > 0 ? { models: [...models] } : {}),
    };
  });
}

function draftFromPreset(
  profileID: EndpointProfileID,
  protocols: readonly ProtocolDescriptor[],
): EndpointDraft {
  const preset = endpointPreset(profileID, protocols);
  return {
    name: preset.defaultName,
    kind: preset.kind,
    baseURL: preset.baseURL,
    authScheme: preset.authScheme,
    headerName: preset.headerName,
    secret: "",
    secretIdentity: null,
    removeCredential: false,
    enabled: true,
    capabilities: cloneCapabilities(preset.capabilities),
    advancedOpen: preset.advancedOnStart,
  };
}

export function createDraftSession(
  protocols: readonly ProtocolDescriptor[] = [],
): EndpointDraftSession {
  return {
    activeProfile: "newapi",
    drafts: { newapi: draftFromPreset("newapi", protocols) },
    original: null,
  };
}

export function editDraftSession(record: EndpointRecord): EndpointDraftSession {
  const endpoint = record.endpoint;
  return {
    activeProfile: "current",
    drafts: {
      current: {
        name: endpoint.name,
        kind: endpoint.kind,
        baseURL: endpoint.base_url,
        authScheme: endpoint.auth.scheme,
        headerName: endpoint.auth.header_name ?? "",
        secret: "",
        secretIdentity: null,
        removeCredential: false,
        enabled: endpoint.enabled,
        capabilities: cloneCapabilities(endpoint.capabilities),
        advancedOpen: false,
      },
    },
    original: record,
  };
}

export function activeDraft(session: EndpointDraftSession): EndpointDraft {
  const draft = session.drafts[session.activeProfile];
  if (!draft) throw new Error(`Missing draft for ${session.activeProfile}`);
  return draft;
}

export function updateActiveDraft(
  session: EndpointDraftSession,
  update: (draft: EndpointDraft) => EndpointDraft,
): EndpointDraftSession {
  return {
    ...session,
    drafts: {
      ...session.drafts,
      [session.activeProfile]: update(activeDraft(session)),
    },
  };
}

export function switchDraftProfile(
  session: EndpointDraftSession,
  profileID: DraftProfileID,
  protocols: readonly ProtocolDescriptor[] = [],
): EndpointDraftSession {
  if (profileID === session.activeProfile) return session;
  if (profileID === "current" && !session.drafts.current) return session;

  const existing = session.drafts[profileID];
  const draft =
    existing ??
    (profileID === "current"
      ? undefined
      : draftFromPreset(profileID, protocols));
  if (!draft) return session;

  return {
    ...session,
    activeProfile: profileID,
    drafts: { ...session.drafts, [profileID]: draft },
  };
}

export function restoreActivePreset(
  session: EndpointDraftSession,
  protocols: readonly ProtocolDescriptor[] = [],
): EndpointDraftSession {
  if (session.activeProfile === "current") return session;
  const current = activeDraft(session);
  const presetDraft = draftFromPreset(session.activeProfile, protocols);
  const restored = {
    ...presetDraft,
    name: current.name,
    enabled: current.enabled,
    advancedOpen: current.advancedOpen,
  };
  const keepSecret =
    current.secret.trim() !== "" &&
    current.secretIdentity === credentialIdentity(restored);
  return {
    ...session,
    drafts: {
      ...session.drafts,
      [session.activeProfile]: {
        ...restored,
        secret: keepSecret ? current.secret : "",
        secretIdentity: keepSecret ? current.secretIdentity : null,
      },
    },
  };
}

export function endpointAuth(draft: EndpointDraft): EndpointAuth {
  return draft.authScheme === "custom_header"
    ? { scheme: draft.authScheme, header_name: draft.headerName }
    : { scheme: draft.authScheme };
}

function authEqual(left: EndpointAuth, right: EndpointAuth): boolean {
  return (
    left.scheme === right.scheme &&
    (left.header_name ?? "") === (right.header_name ?? "")
  );
}

function capabilitiesEqual(
  left: readonly EndpointCapability[],
  right: readonly EndpointCapability[],
): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

function safeOrigin(value: string): string | null {
  try {
    return new URL(value).origin;
  } catch {
    return null;
  }
}

export function credentialIdentity(draft: EndpointDraft): string {
  return JSON.stringify([
    draft.kind,
    safeOrigin(draft.baseURL) ?? `invalid:${draft.baseURL}`,
    draft.authScheme,
    draft.authScheme === "custom_header" ? draft.headerName : "",
  ]);
}

export function bindDraftSecret(
  draft: EndpointDraft,
  secret: string,
): EndpointDraft {
  return {
    ...draft,
    secret,
    secretIdentity:
      secret.trim() === "" ? null : credentialIdentity(draft),
  };
}

export function updateDraftIdentity(
  draft: EndpointDraft,
  update: (current: EndpointDraft) => EndpointDraft,
): EndpointDraft {
  const next = update(draft);
  if (
    draft.secret.trim() !== "" &&
    draft.secretIdentity !== credentialIdentity(next)
  ) {
    return { ...next, secret: "", secretIdentity: null };
  }
  return next;
}

export function hasCurrentDraftSecret(draft: EndpointDraft): boolean {
  return (
    draft.secret.trim() !== "" &&
    draft.secretIdentity === credentialIdentity(draft)
  );
}

export function canKeepStoredCredential(
  session: EndpointDraftSession,
): boolean {
  const original = session.original?.endpoint;
  if (
    session.activeProfile !== "current" ||
    !original?.credential_ref
  ) {
    return false;
  }
  const draft = activeDraft(session);
  return (
    draft.kind === original.kind &&
    authEqual(endpointAuth(draft), original.auth) &&
    safeOrigin(draft.baseURL) !== null &&
    safeOrigin(draft.baseURL) === safeOrigin(original.base_url)
  );
}

const protocolIDPattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const headerNamePattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
const forbiddenAuthHeaders = new Set([
  "connection",
  "content-length",
  "host",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

export function validateDraftIssue(
  session: EndpointDraftSession,
): DraftValidationIssue | null {
  const draft = activeDraft(session);
  if (draft.name.trim() === "") {
    return {
      message: "请填写服务名称。",
      targetID: "endpoint-name",
      advanced: false,
    };
  }
  if ([...draft.name].length > 128) {
    return {
      message: "服务名称不能超过 128 个字符。",
      targetID: "endpoint-name",
      advanced: false,
    };
  }
  if (draft.baseURL.length > 2048) {
    return {
      message: "API 地址不能超过 2048 个字符。",
      targetID: "endpoint-base-url",
      advanced: false,
    };
  }
  if (draft.baseURL !== draft.baseURL.trim()) {
    return {
      message: "API 地址前后不能包含空格。",
      targetID: "endpoint-base-url",
      advanced: false,
    };
  }
  let parsedURL: URL;
  try {
    parsedURL = new URL(draft.baseURL);
  } catch {
    return {
      message: "请填写有效的 API 地址。",
      targetID: "endpoint-base-url",
      advanced: false,
    };
  }
  if (
    (parsedURL.protocol !== "http:" && parsedURL.protocol !== "https:") ||
    parsedURL.username !== "" ||
    parsedURL.password !== "" ||
    parsedURL.search !== "" ||
    parsedURL.hash !== ""
  ) {
    return {
      message:
        "API 地址仅支持安全的 HTTP(S) 地址，不能包含账号、查询参数或锚点。",
      targetID: "endpoint-base-url",
      advanced: false,
    };
  }
  if (
    draft.authScheme === "custom_header" &&
    (draft.headerName.length > 128 ||
      !headerNamePattern.test(draft.headerName))
  ) {
    return {
      message: "请填写有效的认证 Header 名称。",
      targetID: "endpoint-auth-header",
      advanced: true,
    };
  }
  if (
    draft.authScheme === "custom_header" &&
    forbiddenAuthHeaders.has(draft.headerName.toLowerCase())
  ) {
    return {
      message: `认证 Header 不能使用保留名称 ${draft.headerName}。`,
      targetID: "endpoint-auth-header",
      advanced: true,
    };
  }
  if (draft.capabilities.length === 0) {
    return {
      message: "这个服务还没有协议能力，请在高级配置中至少添加一项。",
      targetID: "endpoint-add-capability",
      advanced: true,
    };
  }
  const capabilityKeys = new Set<string>();
  for (const [index, capability] of draft.capabilities.entries()) {
    if (
      capability.protocol.length < 3 ||
      capability.protocol.length > 96 ||
      !protocolIDPattern.test(capability.protocol)
    ) {
      return {
        message: `协议 ID“${capability.protocol || "空"}”格式无效。`,
        targetID: `endpoint-capability-${index}-protocol`,
        advanced: true,
      };
    }
    const key = `${capability.protocol}\u0000${capability.mode}`;
    if (capabilityKeys.has(key)) {
      return {
        message: `协议 ${capability.protocol} 的 ${capability.mode} 模式重复。`,
        targetID: `endpoint-capability-${index}-protocol`,
        advanced: true,
      };
    }
    capabilityKeys.add(key);
    const seenModels = new Set<string>();
    for (const [modelIndex, model] of (capability.models ?? []).entries()) {
      if (model === "") continue;
      if ([...model].length > 256 || seenModels.has(model)) {
        return {
          message: `协议 ${capability.protocol} 的模型列表包含过长或重复项。`,
          targetID: `endpoint-capability-${index}-model-${modelIndex}`,
          advanced: true,
        };
      }
      seenModels.add(model);
    }
  }
  if (
    draft.authScheme !== "none" &&
    draft.secret.trim() !== "" &&
    !hasCurrentDraftSecret(draft)
  ) {
    return {
      message: "服务地址或认证方式已改变，请重新填写 API Key。",
      targetID: "endpoint-secret",
      advanced: false,
    };
  }
  if (
    draft.authScheme !== "none" &&
    draft.secret.trim() === "" &&
    !canKeepStoredCredential(session)
  ) {
    return {
      message: session.original
        ? "服务地址或认证方式已改变，请重新填写 API Key。"
        : "请填写 API Key。",
      targetID: "endpoint-secret",
      advanced: false,
    };
  }
  return null;
}

export function validateDraft(session: EndpointDraftSession): string | null {
  return validateDraftIssue(session)?.message ?? null;
}

export function buildCreateInput(
  session: EndpointDraftSession,
): EndpointCreateInput {
  const draft = activeDraft(session);
  return {
    name: draft.name.trim(),
    kind: draft.kind,
    base_url: draft.baseURL.trim(),
    auth: endpointAuth(draft),
    enabled: draft.enabled,
    capabilities: capabilitiesForPayload(draft.capabilities),
    ...(draft.authScheme !== "none" && hasCurrentDraftSecret(draft)
      ? { credential: { secret: draft.secret } }
      : {}),
  };
}

export function buildPatch(session: EndpointDraftSession): EndpointPatchInput {
  const original = session.original?.endpoint;
  if (!original) throw new Error("Cannot build an update patch without an original endpoint");
  const draft = activeDraft(session);
  const auth = endpointAuth(draft);
  const capabilities = capabilitiesForPayload(draft.capabilities);
  const patch: EndpointPatchInput = {};
  const name = draft.name;
  const baseURL = draft.baseURL;

  if (name !== original.name) patch.name = name;
  if (draft.kind !== original.kind) patch.kind = draft.kind;
  if (baseURL !== original.base_url) patch.base_url = baseURL;
  if (!authEqual(auth, original.auth)) patch.auth = auth;
  if (draft.enabled !== original.enabled) patch.enabled = draft.enabled;
  if (!capabilitiesEqual(capabilities, original.capabilities)) {
    patch.capabilities = capabilities;
  }
  if (draft.removeCredential && original.credential_ref) {
    patch.credential = null;
  } else if (
    draft.authScheme !== "none" &&
    hasCurrentDraftSecret(draft)
  ) {
    patch.credential = { secret: draft.secret };
  }
  return patch;
}
