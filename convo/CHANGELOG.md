# Changelog

All notable changes to the `convo` module are documented here. The module
follows semantic versioning; tags use the nested-module form `convo/vX.Y.Z`.

## Unreleased

- Helper calls no longer move a thread's turn. A client may send title, summary
  or fork requests under the same explicit identity as its conversation.
  `RequestSummary.UserDigests` describes the newest user messages by position,
  and when the newest call of the identity is not one the request's history
  continues, `Policy.Resolve` looks up the newest call it does continue through
  the new `Scope.Continues` (`Continuation`, `TurnPoint`). A request the host
  marks with `RequestSummary.SideCall`, or, for a client under the new
  `Policy.ToolFreeHelpers`, one that continues none and offers no tools
  (`RequestSummary.HasTools`), keeps the matched turn state and is reported as
  `Decision.SideCall`; one that opens its session holds turn 1 with no user
  messages, which `Policy.NextTurn` treats as nothing to compare.
  `Policy.MaxContinuationUsers` bounds the digests. Hosts whose `Lookup` ignores
  `Scope.Continues` still work; `Resolve` checks what it returns.
- Turns are relative, not counted. `Decision.TurnIndex` is replaced by
  `Decision.Turn *TurnState{Index, UserMessages, LastUserFingerprint}`; hosts
  store it and return it as `Match.Turn`. `Policy.NextTurn` starts a new turn
  only when the linked record's user-message count grew or the newest user text
  changed; the first record of a session is always turn 1.
  `RequestSummary.LastUserDigest` feeds the fingerprint.
- Harness text is handled structurally. The phrase list and the `<skill>`
  allow-list are gone. `<system-reminder>` blocks are still dropped from either
  end of a message; every other closed `<tag>…</tag>` wrapper is unwrapped (text
  outside wins, else the last block's inner text), so Cursor's `<user_query>`
  and Paseo's `<skill>` + prompt both yield the typed text without naming them.
- `Policy.NotesFollowPrompt` is for clients that send their own notes as plain
  user messages after the prompt, as DeepSeek Harness does with its runtime and
  clock snapshots. Only the user message that opens a turn then counts and
  supplies `LastUserText`; the rest of its run and user messages after tool
  results are notes. `Adapter` gains `IsToolResult` to tell where a turn's tool
  loop is.
- Initial extraction from AstrLink Core: protocol adapters for OpenAI Chat,
  OpenAI Responses, Anthropic Messages, and Gemini GenerateContent; typed
  session cursors (`explicit`, `echo_id`, `fingerprint`); streaming text
  normalizer with `NormalizationVersion = 1`; HMAC fingerprints with HKDF key
  derivation; `Policy.Resolve` / `Policy.OutputCursors` / `Policy.NextTurn`;
  `memindex` reference index.
