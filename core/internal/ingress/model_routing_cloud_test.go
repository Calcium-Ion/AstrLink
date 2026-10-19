package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func privacyPolicyRedact() privacy.Policy {
	return privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact}
}

// memorySecretStore answers one fixed ref with one fixed secret.
type memorySecretStore struct {
	ref    secretstore.Ref
	secret []byte
}

func (store memorySecretStore) Get(_ context.Context, ref secretstore.Ref) ([]byte, error) {
	if ref != store.ref {
		return nil, secretstore.ErrNotFound
	}
	return append([]byte(nil), store.secret...), nil
}

func (store memorySecretStore) Put(context.Context, secretstore.Ref, []byte) error {
	return nil
}

func (store memorySecretStore) Delete(context.Context, secretstore.Ref) error {
	return nil
}

// cloudTestGroup routes task_type=coding to the strong member; the default is fast.
func cloudTestGroup(labels []string) contract.ModelRoutingGroup {
	group := contract.ModelRoutingGroup{
		ID: "superclaude", Enabled: true,
		Members: []contract.ModelRoutingMember{
			{ID: "fast", ServiceID: "endpoint_test", Model: "fast-model"},
			{ID: "strong", ServiceID: "endpoint_test", Model: "strong-model"},
		},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "coding", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
			TargetMemberID: "strong",
		}},
	}
	group.TaskTypeLabels = labels
	return group
}

func enabledClassifierSettings(serverURL, protocol string) contract.ClassifierSettings {
	return contract.ClassifierSettings{
		Enabled:          true,
		ClassifierSource: contract.ClassifierSourceInline,
		Inline: &contract.ClassifierInlineSource{
			Protocol:      protocol,
			Endpoint:      serverURL,
			CredentialRef: contract.ClassifierCredentialRef,
			Model:         "jev-latest",
		},
	}
}

// cloudRoutingHandler wires a handler whose classification goes to the given
// cloud server through the settings store and the fixed credential.
func cloudRoutingHandler(t *testing.T, cloudServer *httptest.Server, protocol string, group contract.ModelRoutingGroup, records *redirectSettingsStore, forwarder Forwarder) *Handler {
	t.Helper()
	settings := enabledClassifierSettings(cloudServer.URL, protocol)
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false), UpstreamModel: request.Model}, nil
		}),
		RequestRecords:     records,
		ClassifierSettings: ClassifierSettingsProviderFunc(func(context.Context) (contract.ClassifierSettings, error) { return settings, nil }),
		ProxyCredentials:   memorySecretStore{ref: contract.ClassifierCredentialRef, secret: []byte("cloud-key")},
	})
	if forwarder != nil {
		handler.forwarder = forwarder
	} else {
		handler.forwarder = forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			_, _ = writer.Write([]byte(`{"model":"fast-model"}`))
			return nil
		})
	}
	records.settings.ModelRoutingGroups = []contract.ModelRoutingGroup{group}
	return handler
}

func cloudTestForwarder(t *testing.T) (Forwarder, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	forwarder := transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(request.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"model":"fast-model"}`)),
		}, nil
	}))
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
	return forwarder, snapshot
}

func serveCloudRouting(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("X-AstrLink-Routing-Debug", "local-only")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// Low confidence is not a failure: the fact becomes unknown (triUnknown), the
// task_type rule is skipped, and the record carries the dedicated code —
// never the availability fallback.
func TestModelRoutingLowConfidenceBecomesUnknownNotUnavailable(t *testing.T) {
	for _, labels := range [][]string{nil, {"general", "research", "coding", "architect"}} {
		cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte(`{"answers":{"task_type":{"choice":"coding","confidence":0.2}}}`))
		}))
		defer cloudServer.Close()
		store := newRedirectSettingsStore()
		forwarder, bodies := cloudTestForwarder(t)
		handler := cloudRoutingHandler(t, cloudServer, contract.ClassifierProtocolJev, cloudTestGroup(labels), store, forwarder)
		response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"write go code"}`)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		record := store.root(t)
		if record.ModelRouting == nil || !record.ModelRouting.UsedDefault || record.ModelRouting.RuleID != "" {
			t.Fatalf("decision = %+v, want the task_type rule skipped", record.ModelRouting)
		}
		if record.ModelRouting.FallbackReason != "classifier_low_confidence" {
			t.Fatalf("fallback reason = %q, want classifier_low_confidence", record.ModelRouting.FallbackReason)
		}
		if upstreames := bodies(); len(upstreames) != 1 || !strings.Contains(upstreames[0], "fast-model") {
			t.Fatalf("upstream bodies = %v", upstreames)
		}
	}
}

func TestModelRoutingCloudHighConfidenceSelectsRule(t *testing.T) {
	var received []byte
	cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		received = data
		_, _ = writer.Write([]byte(`{"answers":{"task_type":{"choice":"coding","confidence":0.95}}}`))
	}))
	defer cloudServer.Close()
	store := newRedirectSettingsStore()
	handler := cloudRoutingHandler(t, cloudServer, contract.ClassifierProtocolJev, cloudTestGroup(nil), store, nil)
	response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"write go code"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	record := store.root(t)
	if record.ModelRouting == nil || record.ModelRouting.MemberModel != "strong-model" || record.ModelRouting.UsedDefault {
		t.Fatalf("decision = %+v, want the coding rule member", record.ModelRouting)
	}
	if !strings.Contains(string(received), `"criteria"`) {
		t.Fatalf("cloud request missing criteria: %s", received)
	}
	for _, field := range record.ModelRouting.Fields {
		if field.Field == "task_type" && field.ClassifierConfidence != 0.95 {
			t.Fatalf("recorded confidence = %v, want 0.95", field.ClassifierConfidence)
		}
	}
}

// The whole failure matrix stays fail-open: the request succeeds on the
// deterministic path whatever the classifier does.
func TestModelRoutingCloudFailureMatrixFailsOpen(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		status   int
		body     string
		want     string
	}{
		{name: "rate limited", protocol: contract.ClassifierProtocolOpenAIDecisions, status: 429, body: `{}`, want: "classifier_busy"},
		{name: "overloaded", protocol: contract.ClassifierProtocolJev, status: 529, body: `{}`, want: "classifier_busy"},
		{name: "server error", protocol: contract.ClassifierProtocolJev, status: 500, body: `{}`, want: "classifier_unavailable"},
		{name: "jev missing answer", protocol: contract.ClassifierProtocolJev, status: 200, body: `{"answers":{}}`, want: "classifier_invalid_output"},
		{name: "openai refusal", protocol: contract.ClassifierProtocolOpenAIDecisions, status: 200, body: `{"answers":[{"type":"refusal","name":"task_type"}]}`, want: "classifier_refusal"},
		{name: "garbage", protocol: contract.ClassifierProtocolJev, status: 200, body: `nope`, want: "classifier_invalid_output"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer cloudServer.Close()
			store := newRedirectSettingsStore()
			handler := cloudRoutingHandler(t, cloudServer, test.protocol, cloudTestGroup(nil), store, nil)
			response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"hello"}`)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			record := store.root(t)
			if record.ModelRouting == nil || record.ModelRouting.FallbackReason != test.want {
				t.Fatalf("fallback reason = %q, want %q", record.ModelRouting.FallbackReason, test.want)
			}
			if !record.ModelRouting.UsedDefault {
				t.Fatalf("decision did not use the deterministic default: %+v", record.ModelRouting)
			}
		})
	}
}

func TestModelRoutingCloudTimeoutFailsOpen(t *testing.T) {
	cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer cloudServer.Close()
	store := newRedirectSettingsStore()
	handler := cloudRoutingHandler(t, cloudServer, contract.ClassifierProtocolJev, cloudTestGroup(nil), store, nil)
	response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"hello"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	record := store.root(t)
	if record.ModelRouting.FallbackReason != "classifier_timeout" {
		t.Fatalf("fallback reason = %q, want classifier_timeout", record.ModelRouting.FallbackReason)
	}
}

// A failed classification never blocks the request: it records its reason and
// routes to the group's default member. Nothing is substituted for the missing
// label, so a rule that would have matched the substituted value stays
// skipped — the reason code is the whole record of what happened.
func TestModelRoutingCloudFailureNeverBlocks(t *testing.T) {
	cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer cloudServer.Close()
	store := newRedirectSettingsStore()
	handler := cloudRoutingHandler(t, cloudServer, contract.ClassifierProtocolJev, cloudTestGroup(nil), store, nil)
	response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"hello"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s — a classifier failure must never block", response.Code, response.Body.String())
	}
	record := store.root(t)
	if record.ModelRouting.FallbackReason != "classifier_busy" {
		t.Fatalf("fallback reason = %q, want classifier_busy", record.ModelRouting.FallbackReason)
	}
	if !record.ModelRouting.UsedDefault || record.ModelRouting.MemberModel != "fast-model" {
		t.Fatalf("decision = %+v, want the deterministic default member", record.ModelRouting)
	}
}

// No label is substituted on failure, whatever the group's label set happens to
// contain. A group that could accept a synthetic label and one with a custom
// vocabulary must behave identically: the fact stays unknown, so every rule
// testing it skips and the default member serves the request.
func TestModelRoutingCloudFailureSubstitutesNoLabel(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		labels []string
		ruleIn string
	}{
		// "general" is the label the removed fallback filled when the group
		// accepted it, so it is the one a regression would produce here.
		{name: "default labels", ruleIn: "general"},
		{name: "custom labels", labels: []string{"alpha", "beta"}, ruleIn: "alpha"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(http.StatusTooManyRequests)
			}))
			defer cloudServer.Close()
			store := newRedirectSettingsStore()
			group := cloudTestGroup(testCase.labels)
			// A rule on a label a substitution would have produced: if the
			// failure still filled one, this rule would match and pick "strong".
			group.Rules = []contract.ModelRoutingRule{{
				ID: "substitutable", Priority: 1,
				When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{testCase.ruleIn}},
				TargetMemberID: "strong",
			}}
			handler := cloudRoutingHandler(t, cloudServer, contract.ClassifierProtocolJev, group, store, nil)
			response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"hello"}`)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			record := store.root(t)
			if record.ModelRouting.FallbackReason != "classifier_busy" {
				t.Fatalf("fallback reason = %q, want classifier_busy", record.ModelRouting.FallbackReason)
			}
			if !record.ModelRouting.UsedDefault || record.ModelRouting.MemberModel != "fast-model" {
				t.Fatalf("decision = %+v, want the default member and no substituted label", record.ModelRouting)
			}
		})
	}
}

// The cloud classifier must receive the group's custom label set, and a
// confident custom answer routes; an answer outside the set is invalid.
func TestModelRoutingCloudCustomLabelsRoundTrip(t *testing.T) {
	var criteria map[string]any
	cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		var envelope struct {
			Questions struct {
				TaskType struct {
					Criteria map[string]any `json:"criteria"`
				} `json:"task_type"`
			} `json:"questions"`
		}
		_ = json.Unmarshal(data, &envelope)
		criteria = envelope.Questions.TaskType.Criteria
		_, _ = writer.Write([]byte(`{"answers":{"task_type":{"choice":"storyboard","confidence":0.9}}}`))
	}))
	defer cloudServer.Close()
	store := newRedirectSettingsStore()
	group := cloudTestGroup([]string{"storyboard", "general"})
	group.Rules = []contract.ModelRoutingRule{{
		ID: "storyboard", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"storyboard"}},
		TargetMemberID: "strong",
	}}
	handler := cloudRoutingHandler(t, cloudServer, contract.ClassifierProtocolJev, group, store, nil)
	response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"draw a storyboard"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(criteria) != 2 || criteria["storyboard"] != "storyboard" {
		t.Fatalf("criteria = %v, want the custom label set", criteria)
	}
	record := store.root(t)
	if record.ModelRouting.MemberModel != "strong-model" {
		t.Fatalf("decision = %+v, want the storyboard rule member", record.ModelRouting)
	}
}

// The cloud classifier reads the redacted text, and every upstream attempt,
// retries included, forwards the same placeholders.
func TestModelRoutingSanitizedBytesSharedWithFailover(t *testing.T) {
	var cloudBodies []string
	cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		cloudBodies = append(cloudBodies, string(data))
		_, _ = writer.Write([]byte(`{"answers":{"task_type":{"choice":"coding","confidence":0.99}}}`))
	}))
	defer cloudServer.Close()
	retryPolicy := contract.DefaultFailurePolicy()
	retryPolicy.InitialDelayMS = 0
	retryPolicy.NetworkError = contract.FailureRetry
	retryPolicy.MaxRetries = 2
	var upstreamBodies []string
	store := newRedirectSettingsStore()
	settings := enabledClassifierSettings(cloudServer.URL, contract.ClassifierProtocolJev)
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolved := endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false), UpstreamModel: request.Model}
			resolved.FailurePolicy = &retryPolicy
			return resolved, nil
		}),
		RequestRecords:     store,
		ClassifierSettings: ClassifierSettingsProviderFunc(func(context.Context) (contract.ClassifierSettings, error) { return settings, nil }),
		ProxyCredentials:   memorySecretStore{ref: contract.ClassifierCredentialRef, secret: []byte("cloud-key")},
		PrivacyFilter:      testPrivacyEngine(t, privacyPolicyRedact(), nil),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(request.Body)
			upstreamBodies = append(upstreamBodies, string(data))
			if len(upstreamBodies) == 1 {
				return nil, context.DeadlineExceeded
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"model":"fast-model"}`)),
			}, nil
		})),
	})
	group := cloudTestGroup(nil)
	store.settings.ModelRoutingGroups = []contract.ModelRoutingGroup{group}
	response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"email alice@example.com about coding"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(cloudBodies) != 1 || strings.Contains(cloudBodies[0], "alice@example.com") {
		t.Fatalf("cloud bodies = %v", cloudBodies)
	}
	if len(upstreamBodies) != 2 {
		t.Fatalf("upstream attempts = %d, want a retry", len(upstreamBodies))
	}
	for _, body := range upstreamBodies {
		if strings.Contains(body, "alice@example.com") {
			t.Fatalf("upstream received unredacted body: %q", body)
		}
	}
	if upstreamBodies[0] != upstreamBodies[1] {
		t.Fatalf("failover attempts used different bytes:\n%s\n%s", upstreamBodies[0], upstreamBodies[1])
	}
	if !strings.Contains(upstreamBodies[0], "<PRIVATE_EMAIL_") {
		t.Fatalf("upstream body missing placeholder: %q", upstreamBodies[0])
	}
}

// A body the policy blocks is never classified: routing falls back without
// calling the classifier, and the provider attempt is blocked as without
// routing, which the routing record lists.
func TestModelRoutingBlockedBodySkipsClassification(t *testing.T) {
	cloudCalls := 0
	cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cloudCalls++
		_, _ = writer.Write([]byte(`{"answers":{"task_type":{"choice":"coding","confidence":0.99}}}`))
	}))
	defer cloudServer.Close()
	store := newRedirectSettingsStore()
	settings := enabledClassifierSettings(cloudServer.URL, contract.ClassifierProtocolJev)
	upstreamCalls := 0
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false), UpstreamModel: request.Model}, nil
		}),
		RequestRecords:     store,
		ClassifierSettings: ClassifierSettingsProviderFunc(func(context.Context) (contract.ClassifierSettings, error) { return settings, nil }),
		ProxyCredentials:   memorySecretStore{ref: contract.ClassifierCredentialRef, secret: []byte("cloud-key")},
		PrivacyFilter:      testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionBlock}, nil),
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			upstreamCalls++
			_, _ = writer.Write([]byte(`{"model":"fast-model"}`))
			return nil
		}),
	})
	store.settings.ModelRoutingGroups = []contract.ModelRoutingGroup{cloudTestGroup(nil)}
	response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"email alice@example.com about coding"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if cloudCalls != 0 || upstreamCalls != 0 {
		t.Fatalf("classifier calls=%d upstream calls=%d, want none", cloudCalls, upstreamCalls)
	}
	decision := store.root(t).ModelRouting
	if decision == nil || !decision.UsedDefault || decision.FallbackReason != autoclassifier.FallbackUnavailable {
		t.Fatalf("decision = %+v, want the default member without classification", decision)
	}
	if len(decision.PrivacyAttempts) != 1 {
		t.Fatalf("privacy attempts = %+v, want the blocked attempt", decision.PrivacyAttempts)
	}
	attempt := decision.PrivacyAttempts[0]
	if attempt.AttemptIndex != 1 || attempt.ServiceID != "endpoint_test" ||
		attempt.Decision != contract.ModelRoutingPrivacyReject || attempt.FindingCount != 1 {
		t.Fatalf("privacy attempt = %+v", attempt)
	}
}

// Settings read failures degrade to the deterministic fallback; an unreadable
// classifier must never fail the request.
func TestModelRoutingClassifierSettingsErrorFailsOpen(t *testing.T) {
	store := newRedirectSettingsStore()
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false), UpstreamModel: request.Model}, nil
		}),
		RequestRecords: store,
		ClassifierSettings: ClassifierSettingsProviderFunc(func(context.Context) (contract.ClassifierSettings, error) {
			return contract.ClassifierSettings{}, context.DeadlineExceeded
		}),
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			_, _ = writer.Write([]byte(`{"model":"fast-model"}`))
			return nil
		}),
	})
	store.settings.ModelRoutingGroups = []contract.ModelRoutingGroup{cloudTestGroup(nil)}
	response := serveCloudRouting(t, handler, `{"model":"astrlink/superclaude","input":"hello"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	record := store.root(t)
	if record.ModelRouting.FallbackReason != autoclassifier.FallbackUnavailable || !record.ModelRouting.UsedDefault {
		t.Fatalf("decision = %+v, want deterministic fallback", record.ModelRouting)
	}
}
