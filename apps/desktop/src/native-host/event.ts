// Host events: listen(name, handler) resolves to the function that
// unsubscribes, and the handler gets `{ payload }`.
import { eventTable } from "./events";

export type UnlistenFn = () => void;

export interface Event<T> {
  payload: T;
}

/** Subscribes to a host event. */
export async function listen<T>(
  name: string,
  handler: (event: Event<T>) => void,
): Promise<UnlistenFn> {
  const event = eventTable[name as keyof typeof eventTable];
  if (!event) throw new Error(`unknown desktop event: ${name}`);
  return event.on((payload) => handler({ payload: payload as T }));
}
