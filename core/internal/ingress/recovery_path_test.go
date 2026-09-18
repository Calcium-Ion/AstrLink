package ingress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func manualCandidates(policy contract.FailurePolicy, limit int) []endpoint.Resolved {
	base := recoveryCandidates(3, policy, contract.FailoverPolicy{Enabled: true, Strategy: contract.RetryFirst, MaxAttempts: limit})
	result := []endpoint.Resolved{base[0], base[1], base[0], base[2]}
	for i := range result {
		result[i].Path = &endpoint.RecoveryPathSnapshot{ID: "path_test", Name: "Shared path", Version: "version_one", Mode: "steps", StepID: fmt.Sprintf("step_%d", i)}
		result[i].RequestedModel = "public"
	}
	return result
}
func TestManualPathPreviewMatchesActualHTTP(t *testing.T) {
	for _, tt := range []struct {
		name, scenario string
		code, limit    int
		after          string
		want           []string
	}{
		{"custom order", "503", 503, 6, "", []string{"A.example", "B.example", "A.example", "C.example"}},
		{"only switch", "401", 401, 6, "", []string{"A.example", "B.example", "C.example"}},
		{"only retry", "418", 418, 6, "", []string{"A.example", "A.example"}},
		{"stop", "400", 400, 6, "", []string{"A.example"}},
		{"budget", "503", 503, 2, "", []string{"A.example", "B.example"}},
		{"long retry after", "429", 429, 6, "6", []string{"A.example", "B.example", "C.example"}},
		{"network", "network_error", 0, 6, "", []string{"A.example", "B.example", "A.example", "C.example"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS = 0
			policy.MaxRetries = 0
			policy.HTTPStatus["418"] = contract.FailureRetry
			candidates := manualCandidates(policy, tt.limit)
			preview, err := PreviewRecovery(context.Background(), candidates, contract.RecoveryPreviewInput{Error: tt.scenario, Model: "public", RetryAfter: tt.after})
			if err != nil {
				t.Fatal(err)
			}
			var predicted []string
			for _, step := range preview.Steps {
				if step.Status != "skipped" {
					for _, candidate := range candidates {
						if candidate.CanonicalService().ID == step.ServiceID {
							predicted = append(predicted, strings.TrimPrefix(candidate.EffectiveBaseURL(), "https://"))
							break
						}
					}
				}
			}
			var actual []string
			records := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: candidates}, RequestRecords: records, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				actual = append(actual, request.URL.Host)
				if tt.code == 0 {
					return nil, errors.New("connection refused")
				}
				return &http.Response{StatusCode: tt.code, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {tt.after}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"failed"}}`))}, nil
			}))})
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[]}`))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if !reflect.DeepEqual(actual, tt.want) || !reflect.DeepEqual(predicted, actual) {
				t.Fatalf("actual=%v preview=%v want=%v body=%s", actual, predicted, tt.want, response.Body.String())
			}
			if strings.Count(response.Body.String(), "failed") > 1 {
				t.Fatalf("multiple final errors: %s", response.Body.String())
			}
			for _, record := range records.records {
				if record.Recovery == nil || record.Recovery.PathID != "path_test" || record.Recovery.PathVersion != "version_one" || record.Recovery.StepID == "" {
					t.Fatalf("missing path snapshot: %+v", record.Recovery)
				}
			}
		})
	}
}
func TestManualPathSkipsAndCancellation(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 5000
	candidates := manualCandidates(policy, 6)
	candidates[1].Unavailable = "circuit_open"
	preview, err := PreviewRecovery(context.Background(), candidates, contract.RecoveryPreviewInput{Error: "network_error", SuccessAt: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Steps) != 4 || preview.Steps[1].Status != "skipped" || preview.Steps[2].Status != "succeeded" {
		t.Fatalf("preview=%+v", preview)
	}
	schedule := newRecoverySchedule(candidates, true)
	i, _ := schedule.next(context.Background())
	schedule.started(i)
	schedule.recover(i, contract.FailureRetry, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := schedule.next(ctx); ok || schedule.stopReason != "cancelled" {
		t.Fatal("manual backoff ignored cancellation")
	}
	schedule = newRecoverySchedule(candidates, false)
	i, _ = schedule.next(context.Background())
	schedule.started(i)
	if schedule.recover(i, contract.FailureRetryAndFailover, 0) {
		t.Fatal("unreplayable manual request retried")
	}
}
