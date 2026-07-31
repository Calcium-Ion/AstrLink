import type { RequestRecord } from "./request-record-model";

export interface TodayUsageSummary {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  capped: boolean;
}

export function aggregateTodayUsage(
  records: RequestRecord[],
  capped: boolean,
): TodayUsageSummary {
  let requests = 0;
  let input_tokens = 0;
  let output_tokens = 0;
  let total_tokens = 0;
  for (const record of records) {
    // List endpoints return roots only; still exclude failed roots and any
    // stray child rows so daily totals never count retry attempts.
    if (record.parent_request_id !== null) continue;
    if (record.status !== "succeeded") continue;
    requests += 1;
    if (record.usage) {
      input_tokens += record.usage.input_tokens;
      output_tokens += record.usage.output_tokens;
      total_tokens += record.usage.total_tokens;
    }
  }
  return {
    requests,
    input_tokens,
    output_tokens,
    total_tokens,
    capped,
  };
}

export function startOfTodayIso(now: Date): string {
  return new Date(now.getFullYear(), now.getMonth(), now.getDate()).toISOString();
}
