import { invoke } from "@tauri-apps/api/core";
import { parseBillingSummary, parseServiceBilling } from "./pricing-model";
async function call(
  operation: string,
  serviceId?: string,
  input?: unknown,
): Promise<unknown> {
  if (!("__TAURI_INTERNALS__" in window))
    throw new Error("Native gateway unavailable");
  return invoke("pricing", { operation, serviceId, input });
}
export async function getServiceBilling(id: string) {
  return parseServiceBilling(await call("report", id));
}
export async function getBillingSummary(from: string, to: string) {
  return parseBillingSummary(await call("summary", undefined, { from, to }));
}
