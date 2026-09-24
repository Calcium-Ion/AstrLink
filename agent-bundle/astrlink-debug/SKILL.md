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

## Use the MCP tools first

The `astrlink` MCP server is a **local stdio** process (Cursor, Claude Code,
Codex, Grok Build, or any other host). It is read-only. Prefer it over curling
Control API or reading SQLite.

It is **not** a remote OAuth server. Never call `mcp_auth`, never click
Authenticate / Sign in / login for `astrlink`. Hosts sometimes expose that stub
when the stdio handshake failed; authenticating cannot fix a local process.

1. `get_audit_settings` — see whether bodies are being captured.
2. `list_request_sessions` or `list_request_records` — filter with `status`,
   `protocol`, `service_id`, `from`, `to`.
3. `get_request_record` / `get_request_session` — read metadata and `events[]`.
4. `get_request_children` — inspect failed retries under a root record.
5. `get_request_audit` — bodies only if the user already enabled capture for
   that request.
6. `get_routing_settings` — model redirects, failover and retry settings,
   channel stickiness, and identity enforcement.

If the only visible tool is `mcp_auth`, or the server is loading / error /
disconnected:

1. Ask the user to open the AstrLink desktop and wait until the gateway is
   Ready.
2. If the eight read-only tools still do not appear, ask them to open Settings →
   Agent tools, reinstall skill + MCP, then start a **new** agent session in
   that host.
3. If an authenticate / login dialog appears for `astrlink`, tell the user to
   Skip or dismiss it.

If the tools are listed but a call says the control session is unavailable, the
desktop gateway is not running. Ask the user to start it. Do not search sidecar
memory, process arguments, or `astrlink.db`.

## When to look

- Gateway 4xx/5xx, timeouts, or cancellations
- Wrong upstream model or service — check `model_redirect` on the record and
  `get_routing_settings` first; a redirect rule may have replaced the model
- `codex-auto-review` fails (for example `missing_protocol_capability`) — it is
  Codex's auto-review model, which third-party providers rarely list; Codex then
  denies the pending action. Suggest the featured redirect in Routing → Model
  redirects (OpenAI serves it with `gpt-5.6-luna`)
- Privacy policy `block` / `warn` / unexpected redaction
- Retry loops or a child attempt that failed after a root
- `astrlink/auto` picked an unexpected category or fallback

## How to read a record

Metadata is always present. Treat these fields as the source of truth:

- `status`: `pending` | `succeeded` | `failed` | `cancelled` | `blocked`
- `requested_model` (the model the client sent), `input_protocol`, `streaming`
- `model_redirect` `{from, to}`: a routing-settings redirect replaced
  `requested_model` with `to` for routing; absent when no rule matched
- `recovery.upstream_model`: the model finally sent upstream
- `service_id`, `route_id`, `plan`
- `error` (transport failures include the unwrapped cause — host/URL/IP may be
  present; credentials are redacted; no bodies or header maps)
- `input_preview` (short, secrets stripped)
- `privacy_restore` (hit counts only)
- `events[]` trajectory — see
  [references/trajectory.md](references/trajectory.md)

You may quote the transport error on the record, including host or IP, so the
operator can see a disconnect, DNS failure, or refused connection. Do not repeat
credentials, `sk-` tokens, or Authorization material if a redaction marker was
missed. Do not invent an upstream origin that is not already on the record.

## Bodies

Request/response bodies are **off by default**. `get_request_audit` returns
`bodies_captured: false` unless the user enabled body audit in the AstrLink
desktop and acknowledged the risk. Do not try to turn capture on from the agent.
Ask the user to enable it in the app if the prompt/response text is required.

## What not to do

- Do not call `mcp_auth` or complete a host login flow for the local `astrlink`
  server.
- Do not call purge, delete, or change audit or routing settings.
- Do not disable the privacy policy to “make it work”.
- Do not put control tokens, access tokens, or upstream keys into chat, files,
  or MCP config.
