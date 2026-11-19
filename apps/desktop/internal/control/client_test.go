package control

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimeouts(t *testing.T) {
	const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		method, path string
		want         time.Duration
	}{
		{"POST", RequestPurgePath, RequestPurgeTimeout},
		{"GET", RequestPurgePath, RequestListTimeout},
		{"DELETE", "/control/v1/requests/request_one", RequestTimeout},
		{"POST", "/control/v1/builtin-tools/web_search/test", 45 * time.Second},
		{"POST", "/control/v1/builtin-tools/image_generation/test", 195 * time.Second},
		{"PUT", "/control/v1/builtin-tools/web_search/credential", RequestTimeout},
		{"POST", "/control/v1/sync/run", 320 * time.Second},
		{"GET", "/control/v1/sync/status", RequestTimeout},
		{"PUT", "/control/v1/sync/config", RequestTimeout},
		{"POST", "/control/v1/services/service_test/test", 320 * time.Second},
		// Plugin hooks get room for Core's own deadline; reading and
		// writing a plugin stays short.
		{"POST", "/control/v1/plugins/reload", 20 * time.Second},
		{"POST", "/control/v1/plugins/powercontext/test", 20 * time.Second},
		{"POST", "/control/v1/plugins/powercontext/actions", 20 * time.Second},
		{"GET", "/control/v1/plugins/powercontext/status", 20 * time.Second},
		{"GET", "/control/v1/plugins/powercontext", RequestTimeout},
		{"PATCH", "/control/v1/plugins/powercontext", RequestTimeout},
		{"POST", "/control/v1/plugins/sources/preview", 200 * time.Second},
		{"POST", "/control/v1/plugins/sources/install", 75 * time.Second},
		{"POST", "/control/v1/plugins/hello/source/rollback", 75 * time.Second},
		{"POST", "/control/v1/plugins/hello/ui-call", 75 * time.Second},
		{"GET", "/control/v1/plugins/market", 75 * time.Second},
		{"GET", "/control/v1/plugins/market?index=https%3A%2F%2Fexample.com%2Findex.json", 75 * time.Second},
		{"GET", "/control/v1/plugins/sora/tasks?cursor=c1", 20 * time.Second},
		{"DELETE", "/control/v1/plugins/sora/tasks", 20 * time.Second},
		{"GET", "/control/v1/services/service_test/test", RequestTimeout},
		{"POST", "/control/v1/services/service_test/logout", RequestTimeout},
		{"POST", "/control/v1/pricing/refresh", 150 * time.Second},
		{"PATCH", "/control/v1/policies/policy_privacy_default", PrivacyMutationTimeout},
		{"DELETE", "/control/v1/privacy-models/model_" + id, PrivacyMutationTimeout},
		{"POST", PrivacyModelsPath + "/model_" + id + "/pause", PrivacyMutationTimeout},
		{"POST", PrivacyModelsPath + "/model_" + id + "/resume", PrivacyModelMetadataTimeout},
		{"POST", PolicyDryRunPath, PrivacyMutationTimeout},
		{"POST", PrivacyModelProbePath, PrivacyModelMetadataTimeout},
		{"POST", LocalPrivacyModelProbePath, PrivacyModelMetadataTimeout},
		{"POST", PrivacyModelsPath, PrivacyModelMetadataTimeout},
		{"GET", PrivacyModelCatalogPath, RequestTimeout},
		{"GET", PrivacyModelReleasesPath, PrivacyModelMetadataTimeout},
		{"GET", PrivacyModelReleasesPath + "?refresh=1", PrivacyModelMetadataTimeout},
		{"POST", ServiceModelProbesPath, ServiceModelProbeTimeout},
		{"POST", ServiceProxyProbesPath, ServiceModelProbeTimeout},
		{"POST", "/control/v1/services/service_http/probe-models", ServiceModelProbeTimeout},
		{"GET", "/control/v1/requests/req_" + id + "/audit", AuditContentTimeout},
		{"GET", "/control/v1/requests/req_" + id + "/audit/context", AuditContentTimeout},
		{"GET", "/control/v1/request-memory?no_project=true", RequestListTimeout},
		{"GET", "/control/v1/request-memory/entry?file=a.md", RequestListTimeout},
		{"GET", "/control/v1/request-sessions?limit=50", RequestListTimeout},
		{"GET", "/control/v1/requests?limit=200", RequestListTimeout},
		{"GET", "/control/v1/services/service_" + id + "/usage", SubscriptionUsageTimeout},
		{"POST", "/control/v1/services/service_" + id + "/usage/reset", SubscriptionUsageTimeout},
		{"POST", RawUnlockPath, ProofTimeout},
		{"POST", RawAccessPath + "/rawgrant_x/decision", ProofTimeout},
		{"GET", RawSealingPath, RequestTimeout},
	}
	for _, tt := range tests {
		if got := Timeout(tt.method, tt.path); got != tt.want {
			t.Errorf("Timeout(%s %s) = %s, want %s", tt.method, tt.path, got, tt.want)
		}
	}
}

func TestBodyLimitRaisesOnlyForAuditContentAndOneConversation(t *testing.T) {
	const id = "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for path, want := range map[string]int64{
		"/control/v1/requests/" + id + "/audit":                   MaxAuditContentBody,
		"/control/v1/requests/" + id + "/audit/context":           MaxAuditContentBody,
		"/control/v1/request-sessions/session_a":                  MaxSessionDetailBody,
		"/control/v1/requests":                                    MaxControlBody,
		"/control/v1/requests/" + id:                              MaxControlBody,
		"/control/v1/request-sessions":                            MaxControlBody,
		"/control/v1/request-sessions/":                           MaxControlBody,
		"/control/v1/request-sessions/session_a/channel-bindings": MaxControlBody,
		"/control/v1/audit-settings":                              MaxControlBody,
	} {
		if got := BodyLimit(path); got != want {
			t.Errorf("BodyLimit(%s) = %d, want %d", path, got, want)
		}
	}
}

func TestDoSendsTokenETagAndContentType(t *testing.T) {
	var got struct {
		method, uri, auth, ifMatch, contentType, body string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.method, got.uri, got.auth = r.Method, r.URL.RequestURI(), r.Header.Get("Authorization")
		got.ifMatch, got.contentType, got.body = r.Header.Get("If-Match"), r.Header.Get("Content-Type"), string(body)
		w.Header().Set("ETag", `"sha256:abc"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"service_a"}`))
	}))
	defer server.Close()
	client := NewClient()
	tests := []struct {
		req         Request
		contentType string
		body        string
	}{
		{Request{Method: "PATCH", Path: "/control/v1/services/service_a", Body: map[string]any{"name": "A"}, IfMatch: `"v1"`}, "application/merge-patch+json", `{"name":"A"}`},
		{Request{Method: "POST", Path: "/control/v1/services", Body: json.RawMessage(`{"kind":"openai"}`)}, "application/json", `{"kind":"openai"}`},
		{Request{Method: "GET", Path: "/control/v1/services?limit=200&cursor="}, "", ""},
	}
	for _, tt := range tests {
		response, err := client.Do(context.Background(), server.URL+"/", "token", tt.req)
		if err != nil {
			t.Fatal(err)
		}
		if got.method != tt.req.Method || got.uri != tt.req.Path || got.auth != "Bearer token" || got.ifMatch != tt.req.IfMatch ||
			got.contentType != tt.contentType || got.body != tt.body {
			t.Errorf("%s %s sent %+v", tt.req.Method, tt.req.Path, got)
		}
		if response.Status != 200 || response.ETag != `"sha256:abc"` || string(response.Body) != `{"id":"service_a"}` {
			t.Errorf("response %+v", response)
		}
	}
}

func TestDoRejectsOversizedBodies(t *testing.T) {
	for _, declared := range []bool{true, false} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !declared {
				w.Header().Set("Transfer-Encoding", "chunked")
				w.(http.Flusher).Flush()
			}
			w.Write(make([]byte, MaxControlBody+1))
		}))
		_, err := NewClient().Do(context.Background(), server.URL, "token", Request{Method: "GET", Path: "/control/v1/requests/request_one"})
		server.Close()
		if err == nil || err.Error() != "GET /control/v1/requests/request_one response is too large" {
			t.Errorf("declared=%v: %v", declared, err)
		}
	}
}

func TestDoRetriesTransportFailuresOnceExceptForUnrepeatableCalls(t *testing.T) {
	tests := []struct {
		method, path string
		wantCalls    int32
	}{
		{"GET", "/control/v1/services", 2},
		{"POST", RequestPurgePath, 1},
		{"POST", "/control/v1/builtin-tools/web_search/test", 1},
		{"POST", RawPasswordPath, 1},
		{"POST", RawAccessPath + "/rawgrant_x/decision", 1},
	}
	for _, tt := range tests {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			// Drop the connection without an answer.
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		}))
		_, err := NewClient().Do(context.Background(), server.URL, "token", Request{Method: tt.method, Path: tt.path, Body: map[string]any{}})
		server.Close()
		if err == nil || !IsTransportError(err) || !strings.HasPrefix(err.Error(), tt.method+" "+tt.path+" failed: ") {
			t.Errorf("%s %s: %v", tt.method, tt.path, err)
		}
		if got := calls.Load(); got != tt.wantCalls {
			t.Errorf("%s %s was sent %d times, want %d", tt.method, tt.path, got, tt.wantCalls)
		}
	}
}

func TestATimedOutPurgeIsStillRunning(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := NewClient().Do(ctx, server.URL, "token", Request{Method: "POST", Path: RequestPurgePath, Body: map[string]any{"scope": "all"}})
	if err == nil || err.Error() != RequestPurgeStillRunning {
		t.Fatalf("got %v", err)
	}
	_, err = NewClient().Do(ctx, server.URL, "token", Request{Method: "GET", Path: "/control/v1/requests"})
	if err == nil || !IsTransportError(err) {
		t.Fatalf("an ordinary timeout is a transport failure: %v", err)
	}
}

func TestDoNeverFollowsRedirects(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	response, err := NewClient().Do(context.Background(), server.URL, "token", Request{Method: "GET", Path: "/control/v1/services"})
	if err != nil || response.Status != http.StatusFound || followed.Load() {
		t.Fatalf("response %+v, %v, followed %v", response, err, followed.Load())
	}
}

func TestStatusErrorKeepsTheShapeTheFrontendReads(t *testing.T) {
	err := StatusError("POST", "/control/v1/services", 409, []byte(`{"error":{"code":"service_conflict"}}`))
	if err.Error() != `POST /control/v1/services returned 409 Conflict: {"error":{"code":"service_conflict"}}` {
		t.Fatalf("got %q", err)
	}
	long := StatusError("GET", "/x", 500, []byte(strings.Repeat("é", 600)))
	if !strings.HasSuffix(long.Error(), ": "+strings.Repeat("é", 512)) {
		t.Fatalf("the preview is not 512 characters: %q", long)
	}
	if got := StatusError("GET", "/x", 599, nil).Error(); got != "GET /x returned 599 <unknown status code>: " {
		t.Fatalf("got %q", got)
	}
}

type stubCaller struct {
	response Response
	err      error
}

func (s stubCaller) Call(context.Context, Request) (Response, error) { return s.response, s.err }

func TestSendTurnsARefusalIntoAStatusError(t *testing.T) {
	_, err := Send(context.Background(), stubCaller{response: Response{Status: 404, Body: []byte(`{"error":{"code":"not_found"}}`)}}, Request{Method: "GET", Path: "/control/v1/services/service_a"})
	if err == nil || err.Error() != `GET /control/v1/services/service_a returned 404 Not Found: {"error":{"code":"not_found"}}` {
		t.Fatalf("got %v", err)
	}
	if response, err := Send(context.Background(), stubCaller{response: Response{Status: 204}}, Request{}); err != nil || response.Status != 204 {
		t.Fatalf("got %+v, %v", response, err)
	}
}

func TestDoReusesConnectionsAcrossConcurrentBursts(t *testing.T) {
	var opened atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	client := NewClient()
	const burst = 6
	for round := 0; round < 3; round++ {
		var started, done sync.WaitGroup
		release := make(chan struct{})
		for i := 0; i < burst; i++ {
			started.Add(1)
			done.Add(1)
			go func() {
				defer done.Done()
				started.Done()
				<-release
				if _, err := client.Do(context.Background(), server.URL, "token", Request{Method: http.MethodGet, Path: "/control/v1/health"}); err != nil {
					t.Error(err)
				}
			}()
		}
		started.Wait()
		close(release)
		done.Wait()
	}
	if got := opened.Load(); got > burst {
		t.Fatalf("opened %d connections for bursts of %d; idle ones were not kept", got, burst)
	}
}
