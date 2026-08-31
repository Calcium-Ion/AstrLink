# AstrLink Desktop

Tauri 2 owns the desktop process boundary; `astrlink-core` remains a standalone
Go sidecar. React invokes only fixed Core lifecycle, Service, local
access-token, singleton privacy-policy, and privacy-model catalog commands; it
never receives shell execution capability or the per-start control token.

## Local development

Prerequisites: Bun 1.3.14+, Go, Rust, and the platform dependencies required by
Tauri. Bun manages dependencies, scripts, and the JavaScript tool runtime.

From the repository root, one-shot local desktop:

```sh
make dev
```

Or from this directory:

```sh
bun install --frozen-lockfile
bun run typecheck
bun run test
bun run desktop:dev
```

Type checking uses the stable TypeScript 7 native Go compiler. Its preview
command was named `tsgo`; the final TypeScript 7 package publishes it as `tsc`.
Rsbuild provides the development server and production bundle. Vitest remains
the test runner and is not the application bundler.
Local `bunfig.toml` forces Node-shebang CLIs such as Rsbuild, Vitest, and the
Tauri JavaScript wrapper to execute with Bun on every platform.

`desktop:dev` builds the native Go Core for the Rust host target before Tauri
starts. `desktop:build` performs the equivalent production build.
`bun run dev` does not typecheck; use `bun run typecheck` or
`bun run dev:typecheck` for a watch checker. Frontend file changes are
rebuilt by Rsbuild. The debug Rust host polls `/__astrlink_build` and
reloads the WebView; look for
`[astrlink dev] frontend rebuild #N -> reloading webview` on the
`make dev` terminal. A debug tray item **重新加载界面** forces the same
reload. Failures of that poller also go to stderr so a silent stall is
visible.
Core operational diagnostics are written to stderr and forwarded to the
terminal running `desktop:dev`; they are not sent to the WebView console.
Privacy-model download messages contain only the model ID, catalog asset path,
attempt number, and a sanitized reason such as `dns`, `http_503`, `body_read`,
or `sha256`.

## Sidecar handshake

The Core binds inference to the saved `127.0.0.1:<port>` (`8317` by default)
and always chooses a private ephemeral loopback control port. It writes exactly
one `ready` JSON line to stdout. Rust
validates that signal, only accepts loopback endpoint URLs, then reads:

- `/control/v1/health`
- `/control/v1/version`
- `/control/v1/capabilities`

Version fields are checked across all three handshake documents before the UI
reports Core as ready. The settings page shows the saved and active inference
ports separately because a saved change takes effect only after restart. A port
that cannot be bound produces an explicit startup error.

Core can start automatically with the desktop or be started, stopped, and
restarted manually. Unexpected exits use bounded exponential recovery delays of
1, 2, 4, 8, and 16 seconds. The attempt count and pending delay are visible;
recovery stops after five attempts and resets only after 30 seconds of stable
readiness. Manual stop and application exit cancel stale recovery generations.
Stop, restart, and Quit first request token-authenticated Core shutdown so HTTP
servers and SQLite drain cleanly; a bounded force-stop remains only as fallback.

RelayKit is represented only by the Core capability response. Alpha uses native
and delegated passthrough and does not perform local format conversion.

## Desktop preferences and OS integration

Desktop preferences are strict typed JSON in the platform application-config
directory. Missing files use safe defaults. Malformed, invalid, and unreadable
files are reported in Settings while safe defaults are used; failed writes do
not mutate the in-memory saved state. Updates use a same-directory temporary
file, file sync, atomic rename, and directory sync on Unix.

The tray provides **显示 AstrLink** and **退出**. Closing the main window either
hides it to the tray or exits according to the saved preference; explicit tray
Quit always bypasses hide-on-close and performs bounded Core shutdown. A second
application launch restores, unminimizes, and focuses the existing window.

开机启动 uses Tauri's supported OS integration on macOS, Windows, and Linux.
Settings reads the real registration state, reconciles it before persisting the
requested value, and reports registration/query failures instead of claiming
success. It does not use a shell command or a WebView-only preference.

## Service UI boundary

The ordinary-user path starts with new-api and separate OpenAI/Codex, Claude,
and Gemini subscription presets. Selecting the product attached to the API key
atomically supplies authentication and a conservative capability set, leaving
only the name, API address, and API key in the main form. Optional
cross-protocol bridges and other technical overrides are folded under advanced
settings.

Profiles are UI-only templates. Existing Service records are hydrated from the
complete wire document, including unknown protocols, models, and duplicate
protocol IDs with different modes. Secrets stay write-only and isolated per
profile draft; blank edit fields keep the stored credential unless the service
identity changes.

## Local access tokens

The desktop starts persistent Core with one per-start control token delivered
over stdin. That value is used only by Rust for the control plane. Inference
clients instead use independently managed persistent `astr_…` tokens from the
访问令牌 page. React receives a token value only after an explicit create or
reveal action, hides it on page/session changes, and never receives token hashes.
The overview and token rows reserve usage-summary positions without issuing
statistics requests until usage storage is implemented.

## Local privacy models

The safety page keeps Regex available without model assets and exposes a
versioned built-in model catalog, an advanced public Hugging Face probe, and a
local import flow for an already-mounted model package. Local import accepts an
absolute native directory or `.onnx` file path rather than `smb://` or another
URI; network shares must first be mounted by the operating system. Selecting a
file probes only that model variant and its required companion assets.
Users choose one CPU model/quantization installation for the global policy;
several immutable installations may coexist. The WebView receives catalog,
persisted source/license/language details, progress, compatibility, and
sanitized error metadata only. Label mapping is handled in a bounded modal so
large label sets do not expand the main workspace. The WebView never downloads
weights directly and cannot pass arbitrary URLs or executable repository code
to Core. Core returns no source-directory path and copies the selected,
content-pinned assets into AstrLink's private model storage before execution.

Enabling or switching to a model shows its disk/RAM estimate and requires an
explicit confirmation. Model loading remains lazy in the trusted Rust worker.
Frontend tests use mocked catalog/install snapshots and never access Hugging
Face or execute model inference.
