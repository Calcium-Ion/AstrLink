---
name: astrlink-debug
description:
  Debug AstrLink local gateway requests using request records, sessions, and
  trajectory events. Use when an IDE or agent call through AstrLink fails,
  routes to the wrong model, is blocked by privacy policy, retries, or
  astrlink/auto classification looks wrong.
---

# AstrLink request-record debugging

AstrLink is the local API gateway. When a client request through `127.0.0.1`
misbehaves, inspect **request records** instead of guessing from the model error
text.

## Use the AstrLink CLI

AstrLink installed a read-only command-line tool at `{{ASTRLINK_CLI}}`. Run it
from your shell tool. It only reads; the exceptions are `raw-audit`, which files
an approval request that the user decides in the AstrLink desktop, and
`raw-revoke`, which gives up a raw access grant. Use it instead of calling the
Control API with curl or reading SQLite.

Run each command exactly as shown, as the whole shell command: no pipes, `&&`,
`;`, redirections, `cd`, or environment prefixes. The host's allow rules match
the plain command, so a combined command asks the user again or runs inside a
sandbox that blocks AstrLink. Results are JSON on stdout; errors go to stderr
with a non-zero exit code. `{{ASTRLINK_CLI}} help <command>` lists a command's
flags.

1. `{{ASTRLINK_CLI}} audit-settings` — see whether bodies are being captured.
2. `{{ASTRLINK_CLI}} sessions` or `{{ASTRLINK_CLI}} requests` — filter with
   `--status`, `--protocol`, `--service-id`, `--from`, `--to`, and page with
   `--limit` and `--cursor`. `{{ASTRLINK_CLI}} search <text>` takes the same
   filters and matches the record's short input preview.
3. `{{ASTRLINK_CLI}} explain <id>` — one call for a record, its retry attempts,
   and whether bodies were captured. `{{ASTRLINK_CLI}} request <id>` and
   `{{ASTRLINK_CLI}} session <id>` return the raw metadata and `events[]`.
4. `{{ASTRLINK_CLI}} children <id>` — inspect failed retries under a root
   record.
5. `{{ASTRLINK_CLI}} audit <id>` — the shareable bodies, only if the user
   already enabled capture for that request. See [Bodies](#bodies) for withheld
   parts.
6. `{{ASTRLINK_CLI}} routing` — model redirects, failover and retry settings,
   channel stickiness, and identity enforcement.
7. `{{ASTRLINK_CLI}} services` / `{{ASTRLINK_CLI}} service <id>` — configured
   providers, their models and capabilities, subscription state, and a summary
   of recent requests and risk events. Credentials, proxy addresses, and URL
   paths are never returned; `base_origin` is only scheme, host, and port.
8. `{{ASTRLINK_CLI}} privacy` — privacy policy settings. Allowlist entries and
   custom regex rules are reported as counts and types, never their values.

If a command fails before it reaches AstrLink:

- **Command not found** — ask the user to open the AstrLink desktop, go to
  Settings → Agent tools, and install the tools for this host.
- **`AstrLink control session unavailable`** — the desktop gateway is not
  running. Ask the user to start it and wait until it is Ready.
- **`connect: operation not permitted`** — the host's sandbox blocked AstrLink's
  local socket. Ask the user to reinstall the agent tools for this host from
  Settings → Agent tools and start a new session, or to approve running the
  command outside the sandbox.

Do not work around a failure by searching sidecar memory or process arguments,
copying the socket, or reading `astrlink.db` or the control session file.

## When to look

- Gateway 4xx/5xx, timeouts, or cancellations
- Wrong upstream model or service — check `model_redirect` on the record and
  `{{ASTRLINK_CLI}} routing` first; a redirect rule may have replaced the model
- `codex-auto-review` fails (for example `missing_protocol_capability`) — it is
  Codex's auto-review model, which third-party providers rarely list; Codex then
  denies the pending action. Suggest the featured redirect in Routing → Model
  redirects (OpenAI serves it with `gpt-5.6-luna`)
- Privacy policy `block` / `warn` / unexpected redaction. For how a model should
  handle the placeholders themselves, see the `redaction-placeholders` skill
- Retry loops or a child attempt that failed after a root
- `astrlink/auto` picked an unexpected category or fallback

## How to read a record

Metadata is always present. Treat these fields as the source of truth:

- `status`: `pending` | `succeeded` | `failed` | `cancelled` | `blocked`
- `requested_model` (the model the client sent), `input_protocol`, `streaming`
- `model_redirect` `{from, to}`: a routing-settings redirect replaced
  `requested_model` with `to` for routing and in the upstream request; absent
  when no rule matched. Responses pass through unchanged, so they name the
  upstream model rather than `from`
- `recovery.upstream_model`: the model finally sent upstream
- `service_id`, `route_id`, `plan`
- `routing_decision` `{selected, skipped[]}`: why routing chose `service_id`.
  `selected` is `priority`, `session_binding`, `response_affinity`,
  `websocket_connection`, or `failover`; `skipped` lists the higher-priority
  providers excluded before any attempt, each with a `reason` such as
  `disabled`, `model_not_listed`, or `circuit_open`. Without `selected`, no
  provider could serve the call and `skipped` names every exclusion. Absent on
  older records, model discovery, and calls still choosing a provider
- `error` (transport failures include the unwrapped cause — host/URL/IP may be
  present; credentials are redacted; no bodies or header maps)
- `input_preview` (short, secrets stripped)
- `privacy_restore` (hit counts only)
- `privacy_ignored` `{kind, reason, count}[]`: matches the privacy policy left
  in the request because they were `allowlisted`, `low_confidence`, of a
  `kind_disabled`, or an `astrlink_identifier` (an ID AstrLink generated, such
  as a local access token ID or request ID, which grants no access). Use it to
  explain why a value was not replaced; it never carries the value. Absent on
  older records
- `confidence` `{min, max, threshold}` on `privacy_findings` and
  `privacy_ignored` entries: the local model's confidence range and the
  threshold applied. Absent under the regex detector
- `events[]` trajectory — see
  [references/trajectory.md](references/trajectory.md)

You may quote the transport error on the record, including host or IP, so the
operator can see a disconnect, DNS failure, or refused connection. Do not repeat
credentials, `sk-` tokens, or Authorization material if a redaction marker was
missed. Do not invent an upstream origin that is not already on the record.

## Bodies

Request/response bodies are **off by default**. `{{ASTRLINK_CLI}} audit <id>`
returns `bodies_captured: false` unless the user enabled body audit in the
AstrLink desktop and acknowledged the risk. Do not try to turn capture on from
the agent. Ask the user to enable it in the app if the prompt/response text is
required.

Captured bodies come in two levels:

- **Shareable** (`content_view: "shareable"`) — what `audit` returns: parts the
  privacy policy cleared or redacted, such as the request that was sent upstream
  with placeholders. `privacy_findings` lists what was found by kind and JSON
  path, never the values.
- **Raw** — the client's original request, restored responses, and parts that
  were never inspected. `audit` withholds them with a `reason` and
  `raw_available`.

Work from the shareable parts first. Ask for raw parts only when a withheld part
has `raw_available: true` and the shareable parts cannot answer the question:

1. Tell the user which request you want to read raw and why. Raw content enters
   your context and is sent to the model provider you use.
2. Run `{{ASTRLINK_CLI}} raw-audit <id> --reason "<why>" --agent "<name>"` with
   the reason you gave the user and your product name. Give the command a long
   timeout in your shell tool: it waits up to 10 minutes for the decision. If
   your tool allows less, pass `--wait` just under its limit, for example
   `--wait 110s` for a 2-minute limit.
3. Ask the user to approve it in AstrLink's approval window while the command
   waits. Only the user can approve; do not try to click, script, or otherwise
   complete the approval yourself.
4. Once approved, the command prints the audit with `content_view: "raw"`. A
   one-time approval (`once`) allows one read of that request and prints no
   token. A timed approval (`window_5m` or `window_1h`) lets you read the raw
   parts of every request; the output then carries `raw_grant` with its
   `grant_token`. If the wait ends first, ask the user whether they still want
   to approve before running it again.

These rules for a `grant_token` are mandatory:

- Use the same token for the whole investigation: every later raw read, on any
  request, is `{{ASTRLINK_CLI}} raw-audit <id> --grant <token>`. Do not file a
  new request while the token works, and do not revoke it mid-investigation.
- You may give the token to subagents working on the same investigation, only
  through their task prompt. Subagents read with `--grant` and never run
  `raw-revoke`.
- Only the agent that started the investigation revokes the token, exactly once,
  with `{{ASTRLINK_CLI}} raw-revoke --grant <token>`, when the whole
  investigation ends: it is finished, it cannot continue or it failed, or the
  user asked you to stop. Revoke before you give your final result.
- Never write the token into files, code, notes, or commits, and never carry it
  into an unrelated later task.
- A one-time approval has no token and needs no revoke.
- If `raw-audit --grant` fails with `raw_grant_invalid` (the token expired, was
  revoked, or is unknown), request access again with
  `{{ASTRLINK_CLI}} raw-audit <id> --reason "<why>" --agent "<name>"`.

If a withheld part has `raw_available: false`, or `raw-audit` fails with
`raw_access_disabled`, `raw_access_unavailable`, or `raw_access_denied`, do not
ask again unless the user asks you to; continue with the shareable parts.

## Access level

The CLI connects through AstrLink's local control socket (or, on Windows, the
session token in `~/.astrlink/control-session.json`). Both carry **observer**
access only: reads succeed, and every setting change, purge, delete, or token
reveal is refused with `forbidden`. Do not try to use the socket or session
token to change settings; ask the user to make the change in the AstrLink
desktop.

## What not to do

- Do not call purge, delete, or change audit or routing settings.
- Do not disable the privacy policy to “make it work”.
- Do not put control tokens, access tokens, or upstream keys into chat, files,
  or host configuration.
- Do not read, copy, or open `astrlink.db*`, the AstrLink data directory, or
  `~/.astrlink/control-session.json`, and do not run `sqlite3` on them. Tell the
  user: "AstrLink's local database is not an agent interface; I will use the
  AstrLink CLI instead."
