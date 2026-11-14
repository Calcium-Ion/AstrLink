import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import {
  emptyLocalClients,
  parseLocalClients,
  type LocalClientSnapshot,
  type LocalClientTarget,
} from "./local-client-model";

async function call(command: string, args?: Record<string, unknown>) {
  try {
    return parseLocalClients(await invoke(command, args));
  } catch (error) {
    throw error instanceof Error ? error : new Error(String(error));
  }
}
export const getLocalClients = () =>
  isTauri()
    ? call("local_client_status")
    : Promise.resolve(emptyLocalClients());
export const refreshLocalClients = () => call("refresh_local_clients");
export const updateLocalClients = (targets: LocalClientTarget[]) =>
  call("update_local_clients", {
    targets: targets.map(({ id, wsl }) => ({ id, wsl })),
  });
export const repairLocalClients = (targets: LocalClientTarget[]) =>
  call("repair_local_clients", {
    targets: targets.map(({ id, wsl }) => ({ id, wsl })),
  });
export const listenLocalClients = (
  listener: (snapshot: LocalClientSnapshot) => void,
) =>
  isTauri()
    ? listen<unknown>("local-client-status", ({ payload }) => {
        try {
          listener(parseLocalClients(payload));
        } catch (error) {
          console.error("Invalid local client event", error);
        }
      })
    : Promise.resolve(() => {});
