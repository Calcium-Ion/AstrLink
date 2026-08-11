package endpoint

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestCircuitBreakerConsecutiveFailuresAndSuccessReset(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	breaker := newCircuitBreaker(circuitBreakerConfig{
		FailureThreshold: 3,
		Cooldown:         30 * time.Second,
		Now:              func() time.Time { return now },
	})
	candidate := healthCandidate(false)

	for attempt := 0; attempt < 2; attempt++ {
		if !breaker.begin(candidate) {
			t.Fatalf("attempt %d was excluded before threshold", attempt+1)
		}
		breaker.failure(candidate)
	}
	if !breaker.begin(candidate) {
		t.Fatal("third attempt was excluded before threshold")
	}
	breaker.failure(candidate)
	if breaker.begin(candidate) {
		t.Fatal("open circuit admitted an automatic attempt")
	}

	reset := newCircuitBreaker(circuitBreakerConfig{
		FailureThreshold: 3,
		Cooldown:         30 * time.Second,
		Now:              func() time.Time { return now },
	})
	if !reset.begin(candidate) {
		t.Fatal("initial reset candidate was not admitted")
	}
	reset.failure(candidate)
	if !reset.begin(candidate) {
		t.Fatal("candidate was not admitted after one failure")
	}
	reset.success(candidate)
	for attempt := 0; attempt < 2; attempt++ {
		if !reset.begin(candidate) {
			t.Fatalf("post-success attempt %d was excluded", attempt+1)
		}
		reset.failure(candidate)
	}
	if !reset.begin(candidate) {
		t.Fatal("success did not reset the consecutive-failure count")
	}
}

func TestCircuitBreakerAllowsExactlyOneHalfOpenProbe(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	clock := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	breaker := newCircuitBreaker(circuitBreakerConfig{
		FailureThreshold: 1,
		Cooldown:         30 * time.Second,
		Now:              clock,
	})
	candidate := healthCandidate(false)
	if !breaker.begin(candidate) {
		t.Fatal("initial attempt was not admitted")
	}
	breaker.failure(candidate)

	clockMu.Lock()
	now = now.Add(30 * time.Second)
	clockMu.Unlock()

	var admitted atomic.Int32
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if breaker.begin(candidate) {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	group.Wait()
	if got := admitted.Load(); got != 1 {
		t.Fatalf("half-open admissions = %d, want 1", got)
	}
}

func TestCircuitBreakerHalfOpenOutcomes(t *testing.T) {
	tests := []struct {
		name            string
		complete        func(*circuitBreaker, Resolved)
		wantImmediately bool
	}{
		{
			name: "success closes",
			complete: func(breaker *circuitBreaker, candidate Resolved) {
				breaker.success(candidate)
			},
			wantImmediately: true,
		},
		{
			name: "failure reopens",
			complete: func(breaker *circuitBreaker, candidate Resolved) {
				breaker.failure(candidate)
			},
		},
		{
			name: "abandon releases probe",
			complete: func(breaker *circuitBreaker, candidate Resolved) {
				breaker.abandon(candidate)
			},
			wantImmediately: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
			breaker := newCircuitBreaker(circuitBreakerConfig{
				FailureThreshold: 1,
				Cooldown:         30 * time.Second,
				Now:              func() time.Time { return now },
			})
			candidate := healthCandidate(false)
			if !breaker.begin(candidate) {
				t.Fatal("initial attempt was not admitted")
			}
			breaker.failure(candidate)
			now = now.Add(30 * time.Second)
			if !breaker.begin(candidate) {
				t.Fatal("half-open probe was not admitted")
			}
			test.complete(breaker, candidate)
			if got := breaker.begin(candidate); got != test.wantImmediately {
				t.Fatalf("immediate admission = %t, want %t", got, test.wantImmediately)
			}
			if !test.wantImmediately {
				now = now.Add(30 * time.Second)
				if !breaker.begin(candidate) {
					t.Fatal("reopened circuit did not allow its next timed probe")
				}
			}
		})
	}
}

func TestCircuitBreakerPinnedEndpointBypassesOpenWithoutStealingProbe(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	breaker := newCircuitBreaker(circuitBreakerConfig{
		FailureThreshold: 1,
		Cooldown:         30 * time.Second,
		Now:              func() time.Time { return now },
	})
	automatic := healthCandidate(false)
	pinned := healthCandidate(true)
	if !breaker.begin(automatic) {
		t.Fatal("initial automatic attempt was not admitted")
	}
	breaker.failure(automatic)
	if breaker.begin(automatic) {
		t.Fatal("automatic attempt bypassed an open circuit")
	}
	if !breaker.begin(pinned) {
		t.Fatal("explicitly pinned attempt did not bypass an open circuit")
	}
	breaker.success(pinned)
	if breaker.begin(automatic) {
		t.Fatal("pinned bypass stole and closed the automatic circuit")
	}

	now = now.Add(30 * time.Second)
	if !breaker.begin(automatic) {
		t.Fatal("automatic half-open probe was not admitted")
	}
	if !breaker.begin(pinned) {
		t.Fatal("pinned request did not remain usable during half-open probe")
	}
	breaker.failure(pinned)
	if breaker.begin(automatic) {
		t.Fatal("second automatic half-open probe was admitted concurrently")
	}
	breaker.success(automatic)
	if !breaker.begin(automatic) {
		t.Fatal("successful automatic probe did not close the circuit")
	}
}

func TestStoreResolverImplementsAttemptControllerWithDefaultCircuit(t *testing.T) {
	resolver, err := NewStoreResolver(resolverStore{})
	if err != nil {
		t.Fatal(err)
	}
	candidate := healthCandidate(false)
	for attempt := 0; attempt < defaultFailureThreshold; attempt++ {
		if !resolver.BeginAttempt(candidate) {
			t.Fatalf("default attempt %d was excluded before threshold", attempt+1)
		}
		resolver.RecordFailure(candidate)
	}
	if resolver.BeginAttempt(candidate) {
		t.Fatal("default breaker did not open at its configured threshold")
	}
}

func TestStoreResolverExcludesOpenAutomaticCandidateButKeepsExplicitPin(t *testing.T) {
	tests := []struct {
		name        string
		routes      []contract.Route
		wantPinned  bool
		wantOpenErr bool
	}{
		{
			name:        "automatic candidate is excluded",
			wantOpenErr: true,
		},
		{
			name: "single target route remains usable",
			routes: []contract.Route{
				resolverRoute(
					"route_pin",
					0,
					"",
					resolverTarget("endpoint_health", contract.PlanTypeNative, 0),
				),
			},
			wantPinned: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
			candidateEndpoint := resolverEndpoint(
				"endpoint_health",
				contract.CapabilityModeNative,
				true,
				nil,
			)
			resolver, err := NewStoreResolver(resolverStore{
				endpoints: []contract.Endpoint{candidateEndpoint},
				routes:    test.routes,
			})
			if err != nil {
				t.Fatal(err)
			}
			resolver.breaker = newCircuitBreaker(circuitBreakerConfig{
				FailureThreshold: 1,
				Cooldown:         30 * time.Second,
				Now:              func() time.Time { return now },
			})
			request := ResolveRequest{
				Protocol:  contract.ProtocolOpenAIResponses,
				Model:     "gpt-5",
				Streaming: true,
			}
			candidates, err := resolver.ResolveCandidates(context.Background(), request)
			if err != nil || len(candidates) != 1 || candidates[0].Pinned != test.wantPinned {
				t.Fatalf("initial candidates = %#v, %v", candidates, err)
			}
			if !resolver.BeginAttempt(candidates[0]) {
				t.Fatal("initial candidate was not admitted")
			}
			resolver.RecordFailure(candidates[0])

			candidates, err = resolver.ResolveCandidates(context.Background(), request)
			if test.wantOpenErr {
				if !errors.Is(err, ErrNoHealthyEndpoint) || len(candidates) != 0 {
					t.Fatalf("open automatic candidates = %#v, %v", candidates, err)
				}
				now = now.Add(30 * time.Second)
				candidates, err = resolver.ResolveCandidates(context.Background(), request)
				if err != nil || len(candidates) != 1 {
					t.Fatalf("cooled candidates = %#v, %v", candidates, err)
				}
				return
			}
			if err != nil || len(candidates) != 1 || !candidates[0].Pinned {
				t.Fatalf("open pinned candidates = %#v, %v", candidates, err)
			}
		})
	}
}

func healthCandidate(pinned bool) Resolved {
	return Resolved{
		Endpoint: contract.Endpoint{ID: "endpoint_health"},
		Mode:     contract.CapabilityModeNative,
		Pinned:   pinned,
	}
}
