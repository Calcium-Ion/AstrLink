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
}

/** `null` when LLM Metadata lists no official model for it. */
export async function getOfficialModelLimits(
  model: string,
): Promise<OfficialModelLimits | null> {
  const value = (await call("model", undefined, { model })) as {
    context_tokens?: unknown;
    output_tokens?: unknown;
  } | null;
  if (value === null) return null;
  const tokens = (count: unknown) =>
    Number.isSafeInteger(count) && (count as number) > 0
      ? (count as number)
      : 0;
  return {
    contextTokens: tokens(value.context_tokens),
    outputTokens: tokens(value.output_tokens),
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
