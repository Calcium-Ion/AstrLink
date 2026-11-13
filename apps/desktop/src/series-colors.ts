import { modelVendor } from "@/components/ModelBrandIcon";
import { serviceKindMark } from "@/components/ServiceKindIcon";
import type { Vendor } from "@/components/vendor-marks";

import type { ClientType } from "./request-record-model";
import type { ServiceKind } from "./service-model";

/**
 * Chart colors that follow the client or provider, never its rank, so the
 * same client keeps its color in every range and in every chart that draws
 * it. The values are the `--series-*` tokens, which hold separate light and
 * dark steps.
 */
export const SERIES_SLOTS = 8;

const SERIES = Array.from(
  { length: SERIES_SLOTS },
  (_, index) => `var(--series-${index + 1})`,
);

/**
 * Model colors. Only five, so that any two stay easy to tell apart on a
 * chart that has no room for brand marks; the `--model-series-*` tokens.
 */
const MODEL_SERIES = Array.from(
  { length: 5 },
  (_, index) => `var(--model-series-${index + 1})`,
);

/** Calls whose model or client is not known, and the folded rest. */
export const MUTED_SERIES_COLOR = "var(--muted-foreground)";

// The model color nearest each family's brand: orange, green, blue, magenta
// and gold. Grok's mark has no hue of its own; it takes gold, which none of
// the families it usually sits beside use.
const MODEL_VENDOR_SLOTS: Partial<Record<Vendor, number>> = {
  claude: 0,
  anthropic: 0,
  mistral: 0,
  xiaomi: 0,
  openai: 1,
  deepseek: 2,
  zhipu: 2,
  meta: 2,
  doubao: 2,
  bytedance: 2,
  hunyuan: 2,
  wenxin: 2,
  stepfun: 2,
  gemini: 3,
  gemma: 3,
  qwen: 3,
  minimax: 3,
  grok: 4,
  kimi: 4,
};

// The slot nearest each provider family's brand color. Families that share a
// hue share a slot, so a provider reads as its family at a glance.
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
function hashedSlot(name: string, slots = SERIES.length): number {
  let hash = 0;
  for (const char of name) hash = (hash * 31 + char.codePointAt(0)!) >>> 0;
  return hash % slots;
}

/** A model's family, and the model color it asks for. */
function modelFamily(model: string): { key: string; slot: number } {
  const name = model.trim();
  const vendor = modelVendor(name);
  const slot = vendor ? MODEL_VENDOR_SLOTS[vendor] : undefined;
  if (vendor && slot !== undefined) return { key: vendor, slot };
  const key = name.toLowerCase();
  return { key, slot: hashedSlot(key, MODEL_SERIES.length) };
}

/**
 * The colors of models drawn together, busiest first. Models of one family
 * share their family's color. When a busier family already holds it, a
 * family takes the next free one, so two families on one chart never share
 * a color while one is left.
 */
export function modelSeriesColors(models: readonly string[]): string[] {
  const families = models.map(modelFamily);
  const slots = new Map<string, number>();
  const taken = new Set<number>();
  // Every family that can keep its own color does, so one that has to move
  // takes a free color rather than a quieter family's own.
  for (const { key, slot } of families) {
    if (slots.has(key) || taken.has(slot)) continue;
    slots.set(key, slot);
    taken.add(slot);
  }
  for (const { key, slot: wanted } of families) {
    if (slots.has(key)) continue;
    let slot = wanted;
    for (let step = 0; step < MODEL_SERIES.length && taken.has(slot); step += 1)
      slot = (slot + 1) % MODEL_SERIES.length;
    slots.set(key, slot);
    taken.add(slot);
  }
  return families.map(({ key }) => MODEL_SERIES[slots.get(key)!]);
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

/** The color of a detected client; muted for unknown and historical calls. */
export function clientSeriesColor(client: ClientType | null): string {
  const slot = clientSeriesSlot(client);
  return slot === null ? MUTED_SERIES_COLOR : SERIES[slot];
}
