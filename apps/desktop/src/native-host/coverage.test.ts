import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { commandTable } from "./core";
import { eventTable } from "./events";

// Commands the pages invoke that a later part of the port binds. Each list
// shrinks as its part lands; a name here that is already in the command
// table fails, so a list never outlives its commands.
const pendingCommands: Record<string, string[]> = {
  // Windows, the tray, inspectors, files and the currency.
  windows: [],
  // Agent tools, imports, terminals and local client updates.
  agents: [],
  // Only the server edition installs the claude CLI; the desktop never
  // offered it.
  webOnly: ["install_claude_cli"],
};

const sourceRoot = join(import.meta.dirname, "..");

function sources(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      return entry.name === "native-host" ? [] : sources(path);
    }
    return /\.tsx?$/.test(entry.name) &&
      !/\.test\.tsx?$/.test(entry.name) &&
      entry.name !== "mygo.ts"
      ? [path]
      : [];
  });
}

const pages = sources(sourceRoot).map((path) => readFileSync(path, "utf8"));

// The host commands the pages invoke: string literals passed to an invoke,
// or to a bridge's call(command, …) helper that hands its command on, in
// files that import the host's commands. The pricing bridge's call takes
// an operation of the pricing command instead.
function invokedCommands(): Set<string> {
  const names = new Set<string>();
  const invoke =
    /\b(?:invoke\w*)\s*(?:<[^()]*?>)?\(\s*"([a-z][a-z0-9]*(?:_[a-z0-9]+)*)"/g;
  const call = /\bcall\s*(?:<[^()]*?>)?\(\s*"([a-z][a-z0-9_]*)"/g;
  for (const source of pages) {
    if (!source.includes("native-host/core")) continue;
    for (const match of source.matchAll(invoke)) names.add(match[1]);
    if (/function call\(\s*command: string/.test(source)) {
      for (const match of source.matchAll(call)) names.add(match[1]);
    }
  }
  return names;
}

// The host events the pages listen to, by literal or by the constant that
// names them.
function listenedEvents(): Set<string> {
  const constants = new Map<string, string>();
  for (const source of pages) {
    for (const match of source.matchAll(
      /\bconst\s+([A-Z][A-Z0-9_]*_EVENT)\s*=\s*"([^"]+)"/g,
    )) {
      constants.set(match[1], match[2]);
    }
  }
  const names = new Set<string>();
  for (const source of pages) {
    if (!source.includes("native-host/event")) continue;
    for (const match of source.matchAll(
      /\blisten\s*(?:<[^()]*?>)?\(\s*(?:"([^"]+)"|([A-Z][A-Z0-9_]*_EVENT))/g,
    )) {
      const name = match[1] ?? constants.get(match[2]);
      if (!name) throw new Error(`unresolved event constant ${match[2]}`);
      names.add(name);
    }
  }
  return names;
}

describe("the desktop host", () => {
  const pending = new Set(Object.values(pendingCommands).flat());

  it("binds or lists as pending every command the pages invoke", () => {
    const invoked = invokedCommands();
    // A scan that finds nothing would pass trivially.
    expect(invoked.size).toBeGreaterThan(130);
    const unbound = [...invoked].filter(
      (name) => !(name in commandTable) && !pending.has(name),
    );
    expect(unbound).toEqual([]);
  });

  it("lists as pending only commands not bound yet that the pages invoke", () => {
    const invoked = invokedCommands();
    expect([...pending].filter((name) => name in commandTable)).toEqual([]);
    expect([...pending].filter((name) => !invoked.has(name))).toEqual([]);
  });

  it("binds only commands the pages invoke", () => {
    const invoked = invokedCommands();
    expect(
      Object.keys(commandTable).filter((name) => !invoked.has(name)),
    ).toEqual([]);
  });

  it("declares every event the pages listen to", () => {
    const listened = listenedEvents();
    expect(listened.size).toBeGreaterThan(10);
    expect([...listened].filter((name) => !(name in eventTable))).toEqual([]);
    expect(
      Object.keys(eventTable).filter((name) => !listened.has(name)),
    ).toEqual([]);
  });
});
