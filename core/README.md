# AstrLink Core

The Core is an independent Go module and sidecar executable. Run it from this
directory with:

```text
go run ./cmd/astrlink-core
```

The Core is independent when launched without extra flags. Desktop launchers
may pass `--parent-pid <pid>`; on Unix the Core then checks once per second and
begins graceful shutdown if it is reparented. The Windows desktop enforces the
same ownership with a kill-on-close Job Object.

The inference plane binds `127.0.0.1:8317`. The read-only bootstrap control plane
binds an ephemeral loopback port. Once both listeners are ready, stdout emits
exactly one JSON line; operational errors are written to stderr:

```json
{"event":"ready","core_version":"0.1.0-dev","control_api_version":"v1","protocol_contract_version":"v1","inference_url":"http://127.0.0.1:8317","control_url":"http://127.0.0.1:54321"}
```

The control URL always exposes these frozen read-only bootstrap endpoints:

- `GET /control/v1/health`
- `GET /control/v1/version`
- `GET /control/v1/capabilities`

When launched with `--data-dir <path> --control-token-stdin`, Core additionally
exposes authenticated Endpoint, local-access-token, singleton privacy-policy,
and privacy-model operations. One per-start control token is read as a single
line from stdin; it is not accepted through a command-line value or environment
variable. The desktop keeps it out of normal snapshots and proxies fixed
control operations without exposing it to the WebView. It is never an
inference credential.

On Unix, persistent mode also binds `{data-dir}/control.sock` (mode `0600`,
same-uid peer credentials). Local tools such as `astrlink-mcp` call the same
Control API over that socket without holding the per-start token. Windows has
no socket; the desktop writes a user-only session locator instead.

The first M1 slice recognizes all eight Alpha protocol IDs and includes a
protocol-preserving HTTP/SSE forwarder with cancellation and per-request
Endpoint authentication seams. Persistent mode now installs a deterministic
SQLite capability resolver behind the persistent local-token directory,
canonical listener Host validation, Origin rejection, and CORS response
stripping. Any active token may be supplied through Bearer, `X-Api-Key`, or
`X-Goog-Api-Key` so native SDKs can authenticate locally; it is removed before
Endpoint credentials are injected. Successful authentication attaches the
stable local token ID to the request context for future usage attribution.
Headless startup without persistent dependencies retains
`UnavailableResolver`. There is no CLI or environment-variable credential
shortcut.

Resolver priority is global native first, then explicitly declared delegated.
Delegated mode still forwards the original protocol bytes and relies on the
selected gateway to perform any upstream conversion; AstrLink does not invoke
RelayKit. Fixed and classified routes share a retry/failover loop with global
defaults, optional service/route overrides, response-commit boundaries, and
per-request attempt budgets. Configure defaults once via
`GET/PATCH /control/v1/routing-settings`; services inherit them automatically.
Unmatched requests do not switch services unless explicitly enabled.

The inference boundary rejects non-canonical Host values, browser `Origin`
requests, query-string tokens, simple form/text POST media types, and malformed
or opaque encoded routing metadata. It strips upstream CORS response headers.
Persistent-token authentication is the Production Resolver gate. A fresh or
upgraded database creates one `默认令牌` exactly once; deleting every token leaves
the inference plane closed until the user explicitly creates another token.
Creation, reveal, listing, and physical deletion are exposed only through the
authenticated control plane.

## Local request privacy policy

Persistent mode owns exactly one update-only policy,
`policy_privacy_default`. Its ID, display name, priority, and global `match: {}`
scope are immutable; creation, ordering, per-client matching, and deletion are
not implemented. The effective request detector is either `regex` or
`local_model`; model mode also references one ready installation through
`local_model_id`. The request action is `allow`, `warn`, `block`, or `redact`.
The stored `response_action` field is fixed to read-only `allow` by the control
contract and is not executed by this request-side slice.

The policy is resolved after protocol classification and Endpoint plan
validation, but before any upstream credential is loaded or a request is sent.
It currently inspects only selected JSON string roots for Responses, Responses
Compact, Chat Completions, Completions, Anthropic Messages, and Gemini
GenerateContent. Routing fields (`model` and `stream`) and binary, base64,
audio, file, and media-URL payloads are deliberately excluded. Response bodies
and SSE events are not inspected. A `warn` result forwards the unchanged
request and reports only deterministic finding categories and counts in
`X-AstrLink-Policy-Warning` and the local Core log; neither contains matched
plaintext.

The regex detector is a conservative high-confidence filter for known API
secret shapes, named secret assignments, validated payment-card/account
numbers, email addresses, phone numbers, HTTP(S) URLs, and IP addresses. It is
not a general PII classifier and should not be treated as complete DLP: it does
not semantically identify names, dates, or postal addresses, and both false
positives and false negatives remain possible.

Enabled-policy failures are fail closed. A missing or invalid policy, unsafe
JSON, an unsafe rewrite, detector limit/timeout, unavailable model, invalid
worker response, or worker crash cannot fall back to regex or bypass
inspection; Core rejects the request before upstream credential loading.
Request bodies are held only in bounded in-memory buffers for classification,
inspection, and optional rewrite. This slice does not write request plaintext,
response plaintext, or SSE payloads to SQLite, temporary files, normal logs, or
diagnostic output.

## Privacy model and worker boundary

The application embeds only a versioned model catalog: pinned Sheltron Ettin
32M, Nym PII Multilingual Small, and OpenAI Privacy Filter metadata with their
supported CPU quantization variants. Catalog changes arrive with an AstrLink
release; they are not fetched or silently updated at runtime. The desktop
installer contains no model weights, and Core never downloads or loads one
until the user explicitly chooses a model and variant.

Advanced users may probe a public Hugging Face repository by repository ID and
revision. Core resolves branches or tags to an immutable 40-character commit,
accepts only standard ONNX token-classification layouts or a versioned
`astrlink-model.json` descriptor, and rejects repository code, executables,
PyTorch, GGUF, arbitrary URLs, and remote-code loading. Common labels are
mapped to AstrLink's stable privacy kinds; every unknown label needs an
explicit user mapping or `ignore` before installation.

Users may also import the same standard Hugging Face ONNX
token-classification layout from an already-mounted absolute directory or a
specific `.onnx` file. File mode pins only that variant and its required
companion assets. Core reads only the bounded model/config/tokenizer asset
surface, rejects symlinks,
derives a synthetic `local/model-<digest>` identity from full SHA-256 content
digests (that exact shape is reserved; other `local/*` Hugging Face repositories
remain available), and keeps the source path only in a short-lived in-memory
probe cache. Installation streams the selected pinned assets into private
staging and verifies their
identity, size, and digest before the same atomic publication path is used; the
worker never executes directly from the mounted source.

Core can persist multiple immutable repository/commit/variant installations.
Each record also retains nullable license metadata, languages, and the
official/community catalog provenance needed by the installed-model view after
a restart.
Remote downloads and local imports select only the chosen ONNX variant and its
tokenizer/configuration data, use a private staging directory, verify every
pinned byte length and SHA-256 digest, write a normalized execution manifest,
and atomically publish the completed directory. Each remote asset receives
bounded retries for transient DNS,
transport, HTTP 408/425/429/5xx, and truncated-body failures; failed-attempt
bytes are rolled out of reported progress before retrying. Partial and
abandoned downloads are removed, and deleting a downloading installation
cancels it. Download diagnostics contain no response bodies, local paths, or
request content. An installation referenced by the policy cannot be deleted. A
legacy official Q4 directory is registered without another download only after
its original pinned files validate.

Restart validates the database-to-manifest binding and normalized metadata
without hashing every installed multi-gigabyte asset before Core becomes ready.
The selected installation receives a full path, symlink, size, and SHA-256
verification immediately before its first lazy worker load. A successful full
verification is cached only for the exact active
`{model ID, directory, identity, manifest SHA-256}` key and policy generation,
so a bounded worker restart does not hash multi-gigabyte assets again. A model
selection, installation-key, or policy-generation change invalidates that
cache. Damage remains fail closed on use. These checks do not claim continuous
runtime integrity against a same-user process modifying files after
verification.

Tauri packages the Rust `astrlink-privacy-worker` and
`astrlink-classifier-worker` executables and their shared pinned
ONNX Runtime 1.23.2 CPU runtime alongside Core, but not the model assets. On
macOS, the arm64 and x64 release archives are accepted only after their pinned
SHA-256 digest is verified, and the worker loads the fixed-version library only
from its executable directory or the app's `Contents/Frameworks`. The pinned
ONNX Runtime license and third-party notices are verified and included in
every platform bundle. On Linux x64, the official release archive is likewise
verified before its versioned shared library is bundled as a Tauri resource.
The desktop shell resolves that resource to an absolute path and supplies it
through the internal `ASTRLINK_ONNX_RUNTIME_PATH` environment variable; Core
inherits the value to the worker without placing it in process arguments.
Windows keeps the `ort`-managed binary staging behavior. Core starts
the worker lazily on the first protected request and keeps only the selected
model hot. Inference is CPU-only and serialized, with bounded ONNX Runtime
thread counts. The worker supports the constrained OpenAI
BIOES/Viterbi adapter and a generic Hugging Face BIO/BIOES token-classification
adapter. Core sends extracted plaintext segments through a length-prefixed
JSON protocol on child stdin; the worker returns only labels, byte spans, and
scores on stdout. Core validates the protocol version, request ID, frame size,
label set, ranges, and scores, and kills the worker on cancellation, timeout,
or malformed output. After loading the tokenizer, model, decoder, and ONNX
session, a new worker must first send the exact framed ready message
`{"version":1,"ready":true}`. Core rejects unknown fields, trailing JSON,
wrong versions, false readiness, EOF, and startup timeout, and does not publish
the process as usable before this handshake succeeds. Integrity-validation,
startup, or handshake failure is latched for the exact installation key,
preventing repeated model hashing and process-start storms until the policy
generation or installation key changes. A previously healthy hot worker may
restart once within the failing request from the successful-validation cache;
a consecutive failure then latches and remains fail closed. Worker stderr is
discarded and its own fatal message is generic.

Automated tests use fake Hugging Face servers, fake workers, and a sub-megabyte
synthetic ONNX/tokenizer fixture. CI rejects production model artifacts in
source, staged inputs, and extracted application bundles, forbids live
Hugging Face model traffic, and refuses production manifests during worker
tests. It never downloads or runs a catalog model.

This stdio split reduces the ONNX/tokenizer dependency surface in Go; it is not
a sandbox or a privilege boundary. The worker receives request plaintext in
memory and is part of the trusted local computing base under AstrLink's
same-login-user threat model. It exposes no control or inference listener, and
model-directory paths are not secret credentials.

Alpha execution semantics are explicit: `native` and `delegated` preserve the
ingress protocol and bypass conversion. `relaykit` is reserved as an execution
plan type, but the public capability reports it unavailable with no edges.
`relaykitbridge.NoopEngine` always returns an unavailable error; it never
performs conversion or treats pass-through as conversion.

SQLite migrations are forward-only and transactional. The persistent store pins
the pure-Go `modernc.org/sqlite` driver for three-platform sidecar builds.
Credential references are storage-neutral: the Alpha default is
`local://endpoint/<id>`, backed by a dedicated plaintext local table, while
`keyring://` is reserved for optional adapters. Local token metadata and hashes
live in `local_access_tokens`; revealable values live separately in
`local_access_token_secrets` and are returned only by explicit create/reveal
operations. Endpoint documents are strictly revalidated when read, writes use
strong ETags, and referenced Endpoints cannot be deleted. Restrictive filesystem
permissions are baseline hygiene, not same-user process isolation. Generic JSON,
lists, logs, ordinary exports, CLI arguments, and environment variables remain
outside both secret data-flow boundaries.
