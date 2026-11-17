package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCore is Core's control listener: it records each request and answers
// with the reply set for the test.
type fakeCore struct {
	t      *testing.T
	server *httptest.Server
	client *Client

	mu       sync.Mutex
	requests []seenRequest
	reply    func(r *http.Request) (status int, etag, body string)
}

type seenRequest struct {
	method, path, ifMatch, contentType, auth, body string
}

func newFakeCore(t *testing.T) *fakeCore {
	f := &fakeCore{t: t, client: NewClient()}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, seenRequest{
			method:      r.Method,
			path:        r.URL.RequestURI(),
			ifMatch:     r.Header.Get("If-Match"),
			contentType: r.Header.Get("Content-Type"),
			auth:        r.Header.Get("Authorization"),
			body:        string(body),
		})
		reply := f.reply
		f.mu.Unlock()
		status, etag, answer := http.StatusOK, "", "{}"
		if reply != nil {
			status, etag, answer = reply(r)
		}
		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// Call sends req to the fake listener through the real control client, as
// core.Manager does.
func (f *fakeCore) Call(ctx context.Context, req Request) (Response, error) {
	return f.client.Do(ctx, f.server.URL, "operator-token", req)
}

func (f *fakeCore) answer(status int, etag, body string) {
	f.mu.Lock()
	f.reply = func(*http.Request) (int, string, string) { return status, etag, body }
	f.requests = nil
	f.mu.Unlock()
}

func (f *fakeCore) seen() []seenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seenRequest(nil), f.requests...)
}

const strongTag = `"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`

const browserSessionJSON = `{
	"id": "authorization_01",
	"provider": "openai_codex",
	"status": "pending",
	"service_id": "service_a",
	"flow": "browser",
	"authorization_url": "https://auth.openai.com/oauth/authorize",
	"expires_at": "2026-07-28T08:15:00Z",
	"created_at": "2026-07-28T08:00:00Z",
	"updated_at": "2026-07-28T08:00:00Z"
}`

const installationID = "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

const webSearchTool = `{"enabled":true,"backend":"upstream","service_id":"service_a","model":"gpt-5"}`

// bridgeCase is one forwarded command: what the frontend passes, the
// request Core must get, the timeout and size class of that request, and a
// reply Core gives.
type bridgeCase struct {
	name   string
	call   func(ctx context.Context) (any, error)
	method string
	path   string
	// body is the JSON request body, "" for none.
	body    string
	ifMatch string
	timeout time.Duration
	limit   int64
	// reply is Core's 2xx answer.
	status int
	etag   string
	reply  string
	// skipsBody marks commands that never read Core's answer.
	skipsBody bool
}

func raw(literal string) json.RawMessage { return json.RawMessage(literal) }

// localModel is a local model probe for a path absolute on this platform.
var localModel = func() string {
	path := "/models/privacy"
	if runtime.GOOS == "windows" {
		path = `C:\models\privacy`
	}
	body, _ := json.Marshal(map[string]string{"path": path})
	return string(body)
}()

func ptr[T any](v T) *T { return &v }

type testRegistry struct {
	mu        sync.Mutex
	cancels   map[string]func()
	cancelled chan string
}

func (r *testRegistry) RegisterServiceTest(owner, serviceID, testID string, cancel func()) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := owner + "/" + serviceID + "/" + testID
	if _, ok := r.cancels[key]; ok {
		return nil, errors.New(ServiceTestAlreadyRunning)
	}
	if r.cancels == nil {
		r.cancels = map[string]func(){}
	}
	r.cancels[key] = cancel
	return func() {
		r.mu.Lock()
		delete(r.cancels, key)
		r.mu.Unlock()
	}, nil
}

func (r *testRegistry) CancelServiceTest(owner, serviceID, testID string) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[owner+"/"+serviceID+"/"+testID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

type bridge struct {
	core     *fakeCore
	services Services
	pricing  Pricing
	sync     Sync
	records  Records
	routing  Routing
	audit    Audit
	tokens   Tokens
	usage    Usage
	privacy  Privacy
	tests    *testRegistry
	opened   []string
	copied   []string
	changed  map[string]int
}

func newBridge(t *testing.T) *bridge {
	b := &bridge{core: newFakeCore(t), tests: &testRegistry{}, changed: map[string]int{}}
	api := &API{Caller: b.core}
	b.services = Services{API: api, Tests: b.tests, Owner: func(context.Context) string { return "main" },
		OpenURL: func(url string) error { b.opened = append(b.opened, url); return nil }}
	b.pricing = Pricing{api}
	b.sync = Sync{api}
	b.records = Records{API: api, Changed: func() { b.changed["records"]++ }}
	b.routing = Routing{api}
	b.audit = Audit{api}
	b.tokens = Tokens{API: api, WriteClipboard: func(text string) error { b.copied = append(b.copied, text); return nil }}
	b.usage = Usage{api}
	b.privacy = Privacy{API: api,
		PolicyChanged: func() { b.changed["policy"]++ },
		ReleasesRead:  func() { b.changed["releases"]++ }}
	return b
}

func (b *bridge) cases() []bridgeCase {
	s, p, sy, r, ro, a, tk, u, pr := b.services, b.pricing, b.sync, b.records, b.routing, b.audit, b.tokens, b.usage, b.privacy
	get, post, put, patch, del := http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete
	const short, list, usage = RequestTimeout, RequestListTimeout, SubscriptionUsageTimeout
	const normal, audit, conversation = MaxControlBody, MaxAuditContentBody, MaxSessionDetailBody
	service := `{"id":"service_a","name":"A"}`
	routing := `{"default_failure_policy":` + failurePolicyJSON + `,"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}`
	catalog := catalogJSON(privacyVariantJSON, `"0.2.0"`, "true")
	probe := `{"repo_id":"example/privacy-filter","requested_revision":"main","revision":"53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088","name":"Example Privacy","license":"apache-2.0","languages":["en"],"adapter":"hf_token_classification","variants":[` + privacyVariantJSON + `],"labels":[{"label":"EMAIL","suggested_kind":"email"},{"label":"MISC","suggested_kind":null}],"requires_label_mapping":true}`
	install := `{"repo_id":"example/privacy-filter","revision":"53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088","variant_id":"cpu_int8","label_mapping":{"EMAIL":"email","MISC":null}}`
	return []bridgeCase{
		{name: "list_services", call: func(ctx context.Context) (any, error) { return s.ListServices(ctx) },
			method: get, path: "/control/v1/services?limit=200&cursor=", timeout: short, limit: normal,
			reply: `{"items":[` + service + `],"next_cursor":null}`},
		{name: "get_service_order", call: func(ctx context.Context) (any, error) { return s.GetServiceOrder(ctx) },
			method: get, path: "/control/v1/service-order", timeout: short, limit: normal,
			etag: strongTag, reply: `{"service_ids":["service_a"]}`},
		{name: "update_service_order", call: func(ctx context.Context) (any, error) {
			return s.UpdateServiceOrder(ctx, []string{"service_b", "service_a"}, strongTag)
		}, method: put, path: "/control/v1/service-order", body: `{"service_ids":["service_b","service_a"]}`, ifMatch: strongTag,
			timeout: short, limit: normal, etag: strongTag, reply: `{"service_ids":["service_b","service_a"]}`},
		{name: "get_service_packages", call: func(ctx context.Context) (any, error) { return s.GetServicePackages(ctx) },
			method: get, path: "/control/v1/service-packages", timeout: short, limit: normal, reply: `{"packages":[]}`},
		{name: "update_service_package", call: func(ctx context.Context) (any, error) {
			return s.UpdateServicePackage(ctx, "subscription:claude:max 20x", "most_remaining")
		}, method: patch, path: "/control/v1/service-packages/subscription%3Aclaude%3Amax%2020x", body: `{"strategy":"most_remaining"}`,
			timeout: short, limit: normal, reply: `{"id":"subscription:claude:max 20x"}`},
		{name: "get_service", call: func(ctx context.Context) (any, error) { return s.GetService(ctx, "service_a") },
			method: get, path: "/control/v1/services/service_a", timeout: short, limit: normal, etag: strongTag, reply: service},
		{name: "create_service", call: func(ctx context.Context) (any, error) {
			return s.CreateService(ctx, raw(`{"name":"A","proxy":{"mode":"direct"}}`))
		}, method: post, path: "/control/v1/services", body: `{"name":"A","proxy":{"mode":"direct"}}`,
			timeout: short, limit: normal, status: http.StatusCreated, etag: strongTag, reply: service},
		{name: "update_service", call: func(ctx context.Context) (any, error) {
			return s.UpdateService(ctx, "service_a", `"3"`, raw(`{"name":"B","failure_policy":null}`))
		}, method: patch, path: "/control/v1/services/service_a", body: `{"name":"B","failure_policy":null}`, ifMatch: `"3"`,
			timeout: short, limit: normal, etag: strongTag, reply: service},
		{name: "delete_service", call: func(ctx context.Context) (any, error) {
			return nil, s.DeleteService(ctx, "service_a", strongTag)
		}, method: del, path: "/control/v1/services/service_a", ifMatch: strongTag, timeout: short, limit: normal,
			status: http.StatusNoContent, skipsBody: true},
		{name: "get_service_usage", call: func(ctx context.Context) (any, error) {
			return s.GetServiceUsage(ctx, "service_a", ptr(true))
		}, method: get, path: "/control/v1/services/service_a/usage?refresh=1", timeout: usage, limit: normal, reply: `{"windows":[]}`},
		{name: "get_service_reset_credits", call: func(ctx context.Context) (any, error) {
			return s.GetServiceResetCredits(ctx, "service_a")
		}, method: get, path: "/control/v1/services/service_a/usage/reset-credits", timeout: short, limit: normal, reply: `{"credits":1}`},
		{name: "reset_service_usage", call: func(ctx context.Context) (any, error) { return s.ResetServiceUsage(ctx, "service_a") },
			method: post, path: "/control/v1/services/service_a/usage/reset", timeout: usage, limit: normal, reply: `{"ok":true}`},
		{name: "test_service", call: func(ctx context.Context) (any, error) {
			return s.TestService(ctx, "service_a", raw(`{"protocol":"openai.chat"}`), nil)
		}, method: post, path: "/control/v1/services/service_a/test", body: `{"protocol":"openai.chat"}`,
			timeout: 75 * time.Second, limit: normal, reply: `{"ok":true}`},
		{name: "probe_service_models", call: func(ctx context.Context) (any, error) {
			return s.ProbeServiceModels(ctx, "service_a", raw(`{"protocol":"openai.models"}`))
		}, method: post, path: "/control/v1/services/service_a/probe-models", body: `{"protocol":"openai.models"}`,
			timeout: ServiceModelProbeTimeout, limit: normal, reply: `{"models":[]}`},
		{name: "probe_draft_service_models", call: func(ctx context.Context) (any, error) {
			return s.ProbeDraftServiceModels(ctx, raw(`{"base_url":"https://api.example","proxy":{"mode":"inherit"}}`))
		}, method: post, path: "/control/v1/service-model-probes", body: `{"base_url":"https://api.example","proxy":{"mode":"inherit"}}`,
			timeout: ServiceModelProbeTimeout, limit: normal, reply: `{"models":[]}`},
		{name: "probe_service_proxy", call: func(ctx context.Context) (any, error) {
			return s.ProbeServiceProxy(ctx, raw(`{"service_id":"service_a","proxy":{"mode":"custom","url":"socks5://127.0.0.1:1080"}}`))
		}, method: post, path: "/control/v1/service-proxy-probes", body: `{"service_id":"service_a","proxy":{"mode":"custom","url":"socks5://127.0.0.1:1080"}}`,
			timeout: ServiceModelProbeTimeout, limit: normal, reply: `{"ok":true}`},
		{name: "begin_service_authorization", call: func(ctx context.Context) (any, error) {
			return s.BeginServiceAuthorization(ctx, "service_a", "browser")
		}, method: post, path: "/control/v1/services/service_a/authorization", body: `{"flow":"browser"}`,
			timeout: short, limit: normal, status: http.StatusAccepted, reply: browserSessionJSON},
		{name: "get_service_authorization", call: func(ctx context.Context) (any, error) {
			return s.GetServiceAuthorization(ctx, "service_a")
		}, method: get, path: "/control/v1/services/service_a/authorization", timeout: short, limit: normal, reply: browserSessionJSON},
		{name: "complete_service_authorization", call: func(ctx context.Context) (any, error) {
			return s.CompleteServiceAuthorization(ctx, "service_a", "authorization_01", "code#state")
		}, method: put, path: "/control/v1/services/service_a/authorization", body: `{"session_id":"authorization_01","code":"code#state"}`,
			timeout: short, limit: normal, reply: browserSessionJSON},
		{name: "cancel_service_authorization", call: func(ctx context.Context) (any, error) {
			return s.CancelServiceAuthorization(ctx, "service_a")
		}, method: del, path: "/control/v1/services/service_a/authorization", timeout: short, limit: normal, reply: browserSessionJSON},
		{name: "get_claude_cli", call: func(ctx context.Context) (any, error) { return s.GetClaudeCLI(ctx) },
			method: get, path: "/control/v1/claude-cli", timeout: short, limit: normal, reply: `{"installed":false}`},
		{name: "logout_service", call: func(ctx context.Context) (any, error) { return s.LogoutService(ctx, "service_a") },
			method: post, path: "/control/v1/services/service_a/logout", timeout: short, limit: normal, etag: strongTag, reply: service},
		{name: "clear_service_risk", call: func(ctx context.Context) (any, error) { return s.ClearServiceRisk(ctx, "service_a") },
			method: post, path: "/control/v1/services/service_a/risk/clear", timeout: short, limit: normal, etag: strongTag, reply: service},
		{name: "list_service_risk_events", call: func(ctx context.Context) (any, error) {
			return s.ListServiceRiskEvents(ctx, "service_a", ptr(uint32(20)))
		}, method: get, path: "/control/v1/services/service_a/risk-events?limit=20", timeout: short, limit: normal, reply: `{"items":[]}`},

		{name: "pricing status", call: func(ctx context.Context) (any, error) { return p.Pricing(ctx, "status", nil, nil) },
			method: get, path: "/control/v1/pricing/status", timeout: short, limit: normal, reply: `{"ok":true}`},
		{name: "pricing model", call: func(ctx context.Context) (any, error) {
			return p.Pricing(ctx, "model", nil, raw(`{"model":"gpt/5 mini"}`))
		}, method: get, path: "/control/v1/model-metadata?model=gpt%2F5%20mini", timeout: short, limit: normal, reply: `{"model":"gpt-5-mini"}`},
		{name: "pricing sync", call: func(ctx context.Context) (any, error) { return p.Pricing(ctx, "sync", nil, raw("null")) },
			method: post, path: "/control/v1/pricing/sync", timeout: 150 * time.Second, limit: normal, reply: `{"ok":true}`},
		{name: "pricing configure", call: func(ctx context.Context) (any, error) {
			return p.Pricing(ctx, "configure", ptr("service_a"), raw(`{"mode":"official"}`))
		}, method: put, path: "/control/v1/pricing/services/service_a", body: `{"mode":"official"}`, timeout: short, limit: normal, reply: `{"ok":true}`},
		{name: "pricing backfill", call: func(ctx context.Context) (any, error) {
			return p.Pricing(ctx, "backfill", ptr("service_a"), nil)
		}, method: post, path: "/control/v1/pricing/services/service_a/backfill", timeout: 150 * time.Second, limit: normal, reply: `{"ok":true}`},
		{name: "pricing summary", call: func(ctx context.Context) (any, error) {
			return p.Pricing(ctx, "summary", nil, raw(`{"from":"2026-11-01T00:00:00+08:00","to":"2026-11-02T00:00:00+08:00"}`))
		}, method: get, path: "/control/v1/pricing/summary?from=2026-11-01T00%3A00%3A00%2B08%3A00&to=2026-11-02T00%3A00%3A00%2B08%3A00",
			timeout: short, limit: normal, reply: `{"total":0}`},
		{name: "sync_config run", call: func(ctx context.Context) (any, error) { return sy.SyncConfig(ctx, "run", nil) },
			method: post, path: "/control/v1/sync/run", timeout: 320 * time.Second, limit: normal, reply: `{"ok":true}`},
		{name: "sync_config passphrase", call: func(ctx context.Context) (any, error) {
			return sy.SyncConfig(ctx, "passphrase", raw(`{"passphrase":"twelve-chars"}`))
		}, method: put, path: "/control/v1/sync/passphrase", body: `{"passphrase":"twelve-chars"}`, timeout: short, limit: normal, reply: `{"ok":true}`},

		{name: "list_request_records", call: func(ctx context.Context) (any, error) {
			return r.ListRequestRecords(ctx, raw(`{"limit":50,"status":"succeeded"}`))
		}, method: get, path: "/control/v1/requests?limit=50&status=succeeded", timeout: list, limit: normal, reply: `{"items":[]}`},
		{name: "list_request_sessions", call: func(ctx context.Context) (any, error) {
			return r.ListRequestSessions(ctx, raw(`{"kind":"inference"}`))
		}, method: get, path: "/control/v1/request-sessions?kind=inference", timeout: list, limit: normal, reply: `{"items":[]}`},
		{name: "get_request_activity", call: func(ctx context.Context) (any, error) {
			return r.GetRequestActivity(ctx, "2026-11-13T00:00:00+08:00")
		}, method: get, path: "/control/v1/request-activity?since=2026-11-13T00%3A00%3A00%2B08%3A00", timeout: short, limit: normal, reply: `{"changed":false}`},
		{name: "get_project_memory", call: func(ctx context.Context) (any, error) {
			return r.GetProjectMemory(ctx, raw(`{"project":"~/proj"}`))
		}, method: get, path: "/control/v1/request-memory?project=~%2Fproj", timeout: list, limit: normal, reply: `{"entries":[]}`},
		{name: "get_project_memory_entry", call: func(ctx context.Context) (any, error) {
			return r.GetProjectMemoryEntry(ctx, raw(`{"project":"~/proj","file":"a.md"}`))
		}, method: get, path: "/control/v1/request-memory/entry?project=~%2Fproj&file=a.md", timeout: list, limit: normal, reply: `{"turns":[]}`},
		{name: "get_request_session", call: func(ctx context.Context) (any, error) { return r.GetRequestSession(ctx, "session_a") },
			method: get, path: "/control/v1/request-sessions/session_a", timeout: list, limit: conversation, reply: `{"id":"session_a"}`},
		{name: "get_session_channel_bindings", call: func(ctx context.Context) (any, error) {
			return r.GetSessionChannelBindings(ctx, "session_a", ptr(int64(7)))
		}, method: get, path: "/control/v1/request-sessions/session_a/channel-bindings?before=7", timeout: list, limit: normal, reply: `{"bindings":[]}`},
		{name: "release_session_channel_bindings", call: func(ctx context.Context) (any, error) {
			return r.ReleaseSessionChannelBindings(ctx, "session_a")
		}, method: del, path: "/control/v1/request-sessions/session_a/channel-bindings", timeout: short, limit: normal, reply: `{"released":1}`},
		{name: "get_request_record", call: func(ctx context.Context) (any, error) { return r.GetRequestRecord(ctx, "req_a") },
			method: get, path: "/control/v1/requests/req_a", timeout: list, limit: normal, reply: `{"id":"req_a"}`},
		{name: "list_request_record_children", call: func(ctx context.Context) (any, error) {
			return r.ListRequestRecordChildren(ctx, "req_a")
		}, method: get, path: "/control/v1/requests/req_a/children", timeout: list, limit: normal, reply: `{"items":[]}`},
		{name: "delete_request_record", call: func(ctx context.Context) (any, error) {
			return nil, r.DeleteRequestRecord(ctx, "req_a")
		}, method: del, path: "/control/v1/requests/req_a", timeout: short, limit: normal, status: http.StatusNoContent, skipsBody: true},
		{name: "purge_request_records", call: func(ctx context.Context) (any, error) {
			return r.PurgeRequestRecords(ctx, raw(`{"scope":"all","confirm":true}`))
		}, method: post, path: "/control/v1/requests/purge", body: `{"scope":"all","confirm":true}`, timeout: RequestPurgeTimeout, limit: normal, reply: `{"deleted":3}`},
		{name: "get_request_audit_content", call: func(ctx context.Context) (any, error) {
			return r.GetRequestAuditContent(ctx, "req_a")
		}, method: get, path: "/control/v1/requests/req_a/audit", timeout: AuditContentTimeout, limit: audit, reply: `{"parts":[]}`},
		{name: "get_request_context_texts", call: func(ctx context.Context) (any, error) {
			return r.GetRequestContextTexts(ctx, "req_a")
		}, method: get, path: "/control/v1/requests/req_a/audit/context", timeout: AuditContentTimeout, limit: audit, reply: `{"texts":[]}`},

		{name: "builtin_tool_action test", call: func(ctx context.Context) (any, error) {
			return ro.BuiltinToolAction(ctx, "web_search", "test", raw(webSearchTool))
		}, method: post, path: "/control/v1/builtin-tools/web_search/test", body: webSearchTool,
			timeout: 45 * time.Second, limit: normal, reply: `{"ok":true}`},
		{name: "builtin_tool_action status", call: func(ctx context.Context) (any, error) {
			return ro.BuiltinToolAction(ctx, "image_generation", "status", nil)
		}, method: get, path: "/control/v1/builtin-tools/image_generation/credential", timeout: short, limit: normal, reply: `{"configured":false}`},
		{name: "get_routing_settings", call: func(ctx context.Context) (any, error) { return ro.GetRoutingSettings(ctx) },
			method: get, path: "/control/v1/routing-settings", timeout: short, limit: normal, reply: routing},
		{name: "update_routing_settings", call: func(ctx context.Context) (any, error) {
			return ro.UpdateRoutingSettings(ctx, raw(`{"max_attempts":6}`))
		}, method: patch, path: "/control/v1/routing-settings", body: `{"max_attempts":6}`, timeout: short, limit: normal, reply: routing},
		{name: "get_client_identities", call: func(ctx context.Context) (any, error) { return ro.GetClientIdentities(ctx) },
			method: get, path: "/control/v1/client-identities", timeout: short, limit: normal, reply: `{"codex":[]}`},

		{name: "get_audit_settings", call: func(ctx context.Context) (any, error) { return a.GetAuditSettings(ctx) },
			method: get, path: "/control/v1/audit-settings", timeout: short, limit: normal, reply: `{"request_body_enabled":false}`},
		{name: "update_audit_settings", call: func(ctx context.Context) (any, error) {
			return a.UpdateAuditSettings(ctx, raw(`{"request_body_enabled":false}`))
		}, method: patch, path: "/control/v1/audit-settings", body: `{"request_body_enabled":false}`, timeout: short, limit: normal,
			reply: `{"request_body_enabled":false}`},
		{name: "local_data_status", call: func(ctx context.Context) (any, error) { return a.LocalDataStatus(ctx) },
			method: get, path: "/control/v1/local-data", timeout: short, limit: normal, reply: `{"undecryptable":0}`},

		{name: "list_access_tokens", call: func(ctx context.Context) (any, error) { return tk.ListAccessTokens(ctx) },
			method: get, path: "/control/v1/access-tokens", timeout: short, limit: normal, reply: `{"items":[]}`},
		{name: "list_network_addresses", call: func(ctx context.Context) (any, error) { return tk.ListNetworkAddresses(ctx) },
			method: get, path: "/control/v1/network-addresses", timeout: short, limit: normal, reply: `{"items":[]}`},
		{name: "create_access_token scoped", call: func(ctx context.Context) (any, error) {
			return tk.CreateAccessToken(ctx, "laptop", ptr("selected"), nil)
		}, method: post, path: "/control/v1/access-tokens", body: `{"name":"laptop","service_scope":"selected","service_ids":[]}`,
			timeout: short, limit: normal, status: http.StatusCreated, reply: `{"id":"token_a"}`},
		{name: "create_access_token", call: func(ctx context.Context) (any, error) {
			return tk.CreateAccessToken(ctx, "laptop", nil, []string{"service_a"})
		}, method: post, path: "/control/v1/access-tokens", body: `{"name":"laptop"}`,
			timeout: short, limit: normal, status: http.StatusCreated, reply: `{"id":"token_a"}`},
		{name: "update_access_token", call: func(ctx context.Context) (any, error) {
			return tk.UpdateAccessToken(ctx, "token_a", "desk", "all", nil)
		}, method: patch, path: "/control/v1/access-tokens/token_a", body: `{"name":"desk","service_scope":"all","service_ids":[]}`,
			timeout: short, limit: normal, reply: `{"id":"token_a"}`},
		{name: "delete_access_token", call: func(ctx context.Context) (any, error) {
			return nil, tk.DeleteAccessToken(ctx, "token_a")
		}, method: del, path: "/control/v1/access-tokens/token_a", timeout: short, limit: normal, status: http.StatusNoContent, skipsBody: true},
		{name: "copy_access_token", call: func(ctx context.Context) (any, error) { return tk.CopyAccessToken(ctx, "token_a") },
			method: get, path: "/control/v1/access-tokens/token_a/secret", timeout: short, limit: normal, reply: `{"access_token":"sk-local"}`},

		{name: "get_usage_summary", call: func(ctx context.Context) (any, error) {
			return u.GetUsageSummary(ctx, "2026-11-13T00:00:00+08:00", "2026-11-14T00:00:00+08:00", "Asia/Shanghai", "hour")
		}, method: get, path: "/control/v1/usage-summary?from=2026-11-13T00%3A00%3A00%2B08%3A00&to=2026-11-14T00%3A00%3A00%2B08%3A00&time_zone=Asia%2FShanghai&bucket=hour",
			timeout: short, limit: normal, reply: `{"buckets":[]}`},
		{name: "get_privacy_summary", call: func(ctx context.Context) (any, error) {
			return u.GetPrivacySummary(ctx, "a", "b", "UTC", "day")
		}, method: get, path: "/control/v1/privacy-summary?from=a&to=b&time_zone=UTC&bucket=day", timeout: short, limit: normal, reply: `{"buckets":[]}`},
		{name: "get_image_compression_totals", call: func(ctx context.Context) (any, error) {
			return u.GetImageCompressionTotals(ctx)
		}, method: get, path: "/control/v1/image-compression-totals", timeout: short, limit: normal, reply: `{"saved_bytes":0}`},
		{name: "list_access_token_usage", call: func(ctx context.Context) (any, error) {
			return u.ListAccessTokenUsage(ctx, "2026-11-13T00:00:00Z")
		}, method: get, path: "/control/v1/access-token-usage?today_from=2026-11-13T00%3A00%3A00Z", timeout: short, limit: normal, reply: `{"items":[]}`},

		{name: "list_privacy_policies", call: func(ctx context.Context) (any, error) { return pr.ListPrivacyPolicies(ctx) },
			method: get, path: "/control/v1/policies", timeout: short, limit: normal, reply: `{"items":[` + privacyPolicyJSON + `],"next_cursor":null}`},
		{name: "get_privacy_policy", call: func(ctx context.Context) (any, error) { return pr.GetPrivacyPolicy(ctx) },
			method: get, path: "/control/v1/policies/policy_privacy_default", timeout: short, limit: normal, etag: strongTag, reply: privacyPolicyJSON},
		{name: "update_privacy_policy", call: func(ctx context.Context) (any, error) {
			return pr.UpdatePrivacyPolicy(ctx, strongTag, raw(`{"enabled":true}`))
		}, method: patch, path: "/control/v1/policies/policy_privacy_default", body: `{"enabled":true}`, ifMatch: strongTag,
			timeout: PrivacyMutationTimeout, limit: normal, etag: strongTag, reply: privacyPolicyJSON},
		{name: "dry_run_privacy_policy", call: func(ctx context.Context) (any, error) {
			return pr.DryRunPrivacyPolicy(ctx, raw(`{"protocol":"openai.chat","sample_text":"hello"}`))
		}, method: post, path: "/control/v1/policies/policy_privacy_default/dry-run", body: `{"protocol":"openai.chat","sample_text":"hello"}`,
			timeout: PrivacyMutationTimeout, limit: normal, reply: dryRunConfidenceJSON},
		{name: "get_privacy_regex_builtin_rules", call: func(ctx context.Context) (any, error) { return pr.GetPrivacyRegexBuiltinRules(ctx) },
			method: get, path: "/control/v1/privacy/regex-builtin-rules", timeout: short, limit: normal, reply: `{"rules":[{"kind":"email","pattern":"x"}]}`},
		{name: "get_privacy_model_catalog", call: func(ctx context.Context) (any, error) { return pr.GetPrivacyModelCatalog(ctx) },
			method: get, path: "/control/v1/privacy-model-catalog", timeout: short, limit: normal, reply: catalog},
		{name: "get_privacy_model_releases", call: func(ctx context.Context) (any, error) { return pr.GetPrivacyModelReleases(ctx, ptr(true)) },
			method: get, path: "/control/v1/privacy-model-catalog/releases?refresh=1", timeout: PrivacyModelMetadataTimeout, limit: normal, reply: catalog},
		{name: "probe_privacy_model", call: func(ctx context.Context) (any, error) {
			return pr.ProbePrivacyModel(ctx, raw(`{"repo_id":"example/privacy-filter","revision":"main"}`))
		}, method: post, path: "/control/v1/privacy-models/probe", body: `{"repo_id":"example/privacy-filter","revision":"main"}`,
			timeout: PrivacyModelMetadataTimeout, limit: normal, reply: probe},
		{name: "probe_local_privacy_model", call: func(ctx context.Context) (any, error) {
			return pr.ProbeLocalPrivacyModel(ctx, raw(localModel))
		}, method: post, path: "/control/v1/privacy-models/local/probe", body: localModel,
			timeout: PrivacyModelMetadataTimeout, limit: normal, reply: probe},
		{name: "list_privacy_model_installations", call: func(ctx context.Context) (any, error) { return pr.ListPrivacyModelInstallations(ctx) },
			method: get, path: "/control/v1/privacy-models", timeout: short, limit: normal, reply: `{"items":[` + privacyInstallationJSON + `]}`},
		{name: "install_privacy_model", call: func(ctx context.Context) (any, error) { return pr.InstallPrivacyModel(ctx, raw(install)) },
			method: post, path: "/control/v1/privacy-models", body: install, timeout: PrivacyModelMetadataTimeout, limit: normal,
			status: http.StatusAccepted, reply: privacyInstallationJSON},
		{name: "get_privacy_model_installation", call: func(ctx context.Context) (any, error) {
			return pr.GetPrivacyModelInstallation(ctx, installationID)
		}, method: get, path: "/control/v1/privacy-models/" + installationID, timeout: short, limit: normal, reply: privacyInstallationJSON},
		{name: "pause_privacy_model_installation", call: func(ctx context.Context) (any, error) {
			return pr.PausePrivacyModelInstallation(ctx, installationID)
		}, method: post, path: "/control/v1/privacy-models/" + installationID + "/pause", timeout: PrivacyMutationTimeout, limit: normal, reply: privacyInstallationJSON},
		{name: "resume_privacy_model_installation", call: func(ctx context.Context) (any, error) {
			return pr.ResumePrivacyModelInstallation(ctx, installationID)
		}, method: post, path: "/control/v1/privacy-models/" + installationID + "/resume", timeout: PrivacyModelMetadataTimeout, limit: normal, reply: privacyInstallationJSON},
		{name: "delete_privacy_model_installation", call: func(ctx context.Context) (any, error) {
			return nil, pr.DeletePrivacyModelInstallation(ctx, installationID)
		}, method: del, path: "/control/v1/privacy-models/" + installationID, timeout: PrivacyMutationTimeout, limit: normal,
			status: http.StatusNoContent, skipsBody: true},
	}
}

func sameJSON(a, b string) bool {
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}

// TestForwardedCommandsSendTheirRequestAndConvertRefusals checks, for each
// forwarded command, the request Core gets with its timeout and size
// class, that a valid answer passes, that a refusal becomes the error the
// frontend reads its code from, and that an answer that is not JSON fails.
func TestForwardedCommandsSendTheirRequestAndConvertRefusals(t *testing.T) {
	b := newBridge(t)
	ctx := t.Context()
	for _, tc := range b.cases() {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.status
			if status == 0 {
				status = http.StatusOK
			}
			b.core.answer(status, tc.etag, tc.reply)
			if _, err := tc.call(ctx); err != nil {
				t.Fatalf("call: %v", err)
			}
			seen := b.core.seen()
			if len(seen) != 1 {
				t.Fatalf("requests = %+v", seen)
			}
			got := seen[0]
			if got.method != tc.method || got.path != tc.path {
				t.Errorf("request = %s %s, want %s %s", got.method, got.path, tc.method, tc.path)
			}
			if got.auth != "Bearer operator-token" {
				t.Errorf("authorization = %q", got.auth)
			}
			if got.ifMatch != tc.ifMatch {
				t.Errorf("If-Match = %q, want %q", got.ifMatch, tc.ifMatch)
			}
			if tc.body == "" && got.body != "" || tc.body != "" && !sameJSON(got.body, tc.body) {
				t.Errorf("body = %s, want %s", got.body, tc.body)
			}
			// Core takes a merge patch for PATCH and JSON otherwise.
			if want := "application/json"; tc.body != "" {
				if got.method == http.MethodPatch {
					want = "application/merge-patch+json"
				}
				if got.contentType != want {
					t.Errorf("Content-Type = %q, want %q", got.contentType, want)
				}
			}
			if timeout := Timeout(got.method, got.path); timeout != tc.timeout {
				t.Errorf("timeout = %v, want %v", timeout, tc.timeout)
			}
			if limit := BodyLimit(got.path); limit != tc.limit {
				t.Errorf("body limit = %d, want %d", limit, tc.limit)
			}

			refusal := `{"error":{"code":"conflict","message":"busy"}}`
			b.core.answer(http.StatusConflict, "", refusal)
			_, err := tc.call(ctx)
			want := fmt.Sprintf("%s %s returned 409 Conflict: %s", tc.method, tc.path, refusal)
			if err == nil || err.Error() != want {
				t.Errorf("refusal error = %v, want %s", err, want)
			}

			if !tc.skipsBody {
				b.core.answer(status, tc.etag, "not json")
				if _, err := tc.call(ctx); err == nil {
					t.Error("an answer that is not JSON was accepted")
				}
			}
		})
	}
}

// TestForwardedCommandsCheckArgumentsBeforeCallingCore covers arguments
// each command refuses without sending anything.
func TestForwardedCommandsCheckArgumentsBeforeCallingCore(t *testing.T) {
	b := newBridge(t)
	ctx := t.Context()
	s, p, sy, r, ro, a, tk, pr := b.services, b.pricing, b.sync, b.records, b.routing, b.audit, b.tokens, b.privacy
	for name, call := range map[string]func() error{
		"get_service traversal":     func() error { _, err := s.GetService(ctx, "../x"); return err },
		"update_service_order dup":  func() error { _, err := s.UpdateServiceOrder(ctx, []string{"a", "a"}, strongTag); return err },
		"update_service_order weak": func() error { _, err := s.UpdateServiceOrder(ctx, []string{"a"}, `W/"1"`); return err },
		"update_service_package":    func() error { _, err := s.UpdateServicePackage(ctx, "other:x:y", "most_remaining"); return err },
		"create_service proxy secret": func() error {
			_, err := s.CreateService(ctx, raw(`{"proxy":{"mode":"custom","url":"http://u:p@proxy"}}`))
			return err
		},
		"update_service policy": func() error {
			_, err := s.UpdateService(ctx, "service_a", strongTag, raw(`{"failure_policy":{}}`))
			return err
		},
		"delete_service etag":        func() error { return s.DeleteService(ctx, "service_a", "") },
		"probe_service_proxy direct": func() error { _, err := s.ProbeServiceProxy(ctx, raw(`{"proxy":{"mode":"direct"}}`)); return err },
		"probe_service_proxy id": func() error {
			_, err := s.ProbeServiceProxy(ctx, raw(`{"service_id":7,"proxy":{"mode":"custom","url":"http://proxy:1"}}`))
			return err
		},
		"begin_service_authorization": func() error { _, err := s.BeginServiceAuthorization(ctx, "service_a", "telepathy"); return err },
		"complete code":               func() error { _, err := s.CompleteServiceAuthorization(ctx, "service_a", "auth_a", ""); return err },
		"risk events limit":           func() error { _, err := s.ListServiceRiskEvents(ctx, "service_a", ptr(uint32(51))); return err },
		"pricing unknown":             func() error { _, err := p.Pricing(ctx, "refund", nil, nil); return err },
		"pricing model":               func() error { _, err := p.Pricing(ctx, "model", nil, raw(`{}`)); return err },
		"pricing report":              func() error { _, err := p.Pricing(ctx, "report", nil, nil); return err },
		"pricing configure":           func() error { _, err := p.Pricing(ctx, "configure", ptr("service_a"), raw("null")); return err },
		"pricing summary":             func() error { _, err := p.Pricing(ctx, "summary", nil, raw(`{"from":"a"}`)); return err },
		"sync configure":              func() error { _, err := sy.SyncConfig(ctx, "configure", raw("null")); return err },
		"records query":               func() error { _, err := r.ListRequestRecords(ctx, raw(`{"limit":0}`)); return err },
		"activity since":              func() error { _, err := r.GetRequestActivity(ctx, "now&x=1"); return err },
		"bindings cursor":             func() error { _, err := r.GetSessionChannelBindings(ctx, "session_a", ptr(int64(0))); return err },
		"purge confirm":               func() error { _, err := r.PurgeRequestRecords(ctx, raw(`{"scope":"all","confirm":false}`)); return err },
		"builtin tool":                func() error { _, err := ro.BuiltinToolAction(ctx, "calculator", "status", nil); return err },
		"builtin action":              func() error { _, err := ro.BuiltinToolAction(ctx, "web_search", "rotate", nil); return err },
		"builtin test input":          func() error { _, err := ro.BuiltinToolAction(ctx, "web_search", "test", nil); return err },
		"routing patch":               func() error { _, err := ro.UpdateRoutingSettings(ctx, raw(`{"max_attempts":21}`)); return err },
		"audit patch":                 func() error { _, err := a.UpdateAuditSettings(ctx, raw(`{"request_body_enabled":true}`)); return err },
		"token id": func() error {
			_, err := tk.UpdateAccessToken(ctx, "token_a", "x", "selected", []string{"bad/id"})
			return err
		},
		"privacy etag":  func() error { _, err := pr.UpdatePrivacyPolicy(ctx, `W/"1"`, raw(`{"enabled":true}`)); return err },
		"privacy patch": func() error { _, err := pr.UpdatePrivacyPolicy(ctx, strongTag, raw(`{}`)); return err },
		"dry run": func() error {
			_, err := pr.DryRunPrivacyPolicy(ctx, raw(`{"protocol":"grpc","sample_text":"x"}`))
			return err
		},
		"local probe path": func() error { _, err := pr.ProbeLocalPrivacyModel(ctx, raw(`{"path":"relative"}`)); return err },
		"installation id":  func() error { _, err := pr.GetPrivacyModelInstallation(ctx, "model_bad"); return err },
	} {
		b.core.answer(http.StatusOK, "", "{}")
		if err := call(); err == nil {
			t.Errorf("%s accepted", name)
		}
		if seen := b.core.seen(); len(seen) != 0 {
			t.Errorf("%s sent %+v", name, seen)
		}
	}
}

func TestListServicesReadsEveryPageAndStopsOnARepeatedCursor(t *testing.T) {
	b := newBridge(t)
	b.core.mu.Lock()
	b.core.reply = func(r *http.Request) (int, string, string) {
		switch r.URL.Query().Get("cursor") {
		case "":
			return http.StatusOK, "", `{"items":[{"id":"a"}],"next_cursor":"c+1"}`
		case "c+1":
			return http.StatusOK, "", `{"items":[{"id":"b"}],"next_cursor":null}`
		}
		return http.StatusOK, "", `{"items":[],"next_cursor":null}`
	}
	b.core.mu.Unlock()
	page, err := b.services.ListServices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(page)
	if !sameJSON(string(encoded), `{"items":[{"id":"a"},{"id":"b"}],"next_cursor":null}`) {
		t.Errorf("page = %s", encoded)
	}
	if seen := b.core.seen(); len(seen) != 2 || seen[1].path != "/control/v1/services?limit=200&cursor=c%2B1" {
		t.Errorf("requests = %+v", seen)
	}

	b.core.answer(http.StatusOK, "", `{"items":[],"next_cursor":"same"}`)
	if _, err := b.services.ListServices(t.Context()); err == nil || err.Error() != "service pagination did not advance" {
		t.Errorf("repeated cursor error = %v", err)
	}
	b.core.answer(http.StatusOK, "", `{"next_cursor":null}`)
	if _, err := b.services.ListServices(t.Context()); err == nil || err.Error() != "service list omitted items" {
		t.Errorf("missing items error = %v", err)
	}
	b.core.answer(http.StatusOK, "", `{"items":[]}`)
	page, err = b.services.ListServices(t.Context())
	if encoded, _ := json.Marshal(page); err != nil || string(encoded) != `{"items":[],"next_cursor":null}` {
		t.Errorf("empty page = %s, %v", encoded, err)
	}
}

func TestBeginServiceAuthorizationOpensThePageCoreNames(t *testing.T) {
	b := newBridge(t)
	b.core.answer(http.StatusAccepted, "", browserSessionJSON)
	start, err := b.services.BeginServiceAuthorization(t.Context(), "service_a", "browser")
	if err != nil {
		t.Fatal(err)
	}
	if start.Kind != "session" || !strings.Contains(string(start.Session), `"authorization_01"`) {
		t.Errorf("start = %+v", start)
	}
	if !reflect.DeepEqual(b.opened, []string{"https://auth.openai.com/oauth/authorize"}) {
		t.Errorf("opened = %v", b.opened)
	}
	// A session that answers 200 is not a start.
	b.core.answer(http.StatusOK, "", browserSessionJSON)
	if _, err := b.services.BeginServiceAuthorization(t.Context(), "service_a", "browser"); err == nil ||
		!strings.Contains(err.Error(), "returned 200 OK") {
		t.Errorf("200 answer error = %v", err)
	}
	// A browser that cannot open still starts the session.
	b.opened = nil
	b.services.OpenURL = func(string) error { return errors.New("no browser") }
	b.core.answer(http.StatusAccepted, "", browserSessionJSON)
	if _, err := b.services.BeginServiceAuthorization(t.Context(), "service_a", "browser"); err != nil {
		t.Errorf("start without a browser: %v", err)
	}
}

func TestPricingModelIsNullWhenCoreDoesNotKnowIt(t *testing.T) {
	b := newBridge(t)
	b.core.answer(http.StatusNotFound, "", `{"error":{"code":"not_found"}}`)
	value, err := b.pricing.Pricing(t.Context(), "model", nil, raw(`{"model":"unknown"}`))
	if err != nil || string(value) != "null" {
		t.Errorf("unknown model = %s, %v", value, err)
	}
}

func TestCopyAccessTokenWritesTheSecretToTheClipboardOnly(t *testing.T) {
	b := newBridge(t)
	b.core.answer(http.StatusOK, "", `{"access_token":"sk-local"}`)
	copied, err := b.tokens.CopyAccessToken(t.Context(), "token_a")
	if err != nil || !copied || !reflect.DeepEqual(b.copied, []string{"sk-local"}) {
		t.Fatalf("copy = %v, %v, clipboard %v", copied, err, b.copied)
	}
	b.core.answer(http.StatusOK, "", `{"access_token":""}`)
	if _, err := b.tokens.CopyAccessToken(t.Context(), "token_a"); err == nil || err.Error() != "access token reveal returned no token" {
		t.Errorf("empty token error = %v", err)
	}
	b.tokens.WriteClipboard = func(string) error { return errors.New("clipboard busy") }
	b.core.answer(http.StatusOK, "", `{"access_token":"sk-local"}`)
	if copied, err := b.tokens.CopyAccessToken(t.Context(), "token_a"); err != nil || copied {
		t.Errorf("refused clipboard = %v, %v", copied, err)
	}
}

func TestCommandsTellTheAppWhatChanged(t *testing.T) {
	b := newBridge(t)
	ctx := t.Context()
	b.core.answer(http.StatusNoContent, "", "")
	if err := b.records.DeleteRequestRecord(ctx, "req_a"); err != nil {
		t.Fatal(err)
	}
	b.core.answer(http.StatusOK, "", `{"deleted":1}`)
	if _, err := b.records.PurgeRequestRecords(ctx, raw(`{"scope":"all","confirm":true}`)); err != nil {
		t.Fatal(err)
	}
	b.core.answer(http.StatusOK, strongTag, privacyPolicyJSON)
	if _, err := b.privacy.UpdatePrivacyPolicy(ctx, strongTag, raw(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	b.core.answer(http.StatusOK, "", catalogJSON(privacyVariantJSON, `"0.2.0"`, "true"))
	if _, err := b.privacy.GetPrivacyModelReleases(ctx, nil); err != nil {
		t.Fatal(err)
	}
	// A refused change tells nothing.
	b.core.answer(http.StatusConflict, "", `{}`)
	_ = b.records.DeleteRequestRecord(ctx, "req_a")
	_, _ = b.privacy.UpdatePrivacyPolicy(ctx, strongTag, raw(`{"enabled":true}`))
	if want := map[string]int{"records": 2, "policy": 1, "releases": 1}; !reflect.DeepEqual(b.changed, want) {
		t.Errorf("changes = %v, want %v", b.changed, want)
	}
}

func TestTestServiceIsCancelledByItsWindowOnly(t *testing.T) {
	b := newBridge(t)
	arrived := make(chan struct{})
	b.core.mu.Lock()
	b.core.reply = func(r *http.Request) (int, string, string) {
		close(arrived)
		<-r.Context().Done()
		return http.StatusOK, "", `{}`
	}
	b.core.mu.Unlock()
	type result struct {
		value json.RawMessage
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := b.services.TestService(t.Context(), "service_a", raw(`{}`), ptr("test-1"))
		done <- result{value, err}
	}()
	select {
	case <-arrived:
	case r := <-done:
		t.Fatalf("test ended before Core saw it: %+v", r)
	}
	other := b.services
	other.Owner = func(context.Context) string { return "trajectory-inspector-1" }
	if other.CancelServiceTest(t.Context(), "service_a", "test-1") {
		t.Fatal("another window cancelled the test")
	}
	if !b.services.CancelServiceTest(t.Context(), "service_a", "test-1") {
		t.Fatal("the window could not cancel its test")
	}
	select {
	case r := <-done:
		if r.err == nil || r.err.Error() != ServiceTestCancelled {
			t.Errorf("cancelled test = %s, %v", r.value, r.err)
		}
	case <-t.Context().Done():
		t.Fatal("the cancelled test kept running")
	}
	if b.services.CancelServiceTest(t.Context(), "service_a", "test-1") {
		t.Error("a finished test was cancelled")
	}
	for _, ids := range [][2]string{{"", "t"}, {"service_a", ""}, {"service_a", strings.Repeat("t", 129)}} {
		if _, err := b.services.TestService(t.Context(), ids[0], raw(`{}`), &ids[1]); err == nil || err.Error() != ServiceTestInvalidID {
			t.Errorf("ids %q: %v", ids, err)
		}
		if b.services.CancelServiceTest(t.Context(), ids[0], ids[1]) {
			t.Errorf("ids %q cancelled", ids)
		}
	}
}
