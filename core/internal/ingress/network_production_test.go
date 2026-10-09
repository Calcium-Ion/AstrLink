package ingress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

func TestNetworkProductionRequiresTheSameDependenciesAndNoHostPin(t *testing.T) {
	complete := Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) { return nil, nil }),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
			return "", errors.New("not found")
		}),
	}
	if _, err := NewNetworkProduction(complete); err != nil {
		t.Fatalf("NewNetworkProduction: %v", err)
	}
	withoutToken := complete
	withoutToken.AccessTokenAuthenticator = nil
	withoutResolver := complete
	withoutResolver.Resolver = nil
	pinned := complete
	pinned.AllowedHost = "127.0.0.1:8317"
	for name, dependencies := range map[string]Dependencies{
		"no access token authenticator": withoutToken,
		"no resolver":                   withoutResolver,
		"loopback Host pin":             pinned,
	} {
		if _, err := NewNetworkProduction(dependencies); err == nil {
			t.Errorf("%s: NewNetworkProduction accepted it", name)
		}
	}
}

func TestNetworkProductionAcceptsAnyHostButKeepsTokenAndOriginGate(t *testing.T) {
	const token = "astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	store := &memoryRequestRecordStore{}
	resolveCalls := 0
	handler, err := NewNetworkProduction(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolveCalls++
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			return nil, errors.New("authorizer must not run without an endpoint")
		}),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, presented string) (contract.AccessTokenID, error) {
			if presented == token {
				return "token_primary", nil
			}
			return "", errors.New("not found")
		}),
		RequestRecords: store,
	})
	if err != nil {
		t.Fatalf("NewNetworkProduction: %v", err)
	}
	tests := []struct {
		name          string
		host          string
		origin        string
		authorization string
		query         string
		status        int
		code          string
		resolves      bool
	}{
		{name: "LAN address", host: "192.168.1.20:8317", authorization: "Bearer " + token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "container name", host: "astrlink:8317", authorization: "Bearer " + token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "proxied hostname", host: "llm.example.com", authorization: "Bearer " + token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "browser Origin", host: "llm.example.com", origin: "https://llm.example.com", authorization: "Bearer " + token, status: http.StatusForbidden, code: "origin_forbidden"},
		{name: "missing token", host: "llm.example.com", status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "wrong token", host: "llm.example.com", authorization: "Bearer wrong_token_0123456789abcdef", status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "query token", host: "llm.example.com", query: "?key=" + token, status: http.StatusUnauthorized, code: "token_query_forbidden"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := resolveCalls
			request := httptest.NewRequest(http.MethodPost, "/v1/responses"+test.query, strings.NewReader(`{"model":"m","input":"hi"}`))
			request.Host = test.host
			request.Header.Set("Content-Type", "application/json")
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertInferenceError(t, response, test.status, test.code)
			if got := resolveCalls > before; got != test.resolves {
				t.Fatalf("resolver called=%t, want %t", got, test.resolves)
			}
		})
	}
	// Only the three authenticated requests reach the records; nothing an
	// anonymous caller sends is persisted.
	if len(store.records) != 3 {
		t.Fatalf("records = %d, want only the authenticated requests: %#v", len(store.records), store.records)
	}
	for _, record := range store.records {
		if record.LocalAccessTokenID == nil || *record.LocalAccessTokenID != "token_primary" {
			t.Fatalf("unauthenticated request was recorded: %#v", record)
		}
	}
}

func TestNetworkProductionResponsesWebSocketRejectsBrowserOriginUnrecorded(t *testing.T) {
	store := &memoryRequestRecordStore{}
	handler, err := NewNetworkProduction(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			t.Fatal("a rejected upgrade must not resolve")
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) { return nil, nil }),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
			return "token_primary", nil
		}),
		RequestRecords: store,
	})
	if err != nil {
		t.Fatalf("NewNetworkProduction: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	request.Host = "llm.example.com"
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Authorization", "Bearer astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertInferenceError(t, response, http.StatusForbidden, "origin_forbidden")
	if len(store.records) != 0 {
		t.Fatalf("rejected upgrade was recorded: %#v", store.records)
	}
}
