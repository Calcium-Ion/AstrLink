// Package convo identifies conversation sessions and user turns from stateless
// LLM API traffic.
//
// Chat-style APIs (OpenAI Chat Completions, OpenAI Responses, Anthropic
// Messages, Gemini GenerateContent) are stateless: every request replays the
// whole history. A gateway that only sees one HTTP call at a time therefore has
// no session identifier to group by. This package extracts the signals that a
// replayed history does carry and turns them into typed session cursors:
//
//   - explicit cursors the protocol or client already sends
//     (previous_response_id, conversation ids, prompt_cache_key, ...);
//   - echo ids the model produced and the client must send back verbatim
//     (tool call ids, output item ids, Gemini thought signatures);
//   - a keyed fingerprint of the last assistant text, which survives when a
//     conversation contains no tool calls at all.
//
// The package has no storage, logging, or network dependencies. Callers persist
// the cursors a response produced and answer Lookup queries; Policy.Resolve
// decides which stored session a new request continues. Policy.NextTurnIndex
// derives the user-turn number so that an agent tool loop of many calls is
// reported as one turn.
package convo
