package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type policyPageResponse struct {
	Items      []contract.Policy `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func (handler *Handler) registerPolicyRoutes() {
	handler.mux.HandleFunc(PolicyDryRunPath, handler.authenticated(handler.policyDryRun))
	handler.mux.HandleFunc(PrivacyRegexBuiltinRulesPath, handler.authenticated(handler.privacyRegexBuiltinRules))
	handler.mux.HandleFunc(PoliciesPath, handler.authenticated(handler.policyCollection))
	handler.mux.HandleFunc(PoliciesPath+"/", handler.authenticated(handler.policyItem))
}

func (handler *Handler) privacyRegexBuiltinRules(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "regex builtin rules do not accept query parameters")
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	writeJSON(writer, http.StatusOK, contract.PolicyRegexBuiltinRulesResponse{
		Rules: privacy.BuiltinRegexRules(),
	})
}

func (handler *Handler) policyCollection(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "policy list does not accept query parameters")
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	page, err := handler.policyStore.ListPolicies(request.Context())
	if err != nil {
		handler.writePolicyStoreError(writer, err)
		return
	}
	response := policyPageResponse{Items: make([]contract.Policy, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, item.Policy)
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) policyItem(writer http.ResponseWriter, request *http.Request) {
	rawID := strings.TrimPrefix(request.URL.Path, PoliciesPath+"/")
	if rawID == "" || strings.Contains(rawID, "/") {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_policy_id", "policy_id must use its canonical form")
		return
	}
	id := contract.PolicyID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_policy_id", "policy_id is invalid")
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.getPolicy(writer, request, id)
	case http.MethodPatch:
		handler.patchPolicy(writer, request, id)
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and PATCH are allowed")
	}
}

func (handler *Handler) getPolicy(writer http.ResponseWriter, request *http.Request, id contract.PolicyID) {
	record, err := handler.policyStore.GetPolicy(request.Context(), id)
	if err != nil {
		handler.writePolicyStoreError(writer, err)
		return
	}
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, record.Policy)
}

func (handler *Handler) patchPolicy(writer http.ResponseWriter, request *http.Request, id contract.PolicyID) {
	if !requireMediaType(writer, request, "application/merge-patch+json") {
		return
	}
	expectedETag := request.Header.Get("If-Match")
	if expectedETag == "" {
		writeError(writer, http.StatusBadRequest, "if_match_required", "If-Match is required")
		return
	}
	var patch map[string]json.RawMessage
	if !decodeControlJSON(writer, request, &patch) {
		return
	}
	if len(patch) == 0 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_policy_patch", "policy patch must contain at least one property")
		return
	}

	handler.privacyMu.Lock()
	defer handler.privacyMu.Unlock()
	current, err := handler.policyStore.GetPolicy(request.Context(), id)
	if err != nil {
		handler.writePolicyStoreError(writer, err)
		return
	}
	if current.ETag != expectedETag {
		handler.writePolicyStoreError(writer, storage.ErrPrecondition)
		return
	}
	updated, err := applyPolicyPatch(current.Policy, patch)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_policy_patch", "policy patch violates the contract")
		return
	}
	if !handler.requireReadyLocalModel(writer, updated) {
		return
	}
	record, err := handler.policyStore.UpdatePolicy(request.Context(), updated, expectedETag)
	if err != nil {
		handler.writePolicyStoreError(writer, err)
		return
	}
	if handler.policyChanged != nil {
		handler.policyChanged(record.Policy)
	}
	writer.Header().Set("ETag", record.ETag)
	writeJSON(writer, http.StatusOK, record.Policy)
}

func (handler *Handler) policyDryRun(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "policy dry-run does not accept query parameters")
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if handler.privacyFilter == nil {
		writeError(writer, http.StatusServiceUnavailable, "safety_engine_unavailable", "local safety engine is unavailable")
		return
	}
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input contract.PolicyDryRunRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if err := input.Protocol.Validate(); err != nil || !privacy.SupportsInspection(input.Protocol) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_policy_dry_run", "protocol does not support privacy inspection")
		return
	}
	body, err := privacy.WrapSampleText(input.Protocol, input.SampleText)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_policy_dry_run", "sample_text is invalid")
		return
	}

	record, err := handler.policyStore.GetPolicy(request.Context(), contract.DefaultPrivacyPolicyID)
	if err != nil {
		handler.writePolicyStoreError(writer, err)
		return
	}
	effective := record.Policy
	if len(input.Policy) > 0 {
		effective, err = applyPolicyPatch(record.Policy, input.Policy)
		if err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_policy_dry_run", "policy override violates the contract")
			return
		}
	}
	if !handler.requireReadyLocalModel(writer, effective) {
		return
	}
	runtimePolicy, err := privacy.FromContractPolicy(effective)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_policy_dry_run", "policy override violates the contract")
		return
	}
	result, err := handler.privacyFilter.Inspect(request.Context(), runtimePolicy, input.Protocol, body)
	if err != nil {
		handler.writePrivacyDryRunError(writer, err)
		return
	}
	findings := result.Findings
	if findings == nil {
		findings = []privacy.Finding{}
	}
	locations, err := privacy.LocateFindings(input.Protocol, body, findings)
	if err != nil {
		handler.writePrivacyDryRunError(writer, err)
		return
	}
	if locations == nil {
		locations = []contract.PolicyDryRunFinding{}
	}
	suppressed := result.SuppressedFindings
	if suppressed == nil {
		suppressed = []privacy.Finding{}
	}
	suppressedLocations, err := privacy.LocateFindings(input.Protocol, body, suppressed)
	if err != nil {
		handler.writePrivacyDryRunError(writer, err)
		return
	}
	if suppressedLocations == nil {
		suppressedLocations = []contract.PolicyDryRunFinding{}
	}
	response := contract.PolicyDryRunResponse{
		Decision:           string(result.Decision),
		FindingsSummary:    privacy.WarningSummary(findings),
		Findings:           locations,
		SuppressedFindings: suppressedLocations,
		InspectedBody:      string(body),
		Redactions:         make([]contract.PolicyDryRunRedaction, 0, len(result.Redactions)),
	}
	for _, redaction := range result.Redactions {
		response.Redactions = append(response.Redactions, contract.PolicyDryRunRedaction{
			Placeholder: redaction.Placeholder,
			Kind:        string(redaction.Kind),
			Value:       redaction.Value,
			Style:       redaction.Style,
		})
	}
	if result.Decision == privacy.DecisionRedact && len(result.Body) > 0 {
		redacted := string(result.Body)
		response.RedactedBody = &redacted
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) requireReadyLocalModel(writer http.ResponseWriter, policy contract.Policy) bool {
	if !policy.Enabled || policy.Detector != contract.PolicyDetectorLocalModel {
		return true
	}
	if policy.LocalModelID == nil || handler.privacyModels == nil {
		writeError(writer, http.StatusConflict, "privacy_model_not_ready", "the local privacy model must be ready before this policy can be enabled")
		return false
	}
	if _, ready := handler.privacyModels.ReadyInstallation(*policy.LocalModelID); !ready {
		writeError(writer, http.StatusConflict, "privacy_model_not_ready", "the local privacy model must be ready before this policy can be enabled")
		return false
	}
	return true
}

// patchablePolicyFields is the closed set of merge-patch keys the control plane
// accepts for the singleton privacy policy. Everything else, including identity
// and match scope, is fixed by ValidatePrivacyDefault.
var patchablePolicyFields = map[string]bool{
	"enabled":                true,
	"detector":               true,
	"local_model_id":         true,
	"min_confidence":         true,
	"regex_source":           true,
	"custom_regex_rules":     true,
	"kind_rules":             true,
	"allowlist_rules":        true,
	"request_action":         true,
	"response_restore":       true,
	"restore_tool_arguments": true,
	"placeholder_notice":     true,
}

func applyPolicyPatch(policy contract.Policy, patch map[string]json.RawMessage) (contract.Policy, error) {
	for name, raw := range patch {
		if !patchablePolicyFields[name] {
			return policy, errors.New("unknown or immutable policy field")
		}
		if isJSONNull(raw) && name != "local_model_id" {
			return policy, errors.New("policy fields cannot be deleted")
		}
		switch name {
		case "enabled":
			if err := strictUnmarshal(raw, &policy.Enabled); err != nil {
				return policy, err
			}
		case "detector":
			if err := strictUnmarshal(raw, &policy.Detector); err != nil {
				return policy, err
			}
		case "local_model_id":
			if err := strictUnmarshal(raw, &policy.LocalModelID); err != nil {
				return policy, err
			}
		case "min_confidence":
			if err := strictUnmarshal(raw, &policy.MinConfidence); err != nil {
				return policy, err
			}
		case "regex_source":
			if err := strictUnmarshal(raw, &policy.RegexSource); err != nil {
				return policy, err
			}
		case "custom_regex_rules":
			var rules []contract.PolicyRegexRule
			if err := strictUnmarshal(raw, &rules); err != nil {
				return policy, err
			}
			policy.CustomRegexRules = rules
		case "request_action":
			if err := strictUnmarshal(raw, &policy.RequestAction); err != nil {
				return policy, err
			}
		case "response_restore":
			if err := strictUnmarshal(raw, &policy.ResponseRestore); err != nil {
				return policy, err
			}
		case "kind_rules":
			var rules []contract.PolicyKindRule
			if err := strictUnmarshal(raw, &rules); err != nil {
				return policy, err
			}
			policy.KindRules = rules
		case "allowlist_rules":
			var rules []contract.PolicyAllowlistRule
			if err := strictUnmarshal(raw, &rules); err != nil {
				return policy, err
			}
			policy.AllowlistRules = rules
		case "restore_tool_arguments":
			if err := strictUnmarshal(raw, &policy.RestoreToolArguments); err != nil {
				return policy, err
			}
		case "placeholder_notice":
			if err := strictUnmarshal(raw, &policy.PlaceholderNotice); err != nil {
				return policy, err
			}
		}
	}
	contract.NormalizePrivacyPolicyDefaults(&policy)
	if err := contract.ValidatePrivacyDefault(policy); err != nil {
		return policy, err
	}
	return policy, nil
}

func (handler *Handler) writePolicyStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "policy not found")
	case errors.Is(err, storage.ErrPrecondition):
		writeError(writer, http.StatusPreconditionFailed, "precondition_failed", "If-Match does not match the current policy")
	case errors.Is(err, storage.ErrInvalidArgument):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_policy", "policy violates the storage contract")
	case errors.Is(err, storage.ErrInvalidRecord):
		writeError(writer, http.StatusInternalServerError, "persisted_state_invalid", "persisted policy state failed validation")
	default:
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "persistent policy storage is unavailable")
	}
}

func (handler *Handler) writePrivacyDryRunError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, privacy.ErrPolicyUnavailable):
		writeError(writer, http.StatusServiceUnavailable, "privacy_policy_unavailable", "privacy policy could not be resolved")
	case errors.Is(err, privacy.ErrDetectorUnavailable),
		errors.Is(err, privacy.ErrDetectorLimit),
		errors.Is(err, privacy.ErrDetectorTimeout):
		writeError(writer, http.StatusServiceUnavailable, "safety_engine_unavailable", "local safety engine is unavailable")
	case errors.Is(err, privacy.ErrUnsafeInput), errors.Is(err, privacy.ErrUnsafeRewrite):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_policy_dry_run", "sample cannot be inspected safely")
	default:
		writeError(writer, http.StatusServiceUnavailable, "safety_engine_unavailable", "local safety engine is unavailable")
	}
}
