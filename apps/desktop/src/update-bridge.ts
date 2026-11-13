import { isDesktopHost, invoke } from "./native-host/core";
import { listen } from "./native-host/event";
import {
  browserUpdateSnapshot,
  parseUpdateSnapshot,
  type UpdatePreferences,
  type UpdateSnapshot,
} from "./update-model";

async function call(
  command: string,
  args?: Record<string, unknown>,
): Promise<UpdateSnapshot> {
  try {
    return parseUpdateSnapshot(await invoke(command, args));
  } catch (error) {
    throw error instanceof Error ? error : new Error(String(error));
  }
}
export const getAppUpdateStatus = () =>
  isDesktopHost()
    ? call("app_update_status")
    : Promise.resolve(browserUpdateSnapshot());
export const checkAppUpdate = () => call("check_app_update");
export const downloadAppUpdate = () => call("download_app_update");
export const installAppUpdate = () => call("install_app_update");
export const saveUpdatePreferences = (input: UpdatePreferences) =>
  call("update_update_preferences", { input });
export const listenAppUpdates = (
  listener: (snapshot: UpdateSnapshot) => void,
) =>
  isDesktopHost()
    ? listen<unknown>("app-update-status", ({ payload }) => {
        try {
          listener(parseUpdateSnapshot(payload));
        } catch (error) {
          console.error("Invalid update event", error);
        }
      })
    : Promise.resolve(() => {});
