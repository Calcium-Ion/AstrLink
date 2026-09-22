# convo

Session and user-turn identification for stateless LLM API traffic.

`convo` is a dependency-free Go module. Give it the request body a client sent
and the response bytes the client received, and it tells you which earlier
session the request continues and which user turn it belongs to. It works on
replayed history alone, so it needs no cooperation from the client.

Status: pre-1.0. The public API may still change between minor versions; the
fingerprint format is versioned separately through `NormalizationVersion`.

## Get the source

The module is included in the [AstrLink repository](https://github.com/Calcium-Ion/AstrLink)
and has not yet been published as a separately versioned package. Core uses it
through a local Go module replacement. To run its checks from the repository root:

```sh
cd convo
go test ./...
```

Requires Go 1.24 or newer. No third-party dependencies. See the repository's
[development guide](../CONTRIBUTING.md) to build the complete application.

## What it detects

Every supported protocol is handled by one adapter. The core matching logic is
protocol-agnostic and works on three kinds of cursor:

| Kind          | Source                                                          | Scope                                     |
|---------------|-----------------------------------------------------------------|-------------------------------------------|
| `explicit`    | ids the client or protocol already sends                        | global                                    |
| `echo_id`     | opaque ids the model produced and the client echoes back        | same principal, time window, entropy gate |
| `fingerprint` | HMAC of the normalized last assistant text                      | same principal, time window, min length   |

Coverage per protocol is documented in the signal matrix below.

## Signal matrix

| Protocol                  | explicit                                                                           | echo_id                                                              | fingerprint | user turn                                  |
|---------------------------|------------------------------------------------------------------------------------|----------------------------------------------------------------------|-------------|--------------------------------------------|
| `openai.chat`             | `conversation_id`, `metadata.*` extensions                                         | `tool_calls[].id` ↔ `tool_call_id`                                   | yes         | `role=user`                                |
| `openai.responses`        | `previous_response_id`, `conversation`, `prompt_cache_key`                         | `function_call.call_id`, output item `id` (`fc_*`, `msg_*`, `rs_*`)  | yes         | `role=user` message items                  |
| `anthropic.messages`      | `metadata.session_id`, Claude Code session in `metadata.user_id`, `container.id`   | `tool_use.id` ↔ `tool_result.tool_use_id`, server/mcp tool use ids   | yes         | `role=user` with a non-`tool_result` block |
| `gemini.generate_content` | `cachedContent`                                                                    | `functionCall.id` (when present), `thoughtSignature`                 | yes         | `role=user` with a non-`functionResponse` part |

Protocols without a history array (legacy completions, embeddings, images,
audio, rerank) have no session concept; treat each call as its own session.

## Usage

```go
policy := convo.DefaultPolicy()
fp := convo.NewFingerprinter(convo.DeriveFingerprintKey(masterKey, "my-gateway/session-fingerprint/v1"))

// Request side: inspect the body the client sent and ask storage which
// earlier session it continues.
summary, err := policy.InspectBody(convo.OpenAIChat, requestBody)
decision, err := policy.Resolve(ctx, summary, fp, lookup, time.Now())
sessionID := newSessionID()
if decision.Matched {
    sessionID = decision.Match.SessionID
}
turn := decision.Turn // *TurnState; store it and return it from Lookup as Match.Turn

// Response side: observe what the client receives. Either feed decoded SSE
// events through ObserveEvent, or let Write frame the raw bytes.
observer := policy.NewResponseObserver(convo.OpenAIChat, streaming)
io.Copy(io.MultiWriter(clientConn, observer), upstreamBody)

// Persist under sessionID: the explicit cursors the request named plus
// everything the response produced.
cursors := append(decision.PersistentInbound(), policy.OutputCursors(observer.Summary(), fp)...)
```

A complete runnable version of this flow is `Example_toolLoop` in
`example_test.go`.

### Storage contract

`Lookup` is the only callback into your storage:

```go
func lookup(ctx context.Context, kind convo.Kind, values []string, scope convo.Scope) (convo.Match, bool, error)
```

It must return the most recent root record (retries and other child records
excluded) whose stored cursors of `kind` contain any of `values`, subject to:

- `scope.SamePrincipal`: restrict to the principal of the current request
  (API key, user, tenant). Resolve sets it for `echo_id` and `fingerprint`.
- `scope.NotBefore`: ignore records started before that instant. Resolve sets
  it to `now - Policy.Window`.
- Prefer records that produced a value (`DirectionOut`) over records that
  merely named it (`DirectionIn`); the producer carries the turn a
  continuation builds on. Search `DirectionIn` for `KindExplicit` only, so
  client-chosen ids no response emits (`prompt_cache_key`, Claude Code
  session ids) still link sibling requests.

`Match.Turn` must be the `Decision.Turn` you stored with the matched record
(index, user-message count, newest-user-text fingerprint); `NextTurn` places
the new request relative to it. A record without a stored turn restarts the
count at 1.

See `memindex` for a reference in-memory implementation with TTL and size
bounds.

### Turn semantics

A turn is one time a person typed. Nothing in a single request body says
which `role=user` messages a person typed: coding-agent harnesses replay
skill text, tool descriptions, and context-compaction summaries as user
messages, each client in its own wording. So the turn is never the absolute
count of user messages, and there is no list of harness phrases to
recognise. Instead:

- The first request of a session is turn 1, whatever its history holds.
- A linked request starts a new turn when, compared with the record it
  links to, its history holds **more user messages** or its **newest user
  text changed** (`TurnState.UserMessages`, `TurnState.LastUserFingerprint`).
  The next call of an agent loop appends only assistant and tool items, so
  it changes neither and shares the turn: eight calls for one prompt are
  eight records with the same `Index`, displayed as "1 turn · 8 calls".
- A harness that compacts history and replays a new prompt has fewer
  messages but a changed newest text: a new turn. A history that shrank
  with the same newest text (trimmed for context) keeps its turn.
- Stateful Responses chains (`previous_response_id`) carry only the delta;
  a delta with a user message is a new turn and the stored count accumulates.

Tool results, `function_call_output`, and `functionResponse` are never user
messages. The one named exception is `<system-reminder>…</system-reminder>`:
its content is dropped from both ends of a message because Claude Code
attaches it to every tool result, and without that rule every tool result
would be a user message. Any other closed `<tag>…</tag>` wrapper is
unwrapped structurally (see `visibleText`): text outside the blocks is the
user's words; a wholly wrapped message yields the innermost text of its last
block, so `<user_query>q</user_query>` and `<skill>…</skill>\n\nprompt` both
yield what the person typed.

### Fingerprints

`Fingerprint = "fp1_" + hex(HMAC-SHA256(key, "convo/fingerprint/v1" || SHA-256(normalize(text))))[:32]`

`normalize` (see `TextNormalizer`, version 1): collapse Unicode whitespace to
one space, trim, drop one leading `<think>…</think>` block, replace invalid
UTF-8 with U+FFFD. Texts shorter than `Policy.MinFingerprintRunes` (32) are
not fingerprinted: short replies repeat across unrelated conversations.

The key must be per deployment. Derive it with `DeriveFingerprintKey` from a
secret you already protect; stored fingerprints reveal nothing about the text
without it.

## Guarantees

- Never panics on malformed input; unrecognised shapes yield empty summaries.
- No text is retained. Fingerprints are HMACs over a SHA-256 digest of
  normalized text; the digest is computed incrementally while streaming.
- Two implementations with the same `NormalizationVersion` and the same key
  produce identical fingerprints for identical visible assistant text.
- No global mutable state. Protocol adapters are looked up in a `Registry`;
  the default registry is read-only.

## Non-goals

- Reconstructing a session after the client compacted or summarised its history.
- Linking the very first request of a conversation: it has nothing to echo.
- Persisting anything. Storage, principal scoping, and key management are the
  caller's responsibility.
