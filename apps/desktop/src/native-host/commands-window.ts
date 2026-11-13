import { Window } from "../mygo";
import type { CommandTable } from "./types";

// Commands bound in commands_window.go.
export const windowCommands = {
  window_chrome_preferences: () => Window.windowChromePreferences(),
} satisfies CommandTable;
