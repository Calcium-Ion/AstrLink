package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestRoutingSettingsGlobalInheritanceAndOverrides(t *testing.T) {
	store, handler := newRouteHandler(t)
	ctx := context.Background()
	response := controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if response.Code != 200 {
		t.Fatalf("GET: %d %s", response.Code, response.Body.String())
	}
	var settings contract.RoutingSettings
	decode(t, response, &settings)
	if settings.AllowUnmatchedFailover || settings.DefaultFailurePolicy.MaxRetries != 1 || settings.MaxAttempts != 6 {
		t.Fatalf("defaults=%+v", settings)
	}
	// A single global change applies to dozens of existing services.
	for i := 0; i < 32; i++ {
		service := contract.Service{ID: contract.ServiceID(fmt.Sprintf("service_%02d", i)), Name: "Inherited", Kind: contract.ServiceKindOpenAI, Enabled: true, Models: []string{"public"}, HTTP: &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}}}
		if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := endpoint.NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	request := endpoint.ResolveRequest{Protocol: contract.ProtocolOpenAIChat, Model: "public"}
	original, err := resolver.ResolveCandidates(ctx, request)
	if err != nil || len(original) != 1 {
		t.Fatalf("default unmatched candidates=%d %v", len(original), err)
	}
	settings.AllowUnmatchedFailover = true
	settings.Strategy = contract.FailoverOnly
	settings.MaxAttempts = 12
	settings.DefaultFailurePolicy.MaxRetries = 3
	encoded, _ := json.Marshal(settings)
	response = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", string(encoded), "")
	if response.Code != 200 {
		t.Fatalf("PATCH: %d %s", response.Code, response.Body.String())
	}
	candidates, err := resolver.ResolveCandidates(ctx, request)
	if err != nil || len(candidates) != 32 {
		t.Fatalf("new candidates=%d %v", len(candidates), err)
	}
	for _, candidate := range candidates {
		if candidate.FailurePolicy.MaxRetries != 3 || candidate.Failover.MaxAttempts != 12 || candidate.Failover.Strategy != contract.FailoverOnly {
			t.Fatalf("not inherited: %+v", candidate)
		}
	}
	if original[0].FailurePolicy.MaxRetries != 1 || original[0].Failover.MaxAttempts != 6 {
		t.Fatal("in-flight snapshot changed")
	}
	record, err := store.GetService(ctx, "service_00")
	if err != nil {
		t.Fatal(err)
	}
	if record.Service.FailurePolicy != nil {
		t.Fatal("global change rewrote service")
	}
	exception := contract.DefaultFailurePolicy()
	exception.MaxRetries = 0
	body, _ := json.Marshal(map[string]any{"failure_policy": exception})
	response = controlRequest(t, handler, http.MethodPatch, ServicesPath+"/service_00", "application/merge-patch+json", string(body), record.ETag)
	if response.Code != 200 {
		t.Fatalf("service override: %d %s", response.Code, response.Body.String())
	}
	candidates, err = resolver.ResolveCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].CanonicalService().FailurePolicy.MaxRetries != 0 {
		t.Fatal("service exception lost")
	}
	response = controlRequest(t, handler, http.MethodPatch, ServicesPath+"/service_00", "application/merge-patch+json", `{"failure_policy":null}`, response.Header().Get("ETag"))
	if response.Code != 200 {
		t.Fatalf("clear service: %d %s", response.Code, response.Body.String())
	}
	candidates, err = resolver.ResolveCandidates(ctx, request)
	if err != nil || candidates[0].FailurePolicy.MaxRetries != 3 {
		t.Fatal("service did not resume global inheritance")
	}

}

func TestRoutingSettingsRejectInvalidPolicy(t *testing.T) {
	_, handler := newRouteHandler(t)
	for _, body := range []string{`{}`, `{"max_attempts":0}`, `{"max_attempts":21}`, `{"allow_unmatched_failover":null}`, `{"strategy":"random"}`, `{"default_failure_policy":{"max_retries":2}}`, `{"unknown":true}`} {
		response := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, "")
		if response.Code != 422 {
			t.Fatalf("%s => %d %s", body, response.Code, response.Body.String())
		}
	}
}
