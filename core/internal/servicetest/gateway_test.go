package servicetest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func gatewayStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func gatewayDependencies(t *testing.T, store *sqlite.Store) ingress.Dependencies {
	t.Helper()
	return ingress.Dependencies{
		RequestRecords: store, AuditSettings: store, AuditBlobs: store,
		RecordLogger: t.Logf,
	}
}

func onlyGatewayRecord(t *testing.T, store *sqlite.Store) contract.RequestRecord {
	t.Helper()
	page, err := store.ListRequestRecords(context.Background(), storage.RequestRecordListOptions{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("records=%+v err=%v", page, err)
	}
	record := page.Items[0]
	if record.ServiceID == nil || *record.ServiceID != "service_test" || record.LocalAccessTokenID != nil ||
		record.ChildCount != 0 || record.ParentRequestID != nil || record.CompletedAt == nil || record.RouteID != nil {
		t.Fatalf("record attribution/finalization=%+v", record)
	}
	return record
}

func gatewayPrivacy(t *testing.T, policy privacy.Policy) *privacy.Engine {
	t.Helper()
	filter, err := privacy.New(privacy.PolicyProviderFunc(func(_ context.Context, scope privacy.Scope) (privacy.Policy, error) {
		if scope.ServiceID != "service_test" || scope.AccessTokenID != "" {
			t.Errorf("privacy scope=%+v", scope)
		}
		return policy, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	return filter
}

func gatewayReply(text string, stream bool) *http.Response {
	quoted, _ := json.Marshal(text)
	raw := `{"choices":[{"message":{"content":` + string(quoted) + `}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	contentType := "application/json"
	if stream {
		// Split placeholders across SSE events, as real providers often do.
		a, _ := json.Marshal(text[:len(text)/2])
		b, _ := json.Marshal(text[len(text)/2:])
		raw = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":" + string(a) + "}}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":" + string(b) + "}}]}\n\n" +
			"data: [DONE]\n\n"
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(raw))}
}

func TestGatewayPrivacyAndRecords(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			name    string
			enabled bool
			action  privacy.Action
			restore bool
		}{
			{"disabled", false, privacy.ActionRedact, true},
			{"allow", true, privacy.ActionAllow, true},
			{"warn", true, privacy.ActionWarn, true},
			{"block", true, privacy.ActionBlock, true},
			{"restore", true, privacy.ActionRedact, true},
			{"no restore", true, privacy.ActionRedact, false},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				store := gatewayStore(t)
				deps := gatewayDependencies(t, store)
				deps.PrivacyFilter = gatewayPrivacy(t, privacy.Policy{Enabled: test.enabled, Mode: privacy.ModeRegex, Action: test.action, ResponseRestore: test.restore})
				calls, warnings := 0, 0
				deps.PolicyWarningReporter = ingress.PolicyWarningReporterFunc(func(contract.ProtocolID, contract.ServiceID, string) { warnings++ })
				redacted := test.enabled && test.action == privacy.ActionRedact
				placeholder := ""
				deps.Forwarder = transport.New(timingTransport(func(request *http.Request) (*http.Response, error) {
					calls++
					body, _ := io.ReadAll(request.Body)
					var payload struct{ Messages []struct{ Content string } }
					if err := json.Unmarshal(body, &payload); err != nil || len(payload.Messages) != 1 {
						return nil, fmt.Errorf("unexpected request: %s", body)
					}
					if redacted {
						placeholder = regexp.MustCompile(`<PRIVATE_EMAIL_[0-9a-f]{16}>`).FindString(payload.Messages[0].Content)
						if placeholder == "" || strings.Contains(string(body), "alice@example.com") {
							t.Errorf("unredacted upstream body=%s", body)
						}
					} else if payload.Messages[0].Content != "alice@example.com" {
						t.Errorf("unexpected prompt=%s", body)
					}
					for key := range request.Header {
						if strings.HasPrefix(strings.ToLower(key), "x-astrlink-") {
							t.Errorf("local header forwarded: %s", key)
						}
					}
					return gatewayReply(payload.Messages[0].Content, stream), nil
				}))
				result := NewWithDependencies(deps, nil).Test(context.Background(), timingService(), contract.ServiceTestRequest{
					Protocol: contract.ProtocolOpenAIChat, Model: "unlisted-model", Prompt: "alice@example.com", Stream: stream,
				})
				record := onlyGatewayRecord(t, store)
				if record.RequestedModel == nil || *record.RequestedModel != "unlisted-model" || record.Streaming != stream {
					t.Fatalf("wrong model/protocol=%+v", record)
				}
				if test.action == privacy.ActionBlock {
					if calls != 0 || result.OK || result.StatusCode != 0 || result.ErrorCode != "policy_blocked" ||
						result.ResponseHeadersMS != nil || result.FirstTokenMS != nil || record.AttemptIndex != 0 ||
						record.Status != contract.RequestStatusBlocked || record.HTTPStatus == nil || *record.HTTPStatus != 403 ||
						!strings.Contains(result.RawResponse, "policy_blocked") {
						t.Fatalf("blocked result=%+v record=%+v calls=%d", result, record, calls)
					}
					return
				}
				want := "alice@example.com"
				if redacted && !test.restore {
					want = placeholder
				}
				decoded, err := decodeResponse(strings.NewReader(result.RawResponse), result.Protocol, stream, result.ResponseContentType, nil)
				if !result.OK || result.Output != want || decoded != want || err != nil || calls != 1 ||
					record.Status != contract.RequestStatusSucceeded || record.AttemptIndex != 1 ||
					result.ResponseHeadersMS == nil || (result.FirstTokenMS != nil) != stream {
					t.Fatalf("result=%+v rawText=%q err=%v record=%+v calls=%d", result, decoded, err, record, calls)
				}
				if test.action == privacy.ActionWarn && warnings != 1 {
					t.Fatalf("warnings=%d", warnings)
				}
				if redacted && (record.PrivacyRestore == nil || record.PrivacyRestore.MappingCount != 1 || record.PrivacyRestore.Enabled != test.restore) {
					t.Fatalf("privacy record=%+v", record.PrivacyRestore)
				}
				if !stream && (record.Usage == nil || record.Usage.TotalTokens != 5) {
					t.Fatalf("usage=%+v", record.Usage)
				}
			})
		}
	}
}

func TestGatewayAuditUsesExistingCaptureSettings(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprint(capture), func(t *testing.T) {
			store := gatewayStore(t)
			settings := contract.DefaultAuditSettings()
			settings.RequestBodyEnabled, settings.ResponseContentEnabled = capture, capture
			if err := store.UpdateAuditSettings(context.Background(), settings); err != nil {
				t.Fatal(err)
			}
			deps := gatewayDependencies(t, store)
			deps.PrivacyFilter = gatewayPrivacy(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true})
			deps.Authorizer = authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
				return http.Header{"Authorization": {"Bearer test-secret-value"}}, nil
			})
			deps.Forwarder = transport.New(timingTransport(func(request *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(request.Body)
				var payload struct{ Messages []struct{ Content string } }
				_ = json.Unmarshal(body, &payload)
				return gatewayReply(payload.Messages[0].Content, false), nil
			}))
			result := NewWithDependencies(deps, nil).Test(context.Background(), timingService(), contract.ServiceTestRequest{
				Protocol: contract.ProtocolOpenAIChat, Model: "test", Prompt: "alice@example.com",
			})
			if !result.OK {
				t.Fatalf("result=%+v", result)
			}
			record := onlyGatewayRecord(t, store)
			blobs, err := store.GetAuditBlobsByRequest(context.Background(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			key, err := store.GetOrCreateAuditKey(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			plain := map[storage.AuditDirection]string{}
			for _, blob := range blobs {
				if bytes.Contains(blob.Ciphertext, []byte("alice@example.com")) {
					t.Fatal("plaintext audit storage")
				}
				body, err := storage.OpenAuditBlob(key, blob.Nonce, blob.Ciphertext)
				if err != nil {
					t.Fatal(err)
				}
				plain[blob.Direction] = string(body)
			}
			if len(plain) != 2 && !capture || len(plain) != 6 && capture {
				t.Fatalf("capture=%t audit directions=%v", capture, plain)
			}
			if strings.Contains(plain[storage.AuditDirectionUpstreamHTTPMeta], "test-secret-value") {
				t.Fatal("credential leaked to metadata")
			}
			if capture && (!strings.Contains(plain[storage.AuditDirectionRequest], "alice@example.com") ||
				strings.Contains(plain[storage.AuditDirectionUpstreamRequest], "alice@example.com") ||
				!strings.Contains(plain[storage.AuditDirectionUpstreamResponse], "PRIVATE_EMAIL_") ||
				plain[storage.AuditDirectionResponse] != result.RawResponse) {
				t.Fatalf("audit sides differ from actual gateway bodies: %v", plain)
			}
		})
	}
}

type forbiddenRouting struct{ t *testing.T }

func (r forbiddenRouting) Resolve(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
	r.t.Error("test consulted routing")
	return endpoint.Resolved{}, endpoint.ErrUnavailable
}
func (r forbiddenRouting) BeginAttempt(endpoint.Resolved) bool {
	r.t.Error("test consulted health")
	return false
}
func (r forbiddenRouting) RecordSuccess(endpoint.Resolved)  { r.t.Error("test changed health") }
func (r forbiddenRouting) RecordFailure(endpoint.Resolved)  { r.t.Error("test changed health") }
func (r forbiddenRouting) AbandonAttempt(endpoint.Resolved) { r.t.Error("test changed health") }
func (r forbiddenRouting) RecordRateLimit(endpoint.Resolved, time.Duration) {
	r.t.Error("test changed cooldown")
}

func TestGatewayNeverRetriesOrChangesRouting(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"rate limit", 429, `{"error":{"message":"quota"}}`},
		{"server error", 503, `{"error":{"message":"overloaded"}}`},
		{"network error", 0, ""},
		{"reasoning repair", 400, `{"error":{"message":"Item 'rs_123' of type 'reasoning' was provided without its required following item."}}`},
		{"function repair", 400, `{"error":{"message":"No tool output found for function call call_test."}}`},
		{"signature repair", 400, `{"error":{"message":"Invalid signature in thinking block"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := gatewayStore(t)
			deps := gatewayDependencies(t, store)
			deps.Resolver = forbiddenRouting{t}
			deps.AccessTokenAuthenticator = ingress.AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
				t.Error("internal test demanded an inference token")
				return "", errors.New("unexpected authentication")
			})
			calls := 0
			deps.Forwarder = transport.New(timingTransport(func(*http.Request) (*http.Response, error) {
				calls++
				if test.status == 0 {
					return nil, errors.New("connection reset")
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			}))
			service := timingService()
			service.Models = []string{"another-model"}
			policy := contract.DefaultFailurePolicy()
			policy.MaxRetries = 5
			enabled := true
			policy.OpenAIFunctionOutputRecovery = &enabled
			service.FailurePolicy = &policy
			if _, err := store.CreateService(context.Background(), service, storage.CredentialMutation{}); err != nil {
				t.Fatal(err)
			}
			result := NewWithDependencies(deps, nil).Test(context.Background(), service, contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "gpt-5"})
			record := onlyGatewayRecord(t, store)
			if calls != 1 || result.OK || result.StatusCode != test.status || record.AttemptIndex != 1 || record.Status != contract.RequestStatusFailed {
				t.Fatalf("calls=%d result=%+v record=%+v", calls, result, record)
			}
			saved, err := store.GetService(context.Background(), service.ID)
			if err != nil || saved.Service.Enabled || saved.Service.FailurePolicy.MaxRetries != 5 || saved.Service.Models[0] != "another-model" {
				t.Fatalf("test mutated configuration=%+v err=%v", saved, err)
			}
		})
	}
}

func TestGatewayPrivacyFailureAndCancellationAreRecorded(t *testing.T) {
	for _, test := range []struct {
		name     string
		cancel   bool
		upstream bool
	}{
		{name: "policy unavailable"},
		{name: "cancel privacy", cancel: true},
		{name: "cancel upstream", cancel: true, upstream: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := gatewayStore(t)
			deps := gatewayDependencies(t, store)
			started := make(chan struct{})
			filter, err := privacy.New(privacy.PolicyProviderFunc(func(ctx context.Context, _ privacy.Scope) (privacy.Policy, error) {
				if test.upstream {
					return privacy.Policy{}, nil
				}
				if test.cancel {
					close(started)
					<-ctx.Done()
					return privacy.Policy{}, ctx.Err()
				}
				return privacy.Policy{}, errors.New("policy storage unavailable")
			}), nil)
			if err != nil {
				t.Fatal(err)
			}
			deps.PrivacyFilter = filter
			calls := 0
			deps.Forwarder = transport.New(timingTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				close(started)
				<-request.Context().Done()
				return nil, request.Context().Err()
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancel {
				go func() { <-started; cancel() }()
			}
			result := NewWithDependencies(deps, nil).Test(ctx, timingService(), contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test"})
			record := onlyGatewayRecord(t, store)
			wantCode, wantStatus, wantCalls := "privacy_policy_unavailable", contract.RequestStatusFailed, 0
			if test.cancel {
				wantCode, wantStatus = "timeout", contract.RequestStatusCancelled
			}
			if test.upstream {
				wantCalls = 1
			}
			if result.OK || result.ErrorCode != wantCode || result.StatusCode != 0 || result.ResponseHeadersMS != nil ||
				result.FirstTokenMS != nil || calls != wantCalls || record.Status != wantStatus || record.AttemptIndex != wantCalls {
				t.Fatalf("result=%+v record=%+v calls=%d", result, record, calls)
			}
		})
	}
}

func TestGatewayConcurrentTestsKeepPoliciesAndRecordsIsolated(t *testing.T) {
	store := gatewayStore(t)
	deps := gatewayDependencies(t, store)
	deps.PrivacyFilter = gatewayPrivacy(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true})
	deps.Forwarder = transport.New(timingTransport(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		var payload struct{ Messages []struct{ Content string } }
		_ = json.Unmarshal(body, &payload)
		return gatewayReply(payload.Messages[0].Content, true), nil
	}))
	tester := NewWithDependencies(deps, nil)
	var wait sync.WaitGroup
	for i := range 6 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			prompt := fmt.Sprintf("user%d@example.com", i)
			result := tester.Test(context.Background(), timingService(), contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: fmt.Sprintf("model-%d", i), Prompt: prompt, Stream: true})
			if !result.OK || result.Output != prompt {
				t.Errorf("isolated result=%+v", result)
			}
		}()
	}
	wait.Wait()
	page, err := store.ListRequestRecords(context.Background(), storage.RequestRecordListOptions{})
	if err != nil || len(page.Items) != 6 {
		t.Fatalf("records=%+v err=%v", page, err)
	}
	for _, record := range page.Items {
		if record.Status != contract.RequestStatusSucceeded || record.AttemptIndex != 1 || record.ChildCount != 0 || record.CompletedAt == nil {
			t.Fatalf("batch record=%+v", record)
		}
	}
}

type countedGatewayBody struct {
	io.ReadCloser
	read   int
	closed bool
}

func (body *countedGatewayBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	body.read += n
	return n, err
}

func (body *countedGatewayBody) Close() error { body.closed = true; return body.ReadCloser.Close() }

func TestGatewayBoundsUpstreamBeforePrivacyRestoration(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			deps := ingress.Dependencies{PrivacyFilter: gatewayPrivacy(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true})}
			body := &countedGatewayBody{}
			deps.Forwarder = transport.New(timingTransport(func(request *http.Request) (*http.Response, error) {
				_, _ = io.Copy(io.Discard, request.Body)
				response := gatewayReply(strings.Repeat("x", 2*maxResponseBytes), stream)
				body.ReadCloser = response.Body
				response.Body = body
				return response, nil
			}))
			result := NewWithDependencies(deps, nil).Test(context.Background(), timingService(), contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test", Prompt: "alice@example.com", Stream: stream})
			if result.OK || result.ErrorCode != "response_too_large" || !result.RawResponseTruncated || body.read > maxResponseBytes+1 || !body.closed {
				t.Fatalf("result=%+v consumed=%d closed=%t", result, body.read, body.closed)
			}
		})
	}
}

func TestGatewayUsesSavedInstanceProxy(t *testing.T) {
	store := gatewayStore(t)
	calls := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.String() != "http://provider.invalid/v1/chat/completions" ||
			request.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-password")) {
			t.Error("missing saved proxy or authentication")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer proxy.Close()
	service := timingService()
	service.HTTP.BaseURL = "http://provider.invalid/v1"
	service.Proxy = &contract.ServiceProxy{Mode: "custom", URL: proxy.URL, CredentialRef: "local://service-proxy/service_test"}
	if _, err := store.CreateService(context.Background(), service, storage.CredentialMutation{ProxyPresent: true, Proxy: &contract.ProxyCredential{Username: "proxy-user", Password: "proxy-password"}}); err != nil {
		t.Fatal(err)
	}
	deps := gatewayDependencies(t, store)
	deps.ProxyCredentials = store
	result := NewWithDependencies(deps, nil).Test(context.Background(), service, contract.ServiceTestRequest{Protocol: contract.ProtocolOpenAIChat, Model: "test"})
	if !result.OK || result.Output != "OK" || calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
	_ = onlyGatewayRecord(t, store)
}
