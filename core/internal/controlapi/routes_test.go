package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestRouteControlAPICRUDAndRuntimeGate(t *testing.T) {
	store, handler := newRouteHandler(t)
	ctx := context.Background()
	service := contract.Service{
		ID:      "service_route",
		Name:    "route target",
		Kind:    contract.ServiceKindOpenAI,
		Enabled: true,
		Models:  []string{"gpt-5.2", "model-code", "model-general"},
		HTTP: &contract.HTTPConnection{
			BaseURL: "https://api.example/v1",
			Auth:    contract.ServiceAuth{Scheme: contract.AuthSchemeNone},
		},
		Capabilities: []contract.Capability{{
			Protocol:  contract.ProtocolOpenAIResponses,
			Mode:      contract.CapabilityModeNative,
			Streaming: true,
		}},
	}
	if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}

	createBody := `{
		"name":"code alias",
		"enabled":true,
		"priority":10,
		"match":{"protocol":"openai.responses","model":"team/code"},
		"selection":{"mode":"priority"},
		"targets":[{
			"service_id":"service_route",
			"plan_type":"native",
			"upstream_protocol":"openai.responses",
			"priority":0,
			"upstream_model":"gpt-5.2"
		}]
	}`
	response := controlRequest(
		t,
		handler,
		http.MethodPost,
		RoutesPath,
		"application/json",
		createBody,
		"",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	createdETag := response.Header().Get("ETag")
	if createdETag == "" || response.Header().Get("Location") != RoutesPath+"/route_test" {
		t.Fatalf("create headers=%#v", response.Header())
	}
	var created contract.Route
	decode(t, response, &created)
	if created.ID != "route_test" || created.Targets[0].UpstreamModel != "gpt-5.2" {
		t.Fatalf("created route=%#v", created)
	}

	response = controlRequest(t, handler, http.MethodGet, RoutesPath+"/route_test", "", "", "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") != createdETag {
		t.Fatalf(
			"get status=%d etag=%q body=%s",
			response.Code,
			response.Header().Get("ETag"),
			response.Body.String(),
		)
	}

	response = controlRequest(t, handler, http.MethodGet, RoutesPath+"?limit=1&enabled=true", "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var page routePageResponse
	decode(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].ID != created.ID || page.NextCursor != nil {
		t.Fatalf("route page=%#v", page)
	}

	response = controlRequest(
		t,
		handler,
		http.MethodPatch,
		RoutesPath+"/route_test",
		"application/merge-patch+json",
		`{"name":"renamed"}`,
		`"sha256:stale"`,
	)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale patch status=%d body=%s", response.Code, response.Body.String())
	}
	response = controlRequest(
		t,
		handler,
		http.MethodPatch,
		RoutesPath+"/route_test",
		"application/merge-patch+json",
		`{"name":"renamed","enabled":false}`,
		createdETag,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", response.Code, response.Body.String())
	}
	updatedETag := response.Header().Get("ETag")
	var updated contract.Route
	decode(t, response, &updated)
	if updated.Name != "renamed" || updated.Enabled || updatedETag == createdETag {
		t.Fatalf("updated route=%#v etag=%q", updated, updatedETag)
	}

	response = controlRequest(
		t,
		handler,
		http.MethodDelete,
		RoutesPath+"/route_test",
		"",
		"",
		createdETag,
	)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale delete status=%d body=%s", response.Code, response.Body.String())
	}
	response = controlRequest(
		t,
		handler,
		http.MethodDelete,
		RoutesPath+"/route_test",
		"",
		"",
		updatedETag,
	)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}

	autoBody := `{
		"name":"auto",
		"priority":0,
		"match":{"protocol":"openai.responses","model":"astrlink/auto"},
		"selection":{"mode":"auto","taxonomy_id":"astrlink-text-v1"},
		"categories":[
			{"category_id":"coding","targets":[{"service_id":"service_route","plan_type":"native","upstream_protocol":"openai.responses","priority":0,"upstream_model":"model-code"}]},
			{"category_id":"general","targets":[{"service_id":"service_route","plan_type":"native","upstream_protocol":"openai.responses","priority":0,"upstream_model":"model-general"}]}
		]
	}`
	response = controlRequest(
		t,
		handler,
		http.MethodPost,
		RoutesPath,
		"application/json",
		autoBody,
		"",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("auto create status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRouteControlAPIRejectsUnsupportedReferencesAndStrictBoundaryDrift(t *testing.T) {
	store, handler := newRouteHandler(t)
	ctx := context.Background()
	service := contract.Service{
		ID:      "service_route",
		Name:    "route target",
		Kind:    contract.ServiceKindOpenAI,
		Enabled: true,
		HTTP: &contract.HTTPConnection{
			BaseURL: "https://api.example/v1",
			Auth:    contract.ServiceAuth{Scheme: contract.AuthSchemeNone},
		},
		Capabilities: []contract.Capability{{
			Protocol:  contract.ProtocolOpenAIResponses,
			Mode:      contract.CapabilityModeNative,
			Streaming: true,
		}},
	}
	if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "unknown field",
			body: `{"name":"x","priority":0,"match":{"protocol":"openai.responses"},"targets":[],"unexpected":true}`,
		},
		{
			name: "unsupported delegated target",
			body: `{"name":"x","priority":0,"match":{"protocol":"openai.responses"},"targets":[{"service_id":"service_route","plan_type":"delegated","upstream_protocol":"openai.responses","priority":0}]}`,
		},
		{
			name: "missing service",
			body: `{"name":"x","priority":0,"match":{"protocol":"openai.responses"},"targets":[{"service_id":"service_missing","plan_type":"native","upstream_protocol":"openai.responses","priority":0}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := controlRequest(
				t,
				handler,
				http.MethodPost,
				RoutesPath,
				"application/json",
				test.body,
				"",
			)
			if response.Code != http.StatusBadRequest &&
				response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	response := controlRequest(t, handler, http.MethodGet, RoutesPath+"?unknown=true", "", "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown query status=%d body=%s", response.Code, response.Body.String())
	}
	response = controlRequest(
		t,
		handler,
		http.MethodPatch,
		RoutesPath+"/route_test",
		"application/merge-patch+json",
		`{"enabled":false}`,
		"",
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing If-Match status=%d body=%s", response.Code, response.Body.String())
	}
}
