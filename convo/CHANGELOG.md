# Changelog

All notable changes to the `convo` module are documented here. The module
follows semantic versioning; tags use the nested-module form `convo/vX.Y.Z`.

## Unreleased

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
- Initial extraction from AstrLink Core: protocol adapters for OpenAI Chat,
  OpenAI Responses, Anthropic Messages, and Gemini GenerateContent; typed
  session cursors (`explicit`, `echo_id`, `fingerprint`); streaming text
  normalizer with `NormalizationVersion = 1`; HMAC fingerprints with HKDF key
  derivation; `Policy.Resolve` / `Policy.OutputCursors` / `Policy.NextTurn`;
  `memindex` reference index.
