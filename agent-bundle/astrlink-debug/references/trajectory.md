# Request trajectory

`events[]` on a request record is the gateway pipeline. Read it in order.

| kind | Meaning |
| --- | --- |
| `accepted` | Local inference accepted the request and assigned an id |
| `privacy` | Request privacy policy ran (`allow`, `warn`, `block`, or `redact`) |
| `routed` | Route / `astrlink/auto` category and target were chosen |
| `upstream` | The selected service was invoked |
| `restore` | Privacy placeholders were restored on the way back |
| `completed` | Terminal status written (`succeeded`, `failed`, `cancelled`, `blocked`). Failures use `error.code ·` the unwrapped transport cause (host/URL allowed; credentials redacted). |

Retries appear as **child** records (`parent_request_id` set). `get_request_children` lists them. The root keeps `child_count` and the successful or last-failed outcome.

Useful session fields:

- `session_id` groups requests from one client conversation
- `turn_index` is the 1-based **user turn**. Every model call of one agent
  loop (tool call → tool result → next call) shares the same value, so a
  session summary reads `1 turn · 8 calls`. Harness text replayed as
  `role=user` (`<system-reminder>` / `<skill>` blocks, compaction
  summaries) is not a turn. `null` means the protocol has no user turns
  (completions) or the row predates linking.
- `session_link` tells how the record joined its session; `null` means it
  started the session. `kind` is one of:
  - `explicit` — the client named the conversation (`previous_response_id`,
    `conversation`, `metadata.session_id`, Claude Code session,
    `prompt_cache_key`, Anthropic `container`, Gemini `cachedContent`).
    Matched across tokens.
  - `echo_id` — the request replayed an opaque id an earlier response
    produced (tool call ids, Responses item ids, Gemini thought signatures).
    Matched only within the same access token and a 7-day window.
  - `fingerprint` — the request replayed the last assistant reply verbatim;
    linked by a keyed digest of the normalized text (≥ 32 runes). Same
    scope as `echo_id`.
- `cursors[]` are the typed values stored for the record (`kind`,
  `direction` `in`/`out`, `value`). `out` rows are what this record's
  response produced (its output id, minted tool call ids, reply
  fingerprint) and are what later requests link to. `in` rows are only the
  explicit cursors the request named; replayed tool ids and reply digests
  are lookup keys and are not stored, so look at `session_link` for the one
  that matched. Fingerprint values are HMAC digests and cannot be reversed
  to text.
- `previous_response_id` / `output_response_id` are the legacy protocol
  cursor columns; `previous_response_id` holds whichever explicit cursor the
  request named.
- `input_preview` is the newest visible user text of that request (redacted
  and clamped); the session title uses the first record's preview.
- `audit` on the record is flags only (what was captured), not the ciphertext

If two requests that clearly belong together landed in different sessions,
check `session_link` on the second: no explicit cursor, a low-entropy tool
call id (for example `call_0`), a reply shorter than 32 runes, a different
access token, or a client that compacted the history all prevent linking.
That is expected, not a gateway fault.

If `status` is `blocked`, start with the `privacy` event and `privacy_restore` counts. If the model name looks wrong, start with `routed` and `requested_model`. If the client saw a 5xx after a delay, compare root `events` with child retries.
