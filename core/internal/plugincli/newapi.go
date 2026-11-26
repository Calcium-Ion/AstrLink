package plugincli

import "strings"

// newAPIFiles are the files of a new project in new-api's plugin format:
// plugin.js as both gateways install it, its usage guide and release notes
// as new-api's marketplace checks them, and the fixtures in new-api's
// format. It has no package.json, which is how AstrLink tells it apart.
func newAPIFiles(key string, chosen template) map[string]string {
	replace := strings.NewReplacer("{{key}}", key, "{{name}}", titleOf(key))
	return map[string]string{
		"plugin.js":         replace.Replace(chosen.plugin),
		"tests/plugin.json": replace.Replace(chosen.tests),
		"README.md":         replace.Replace(chosen.readme),
		"CHANGELOG.md": replace.Replace(`---
changelogVersion: 1
plugin: "{{key}}"
version: "0.1.0"
locale: "en"
---
# Changelog

## [0.1.0]

### Added

` + chosen.changes),
	}
}

func newAPITemplates() map[string]template {
	return map[string]template{
		"request": {
			summary: "New API format: an ingress hook that rejects requests with a blocked keyword, and an outbound hook that sets a header.",
			newAPI:  true,
			plugin: `// {{name}}: rejects requests that contain a blocked keyword, and sets a
// header on the requests sent upstream. It is written in New API's plugin
// format (apiVersion 1), which New API and AstrLink both run; the contract
// is the Request processing part of
// https://github.com/QuantumNous/new-api/blob/main/docs/plugin-api/v1.md

export const meta = {
  apiVersion: 1,
  key: "{{key}}",
  name: "{{name}}",
  description: { en: "Rejects requests with blocked keywords and sets a header upstream" },
  version: "0.1.0",
  // Your name, and url: your GitHub page.
  author: { name: "Your name" },
  configSchema: {
    type: "object",
    properties: {
      keywords: {
        type: "string",
        format: "keywords",
        title: { en: "Blocked keywords" },
        description: { en: "One per line; a request whose text contains any of them is rejected" },
        default: "",
        maxLength: 65536,
      },
      exempt: {
        type: "string",
        format: "keywords",
        title: { en: "Exempt callers" },
        description: {
          en: "One per line: group:<name> for a New API user group, client:<name> for an AstrLink client such as client:claude_code",
        },
        default: "",
        maxLength: 4096,
      },
      headerName: {
        type: "string",
        title: { en: "Header name" },
        description: { en: "A header to set on upstream requests; leave empty to set none" },
        default: "",
        maxLength: 128,
      },
      headerValue: {
        type: "string",
        title: { en: "Header value" },
        default: "",
        maxLength: 1024,
      },
    },
  },
};

function lines(text) {
  return String(text || "")
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

// texts collects the strings of a request body, so that a keyword matches
// what the caller wrote and not the names of the body's members.
function texts(value, into) {
  if (typeof value === "string") into.push(value.toLowerCase());
  else if (Array.isArray(value)) value.forEach((item) => texts(item, into));
  else if (value !== null && typeof value === "object") Object.values(value).forEach((item) => texts(item, into));
  return into;
}

// Some ctx members exist on one gateway only. New API sets ctx.group,
// ctx.user, ctx.token and, from the request stage on, ctx.channel; AstrLink
// leaves them out. AstrLink sets ctx.client, the client it detected such as
// "claude_code", and ctx.session, {id, turn}; New API leaves them out. Check
// a member before using it, and keep working without it.
function exempt(ctx, config) {
  const callers = lines(config.exempt);
  if (ctx.group && callers.includes("group:" + ctx.group)) return true;
  if (ctx.client && callers.includes("client:" + ctx.client)) return true;
  return false;
}

export function ingress(ctx, req) {
  const config = ctx.config || {};
  const keywords = lines(config.keywords).map((word) => word.toLowerCase());
  if (keywords.length === 0 || exempt(ctx, config)) return { action: "continue" };
  const written = texts(req.body, []);
  const count = keywords.filter((word) => written.some((text) => text.includes(word))).length;
  if (count === 0) return { action: "continue" };
  // Findings name the rule and how often it matched, never the matched text.
  const result = {
    action: "reject",
    code: "blocked.keyword",
    message: "This request contains a blocked keyword",
    findings: [{ rule: "keyword", count }],
  };
  if (ctx.session) result.notice = { en: "Blocked a keyword in turn " + ctx.session.turn + " of the conversation" };
  return result;
}

export function outbound(ctx, req) {
  const config = ctx.config || {};
  const name = String(config.headerName || "").trim();
  if (!name) return { action: "continue" };
  return { action: "continue", effects: [{ type: "header.patch", set: { [name]: String(config.headerValue || "") } }] };
}
`,
			tests: `{
  "cases": [
    {
      "name": "rejects a blocked keyword",
      "hook": "ingress",
      "args": [
        {
          "stage": "ingress", "format": "openai", "path": "/v1/chat/completions", "model": "gpt-4o-mini", "attempt": 0,
          "instance": { "id": "1", "name": "{{name}}" },
          "config": { "keywords": "internal only\nsecret plan", "exempt": "", "headerName": "", "headerValue": "" }
        },
        { "body": { "model": "gpt-4o-mini", "messages": [{ "role": "user", "content": "Summarize this memo. It is Internal Only." }] } }
      ],
      "expected": {
        "action": "reject", "code": "blocked.keyword", "message": "This request contains a blocked keyword",
        "findings": [{ "rule": "keyword", "count": 1 }]
      }
    },
    {
      "name": "lets other requests through",
      "hook": "ingress",
      "args": [
        {
          "stage": "ingress", "format": "openai", "path": "/v1/chat/completions", "model": "gpt-4o-mini", "attempt": 0,
          "instance": { "id": "1", "name": "{{name}}" },
          "config": { "keywords": "internal only", "exempt": "", "headerName": "", "headerValue": "" }
        },
        { "body": { "model": "gpt-4o-mini", "messages": [{ "role": "user", "content": "What is the weather like?" }] } }
      ],
      "expected": { "action": "continue" }
    },
    {
      "name": "exempts a New API group",
      "hook": "ingress",
      "args": [
        {
          "stage": "ingress", "format": "openai", "path": "/v1/chat/completions", "model": "gpt-4o-mini", "attempt": 0,
          "group": "staff", "user": { "id": 1, "username": "alice" },
          "instance": { "id": "1", "name": "{{name}}" },
          "config": { "keywords": "internal only", "exempt": "group:staff", "headerName": "", "headerValue": "" }
        },
        { "body": { "model": "gpt-4o-mini", "messages": [{ "role": "user", "content": "This memo is internal only." }] } }
      ],
      "expected": { "action": "continue" }
    },
    {
      "name": "exempts an AstrLink client",
      "hook": "ingress",
      "args": [
        {
          "stage": "ingress", "format": "claude", "path": "/v1/messages", "model": "claude-sonnet-4-5", "attempt": 0,
          "client": "claude_code", "session": { "id": "s1", "turn": 3 },
          "instance": { "id": "1", "name": "{{name}}" },
          "config": { "keywords": "internal only", "exempt": "client:claude_code", "headerName": "", "headerValue": "" }
        },
        { "body": { "model": "claude-sonnet-4-5", "messages": [{ "role": "user", "content": "This memo is internal only." }] } }
      ],
      "expected": { "action": "continue" }
    },
    {
      "name": "names the turn on AstrLink",
      "hook": "ingress",
      "args": [
        {
          "stage": "ingress", "format": "claude", "path": "/v1/messages", "model": "claude-sonnet-4-5", "attempt": 0,
          "client": "codex", "session": { "id": "s1", "turn": 3 },
          "instance": { "id": "1", "name": "{{name}}" },
          "config": { "keywords": "internal only", "exempt": "client:claude_code", "headerName": "", "headerValue": "" }
        },
        { "body": { "model": "claude-sonnet-4-5", "messages": [{ "role": "user", "content": "This memo is internal only." }] } }
      ],
      "expected": {
        "action": "reject", "code": "blocked.keyword", "message": "This request contains a blocked keyword",
        "findings": [{ "rule": "keyword", "count": 1 }],
        "notice": { "en": "Blocked a keyword in turn 3 of the conversation" }
      }
    },
    {
      "name": "sets the header upstream",
      "hook": "outbound",
      "args": [
        {
          "stage": "outbound", "format": "openai", "path": "/v1/chat/completions", "model": "gpt-4o-mini", "upstreamModel": "gpt-4o-mini", "attempt": 0,
          "channel": { "id": 7, "type": 1 },
          "instance": { "id": "1", "name": "{{name}}" },
          "config": { "keywords": "", "exempt": "", "headerName": "X-Team", "headerValue": "research" }
        },
        { "body": { "model": "gpt-4o-mini", "messages": [] }, "headers": {} }
      ],
      "expected": { "action": "continue", "effects": [{ "type": "header.patch", "set": { "X-Team": "research" } }] }
    },
    {
      "name": "sets no header without a name",
      "hook": "outbound",
      "args": [
        {
          "stage": "outbound", "format": "openai", "path": "/v1/chat/completions", "model": "gpt-4o-mini", "attempt": 0,
          "instance": { "id": "1", "name": "{{name}}" },
          "config": { "keywords": "", "exempt": "", "headerName": "", "headerValue": "" }
        },
        { "body": { "model": "gpt-4o-mini", "messages": [] }, "headers": {} }
      ],
      "expected": { "action": "continue" }
    }
  ]
}
`,
			readme: "# {{name}}\n\n" +
				"Rejects requests whose text contains a blocked keyword, and can set a header on the requests sent upstream.\n\n" +
				"## Setup\n\n" +
				"Add an instance of the plugin to the requests it should check, then fill in its settings:\n\n" +
				"| Setting | What it does |\n| --- | --- |\n" +
				"| Blocked keywords | One per line. A request whose text contains any of them is rejected. Letter case does not matter. |\n" +
				"| Exempt callers | One per line. `group:<name>` exempts a New API user group, `client:<name>` an AstrLink client such as `client:claude_code`. |\n" +
				"| Header name and value | A header set on every upstream request. Leave the name empty to set none. |\n\n" +
				"## What callers see\n\n" +
				"A rejected request gets HTTP 400 with error code `request_rejected` and the message `This request contains a blocked keyword (code: blocked.keyword)`. It is not sent upstream.\n" +
				"The request log records how many keywords matched, never the matched text.\n\n" +
				"## Example\n\n" +
				"With `internal only` blocked, this request is rejected:\n\n" +
				"```bash\ncurl https://gateway.example.com/v1/chat/completions \\\n" +
				"  -H \"Authorization: Bearer sk-...\" \\\n" +
				"  -H \"Content-Type: application/json\" \\\n" +
				"  -d '{\"model\": \"gpt-4o-mini\", \"messages\": [{\"role\": \"user\", \"content\": \"Summarize this internal only memo\"}]}'\n```\n\n" +
				"## Limits\n\n" +
				"- Keywords match the text of the request body, not uploaded files or images.\n" +
				"- The channel's own header override can replace the header this plugin sets.\n",
			changes: "- Reject requests whose text contains one of the keywords you block.\n" +
				"- Exempt New API user groups or AstrLink clients from the check.\n" +
				"- Set a header of your choice on the requests sent upstream.\n",
		},
		"provider": {
			summary: "New API format: a provider part that serves an OpenAI-compatible API over relay routes and drops members the vendor rejects.",
			newAPI:  true,
			plugin: `// {{name}}: serves the chat models of an OpenAI-compatible API, to Chat
// Completions and Claude Messages clients. It is written in New API's
// plugin format (apiVersion 1), which New API and AstrLink both run; the
// contract is the Provider part of
// https://github.com/QuantumNous/new-api/blob/main/docs/plugin-api/v1.md
// Change the address, models and routes below to your vendor's.

export const meta = {
  apiVersion: 1,
  key: "{{key}}",
  name: "{{name}}",
  description: { en: "Chat models of Example's API for Chat Completions and Claude Messages clients" },
  version: "0.1.0",
  // Your name, and url: your GitHub page.
  author: { name: "Your name" },
  baseUrl: "https://api.example.com",
  models: ["example-chat-1"],
  relay: [
    { incoming_path: "/v1/chat/completions", upstream_path: "/v1/chat/completions" },
    { incoming_path: "/v1/messages", upstream_path: "/v1/chat/completions", converter: "anthropic_messages_to_openai_chat_completions" },
    { incoming_path: "/v1/models", upstream_path: "/v1/models" },
  ],
};

// Members of a Chat Completions body the vendor refuses.
const UNSUPPORTED = ["metadata", "store"];

// prepareRequest runs on every JSON body before it is sent, and returns
// effects instead of editing the request.
export function prepareRequest(ctx, request) {
  const body = request.body;
  if (body === null || typeof body !== "object" || Array.isArray(body)) return null;
  const ops = UNSUPPORTED.filter((name) => name in body).map((name) => ({ op: "remove", path: "/" + name }));
  if (ops.length === 0) return null;
  return { effects: [{ type: "json.patch", ops }] };
}
`,
			tests: `{
  "cases": [
    {
      "name": "drops the members the vendor refuses",
      "hook": "prepareRequest",
      "args": [
        { "baseUrl": "https://api.example.com", "path": "/v1/chat/completions", "config": {} },
        {
          "path": "/v1/chat/completions", "upstreamPath": "/v1/chat/completions", "model": "example-chat-1", "stream": false, "headers": {},
          "body": { "model": "example-chat-1", "messages": [{ "role": "user", "content": "hi" }], "store": true, "metadata": { "team": "a" } }
        }
      ],
      "expected": { "effects": [{ "type": "json.patch", "ops": [{ "op": "remove", "path": "/metadata" }, { "op": "remove", "path": "/store" }] }] }
    },
    {
      "name": "sends other bodies as they are",
      "hook": "prepareRequest",
      "args": [
        { "baseUrl": "https://api.example.com", "path": "/v1/messages", "config": {} },
        {
          "path": "/v1/messages", "upstreamPath": "/v1/chat/completions", "model": "example-chat-1", "stream": true, "headers": {},
          "body": { "model": "example-chat-1", "messages": [{ "role": "user", "content": "hi" }], "stream": true }
        }
      ],
      "expected": null
    }
  ]
}
`,
			readme: "# {{name}}\n\n" +
				"Serves the chat models of Example's API to Chat Completions and Claude Messages clients.\n\n" +
				"## Channel setup\n\n" +
				"Choose the Task Plugin channel type, pick this plugin and paste the API key from Example's console.\n" +
				"The default API address is `https://api.example.com`. Change it only for a regional endpoint.\n\n" +
				"## Models and endpoints\n\n" +
				"| Endpoint | Models |\n| --- | --- |\n" +
				"| `POST /v1/chat/completions` | `example-chat-1` |\n" +
				"| `POST /v1/messages` | `example-chat-1` |\n\n" +
				"Claude Messages requests are converted to Chat Completions before they are sent.\n\n" +
				"## Billing\n\n" +
				"Requests are billed by the input and output tokens the upstream reports, at each model's token prices.\n\n" +
				"## Example\n\n" +
				"```bash\ncurl https://gateway.example.com/v1/chat/completions \\\n" +
				"  -H \"Authorization: Bearer sk-...\" \\\n" +
				"  -H \"Content-Type: application/json\" \\\n" +
				"  -d '{\"model\": \"example-chat-1\", \"messages\": [{\"role\": \"user\", \"content\": \"Hello\"}]}'\n```\n\n" +
				"## Limits\n\n" +
				"- The `metadata` and `store` members of a request are not sent, because Example refuses them.\n",
			changes: "- Use Example's `example-chat-1` model from Chat Completions and Claude Messages clients.\n" +
				"- List Example's models through `GET /v1/models`.\n",
		},
		"task": {
			summary: "New API format: a task part that submits a job to a vendor and polls it until it finishes.",
			newAPI:  true,
			plugin: `// {{name}}: submits video jobs to an API and polls them until they finish.
// It is written in New API's plugin format (apiVersion 1), which New API and
// AstrLink both run; the contract is
// https://github.com/QuantumNous/new-api/blob/main/docs/plugin-api/v1.md
// Change the address, models, routes and shapes below to your vendor's.

export const meta = {
  apiVersion: 1,
  key: "{{key}}",
  name: "{{name}}",
  description: { en: "Video jobs on Example's API, submitted and polled until they finish" },
  version: "0.1.0",
  // Your name, and url: your GitHub page.
  author: { name: "Your name" },
  baseUrl: "https://api.example.com",
  models: ["example-video-1"],
  fetchMode: "per_task",
  routes: [
    { method: "POST", path: "/{{key}}/v1/jobs", type: "submit", decode: "createJob", render: "jobCreated" },
    { method: "GET", path: "/{{key}}/v1/jobs/:task_id", type: "query", render: "jobStatus" },
  ],
};

// The gateway's own routes: native turns a client's request into a task,
// and presents the task back in the same format.
export const native = {
  createJob(ctx) {
    if (ctx.body.kind !== "json") throw new Error("JSON body required");
    const body = ctx.body.value;
    if (typeof body.model !== "string" || !body.model) throw new Error("model is required");
    if (typeof body.prompt !== "string" || !body.prompt.trim()) throw new Error("prompt is required");
    return { kind: "submit", model: body.model, requestBody: body };
  },
  jobCreated(ctx, task) {
    return { id: task.task_id, status: task.status };
  },
  jobStatus(ctx, task) {
    return { id: task.task_id, status: task.status, progress: task.progress || "", error: task.fail_reason || "" };
  },
};

export function buildSubmitRequest(ctx) {
  const body = ctx.requestBody;
  return {
    method: "POST",
    url: ctx.baseUrl + "/v1/videos",
    headers: { Authorization: "Bearer " + ctx.apiKey },
    body: { model: ctx.upstreamModel || ctx.model, prompt: body.prompt, seconds: body.seconds || 5 },
  };
}

export function parseSubmitResponse(ctx, response) {
  if (!response.body || !response.body.id) throw new Error("the upstream returned no job id");
  return { taskId: response.body.id };
}

export function buildQueryRequest(ctx) {
  return { url: ctx.baseUrl + "/v1/videos/" + encodeURIComponent(ctx.taskId), headers: { Authorization: "Bearer " + ctx.apiKey } };
}

const STATUSES = { queued: "QUEUED", running: "IN_PROGRESS", succeeded: "SUCCESS", failed: "FAILURE" };

export function parseTaskResult(ctx, body) {
  const status = STATUSES[body.status];
  if (!status) return { status: "UNKNOWN", reason: "unrecognized status: " + String(body.status || "") };
  const result = { status };
  if (typeof body.progress === "number") result.progress = body.progress + "%";
  if (status === "SUCCESS") result.url = body.video_url;
  if (status === "FAILURE") result.reason = (body.error && body.error.message) || "the job failed";
  return result;
}
`,
			tests: `{
  "cases": [
    {
      "name": "accepts a job",
      "hook": "native",
      "member": "createJob",
      "args": [{ "body": { "kind": "json", "value": { "model": "example-video-1", "prompt": "A cat on a windowsill" } } }],
      "expected": { "kind": "submit", "model": "example-video-1", "requestBody": { "model": "example-video-1", "prompt": "A cat on a windowsill" } }
    },
    {
      "name": "needs a prompt",
      "hook": "native",
      "member": "createJob",
      "args": [{ "body": { "kind": "json", "value": { "model": "example-video-1" } } }],
      "expectedError": "prompt is required"
    },
    {
      "name": "builds the upstream request",
      "hook": "buildSubmitRequest",
      "args": [{ "baseUrl": "https://api.example.com", "apiKey": "sk-test", "model": "example-video-1", "upstreamModel": "example-video-1", "requestBody": { "model": "example-video-1", "prompt": "A cat" } }],
      "expected": {
        "method": "POST", "url": "https://api.example.com/v1/videos", "headers": { "Authorization": "Bearer sk-test" },
        "body": { "model": "example-video-1", "prompt": "A cat", "seconds": 5 }
      }
    },
    {
      "name": "reads the job id",
      "hook": "parseSubmitResponse",
      "args": [{}, { "statusCode": 200, "headers": {}, "body": { "id": "job-1" } }],
      "expected": { "taskId": "job-1" }
    },
    {
      "name": "reports progress",
      "hook": "parseTaskResult",
      "args": [{}, { "status": "running", "progress": 40 }],
      "expected": { "status": "IN_PROGRESS", "progress": "40%" }
    },
    {
      "name": "reports the video",
      "hook": "parseTaskResult",
      "args": [{}, { "status": "succeeded", "video_url": "https://cdn.example.com/job-1.mp4" }],
      "expected": { "status": "SUCCESS", "url": "https://cdn.example.com/job-1.mp4" }
    },
    {
      "name": "keeps an unknown status unknown",
      "hook": "parseTaskResult",
      "args": [{}, { "status": "paused" }],
      "expected": { "status": "UNKNOWN", "reason": "unrecognized status: paused" }
    }
  ]
}
`,
			readme: "# {{name}}\n\n" +
				"Generates videos with Example's video models. The gateway submits each job and polls it until it finishes.\n\n" +
				"## Channel setup\n\n" +
				"Choose the Task Plugin channel type, pick this plugin and paste the API key from Example's console.\n" +
				"The default API address is `https://api.example.com`. Change it only for a regional endpoint.\n\n" +
				"## Models and endpoints\n\n" +
				"| Endpoint | Models |\n| --- | --- |\n" +
				"| `POST /{{key}}/v1/jobs` | `example-video-1` |\n\n" +
				"Clients poll `GET /{{key}}/v1/jobs/{task_id}` for the status. A finished job's status links to the video.\n\n" +
				"## Billing\n\n" +
				"Each job is billed at the model's price per request.\n\n" +
				"## Example\n\n" +
				"```bash\ncurl https://gateway.example.com/{{key}}/v1/jobs \\\n" +
				"  -H \"Authorization: Bearer sk-...\" \\\n" +
				"  -H \"Content-Type: application/json\" \\\n" +
				"  -d '{\"model\": \"example-video-1\", \"prompt\": \"A cat on a windowsill\", \"seconds\": 5}'\n```\n\n" +
				"## Limits\n\n" +
				"- A request needs a `prompt`. Jobs last 5 seconds unless the request sets `seconds`.\n",
			changes: "- Generate videos with Example's `example-video-1` model through `POST /{{key}}/v1/jobs`.\n" +
				"- Poll a job's status and get its video through `GET /{{key}}/v1/jobs/{task_id}`.\n",
		},
	}
}
