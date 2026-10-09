package providerapi_test

import (
	"net/url"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/providerapi"
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
		{contract.ServiceKindMagpie, "http://127.0.0.1:3425", "http://127.0.0.1:3425/v1/messages?trace=a%2Fb"},
		{contract.ServiceKindGLMCoding, "https://proxy.example/api/anthropic", "https://proxy.example/api/anthropic/v1/messages?trace=a%2Fb"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			base, err := url.Parse(tt.base)
			if err != nil {
				t.Fatal(err)
			}
			incoming, _ := url.Parse("/v1/messages?trace=a%2Fb")
			target := transport.JoinTargetURL(providerapi.BaseURL(tt.kind, contract.ProtocolAnthropicMessages, base), providerapi.RequestURL(tt.kind, contract.ProtocolAnthropicMessages, incoming))
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
			if got := providerapi.Auth(kind, contract.ProtocolAnthropicMessages, configured); got != configured {
				t.Fatalf("overrode explicit auth: %#v", got)
			}
		}
		configured := contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}
		if got := providerapi.Auth(kind, contract.ProtocolOpenAIChat, configured); got != configured {
			t.Fatalf("overrode Chat auth: %#v", got)
		}
	}
}

// Saved Coding Plan bases predate the OpenAI surface and point at the Messages
// root; they must reach every native protocol exactly like the current presets.
func TestCodingPlanSurfacesAcceptSavedAndDocumentedBases(t *testing.T) {
	for _, tt := range []struct {
		kind     contract.ServiceKind
		protocol contract.ProtocolID
		path     string
		bases    []string
		want     string
	}{
		{contract.ServiceKindMiniMaxCoding, contract.ProtocolAnthropicMessages, "/v1/messages",
			[]string{"https://api.minimax.cn/anthropic", "https://api.minimax.cn/v1"}, "https://api.minimax.cn/anthropic/v1/messages"},
		{contract.ServiceKindMiniMaxCoding, contract.ProtocolOpenAIChat, "/v1/chat/completions",
			[]string{"https://api.minimax.cn/anthropic", "https://api.minimax.cn/v1"}, "https://api.minimax.cn/v1/chat/completions"},
		{contract.ServiceKindMiniMaxCoding, contract.ProtocolOpenAIResponses, "/v1/responses",
			[]string{"https://api.minimax.io/anthropic/", "https://api.minimax.io/v1"}, "https://api.minimax.io/v1/responses"},
		{contract.ServiceKindGLMCoding, contract.ProtocolAnthropicMessages, "/v1/messages",
			[]string{"https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn/api/coding/paas/v4"}, "https://open.bigmodel.cn/api/anthropic/v1/messages"},
		{contract.ServiceKindGLMCoding, contract.ProtocolOpenAIChat, "/v1/chat/completions",
			[]string{"https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/paas/v4"},
			"https://open.bigmodel.cn/api/coding/paas/v4/chat/completions"},
		{contract.ServiceKindGLMCoding, contract.ProtocolOpenAIResponses, "/v1/responses",
			[]string{"https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/v1"},
			"https://open.bigmodel.cn/api/v1/responses"},
		// The international Z.ai site shares BigModel's paths.
		{contract.ServiceKindGLMCoding, contract.ProtocolAnthropicMessages, "/v1/messages",
			[]string{"https://api.z.ai/api/coding/paas/v4"}, "https://api.z.ai/api/anthropic/v1/messages"},
		{contract.ServiceKindGLMCoding, contract.ProtocolOpenAIChat, "/v1/chat/completions",
			[]string{"https://api.z.ai/api/coding/paas/v4"}, "https://api.z.ai/api/coding/paas/v4/chat/completions"},
		{contract.ServiceKindGLMCoding, contract.ProtocolOpenAIResponses, "/v1/responses",
			[]string{"https://api.z.ai/api/coding/paas/v4"}, "https://api.z.ai/api/v1/responses"},
		{contract.ServiceKindMiniMaxCoding, contract.ProtocolAnthropicMessages, "/v1/messages",
			[]string{"https://api.minimax.io/v1"}, "https://api.minimax.io/anthropic/v1/messages"},
		{contract.ServiceKindKimiCoding, contract.ProtocolAnthropicMessages, "/v1/messages",
			[]string{"https://api.kimi.ai/coding", "https://api.kimi.ai/coding/", "https://api.kimi.ai/coding/v1"}, "https://api.kimi.ai/coding/v1/messages"},
		{contract.ServiceKindKimiCoding, contract.ProtocolOpenAIChat, "/v1/chat/completions",
			[]string{"https://api.kimi.ai/coding", "https://api.kimi.ai/coding/v1"}, "https://api.kimi.ai/coding/v1/chat/completions"},
		{contract.ServiceKindKimiCoding, contract.ProtocolOpenAIResponses, "/v1/responses",
			[]string{"https://api.kimi.ai/coding", "https://api.kimi.ai/coding/v1"}, "https://api.kimi.ai/coding/v1/responses"},
		{contract.ServiceKindKimiCoding, contract.ProtocolOpenAIModels, "/v1/models",
			[]string{"https://api.kimi.ai/coding", "https://api.kimi.ai/coding/v1"}, "https://api.kimi.ai/coding/v1/models"},
	} {
		for _, raw := range tt.bases {
			t.Run(string(tt.kind)+"/"+string(tt.protocol)+"/"+raw, func(t *testing.T) {
				base, err := url.Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
				incoming, _ := url.Parse(tt.path)
				target := transport.JoinTargetURL(providerapi.BaseURL(tt.kind, tt.protocol, base), providerapi.RequestURL(tt.kind, tt.protocol, incoming))
				if target.String() != tt.want {
					t.Fatalf("target = %s, want %s", target, tt.want)
				}
			})
		}
	}
}

func TestKimiCodingAuthFollowsProtocolForEitherSavedScheme(t *testing.T) {
	for _, configured := range []contract.ServiceAuth{
		{Scheme: contract.AuthSchemeBearer},
		{Scheme: contract.AuthSchemeAnthropicAPIKey},
	} {
		if got := providerapi.Auth(contract.ServiceKindKimiCoding, contract.ProtocolAnthropicMessages, configured); got.Scheme != contract.AuthSchemeAnthropicAPIKey {
			t.Fatalf("Messages auth = %#v", got)
		}
		for _, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIChat, contract.ProtocolOpenAIModels} {
			if got := providerapi.Auth(contract.ServiceKindKimiCoding, protocol, configured); got.Scheme != contract.AuthSchemeBearer {
				t.Fatalf("%s auth = %#v", protocol, got)
			}
		}
	}
	custom := contract.ServiceAuth{Scheme: contract.AuthSchemeCustomHeader, HeaderName: "X-Provider-Key"}
	if got := providerapi.Auth(contract.ServiceKindKimiCoding, contract.ProtocolOpenAIChat, custom); got != custom {
		t.Fatalf("overrode custom auth: %#v", got)
	}
}
