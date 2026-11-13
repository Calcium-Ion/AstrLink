package ingress

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/systemprompt"
)

// gatewayRewrite holds the Routing settings that edit inference requests:
// the global system prompt and the reasoning settings.
type gatewayRewrite struct {
	prefix, suffix string
	reasoning      reasoningConfig
}

func gatewayRewriteFrom(settings contract.RoutingSettings) gatewayRewrite {
	rewrite := gatewayRewrite{reasoning: reasoningConfigFrom(settings)}
	if settings.SystemPromptEnabled {
		// Blank text adds nothing; other text is sent as written.
		if strings.TrimSpace(settings.SystemPromptPrefix) != "" {
			rewrite.prefix = settings.SystemPromptPrefix
		}
		if strings.TrimSpace(settings.SystemPromptSuffix) != "" {
			rewrite.suffix = settings.SystemPromptSuffix
		}
	}
	return rewrite
}

// claudeCodeHaikuRequest reports a Claude Code request for a Claude Haiku
// model, the background route of claude-code-router: Claude Code sends its
// titles, summaries and command checks to its Haiku model. model is the one
// the client asked for, before any redirect. Other clients have no such
// rule, so all their requests count as the main conversation.
func claudeCodeHaikuRequest(client contract.ClientType, model string) bool {
	model = strings.ToLower(model)
	return client == contract.ClientClaudeCode && strings.Contains(model, "claude") && strings.Contains(model, "haiku")
}

// rewritesProtocol reports whether an upstream protocol has the system prompt
// and reasoning controls the rewrite edits. A raw completion has neither.
func rewritesProtocol(protocol contract.ProtocolID) bool {
	switch protocol {
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact,
		contract.ProtocolAnthropicMessages, contract.ProtocolGoogleGenerateContent:
		return true
	default:
		return false
	}
}

// gatewayReasoning is what the reasoning settings and the global system
// prompt did to one attempt.
type gatewayReasoning struct {
	// effort is the level sent when the settings changed the body; empty
	// keeps the client's level.
	effort string
	// diagnostic explains the decision for the request record; empty when
	// the settings leave reasoning to the client.
	diagnostic string
	// systemPrompt is set when the global system prompt was added.
	systemPrompt bool
}

// applyGatewayRewrite applies the reasoning settings and adds the global
// system prompt to one attempt's body. The body is already in the upstream
// protocol, so converted requests carry both in the upstream's own fields.
// Every attempt starts again from the client's body, so retries and failover
// add the text exactly once. A Claude Code Haiku request is forwarded as it
// is.
//
// An edit the body's shape does not allow is skipped and the body forwarded
// as it was; only a failed read is an error.
func (handler *Handler) applyGatewayRewrite(
	request *http.Request,
	protocol contract.ProtocolID,
	rewrite gatewayRewrite,
	haiku bool,
) (gatewayReasoning, error) {
	prompt := rewrite.prefix != "" || rewrite.suffix != ""
	reasons := rewrite.reasoning.mode == contract.ReasoningModeFixed || rewrite.reasoning.mode == contract.ReasoningModeAdaptive
	if (!prompt && !reasons) || !rewritesProtocol(protocol) {
		return gatewayReasoning{}, nil
	}
	if haiku {
		if !reasons {
			return gatewayReasoning{}, nil
		}
		return gatewayReasoning{diagnostic: "background request (Claude Code haiku): unchanged"}, nil
	}
	if request.Body == nil || request.Body == http.NoBody {
		return gatewayReasoning{}, nil
	}
	if encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding"))); encoding != "" && encoding != "identity" {
		return gatewayReasoning{}, nil
	}
	raw, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		return gatewayReasoning{}, err
	}
	body, result := raw, gatewayReasoning{}
	if ojson.ValidBytes(raw) && ojson.ParseBytes(raw).IsObject() {
		// The signals are read before the prompt adds a system message.
		if target, ok := reasoningTargetFor(rewrite.reasoning, protocol, body); ok {
			model := upstreamRequestModel(request, protocol, body)
			edit := overrideReasoning(protocol, body, model, target, handler.codexModels.lookup(model))
			if edit.err != nil {
				edit = reasoningEdit{kept: "body could not be edited"}
			} else {
				body = edit.body
				if len(edit.changed) > 0 {
					result.effort = edit.sent
				}
			}
			result.diagnostic = target.describe(edit, protocol)
		}
		if prompt {
			for _, edit := range []struct {
				text string
				add  func(contract.ProtocolID, []byte, string) ([]byte, bool, error)
			}{{rewrite.prefix, systemprompt.Prepend}, {rewrite.suffix, systemprompt.Append}} {
				if updated, changed, editErr := edit.add(protocol, body, edit.text); editErr == nil && changed {
					body = updated
					result.systemPrompt = true
				}
			}
		}
	}
	replaceRecoveryRequestBody(request, body)
	return result, nil
}

// upstreamRequestModel is the model an upstream body asks for: Gemini names it
// in the path, the other protocols in the body.
func upstreamRequestModel(request *http.Request, protocol contract.ProtocolID, body []byte) string {
	if protocol != contract.ProtocolGoogleGenerateContent {
		return ojson.GetBytes(body, "model").Str
	}
	path := request.URL.Path
	if index := strings.LastIndex(path, "/models/"); index >= 0 {
		path = path[index+len("/models/"):]
	}
	if colon := strings.LastIndex(path, ":"); colon >= 0 {
		path = path[:colon]
	}
	return path
}

// maxCodexCatalogModels bounds the remembered Codex catalog.
const maxCodexCatalogModels = 512

// codexCatalogModels remembers the reasoning levels Codex's model catalog
// lists for each model, so the reasoning settings keep to levels the model
// takes. Model discovery fills it whenever it reads a Codex catalog.
type codexCatalogModels struct {
	mu     sync.RWMutex
	models map[string]codexModel
}

func (cache *codexCatalogModels) remember(entries []discoveryEntry) {
	for _, entry := range entries {
		if len(entry.codexCatalog) == 0 {
			continue
		}
		var model codexModel
		for _, level := range ojson.GetBytes(entry.codexCatalog, "supported_reasoning_levels.#.effort").Array() {
			if level.Type == ojson.String && reasoningRank(level.Str) >= 0 {
				model.levels = append(model.levels, strings.ToLower(level.Str))
			}
		}
		cache.mu.Lock()
		if cache.models == nil {
			cache.models = make(map[string]codexModel)
		}
		if _, known := cache.models[entry.id]; len(model.levels) == 0 {
			delete(cache.models, entry.id)
		} else if known || len(cache.models) < maxCodexCatalogModels {
			cache.models[entry.id] = model
		}
		cache.mu.Unlock()
	}
}

func (cache *codexCatalogModels) lookup(model string) codexModel {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.models[model]
}
