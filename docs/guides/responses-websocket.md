# Responses WebSocket

AstrLink accepts authenticated WebSocket upgrades on `GET /v1/responses`.
Each `response.create` runs through the same model selection, upstream
credentials, privacy inspection/restoration, usage scanning, request records,
and response-model alias restoration as HTTP Responses.

## Channel configuration

`responses_websocket_enabled` is an optional service/create/patch boolean.
Omitted values default to **true for Codex subscriptions**, including existing
stored API providers, and **false for other providers**. Explicit false persists and
overrides the Codex default. The switch is independent of ordinary HTTP support.

Only enabled API providers declaring streaming `openai.responses` without local
protocol conversion can serve WebSocket requests. The provider list groups the
WebSocket indicator with model/API capabilities only when enabled. Hover or
focus the small icon for its meaning. The switch lives in connection settings
and saves with the form; the list does not change this setting.

## Connection behavior

Connect to `ws://127.0.0.1:<inference-port>/v1/responses` with the local access
token in the Authorization header. Browser origins and token query parameters
remain forbidden. The upstream receives its own credentials, never the local
token or cookies, and uses the gateway's configured outbound proxy.

```json
{"type":"response.create","model":"your-model","input":"Hello","store":false}
```

Events arrive as JSON WebSocket text messages. Continue on the same connection
with another `response.create`, the prior `previous_response_id`, and new input.
`generate:false` warmups, sequential `stream_id` values, and `response.cancel`
are forwarded. The legacy `{type:"response.create", response:{...}}` form is
also accepted. HTTP-only `stream`, `stream_options`, and `background` fields
are omitted upstream.

A connection supports **one active response at a time**, matching the reference
new-api relay. A concurrent create receives a 409 error event; multiplexed
parallel streams and mid-turn steering are not implemented. Once connected,
the API provider and model are pinned. Changing the model, API provider, or credential
requires a new connection. Every turn rechecks local authentication and current
API provider eligibility, so disabling the switch or revoking a token blocks the
next turn.

Handshake failures use the existing retry/failover policy. Once a create is
sent, AstrLink never replays it. Disconnects cancel the upstream; interrupted
and failed generations are recorded as failures. The upstream connection stays
open between successful turns and continues processing ping/close frames.
Incoming messages follow the request-size setting, with a 128 MiB safety bound
when that setting is unlimited. Individual upstream events are capped at
128 MiB.

Protocol reference: [OpenAI WebSocket mode](https://developers.openai.com/api/docs/guides/websocket-mode).

## Verification

Local WebSocket integration tests cover two-turn connection reuse, continuation,
privacy redaction/restoration, public model aliases, authentication on each turn,
API provider opt-out, conversion exclusion, cancellation, concurrent admission,
disconnect cleanup, Codex paths/defaults, failed-generation records, handshake
failover, no replay after dispatch, and message limits. Control API tests cover
defaults, explicit false, patch validation and storage round trips.

The desktop preview uses the shared application shell and macOS title-bar
spacing with mock provider data. List content heights: 498 px at 1280×720,
378 px at 1024×600, and 480 px at 600×720; no document horizontal overflow.
No live paid upstream or real OAuth account was used for verification.
