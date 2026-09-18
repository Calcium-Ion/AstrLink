package ingress

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	openAIReasoningRecoveryReason      = "openai_reasoning_repair"
	openAIFunctionOutputRecoveryReason = "openai_function_output_repair"
)

var (
	openAIReasoningModel        = regexp.MustCompile(`^(?:gpt-|codex(?:-|$)|o[1-9][0-9]*(?:-|$))`)
	openAIReasoningParam        = regexp.MustCompile(`^input(?:\[([0-9]+)\]|\.([0-9]+))(?:\.(encrypted_content|content|status))?$`)
	openAIReasoningMessageParam = regexp.MustCompile(`input(?:\[[0-9]+\]|\.[0-9]+)\.(?:encrypted_content|content|status)\b`)
	openAIOrphanReasoning       = regexp.MustCompile("(?i)\\bitem ['\"`]([^'\"`]+)['\"`] of type ['\"`]reasoning['\"`] was provided without its required following item")
	openAIMissingReasoning      = regexp.MustCompile("(?i)\\bitem ['\"`]([^'\"`]+)['\"`] of type ['\"`](message|function_call|custom_tool_call)['\"`] was provided without its required ['\"`]reasoning['\"`] item")
	openAIMissingReasoningID    = regexp.MustCompile("(?i)\\bitem with id ['\"`](rs_[a-zA-Z0-9_-]+)['\"`] not found")
	openAIEncryptedItemID       = regexp.MustCompile("(?i)encrypted content for item ['\"`]?(rs_[a-zA-Z0-9_-]+)")
)

func supportsOpenAIReasoningRecovery(plan contract.ExecutionPlan, model string) bool {
	if plan.Type == contract.PlanTypeRelayKit || plan.InputProtocol != plan.UpstreamProtocol {
		return false
	}
	if plan.InputProtocol != contract.ProtocolOpenAIResponses && plan.InputProtocol != contract.ProtocolOpenAIResponsesCompact {
		return false
	}
	return openAIReasoningModel.MatchString(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "openai/"))
}

type openAIReasoningError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param"`
}

func reasoningErrorJSONBytes(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	if json.Valid(trimmed) {
		return trimmed
	}
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		payload, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		payload = bytes.TrimSpace(payload)
		if json.Valid(payload) {
			return payload
		}
	}
	return trimmed
}

func parseOpenAIReasoningError(body []byte) openAIReasoningError {
	var result openAIReasoningError
	body = reasoningErrorJSONBytes(body)
	if !json.Valid(body) {
		return result
	}
	// Some relays wrap the original error JSON in error.message and append a
	// trace ID. Decode only a leading JSON value, with a bounded nesting depth.
	for depth := 0; depth < 3; depth++ {
		var envelope struct {
			openAIReasoningError
			Error json.RawMessage `json:"error"`
		}
		if json.NewDecoder(bytes.NewReader(body)).Decode(&envelope) != nil {
			break
		}
		current := envelope.openAIReasoningError
		if len(envelope.Error) > 0 && !bytes.Equal(envelope.Error, []byte("null")) {
			if json.Unmarshal(envelope.Error, &current) != nil {
				break
			}
		}
		if depth == 0 {
			result = current
		}
		if current.Code != "" && current.Code != "invalid_request_error" {
			return current
		}
		if current.Message != "" {
			result = current
		}
		body = []byte(strings.TrimSpace(current.Message))
		if len(body) == 0 || body[0] != '{' {
			break
		}
	}
	return result
}

type openAIRepairScope struct {
	reasoning      bool
	functionOutput bool
}

// A repair is driven by the actual upstream rejection, never by merely
// observing reasoning in a request. Conversation and tool identities remain
// intact. Compaction may be the only surviving conversation history, so it is
// never discarded even though sub2api's encrypted-content fallback does so.
// "The encrypted content for item rs_…" proves the failure is a reasoning
// blob, so every reasoning ciphertext can be stripped while compaction stays.
// Dropping those empty items also detaches the following complete
// assistant/tool provider ids.
func rectifyOpenAIReasoning(body, errorBody []byte) ([]byte, bool) {
	repaired, _, ok := rectifyOpenAIRequest(body, errorBody, openAIRepairScope{reasoning: true})
	return repaired, ok
}

func isEncryptedFunctionOutputError(code, message string) bool {
	if strings.Contains(message, "encrypted function output") {
		return true
	}
	return (code == "invalid_encrypted_content" || code == "" || code == "invalid_request_error") &&
		strings.Contains(message, "function output") &&
		(strings.Contains(message, "decrypt") || strings.Contains(message, "decode") || strings.Contains(message, "verified"))
}

func rectifyOpenAIRequest(body, errorBody []byte, scope openAIRepairScope) ([]byte, string, bool) {
	if !scope.reasoning && !scope.functionOutput {
		return nil, "", false
	}
	failure := parseOpenAIReasoningError(errorBody)
	code := strings.ToLower(strings.TrimSpace(failure.Code))
	message := strings.ToLower(failure.Message)
	functionOutputError := isEncryptedFunctionOutputError(code, message)
	if functionOutputError && !scope.functionOutput {
		return nil, "", false
	}
	if !functionOutputError && !scope.reasoning {
		return nil, "", false
	}
	encryptedError := !functionOutputError && (code == "invalid_encrypted_content" ||
		((code == "" || code == "invalid_request_error") &&
			(strings.Contains(message, "invalid_encrypted_content") ||
				(strings.Contains(message, "encrypted content") && strings.Contains(message, "could not be verified")) ||
				(strings.Contains(message, "encrypted_content") && strings.Contains(message, "could not decrypt")))))
	param := strings.TrimSpace(failure.Param)
	messageParam := openAIReasoningMessageParam.FindString(failure.Message)
	if param == "" {
		param = messageParam
	}
	index, field, indexed := reasoningErrorInputIndex(param)
	if param != "" && messageParam != "" {
		messageIndex, messageField, ok := reasoningErrorInputIndex(messageParam)
		if !ok || !indexed || messageIndex != index || messageField != field {
			return nil, "", false
		}
	}
	orphan := openAIOrphanReasoning.FindStringSubmatch(failure.Message)
	missing := openAIMissingReasoning.FindStringSubmatch(failure.Message)
	missingID := openAIMissingReasoningID.FindStringSubmatch(failure.Message)
	structuralError := code == "" || code == "invalid_request_error" || code == "invalid_value" || code == "item_not_found"
	fieldError := indexed && (field == "content" || field == "status") &&
		(code == "unknown_parameter" || code == "unsupported_parameter" || code == "invalid_type" || code == "array_above_max_length" || code == "invalid_request_error" || code == "")
	structuralMatch := structuralError && (len(orphan) > 0 || len(missing) > 0 || len(missingID) > 0)
	if !functionOutputError && !encryptedError && !fieldError && !structuralMatch {
		return nil, "", false
	}
	if !json.Valid(body) {
		return nil, "", false
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(body, &document) != nil {
		return nil, "", false
	}
	var input []json.RawMessage
	if json.Unmarshal(document["input"], &input) != nil || len(input) == 0 {
		return nil, "", false
	}
	if indexed && (index >= len(input) || index < 0) {
		return nil, "", false
	}
	if encryptedError && param != "" && param != "input" && (!indexed || (field != "" && field != "encrypted_content")) {
		return nil, "", false
	}
	encryptedID := ""
	if encryptedError {
		if match := openAIEncryptedItemID.FindStringSubmatch(failure.Message); len(match) > 1 {
			encryptedID = match[1]
		}
	}
	if encryptedError && encryptedID != "" {
		found := false
		for _, raw := range input {
			var item map[string]json.RawMessage
			if json.Unmarshal(raw, &item) != nil || reasoningString(item["id"]) != encryptedID {
				continue
			}
			kind := reasoningString(item["type"])
			if kind == "compaction" || kind == "compaction_summary" {
				return nil, "", false
			}
			if kind == "reasoning" && len(item["encrypted_content"]) > 0 {
				found = true
			}
		}
		if !found {
			return nil, "", false
		}
		if indexed {
			var item map[string]json.RawMessage
			if json.Unmarshal(input[index], &item) != nil || reasoningString(item["id"]) != encryptedID {
				return nil, "", false
			}
		}
	}
	if encryptedError && !indexed && encryptedID == "" {
		for _, raw := range input {
			var item map[string]json.RawMessage
			if json.Unmarshal(raw, &item) != nil {
				continue
			}
			kind := reasoningString(item["type"])
			if (kind == "compaction" || kind == "compaction_summary") && len(item["encrypted_content"]) > 0 {
				return nil, "", false
			}
		}
	}
	changed := false
	detachedIDs := map[string]bool{}
	repaired := make([]json.RawMessage, 0, len(input))
	detachFollowing := false
	for i, raw := range input {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil || item == nil {
			repaired = append(repaired, raw)
			detachFollowing = false
			continue
		}
		kind, id := reasoningString(item["type"]), reasoningString(item["id"])
		modified, drop := false, false
		if detachFollowing {
			if standaloneReasoningDependentItem(item) && id != "" {
				delete(item, "id")
				modified = true
				detachedIDs[id] = true
			}
			detachFollowing = false
		}
		namedCipher := encryptedID != ""
		unscopedCipher := encryptedID == "" && (!indexed || i == index)
		switch {
		case encryptedError && kind == "reasoning" && (namedCipher || unscopedCipher) && len(item["encrypted_content"]) > 0:
			delete(item, "encrypted_content")
			// A store:false Codex response has no server-side item to resolve once
			// its ciphertext is removed. Keep any visible summary as inline data.
			delete(item, "id")
			if bytes.Equal(bytes.TrimSpace(item["content"]), []byte("null")) {
				delete(item, "content")
			}
			drop = !hasReasoningText(item["summary"]) && !hasReasoningText(item["content"])
			if !drop && (len(item["summary"]) == 0 || bytes.Equal(bytes.TrimSpace(item["summary"]), []byte("null"))) {
				item["summary"] = json.RawMessage(`[]`)
			}
			modified = true
		case !encryptedError && structuralError && kind == "reasoning" && len(orphan) > 0 && id == orphan[1]:
			modified, drop = true, true
		case !encryptedError && structuralError && kind == "reasoning" && len(missingID) > 0 && id == missingID[1] && reasoningString(item["encrypted_content"]) != "":
			delete(item, "id")
			modified = true
		case !encryptedError && structuralError && len(missing) > 0 && id == missing[1] && kind == missing[2] && standaloneReasoningDependentItem(item):
			// Detach a complete visible item from an unavailable server-side
			// reasoning predecessor. call_id is the tool-pair key and is retained.
			delete(item, "id")
			modified = true
		case !encryptedError && fieldError && i == index && kind == "reasoning" && len(item[field]) > 0:
			unsupported := code == "unknown_parameter" || code == "unsupported_parameter" || strings.Contains(message, "unknown parameter") || strings.Contains(message, "unsupported parameter")
			nullContent := field == "content" && bytes.Equal(bytes.TrimSpace(item[field]), []byte("null")) && strings.Contains(message, "null") && (code == "invalid_type" || strings.Contains(message, "invalid type"))
			zeroContent := field == "content" && code == "array_above_max_length" && strings.Contains(message, "maximum length 0")
			if unsupported || nullContent || zeroContent {
				delete(item, field)
				modified = true
			}
		}
		if scope.functionOutput && (functionOutputError || encryptedError) && stripFunctionOutputCipher(item) {
			modified = true
		}
		if modified {
			changed = true
			if id != "" && (drop || len(item["id"]) == 0) {
				detachedIDs[id] = true
			}
			if drop {
				// Only a named ciphertext drop is known to be that item's
				// predecessor. Unindexed cleanup must not rewrite later tool ids.
				detachFollowing = encryptedID != ""
				continue
			}
			var err error
			raw, err = json.Marshal(item)
			if err != nil {
				return nil, "", false
			}
		}
		repaired = append(repaired, raw)
	}
	if !changed || len(repaired) == 0 {
		return nil, "", false
	}
	for _, raw := range input {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) == nil && reasoningString(item["type"]) == "item_reference" && detachedIDs[reasoningString(item["id"])] {
			return nil, "", false // A reference-only item cannot be reconstructed safely.
		}
	}
	document["input"], _ = json.Marshal(repaired)
	result, err := json.Marshal(document)
	if err != nil {
		return nil, "", false
	}
	reason := openAIReasoningRecoveryReason
	if functionOutputError && !encryptedError && !fieldError && !structuralMatch {
		reason = openAIFunctionOutputRecoveryReason
	}
	return result, reason, true
}

func reasoningErrorInputIndex(param string) (int, string, bool) {
	match := openAIReasoningParam.FindStringSubmatch(param)
	if len(match) == 0 {
		return 0, "", false
	}
	digits := match[1]
	if digits == "" {
		digits = match[2]
	}
	index, err := strconv.Atoi(digits)
	return index, match[3], err == nil
}

func reasoningString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func hasReasoningText(raw json.RawMessage) bool {
	if strings.TrimSpace(reasoningString(raw)) != "" {
		return true
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return false
	}
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" {
			return true
		}
	}
	return false
}

func stripFunctionOutputCipher(item map[string]json.RawMessage) bool {
	kind := reasoningString(item["type"])
	if kind != "function_call_output" && kind != "custom_tool_call_output" {
		return false
	}
	changed := false
	if len(item["encrypted_content"]) > 0 {
		delete(item, "encrypted_content")
		changed = true
	}
	raw, exists := item["output"]
	if !exists {
		if changed {
			delete(item, "id")
		}
		return changed
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		if changed {
			delete(item, "id")
		}
		return changed
	}
	kept := make([]json.RawMessage, 0, len(parts))
	removed := false
	for _, part := range parts {
		var obj map[string]json.RawMessage
		if json.Unmarshal(part, &obj) == nil && reasoningString(obj["type"]) == "encrypted_content" {
			removed = true
			continue
		}
		kept = append(kept, part)
	}
	if !removed {
		if changed {
			delete(item, "id")
		}
		return changed
	}
	if len(kept) == 0 {
		item["output"] = json.RawMessage(`""`)
	} else {
		encoded, err := json.Marshal(kept)
		if err != nil {
			return false
		}
		item["output"] = encoded
	}
	delete(item, "id")
	return true
}

func standaloneReasoningDependentItem(item map[string]json.RawMessage) bool {
	switch reasoningString(item["type"]) {
	case "message":
		return reasoningString(item["role"]) == "assistant" && hasReasoningText(item["content"])
	case "function_call":
		return reasoningString(item["call_id"]) != "" && reasoningString(item["name"]) != "" && reasoningString(item["arguments"]) != ""
	case "custom_tool_call":
		return reasoningString(item["call_id"]) != "" && reasoningString(item["name"]) != "" && reasoningString(item["input"]) != ""
	}
	return false
}
