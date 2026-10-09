package endpoint

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestStoreResolverWithholdsOnlyRateLimitedRoutes(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	candidateEndpoint := resolverEndpoint(
		"endpoint_health",
		contract.CapabilityModeNative,
		true,
		[]string{"gpt-5", "gpt-5-mini"},
	)
	resolver, err := NewStoreResolver(resolverStore{
		endpoints: []contract.Endpoint{candidateEndpoint},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver.clock = func() time.Time { return now }
	request := ResolveRequest{
		Protocol:  contract.ProtocolOpenAIResponses,
		Model:     "gpt-5",
		Streaming: true,
	}
	candidates, err := resolver.ResolveCandidates(context.Background(), request)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("initial candidates = %#v, %v", candidates, err)
	}
	resolver.RecordRateLimit(candidates[0], 30*time.Second)

	other := request
	other.Model = "gpt-5-mini"
	if candidates, err := resolver.ResolveCandidates(context.Background(), other); err != nil || len(candidates) != 1 {
		t.Fatalf("another model of the limited service = %#v, %v", candidates, err)
	}
	candidates, err = resolver.ResolveCandidates(context.Background(), request)
	if !errors.Is(err, ErrNoHealthyEndpoint) || len(candidates) != 0 {
		t.Fatalf("rate-limited candidates = %#v, %v", candidates, err)
	}
	var limited *RateLimitedCandidatesError
	if !errors.As(err, &limited) || len(limited.Limits) != 1 ||
		limited.Limits[0].Service != "endpoint_health" || limited.Limits[0].Model != "gpt-5" ||
		!limited.RetryAt().Equal(now.Add(30*time.Second)) {
		t.Fatalf("rate-limited error = %#v, want the limited route and its deadline", err)
	}
	now = now.Add(30 * time.Second)
	candidates, err = resolver.ResolveCandidates(context.Background(), request)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("cooled candidates = %#v, %v", candidates, err)
	}
}

func TestReleaseRateLimitsDropsOnlyThatService(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	resolver := &StoreResolver{clock: func() time.Time { return now }}
	base := Resolved{
		Service:          contract.Service{ID: "service_a"},
		UpstreamProtocol: contract.ProtocolOpenAIResponses,
		UpstreamModel:    "gpt-5",
		Mode:             contract.CapabilityModeNative,
	}
	otherModel, otherProtocol, otherPlan, otherService := base, base, base, base
	otherModel.UpstreamModel = "gpt-5-mini"
	otherProtocol.UpstreamProtocol = contract.ProtocolOpenAIChat
	otherPlan.PlanType = contract.PlanTypeDelegated
	otherService.Service.ID = "service_ab"
	targets := []Resolved{base, otherModel, otherProtocol, otherPlan}
	for _, candidate := range targets {
		resolver.RecordRateLimit(candidate, 7*24*time.Hour)
	}
	resolver.RecordRateLimit(otherService, time.Hour)
	otherUntil := resolver.RateLimitedUntil(otherService)

	resolver.ReleaseRateLimits("service_a")
	resolver.ReleaseRateLimits("service_a") // Repeated clearing is harmless.
	resolver.ReleaseRateLimits("service_missing")
	for _, candidate := range targets {
		if until := resolver.RateLimitedUntil(candidate); !until.IsZero() {
			t.Fatalf("cleared route still limited: %+v until %v", candidate, until)
		}
	}
	if until := resolver.RateLimitedUntil(otherService); !until.Equal(otherUntil) {
		t.Fatalf("service with shared ID prefix changed: %v, want %v", until, otherUntil)
	}
	resolver.RecordRateLimit(base, time.Minute)
	if until := resolver.RateLimitedUntil(base); !until.Equal(now.Add(time.Minute)) {
		t.Fatalf("new upstream 429 did not restore cooldown: %v", until)
	}
}

func healthCandidate() Resolved {
	return Resolved{
		Endpoint: contract.Endpoint{ID: "endpoint_health"},
		Mode:     contract.CapabilityModeNative,
	}
}

func TestRateLimitCooldownIsIsolatedAndConcurrent(t *testing.T) {
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	var limits rateLimits
	a := healthCandidate()
	a.UpstreamModel = "a"
	a.UpstreamProtocol = contract.ProtocolOpenAIChat
	b := a
	b.UpstreamModel = "b"
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			limits.limit(a, time.Second, now)
			if limits.until(a, now).IsZero() {
				t.Error("rate-limited target admitted")
			}
			if !limits.until(b, now).IsZero() {
				t.Error("other model blocked")
			}
		}()
	}
	group.Wait()
	if !limits.until(a, now.Add(time.Second)).IsZero() {
		t.Fatal("cooldown did not expire")
	}
}
