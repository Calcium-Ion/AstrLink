# AstrLink

AstrLink is a Responses-first local AI API gateway for personal AI
subscriptions. Its primary product path is a real Codex subscription
connection through ChatGPT browser sign-in, with AstrLink exposing that
subscription to local IDEs and agents through a stable localhost API. new-api
is the second-priority external gateway path. Other API-key services are
advanced connections, while Claude and Gemini subscription sign-in are
post-Alpha work.

Every configured upstream is one `Service`. A Codex subscription and a new-api
gateway are different service variants with different forms, credentials, and
lifecycle rules—not separate top-level resources. The desktop has one
**API Services** page and one **Add Service** flow; users may add multiple Codex
accounts alongside multiple HTTP services.

## Alpha execution boundary

- `native`: preserve the incoming protocol and relay it to an upstream that
  explicitly supports the same protocol.
- `delegated`: preserve the public protocol and let a compatible upstream
  gateway—such as new-api or another explicitly configured external
  gateway—route the request and perform any required upstream conversion.
- `relaykit`: reserved in stable contracts for a later Beta. The Alpha runtime
  exposes no conversion edges and must never select this plan type.

There is no implicit Responses-to-Chat downgrade.

## Connection priority

- P0: `codex_subscription` Service with user-selected browser OAuth or Device
  Code, secure token storage, refresh, account status, Responses/Compaction,
  models, and logout/revocation.
- P1: `newapi` HTTP Service with Base URL + API key and delegated
  capabilities.
- P2: OpenAI API-key, OpenAI-compatible, and custom HTTP services in advanced
  settings.
- Post-Alpha: Claude and Gemini subscription service variants using their own official
  authorization flows.
- Authentication and per-capability mode/streaming settings remain specific to
  HTTP services. The exact model allow-list is configured once per Service for
  both HTTP and subscription services; an empty list disables inference routing.
- Existing HTTP Service documents are edited losslessly; presets do not change the
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

Start the local desktop (install deps, build sidecars, launch Tauri):

```sh
make dev
```

Run checks:

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

The canonical control-plane resource is now `Service`: `/control/v1/services`
creates, lists, edits, and deletes Codex subscription and HTTP variants. OAuth
sessions are scoped by `service_id`, so multiple Codex accounts can authorize
independently. Routes, execution plans, policies, and request records also use
`service_id`; old `/endpoints` and `/subscription-accounts` control paths are
retired.

HTTP API keys use `service_credentials` with canonical
`local://service/<service_id>` references. Codex OAuth tokens remain isolated in
the OS credential store and never enter React state or ordinary IPC responses.
The Codex data plane injects the account-scoped authorization headers and
supports Responses routing through the selected subscription service.

Browser login uses Codex's official public OAuth client and registered callback
ports 1455/1457; Device Code is also available from the initial add-Service
flow. P0 is not complete until real-account browser and Device Code login,
refresh, Responses/SSE, logout, and re-login E2E have passed. new-api remains
P1; other subscription providers remain Post-Alpha. Do not expose development
builds beyond loopback.
