// The desktop host's API in the shape the frontend used under Tauri:
// invoke(command, args), listen(event, handler) and the current window.
// Commands and events map onto the client `mygo generate` writes for the Go
// services (../mygo.ts); see the commands-*.ts tables.
import { isMyGo } from "mygo-runtime";
import { agentsCommands } from "./commands-agents";
import { clientsCommands } from "./commands-clients";
import { coreCommands } from "./commands-core";
import { releaseCommands } from "./commands-release";
import { windowCommands } from "./commands-window";
import { eventTable } from "./events";
import type { CommandTable } from "./types";

export type UnlistenFn = () => void;

export interface Event<T> {
  payload: T;
}

export const commandTable: CommandTable = {
  ...coreCommands,
  ...windowCommands,
  ...clientsCommands,
  ...agentsCommands,
  ...releaseCommands,
};

/** Reports whether the page runs in the desktop app. */
export function isTauri(): boolean {
  return isMyGo();
}

/**
 * Calls a host command. A failed call rejects with the error's message, a
 * string, as Tauri commands did, so callers keep their error handling.
 */
export async function invoke<T>(
  command: string,
  args?: Record<string, unknown>,
): Promise<T> {
  const call = commandTable[command];
  if (!call) throw `unknown desktop command: ${command}`;
  try {
    return (await call(args ?? {})) as T;
  } catch (error) {
    throw error instanceof Error ? error.message : error;
  }
}

/** Subscribes to a host event; the handler gets `{ payload }`. */
export async function listen<T>(
  name: string,
  handler: (event: Event<T>) => void,
): Promise<UnlistenFn> {
  const event = eventTable[name as keyof typeof eventTable];
  if (!event) throw new Error(`unknown desktop event: ${name}`);
  return event.on((payload) => handler({ payload: payload as T }));
}
