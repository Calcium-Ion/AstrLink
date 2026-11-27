package plugin

import (
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// relaySource is a provider-only new-api plugin with the relay routes and
// the hooks given.
func relaySource(relay, hooks string) string {
	return `export const meta = { apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  channelConfig: { type: "object", properties: { region: { type: "string", title: { en: "Region" }, enum: ["us", "eu"], default: "us" }, uploadUrl: { type: "string", format: "url", title: "Upload address", default: "https://upload.vendor.example/" } } },
  keyHint: { en: "AccessKey|SecretKey" },
  relay: ` + relay + ` };
` + hooks
}

func TestAProviderOnlyPluginLoads(t *testing.T) {
	relay := `[
  { incoming_path: "/v1/chat/completions", upstream_path: "/v1/chat/completions", models: ["vendor-large", "re:^vendor-mini-"] },
  { incoming_path: "/v1/chat/completions", upstream_path: "/v1/messages", converter: "openai_chat_completions_to_anthropic_messages", auth: { type: "header", name: "x-api-key", value: "{api_key}" } },
  { incoming_path: "/v1beta/models/{model}:generateContent", upstream_path: "{config.uploadUrl}/models/{model}:generateContent" },
  { incoming_path: "/v1/models", upstream_path: "/v1/models" }
]`
	checked, err := Check(Source{File: "plugin.js", Text: relaySource(relay, `export function prepareRequest(ctx, request) { return null; }
export function signRequest(ctx, request) { return { headers: { "X-Sign": request.bodySha256 } }; }`)})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	meta := checked.Task
	if meta == nil || meta.HasTaskPart() || len(meta.Meta.Relay) != 4 || meta.Meta.KeyHint.Text("en") != "AccessKey|SecretKey" {
		t.Fatalf("manifest = %+v", meta)
	}
	if got := RelayConverter(meta.Meta.Relay[0]); got != "" {
		t.Fatalf("a route without a converter = %q", got)
	}
	for _, test := range []struct {
		path, model, upstream string
		protocol              contract.ProtocolID
	}{
		{"/v1/chat/completions", "vendor-large", "/v1/chat/completions", contract.ProtocolOpenAIChat},
		{"/v1/chat/completions", "vendor-mini-2", "/v1/chat/completions", contract.ProtocolOpenAIChat},
		{"/v1/chat/completions", "other", "/v1/messages", contract.ProtocolAnthropicMessages},
		{"/v1beta/models/vendor-large:streamGenerateContent", "vendor-large", "{config.uploadUrl}/models/{model}:generateContent", contract.ProtocolGoogleGenerateContent},
	} {
		route := meta.RelayRouteFor(test.path, test.model)
		if route == nil || route.UpstreamPath != test.upstream || RelayUpstreamProtocol(*route) != test.protocol {
			t.Errorf("route of %s %s = %+v", test.path, test.model, route)
		}
	}
	if route := meta.RelayRouteFor("/v1/embeddings", "vendor-large"); route != nil {
		t.Errorf("a path no route serves = %+v", route)
	}
}

func TestATaskPluginMayRelayToo(t *testing.T) {
	chat := `[{ incoming_path: "/v1/chat/completions", upstream_path: "/v1/chat/completions" }]`
	both := strings.Replace(taskSource("demo", ""), `fetchMode: "per_task"`, `fetchMode: "per_task", relay: `+chat, 1)
	checked, err := Check(Source{File: "plugin.js", Text: both})
	if err != nil || !checked.Task.HasTaskPart() || len(checked.Task.Meta.Relay) != 1 {
		t.Fatalf("task and provider parts = %+v, %v", checked, err)
	}
}

func TestGeminiRoutesUnderBothVersionsAreTheGateways(t *testing.T) {
	for path, want := range map[string]contract.ProtocolID{
		"/v1beta/models/{model}:generateContent":        contract.ProtocolGoogleGenerateContent,
		"/v1/models/{model}:generateContent":            contract.ProtocolGoogleGenerateContent,
		"/v1/models/{model}:streamGenerateContent":      contract.ProtocolGoogleGenerateContent,
		"/v1beta/models/{model}:embedContent":           contract.ProtocolGoogleEmbeddings,
		"/v1/models/{model}:batchEmbedContents":         contract.ProtocolGoogleEmbeddings,
		"/v1beta/models/{model}:countTokens":            "",
		"/v1beta/models/{model}:predictLongRunning":     "",
		"/v1beta/openai/models/{model}:generateContent": "",
	} {
		if got := RelayProtocol(path); got != want {
			t.Errorf("RelayProtocol(%q) = %q, want %q", path, got, want)
		}
	}

	relay := `[
  { incoming_path: "/v1beta/models/{model}:generateContent", upstream_path: "/v1beta/models/{model}:generateContent" },
  { incoming_path: "/v1/models/{model}:embedContent", upstream_path: "/v1/models/{model}:embedContent" }
]`
	checked, err := Check(Source{File: "plugin.js", Text: relaySource(relay, "")})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	meta := checked.Task
	for path, upstream := range map[string]string{
		"/v1beta/models/gemini:streamGenerateContent": "/v1beta/models/{model}:generateContent",
		// A call under the version no route names takes the other's route.
		"/v1/models/gemini:generateContent":       "/v1beta/models/{model}:generateContent",
		"/v1/models/gemini:streamGenerateContent": "/v1beta/models/{model}:generateContent",
		"/v1beta/models/gemini:embedContent":      "/v1/models/{model}:embedContent",
	} {
		if route := meta.RelayRouteFor(path, "gemini"); route == nil || route.UpstreamPath != upstream {
			t.Errorf("route of %s = %+v, want upstream %s", path, route, upstream)
		}
	}
	if route := meta.RelayRouteFor("/v1/models/gemini:batchEmbedContents", "gemini"); route != nil {
		t.Errorf("a call no route serves = %+v", route)
	}
}
