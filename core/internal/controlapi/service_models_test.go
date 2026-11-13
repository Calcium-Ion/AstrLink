package controlapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

type controlRoundTripFunc func(*http.Request) (*http.Response, error)

func (function controlRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestServiceModelProbesUsePersistedAndDraftHTTPConfiguration(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var urls, authorizations []string
	client := &http.Client{Transport: controlRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		urls = append(urls, request.URL.String())
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"zeta"},{"id":"alpha"}]}`)),
		}, nil
	})}
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			ServiceModels: servicemodel.New(store, nil, client),
			ControlToken:  testControlToken,
			NewServiceID: func() (contract.ServiceID, error) {
				return "service_probe_http", nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created := controlRequest(t, handler, http.MethodPost, ServicesPath, "application/json", `{
        "name":"probe service",
        "kind":"openai",
        "models":["zeta","alpha","zeta"],
        "http":{
          "base_url":"https://old.example/v1",
          "auth":{"scheme":"bearer"},
          "credential":{"secret":"persisted-secret"}
        },
        "capabilities":[
          {"protocol":"openai.models","mode":"native","streaming":false}
        ]
      }`, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var service contract.Service
	decode(t, created, &service)
	if got := strings.Join(service.Models, ","); got != "alpha,zeta" {
		t.Fatalf("normalized models = %q", got)
	}

	persisted := controlRequest(
		t, handler, http.MethodPost,
		ServicesPath+"/service_probe_http/probe-models",
		"application/json", `{"protocol":"openai.models"}`, "",
	)
	if persisted.Code != http.StatusOK {
		t.Fatalf("persisted probe status=%d body=%s", persisted.Code, persisted.Body.String())
	}
	var persistedResult serviceModelProbeResponse
	decode(t, persisted, &persistedResult)
	if persistedResult.ServiceID != "service_probe_http" ||
		persistedResult.Protocol != contract.ProtocolOpenAIModels ||
		strings.Join(persistedResult.ModelIDs, ",") != "alpha,zeta" {
		t.Fatalf("persisted result = %#v", persistedResult)
	}

	draft := controlRequest(t, handler, http.MethodPost, ServiceModelProbesPath, "application/json", `{
        "service_id":"service_probe_http",
        "kind":"openai",
        "http":{"base_url":"https://new.example/v1","auth":{"scheme":"bearer"}},
        "protocol":"openai.models"
      }`, "")
	if draft.Code != http.StatusOK {
		t.Fatalf("draft probe status=%d body=%s", draft.Code, draft.Body.String())
	}
	if got, want := strings.Join(urls, ","),
		"https://old.example/v1/models,https://new.example/v1/models"; got != want {
		t.Fatalf("probe URLs = %q, want %q", got, want)
	}

	explicit := controlRequest(t, handler, http.MethodPost, ServiceModelProbesPath, "application/json", `{
        "service_id":"service_probe_http",
        "kind":"openai",
        "http":{
          "base_url":"https://explicit.example/v1",
          "auth":{"scheme":"bearer"},
          "credential":{"secret":"explicit-secret"}
        },
        "protocol":"openai.models"
      }`, "")
	if explicit.Code != http.StatusOK {
		t.Fatalf("explicit probe status=%d body=%s", explicit.Code, explicit.Body.String())
	}
	if got, want := strings.Join(authorizations, ","),
		"Bearer persisted-secret,Bearer persisted-secret,Bearer explicit-secret"; got != want {
		t.Fatalf("probe authorizations = %q, want %q", got, want)
	}
	if strings.Contains(persisted.Body.String()+draft.Body.String()+explicit.Body.String(), "secret") {
		t.Fatal("model probe response leaked credential material")
	}
	if strings.Contains(persisted.Body.String(), "model_protocols") {
		t.Fatalf("a list without endpoint types reported formats: %s", persisted.Body.String())
	}
}

// A New API list names each model's endpoint types; the probe reports the
// format of a model served in exactly one.
func TestDraftModelProbeReportsListedFormats(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	client := &http.Client{Transport: controlRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-x","supported_endpoint_types":["openai"]},` +
				`{"id":"gemini-x","supported_endpoint_types":["gemini"]}]}`)),
		}, nil
	})}
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{ServiceStore: store, ServiceModels: servicemodel.New(store, nil, client), ControlToken: testControlToken},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := controlRequest(t, handler, http.MethodPost, ServiceModelProbesPath, "application/json", `{
        "kind":"newapi",
        "http":{"base_url":"https://newapi.example","auth":{"scheme":"bearer"},"credential":{"secret":"draft-secret"}},
        "protocol":"openai.models"
      }`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("probe status=%d body=%s", response.Code, response.Body.String())
	}
	var result serviceModelProbeResponse
	decode(t, response, &result)
	if strings.Join(result.ModelIDs, ",") != "gemini-x,gpt-x" || len(result.ModelProtocols) != 1 ||
		result.ModelProtocols[0] != (contract.ModelProtocol{Model: "gemini-x", Protocol: contract.ProtocolGoogleGenerateContent}) {
		t.Fatalf("probe result = %#v", result)
	}
}

func TestDraftServiceModelProbeRejectsUnsupportedProtocol(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			ServiceModels: servicemodel.New(store, nil, &http.Client{}),
			ControlToken:  testControlToken,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := controlRequest(t, handler, http.MethodPost, ServiceModelProbesPath, "application/json", `{
        "kind":"gemini",
        "http":{"base_url":"https://example.test","auth":{"scheme":"none"}},
        "protocol":"openai.models"
      }`, "")
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSavedCodexModelProbeRequiresConnectedSubscription(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager, err := subscription.NewManager(
		subscription.StorageAccountStore{Store: store},
		accountauth.NewMemoryCredentialStore(),
		accountauth.OAuthConfig{ClientID: "astrlink_model_probe_test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := contract.Service{
		ID:           "service_codex_probe",
		Name:         "Codex probe",
		Kind:         contract.ServiceKindCodexSubscription,
		Enabled:      true,
		Models:       []string{},
		Capabilities: contract.DefaultOpenAICodexCapabilities(),
		Subscription: &contract.SubscriptionConnection{
			Provider: contract.SubscriptionProviderOpenAICodex,
			Status:   contract.SubscriptionStatusDisconnected,
		},
	}
	if _, err := store.CreateService(context.Background(), service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			ServiceModels: servicemodel.New(store, manager, nil),
			ControlToken:  testControlToken,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := controlRequest(
		t, handler, http.MethodPost,
		ServicesPath+"/service_codex_probe/probe-models",
		"application/json", `{"protocol":"openai.models"}`, "",
	)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "service_not_connected") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSanitizedModelProbeMessageKeepsStatusWithoutBody(t *testing.T) {
	if got, want := sanitizedModelProbeMessage(
		fmt.Errorf("%w: %v", servicemodel.ErrUpstream, fmt.Errorf("codex models returned status 403")),
	), "upstream model discovery failed (HTTP 403)"; got != want {
		t.Fatalf("status message = %q, want %q", got, want)
	}
	if got, want := sanitizedModelProbeMessage(
		fmt.Errorf("%w: %v", servicemodel.ErrUpstream, fmt.Errorf("decode codex models: unexpected")),
	), "upstream model discovery failed (invalid catalog)"; got != want {
		t.Fatalf("decode message = %q, want %q", got, want)
	}
}

func TestServiceModelProbeErrorStatusMapping(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "unsupported", err: servicemodel.ErrUnsupported, status: http.StatusUnprocessableEntity},
		{name: "credential", err: servicemodel.ErrCredentialUnavailable, status: http.StatusConflict},
		{name: "subscription", err: servicemodel.ErrNotConnected, status: http.StatusConflict},
		{name: "upstream", err: servicemodel.ErrUpstream, status: http.StatusBadGateway},
		{name: "timeout", err: context.DeadlineExceeded, status: http.StatusGatewayTimeout},
		{name: "wrapped timeout", err: errors.Join(errors.New("probe"), context.DeadlineExceeded), status: http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeServiceModelProbeError(response, test.err)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}
}

// A failed upstream list says why, so the editor can point at the key or the
// address instead of showing a bare failure.
func TestDraftModelProbeFailureNamesReason(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, test := range []struct {
		name    string
		status  int
		body    string
		dialErr bool
		reason  string
		message string
	}{
		{name: "wrong key", status: http.StatusUnauthorized, reason: "upstream_unauthorized", message: "upstream model discovery failed (HTTP 401)"},
		{name: "forbidden", status: http.StatusForbidden, reason: "upstream_unauthorized", message: "upstream model discovery failed (HTTP 403)"},
		{name: "no model list", status: http.StatusNotFound, reason: "upstream_not_found", message: "upstream model discovery failed (HTTP 404)"},
		{name: "method not allowed", status: http.StatusMethodNotAllowed, reason: "upstream_not_found", message: "upstream model discovery failed (HTTP 405)"},
		{name: "rate limited", status: http.StatusTooManyRequests, reason: "upstream_rate_limited", message: "upstream model discovery failed (HTTP 429)"},
		{name: "server error", status: http.StatusServiceUnavailable, reason: "upstream_unavailable", message: "upstream model discovery failed (HTTP 503)"},
		{name: "other status", status: http.StatusTeapot, reason: "upstream_status", message: "upstream model discovery failed (HTTP 418)"},
		{name: "not a model list", status: http.StatusOK, body: "<html>welcome</html>", reason: "upstream_invalid_response", message: "upstream model discovery failed (invalid response)"},
		{name: "unreachable", dialErr: true, reason: "upstream_unreachable", message: "upstream model discovery failed (connection failed)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: controlRoundTripFunc(func(*http.Request) (*http.Response, error) {
				if test.dialErr {
					return nil, errors.New("dial tcp 127.0.0.1:9: connect: connection refused")
				}
				return &http.Response{
					StatusCode: test.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(test.body)),
				}, nil
			})}
			handler, err := NewWithDependencies(
				contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
				Dependencies{ServiceStore: store, ServiceModels: servicemodel.New(store, nil, client), ControlToken: testControlToken},
			)
			if err != nil {
				t.Fatal(err)
			}
			response := controlRequest(t, handler, http.MethodPost, ServiceModelProbesPath, "application/json", `{
        "kind":"openai_compatible",
        "http":{"base_url":"https://upstream.example/v1","auth":{"scheme":"bearer"},"credential":{"secret":"draft-secret"}},
        "protocol":"openai.models"
      }`, "")
			if response.Code != http.StatusBadGateway {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var envelope errorEnvelope
			decode(t, response, &envelope)
			if envelope.Error.Code != "upstream_model_discovery_failed" || envelope.Error.Message != test.message {
				t.Fatalf("error = %#v, want message %q", envelope.Error, test.message)
			}
			if len(envelope.Error.Details) != 1 || envelope.Error.Details[0].Reason != test.reason {
				t.Fatalf("details = %#v, want reason %q", envelope.Error.Details, test.reason)
			}
			if strings.Contains(response.Body.String(), "draft-secret") || strings.Contains(response.Body.String(), "welcome") {
				t.Fatalf("error leaked upstream material: %s", response.Body.String())
			}
		})
	}
}

func TestModelProbeFailureReasonLeavesUnknownFailuresBlank(t *testing.T) {
	for _, err := range []error{
		nil,
		servicemodel.ErrUpstream,
		fmt.Errorf("%w: pagination limit exceeded", servicemodel.ErrUpstream),
	} {
		if got := modelProbeFailureReason(err); got != "" {
			t.Fatalf("reason for %v = %q, want none", err, got)
		}
	}
	codex := fmt.Errorf("%w: %v", servicemodel.ErrUpstream, errors.New("decode codex models: unexpected"))
	if got := modelProbeFailureReason(codex); got != "upstream_invalid_response" {
		t.Fatalf("codex catalog reason = %q", got)
	}
}
