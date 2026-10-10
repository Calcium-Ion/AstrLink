import { act } from "react";

/**
 * Radix returns focus to the trigger one timer tick after a Select, Popover,
 * or Dialog closes. Call this after closing one and before the next
 * interaction or focus assertion, so the deferred focus cannot land inside the
 * next step and dismiss a layer it just opened.
 */
export async function settleClosedLayer(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => setTimeout(resolve, 0));
  });
}
