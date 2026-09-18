package ingress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func recoveryCandidates(count int, policy contract.FailurePolicy, failover contract.FailoverPolicy) []endpoint.Resolved {
	result := make([]endpoint.Resolved, count)
	for i := range result {
		service := validEndpoint(contract.ProtocolOpenAIChat, false)
		service.ID = contract.ServiceID("endpoint_" + string(rune('a'+i)))
		service.BaseURL = "https://" + string(rune('A'+i)) + ".example"
		result[i] = endpoint.Resolved{Endpoint: service, FailurePolicy: &policy, Failover: &failover}
	}
	return result
}

func TestRecoveryOrderingAndRules(t *testing.T) {
	for _, tt := range []struct {
		name    string
		order   contract.FailoverStrategy
		action  contract.FailureAction
		enabled bool
		limit   int
		want    []int
	}{
		{"once per service", contract.FailoverOnly, contract.FailureRetryAndFailover, true, 6, []int{0, 1, 2}},
		{"once cannot retry-only", contract.FailoverOnly, contract.FailureRetry, true, 6, []int{0}},
		{"once with switching off", contract.FailoverOnly, contract.FailureRetryAndFailover, false, 6, []int{0}},
		{"once respects budget", contract.FailoverOnly, contract.FailureRetryAndFailover, true, 2, []int{0, 1}},
		{"retry first", contract.RetryFirst, contract.FailureRetryAndFailover, true, 6, []int{0, 0, 1, 1, 2, 2}},
		{"failover first", contract.FailoverFirst, contract.FailureRetryAndFailover, true, 6, []int{0, 1, 2, 0, 1, 2}},
		{"only retry overrides order", contract.FailoverFirst, contract.FailureRetry, true, 6, []int{0, 0}},
		{"only switch overrides order", contract.RetryFirst, contract.FailureFailover, true, 6, []int{0, 1, 2}},
		{"direct error", contract.RetryFirst, contract.FailureStop, true, 6, []int{0}},
		{"switch disabled", contract.FailoverFirst, contract.FailureRetryAndFailover, false, 6, []int{0, 0}},
		{"shared budget", contract.RetryFirst, contract.FailureRetryAndFailover, true, 3, []int{0, 0, 1}},
		{"one attempt", contract.RetryFirst, contract.FailureRetryAndFailover, true, 1, []int{0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS = 0
			schedule := newRecoverySchedule(recoveryCandidates(3, policy, contract.FailoverPolicy{Enabled: tt.enabled, Strategy: tt.order, MaxAttempts: tt.limit}), true)
			var got []int
			for {
				i, ok := schedule.next(context.Background())
				if !ok {
					break
				}
				got = append(got, i)
				schedule.started(i)
				if !schedule.recover(i, tt.action, 0) {
					break
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("attempts %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecoverySkippedCandidatesAndRetryAfter(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 0
	candidates := recoveryCandidates(3, policy, contract.FailoverPolicy{Enabled: true, Strategy: contract.FailoverFirst, MaxAttempts: 6})
	schedule := newRecoverySchedule(candidates, true)
	i, _ := schedule.next(context.Background())
	schedule.started(i)
	schedule.recover(i, contract.FailureRetryAndFailover, 0)
	// B and C fail local preparation. Neither spends quota, and A can still retry.
	for _, want := range []int{1, 2, 0} {
		i, ok := schedule.next(context.Background())
		if !ok || i != want {
			t.Fatalf("next %d/%v want %d", i, ok, want)
		}
	}
	if schedule.total != 1 {
		t.Fatalf("phantom attempts: %d", schedule.total)
	}
	schedule = newRecoverySchedule(candidates, true)
	i, _ = schedule.next(context.Background())
	schedule.started(i)
	if !schedule.recover(i, contract.FailureRetryAndFailover, 6*time.Second) || schedule.nextIndex != 1 || schedule.retryable[0] {
		t.Fatal("long Retry-After must skip A")
	}
	schedule = newRecoverySchedule(candidates, true)
	i, _ = schedule.next(context.Background())
	schedule.started(i)
	if schedule.recover(i, contract.FailureRetry, 6*time.Second) || schedule.stopReason != "retry_after_limit" {
		t.Fatal("retry-only must stop at maximum wait")
	}
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		header string
		want   time.Duration
	}{{"2", 2 * time.Second}, {now.Add(3 * time.Second).Format(http.TimeFormat), 3 * time.Second}, {"-2", 0}, {"invalid", 0}, {now.Add(-time.Second).Format(http.TimeFormat), 0}} {
		if got := parseRetryAfter(tt.header, now); got != tt.want {
			t.Fatalf("%s => %s want %s", tt.header, got, tt.want)
		}
	}
	schedule = newRecoverySchedule(candidates, false)
	i, _ = schedule.next(context.Background())
	schedule.started(i)
	if schedule.recover(i, contract.FailureRetryAndFailover, 0) {
		t.Fatal("opaque body replayed")
	}
}

func TestRecoveryCancelDuringBackoff(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 5000
	schedule := newRecoverySchedule(recoveryCandidates(1, policy, contract.DefaultFailoverPolicy()), true)
	i, _ := schedule.next(context.Background())
	schedule.started(i)
	schedule.recover(i, contract.FailureRetry, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, ok := schedule.next(ctx); ok || time.Since(start) > 100*time.Millisecond || schedule.stopReason != "cancelled" {
		t.Fatal("cancellation did not stop backoff")
	}
}

func TestHTTPRecoveryPreservesResponseBoundary(t *testing.T) {
	for _, tt := range []struct {
		name   string
		code   int
		action contract.FailureAction
		order  contract.FailoverStrategy
		want   []string
		final  int
	}{
		{"429 once then backup", 429, contract.FailureRetryAndFailover, contract.FailoverOnly, []string{"A.example", "B.example"}, 200},
		{"418 retry-only stops in ABC", 418, contract.FailureRetry, contract.FailoverOnly, []string{"A.example"}, 418},
		{"429 retry then backup", 429, contract.FailureRetryAndFailover, contract.RetryFirst, []string{"A.example", "A.example", "B.example"}, 200},
		{"503 backup first", 503, contract.FailureRetryAndFailover, contract.FailoverFirst, []string{"A.example", "B.example"}, 200},
		{"401 backup only", 401, contract.FailureFailover, contract.RetryFirst, []string{"A.example", "B.example"}, 200},
		{"custom 418 retry only", 418, contract.FailureRetry, contract.FailoverFirst, []string{"A.example", "A.example"}, 418},
		{"unlisted 400", 400, contract.FailureStop, contract.RetryFirst, []string{"A.example"}, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.InitialDelayMS = 0
			policy.HTTPStatus["418"] = tt.action
			failover := contract.DefaultFailoverPolicy()
			failover.Strategy = tt.order
			var attempts []string
			store := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: recoveryCandidates(2, policy, failover)}, RequestRecords: store, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				attempts = append(attempts, request.URL.Host)
				code, body, marker := tt.code, `{"error":{"message":"A failed"}}`, "intermediate"
				if request.URL.Host == "B.example" {
					code, body, marker = 200, `{"choices":[],"model":"public"}`, "final"
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}, "X-Attempt": {marker}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))})
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[]}`))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if !reflect.DeepEqual(attempts, tt.want) || response.Code != tt.final {
				t.Fatalf("%v status=%d body=%s", attempts, response.Code, response.Body.String())
			}
			if tt.final == 200 && (strings.Contains(response.Body.String(), "A failed") || response.Header().Get("X-Attempt") != "final") {
				t.Fatalf("intermediate response leaked: %v %s", response.Header(), response.Body.String())
			}
			roots, children := 0, 0
			for _, record := range store.records {
				if record.ParentRequestID == nil {
					roots++
					if record.AttemptIndex != len(attempts) || record.ChildCount != len(attempts)-1 {
						t.Fatalf("root attempt=%d children=%d", record.AttemptIndex, record.ChildCount)
					}
				} else {
					children++
					if record.Recovery == nil || record.Recovery.Reason == "" {
						t.Fatal("missing failure reason")
					}
				}
			}
			if roots != 1 || children != len(attempts)-1 {
				t.Fatalf("roots=%d children=%d", roots, children)
			}
		})
	}
}

func TestRecoveryCancellationDoesNotInventChild(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.InitialDelayMS = 5000
	store := &memoryRequestRecordStore{}
	ctx, cancel := context.WithCancel(context.Background())
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: recoveryCandidates(2, policy, contract.DefaultFailoverPolicy())}, RequestRecords: store, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		time.AfterFunc(10*time.Millisecond, cancel)
		return nil, errors.New("connection refused")
	}))})
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[]}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if len(store.records) != 1 || store.records[0].AttemptIndex != 1 || store.records[0].ChildCount != 0 || store.records[0].Status != contract.RequestStatusCancelled {
		t.Fatalf("records=%+v", store.records)
	}
}

func TestRecoveryFinalHTTPWhenBackupCannotBePrepared(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.MaxRetries = 0
	policy.InitialDelayMS = 0
	candidates := recoveryCandidates(2, policy, contract.DefaultFailoverPolicy())
	candidates[1].Endpoint.Capabilities = nil
	store := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: candidates}, RequestRecords: store, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"last real error"}}`))}, nil
	}))})
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != 503 || !strings.Contains(response.Body.String(), "last real error") || len(store.records) != 1 || store.records[0].AttemptIndex != 1 || store.records[0].ChildCount != 0 {
		t.Fatalf("status=%d body=%s records=%+v", response.Code, response.Body.String(), store.records)
	}
}

type recoveryTimeoutError struct{}

func (recoveryTimeoutError) Error() string   { return "upstream timed out" }
func (recoveryTimeoutError) Timeout() bool   { return true }
func (recoveryTimeoutError) Temporary() bool { return true }

func TestRecoveryFinalTransportErrorReplacesDiscardedHTTPError(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
	}{{"connection", io.ErrUnexpectedEOF, 502}, {"timeout", recoveryTimeoutError{}, 504}} {
		t.Run(tt.name, func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.MaxRetries = 0
			policy.InitialDelayMS = 0
			trips := 0
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: recoveryCandidates(2, policy, contract.DefaultFailoverPolicy())}, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
				trips++
				if trips == 1 {
					return jsonResponse(503, `{"error":{"message":"discarded"}}`), nil
				}
				return nil, tt.err
			}))})
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public","messages":[]}`))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(response, request)
			if trips != 2 || response.Code != tt.status || strings.Contains(response.Body.String(), "discarded") || strings.Count(response.Body.String(), `"error"`) != 1 {
				t.Fatalf("%d %d %s", trips, response.Code, response.Body.String())
			}
		})
	}
}
