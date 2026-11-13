package clients

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func qoderPath(home agents.Home) string { return filepath.Join(home.Dir(), ".qoder", "settings.json") }

func qoderRead(t *testing.T, home agents.Home) any {
	t.Helper()
	text, _ := readFile(t, qoderPath(home))
	value, err := cc.ReadJSON(QoderCLI.Files()[0], text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func TestQoderCLIWritesAProviderPerProtocolAndTheStartupModel(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".qoder")
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "deepseek-v4")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"claude-sonnet-5": {Reasoning: []string{"none", "low", "high"}, HasReasoning: true, ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000))}}
	if asked := writePlace(t, home, place(QoderCLI), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := `{
	  "providers": {
	    "local-chat": {
	      "type": "openai-compatible", "displayName": "AstrLink", "protocol": "openai",
	      "authType": "bearer", "baseUrl": "` + origin + `/v1", "apiKey": "` + token + `",
	      "model": "deepseek-v4",
	      "models": [{"model": "deepseek-v4", "capabilities": {"tools": true}}]},
	    "local-anthropic": {
	      "type": "openai-compatible", "displayName": "AstrLink Anthropic", "protocol": "anthropic",
	      "authType": "api-key", "baseUrl": "` + origin + `/v1", "apiKey": "` + token + `",
	      "model": "claude-sonnet-5",
	      "models": [{
	        "model": "claude-sonnet-5", "contextWindow": 200000, "maxOutputTokens": 64000,
	        "capabilities": {"tools": true, "thinking": {
	          "modes": ["enabled"], "supportsEffort": true, "supportedEffortLevels": ["low", "high"]}}}]}
	  },
	  "model": {"name": "local-anthropic/claude-sonnet-5"}
	}`
	if got := qoderRead(t, home); !reflect.DeepEqual(got, decode(t, want)) {
		t.Fatalf("%v", got)
	}
	status := statusOf(t, home, QoderCLI, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" {
		t.Fatalf("%+v", status)
	}
	sameStrings(t, status.Notes, "account")

	syncAt(t, home, "http://127.0.0.1:9000/")
	synced := qoderRead(t, home)
	if at(synced, "providers", "local-chat", "baseUrl") != "http://127.0.0.1:9000/v1" || at(synced, "providers", "local-anthropic", "baseUrl") != "http://127.0.0.1:9000/v1" {
		t.Fatalf("%v", synced)
	}

	removeAt(t, home, QoderCLI)
	if _, exists := readFile(t, qoderPath(home)); exists {
		t.Fatal("file left")
	}
}

const qoderUser = "{\n  // my qoder\n  \"model\": {\"name\": \"auto\"},\n  \"providers\": {\n    \"mine\": {\"protocol\": \"openai\", \"baseUrl\": \"https://example.invalid/v1\", \"apiKey\": \"sk-mine\", \"models\": [{\"model\": \"glm-5\"}]},\n  },\n}\n"

func TestQoderCLIKeepsTheUsersProviderAndPutsTheirModelBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, qoderPath(home), qoderUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writePlace(t, home, place(QoderCLI), chosen, cc.Catalog{}, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "model.name")
	writePlace(t, home, place(QoderCLI), chosen, cc.Catalog{}, true)
	written := qoderRead(t, home)
	if at(written, "providers", "mine", "apiKey") != "sk-mine" || at(written, "providers", "local-chat", "apiKey") != token || at(written, "model", "name") != "local-chat/deepseek-v4" {
		t.Fatalf("%v", written)
	}

	removeAt(t, home, QoderCLI)
	if text, _ := readFile(t, qoderPath(home)); text != qoderUser {
		t.Fatalf("%q", text)
	}
}
