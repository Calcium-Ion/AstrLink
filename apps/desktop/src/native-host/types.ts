// A command the frontend invokes by its snake_case name, with the camelCase
// argument object it always passed. Each entry calls the generated client
// (../mygo.ts), so tsc checks the arguments against the Go method.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type CommandArgs = any;
export type Command = (args: CommandArgs) => Promise<unknown>;
export type CommandTable = Record<string, Command>;
