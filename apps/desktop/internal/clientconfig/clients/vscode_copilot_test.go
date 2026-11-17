package clients

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

func vscodeFile(home agents.Home) string {
	return filepath.Join(VSCodeCopilot.Dir(home, "stable"), "chatLanguageModels.json")
}

func vscodeRead(t *testing.T, home agents.Home) any {
	t.Helper()
	text, _ := readFile(t, vscodeFile(home))
	return vscodeJSON(t, text)
}

func vscodeJSON(t *testing.T, text string) any {
	t.Helper()
	value, err := cc.ReadJSON(VSCodeCopilot.Files()[0], text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func TestVSCodeWritesOneGroupWithTheDefaultFirst(t *testing.T) {
	home := scratchHome(t)
	stable := targeted(t, VSCodeCopilot, "stable")
	mkdir(t, VSCodeCopilot.Dir(home, "stable"))
	if VSCodeCopilot.Detected(home, "insiders") {
		t.Fatal("insiders detected")
	}
	chosen := listed(models("model", "gpt-5.5"), "claude-sonnet-5", "gpt-5.5", "deepseek-v4")
	chosen.Target = "stable"
	chosen.Protocols = map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"gpt-5.5": {
		Reasoning: []string{"none", "low", "medium", "high"}, HasReasoning: true,
		ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000)),
	}}
	if asked := writePlace(t, home, stable, chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	bearer := "Bearer " + token
	want := []any{map[string]any{
		"name":   "AstrLink",
		"vendor": "customendpoint",
		"models": []any{
			map[string]any{"id": "gpt-5.5", "name": "gpt-5.5", "url": origin + "/v1/responses",
				"apiType": "responses", "toolCalling": true, "vision": false,
				"maxInputTokens": number(272_000), "maxOutputTokens": number(128_000), "thinking": true,
				"supportsReasoningEffort": []any{"low", "medium", "high"}, "statefulResponses": false,
				"requestHeaders": map[string]any{"Authorization": bearer}},
			map[string]any{"id": "claude-sonnet-5", "name": "claude-sonnet-5", "url": origin + "/v1/messages",
				"apiType": "messages", "toolCalling": true, "vision": false,
				"maxInputTokens": number(128_000), "maxOutputTokens": number(16_000),
				"requestHeaders": map[string]any{"x-api-key": token}},
			map[string]any{"id": "deepseek-v4", "name": "deepseek-v4", "url": origin + "/v1/chat/completions",
				"apiType": "chat-completions", "toolCalling": true, "vision": false,
				"maxInputTokens": number(128_000), "maxOutputTokens": number(16_000),
				"requestHeaders": map[string]any{"Authorization": bearer}},
		},
	}}
	if got := vscodeRead(t, home); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	statuses, err := cc.StatusOf(home, []cc.Client{VSCodeCopilot}, origin, tokens())
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if status.Target != nil && *status.Target == "stable" && (status.State != cc.Configured || *status.Models.Model != "gpt-5.5") {
			t.Fatalf("%+v", status)
		}
	}

	// VS Code keeps a per-model option in the group; it stays.
	text, _ := readFile(t, vscodeFile(home))
	groups := vscodeRead(t, home).([]any)
	groups[0].(map[string]any)["settings"] = map[string]any{"gpt-5.5": map[string]any{"reasoningEffort": "high"}}
	writeFile(t, vscodeFile(home), format.EncodePretty(groups))
	syncAt(t, home, "http://127.0.0.1:9000/")
	synced := vscodeRead(t, home).([]any)
	if at(synced[0], "models").([]any)[1].(map[string]any)["url"] != "http://127.0.0.1:9000/v1/messages" ||
		at(synced[0], "settings", "gpt-5.5", "reasoningEffort") != "high" {
		t.Fatalf("%v", synced)
	}
	statuses, err = cc.StatusOf(home, []cc.Client{VSCodeCopilot}, "http://127.0.0.1:9000", tokens())
	if err != nil || statuses[0].State != cc.Configured {
		t.Fatalf("%+v %v", statuses, err)
	}
	if now, _ := readFile(t, vscodeFile(home)); now == text {
		t.Fatal("sync changed nothing")
	}

	removePlace(t, home, stable)
	if _, exists := readFile(t, vscodeFile(home)); exists {
		t.Fatal("file left")
	}
}

const vscodeUser = "[\n\t{\n\t\t\"name\": \"My vLLM\",\n\t\t\"vendor\": \"customendpoint\",\n\t\t\"apiKey\": \"${input:chat.lm.secret.5f2a91c0}\",\n\t\t\"models\": []\n\t}\n]"

func TestVSCodeAddsItsGroupAfterTheUsersAndTakesItOutAgain(t *testing.T) {
	home := scratchHome(t)
	stable := targeted(t, VSCodeCopilot, "stable")
	writeFile(t, vscodeFile(home), vscodeUser)
	chosen := connection(models("model", "deepseek-v4"))
	chosen.Target = "stable"
	if asked := writePlace(t, home, stable, chosen, cc.Catalog{}, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	written := vscodeRead(t, home).([]any)
	if at(written[0], "name") != "My vLLM" || at(written[1], "name") != "AstrLink" {
		t.Fatalf("%v", written)
	}

	removePlace(t, home, stable)
	if got := vscodeRead(t, home); !reflect.DeepEqual(got, vscodeJSON(t, vscodeUser)) {
		t.Fatalf("%v", got)
	}
}
