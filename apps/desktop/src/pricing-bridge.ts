import { isWebEdition } from "./edition";
import { webInvoke } from "./web-transport";
import { invoke } from "@tauri-apps/api/core";
import {
  parseBillingSummary,
  parseQuotaEstimates,
  parseServiceBilling,
} from "./pricing-model";
async function call(
  operation: string,
  serviceId?: string,
  input?: unknown,
): Promise<unknown> {
  if (isWebEdition)
    return webInvoke("pricing", { operation, serviceId, input });
  if (!("__TAURI_INTERNALS__" in window))
    throw new Error("Native gateway unavailable");
  return invoke("pricing", { operation, serviceId, input });
}
/** The limits LLM Metadata lists for the official model a gateway model reaches. */
export interface OfficialModelLimits {
  contextTokens: number;
  outputTokens: number;
  /**
   * The reasoning levels the model takes, such as `low` and `high`; absent
   * when the model does not reason or the catalog names none.
   */
  reasoningLevels?: string[];
}

/** `null` when LLM Metadata lists no official model for it. */
export async function getOfficialModelLimits(
  model: string,
): Promise<OfficialModelLimits | null> {
  const value = (await call("model", undefined, { model })) as {
    context_tokens?: unknown;
    output_tokens?: unknown;
    reasoning?: unknown;
    reasoning_levels?: unknown;
  } | null;
  if (value === null) return null;
  const tokens = (count: unknown) =>
    Number.isSafeInteger(count) && (count as number) > 0
      ? (count as number)
      : 0;
  const levels =
    value.reasoning === true && Array.isArray(value.reasoning_levels)
      ? value.reasoning_levels.filter(
          (level): level is string =>
            typeof level === "string" && /^[a-z]{1,32}$/.test(level),
        )
      : [];
  return {
    contextTokens: tokens(value.context_tokens),
    outputTokens: tokens(value.output_tokens),
    ...(levels.length > 0 ? { reasoningLevels: levels } : {}),
  };
}

export async function getServiceBilling(id: string) {
  return parseServiceBilling(await call("report", id));
}
export async function getBillingSummary(from: string, to: string) {
  return parseBillingSummary(await call("summary", undefined, { from, to }));
}
/** Estimated whole quota windows of every subscription account. */
export async function getQuotaEstimates() {
  return parseQuotaEstimates(await call("estimates"));
}
