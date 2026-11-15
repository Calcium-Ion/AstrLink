package clients

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func zCodePath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".zcode", "v2", "provider_config.json")
}

func zCodeProvider(id, name, kind, models string) string {
	return `{"providerId": "` + id + `", "templateId": null, "providerName": "` + name + `", "enabled": true,
	  "config": {"group": "standard-personal", "access": {"type": "api-key", "apiKey": "` + token + `"},
	    "api": {"type": "` + kind + `", "baseUrl": "` + origin + `/v1", "headers": {}},
	    "personalModelIds": ` + models + `, "modelOrder": ` + models + `, "visibility": "visible"}}`
}

func TestZCodeWritesAProviderPerProtocolAndARulePerModel(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, home.Dir(), ".zcode", "v2")
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "deepseek-v4")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages"}
	details := cc.Catalog{"claude-sonnet-5": {ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000)), Reasoning: []string{"low", "high"}, HasReasoning: true}}
	if asked := writePlace(t, home, place(ZCode), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := `{
  "schemaVersion": 1,
  "config": {
    "providerOrder": ["local-chat", "local-anthropic"],
    "providerConfigRules": {"providerRules": [
      ` + zCodeProvider("local-chat", "AstrLink", "openai-chat-completions", `["deepseek-v4"]`) + `,
      ` + zCodeProvider("local-anthropic", "AstrLink Anthropic", "anthropic-messages", `["claude-sonnet-5"]`) + `
    ]},
    "modelConfigRules": {
      "providerModelRules": [
        {"providerId": "local-chat", "modelId": "deepseek-v4",
         "config": {"enabled": true, "properties": {"supportsToolCall": true}}},
        {"providerId": "local-anthropic", "modelId": "claude-sonnet-5",
         "config": {"enabled": true, "properties": {"contextWindow": 200000, "supportsToolCall": true},
                    "optionSpecs": {"maxOutputTokens": {"max": 64000}}}}
      ],
      "manualProviderModelRules": []
    },
    "defaultModelSelection": {"providerId": "local-anthropic", "modelId": "claude-sonnet-5", "options": {"reasoningLevel": "high"}}
  }
}`
	if got := readJSON(t, zCodePath(home)); !reflect.DeepEqual(got, decode(t, want)) {
		text, _ := readFile(t, zCodePath(home))
		t.Fatalf("%s", text)
	}
	status := statusOf(t, home, ZCode, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" {
		t.Fatalf("%+v", status)
	}

	// Without the Claude model, its provider, rule and place go.
	writePlace(t, home, place(ZCode), connection(models("model", "deepseek-v4")), cc.Catalog{}, false)
	written := readJSON(t, zCodePath(home))
	if !reflect.DeepEqual(at(written, "config", "providerOrder"), decode(t, `["local-chat"]`)) ||
		len(at(written, "config", "providerConfigRules", "providerRules").([]any)) != 1 ||
		len(at(written, "config", "modelConfigRules", "providerModelRules").([]any)) != 1 {
		t.Fatalf("%v", written)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	rules := at(readJSON(t, zCodePath(home)), "config", "providerConfigRules", "providerRules")
	if got := at(item(rules, 0), "config", "api", "baseUrl"); got != "http://127.0.0.1:9000/v1" {
		t.Fatal(got)
	}

	removeAt(t, home, ZCode)
	if _, exists := readFile(t, zCodePath(home)); exists {
		t.Fatal("file left")
	}
}

const zCodeUser = `{
  "schemaVersion": 1,
  "config": {
    "providerOrder": ["deepseek"],
    "providerConfigRules": {
      "providerRules": [
        {"providerId": "deepseek", "templateId": null, "providerName": "DeepSeek", "enabled": true, "config": {"group": "standard-personal"}}
      ]
    },
    "modelConfigRules": {
      "providerModelRules": [],
      "manualProviderModelRules": []
    },
    "defaultModelSelection": {"providerId": "deepseek", "modelId": "deepseek-v4-pro"}
  }
}
`

func TestZCodeAddsItsProviderAfterTheUsersAndPutsTheirDefaultBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, zCodePath(home), zCodeUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writePlace(t, home, place(ZCode), chosen, cc.Catalog{}, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "config.defaultModelSelection")
	writePlace(t, home, place(ZCode), chosen, cc.Catalog{}, true)
	written := readJSON(t, zCodePath(home))
	if !reflect.DeepEqual(at(written, "config", "providerOrder"), decode(t, `["deepseek", "local-chat"]`)) {
		t.Fatalf("%v", written)
	}
	rules := at(written, "config", "providerConfigRules", "providerRules")
	if at(item(rules, 0), "providerId") != "deepseek" || at(item(rules, 1), "providerId") != "local-chat" {
		t.Fatalf("%v", rules)
	}
	if got := at(written, "config", "defaultModelSelection"); !reflect.DeepEqual(got, decode(t, `{"providerId": "local-chat", "modelId": "deepseek-v4", "options": {"reasoningLevel": "disabled"}}`)) {
		t.Fatalf("%v", got)
	}

	removeAt(t, home, ZCode)
	if got := readJSON(t, zCodePath(home)); !reflect.DeepEqual(got, decode(t, zCodeUser)) {
		text, _ := readFile(t, zCodePath(home))
		t.Fatalf("%s", text)
	}
}
