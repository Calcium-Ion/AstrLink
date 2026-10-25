/** Keep in sync with the Rust constants in sidecar.rs. */
export const SERVICE_TEST_CANCELLED_ERROR = "service_test_cancelled";
export const SERVICE_TEST_ALREADY_RUNNING_ERROR =
  "service_test_already_running";
export const SERVICE_TEST_INVALID_ID_ERROR = "service_test_invalid_id";

let sequence = 0;

export function createServiceTestId(): string {
  return `test_${Date.now()}_${++sequence}`;
}

export function isServiceTestCancelled(cause: unknown): boolean {
  if (cause === SERVICE_TEST_CANCELLED_ERROR) return true;
  if (!cause || typeof cause !== "object") return false;
  return (
    ("name" in cause && cause.name === "AbortError") ||
    ("message" in cause && cause.message === SERVICE_TEST_CANCELLED_ERROR)
  );
}
