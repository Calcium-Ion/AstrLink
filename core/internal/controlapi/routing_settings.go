package controlapi

import (
	"encoding/json"
	"net/http"
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
		writeJSON(writer, http.StatusOK, settings)
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
			case "default_recovery_paths":
				writeError(writer, http.StatusGone, "routing_feature_retired", "default call paths are retired")
				return
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
		if err := settings.Validate(); err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_routing_settings", err.Error())
			return
		}
		if err := handler.routingSettings.UpdateRoutingSettings(request.Context(), settings); err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	default:
		writer.Header().Set("Allow", "GET, PATCH")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and PATCH are allowed")
	}
}
