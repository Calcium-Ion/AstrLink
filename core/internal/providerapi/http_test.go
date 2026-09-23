package providerapi

import (
	"net/url"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestProviderSurfacePreservesOriginPrefixAndInput(t *testing.T) {
	for _, tt := range []struct {
		kind       contract.ServiceKind
		base, want string
	}{
		{contract.ServiceKindDeepSeek, "https://proxy.example:8443/tenant%2Fone/v1/", "https://proxy.example:8443/tenant%2Fone/anthropic/v1/messages?trace=a%2Fb"},
		{contract.ServiceKindMoonshot, "https://api.moonshot.ai/v1", "https://api.moonshot.ai/anthropic/v1/messages?trace=a%2Fb"},
		{contract.ServiceKindMiniMax, "https://api.minimax.io/v1", "https://api.minimax.io/anthropic/v1/messages?trace=a%2Fb"},
		{contract.ServiceKindQwen, "https://workspace.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1", "https://workspace.ap-southeast-1.maas.aliyuncs.com/apps/anthropic/v1/messages?trace=a%2Fb"},
		{contract.ServiceKindNewAPI, "https://proxy.example/custom/v1", "https://proxy.example/custom/v1/messages?trace=a%2Fb"},
		{contract.ServiceKindGLMCoding, "https://proxy.example/api/anthropic", "https://proxy.example/api/anthropic/v1/messages?trace=a%2Fb"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			base, err := url.Parse(tt.base)
			if err != nil {
				t.Fatal(err)
			}
			incoming, _ := url.Parse("/v1/messages?trace=a%2Fb")
			target := transport.JoinTargetURL(BaseURL(tt.kind, contract.ProtocolAnthropicMessages, base), RequestURL(tt.kind, contract.ProtocolAnthropicMessages, incoming))
			if target.String() != tt.want {
				t.Fatalf("target = %s, want %s", target, tt.want)
			}
			if base.String() != tt.base || incoming.String() != "/v1/messages?trace=a%2Fb" {
				t.Fatal("mutated a shared URL")
			}
		})
	}
}

func TestDeepSeekOpenAISurfaceUsesVendorRoot(t *testing.T) {
	for _, test := range []struct {
		name     string
		base     string
		protocol contract.ProtocolID
		incoming string
		want     string
	}{
		{
			name:     "chat from SDK base",
			base:     "https://api.deepseek.com/v1",
			protocol: contract.ProtocolOpenAIChat,
			incoming: "/v1/chat/completions?trace=a%2Fb",
			want:     "https://api.deepseek.com/chat/completions?trace=a%2Fb",
		},
		{
			name:     "models from SDK base",
			base:     "https://api.deepseek.com/v1",
			protocol: contract.ProtocolOpenAIModels,
			incoming: "/v1/models?limit=2",
			want:     "https://api.deepseek.com/models?limit=2",
		},
		{
			name:     "responses from escaped proxy prefix",
			base:     "https://proxy.example/tenant%2Fone/v1/",
			protocol: contract.ProtocolOpenAIResponses,
			incoming: "/v1/responses",
			want:     "https://proxy.example/tenant%2Fone/responses",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, err := url.Parse(test.base)
			if err != nil {
				t.Fatal(err)
			}
			incoming, err := url.Parse(test.incoming)
			if err != nil {
				t.Fatal(err)
			}
			target := transport.JoinTargetURL(
				BaseURL(contract.ServiceKindDeepSeek, test.protocol, base),
				RequestURL(contract.ServiceKindDeepSeek, test.protocol, incoming),
			)
			if target.String() != test.want {
				t.Fatalf("target = %s, want %s", target, test.want)
			}
			if base.String() != test.base || incoming.String() != test.incoming {
				t.Fatal("mutated a shared URL")
			}
		})
	}
}

func TestProviderAuthPreservesExplicitOverridesAndOtherProtocols(t *testing.T) {
	for _, kind := range []contract.ServiceKind{contract.ServiceKindDeepSeek, contract.ServiceKindGLM, contract.ServiceKindDoubao} {
		for _, configured := range []contract.ServiceAuth{
			{Scheme: contract.AuthSchemeNone},
			{Scheme: contract.AuthSchemeCustomHeader, HeaderName: "X-Provider-Key"},
			{Scheme: contract.AuthSchemeAnthropicAPIKey},
		} {
			if got := Auth(kind, contract.ProtocolAnthropicMessages, configured); got != configured {
				t.Fatalf("overrode explicit auth: %#v", got)
			}
		}
		configured := contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}
		if got := Auth(kind, contract.ProtocolOpenAIChat, configured); got != configured {
			t.Fatalf("overrode Chat auth: %#v", got)
		}
	}
}
