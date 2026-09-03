# Changelog

All notable changes to the `convo` module are documented here. The module
follows semantic versioning; tags use the nested-module form `convo/vX.Y.Z`.

## Unreleased

- Harness filtering: leading and trailing `<system-reminder>` / `<skill>`
  blocks are stripped from user text instead of discarding the whole
  message, so a prompt that follows a skill injection still counts as a
  turn and becomes the preview. Compaction summaries ("The conversation
  history before this point…", "This session is being continued from a
  previous conversation…") are harness text, not turns.
- Initial extraction from AstrLink Core: protocol adapters for OpenAI Chat,
  OpenAI Responses, Anthropic Messages, and Gemini GenerateContent; typed
  session cursors (`explicit`, `echo_id`, `fingerprint`); streaming text
  normalizer with `NormalizationVersion = 1`; HMAC fingerprints with HKDF key
  derivation; `Policy.Resolve` / `Policy.OutputCursors` / `Policy.NextTurnIndex`;
  `memindex` reference index.
