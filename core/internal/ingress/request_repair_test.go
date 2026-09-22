package ingress

import (
	"context"
	"fmt"
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

func TestRequestRepairIndependentOfOrdinaryRetryPolicy(t *testing.T) {
	for _, repair := range []struct {
		name, body, failure, path, reason string
	}{
		{"thinking", signedThinkingRequest, signatureFailure, "/v1/messages", thinkingSignatureRecoveryReason},
		{"reasoning", codexReasoningRequest, invalidReasoningCipher, "/v1/responses", openAIReasoningRecoveryReason},
		{"function output", encryptedFunctionOutputRequest, invalidFunctionOutputCipher, "/v1/responses", openAIFunctionOutputRecoveryReason},
	} {
		for _, strategy := range []contract.FailoverStrategy{contract.FailoverOnly, contract.RetryFirst, contract.FailoverFirst} {
			for _, manual := range []bool{false, true} {
				for _, rule := range []contract.FailureAction{contract.FailureStop, contract.FailureFailover} {
					t.Run(fmt.Sprintf("%s/%s/manual=%v/%s", repair.name, strategy, manual, rule), func(t *testing.T) {
						policy := contract.DefaultFailurePolicy()
						policy.MaxRetries = 0
						policy.InitialDelayMS, policy.MaxDelayMS = 60000, 60000
						policy.HTTPStatus["400"] = rule
						on := true
						policy.OpenAIFunctionOutputRecovery = &on
						candidates := openAIRecoveryCandidates(policy, 6, contract.ProtocolOpenAIResponses, "gpt-5.3-codex")
						if repair.name == "thinking" {
							candidates = thinkingCandidates(policy, 6, "claude-sonnet-4-6")
						}
						candidates[0].Failover.Strategy = strategy
						candidates[0].Failover.Enabled = false
						if manual {
							candidates[0].Path = &endpoint.RecoveryPathSnapshot{ID: "path_test", Mode: "steps", StepID: "step_first"}
						}
						var sent []string
						records := &memoryRequestRecordStore{}
						handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: candidates}, RequestRecords: records, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
							data, _ := io.ReadAll(request.Body)
							sent = append(sent, string(data))
							if len(sent) == 1 {
								response := jsonResponse(400, repair.failure)
								response.Header.Set("Retry-After", "120")
								return response, nil
							}
							return jsonResponse(200, `{"id":"resp_ok","type":"message","content":[],"output":[]}`), nil
						}))})
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						request := httptest.NewRequest("POST", repair.path, strings.NewReader(repair.body)).WithContext(ctx)
						request.Header.Set("Content-Type", "application/json")
						response := httptest.NewRecorder()
						handler.ServeHTTP(response, request)
						if len(sent) != 2 || response.Code != 200 || sent[0] == sent[1] || ctx.Err() != nil {
							t.Fatalf("repair blocked: attempts=%d status=%d context=%v", len(sent), response.Code, ctx.Err())
						}
						roots, children := 0, 0
						for _, record := range records.records {
							if record.ParentRequestID == nil {
								roots++
								if record.AttemptIndex != 2 || record.ChildCount != 1 {
									t.Fatalf("invalid final attempt: index=%d children=%d", record.AttemptIndex, record.ChildCount)
								}
								continue
							}
							children++
							if record.Recovery == nil || record.Recovery.Reason != repair.reason || record.Recovery.DelayMS != 0 || record.AttemptIndex != 1 {
								t.Fatalf("invalid repair record: attempt=%d recovery=%+v", record.AttemptIndex, record.Recovery)
							}
							if manual && record.Recovery.StepID != "step_first" {
								t.Fatalf("repair consumed a manual step: %+v", record.Recovery)
							}
						}
						if roots != 1 || children != 1 {
							t.Fatalf("repair roots=%d children=%d", roots, children)
						}
					})
				}
			}
		}
	}
}

func TestRequestRepairPreservesOrdinaryRetryQuota(t *testing.T) {
	for _, statuses := range [][]int{{400, 503, 200}, {503, 400, 200}} {
		t.Run(fmt.Sprint(statuses), func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.MaxRetries, policy.InitialDelayMS = 1, 0
			trips := 0
			handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: thinkingCandidates(policy, 6, "claude-sonnet-4-6")}, Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
				if trips >= len(statuses) {
					t.Fatal("unexpected extra attempt")
				}
				status := statuses[trips]
				trips++
				return jsonResponse(status, signatureFailure), nil
			}))})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(signedThinkingRequest)))
			if trips != 3 || response.Code != 200 {
				t.Fatalf("repair consumed retry quota: trips=%d status=%d", trips, response.Code)
			}
		})
	}
}

func TestRequestRepairResumesManualPathWithoutRepeatingRepair(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.MaxRetries, policy.InitialDelayMS = 0, 0
	base := thinkingCandidates(policy, 6, "claude-sonnet-4-6", "claude-opus-4-6")
	candidates := []endpoint.Resolved{base[0], base[1], base[0]}
	for i := range candidates {
		candidates[i].Path = &endpoint.RecoveryPathSnapshot{ID: "path_test", Mode: "steps", StepID: fmt.Sprintf("step_%d", i)}
	}
	var attempts []string
	records := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: candidates}, RequestRecords: records, Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts = append(attempts, request.URL.Host)
		data, _ := io.ReadAll(request.Body)
		if len(attempts) == 2 || len(attempts) == 4 {
			if strings.Contains(string(data), `"signature":"invalid"`) {
				t.Fatal("same target lost its repaired body")
			}
		}
		if len(attempts) == 2 || len(attempts) == 3 {
			return jsonResponse(503, `{"error":{"message":"unavailable"}}`), nil
		}
		return jsonResponse(400, signatureFailure), nil
	}))})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(signedThinkingRequest)))
	if response.Code != 400 || !reflect.DeepEqual(attempts, []string{"A.example", "A.example", "B.example", "A.example"}) {
		t.Fatalf("attempts=%v status=%d", attempts, response.Code)
	}
	for _, record := range records.records {
		if record.AttemptIndex == 2 && (record.Recovery == nil || record.Recovery.StepID != "step_0") {
			t.Fatalf("repair moved to a later step: %+v", record.Recovery)
		}
	}
}

func TestRequestRepairKeepsBackoffAndTotalBudget(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	candidates := recoveryCandidates(1, policy, contract.DefaultFailoverPolicy())
	schedule := newRecoverySchedule(candidates, true)
	schedule.simulation = true
	i, _ := schedule.next(context.Background())
	schedule.started(i)
	if !schedule.repair(i) {
		t.Fatal("repair refused")
	}
	i, _ = schedule.next(context.Background())
	schedule.started(i)
	if !schedule.recover(i, contract.FailureRetry, 0) || schedule.waitBounds[i] != [2]int{400, 600} {
		t.Fatalf("repair advanced ordinary backoff: %v", schedule.waitBounds[i])
	}
	if schedule.total != 2 || schedule.counts[i] != 1 {
		t.Fatalf("invalid counters: total=%d ordinary=%d", schedule.total, schedule.counts[i])
	}
	schedule.policy.MaxAttempts = 2
	if schedule.repair(i) || schedule.recover(i, contract.FailureRetryAndFailover, 0) || schedule.stopReason != "attempt_limit" {
		t.Fatal("repair escaped total network budget")
	}
}
