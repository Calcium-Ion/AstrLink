import { expect, it, vi } from "vitest";
import {
  MAX_BATCH_MODELS,
  runModelTestBatch,
  type ModelTestRow,
} from "./service-test-batch";
import type { ServiceTestInput, ServiceTestResult } from "./service-test-model";

const input = {
  protocol: "openai.chat",
  stream: true,
  prompt: "Reply briefly",
} as const;
const result = (model: string): ServiceTestResult => ({
  ...input,
  model,
  service_id: "service_test",
  ok: true,
  output: "OK",
  status_code: 200,
  duration_ms: 100,
});
const flush = async () => {
  await Promise.resolve();
  await Promise.resolve();
};

it("bounds concurrency, snapshots inputs and continues after independent failures", async () => {
  const models = ["a", "b", "c", "d"];
  const mutableInput = { ...input };
  const updates: ModelTestRow[] = [];
  const pending = new Map<
    string,
    {
      resolve: (value: ServiceTestResult) => void;
      reject: (error: Error) => void;
    }
  >();
  const test = vi.fn(
    (payload: ServiceTestInput) =>
      new Promise<ServiceTestResult>((resolve, reject) =>
        pending.set(payload.model, { resolve, reject }),
      ),
  );
  const run = runModelTestBatch({
    models,
    input: mutableInput,
    concurrency: 2,
    shouldStop: () => false,
    onUpdate: (row) => updates.push(row),
    test,
  });
  expect(test.mock.calls.map(([payload]) => payload.model)).toEqual(["a", "b"]);
  models[2] = "changed";
  mutableInput.prompt = "changed" as typeof mutableInput.prompt;
  pending.get("b")!.reject(new Error("Disconnected"));
  await flush();
  expect(test).toHaveBeenCalledTimes(3);
  expect(test.mock.calls[2][0]).toEqual({ ...input, model: "c" });
  pending
    .get("a")!
    .resolve({ ...result("a"), ok: false, error_code: "upstream_error" });
  await flush();
  expect(test).toHaveBeenCalledTimes(4);
  pending.get("c")!.resolve(result("c"));
  pending.get("d")!.resolve(result("d"));
  await run;
  expect(updates.filter((row) => row.state !== "running")).toEqual([
    { model: "b", state: "failed", error: "Disconnected" },
    {
      model: "a",
      state: "failed",
      result: { ...result("a"), ok: false, error_code: "upstream_error" },
    },
    { model: "c", state: "success", result: result("c") },
    { model: "d", state: "success", result: result("d") },
  ]);
});

it("stops queued models while preserving in-flight results", async () => {
  let stop = false;
  let finish!: (value: ServiceTestResult) => void;
  const updates: ModelTestRow[] = [];
  const test = vi.fn(
    () =>
      new Promise<ServiceTestResult>((resolve) => {
        finish = resolve;
      }),
  );
  const run = runModelTestBatch({
    models: ["a", "b", "c"],
    input,
    concurrency: 1,
    shouldStop: () => stop,
    onUpdate: (row) => updates.push(row),
    test,
  });
  stop = true;
  finish(result("a"));
  await run;
  expect(test).toHaveBeenCalledOnce();
  expect(updates).toEqual([
    { model: "a", state: "running" },
    { model: "a", state: "success", result: result("a") },
    { model: "b", state: "stopped" },
    { model: "c", state: "stopped" },
  ]);
});

it("rejects invalid queues before making requests", async () => {
  const test = vi.fn();
  for (const options of [
    { concurrency: 0, models: ["a"] },
    { concurrency: 4, models: ["a"] },
    { concurrency: 1.5, models: ["a"] },
    { concurrency: 2, models: ["a", "a"] },
    {
      concurrency: 2,
      models: Array.from({ length: MAX_BATCH_MODELS + 1 }, (_, index) =>
        String(index),
      ),
    },
  ]) {
    await expect(
      runModelTestBatch({
        ...options,
        input,
        shouldStop: () => false,
        onUpdate: vi.fn(),
        test,
      }),
    ).rejects.toThrow("Invalid model test batch");
  }
  expect(test).not.toHaveBeenCalled();
});
