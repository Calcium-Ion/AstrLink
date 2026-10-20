package endpoint

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type directResolverStore struct {
	service contract.Service
	gets    int
	err     error
}

func TestPinnedSubscriptionRechecksRiskExpiry(t *testing.T) {
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	kind := contract.ServiceKindCodexSubscription
	store := &directResolverStore{service: contract.Service{
		ID: "svc_cooling", Name: "Cooling", Kind: kind, Enabled: true,
		Models: []string{"gpt-5"}, Capabilities: kind.SubscriptionProvider().Capabilities(),
		Subscription: &contract.SubscriptionConnection{
			Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected,
			CredentialRef: "keyring://subscription/svc_cooling",
			Risk: &contract.SubscriptionRisk{
				State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeRateLimit5h,
				ObservedAt: now, PausedUntil: &until,
			},
		},
	}}
	resolver, err := NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	resolver.clock = func() time.Time { return now }
	request := ResolveRequest{Service: store.service.ID, Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true}
	if _, err := resolver.Resolve(t.Context(), request); err == nil {
		t.Fatal("cooling subscription was scheduled")
	}
	now = until.Add(time.Second)
	if _, err := resolver.Resolve(t.Context(), request); err != nil {
		t.Fatalf("expired risk was not reconsidered: %v", err)
	}
}

func BenchmarkPinnedServiceResolution(b *testing.B) {
	services := make([]contract.Service, 200)
	for index := range services {
		services[index] = contract.ServiceFromEndpoint(resolverEndpoint(
			contract.ServiceID(fmt.Sprintf("service_%03d", index)), contract.CapabilityModeNative, true, nil,
		))
		services[index].Models = make([]string, 500)
		for model := range services[index].Models {
			services[index].Models[model] = fmt.Sprintf("model-%03d", model)
		}
	}
	request := ResolveRequest{Service: services[0].ID, Protocol: contract.ProtocolOpenAIResponses, Model: "model-001", Streaming: true}
	for _, direct := range []bool{false, true} {
		b.Run(fmt.Sprintf("direct=%t", direct), func(b *testing.B) {
			var source any = rankingStore{services: services}
			if direct {
				source = &directResolverStore{service: services[0]}
			}
			resolver, err := NewStoreResolver(source)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := resolver.Resolve(b.Context(), request); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func (*directResolverStore) ListServices(context.Context, storage.ServiceListOptions) (storage.ServicePage, error) {
	return storage.ServicePage{}, errors.New("named route unexpectedly listed all services")
}

func (store *directResolverStore) GetService(_ context.Context, id contract.ServiceID) (storage.ServiceRecord, error) {
	store.gets++
	if store.err != nil {
		return storage.ServiceRecord{}, store.err
	}
	if id != store.service.ID {
		return storage.ServiceRecord{}, storage.ErrNotFound
	}
	return storage.ServiceRecord{Service: store.service}, nil
}

func TestPinnedServiceUsesDirectReadAndPreservesRoutingErrors(t *testing.T) {
	service := contract.ServiceFromEndpoint(resolverEndpoint("svc_named", contract.CapabilityModeNative, true, nil))
	store := &directResolverStore{service: service}
	resolver, err := NewStoreResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	request := ResolveRequest{Service: service.ID, Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true}
	candidates, ranking, err := resolver.ResolveRankedCandidates(t.Context(), request)
	if err != nil || len(candidates) != 1 || len(ranking) != 1 || store.gets != 1 {
		t.Fatalf("candidates=%v ranking=%v gets=%d error=%v", candidates, ranking, store.gets, err)
	}
	if _, err := resolver.ResolveService(t.Context(), service.ID); err != nil || store.gets != 2 {
		t.Fatalf("ResolveService gets=%d error=%v", store.gets, err)
	}
	store.service.Enabled = false
	_, ranking, err = resolver.ResolveRankedCandidates(t.Context(), request)
	var pinned *PinnedServiceError
	if !errors.As(err, &pinned) || pinned.Name != service.Name || pinned.Reason != contract.RoutingSkipDisabled || len(ranking) != 1 {
		t.Fatalf("disabled ranking=%v error=%v", ranking, err)
	}
	request.Service = "svc_missing"
	_, ranking, err = resolver.ResolveRankedCandidates(t.Context(), request)
	if !errors.As(err, &pinned) || pinned.Name != "" || len(ranking) != 0 {
		t.Fatalf("missing ranking=%v error=%v", ranking, err)
	}
	request.Service = service.ID
	store.service.Enabled = true
	resolver.RecordRateLimit(candidates[0], time.Minute)
	_, ranking, err = resolver.ResolveRankedCandidates(t.Context(), request)
	var limited *RateLimitedCandidatesError
	if !errors.As(err, &limited) || len(ranking) != 1 || ranking[0].Skip != contract.RoutingSkipRateLimited {
		t.Fatalf("rate limit ranking=%v error=%v", ranking, err)
	}
	store.err = errors.New("storage unavailable")
	if _, err := resolver.ResolveService(t.Context(), service.ID); !errors.Is(err, store.err) {
		t.Fatalf("storage error lost: %v", err)
	}
}
