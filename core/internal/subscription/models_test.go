package subscription

import (
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func TestCodexUsageURLUsesOfficialChatGPTAndCodexPaths(t *testing.T) {
	got := CodexUsageURL(accountauth.DefaultCodexAPIBaseURL + "/")
	if got != "https://chatgpt.com/backend-api/wham/usage" {
		t.Fatalf("default usage URL = %q", got)
	}
	custom := CodexUsageURL("https://codex.example/backend-api/codex")
	if custom != "https://codex.example/backend-api/wham/usage" {
		t.Fatalf("custom usage URL = %q", custom)
	}
	apiStyle := CodexUsageURL("https://api.openai.com")
	if apiStyle != "https://api.openai.com/api/codex/usage" {
		t.Fatalf("api-style usage URL = %q", apiStyle)
	}
}

func TestCodexConsumeResetURLUsesOfficialChatGPTAndCodexPaths(t *testing.T) {
	got := CodexConsumeResetURL(accountauth.DefaultCodexAPIBaseURL + "/")
	if got != "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume" {
		t.Fatalf("default consume URL = %q", got)
	}
	custom := CodexConsumeResetURL("https://codex.example/backend-api/codex")
	if custom != "https://codex.example/backend-api/wham/rate-limit-reset-credits/consume" {
		t.Fatalf("custom consume URL = %q", custom)
	}
	apiStyle := CodexConsumeResetURL("https://api.openai.com")
	if apiStyle != "https://api.openai.com/api/codex/rate-limit-reset-credits/consume" {
		t.Fatalf("api-style consume URL = %q", apiStyle)
	}
}

func TestCodexModelsURLAppendsClientVersion(t *testing.T) {
	got := CodexModelsURL("https://chatgpt.com/backend-api/codex/", "")
	want := "https://chatgpt.com/backend-api/codex/models?client_version=" +
		accountauth.DefaultCodexModelsClientVersion
	if got != want {
		t.Fatalf("CodexModelsURL() = %q, want %q", got, want)
	}
	custom := CodexModelsURL("https://codex.example/backend-api/codex", "0.99.0")
	if custom != "https://codex.example/backend-api/codex/models?client_version=0.99.0" {
		t.Fatalf("CodexModelsURL(custom) = %q", custom)
	}
}

func TestDecodeCodexModelsOfficialAndCompatibleEnvelopes(t *testing.T) {
	official, err := DecodeCodexModels([]byte(`{
		"models":[
			{"slug":"gpt-5","visibility":"list","supported_in_api":false},
			{"slug":"gpt-5-codex","visibility":"LIST"},
			{"slug":"hidden","visibility":"hide"},
			{"slug":"none","visibility":"none"},
			{"slug":"","visibility":"list"},
			{"slug":"  gpt-4.1  ","visibility":"list"}
		]
	}`))
	if err != nil {
		t.Fatalf("official DecodeCodexModels() = %v", err)
	}
	if got, want := modelIDs(official), "gpt-5,gpt-5-codex,gpt-4.1,codex-auto-review"; got != want {
		t.Fatalf("official IDs = %q, want %q", got, want)
	}

	compatible, err := DecodeCodexModels([]byte(`{"object":"list","data":[{"id":"gpt-z"},{"id":"gpt-a"},{"id":""}]}`))
	if err != nil {
		t.Fatalf("compatible DecodeCodexModels() = %v", err)
	}
	if got, want := modelIDs(compatible), "gpt-z,gpt-a,codex-auto-review"; got != want {
		t.Fatalf("compatible IDs = %q, want %q", got, want)
	}

	emptyOfficial, err := DecodeCodexModels([]byte(`{"models":[]}`))
	if err != nil || modelIDs(emptyOfficial) != "codex-auto-review" {
		t.Fatalf("empty official = %#v err=%v", emptyOfficial, err)
	}
	emptyCompatible, err := DecodeCodexModels([]byte(`{"data":[]}`))
	if err != nil || modelIDs(emptyCompatible) != "codex-auto-review" {
		t.Fatalf("empty compatible = %#v err=%v", emptyCompatible, err)
	}
}

func TestDecodeCodexModelsRejectsMalformedEnvelopes(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"models":{}}`,
		`{"data":{}}`,
		`{"models":null}`,
		`not json`,
	} {
		if _, err := DecodeCodexModels([]byte(body)); err == nil {
			t.Fatalf("DecodeCodexModels(%s) succeeded", body)
		}
	}
}

func TestEncodeOpenAIModelDiscovery(t *testing.T) {
	body, err := EncodeOpenAIModelDiscovery(ModelList{Data: []ModelRecord{{ID: "gpt-5"}, {ID: "gpt-4.1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"data":[{"id":"gpt-5"},{"id":"gpt-4.1"}]}` {
		t.Fatalf("body = %s", body)
	}
	empty, err := EncodeOpenAIModelDiscovery(ModelList{Data: []ModelRecord{}})
	if err != nil || string(empty) != `{"data":[]}` {
		t.Fatalf("empty = %s err=%v", empty, err)
	}
}

func TestApplyCodexModelsQueryLeavesExistingVersion(t *testing.T) {
	query := url.Values{"client_version": {"0.1.0"}}
	ApplyCodexModelsQuery(query, "0.99.0")
	if query.Get("client_version") != "0.1.0" {
		t.Fatalf("query = %v", query)
	}
}

func modelIDs(list ModelList) string {
	ids := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		ids = append(ids, model.ID)
	}
	return strings.Join(ids, ",")
}
