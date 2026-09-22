export type ProxyMode = "inherit" | "direct" | "custom";
export interface ServiceProxy {
  mode: ProxyMode;
  url?: string;
  credential_ref?: string;
}
export interface ServiceProxyInput {
  mode: ProxyMode;
  url?: string;
  credential?: { username: string; password: string } | null;
}
export interface ProxyDraft {
  mode: ProxyMode;
  url: string;
  username: string;
  password: string;
  removeCredential: boolean;
}

export function proxyDraft(proxy?: ServiceProxy): ProxyDraft {
  return { mode: proxy?.mode ?? "inherit", url: proxy?.url ?? "", username: "", password: "", removeCredential: false };
}

export function validProxyURL(value: string): boolean {
  try {
    const u = new URL(value);
    return value.trim() === value && !/[@?#\u0000-\u0020]/.test(value) && value.length <= 2048 && ["http:", "https:", "socks5:"].includes(u.protocol) && !!u.hostname &&
      !u.username && !u.password && !u.search && !u.hash && (u.pathname === "" || u.pathname === "/") &&
      !value.includes("?") && !value.includes("#") && !value.endsWith(":") &&
      (!u.port || (Number(u.port) > 0 && Number(u.port) <= 65535));
  } catch { return false; }
}

export function validProxyDraft(draft: ProxyDraft): boolean {
  if (draft.mode !== "custom") return true;
  return validProxyURL(draft.url.trim()) && (draft.removeCredential || (
    new TextEncoder().encode(draft.username).length <= 255 && new TextEncoder().encode(draft.password).length <= 255 &&
    !/[:\r\n\0]/.test(draft.username) && !/[\r\n\0]/.test(draft.password) && (!draft.password || !!draft.username)
  ));
}

export function proxyInput(draft: ProxyDraft): ServiceProxyInput | null {
  if (draft.mode === "inherit") return null;
  if (draft.mode === "direct") return { mode: "direct" };
  return { mode: "custom", url: draft.url.trim(), ...(draft.removeCredential ? { credential: null } :
    draft.username ? { credential: { username: draft.username, password: draft.password } } : {}) };
}

export function parseServiceProxy(value: unknown, serviceID: string): ServiceProxy {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid service proxy");
  const p = value as Record<string, unknown>;
  if (Object.keys(p).some(k => !["mode", "url", "credential_ref"].includes(k)) ||
    !["inherit", "direct", "custom"].includes(p.mode as string)) throw new Error("Invalid service proxy");
  if (p.mode === "custom") {
    if (typeof p.url !== "string" || !validProxyURL(p.url)) throw new Error("Invalid proxy URL");
    if (p.credential_ref !== undefined && p.credential_ref !== `local://service-proxy/${serviceID}`) throw new Error("Invalid proxy credential reference");
  } else if (p.url !== undefined || p.credential_ref !== undefined) throw new Error("Unexpected proxy fields");
  return p as unknown as ServiceProxy;
}
