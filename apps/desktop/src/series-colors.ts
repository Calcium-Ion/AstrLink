import { modelVendor } from "@/components/ModelBrandIcon";
import { serviceKindMark } from "@/components/ServiceKindIcon";
import type { Vendor } from "@/components/vendor-marks";

import type { ClientType } from "./request-record-model";
import type { ServiceKind } from "./service-model";

/**
 * Chart colors that follow the model or client, never its rank, so the same
 * model keeps its color in every range and in every chart that draws it. The
 * values are the `--series-*` tokens, which hold separate light and dark
 * steps.
 */
export const SERIES_SLOTS = 8;

const SERIES = Array.from(
  { length: SERIES_SLOTS },
  (_, index) => `var(--series-${index + 1})`,
);

/** Calls whose model or client is not known, and the folded rest. */
export const MUTED_SERIES_COLOR = "var(--muted-foreground)";

// The slot nearest each family's brand color. Families that share a hue
// share a slot, so a model reads as its family at a glance.
const VENDOR_SLOTS: Partial<Record<Vendor, number>> = {
  zhipu: 0,
  meta: 0,
  doubao: 0,
  bytedance: 0,
  hunyuan: 0,
  wenxin: 0,
  stepfun: 0,
  deepseek: 0,
  claude: 1,
  anthropic: 1,
  mistral: 1,
  xiaomi: 1,
  openai: 2,
  kimi: 3,
  minimax: 4,
  gemini: 6,
  gemma: 6,
  qwen: 6,
  grok: 7,
};

// Codex takes violet. Cursor's mark has no hue of its own; it takes red so it
// shares neither Codex's violet nor DeepSeek Harness's blue.
const CLIENT_SLOTS: Partial<Record<ClientType, number>> = {
  deepseek_harness: 0,
  claude_code: 1,
  codex: 6,
  cursor: 7,
  kimi_code: 3,
  pi: 4,
  opencode: 5,
  gemini_cli: 2,
  qwen_code: 6,
  grok_cli: 7,
};

/** A stable slot for a name without a brand slot. */
function hashedSlot(name: string): number {
  let hash = 0;
  for (const char of name) hash = (hash * 31 + char.codePointAt(0)!) >>> 0;
  return hash % SERIES.length;
}

/** The series slot, 0–7, of a model; null when it is unknown. */
export function modelSeriesSlot(model: string | null | undefined) {
  const name = model?.trim();
  if (!name) return null;
  const vendor = modelVendor(name);
  const slot = vendor ? VENDOR_SLOTS[vendor] : undefined;
  return slot ?? hashedSlot(name.toLowerCase());
}

/** The series slot of a detected client; null for unknown and historical calls. */
export function clientSeriesSlot(client: ClientType | null) {
  if (!client || client === "unknown") return null;
  return CLIENT_SLOTS[client] ?? hashedSlot(client);
}

/** The series slot of a provider: its vendor's, or one hashed from its id. */
export function serviceSeriesSlot(kind: ServiceKind, id: string): number {
  const mark = serviceKindMark(kind);
  const slot =
    mark === "codex"
      ? VENDOR_SLOTS.openai
      : mark in VENDOR_SLOTS
        ? VENDOR_SLOTS[mark as Vendor]
        : undefined;
  return slot ?? hashedSlot(id);
}

/** The color of a model in every usage chart; muted when it is unknown. */
export function modelSeriesColor(model: string | null | undefined): string {
  const slot = modelSeriesSlot(model);
  return slot === null ? MUTED_SERIES_COLOR : SERIES[slot];
}

/** The color of a detected client; muted for unknown and historical calls. */
export function clientSeriesColor(client: ClientType | null): string {
  const slot = clientSeriesSlot(client);
  return slot === null ? MUTED_SERIES_COLOR : SERIES[slot];
}
