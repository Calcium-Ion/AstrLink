package controlapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const RoutingSettingsPath = "/control/v1/routing-settings"

func (handler *Handler) routingSettingsResource(writer http.ResponseWriter, request *http.Request) {
	if handler.routingSettings == nil {
		writeError(writer, http.StatusServiceUnavailable, "routing_settings_unavailable", "routing settings are unavailable")
		return
	}
	switch request.Method {
	case http.MethodGet:
		settings, err := handler.routingSettings.GetRoutingSettings(request.Context())
		if err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, normalizeRoutingSettings(settings))
	case http.MethodPatch:
		if !requireMediaType(writer, request, "application/merge-patch+json") {
			return
		}
		var patch map[string]json.RawMessage
		if !decodeControlJSON(writer, request, &patch) {
			return
		}
		handler.routingSettingsMu.Lock()
		defer handler.routingSettingsMu.Unlock()
		settings, err := handler.routingSettings.GetRoutingSettings(request.Context())
		if err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		if len(patch) == 0 {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_routing_settings", "patch must change at least one field")
			return
		}
		for key, raw := range patch {
			var destination any
			switch key {
			case "codex_identity_enforcement":
				destination = &settings.CodexIdentityEnforcement
			case "claude_identity_enforcement":
				destination = &settings.ClaudeIdentityEnforcement
			case "grok_identity_enforcement":
				destination = &settings.GrokIdentityEnforcement
			case "subscription_risk_protection":
				destination = &settings.SubscriptionRiskProtection
			case "codex_request_normalization":
				destination = &settings.CodexRequestNormalization
			case "claude_request_normalization":
				destination = &settings.ClaudeRequestNormalization
			case "subscription_session_isolation":
				destination = &settings.SubscriptionSessionIsolation
			case "official_client_passthrough":
				destination = &settings.OfficialClientPassthrough
			case "claude_identity_auto_learn":
				destination = &settings.ClaudeIdentityAutoLearn
			case "codex_identity_auto_learn":
				destination = &settings.CodexIdentityAutoLearn
			case "grok_identity_auto_learn":
				destination = &settings.GrokIdentityAutoLearn
			case "claude_identity_version":
				destination = &settings.ClaudeIdentityVersion
			case "codex_identity_version":
				destination = &settings.CodexIdentityVersion
			case "grok_identity_version":
				destination = &settings.GrokIdentityVersion
			case "system_prompt_enabled":
				destination = &settings.SystemPromptEnabled
			case "system_prompt_prefix":
				destination = &settings.SystemPromptPrefix
			case "system_prompt_suffix":
				destination = &settings.SystemPromptSuffix
			case "reasoning_mode":
				destination = &settings.ReasoningMode
			case "reasoning_level":
				destination = &settings.ReasoningLevel
			case "adaptive_preset":
				destination = &settings.AdaptivePreset
			case "adaptive_min":
				destination = &settings.AdaptiveMin
			case "adaptive_max":
				destination = &settings.AdaptiveMax
			case "image_compression":
				destination = &settings.ImageCompression
			case "default_recovery_paths":
				writeError(writer, http.StatusGone, "routing_feature_retired", "default call paths are retired")
				return
			case "builtin_tools":
				destination = &settings.BuiltinTools
			case "model_redirects":
				destination = &settings.ModelRedirects
			case "model_routing_groups":
				destination = &settings.ModelRoutingGroups
			case "channel_stickiness":
				destination = &settings.ChannelStickiness
			case "default_failure_policy":
				destination = &settings.DefaultFailurePolicy
			case "allow_unmatched_failover":
				destination = &settings.AllowUnmatchedFailover
			case "strategy":
				destination = &settings.Strategy
			case "max_attempts":
				destination = &settings.MaxAttempts
			default:
				writeError(writer, http.StatusUnprocessableEntity, "invalid_routing_settings", "unknown field")
				return
			}
			if isJSONNull(raw) || strictUnmarshal(raw, destination) != nil {
				writeError(writer, http.StatusUnprocessableEntity, "invalid_routing_settings", "invalid field value")
				return
			}
		}
		settings = normalizeRoutingSettings(settings)
		// The dimensions live in the classifier-settings singleton, not in this
		// document, so the save path supplies them: without the read a rule
		// naming a user-defined dimension would be refused here even though the
		// dimension-aware validator accepts it, and the feature would be
		// unreachable through the API entirely. A store that cannot be read
		// leaves the set empty, which is the built-in-only validation this path
		// has always applied.
		var dimensions []contract.ClassifierDimension
		if handler.classifierSettings != nil {
			if classifier, readErr := handler.classifierSettings.GetClassifierSettings(request.Context()); readErr == nil {
				dimensions = classifier.Dimensions
			}
		}
		if err := settings.ValidateWithDimensions(dimensions); err != nil {
			code := "invalid_routing_settings"
			if strings.Contains(err.Error(), "unsupported_routing_field") {
				code = "unsupported_routing_field"
			}
			writeError(writer, http.StatusUnprocessableEntity, code, err.Error())
			return
		}
		if _, ok := patch["builtin_tools"]; ok && !handler.toolProvidersUsable(writer, request, settings.BuiltinTools) {
			return
		}
		var updateErr error
		if coordinator, ok := handler.routingSettings.(storage.ModelRoutingWriteCoordinator); ok {
			updateErr = coordinator.UpdateRoutingSettingsAtomic(request.Context(), settings)
		} else {
			updateErr = handler.routingSettings.UpdateRoutingSettings(request.Context(), settings)
		}
		if updateErr != nil {
			handler.writeStoreError(writer, updateErr)
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	default:
		writer.Header().Set("Allow", "GET, PATCH")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and PATCH are allowed")
	}
}

// toolProvidersUsable checks each turned-on tool that calls a provider's own
// API, image generation through its Images API or web search through its
// search API, against that provider, and answers the request when the
// provider cannot serve it. The mistake then shows on save instead of on
// every tool call. A turned-off tool keeps any choice, so a stale one can
// always be turned off. Whether the provider is enabled and connected is left
// to each call. Each refusal has its own code, which the desktop shows in the
// operator's language.
func (handler *Handler) toolProvidersUsable(writer http.ResponseWriter, request *http.Request, tools *contract.BuiltinTools) bool {
	if tools == nil {
		return true
	}
	for _, check := range []struct {
		kind, code, backend, unsupported string
		tool                             contract.BuiltinTool
		serves                           func(contract.ServiceKind) bool
	}{
		{"image_generation", "image", "service_images", "the selected provider has no Images API. Pick an OpenAI, OpenAI-compatible, New API or MiniMax provider, or use the upstream backend with a Codex subscription.", tools.ImageGeneration, contract.BuiltinImagesServiceKind},
		{"web_search", "search", "service_search", "the selected provider has no search API. Pick a Codex subscription or New API provider.", tools.WebSearch, contract.BuiltinSearchServiceKind},
	} {
		if !check.tool.Enabled || check.tool.Backend != check.backend {
			continue
		}
		record, err := handler.serviceStore.GetService(request.Context(), check.tool.ServiceID)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(writer, http.StatusUnprocessableEntity, check.code+"_provider_not_found", check.kind+": the selected provider does not exist")
		case err != nil:
			handler.writeStoreError(writer, err)
		case !check.serves(record.Service.Kind):
			writeError(writer, http.StatusUnprocessableEntity, check.code+"_provider_unsupported", check.kind+": "+check.unsupported)
		default:
			continue
		}
		return false
	}
	return true
}

// normalizeRoutingSettings keeps model_redirects an array on the wire even if
// a store returns a nil table.
func normalizeRoutingSettings(settings contract.RoutingSettings) contract.RoutingSettings {
	if settings.ModelRedirects == nil {
		settings.ModelRedirects = []contract.ModelRedirect{}
	}
	if settings.ModelRoutingGroups == nil {
		settings.ModelRoutingGroups = []contract.ModelRoutingGroup{}
	}
	return settings
}
