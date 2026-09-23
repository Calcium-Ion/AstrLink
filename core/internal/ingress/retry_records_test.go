package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestExecuteCandidatesDemotesFailedAttemptsIntoIndependentChildren(t *testing.T) {
	store := &memoryRequestRecordStore{}
	blobs := &memoryAuditBlobs{}
	settings := &memoryAuditSettings{settings: contract.AuditSettings{
		RequestBodyEnabled: true, ResponseContentEnabled: true, HTTPMetaEnabled: true,
		RequestBodyMaxBytes: 1024, ResponseContentMaxBytes: 1024,
		MetadataRetentionDays: 30, ContentRetentionDays: 7,
	}}
	var trips atomic.Int32
	endpointA := validEndpoint(contract.ProtocolOpenAIChat, false)
	endpointB := validEndpoint(contract.ProtocolOpenAIChat, false)
	endpointC := validEndpoint(contract.ProtocolOpenAIChat, false)
	endpointA.ID = "endpoint_retry_a"
	endpointB.ID = "endpoint_retry_b"
	endpointC.ID = "endpoint_retry_c"

	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{
			{Endpoint: endpointA, UpstreamModel: "upstream-a"},
			{Endpoint: endpointB, UpstreamModel: "upstream-b"},
			{Endpoint: endpointC, UpstreamModel: "upstream-c"},
		}},
		RequestRecords: store,
		AuditSettings:  settings,
		AuditBlobs:     blobs,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			trip := trips.Add(1)
			if _, err := io.ReadAll(request.Body); err != nil {
				t.Fatalf("read outbound request %d: %v", trip, err)
			}
			if trip < 3 {
				return nil, io.ErrUnexpectedEOF
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"chat_ok","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
				)),
				Request: request,
			}, nil
		})),
	})

	response := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"public-alias","messages":[{"role":"user","content":"hi"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if trips.Load() != 3 {
		t.Fatalf("round trips=%d, want 3", trips.Load())
	}

	var root *contract.RequestRecord
	var children []contract.RequestRecord
	for index := range store.records {
		record := store.records[index]
		if record.Status == contract.RequestStatusPending {
			continue
		}
		if record.ParentRequestID == nil {
			if root != nil && root.ID != record.ID {
				t.Fatalf("multiple roots: %s and %s", root.ID, record.ID)
			}
			copy := record
			root = &copy
			continue
		}
		children = append(children, record)
	}
	if root == nil {
		t.Fatalf("missing root among %#v", store.records)
	}
	if root.AttemptIndex != 3 || root.Status != contract.RequestStatusSucceeded {
		t.Fatalf("root=%#v", root)
	}
	if root.ChildCount != 2 {
		t.Fatalf("root child_count=%d, want 2", root.ChildCount)
	}
	if root.Usage == nil ||
		root.Usage.InputTokens != 1 ||
		root.Usage.OutputTokens != 2 ||
		root.Usage.TotalTokens != 3 {
		t.Fatalf("root usage=%#v, want 1/2/3", root.Usage)
	}
	if len(children) != 2 {
		t.Fatalf("children=%d %#v", len(children), children)
	}
	indexes := map[int]bool{}
	for _, child := range children {
		if *child.ParentRequestID != root.ID {
			t.Fatalf("child parent=%s root=%s", *child.ParentRequestID, root.ID)
		}
		if child.Status != contract.RequestStatusFailed {
			t.Fatalf("child status=%s", child.Status)
		}
		if child.ID == root.ID {
			t.Fatal("child reused root id")
		}
		indexes[child.AttemptIndex] = true
	}
	if !indexes[1] || !indexes[2] {
		t.Fatalf("child attempt indexes=%v", indexes)
	}
	if root.SessionID == nil {
		t.Fatal("root missing session id")
	}
	if root.TurnIndex == nil || *root.TurnIndex != 1 {
		t.Fatalf("root turn_index=%v, want 1", root.TurnIndex)
	}
	if !hasSessionCursor(root.Cursors, contract.SessionCursorExplicit, contract.SessionCursorOut, "chat_ok") {
		t.Fatalf("root cursors=%#v, want explicit out chat_ok", root.Cursors)
	}
	for _, child := range children {
		if child.SessionID == nil || *child.SessionID != *root.SessionID {
			t.Fatalf("child session=%v root=%v", child.SessionID, root.SessionID)
		}
		if len(child.Cursors) != 0 || child.SessionLink != nil {
			t.Fatalf("failed attempt must not anchor the session: cursors=%#v link=%#v", child.Cursors, child.SessionLink)
		}
		if len(child.Events) == 0 {
			t.Fatal("child missing attempt events")
		}
		for _, event := range child.Events {
			if event.AttemptIndex != child.AttemptIndex {
				t.Fatalf("child event attempt=%d child=%d", event.AttemptIndex, child.AttemptIndex)
			}
		}
	}

	plainByRecord := make(map[contract.RequestID]map[storage.AuditDirection][]byte)
	for _, blob := range blobs.blobs {
		plain, err := storage.OpenAuditBlob(blobs.key, blob.Nonce, blob.Ciphertext)
		if err != nil {
			t.Fatalf("decrypt %s %s: %v", blob.RequestID, blob.Direction, err)
		}
		if plainByRecord[blob.RequestID] == nil {
			plainByRecord[blob.RequestID] = make(map[storage.AuditDirection][]byte)
		}
		plainByRecord[blob.RequestID][blob.Direction] = plain
	}

	recordsByAttempt := map[int]contract.RequestRecord{root.AttemptIndex: *root}
	for _, child := range children {
		recordsByAttempt[child.AttemptIndex] = child
	}
	for attempt, upstreamModel := range map[int]string{
		1: "upstream-a",
		2: "upstream-b",
		3: "upstream-c",
	} {
		record := recordsByAttempt[attempt]
		plain := plainByRecord[record.ID]
		requestBody := plain[storage.AuditDirectionUpstreamRequest]
		if len(requestBody) == 0 {
			t.Fatalf("attempt %d missing upstream request body", attempt)
		}
		var document map[string]any
		if err := json.Unmarshal(requestBody, &document); err != nil {
			t.Fatalf("attempt %d decode upstream request: %v", attempt, err)
		}
		if document["model"] != upstreamModel {
			t.Fatalf("attempt %d model=%v, want %s", attempt, document["model"], upstreamModel)
		}
		if len(plain[storage.AuditDirectionUpstreamHTTPMeta]) == 0 {
			t.Fatalf("attempt %d missing upstream HTTP metadata", attempt)
		}
		if attempt < 3 {
			if _, ok := plain[storage.AuditDirectionUpstreamResponse]; ok {
				t.Fatalf("attempt %d captured a response that never existed", attempt)
			}
			continue
		}
		const finalResponse = `{"id":"chat_ok","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
		if got := string(plain[storage.AuditDirectionUpstreamResponse]); got != finalResponse {
			t.Fatalf("final upstream response=%q, want %q", got, finalResponse)
		}
	}
}

func TestFailedRequestRecordsAttemptedServiceWithoutRoundTrip(t *testing.T) {
	endpointA := validEndpoint(contract.ProtocolOpenAIChat, false)
	endpointB := validEndpoint(contract.ProtocolOpenAIChat, false)
	endpointA.ID = "endpoint_attempt_a"
	endpointB.ID = "endpoint_attempt_b"
	tests := []struct {
		name        string
		candidates  []contract.Endpoint
		denied      string
		policyErr   error
		code        string
		wantService string
		wantTrips   int32
		// wantRejected lists the failed routed events left for providers that
		// were chosen but never called.
		wantRejected []string
	}{
		{name: "credential", candidates: []contract.Endpoint{endpointA}, denied: "endpoint_attempt_a", code: "credential_unavailable", wantService: "endpoint_attempt_a", wantRejected: []string{"endpoint_attempt_a · credential_unavailable"}},
		{name: "privacy", candidates: []contract.Endpoint{endpointA}, policyErr: errors.New("policy offline"), code: "privacy_policy_unavailable", wantService: "endpoint_attempt_a"},
		// The client sees A's network failure, so the root keeps A.
		{name: "network then credential", candidates: []contract.Endpoint{endpointA, endpointB}, denied: "endpoint_attempt_b", code: "upstream_unavailable", wantService: "endpoint_attempt_a", wantTrips: 1, wantRejected: []string{"endpoint_attempt_b · credential_unavailable"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := make([]endpoint.Resolved, 0, len(test.candidates))
			for _, candidate := range test.candidates {
				candidates = append(candidates, endpoint.Resolved{Endpoint: candidate})
			}
			var filter privacy.Filter
			if test.policyErr != nil {
				engine, err := privacy.New(
					privacy.PolicyProviderFunc(func(context.Context, privacy.Scope) (privacy.Policy, error) {
						return privacy.Policy{}, test.policyErr
					}),
					privacy.DetectorFunc(func(context.Context, privacy.DetectInput) ([]privacy.Finding, error) {
						return nil, nil
					}),
				)
				if err != nil {
					t.Fatal(err)
				}
				filter = engine
			}
			var trips atomic.Int32
			store := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{
				Resolver:       candidateResolver{candidates: candidates},
				RequestRecords: store,
				PrivacyFilter:  filter,
				Authorizer: authorizerFunc(func(_ context.Context, candidate contract.Endpoint) (http.Header, error) {
					if string(candidate.ID) == test.denied {
						return nil, errors.New("credential missing")
					}
					return http.Header{}, nil
				}),
				Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
					trips.Add(1)
					return nil, io.ErrUnexpectedEOF
				})),
			})
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(`{"model":"public-alias","messages":[{"role":"user","content":"hi"}]}`),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if trips.Load() != test.wantTrips {
				t.Fatalf("round trips=%d, want %d", trips.Load(), test.wantTrips)
			}
			var roots []contract.RequestRecord
			for _, record := range store.records {
				if record.ParentRequestID == nil {
					roots = append(roots, record)
				}
			}
			if len(roots) != 1 {
				t.Fatalf("roots=%#v", roots)
			}
			root := roots[0]
			if root.Status != contract.RequestStatusFailed || root.Error == nil || root.Error.Code != test.code {
				t.Fatalf("root status=%s error=%#v, want failed %s; body=%s", root.Status, root.Error, test.code, response.Body.String())
			}
			if root.ServiceID == nil || string(*root.ServiceID) != test.wantService {
				t.Fatalf("root service=%v, want %s", root.ServiceID, test.wantService)
			}
			if got := rejectedCandidates(root); strings.Join(got, "\n") != strings.Join(test.wantRejected, "\n") {
				t.Fatalf("rejected candidates=%q, want %q", got, test.wantRejected)
			}
		})
	}
}

func rejectedCandidates(record contract.RequestRecord) []string {
	var rejected []string
	for _, event := range record.Events {
		if event.Kind == contract.RequestEventRouted && event.Status == contract.RequestStatusFailed {
			rejected = append(rejected, event.Summary)
		}
	}
	return rejected
}
