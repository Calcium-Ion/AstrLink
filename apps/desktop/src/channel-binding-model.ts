export interface ChannelBindingScope {
  session_id: string;
  local_access_token_id: string;
  protocol: string;
  model: string;
}

export interface ChannelBinding extends ChannelBindingScope {
  service_id: string;
  source: string;
  request_id: string;
  updated_at: string;
  expires_at: string;
}

export interface ChannelBindingEvent extends ChannelBindingScope {
  id: number;
  at: string;
  action: "hit" | "miss" | "strict" | "bound" | "switched" | "released";
  reason: string;
  source: string;
  service_id: string;
  previous_service_id: string;
  request_id: string;
}

export interface ChannelBindingAudit {
  enabled: boolean;
  bindings: ChannelBinding[];
  events: ChannelBindingEvent[];
  has_more: boolean;
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw Error("Invalid API provider binding document");
  return value as Record<string, unknown>;
}
function text(value: unknown): string {
  if (typeof value !== "string") throw Error("Invalid API provider binding field");
  return value;
}
function date(value: unknown): string {
  const raw = text(value);
  if (!Number.isFinite(Date.parse(raw))) throw Error("Invalid API provider binding time");
  return raw;
}
function scope(value: Record<string, unknown>): ChannelBindingScope {
  return { session_id: text(value.session_id), local_access_token_id: text(value.local_access_token_id), protocol: text(value.protocol), model: text(value.model) };
}

export function parseChannelBindingAudit(value: unknown): ChannelBindingAudit {
  const data = object(value);
  if (typeof data.enabled !== "boolean" || typeof data.has_more !== "boolean" || !Array.isArray(data.bindings) || !Array.isArray(data.events)) throw Error("Invalid API provider binding audit");
  return {
    enabled: data.enabled, has_more: data.has_more,
    bindings: data.bindings.map((raw) => {
      const item = object(raw);
      return { ...scope(item), service_id: text(item.service_id), source: text(item.source), request_id: text(item.request_id), updated_at: date(item.updated_at), expires_at: date(item.expires_at) };
    }),
    events: data.events.map((raw) => {
      const item = object(raw);
      if (!Number.isSafeInteger(item.id) || (item.id as number) < 1 || !["hit", "miss", "strict", "bound", "switched", "released"].includes(text(item.action))) throw Error("Invalid API provider binding event");
      return { ...scope(item), id: item.id as number, action: item.action as ChannelBindingEvent["action"], at: date(item.at), reason: text(item.reason), source: text(item.source), service_id: text(item.service_id), previous_service_id: text(item.previous_service_id), request_id: text(item.request_id) };
    }),
  };
}
