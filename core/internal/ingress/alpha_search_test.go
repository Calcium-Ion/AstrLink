package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const alphaSearchBody = ` {
 "id":"search-session", "model":"gpt-6-astra", "stream":true,
 "reasoning":{"effort":"high"}, "input":[{"role":"user","content":"Find release notes"}],
 "commands":{"search_query":[{"q":"OpenAI news","recency":7,"domains":["openai.com"]}]},
 "settings":{"external_web_access":true}, "max_output_tokens":2500,
 "metadata":{"caller":"preserve"}, "future_field":{"nested":true}
} `

const alphaSearchResponse = ` {"output":"Search results", "encrypted_output":"opaque-ciphertext", "results":[{"type":"text_result","ref_id":"turn0search0","future":true}]} `

func alphaSearchCandidate(kind contract.ServiceKind) endpoint.Resolved {
	if kind == contract.ServiceKindCodexSubscription {
		return codexVersionCandidate("https://chatgpt.com/backend-api/codex")
	}
	return endpoint.Resolved{Service: contract.Service{
		ID: "service_newapi", Name: "New API", Kind: kind, Enabled: true,
		Models:       []string{"gpt-6-astra"},
		Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIAlphaSearch, Mode: contract.CapabilityModeNative}},
		HTTP: &contract.HTTPConnection{
			BaseURL: "https://gateway.example/prefix/v1", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer},
			CredentialRef: "local://service/service_newapi",
		},
	}}
}

func TestAlphaSearchPassthroughAndRecords(t *testing.T) {
	for _, kind := range []contract.ServiceKind{contract.ServiceKindCodexSubscription, contract.ServiceKindNewAPI} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(string(kind)+"/"+http.StatusText(status), func(t *testing.T) {
				candidate := alphaSearchCandidate(kind)
				upstreamBody := alphaSearchBody
				if kind == contract.ServiceKindCodexSubscription {
					upstreamBody = strings.Replace(alphaSearchBody, `"search-session"`, `"`+accountauth.ScopedSessionID(candidate.Service.ID, "search-session")+`"`, 1)
				}
				records := &memoryRequestRecordStore{}
				blobs := &memoryAuditBlobs{records: records}
				responseBody := alphaSearchResponse
				if status != http.StatusOK {
					responseBody = `{"error":{"code":"invalid_search","message":"search rejected"}}`
				}
				sent := 0
				handler := NewWithDependencies(Dependencies{
					Resolver:       candidateResolver{candidates: []endpoint.Resolved{candidate}},
					Authorizer:     endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
					RequestRecords: records, AuditBlobs: blobs,
					AuditSettings: &memoryAuditSettings{settings: contract.AuditSettings{
						RequestBodyEnabled: true, ResponseContentEnabled: true, HTTPMetaEnabled: true,
						RequestBodyMaxBytes: 4096, ResponseContentMaxBytes: 4096,
						MetadataRetentionDays: 30, ContentRetentionDays: 7,
					}},
					Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
						sent++
						wantURL, wantAuth := "https://gateway.example/prefix/v1/alpha/search?trace=a%2Fb", "Bearer plan-key"
						if kind == contract.ServiceKindCodexSubscription {
							wantURL, wantAuth = "https://chatgpt.com/backend-api/codex/alpha/search?trace=a%2Fb", "Bearer subscription-token"
							if request.Header.Get("originator") != "codex-tui" {
								t.Errorf("originator = %q", request.Header.Get("originator"))
							}
							if request.Header.Get("Session_id") != accountauth.ScopedSessionID(candidate.Service.ID, "client-session") {
								t.Errorf("Codex session was not scoped: %q", request.Header.Get("Session_id"))
							}
						}
						if request.Method != http.MethodPost || request.URL.String() != wantURL || request.Header.Get("Authorization") != wantAuth {
							t.Errorf("outgoing request = %s %s auth=%q", request.Method, request.URL, request.Header.Get("Authorization"))
						}
						if request.Header.Get("X-AstrLink-Debug") != "" || request.Header.Get("X-Api-Key") != "" {
							t.Error("local headers leaked upstream")
						}
						if request.Header.Get("Accept") != "application/json" {
							t.Error("search must not force SSE")
						}
						body, err := io.ReadAll(request.Body)
						if err != nil || string(body) != upstreamBody {
							t.Errorf("body changed beyond session isolation: %s, err=%v", body, err)
						}
						if len(records.records) != 1 || records.records[0].Status != contract.RequestStatusPending {
							t.Error("request was not persisted while pending")
						}
						response := jsonResponse(status, responseBody)
						response.Header.Set("X-Request-ID", "search-upstream")
						return response, nil
					})),
				})
				request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search?trace=a%2Fb", strings.NewReader(alphaSearchBody))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Accept", "application/json")
				request.Header.Set("X-Api-Key", "local-secret")
				request.Header.Set("X-AstrLink-Debug", "local-only")
				request.Header.Set("Session_id", "client-session")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != status || response.Body.String() != responseBody || sent != 1 {
					t.Fatalf("response = %d %s, sent=%d", response.Code, response.Body.String(), sent)
				}
				if response.Header().Get("X-Request-ID") != "search-upstream" {
					t.Error("upstream response header was not forwarded")
				}
				if len(records.records) != 1 {
					t.Fatalf("records = %#v", records.records)
				}
				record := records.records[0]
				wantStatus := contract.RequestStatusSucceeded
				if status != http.StatusOK {
					wantStatus = contract.RequestStatusFailed
				}
				if err := record.Validate(); err != nil {
					t.Fatalf("invalid record: %v", err)
				}
				if record.InputProtocol != contract.ProtocolOpenAIAlphaSearch || record.Status != wantStatus || record.Streaming ||
					record.ServiceID == nil || *record.ServiceID != candidate.Service.ID || record.Plan == nil || record.Plan.Type != contract.PlanTypeNative ||
					record.HTTPStatus == nil || *record.HTTPStatus != status || record.LatencyMs == nil || record.CompletedAt == nil ||
					record.SessionID == nil || record.PreviousResponseID == nil || *record.PreviousResponseID != "client-session" ||
					record.RequestedModel == nil || *record.RequestedModel != "gpt-6-astra" || record.ReasoningEffort == nil || *record.ReasoningEffort != "high" {
					t.Fatalf("incomplete record: %#v", record)
				}
				if record.Usage != nil || record.OutputResponseID != nil || record.FirstTokenMs != nil || record.FirstAnswerMs != nil {
					t.Fatal("invented generation metadata for search")
				}
				for _, event := range []contract.RequestEventKind{contract.RequestEventAccepted, contract.RequestEventRouted, contract.RequestEventUpstream, contract.RequestEventCompleted} {
					if !slices.ContainsFunc(record.Events, func(item contract.RequestEvent) bool { return item.Kind == event }) {
						t.Errorf("missing %s event", event)
					}
				}
				for direction, want := range map[storage.AuditDirection]string{
					storage.AuditDirectionRequest: alphaSearchBody, storage.AuditDirectionUpstreamRequest: upstreamBody,
					storage.AuditDirectionResponse: responseBody, storage.AuditDirectionUpstreamResponse: responseBody,
					storage.AuditDirectionHTTPMeta: "", storage.AuditDirectionUpstreamHTTPMeta: "",
				} {
					index := slices.IndexFunc(blobs.blobs, func(blob storage.AuditBlob) bool { return blob.Direction == direction })
					if index < 0 {
						t.Errorf("missing %s audit blob", direction)
						continue
					}
					blob := blobs.blobs[index]
					plain, err := storage.OpenAuditBlob(blobs.key, blob.Nonce, blob.Ciphertext)
					if err != nil || (want != "" && string(plain) != want) {
						t.Errorf("%s audit = %s, err=%v", direction, plain, err)
					}
					if want == "" {
						var meta contract.AuditHTTPMeta
						if json.Unmarshal(plain, &meta) != nil || meta.Validate() != nil || meta.Method != http.MethodPost ||
							!strings.Contains(meta.URL, "/alpha/search") || meta.ResponseStatus == nil || *meta.ResponseStatus != status {
							t.Errorf("incomplete %s HTTP metadata: %s", direction, plain)
						}
						name := "authorization"
						if direction == storage.AuditDirectionHTTPMeta {
							name = "x-api-key"
						}
						index := slices.IndexFunc(meta.RequestHeaders, func(h contract.AuditHeader) bool { return h.Name == name })
						if index < 0 || !meta.RequestHeaders[index].Redacted {
							t.Errorf("%s credential header was not captured and masked", direction)
						}
						for _, credential := range []string{"local-secret", "subscription-token", "plan-key"} {
							if strings.Contains(string(plain), credential) {
								t.Errorf("%s leaked a credential", direction)
							}
						}
					}
				}
			})
		}
	}
}

func TestAlphaSearchClassify(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(alphaSearchBody))
	request.Header.Set("Session_id", "session-search")
	classified, err := classify(request, 4096)
	if err != nil || classified.Protocol != contract.ProtocolOpenAIAlphaSearch || classified.Streaming || classified.Model != "gpt-6-astra" ||
		classified.Conversation.SessionCursor != "session-search" || classified.InputPreview != "OpenAI news" {
		t.Fatalf("classified = %#v, err=%v", classified, err)
	}
	body, _ := io.ReadAll(request.Body)
	if string(body) != alphaSearchBody {
		t.Fatal("metadata inspection changed the body")
	}
}

func TestAlphaSearchBoundaryFailuresAreRecorded(t *testing.T) {
	for _, test := range []struct {
		name, method, body, encoding string
		limit                        uint32
		status                       int
	}{
		{name: "method", method: http.MethodGet, status: http.StatusMethodNotAllowed},
		{name: "malformed", method: http.MethodPost, body: "{", status: http.StatusBadRequest},
		{name: "missing model", method: http.MethodPost, body: `{"commands":{}}`, status: http.StatusBadRequest},
		{name: "empty body", method: http.MethodPost, status: http.StatusBadRequest},
		{name: "blank model", method: http.MethodPost, body: `{"model":" "}`, status: http.StatusBadRequest},
		{name: "encoded", method: http.MethodPost, body: alphaSearchBody, encoding: "gzip", status: http.StatusUnsupportedMediaType},
		{name: "too large", method: http.MethodPost, body: `{"model":"m","input":"` + strings.Repeat("a", 1<<20) + `"}`, limit: 1, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			records := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{RequestRecords: records, MaxRequestBodyMiB: test.limit})
			request := httptest.NewRequest(test.method, "/v1/alpha/search", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Content-Encoding", test.encoding)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || len(records.records) != 1 || records.records[0].InputProtocol != contract.ProtocolOpenAIAlphaSearch || records.records[0].Status != contract.RequestStatusFailed {
				t.Fatalf("response=%d %s records=%#v", response.Code, response.Body.String(), records.records)
			}
		})
	}
}

func TestAlphaSearchFailoverRewritesOnlyModelAndRecordsAttempts(t *testing.T) {
	first, second := alphaSearchCandidate(contract.ServiceKindCodexSubscription), alphaSearchCandidate(contract.ServiceKindNewAPI)
	first.UpstreamModel, second.UpstreamModel = "gpt-first", "gpt-second"
	records := &memoryRequestRecordStore{}
	trips := 0
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{first, second}},
		RequestRecords: records,
		Authorizer:     endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			trips++
			model := "gpt-first"
			if trips == 2 {
				model = "gpt-second"
			}
			body, _ := io.ReadAll(request.Body)
			wantBody := strings.Replace(alphaSearchBody, `"gpt-6-astra"`, `"`+model+`"`, 1)
			if trips == 1 {
				wantBody = strings.Replace(wantBody, `"search-session"`, `"`+accountauth.ScopedSessionID(first.Service.ID, "search-session")+`"`, 1)
			}
			if string(body) != wantBody {
				t.Errorf("unexpected replay: %s", body)
			}
			if trips == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			return jsonResponse(http.StatusOK, alphaSearchResponse), nil
		})),
	})
	// A real server request has no GetBody; inspected JSON must remain replayable.
	request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", io.NopCloser(strings.NewReader(alphaSearchBody)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || trips != 2 || len(records.records) != 2 {
		t.Fatalf("response=%d %s trips=%d records=%#v", response.Code, response.Body.String(), trips, records.records)
	}
	var root, child *contract.RequestRecord
	for i := range records.records {
		if records.records[i].ParentRequestID == nil {
			root = &records.records[i]
		} else {
			child = &records.records[i]
		}
	}
	if root == nil || child == nil || root.Status != contract.RequestStatusSucceeded || child.Status != contract.RequestStatusFailed ||
		root.ChildCount != 1 || root.AttemptIndex != 2 || *child.ParentRequestID != root.ID || *root.ServiceID != second.Service.ID || *child.ServiceID != first.Service.ID {
		t.Fatalf("root=%#v child=%#v", root, child)
	}
}

func TestAlphaSearchPrivacyAndVisibleOutputRestoration(t *testing.T) {
	for _, action := range []privacy.Action{privacy.ActionBlock, privacy.ActionRedact} {
		t.Run(string(action), func(t *testing.T) {
			records := &memoryRequestRecordStore{}
			blobs := &memoryAuditBlobs{records: records}
			var upstreamResponse string
			sent := false
			handler := NewWithDependencies(Dependencies{
				Resolver:       candidateResolver{candidates: []endpoint.Resolved{alphaSearchCandidate(contract.ServiceKindNewAPI)}},
				Authorizer:     endpoint.NewServiceAuthorizer(codingPlanCredentials{}, nil),
				RequestRecords: records, AuditBlobs: blobs,
				AuditSettings: &memoryAuditSettings{settings: contract.AuditSettings{
					RequestBodyEnabled: true, ResponseContentEnabled: true, HTTPMetaEnabled: true,
					RequestBodyMaxBytes: 4096, ResponseContentMaxBytes: 4096,
					MetadataRetentionDays: 30, ContentRetentionDays: 7,
				}},
				PrivacyFilter: testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: action, ResponseRestore: true}, nil),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					sent = true
					body, _ := io.ReadAll(request.Body)
					var document struct {
						Commands struct {
							SearchQuery []struct {
								Q string `json:"q"`
							} `json:"search_query"`
						} `json:"commands"`
					}
					if err := json.Unmarshal(body, &document); err != nil || len(document.Commands.SearchQuery) != 1 {
						t.Fatalf("request = %s, err=%v", body, err)
					}
					query := document.Commands.SearchQuery[0].Q
					if query == "alice@example.com" {
						t.Fatal("search query was not redacted")
					}
					response, _ := json.Marshal(map[string]any{"output": query, "encrypted_output": query, "results": []any{map[string]any{"ref_id": query}}})
					upstreamResponse = string(response)
					return jsonResponse(http.StatusOK, upstreamResponse), nil
				})),
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(`{"id":"search-id","model":"gpt-6-astra","commands":{"search_query":[{"q":"alice@example.com"}]}}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if action == privacy.ActionBlock {
				if sent || len(records.records) != 1 || records.records[0].Status != contract.RequestStatusBlocked {
					t.Fatalf("block failed: %d %s", response.Code, response.Body.String())
				}
				return
			}
			var result struct {
				Output    string `json:"output"`
				Encrypted string `json:"encrypted_output"`
				Results   []struct {
					Ref string `json:"ref_id"`
				} `json:"results"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Output != "alice@example.com" ||
				result.Encrypted == "alice@example.com" || len(result.Results) != 1 || result.Results[0].Ref != result.Encrypted {
				t.Fatalf("restore changed opaque result fields: %d %s", response.Code, response.Body.String())
			}
			for direction, want := range map[storage.AuditDirection]string{
				storage.AuditDirectionUpstreamResponse: upstreamResponse,
				storage.AuditDirectionResponse:         response.Body.String(),
			} {
				index := slices.IndexFunc(blobs.blobs, func(blob storage.AuditBlob) bool { return blob.Direction == direction })
				if index < 0 {
					t.Fatalf("missing %s audit", direction)
				}
				blob := blobs.blobs[index]
				plain, err := storage.OpenAuditBlob(blobs.key, blob.Nonce, blob.Ciphertext)
				if err != nil || string(plain) != want {
					t.Errorf("%s audit mismatch: %s, %v", direction, plain, err)
				}
			}
		})
	}
}

func TestAlphaSearchSessionIDLinksSearchCalls(t *testing.T) {
	records := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver:   candidateResolver{candidates: []endpoint.Resolved{alphaSearchCandidate(contract.ServiceKindNewAPI)}},
		Authorizer: endpoint.NewServiceAuthorizer(codingPlanCredentials{}, nil), RequestRecords: records,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			_, _ = io.Copy(io.Discard, request.Body)
			return jsonResponse(http.StatusOK, alphaSearchResponse), nil
		})),
	})
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(alphaSearchBody))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request.WithContext(context.Background()))
		if response.Code != http.StatusOK {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	}
	if len(records.records) != 2 || records.records[0].SessionID == nil || records.records[1].SessionID == nil || *records.records[0].SessionID != *records.records[1].SessionID {
		t.Fatalf("search calls did not join session: %#v", records.records)
	}
}

func TestAlphaSearchSubscriptionSessionIsolation(t *testing.T) {
	for _, test := range []struct {
		name                string
		isolation, official bool
	}{
		{name: "isolated", isolation: true},
		{name: "disabled"},
		{name: "official passthrough", isolation: true, official: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, id := range []contract.ServiceID{"service_codex_a", "service_codex_b"} {
				upstream := newCapturedUpstream(t, http.StatusOK, "application/json", alphaSearchResponse)
				candidate := riskCodexCandidate(id, upstream.url)
				settings := contract.DefaultRoutingSettings()
				settings.SubscriptionSessionIsolation = test.isolation
				header := make(http.Header)
				if test.official {
					header.Set("User-Agent", "codex_cli_rs/0.156.0 (Mac OS; arm64)")
					header.Set("originator", "codex_cli_rs")
					header.Set("Session-Id", "search-session")
					if !accountauth.RecognizedCodexOfficialClient(header) {
						t.Fatal("fixture is not an official client")
					}
				}
				response := serveProtectedRequest(t, settings, nil, candidate, "/v1/alpha/search", alphaSearchBody, header)
				if response.Code != http.StatusOK {
					t.Fatalf("response=%d %s", response.Code, response.Body.String())
				}
				_, body := upstream.request(t)
				want := alphaSearchBody
				if test.isolation && !test.official {
					want = strings.Replace(want, `"search-session"`, `"`+accountauth.ScopedSessionID(id, "search-session")+`"`, 1)
				}
				if string(body) != want {
					t.Fatalf("unexpected search body: %s", body)
				}
			}
		})
	}
}
