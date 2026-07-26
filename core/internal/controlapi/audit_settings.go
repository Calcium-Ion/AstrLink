package controlapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const AuditSettingsPath = "/control/v1/audit-settings"

func (handler *Handler) registerAuditSettingsRoutes() {
	handler.mux.HandleFunc(AuditSettingsPath, handler.authenticated(handler.auditSettingsResource))
}

func (handler *Handler) auditSettingsResource(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		handler.getAuditSettings(writer, request)
	case http.MethodPatch:
		handler.patchAuditSettings(writer, request)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and PATCH are allowed")
	}
}

func (handler *Handler) getAuditSettings(writer http.ResponseWriter, request *http.Request) {
	settings, err := handler.auditSettings.GetAuditSettings(request.Context())
	if err != nil {
		handler.writeAuditSettingsStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, settings)
}

func (handler *Handler) patchAuditSettings(writer http.ResponseWriter, request *http.Request) {
	if !requireMediaType(writer, request, "application/merge-patch+json") {
		return
	}
	var raw map[string]json.RawMessage
	if !decodeControlJSON(writer, request, &raw) {
		return
	}
	if len(raw) == 0 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_audit_settings_patch", "audit settings patch must contain at least one property")
		return
	}
	current, err := handler.auditSettings.GetAuditSettings(request.Context())
	if err != nil {
		handler.writeAuditSettingsStoreError(writer, err)
		return
	}
	updated, err := applyAuditSettingsPatch(current, raw)
	if err != nil {
		var validation *auditValidationError
		if errors.As(err, &validation) {
			writeValidationFailed(writer, validation.Message, validation.Details)
			return
		}
		writeError(writer, http.StatusUnprocessableEntity, "invalid_audit_settings_patch", err.Error())
		return
	}
	if err := updated.Validate(); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_audit_settings", err.Error())
		return
	}
	if err := handler.auditSettings.UpdateAuditSettings(request.Context(), updated); err != nil {
		handler.writeAuditSettingsStoreError(writer, err)
		return
	}
	if (updated.RequestBodyEnabled || updated.ResponseContentEnabled) && handler.auditKeys != nil {
		if _, err := handler.auditKeys.GetOrCreateAuditKey(request.Context()); err != nil {
			writeError(writer, http.StatusInternalServerError, "audit_key_unavailable", "audit encryption key could not be prepared")
			return
		}
	}
	writeJSON(writer, http.StatusOK, updated)
}

type auditValidationError struct {
	Message string
	Details []errorDetail
}

func (err *auditValidationError) Error() string { return err.Message }

func applyAuditSettingsPatch(
	settings contract.AuditSettings,
	patch map[string]json.RawMessage,
) (contract.AuditSettings, error) {
	var enablingCapture bool
	var acknowledged *bool
	for name, raw := range patch {
		switch name {
		case "request_body_enabled":
			if isJSONNull(raw) {
				return settings, errors.New("request_body_enabled cannot be deleted")
			}
			var value bool
			if err := strictUnmarshal(raw, &value); err != nil {
				return settings, err
			}
			// Require ack when flipping false→true (and whenever the patch asserts true).
			if value {
				enablingCapture = true
			}
			settings.RequestBodyEnabled = value
		case "response_content_enabled":
			if isJSONNull(raw) {
				return settings, errors.New("response_content_enabled cannot be deleted")
			}
			var value bool
			if err := strictUnmarshal(raw, &value); err != nil {
				return settings, err
			}
			if value {
				enablingCapture = true
			}
			settings.ResponseContentEnabled = value
		case "request_body_max_bytes":
			if isJSONNull(raw) {
				return settings, errors.New("request_body_max_bytes cannot be deleted")
			}
			var value int
			if err := strictUnmarshal(raw, &value); err != nil {
				return settings, err
			}
			settings.RequestBodyMaxBytes = value
		case "response_content_max_bytes":
			if isJSONNull(raw) {
				return settings, errors.New("response_content_max_bytes cannot be deleted")
			}
			var value int
			if err := strictUnmarshal(raw, &value); err != nil {
				return settings, err
			}
			settings.ResponseContentMaxBytes = value
		case "metadata_retention_days":
			if isJSONNull(raw) {
				return settings, errors.New("metadata_retention_days cannot be deleted")
			}
			var value int
			if err := strictUnmarshal(raw, &value); err != nil {
				return settings, err
			}
			settings.MetadataRetentionDays = value
		case "content_retention_days":
			if isJSONNull(raw) {
				return settings, errors.New("content_retention_days cannot be deleted")
			}
			var value int
			if err := strictUnmarshal(raw, &value); err != nil {
				return settings, err
			}
			settings.ContentRetentionDays = value
		case "audit_risk_acknowledged":
			if isJSONNull(raw) {
				return settings, errors.New("audit_risk_acknowledged cannot be null")
			}
			var value bool
			if err := strictUnmarshal(raw, &value); err != nil {
				return settings, err
			}
			acknowledged = &value
		case "extensions":
			if isJSONNull(raw) {
				settings.Extensions = nil
				continue
			}
			var extensions map[string]any
			if err := strictUnmarshal(raw, &extensions); err != nil {
				return settings, err
			}
			settings.Extensions = extensions
		default:
			return settings, errors.New("unknown or immutable audit settings field")
		}
	}
	if enablingCapture {
		if acknowledged == nil || !*acknowledged {
			return settings, &auditValidationError{
				Message: "enabling body audit requires audit_risk_acknowledged=true",
				Details: []errorDetail{{
					Field:  "audit_risk_acknowledged",
					Reason: "must be true when enabling request_body_enabled or response_content_enabled",
				}},
			}
		}
	}
	// writeOnly: never persist or echo acknowledgement; it is consumed above.
	_ = acknowledged
	return settings, nil
}

func (handler *Handler) writeAuditSettingsStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "audit settings not found")
	case errors.Is(err, storage.ErrInvalidArgument), errors.Is(err, storage.ErrInvalidRecord):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_audit_settings", "audit settings are invalid")
	default:
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "audit settings storage is unavailable")
	}
}
