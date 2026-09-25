package routinggraph

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestThreeValuedConditionsAndQuotaExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	unknown := contract.RoutingPredicate{Field: "quota.exhausted", Operator: "eq", Value: json.RawMessage(`true`), ServiceID: "service_test", Window: "primary"}
	truePredicate := contract.RoutingPredicate{Field: "request.streaming", Operator: "eq", Value: json.RawMessage(`true`)}
	facts := Facts{Streaming: true, Now: now}
	if _, known := Evaluate(unknown, facts); known {
		t.Fatal("missing quota was treated as known")
	}
	if value, known := Evaluate(contract.RoutingPredicate{Any: []contract.RoutingPredicate{unknown, truePredicate}}, facts); !known || !value {
		t.Fatal("true OR unknown must be true")
	}
	falsePredicate := truePredicate
	falsePredicate.Value = json.RawMessage(`false`)
	if value, known := Evaluate(contract.RoutingPredicate{All: []contract.RoutingPredicate{unknown, falsePredicate}}, facts); !known || value {
		t.Fatal("false AND unknown must be false")
	}
	reset := now.Add(time.Minute)
	facts.Quota = map[contract.ServiceID]contract.SubscriptionUsage{"service_test": {ServiceID: "service_test", FetchedAt: now, Primary: &contract.RateLimitWindow{UsedPercent: 100, ResetAt: &reset}}}
	if value, known := Evaluate(unknown, facts); !known || !value {
		t.Fatal("confirmed exhausted quota was ignored")
	}
	facts.Now = reset.Add(time.Second)
	if _, known := Evaluate(unknown, facts); known {
		t.Fatal("an unverified reset must become unknown")
	}
	facts.Now = now.Add(11 * time.Minute)
	if _, known := Evaluate(unknown, facts); known {
		t.Fatal("stale quota must become unknown")
	}
}
