package plugin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestProviderDeclarationsAndHooksAreCheckedBothWays(t *testing.T) {
	const provider = `providers: [{ id: "acme", name: "Acme", protocols: { "openai.chat": { stream: true } } }]`
	const hooks = `export async function buildRequest() { return { url: "/x" }; }
export async function parseResponse() { return { json: {} }; }
export async function parseStreamEvent() { return { events: [] }; }
`
	source := func(capabilities, declaration, exported string) string {
		return `export const meta = { apiVersion: 2, key: "acme", name: "Acme", version: "1.0.0", capabilities: ` + capabilities + declaration + ` };
` + exported
	}
	for _, test := range []struct {
		name, text, want string
	}{
		{"providers without the capability", source(`[]`, ", "+provider, hooks), "needs the provider capability"},
		{"the capability without providers", source(`["provider"]`, "", ""), "needs meta.providers"},
		{"an unknown protocol", source(`["provider"]`, `, providers: [{ id: "acme", name: "Acme", protocols: { "openai.images": {} } }]`, hooks), "is not one of"},
		{"no mode", source(`["provider"]`, `, providers: [{ id: "acme", name: "Acme", protocols: { "openai.chat": { sync: false } } }]`, hooks), "neither whole responses nor streams"},
		{"a bad id", source(`["provider"]`, `, providers: [{ id: "Acme!", name: "Acme", protocols: { "openai.chat": {} } }]`, hooks), "lowercase letter"},
		{"a bad address", source(`["provider"]`, `, providers: [{ id: "acme", name: "Acme", baseUrl: "ftp://x", protocols: { "openai.chat": {} } }]`, hooks), "baseUrl"},
		{"a bad auth", source(`["provider"]`, `, providers: [{ id: "acme", name: "Acme", auth: "cookie", protocols: { "openai.chat": {} } }]`, hooks), "auth must be"},
		{"no buildRequest", source(`["provider"]`, ", "+provider, `export async function parseResponse() {}
export async function parseStreamEvent() {}`), "does not export buildRequest"},
		{"a stream without its parser", source(`["provider"]`, ", "+provider, `export async function buildRequest() {}
export async function parseResponse() {}`), "does not export parseStreamEvent"},
		{"a stream parser without a stream", source(`["provider"]`, `, providers: [{ id: "acme", name: "Acme", protocols: { "openai.chat": {} } }]`, hooks), "declares no stream"},
		{"adapter hooks without providers", source(`[]`, "", `export async function buildRequest() {}`), "without meta.providers"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Check(Source{File: "plugin.js", Text: test.text})
			var loadErr *LoadError
			if !errors.As(err, &loadErr) || !strings.Contains(loadErr.Message, test.want) {
				t.Fatalf("Check = %v, want %q", err, test.want)
			}
		})
	}
	checked, err := Check(Source{File: "plugin.js", Text: source(`["provider"]`, `, providers: [{ id: "acme", name: "Acme", baseUrl: "https://api.acme.example/v1", auth: { header: "X-Acme-Key" },
  protocols: { "anthropic.messages": { stream: true, sync: false }, "openai.chat": { stream: true } }, convert: true, models: ["acme-1"] }]`, hooks)})
	if err != nil {
		t.Fatal(err)
	}
	declared := checked.Meta.Providers[0]
	if declared.Auth.Service() != (contract.ServiceAuth{Scheme: contract.AuthSchemeCustomHeader, HeaderName: "X-Acme-Key"}) || !declared.Convert ||
		declared.Protocols[contract.ProtocolAnthropicMessages].Synchronous() || !declared.Streams() {
		t.Fatalf("provider = %+v", declared)
	}
	// Conversion targets the first protocol in the gateway's own order.
	if order := declared.ProtocolOrder(); len(order) != 2 || order[0] != contract.ProtocolOpenAIChat {
		t.Fatalf("ProtocolOrder = %v", order)
	}
	if data, _ := json.Marshal(declared.Auth); string(data) != `{"header":"X-Acme-Key"}` {
		t.Fatalf("auth = %s", data)
	}
}

func TestProviderPluginsGetRoomForWholeBodies(t *testing.T) {
	limits := providerLimits(DefaultLimits())
	if limits.ResultBytes < 32<<20 || limits.Memory < 256<<20 || limits.ScriptTime < DefaultLimits().ScriptTime {
		t.Fatalf("providerLimits = %+v", limits)
	}
}
