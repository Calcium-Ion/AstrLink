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
	if meta == nil || meta.HasTaskPart() || len(meta.Relay) != 4 || meta.KeyHint.In("en") != "AccessKey|SecretKey" {
		t.Fatalf("manifest = %+v", meta)
	}
	if got := meta.Relay[0].Converter; got != RelayConverterNone {
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
		if route == nil || route.UpstreamPath != test.upstream || route.UpstreamProtocol() != test.protocol {
			t.Errorf("route of %s %s = %+v", test.path, test.model, route)
		}
	}
	if route := meta.RelayRouteFor("/v1/embeddings", "vendor-large"); route != nil {
		t.Errorf("a path no route serves = %+v", route)
	}
	defaults := meta.ChannelConfigDefaults()
	if string(defaults["region"]) != `"us"` || string(defaults["uploadUrl"]) != `"https://upload.vendor.example"` {
		t.Fatalf("defaults = %s", defaults)
	}
}

func TestTheProviderPartIsChecked(t *testing.T) {
	chat := `[{ incoming_path: "/v1/chat/completions", upstream_path: "/v1/chat/completions" }]`
	for _, test := range []struct {
		name, text, message string
	}{
		{"no part", strings.Replace(relaySource(`[]`, ""), "relay: [] ", "", 1), "no part this host runs"},
		{"task fields", strings.Replace(relaySource(chat, ""), `keyHint:`, `fetchMode: "per_task", keyHint:`, 1), "without a task part"},
		{"unknown route key", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "/x", priority: 1 }]`, ""), `unknown field "priority"`},
		{"unknown converter", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "/x", converter: "chat_to_bard" }]`, ""), "not none, plugin or a converter"},
		{"converter path", relaySource(`[{ incoming_path: "/v1/responses", upstream_path: "/x", converter: "openai_chat_completions_to_anthropic_messages" }]`, ""), "does not serve /v1/responses"},
		{"two fallbacks", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "/a" }, { incoming_path: "/v1/chat/completions", upstream_path: "/b" }]`, ""), "one route for every other model"},
		{"model twice", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "/a", models: ["m"] }, { incoming_path: "/v1/chat/completions", upstream_path: "/b", models: ["m"] }]`, ""), "in two routes"},
		{"bad regexp", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "/a", models: ["re:("] }]`, ""), "not a regular expression"},
		{"models route", relaySource(`[{ incoming_path: "/v1/models", upstream_path: "/v1/models", models: ["m"] }]`, ""), "management route"},
		{"config field", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "{config.region}/v1/chat" }]`, ""), "not a url field"},
		{"double slash", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "//evil.example/v1" }]`, ""), "single /"},
		{"auth type", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "/a", auth: { type: "cookie", name: "a", value: "b" } }]`, ""), "not none, header or query"},
		{"pass through", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "/a", converter: "openai_chat_completions_to_openai_responses", pass_through_body_enabled: true }]`, ""), "pass_through_body_enabled"},
		{"converters without route", relaySource(chat, `export function convertRequest() {} export function convertResponse() {} export function convertStreamEvent() {}`), "converter plugin"},
		{"plugin route hooks", relaySource(`[{ incoming_path: "/v1/chat/completions", upstream_path: "/a", converter: "plugin" }]`, `export function convertRequest() {}`), "needs convertRequest, convertResponse and convertStreamEvent"},
		{"models plugin route", relaySource(`[{ incoming_path: "/v1/models", upstream_path: "/models", converter: "plugin" }]`, `export function convertRequest() {} export function convertResponse() {}`), "convertResponse alone"},
		{"one balance hook", relaySource(chat, `export function parseBalance() {}`), "exported together"},
		{"not a function", relaySource(chat, `export const prepareRequest = 1;`), "not a function"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Check(Source{File: "plugin.js", Text: test.text})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Check = %v, want an error mentioning %q", err, test.message)
			}
		})
	}
	// A /v1/models route of the plugin's own converts the reply alone.
	if _, err := Check(Source{File: "plugin.js", Text: relaySource(`[{ incoming_path: "/v1/models", upstream_path: "/models", converter: "plugin" }]`, `export function convertResponse() {}`)}); err != nil {
		t.Fatalf("a models-only plugin route: %v", err)
	}
	// A task plugin may relay too, and stays a task plugin.
	both := strings.Replace(taskSource("demo", ""), `fetchMode: "per_task"`, `fetchMode: "per_task", relay: `+chat, 1)
	checked, err := Check(Source{File: "plugin.js", Text: both})
	if err != nil || !checked.Task.HasTaskPart() || len(checked.Task.Relay) != 1 {
		t.Fatalf("task and provider parts = %+v, %v", checked, err)
	}
}
