import type { ServiceTestInput, ServiceTestResult } from "./service-test-model";

export const MAX_BATCH_MODELS = 100;
export type ModelTestState =
  | "queued"
  | "running"
  | "success"
  | "failed"
  | "stopped";
export interface ModelTestRow {
  model: string;
  state: ModelTestState;
  result?: ServiceTestResult;
  error?: string;
}

/** A bounded queue; stopping never pretends that an in-flight request was cancelled. */
export async function runModelTestBatch({
  models,
  input,
  concurrency,
  shouldStop,
  onUpdate,
  test,
}: {
  models: string[];
  input: Omit<ServiceTestInput, "model">;
  concurrency: number;
  shouldStop: () => boolean;
  onUpdate: (row: ModelTestRow) => void;
  test: (input: ServiceTestInput) => Promise<ServiceTestResult>;
}) {
  if (
    !Number.isInteger(concurrency) ||
    concurrency < 1 ||
    concurrency > 3 ||
    models.length > MAX_BATCH_MODELS ||
    new Set(models).size !== models.length
  ) {
    throw new Error("Invalid model test batch");
  }
  const targets = [...models];
  const snapshot = { ...input };
  let index = 0;
  async function worker() {
    while (!shouldStop() && index < targets.length) {
      const model = targets[index++];
      onUpdate({ model, state: "running" });
      try {
        const result = await test({ ...snapshot, model });
        onUpdate({ model, state: result.ok ? "success" : "failed", result });
      } catch (error) {
        onUpdate({
          model,
          state: "failed",
          error: error instanceof Error ? error.message : String(error),
        });
      }
    }
  }
  await Promise.all(
    Array.from({ length: Math.min(concurrency, targets.length) }, worker),
  );
  for (; index < targets.length; index++)
    onUpdate({ model: targets[index], state: "stopped" });
}
