import { Release } from "../mygo";
import type { CommandTable } from "./types";

// Commands bound in commands_release.go: app updates and the privacy model
// update reminder.
export const releaseCommands = {
  privacy_model_update_status: () => Release.privacyModelUpdateStatus(),
} satisfies CommandTable;
