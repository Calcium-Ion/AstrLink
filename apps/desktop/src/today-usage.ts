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
  let input_tokens = 0;
  let output_tokens = 0;
  let total_tokens = 0;
  for (const record of records) {
    if (record.usage) {
      input_tokens += record.usage.input_tokens;
      output_tokens += record.usage.output_tokens;
      total_tokens += record.usage.total_tokens;
    }
  }
  return {
    requests: records.length,
    input_tokens,
    output_tokens,
    total_tokens,
    capped,
  };
}

export function startOfTodayIso(now: Date): string {
  return new Date(now.getFullYear(), now.getMonth(), now.getDate()).toISOString();
}
