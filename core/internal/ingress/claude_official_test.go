package ingress

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/claudecli"
	"github.com/QuantumNous/astrlink/core/internal/claudecli/fakeclaude"
	"github.com/QuantumNous/astrlink/core/internal/claudeofficial"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestMain(m *testing.M) {
	fakeclaude.Main()
	os.Exit(m.Run())
}

// lockedRecordStore is a request record store the outer request and the
// CLI's loopback calls write at the same time.
type lockedRecordStore struct {
	mu    sync.Mutex
	store memoryRequestRecordStore
}

func (locked *lockedRecordStore) UpsertRequestRecord(ctx context.Context, record contract.RequestRecord) error {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.store.UpsertRequestRecord(ctx, record)
}

func (locked *lockedRecordStore) InsertRequestRecord(ctx context.Context, record contract.RequestRecord) error {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.store.InsertRequestRecord(ctx, record)
}

func (locked *lockedRecordStore) FindSessionLink(ctx context.Context, kind contract.SessionCursorKind, values []string, scope storage.SessionCursorScope) (storage.SessionLinkMatch, bool, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.store.FindSessionLink(ctx, kind, values, scope)
}

func (locked *lockedRecordStore) records() []contract.RequestRecord {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return append([]contract.RequestRecord(nil), locked.store.records...)
}

// officialResolver routes every request to one official Claude account,
// converting OpenAI Chat to Claude Messages.
type officialResolver struct {
	service contract.Service
	baseURL string
}

func (resolver officialResolver) candidate(protocol contract.ProtocolID) endpoint.Resolved {
	candidate := endpoint.Resolved{Service: resolver.service, BaseURL: resolver.baseURL, Mode: contract.CapabilityModeNative}
	if protocol != contract.ProtocolAnthropicMessages {
		candidate.PlanType, candidate.UpstreamProtocol = contract.PlanTypeRelayKit, contract.ProtocolAnthropicMessages
	}
	return candidate
}

func (resolver officialResolver) Resolve(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
	return resolver.candidate(request.Protocol), nil
}

func (resolver officialResolver) ResolveCandidates(_ context.Context, request endpoint.ResolveRequest) ([]endpoint.Resolved, error) {
	return []endpoint.Resolved{resolver.candidate(request.Protocol)}, nil
}

func (resolver officialResolver) ResolveService(_ context.Context, id contract.ServiceID) (endpoint.Resolved, error) {
	if id != resolver.service.ID {
		return endpoint.Resolved{}, endpoint.ErrNoEndpoint
	}
	return resolver.candidate(contract.ProtocolAnthropicMessages), nil
}

// proxiedCall is one model call as it left AstrLink, seen by the account's
// proxy, which also answers it.
type proxiedCall struct {
	url    string
	header http.Header
	body   map[string]any
}

type officialHarness struct {
	server    *httptest.Server
	records   *lockedRecordStore
	configDir string
	official  *claudeofficial.Runtime
	service   contract.ServiceID
	// identities learns from the requests the gateway serves.
	identities *accountauth.IdentityRegistry

	mu       sync.Mutex
	calls    []proxiedCall
	connects []string
	usage    []string
}

// observedUsage lists the 5-hour utilization each model call reported to the
// account's usage, as "service_id=value".
func (harness *officialHarness) observedUsage() []string {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return append([]string(nil), harness.usage...)
}

// tunneled lists the CONNECT targets the account's proxy saw: the CLI's own
// traffic, relayed by its tunnel.
func (harness *officialHarness) tunneled() []string {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return append([]string(nil), harness.connects...)
}

func (harness *officialHarness) proxied() []proxiedCall {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return append([]proxiedCall(nil), harness.calls...)
}

func newOfficialHarness(t *testing.T) *officialHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is linked by symlink")
	}
	return newOfficialHarnessWith(t, fakeclaude.Install(t, t.TempDir(), fakeclaude.Settings{OwnTraffic: true}))
}

// newOfficialHarnessWith serves one official account through the claude
// executable at binary.
func newOfficialHarnessWith(t *testing.T, binary string) *officialHarness {
	t.Helper()
	harness := &officialHarness{records: &lockedRecordStore{}}
	// Hosts the CLI reaches on its own all answer from one TLS origin.
	origin := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(origin.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodConnect {
			harness.mu.Lock()
			harness.connects = append(harness.connects, request.Host)
			harness.mu.Unlock()
			relayConnect(t, writer, origin.Listener.Addr().String())
			return
		}
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		messages, _ := body["messages"].([]any)
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/v1/messages") || len(messages) == 0 {
			http.NotFound(writer, request)
			return
		}
		harness.mu.Lock()
		index := len(harness.calls)
		harness.calls = append(harness.calls, proxiedCall{url: request.URL.String(), header: request.Header.Clone(), body: body})
		harness.mu.Unlock()
		tools, _ := body["tools"].([]any)
		// The real CLI may end with its own system message, so look at the
		// whole history.
		answered := strings.Contains(fmt.Sprint(messages), "tool_result")
		events := []string{
			fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_%d","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"usage":{"input_tokens":100,"cache_read_input_tokens":20,"output_tokens":1}}}`, index),
		}
		if len(tools) > 0 && !answered {
			name := tools[0].(map[string]any)["name"]
			events = append(events,
				fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_%d","name":%q,"input":{}}}`, index, name),
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Paris\"}"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`,
			)
		} else {
			events = append(events,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"It is sunny."}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			)
		}
		events = append(events, `{"type":"message_stop"}`)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Anthropic-Ratelimit-Unified-Status", "allowed")
		writer.Header().Set("Anthropic-Ratelimit-Unified-5h-Utilization", fmt.Sprintf("0.%d", index+1))
		for _, event := range events {
			var parsed struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(event), &parsed)
			fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", parsed.Type, event)
		}
	}))
	t.Cleanup(proxy.Close)

	root := t.TempDir()
	layout := claudecli.Layout{Root: filepath.Join(root, "accounts"), WorkDir: filepath.Join(root, "work")}
	service := contract.Service{
		ID: "service_claude_official", Name: "Claude", Kind: contract.ServiceKindClaudeSubscription, Enabled: true,
		Models: []string{"claude-sonnet-5-5"},
		Capabilities: []contract.Capability{
			{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true},
			{Protocol: contract.ProtocolOpenAIModels, Mode: contract.CapabilityModeNative},
			{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: contract.ProtocolAnthropicMessages},
		},
		Subscription: &contract.SubscriptionConnection{
			Provider: contract.SubscriptionProviderClaudeCode, Status: contract.SubscriptionStatusConnected,
			Mode: contract.SubscriptionModeOfficial,
		},
		Proxy: &contract.ServiceProxy{Mode: "custom", URL: proxy.URL},
	}
	configDir, err := layout.EnsureConfigDir(service.ID)
	if err != nil {
		t.Fatal(err)
	}
	harness.configDir = configDir
	if err := os.WriteFile(fakeclaude.AccountFile(configDir), []byte(`{"email":"person@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	official := claudeofficial.New(claudeofficial.Options{Accounts: claudecli.Accounts{
		Locator: &claudecli.Locator{Configured: binary},
		Layout:  layout, Environ: []string{"PATH=/usr/bin:/bin", "HOME=" + root},
		Network: func(ctx context.Context, _ contract.ServiceID) (context.Context, error) {
			return networkproxy.Bind(ctx, service, nil)
		},
	}, Usage: func(_ context.Context, id contract.ServiceID, header http.Header) {
		harness.mu.Lock()
		harness.usage = append(harness.usage, string(id)+"="+header.Get("Anthropic-Ratelimit-Unified-5h-Utilization"))
		harness.mu.Unlock()
	}})
	t.Cleanup(official.Close)
	harness.official, harness.service = official, service.ID
	harness.identities = accountauth.NewIdentityRegistry(nil, nil)
	handler := NewWithDependencies(Dependencies{
		Resolver:         officialResolver{service: service, baseURL: "http://provider.invalid"},
		RequestRecords:   harness.records,
		ConversionEngine: relaykitbridge.NewEngine(),
		ClaudeOfficial:   official,
		Identities:       harness.identities,
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, token string) (contract.AccessTokenID, error) {
			if token != "local-token" {
				return "", fmt.Errorf("unknown token")
			}
			return "token_local", nil
		}),
	})
	harness.server = httptest.NewServer(handler)
	t.Cleanup(harness.server.Close)
	official.SetBaseURL(harness.server.URL)
	return harness
}

// relayConnect answers a CONNECT and pipes it to address.
func relayConnect(t *testing.T, writer http.ResponseWriter, address string) {
	target, err := net.Dial("tcp", address)
	if err != nil {
		http.Error(writer, "unreachable", http.StatusBadGateway)
		return
	}
	defer target.Close()
	conn, buffered, err := http.NewResponseController(writer).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go func() { _, _ = io.Copy(target, buffered) }()
	_, _ = io.Copy(conn, target)
}

func (harness *officialHarness) post(t *testing.T, path, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, harness.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer local-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

// settledRecords stops the gateway, which waits for the requests it is
// still serving, and returns their records. The CLI's loopback calls finish
// their records after the outer answer may already have reached the client.
func (harness *officialHarness) settledRecords() []contract.RequestRecord {
	harness.server.Close()
	return harness.records.records()
}

func (harness *officialHarness) secret(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(fakeclaude.EnvFile(harness.configDir, "serve"))
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "ANTHROPIC_CUSTOM_HEADERS="+claudeofficial.ProcessHeader+": "); ok {
			return value
		}
	}
	t.Fatal("the CLI had no process header")
	return ""
}

func TestOfficialClaudeServesThroughTheCLIAndItsLoopback(t *testing.T) {
	harness := newOfficialHarness(t)
	tools := `"tools":[{"name":"get_weather","description":"Weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]`
	status, body := harness.post(t, "/v1/messages", `{"model":"claude-sonnet-5-5","stream":true,"max_tokens":512,`+tools+`,
		"messages":[{"role":"user","content":"Weather in Paris?"}]}`)
	if status != http.StatusOK || !strings.Contains(body, `"name":"get_weather"`) || !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Fatalf("first turn = %d %s", status, body)
	}
	status, body = harness.post(t, "/v1/messages", `{"model":"claude-sonnet-5-5","stream":true,`+tools+`,"messages":[
		{"role":"user","content":"Weather in Paris?"},
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_0","name":"get_weather","input":{"city":"Paris"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_0","content":"22C"}]}]}`)
	if status != http.StatusOK || !strings.Contains(body, "It is sunny.") {
		t.Fatalf("second turn = %d %s", status, body)
	}
	secret := harness.secret(t)

	calls := harness.proxied()
	if len(calls) != 2 {
		t.Fatalf("upstream calls = %d", len(calls))
	}
	for _, call := range calls {
		if !strings.HasPrefix(call.url, "http://provider.invalid/v1/messages") {
			t.Fatalf("the call did not go through the account's proxy: %s", call.url)
		}
		if got := call.header.Get("Authorization"); got != "Bearer "+fakeclaude.FakeToken {
			t.Fatalf("upstream Authorization = %q, want the CLI's own", got)
		}
		for name, values := range call.header {
			lower := strings.ToLower(name + ": " + strings.Join(values, ","))
			if strings.HasPrefix(strings.ToLower(name), "x-astrlink-") || strings.Contains(lower, "astrlink") || strings.Contains(lower, secret) {
				t.Fatalf("upstream header %s: %v", name, values)
			}
		}
		if !strings.HasPrefix(call.header.Get("User-Agent"), "claude-cli/") {
			t.Fatalf("upstream User-Agent = %q", call.header.Get("User-Agent"))
		}
	}
	if last := fmt.Sprint(calls[1].body["messages"]); !strings.Contains(last, "tool_result") || !strings.Contains(last, "22C") {
		t.Fatalf("the tool result did not reach the model: %s", last)
	}
	// The plan usage of each answer reached the account, and nothing else
	// was asked for it.
	if usage := strings.Join(harness.observedUsage(), " "); usage != "service_claude_official=0.1 service_claude_official=0.2" {
		t.Fatalf("observed usage = %q", usage)
	}
	// The CLI's own requests (token refresh and the rest) left through the
	// account's proxy too, end to end, while its model calls came back to
	// AstrLink.
	tunneled := strings.Join(harness.tunneled(), " ")
	if tunneled != fakeclaude.RefreshHost+":443 "+fakeclaude.OtherHost+":443" {
		t.Fatalf("account proxy tunneled %q", tunneled)
	}
	own, err := os.ReadFile(fakeclaude.OwnTrafficFile(harness.configDir))
	if err != nil || string(own) != fakeclaude.RefreshHost+" ok\n"+fakeclaude.OtherHost+" ok\n" {
		t.Fatalf("the CLI's own requests = %q, %v", own, err)
	}

	records := harness.settledRecords()
	roots := map[contract.RequestID]contract.RequestRecord{}
	var children []contract.RequestRecord
	for _, record := range records {
		if record.ParentRequestID == nil {
			roots[record.ID] = record
		} else {
			children = append(children, record)
		}
		raw, _ := json.Marshal(record)
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), fakeclaude.FakeToken) {
			t.Fatalf("a record holds the process secret or the CLI's credential: %s", raw)
		}
	}
	if len(roots) != 2 || len(children) != 2 {
		t.Fatalf("roots=%d children=%d", len(roots), len(children))
	}
	rootInput := 0
	for _, root := range roots {
		if root.Status != contract.RequestStatusSucceeded || root.ServiceID == nil || *root.ServiceID != "service_claude_official" {
			t.Fatalf("root = %+v", root)
		}
		if root.Usage == nil {
			t.Fatalf("root has no usage")
		}
		rootInput += root.Usage.InputTokens
	}
	childInput := 0
	for _, child := range children {
		if child.Usage != nil {
			childInput += child.Usage.InputTokens
		}
	}
	if rootInput == 0 || rootInput != childInput {
		t.Fatalf("root input tokens = %d, upstream calls used %d", rootInput, childInput)
	}
	for _, child := range children {
		parent, ok := roots[*child.ParentRequestID]
		if !ok || child.RoutingDecision == nil || child.RoutingDecision.Selected != contract.RoutingSelectionClaudeCLI {
			t.Fatalf("child = %+v", child)
		}
		if child.SessionID != nil || child.LocalAccessTokenID == nil || *child.LocalAccessTokenID != "token_local" {
			t.Fatalf("child session=%v token=%v", child.SessionID, child.LocalAccessTokenID)
		}
		if child.Status != contract.RequestStatusSucceeded || parent.ID == child.ID {
			t.Fatalf("child = %+v", child)
		}
	}
}

func TestOfficialClaudeServesConvertedRequests(t *testing.T) {
	harness := newOfficialHarness(t)
	status, body := harness.post(t, "/v1/chat/completions", `{"model":"claude-sonnet-5-5","stream":true,
		"messages":[{"role":"system","content":"Be brief."},{"role":"user","content":"Weather?"}]}`)
	if status != http.StatusOK || !strings.Contains(body, "It is sunny.") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("chat = %d %s", status, body)
	}
	calls := harness.proxied()
	if len(calls) != 1 || !strings.Contains(fmt.Sprint(calls[0].body["system"]), "Be brief.") {
		t.Fatalf("upstream calls = %+v", calls)
	}
	// The serving CLI is the user's own Claude Code, so its call teaches the
	// identity even when the outer request came from another client.
	if got := harness.identities.ClientIdentities().Claude.LearnedVersion; got != "2.1.300" {
		t.Fatalf("learned Claude Code version = %q, want the serving CLI's", got)
	}
}

func TestClaudeProcessHeaderWithoutALiveSecretIsRefused(t *testing.T) {
	harness := newOfficialHarness(t)
	request, err := http.NewRequest(http.MethodPost, harness.server.URL+"/v1/messages", strings.NewReader(`{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"Hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(claudeofficial.ProcessHeader, "guessed")
	request.Header.Set("Authorization", "Bearer local-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if records := harness.records.records(); len(records) != 0 || len(harness.proxied()) != 0 {
		t.Fatalf("a refused call was recorded or forwarded: %d records", len(records))
	}
}
