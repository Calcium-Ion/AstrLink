# AstrLink

AstrLink is a Responses-first local AI API gateway and desktop client for the
new-api ecosystem and OpenAI/Codex, Claude, and Gemini API subscriptions. Its default desktop
flow is built for ordinary users: choose a service type, enter the API address
and API key, and let AstrLink configure authentication and protocol
capabilities. Vendor-native APIs remain optional direct connections.

## Alpha execution boundary

- `native`: preserve the incoming protocol and relay it to an upstream that
  explicitly supports the same protocol.
- `delegated`: preserve the public protocol and let a compatible upstream
  gateway—such as new-api or a single-key-verified subscription service—route
  the request and perform any required upstream conversion.
- `relaykit`: reserved in stable contracts for a later Beta. The Alpha runtime
  exposes no conversion edges and must never select this plan type.

There is no implicit Responses-to-Chat downgrade.

## Endpoint experience

- `new-api` is the default profile and enables all eight Alpha capabilities as
  delegated gateway paths.
- Subscription profiles follow the product attached to one API key:
  OpenAI/Codex, Claude, or Gemini. Each preset declares only its conservative,
  code-backed protocol set; optional cross-protocol bridges stay advanced.
- Authentication, per-capability mode/streaming/models, and enablement live in
  folded advanced settings.
- Existing Endpoint documents are edited losslessly; presets do not change the
  stable wire contract or become a second persisted source of truth.

## Repository layout

```text
apps/desktop/  Tauri 2 + React 19 desktop application (Bun, TypeScript 7, Rsbuild)
contracts/     Stable control API and capability schemas
core/          Standalone Go sidecar
docs/          Architecture decisions and project notes
```

## Development

Requirements: Go 1.23+, Bun 1.3.14+, Rust stable, and the
platform-specific [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/).
Bun owns frontend dependency installation, script orchestration, and the
JavaScript tool runtime.

```sh
make core-check
make core-race
make contracts-check
make contracts-race
make desktop-install
make desktop-check
make desktop-rust-check
```

`make check` runs every check after desktop dependencies have been installed.
Frontend type checking uses the stable TypeScript 7 native Go compiler (known as
`tsgo` during preview and published as `tsc`), while Rsbuild owns development and
production bundles. `apps/desktop/bun.lock` is the only frontend lockfile.
The Tauri bundle additionally needs a target-suffixed `astrlink-core` binary in
`apps/desktop/src-tauri/binaries`; CI prepares it before packaging.

## Status

M0 contract and lifecycle hardening is complete. The first M1 vertical slice
recognizes every Alpha text route and contains a tested native HTTP/SSE
transport. Persistent Endpoint CRUD, the dedicated plaintext local
`endpoint_credentials` table, authenticated desktop control proxy, and upstream
configuration UI are now implemented. The first M2 slice adds preset-first
new-api and per-product subscription setup, full multi-capability editing, isolated per-profile
drafts, and credential-safe minimal PATCH behavior. Restrictive file permissions are baseline
hygiene and cross-account isolation, not protection from other processes running
as the same user; system keyrings remain optional future adapters. The inference
plane now installs the SQLite native-capability resolver only in persistent
desktop mode, behind persistent multi-token authentication plus strict
Host/Origin/CORS controls. Token metadata and SHA-256 hashes are isolated from
revealable token secrets; the per-start control token is still used only for
desktop-to-Core control traffic. Headless Core without persistent startup
dependencies continues to fail closed with `endpoint_resolver_unavailable`.
Routing now consumes validated persisted Route rules with deterministic
route/target priority and lexical tie-breaks, while retaining the original
native-by-Endpoint-ID then delegated-by-Endpoint-ID behavior when no rule
matches. Automatic candidates use an in-memory per-Endpoint circuit breaker
with timed half-open probes; a one-Endpoint Route is an explicit pin and remains
usable while its automatic circuit is open. Failures can switch across at most
three candidates only while the downstream response remains uncommitted and
the request body is safely replayable; this includes a fully buffered
non-stream response that fails before copy/restore completes. A status, flush,
or response byte permanently disables failover for that request. Missing
capability errors name the required protocol, mode, and streaming support; no
Responses request is silently downgraded to Chat.
Model discovery listings (`/v1/models`, `/v1beta/models`) no longer relay a
single upstream: they fan out to every enabled endpoint declaring the matching
models capability with a small fixed concurrency bound, per-endpoint timeout,
and an 8 MiB per-response cap. Conflicting public model IDs deterministically
keep the entry from the endpoint that wins routing order, output is sorted by
model ID, protocol envelopes stay wire-correct without endpoint attribution,
open circuits are skipped, and the aggregate is served from whichever capable
endpoints succeed; a structured upstream error is returned only when all fail.
Model aliases (ADR 0006) are ordinary Routes whose `match.model` is the public
name and whose targets set optional `upstream_model`: the gateway rewrites the
request model for that target, restores the public alias in response
model-identity members (including SSE and upstream error bodies) under exact
value equality, and lists alias names in discovery without disclosing the
target endpoint or upstream model. Always-on request metadata records and
protocol usage extraction (ADR 0007, M3 scope pulled forward) persist one
`RequestRecord` per classified inference request after the response completes
and expose list/get/delete/purge on the control API. Opt-in encrypted body
audit (ADR 0007 sub-slice 5b) adds privileged audit-settings, AES-GCM blobs,
and authenticated `GET .../audit` decryption; capture defaults off. The
request-side privacy policy offers
high-confidence Regex filtering or one explicitly selected local ONNX
token-classification installation. AstrLink ships only a pinned metadata
catalog, never model weights; users may install multiple public Hugging Face
model/quantization variants while one is active. Automated checks use synthetic
fixtures and reject production model artifacts. Route CRUD UI and retry-policy
configuration remain later work. Do not expose the development build beyond
loopback.
