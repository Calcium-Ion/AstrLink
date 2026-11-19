package plugincli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	builtinplugins "github.com/QuantumNous/astrlink/core/plugins"

	"github.com/QuantumNous/astrlink/core/contract"
)

// template is a starting point for a project: its src/plugin.ts and the test
// cases that go with it.
type template struct {
	summary string
	plugin  string
	tests   string
	client  string
	// keeps are files the plugin reads beside its code, by path, which
	// package.json lists in astrlink.files; node gives the editor the
	// types of Node's modules.
	keeps map[string]string
	node  bool
}

func runNew(_ context.Context, env Environment, args []string) int {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	name := flags.String("template", "basic", "the template to start from: "+strings.Join(templateNames(), ", "))
	parent := flags.String("dir", ".", "the directory to create the project in")
	positional, ok := parse(env, "new", flags, args, 1)
	if !ok {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintf(env.Stderr, "astrlink plugin new: usage: %s\n", usageOf("new"))
		return 2
	}
	key := positional[0]
	if !contract.ValidPluginKey(key) {
		fmt.Fprintf(env.Stderr, "error: %q cannot be a plugin key: a lowercase letter, then up to 31 lowercase letters, digits and hyphens\n", key)
		return 2
	}
	chosen, found := templates()[*name]
	if !found {
		fmt.Fprintf(env.Stderr, "error: no template %q; choose one of %s\n", *name, strings.Join(templateNames(), ", "))
		return 2
	}
	dir := filepath.Join(*parent, key)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		fmt.Fprintf(env.Stderr, "error: %s already exists and is not empty\n", dir)
		return 1
	}
	files := projectFiles(key, chosen)
	names := make([]string, 0, len(files))
	for file := range files {
		names = append(names, file)
	}
	sort.Strings(names)
	for _, file := range names {
		path := filepath.Join(dir, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.WriteFile(path, []byte(files[file]), 0o644); err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(env.Stderr, "created %s from the %s template\nnext: cd %s && astrlink plugin test && astrlink plugin dev\n", dir, *name, dir)
	printJSON(env.Stdout, map[string]any{"key": key, "dir": dir, "template": *name, "files": names})
	return 0
}

func templateNames() []string {
	names := make([]string, 0, len(templates()))
	for name := range templates() {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// projectFiles are the files of a new project of key.
func projectFiles(key string, chosen template) map[string]string {
	replace := strings.NewReplacer("{{key}}", key, "{{name}}", titleOf(key))
	entries := `"plugin": "src/plugin.ts"`
	if chosen.client != "" {
		entries += `, "client": "src/client.tsx"`
	}
	if len(chosen.keeps) > 0 {
		kept := map[string]bool{}
		for name := range chosen.keeps {
			kept[strings.Split(name, "/")[0]] = true
		}
		names, _ := json.Marshal(slices.Sorted(maps.Keys(kept)))
		entries += `, "files": ` + strings.ReplaceAll(string(names), ",", ", ")
	}
	// The interface's React types and Node's are for the editor alone: the
	// window provides React, and the gateway Node's modules.
	devDependencies := ""
	types := `[]`
	include := `"src", "astrlink-plugin.d.ts"`
	if chosen.client != "" {
		devDependencies = `,
  "devDependencies": { "@types/react": "^19.1.0" }`
		include += `, "astrlink-plugin-client.d.ts"`
	}
	if chosen.node {
		devDependencies = `,
  "devDependencies": { "@types/node": "^22.0.0" }`
		types = `["node"]`
	}
	files := map[string]string{
		"package.json": replace.Replace(`{
  "name": "astrlink-plugin-{{key}}",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "engines": { "astrlink": ">=0.1.0-0" },
  "astrlink": { ` + entries + ` }` + devDependencies + `
}
`),
		"tsconfig.json": `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "lib": ["ES2022", "DOM"],
    "jsx": "react-jsx",
    "types": ` + types + `,
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true
  },
  "include": [` + include + `]
}
`,
		".gitignore":           "node_modules/\ndist/\n",
		"astrlink-plugin.d.ts": builtinplugins.TypeDefinitions,
		"src/plugin.ts":        replace.Replace(chosen.plugin),
		"tests/plugin.json":    replace.Replace(chosen.tests),
		"README.md": replace.Replace("# {{name}}\n\nAn AstrLink plugin.\n\n" +
			"- `astrlink plugin test` runs the cases in `tests/`.\n" +
			"- `astrlink plugin dev` keeps the plugin built into the running AstrLink and shows its logs; enable it on the plugin page.\n" +
			"- `astrlink plugin check` checks it as AstrLink loads it.\n\n" +
			"Install it elsewhere by pasting the GitHub address of this repository on the plugin page. AstrLink builds it from source; commit a lock file when you add dependencies.\n"),
	}
	if chosen.client != "" {
		files["src/client.tsx"] = replace.Replace(chosen.client)
		files["astrlink-plugin-client.d.ts"] = builtinplugins.ClientTypeDefinitions
	}
	for name, text := range chosen.keeps {
		files[name] = replace.Replace(text)
	}
	return files
}

// titleOf makes a display name of a key: memory-notes becomes Memory Notes.
func titleOf(key string) string {
	words := strings.Split(key, "-")
	for index, word := range words {
		if word != "" {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func templates() map[string]template {
	return map[string]template{
		"gate": {
			summary: "An entry hook that tags requests and refuses those that break a rule.",
			plugin: `// {{name}}: refuses requests outside working hours for expensive models,
// and tags the rest.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["request.gate"],
  settings: [{ key: "expensive", label: "Expensive models", type: "text", placeholder: "claude-opus" }],
} satisfies AstrLinkPlugin.Meta;

export async function onRequest(ctx: AstrLinkPlugin.RequestContext): Promise<AstrLinkPlugin.RequestDecision> {
  const expensive = String(astrlink.config.get().expensive ?? "opus");
  const hour = new Date(astrlink.clock.now() + astrlink.clock.offset() * 60_000).getUTCHours();
  const afterHours = hour < 9 || hour >= 19;
  if (afterHours && ctx.request.model.includes(expensive)) {
    return { action: "deny", status: 403, code: "after_hours", message: "Expensive models are off after hours." };
  }
  return { action: "allow", tags: [afterHours ? "after-hours" : "working-hours"] };
}
`,
			tests: `[
  {
    "name": "lets a cheap model in",
    "hook": "onRequest",
    "args": [{ "request": { "model": "gpt-4o-mini", "protocol": "openai.chat", "tags": [] } }],
    "expect": { "match": { "action": "allow" } }
  }
]
`,
		},
		"route": {
			summary: "A route hook that sends requests to the cheapest provider first.",
			plugin: `// {{name}}: puts the providers whose names say "cheap" first.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["routing"],
} satisfies AstrLinkPlugin.Meta;

export async function route(ctx: AstrLinkPlugin.RouteContext): Promise<AstrLinkPlugin.RouteDecision> {
  const cheap = ctx.candidates.filter((candidate) => /cheap/i.test(candidate.name));
  if (cheap.length === 0) return {};
  return { order: cheap.map((candidate) => candidate.id), reason: "cheap providers first" };
}
`,
			tests: `[
  {
    "name": "moves the cheap provider first",
    "hook": "route",
    "args": [{ "request": { "model": "m", "tags": [] }, "candidates": [
      { "id": "a", "name": "Main", "kind": "openai", "model": "m" },
      { "id": "b", "name": "Cheap backup", "kind": "openai", "model": "m" }
    ] }],
    "expect": { "result": { "order": ["b"], "reason": "cheap providers first" } }
  }
]
`,
		},
		"forward": {
			summary: "A beforeForward hook that adds house rules to the system prompt and caps the reply.",
			plugin: `// {{name}}: appends the team's rules to the system prompt and caps the
// length of replies.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["request.write"],
  settings: [{ key: "rules", label: "Rules", type: "text", default: "Answer in the user's language." }],
} satisfies AstrLinkPlugin.Meta;

export async function beforeForward(): Promise<AstrLinkPlugin.ForwardEdits> {
  const rules = String(astrlink.config.get().rules ?? "");
  return { system: { append: rules }, params: { max_tokens: 4096 } };
}
`,
			tests: `[
  {
    "name": "appends the rules",
    "hook": "beforeForward",
    "config": { "rules": "Be brief." },
    "args": [{ "request": { "model": "m", "tags": [] } }],
    "expect": { "result": { "system": { "append": "Be brief." }, "params": { "max_tokens": 4096 } } }
  }
]
`,
		},
		"stream": {
			summary: "Stream hooks that mask secrets in replies as they stream, in every protocol.",
			plugin: `// {{name}}: masks what looks like an API key in replies, also where the
// stream splits one across two events.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["response.write"],
} satisfies AstrLinkPlugin.Meta;

export const { onStreamEvent, onStreamEnd } = astrlink.textStream({
  replace: [[/sk-[A-Za-z0-9]{20,}/, "sk-…"]],
  maxMatch: 64,
});
`,
			tests: `[
  {
    "name": "passes an event without text",
    "hook": "onStreamEvent",
    "args": [{ "protocol": "openai.chat" }, { "data": "[DONE]" }, null],
    "expect": { "match": { "events": [{ "data": "[DONE]" }] } }
  }
]
`,
		},
		"response": {
			summary: "A rewriteResponse hook that adds a footer to whole replies.",
			plugin: `// {{name}}: adds a footer to Chat Completions replies that do not stream.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["response.write"],
} satisfies AstrLinkPlugin.Meta;

export async function rewriteResponse(ctx: AstrLinkPlugin.ResponseContext): Promise<AstrLinkPlugin.ResponseEdits> {
  const body = structuredClone(ctx.response.body) as { choices?: Array<{ message?: { content?: string } }> };
  const message = body.choices?.[0]?.message;
  if (ctx.request.protocol !== "openai.chat" || typeof message?.content !== "string") return {};
  message.content += "\n\n— reviewed by {{name}}";
  return { body };
}
`,
			tests: `[
  {
    "name": "adds the footer",
    "hook": "rewriteResponse",
    "args": [{ "request": { "protocol": "openai.chat", "tags": [] }, "response": { "status": 200, "body": { "choices": [{ "message": { "content": "Hi" } }] } } }],
    "expect": { "result": { "body": { "choices": [{ "message": { "content": "Hi\n\n— reviewed by {{name}}" } }] } } }
  }
]
`,
		},
		"schedule": {
			summary: "A scheduled task that refreshes a list from a server into the plugin's store.",
			plugin: `// {{name}}: every 30 minutes, keeps a copy of a remote list in the store,
// for the hooks of requests to read without waiting on the network.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["schedule", "network", "storage"],
  network: { purpose: "Download the list the plugin keeps" },
  schedules: [{ name: "refresh", everyMinutes: 30 }],
  settings: [{ key: "listUrl", label: "List address", type: "server", required: true }],
} satisfies AstrLinkPlugin.Meta;

export async function onSchedule(run: AstrLinkPlugin.ScheduleRun) {
  const response = await fetch(String(astrlink.config.get().listUrl));
  if (!response.ok) throw new Error("the list answered " + response.status);
  await astrlink.store.set("list", await response.json());
  console.info(run.name, "kept the list");
}
`,
			tests: `[
  {
    "name": "keeps the list",
    "hook": "onSchedule",
    "config": { "listUrl": "https://lists.example/models.json" },
    "args": [{ "name": "refresh", "everyMinutes": 30 }],
    "fetch": [{ "url": "https://lists.example/models.json", "json": ["a", "b"] }],
    "expect": { "store": { "list": ["a", "b"] } }
  }
]
`,
		},
		"http": {
			summary: "A local endpoint under /plugins/<key>/ on the inference port.",
			plugin: `// {{name}}: answers GET /plugins/{{key}}/health on the inference port.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["http.serve"],
} satisfies AstrLinkPlugin.Meta;

export async function onHttp(request: AstrLinkPlugin.HttpServeRequest): Promise<AstrLinkPlugin.HttpServeResponse> {
  if (request.method === "GET" && request.path === "/health") return { json: { ok: true } };
  return { status: 404, body: "not found" };
}
`,
			tests: `[
  {
    "name": "answers health",
    "hook": "onHttp",
    "args": [{ "method": "GET", "path": "/health", "query": {}, "headers": {} }],
    "expect": { "result": { "json": { "ok": true } } }
  }
]
`,
		},
		"provider": {
			summary: "A provider adapter: Chat Completions requests to an API of its own, whole and streamed.",
			plugin: `// {{name}}: serves Chat Completions with an API that takes a prompt and
// answers text, whole or as a stream of deltas. Change the shapes below to
// your API's; the gateway sends the key, keeps the requests on the
// provider's address, and does routing, failover, records and prices.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["provider"],
  providers: [
    {
      id: "{{key}}",
      name: "{{name}}",
      baseUrl: "https://api.example.com/v1",
      auth: "bearer",
      protocols: { "openai.chat": { stream: true } },
      // Other protocols reach the plugin converted into Chat Completions.
      convert: true,
      models: ["example-1"],
    },
  ],
} satisfies AstrLinkPlugin.Meta;

type Chat = { messages: Array<{ role: string; content: unknown }> };

export async function buildRequest(ctx: AstrLinkPlugin.ProviderContext): Promise<AstrLinkPlugin.ProviderRequest | null> {
  if (ctx.purpose === "models") return { method: "GET", url: "/models" };
  const body = ctx.body as Chat;
  const prompt = body.messages.map((message) => message.role + ": " + String(message.content)).join("\n");
  return { url: "/generate", body: { model: ctx.model, prompt, stream: ctx.stream } };
}

export async function parseResponse(ctx: AstrLinkPlugin.ProviderContext, res: AstrLinkPlugin.ProviderResponse): Promise<AstrLinkPlugin.ProviderResult> {
  if (ctx.purpose === "models") return { models: (res.json.data as Array<{ id: string }>).map((model) => model.id) };
  return {
    json: {
      id: "chatcmpl-" + astrlink.util.sha256(res.body).slice(0, 12),
      object: "chat.completion",
      model: ctx.model,
      choices: [{ index: 0, message: { role: "assistant", content: res.json.output }, finish_reason: "stop" }],
      usage: { prompt_tokens: res.json.usage.input, completion_tokens: res.json.usage.output, total_tokens: res.json.usage.input + res.json.usage.output },
    },
  };
}

export async function parseStreamEvent(ctx: AstrLinkPlugin.ProviderContext, event: AstrLinkPlugin.UpstreamEvent, state: number | null): Promise<AstrLinkPlugin.StreamStep<number>> {
  const chunk = (delta: object, finish: string | null) => ({
    id: "chatcmpl-stream", object: "chat.completion.chunk", model: ctx.model,
    choices: [{ index: 0, delta, finish_reason: finish }],
  });
  if (event.json?.done) return { events: [{ data: chunk({}, "stop") }, { data: "[DONE]" }], done: true };
  return { events: [{ data: chunk({ content: event.json?.delta ?? "" }, null) }], state: (state ?? 0) + 1 };
}

export async function parseError(_ctx: AstrLinkPlugin.ProviderContext, res: AstrLinkPlugin.ProviderResponse): Promise<AstrLinkPlugin.ProviderError> {
  return { status: res.status, message: res.json?.error ?? res.body, retryable: res.status === 429 || res.status >= 500 };
}
`,
			tests: `[
  {
    "name": "builds the request",
    "hook": "buildRequest",
    "args": [{ "purpose": "chat", "provider": "{{key}}", "baseUrl": "https://api.example.com/v1", "settings": {}, "service": { "id": "s", "name": "S" }, "protocol": "openai.chat", "model": "example-1", "stream": false, "body": { "messages": [{ "role": "user", "content": "hi" }] } }],
    "expect": { "result": { "url": "/generate", "body": { "model": "example-1", "prompt": "user: hi", "stream": false } } }
  },
  {
    "name": "turns a delta into a chunk",
    "hook": "parseStreamEvent",
    "args": [{ "purpose": "chat", "model": "example-1" }, { "data": "{\"delta\":\"he\"}", "json": { "delta": "he" } }, null],
    "expect": { "match": { "state": 1 } }
  }
]
`,
		},
		"ui": {
			summary: "An interface: a card on the overview and a tab on its plugin page, which ask the plugin's backend for a count.",
			plugin: `// {{name}}: keeps a count of the turns it saw; its interface shows it.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["storage", "ui.code"],
  storage: [{ label: "The number of turns seen", retention: "Until the plugin is removed" }],
} satisfies AstrLinkPlugin.Meta;

export async function observeTurn() {
  const seen = ((await astrlink.store.get("turns")) as number | null) ?? 0;
  await astrlink.store.set("turns", seen + 1);
}

// The interface's calls: call("count") in src/client.tsx.
export async function onUiCall(call: AstrLinkPlugin.UiCall) {
  if (call.name === "count") return { turns: ((await astrlink.store.get("turns")) as number | null) ?? 0 };
  throw new Error("no such call: " + call.name);
}
`,
			client: `// {{name}}: the interface. React and the components come from the window.
import { useEffect, useState } from "react";
import { call, LoadingState, Panel, PanelHeader, registerSlot, t } from "@astrlink/plugin/client";

function Count() {
  const [turns, setTurns] = useState<number | null>(null);
  useEffect(() => {
    void call<{ turns: number }>("count").then((answer) => setTurns(answer.turns));
  }, []);
  if (turns === null) return <LoadingState label={t({ en: "Counting…", "zh-CN": "统计中…" })} />;
  return <p>{t({ en: "{{n}} turns seen", "zh-CN": "看到了 {{n}} 轮对话" }, { n: turns })}</p>;
}

registerSlot("overview.widget", () => (
  <Panel>
    <PanelHeader size="sm">{{name}}</PanelHeader>
    <div className="p-3 text-sm">
      <Count />
    </div>
  </Panel>
));

registerSlot("plugin.tab", Count, { title: t({ en: "Count", "zh-CN": "统计" }) });
`,
			tests: `[
  {
    "name": "answers the count",
    "hook": "onUiCall",
    "args": [{ "name": "count", "input": null, "language": "en" }],
    "store": { "turns": 4 },
    "expect": { "result": { "turns": 4 } }
  }
]
`,
		},
		"files": {
			summary: "Node's modules: a journal in the plugin's own directory, and a file it carries beside its code.",
			node:    true,
			keeps:   map[string]string{"assets/greeting.txt": "Hello from {{name}}\n"},
			plugin: `// {{name}}: keeps a journal of the turns it sees in its own directory, and
// shows the greeting it carries beside its code.
import { appendFile, readFile } from "node:fs/promises";
import { createReadStream, existsSync } from "node:fs";
import { createInterface } from "node:readline";
import { createHash } from "node:crypto";
import path from "node:path";

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: [],
} satisfies AstrLinkPlugin.Meta;

// A relative path is in the plugin's data directory, its working
// directory; node:fs reaches nothing outside its own directories.
const journal = "journal.jsonl";

export async function observeTurn(turn: AstrLinkPlugin.TurnView) {
  const digest = createHash("sha256").update(turn.user.text).digest("hex").slice(0, 12);
  await appendFile(journal, JSON.stringify({ session: turn.session.id, turn: turn.session.turn, digest }) + "\n");
}

export async function describeStatus(): Promise<AstrLinkPlugin.ViewBlock[]> {
  // package.json lists assets in astrlink.files, which the install puts
  // beside the code, in import.meta.dirname.
  const greeting = await readFile(path.join(import.meta.dirname, "assets", "greeting.txt"), "utf8");
  let turns = 0;
  if (existsSync(journal)) {
    for await (const line of createInterface({ input: createReadStream(journal) })) if (line) turns++;
  }
  return [{ type: "kv", items: [{ label: "Greeting", value: greeting.trim() }, { label: "Turns in the journal", value: String(turns) }] }];
}
`,
			tests: `[
  {
    "name": "writes a turn to the journal",
    "hook": "observeTurn",
    "args": [{ "session": { "id": "s1", "turn": 1 }, "user": { "text": "hello", "typed": "hello" } }],
    "files": { "journal.jsonl": "{\"session\":\"s0\",\"turn\":1,\"digest\":\"000000000000\"}\n" },
    "expect": { "files": { "journal.jsonl": "{\"session\":\"s0\",\"turn\":1,\"digest\":\"000000000000\"}\n{\"session\":\"s1\",\"turn\":1,\"digest\":\"2cf24dba5fb0\"}\n" } }
  },
  {
    "name": "shows the greeting and the turns",
    "hook": "describeStatus",
    "files": { "journal.jsonl": "{}\n{}\n" },
    "expect": { "result": [{ "type": "kv", "items": [{ "label": "Greeting", "value": "Hello from {{name}}" }, { "label": "Turns in the journal", "value": "2" }] }] }
  }
]
`,
		},
		"basic": {
			summary: "A plugin that keeps a count of the turns it saw and reports it on its page.",
			plugin: `// {{name}}: counts the turns of the conversations it sees.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["storage"],
  storage: [{ label: "The number of turns seen", retention: "Until the plugin is removed" }],
} satisfies AstrLinkPlugin.Meta;

export async function observeTurn(turn: AstrLinkPlugin.TurnView) {
  const seen = ((await astrlink.store.get("turns")) as number | null) ?? 0;
  await astrlink.store.set("turns", seen + 1);
  console.info("turn", turn.session.turn, "of session", turn.session.id);
}

export async function describeStatus(): Promise<AstrLinkPlugin.ViewBlock[]> {
  const seen = ((await astrlink.store.get("turns")) as number | null) ?? 0;
  return [{ type: "kv", items: [{ label: "Turns seen", value: String(seen) }] }];
}
`,
			tests: `[
  {
    "name": "counts a turn",
    "hook": "observeTurn",
    "args": [{ "session": { "id": "s1", "turn": 3 }, "user": { "text": "hello", "typed": "hello" } }],
    "store": { "turns": 1 },
    "expect": { "store": { "turns": 2 } }
  },
  {
    "name": "reports the count",
    "hook": "describeStatus",
    "store": { "turns": 5 },
    "expect": { "result": [{ "type": "kv", "items": [{ "label": "Turns seen", "value": "5" }] }] }
  }
]
`,
		},
	}
}
