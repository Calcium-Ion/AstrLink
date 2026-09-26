package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

const claudeCodeBanner = "You are Claude Code, Anthropic's official CLI for Claude."

const (
	claudeMaxCacheBreakpoints = 4
	claudeMinThinkingBudget   = 1024
	claudeDefaultMaxTokens    = 8192
)

// claudeRelayKitBetas are the feature betas a converted request may carry.
// Other client betas describe the source SDK, not a Claude Code client.
var claudeRelayKitBetas = []string{
	"claude-code-", "oauth-", "interleaved-thinking-", "fine-grained-tool-streaming-",
	"context-1m-", "prompt-caching-", "context-management-", "effort-",
}

// claudeRequestOptions selects the Routing protections of a Claude attempt.
type claudeRequestOptions struct {
	normalize bool
	// session scopes metadata.user_id and the session header to one account;
	// nil forwards them unchanged.
	session *claudeSessionScope
}

type claudeSessionScope struct {
	serviceID   contract.ServiceID
	accountUUID string
	// identity reports an enforced Claude Code identity, whose release sends
	// the JSON user_id and the session header.
	identity bool
}

// peekClaudeRequestBody reads the attempt body to classify the client, then
// restores it so an official passthrough forwards the original bytes unchanged.
// A read error restores what was read and reports failure, leaving the request
// to the third-party path that reads and rejects it in the usual way.
func peekClaudeRequestBody(request *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	replaceRecoveryRequestBody(request, raw)
	return raw, err == nil
}

// prepareClaudeSubscriptionRequest adds the Claude Code compatibility banner
// and repairs request structure the Messages API rejects: breakpoint limits and
// TTL order, thinking parameter conflicts, a missing max_tokens, empty text
// blocks and unsigned thinking history. Every rule is a no-op for a compliant
// request, which is then forwarded byte-for-byte. Content is never rewritten
// or synthesized; other system blocks remain intact.
func prepareClaudeSubscriptionRequest(request *http.Request, options claudeRequestOptions) error {
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return fmt.Errorf("Claude subscription request could not be prepared")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return fmt.Errorf("invalid Claude subscription request")
	}
	changed := false
	if options.normalize {
		if changed, err = normalizeClaudeSubscriptionBody(body, request.Header.Values("Anthropic-Beta")); err != nil {
			return err
		}
	}
	if options.session != nil && scopeClaudeSession(body, request.Header, *options.session) {
		changed = true
	}
	bannered, err := ensureClaudeCodeBanner(body)
	if err != nil {
		return err
	}
	if !changed && !bannered {
		replaceRecoveryRequestBody(request, raw)
		return nil
	}
	updated, err := json.Marshal(body)
	if err != nil {
		return err
	}
	replaceRecoveryRequestBody(request, updated)
	return nil
}

// scopeClaudeSession presents one device per account and maps the caller's
// session into that account's namespace, so a conversation that fails over
// does not link accounts. Other metadata fields are kept; metadata that is not
// an object is left for the upstream to judge.
func scopeClaudeSession(body map[string]json.RawMessage, header http.Header, scope claudeSessionScope) bool {
	metadata := map[string]json.RawMessage{}
	if raw, ok := body["metadata"]; ok && !isJSONNull(raw) {
		if json.Unmarshal(raw, &metadata) != nil || metadata == nil {
			return false
		}
	}
	userID, _ := claudeString(metadata["user_id"])
	source := accountauth.ClaudeUserIDSession(userID)
	if source == "" {
		source = strings.TrimSpace(header.Get(accountauth.ClaudeCodeSessionHeader))
	}
	if source == "" {
		source = userID
	}
	if source == "" {
		source = firstClaudeUserContent(body["messages"])
	}
	session := accountauth.ScopedSessionID(scope.serviceID, source)
	if scope.identity || header.Get(accountauth.ClaudeCodeSessionHeader) != "" {
		header.Set(accountauth.ClaudeCodeSessionHeader, session)
	}
	legacy := !scope.identity && accountauth.ClaudeLegacyUserID(userID)
	scoped := accountauth.ClaudeMetadataUserID(scope.serviceID, scope.accountUUID, session, legacy)
	if scoped == userID {
		return false
	}
	if encodeClaudeField(metadata, "user_id", scoped) != nil || encodeClaudeField(body, "metadata", metadata) != nil {
		return false
	}
	return true
}

// firstClaudeUserContent seeds a session for callers that name none, so the
// turns of one conversation still share it.
func firstClaudeUserContent(raw json.RawMessage) string {
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &messages) != nil {
		return ""
	}
	for _, message := range messages {
		if message.Role == "user" {
			return string(message.Content)
		}
	}
	return ""
}

func ensureClaudeCodeBanner(body map[string]json.RawMessage) (bool, error) {
	var blocks []json.RawMessage
	if system := body["system"]; len(system) > 0 && !isJSONNull(system) {
		var text string
		if json.Unmarshal(system, &text) == nil {
			if text == claudeCodeBanner {
				return false, nil
			}
			if strings.TrimSpace(text) != "" {
				block, _ := json.Marshal(map[string]string{"type": "text", "text": text})
				blocks = append(blocks, block)
			}
		} else if json.Unmarshal(system, &blocks) != nil {
			return false, fmt.Errorf("invalid Claude subscription system prompt")
		}
	}
	for _, block := range blocks {
		var text struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(block, &text) == nil && text.Type == "text" && text.Text == claudeCodeBanner {
			return false, nil
		}
	}
	banner, _ := json.Marshal(map[string]string{"type": "text", "text": claudeCodeBanner})
	encoded, err := json.Marshal(append([]json.RawMessage{banner}, blocks...))
	if err != nil {
		return false, err
	}
	body["system"] = encoded
	return true, nil
}

// filterClaudeRelayKitHeaders drops client SDK fingerprints and OpenAI
// protocol headers from a converted request so the forwarded identity is not a
// mix of two clients.
func filterClaudeRelayKitHeaders(header http.Header) {
	stripHeaderPrefixes(header, "x-stainless-", "openai-")
	var kept []string
	for _, value := range header.Values("Anthropic-Beta") {
		for beta := range strings.SplitSeq(value, ",") {
			beta = strings.TrimSpace(beta)
			for _, prefix := range claudeRelayKitBetas {
				if strings.HasPrefix(beta, prefix) {
					kept = append(kept, beta)
					break
				}
			}
		}
	}
	header.Del("Anthropic-Beta")
	if len(kept) > 0 {
		header.Set("Anthropic-Beta", strings.Join(kept, ","))
	}
}

func stripHeaderPrefixes(header http.Header, prefixes ...string) {
	for name := range header {
		lower := strings.ToLower(name)
		for _, prefix := range prefixes {
			if strings.HasPrefix(lower, prefix) {
				delete(header, name)
				break
			}
		}
	}
}

// claudeBlocks is a JSON array of objects decoded one level deep, so fields
// that are not inspected are forwarded verbatim.
type claudeBlocks struct {
	items []map[string]json.RawMessage
	dirty bool
}

func decodeClaudeBlocks(raw json.RawMessage) *claudeBlocks {
	var items []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return &claudeBlocks{items: items}
}

// drop removes matching blocks. Unless allowEmpty is set, a list that would
// become empty is kept unchanged rather than inventing replacement content.
func (blocks *claudeBlocks) drop(remove func(map[string]json.RawMessage) bool, allowEmpty bool) {
	kept := make([]map[string]json.RawMessage, 0, len(blocks.items))
	for _, block := range blocks.items {
		if block == nil || !remove(block) {
			kept = append(kept, block)
		}
	}
	if len(kept) == len(blocks.items) || (len(kept) == 0 && !allowEmpty) {
		return
	}
	blocks.items, blocks.dirty = kept, true
}

type claudeMessage struct {
	fields  map[string]json.RawMessage
	content *claudeBlocks
	// results holds tool_result content arrays by index in content.
	results map[int]*claudeBlocks
}

type claudeBreakpoint struct {
	blocks  *claudeBlocks
	index   int
	message bool
}

func normalizeClaudeSubscriptionBody(body map[string]json.RawMessage, betas []string) (bool, error) {
	changed := normalizeClaudeThinking(body, betas)

	system := (*claudeBlocks)(nil)
	if raw := body["system"]; len(raw) > 0 && raw[0] == '[' {
		system = decodeClaudeBlocks(raw)
	}
	if system != nil {
		system.drop(isEmptyClaudeText, true)
	}
	var messages []claudeMessage
	var rawMessages []map[string]json.RawMessage
	if json.Unmarshal(body["messages"], &rawMessages) == nil {
		messages = make([]claudeMessage, len(rawMessages))
		for index, fields := range rawMessages {
			messages[index].fields = fields
			if fields == nil {
				continue
			}
			content := decodeClaudeBlocks(fields["content"])
			if content == nil {
				continue
			}
			content.drop(isEmptyClaudeText, false)
			if role, _ := claudeString(fields["role"]); role == "assistant" {
				content.drop(isUnsignedClaudeThinking, false)
			}
			messages[index].content = content
		}
	}
	tools := decodeClaudeBlocks(body["tools"])

	breakpoints := collectClaudeBreakpoints(tools, system, messages)
	if err := enforceClaudeCacheTTLOrder(breakpoints); err != nil {
		return false, err
	}
	enforceClaudeBreakpointLimit(breakpoints)

	if tools != nil && tools.dirty {
		if err := encodeClaudeField(body, "tools", tools.items); err != nil {
			return false, err
		}
		changed = true
	}
	if system != nil && system.dirty {
		if err := encodeClaudeField(body, "system", system.items); err != nil {
			return false, err
		}
		changed = true
	}
	messagesDirty := false
	for _, message := range messages {
		if message.content == nil {
			continue
		}
		for index, results := range message.results {
			if results.dirty {
				if err := encodeClaudeField(message.content.items[index], "content", results.items); err != nil {
					return false, err
				}
				message.content.dirty = true
			}
		}
		if message.content.dirty {
			if err := encodeClaudeField(message.fields, "content", message.content.items); err != nil {
				return false, err
			}
			messagesDirty = true
		}
	}
	if messagesDirty {
		if err := encodeClaudeField(body, "messages", rawMessages); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

// normalizeClaudeThinking resolves parameters the API rejects together with
// extended thinking. Adaptive thinking is left to the upstream.
func normalizeClaudeThinking(body map[string]json.RawMessage, betas []string) bool {
	changed := false
	var thinking map[string]json.RawMessage
	thinkingType := ""
	if json.Unmarshal(body["thinking"], &thinking) == nil && thinking != nil {
		thinkingType, _ = claudeString(thinking["type"])
	}
	budget, hasBudget := claudeInt(thinking["budget_tokens"])
	if thinkingType == "enabled" && claudeForcesToolUse(body["tool_choice"]) {
		delete(body, "thinking")
		thinkingType, changed = "", true
	}
	if raw, ok := body["max_tokens"]; !ok || isJSONNull(raw) {
		maxTokens := int64(claudeDefaultMaxTokens)
		if thinkingType == "enabled" && hasBudget {
			maxTokens = max(maxTokens, budget+claudeMinThinkingBudget)
		}
		body["max_tokens"] = json.RawMessage(strconv.FormatInt(maxTokens, 10))
		changed = true
	}
	if thinkingType != "enabled" {
		return changed
	}
	// Interleaved thinking budgets span the turn and may exceed max_tokens.
	if maxTokens, ok := claudeInt(body["max_tokens"]); ok && hasBudget && budget >= maxTokens && !claudeInterleavedThinking(betas) {
		budget = maxTokens - 1
		thinking["budget_tokens"] = json.RawMessage(strconv.FormatInt(budget, 10))
		if encoded, err := json.Marshal(thinking); err == nil {
			body["thinking"] = encoded
			changed = true
		}
	}
	if hasBudget && budget < claudeMinThinkingBudget {
		delete(body, "thinking")
		return true
	}
	if _, ok := body["top_k"]; ok {
		delete(body, "top_k")
		changed = true
	}
	if raw, ok := body["temperature"]; ok {
		if value, isNumber := claudeNumber(raw); !isNumber || value != 1 {
			delete(body, "temperature")
			changed = true
		}
	}
	if value, ok := claudeNumber(body["top_p"]); ok && value < 0.95 {
		delete(body, "top_p")
		changed = true
	}
	return changed
}

// collectClaudeBreakpoints lists cache_control blocks in the order the API
// evaluates them: tools, system, then messages.
func collectClaudeBreakpoints(tools, system *claudeBlocks, messages []claudeMessage) []claudeBreakpoint {
	var breakpoints []claudeBreakpoint
	add := func(blocks *claudeBlocks, message bool) {
		if blocks == nil {
			return
		}
		for index, block := range blocks.items {
			if raw, ok := block["cache_control"]; ok && !isJSONNull(raw) {
				breakpoints = append(breakpoints, claudeBreakpoint{blocks: blocks, index: index, message: message})
			}
		}
	}
	add(tools, false)
	add(system, false)
	for messageIndex := range messages {
		message := &messages[messageIndex]
		if message.content == nil {
			continue
		}
		for index, block := range message.content.items {
			if raw, ok := block["cache_control"]; ok && !isJSONNull(raw) {
				breakpoints = append(breakpoints, claudeBreakpoint{blocks: message.content, index: index, message: true})
			}
			if kind, _ := claudeString(block["type"]); kind != "tool_result" {
				continue
			}
			if results := decodeClaudeBlocks(block["content"]); results != nil {
				if message.results == nil {
					message.results = make(map[int]*claudeBlocks)
				}
				message.results[index] = results
				add(results, true)
			}
		}
	}
	return breakpoints
}

// enforceClaudeCacheTTLOrder drops the ttl of a 1h breakpoint that follows a
// 5m one (the default), which the API rejects.
func enforceClaudeCacheTTLOrder(breakpoints []claudeBreakpoint) error {
	sawShort := false
	for _, breakpoint := range breakpoints {
		block := breakpoint.blocks.items[breakpoint.index]
		var control map[string]json.RawMessage
		if json.Unmarshal(block["cache_control"], &control) != nil || control == nil {
			continue
		}
		if ttl, _ := claudeString(control["ttl"]); ttl != "1h" {
			sawShort = true
			continue
		}
		if !sawShort {
			continue
		}
		delete(control, "ttl")
		if err := encodeClaudeField(block, "cache_control", control); err != nil {
			return err
		}
		breakpoint.blocks.dirty = true
	}
	return nil
}

// enforceClaudeBreakpointLimit removes the earliest message breakpoints first,
// keeping the tool and system prefixes cached, until at most four remain.
func enforceClaudeBreakpointLimit(breakpoints []claudeBreakpoint) {
	excess := len(breakpoints) - claudeMaxCacheBreakpoints
	if excess <= 0 {
		return
	}
	// A later breakpoint still caches the prefix before it, so the earliest
	// ones are the cheapest to lose; tools and system are the last resort.
	order := make([]claudeBreakpoint, 0, len(breakpoints))
	for _, message := range []bool{true, false} {
		for _, breakpoint := range breakpoints {
			if breakpoint.message == message {
				order = append(order, breakpoint)
			}
		}
	}
	for _, breakpoint := range order[:excess] {
		delete(breakpoint.blocks.items[breakpoint.index], "cache_control")
		breakpoint.blocks.dirty = true
	}
}

func encodeClaudeField[T any](fields map[string]json.RawMessage, name string, value T) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fields[name] = encoded
	return nil
}

func isEmptyClaudeText(block map[string]json.RawMessage) bool {
	if kind, _ := claudeString(block["type"]); kind != "text" {
		return false
	}
	text, ok := claudeString(block["text"])
	return ok && strings.TrimSpace(text) == ""
}

// isUnsignedClaudeThinking reports assistant history the API cannot verify.
func isUnsignedClaudeThinking(block map[string]json.RawMessage) bool {
	switch kind, _ := claudeString(block["type"]); kind {
	case "thinking":
		signature, _ := claudeString(block["signature"])
		return strings.TrimSpace(signature) == ""
	case "redacted_thinking":
		data, _ := claudeString(block["data"])
		return strings.TrimSpace(data) == ""
	default:
		return false
	}
}

func claudeForcesToolUse(raw json.RawMessage) bool {
	var choice struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &choice) == nil && (choice.Type == "any" || choice.Type == "tool")
}

func claudeInterleavedThinking(betas []string) bool {
	for _, value := range betas {
		for beta := range strings.SplitSeq(value, ",") {
			if strings.HasPrefix(strings.TrimSpace(beta), "interleaved-thinking-") {
				return true
			}
		}
	}
	return false
}

func claudeString(raw json.RawMessage) (string, bool) {
	var value string
	return value, len(raw) > 0 && json.Unmarshal(raw, &value) == nil
}

func claudeNumber(raw json.RawMessage) (float64, bool) {
	var value float64
	return value, len(raw) > 0 && !isJSONNull(raw) && json.Unmarshal(raw, &value) == nil
}

func claudeInt(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || isJSONNull(raw) {
		return 0, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if decoder.Decode(&number) != nil {
		return 0, false
	}
	value, err := number.Int64()
	return value, err == nil
}
