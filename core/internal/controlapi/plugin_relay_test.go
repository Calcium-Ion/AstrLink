package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
)

// relayPlugin is a new-api plugin with only a provider part: chat natively
// for its own models, converted to Claude Messages for the others, a route
// the plugin's hooks convert, and one of a path the gateway does not
// answer, which AstrLink leaves out.
const relayPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: { en: "Vendor", "zh-CN": "供应商" }, version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["vendor-large"],
  relay: [
    { incoming_path: "/v1/chat/completions", upstream_path: "/v1/chat/completions", models: ["re:^vendor-"] },
    { incoming_path: "/v1/chat/completions", upstream_path: "/v1/messages", converter: "openai_chat_completions_to_anthropic_messages" },
    { incoming_path: "/v1/messages", upstream_path: "/v1/messages" },
    { incoming_path: "/v1/embeddings", upstream_path: "/embed", converter: "plugin" },
    { incoming_path: "/v1/audio/speech", upstream_path: "/speech" },
  ],
};
export function convertRequest() {} export function convertResponse() {} export function convertStreamEvent() {}
export function checkKey(ctx, key) { return key.includes("|") ? undefined : { error: { en: "Write it as AccessKey|SecretKey", "zh-CN": "要写成 AccessKey|SecretKey" } }; }
`

func TestAProviderPluginOffersARelayProvider(t *testing.T) {
	_, handler, manager, directory := newPluginHandler(t)
	handler.capabilities.ConversionEngine = relaykitbridge.Descriptor(relaykitbridge.NewEngine())
	writePlugin(t, directory, "vendor", "plugin.js", relayPlugin)
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/vendor", "application/json", `{"enabled":true}`, ""); response.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", response.Code, response.Body)
	}
	detail := decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/vendor", "", "", ""))
	if detail.Kind != contract.PluginKindProvider || len(detail.Providers) != 1 {
		t.Fatalf("detail = %+v", detail.PluginSummary)
	}
	offered := detail.Providers[0]
	protocols := []contract.ProtocolID{}
	for _, protocol := range offered.Protocols {
		protocols = append(protocols, protocol.Protocol)
	}
	if offered.ID != contract.PluginProviderRelay || offered.BaseURL != "https://api.vendor.example" || offered.Auth.Scheme != contract.AuthSchemeBearer ||
		!slices.Equal(protocols, []contract.ProtocolID{contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIEmbeddings}) {
		t.Fatalf("provider = %+v", offered)
	}
	// A provider of it takes its capabilities from the routes, whatever the
	// request says.
	service := createServiceForTest(t, handler,
		`{"name":"Vendor","kind":"plugin","plugin":{"key":"vendor","provider":"relay"},"http":{"base_url":"","auth":{"scheme":"bearer"},"credential":{"secret":"ak|sk"}},"capabilities":[{"protocol":"openai.images","mode":"native","streaming":false}]}`)
	want := []contract.Capability{
		{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true},
		{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true},
		{Protocol: contract.ProtocolOpenAIEmbeddings, Mode: contract.CapabilityModeNative},
	}
	if !slices.Equal(service.Capabilities, want) || service.HTTP.BaseURL != "https://api.vendor.example" || service.HTTP.Auth.Scheme != contract.AuthSchemeBearer {
		t.Fatalf("service = %+v %+v", service.Capabilities, service.HTTP)
	}
	// The plugin checks the form of a key it is given.
	response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json",
		`{"name":"Vendor","kind":"plugin","plugin":{"key":"vendor","provider":"relay"},"http":{"base_url":"","auth":{"scheme":"bearer"},"credential":{"secret":"plain"}},"capabilities":[]}`, "")
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "AccessKey|SecretKey") {
		t.Fatalf("a key the plugin refuses = %d %s", response.Code, response.Body)
	}
	if response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json",
		`{"name":"Vendor","kind":"plugin","plugin":{"key":"vendor","provider":"relay","upstream":"new_api"},"http":{"base_url":"","auth":{"scheme":"none"}},"capabilities":[]}`, ""); response.Code < 400 {
		t.Fatalf("a relay provider through another gateway = %d", response.Code)
	}
}

func TestRelaySettingsKeepWhatTheSchemaDeclares(t *testing.T) {
	checked, err := plugin.Check(plugin.Source{File: "plugin.js", Text: `export const meta = { apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" },
  channelConfig: { type: "object", properties: { region: { type: "string", title: "Region", enum: ["us", "eu"], default: "us" } } },
  relay: [{ incoming_path: "/v1/chat/completions", upstream_path: "/v1/chat/completions" }] };`})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := relaySettings(checked.Task, json.RawMessage(`{"region":"eu","stray":1}`))
	if err != nil || string(settings) != `{"region":"eu"}` {
		t.Fatalf("settings = %s, %v", settings, err)
	}
	if _, err := relaySettings(checked.Task, json.RawMessage(`{"region":"mars"}`)); err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("a value the schema refuses = %v", err)
	}
}
