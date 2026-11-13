package ingress

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudecli/fakeclaude"
	"github.com/QuantumNous/astrlink/core/internal/claudeofficial"
)

var toolUseIDPattern = regexp.MustCompile(`"id":"(toolu_[^"]+)"`)

// TestOfficialClaudeWithTheRealCLI drives the user's real claude executable,
// named by ASTRLINK_TEST_CLAUDE_BIN, through a tool round trip. It signs in
// with a fake token and nothing it sends reaches the internet: its model calls
// come back to AstrLink and on to the capture proxy, and macOS's sandbox-exec
// denies it every connection off this machine, so its own requests can only
// leave through AstrLink's tunnel, which the capture proxy answers with a
// local TLS origin. Elsewhere its HTTPS_PROXY points at a refusing proxy and
// the tunnel goes untested.
func TestOfficialClaudeWithTheRealCLI(t *testing.T) {
	binary := os.Getenv("ASTRLINK_TEST_CLAUDE_BIN")
	if binary == "" {
		t.Skip("set ASTRLINK_TEST_CLAUDE_BIN to run the real claude CLI against a capture server")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the wrapper is a shell script")
	}
	dir := t.TempDir()
	wrapper, headers := filepath.Join(dir, "claude"), filepath.Join(dir, "custom-headers")
	launch := fmt.Sprintf("exec %q \"$@\"", binary)
	sandbox, sandboxErr := exec.LookPath("sandbox-exec")
	if sandboxErr == nil {
		profile := `(version 1)(allow default)(deny network-outbound)(allow network-outbound (remote ip "localhost:*"))(allow network-outbound (remote unix-socket))`
		launch = fmt.Sprintf("exec %q -p %q %q \"$@\"", sandbox, profile, binary)
	} else {
		refusing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "refused", http.StatusForbidden)
		}))
		t.Cleanup(refusing.Close)
		launch = fmt.Sprintf("export HTTPS_PROXY=%q https_proxy=%q\n%s", refusing.URL, refusing.URL, launch)
	}
	script := fmt.Sprintf(`#!/bin/sh
[ -n "$ANTHROPIC_CUSTOM_HEADERS" ] && printf '%%s' "$ANTHROPIC_CUSTOM_HEADERS" > %q
export CLAUDE_CODE_OAUTH_TOKEN=%q
export DISABLE_AUTOUPDATER=1
%s
`, headers, fakeclaude.FakeToken, launch)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	harness := newOfficialHarnessWith(t, wrapper)
	tools := `"tools":[{"name":"get_weather","description":"Weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]`
	status, body := harness.post(t, "/v1/messages", `{"model":"claude-sonnet-5-5","stream":true,"max_tokens":512,`+tools+`,
		"messages":[{"role":"user","content":"Weather in Paris?"}]}`)
	if status != http.StatusOK || !strings.Contains(body, `"name":"get_weather"`) || !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Fatalf("first turn = %d %s", status, body)
	}
	use := toolUseIDPattern.FindStringSubmatch(body)
	if use == nil {
		t.Fatalf("first turn has no tool use id: %s", body)
	}
	status, body = harness.post(t, "/v1/messages", `{"model":"claude-sonnet-5-5","stream":true,`+tools+`,"messages":[
		{"role":"user","content":"Weather in Paris?"},
		{"role":"assistant","content":[{"type":"tool_use","id":"`+use[1]+`","name":"get_weather","input":{"city":"Paris"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"`+use[1]+`","content":"22C"}]}]}`)
	if status != http.StatusOK || !strings.Contains(body, "It is sunny.") {
		t.Fatalf("second turn = %d %s", status, body)
	}
	recorded, err := os.ReadFile(headers)
	if err != nil {
		t.Fatal(err)
	}
	secret, ok := strings.CutPrefix(string(recorded), claudeofficial.ProcessHeader+": ")
	if !ok || secret == "" {
		t.Fatalf("the CLI had no process header: %q", recorded)
	}

	calls := harness.proxied()
	if len(calls) < 2 {
		t.Fatalf("upstream calls = %d", len(calls))
	}
	answered := false
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
		if strings.Contains(fmt.Sprint(call.body), "22C") {
			answered = true
		}
	}
	if !answered {
		t.Fatal("the tool result did not reach the model")
	}
	for _, record := range harness.settledRecords() {
		if record.ParentRequestID != nil && (record.RoutingDecision == nil || record.RoutingDecision.Selected != contract.RoutingSelectionClaudeCLI) {
			t.Fatalf("child = %+v", record)
		}
	}
	t.Logf("the CLI's own requests through the tunnel: %v", harness.tunneled())

	// The model list comes from the CLI's initialize reply and makes no
	// model call.
	models, err := harness.official.Models(t.Context(), harness.service)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Fatal("the CLI listed no models")
	}
	for _, model := range models {
		if !strings.HasPrefix(model, "claude-") || strings.Contains(model, "[") {
			t.Fatalf("model %q in %v", model, models)
		}
	}
	if after := harness.proxied(); len(after) != len(calls) {
		t.Fatalf("listing models made %d model calls", len(after)-len(calls))
	}
	t.Logf("models the CLI offers: %v", models)
}
