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
