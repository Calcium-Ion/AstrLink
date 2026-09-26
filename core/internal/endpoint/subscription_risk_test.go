package endpoint

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type serviceReaderFunc func(context.Context, storage.ServiceListOptions) (storage.ServicePage, error)

func (function serviceReaderFunc) ListServices(ctx context.Context, options storage.ServiceListOptions) (storage.ServicePage, error) {
	return function(ctx, options)
}

func TestStoreResolverSkipsRiskPausedSubscriptions(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	future, past := now.Add(time.Minute), now.Add(-time.Minute)
	subscription := func(id contract.ServiceID, risk *contract.SubscriptionRisk) contract.Service {
		kind := contract.ServiceKindCodexSubscription
		return contract.Service{
			ID: id, Name: string(id), Kind: kind, Enabled: true, Models: []string{"gpt-5"},
			Capabilities: kind.SubscriptionProvider().Capabilities(),
			Subscription: &contract.SubscriptionConnection{
				Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected,
				CredentialRef: "keyring://subscription/" + string(id), Risk: risk,
			},
		}
	}
	services := []contract.Service{
		subscription("service_suspended", &contract.SubscriptionRisk{
			State: contract.SubscriptionRiskSuspended, Code: contract.RiskCodeOrganizationDisabled, ObservedAt: past,
		}),
		subscription("service_cooling", &contract.SubscriptionRisk{
			State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeRateLimit5h, ObservedAt: past, PausedUntil: &future,
		}),
		subscription("service_expired", &contract.SubscriptionRisk{
			State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeRateLimit5h, ObservedAt: past, PausedUntil: &past,
		}),
		subscription("service_healthy", nil),
	}
	resolver, err := NewStoreResolver(serviceReaderFunc(func(context.Context, storage.ServiceListOptions) (storage.ServicePage, error) {
		items := make([]storage.ServiceRecord, 0, len(services))
		for _, service := range services {
			items = append(items, storage.ServiceRecord{Service: service})
		}
		return storage.ServicePage{Items: items}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	resolver.breaker = newCircuitBreaker(circuitBreakerConfig{Now: func() time.Time { return now }})
	candidates, err := resolver.ResolveCandidates(context.Background(), ResolveRequest{
		Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true, AllCandidates: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[contract.ServiceID]bool{}
	for _, candidate := range candidates {
		got[candidate.CanonicalService().ID] = true
	}
	if len(got) != 2 || !got["service_expired"] || !got["service_healthy"] {
		t.Fatalf("scheduled services = %v, want service_expired and service_healthy", got)
	}
}
