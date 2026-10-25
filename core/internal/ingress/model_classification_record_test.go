package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const (
	recordTestBusinessOK  = `{"id":"resp_business","object":"response","status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`
	recordTestUpstreamErr = `{"error":{"message":"upstream exploded","type":"server_error"}}`
)

// classifierWithID is the classifier model's answer as a real Responses
// upstream sends it: with a response id of its own.
func classifierWithID(text string) string {
	return `{"id":"resp_classifier",` + classificationEnvelope(text)[1:]
}

// classificationRecordHandler serves an existing_model classifier and the
// business call from one fake upstream. The classification request is the
// one carrying instructions; every other request is the business call.
func classificationRecordHandler(t *testing.T, store *modelRoutingBindingTestStore, classifierStatus int, classifierBody string, businessStatus int, businessBody string) *Handler {
	t.Helper()
	var mu sync.Mutex
	forwarder := transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(request.Body)
		mu.Lock()
		defer mu.Unlock()
		status, reply := businessStatus, businessBody
		if requestInstructions(string(data)) != "" {
			status, reply = classifierStatus, classifierBody
		}
		return &http.Response{
			StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(reply)),
		}, nil
	}))
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false), UpstreamModel: request.Model}, nil
		}),
		RequestRecords:     store,
		ClassifierSettings: ClassifierSettingsProviderFunc(func(context.Context) (contract.ClassifierSettings, error) { return existingModelSettings(), nil }),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
			return "token-x", nil
		}),
	})
	handler.forwarder = forwarder
	store.settings.ModelRoutingGroups = []contract.ModelRoutingGroup{cloudTestGroup(nil)}
	store.settings.ChannelStickiness = &contract.ChannelStickiness{Enabled: true, TTLSeconds: 600}
	return handler
}

func serveClassificationRecord(t *testing.T, handler *Handler) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"astrlink/superclaude","input":"write go code"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func affinityResponseIDs(handler *Handler) []string {
	handler.affinities.mu.Lock()
	defer handler.affinities.mu.Unlock()
	var ids []string
	for key := range handler.affinities.entries {
		ids = append(ids, key.responseID)
	}
	return ids
}

// The existing-model classifier's sub-call keeps a record session of its own,
// so whatever it answers, the parent request's record, response affinity and
// session binding describe the business call alone.
func TestExistingModelClassificationLeavesTheRecordToTheBusinessCall(t *testing.T) {
	cases := []struct {
		name             string
		classifierStatus int
		classifierBody   string
		businessStatus   int
		businessBody     string
		wantStatus       contract.RequestStatus
		wantMember       string
	}{
		{"classifier fails, business succeeds", http.StatusInternalServerError, recordTestUpstreamErr, http.StatusOK, recordTestBusinessOK, contract.RequestStatusSucceeded, "fast"},
		{"both succeed", http.StatusOK, classifierWithID(classificationStrictJSONAnswer), http.StatusOK, recordTestBusinessOK, contract.RequestStatusSucceeded, "strong"},
		{"classifier succeeds, business fails", http.StatusOK, classifierWithID(classificationStrictJSONAnswer), http.StatusBadRequest, recordTestUpstreamErr, contract.RequestStatusFailed, ""},
		{"both fail", http.StatusInternalServerError, recordTestUpstreamErr, http.StatusBadRequest, recordTestUpstreamErr, contract.RequestStatusFailed, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := &modelRoutingBindingTestStore{redirectSettingsStore: newRedirectSettingsStore()}
			handler := classificationRecordHandler(t, store, test.classifierStatus, test.classifierBody, test.businessStatus, test.businessBody)
			response := serveClassificationRecord(t, handler)
			if response.Code != test.businessStatus {
				t.Fatalf("client status = %d, want the business call's %d", response.Code, test.businessStatus)
			}
			record := store.root(t)
			if record.Status != test.wantStatus || record.HTTPStatus == nil || *record.HTTPStatus != test.businessStatus {
				t.Fatalf("record status = %s, http = %v; want %s, %d", record.Status, record.HTTPStatus, test.wantStatus, test.businessStatus)
			}
			if record.AttemptIndex != 1 {
				t.Fatalf("record attempt = %d, want the business call as attempt 1", record.AttemptIndex)
			}
			if record.ModelRouting == nil {
				t.Fatal("no model routing decision recorded")
			}
			succeeded := test.wantStatus == contract.RequestStatusSucceeded
			// The routing decision follows the request to its end, failed
			// as well as served.
			wantExecution := contract.ModelRoutingExecutionFailed
			if succeeded {
				wantExecution = contract.ModelRoutingExecutionSucceeded
			}
			if execution := record.ModelRouting.Execution; execution.Status != wantExecution || execution.AttemptCount != 1 {
				t.Fatalf("routing execution = %+v, want %s after 1 attempt", execution, wantExecution)
			}
			if succeeded {
				if record.Error != nil {
					t.Fatalf("record error = %+v on a served request", record.Error)
				}
				if record.OutputResponseID == nil || *record.OutputResponseID != "resp_business" {
					t.Fatalf("output response id = %v, want resp_business", record.OutputResponseID)
				}
				if ids := affinityResponseIDs(handler); len(ids) != 1 || ids[0] != "resp_business" {
					t.Fatalf("response affinity = %v, want resp_business alone", ids)
				}
				if store.binding == nil || store.binding.MemberID != test.wantMember {
					t.Fatalf("session binding = %+v, want member %s", store.binding, test.wantMember)
				}
			} else {
				if record.Error == nil {
					t.Fatal("failed request recorded no error")
				}
				if ids := affinityResponseIDs(handler); len(ids) != 0 {
					t.Fatalf("response affinity = %v after a failed request", ids)
				}
				if store.upsertCalls != 0 {
					t.Fatalf("session binding written for a failed request: %+v", store.binding)
				}
			}
		})
	}
}

// A business call that is retried after a 502 leaves child records that say
// it failed, whatever the classifier answered before it.
func TestExistingModelClassificationLeavesRetriedAttemptsFailed(t *testing.T) {
	store := &modelRoutingBindingTestStore{redirectSettingsStore: newRedirectSettingsStore()}
	handler := classificationRecordHandler(t, store, http.StatusOK, classifierWithID(classificationStrictJSONAnswer), http.StatusBadGateway, recordTestUpstreamErr)
	_ = serveClassificationRecord(t, handler)
	children := 0
	for _, record := range store.snapshot() {
		if record.ParentRequestID == nil {
			continue
		}
		children++
		if record.Status != contract.RequestStatusFailed || record.HTTPStatus == nil || *record.HTTPStatus != http.StatusBadGateway || record.Error == nil {
			t.Fatalf("retried attempt %d: status %s, http %v, error %+v; want failed with 502", record.AttemptIndex, record.Status, record.HTTPStatus, record.Error)
		}
	}
	if children == 0 {
		t.Fatal("the 502 left no retried attempt to check")
	}
}
