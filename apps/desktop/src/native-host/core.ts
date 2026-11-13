// The desktop host's commands: invoke(command, args) with the command's
// snake_case name and camelCase arguments. Each command maps onto the
// client `mygo generate` writes for the Go services (../mygo.ts); see the
// commands-*.ts tables.
import { isMyGo } from "mygo-runtime";
import { agentsCommands } from "./commands-agents";
import { clientsCommands } from "./commands-clients";
import { coreCommands } from "./commands-core";
import { releaseCommands } from "./commands-release";
import { windowCommands } from "./commands-window";
import type { CommandTable } from "./types";

export const commandTable: CommandTable = {
  ...coreCommands,
  ...windowCommands,
  ...clientsCommands,
  ...agentsCommands,
  ...releaseCommands,
};

/** Reports whether the page runs in the desktop app. */
export function isDesktopHost(): boolean {
  return isMyGo();
}

/**
 * Calls a host command. A failed call rejects with the error's message, a
 * string, so callers keep their error handling.
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
