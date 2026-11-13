package clients

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func zedFile(home agents.Home) string { return filepath.Join(Zed.Dir(home, ""), "settings.json") }

func zedRead(t *testing.T, text string) any {
	t.Helper()
	value, err := cc.ReadJSON(Zed.Files()[0], text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func zedFileValue(t *testing.T, home agents.Home) any {
	t.Helper()
	text, _ := readFile(t, zedFile(home))
	return zedRead(t, text)
}

func TestZedWritesAProviderPerKindAndAsksForAPastePerAddress(t *testing.T) {
	home := scratchHome(t)
	mkdir(t, Zed.Dir(home, ""))
	protocols := map[string]string{"gpt-5.5": "openai.responses", "claude-sonnet-5": "anthropic.messages"}
	chosen := listed(models("model", "gpt-5.5"), "claude-sonnet-5", "gpt-5.5", "deepseek-v4")
	chosen.Protocols = protocols
	details := cc.Catalog{"gpt-5.5": {ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000))}}
	if asked := writePlace(t, home, place(Zed), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	capabilities := func(chat bool) map[string]any {
		return map[string]any{
			"tools": true, "images": false, "parallel_tool_calls": false, "prompt_cache_key": false,
			"chat_completions": chat, "interleaved_reasoning": false, "max_tokens_parameter": false,
		}
	}
	want := map[string]any{
		"language_models": map[string]any{
			"openai_compatible": map[string]any{"AstrLink": map[string]any{
				"api_url": origin + "/v1",
				"available_models": []any{
					map[string]any{"name": "gpt-5.5", "max_tokens": number(400_000), "max_output_tokens": number(128_000), "capabilities": capabilities(false)},
					map[string]any{"name": "deepseek-v4", "max_tokens": number(128_000), "capabilities": capabilities(true)},
				},
			}},
			"anthropic_compatible": map[string]any{"AstrLink Anthropic": map[string]any{
				"api_url": origin,
				"available_models": []any{
					map[string]any{"name": "claude-sonnet-5", "max_tokens": number(200_000), "max_output_tokens": number(32_000),
						"capabilities": map[string]any{"tools": true, "images": false, "prompt_caching": true}},
				},
			}},
		},
		"agent": map[string]any{"default_model": map[string]any{"provider": "AstrLink", "model": "gpt-5.5"}},
	}
	if got := zedFileValue(t, home); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	status := statusOf(t, home, Zed, origin)
	if status.State != cc.Configured || *status.Models.Model != "gpt-5.5" || status.PasteSinceUnix == nil {
		t.Fatalf("%+v", status)
	}
	sameStrings(t, status.PasteInto, "AstrLink", "AstrLink Anthropic")
	since := *status.PasteSinceUnix

	// Another model keeps the pasted token; another port does not.
	again := listed(models("model", "deepseek-v4"), "claude-sonnet-5", "gpt-5.5", "deepseek-v4")
	again.Protocols = protocols
	if _, _, err := cc.WriteWith(home, place(Zed), again, details, tokens(), false); err != nil {
		t.Fatal(err)
	}
	if status := statusOf(t, home, Zed, origin); status.PasteSinceUnix == nil || *status.PasteSinceUnix != since {
		t.Fatalf("%+v", status)
	}
	records, err := cc.ReadRecords(home)
	if err != nil {
		t.Fatal(err)
	}
	record := records.Get(place(Zed)).Clone()
	record.PasteSinceUnix = cc.Some(uint64(1))
	records.Set(place(Zed), record)
	if err := cc.WriteRecords(home, records); err != nil {
		t.Fatal(err)
	}
	syncAt(t, home, "http://127.0.0.1:9000/")
	synced := zedFileValue(t, home)
	if at(synced, "language_models", "openai_compatible", "AstrLink", "api_url") != "http://127.0.0.1:9000/v1" ||
		at(synced, "language_models", "anthropic_compatible", "AstrLink Anthropic", "api_url") != "http://127.0.0.1:9000" {
		t.Fatalf("%v", synced)
	}
	status = statusOf(t, home, Zed, "http://127.0.0.1:9000")
	if status.State != cc.Configured || status.PasteSinceUnix == nil || *status.PasteSinceUnix <= 1 {
		t.Fatalf("%+v", status)
	}

	removeAt(t, home, Zed)
	if _, exists := readFile(t, zedFile(home)); exists {
		t.Fatal("settings left")
	}
}

const zedUser = "// Zed settings\n{\n  \"theme\": \"One Dark\",\n  \"language_models\": {\n    \"openai\": { \"api_url\": \"https://api.openai.com/v1\" },\n  },\n  \"agent\": {\n    \"default_model\": { \"provider\": \"zed.dev\", \"model\": \"claude-sonnet-4\" },\n  },\n  \"stable\": { \"agent\": { \"default_model\": { \"provider\": \"openai\", \"model\": \"gpt-5\" } } },\n}\n"

func TestZedLeavesZedsProvidersAndPutsTheModelBack(t *testing.T) {
	home := scratchHome(t)
	writeFile(t, zedFile(home), zedUser)
	chosen := connection(models("model", "deepseek-v4"))
	asked := writeAt(t, home, Zed, chosen, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "agent.default_model")
	writeAt(t, home, Zed, chosen, true)
	written := zedFileValue(t, home)
	if at(written, "language_models", "openai", "api_url") != "https://api.openai.com/v1" || at(written, "agent", "default_model", "provider") != "AstrLink" {
		t.Fatalf("%v", written)
	}
	status := statusOf(t, home, Zed, origin)
	sameStrings(t, status.Notes, "overridden")
	sameStrings(t, status.PasteInto, "AstrLink")

	removeAt(t, home, Zed)
	restored, _ := readFile(t, zedFile(home))
	if !strings.HasPrefix(restored, "// Zed settings") || !reflect.DeepEqual(zedRead(t, restored), zedRead(t, zedUser)) {
		t.Fatal(restored)
	}
}
