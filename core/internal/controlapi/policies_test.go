package controlapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestPolicyControlAPIListsGetsAndPatchesFixedPrivacyPolicy(t *testing.T) {
	store, handler, model := newPolicyHandler(t)

	response := policyRequest(t, handler, http.MethodGet, PoliciesPath, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var page policyPageResponse
	decode(t, response, &page)
	if len(page.Items) != 1 || page.NextCursor != nil ||
		!reflect.DeepEqual(page.Items[0], contract.DefaultPrivacyPolicy()) {
		t.Fatalf("policy page=%#v", page)
	}

	response = policyRequest(
		t, handler, http.MethodGet,
		PoliciesPath+"/"+string(contract.DefaultPrivacyPolicyID), "", "", "",
	)
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" {
		t.Fatalf("get status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
	etag := response.Header().Get("ETag")

	response = policyRequest(
		t, handler, http.MethodPatch,
		PoliciesPath+"/"+string(contract.DefaultPrivacyPolicyID),
		"application/merge-patch+json",
		`{"enabled":true}`,
		etag,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("regex patch status=%d body=%s", response.Code, response.Body.String())
	}
	var regexPolicy contract.Policy
	decode(t, response, &regexPolicy)
	if !regexPolicy.Enabled || regexPolicy.Detector != contract.PolicyDetectorRegex ||
		regexPolicy.ResponseAction != contract.PolicyActionAllow {
		t.Fatalf("regex policy=%#v", regexPolicy)
	}
	regexETag := response.Header().Get("ETag")

	response = policyRequest(
		t, handler, http.MethodPatch,
		PoliciesPath+"/"+string(contract.DefaultPrivacyPolicyID),
		"application/merge-patch+json",
		`{"detector":"local_model","local_model_id":"model_de5ac42e03b4af887b31a7645d3ce111"}`,
		regexETag,
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("model-not-ready status=%d body=%s", response.Code, response.Body.String())
	}
	current, err := store.GetPolicy(context.Background(), contract.DefaultPrivacyPolicyID)
	if err != nil || current.ETag != regexETag || current.Policy.Detector != contract.PolicyDetectorRegex {
		t.Fatalf("rejected patch changed policy=%#v, %v", current, err)
	}

	model.ready[contract.LegacyOpenAIPrivacyFilterInstallationID] = true
	response = policyRequest(
		t, handler, http.MethodPatch,
		PoliciesPath+"/"+string(contract.DefaultPrivacyPolicyID),
		"application/merge-patch+json",
		`{"detector":"local_model","local_model_id":"model_de5ac42e03b4af887b31a7645d3ce111","min_confidence":0.9,"request_action":"redact"}`,
		regexETag,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("ready model patch status=%d body=%s", response.Code, response.Body.String())
	}
	var modelPolicy contract.Policy
	decode(t, response, &modelPolicy)
	if modelPolicy.Detector != contract.PolicyDetectorLocalModel ||
		modelPolicy.LocalModelID == nil ||
		*modelPolicy.LocalModelID != contract.LegacyOpenAIPrivacyFilterInstallationID ||
		modelPolicy.MinConfidence != 0.9 ||
		!modelPolicy.Enabled {
		t.Fatalf("model policy=%#v", modelPolicy)
	}
}

func TestPolicyPatchRequiresETagAndRejectsMutableOrInvalidFields(t *testing.T) {
	_, handler, _ := newPolicyHandler(t)
	path := PoliciesPath + "/" + string(contract.DefaultPrivacyPolicyID)
	get := policyRequest(t, handler, http.MethodGet, path, "", "", "")
	etag := get.Header().Get("ETag")
	tests := []struct {
		name        string
		contentType string
		body        string
		etag        string
		status      int
	}{
		{name: "media type", contentType: "application/json", body: `{}`, etag: etag, status: http.StatusUnsupportedMediaType},
		{name: "missing etag", contentType: "application/merge-patch+json", body: `{"enabled":true}`, status: http.StatusBadRequest},
		{name: "stale etag", contentType: "application/merge-patch+json", body: `{"enabled":true}`, etag: `"stale"`, status: http.StatusPreconditionFailed},
		{name: "empty patch", contentType: "application/merge-patch+json", body: `{}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "immutable name", contentType: "application/merge-patch+json", body: `{"name":"other"}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "immutable match", contentType: "application/merge-patch+json", body: `{"match":{}}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "immutable response action", contentType: "application/merge-patch+json", body: `{"response_action":"allow"}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "null", contentType: "application/merge-patch+json", body: `{"detector":null}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "invalid detector", contentType: "application/merge-patch+json", body: `{"detector":"remote"}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "invalid action", contentType: "application/merge-patch+json", body: `{"request_action":"drop"}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "null confidence", contentType: "application/merge-patch+json", body: `{"min_confidence":null}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "negative confidence", contentType: "application/merge-patch+json", body: `{"min_confidence":-0.01}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "confidence above one", contentType: "application/merge-patch+json", body: `{"min_confidence":1.01}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "string confidence", contentType: "application/merge-patch+json", body: `{"min_confidence":"0.8"}`, etag: etag, status: http.StatusUnprocessableEntity},
		{name: "response restore off", contentType: "application/merge-patch+json", body: `{"response_restore":false}`, etag: etag, status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := policyRequest(
				t, handler, http.MethodPatch, path,
				test.contentType, test.body, test.etag,
			)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if test.status == http.StatusOK {
				var record struct {
					Policy contract.Policy `json:"policy"`
				}
				decode(t, response, &record)
				if record.Policy.ResponseRestore {
					t.Fatalf("response_restore still enabled: %#v", record.Policy)
				}
			}
		})
	}
	for _, operation := range []struct {
		method string
		path   string
		allow  string
	}{
		{http.MethodPost, PoliciesPath, http.MethodGet},
		{http.MethodDelete, path, "GET, PATCH"},
	} {
		response := policyRequest(t, handler, operation.method, operation.path, "", "", "")
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != operation.allow {
			t.Fatalf("%s status=%d allow=%q", operation.method, response.Code, response.Header().Get("Allow"))
		}
	}
}

func TestPolicyAndPrivacyModelRoutesRequireAuthenticationAndNoStore(t *testing.T) {
	_, handler, _ := newPolicyHandler(t)
	for _, operation := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: PoliciesPath},
		{method: http.MethodPost, path: PolicyDryRunPath},
		{method: http.MethodGet, path: PrivacyModelCatalogPath},
		{method: http.MethodGet, path: PrivacyModelsPath},
		{method: http.MethodPost, path: PrivacyModelProbePath},
		{method: http.MethodPost, path: PrivacyModelLocalProbePath},
	} {
		request := httptest.NewRequest(operation.method, operation.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized ||
			response.Header().Get("Cache-Control") != "no-store" ||
			response.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s %s status=%d headers=%#v", operation.method, operation.path, response.Code, response.Header())
		}
	}
}

func TestPolicyChangeHookRunsSynchronously(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var changed []contract.Policy
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			PolicyStore:   store,
			PrivacyModels: newFakePrivacyModelRegistry(),
			PolicyChanged: func(policy contract.Policy) {
				changed = append(changed, policy)
			},
			ControlToken: testControlToken,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	path := PoliciesPath + "/" + string(contract.DefaultPrivacyPolicyID)
	get := policyRequest(t, handler, http.MethodGet, path, "", "", "")
	patched := policyRequest(
		t,
		handler,
		http.MethodPatch,
		path,
		"application/merge-patch+json",
		`{"enabled":true}`,
		get.Header().Get("ETag"),
	)
	if patched.Code != http.StatusOK || len(changed) != 1 || !changed[0].Enabled {
		t.Fatalf("patch status=%d changed=%#v", patched.Code, changed)
	}
}

func TestPolicyDryRunPreviewsRegexRedactAndRespectsOverrides(t *testing.T) {
	_, handler, model := newPolicyHandler(t)
	provider, err := privacy.NewStorePolicyProvider(handler.policyStore)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := privacy.New(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.privacyFilter = filter

	response := policyRequest(
		t, handler, http.MethodPost, PolicyDryRunPath, "application/json",
		`{"protocol":"openai.chat","sample_text":"email alice@example.com","policy":{"enabled":true,"detector":"regex","local_model_id":null,"request_action":"redact"}}`,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run status=%d body=%s", response.Code, response.Body.String())
	}
	var result contract.PolicyDryRunResponse
	decode(t, response, &result)
	if result.Decision != "redact" || result.FindingsSummary == "" ||
		!strings.Contains(result.FindingsSummary, "email=") ||
		result.RedactedBody == nil || strings.Contains(*result.RedactedBody, "alice@example.com") ||
		strings.Contains(result.InspectedBody, "alice@example.com") == false {
		t.Fatalf("dry-run result=%#v", result)
	}
	if len(result.Findings) == 0 || result.Findings[0].Kind != "email" || result.Findings[0].Path == "" {
		t.Fatalf("findings=%#v", result.Findings)
	}
	if result.Findings[0].Confidence != 1 || len(result.SuppressedFindings) != 0 {
		t.Fatalf("confidence dry-run fields=%#v suppressed=%#v", result.Findings, result.SuppressedFindings)
	}
	for _, finding := range result.Findings {
		encoded, _ := json.Marshal(finding)
		if strings.Contains(string(encoded), "alice@example.com") {
			t.Fatalf("plaintext leaked in finding: %s", encoded)
		}
	}
	if len(result.Redactions) == 0 ||
		result.Redactions[0].Placeholder == "" ||
		result.Redactions[0].Kind != "email" ||
		result.Redactions[0].Value != "alice@example.com" {
		t.Fatalf("redactions=%#v", result.Redactions)
	}
	// email ships with the natural style, so the stand-in is a well-formed
	// address under the permanently unresolvable .invalid TLD.
	placeholder := result.Redactions[0].Placeholder
	if result.Redactions[0].Style != contract.PlaceholderStyleNatural ||
		!strings.HasPrefix(placeholder, "redacted-") ||
		!strings.HasSuffix(placeholder, "@private.invalid") {
		t.Fatalf("natural placeholder=%q style=%q", placeholder, result.Redactions[0].Style)
	}
	if !strings.Contains(*result.RedactedBody, placeholder) {
		t.Fatalf("redacted body does not contain generated placeholder: %s", *result.RedactedBody)
	}

	response = policyRequest(
		t, handler, http.MethodPost, PolicyDryRunPath, "application/json",
		`{"protocol":"openai.chat","sample_text":"email alice@example.com",`+
			`"policy":{"enabled":true,"request_action":"redact",`+
			`"kind_rules":[{"kind":"email","enabled":true,"style":"token"}]}}`,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("token dry-run status=%d body=%s", response.Code, response.Body.String())
	}
	var tokenStyle contract.PolicyDryRunResponse
	decode(t, response, &tokenStyle)
	if len(tokenStyle.Redactions) != 1 ||
		tokenStyle.Redactions[0].Style != contract.PlaceholderStyleToken {
		t.Fatalf("token override redactions=%#v", tokenStyle.Redactions)
	}
	const emailPlaceholderPrefix = "<PRIVATE_EMAIL_"
	tokenPlaceholder := tokenStyle.Redactions[0].Placeholder
	if !strings.HasPrefix(tokenPlaceholder, emailPlaceholderPrefix) ||
		!strings.HasSuffix(tokenPlaceholder, ">") {
		t.Fatalf("token placeholder=%q", tokenPlaceholder)
	}
	suffix := strings.TrimSuffix(strings.TrimPrefix(tokenPlaceholder, emailPlaceholderPrefix), ">")
	if len(suffix) != 16 {
		t.Fatalf("token placeholder suffix=%q", suffix)
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		t.Fatalf("token placeholder suffix=%q: %v", suffix, err)
	}

	// A patch that tries to dress a credential up as a usable-looking value must
	// be refused: the shape of a secret placeholder is a safety property.
	response = policyRequest(
		t, handler, http.MethodPost, PolicyDryRunPath, "application/json",
		`{"protocol":"openai.chat","sample_text":"x",`+
			`"policy":{"kind_rules":[{"kind":"common_secret","enabled":true,"style":"natural"}]}}`,
		"",
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("natural secret dry-run status=%d body=%s", response.Code, response.Body.String())
	}

	response = policyRequest(
		t, handler, http.MethodPost, PolicyDryRunPath, "application/json",
		`{"protocol":"openai.chat","sample_text":"email alice@example.com","policy":{"enabled":false}}`,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("disabled dry-run status=%d body=%s", response.Code, response.Body.String())
	}
	var disabled contract.PolicyDryRunResponse
	decode(t, response, &disabled)
	if disabled.Decision != "allow" || len(disabled.Findings) != 0 || disabled.RedactedBody != nil {
		t.Fatalf("disabled result=%#v", disabled)
	}

	response = policyRequest(
		t, handler, http.MethodPost, PolicyDryRunPath, "application/json",
		`{"protocol":"openai.models","sample_text":"x"}`,
		"",
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid protocol status=%d body=%s", response.Code, response.Body.String())
	}
	response = policyRequest(
		t, handler, http.MethodPost, PolicyDryRunPath, "application/json",
		`{"protocol":"openai.chat","sample_text":""}`,
		"",
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty sample status=%d body=%s", response.Code, response.Body.String())
	}

	response = policyRequest(
		t, handler, http.MethodPost, PolicyDryRunPath, "application/json",
		`{"protocol":"openai.chat","sample_text":"x","policy":{"enabled":true,"detector":"local_model","local_model_id":"model_de5ac42e03b4af887b31a7645d3ce111","request_action":"block"}}`,
		"",
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("model-not-ready status=%d body=%s", response.Code, response.Body.String())
	}
	_ = model
}

func TestPolicyDryRunSeparatesAcceptedAndSuppressedModelFindings(t *testing.T) {
	_, handler, models := newPolicyHandler(t)
	models.ready[contract.LegacyOpenAIPrivacyFilterInstallationID] = true
	provider, err := privacy.NewStorePolicyProvider(handler.policyStore)
	if err != nil {
		t.Fatal(err)
	}
	detector := privacy.DetectorFunc(func(_ context.Context, input privacy.DetectInput) ([]privacy.Finding, error) {
		if len(input.Segments) != 1 || input.Segments[0].Value != "画一张猫的图片" {
			t.Fatalf("detector input=%#v", input)
		}
		return []privacy.Finding{{
			Segment: 0, Start: 0, End: len(input.Segments[0].Value),
			Kind: privacy.KindPerson, Confidence: 0.596717,
		}}, nil
	})
	filter, err := privacy.New(provider, detector)
	if err != nil {
		t.Fatal(err)
	}
	handler.privacyFilter = filter

	for _, test := range []struct {
		name           string
		minConfidence  float64
		wantDecision   string
		wantFindings   int
		wantSuppressed int
	}{
		{
			name:          "default threshold suppresses",
			minConfidence: contract.DefaultPrivacyMinConfidence,
			wantDecision:  "allow", wantSuppressed: 1,
		},
		{
			name: "lower threshold accepts", minConfidence: 0.59,
			wantDecision: "block", wantFindings: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"protocol":"openai.responses","sample_text":"画一张猫的图片","policy":{"enabled":true,"detector":"local_model","local_model_id":"%s","min_confidence":%.2f,"request_action":"block"}}`,
				contract.LegacyOpenAIPrivacyFilterInstallationID,
				test.minConfidence,
			)
			response := policyRequest(
				t, handler, http.MethodPost, PolicyDryRunPath,
				"application/json", body, "",
			)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var result contract.PolicyDryRunResponse
			decode(t, response, &result)
			if result.Decision != test.wantDecision ||
				len(result.Findings) != test.wantFindings ||
				len(result.SuppressedFindings) != test.wantSuppressed {
				t.Fatalf("result=%#v", result)
			}
			candidates := append(
				append([]contract.PolicyDryRunFinding{}, result.Findings...),
				result.SuppressedFindings...,
			)
			if len(candidates) != 1 ||
				candidates[0].Confidence != 0.596717 ||
				candidates[0].Kind != "private_person" ||
				candidates[0].Path == "" {
				t.Fatalf("candidates=%#v", candidates)
			}
		})
	}
}

func newPolicyHandler(t *testing.T) (*sqlite.Store, *Handler, *fakePrivacyModelRegistry) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	model := newFakePrivacyModelRegistry()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, PolicyStore: store, PrivacyModels: model,
		ControlToken: testControlToken,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return store, handler, model
}

func policyRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	contentType string,
	body string,
	etag string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q", response.Header().Get("Cache-Control"))
	}
	return response
}

func TestPrivacyRegexBuiltinRulesEndpoint(t *testing.T) {
	_, handler, _ := newPolicyHandler(t)
	response := policyRequest(t, handler, http.MethodGet, PrivacyRegexBuiltinRulesPath, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload contract.PolicyRegexBuiltinRulesResponse
	decode(t, response, &payload)
	if len(payload.Rules) == 0 {
		t.Fatal("expected builtin rules")
	}
	builtin := privacy.BuiltinRegexRules()
	if !reflect.DeepEqual(payload.Rules, builtin) {
		t.Fatalf("rules=%#v want %#v", payload.Rules, builtin)
	}
}

func TestPolicyControlAPIAcceptsCustomRegexRules(t *testing.T) {
	_, handler, _ := newPolicyHandler(t)
	response := policyRequest(
		t, handler, http.MethodGet,
		PoliciesPath+"/"+string(contract.DefaultPrivacyPolicyID), "", "", "",
	)
	etag := response.Header().Get("ETag")
	response = policyRequest(
		t, handler, http.MethodPatch,
		PoliciesPath+"/"+string(contract.DefaultPrivacyPolicyID),
		"application/merge-patch+json",
		`{"regex_source":"custom","custom_regex_rules":[{"kind":"email","pattern":"(?i)custom@[a-z.]+"}]}`,
		etag,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("custom patch status=%d body=%s", response.Code, response.Body.String())
	}
	var policy contract.Policy
	decode(t, response, &policy)
	if policy.RegexSource != contract.PolicyRegexSourceCustom || len(policy.CustomRegexRules) != 1 {
		t.Fatalf("policy=%#v", policy)
	}

	response = policyRequest(
		t, handler, http.MethodPatch,
		PoliciesPath+"/"+string(contract.DefaultPrivacyPolicyID),
		"application/merge-patch+json",
		`{"regex_source":"custom","custom_regex_rules":[]}`,
		response.Header().Get("ETag"),
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty custom rules status=%d body=%s", response.Code, response.Body.String())
	}
}
