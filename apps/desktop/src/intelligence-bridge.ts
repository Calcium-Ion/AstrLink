import { invoke } from "@tauri-apps/api/core";

export interface IntelligenceQuestion {
  id: string;
  name: string;
  kind: "number" | "text" | "svg";
  prompt: string;
  answer: string;
  model: string;
  reference_png?: string;
}
export interface IntelligenceSettings {
  default_model: string;
  judge_service_id: string;
  judge_model: string;
  timeout_seconds: number;
  questions: IntelligenceQuestion[];
  default_question_ids: string[];
}
export interface IntelligenceChannel {
  use_default: boolean;
  model: string;
  question_ids: string[];
}
export interface IntelligenceItem {
  question: IntelligenceQuestion;
  model: string;
  status: string;
  output: string;
  reason: string;
  png?: string;
  duration_ms: number;
  started_at?: string;
  judge_output?: string;
}
export interface IntelligenceRun {
  id: string;
  service_id: string;
  started_at: string;
  status: string;
  items: IntelligenceItem[];
  judge_service_id: string;
  judge_model: string;
}
export async function intelligence<T>(
  operation: string,
  serviceId?: string,
  input?: unknown,
  runId?: string,
): Promise<T> {
  try {
    return await invoke<T>("intelligence", {
      operation,
      serviceId,
      runId,
      input,
    });
  } catch (error) {
    const text = error instanceof Error ? error.message : String(error);
    const start = text.indexOf('{"error":');
    if (start >= 0) {
      let detail: unknown;
      try {
        detail = JSON.parse(text.slice(start));
      } catch {
        /* Preserve transport errors below. */
      }
      if (detail && typeof detail === "object" && "error" in detail) {
        const inner = detail.error;
        if (
          inner &&
          typeof inner === "object" &&
          "message" in inner &&
          typeof inner.message === "string"
        )
          throw new Error(inner.message);
      }
    }
    throw new Error(text);
  }
}
export function runSummary(run: IntelligenceRun) {
  if (run.status === "running") return "检测中";
  if (run.status === "cancelled" || run.status === "interrupted")
    return "已停止";
  return `${run.items.filter((x) => x.status === "passed").length}/${run.items.length} 通过`;
}
export const intelligenceStatus: Record<string, string> = {
  queued: "等待",
  generating: "答题中",
  rendering: "绘图中",
  judging: "判图中",
  passed: "通过",
  failed: "未通过",
  ungraded: "待判定",
  error: "出错",
  cancelled: "已停止",
};
