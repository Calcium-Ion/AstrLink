---
name: astrlink-debug
description: Debug AstrLink local gateway requests using request records, sessions, and trajectory events. Use when an IDE or agent call through AstrLink fails, routes to the wrong model, is blocked by privacy policy, retries, or astrlink/auto classification looks wrong.
---

# AstrLink request-record debugging

AstrLink is the local API gateway. When a client request through `127.0.0.1` misbehaves, inspect **request records** instead of guessing from the model error text.

## Use the MCP tools first

The `astrlink` MCP server is read-only. Prefer it over curling Control API or reading SQLite.

1. `get_audit_settings` — see whether bodies are being captured.
2. `list_request_sessions` or `list_request_records` — filter with `status`, `protocol`, `service_id`, `from`, `to`.
3. `get_request_record` / `get_request_session` — read metadata and `events[]`.
4. `get_request_children` — inspect failed retries under a root record.
5. `get_request_audit` — bodies only if the user already enabled capture for that request.

If MCP tools are missing, ask the user to open AstrLink → Settings → Agent debugging and install the skill + MCP, then start a **new** agent session. Do not search sidecar memory, process arguments, or `astrlink.db`.

## When to look

- Gateway 4xx/5xx, timeouts, or cancellations
- Wrong upstream model or service
- Privacy policy `block` / `warn` / unexpected redaction
- Retry loops or a child attempt that failed after a root
- `astrlink/auto` picked an unexpected category or fallback

## How to read a record

Metadata is always present. Treat these fields as the source of truth:

- `status`: `pending` | `succeeded` | `failed` | `cancelled` | `blocked`
- `requested_model`, `input_protocol`, `streaming`
- `service_id`, `route_id`, `plan`
- `error` (sanitized; no URLs, headers, or bodies)
- `input_preview` (short, secrets stripped)
- `privacy_restore` (hit counts only)
- `events[]` trajectory — see [references/trajectory.md](references/trajectory.md)

Do not tell the user the real upstream origin or credentials. AstrLink hides those on purpose.

## Bodies

Request/response bodies are **off by default**. `get_request_audit` returns `bodies_captured: false` unless the user enabled body audit in the AstrLink desktop and acknowledged the risk. Do not try to turn capture on from the agent. Ask the user to enable it in the app if the prompt/response text is required.

## What not to do

- Do not call purge, delete, or change audit settings.
- Do not disable the privacy policy to “make it work”.
- Do not put control tokens, access tokens, or upstream keys into chat, files, or MCP config.
