package endpoint

import (
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func scopeTestResolver(t *testing.T) *StoreResolver {
	t.Helper()
	services := []contract.Service{
		contract.ServiceFromEndpoint(resolverEndpoint("service_alpha", contract.CapabilityModeNative, true, []string{"gpt-5"})),
		contract.ServiceFromEndpoint(resolverEndpoint("service_beta", contract.CapabilityModeNative, true, []string{"gpt-5", "only-beta"})),
	}
	resolver, err := NewStoreResolver(rankingStore{services: services, order: []contract.ServiceID{"service_alpha", "service_beta"}})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func TestAccessTokenScopeLimitsCandidates(t *testing.T) {
	resolver := scopeTestResolver(t)
	ctx := WithAccessTokenServices(t.Context(), contract.AccessTokenServices{
		Scope: contract.AccessTokenServicesSelected, ServiceIDs: []contract.ServiceID{"service_beta"},
	})
	request := ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true, AllCandidates: true}
	candidates, ranking, err := resolver.ResolveRankedCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].CanonicalService().ID != "service_beta" {
		t.Fatalf("candidates = %v, want only service_beta", candidates)
	}
	skipped := false
	for _, ranked := range ranking {
		if ranked.ServiceID == "service_alpha" && ranked.Skip == contract.RoutingSkipAccessTokenScope {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("ranking = %+v, want service_alpha skipped for the token scope", ranking)
	}
}

func TestAccessTokenScopeRefusesModelOnlyOthersServe(t *testing.T) {
	resolver := scopeTestResolver(t)
	ctx := WithAccessTokenServices(t.Context(), contract.AccessTokenServices{
		Scope: contract.AccessTokenServicesSelected, ServiceIDs: []contract.ServiceID{"service_alpha"},
	})
	_, err := resolver.Resolve(ctx, ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "only-beta", Streaming: true})
	var scopeErr *AccessTokenScopeError
	if !errors.As(err, &scopeErr) || scopeErr.Service != "" {
		t.Fatalf("err = %v, want a scope error that names no provider", err)
	}
}

func TestAccessTokenScopeRefusesPinnedProviderOutsideIt(t *testing.T) {
	resolver := scopeTestResolver(t)
	ctx := WithAccessTokenServices(t.Context(), contract.AccessTokenServices{
		Scope: contract.AccessTokenServicesSelected, ServiceIDs: []contract.ServiceID{"service_alpha"},
	})
	_, err := resolver.Resolve(ctx, ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true, Service: "service_beta"})
	var scopeErr *AccessTokenScopeError
	if !errors.As(err, &scopeErr) || scopeErr.Service != "service_beta" {
		t.Fatalf("pinned err = %v, want scope error for service_beta", err)
	}
	if _, err := resolver.ResolveService(ctx, "service_beta"); !errors.As(err, &scopeErr) {
		t.Fatalf("ResolveService err = %v, want scope error", err)
	}
	if _, err := resolver.ResolveService(ctx, "service_alpha"); err != nil {
		t.Fatalf("ResolveService in scope: %v", err)
	}
}

func TestAccessTokenScopeWithDeletedProvidersReachesNothing(t *testing.T) {
	resolver := scopeTestResolver(t)
	ctx := WithAccessTokenServices(t.Context(), contract.AccessTokenServices{
		Scope: contract.AccessTokenServicesSelected, ServiceIDs: []contract.ServiceID{"service_gone"},
	})
	_, err := resolver.Resolve(ctx, ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true})
	var scopeErr *AccessTokenScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("err = %v, want scope error instead of every provider", err)
	}
}

func TestUnscopedRequestUsesEveryProvider(t *testing.T) {
	resolver := scopeTestResolver(t)
	for _, ctx := range []struct {
		name  string
		scope *contract.AccessTokenServices
	}{{name: "no token"}, {name: "all", scope: &contract.AccessTokenServices{Scope: contract.AccessTokenServicesAll}}} {
		t.Run(ctx.name, func(t *testing.T) {
			requestCtx := t.Context()
			if ctx.scope != nil {
				requestCtx = WithAccessTokenServices(requestCtx, *ctx.scope)
			}
			candidates, err := resolver.ResolveCandidates(requestCtx, ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true, AllCandidates: true})
			if err != nil || len(candidates) != 2 {
				t.Fatalf("candidates = %d err = %v, want both providers", len(candidates), err)
			}
		})
	}
}

func TestAccessTokenScopeLeavesUnservedModelsToTheUsualError(t *testing.T) {
	resolver := scopeTestResolver(t)
	ctx := WithAccessTokenServices(t.Context(), contract.AccessTokenServices{
		Scope: contract.AccessTokenServicesSelected, ServiceIDs: []contract.ServiceID{"service_alpha"},
	})
	_, err := resolver.Resolve(ctx, ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "nobody-serves", Streaming: true})
	var scopeErr *AccessTokenScopeError
	if errors.As(err, &scopeErr) {
		t.Fatalf("err = %v, want the error any token gets for a model no provider serves", err)
	}
	var unavailable *CapabilityUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("err = %T %v, want CapabilityUnavailableError", err, err)
	}
}
