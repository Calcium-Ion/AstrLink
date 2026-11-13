import { Release } from "../mygo";
import type { CommandTable } from "./types";

// Commands bound in commands_release.go: app updates and the privacy model
// update reminder.
export const releaseCommands = {
  app_update_status: () => Release.appUpdateStatus(),
  check_app_update: () => Release.checkAppUpdate(),
  download_app_update: () => Release.downloadAppUpdate(),
  install_app_update: () => Release.installAppUpdate(),
  update_update_preferences: (a: { input: unknown }) =>
    Release.updateUpdatePreferences(a.input),
  privacy_model_update_status: () => Release.privacyModelUpdateStatus(),
} satisfies CommandTable;
